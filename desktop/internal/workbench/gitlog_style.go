package workbench

import (
	"math"
	"strings"
	"unicode/utf8"

	"github.com/1lck/Lithe-IDEA/desktop/internal/core"
	"github.com/1lck/Lithe-IDEA/desktop/internal/icons"
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/ui"
)

// This file gives the Log tab's commit list the look of SourceGit's Histories view: the
// neutral Contents palette of its themes, a column header, the lane colors and dots of its
// graph, and one bordered label per reference instead of the grouped title IDEA draws.
// SourceGit's own values are in src/Resources/Themes.axaml, src/Resources/Styles.axaml,
// src/Views/Histories.axaml and src/Models/CommitGraph.cs of sourcegit-org/sourcegit.

// The commit list's columns. The author and SHA columns are dropped on a narrow list so
// that the subject keeps its room, as SourceGit lets its user hide them.
const (
	logHeaderHeight   = 24
	logAuthorColumn   = 128
	logShaColumn      = 76
	logDateColumn     = 132
	logColumnGap      = 8
	logAuthorColumnAt = 470
	logShaColumnAt    = 620
	logDateColumnAt   = 620
	// The reference labels: SourceGit's 16 point rounded chip with its 16 point icon slot.
	logRowChipHeight = 16
	logRefChipRadius = 4
	logRefChipIcon   = 10
	logRefChipSlot   = 16
	logRowChipGap    = 4
	logRefChipText   = 20
	// logRefChipFloor is the least room a chip takes beside the subject; a chip that cannot
	// have it is not drawn.
	logRefChipFloor = 34
	// logAvatarSize is the author's picture at the start of the author column.
	logAvatarSize = 16
)

// graphPalette is SourceGit's default lane color list, in its order, used for every theme
// as SourceGit uses it for its Light and Dark themes alike.
var graphPalette = [...]uint32{
	0xFFA500, // Orange
	0x228B22, // ForestGreen
	0x40E0D0, // Turquoise
	0x808000, // Olive
	0xFF00FF, // Magenta
	0xFF0000, // Red
	0xF0E68C, // Khaki
	0x00FF00, // Lime
	0x4169E1, // RoyalBlue
	0x008080, // Teal
}

// graphPaletteAt is the lane color at index i of SourceGit's list, repeated when a graph
// has more lanes than colors.
func graphPaletteAt(i int) gfx.Color {
	n := len(graphPalette)
	i %= n
	if i < 0 {
		i += n
	}
	return gfx.RGB24(graphPalette[i])
}

// laneColors assigns SourceGit's palette to the color ids of one graph in the order the
// graph first uses them, as SourceGit's color picker hands its lanes out; Core's ids are
// hashes of reference names, not palette indices. The cache is rebuilt when the graph is
// replaced.
type laneColors struct {
	colors map[int32]gfx.Color
	rows   *core.GraphRow
	count  int
	gen    int
}

// of is the color of each color id of rows, the graph of projection gen.
func (c *laneColors) of(rows []core.GraphRow, gen int) func(int32) gfx.Color {
	if len(rows) == 0 {
		return func(int32) gfx.Color { return gfx.RGB24(graphPalette[0]) }
	}
	if c.colors == nil || c.rows != &rows[0] || c.count != len(rows) || c.gen != gen {
		c.rows, c.count, c.gen = &rows[0], len(rows), gen
		c.colors = make(map[int32]gfx.Color, 16)
		next := 0
		add := func(id int32) {
			if _, ok := c.colors[id]; ok {
				return
			}
			c.colors[id] = graphPaletteAt(next)
			next++
		}
		for i := range rows {
			add(rows[i].ColorID)
			for _, e := range rows[i].PrintElements {
				add(e.ColorID)
			}
		}
	}
	return func(id int32) gfx.Color {
		if color, ok := c.colors[id]; ok {
			return color
		}
		return graphPaletteAt(int(id))
	}
}

// gitListColors is the commit list's palette, taken from SourceGit's Contents, Border0,
// FG1 and FG2 resources.
type gitListColors struct {
	contents  gfx.Color
	hover     gfx.Color
	selection gfx.Color
	separator gfx.Color
	text      gfx.Color
	muted     gfx.Color
	// tag is the color of a tag's reference label, grey as SourceGit draws tags.
	tag gfx.Color
}

