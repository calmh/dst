// Copyright 2014 The DST Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// +build integration

package dst_test

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/calmh/dst"
)

func TestIntegrationMultipleConnections(t *testing.T) {
	errors := make(chan error)

	echoServer := func(c net.Conn) {
		buf := make([]byte, 1024)
		n, err := c.Read(buf)
		if err != nil {
			errors <- err
			return
		}
		_, err = c.Write(buf[:n])
		if err != nil {
			errors <- err
			return
		}
		err = c.Close()
		if err != nil {
			errors <- err
			return
		}
	}

	srvConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	srvMux := dst.NewMux(srvConn, 0)

	go func() {
		for {
			conn, err := srvMux.Accept()
			if err != nil {
				errors <- err
				return
			}
			go echoServer(conn)
		}
	}()

	clientConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	clientMux := dst.NewMux(clientConn, 0)

	var wg sync.WaitGroup
	t0 := time.Now()
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := 0
			for time.Since(t0) < 30*time.Second {
				conn, err := clientMux.Dial("dst", srvMux.Addr().String())
				if err != nil {
					errors <- err
					return
				}

				msg := []byte("A short text message!")
				_, err = conn.Write(msg)
				if err != nil {
					errors <- err
					return
				}

				buf := make([]byte, 1024)
				n, err := conn.Read(buf)
				if err != nil {
					errors <- err
					return
				}

				if n != len(msg) {
					errors <- fmt.Errorf("Incorrect message length %d", len(msg))
					return
				}
				if bytes.Compare(msg, buf[:n]) != 0 {
					errors <- fmt.Errorf("Incorrect message content")
					return
				}

				err = conn.Close()
				if err != nil {
					errors <- err
					return
				}

				c++
			}

			log.Printf("%d connections served; %.1f/s", c, float64(c)/time.Since(t0).Seconds())
		}()
	}

	go func() {
		wg.Wait()
		errors <- nil
	}()

	err = <-errors
	if err != nil {
		t.Error(err)
	}
}

func TestIntegrationSlowTransfer(t *testing.T) {
	errors := make(chan error)

	echoServer := func(c net.Conn) {
		buf := make([]byte, 1024)
		for {
			n, err := c.Read(buf)
			if err != nil {
				errors <- err
				return
			}
			_, err = c.Write(buf[:n])
			if err != nil {
				errors <- err
				return
			}
		}
	}

	srvConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	srvMux := dst.NewMux(srvConn, 0)

	go func() {
		for {
			conn, err := srvMux.Accept()
			if err != nil {
				errors <- err
				return
			}
			go echoServer(conn)
		}
	}()

	clientConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	clientMux := dst.NewMux(clientConn, 0)

	conn, err := clientMux.Dial("dst", srvMux.Addr().String())
	if err != nil {
		errors <- err
		return
	}

	// Send a message and check for the echo

	msg := []byte("A short text message!")
	_, err = conn.Write(msg)
	if err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}

	if n != len(msg) {
		t.Fatalf("Incorrect message length %d", len(msg))
	}
	if bytes.Compare(msg, buf[:n]) != 0 {
		t.Fatalf("Incorrect message content")
	}

	// Give things time to stall or time out, possibly

	time.Sleep(45 * time.Second)

	// Send a message and check for the echo

	msg = []byte("The second message is different.")
	_, err = conn.Write(msg)
	if err != nil {
		t.Fatal(err)
	}

	n, err = conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}

	if n != len(msg) {
		t.Fatalf("Incorrect message length %d", len(msg))
	}
	if bytes.Compare(msg, buf[:n]) != 0 {
		t.Fatalf("Incorrect message content")
	}
}
