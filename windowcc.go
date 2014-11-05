package mdstp

type WindowCC struct {
	minWindow     int
	maxWindow     int
	currentWindow int
}

func NewWindowCC() *WindowCC {
	return &WindowCC{
		minWindow:     16,
		maxWindow:     4096,
		currentWindow: 16,
	}
}

func (w *WindowCC) Ack() {
	if w.currentWindow < w.maxWindow/2 {
		w.currentWindow = w.currentWindow * 3 / 2
	}
	if w.currentWindow < w.maxWindow {
		w.currentWindow += w.minWindow
	}
}

func (w *WindowCC) Exp() {
	w.currentWindow = w.minWindow
}

func (w *WindowCC) SendWindow() int {
	return w.currentWindow
}

func (w *WindowCC) AckPacketIntv() int {
	return 1e6
}
