// Copyright 2014 The DST Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst

import (
	"net"
	"testing"
)

func TestCookie(t *testing.T) {
	c0 := cookie(&net.UDPAddr{})
	if c0 == 0 {
		t.Error("very suspicious cookie value 0")
	}

	c1 := cookie(&net.UDPAddr{IP: net.IP{1, 2, 3, 4}, Port: 42})
	if c1 == 0 {
		t.Error("very suspicious cookie value 0")
	}
	if c1 == c0 {
		t.Error("identical cookies with value", c0)
	}

	c2 := cookie(&net.UDPAddr{IP: net.IP{1, 2, 3, 4}, Port: 43})
	if c2 == 0 {
		t.Error("very suspicious cookie value 0")
	}
	if c2 == c1 {
		t.Error("identical cookies with value", c2)
	}
}

var global uint32

func BenchmarkCookie(b *testing.B) {
	addr := &net.UDPAddr{IP: net.IP{1, 2, 3, 4}, Port: 43}
	for i := 0; i < b.N; i++ {
		global = cookie(addr)
	}
}
