package miniudt

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defExpTime        = 100 * time.Millisecond // N * (4 * RTT + RTTVar + SYN)
	defSynTime        = 10 * time.Millisecond
	maxUnackedPkts    = 16
	expCountClose     = 16               // close connection after this many EXPs
	minTimeClose      = 15 * time.Second // if at least this long has passed
	handshakeInterval = time.Second

	sliceOverhead = 8 /*pppoe, similar*/ + 20 /*ipv4*/ + 8 /*udp*/ + 16 /*udt*/
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

// Conn is a UDT connection carried over a Mux.
type Conn struct {
	// Set at creation, thereafter immutable:

	mux          *Mux
	dst          net.Addr
	connID       uint32
	remoteConnID uint32
	in           chan connPacket

	// Touched by more than one goroutine, needs locking.

	nextSeqNo    uint32
	nextMsgNo    uint32
	nextSeqNoMut sync.Mutex

	inbuf     []byte
	inbufMut  sync.Mutex
	inbufCond *sync.Cond

	unacked     []connPacket
	maxUnacked  int
	unackedMut  sync.Mutex
	unackedCond *sync.Cond

	currentState connState
	stateMut     sync.Mutex
	stateCond    *sync.Cond

	exp    *time.Timer
	expMut sync.Mutex

	syn    *time.Timer
	synMut sync.Mutex

	packetSize int

	// Only touched by the reader routine, needs no locking

	rcvdUnacked int

	lastRecvSeqNo  uint32
	lastAckedSeqNo uint32

	expCount int
	expReset time.Time

	// Only touched by the handshake routine, needs no locking

	remoteCookie       uint32
	remoteCookieUpdate chan uint32

	// Special

	closed    chan struct{}
	closeOnce sync.Once

	debugResetRecvSeqNo chan uint32

	dataPacketsIn  uint64
	dataPacketsOut uint64
	dataBytesIn    uint64
	dataBytesOut   uint64
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

func newConn(m *Mux, dst net.Addr, size int) *Conn {
	conn := &Conn{
		mux:        m,
		dst:        dst,
		nextSeqNo:  uint32(rand.Int31()),
		packetSize: size,
		maxUnacked: maxUnackedPkts,
		in:         make(chan connPacket, maxUnackedPkts),
		closed:     make(chan struct{}),
	}

	cookieCacheMut.Lock()
	conn.remoteCookie = cookieCache[dst.String()]
	cookieCacheMut.Unlock()

	conn.unackedCond = sync.NewCond(&conn.unackedMut)
	conn.stateCond = sync.NewCond(&conn.stateMut)
	conn.inbufCond = sync.NewCond(&conn.inbufMut)

	conn.exp = time.NewTimer(defExpTime)
	conn.syn = time.NewTimer(defSynTime)

	conn.remoteCookieUpdate = make(chan uint32)
	conn.debugResetRecvSeqNo = make(chan uint32)
	conn.expReset = time.Now()

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

			data := make([]byte, (8*32+128)/8)
			c.nextSeqNoMut.Lock()
			handshakeData(data, c.nextSeqNo, uint32(c.packetSize), c.connID, c.remoteCookie, connectionTypeRequest)
			c.nextSeqNoMut.Unlock()
			c.mux.out <- connPacket{
				src: c.connID,
				dst: c.dst,
				hdr: &controlHeader{
					packetType: typeHandshake,
					additional: 0,
					connID:     0,
				},
				data: data,
			}
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
			c.mux.out <- connPacket{
				src: c.connID,
				dst: c.dst,
				hdr: &controlHeader{
					packetType: typeShutdown,
					connID:     c.remoteConnID,
				},
			}
			return

		case pkt := <-c.in:
			if debugConnection {
				log.Println(c, "Read", pkt)
			}

			c.expCount = 1
			c.expReset = time.Now()
			c.unackedMut.Lock()
			if len(c.unacked) == 0 {
				c.resetExp(defExpTime)
			}
			c.unackedMut.Unlock()

			switch hdr := pkt.hdr.(type) {
			case *controlHeader:
				c.processControlPacket(hdr, pkt.data)

			case *dataHeader:
				c.processDataPacket(hdr, pkt.data)
			}

		case <-c.exp.C:
			if c.getState() != stateConnected {
				continue
			}

			resent := false
			c.unackedMut.Lock()
			if len(c.unacked) > 0 {
				resent = true
				if debugConnection {
					log.Println(c, "Resend")
				}
				for _, pkt := range c.unacked {
					c.mux.out <- pkt
				}
			}
			c.unackedMut.Unlock()

			c.expCount++
			if resent && c.expCount > expCountClose && time.Since(c.expReset) > minTimeClose {
				c.Close()
			}
			c.resetExp(time.Duration(c.expCount) * defExpTime)

		case <-c.syn.C:
			if c.getState() != stateConnected {
				continue
			}
			if c.rcvdUnacked > 0 {
				c.sendACK()
			}

		case n := <-c.debugResetRecvSeqNo:
			// Back door for testing
			c.lastAckedSeqNo = n + 1
			c.lastRecvSeqNo = n
		}
	}
}

