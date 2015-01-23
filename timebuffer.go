// Copyright 2014 The DST Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst

import (
	"sync"
	"time"
)

type timeBuffer struct {
	seq2Sent []timeBufValue
	next     int
	mut      sync.Mutex
}

type timeBufValue struct {
	seq  sequenceNo
	sent time.Time
	recv time.Time
}

func newTimeBuffer(size int) *timeBuffer {
	return &timeBuffer{
		seq2Sent: make([]timeBufValue, size),
	}
}

func (b *timeBuffer) Sent(seq sequenceNo) {
	now := time.Now()
	b.mut.Lock()
	b.seq2Sent[b.next] = timeBufValue{seq: seq, sent: now}
	b.next = (b.next + 1) % len(b.seq2Sent)
	b.mut.Unlock()
}

func (b *timeBuffer) Recv(seq sequenceNo) {
	now := time.Now()
	b.mut.Lock()
	for i := range b.seq2Sent {
		if b.seq2Sent[i].seq == seq {
			b.seq2Sent[i].recv = now
			b.mut.Unlock()
			return
		}
	}
	b.mut.Unlock()
}

func (b *timeBuffer) Average() (time.Duration, int) {
	var emptyTime time.Time
	var total time.Duration
	var n int

	b.mut.Lock()
	for _, v := range b.seq2Sent {
		if v.recv != emptyTime {
			total += v.recv.Sub(v.sent)
			n++
		}
	}
	b.mut.Unlock()

	if n == 0 {
		return 0, 0
	}

	return total / time.Duration(n), n
}
