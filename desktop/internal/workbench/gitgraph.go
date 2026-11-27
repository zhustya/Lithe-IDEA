package workbench

import (
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/1lck/Lithe-IDEA/desktop/internal/core"
	"github.com/vivaui/viva/gfx"
)

// This file paints the rows of the commit list: the graph, the subject, the author, the
// date and the reference labels. The rows themselves (their hit, selection, hover and
// current branch backgrounds) are list rows declared by gitlog_view.go; what is drawn in them
// is painted here by the row's draw callback, into the rectangle the row was laid out at,
// because the graph is vector geometry no ui box can show.
//
// The measurements are those of the macOS log, which scales the 22 point grid of the
// IntelliJ log to its 26 point rows.

const (
	logRowHeight = 26
	logFontSize  = 13
	logMetaSize  = 12
	logRefSize   = 12
	// logAuthorWidth is the author column of the Worktrees history.
	logAuthorWidth = 104
	logDatePad     = 8
	logDateFormat  = "2006/1/2 15:04"
	logDateSample  = "0000/00/00 00:00"
	// logRootStripWidth is the strip at a row's left edge in the color of its repository, in a
	// folder with several; the first lane's node starts past it.
	logRootStripWidth = 3
)

// The logical grid of the graph, as SourceGit lays it out: a lane every 12 points with a
// dot of 6 points, a head or merge dot of 12 and a 2 point line.
const (
	graphLaneSpacing = 12
	graphLaneCenter  = 10
	graphDotRadius   = 3
	graphHeadRadius  = 6
)

// graphMetrics is the graph's grid aligned to device pixels, as the macOS painter
// aligns it: the centers of rows and lanes and the widths of lines and nodes fall on
// whole pixels, the ones that must be odd on odd pixels.
type graphMetrics struct {
	rowCenter, laneSpacing, laneCenter, lineWidth, dotRadius, headRadius float32
}

func newGraphMetrics(scale float32) graphMetrics {
	if scale <= 0 || math.IsInf(float64(scale), 0) || math.IsNaN(float64(scale)) {
		scale = 1
	}
	align := func(v float32, odd bool) float32 {
		px := float32(math.Floor(float64(v * scale)))
		if odd && int(px)%2 == 0 {
			px--
		}
		return px / scale
	}
	return graphMetrics{
		rowCenter:   align(logRowHeight/2, true),
		laneSpacing: align(graphLaneSpacing, false),
		laneCenter:  align(graphLaneCenter, false),
		lineWidth:   align(2, false),
		dotRadius:   align(graphDotRadius, false),
		headRadius:  align(graphHeadRadius, false),
	}
}

// graphSegment is one print element's path in the row's coordinates: the straight part of a
// lane that keeps its column, or one half of the smooth curve between this row's center and
// the lane of the adjacent row. The other half is the adjacent row's element, and the two
// meet with the same tangent at the row's edge, so a branch never shows a sharp corner there.
// Arrows and the caps of a missing parent stay on a straight segment to the edge, where their
// heads are drawn.
type graphSegment struct {
	x0, y0, x1, y1     float32
	cx1, cy1, cx2, cy2 float32
	curved             bool
}

// segment is the path of element e, from the center of this row toward the row above or
// below. A lane that changes column follows the half of a cubic whose control points keep
// the curve vertical at both commit centers, as SourceGit's CommitGraph draws its paths.
func (m graphMetrics) segment(e core.GraphElement) graphSegment {
	pos := m.laneCenter + float32(e.Position)*m.laneSpacing
	adj := m.laneCenter + float32(e.AdjacentPosition)*m.laneSpacing
	s := graphSegment{x0: pos, y0: m.rowCenter}
	up := e.Direction == "up"
	if e.Position == e.AdjacentPosition || e.IsTerminal || e.HasArrow {
		switch {
		case e.IsTerminal:
			gap := m.dotRadius/2 + 1
			if up {
				s.y1 = gap
			} else {
				s.y1 = logRowHeight - gap
			}
		case up:
			s.y1 = 0
		default:
			s.y1 = logRowHeight
		}
		s.x1 = (pos + adj) / 2
		return s
	}
	half := float32(logRowHeight) / 2
	if up {
		s.x0, s.y0 = (pos+adj)/2, m.rowCenter-half
		s.cx1, s.cy1 = (adj+3*pos)/4, m.rowCenter-half+float32(logRowHeight)/8
		s.cx2, s.cy2 = pos, m.rowCenter-half+float32(logRowHeight)/4
		s.x1, s.y1 = pos, m.rowCenter
	} else {
		s.x1, s.y1 = (pos+adj)/2, m.rowCenter+half
		s.cx1, s.cy1 = pos, m.rowCenter+float32(logRowHeight)/4
		s.cx2, s.cy2 = (3*pos+adj)/4, m.rowCenter+3*float32(logRowHeight)/8
	}
	s.curved = true
	return s
}

