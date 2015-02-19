dst
===

[![Latest Build](http://img.shields.io/jenkins/s/http/build.syncthing.net/dst.svg?style=flat-square)](http://build.syncthing.net/job/dst/lastBuild/)
[![API Documentation](http://img.shields.io/badge/api-Godoc-blue.svg?style=flat-square)](http://godoc.org/github.com/calmh/dst)
[![MIT License](http://img.shields.io/badge/license-MIT-blue.svg?style=flat-square)](http://opensource.org/licenses/MIT)

DST is the Datagram Stream Transfer protocol. In principle it's yet
another way to provide a reliable stream protocol on top of UDP, similar
to uTP, RUDP, and DST.

In fact, it's mostly based on DST with some significant differences;

 - The packet format is simplified.

 - The keepalive mechanism has been removed to reduce complexity and
   bandwidth use. Applications can perform keepalives as desired.

 - Windowing and congestion control is simpler, with room for
   future improvement.

There's currently no protocol specification document apart from the
code. One will be written once it's proven to work and the formats can
be locked down.

The API follows the usual `net` conventions and should be familiar.

Documentation
-------------

http://godoc.org/github.com/calmh/dst

