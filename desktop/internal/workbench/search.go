package workbench

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/1lck/Lithe-IDEA/desktop/internal/core"
	"github.com/1lck/Lithe-IDEA/desktop/internal/i18n"
	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/ui"
)

// The query and mask fields are named by the application so the window can
// move the keyboard to the query (Find in Files) and tests can address them.
var (
	searchQueryID = tree.ID(shellPage, "project-search-query")
	searchMaskID  = tree.ID(shellPage, "project-search-mask")
)

const (
	searchResultHeight = 24
	searchMaxResults   = 200
	searchDebounce     = 250 * time.Millisecond
	searchTimeout      = 30 * time.Second
	searchEventLimit   = 4096
	searchUpdateLimit  = 512
	// searchWarmTimeout bounds building the content index a search page narrows
	// candidates with. A large workspace is read once, so it gets longer than one
	// request: minutes on a workspace of gigabytes, not seconds.
	searchWarmTimeout = 10 * time.Minute
	// searchCountDelay lets a burst of typing replace a query before the workspace
	// is scanned for its totals.
	searchCountDelay = 400 * time.Millisecond
)

// The two ways a directory watch stops keeping the project tree or a search index up to date
// change by change: the watcher fell behind and dropped events, or more changes arrived than
// searchEventLimit lets the window apply one by one. Either way the window rebuilds from a
// fresh listing without telling the user, so the log records it as background work it fell
// back from (backgroundFailed).
var (
	errWatchEventsLost = errors.New("directory watch lost events")
	errWatchEventLimit = errors.New("directory watch changes exceeded the incremental update limit")
)

type searchQuery struct {
	text, mask, scope                string
	matchCase, wholeWords, regex     bool
	includeIgnored                   bool
	includeGenerated, followSymlinks bool
}

// searchView contains only controls and options; results belong to a project.
type searchView struct {
	scopeMode                                        string
	scopeModule                                      string
	scope                                            ui.Field
	includeIgnored                                   bool
	includeGenerated, followSymlinks                 bool
	query, mask                                      ui.Field
	optionsOpen, matchCase, wholeWords, regex, focus bool
	// replace is the Replace in Files field, shown while replaceOpen.
	replace     ui.Field
	replaceOpen bool
	// list is the result list's selection and scroll.
	list ui.ListState
	// delay is overridden in deterministic tests; negative means the normal delay.
	delay time.Duration
}

func (v *searchView) reset() {
	v.query.SetText("")
	v.mask.SetText("")
	v.scope.SetText("")
	v.scopeMode, v.scopeModule = "project", ""
	v.includeIgnored = false
	v.includeGenerated, v.followSymlinks = false, false
	v.replace.SetText("")
	v.replaceOpen = false
	v.optionsOpen, v.matchCase, v.wholeWords, v.regex, v.focus = false, false, false, false, false
	v.list = ui.ListState{}
}

type projectSearch struct {
	counts                 *core.WorkspaceSearchCounts
	countDone              <-chan struct{}
	countFailed            bool
	countRevision          uint64
	countCancel            context.CancelFunc
	request                core.WorkspaceSearchRequest
	cursor                 *string
	next                   bool
	want                   searchQuery
	generation             uint64
	cancel                 context.CancelFunc
	running, loading       bool
	indexUsed              bool
	matches                []core.WorkspaceSearchMatch
	collapsed              map[string]bool
	presentation           core.SearchPresentation
	presentationGeneration uint64
	failed                 bool
	watch                  *documentWatch
	watchAttached          bool
	watchReady             bool
	watchAvailable         bool
	fullResync             bool
	pending                []string
	done                   <-chan struct{}
	// warmNeeded are the option variants whose content index should be built once the current
	// page has answered, warmStarted the ones already building or built, and warming counts the
	// builds still running.
	warmNeeded  map[bool]searchQuery
	warmStarted map[bool]bool
	warming     int
}