// titleOffset is where the subject of a row starts: the room the row's lanes take, as
// SourceGit measures it, plus the inset its own graph leaves before the text. It covers the
// lane a half-edge reaches in the neighbouring row too, so a branch that leaves or enters
// this row never draws over the text.
func titleOffset(row core.GraphRow) float32 {
	last := float32(row.Lane)
	for _, e := range row.PrintElements {
		last = max(last, float32(e.Position), float32(e.AdjacentPosition))
	}
	lanes := max(last+1, 1)
	return float32(math.Floor(float64(lanes*graphLaneSpacing))) + 6
}

// referenceColor is the color of a reference label's icon by kind.
func referenceColor(kind string, dark bool) gfx.Color {
	switch kind {
	case "head":
		return pick(dark, 0xF5D273, 0xFFAF0F)
	case "branch":
		return pick(dark, 0x5FAD65, 0x369650)
	case "remote":
		return pick(dark, 0xB589EC, 0x834DF0)
	default:
		return pick(dark, 0x868A91, 0x6C707E)
	}
}

func pick(dark bool, darkRGB, lightRGB uint32) gfx.Color {
	if dark {
		return gfx.RGB24(darkRGB)
	}
	return gfx.RGB24(lightRGB)
}

// mergeForeground is the text color of a merge commit's row.
func mergeForeground(dark bool) gfx.Color {
	if dark {
		return gfx.RGB(111, 115, 122)
	}
	return gfx.RGB(129, 133, 148)
}

// referenceGroup is what a row's reference labels show: one title, the icons of the
// kinds it holds (at most two of a kind) and every label for the detail pane.
type referenceGroup struct {
	title  string
	labels []core.GraphLabel
	kinds  []string
}

// groupReferences orders a row's labels and picks the title as the macOS log does:
// the checked out branch, else a local branch with the remote branch it tracks, shown
// as "remote & branch", else the first label that is not a tag.
func groupReferences(labels []core.GraphLabel, current string, upstream map[string]string) referenceGroup {
	rank := func(l core.GraphLabel) int {
		switch l.Kind {
		case "head":
			return 0
		case "branch":
			switch {
			case l.Title == current:
				return 1
			case l.Title == "main" || l.Title == "master":
				return 2
			}
			return 4
		case "remote":
			if l.Title == "origin/main" || l.Title == "origin/master" {
				return 3
			}
			return 5
		}
		return 6
	}
	sorted := slices.Clone(labels)
	slices.SortStableFunc(sorted, func(x, y core.GraphLabel) int {
		if rx, ry := rank(x), rank(y); rx != ry {
			return rx - ry
		}
		return strings.Compare(strings.ToLower(x.Title), strings.ToLower(y.Title))
	})
	remotes := map[string]core.GraphLabel{}
	byBranch := map[string]core.GraphLabel{}
	for _, l := range sorted {
		if l.Kind != "remote" {
			continue
		}
		if _, ok := remotes[l.Title]; !ok {
			remotes[l.Title] = l
		}
		name := l.Title
		if i := strings.IndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		}
		if _, ok := byBranch[name]; !ok {
			byBranch[name] = l
		}
	}
	paired := map[string]bool{}
	tracked := ""
	for _, l := range sorted {
		if l.Kind != "branch" {
			continue
		}
		remote, ok := remotes[upstream[l.Title]]
		if !ok {
			remote, ok = byBranch[l.Title]
		}
		if !ok {
			continue
		}
		if tracked == "" {
			name, _, _ := strings.Cut(remote.Title, "/")
			tracked = name + " & " + l.Title
		}
		paired[l.ID], paired[remote.ID] = true, true
	}
	g := referenceGroup{labels: sorted}
	var first *core.GraphLabel
	for i, l := range sorted {
		if l.Kind == "head" || paired[l.ID] {
			continue
		}
		if l.Kind == "branch" && l.Title == current {
			g.title = l.Title
			first = nil
			break
		}
		if first == nil {
			first = &sorted[i]
		}
	}
	switch {
	case g.title != "":
	case tracked != "":
		g.title = tracked
	case first != nil:
		if first.Kind != "tag" {
			g.title = first.Title
		}
	case slices.ContainsFunc(sorted, func(l core.GraphLabel) bool { return l.Kind == "head" }):
		g.title = "HEAD"
	}
	for _, l := range sorted {
		n := 0
		for _, k := range g.kinds {
			if k == l.Kind {
				n++
			}
		}
		if n < 2 {
			g.kinds = append(g.kinds, l.Kind)
		}
	}
	return g
}

