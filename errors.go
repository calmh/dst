// Copyright 2014 The DST Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst

// Error represents the various dst-internal error conditions.
type Error struct {
	Err string
}

// Error returns a string representation of the error.
func (e Error) Error() string {
	return e.Err
}

var (
	ErrCloseClosed      = &Error{"close on already closed mux"}
	ErrAcceptClosed     = &Error{"accept on closed mux"}
	ErrNotDSTNetwork    = &Error{"network is not dst"}
	ErrHandshakeTimeout = &Error{"handshake timeout"}
	ErrClosed           = &Error{"operation on closed connection"}
)