func (a *App) cancelProjectSearch(p *project) {
	if p.search.countCancel != nil {
		p.search.countCancel()
		p.search.countCancel = nil
	}
	p.search.counts = nil
	p.search.generation++
	p.search.presentationGeneration++
	p.search.request = core.WorkspaceSearchRequest{}
	p.search.cursor = nil
	p.search.matches = nil
	p.search.presentation = core.SearchPresentation{}
	p.search.want = searchQuery{}
	p.search.loading = false
	a.searchView.optionsOpen = false
	if p.search.cancel != nil {
		p.search.cancel()
	}
	p.search.watchAttached = false
	if !p.everywhere.watchAttached && !p.tree.recursiveWatch && p.search.watch != nil {
		p.search.watch.close()
		p.search.watch = nil
		p.search.watchReady, p.search.watchAvailable = false, false
	}
	p.search.pending = nil
	p.search.fullResync = true
}

// Register away from the window thread: Linux may need to walk the tree and
// create one OS registration for every directory.
func (a *App) startProjectSearchWatch(p *project) {
	s := &p.search
	if s.watch != nil {
		return
	}
	slot := &documentWatch{}
	s.watch = slot
	s.watchReady, s.watchAvailable = false, false
	if p.everywhere.watchAttached {
		p.everywhere.watchReady, p.everywhere.watchAvailable = false, false
	}
	wake := func() { a.post(func(a *App) { a.drainProjectSearchWatch(p, slot) }) }
	if p.remote != nil {
		// A remote workspace's folder is on the host, which the local watch cannot observe: the
		// host watch feeds the index instead (remoteSearchChanged), and while it does not watch
		// every query builds the index from the host again.
		slot.finish(nil, nil)
		wake()
		return
	}
	a.tasks.running.Add(1)
	go func() {
		defer a.tasks.running.Done()
		watch, err := app.WatchTree(p.root, nil, wake)
		var observed documentDirectoryWatch
		if watch != nil {
			observed = watch
		}
		slot.finish(observed, err)
		wake()
	}()
}

func (a *App) drainProjectSearchWatch(p *project, slot *documentWatch) {
	s := &p.search
	if a.project != p || s.watch != slot {
		return
	}
	watch, ready, err := slot.state()
	available := watch != nil || p.remoteWatching()
	if ready {
		s.watchReady, s.watchAvailable, s.fullResync = true, available, true
		if err != nil && s.watchAttached {
			a.operationFailed("workspace_search_watch", err)
			a.notifyError(i18n.Shared(a.language(), "desktop.search.watchUnavailable"))
		}
	}
	changed := false
	var events app.DirectoryChanges
	if watch != nil {
		events = watch.Changes(nil)
		if events.Resync {
			operation := "workspace_search_incremental_update"
			if !s.watchAttached && !p.everywhere.watchAttached && p.tree.recursiveWatch {
				operation = "project_tree_watch"
			}
			a.backgroundFailed(operation, errWatchEventsLost)
			// The root may have been replaced, so install a fresh watch before
			// rebuilding. The new watch covers changes during the rebuild.
			slot.close()
			s.watch = nil
			s.pending = nil
			s.fullResync = true
			a.startProjectSearchWatch(p)
			changed = true
		} else if len(events.Names) > 0 {
			a.queueSearchChanges(&s.pending, &s.fullResync, events.Names)
			changed = true
		}
	}
	if p.everywhere.watchAttached {
		a.applyEverywhereWatch(p, ready, available, err, events)
	}
	if p.tree.recursiveWatch {
		a.applyTreeRecursiveWatch(p, ready, watch != nil, err, events)
	}
	if changed {
		a.projectSearchOutdated(p)
	}
	if s.watchReady && !s.running && strings.TrimSpace(s.want.text) != "" {
		a.runProjectSearch(p)
	}
}

// queueSearchChanges adds names, changed paths relative to the folder, to a search's pending
// changes, which update the index before its next query. More than searchEventLimit of them
// build the index again instead (full), and none are kept while that is due.
func (a *App) queueSearchChanges(pending *[]string, full *bool, names []string) {
	if len(*pending)+len(names) > searchEventLimit {
		a.backgroundFailed("workspace_search_incremental_update", errWatchEventLimit)
		*pending, *full = nil, true
	} else if !*full {
		*pending = append(*pending, names...)
	}
}

