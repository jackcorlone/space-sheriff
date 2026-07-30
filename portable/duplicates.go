package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type excludeMatcher struct {
	names map[string]bool
	paths []string
}

func newExcludeMatcher(root string, values []string) excludeMatcher {
	matcher := excludeMatcher{names: make(map[string]bool)}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !filepath.IsAbs(value) && !strings.ContainsAny(value, `/\`) {
			matcher.names[strings.ToLower(value)] = true
			continue
		}
		if !filepath.IsAbs(value) {
			value = filepath.Join(root, value)
		}
		if absolute, err := filepath.Abs(filepath.Clean(value)); err == nil {
			matcher.paths = append(matcher.paths, absolute)
		}
	}
	return matcher
}

func (m excludeMatcher) match(path string) bool {
	if m.names[strings.ToLower(filepath.Base(path))] {
		return true
	}
	for _, excluded := range m.paths {
		if path == excluded || strings.HasPrefix(path, excluded+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (j *scanJob) findDuplicates(ctx context.Context, candidates []FileRecord) ([]DuplicateGroup, map[string]string, error) {
	bySize := make(map[int64][]FileRecord)
	for _, record := range candidates {
		bySize[record.Size] = append(bySize[record.Size], record)
	}
	byHash := make(map[string][]FileRecord)
	for _, records := range bySize {
		if len(records) < 2 {
			continue
		}
		for _, record := range records {
			select {
			case <-ctx.Done():
				return nil, nil, context.Canceled
			default:
			}
			hash := ""
			reused := false
			var err error
			if j.store != nil {
				hash, reused, err = j.store.cachedHash(record)
			}
			if err == nil && !reused {
				hash, err = hashFile(ctx, record.Path)
				if err == nil && j.store != nil {
					err = j.store.saveFile(record, hash, j.sessionID)
				}
			}
			j.mu.Lock()
			if reused {
				j.status.HashesReused++
			} else {
				j.status.FilesHashed++
			}
			j.status.CurrentPath = record.Path
			if err != nil && err != context.Canceled {
				j.status.Errors++
			}
			j.mu.Unlock()
			if err == context.Canceled {
				return nil, nil, context.Canceled
			}
			if err == nil {
				byHash[hash] = append(byHash[hash], record)
			}
		}
	}

	groups := make([]DuplicateGroup, 0)
	byPath := make(map[string]string)
	for hash, records := range byHash {
		if len(records) < 2 {
			continue
		}
		sort.Slice(records, func(i, k int) bool {
			if records[i].ModifiedUnixNano == records[k].ModifiedUnixNano {
				return records[i].Path < records[k].Path
			}
			return records[i].ModifiedUnixNano > records[k].ModifiedUnixNano
		})
		id := hash[:16]
		group := DuplicateGroup{
			ID:          id,
			Size:        records[0].Size,
			Reclaimable: records[0].Size * int64(len(records)-1),
			Files:       records,
		}
		groups = append(groups, group)
		for _, record := range records {
			byPath[record.Path] = id
		}
	}
	sort.Slice(groups, func(i, k int) bool {
		if groups[i].Reclaimable == groups[k].Reclaimable {
			return groups[i].ID < groups[k].ID
		}
		return groups[i].Reclaimable > groups[k].Reclaimable
	})
	return groups, byPath, nil
}

func hashFile(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, contextReader{ctx: ctx, reader: file}); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(buffer)
	}
}
