// Copyright 2014 The DST Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst

import (
	"log"
	"time"
)

type windowCC struct {
	minWindow     int
	maxWindow     int
	currentWindow int
	minRate       int
	maxRate       int
	currentRate   int

	curRTT time.Duration
	minRTT time.Duration
}

func newWindowCC() *windowCC {
	return &windowCC{
		minWindow:     2,
		maxWindow:     4096,
		currentWindow: 16,

		minRate:     100,
		maxRate:     200e3,
		currentRate: 100,

		minRTT: 10 * time.Second,
	}
}

func (w *windowCC) Ack() {
	changed := false

	if w.curRTT > 100000 {
		return
	}

	if w.currentWindow < w.maxWindow/2 {
		w.currentWindow = w.currentWindow * 3 / 2
		changed = true
	} else if w.currentWindow < w.maxWindow {
		w.currentWindow += w.minWindow
		changed = true
	}

	if w.currentRate < w.maxRate/2 {
		w.currentRate = w.currentRate * 3 / 2
		changed = true
	} else if w.currentRate < w.maxRate {
		w.currentRate += w.minRate
		changed = true
	}

	if changed && debugCC {
		log.Println("Ack", w.currentWindow, w.currentRate)
	}
}

func (w *windowCC) NegAck() {
	if w.currentWindow > w.minWindow {
		w.currentWindow /= 2
	}
	if w.currentRate > w.minRate {
		w.currentRate /= 2
	}
	if debugCC {
		log.Println("NegAck", w.currentWindow, w.currentRate)
	}
}

func (w *windowCC) Exp() {
	w.currentWindow = w.minWindow
	if w.currentRate > w.minRate {
		w.currentRate = w.currentRate / 2
	}
	if debugCC {
		log.Println("Exp", w.currentWindow, w.currentRate)
	}
}

func (w *windowCC) SendWindow() int {
	if w.currentWindow < w.minWindow {
		return w.minWindow
	}
	if w.currentWindow > w.maxWindow {
		return w.maxWindow
	}
	return w.currentWindow
}

func (w *windowCC) PacketRate() int {
	if w.currentRate < w.minRate {
		return w.minRate
	}
	if w.currentRate > w.maxRate {
		return w.maxRate
	}
	return w.currentRate
}

func (w *windowCC) UpdateRTT(rtt time.Duration) {
	w.curRTT = rtt
	if w.curRTT < w.minRTT {
		w.minRTT = w.curRTT
		if debugCC {
			log.Println("Min RTT", w.minRTT)
		}
	}

	if w.curRTT > w.minRTT+100*time.Millisecond {
		// RTT increased 100ms over minimum
		w.currentRate = w.currentRate * 7 / 8
		w.maxRate = w.currentRate * 3 / 2
		w.minRTT = w.curRTT
		if debugCC {
			log.Println("Nailing rate", w.currentRate, w.maxRate)
		}
	}

	if debugCC {
		log.Println("RTT", w.curRTT)
	}
}