// projectSearchOutdated drops the Find in Files query and counts running after the folder
// changed; the caller runs the query again.
func (a *App) projectSearchOutdated(p *project) {
	s := &p.search
	if s.countCancel != nil {
		s.countCancel()
		s.countCancel = nil
	}
	s.counts = nil
	s.countFailed = false
	s.generation++
	if s.cancel != nil {
		s.cancel()
	}
	if strings.TrimSpace(s.want.text) != "" {
		s.loading = true
	}
}

// updateProjectSearch keeps at most one kernel operation running. Each newer
// request cancels the old one; a completed stale operation starts its successor.
func (a *App) updateProjectSearch(p *project, query searchQuery) {
	s := &p.search
	if s.want == query {
		return
	}
	s.want = query
	if s.countCancel != nil {
		s.countCancel()
		s.countCancel = nil
	}
	s.counts = nil
	s.countFailed = false
	s.generation++
	s.matches, s.failed = nil, false
	s.cursor, s.next = nil, false
	s.collapsed = make(map[string]bool)
	s.presentation = core.SearchPresentation{}
	s.presentationGeneration++
	a.searchView.list.ScrollTo(0, ui.ListStart)
	if s.cancel != nil {
		s.cancel()
	}
	if strings.TrimSpace(query.text) == "" {
		s.loading = false
		return
	}
	s.loading = true
	a.startSearchWarm(p, query)
	wasAttached := s.watchAttached
	s.watchAttached = true
	// A remote workspace's search waits for no local watch; the host watch tells the user once
	// when it cannot watch the folder (remoteWatchUnavailable).
	if !wasAttached && s.watchReady && !s.watchAvailable && s.watch != nil && p.remote == nil {
		_, _, err := s.watch.state()
		if err != nil {
			a.operationFailed("workspace_search_watch", err)
		}
		a.notifyError(i18n.Shared(a.language(), "desktop.search.watchUnavailable"))
	}
	if s.watch == nil {
		a.startProjectSearchWatch(p)
	}
	if !s.running && s.watchReady {
		a.runProjectSearch(p)
	}
}

// startSearchWarm begins building Core's content search index for the query's visibility
// variant, once per variant and project. Interactive pages narrow the files they read with its
// trigrams, which is what makes a rare query take milliseconds instead of minutes; they never
// build it themselves, because that would read the whole workspace before the first result.
func (a *App) startSearchWarm(p *project, query searchQuery) {
	s := &p.search
	if p.remote != nil {
		// A remote workspace's index lives on the host and is kept current by the host watch.
		// Warming it here would read the local placeholder and re-run the host query.
		return
	}
	if s.warmStarted[query.includeIgnored] || p.ctx == nil {
		return
	}
	// The warm request carries the file budget and the include-ignored option, which are what
	// Core's index key covers. Generated folders and internal links change the key in ways the
	// request cannot express, so those variants keep the names-only path.
	if query.followSymlinks || query.includeGenerated {
		return
	}
	if s.warmStarted == nil {
		s.warmStarted = make(map[bool]bool)
	}
	s.warmStarted[query.includeIgnored] = true
	s.warming++
	s.indexUsed = true
	root, maxFileBytes := p.root, a.searchPreferences.MaxFileBytes
	a.background(p, searchWarmTimeout, func(ctx context.Context) func(*App) {
		_, err := a.kernel.WarmWorkspaceSearch(ctx, root, maxFileBytes, query.includeIgnored)
		if ctx.Err() != nil {
			// The project is closing (or the build ran past its bound) and the caller that
			// drops Core's index may already have run, so drop whatever this build published
			// so a closed project does not keep its index resident.
			drop, cancel := context.WithTimeout(context.Background(), kernelCallTimeout)
			defer cancel()
			if dropErr := a.kernel.InvalidateWorkspaceSearch(drop, root); dropErr != nil && !errors.Is(dropErr, context.Canceled) {
				a.backgroundFailedIn(drop, "workspace_search_cleanup", dropErr)
			}
			return nil
		}
		return func(a *App) {
			s.warming = max(s.warming-1, 0)
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					a.backgroundFailed("workspace_search_index_warm", err)
					// A search that waited for the index must not stay loading forever.
					if s.loading && !s.running {
						s.loading, s.failed = false, true
					}
				}
				return
			}
			// A page that ran while the index was building scanned the whole workspace, or ran
			// past its bound; without this the user has to type again to see the result.
			if s.loading && s.watchReady && !s.running && strings.TrimSpace(s.want.text) != "" {
				a.runProjectSearch(p)
			}
			if e := &p.everywhere; a.everywhereView.open && e.loading && !e.running && strings.TrimSpace(e.query) != "" {
				a.runEverywhere(p, false)
			}
		}
	})
}

