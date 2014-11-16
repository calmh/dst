// Copyright 2014 The DST Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst

import (
	"reflect"
	"testing"
)

func TestInsertSorted(t *testing.T) {
	var l packetList
	l.Resize(128)

	pkt40 := packet{hdr: header{sequenceNo: 40}}
	pkt42 := packet{hdr: header{sequenceNo: 42}}
	pkt44 := packet{hdr: header{sequenceNo: 44}}
	pkt46 := packet{hdr: header{sequenceNo: 46}}

	// Insert first

	l.InsertSorted(pkt42)
	expected := []packet{pkt42}

	if !reflect.DeepEqual(l.All(), expected) {
		t.Errorf("Mismatch; %v != %v", l.All(), expected)
	}

	// Insert in front

	l.InsertSorted(pkt40)
	expected = []packet{pkt40, pkt42}

	if !reflect.DeepEqual(l.All(), expected) {
		t.Errorf("Mismatch; %v != %v", l.All(), expected)
	}

	// Insert at end

	l.InsertSorted(pkt46)
	expected = []packet{pkt40, pkt42, pkt46}

	if !reflect.DeepEqual(l.All(), expected) {
		t.Errorf("Mismatch; %v != %v", l.All(), expected)
	}

	// Insert in middle

	l.InsertSorted(pkt44)
	expected = []packet{pkt40, pkt42, pkt44, pkt46}

	if !reflect.DeepEqual(l.All(), expected) {
		t.Errorf("Mismatch; %v != %v", l.All(), expected)
	}

	// Insert duplicate

	l.InsertSorted(pkt44)
	expected = []packet{pkt40, pkt42, pkt44, pkt46}

	if !reflect.DeepEqual(l.All(), expected) {
		t.Errorf("Mismatch; %v != %v", l.All(), expected)
	}
}

func TestInsertSortedFull(t *testing.T) {
	var l packetList
	l.Resize(3)

	pkt38 := packet{hdr: header{sequenceNo: 38}}
	pkt40 := packet{hdr: header{sequenceNo: 40}}
	pkt42 := packet{hdr: header{sequenceNo: 42}}
	pkt44 := packet{hdr: header{sequenceNo: 44}}
	pkt46 := packet{hdr: header{sequenceNo: 46}}

	// Fill buffer

	l.InsertSorted(pkt40)
	l.InsertSorted(pkt42)
	l.InsertSorted(pkt44)
	expected := []packet{pkt40, pkt42, pkt44}

	if !reflect.DeepEqual(l.All(), expected) {
		t.Errorf("Mismatch; %v != %v", l.All(), expected)
	}

	// Insert a higher sequence number, ignored

	l.InsertSorted(pkt46)
	expected = []packet{pkt40, pkt42, pkt44}

	if !reflect.DeepEqual(l.All(), expected) {
		t.Errorf("Mismatch; %v != %v", l.All(), expected)
	}

	// Insert a lower sequence number, pushed at front

	l.InsertSorted(pkt38)
	expected = []packet{pkt38, pkt40, pkt42}

	if !reflect.DeepEqual(l.All(), expected) {
		t.Errorf("Mismatch; %v != %v", l.All(), expected)
	}
}
