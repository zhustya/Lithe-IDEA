package core

import (
	"context"
	"os"
	"testing"
	"time"
)

// BenchmarkWorkspaceColdContentSearch compares the sidebar, the popup and the warm index on the
// same real checkout. An explicit root avoids treating a small generated workspace as a
// large-project measurement; LITHE_SEARCH_BENCH_QUERY picks the query, so a workspace whose
// identifiers are not Go names can be measured too.
func BenchmarkWorkspaceColdContentSearch(b *testing.B) {
	root := os.Getenv("LITHE_SEARCH_BENCH_ROOT")
	if root == "" {
		b.Skip("set LITHE_SEARCH_BENCH_ROOT to a real workspace")
	}
	query := os.Getenv("LITHE_SEARCH_BENCH_QUERY")
	if query == "" {
		query = "LockOSThread"
	}
	client := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	status, err := client.ConfigureIndexing(ctx, root, "search-latency-benchmark", IndexingPolicyOf(nil, true), false)
	cancel()
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := client.ReleaseIndexing(ctx, root, "search-latency-benchmark", status.Revision); err != nil {
			b.Error(err)
		}
	})
	for _, popup := range []bool{false, true} {
		name := "sidebar"
		if popup {
			name = "popup-text"
		}
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				if err := client.InvalidateWorkspaceSearch(ctx, root); err != nil {
					cancel()
					b.Fatal(err)
				}
				var count int
				var err error
				if popup {
					var page EverywhereSearchPage
					page, err = client.SearchEverywherePage(ctx, EverywhereSearchRequest{Root: root, Query: query, PageSize: 50, ResultKinds: []string{"content"}, IncludeContentRanges: true, MaxFileBytes: 16 << 20})
					count = len(page.Matches)
				} else {
					var page WorkspaceSearchPage
					page, err = client.SearchWorkspacePage(ctx, WorkspaceSearchRequest{Root: root, Query: query, PageSize: 50, IncludeContentRanges: true, MaxFileBytes: 16 << 20})
					count = len(page.Matches)
				}
				cancel()
				if err != nil || count == 0 {
					b.Fatalf("real search returned %d matches: %v", count, err)
				}
			}
		})
	}
	// The window builds the content index once per project in the background; the pages that
	// follow narrow their candidates with its trigrams instead of reading the whole workspace.
	b.Run("sidebar-warm", func(b *testing.B) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		if _, err := client.WarmWorkspaceSearch(ctx, root, 16<<20, false); err != nil {
			cancel()
			b.Fatal(err)
		}
		cancel()
		for b.Loop() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			page, err := client.SearchWorkspacePage(ctx, WorkspaceSearchRequest{Root: root, Query: query, PageSize: 50, IncludeContentRanges: true, MaxFileBytes: 16 << 20})
			cancel()
			if err != nil || len(page.Matches) == 0 {
				b.Fatalf("warm search returned %d matches: %v", len(page.Matches), err)
			}
		}
	})
	// The complete counts read only the files holding the query's trigrams as well.
	b.Run("count-warm", func(b *testing.B) {
		for b.Loop() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			counts, err := client.CountWorkspaceSearch(ctx, WorkspaceSearchRequest{Root: root, Query: query, MaxResults: 200, MaxFileBytes: 16 << 20})
			cancel()
			if err != nil || counts.Occurrences == 0 {
				b.Fatalf("warm count returned %d occurrences: %v", counts.Occurrences, err)
			}
		}
	})
}