func (a *App) refreshProjectSearch(p *project) {
	query := p.search.want
	p.search.fullResync = true
	p.search.want = searchQuery{}
	a.updateProjectSearch(p, query)
}

func (a *App) runProjectSearch(p *project) {
	s := &p.search
	if s.running || !s.watchReady || strings.TrimSpace(s.want.text) == "" {
		return
	}
	query, generation := s.want, s.generation
	full, paths := s.fullResync || !s.watchAvailable || p.remoteWatchMisses(query.includeGenerated), s.pending
	s.fullResync, s.pending = false, nil
	s.indexUsed = true
	ctx, cancel := context.WithCancel(p.ctx)
	s.cancel, s.running = cancel, true
	finished := make(chan struct{})
	s.done = finished
	delay := a.searchView.delay
	if delay < 0 {
		delay = searchDebounce
	}
	kernel, root := a.kernel, p.root
	previous := s.matches
	collapsed := []string{}
	for path, value := range s.collapsed {
		if value {
			collapsed = append(collapsed, path)
		}
	}
	snapshots := captureSearchText(p)
	request := s.request
	next := s.next
	s.next = false
	if !next {
		noFileResults := 0
		request = core.WorkspaceSearchRequest{Root: root, Query: query.text, FileMask: query.mask,
			CaseSensitive: query.matchCase, WholeWords: query.wholeWords, RegularExpression: query.regex,
			MaxResults: searchMaxResults, MaxFileResults: &noFileResults, IncludeContentRanges: true,
			PageSize: 50, MaxFileBytes: a.searchPreferences.MaxFileBytes,
			ScopeExpression: query.scope, IncludeIgnored: query.includeIgnored,
			IncludeGenerated: query.includeGenerated, FollowSymlinks: query.followSymlinks}
		s.request = request
	} else {
		request.Cursor = s.cursor
	}
	a.background(p, 0, func(context.Context) func(*App) {
		defer close(finished)
		defer cancel()
		if delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return func(a *App) { a.finishProjectSearch(p, generation, nil, ctx.Err(), full || len(paths) > 0) }
			}
		}
		if !next {
			request.TextOverrides = materializeSearchText(snapshots)
		}
		operation, done := context.WithTimeout(ctx, searchTimeout)
		defer done()
		var err error
		if !next {
			err = synchronizeSearchIndex(operation, kernel, root, full, paths)
		}
		var matches []core.WorkspaceSearchMatch
		var cursor *string
		if err == nil {
			page, pageErr := kernel.SearchWorkspacePage(operation, request)
			matches, cursor, err = page.Matches, page.NextCursor, pageErr
		}
		if next && err == nil {
			if cursor != nil && ((request.Cursor != nil && *cursor == *request.Cursor) || len(matches) == 0) {
				err = errors.New("search page did not advance")
			} else {
				matches = append(previous, matches...)
			}
		}
		var presentation core.SearchPresentation
		if err == nil {
			identities := make([]string, len(matches))
			for i := range matches {
				identities[i] = matches[i].Path
			}
			presentation, err = core.NewClient().SearchPresentation(operation, identities, collapsed)
		}
		return func(a *App) {
			if generation == s.generation && err == nil {
				s.cursor = cursor
				s.request = request
				s.request.Cursor = nil
				s.presentation = presentation
				s.presentationGeneration++
			}
			a.finishProjectSearch(p, generation, matches, err, full || len(paths) > 0)
			if generation == s.generation && err == nil {
				needsCounts := !next
				for _, match := range matches[max(0, len(matches)-50):] {
					if s.counts != nil {
						if _, exists := s.counts.FileOccurrences[match.Path]; !exists {
							needsCounts = true
						}
					}
				}
				if needsCounts {
					a.countProjectSearch(p, request, generation)
				}
			}
		}
	})
}

