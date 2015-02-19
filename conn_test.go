// Copyright 2014 The DST Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHandshake(t *testing.T) {
	_, _, err := connPair(0, 0)
	if err != nil {
		t.Error(err)
	}
}

func TestHandshakeTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("slow test")
	}

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	mux := NewMux(conn, 0)

	_, err = mux.Dial("dst", "192.0.2.42:4242")
	if err != ErrHandshakeTimeout {
		t.Error("Unexpected error", err)
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Error("Unexpected error string", err.Error())
	}
}

func TestManyConnections(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("need GOMAXPROCS > 1")
	}
	conn0, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	mux0 := NewMux(conn0, 0)
	go func() {
		for {
			mux0.Accept()
		}
	}()

	conn1, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	mux1 := NewMux(conn1, 0)

	allocs := testing.AllocsPerRun(10000, func() {
		_, err = mux1.Dial("dst", conn0.LocalAddr().String())
		if err != nil {
			t.Error("Unexpected error", err)
		}
	})
	t.Logf("%.0f allocs/run", allocs)
}

func TestAddrs(t *testing.T) {
	a, b, err := connPair(0, 0)
	if err != nil {
		t.Error(err)
	}

	als := a.LocalAddr().String()
	ars := a.RemoteAddr().String()
	bls := b.LocalAddr().String()
	brs := b.RemoteAddr().String()
	if !strings.HasPrefix(als, "127.0.0.1") {
		t.Errorf("A local %s missing 127.0.0.1 prefix", als)
	}
	if !strings.HasPrefix(bls, "127.0.0.1") {
		t.Errorf("B local %s missing 127.0.0.1 prefix", bls)
	}
	if als == ars {
		t.Errorf("A remote == A local address; %s", als)
	}
	if bls == brs {
		t.Errorf("B remote == B local address; %s", bls)
	}
	if als == bls {
		t.Errorf("A local == B local address; %s", als)
	}
	if ars == brs {
		t.Errorf("A remote == B remote address; %s", ars)
	}
	if ars != bls {
		t.Errorf("A remote %s != B local %s", ars, bls)
	}
	if als != brs {
		t.Errorf("A local %s != B remote %s", als, brs)
	}
}

