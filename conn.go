// Copyright 2014 The DST Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/juju/ratelimit"
)

const (
	defExpTime     = 100 * time.Millisecond // N * (4 * RTT + RTTVar + SYN)
	defSynTime     = 10 * time.Millisecond
	expCountClose  = 16               // close connection after this many EXPs
	minTimeClose   = 15 * time.Second // if at least this long has passed
	maxInputBuffer = 8192 * 1024

	sliceOverhead = 8 /*pppoe, similar*/ + 20 /*ipv4*/ + 8 /*udp*/ + 16 /*dst*/
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

type CongestionController interface {
	Ack()
	Exp()
	SendWindow() int
	PacketRate() int // PPS
	UpdateRTT(time.Duration)
}

// Conn is an SDT connection carried over a Mux.
type Conn struct {
	// Set at creation, thereafter immutable:

	mux          *Mux
	dst          net.Addr
	connID       uint32
	remoteConnID uint32
	in           chan packet
	cc           CongestionController

	// Touched by more than one goroutine, needs locking.

	nextSeqNo    uint32
	nextSeqNoMut sync.Mutex

	inbuf     bytes.Buffer
	inbufMut  sync.Mutex
	inbufCond *sync.Cond

	sendBuffer *sendBuffer
	recvBuffer packetList

	exp    *time.Timer
	expMut sync.Mutex

	syn    *time.Timer
	synMut sync.Mutex

	packetSize int

	sentPackets *timeBuffer

	// Only touched by the reader routine, needs no locking

	nextRecvSeqNo  uint32
	lastAckedSeqNo uint32

	expCount int
	expReset time.Time

	// Special

	writeScheduler *ratelimit.Bucket

	closed    chan struct{}
	closeOnce sync.Once

	debugResetRecvSeqNo chan uint32

	packetsIn         uint64
	packetsOut        uint64
	bytesIn           uint64
	bytesOut          uint64
	resentPackets     uint64
	droppedPackets    uint64
	outOfOrderPackets uint64
}

type ackTimestamp struct {
	sequenceNo uint32
	time       int64
	packets    uint64
}

func newConn(m *Mux, dst net.Addr) *Conn {
	conn := &Conn{
		mux:                 m,
		dst:                 dst,
		nextSeqNo:           rand.Uint32(),
		packetSize:          maxPacketSize,
		in:                  make(chan packet, 1024),
		closed:              make(chan struct{}),
		sentPackets:         NewTimeBuffer(32),
		sendBuffer:          newSendBuffer(m),
		exp:                 time.NewTimer(defExpTime),
		syn:                 time.NewTimer(defSynTime),
		debugResetRecvSeqNo: make(chan uint32),
		expReset:            time.Now(),
	}

	conn.inbufCond = sync.NewCond(&conn.inbufMut)

	conn.cc = NewWindowCC()
	conn.sendBuffer.SetWindowAndRate(conn.cc.SendWindow(), conn.cc.PacketRate())
	conn.recvBuffer.Resize(128)

	return conn
}

func (c *Conn) start() {
	go c.reader()
}

func (c *Conn) reader() {
	if debugConnection {
		log.Println(c, "reader() starting")
		defer log.Println(c, "reader() exiting")
	}

	for {
		select {
		case <-c.closed:
			// Ack any received but not yet acked messages.
			c.sendACK()

			// Send a shutdown message.
			c.nextSeqNoMut.Lock()
			c.mux.write(packet{
				src: c.connID,
				dst: c.dst,
				hdr: header{
					packetType: typeShutdown,
					connID:     c.remoteConnID,
					sequenceNo: c.nextSeqNo,
				},
			})
			c.nextSeqNo++
			c.nextSeqNoMut.Unlock()
			atomic.AddUint64(&c.packetsOut, 1)
			atomic.AddUint64(&c.bytesOut, dstHeaderLen)
			return

		case pkt := <-c.in:
			if debugConnection {
				log.Println(c, "Read", pkt)
			}

			atomic.AddUint64(&c.packetsIn, 1)
			atomic.AddUint64(&c.bytesIn, dstHeaderLen+uint64(len(pkt.data)))

			c.expCount = 1

			switch pkt.hdr.packetType {
			case typeData:
				c.recvdData(pkt)
			case typeKeepAlive:
				c.recvdKeepAlive(pkt)
			case typeACK:
				c.recvdACK(pkt)
			case typeShutdown:
				c.recvdShutdown(pkt)
			default:
				log.Println("Unhandled packet", pkt)
				continue
			}

		case <-c.exp.C:
			c.eventEXP()
			c.resetExp()

		case <-c.syn.C:
			c.eventSYN()
			c.resetSyn(defSynTime)

		case n := <-c.debugResetRecvSeqNo:
			// Back door for testing
			c.lastAckedSeqNo = n + 1
			c.nextRecvSeqNo = n
		}
	}
}

func (c *Conn) eventEXP() {
	c.expCount++

	resent := c.sendBuffer.ScheduleResend()
	if resent {
		c.cc.Exp()
		c.sendBuffer.SetWindowAndRate(c.cc.SendWindow(), c.cc.PacketRate())

		if c.expCount > expCountClose && time.Since(c.expReset) > minTimeClose {
			if debugConnection {
				log.Println(c, "close due to EXP")
			}
			c.Close()
		}
	}
}

func (c *Conn) eventSYN() {
	c.sendACK()
}

func (c *Conn) recvdACK(pkt packet) {
	ack := pkt.hdr.sequenceNo

	if debugConnection {
		log.Printf("%v read ACK 0x%08x", c, ack)
	}

	c.sentPackets.Recv(ack)
	c.sendBuffer.Acknowledge(ack)

	c.cc.Ack()
	rtt, n := c.sentPackets.Average()
	if n > 8 {
		c.cc.UpdateRTT(rtt)
	}

	c.sendBuffer.SetWindowAndRate(c.cc.SendWindow(), c.cc.PacketRate())
}

func (c *Conn) recvdKeepAlive(pkt packet) {
}

func (c *Conn) recvdShutdown(pkt packet) {
	// XXX: We accept shutdown packets somewhat from the future since the
	// sender will number the shutdown after any packets that might still be
	// in the write buffer. This should be fixed to let the write buffer empty
	// on close and reduce the window here.
	if pkt.LessSeq(c.nextRecvSeqNo + 128) {
		if debugConnection {
			log.Println(c, "close due to shutdown")
		}
		c.Close()
	}
}

func (c *Conn) recvdData(pkt packet) {
	if pkt.LessSeq(c.nextRecvSeqNo) {
		if debugConnection {
			log.Printf("%v old packet received; seq 0x%08x, expected 0x%08x", c, pkt.hdr.sequenceNo, c.nextRecvSeqNo)
		}
		atomic.AddUint64(&c.droppedPackets, 1)
		return
	}

	if debugConnection {
		log.Println(c, "into recv buffer:", pkt)
	}
	c.recvBuffer.InsertSorted(pkt)
	if c.recvBuffer.LowestSeq() == c.nextRecvSeqNo {
		for _, pkt := range c.recvBuffer.PopSequence() {
			if debugConnection {
				log.Println(c, "from recv buffer:", pkt)
			}

			// An in-sequence packet.

			c.nextRecvSeqNo = pkt.hdr.sequenceNo + 1

			c.sendACK()

			c.inbufMut.Lock()
			for c.inbuf.Len() > len(pkt.data)+maxInputBuffer {
				c.inbufCond.Wait()
				select {
				case <-c.closed:
					return
				default:
				}
			}

			c.inbuf.Write(pkt.data)
			c.inbufCond.Broadcast()
			c.inbufMut.Unlock()
		}
	} else {
		c.recvBuffer.InsertSorted(pkt)
		if debugConnection {
			log.Printf("%v lost; seq 0x%08x, expected 0x%08x", c, pkt.hdr.sequenceNo, c.nextRecvSeqNo)
		}
		atomic.AddUint64(&c.outOfOrderPackets, 1)
	}
}

func (c *Conn) sendACK() {
	if c.lastAckedSeqNo == c.nextRecvSeqNo {
		return
	}

	c.mux.write(packet{
		src: c.connID,
		dst: c.dst,
		hdr: header{
			packetType: typeACK,
			connID:     c.remoteConnID,
			sequenceNo: c.nextRecvSeqNo,
		},
	})

	atomic.AddUint64(&c.packetsOut, 1)
	atomic.AddUint64(&c.bytesOut, dstHeaderLen)
	if debugConnection {
		log.Printf("%v send ACK 0x%08x", c, c.nextRecvSeqNo)
	}

	c.lastAckedSeqNo = c.nextRecvSeqNo
}

func (c *Conn) resetExp() {
	d, _ := c.sentPackets.Average()
	d *= 4
	d += defSynTime

	if d < defExpTime {
		d = defExpTime
	}

	c.expMut.Lock()
	c.exp.Reset(d)
	c.expMut.Unlock()
}

func (c *Conn) resetSyn(d time.Duration) {
	c.synMut.Lock()
	c.syn.Reset(d)
	c.synMut.Unlock()
}

// String returns a string representation of the connection.
func (c *Conn) String() string {
	return fmt.Sprintf("Connection-%08x/%v/%v", c.connID, c.LocalAddr(), c.RemoteAddr())
}

// Read reads data from the connection.
// Read can be made to time out and return a Error with Timeout() == true
// after a fixed time limit; see SetDeadline and SetReadDeadline.
func (c *Conn) Read(b []byte) (n int, err error) {
	c.inbufMut.Lock()
	defer c.inbufMut.Unlock()
	for c.inbuf.Len() == 0 {
		select {
		case <-c.closed:
			return 0, io.EOF
		default:
		}
		c.inbufCond.Wait()
	}
	return c.inbuf.Read(b)
}

// Write writes data to the connection.
// Write can be made to time out and return a Error with Timeout() == true
// after a fixed time limit; see SetDeadline and SetWriteDeadline.
func (c *Conn) Write(b []byte) (n int, err error) {
	select {
	case <-c.closed:
		return 0, ErrClosed
	default:
	}

	sent := 0
	sliceSize := c.packetSize - sliceOverhead
	for i := 0; i < len(b); i += sliceSize {
		nxt := i + sliceSize
		if nxt > len(b) {
			nxt = len(b)
		}
		slice := b[i:nxt]
		sliceCopy := c.mux.buffers.Get().([]byte)[:len(slice)]
		copy(sliceCopy, slice)

		c.nextSeqNoMut.Lock()
		pkt := packet{
			src: c.connID,
			dst: c.dst,
			hdr: header{
				packetType: typeData,
				sequenceNo: c.nextSeqNo,
				connID:     c.remoteConnID,
			},
			data: sliceCopy,
		}
		c.nextSeqNo++
		c.nextSeqNoMut.Unlock()

		if err := c.sendBuffer.Write(pkt); err != nil {
			return sent, err
		}

		c.sentPackets.Sent(pkt.hdr.sequenceNo)

		atomic.AddUint64(&c.packetsOut, 1)
		atomic.AddUint64(&c.bytesOut, uint64(len(slice)+dstHeaderLen))

		sent += len(slice)
		c.resetExp()
	}
	return sent, nil
}

// Close closes the connection.
// Any blocked Read or Write operations will be unblocked and return errors.
func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		// XXX: Ugly hack to implement lingering sockets...
		time.Sleep(4 * defExpTime)

		c.sendBuffer.Stop()
		c.mux.removeConn(c)
		close(c.closed)

		c.inbufMut.Lock()
		c.inbufCond.Broadcast()
		c.inbufMut.Unlock()
	})
	return nil
}

