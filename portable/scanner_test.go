package main

import (
	"context"
	"os"
	"path/filepath"
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
	job.run(context.Background(), root, 5, 2)
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
	job.run(context.Background(), root, 0, 10)

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
