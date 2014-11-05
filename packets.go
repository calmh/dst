package mdstp

import (
	"encoding/binary"
	"fmt"
)

type position uint8

const mdstpHeaderLen = 16

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
	typeNAK                  = 0x4
	typeShutdown             = 0x5
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
	case typeNAK:
		return "nak"
	case typeShutdown:
		return "shutdown"
	default:
		return "uknown"
	}
}

const (
	flagsRequest  = 1 << 0
	flagsResponse = 1 << 1
	flagsCookie   = 1 << 2
)

type header struct {
	packetType packetType // 4 bits
	flags      uint8      // 4 bits
	connID     uint32
	sequenceNo uint32
	timestamp  uint32
	extra      uint32
}

func (h header) marshal(bs []byte) {
	binary.BigEndian.PutUint32(bs, uint32(h.connID&0xffffff))
	bs[0] = h.flags | uint8(h.packetType)<<4
	binary.BigEndian.PutUint32(bs[4:], h.sequenceNo)
	binary.BigEndian.PutUint32(bs[8:], h.timestamp)
	binary.BigEndian.PutUint32(bs[12:], h.extra)
}

func (h *header) unmarshal(bs []byte) {
	h.packetType = packetType(bs[0] >> 4)
	h.flags = bs[0] & 0xf
	h.connID = binary.BigEndian.Uint32(bs) & 0xffffff
	h.sequenceNo = binary.BigEndian.Uint32(bs[4:])
	h.timestamp = binary.BigEndian.Uint32(bs[8:])
	h.extra = binary.BigEndian.Uint32(bs[12:])
}

func (h header) String() string {
	return fmt.Sprintf("header{type=%s flags=0x%x connID=0x%06x seq=0x%08x time=0x%08x extra=0x%08x}", h.packetType, h.flags, h.connID, h.sequenceNo, h.timestamp, h.extra)
}

type handshakeData struct {
	seqNo      uint32
	packetSize uint32
	connID     uint32
	cookie     uint32
}

func (h handshakeData) marshal(data []byte) {
	binary.BigEndian.PutUint32(data[0:], h.seqNo)
	binary.BigEndian.PutUint32(data[4:], h.packetSize)
	binary.BigEndian.PutUint32(data[8:], h.connID)
	binary.BigEndian.PutUint32(data[12:], h.cookie)
}

func (h *handshakeData) unmarshal(data []byte) {
	h.seqNo = binary.BigEndian.Uint32(data[0:])
	h.packetSize = binary.BigEndian.Uint32(data[4:])
	h.connID = binary.BigEndian.Uint32(data[8:])
	h.cookie = binary.BigEndian.Uint32(data[12:])
}

func (h handshakeData) String() string {
	return fmt.Sprintf("handshake{seq=0x%08x size=%d connID=0x%06x cookie=0x%08x", h.seqNo, h.packetSize, h.connID, h.cookie)
}
