package main

import (
	"container/heap"
	"context"
	"io/fs"
	"path/filepath"
	"sync"
	"time"
)

type FileRecord struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modifiedAt"`
	Advice     Advice `json:"advice"`
}

type ScanStatus struct {
	State       string       `json:"state"`
	Root        string       `json:"root"`
	FilesSeen   int64        `json:"filesSeen"`
	BytesSeen   int64        `json:"bytesSeen"`
	Errors      int64        `json:"errors"`
	CurrentPath string       `json:"currentPath"`
	ElapsedMS   int64        `json:"elapsedMs"`
	Results     []FileRecord `json:"results,omitempty"`
	Message     string       `json:"message,omitempty"`
}

type scanJob struct {
	mu      sync.RWMutex
	status  ScanStatus
	started time.Time
	cancel  context.CancelFunc
	known   map[string]FileRecord
}

func (j *scanJob) snapshot() ScanStatus {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := j.status
	out.Results = append([]FileRecord(nil), j.status.Results...)
	if j.status.State == "running" {
		out.ElapsedMS = time.Since(j.started).Milliseconds()
	}
	return out
}

type recordHeap []FileRecord

func (h recordHeap) Len() int           { return len(h) }
func (h recordHeap) Less(i, k int) bool { return h[i].Size < h[k].Size }
func (h recordHeap) Swap(i, k int)      { h[i], h[k] = h[k], h[i] }
func (h *recordHeap) Push(x any)        { *h = append(*h, x.(FileRecord)) }
func (h *recordHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

func (j *scanJob) run(ctx context.Context, root string, minimum int64, limit int) {
	found := &recordHeap{}
	heap.Init(found)
	now := time.Now()

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		select {
		case <-ctx.Done():
			return context.Canceled
		default:
		}
		if walkErr != nil {
			j.mu.Lock()
			j.status.Errors++
			j.mu.Unlock()
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			j.mu.Lock()
			j.status.CurrentPath = path
			j.mu.Unlock()
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil || !info.Mode().IsRegular() {
			if statErr != nil {
				j.mu.Lock()
				j.status.Errors++
				j.mu.Unlock()
			}
			return nil
		}

		j.mu.Lock()
		j.status.FilesSeen++
		j.status.BytesSeen += info.Size()
		j.mu.Unlock()

		if info.Size() >= minimum {
			record := FileRecord{
				Path:       path,
				Size:       info.Size(),
				ModifiedAt: info.ModTime().Format("2006-01-02 15:04"),
				Advice:     advise(path, info.Size(), info.ModTime(), now),
			}
			if found.Len() < limit {
				heap.Push(found, record)
			} else if (*found)[0].Size < record.Size {
				heap.Pop(found)
				heap.Push(found, record)
			}
		}
		return nil
	})

	results := make([]FileRecord, found.Len())
	for i := len(results) - 1; i >= 0; i-- {
		results[i] = heap.Pop(found).(FileRecord)
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	j.status.ElapsedMS = time.Since(j.started).Milliseconds()
	j.status.Results = results
	j.known = make(map[string]FileRecord, len(results))
	for _, record := range results {
		j.known[record.Path] = record
	}
	switch {
	case err == context.Canceled:
		j.status.State = "cancelled"
	case err != nil:
		j.status.State = "error"
		j.status.Message = err.Error()
	default:
		j.status.State = "done"
	}
}
