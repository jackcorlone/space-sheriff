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
