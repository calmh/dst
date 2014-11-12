// Copyright (C) 2014 Jakob Borg and Contributors (see the CONTRIBUTORS file).
// All rights reserved. Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst

import (
	"fmt"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"
)

const (
	maxIncomingRequests = 64
	maxPacketSize       = 1500
	handshakeTimeout    = 30 * time.Second
	handshakeInterval   = 1 * time.Second
)

// Mux is a UDP multiplexer of UDT connections.
type Mux struct {
	conn       net.PacketConn
	packetSize int

	conns      map[uint32]*Conn
	handshakes map[uint32]chan packet
	connsMut   sync.Mutex

	incoming  chan *Conn
	closed    chan struct{}
	closeOnce sync.Once

	buffers *sync.Pool
}

// NewMux creates a new UDT Mux on top of a packet connection.
func NewMux(conn net.PacketConn, packetSize int) *Mux {
	if packetSize <= 0 {
		packetSize = maxPacketSize
	}
	m := &Mux{
		conn:       conn,
		packetSize: packetSize,
		conns:      map[uint32]*Conn{},
		handshakes: make(map[uint32]chan packet),
		incoming:   make(chan *Conn, maxIncomingRequests),
		closed:     make(chan struct{}),
		buffers: &sync.Pool{
			New: func() interface{} {
				return make([]byte, packetSize)
			},
		},
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
// Network must be "dst".
//
// Addresses have the form host:port. If host is a literal IPv6 address or
// host name, it must be enclosed in square brackets as in "[::1]:80",
// "[ipv6-host]:http" or "[ipv6-host%zone]:80". The functions JoinHostPort and
// SplitHostPort manipulate addresses in this form.
//
// Examples:
//	Dial("dst", "12.34.56.78:80")
//	Dial("dst", "google.com:http")
//	Dial("dst", "[2001:db8::1]:http")
//	Dial("dst", "[fe80::1%lo0]:80")
func (m *Mux) Dial(network, addr string) (*Conn, error) {
	return m.DialUDT(network, addr)
}

// Dial connects to the address on the named network.
//
// Network must be "dst".
//
// Addresses have the form host:port. If host is a literal IPv6 address or
// host name, it must be enclosed in square brackets as in "[::1]:80",
// "[ipv6-host]:http" or "[ipv6-host%zone]:80". The functions JoinHostPort and
// SplitHostPort manipulate addresses in this form.
//
// Examples:
//	Dial("dst", "12.34.56.78:80")
//	Dial("dst", "google.com:http")
//	Dial("dst", "[2001:db8::1]:http")
//	Dial("dst", "[fe80::1%lo0]:80")
func (m *Mux) DialUDT(network, addr string) (*Conn, error) {
	if network != "dst" {
		return nil, ErrNotUDTNetwork
	}

	dst, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}

	resp := make(chan packet)

	m.connsMut.Lock()
	connID := m.newConnID()
	m.handshakes[connID] = resp
	m.connsMut.Unlock()

	conn, err := m.clientHandshake(dst, connID, resp)

	m.connsMut.Lock()
	defer m.connsMut.Unlock()
	delete(m.handshakes, connID)

	if err != nil {
		return nil, err
	}

	m.conns[connID] = conn
	return conn, nil
}

// handshake performs the client side handshake (i.e. Dial)
func (m *Mux) clientHandshake(dst net.Addr, connID uint32, resp chan packet) (*Conn, error) {
	if debugMux {
		log.Printf("%v dial %v connID 0x%08x", m, dst, connID)
	}

	nextHandshake := time.NewTimer(0)
	defer nextHandshake.Stop()

	handshakeTimeout := time.NewTimer(handshakeTimeout)
	defer handshakeTimeout.Stop()

	var remoteCookie uint32
	seqNo := rand.Uint32()

	for {
		select {
		case <-m.closed:
			// Failure. The mux has been closed.
			return nil, ErrClosed

		case <-handshakeTimeout.C:
			// Handshake timeout. Close and abort.
			return nil, ErrHandshakeTimeout

		case <-nextHandshake.C:
			// Send a handshake request.

			m.write(packet{
				src: connID,
				dst: dst,
				hdr: header{
					packetType: typeHandshake,
					flags:      flagRequest,
					connID:     0,
					sequenceNo: seqNo,
					timestamp:  timestampMicros(),
				},
				data: handshakeData{uint32(m.packetSize), connID, remoteCookie}.marshal(),
			})
			nextHandshake.Reset(handshakeInterval)

		case pkt := <-resp:
			hd := unmarshalHandshakeData(pkt.data)

			if pkt.hdr.flags&flagCookie == flagCookie {
				// We should resend the handshake request with a different cookie value.
				remoteCookie = hd.cookie
				nextHandshake.Reset(0)
			} else if pkt.hdr.flags&flagResponse == flagResponse {
				// Successfull handshake response.
				conn := newConn(m, dst)

				conn.connID = connID
				conn.remoteConnID = hd.connID
				conn.nextRecvSeqNo = pkt.hdr.sequenceNo + 1
				conn.packetSize = int(hd.packetSize)
				if conn.packetSize > m.packetSize {
					conn.packetSize = m.packetSize
				}

				conn.nextSeqNo = seqNo + 1

				conn.start()

				return conn, nil
			}
		}
	}
}

func (m *Mux) readerLoop() {
	buf := make([]byte, m.packetSize)
	for {
		buf = buf[:cap(buf)]
		n, from, err := m.conn.ReadFrom(buf)
		if err != nil {
			m.Close()
			return
		}
		buf = buf[:n]

		hdr := unmarshalHeader(buf)

		var bufCopy []byte
		if len(buf) > dstHeaderLen {
			bufCopy = m.buffers.Get().([]byte)[:len(buf)-dstHeaderLen]
			copy(bufCopy, buf[dstHeaderLen:])
		}

		pkt := packet{hdr: hdr, data: bufCopy}
		if debugMux {
			log.Println(m, "read", pkt)
		}

		switch hdr.packetType {
		case typeData:
			m.connsMut.Lock()
			conn, ok := m.conns[hdr.connID]
			m.connsMut.Unlock()

			if ok {
				conn.in <- packet{
					dst:  nil,
					hdr:  hdr,
					data: bufCopy,
				}
			} else if debugMux {
				log.Printf("Data packet for unknown conn %08x", hdr.connID)
			}

		case typeHandshake:
			m.incomingHandshake(from, hdr, bufCopy)

		default:
			m.connsMut.Lock()
			conn, ok := m.conns[hdr.connID]
			m.connsMut.Unlock()

			if ok {
				conn.in <- packet{
					dst:  nil,
					hdr:  hdr,
					data: bufCopy,
				}
			} else if debugMux && hdr.packetType != typeShutdown {
				log.Printf("Control packet %v for unknown conn %08x", hdr, hdr.connID)
			}
		}
	}
}

func (m *Mux) incomingHandshake(from net.Addr, hdr header, data []byte) {
	switch hdr.connID {
	case 0:
		// A new incoming handshake request.

		if hdr.flags&flagRequest != flagRequest {
			log.Println("Handshake pattern with flags 0x%x to connID zero", hdr.flags)
			return
		}

		hd := unmarshalHandshakeData(data)

		correctCookie := cookie(from)
		if hd.cookie != correctCookie {
			// Incorrect or missing SYN cookie. Send back a handshake
			// with the expected one.
			m.write(packet{
				dst: from,
				hdr: header{
					packetType: typeHandshake,
					flags:      flagResponse | flagCookie,
					connID:     hd.connID,
					timestamp:  timestampMicros(),
				},
				data: handshakeData{
					packetSize: uint32(m.packetSize),
					cookie:     correctCookie,
				}.marshal(),
			})
			return
		}

		seqNo := rand.Uint32()

		m.connsMut.Lock()
		connID := m.newConnID()

		conn := newConn(m, from)
		conn.connID = connID
		conn.remoteConnID = hd.connID
		conn.nextSeqNo = seqNo + 1
		conn.nextRecvSeqNo = hdr.sequenceNo + 1
		conn.packetSize = int(hd.packetSize)
		if conn.packetSize > m.packetSize {
			conn.packetSize = m.packetSize
		}
		conn.start()

		m.conns[connID] = conn
		m.connsMut.Unlock()

		m.write(packet{
			dst: from,
			hdr: header{
				packetType: typeHandshake,
				flags:      flagResponse,
				connID:     hd.connID,
				sequenceNo: seqNo,
				timestamp:  timestampMicros(),
			},
			data: handshakeData{
				connID:     conn.connID,
				packetSize: uint32(conn.packetSize),
			}.marshal(),
		})

		m.incoming <- conn

	default:
		m.connsMut.Lock()
		handShake, ok := m.handshakes[hdr.connID]
		m.connsMut.Unlock()

		if ok {
			// This is a response to a handshake in progress.
			handShake <- packet{
				dst:  nil,
				hdr:  hdr,
				data: data,
			}
		} else if debugMux && hdr.packetType != typeShutdown {
			log.Printf("Handshake packet %v for unknown conn %08x", hdr, hdr.connID)
		}
	}
}

func (m *Mux) write(pkt packet) (int, error) {
	buf := m.buffers.Get().([]byte)
	buf = buf[:dstHeaderLen+len(pkt.data)]
	pkt.hdr.marshal(buf)
	copy(buf[dstHeaderLen:], pkt.data)
	if debugMux {
		log.Println(m, "write", pkt)
	}
	n, err := m.conn.WriteTo(buf, pkt.dst)
	m.buffers.Put(buf)
	return n, err
}

func (m *Mux) String() string {
	return fmt.Sprintf("Mux-%v", m.Addr())
}

// Find a unique connection ID
func (m *Mux) newConnID() uint32 {
	for {
		connID := uint32(rand.Uint32() & 0xffffff)
		if _, ok := m.conns[connID]; ok {
			continue
		}
		if _, ok := m.handshakes[connID]; ok {
			continue
		}
		return connID
	}
}

func (m *Mux) removeConn(c *Conn) {
	m.connsMut.Lock()
	delete(m.conns, c.connID)
	m.connsMut.Unlock()
}
