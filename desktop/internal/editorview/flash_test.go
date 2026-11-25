package editorview

import (
	"testing"
	"time"

	"github.com/vivaui/viva/gfx"
)

// A navigation flash lasts about a second, fades, and asks for the frames it needs so it ends
// even when nothing else changes the view.
func TestFlashLineFadesOutAndAsksForItsFrames(t *testing.T) {
	v := newTestView(t, "one\ntwo\nthree\nfour\n")
	canvas := new(gfx.Canvas)
	now := time.Duration(0)
	canvas.SetClock(func() time.Duration { return now })
	area := gfx.Rect{X: 0, Y: 0, Width: 400, Height: 200}
	paint := func() (time.Duration, bool) {
		canvas.Reset()
		canvas.BeginFrame()
		v.Paint(canvas, area, Look{})
		canvas.EndFrame()
		return canvas.NextFrame()
	}
	paint()

	v.FlashLine(2)
	if delay, ok := paint(); !ok || delay != flashFrame {
		t.Fatalf("flash asked for the next frame in %v (ok %v), want %v", delay, ok, flashFrame)
	}
	if !v.flash.active || v.flash.line != 2 {
		t.Fatalf("flash is %+v after FlashLine(2)", v.flash)
	}

	now += flashPeriod / 2
	if _, ok := paint(); !ok {
		t.Fatal("flash stopped asking for frames halfway through its fade")
	}
	if !v.flash.active {
		t.Fatal("flash ended halfway through its fade")
	}

	now += flashPeriod
	if _, ok := paint(); ok {
		t.Fatal("a finished flash kept asking for frames")
	}
	if v.flash.active {
		t.Fatal("flash did not end after its period")
	}
}

func TestFlashLineIgnoresALineOutsideTheText(t *testing.T) {
	v := newTestView(t, "one\n")
	v.FlashLine(-1)
	if v.flash.active {
		t.Fatalf("flash is %+v for a negative line", v.flash)
	}
	v.FlashLine(2)
	if v.flash.active {
		t.Fatalf("flash is %+v for a line past the text", v.flash)
	}
	v.FlashLine(1)
	if !v.flash.active || v.flash.line != 1 {
		t.Fatalf("flash is %+v for the empty line after the text", v.flash)
	}
}