// iconWidth is the width of the group's overlapping icons at a height.
func (g referenceGroup) iconWidth(h float32) float32 {
	if len(g.labels) == 0 {
		return 0
	}
	return float32(math.Round(float64(h + float32(len(g.kinds)-1)*h/6.25*2)))
}

// shortenTitle keeps at least 22 characters of a title, shortening a long one first by
// its leading folder and then from the end.
func shortenTitle(canvas *gfx.Canvas, title string, available, size float32) string {
	if utf8.RuneCountInString(title) <= 22 || canvas.TextWidth(title, size) <= available {
		return title
	}
	result := title
	if i := strings.IndexByte(result, '/'); i > 2 {
		result = ".." + result[i:]
	}
	if available > 0 && canvas.TextWidth(result, size) <= available {
		return result
	}
	runes := []rune(strings.TrimSuffix(result, "…"))
	for len(runes) > 22 {
		runes = runes[:len(runes)-1]
		result = string(runes) + "…"
		if canvas.TextWidth(result, size) <= available {
			return result
		}
	}
	return result
}

// tagIcon traces the reference label icon, a tag, in a size×size box.
func tagIcon(b *gfx.PathBuilder, size float32) gfx.Path {
	s := size / 16
	b.MoveTo(13.4495*s, 7.5001*s)
	b.LineTo(7.01028*s, 13.9394*s)
	b.CubicTo(6.81502*s, 14.1347*s, 6.49843*s, 14.1347*s, 6.30317*s, 13.9394*s)
	b.LineTo(2.06053*s, 9.69679*s)
	b.CubicTo(1.86527*s, 9.50152*s, 1.86527*s, 9.18494*s, 2.06053*s, 8.98968*s)
	b.LineTo(8.49978*s, 2.55035*s)
	b.CubicTo(8.61106*s, 2.43907*s, 8.76824*s, 2.38667*s, 8.92404*s, 2.40893*s)
	b.LineTo(12.6363*s, 2.93926*s)
	b.CubicTo(12.8563*s, 2.97069*s, 13.0292*s, 3.14354*s, 13.0606*s, 3.36352*s)
	b.LineTo(13.5909*s, 7.07584*s)
	b.CubicTo(13.6132*s, 7.23163*s, 13.5608*s, 7.38882*s, 13.4495*s, 7.5001*s)
	b.Close()
	return b.Path(size, size)
}

