package main

import (
	"container/heap"
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type FileRecord struct {
	Path             string `json:"path"`
	Size             int64  `json:"size"`
	ModifiedAt       string `json:"modifiedAt"`
	ModifiedUnixNano int64  `json:"-"`
	Identity         string `json:"-"`
	PhysicalSize     int64  `json:"physicalSize,omitempty"`
	LinkCount        uint64 `json:"linkCount,omitempty"`
	Advice           Advice `json:"advice"`
}

type FolderRecord struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	FileCount  int64  `json:"fileCount"`
	ModifiedAt string `json:"modifiedAt"`
}

type FolderView struct {
	Current  FolderRecord   `json:"current"`
	Parent   string         `json:"parent,omitempty"`
	Children []FolderRecord `json:"children"`
}

type DuplicateGroup struct {
	ID          string       `json:"id"`
	Size        int64        `json:"size"`
	Reclaimable int64        `json:"reclaimable"`
	Files       []FileRecord `json:"files"`
}

type ScanStatus struct {
	State           string           `json:"state"`
	Phase           string           `json:"phase"`
	Root            string           `json:"root"`
	PolicyID        string           `json:"policyId,omitempty"`
	PolicyVersion   int              `json:"policyVersion,omitempty"`
	FilesSeen       int64            `json:"filesSeen"`
	FilesHashed     int64            `json:"filesHashed"`
	HashesReused    int64            `json:"hashesReused"`
	BytesSeen       int64            `json:"bytesSeen"`
	Errors          int64            `json:"errors"`
	Excluded        int64            `json:"excluded"`
	CurrentPath     string           `json:"currentPath"`
	ElapsedMS       int64            `json:"elapsedMs"`
	Results         []FileRecord     `json:"results,omitempty"`
	DuplicateGroups []DuplicateGroup `json:"duplicateGroups,omitempty"`
	Message         string           `json:"message,omitempty"`
}

type scanJob struct {
	mu              sync.RWMutex
	status          ScanStatus
	started         time.Time
	cancel          context.CancelFunc
	known           map[string]FileRecord
	folders         map[string]FolderRecord
	duplicateByPath map[string]string
	store           *Store
	sessionID       string
}

