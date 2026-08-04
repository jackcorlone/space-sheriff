package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
				return nil, nil, ctx.Err()
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
			j.mu.Unlock()
			if err != nil && !errors.Is(err, context.Canceled) {
				j.noteError("hash", record.Path, err)
			}
			if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, nil, ctx.Err()
			}
			if err == nil {
				byHash[hash] = append(byHash[hash], record)
			}
		}
	}

	return duplicateGroupsFromHashes(byHash), duplicatePathsFromHashes(byHash), nil
}

func (j *scanJob) findStoredDuplicates(
	ctx context.Context, minimum int64, policy Policy, now time.Time,
) ([]DuplicateGroup, map[string]string, error) {
	sizes, err := j.store.duplicateSizes(ctx, j.sessionID, minimum)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			j.noteError("index", "", err)
		}
		return nil, nil, err
	}
	groups := make([]DuplicateGroup, 0)
	byPath := make(map[string]string)
	for _, size := range sizes {
		records, err := j.store.filesBySize(ctx, j.sessionID, size)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				j.noteError("index", "", err)
			}
			return nil, nil, err
		}
		quickGroups := make(map[string][]FileRecord)
		for _, record := range records {
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			default:
			}
			record.Advice = adviseWithPolicy(policy, record.Path, record.Size,
				time.Unix(0, record.ModifiedUnixNano), now)
			fingerprint, fingerprintErr := quickFingerprint(ctx, record.Path, record.Size)
			j.mu.Lock()
			j.status.FilesFingerprinted++
			j.status.CurrentPath = record.Path
			j.mu.Unlock()
			if fingerprintErr != nil && !errors.Is(fingerprintErr, context.Canceled) {
				j.noteError("fingerprint", record.Path, fingerprintErr)
			}
			if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, nil, ctx.Err()
			}
			if fingerprintErr != nil {
				continue
			}
			quickGroups[fingerprint] = append(quickGroups[fingerprint], record)
		}
		for _, candidates := range quickGroups {
			if len(candidates) < 2 {
				continue
			}
			contentGroups, err := j.hashDuplicateRecords(ctx, candidates)
			if err != nil {
				return nil, nil, err
			}
			groups = append(groups, contentGroups...)
			for _, group := range contentGroups {
				for _, record := range group.Files {
					byPath[record.Path] = group.ID
				}
			}
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

func (j *scanJob) hashDuplicateRecords(ctx context.Context, candidates []FileRecord) ([]DuplicateGroup, error) {
	byHash := make(map[string][]FileRecord)
	pending := make([]fileHashRecord, 0, 256)
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		if err := j.store.saveFileHashes(ctx, pending, j.sessionID); err != nil {
			if !errors.Is(err, context.Canceled) {
				j.noteError("index", "", err)
			}
			return err
		}
		pending = pending[:0]
		return nil
	}
	for _, record := range candidates {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		hash, reused, err := j.store.cachedHash(record)
		if err == nil && !reused {
			hash, err = hashFile(ctx, record.Path)
		}
		j.mu.Lock()
		if reused {
			j.status.HashesReused++
		} else {
			j.status.FilesHashed++
		}
		j.status.CurrentPath = record.Path
		j.mu.Unlock()
		if err != nil && !errors.Is(err, context.Canceled) {
			j.noteError("hash", record.Path, err)
		}
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ctx.Err()
		}
		if err != nil {
			continue
		}
		byHash[hash] = append(byHash[hash], record)
		if !reused {
			pending = append(pending, fileHashRecord{record: record, hash: hash})
			if len(pending) == cap(pending) {
				if err := flush(); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return duplicateGroupsFromHashes(byHash), nil
}

func duplicateGroupsFromHashes(byHash map[string][]FileRecord) []DuplicateGroup {
	groups := make([]DuplicateGroup, 0)
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
	}
	sort.Slice(groups, func(i, k int) bool {
		if groups[i].Reclaimable == groups[k].Reclaimable {
			return groups[i].ID < groups[k].ID
		}
		return groups[i].Reclaimable > groups[k].Reclaimable
	})
	return groups
}

func duplicatePathsFromHashes(byHash map[string][]FileRecord) map[string]string {
	byPath := make(map[string]string)
	for hash, records := range byHash {
		if len(records) < 2 {
			continue
		}
		id := hash[:16]
		for _, record := range records {
			byPath[record.Path] = id
		}
	}
	return byPath
}

const quickFingerprintChunkSize = 64 * 1024

func quickFingerprint(ctx context.Context, path string, size int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() != size {
		return "", fmt.Errorf("文件在扫描期间发生变化")
	}
	hash := sha256.New()
	var sizeBuffer [8]byte
	binary.BigEndian.PutUint64(sizeBuffer[:], uint64(size))
	_, _ = hash.Write(sizeBuffer[:])
	chunkSize := int64(quickFingerprintChunkSize)
	if size <= chunkSize*2 {
		_, err = io.Copy(hash, contextReader{ctx: ctx, reader: file})
		if err != nil {
			return "", err
		}
		return hex.EncodeToString(hash.Sum(nil)), nil
	}
	buffer := make([]byte, quickFingerprintChunkSize)
	if _, err := io.ReadFull(contextReader{ctx: ctx, reader: file}, buffer); err != nil {
		return "", err
	}
	_, _ = hash.Write(buffer)
	if _, err := file.Seek(-chunkSize, io.SeekEnd); err != nil {
		return "", err
	}
	if _, err := io.ReadFull(contextReader{ctx: ctx, reader: file}, buffer); err != nil {
		return "", err
	}
	_, _ = hash.Write(buffer)
	return hex.EncodeToString(hash.Sum(nil)), nil
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
