package udt

import (
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
)

var headerTests = []struct {
	hex string
	hdr interface{}
}{
	{
		"00000000 00000000 00000000 00000000",
		&dataHeader{},
	},
	{
		"12345678 A0008901 98765432 23457853",
		&dataHeader{
			sequenceNo: 0x12345678,
			position:   positionFirst,
			inOrder:    true,
			messageNo:  0x8901,
			timestamp:  0x98765432,
			connID:     0x23457853,
		},
	},
	{
		"92340000 22334455 55667788 99887766",
		&controlHeader{
			packetType: 0x1234,
			additional: 0x22334455,
			timestamp:  0x55667788,
			connID:     0x99887766,
		},
	},
}

func TestEncodeHeaders(t *testing.T) {
	for i, tc := range headerTests {
		var actual [16]byte
		switch h := tc.hdr.(type) {
		case *dataHeader:
			h.marshal(actual[:])
		case *controlHeader:
			h.marshal(actual[:])
		}
		expected, _ := hex.DecodeString(strings.Replace(tc.hex, " ", "", -1))

		if !reflect.DeepEqual(actual[:], expected) {
			t.Errorf("Encode %d incorrect;\n  A: %#v\n  E: %#v", i, actual[:], expected)
		}
	}
}

func TestDecodeHeaders(t *testing.T) {
	for i, tc := range headerTests {
		data, _ := hex.DecodeString(strings.Replace(tc.hex, " ", "", -1))
		actual := unmarshalHeader(data)

		switch h := actual.(type) {
		case *dataHeader:
			if !reflect.DeepEqual(h, tc.hdr) {
				t.Errorf("Decode %d incorrect;\n  A: %#v\n  E: %#v", i, h, tc.hdr)
			}
		case *controlHeader:
			if !reflect.DeepEqual(h, tc.hdr) {
				t.Errorf("Decode %d incorrect;\n  A: %#v\n  E: %#v", i, h, tc.hdr)
			}
		default:
			t.Fatalf("Unknown type %T", h)
		}

	}
}
