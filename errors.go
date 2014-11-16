// Copyright 2014 The DST Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst

type Error struct {
	Err string
}

func (e Error) Error() string {
	return e.Err
}

var (
	ErrCloseClosed      = &Error{"close on already closed mux"}
	ErrAcceptClosed     = &Error{"accept on closed mux"}
	ErrNotUDTNetwork    = &Error{"network is not dst"}
	ErrHandshakeTimeout = &Error{"handshake timeout"}
	ErrClosed           = &Error{"operation on closed connection"}
)
