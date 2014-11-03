miniudt
=======

This is the start of an implementation of UDT in Go. The API supports
the usual interfaces and should be fairly stable.

> !!!
> This is not a complete implementation of UDT. This package will not
> communicate successfully with the default/standard implementation.
> Many aspects are simply not implemented, other differ intentionally
> (i.e. KeepAlive and Shutdown handling).
> !!!

The following features are implemented:

 - Basic UDT packet format.
 - UDP Muxing.
 - Stream connections.
 - Client/server connection establishment.
 - Data transfer and resends based on ACK and receive timeouts.
 - SYN cookies.

The following features are *not* currently supported:

 - High performance.
 - Rendezvous connection establishment.
 - Congestion control.
 - Messaging mode connections.
 - NAK, Shutdown, ACK2, Drop and KeepAlive messages.

Documentation
-------------

http://godoc.org/github.com/calmh/miniudt

License
-------

MIT

