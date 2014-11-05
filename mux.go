package miniudt

import (
	"encoding/binary"
	"fmt"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"
)

const (
	maxIncomingRequests = 64
	maxMessageSize      = 128 * 1024 //bytes
)

// Mux is a UDP multiplexer of UDT connections.
type Mux struct {
	conn net.PacketConn

	conns    map[uint32]*Conn
	connsMut sync.Mutex

	incoming  chan *Conn
	closed    chan struct{}
	closeOnce sync.Once
	out       chan connPacket

	packetSize    int
	packetSizeMut sync.Mutex
}

// NewMux creates a new UDT Mux on top of a packet connection.
func NewMux(conn net.PacketConn) *Mux {
	m := &Mux{
		conn:       conn,
		conns:      map[uint32]*Conn{},
		incoming:   make(chan *Conn, maxIncomingRequests),
		closed:     make(chan struct{}),
		out:        make(chan connPacket),
		packetSize: 1500,
	}

	// Attempt to maximize buffer space. Start at 16 MB and work downwards 0.5
	// MB at a time.

	if conn, ok := conn.(*net.UDPConn); ok {
		for buf := 16384 * 1024; buf >= 512*1024; buf -= 512 * 1024 {
			err := conn.SetReadBuffer(buf)
			if err == nil {
				break
			}
		}
		for buf := 16384 * 1024; buf >= 512*1024; buf -= 512 * 1024 {
			err := conn.SetWriteBuffer(buf)
			if err == nil {
				break
			}
		}
	}

	go m.readerLoop()
	go m.writerLoop()

	return m
}

// Accept waits for and returns the next connection to the listener.
func (m *Mux) Accept() (net.Conn, error) {
	return m.AcceptUDT()
}

// AcceptUDT waits for and returns the next connection to the listener.
func (m *Mux) AcceptUDT() (*Conn, error) {
	conn, ok := <-m.incoming
	if !ok {
		return nil, ErrAcceptClosed
	}
	return conn, nil
}

// Close closes the listener.
// Any blocked Accept operations will be unblocked and return errors.
func (m *Mux) Close() error {
	var err error = ErrCloseClosed
	m.closeOnce.Do(func() {
		err = m.conn.Close()
		close(m.incoming)
		close(m.closed)
	})
	return err
}

// Addr returns the listener's network address.
func (m *Mux) Addr() net.Addr {
	return m.conn.LocalAddr()
}

// Dial connects to the address on the named network.
//
// Network must be "udt".
//
// Addresses have the form host:port. If host is a literal IPv6 address or
// host name, it must be enclosed in square brackets as in "[::1]:80",
// "[ipv6-host]:http" or "[ipv6-host%zone]:80". The functions JoinHostPort and
// SplitHostPort manipulate addresses in this form.
//
// Examples:
//	Dial("udt", "12.34.56.78:80")
//	Dial("udt", "google.com:http")
//	Dial("udt", "[2001:db8::1]:http")
//	Dial("udt", "[fe80::1%lo0]:80")
func (m *Mux) Dial(network, addr string) (*Conn, error) {
	return m.DialUDT(network, addr)
}

// Dial connects to the address on the named network.
//
// Network must be "udt".
//
// Addresses have the form host:port. If host is a literal IPv6 address or
// host name, it must be enclosed in square brackets as in "[::1]:80",
// "[ipv6-host]:http" or "[ipv6-host%zone]:80". The functions JoinHostPort and
// SplitHostPort manipulate addresses in this form.
//
// Examples:
//	Dial("udt", "12.34.56.78:80")
//	Dial("udt", "google.com:http")
//	Dial("udt", "[2001:db8::1]:http")
//	Dial("udt", "[fe80::1%lo0]:80")
func (m *Mux) DialUDT(network, addr string) (*Conn, error) {
	if network != "udt" {
		return nil, ErrNotUDTNetwork
	}

	dst, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}

	m.packetSizeMut.Lock()
	packetSize := m.packetSize
	m.packetSizeMut.Unlock()

	conn := newConn(m, dst, packetSize, nil)
	conn.connID = m.newConn(conn)
	conn.setState(stateClientHandshake)

	err = conn.handshake(5 * time.Second)
	if err != nil {
		m.removeConn(conn)
		return nil, err
	}

	return conn, nil
}

