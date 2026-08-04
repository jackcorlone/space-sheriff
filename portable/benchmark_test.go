package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func benchmarkFileCount(b *testing.B) int {
	b.Helper()
	count := 1000
	if raw := os.Getenv("SPACE_SHERIFF_BENCH_FILES"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 1_000_000 {
			b.Fatalf("SPACE_SHERIFF_BENCH_FILES must be between 1 and 1000000: %q", raw)
		}
		count = parsed
	}
	return count
}

func BenchmarkScanManyFiles(b *testing.B) {
	count := benchmarkFileCount(b)
	root := b.TempDir()
	content := []byte("space-sheriff-benchmark")
	for index := 0; index < count; index++ {
		path := filepath.Join(root, fmt.Sprintf("file-%07d.bin", index))
		if err := os.WriteFile(path, content, 0o600); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		job := &scanJob{
			status:  ScanStatus{State: "running", Root: root},
			started: time.Now(), known: make(map[string]FileRecord),
			folders: make(map[string]FolderRecord),
		}
		job.run(context.Background(), root, ScanOptions{
			Minimum: 0, DuplicateMinimum: 1 << 60, Limit: 200,
		})
		if status := job.snapshot(); status.State != "done" || status.FilesSeen != int64(count) {
			b.Fatalf("benchmark scan failed: %+v", status)
		}
		b.ReportMetric(float64(count), "files/op")
	}
}

func BenchmarkIndexedScanManyFiles(b *testing.B) {
	count := benchmarkFileCount(b)
	root := b.TempDir()
	content := []byte("space-sheriff-indexed-benchmark")
	for index := 0; index < count; index++ {
		path := filepath.Join(root, fmt.Sprintf("file-%07d.bin", index))
		if err := os.WriteFile(path, content, 0o600); err != nil {
			b.Fatal(err)
		}
	}
	store, err := openStore(filepath.Join(b.TempDir(), "index.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		sessionID, err := store.beginScan(root)
		if err != nil {
			b.Fatal(err)
		}
		job := &scanJob{
			status:  ScanStatus{State: "running", Root: root},
			started: time.Now(), known: make(map[string]FileRecord),
			folders: make(map[string]FolderRecord), duplicateByPath: make(map[string]string),
			store: store, sessionID: sessionID,
		}
		job.run(context.Background(), root, ScanOptions{
			Minimum: 0, DuplicateMinimum: 1 << 60, Limit: 200,
		})
		if status := job.snapshot(); status.State != "done" || status.FilesSeen != int64(count) {
			b.Fatalf("indexed benchmark scan failed: %+v", status)
		}
		b.ReportMetric(float64(count), "files/op")
	}
}