func (a *App) loadMoreProjectSearch(p *project) {
	s := &p.search
	if s.running || s.failed || s.cursor == nil {
		return
	}
	s.next = true
	a.runProjectSearch(p)
}

// Count after publishing the first page so a complete scan cannot delay navigation.
func (a *App) countProjectSearch(p *project, request core.WorkspaceSearchRequest, generation uint64) {
	if p.search.countCancel != nil {
		p.search.countCancel()
	}
	p.search.countRevision++
	countRevision := p.search.countRevision
	ctx, cancel := context.WithCancel(p.ctx)
	p.search.countCancel = cancel
	p.search.countFailed = false
	finished := make(chan struct{})
	p.search.countDone = finished
	// Only loaded headings cross the bridge; totals still cover the full query.
	seen := map[string]bool{}
	for _, match := range p.search.matches[max(0, len(p.search.matches)-50):] {
		if !seen[match.Path] {
			request.CountPaths = append(request.CountPaths, match.Path)
			seen[match.Path] = true
		}
	}
	kernel := a.kernel
	a.background(p, searchTimeout, func(deadline context.Context) func(*App) {
		defer close(finished)
		defer cancel()
		stop := context.AfterFunc(deadline, cancel)
		defer stop()
		// A burst of typing replaces the query before this scan starts; there is no point
		// reading the workspace for totals of a query that is already gone.
		if searchCountDelay > 0 {
			timer := time.NewTimer(searchCountDelay)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return nil
			}
		}
		counts, err := kernel.CountWorkspaceSearch(ctx, request)
		return func(a *App) {
			if generation != p.search.generation || countRevision != p.search.countRevision {
				return
			}
			p.search.countCancel = nil
			if err != nil {
				p.search.countFailed = true
				if !errors.Is(err, context.Canceled) {
					a.backgroundFailed("workspace_search_count", p.logError(err))
				}
				return
			}
			if previous := p.search.counts; previous != nil {
				for path, count := range previous.FileOccurrences {
					if _, exists := counts.FileOccurrences[path]; !exists {
						counts.FileOccurrences[path] = count
					}
				}
			}
			p.search.counts = &counts
		}
	})
}

// synchronizeSearchIndex delegates visibility and incremental updates to Core.
// A lost or excessively large event batch requires a complete rebuild instead.
func synchronizeSearchIndex(ctx context.Context, kernel Kernel, root string, full bool, paths []string) error {
	if full {
		return kernel.InvalidateWorkspaceSearch(ctx, root)
	}
	if len(paths) == 0 {
		return nil
	}
	filtered, err := kernel.FilterWorkspaceChanges(ctx, paths)
	if err != nil {
		return err
	}
	if len(filtered) > searchUpdateLimit {
		return kernel.InvalidateWorkspaceSearch(ctx, root)
	}
	if len(filtered) == 0 {
		return nil
	}
	return kernel.UpdateWorkspaceSearch(ctx, root, filtered)
}

