package udt

import (
	"bytes"
	"crypto/rand"
	"io"
	"sync"
	"testing"
)

func TestHandshake(t *testing.T) {
	_, _, err := connPair(0, 0)
	if err != nil {
		t.Error(err)
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
	a, _, err := connPair(0, 0)
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
	if err != ErrClosed {
		t.Error("Unexpected non-ErrClosed error", err)
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
