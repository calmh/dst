package udt

import (
	"log"
	"net"
	"sync"
	"time"
)

const (
	maxIncomingRequests = 64
	maxMessageSize      = 128 * 1024 //bytes
)

type Error struct {
	Err string
}

func (e Error) Error() string {
	return e.Err
}

var (
	ErrCloseClosed   = &Error{"close on already closed multiple"}
	ErrAcceptClosed  = &Error{"accept on closed Mux"}
	ErrNotUDTNetwork = &Error{"network is not udt"}
)

// Mux implements net.Listener and Dial
type Mux struct {
	conn net.PacketConn

	conns     map[uint32]*Conn
	incoming  chan *Conn
	closed    chan struct{}
	closeOnce sync.Once
	out       chan connPacket
}

// NewMux creates a new UDT Mux on top of a packet connection.
func NewMux(conn net.PacketConn) *Mux {
	m := &Mux{
		conn:     conn,
		conns:    map[uint32]*Conn{},
		incoming: make(chan *Conn, maxIncomingRequests),
		closed:   make(chan struct{}),
		out:      make(chan connPacket),
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
	return m.Dial(network, addr)
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

	conn := newConn(m)
	conn.dst = dst
	conn.setState(stateClientHandshake)
	m.conns[conn.connID] = conn

	err = conn.handshake(5 * time.Second)
	if err != nil {
		return nil, err
	}

	return conn, nil
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

		switch hdr := hdr.(type) {
		case *controlHeader:
			if hdr.connID == 0 {
				// This should be a handshake packet
				if hdr.packetType != typeHandshake {
					log.Println("Got control packet type 0x%x with sockID 0", hdr.packetType)
					continue
				}
				conn := newConn(m)
				conn.dst = from
				conn.setState(stateServerHandshake)
				conn.in <- connPacket{
					dst:  nil,
					hdr:  hdr,
					data: buf[hdr.len():],
				}
				m.incoming <- conn
				continue
			}

			conn, ok := m.conns[hdr.connID]
			if !ok {
				log.Println("Control packet for unknown conn", hdr.connID)
				continue
			}
			conn.in <- connPacket{
				dst:  nil,
				hdr:  hdr,
				data: buf[hdr.len():],
			}

		case *dataHeader:
			conn, ok := m.conns[hdr.connID]
			if !ok {
				log.Println("Data packet for unknown conn", hdr.connID)
				continue
			}
			bufCopy := make([]byte, len(buf)-hdr.len())
			copy(bufCopy, buf[hdr.len():])
			conn.in <- connPacket{
				dst:  nil,
				hdr:  hdr,
				data: bufCopy,
			}
		}
	}
}

func (m *Mux) writerLoop() {
	buf := make([]byte, 65536)
	for sp := range m.out {
		buf = buf[:sp.hdr.len()+len(sp.data)]
		switch hdr := sp.hdr.(type) {
		case *dataHeader:
			hdr.timestamp = uint32(time.Now().UnixNano() / 1000)
			hdr.marshal(buf)
		case *controlHeader:
			hdr.timestamp = uint32(time.Now().UnixNano() / 1000)
			hdr.marshal(buf)
		}
		copy(buf[16:], sp.data)
		_, err := m.conn.WriteTo(buf, sp.dst)
		if err != nil {
			panic(err)
		}
	}
}
