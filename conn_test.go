package mdstp

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"net"
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

func TestSingleDataPacket(t *testing.T) {
	a, b, err := connPair(0, 0)
	if err != nil {
		t.Fatal(err)
	}

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
	if err != ErrClosed {
		t.Error("Unexpected non-ErrClosed error", err)
	}

	_, err = a.Read([]byte("something"))
	if err != io.EOF {
		t.Error("Unexpected non-EOF error", err)
	}

	time.Sleep(10 * time.Millisecond)
	// b should also have closed

	_, err = b.Write([]byte("something"))
	if err != ErrClosed {
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
	size := 65536
	n := 128

	src := make([]byte, size)
	io.ReadFull(rand.Reader, src)

	aConn.nextSeqNo = 2<<30 - uint32(size*n/2)
	bConn.debugResetRecvSeqNo <- 2<<30 - uint32(size*n/2)

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
		t.Errorf("Incorrect data % x != % x", data[:16], src[:16])
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
		t.Errorf("Incorrect data % x != % x", data[:16], src[:16])
	}
}

func TestTLSOnTop(t *testing.T) {
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

	a, b, err := connPair(0, 0)
	if err != nil {
		t.Fatal(err)
	}

	client := tls.Client(b, clientConfig)
	server := tls.Server(a, serverConfig)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := server.Handshake()
		if err != nil {
			t.Error(err)
		}
	}()

	err = client.Handshake()
	if err != nil {
		t.Fatal(err)
	}

	wg.Wait()
}

func TestTimeoutCloseWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("slow test")
	}

	a, b, err := connPair(0, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Sneakily stop responding on the server side
	close(b.closed)

	for {
		_, err := a.Write([]byte("stuff to write"))
		if err == ErrClosed {
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
	muxA := NewMux(connA)
	muxA.SetPacketSize(256)

	connB, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	muxB := NewMux(connB)

	errors := make(chan error)
	go func() {
		conn, err := muxA.AcceptUDT()
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
		if uint64(n)/stats.DataPacketsIn > 256 {
			errors <- fmt.Errorf("Too much data read; %d bytes in %d packets", n, stats.DataPacketsIn)
			return
		}
		errors <- nil
	}()

	conn, err := muxB.Dial("mdstp", muxA.Addr().String())
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
