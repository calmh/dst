udt
===

This is the start of an implementation of UDT in Go. The API supports
the usual interfaces and should be fairly stable.

> !!!
> This is not a complete implementation of UDT. This package will not
> communicate successfully with the default/standard implementation.
> !!!

The following features are implemented:

 - Basic UDT packet format.
 - UDP Muxing.
 - Stream connections.
 - Client/server connection establishment.
 - Data transfer and resends based on ACK and receive timeouts.

The following features are *not* currently supported:

 - Rendezvous connection establishment.
 - Congestion control.
 - Messaging mode connections.
 - NAK, Shutdown, ACK2, Drop and KeepAlive messages.

Documentation
-------------

http://godoc.org/github.com/calmh/udt

License
-------

MIT