// paintLogRow paints row index of the commit list into rect, the rectangle its element was
// laid out at, as SourceGit's Histories row does: the graph, one label per reference, the
// subject, the author with a picture, the short hash and the author time. A row that is not
// on the checked out branch is drawn at 65 percent, and the head's row is bold.
func (a *App) paintLogRow(canvas *gfx.Canvas, rect gfx.Rect, index int) {
	v := &a.logView
	if a.project == nil || rect.Width <= 0 || rect.Height <= 0 {
		return
	}
	l := &a.project.log
	if index >= len(l.graph.Rows) {
		return
	}
	color := a.logView.lanes.of(l.graph.Rows, l.projGen)
	styles := a.gitListPalette()
	m := newGraphMetrics(canvas.PixelScale())
	canvas.Clip(rect)
	defer canvas.Unclip()
	row := l.graph.Rows[index]
	commit, _ := l.commit(row.CommitHash)
	head := rowHasHead(row.Labels)
	a.paintGraphRow(canvas, m, rect, row, color, styles, head, len(commit.ParentHashes) > 1)
	if slot, ok := a.project.repositorySlot(a.project.logCommitRepo(row.CommitHash)); ok && a.project.workspaceMode() {
		// As IDEA marks each commit with the color of its root, the strip at the left edge says which
		// repository of the folder the commit belongs to, also while the log walks several at once.
		canvas.Fill(gfx.Rect{X: rect.X, Y: rect.Y, Width: logRootStripWidth, Height: rect.Height}, repositoryColor(slot, a.colors.Dark))
	}

	// SourceGit dims a commit that is not merged into the checked out branch, and bolds the
	// head's row; a selected row keeps the same text.
	dim := float32(1)
	if len(l.currentRows) > 0 && !l.currentRows[row.CommitHash] {
		dim = 0.65
	}
	text := styles.text.WithAlpha(dim)
	weight := float32(gfx.WeightRegular)
	if head {
		weight = gfx.WeightBold
	}
	cols := logColumnsFor(rect.Width)
	x := rect.X + titleOffset(row)
	subjectRight := rect.X + cols.subjectRight
	if l.decorations && len(row.Labels) > 0 {
		// SourceGit gives the reference labels the row's room before the subject: a branch or
		// tag name is only shortened when the whole column cannot hold it, and the subject is
		// what gives way first.
		x = a.paintLogRefs(canvas, rect, x, subjectRight, row.Labels, l.current, color(row.ColorID), styles, dim)
	}
	subjectEnd := rect.X + cols.subjectRight
	a.paintLogText(canvas, commit.Subject, gfx.Rect{X: x, Y: rect.Y, Width: max(0, subjectEnd-x), Height: rect.Height}, logFontSize, weight, false, text, gfx.AlignStart)
	if cols.author {
		authorEnd := rect.Width
		if cols.date {
			authorEnd = cols.dateX
		}
		if cols.sha {
			authorEnd = cols.shaX
		}
		avatar := gfx.Rect{X: rect.X + cols.authorX + 4, Y: rect.Y + (rect.Height-logAvatarSize)/2, Width: logAvatarSize, Height: logAvatarSize}
		a.paintAvatar(canvas, avatar, commit.AuthorName, dim)
		a.paintLogText(canvas, commit.AuthorName, gfx.Rect{X: avatar.X + logAvatarSize + 4, Y: rect.Y, Width: max(0, rect.X+authorEnd-logColumnGap-(avatar.X+logAvatarSize+4)), Height: rect.Height}, logMetaSize, weight, false, text, gfx.AlignStart)
	}
	if cols.sha {
		a.paintLogText(canvas, commit.ShortHash, gfx.Rect{X: rect.X + cols.shaX + logColumnGap, Y: rect.Y, Width: logShaColumn - 2*logColumnGap, Height: rect.Height}, logMetaSize, weight, true, text, gfx.AlignStart)
	}
	if cols.date {
		a.paintLogText(canvas, v.date(commit), gfx.Rect{X: rect.X + cols.dateX + logColumnGap, Y: rect.Y, Width: logDateColumn - 2*logColumnGap, Height: rect.Height}, logMetaSize, weight, false, text, gfx.AlignStart)
	}
}

// paintLogText paints one column of a commit row cut to its rectangle with an ellipsis.
func (a *App) paintLogText(canvas *gfx.Canvas, text string, rect gfx.Rect, size, weight float32, mono bool, color gfx.Color, align gfx.Alignment) {
	if rect.Width <= 0 || text == "" {
		return
	}
	line := canvas.FitLine(text, size, weight, rect.Width)
	if line == "" {
		return
	}
	canvas.LabelAligned(line, rect, gfx.TextStyle{Size: size, Weight: weight, Mono: mono, Color: color}, align)
	if line != text {
		canvas.HoverHint(0, rect, text)
	}
}

// dateColumn is the width of the date column in the canvas's fonts, measured again when they
// change.
func (v *logView) dateColumn(canvas *gfx.Canvas) float32 {
	if fonts := canvas.FontGeneration(); v.dateWidth == 0 || v.dateFonts != fonts {
		v.dateWidth, v.dateFonts = canvas.TextWidth(logDateSample, logMetaSize), fonts
	}
	return v.dateWidth
}

