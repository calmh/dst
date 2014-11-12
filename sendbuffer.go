// Copyright (C) 2014 Jakob Borg and Contributors (see the CONTRIBUTORS file).
// All rights reserved. Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst

import (
	"fmt"
	"log"
	"sync"

	"github.com/juju/ratelimit"
)

/*
	                          sendWindow
	                         v
		[S|S|S|S|Q|Q|Q|Q| | | | | | | | | ]
		         ^       ^writeSlot
		          sendSlot
*/
type sendBuffer struct {
	mux       *Mux              // we send packets here
	scheduler *ratelimit.Bucket // sets send rate for packets

	sendWindow int // maximum number of outstanding non-acked packets
	packetRate int // target pps

	buffer    []packet // buffered packets
	sendSlot  int      // buffer slot from which to send next packet
	writeSlot int      // buffer slot in which to write next packet

	lost         []packet // list of packets reported lost by timeout
	lostSendSlot int      // next lost packet to resend

	closed chan struct{}
	mut    sync.Mutex
	cond   *sync.Cond
}

const (
	schedulerRate     = 1e6
	schedulerCapacity = schedulerRate / 40
)

// newSendBuffer creates a new send buffer with a zero window.
// SetRateAndWindow() must be called to set an initial packet rate and send
// window before using.
func newSendBuffer(m *Mux) *sendBuffer {
	b := &sendBuffer{
		mux:       m,
		scheduler: ratelimit.NewBucketWithRate(schedulerRate, schedulerCapacity),
		closed:    make(chan struct{}),
	}
	b.cond = sync.NewCond(&b.mut)
	go b.writerLoop()
	return b
}

// Write puts a new packet in send buffer and schedules a send. Blocks when
// the window size is or would be exceeded.
func (b *sendBuffer) Write(pkt packet) {
	b.mut.Lock()
	for b.writeSlot == len(b.buffer) || b.writeSlot >= b.sendWindow {
		if debugConnection {
			log.Println(b, "Write blocked")
		}
		b.cond.Wait()
		// Connection may have closed while we were waiting
		select {
		case <-b.closed:
			b.mut.Unlock()
			return
		default:
		}
	}
	b.buffer[b.writeSlot] = pkt
	b.writeSlot++
	b.cond.Broadcast()
	b.mut.Unlock()
}

// Acknowledge removes packets with lower sequence numbers from the loss list
// or send buffer.
func (b *sendBuffer) Acknowledge(ack uint32) {
	b.mut.Lock()

	// Cut packets from the loss list if they have been acked

	cut := 0
	for i := range b.lost {
		if i >= b.lostSendSlot {
			break
		}
		if diff := b.lost[i].hdr.sequenceNo - ack; diff < 1<<30 {
			break
		}
		cut = i + 1
	}
	if cut > 0 {
		for _, pkt := range b.lost[:cut] {
			b.mux.buffers.Put(pkt.data)
		}
		b.lost = b.lost[cut:]
		b.lostSendSlot -= cut
		b.cond.Broadcast()
	}

	// Find the first packet that is still unacked, so we can cut
	// the packets ahead of it in the list.

	cut = 0
	for i := range b.buffer {
		if i >= b.sendSlot {
			break
		}
		if diff := b.buffer[i].hdr.sequenceNo - ack; diff < 1<<30 {
			break
		}
		cut = i + 1
	}

	// If something is to be cut, do so and notify any writers
	// that might be blocked.

	if cut > 0 {
		for _, pkt := range b.buffer[:cut] {
			b.mux.buffers.Put(pkt.data)
		}

		copy(b.buffer, b.buffer[cut:])
		b.sendSlot -= cut
		b.writeSlot -= cut

		b.cond.Broadcast()
	}
	b.mut.Unlock()
}

// ScheduleResend arranges for a resend of all currently unacknowledged
// packets, up to the current window size. Returns true if at least one packet
// was scheduled for resend, otherwise false.
func (b *sendBuffer) ScheduleResend() (resent bool) {
	b.mut.Lock()

	if b.sendSlot > 0 {
		// There are packets that have been sent but not acked. Move them from
		// the send buffer to the loss list for retransmission.
		resent = true
		if debugConnection {
			log.Println(b, "resend from buffer", b.sendSlot)
		}

		// Append the packets to the loss list
		b.lost = append(b.lost, b.buffer[:b.sendSlot]...)

		// Rewind the send buffer
		copy(b.buffer, b.buffer[b.sendSlot:])
		b.writeSlot -= b.sendSlot
		b.sendSlot = 0
	}

	if b.lostSendSlot > 0 {
		// Also resend whatever was already in the loss list
		resent = true
		if debugConnection {
			log.Println(b, "resend from loss list", b.lostSendSlot)
		}
		b.lostSendSlot = 0
	}

	if resent {
		b.cond.Broadcast()
	}

	b.mut.Unlock()
	return
}

// SetWindowAndRate sets the window size (in packets) and packet rate (in
// packets per second) to use when sending.
func (b *sendBuffer) SetWindowAndRate(sendWindow, packetRate int) {
	b.mut.Lock()
	b.packetRate = packetRate
	b.sendWindow = sendWindow
	if b.sendWindow > len(b.buffer) {
		if b.sendWindow <= cap(b.buffer) {
			b.buffer = b.buffer[:b.sendWindow]
		} else {
			sb := make([]packet, b.sendWindow)
			copy(sb, b.buffer)
			b.buffer = sb
		}
		b.cond.Broadcast()
	}
	b.mut.Unlock()
}

// Stop stops the send buffer from any doing further sending.
func (b *sendBuffer) Stop() {
	close(b.closed)
}

func (b *sendBuffer) String() string {
	return fmt.Sprintf("sendBuffer@%p", b)
}

func (b *sendBuffer) writerLoop() {
	if debugConnection {
		log.Println(b, "writer() starting")
		defer log.Println(b, "writer() exiting")
	}

	b.scheduler.Take(schedulerCapacity)
	for {
		var pkt packet
		b.mut.Lock()
		for b.lostSendSlot >= b.sendWindow ||
			(b.sendSlot == b.writeSlot && b.lostSendSlot == len(b.lost)) {
			select {
			case <-b.closed:
				return
			default:
			}

			if debugConnection {
				log.Println(b, "writer() paused", b.lostSendSlot, b.sendSlot, b.sendWindow, len(b.lost))
			}
			b.cond.Wait()
		}

		if b.lostSendSlot < len(b.lost) {
			pkt = b.lost[b.lostSendSlot]
			pkt.hdr.timestamp = timestampMicros()
			b.lostSendSlot++
		} else if b.sendSlot < b.writeSlot {
			pkt = b.buffer[b.sendSlot]
			pkt.hdr.timestamp = timestampMicros()
			b.sendSlot++
		}

		b.cond.Broadcast()
		packetRate := b.packetRate
		b.mut.Unlock()

		if pkt.dst != nil {
			b.scheduler.Wait(schedulerRate / int64(packetRate))
			b.mux.write(pkt)
		}
	}
}
