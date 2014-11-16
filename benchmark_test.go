// Copyright 2014 The DST Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst

import (
	"crypto/rand"
	"io"
	"net"
	"testing"
)

func BenchmarkDST(b *testing.B) {
	aConn, bConn, err := connPair(0, 0)
	if err != nil {
		b.Fatal(err)
	}
	benchmarkConns(b, aConn, bConn)
}

func BenchmarkConstantLoss100ppm(b *testing.B) {
	aConn, bConn, err := connPair(100/1e6, 100/1e6)
	if err != nil {
		b.Fatal(err)
	}
	benchmarkConns(b, aConn, bConn)
}

func BenchmarkConstantLoss1000ppm(b *testing.B) {
	aConn, bConn, err := connPair(1000/1e6, 1000/1e6)
	if err != nil {
		b.Fatal(err)
	}
	benchmarkConns(b, aConn, bConn)
}

func BenchmarkConstantLoss10000ppm(b *testing.B) {
	aConn, bConn, err := connPair(10000/1e6, 10000/1e6)
	if err != nil {
		b.Fatal(err)
	}
	benchmarkConns(b, aConn, bConn)
}

func BenchmarkLimited128KBps(b *testing.B) {
	aConn, bConn, err := limitedConnPair(128e3, 128e3)
	if err != nil {
		b.Fatal(err)
	}
	benchmarkConns(b, aConn, bConn)
}

func BenchmarkLimited1MBps(b *testing.B) {
	aConn, bConn, err := limitedConnPair(1e6, 1e6)
	if err != nil {
		b.Fatal(err)
	}
	benchmarkConns(b, aConn, bConn)
}

func BenchmarkLimited10MBps(b *testing.B) {
	aConn, bConn, err := limitedConnPair(10e6, 10e6)
	if err != nil {
		b.Fatal(err)
	}
	benchmarkConns(b, aConn, bConn)
}

func BenchmarkLimited50MBps(b *testing.B) {
	aConn, bConn, err := limitedConnPair(50e6, 50e6)
	if err != nil {
		b.Fatal(err)
	}
	benchmarkConns(b, aConn, bConn)
}

func benchmarkConns(b *testing.B, aConn, bConn net.Conn) {
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
