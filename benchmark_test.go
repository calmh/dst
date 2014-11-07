package dst

import (
	"crypto/rand"
	"io"
	"net"
	"testing"
)

func BenchmarkDST(b *testing.B) {
	benchmarkWithLoss(b, 0)
}

func Benchmark0p0001Loss(b *testing.B) {
	benchmarkWithLoss(b, 0.0001)
}

func Benchmark0p001Loss(b *testing.B) {
	benchmarkWithLoss(b, 0.001)
}

func Benchmark0p01Loss(b *testing.B) {
	benchmarkWithLoss(b, 0.01)
}

func benchmarkWithLoss(b *testing.B, loss float64) {
	aConn, bConn, err := connPair(loss, 0)
	if err != nil {
		b.Fatal(err)
	}

	src := make([]byte, 65536)
	io.ReadFull(rand.Reader, src)

	go func(n int) {
		for i := 0; i < n; i++ {
			_, err := aConn.Write(src)
			if err != nil {
				b.Fatal(err)
			}
		}
	}(b.N)

	b.ResetTimer()

	buf := make([]byte, 65536)
	for i := 0; i < b.N; i++ {
		_, err := io.ReadFull(bConn, buf)
		if err != nil {
			b.Fatal(err)
		}
	}

	b.SetBytes(65536)
}

func BenchmarkTCP(b *testing.B) {
	aConn, bConn, err := tcpConnPair()
	if err != nil {
		b.Fatal(err)
	}

	src := make([]byte, 65536)
	io.ReadFull(rand.Reader, src)

	go func(n int) {
		for i := 0; i < n; i++ {
			_, err := aConn.Write(src)
			if err != nil {
				b.Fatal(err)
			}
		}
	}(b.N)

	b.ResetTimer()

	buf := make([]byte, 65536)
	for i := 0; i < b.N; i++ {
		_, err := io.ReadFull(bConn, buf)
		if err != nil {
			b.Fatal(err)
		}
	}

	b.SetBytes(65536)
}

func BenchmarkUDP(b *testing.B) {
	aConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		b.Fatal(err)
	}
	aConn.SetWriteBuffer(4096 * 1024)

	bConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		b.Fatal(err)
	}
	bConn.SetReadBuffer(4096 * 1024)
	bAddr := bConn.LocalAddr()

	src := make([]byte, 1472)
	io.ReadFull(rand.Reader, src)

	go func(n int) {
		for i := 0; i < n; i++ {
			_, err := aConn.WriteTo(src, bAddr)
			if err != nil {
				b.Fatal(err)
			}
		}
	}(b.N)

	b.ResetTimer()

	buf := make([]byte, 1472)
	for i := 0; i < b.N; i++ {
		n, err := bConn.Read(buf)
		if err != nil {
			b.Fatal(err)
		}
		if n != 1472 {
			b.Fatalf("%d != 1472", n)
		}
	}

	b.SetBytes(1024)
}

func BenchmarkUDPDevNull(b *testing.B) {
	aConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		b.Fatal(err)
	}
	aConn.SetWriteBuffer(4096 * 1024)

	bConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		b.Fatal(err)
	}
	bConn.SetReadBuffer(4096 * 1024)
	bAddr := bConn.LocalAddr()

	src := make([]byte, 1472)
	io.ReadFull(rand.Reader, src)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := aConn.WriteTo(src, bAddr)
		if err != nil {
			b.Fatal(err)
		}
	}

	b.SetBytes(1024)
}
