package main

import (
	"crypto/rand"
	"flag"
	"io"
	"log"
	"net"
	"time"

	"github.com/calmh/mdstp"
)

func main() {
	flag.Parse()

	sock, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		log.Fatal(err)
	}

	mux := mdstp.NewMux(sock)
	log.Println(mux.Addr())

	go func() {
		for {
			conn, err := mux.Accept()
			if err != nil {
				log.Fatal(err)
			}
			log.Println(conn)
			go test(conn)
		}
	}()

	if flag.NArg() > 0 {
		conn, err := mux.Dial("mdstp", flag.Arg(0))
		if err != nil {
			log.Fatal(err)
		}
		log.Println(conn)
		go test(conn)
	}

	select {}
}

func test(conn net.Conn) {
	go func() {
		buf := make([]byte, 65536)
		t0 := time.Now()
		var c int64
		for {
			n, err := conn.Read(buf)
			c += int64(n)
			if err != nil {
				break
			}
		}
		log.Printf("%v recv %.1f MB, %.1f KB/s", conn, float64(c)/1024/1024, float64(c)/1024/time.Since(t0).Seconds())
	}()

	buf := make([]byte, 65536)
	io.ReadFull(rand.Reader, buf)

	t0 := time.Now()
	var c int64
	for time.Since(t0) < 30*time.Second {
		n, err := conn.Write(buf)
		c += int64(n)
		if err != nil {
			break
		}
	}
	log.Printf("%v send %.1f MB, %.1f KB/s", conn, float64(c)/1024/1024, float64(c)/1024/time.Since(t0).Seconds())

	conn.Close()
}
