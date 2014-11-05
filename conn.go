package mdstp

import (
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

	sliceOverhead = 8 /*pppoe, similar*/ + 20 /*ipv4*/ + 8 /*udp*/ + 16 /*mdstp*/
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
}

// Conn is a UDT connection carried over a Mux.
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
	nextMsgNo    uint32
	nextSeqNoMut sync.Mutex

	inbuf     []byte
	inbufMut  sync.Mutex
	inbufCond *sync.Cond

	sendBuffer      []connPacket
	sendBufferSend  int
	sendBufferWrite int
	sendLost        []connPacket
	sendLostSend    int
	sendWindow      int
	unackedMut      sync.Mutex
	unackedCond     *sync.Cond

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

	packetsIn      uint64
	packetsOut     uint64
	bytesIn        uint64
	bytesOut       uint64
	resentPackets  uint64
	droppedPackets uint64
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
		mux:        m,
		dst:        dst,
		nextSeqNo:  uint32(rand.Int31()),
		packetSize: size,
		in:         make(chan connPacket, 1024),
		closed:     make(chan struct{}),
		cc:         cc,
	}

	cookieCacheMut.Lock()
	conn.remoteCookie = cookieCache[dst.String()]
	cookieCacheMut.Unlock()

	conn.unackedCond = sync.NewCond(&conn.unackedMut)
	conn.stateCond = sync.NewCond(&conn.stateMut)
	conn.inbufCond = sync.NewCond(&conn.inbufMut)
	conn.sendBuffer = make([]connPacket, 16)

	conn.exp = time.NewTimer(defExpTime)
	conn.syn = time.NewTimer(defSynTime)

	conn.remoteCookieUpdate = make(chan uint32)
	conn.debugResetRecvSeqNo = make(chan uint32)
	conn.expReset = time.Now()

	if conn.cc == nil {
		conn.cc = NewWindowCC()
		conn.adjustSendBuffer()
	}

	go conn.reader()
	go conn.writer()

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
			c.mux.out <- connPacket{
				src: c.connID,
				dst: c.dst,
				hdr: header{
					packetType: typeHandshake,
					flags:      flagsRequest,
					connID:     0,
				},
				data: data,
			}
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
			c.mux.out <- connPacket{
				src: c.connID,
				dst: c.dst,
				hdr: header{
					packetType: typeShutdown,
					connID:     c.remoteConnID,
				},
			}
			atomic.AddUint64(&c.packetsOut, 1)
			atomic.AddUint64(&c.bytesOut, mdstpHeaderLen)
			return

		case pkt := <-c.in:
			if debugConnection {
				log.Println(c, "Read", pkt)
			}

			atomic.AddUint64(&c.packetsIn, 1)
			atomic.AddUint64(&c.bytesIn, mdstpHeaderLen+uint64(len(pkt.data)))

			c.expCount = 1
			c.expReset = time.Now()
			c.unackedMut.Lock()
			if len(c.sendLost) == 0 {
				c.resetExp(defExpTime)
			}
			c.unackedMut.Unlock()

			switch pkt.hdr.packetType {
			case typeData:
				c.recvdData(pkt)
			case typeHandshake:
				c.recvdHandshake(pkt)
			case typeKeepAlive:
				c.recvdKeepAlive(pkt)
			case typeACK:
				c.recvdACK(pkt)
			case typeShutdown:
				c.recvdShutdown(pkt)
			}

		case <-c.exp.C:
			c.eventEXP()
			c.resetExp(time.Duration(c.expCount) * defExpTime)

		case <-c.syn.C:
			c.eventSYN()
			c.resetSyn(defSynTime)

		case n := <-c.debugResetRecvSeqNo:
			// Back door for testing
			c.lastAckedSeqNo = n + 1
			c.lastRecvSeqNo = n
		}
	}
}