func (m *Mux) SetPacketSize(size int) {
	m.packetSizeMut.Lock()
	m.packetSize = size
	m.packetSizeMut.Unlock()
}

func (m *Mux) readerLoop() {
	buf := make([]byte, maxMessageSize)
	for {
		buf = buf[:cap(buf)]
		n, from, err := m.conn.ReadFrom(buf)
		if err != nil {
			m.Close()
			return
		}
		_ = from

		buf = buf[:n]
		hdr := unmarshalHeader(buf)
		bufCopy := make([]byte, len(buf)-udtHeaderLen)
		copy(bufCopy, buf[udtHeaderLen:])

		if debugConnection {
			log.Println(m, "Read", hdr)
		}

		switch hdr := hdr.(type) {
		case *controlHeader:
			if hdr.connID == 0 {
				// This should be a handshake packet
				if hdr.packetType != typeHandshake {
					log.Printf("Got control packet type 0x%x with sockID 0", hdr.packetType)
					continue
				}

				clientConnID := binary.BigEndian.Uint32(bufCopy[24:])
				rcvdCookie := binary.BigEndian.Uint32(bufCopy[28:])
				correctCookie := cookie(from)
				if rcvdCookie != correctCookie {
					// Incorrect or missing SYN cookie. Send back a handshake
					// with the expected one.
					data := make([]byte, (8*32+128)/8)
					handshakeData(data, 0, 0, 0, correctCookie, connectionTypeResponse)
					m.out <- connPacket{
						dst: from,
						hdr: &controlHeader{
							packetType: typeHandshake,
							connID:     clientConnID,
						},
						data: data,
					}
					continue
				}

				m.packetSizeMut.Lock()
				packetSize := m.packetSize
				m.packetSizeMut.Unlock()

				conn := newConn(m, from, packetSize, nil)
				conn.connID = m.newConn(conn)
				conn.setState(stateServerHandshake)
				conn.in <- connPacket{
					dst:  nil,
					hdr:  hdr,
					data: bufCopy,
				}
				m.incoming <- conn
				continue
			}

			m.connsMut.Lock()
			conn, ok := m.conns[hdr.connID]
			m.connsMut.Unlock()
			if ok {
				conn.in <- connPacket{
					dst:  nil,
					hdr:  hdr,
					data: bufCopy,
				}
			} else if debugConnection && hdr.packetType != typeShutdown {
				log.Printf("Control packet %v for unknown conn %08x", hdr, hdr.connID)
			}

		case *dataHeader:
			m.connsMut.Lock()
			conn, ok := m.conns[hdr.connID]
			m.connsMut.Unlock()
			if ok {
				conn.in <- connPacket{
					dst:  nil,
					hdr:  hdr,
					data: bufCopy,
				}
			} else if debugConnection {
				log.Printf("Data packet for unknown conn %08x", hdr.connID)
			}
		}
	}
}

func (m *Mux) String() string {
	return fmt.Sprintf("Mux-%v", m.Addr())
}

func (m *Mux) writerLoop() {
	buf := make([]byte, 65536)
	for sp := range m.out {
		buf = buf[:udtHeaderLen+len(sp.data)]
		switch hdr := sp.hdr.(type) {
		case *dataHeader:
			hdr.timestamp = uint32(time.Now().UnixNano() / 1000)
			hdr.marshal(buf)
		case *controlHeader:
			hdr.timestamp = uint32(time.Now().UnixNano() / 1000)
			hdr.marshal(buf)
		}
		copy(buf[16:], sp.data)
		if debugConnection {
			log.Println(m, "Write", sp)
		}
		_, err := m.conn.WriteTo(buf, sp.dst)
		if err != nil {
			panic(err)
		}
	}
}

func (m *Mux) newConn(c *Conn) uint32 {
	// Find a unique connection ID
	m.connsMut.Lock()
	connID := uint32(rand.Int31())
	for _, ok := m.conns[connID]; ok; _, ok = m.conns[connID] {
		connID = uint32(rand.Int31())
	}
	m.conns[connID] = c
	m.connsMut.Unlock()

	return connID
}

func (m *Mux) removeConn(c *Conn) {
	m.connsMut.Lock()
	delete(m.conns, c.connID)
	m.connsMut.Unlock()
}
