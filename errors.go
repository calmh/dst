package miniudt

type Error struct {
	Err string
}

func (e Error) Error() string {
	return e.Err
}

var (
	ErrCloseClosed      = &Error{"close on already closed mux"}
	ErrAcceptClosed     = &Error{"accept on closed mux"}
	ErrNotUDTNetwork    = &Error{"network is not udt"}
	ErrHandshakeTimeout = &Error{"handshake timeout"}
	ErrClosed           = &Error{"operation on closed connection"}
)