// releaseProjectSearch drops Core's index after the project closes so switching
// among projects does not retain every workspace inventory for the whole session.
// A remote workspace's session has ended by then, so this drops only the index of
// its empty placeholder; the indexes Core keyed by the session stay until Core
// drops them when a session closes (#2277).
func (a *App) releaseProjectSearch(p *project) {
	if p.search.countCancel != nil {
		p.search.countCancel()
		p.search.countCancel = nil
	}
	if p.search.watch != nil {
		p.search.watch.close()
		p.search.watch = nil
	}
	if !p.search.indexUsed {
		return
	}
	kernel, root, sink, finished, everywhereFinished := a.kernel, p.root, a.logSink, p.search.done, p.everywhere.done
	countFinished := p.search.countDone
	a.background(nil, kernelCallTimeout, func(ctx context.Context) func(*App) {
		var err error
		for _, job := range []<-chan struct{}{finished, everywhereFinished, countFinished} {
			if job == nil || err != nil {
				continue
			}
			select {
			case <-job:
			case <-ctx.Done():
				err = ctx.Err()
			}
		}
		if err == nil {
			err = kernel.InvalidateWorkspaceSearch(ctx, root)
		}
		if err != nil && !errors.Is(err, context.Canceled) && sink != nil {
			sink.Emit(backgroundFailedLevel, backgroundFailedEvent, "", operationFailedFields("workspace_search_cleanup", err))
		}
		return nil
	})
}

func (a *App) finishProjectSearch(p *project, generation uint64, matches []core.WorkspaceSearchMatch, err error, indexChanged bool) {
	s := &p.search
	s.running, s.cancel = false, nil
	if err != nil && indexChanged {
		s.fullResync = true
	}
	if generation != s.generation {
		if err != nil && !errors.Is(err, context.Canceled) {
			a.backgroundFailed("workspace_search", p.logError(err))
		}
		if s.watchReady && strings.TrimSpace(s.want.text) != "" {
			a.runProjectSearch(p)
		}
		return
	}
	s.loading = false
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		// A cold page scan can run past its bound on a large workspace while the content
		// index it competes with is still building; the build retries it, so the user sees
		// the search still running instead of a failure that fixes itself moments later.
		if s.warming > 0 && errors.Is(err, context.DeadlineExceeded) {
			s.loading = true
			return
		}
		s.failed = true
		a.operationFailed("workspace_search", p.logError(err))
		return
	}
	s.matches = matches
}

func (a *App) openSearchResult(p *project, result core.WorkspaceSearchMatch) {
	// Defend the adapter boundary even though Core returns workspace-relative paths.
	relative := filepath.FromSlash(result.Path)
	if relative == "" || filepath.IsAbs(relative) {
		a.operationRejected("workspace_search", "invalid_result_path")
		return
	}
	path := filepath.Join(p.root, relative)
	rel, err := filepath.Rel(p.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		a.operationRejected("workspace_search", "invalid_result_path")
		return
	}
	if result.Line != nil && len(result.ContentMatchRanges) > 0 {
		// The caret goes to the match itself, not only its line, and the arrival is revealed:
		// the line is centered and flashes once.
		a.openFileAtPositionRevealed(p, path, *result.Line-1, result.ContentMatchRanges[0].Start, true)
		return
	}
	if result.Line != nil {
		a.openFileAtPositionRevealed(p, path, *result.Line-1, 0, true)
		return
	}
	a.openFileAt(p, path, 0)
}

// searchMatchColumn is the one-based column of a content match's first hit, or 0.
func searchMatchColumn(match core.WorkspaceSearchMatch) int {
	if match.Line == nil || len(match.ContentMatchRanges) == 0 {
		return 0
	}
	return match.ContentMatchRanges[0].Start + 1
}

// openFindInFiles shows the Find in Files tool window with the keyboard in its query, and
// replace opens its Replace field too. A selection of one line in the editor becomes the
// query, as in the IDE; otherwise the last query stays.
func (a *App) openFindInFiles(p *project, replace bool) {
	if d := p.selectedDocument(p.docs.group); d != nil {
		if text := d.view.SelectedText(); text != "" && !strings.ContainsAny(text, "\r\n") {
			a.searchView.query.SetText(text)
		}
	}
	a.reduceChrome(core.SetChromeOpen(itemFind, true))
	if replace {
		a.searchView.replaceOpen = true
	}
	a.searchView.focus = true
}