func TestSingleDataPacketC2S(t *testing.T) {
	a, b, err := connPair(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	testSingleDataPacket(a, b, t)
}

func TestSingleDataPacketS2C(t *testing.T) {
	a, b, err := connPair(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	testSingleDataPacket(b, a, t)
}

func testSingleDataPacket(a, b net.Conn, t *testing.T) {
	src := []byte("Hello, World!")

	var wg sync.WaitGroup
	wg.Add(1)
	var aErr error
	go func() {
		defer wg.Done()
		_, aErr = a.Write(src)
	}()

	buf := make([]byte, 65536)
	n, bErr := b.Read(buf)

	wg.Wait()

	if aErr != nil {
		t.Error(aErr)
	}
	if bErr != nil {
		t.Error(bErr)
	}
	if data := buf[:n]; bytes.Compare(data, src) != 0 {
		t.Errorf("Incorrect data %q != %q", data, src)
	}

	// So that an ACK is visible in the trace when running with -tags debug...
	time.Sleep(100 * time.Millisecond)
}

func TestClosedReadWrite(t *testing.T) {
	a, b, err := connPair(0, 0)
	if err != nil {
		t.Fatal(err)
	}

	err = a.Close()
	if err != nil {
		t.Error(err)
	}

	err = a.Close()
	if err != nil {
		t.Error(err)
	}

	_, err = a.Write([]byte("something"))
	if err != ErrClosedConn {
		t.Error("Unexpected non-ErrClosed error", err)
	}

	_, err = a.Read([]byte("something"))
	if err != io.EOF {
		t.Error("Unexpected non-EOF error", err)
	}

	// Time for shutdown packets to pass and linger to occur.

	time.Sleep(500 * time.Millisecond)

	// b should also have closed

	_, err = b.Write([]byte("something"))
	if err != ErrClosedConn {
		t.Error("Unexpected non-ErrClosed error", err)
	}

	_, err = b.Read([]byte("something"))
	if err != io.EOF {
		t.Error("Unexpected non-EOF error", err)
	}
}

func TestSequenceWrap(t *testing.T) {
	aConn, bConn, err := connPair(0, 0)
	if err != nil {
		t.Fatal(err)
	}

	// needs to be large enough to enforce an ack cycle, to test that code
	size := 10240
	n := 128

	src := make([]byte, size)
	io.ReadFull(rand.Reader, src)

	aConn.nextSeqNo = sequenceNo(2<<31 - size*n/2)
	bConn.debugResetRecvSeqNo <- sequenceNo(2<<31 - size*n/2)

	go func() {
		for i := 0; i < n; i++ {
			aConn.Write(src)
		}
	}()

	buf := make([]byte, size)
	for i := 0; i < n; i++ {
		n, err := io.ReadFull(bConn, buf)

		if err != nil {
			t.Fatal(err)
		}
		if data := buf[:n]; bytes.Compare(data, src) != 0 {
			t.Fatalf("Incorrect data %q != %q", data, src)
		}
	}
}

func TestLargeData(t *testing.T) {
	a, b, err := connPair(0, 0)
	if err != nil {
		t.Fatal(err)
	}

	src := make([]byte, 1<<18)
	io.ReadFull(rand.Reader, src)

	var wg sync.WaitGroup
	wg.Add(1)
	var aErr error
	go func() {
		defer wg.Done()
		_, aErr = a.Write(src)
	}()

	buf := make([]byte, 1<<18)
	n, bErr := io.ReadFull(b, buf)

	wg.Wait()

	if aErr != nil {
		t.Error(aErr)
	}
	if bErr != nil {
		t.Error(bErr)
	}
	if data := buf[:n]; bytes.Compare(data, src) != 0 {
		t.Errorf("Incorrect data\n  % x...% x (%d) !=\n  % x...% x (%d)", data[:8], data[len(data)-8:], len(data), src[:8], src[len(src)-8:], len(src))
	}
}

func TestLargeDataLossy(t *testing.T) {
	a, b, err := connPair(0.1, 0)
	if err != nil {
		t.Fatal(err)
	}

	src := make([]byte, 256*1024)
	io.ReadFull(rand.Reader, src)

	var wg sync.WaitGroup
	wg.Add(1)
	var aErr error
	go func() {
		defer wg.Done()
		_, aErr = a.Write(src)
	}()

	buf := make([]byte, 1<<18)
	n, bErr := io.ReadFull(b, buf)

	wg.Wait()

	if aErr != nil {
		t.Error(aErr)
	}
	if bErr != nil {
		t.Error(bErr)
	}
	if data := buf[:n]; bytes.Compare(data, src) != 0 {
		t.Errorf("Incorrect data\n  % x...% x (%d) !=\n  % x...% x (%d)", data[:8], data[len(data)-8:], len(data), src[:8], src[len(src)-8:], len(src))
	}
}

func TestTLSOnTopOfLossy(t *testing.T) {
	if testing.Short() {
		t.Skip("slow test")
	}

	keypair, err := tls.LoadX509KeyPair("testdata/cert.pem", "testdata/key.pem")
	if err != nil {
		t.Fatal(err)
	}

	serverConfig := &tls.Config{
		Certificates: []tls.Certificate{keypair},
	}

	clientConfig := &tls.Config{
		InsecureSkipVerify: true,
	}

	a, b, err := connPair(0.001, 0.001)
	if err != nil {
		t.Fatal(err)
	}

	client := tls.Client(b, clientConfig)
	server := tls.Server(a, serverConfig)

	errors := make(chan error)

	go func() {
		errors <- server.Handshake()
	}()

	err = client.Handshake()
	if err != nil {
		t.Fatal(err)
	}

	err = <-errors
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		buf := make([]byte, 65536)
		for {
			_, err := client.Read(buf)
			if err == io.EOF {
				errors <- nil
				return
			} else if err != nil {
				errors <- err
				return
			}
		}
	}()

	go func() {
		t0 := time.Now()
		buf := make([]byte, 65536)
		for time.Since(t0) < 10*time.Second {
			_, err := server.Write(buf)
			if err != nil {
				errors <- err
			}
		}

		server.Close()
		errors <- nil
	}()

	err = <-errors
	if err != nil {
		t.Fatal(err)
	}
	err = <-errors
	if err != nil {
		t.Fatal(err)
	}
}

func TestTimeoutCloseWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("slow test")
	}

	a, b, err := connPair(0, 0)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		t0 := time.Now()
		buf := make([]byte, 65536)
		for time.Since(t0) < 5*time.Second {
			b.Read(buf)
		}
		// Sneakily stop responding on the server side
		b.mux.conn.Close()
	}()

	for {
		_, err := a.Write([]byte("stuff to write"))
		if err == ErrClosedConn {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestPacketSize(t *testing.T) {
	connA, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	muxA := NewMux(connA, 256)

	connB, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	muxB := NewMux(connB, 0)

	testPacketSize(muxA, muxB, t) // Small packets on the server side
	testPacketSize(muxB, muxA, t) // Small packets on the client side
}

func testPacketSize(muxA, muxB *Mux, t *testing.T) {
	errors := make(chan error)
	go func() {
		conn, err := muxA.AcceptDST()
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

		stats := conn.GetStatistics()
		if int64(n)/stats.DataPacketsIn > 256 {
			errors <- fmt.Errorf("Too much data read; %d bytes in %d packets", n, stats.DataPacketsIn)
			return
		}
		errors <- nil
	}()

	conn, err := muxB.Dial("dst", muxA.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	n, err := conn.Write(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1024 {
		t.Errorf("Too little data written; %d bytes", n)
	}

	if err = <-errors; err != nil {
		t.Error(err)
	}
}