func (c *Conn) processControlPacket(hdr *controlHeader, data []byte) {
	switch hdr.packetType {
	case typeHandshake:
		switch c.getState() {
		case stateServerHandshake:
			// We received an initial handshake from a client. Respond
			// to it.

			c.lastRecvSeqNo = binary.BigEndian.Uint32(data[8:])
			c.lastAckedSeqNo = c.lastRecvSeqNo
			packetSize := int(binary.BigEndian.Uint32(data[12:]))
			if packetSize < c.packetSize {
				c.packetSize = packetSize
			}
			c.remoteConnID = binary.BigEndian.Uint32(data[24:])
			cookie := binary.BigEndian.Uint32(data[28:])

			data := make([]byte, (8*32+128)/8)
			c.nextSeqNoMut.Lock()
			handshakeData(data, c.nextSeqNo, uint32(c.packetSize), c.connID, cookie, connectionTypeResponse)
			c.nextSeqNoMut.Unlock()
			c.mux.out <- connPacket{
				src: c.connID,
				dst: c.dst,
				hdr: &controlHeader{
					packetType: typeHandshake,
					additional: 0,
					connID:     c.remoteConnID,
				},
				data: data,
			}
			c.setState(stateConnected)

		case stateClientHandshake:
			// We received a handshake response.
			c.remoteConnID = binary.BigEndian.Uint32(data[24:])
			if c.remoteConnID == 0 {
				// Not actually a response, but a cookie challenge.
				cookie := binary.BigEndian.Uint32(data[28:])
				cookieCacheMut.Lock()
				cookieCache[c.dst.String()] = cookie
				cookieCacheMut.Unlock()
				c.remoteCookieUpdate <- cookie
			} else {
				c.lastRecvSeqNo = binary.BigEndian.Uint32(data[8:])
				c.packetSize = int(binary.BigEndian.Uint32(data[12:]))
				c.lastAckedSeqNo = c.lastRecvSeqNo
				c.setState(stateConnected)
			}

		default:
			log.Println("handshake packet in non handshake mode")
			return
		}

	case typeACK:
		ack := binary.BigEndian.Uint32(data)
		c.unackedMut.Lock()

		// Find the first packet that is still unacked, so we can cut
		// the packets ahead of it in the list.

		cut := 0
		for i := range c.unacked {
			if diff := c.unacked[i].hdr.(*dataHeader).sequenceNo - ack; diff < 1<<30 {
				break
			}
			cut = i + 1
		}

		// If something is to be cut, do so and notify any writers
		// that might be blocked.

		if cut > 0 {
			c.unacked = c.unacked[cut:]
			c.unackedCond.Broadcast()
		}
		c.unackedMut.Unlock()
		c.resetExp(defExpTime)

	case typeKeepAlive:
		// do nothing

	case typeShutdown:
		c.Close()
	}
}

