// Copyright (C) 2014 Jakob Borg and Contributors (see the CONTRIBUTORS file).
// All rights reserved. Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst

import (
	"log"
	"os"
	"strings"
)

type WindowCC struct {
	minWindow     int
	maxWindow     int
	currentWindow int
	minRate       int
	maxRate       int
	currentRate   int

	curPPS int
	curRTT int // microseconds
}

var debugWindowCC = strings.Contains(os.Getenv("DSTDEBUG"), "windowcc")

func NewWindowCC() *WindowCC {
	return &WindowCC{
		minWindow:     8,
		maxWindow:     4096,
		currentWindow: 16,

		minRate:     100,
		maxRate:     200e3,
		currentRate: 100,
	}
}

func (w *WindowCC) Ack() {
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
		w.currentRate += w.maxRate / 1000
		changed = true
	}
	if w.curPPS > 0 && w.currentRate > w.curPPS*4/3 {
		w.currentRate = w.curPPS * 4 / 3
	}

	if changed && debugWindowCC {
		log.Println("ACK", w.currentWindow, w.currentRate)
	}
}

func (w *WindowCC) Exp() {
	w.currentWindow = w.minWindow
	if w.currentRate > w.minRate {
		w.currentRate = w.currentRate / 2
	}
	if debugWindowCC {
		log.Println("EXP", w.currentWindow, w.currentRate)
	}
}

func (w *WindowCC) SendWindow() int {
	return w.currentWindow
}

func (w *WindowCC) AckPacketIntv() int {
	return 1e6
}

func (w *WindowCC) PacketRate() int {
	return w.currentRate
}

func (w *WindowCC) RTTPPS(rtt, pps int) {
	w.curRTT = (w.curRTT*7 + rtt) / 8
	w.curPPS = (w.curPPS*7 + pps) / 8

	if w.curRTT > 100000 {
		w.maxRate = w.curPPS
		w.currentRate = w.curPPS
	}

	if debugWindowCC {
		log.Println("RTT PPS", w.curRTT, w.curPPS)
	}
}
