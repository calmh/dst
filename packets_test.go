// Copyright 2014 The DST Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst

import (
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
)

var headerTests = []struct {
	hex string
	hdr header
}{
	{
		"00000000 00000000 00000000",
		header{},
	},
	{
		"10123456 A0008901 98765432",
		header{
			packetType: typeData,
			flags:      0,
			connID:     0x123456,
			sequenceNo: 0xA0008901,
			timestamp:  0x98765432,
		},
	},
	{
		"24340000 22334455 55667788",
		header{
			packetType: typeAck,
			flags:      flagCookie,
			connID:     0x340000,
			sequenceNo: 0x22334455,
			timestamp:  0x55667788,
		},
	},
}

func TestEncodeHeaders(t *testing.T) {
	for i, tc := range headerTests {
		var actual [dstHeaderLen]byte
		tc.hdr.marshal(actual[:])
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

		if !reflect.DeepEqual(actual, tc.hdr) {
			t.Errorf("Decode %d incorrect;\n  A: %#v\n  E: %#v", i, actual, tc.hdr)
		}
	}
}

func TestComparison(t *testing.T) {
	pkts := []packet{
		packet{hdr: header{sequenceNo: 0}},
		packet{hdr: header{sequenceNo: 42}},
		packet{hdr: header{sequenceNo: 96}},
		packet{hdr: header{sequenceNo: 2<<30 - 1}},
		packet{hdr: header{sequenceNo: 2 << 30}},
		packet{hdr: header{sequenceNo: 2<<30 + 1}},
		packet{hdr: header{sequenceNo: 2<<31 - 2}},
		packet{hdr: header{sequenceNo: 2<<31 - 1}},
		packet{hdr: header{sequenceNo: 0}},
		packet{hdr: header{sequenceNo: 42}},
	}
	for i := range pkts {
		if i > 0 {
			if !pkts[i-1].Less(pkts[i]) {
				t.Error(pkts[i-1], "not <", pkts[i])
			}
			if pkts[i].Less(pkts[i-1]) {
				t.Error(pkts[i], "<", pkts[i-1])
			}
			if pkts[i].Less(pkts[i]) {
				t.Error(pkts[i], "<", pkts[i-1])
			}
		}
	}
}
