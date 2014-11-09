package dst

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/juju/ratelimit"

	"code.google.com/p/curvecp/ringbuf"
)

const (
	defExpTime        = 100 * time.Millisecond // N * (4 * RTT + RTTVar + SYN)
	defSynTime        = 10 * time.Millisecond
	expCountClose     = 16               // close connection after this many EXPs
	minTimeClose      = 15 * time.Second // if at least this long has passed
	handshakeInterval = time.Second

	sliceOverhead = 8 /*pppoe, similar*/ + 20 /*ipv4*/ + 8 /*udp*/ + 16 /*dst*/
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

var (
	// The cookie cache improves connection setup time by remembering the SYN
	// cookies sent by remote muxes.
	cookieCache    = map[string]uint32{}
	cookieCacheMut sync.Mutex
)

type CongestionController interface {
	Ack()
	Exp()
	SendWindow() int
	AckPacketIntv() int
	PacketRate() int
	RTTPPS(int, int)
}

// Conn is an SDT connection carried over a Mux.
type Conn struct {
	// Set at creation, thereafter immutable:

	mux          *Mux
	dst          net.Addr
	connID       uint32
	remoteConnID uint32
	in           chan connPacket
	cc           CongestionController

	// Touched by more than one goroutine, needs locking.

	nextSeqNo    uint32
	nextSeqNoMut sync.Mutex

	inbuf     *ringbuf.Ringbuf
	inbufMut  sync.Mutex
	inbufCond *sync.Cond

	sendBuffer *sendBuffer

	currentState connState
	stateMut     sync.Mutex
	stateCond    *sync.Cond

	exp    *time.Timer
	expMut sync.Mutex

	syn    *time.Timer
	synMut sync.Mutex

	packetSize int

	ackSent     []ackTimestamp // timestamps of the last transmitted ACKs
	nextAckSent int            // index to write next timestamp at
	avgRTT      int            // microseconds
	avgPPS      int
	rttMut      sync.Mutex

	// Only touched by the reader routine, needs no locking

	rcvdUnacked int

	nextRecvSeqNo  uint32
	lastAckedSeqNo uint32

	expCount int
	expReset time.Time

	// Only touched by the handshake routine, needs no locking

	remoteCookie       uint32
	remoteCookieUpdate chan uint32

	// Special

	writeScheduler *ratelimit.Bucket

	closed    chan struct{}
	closeOnce sync.Once

	debugResetRecvSeqNo chan uint32

	packetsIn      uint64
	packetsOut     uint64
	bytesIn        uint64
	bytesOut       uint64
	resentPackets  uint64
	droppedPackets uint64
}

type ackTimestamp struct {
	sequenceNo uint32
	time       int64
	packets    uint64
}

type connPacket struct {
	src  uint32
	dst  net.Addr
	hdr  header
	data []byte
}

func (p connPacket) String() string {
	return fmt.Sprintf("%08x->%v %v %d", p.src, p.dst, p.hdr, len(p.data))
}

type connState int

const (
	stateInit connState = iota
	stateClientHandshake
	stateServerHandshake
	stateConnected
	stateClosed
)

func (s connState) String() string {
	switch s {
	case stateInit:
		return "Init"
	case stateClientHandshake:
		return "ClientHandshake"
	case stateServerHandshake:
		return "ServerHandshake"
	case stateConnected:
		return "Connected"
	case stateClosed:
		return "Closed"
	default:
		return "Unknown"
	}
}

func newConn(m *Mux, dst net.Addr, size int, cc CongestionController) *Conn {
	conn := &Conn{
		mux:                 m,
		dst:                 dst,
		nextSeqNo:           uint32(rand.Int31()),
		packetSize:          size,
		in:                  make(chan connPacket, 1024),
		closed:              make(chan struct{}),
		cc:                  cc,
		ackSent:             make([]ackTimestamp, 16),
		inbuf:               ringbuf.New(8192 * 1024),
		sendBuffer:          newSendBuffer(m),
		exp:                 time.NewTimer(defExpTime),
		syn:                 time.NewTimer(defSynTime),
		remoteCookieUpdate:  make(chan uint32),
		debugResetRecvSeqNo: make(chan uint32),
		expReset:            time.Now(),
	}

	cookieCacheMut.Lock()
	conn.remoteCookie = cookieCache[dst.String()]
	cookieCacheMut.Unlock()

	conn.stateCond = sync.NewCond(&conn.stateMut)
	conn.inbufCond = sync.NewCond(&conn.inbufMut)

	if conn.cc == nil {
		conn.cc = NewWindowCC()
	}
	conn.sendBuffer.SetWindowAndRate(conn.cc.SendWindow(), conn.cc.PacketRate())

	go conn.reader()

	return conn
}

func (c *Conn) setState(newState connState) {
	c.stateMut.Lock()
	c.currentState = newState
	if debugConnection {
		log.Println(c, "new state", newState)
	}
	c.stateCond.Broadcast()
	c.stateMut.Unlock()
}

func (c *Conn) waitForNewState() connState {
	c.stateMut.Lock()
	defer c.stateMut.Unlock()
	c.stateCond.Wait()
	return c.currentState
}
func (c *Conn) getState() connState {
	c.stateMut.Lock()
	defer c.stateMut.Unlock()
	return c.currentState
}

// handshake performs the client side handshake (i.e. Dial)
func (c *Conn) handshake(timeout time.Duration) error {
	nextHandshake := time.NewTimer(0)
	handshakeTimeout := time.NewTimer(timeout)

	// Feed state changes into the states channel. Make sure to exit when the
	// handshake is complete or aborted.

	states := make(chan connState)
	done := make(chan struct{})
	defer close(done)

	go func() {
		for {
			state := c.waitForNewState()
			select {
			case <-done:
				return
			case states <- state:
			}
		}
	}()

	// Wait for timers or state changes.

	for {
		select {
		case <-c.closed:
			// Failure. The connection has been closed during handshake for
			// whatever reason.
			return ErrClosed

		case state := <-states:
			switch state {
			case stateConnected:
				// Success. The reader changed state to connected.
				return nil
			case stateClosed:
				// Failure. The connection has been closed during handshake
				// for whatever reason.
				return ErrClosed
			default:
				panic(fmt.Sprintf("bug: weird state transition: %v", state))
			}

		case <-handshakeTimeout.C:
			// Handshake timeout. Close and abort.
			c.Close()
			return ErrHandshakeTimeout

		case cookie := <-c.remoteCookieUpdate:
			// We received a cookie challenge from the server side. Update our
			// cookie and immediately schedule a new handshake request.
			c.remoteCookie = cookie
			nextHandshake.Reset(0)

		case <-nextHandshake.C:
			// Send a handshake request.

			data := make([]byte, 4*4)
			c.nextSeqNoMut.Lock()
			handshakeData{c.nextSeqNo, uint32(c.packetSize), c.connID, c.remoteCookie}.marshal(data)
			c.nextSeqNoMut.Unlock()
			c.mux.write(connPacket{
				src: c.connID,
				dst: c.dst,
				hdr: header{
					packetType: typeHandshake,
					flags:      flagsRequest,
					connID:     0,
				},
				data: data,
			})
			atomic.AddUint64(&c.bytesOut, uint64(len(data)))
			nextHandshake.Reset(handshakeInterval)
		}
	}
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
			if c.rcvdUnacked > 0 {
				c.sendACK()
			}
			// Send a shutdown message.
			c.mux.write(connPacket{
				src: c.connID,
				dst: c.dst,
				hdr: header{
					packetType: typeShutdown,
					connID:     c.remoteConnID,
				},
			})
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
			case typeHandshake:
				c.recvdHandshake(pkt)
			case typeKeepAlive:
				c.recvdKeepAlive(pkt)
			case typeACK:
				c.recvdACK(pkt)
			case typeACK2:
				c.recvdACK2(pkt)
			case typeShutdown:
				c.recvdShutdown(pkt)
			}

			if len(pkt.data) > 0 {
				c.mux.buffers.Put(pkt.data)
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
	if c.getState() != stateConnected {
		return
	}

	resent := c.sendBuffer.ScheduleResend()
	if resent {
		c.cc.Exp()
		c.sendBuffer.SetWindowAndRate(c.cc.SendWindow(), c.cc.PacketRate())
	}

	c.expCount++
	if resent && c.expCount > expCountClose && time.Since(c.expReset) > minTimeClose {
		c.Close()
	}
}

func (c *Conn) eventSYN() {
	if c.getState() != stateConnected {
		return
	}
	if c.rcvdUnacked > 0 {
		c.sendACK()
	}
}

func (c *Conn) recvdHandshake(pkt connPacket) {
	switch pkt.hdr.packetType {
	case typeHandshake:
		switch c.getState() {
		case stateServerHandshake:
			// We received an initial handshake from a client. Respond
			// to it.

			var hd handshakeData
			hd.unmarshal(pkt.data)

			c.nextRecvSeqNo = hd.seqNo
			c.lastAckedSeqNo = hd.seqNo
			if int(hd.packetSize) < c.packetSize {
				c.packetSize = int(hd.packetSize)
			}
			c.remoteConnID = hd.connID

			data := make([]byte, 4*4)
			c.nextSeqNoMut.Lock()
			handshakeData{c.nextSeqNo, uint32(c.packetSize), c.connID, hd.cookie}.marshal(data)
			c.nextSeqNoMut.Unlock()
			c.mux.write(connPacket{
				src: c.connID,
				dst: c.dst,
				hdr: header{
					packetType: typeHandshake,
					flags:      flagsResponse,
					connID:     c.remoteConnID,
				},
				data: data,
			})
			atomic.AddUint64(&c.packetsOut, 1)
			atomic.AddUint64(&c.bytesOut, dstHeaderLen+uint64(len(data)))
			c.setState(stateConnected)

		case stateClientHandshake:
			// We received a handshake response.
			var hd handshakeData
			hd.unmarshal(pkt.data)

			if pkt.hdr.flags&flagsCookie != 0 {
				// Not actually a response, but a cookie challenge.
				cookieCacheMut.Lock()
				cookieCache[c.dst.String()] = hd.cookie
				cookieCacheMut.Unlock()
				c.remoteCookieUpdate <- hd.cookie
			} else {
				c.remoteConnID = hd.connID
				c.nextRecvSeqNo = hd.seqNo
				c.packetSize = int(hd.packetSize)
				c.lastAckedSeqNo = c.nextRecvSeqNo
				c.setState(stateConnected)
			}

		default:
			log.Println("handshake packet in non handshake mode")
			return
		}
	}
}

func (c *Conn) recvdACK(pkt connPacket) {
	ack := pkt.hdr.extra

	data := make([]byte, 8)
	binary.BigEndian.PutUint32(data, uint32(c.avgRTT))
	binary.BigEndian.PutUint32(data[4:], uint32(c.avgPPS))

	c.mux.write(connPacket{
		src: c.connID,
		dst: c.dst,
		hdr: header{
			packetType: typeACK2,
			connID:     c.remoteConnID,
			extra:      ack,
		},
		data: data,
	})

	if debugConnection {
		log.Printf("%v read ACK 0x%08x", c, ack)
	}

	c.sendBuffer.Acknowledge(ack)

	c.cc.Ack()
	c.sendBuffer.SetWindowAndRate(c.cc.SendWindow(), c.cc.PacketRate())

	c.resetExp()
}

func (c *Conn) recvdACK2(pkt connPacket) {
	ack := pkt.hdr.extra
	now := time.Now().UnixNano() / 1000

	if len(pkt.data) == 8 {
		remoteRTT := int(binary.BigEndian.Uint32(pkt.data))
		remotePPS := int(binary.BigEndian.Uint32(pkt.data[4:]))
		c.cc.RTTPPS(remoteRTT, remotePPS)
	}

	c.rttMut.Lock()
	for _, ts := range c.ackSent {
		if ts.sequenceNo == ack {
			diff := int(now - ts.time)
			c.avgRTT = (c.avgRTT/8)*7 + diff
			break
		}
	}

	var pps []int
	prev := c.ackSent[0]
	for _, ts := range c.ackSent[1:] {
		if diff := ts.time - prev.time; diff > 0 {
			packets := ts.packets - prev.packets
			if packets < 20 {
				continue
			}
			// time difference is in microseconds
			rate := int(float64(1e6*packets) / float64(diff))
			pps = append(pps, rate)
		}
		prev = ts
	}

	if len(pps) > len(c.ackSent)/2 {
		sort.Ints(pps)
		c.avgPPS = pps[len(pps)/2]
	}

	c.rttMut.Unlock()
}

func (c *Conn) recvdKeepAlive(pkt connPacket) {
}

func (c *Conn) recvdShutdown(pkt connPacket) {
	c.Close()
}

func (c *Conn) recvdData(pkt connPacket) {
	if pkt.hdr.sequenceNo == c.nextRecvSeqNo {
		// An in-sequence packet.

		c.nextRecvSeqNo = pkt.hdr.sequenceNo + uint32(len(pkt.data))
		c.rcvdUnacked++

		if c.rcvdUnacked >= c.cc.AckPacketIntv() {
			c.sendACK()
		}

		s := 0
		for {
			c.inbufMut.Lock()
			n := c.inbuf.Write(pkt.data[s:])
			s += n
			c.inbufCond.Broadcast()
			if len(pkt.data[s:]) > 0 {
				c.inbufCond.Wait()
				continue
			} else {
				c.inbufMut.Unlock()
				break
			}
		}
	} else if diff := pkt.hdr.sequenceNo - c.nextRecvSeqNo; diff > 1<<30 {
		if debugConnection {
			log.Printf("%v old packet received; seq 0x%08x, expected 0x%08x", c, pkt.hdr.sequenceNo, c.nextRecvSeqNo)
		}
		c.rcvdUnacked++
		atomic.AddUint64(&c.droppedPackets, 1)
	} else {
		if debugConnection {
			log.Printf("%v lost; seq 0x%08x, expected 0x%08x", c, pkt.hdr.sequenceNo, c.nextRecvSeqNo)
		}
		atomic.AddUint64(&c.droppedPackets, 1)
	}
}

func (c *Conn) sendACK() {

	now := time.Now().UnixNano() / 1000
	c.mux.write(connPacket{
		src: c.connID,
		dst: c.dst,
		hdr: header{
			packetType: typeACK,
			connID:     c.remoteConnID,
			extra:      c.nextRecvSeqNo,
		},
	})

	c.rttMut.Lock()
	c.ackSent[c.nextAckSent] = ackTimestamp{
		sequenceNo: c.nextRecvSeqNo,
		time:       now,
		packets:    atomic.LoadUint64(&c.packetsIn),
	}
	c.nextAckSent = (c.nextAckSent + 1) % len(c.ackSent)
	c.rttMut.Unlock()

	atomic.AddUint64(&c.packetsOut, 1)
	atomic.AddUint64(&c.bytesOut, dstHeaderLen)
	if debugConnection {
		log.Printf("%v ACK 0x%08x", c, c.nextRecvSeqNo)
	}

	c.rcvdUnacked = 0
	c.lastAckedSeqNo = c.nextRecvSeqNo
	c.lastAckedSeqNo &= uint32(1<<31 - 1)
}

func (c *Conn) resetExp() {
	c.rttMut.Lock()
	d := time.Duration(c.avgRTT*4)*1000 + defSynTime
	c.rttMut.Unlock()

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
	for c.inbuf.Size() == 0 {
		select {
		case <-c.closed:
			return 0, io.EOF
		default:
		}
		c.inbufCond.Wait()
	}
	return c.inbuf.Read(b), nil
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
		pkt := connPacket{
			src: c.connID,
			dst: c.dst,
			hdr: header{
				packetType: typeData,
				sequenceNo: c.nextSeqNo,
				connID:     c.remoteConnID,
			},
			data: sliceCopy,
		}
		c.nextSeqNo += uint32(len(slice))
		c.nextSeqNoMut.Unlock()

		c.sendBuffer.Write(pkt)

		sent += len(slice)
		c.resetExp()
	}
	return sent, nil
}

// Close closes the connection.
// Any blocked Read or Write operations will be unblocked and return errors.
func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		c.sendBuffer.Stop()
		c.mux.removeConn(c)
		close(c.closed)
		c.setState(stateClosed)
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
	DataPacketsIn  uint64
	DataPacketsOut uint64
	DataBytesIn    uint64
	DataBytesOut   uint64
	ResentPackets  uint64
	DroppedPackets uint64
}

func (c *Conn) GetStatistics() Statistics {
	return Statistics{
		DataPacketsIn:  atomic.LoadUint64(&c.packetsIn),
		DataPacketsOut: atomic.LoadUint64(&c.packetsOut),
		DataBytesIn:    atomic.LoadUint64(&c.bytesIn),
		DataBytesOut:   atomic.LoadUint64(&c.bytesOut),
		ResentPackets:  atomic.LoadUint64(&c.resentPackets),
		DroppedPackets: atomic.LoadUint64(&c.droppedPackets),
	}
}