// gitListLook resolves SourceGit's colors for the Log tab's commit list in this theme.
func gitListLook(dark bool) gitListColors {
	if dark {
		return gitListColors{
			contents:  gfx.RGB24(0x1C1C1C),
			hover:     gfx.RGB24(0x262626),
			selection: gfx.RGB24(0x2D2D2D),
			separator: gfx.RGB24(0x181818),
			text:      gfx.RGB24(0xDFDFDF),
			muted:     gfx.RGB24(0x9F9F9F),
			tag:       gfx.RGB24(0x808080),
		}
	}
	return gitListColors{
		contents:  gfx.RGB24(0xFFFFFF),
		hover:     gfx.RGB24(0xF5F5F5),
		selection: gfx.RGB24(0xE9EEF6),
		separator: gfx.RGB24(0xCFCFCF),
		text:      gfx.RGB24(0x1F1F1F),
		muted:     gfx.RGB24(0x6F6F6F),
		tag:       gfx.RGB24(0x808080),
	}
}

// gitListPalette is the commit list's palette of the current theme.
func (a *App) gitListPalette() gitListColors { return gitListLook(a.colors.Dark) }

// logColumns is the commit list's column layout: which columns show and where each one
// stands in a list width wide. The graph and the subject share the room left of authorX.
type logColumns struct {
	author, sha, date    bool
	authorX, shaX, dateX float32
	subjectRight         float32
}

// logColumnsFor lays out the commit list's columns in a list width wide. A width of zero,
// a list that has not been laid out yet, keeps only the subject in the header; the next
// frame, together with the painted rows, shows the columns the real width has room for.
// A narrow list drops the author, SHA and then the date so that reference names and the
// subject keep their room; the date is still in the commit details.
func logColumnsFor(width float32) logColumns {
	c := logColumns{}
	if width <= 0 {
		return c
	}
	right := width
	if width >= logDateColumnAt {
		c.date = true
		right -= logDateColumn
		c.dateX = right
	}
	if width >= logShaColumnAt {
		c.sha = true
		right -= logShaColumn
		c.shaX = right
	}
	if width >= logAuthorColumnAt {
		c.author = true
		right -= logAuthorColumn
		c.authorX = right
	}
	c.subjectRight = max(0, right-logColumnGap)
	return c
}

// drawLogHeader declares the commit list's column header, the 24 point row SourceGit puts
// above its history with the graph, author, SHA and author time titles. The titles are
// painted from the row's own width, so the first frame already matches the columns the rows
// paint at any window size.
func (a *App) drawLogHeader(c ui.C) {
	styles := a.gitListPalette()
	g := a.text.Git
	ui.Row(c).Height(logHeaderHeight).FillWidth().Shrink(0).Background(styles.contents).Draw(func(d *ui.DrawContext) {
		cols := logColumnsFor(d.Rect.Width)
		label := func(text string, x, width float32) {
			if width <= 0 || text == "" {
				return
			}
			canvas := d.Canvas
			line := canvas.FitLine(text, logMetaSize, gfx.WeightBold, width)
			canvas.LabelAligned(line, gfx.Rect{X: x, Y: d.Rect.Y, Width: width, Height: d.Rect.Height},
				gfx.TextStyle{Size: logMetaSize, Weight: gfx.WeightBold, Color: styles.muted}, gfx.AlignCenter)
		}
		label(g.ColumnGraphSubject, d.Rect.X, cols.subjectRight)
		if cols.author {
			label(g.ColumnAuthor, d.Rect.X+cols.authorX, logAuthorColumn)
		}
		if cols.sha {
			label(g.ColumnSHA, d.Rect.X+cols.shaX, logShaColumn)
		}
		if cols.date {
			label(g.ColumnAuthorTime, d.Rect.X+cols.dateX, logDateColumn)
		}
	})
	ui.Row(c).Height(1).FillWidth().Shrink(0).Background(styles.separator)
}

