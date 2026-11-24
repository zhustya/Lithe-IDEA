package workbench

import (
	"math"
	"testing"

	"github.com/1lck/Lithe-IDEA/desktop/internal/core"
)

// TestLogColumnsDropAuthorAndSHAOnANarrowList pins the thresholds that keep reference names
// and the subject's room when the commit list is narrow, and the order of the columns that
// remain.
func TestLogColumnsDropAuthorAndSHAOnANarrowList(t *testing.T) {
	for _, tc := range []struct {
		name              string
		width             float32
		author, sha, date bool
	}{
		{"not laid out yet", 0, false, false, false},
		{"below the author threshold", 469, false, false, false},
		{"at the author threshold", 470, true, false, false},
		{"below the SHA threshold", 619, true, false, false},
		{"at the SHA threshold", 620, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := logColumnsFor(tc.width)
			if got.author != tc.author || got.sha != tc.sha || got.date != tc.date {
				t.Fatalf("author %v sha %v date %v, want author %v sha %v date %v",
					got.author, got.sha, got.date, tc.author, tc.sha, tc.date)
			}
			if tc.width > 0 && got.subjectRight <= 0 {
				t.Fatalf("no room left for the subject: %+v", got)
			}
			if tc.author && got.authorX < got.subjectRight {
				t.Fatalf("author column out of order: %+v", got)
			}
			if tc.sha && got.shaX < got.authorX {
				t.Fatalf("SHA column out of order: %+v", got)
			}
			if tc.date && got.dateX < got.shaX {
				t.Fatalf("date column out of order: %+v", got)
			}
		})
	}
}

// TestLaneColorsTakeSourceGitPaletteInGraphOrder pins that Core's stable color ids are
// mapped to SourceGit's palette by the order the graph first uses them, not by the id.
func TestLaneColorsTakeSourceGitPaletteInGraphOrder(t *testing.T) {
	rows := []core.GraphRow{
		{ColorID: 417, PrintElements: []core.GraphElement{{ColorID: -92}}},
		{ColorID: 417},
	}
	var cache laneColors
	color := cache.of(rows, 1)
	if got, want := color(417), graphPaletteAt(0); got != want {
		t.Fatalf("first lane %v, want %v", got, want)
	}
	if got, want := color(-92), graphPaletteAt(1); got != want {
		t.Fatalf("second lane %v, want %v", got, want)
	}
	if again := cache.of(rows, 1); again(417) != color(417) {
		t.Fatal("the lane colors changed on a later paint of the same graph")
	}
	var other laneColors
	if got, want := other.of(rows[:1], 1)(417), graphPaletteAt(0); got != want {
		t.Fatalf("another graph starts at %v, want %v", got, want)
	}
}

// TestGraphSegmentJoinsCurveHalves pins that a lane change is two halves of the same curve:
// the lower half of one row and the upper half of the next meet at the row edge with the
// same tangent, so the branch has no corner there.
func TestGraphSegmentJoinsCurveHalves(t *testing.T) {
	m := newGraphMetrics(1)
	down := m.segment(core.GraphElement{Position: 0, AdjacentPosition: 1, Direction: "down"})
	up := m.segment(core.GraphElement{Position: 1, AdjacentPosition: 0, Direction: "up"})
	if !down.curved || !up.curved {
		t.Fatalf("a lane change is not curved: down %+v up %+v", down, up)
	}
	if down.x1 != up.x0 || down.y1 != logRowHeight || up.y0 != 0 {
		t.Fatalf("halves do not meet at the row edge: down ends %v,%v up starts %v,%v", down.x1, down.y1, up.x0, up.y0)
	}
	dx1, dy1 := down.x1-down.cx2, down.y1-down.cy2
	dx2, dy2 := up.cx1-up.x0, up.cy1-up.y0
	if cross := dx1*dy2 - dy1*dx2; math.Abs(float64(cross)) > 0.001 {
		t.Fatalf("tangents differ at the row edge: %v,%v and %v,%v", dx1, dy1, dx2, dy2)
	}
	if dot := dx1*dx2 + dy1*dy2; dot <= 0 {
		t.Fatalf("halves turn back at the row edge: dot %v", dot)
	}
	straight := m.segment(core.GraphElement{Position: 0, AdjacentPosition: 0, Direction: "down"})
	if straight.curved || straight.x0 != straight.x1 {
		t.Fatalf("a lane that keeps its column is not straight: %+v", straight)
	}
}
