// Copyright 2014 The DST Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst_test

import (
	"net"
	"testing"

	"github.com/calmh/dst"
)

func TestMuxNew(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		t.Fatal(err)
	}

	mp := dst.NewMux(conn, 0)
	if mp == nil {
		t.Error("Unexpected nil Mux")
	}
}

func TestMuxClose(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		t.Fatal(err)
	}

	mp := dst.NewMux(conn, 0)
	if mp == nil {
		t.Error("Unexpected nil Mux")
	}

	err = mp.Close()
	if err != nil {
		t.Error(err)
	}

	err = mp.Close()
	if err != dst.ErrClosedMux {
		t.Error("Unexpected error", err)
	}
}

func TestMuxAddr(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		t.Fatal(err)
	}

	mp := dst.NewMux(conn, 0)
	if mp == nil {
		t.Error("Unexpected nil Mux")
	}

	if mp.Addr() != conn.LocalAddr() {
		t.Error("Unexpected Addr", mp.Addr())
	}
}
