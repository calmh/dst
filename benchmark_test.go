package mdstp

import (
	"crypto/rand"
	"io"
	"log"
	"testing"
)

func BenchmarkSendNoLoss(b *testing.B) {
	benchmarkWithLoss(b, 0)
}

func BenchmarkSend0p0001Loss(b *testing.B) {
	benchmarkWithLoss(b, 0.0001)
}

func BenchmarkSend0p001Loss(b *testing.B) {
	benchmarkWithLoss(b, 0.001)
}

func BenchmarkSend0p01Loss(b *testing.B) {
	benchmarkWithLoss(b, 0.01)
}

func benchmarkWithLoss(b *testing.B, loss float64) {
	aConn, bConn, err := connPair(loss, 0)
	if err != nil {
		b.Fatal(err)
	}

	src := make([]byte, 65536)
	io.ReadFull(rand.Reader, src)

	go func() {
		for i := 0; i < b.N; i++ {
			_, err := aConn.Write(src)
			if err != nil {
				b.Fatal(err)
			}
		}
	}()

	b.ResetTimer()

	buf := make([]byte, 65536)
	for i := 0; i < b.N; i++ {
		_, err := io.ReadFull(bConn, buf)
		if err != nil {
			b.Fatal(err)
		}
	}

	log.Println("stats", aConn.GetStatistics(), bConn.GetStatistics())
	b.SetBytes(65536)
}
