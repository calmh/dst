package udt

import (
	"encoding/binary"
	"fmt"
)

type position uint8

const (
	positionMiddle position = iota
	positionLast
	positionFirst
	positionOnly
)

type packetType uint16

const (
	typeHandshake packetType = 0x0
	typeKeepAlive            = 0x1
	typeACK                  = 0x2
	//typeNAK                  = 0x3
	//typeShutdown             = 0x5
	//typeACK2                 = 0x6
	//typeDrop                 = 0x7
	//typeUser                 = 0x7fff
)

type dataHeader struct {
	sequenceNo uint32
	position   position
	inOrder    bool
	messageNo  uint32
	timestamp  uint32
	connID     uint32
}

type header interface {
	len() int
	marshal(bs []byte)
	String() string
}

const (
	typeBit       = 1 << 31
	ffoBits       = 1<<31 | 1<<30 | 1<<29
	positionShift = 30
	inOrderBit    = 1 << 29
)

func unmarshalHeader(bs []byte) header {
	isControl := binary.BigEndian.Uint32(bs)&typeBit != 0
	if isControl {
		h := &controlHeader{}
		h.unmarshal(bs)
		return h
	} else {
		h := &dataHeader{}
		h.unmarshal(bs)
		return h
	}
}

func (h *dataHeader) marshal(bs []byte) {
	var f uint32 = h.messageNo &^ ffoBits
	f |= uint32(h.position) << positionShift
	if h.inOrder {
		f |= inOrderBit
	}

	binary.BigEndian.PutUint32(bs[0:], h.sequenceNo&^typeBit)
	binary.BigEndian.PutUint32(bs[4:], f)
	binary.BigEndian.PutUint32(bs[8:], h.timestamp)
	binary.BigEndian.PutUint32(bs[12:], h.connID)
}

func (h *dataHeader) unmarshal(bs []byte) {
	h.sequenceNo = binary.BigEndian.Uint32(bs[0:])
	f := binary.BigEndian.Uint32(bs[4:])
	h.position = position(f >> positionShift)
	h.inOrder = f&inOrderBit != 0
	h.messageNo = f &^ ffoBits
	h.timestamp = binary.BigEndian.Uint32(bs[8:])
	h.connID = binary.BigEndian.Uint32(bs[12:])
}

func (h *dataHeader) len() int {
	return 16
}

func (h *dataHeader) String() string {
	return fmt.Sprintf("%#v", h)
}

type controlHeader struct {
	packetType packetType
	additional uint32
	timestamp  uint32
	connID     uint32
}

func (h *controlHeader) marshal(bs []byte) {
	var f uint32 = uint32(h.packetType)<<16 | typeBit
	binary.BigEndian.PutUint32(bs[0:], f)
	binary.BigEndian.PutUint32(bs[4:], h.additional)
	binary.BigEndian.PutUint32(bs[8:], h.timestamp)
	binary.BigEndian.PutUint32(bs[12:], h.connID)
}

func (h *controlHeader) unmarshal(bs []byte) {
	h.packetType = packetType(binary.BigEndian.Uint32(bs) &^ typeBit >> 16)
	h.additional = binary.BigEndian.Uint32(bs[4:])
	h.timestamp = binary.BigEndian.Uint32(bs[8:])
	h.connID = binary.BigEndian.Uint32(bs[12:])
}

func (h *controlHeader) len() int {
	return 16
}

func (h *controlHeader) String() string {
	return fmt.Sprintf("%#v", h)
}
