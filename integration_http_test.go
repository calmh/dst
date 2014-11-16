// Copyright 2014 The DST Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// +build integration

package dst_test

import (
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/calmh/dst"
)

func TestIntegrationHTTP(t *testing.T) {
	const result = "Hello, World!"

	srvConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	srvMux := dst.NewMux(srvConn, 0)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, result)
	})

	go http.Serve(srvMux, nil)

	clientConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	clientMux := dst.NewMux(clientConn, 0)
	client := &http.Client{
		Transport: &http.Transport{
			Dial: func(net, addr string) (net.Conn, error) {
				return clientMux.Dial("dst", addr)
			},
		},
	}

	url := fmt.Sprintf("http://%s/", srvMux.Addr())
	errors := make(chan error)

	var wg sync.WaitGroup
	t0 := time.Now()
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := 0
			for time.Since(t0) < 30*time.Second {
				resp, err := client.Get(url)
				if err != nil {
					errors <- err
					return
				}
				if resp.StatusCode != 200 {
					errors <- fmt.Errorf("%s", resp.Status)
					return
				}

				bs, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					errors <- err
					return
				}
				resp.Body.Close()

				if string(bs) != result {
					errors <- fmt.Errorf("Incorrect string %q", bs)
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
