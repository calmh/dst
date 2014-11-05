package miniudt

type WindowCC struct {
	initialWindow int
	currentWindow int
}

func NewWindowCC(initial int) *WindowCC {
	return &WindowCC{
		initialWindow: initial,
		currentWindow: initial,
	}
}

func (w *WindowCC) Ack() {
	if w.currentWindow < w.initialWindow*32 {
		w.currentWindow += w.initialWindow
	}
}

func (w *WindowCC) Exp() {
	if w.currentWindow > 2 {
		w.currentWindow /= 2
	}
}

func (w *WindowCC) SendWindow() int {
	return w.currentWindow
}

func (w *WindowCC) AckPacketIntv() int {
	return 1e6
}
