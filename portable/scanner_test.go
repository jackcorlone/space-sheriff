package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScannerFiltersSortsAndLimits(t *testing.T) {
	root := t.TempDir()
	for name, size := range map[string]int{"small": 3, "medium": 10, "large": 20} {
		if err := os.WriteFile(filepath.Join(root, name+".bin"), make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	job := &scanJob{status: ScanStatus{State: "running"}, started: time.Now(), known: make(map[string]FileRecord)}
	job.run(context.Background(), root, ScanOptions{Minimum: 5, DuplicateMinimum: 1 << 60, Limit: 2})
	status := job.snapshot()
	if status.State != "done" || len(status.Results) != 2 {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.Results[0].Size != 20 || status.Results[1].Size != 10 {
		t.Fatalf("results not sorted: %+v", status.Results)
	}
}

func TestScannerAggregatesFolders(t *testing.T) {
	root := t.TempDir()
	files := map[string]int{
		filepath.Join("a", "one.bin"):           10,
		filepath.Join("a", "nested", "two.bin"): 20,
		filepath.Join("b", "three.bin"):         5,
	}
	for name, size := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	job := &scanJob{
		status:  ScanStatus{State: "running", Root: root},
		started: time.Now(),
		known:   make(map[string]FileRecord),
		folders: make(map[string]FolderRecord),
	}
	job.run(context.Background(), root, ScanOptions{Minimum: 0, DuplicateMinimum: 1 << 60, Limit: 10})

	view, ok := job.folderView(root)
	if !ok || len(view.Children) != 2 {
		t.Fatalf("unexpected root view: %+v", view)
	}
	if view.Current.Size != 35 || view.Current.FileCount != 3 {
		t.Fatalf("unexpected root aggregate: %+v", view.Current)
	}
	if view.Children[0].Name != "a" || view.Children[0].Size != 30 || view.Children[0].FileCount != 2 {
		t.Fatalf("unexpected largest folder: %+v", view.Children[0])
	}
}

func TestScannerReportsPersistenceFailure(t *testing.T) {
	store, _ := testStore(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.bin"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	job := &scanJob{
		status: ScanStatus{State: "running", Root: root}, started: time.Now(),
		known: make(map[string]FileRecord), folders: make(map[string]FolderRecord),
		store: store, sessionID: "session",
	}
	job.run(context.Background(), root, ScanOptions{
		Minimum: 0, DuplicateMinimum: 1 << 60, Limit: 10,
	})
	status := job.snapshot()
	if status.State != "error" || !strings.Contains(status.Message, "保存扫描报告失败") {
		t.Fatalf("persistence failure was hidden: %+v", status)
	}
}

func TestScannerFindsContentDuplicatesAndHonorsExcludes(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one.bin", filepath.Join("nested", "two.bin")} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("same-content"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "different.bin"), []byte("other-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	excluded := filepath.Join(root, "excluded")
	if err := os.MkdirAll(excluded, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(excluded, "copy.bin"), []byte("same-content"), 0o600); err != nil {
		t.Fatal(err)
	}

	job := &scanJob{
		status:          ScanStatus{State: "running", Root: root},
		started:         time.Now(),
		known:           make(map[string]FileRecord),
		folders:         make(map[string]FolderRecord),
		duplicateByPath: make(map[string]string),
	}
	job.run(context.Background(), root, ScanOptions{
		Minimum:          0,
		DuplicateMinimum: 1,
		Limit:            20,
		Excludes:         []string{"excluded"},
	})
	status := job.snapshot()
	if len(status.DuplicateGroups) != 1 {
		t.Fatalf("got duplicate groups %+v", status.DuplicateGroups)
	}
	group := status.DuplicateGroups[0]
	if len(group.Files) != 2 || group.Reclaimable != int64(len("same-content")) {
		t.Fatalf("unexpected duplicate group: %+v", group)
	}
	if status.Excluded != 1 {
		t.Fatalf("excluded count = %d, want 1", status.Excluded)
	}
}

func TestCleanupPlanCannotRemoveEveryDuplicate(t *testing.T) {
	group := DuplicateGroup{
		ID: "group",
		Files: []FileRecord{
			{Path: "one"},
			{Path: "two"},
		},
	}
	job := &scanJob{status: ScanStatus{DuplicateGroups: []DuplicateGroup{group}}}
	if !job.wouldRemoveEveryDuplicate([]string{"one", "two"}) {
		t.Fatal("plan removing every copy was allowed")
	}
	if job.wouldRemoveEveryDuplicate([]string{"one"}) {
		t.Fatal("plan keeping one copy was rejected")
	}
}

func TestExcludeMatcherSupportsNamesRelativeAndAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	absolute := filepath.Join(root, "absolute")
	matcher := newExcludeMatcher(root, []string{"node_modules", filepath.Join("build", "cache"), absolute, ""})
	for _, path := range []string{
		filepath.Join(root, "app", "node_modules"),
		filepath.Join(root, "build", "cache", "item.bin"),
		filepath.Join(absolute, "item.bin"),
	} {
		if !matcher.match(path) {
			t.Fatalf("expected exclusion for %s", path)
		}
	}
	if matcher.match(filepath.Join(root, "source", "item.bin")) {
		t.Fatal("unrelated path was excluded")
	}
}

func TestHashFileCanBeCancelled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.bin")
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := hashFile(ctx, path); err != context.Canceled {
		t.Fatalf("got %v, want context canceled", err)
	}
}

func TestScannerDoesNotReportHardLinksAsDuplicates(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "original.bin")
	link := filepath.Join(root, "link.bin")
	if err := os.WriteFile(original, []byte("same-physical-file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(original, link); err != nil {
		t.Skipf("hard links are unavailable: %v", err)
	}
	job := &scanJob{
		status:          ScanStatus{State: "running", Root: root},
		started:         time.Now(),
		known:           make(map[string]FileRecord),
		folders:         make(map[string]FolderRecord),
		duplicateByPath: make(map[string]string),
	}
	job.run(context.Background(), root, ScanOptions{Minimum: 0, DuplicateMinimum: 1, Limit: 20})
	status := job.snapshot()
	if len(status.DuplicateGroups) != 0 {
		t.Fatalf("hard links were reported as duplicates: %+v", status.DuplicateGroups)
	}
}

func TestScannerReusesHashesOnSecondScan(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one.bin", "two.bin"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("same-content"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store, _ := testStore(t)
	run := func() ScanStatus {
		sessionID, err := store.beginScan(root)
		if err != nil {
			t.Fatal(err)
		}
		job := &scanJob{
			status:          ScanStatus{State: "running", Root: root},
			started:         time.Now(),
			known:           make(map[string]FileRecord),
			folders:         make(map[string]FolderRecord),
			duplicateByPath: make(map[string]string),
			store:           store,
			sessionID:       sessionID,
		}
		job.run(context.Background(), root, ScanOptions{Minimum: 0, DuplicateMinimum: 1, Limit: 20})
		return job.snapshot()
	}
	first := run()
	second := run()
	if first.FilesHashed != 2 || first.HashesReused != 0 {
		t.Fatalf("unexpected first scan cache stats: %+v", first)
	}
	if second.FilesHashed != 0 || second.HashesReused != 2 {
		t.Fatalf("unexpected second scan cache stats: %+v", second)
	}
}
