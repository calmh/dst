// Copyright 2014 The DST Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst

import (
	mr "math/rand"
	"net"
	"sync"
	"time"

	"github.com/juju/ratelimit"
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
		otherConn, otherErr = mpB.AcceptDST()
	}()

	conn, err := mpA.DialDST("dst", mpB.Addr().String())
	if err != nil {
		return nil, nil, err
	}

	wg.Wait()
	if otherErr != nil {
		return nil, nil, otherErr
	}

	return conn, otherConn, nil
}

func limitedConnPair(aRate, bRate int64) (*Conn, *Conn, error) {
	mpA, err := newLimitedMux(aRate)
	if err != nil {
		return nil, nil, err
	}
	mpB, err := newLimitedMux(bRate)
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
		otherConn, otherErr = mpB.AcceptDST()
	}()

	conn, err := mpA.DialDST("dst", mpB.Addr().String())
	if err != nil {
		return nil, nil, err
	}

	wg.Wait()
	if otherErr != nil {
		return nil, nil, otherErr
	}

	return conn, otherConn, nil
}

func tcpConnPair() (net.Conn, net.Conn, error) {
	mpA, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, nil, err
	}

	// Dial

	var wg sync.WaitGroup
	wg.Add(1)
	var otherErr error
	var otherConn net.Conn
	go func() {
		defer wg.Done()
		otherConn, otherErr = mpA.Accept()
	}()

	conn, err := net.Dial("tcp", mpA.Addr().String())
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
		}, 0)
		return mp, nil
	}

	return NewMux(conn, 0), nil
}

func newLimitedMux(rate int64) (*Mux, error) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		return nil, err
	}

	if rate > 0 {
		mp := NewMux(&LimitedPacketConn{
			limiter: ratelimit.NewBucketWithRate(float64(rate), rate/4),
			conn:    conn,
		}, 0)
		return mp, nil
	}

	return NewMux(conn, 0), nil
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

type LimitedPacketConn struct {
	limiter *ratelimit.Bucket
	conn    net.PacketConn
}

func (c *LimitedPacketConn) ReadFrom(b []byte) (n int, addr net.Addr, err error) {
	return c.conn.ReadFrom(b)
}

func (c *LimitedPacketConn) WriteTo(b []byte, addr net.Addr) (n int, err error) {
	if _, ok := c.limiter.TakeMaxDuration(int64(len(b)), 100*time.Millisecond); !ok {
		// Queue size exceeded, packet dropped
		return len(b), nil
	}
	return c.conn.WriteTo(b, addr)
}

func (c *LimitedPacketConn) Close() error {
	return c.conn.Close()
}

func (c *LimitedPacketConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *LimitedPacketConn) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}

func (c *LimitedPacketConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (c *LimitedPacketConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}