// paintGraphRow strokes the edges of one row and draws its dot as SourceGit does: a normal
// commit is a small ring, the head a large ring with a filled center, and a merge a large
// filled dot crossed with the contents color.
func (a *App) paintGraphRow(canvas *gfx.Canvas, m graphMetrics, rect gfx.Rect, row core.GraphRow, color func(int32) gfx.Color, styles gitListColors, head, merge bool) {
	width := titleOffset(row)
	box := gfx.Rect{X: rect.X, Y: rect.Y, Width: width, Height: logRowHeight}
	for _, e := range row.PrintElements {
		s := m.segment(e)
		b := canvas.PathBuilder()
		if e.IsDotted && !e.HasArrow {
			if s.curved {
				dashCurve(b, s)
			} else {
				dashLine(b, s.x0, s.y0, s.x1, s.y1)
			}
		} else {
			b.MoveTo(s.x0, s.y0)
			if s.curved {
				b.CubicTo(s.cx1, s.cy1, s.cx2, s.cy2, s.x1, s.y1)
			} else {
				b.LineTo(s.x1, s.y1)
			}
		}
		if e.HasArrow {
			length := max(1, float32(math.Hypot(float64(s.x1-s.x0), float64(s.y1-s.y0))))
			vx := (s.x0 - s.x1) / length * logRowHeight * 0.3
			vy := (s.y0 - s.y1) / length * logRowHeight * 0.3
			for _, sign := range [2]float32{-1, 1} {
				b.MoveTo(s.x1, s.y1)
				b.LineTo(s.x1+vx*sqrt07-sign*vy*sqrt03, s.y1+sign*vx*sqrt03+vy*sqrt07)
			}
		}
		canvas.DrawPath(b.Path(width, logRowHeight), box, gfx.PathStyle{
			Stroke: color(e.ColorID), StrokeWidth: m.lineWidth, Cap: gfx.CapRound, Join: gfx.JoinRound,
		})
	}
	centerX := rect.X + m.laneCenter + float32(row.Lane)*m.laneSpacing
	centerY := rect.Y + m.rowCenter
	lane := color(row.ColorID)
	dot := func(radius float32) gfx.Rect {
		return gfx.Rect{X: centerX - radius, Y: centerY - radius, Width: 2 * radius, Height: 2 * radius}
	}
	switch {
	case head:
		outer := dot(m.headRadius)
		canvas.Rounded(outer, m.headRadius, styles.contents)
		canvas.RoundedOutline(outer, m.headRadius, m.lineWidth, lane)
		canvas.Rounded(dot(m.dotRadius), m.dotRadius, lane)
	case merge:
		canvas.Rounded(dot(m.headRadius), m.headRadius, lane)
		cross := m.dotRadius
		canvas.Fill(gfx.Rect{X: centerX - cross, Y: centerY - m.lineWidth/2, Width: 2 * cross, Height: m.lineWidth}, styles.contents)
		canvas.Fill(gfx.Rect{X: centerX - m.lineWidth/2, Y: centerY - cross, Width: m.lineWidth, Height: 2 * cross}, styles.contents)
	default:
		inner := dot(m.dotRadius)
		canvas.Rounded(inner, m.dotRadius, styles.contents)
		canvas.RoundedOutline(inner, m.dotRadius, m.lineWidth, lane)
	}
}

var (
	sqrt07 = float32(math.Sqrt(0.7))
	sqrt03 = float32(math.Sqrt(0.3))
)

// dashCurve traces a dotted lane change along its cubic with the pattern of dashLine: one
// dash and one gap fitted into a row, the pattern starting half a dash in.
func dashCurve(b *gfx.PathBuilder, s graphSegment) {
	const samples = 24
	type point struct{ x, y float32 }
	var pts [samples + 1]point
	total := float32(0)
	for i := 0; i <= samples; i++ {
		t := float32(i) / samples
		u := 1 - t
		x := u*u*u*s.x0 + 3*u*u*t*s.cx1 + 3*u*t*t*s.cx2 + t*t*t*s.x1
		y := u*u*u*s.y0 + 3*u*u*t*s.cy1 + 3*u*t*t*s.cy2 + t*t*t*s.y1
		pts[i] = point{x, y}
		if i > 0 {
			total += float32(math.Hypot(float64(x-pts[i-1].x), float64(y-pts[i-1].y)))
		}
	}
	space := float32(logRowHeight/2 - 2)
	length := total * 2
	dash := length/max(1, float32(math.Floor(float64(length/logRowHeight)))) - space
	if dash <= 0 {
		b.MoveTo(s.x0, s.y0)
		b.CubicTo(s.cx1, s.cy1, s.cx2, s.cy2, s.x1, s.y1)
		return
	}
	drawing, penDown := true, false
	remaining := dash / 2
	for i := 1; i <= samples; i++ {
		ax, ay := pts[i-1].x, pts[i-1].y
		bx, by := pts[i].x, pts[i].y
		segment := float32(math.Hypot(float64(bx-ax), float64(by-ay)))
		if segment <= 0 {
			continue
		}
		for pos := float32(0); pos < segment-0.001; {
			step := min(remaining, segment-pos)
			if step <= 0 {
				break
			}
			x0, y0 := ax+(bx-ax)*pos/segment, ay+(by-ay)*pos/segment
			x1, y1 := ax+(bx-ax)*(pos+step)/segment, ay+(by-ay)*(pos+step)/segment
			if drawing {
				if !penDown {
					b.MoveTo(x0, y0)
					penDown = true
				}
				b.LineTo(x1, y1)
			} else {
				penDown = false
			}
			pos += step
			remaining -= step
			if remaining <= 0.001 {
				drawing = !drawing
				if drawing {
					remaining = dash
				} else {
					remaining, penDown = space, false
				}
			}
		}
	}
}