// LocalAddr returns the local network address.
func (c *Conn) LocalAddr() net.Addr {
	return c.mux.Addr()
}

// RemoteAddr returns the remote network address.
func (c *Conn) RemoteAddr() net.Addr {
	return c.dst
}

// SetDeadline sets the read and write deadlines associated
// with the connection. It is equivalent to calling both
// SetReadDeadline and SetWriteDeadline.
//
// A deadline is an absolute time after which I/O operations
// fail with a timeout (see type Error) instead of
// blocking. The deadline applies to all future I/O, not just
// the immediately following call to Read or Write.
//
// An idle timeout can be implemented by repeatedly extending
// the deadline after successful Read or Write calls.
//
// A zero value for t means I/O operations will not time out.
func (c *Conn) SetDeadline(t time.Time) error {
	return nil
}

// SetReadDeadline sets the deadline for future Read calls.
// A zero value for t means Read will not time out.
func (c *Conn) SetReadDeadline(t time.Time) error {
	return nil
}

// SetWriteDeadline sets the deadline for future Write calls.
// Even if write times out, it may return n > 0, indicating that
// some of the data was successfully written.
// A zero value for t means Write will not time out.
func (c *Conn) SetWriteDeadline(t time.Time) error {
	return nil
}

type Statistics struct {
	DataPacketsIn     uint64
	DataPacketsOut    uint64
	DataBytesIn       uint64
	DataBytesOut      uint64
	ResentPackets     uint64
	DroppedPackets    uint64
	OutOfOrderPackets uint64
}

func (s Statistics) String() string {
	return fmt.Sprintf("PktsIn: %d, PktsOut: %d, BytesIn: %d, BytesOut: %d, PktsResent: %d, PktsDropped: %d, PktsOutOfOrder: %d",
		s.DataPacketsIn, s.DataPacketsOut, s.DataBytesIn, s.DataBytesOut, s.ResentPackets, s.DroppedPackets, s.OutOfOrderPackets)
}

func (c *Conn) GetStatistics() Statistics {
	return Statistics{
		DataPacketsIn:     atomic.LoadUint64(&c.packetsIn),
		DataPacketsOut:    atomic.LoadUint64(&c.packetsOut),
		DataBytesIn:       atomic.LoadUint64(&c.bytesIn),
		DataBytesOut:      atomic.LoadUint64(&c.bytesOut),
		ResentPackets:     atomic.LoadUint64(&c.resentPackets),
		DroppedPackets:    atomic.LoadUint64(&c.droppedPackets),
		OutOfOrderPackets: atomic.LoadUint64(&c.outOfOrderPackets),
	}
}
