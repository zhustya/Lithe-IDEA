package editorview

import (
	"time"

	"github.com/vivaui/viva/gfx"
)

// A navigation target flashes: for a moment the line the caret arrived on is tinted across the
// gutter and the text, then the tint fades out. A search result, a definition or a usage is
// usually somewhere the reader was not looking, so the caret alone (a thin line, possibly at the
// edge of the view) does not say where the view went. The flash is view-local animation state:
// it allocates nothing per frame and asks only for the frames of its fade.

const (
	// flashPeriod is how long the tint lasts, from full strength to none.
	flashPeriod = 900 * time.Millisecond
	// flashFrame paces the fade's frames: fine enough for the eye, and far fewer than painting
	// the whole period at the display's rate.
	flashFrame = 16 * time.Millisecond
	// flashAlpha is the tint's opacity when it appears. It is multiplied by the theme caret
	// color, so it reads as a soft accent band in both themes.
	flashAlpha = 0.28
	// flashFadeFloor is the opacity below which the flash ends instead of drawing an invisible
	// band and asking for yet another frame.
	flashFadeFloor = 0.008
)

// lineFlash is the line a navigation left the caret on, and when its fade started. from is -1
// until the first paint after FlashLine takes the frame clock as the reference.
type lineFlash struct {
	active bool
	line   int
	from   time.Duration
}

// FlashLine tints a line for a moment, fading out, as the arrival of a navigation target. The
// next paint asks for the frames the fade needs, so a view that starts one keeps animating until
// it ends. A line past the text is ignored.
func (v *View) FlashLine(line int) {
	if line < 0 || line >= v.text.LineCount() {
		return
	}
	v.flash = lineFlash{active: true, line: line, from: -1}
}

// paintFlash draws the fade of a navigation flash over the rows of its line. It is painted after
// the line highlights and before the text, so the line reads as the other line bands do.
func (v *View) paintFlash(canvas *gfx.Canvas, look Look, firstLine, lastLine int) {
	f := &v.flash
	if !f.active {
		return
	}
	if f.line < 0 || f.line >= v.text.LineCount() {
		f.active = false
		return
	}
	if f.from < 0 || f.from > v.clock {
		f.from = v.clock
	}
	elapsed := v.clock - f.from
	if elapsed >= flashPeriod {
		f.active = false
		return
	}
	progress := float32(elapsed) / float32(flashPeriod)
	color := look.Caret
	if color.Alpha == 0 {
		color = look.Foreground
	}
	// The theme color's own alpha is not the band's: the flash owns how strong it is.
	color.Alpha = flashAlpha * (1 - progress)
	if color.Alpha <= flashFadeFloor {
		f.active = false
		return
	}
	if f.line >= firstLine && f.line <= lastLine {
		top := v.LineTop(f.line)
		height := float32(len(v.rowsOf(v.ref(f.line)))) * v.lineH
		canvas.Clip(v.area)
		canvas.Fill(gfx.Rect{X: v.area.X, Y: top, Width: v.textRight - v.area.X, Height: max(height, v.lineH)}, color)
		canvas.Unclip()
	}
	canvas.RequestFrameIn(flashFrame)
}
