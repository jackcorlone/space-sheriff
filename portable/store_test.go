package main

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.db")
	store, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store, path
}

func TestStoreMigratesAndRejectsNewerSchema(t *testing.T) {
	store, path := testStore(t)
	var version int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := openStore(path); err == nil || !strings.Contains(err.Error(), "高于") {
		t.Fatalf("newer schema was not rejected: %v", err)
	}
}

func TestStoreMigratesV3ScheduleBudgets(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "v3.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE scan_schedules(id TEXT PRIMARY KEY); PRAGMA user_version = 3"); err != nil {
		t.Fatal(err)
	}
	store := &Store{db: db}
	if err := store.migrateV4(); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 4 {
		t.Fatalf("migrated schema version = %d, want 4", version)
	}
	for _, column := range []string{"max_bytes", "max_duration_seconds"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('scan_schedules') WHERE name = ?", column).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("migration did not add %s", column)
		}
	}
}

func TestStoreHashCacheRequiresMatchingMetadata(t *testing.T) {
	store, _ := testStore(t)
	sessionID, err := store.beginScan(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := FileRecord{
		Path:             "/file.bin",
		Identity:         "device:inode",
		Size:             10,
		ModifiedUnixNano: 20,
		PhysicalSize:     10,
		LinkCount:        1,
	}
	if err := store.saveFile(record, "abc", sessionID); err != nil {
		t.Fatal(err)
	}
	hash, ok, err := store.cachedHash(record)
	if err != nil || !ok || hash != "abc" {
		t.Fatalf("cache miss: hash=%q ok=%v err=%v", hash, ok, err)
	}
	record.Size++
	if _, ok, err := store.cachedHash(record); err != nil || ok {
		t.Fatalf("changed metadata reused cache: ok=%v err=%v", ok, err)
	}
}

func TestStoreSaveFilesContextCancellationIsAtomic(t *testing.T) {
	store, _ := testStore(t)
	sessionID, err := store.beginScan(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	records := []FileRecord{
		{Identity: "one", Path: "/one", Size: 1, ModifiedUnixNano: 1},
		{Identity: "two", Path: "/two", Size: 2, ModifiedUnixNano: 2},
	}
	if err := store.saveFilesContext(ctx, records, sessionID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled batch got %v, want context canceled", err)
	}
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM files").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("cancelled batch partially committed %d files", count)
	}
}

func TestStorePersistsCleanupPlan(t *testing.T) {
	store, path := testStore(t)
	empty, err := store.loadPlan()
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty plan must be a non-nil slice: %#v, %v", empty, err)
	}
	record := FileRecord{
		Path:             "/candidate.bin",
		Identity:         "identity",
		Size:             42,
		ModifiedAt:       "2026-07-30 10:00",
		ModifiedUnixNano: 123,
		PhysicalSize:     4096,
		LinkCount:        1,
		Advice: Advice{
			Level: "review", Label: "需人工确认", Reason: "测试",
			Score: 40, RuleID: "TEST", Category: "其他",
		},
	}
	if err := store.replacePlan([]FileRecord{record}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	records, err := reopened.loadPlan()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Identity != record.Identity ||
		records[0].ModifiedUnixNano != record.ModifiedUnixNano ||
		records[0].Advice.RuleID != record.Advice.RuleID {
		t.Fatalf("unexpected restored plan: %+v", records)
	}
}

func TestStoreTracksAndRecoversScanSessions(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("SPACE_SHERIFF_DATA_DIR", dataDir)
	resolved, err := defaultDataDir()
	if err != nil || resolved != dataDir {
		t.Fatalf("data dir = %q, err=%v", resolved, err)
	}
	path := filepath.Join(dataDir, "index.db")
	store, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := store.beginScan(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.setScanState(sessionID, "hashing"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(
		"UPDATE scan_sessions SET started_at = ? WHERE id = ?",
		time.Now().Add(-scheduleLeaseTTL-time.Minute).UnixNano(), sessionID,
	); err != nil {
		t.Fatal(err)
	}
	completedID, err := store.beginScan(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	completedStatus := ScanStatus{
		FilesSeen: 3, BytesSeen: 30, Errors: 1, Excluded: 2, HashesReused: 1,
	}
	if err := store.finishScan(completedID, "completed", "", completedStatus); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var state, message string
	if err := reopened.db.QueryRow(
		"SELECT state, message FROM scan_sessions WHERE id = ?", sessionID,
	).Scan(&state, &message); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || message != "上次进程中断" {
		t.Fatalf("unexpected recovered scan: state=%q message=%q", state, message)
	}
}

func TestStoreDoesNotRecoverRecentWorkFromAnotherProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	first, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	sessionID, err := first.beginScan(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var state string
	if err := second.db.QueryRow(
		"SELECT state FROM scan_sessions WHERE id = ?", sessionID,
	).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "walking" {
		t.Fatalf("recent concurrent scan was changed to %q", state)
	}
}

func TestStoreDoesNotRecoverLongRunningScheduledScanWithFreshLease(t *testing.T) {
	store, _ := testStore(t)
	sessionID, err := store.beginScanWithContext(
		t.TempDir(), "scheduled-id", "scheduled", balancedPolicy(),
	)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-scheduleLeaseTTL - time.Minute)
	if _, err := store.db.Exec(
		"UPDATE scan_sessions SET started_at = ? WHERE id = ?",
		old.UnixNano(), sessionID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(
		"INSERT INTO schedule_leases(schedule_id, owner, acquired_at) VALUES(?, ?, ?)",
		"scheduled-id", "live-owner", time.Now().UnixNano(),
	); err != nil {
		t.Fatal(err)
	}
	if err := store.recoverInterrupted(); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := store.db.QueryRow(
		"SELECT state FROM scan_sessions WHERE id = ?", sessionID,
	).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "walking" {
		t.Fatalf("fresh-lease scheduled scan was changed to %q", state)
	}
}

func TestStoreCleanupJournalAndRecovery(t *testing.T) {
	store, path := testStore(t)
	record := FileRecord{Path: "/candidate.bin", Identity: "identity", Size: 42, ModifiedUnixNano: 123}
	transactionID, err := store.beginCleanup([]FileRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := store.cleanupTransaction(transactionID)
	if err != nil {
		t.Fatal(err)
	}
	if transaction.State != "executing" || transaction.PlannedBytes != 42 {
		t.Fatalf("unexpected prepared transaction: %+v", transaction)
	}
	if _, err := store.db.Exec(
		"UPDATE cleanup_transactions SET started_at = ? WHERE id = ?",
		time.Now().Add(-scheduleLeaseTTL-time.Minute).UnixNano(), transactionID,
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	transaction, err = reopened.cleanupTransaction(transactionID)
	if err != nil {
		t.Fatal(err)
	}
	if transaction.State != "interrupted" {
		t.Fatalf("recovered state = %q, want interrupted", transaction.State)
	}

	completedID, err := reopened.beginCleanup([]FileRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.markCleanupItem(completedID, record.Path, "trashed", "", record.Size); err != nil {
		t.Fatal(err)
	}
	if err := reopened.finishCleanup(completedID, "completed"); err != nil {
		t.Fatal(err)
	}
	completed, err := reopened.cleanupTransaction(completedID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != "completed" || completed.ReleasedBytes != record.Size {
		t.Fatalf("unexpected completed transaction: %+v", completed)
	}
}
