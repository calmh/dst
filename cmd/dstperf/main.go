// Copyright 2014 The DST Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"crypto/rand"
	"flag"
	"io"
	"log"
	"net"
	"runtime"
	"time"

	"github.com/calmh/dst"
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	flag.Parse()

	sock, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		log.Fatal(err)
	}

	mux := dst.NewMux(sock, 0)

	if flag.NArg() > 0 {
		conn, err := mux.Dial("dst", flag.Arg(0))
		if err != nil {
			log.Fatal(err)
		}
		log.Println("Connected to", conn)
		test(conn)
		return
	} else {
		log.Println("Accpting connections on", mux.Addr())
		for {
			conn, err := mux.Accept()
			if err != nil {
				log.Fatal(err)
			}
			log.Println("Connection from", conn)
			go test(conn)
		}
	}
}

func test(conn net.Conn) {
	go func() {
		writeTo(conn)
		conn.Close()
	}()

	readFrom(conn)

	log.Println(conn, conn.(*dst.Conn).GetStatistics())
}

func readFrom(conn net.Conn) {
	buf := make([]byte, 65536)
	t0 := time.Now()
	var c int64
	for {
		n, err := conn.Read(buf)
		c += int64(n)
		if err != nil {
			break
		}
		if time.Since(t0) > time.Second {
			log.Printf("%v recv %.1f MB, %.1f KB/s", conn, float64(c)/1024/1024, float64(c)/1024/time.Since(t0).Seconds())
			c = 0
			t0 = time.Now()
		}
	}
}

func writeTo(conn net.Conn) {
	buf := make([]byte, 65536)
	io.ReadFull(rand.Reader, buf)

	t0 := time.Now()
	t1 := time.Now()
	var c int64
	for time.Since(t0) < 30*time.Second {
		n, err := conn.Write(buf)
		c += int64(n)
		if err != nil {
			break
		}
		if time.Since(t1) > time.Second {
			log.Printf("%v send %.1f MB, %.1f KB/s", conn, float64(c)/1024/1024, float64(c)/1024/time.Since(t1).Seconds())
			c = 0
			t1 = time.Now()
		}
	}
}
