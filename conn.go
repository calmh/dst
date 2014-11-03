package udt

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"
)

const (
	defExpTime    = 100 * time.Millisecond // N * (4 * RTT + RTTVar + SYN)
	defSynTime    = 10 * time.Millisecond
	expCountClose = 16                                                         // close connection after this many EXPs
	minTimeClose  = 15 * time.Second                                           // if at least this long has passed
	sliceSize     = 1500 - 8 /*pppoe, similar*/ - 20 /*ipv4*/ - 8 /*udp*/ - 16 /*udt*/
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// Conn is a UDT connection carried over a Mux.
type Conn struct {
	mux *Mux
	dst net.Addr

	connID        uint32
	remoteConnID  uint32
	nextSeqNo     uint32
	nextSeqNoMut  sync.Mutex
	nextMsgNo     uint32
	packetSize    int
	interPktDelay time.Duration

	inbuf     []byte
	inbufMut  sync.Mutex
	inbufCond *sync.Cond

	unacked     []connPacket
	maxUnacked  int
	unackedMut  sync.Mutex
	unackedCond *sync.Cond

	rcvdUnacked int

	in chan connPacket

	currentState connState
	stateMut     sync.Mutex
	stateCond    *sync.Cond

	lastRecvSeqNo  uint32
	lastAckedSeqNo uint32

	expCount int
	expReset time.Time

	exp    *time.Timer
	expMut sync.Mutex

	syn    *time.Timer
	synMut sync.Mutex

	closed    chan struct{}
	closeOnce sync.Once

	debugResetRecvSeqNo chan uint32
}

type connPacket struct {
	dst  net.Addr
	hdr  header
	data []byte
}

func (p connPacket) String() string {
	data := p.data
	if len(data) > 16 {
		data = data[:16]
	}
	return fmt.Sprintf("->%v %v % x", p.dst, p.hdr, data)
}

type connState int

const (
	stateInit connState = iota
	stateClientHandshake
	stateServerHandshake
	stateConnected
	stateClosed
)

func newConn(m *Mux) *Conn {
	conn := &Conn{
		mux:        m,
		connID:     uint32(rand.Int31()),
		nextSeqNo:  uint32(rand.Int31()),
		packetSize: 1500,
		maxUnacked: 16,
		in:         make(chan connPacket),
		closed:     make(chan struct{}),
	}

	conn.unackedCond = sync.NewCond(&conn.unackedMut)
	conn.stateCond = sync.NewCond(&conn.stateMut)
	conn.inbufCond = sync.NewCond(&conn.inbufMut)

	conn.exp = time.NewTimer(defExpTime)
	conn.syn = time.NewTimer(defSynTime)

	conn.debugResetRecvSeqNo = make(chan uint32)
	conn.expReset = time.Now()

	m.conns[conn.connID] = conn
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
	t0 := time.NewTimer(0)
	nextWait := time.Second
	tz := time.NewTimer(timeout)

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

	for {
		select {
		case <-c.closed:
			return ErrClosed

		case state := <-states:
			switch state {
			case stateConnected:
				return nil
			case stateClosed:
				return ErrClosed
			default:
				panic(fmt.Sprintf("bug: weird state transition: %d", state))
			}

		case <-tz.C:
			c.Close()
			return ErrHandshakeTimeout

		case <-t0.C:
			data := make([]byte, (8*32+128)/8)
			binary.BigEndian.PutUint32(data[0:], 4) // UDT version
			binary.BigEndian.PutUint32(data[4:], 0)
			c.nextSeqNoMut.Lock()
			binary.BigEndian.PutUint32(data[8:], c.nextSeqNo) // Initial Sequence Number
			c.nextSeqNoMut.Unlock()
			binary.BigEndian.PutUint32(data[12:], uint32(c.packetSize)) // Packet Size
			binary.BigEndian.PutUint32(data[16:], 0)                    // Flow Window
			binary.BigEndian.PutUint32(data[20:], 0)                    // Connection Type (TODO)
			binary.BigEndian.PutUint32(data[24:], c.connID)             // Client Conn ID
			binary.BigEndian.PutUint32(data[28:], 0)                    // Cookie (TODO)
			binary.BigEndian.PutUint32(data[32:], 0)                    // Peer IP Address (TODO)
			c.mux.out <- connPacket{
				dst: c.dst,
				hdr: &controlHeader{
					packetType: typeHandshake,
					additional: 0,
					connID:     0,
				},
				data: data,
			}
			t0.Reset(nextWait)
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
			c.lastAckedSeqNo = binary.BigEndian.Uint32(data[8:])
			c.remoteConnID = binary.BigEndian.Uint32(data[24:])

			data := make([]byte, (8*32+128)/8)
			binary.BigEndian.PutUint32(data[0:], 4) // UDT version
			binary.BigEndian.PutUint32(data[4:], 0) // STREAM
			c.nextSeqNoMut.Lock()
			binary.BigEndian.PutUint32(data[8:], c.nextSeqNo) // Initial Sequence Number
			c.nextSeqNoMut.Unlock()
			binary.BigEndian.PutUint32(data[12:], uint32(c.packetSize)) // Packet Size
			binary.BigEndian.PutUint32(data[16:], 0)                    // Flow Window
			binary.BigEndian.PutUint32(data[20:], 0)                    // Connection Type (TODO)
			binary.BigEndian.PutUint32(data[24:], c.connID)             // Client Conn ID
			binary.BigEndian.PutUint32(data[28:], 0)                    // Cookie (TODO)
			binary.BigEndian.PutUint32(data[32:], 0)                    // Peer IP Address (TODO)
			c.mux.out <- connPacket{
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
			c.lastRecvSeqNo = binary.BigEndian.Uint32(data[8:])
			c.lastAckedSeqNo = binary.BigEndian.Uint32(data[8:])
			c.remoteConnID = binary.BigEndian.Uint32(data[24:])
			c.setState(stateConnected)

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
	return fmt.Sprintf("Connection/%v/%v/@%p", c.LocalAddr(), c.RemoteAddr(), c)
}

// Read reads data from the connection.
// Read can be made to time out and return a Error with Timeout() == true
// after a fixed time limit; see SetDeadline and SetReadDeadline.
func (c *Conn) Read(b []byte) (n int, err error) {
	select {
	case <-c.closed:
		return 0, io.EOF
	default:
	}

	c.inbufMut.Lock()
	defer c.inbufMut.Unlock()
	for len(c.inbuf) == 0 {
		c.inbufCond.Wait()
		// Connection may have closed while we were waiting.
		select {
		case <-c.closed:
			if len(c.inbuf) == 0 {
				return 0, io.EOF
			}
		default:
		}
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

		if debugConnection {
			log.Println(c, "Write", pkt)
		}
		c.mux.out <- pkt
		sent += len(slice)
		c.resetExp(defExpTime)
	}
	return sent, nil
}

// Close closes the connection.
// Any blocked Read or Write operations will be unblocked and return errors.
func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		c.mux.out <- connPacket{
			dst: c.dst,
			hdr: &controlHeader{
				packetType: typeShutdown,
				connID:     c.remoteConnID,
			},
		}
		close(c.closed)
		c.setState(stateClosed)
		c.inbufMut.Lock()
		c.inbufCond.Broadcast()
		c.inbufMut.Unlock()
		c.unackedMut.Lock()
		c.unackedCond.Broadcast()
		c.unackedMut.Unlock()
		c.mux.removeConnection(c.connID)
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