// paintLogRefs paints one bordered label per reference of a commit row, as SourceGit's
// CommitRefsPresenter does, and returns where the subject starts. color is the row's lane
// color; current is the checked out branch; dim scales the labels of a commit that is not
// on that branch. HEAD is not drawn beside the branch it points at, and a remote's
// symbolic HEAD is left out, as SourceGit decorates its history.
func (a *App) paintLogRefs(canvas *gfx.Canvas, rect gfx.Rect, x, right float32, labels []core.GraphLabel, current string, color gfx.Color, styles gitListColors, dim float32) float32 {
	hasCurrent := false
	for i := range labels {
		if labels[i].Kind == "branch" && labels[i].Title == current {
			hasCurrent = true
		}
	}
	ordered := make([]core.GraphLabel, 0, len(labels))
	for _, kind := range [...]string{"head", "branch", "remote", "tag"} {
		for _, l := range labels {
			switch {
			case l.Kind == kind && kind == "head" && hasCurrent:
				continue
			case l.Kind == kind && kind == "remote" && strings.HasSuffix(l.Title, "/HEAD"):
				continue
			case l.Kind == kind:
				ordered = append(ordered, l)
			}
		}
	}
	centerY := rect.Y + rect.Height/2
	for i := range ordered {
		l := &ordered[i]
		head := l.Kind == "head" || l.Kind == "branch" && l.Title == current
		chipColor := color
		if l.Kind == "tag" {
			chipColor = styles.tag
		}
		size := float32(logRefSize)
		weight := float32(gfx.WeightRegular)
		if head {
			size, weight = logFontSize, gfx.WeightBold
		}
		if i > 0 {
			x += logRowChipGap
		}
		available := right - x
		if available < logRefChipFloor {
			break
		}
		fitted := canvas.FitLine(l.Title, size, weight, available-logRefChipText-6)
		if fitted == "" {
			break
		}
		textW := float32(math.Ceil(float64(canvas.TextWidthWeight(fitted, size, weight)))) + 4
		width := min(available, logRefChipText+textW+6)
		box := gfx.Rect{X: x, Y: centerY - logRowChipHeight/2, Width: width, Height: logRowChipHeight}
		if head {
			canvas.Rounded(box, logRefChipRadius, chipColor.WithAlpha(0.6*dim))
		} else {
			tint := gfx.Rect{X: box.X + logRefChipSlot, Y: box.Y, Width: max(0, box.Width-logRefChipSlot), Height: box.Height}
			canvas.Rounded(tint, logRefChipRadius, chipColor.WithAlpha(0.2*dim))
			canvas.Fill(gfx.Rect{X: box.X + logRefChipSlot, Y: box.Y, Width: 1, Height: box.Height}, chipColor.WithAlpha(dim))
		}
		canvas.RoundedOutline(box, logRefChipRadius, 1, chipColor.WithAlpha(dim))
		iconBox := gfx.Rect{X: box.X + 3, Y: centerY - logRefChipIcon/2, Width: logRefChipIcon, Height: logRefChipIcon}
		canvas.DrawIcon(referenceIcon(l.Kind), iconBox, styles.text.WithAlpha(dim))
		textRect := gfx.Rect{X: box.X + logRefChipText, Y: box.Y, Width: max(0, box.Width-logRefChipText-4), Height: box.Height}
		canvas.LabelAligned(fitted, textRect, gfx.TextStyle{Size: size, Weight: weight, Color: styles.text.WithAlpha(dim)}, gfx.AlignStart)
		x = box.X + box.Width
	}
	return min(x+logColumnGap, right)
}

// referenceIcon is the glyph in a reference label's icon slot.
func referenceIcon(kind string) gfx.Icon {
	switch kind {
	case "head":
		return icons.CircleDot
	case "remote":
		return icons.Cloud
	case "tag":
		return icons.Tag
	default:
		return icons.GitBranch
	}
}

// paintAvatar draws the 16 point author picture at the start of the author column: the
// first letter of the name over a color of its own, as SourceGit's fallback avatar does.
func (a *App) paintAvatar(canvas *gfx.Canvas, box gfx.Rect, name string, dim float32) {
	letter := "?"
	if r, size := utf8.DecodeRuneInString(strings.TrimSpace(name)); size > 0 && r != utf8.RuneError {
		letter = strings.ToUpper(string(r))
	}
	sum := 0
	for _, r := range name {
		sum += int(r)
	}
	colors := [...]uint32{0xD6645C, 0xD19A66, 0xE5C07B, 0x98C379, 0x56B6C2, 0x61AFEF, 0xC678DD, 0x7F8FA6}
	background := gfx.RGB24(colors[sum%len(colors)]).WithAlpha(dim)
	canvas.Rounded(box, 2, background)
	canvas.LabelAligned(letter, box, gfx.TextStyle{Size: 10, Weight: gfx.WeightBold, Color: gfx.RGB24(0xFFFFFF)}, gfx.AlignCenter)
}

// rowHasHead says whether a row's labels name the checked out commit, which SourceGit
// draws bold.
func rowHasHead(labels []core.GraphLabel) bool {
	for i := range labels {
		if labels[i].Kind == "head" {
			return true
		}
	}
	return false
}