// dashLine traces a dotted edge as the IntelliJ log dashes it: one dash and one gap
// fitted into a row, the pattern starting half a dash in.
func dashLine(b *gfx.PathBuilder, x0, y0, x1, y1 float32) {
	space := float32(logRowHeight/2 - 2)
	segment := float32(math.Hypot(float64(x1-x0), float64(y1-y0)))
	if segment <= 0 {
		return
	}
	length := segment * 2
	dash := length/max(1, float32(math.Floor(float64(length/logRowHeight)))) - space
	if dash <= 0 {
		b.MoveTo(x0, y0)
		b.LineTo(x1, y1)
		return
	}
	dx, dy := (x1-x0)/segment, (y1-y0)/segment
	for pos := -dash / 2; pos < segment; pos += dash + space {
		from, to := max(pos, 0), min(pos+dash, segment)
		if to <= from {
			continue
		}
		b.MoveTo(x0+dx*from, y0+dy*from)
		b.LineTo(x0+dx*to, y0+dy*to)
	}
}

// paintLabels paints a row's reference group: the overlapping tag icons of its kinds,
// the last with its hole, and the title beside them.
func (a *App) paintLabels(canvas *gfx.Canvas, rect gfx.Rect, g referenceGroup, text string, background gfx.Color) {
	size := float32(logRefSize + 3)
	canvas.Clip(rect)
	defer canvas.Unclip()
	for i := len(g.kinds) - 1; i >= 0; i-- {
		kind := g.kinds[i]
		index := len(g.kinds) - 1 - i
		box := gfx.Rect{X: rect.X + 4 + float32(index)*size/6.25*2, Y: rect.Y + rect.Height/2 - size/2, Width: size, Height: size}
		stroke := referenceColor(kind, a.colors.Dark)
		canvas.DrawPath(tagIcon(canvas.PathBuilder(), size), box, gfx.PathStyle{
			Fill: background, Stroke: stroke, StrokeWidth: size / 16, Join: gfx.JoinRound,
		})
		if index == len(g.kinds)-1 {
			hole := gfx.Rect{X: box.X + 9.5*size/16, Y: box.Y + 4.5*size/16, Width: size / 8, Height: size / 8}
			canvas.Rounded(hole, size/16, stroke)
		}
	}
	icon := g.iconWidth(size)
	a.paintText(canvas, text, gfx.Rect{X: rect.X + 5 + icon, Y: rect.Y, Width: max(0, rect.Width-icon-9), Height: rect.Height}, logRefSize, a.colors.Text, gfx.AlignStart)
	if text != g.title {
		canvas.HoverHint(0, rect, g.title)
	}
}

// paintText paints one line cut to its rectangle with an ellipsis.
func (a *App) paintText(canvas *gfx.Canvas, text string, rect gfx.Rect, size float32, color gfx.Color, align gfx.Alignment) {
	if rect.Width <= 0 || text == "" {
		return
	}
	line := canvas.FitLine(text, size, gfx.WeightRegular, rect.Width)
	if line == "" {
		return
	}
	canvas.LabelAligned(line, rect, gfx.TextStyle{Size: size, Color: color}, align)
	if line != text {
		canvas.HoverHint(0, rect, text)
	}
}

// date is the date a row shows, in local time, cached by commit.
func (v *logView) date(c core.GitCommit) string {
	if s, ok := v.dates[c.Hash]; ok {
		return s
	}
	var t time.Time
	if parsed, err := time.Parse(time.RFC3339, c.Date); err == nil {
		t = parsed
	} else if c.DateUnixSeconds != nil {
		t = time.Unix(*c.DateUnixSeconds, 0)
	}
	s := c.Date
	if !t.IsZero() {
		s = t.Local().Format(logDateFormat)
	}
	if v.dates == nil || len(v.dates) > 20000 {
		v.dates = map[string]string{}
	}
	v.dates[c.Hash] = s
	return s
}
