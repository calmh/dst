// Copyright 2014 The DST Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst

import (
	"encoding/binary"
	"fmt"
	"net"
)

type position uint8

const dstHeaderLen = 12

const (
	positionMiddle position = iota
	positionLast
	positionFirst
	positionOnly
)

type packetType int8

const (
	typeData      packetType = 0x0
	typeHandshake            = 0x1
	typeKeepAlive            = 0x2
	typeACK                  = 0x3
	typeACK2                 = 0x4
	typeNAK                  = 0x5
	typeShutdown             = 0x6
)

func (t packetType) String() string {
	switch t {
	case typeData:
		return "data"
	case typeHandshake:
		return "handshake"
	case typeKeepAlive:
		return "keepalive"
	case typeACK:
		return "ack"
	case typeACK2:
		return "ack2"
	case typeNAK:
		return "nak"
	case typeShutdown:
		return "shutdown"
	default:
		return "unknown"
	}
}

const (
	flagRequest  = 1 << 0 // This packet is a handshake request
	flagResponse = 1 << 1 // This packet is a handshake response
	flagCookie   = 1 << 2 // This packet contains a coookie challenge
)

type header struct {
	packetType packetType // 4 bits
	flags      uint8      // 4 bits
	connID     uint32     // 24 bits
	sequenceNo uint32
	timestamp  uint32
}

func (h header) marshal(bs []byte) {
	binary.BigEndian.PutUint32(bs, uint32(h.connID&0xffffff))
	bs[0] = h.flags | uint8(h.packetType)<<4
	binary.BigEndian.PutUint32(bs[4:], h.sequenceNo)
	binary.BigEndian.PutUint32(bs[8:], h.timestamp)
}

func unmarshalHeader(bs []byte) header {
	var h header
	h.packetType = packetType(bs[0] >> 4)
	h.flags = bs[0] & 0xf
	h.connID = binary.BigEndian.Uint32(bs) & 0xffffff
	h.sequenceNo = binary.BigEndian.Uint32(bs[4:])
	h.timestamp = binary.BigEndian.Uint32(bs[8:])
	return h
}

func (h header) String() string {
	return fmt.Sprintf("header{type=%s flags=0x%x connID=0x%06x seq=0x%08x time=0x%08x}", h.packetType, h.flags, h.connID, h.sequenceNo, h.timestamp)
}

type handshakeData struct {
	packetSize uint32
	connID     uint32
	cookie     uint32
}

func (h handshakeData) marshalInto(data []byte) {
	binary.BigEndian.PutUint32(data[0:], h.packetSize)
	binary.BigEndian.PutUint32(data[4:], h.connID)
	binary.BigEndian.PutUint32(data[8:], h.cookie)
}

func (h handshakeData) marshal() []byte {
	var data [12]byte
	h.marshalInto(data[:])
	return data[:]
}

func unmarshalHandshakeData(data []byte) handshakeData {
	var h handshakeData
	h.packetSize = binary.BigEndian.Uint32(data[0:])
	h.connID = binary.BigEndian.Uint32(data[4:])
	h.cookie = binary.BigEndian.Uint32(data[8:])
	return h
}

func (h handshakeData) String() string {
	return fmt.Sprintf("handshake{size=%d connID=0x%06x cookie=0x%08x}", h.packetSize, h.connID, h.cookie)
}

type packet struct {
	src  uint32
	dst  net.Addr
	hdr  header
	data []byte
}

func (p packet) String() string {
	var dst string
	if p.dst != nil {
		dst = "dst=" + p.dst.String() + " "
	}
	switch p.hdr.packetType {
	case typeHandshake:
		return fmt.Sprintf("%spacket{src=0x%08x %v %v}", dst, p.src, p.hdr, unmarshalHandshakeData(p.data))
	default:
		return fmt.Sprintf("%spacket{src=0x%08x %v data[:%d]}", dst, p.src, p.hdr, len(p.data))
	}
}

func (p packet) LessSeq(seq uint32) bool {
	diff := seq - p.hdr.sequenceNo
	if diff == 0 {
		return false
	}
	return diff < 1<<31
}

func (a packet) Less(b packet) bool {
	return a.LessSeq(b.hdr.sequenceNo)
}