func (c *Conn) writer() {
	if debugConnection {
		log.Println(c, "writer() starting")
		defer log.Println(c, "writer() exiting")
	}

	for {
		/*
			                          sendBufferWindow
			                         v
				[S|S|S|S|Q|Q|Q|Q| | | | | | | | | ]
				         ^       ^sendBufferWrite
				          sendBufferSend
		*/

		c.unackedMut.Lock()
		if c.sendLostSend < len(c.sendLost) {
			pkt := c.sendLost[c.sendLostSend]
			pkt.hdr.timestamp = uint32(time.Now().UnixNano() / 1000)
			c.mux.out <- pkt
			c.sendLostSend++
			atomic.AddUint64(&c.resentPackets, 1)
		} else if c.sendBufferSend < c.sendBufferWrite {
			pkt := c.sendBuffer[c.sendBufferSend]
			pkt.hdr.timestamp = uint32(time.Now().UnixNano() / 1000)
			c.mux.out <- pkt
			c.sendBufferSend++
			atomic.AddUint64(&c.packetsOut, 1)
			atomic.AddUint64(&c.bytesOut, mdstpHeaderLen+uint64(len(pkt.data)))
		}

		c.unackedCond.Broadcast()

		for c.sendLostSend >= c.sendWindow || (c.sendBufferSend == c.sendBufferWrite && c.sendLostSend == len(c.sendLost)) {
			if debugConnection {
				log.Println(c, "writer() paused")
			}
			c.unackedCond.Wait()
		}
		c.unackedMut.Unlock()
	}
}

func (c *Conn) eventEXP() {
	if c.getState() != stateConnected {
		return
	}

	resent := false
	c.unackedMut.Lock()
	if c.sendBufferSend > 0 {
		// There are packets that have been sent but not acked. Move them from
		// the send buffer to the loss list for retransmission.
		resent = true
		if debugConnection {
			log.Println(c, "resend from buffer", c.sendBufferSend)
		}

		// Append the packets to the loss list
		c.sendLost = append(c.sendLost, c.sendBuffer[:c.sendBufferSend]...)

		// Rewind the send buffer
		copy(c.sendBuffer, c.sendBuffer[c.sendBufferSend:])
		c.sendBufferWrite -= c.sendBufferSend
		c.sendBufferSend = 0
	}

	if c.sendLostSend > 0 {
		// Also resend whatever was already in the loss list
		resent = true
		if debugConnection {
			log.Println(c, "resend from loss list", c.sendLostSend)
		}
		c.sendLostSend = 0
	}

	if resent {
		c.cc.Exp()
		c.adjustSendBuffer()
		c.unackedCond.Broadcast()
	}

	c.unackedMut.Unlock()

	c.expCount++
	if resent && c.expCount > expCountClose && time.Since(c.expReset) > minTimeClose {
		c.Close()
	}
}

