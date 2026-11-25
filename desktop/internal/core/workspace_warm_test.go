package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Warming builds Core's content index without searching, so the interactive pages that follow
// narrow their candidates with it instead of reading every file of the workspace.
func TestWarmWorkspaceSearchBuildsTheContentIndex(t *testing.T) {
	root := t.TempDir()
	for name, text := range map[string]string{"a.txt": "needle\n", "b.txt": "unrelated\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := NewClient()
	status, err := client.WarmWorkspaceSearch(ctx, root, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if status.FileCount != 2 {
		t.Fatalf("the warm index holds %d files, want 2", status.FileCount)
	}
	if status.PostingCount == 0 {
		t.Fatal("the warm index holds no trigram postings")
	}
	page, err := client.SearchWorkspacePage(ctx, WorkspaceSearchRequest{
		Root: root, Query: "needle", MaxResults: 10, PageSize: 10, IncludeContentRanges: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Matches) != 1 || page.Matches[0].Path != "a.txt" {
		t.Fatalf("page = %+v, want the one content match in a.txt", page.Matches)
	}
}
