package settingswindow

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/1lck/Lithe-IDEA/desktop/internal/i18n"
	"github.com/1lck/Lithe-IDEA/desktop/internal/searchprefs"
)

// Observe the page-owned effect through frame wakes; no real user preferences are touched.
func waitSearchPage(t *testing.T, d *windowDriver, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	d.pump()
	for !ready() {
		select {
		case <-d.driver.Wakes():
			d.pump()
		case <-deadline.C:
			t.Fatal("search settings did not settle before the deadline")
		}
	}
}

// The index card is Lithe's own setting: its switch saves at once, without Apply, and never
// touches the search.json source.
func TestSearchIndexSwitchSavesAtOnce(t *testing.T) {
	var saved []bool
	root := filepath.Join(t.TempDir(), "preferences")
	w := newWindow(NewController(Options{
		SearchPreferences:           searchprefs.Source{Root: root},
		SetDisableSearchIndexOnOpen: func(value bool) { saved = append(saved, value) },
	}), Values{Language: "en", Theme: "dark"})
	w.sess.page = pageSearch
	d := openWindow(t, w)
	waitSearchPage(t, d, func() bool { return w.searchDraft.ready && !w.searchDraft.busy })
	label := i18n.Shared(w.marketLanguage(), "desktop.search.settings.indexOnOpen")
	d.pump()
	d.click(label)
	if !w.sess.values.DisableSearchIndexOnOpen {
		t.Fatal("the switch did not defer the index")
	}
	if len(saved) != 1 || !saved[0] {
		t.Fatalf("saved %v, want one deferred value", saved)
	}
	d.pump()
	d.click(label)
	if w.sess.values.DisableSearchIndexOnOpen || len(saved) != 2 || saved[1] {
		t.Fatalf("turning the switch back on saved %v (deferred %v)", saved, w.sess.values.DisableSearchIndexOnOpen)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the switch wrote the search preference source", err)
	}
}

func TestSearchPageReadsAppliesAndKeepsDraftOnConflict(t *testing.T) {
	root := filepath.Join(t.TempDir(), "preferences")
	w := newWindow(NewController(Options{SearchPreferences: searchprefs.Source{Root: root}}), Values{Language: "en", Theme: "dark"})
	w.sess.page = pageSearch
	d := openWindow(t, w)
	waitSearchPage(t, d, func() bool { return w.searchDraft.ready && !w.searchDraft.busy })
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("opening settings wrote a source", err)
	}
	w.searchDraft.search.SetText("32")
	w.searchDraft.replace.SetText("24")
	d.pump()
	d.click(i18n.Shared(w.marketLanguage(), "desktop.buildSettings.apply"))
	waitSearchPage(t, d, func() bool { return !w.searchDraft.busy })
	if w.searchDraft.snapshot.Effective.MaxFileBytes != 32<<20 || w.searchDraft.snapshot.Effective.ReplacementMaxFileBytes != 24<<20 {
		t.Fatalf("saved %+v", w.searchDraft.snapshot.Effective)
	}
	if err := os.WriteFile(w.searchDraft.snapshot.Path, []byte(`{"maxFileBytes":1048576}`), 0600); err != nil {
		t.Fatal(err)
	}
	w.searchDraft.search.SetText("48")
	d.pump()
	d.click(i18n.Shared(w.marketLanguage(), "desktop.buildSettings.apply"))
	waitSearchPage(t, d, func() bool { return !w.searchDraft.busy })
	if !w.searchDraft.stale || w.searchDraft.search.Text() != "48" || w.searchDraft.failure == "" {
		t.Fatal("conflict lost draft or remained writable")
	}
	d.click(i18n.Shared(w.marketLanguage(), "ui.refresh"))
	waitSearchPage(t, d, func() bool { return !w.searchDraft.busy && !w.searchDraft.stale })
	if w.searchDraft.search.Text() != "1" {
		t.Fatal("refresh did not read real source")
	}
}