func (c *Conn) adjustSendBuffer() {
	c.sendWindow = c.cc.SendWindow()
	if c.sendWindow > len(c.sendBuffer) {
		if c.sendWindow <= cap(c.sendBuffer) {
			c.sendBuffer = c.sendBuffer[:c.sendWindow]
		} else {
			sb := make([]connPacket, c.sendWindow)
			copy(sb, c.sendBuffer)
			c.sendBuffer = sb
		}
		c.unackedCond.Broadcast()
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

			c.lastRecvSeqNo = hd.seqNo
			c.lastAckedSeqNo = hd.seqNo
			if int(hd.packetSize) < c.packetSize {
				c.packetSize = int(hd.packetSize)
			}
			c.remoteConnID = hd.connID

			data := make([]byte, 4*4)
			c.nextSeqNoMut.Lock()
			handshakeData{c.nextSeqNo, uint32(c.packetSize), c.connID, hd.cookie}.marshal(data)
			c.nextSeqNoMut.Unlock()
			c.mux.out <- connPacket{
				src: c.connID,
				dst: c.dst,
				hdr: header{
					packetType: typeHandshake,
					flags:      flagsResponse,
					connID:     c.remoteConnID,
				},
				data: data,
			}
			atomic.AddUint64(&c.packetsOut, 1)
			atomic.AddUint64(&c.bytesOut, mdstpHeaderLen+uint64(len(data)))
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
				c.lastRecvSeqNo = hd.seqNo
				c.packetSize = int(hd.packetSize)
				c.lastAckedSeqNo = c.lastRecvSeqNo
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
	if debugConnection {
		log.Printf("%v read ACK 0x%08x", c, ack)
	}
	c.unackedMut.Lock()

	// Cut packets from the loss list if they have been acked

	cut := 0
	for i := range c.sendLost {
		if i >= c.sendLostSend {
			break
		}
		if diff := c.sendLost[i].hdr.sequenceNo - ack; diff < 1<<30 {
			break
		}
		cut = i + 1
	}
	if cut > 0 {
		c.sendLost = c.sendLost[cut:]
		c.sendLostSend -= cut
		c.unackedCond.Broadcast()
	}

	// Find the first packet that is still unacked, so we can cut
	// the packets ahead of it in the list.

	cut = 0
	for i := range c.sendBuffer {
		if i >= c.sendBufferSend {
			break
		}
		if diff := c.sendBuffer[i].hdr.sequenceNo - ack; diff < 1<<30 {
			break
		}
		cut = i + 1
	}

	// If something is to be cut, do so and notify any writers
	// that might be blocked.

	if cut > 0 {
		copy(c.sendBuffer, c.sendBuffer[cut:])
		c.sendBufferSend -= cut
		c.sendBufferWrite -= cut

		c.cc.Ack()
		c.adjustSendBuffer()

		c.unackedCond.Broadcast()
	}
	c.unackedMut.Unlock()
	c.resetExp(defExpTime)
}

func (c *Conn) recvdKeepAlive(pkt connPacket) {
}

func (c *Conn) recvdShutdown(pkt connPacket) {
	c.Close()
}

func (c *Conn) recvdData(pkt connPacket) {
	expected := c.lastRecvSeqNo
	if pkt.hdr.sequenceNo == expected {
		// An in-sequence packet.

		c.lastRecvSeqNo = (pkt.hdr.sequenceNo + uint32(len(pkt.data))) & (1<<31 - 1)
		c.rcvdUnacked++

		if c.rcvdUnacked >= c.cc.AckPacketIntv() {
			c.sendACK()
		}

		c.inbufMut.Lock()
		c.inbuf = append(c.inbuf, pkt.data...)
		c.inbufCond.Signal()
		c.inbufMut.Unlock()
	} else if diff := pkt.hdr.sequenceNo - expected; diff > 1<<30 {
		if debugConnection {
			log.Printf("%v old packet received; seq 0x%08x, expected 0x%08x", c, pkt.hdr.sequenceNo, expected)
		}
		c.rcvdUnacked++
		atomic.AddUint64(&c.droppedPackets, 1)
	} else {
		if debugConnection {
			log.Printf("%v lost; seq 0x%08x, expected 0x%08x", c, pkt.hdr.sequenceNo, expected)
		}
		atomic.AddUint64(&c.droppedPackets, 1)
	}
}

func (c *Conn) sendACK() {
	c.mux.out <- connPacket{
		src: c.connID,
		dst: c.dst,
		hdr: header{
			packetType: typeACK,
			connID:     c.remoteConnID,
			extra:      c.lastRecvSeqNo,
		},
	}
	atomic.AddUint64(&c.packetsOut, 1)
	atomic.AddUint64(&c.bytesOut, mdstpHeaderLen)
	if debugConnection {
		log.Printf("%v ACK 0x%08x", c, c.lastRecvSeqNo)
	}

	c.rcvdUnacked = 0
	c.lastAckedSeqNo = c.lastRecvSeqNo
	c.lastAckedSeqNo &= uint32(1<<31 - 1)
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
	for len(c.inbuf) == 0 {
		select {
		case <-c.closed:
			c.inbufMut.Unlock()
			return 0, io.EOF
		default:
		}
		c.inbufCond.Wait()
	}
	n = copy(b, c.inbuf)
	c.inbuf = c.inbuf[n:]
	c.inbufMut.Unlock()
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
		c.nextSeqNo &= uint32(1<<31 - 1)
		c.nextMsgNo++
		c.nextSeqNoMut.Unlock()

		c.unackedMut.Lock()
		for c.sendBufferWrite == len(c.sendBuffer) || c.sendBufferWrite >= c.sendWindow {
			if debugConnection {
				log.Println(c, "Write blocked")
			}
			c.unackedCond.Wait()
			// Connection may have closed while we were waiting
			select {
			case <-c.closed:
				c.unackedMut.Unlock()
				return sent, ErrClosed
			default:
			}
		}
		c.sendBuffer[c.sendBufferWrite] = pkt
		c.sendBufferWrite++
		c.unackedCond.Broadcast()
		c.unackedMut.Unlock()

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
