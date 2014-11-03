package miniudt

import (
	"encoding/binary"
	"fmt"
)

type position uint8

const udtHeaderLen = 16

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
	typeShutdown = 0x5
	//typeACK2                 = 0x6
	//typeDrop                 = 0x7
	//typeUser                 = 0x7fff
)

const (
	connectionTypeRequest            = 1
	connectionTypeResponse           = -1
	connectionTypeRendezvous         = 0
	connectionTypeRendezvousComplete = -2
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

func (h *controlHeader) String() string {
	return fmt.Sprintf("%#v", h)
}

func handshakeData(data []byte, seqNo, packetSize, connID, cookie uint32, connType int32) {
	binary.BigEndian.PutUint32(data[0:], 4)                 // UDT version
	binary.BigEndian.PutUint32(data[4:], 0)                 // Socket Type (STREAM)
	binary.BigEndian.PutUint32(data[8:], seqNo)             // Initial Sequence Number
	binary.BigEndian.PutUint32(data[12:], packetSize)       // Packet Size
	binary.BigEndian.PutUint32(data[16:], 0)                // Flow Window
	binary.BigEndian.PutUint32(data[20:], uint32(connType)) // Connection Type
	binary.BigEndian.PutUint32(data[24:], connID)           // Client Conn ID
	binary.BigEndian.PutUint32(data[28:], cookie)           // Cookie
	binary.BigEndian.PutUint32(data[32:], 0)                // Peer IP Address (TODO)
}