func (j *scanJob) snapshot() ScanStatus {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := j.status
	out.Results = append([]FileRecord(nil), j.status.Results...)
	out.DuplicateGroups = cloneDuplicateGroups(j.status.DuplicateGroups)
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

type folderAggregate struct {
	size      int64
	fileCount int64
	modified  time.Time
}

type ScanOptions struct {
	Minimum          int64
	DuplicateMinimum int64
	Limit            int
	Excludes         []string
	Policy           Policy
}

func (j *scanJob) run(ctx context.Context, root string, options ScanOptions) {
	if options.Policy.ID == "" {
		options.Policy = balancedPolicy()
	}
	found := &recordHeap{}
	heap.Init(found)
	now := time.Now()
	direct := map[string]*folderAggregate{root: {}}
	duplicateCandidates := make([]FileRecord, 0)
	duplicateIdentities := make(map[string]bool)
	indexBatch := make([]FileRecord, 0, 256)
	excludes := newExcludeMatcher(root, options.Excludes)

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
		if path != root && excludes.match(path) {
			j.mu.Lock()
			j.status.Excluded++
			j.mu.Unlock()
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if direct[path] == nil {
				direct[path] = &folderAggregate{}
			}
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
		filesSeen := j.status.FilesSeen
		j.mu.Unlock()

		directory := filepath.Dir(path)
		aggregate := direct[directory]
		if aggregate == nil {
			aggregate = &folderAggregate{}
			direct[directory] = aggregate
		}
		aggregate.size += info.Size()
		aggregate.fileCount++
		if info.ModTime().After(aggregate.modified) {
			aggregate.modified = info.ModTime()
		}

		identity, physicalSize, linkCount := fileIdentity(path, info)
		if identity == "" {
			identity = "path:" + filepath.Clean(path)
		}
		record := FileRecord{
			Path:             path,
			Size:             info.Size(),
			ModifiedAt:       info.ModTime().Format("2006-01-02 15:04"),
			ModifiedUnixNano: info.ModTime().UnixNano(),
			Identity:         identity,
			PhysicalSize:     physicalSize,
			LinkCount:        linkCount,
		}
		if j.store != nil {
			indexBatch = append(indexBatch, record)
			if len(indexBatch) == cap(indexBatch) {
				if saveErr := j.store.saveFiles(indexBatch, j.sessionID); saveErr != nil {
					j.mu.Lock()
					j.status.Errors++
					j.mu.Unlock()
				}
				indexBatch = indexBatch[:0]
			}
		}
		if info.Size() >= options.Minimum || info.Size() >= options.DuplicateMinimum {
			record.Advice = adviseWithPolicy(options.Policy, path, info.Size(), info.ModTime(), now)
			if info.Size() >= options.DuplicateMinimum && !duplicateIdentities[identity] {
				duplicateCandidates = append(duplicateCandidates, record)
				duplicateIdentities[identity] = true
			}
			if info.Size() >= options.Minimum {
				if found.Len() < options.Limit {
					heap.Push(found, record)
				} else if (*found)[0].Size < record.Size {
					heap.Pop(found)
					heap.Push(found, record)
				}
			}
		}
		if filesSeen%5000 == 0 {
			j.mu.Lock()
			j.status.Results = sortedRecords(*found)
			j.mu.Unlock()
		}
		return nil
	})
	if j.store != nil && len(indexBatch) > 0 {
		if saveErr := j.store.saveFiles(indexBatch, j.sessionID); saveErr != nil {
			j.mu.Lock()
			j.status.Errors++
			j.mu.Unlock()
		}
	}

	results := sortedRecords(*found)
	var duplicateGroups []DuplicateGroup
	var duplicateByPath map[string]string
	if err == nil {
		j.mu.Lock()
		j.status.Phase = "duplicates"
		j.status.CurrentPath = ""
		j.mu.Unlock()
		if j.store != nil {
			_ = j.store.setScanState(j.sessionID, "hashing")
		}
		duplicateGroups, duplicateByPath, err = j.findDuplicates(ctx, duplicateCandidates)
	}
	paths := make([]string, 0, len(direct))
	for path := range direct {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, k int) bool {
		return pathDepth(paths[i]) > pathDepth(paths[k])
	})
	for _, path := range paths {
		if path == root {
			continue
		}
		parent := filepath.Dir(path)
		parentAggregate := direct[parent]
		if parentAggregate == nil {
			continue
		}
		child := direct[path]
		parentAggregate.size += child.size
		parentAggregate.fileCount += child.fileCount
		if child.modified.After(parentAggregate.modified) {
			parentAggregate.modified = child.modified
		}
	}

	folders := make(map[string]FolderRecord, len(direct))
	for path, aggregate := range direct {
		modified := ""
		if !aggregate.modified.IsZero() {
			modified = aggregate.modified.Format("2006-01-02 15:04")
		}
		name := filepath.Base(path)
		if path == root {
			name = path
		}
		folders[path] = FolderRecord{
			Path:       path,
			Name:       name,
			Size:       aggregate.size,
			FileCount:  aggregate.fileCount,
			ModifiedAt: modified,
		}
	}

	j.mu.Lock()
	j.status.ElapsedMS = time.Since(j.started).Milliseconds()
	j.status.Results = results
	j.status.DuplicateGroups = duplicateGroups
	j.known = make(map[string]FileRecord, len(results))
	j.folders = folders
	j.duplicateByPath = duplicateByPath
	for _, record := range results {
		j.known[record.Path] = record
	}
	for _, group := range duplicateGroups {
		for _, record := range group.Files {
			j.known[record.Path] = record
		}
	}
	switch {
	case err == context.Canceled:
		j.status.State = "cancelled"
	case err != nil:
		j.status.State = "error"
		j.status.Message = err.Error()
	default:
		j.status.State = "done"
		j.status.Phase = "done"
	}
	finalStatus := j.status
	j.mu.Unlock()
	if j.store != nil {
		state := map[string]string{
			"done":      "completed",
			"cancelled": "cancelled",
			"error":     "failed",
		}[finalStatus.State]
		_ = j.store.finishScan(j.sessionID, state, finalStatus.Message, finalStatus)
	}
}

func cloneDuplicateGroups(groups []DuplicateGroup) []DuplicateGroup {
	cloned := append([]DuplicateGroup(nil), groups...)
	for index := range cloned {
		cloned[index].Files = append([]FileRecord(nil), cloned[index].Files...)
	}
	return cloned
}

func sortedRecords(records recordHeap) []FileRecord {
	result := append([]FileRecord(nil), records...)
	sort.Slice(result, func(i, k int) bool {
		if result[i].Size == result[k].Size {
			return result[i].Path < result[k].Path
		}
		return result[i].Size > result[k].Size
	})
	return result
}

func pathDepth(path string) int {
	cleaned := filepath.Clean(path)
	return len(strings.FieldsFunc(cleaned, func(r rune) bool {
		return r == '/' || r == '\\'
	}))
}

func (j *scanJob) folderView(path string) (FolderView, bool) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	current, ok := j.folders[path]
	if !ok {
		return FolderView{}, false
	}
	children := make([]FolderRecord, 0)
	for childPath, child := range j.folders {
		if childPath != path && filepath.Dir(childPath) == path {
			children = append(children, child)
		}
	}
	sort.Slice(children, func(i, k int) bool {
		if children[i].Size == children[k].Size {
			return children[i].Name < children[k].Name
		}
		return children[i].Size > children[k].Size
	})
	parent := ""
	if path != j.status.Root {
		parent = filepath.Dir(path)
	}
	return FolderView{Current: current, Parent: parent, Children: children}, true
}