func (c *Conn) processDataPacket(hdr *dataHeader, data []byte) {
	atomic.AddUint64(&c.dataPacketsIn, 1)
	expected := (c.lastRecvSeqNo + uint32(len(data))) & (1<<31 - 1)
	if hdr.sequenceNo == expected {
		// An in-sequence packet.

		c.lastRecvSeqNo = hdr.sequenceNo
		c.rcvdUnacked++

		if c.rcvdUnacked >= c.maxUnacked/2 {
			c.sendACK()
		}

		c.inbufMut.Lock()
		c.inbuf = append(c.inbuf, data...)
		c.inbufCond.Signal()
		c.inbufMut.Unlock()
		atomic.AddUint64(&c.dataBytesIn, uint64(len(data)))
	} else if diff := hdr.sequenceNo - expected; diff > 1<<30 {
		if debugConnection {
			log.Println(c, "old packet received; seq", hdr.sequenceNo, "expected", expected)
		}
		c.sendACK()
	} else if debugConnection {
		log.Println(c, "reordered; seq", hdr.sequenceNo, "expected", expected)
	}
}

func (c *Conn) sendACK() {
	data := make([]byte, 4)
	binary.BigEndian.PutUint32(data[:], c.lastRecvSeqNo+1)
	c.mux.out <- connPacket{
		src: c.connID,
		dst: c.dst,
		hdr: &controlHeader{
			packetType: typeACK,
			additional: 0,
			connID:     c.remoteConnID,
		},
		data: data,
	}
	if debugConnection {
		log.Println(c, "ACK", c.lastRecvSeqNo+1)
	}

	c.rcvdUnacked = 0
	c.lastAckedSeqNo = c.lastRecvSeqNo + 1
	c.lastAckedSeqNo &= uint32(1<<31 - 1)

	c.resetSyn(defSynTime)
}

func (c *Conn) resetExp(d time.Duration) {
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
	for len(c.inbuf) == 0 {
		select {
		case <-c.closed:
			return 0, io.EOF
		default:
		}
		c.inbufCond.Wait()
	}
	n = copy(b, c.inbuf)
	c.inbuf = c.inbuf[n:]
	return n, nil
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
		sliceCopy := make([]byte, len(slice))
		copy(sliceCopy, slice)

		c.nextSeqNoMut.Lock()
		c.nextSeqNo += uint32(len(slice))
		c.nextSeqNo &= uint32(1<<31 - 1)
		c.nextMsgNo++

		pkt := connPacket{
			src: c.connID,
			dst: c.dst,
			hdr: &dataHeader{
				sequenceNo: c.nextSeqNo,
				position:   positionOnly,
				inOrder:    true,
				messageNo:  c.nextMsgNo,
				connID:     c.remoteConnID,
			},
			data: sliceCopy,
		}
		c.nextSeqNoMut.Unlock()

		c.unackedMut.Lock()
		for len(c.unacked) == c.maxUnacked {
			if debugConnection {
				log.Println(c, "Write blocked")
			}
			c.unackedCond.Wait()
			// Connection may have closed while we were waiting
			select {
			case <-c.closed:
				return sent, ErrClosed
			default:
			}
		}
		c.unacked = append(c.unacked, pkt)
		c.unackedMut.Unlock()

		c.mux.out <- pkt
		atomic.AddUint64(&c.dataPacketsOut, 1)
		atomic.AddUint64(&c.dataBytesOut, uint64(len(slice)))
		sent += len(slice)
		c.resetExp(defExpTime)
	}
	return sent, nil
}

// Close closes the connection.
// Any blocked Read or Write operations will be unblocked and return errors.
func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		c.mux.removeConn(c)
		close(c.closed)
		c.setState(stateClosed)
		c.inbufMut.Lock()
		c.inbufCond.Broadcast()
		c.inbufMut.Unlock()
		c.unackedMut.Lock()
		c.unackedCond.Broadcast()
		c.unackedMut.Unlock()
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
}

func (c *Conn) GetStatistics() Statistics {
	return Statistics{
		DataPacketsIn:  atomic.LoadUint64(&c.dataPacketsIn),
		DataPacketsOut: atomic.LoadUint64(&c.dataPacketsOut),
		DataBytesIn:    atomic.LoadUint64(&c.dataBytesIn),
		DataBytesOut:   atomic.LoadUint64(&c.dataBytesOut),
	}
}
