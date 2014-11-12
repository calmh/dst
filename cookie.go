// Copyright (C) 2014 Jakob Borg and Contributors (see the CONTRIBUTORS file).
// All rights reserved. Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"net"
)

var cookieKey = make([]byte, 16)

func init() {
	_, err := rand.Reader.Read(cookieKey)
	if err != nil {
		panic(err)
	}
}

func cookie(remote net.Addr) uint32 {
	hash := sha256.New()
	hash.Write([]byte(remote.String()))
	hash.Write(cookieKey)
	bs := hash.Sum(nil)
	return binary.BigEndian.Uint32(bs)
}
