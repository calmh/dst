package mdstp

import (
	mr "math/rand"
	"net"
	"sync"
	"time"
)

func connPair(aLoss, bLoss float64) (*Conn, *Conn, error) {
	mpA, err := newLossyMux(aLoss)
	if err != nil {
		return nil, nil, err
	}
	mpB, err := newLossyMux(bLoss)
	if err != nil {
		return nil, nil, err
	}

	// Dial

	var wg sync.WaitGroup
	wg.Add(1)
	var otherErr error
	var otherConn *Conn
	go func() {
		defer wg.Done()
		otherConn, otherErr = mpB.AcceptUDT()
	}()

	conn, err := mpA.DialUDT("mdstp", mpB.Addr().String())
	if err != nil {
		return nil, nil, err
	}

	wg.Wait()
	if otherErr != nil {
		return nil, nil, otherErr
	}

	return conn, otherConn, nil
}

func newLossyMux(loss float64) (*Mux, error) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		return nil, err
	}

	if loss > 0 {
		mp := NewMux(&LossyPacketConn{
			lossProbability: loss,
			conn:            conn,
		})
		return mp, nil
	}

	return NewMux(conn), nil
}

type LossyPacketConn struct {
	lossProbability float64
	conn            net.PacketConn
}

func (c *LossyPacketConn) ReadFrom(b []byte) (n int, addr net.Addr, err error) {
	return c.conn.ReadFrom(b)
}

func (c *LossyPacketConn) WriteTo(b []byte, addr net.Addr) (n int, err error) {
	if mr.Float64() <= c.lossProbability {
		return len(b), nil
	}
	return c.conn.WriteTo(b, addr)
}

func (c *LossyPacketConn) Close() error {
	return c.conn.Close()
}

func (c *LossyPacketConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *LossyPacketConn) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}

func (c *LossyPacketConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (c *LossyPacketConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}
