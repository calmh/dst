package mdstp_test

import (
	"net"
	"testing"

	"github.com/calmh/mdstp"
)

func TestMuxNew(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		t.Fatal(err)
	}

	mp := mdstp.NewMux(conn)
	if mp == nil {
		t.Error("Unexpected nil Mux")
	}
}

func TestMuxClose(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		t.Fatal(err)
	}

	mp := mdstp.NewMux(conn)
	if mp == nil {
		t.Error("Unexpected nil Mux")
	}

	err = mp.Close()
	if err != nil {
		t.Error(err)
	}

	err = mp.Close()
	if err != mdstp.ErrCloseClosed {
		t.Error("Unexpected error", err)
	}
}

func TestMuxAddr(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		t.Fatal(err)
	}

	mp := mdstp.NewMux(conn)
	if mp == nil {
		t.Error("Unexpected nil Mux")
	}

	if mp.Addr() != conn.LocalAddr() {
		t.Error("Unexpected Addr", mp.Addr())
	}
}
