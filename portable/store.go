package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schemaVersion = 4
const scanSessionRetention = 90 * 24 * time.Hour

type Store struct {
	db   *sql.DB
	path string
}

type cleanupTransaction struct {
	ID            string
	State         string
	PlannedBytes  int64
	ReleasedBytes int64
}

func defaultDataDir() (string, error) {
	if directory := os.Getenv("SPACE_SHERIFF_DATA_DIR"); directory != "" {
		return filepath.Abs(directory)
	}
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "SpaceSheriff"), nil
}

func openStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("创建数据目录: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, path: path}
	if err := store.configure(); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.recoverInterrupted(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) configure() error {
	for _, statement := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = FULL",
	} {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("配置数据库: %w", err)
		}
	}
	return nil
}

func (s *Store) migrate() error {
	var current int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("读取数据库版本: %w", err)
	}
	if current > schemaVersion {
		return fmt.Errorf("数据库版本 %d 高于应用支持的版本 %d", current, schemaVersion)
	}
	for current < schemaVersion {
		switch current {
		case 0:
			if err := s.migrateV1(); err != nil {
				return err
			}
		case 1:
			if err := s.migrateV2(); err != nil {
				return err
			}
		case 2:
			if err := s.migrateV3(); err != nil {
				return err
			}
		case 3:
			if err := s.migrateV4(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("不支持从数据库版本 %d 迁移", current)
		}
		current++
	}
	return s.ensureBuiltInPolicies()
}

func (s *Store) migrateV1() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	schema := `
CREATE TABLE scan_sessions (
	id TEXT PRIMARY KEY,
	root TEXT NOT NULL,
	state TEXT NOT NULL,
	started_at INTEGER NOT NULL,
	completed_at INTEGER,
	files_seen INTEGER NOT NULL DEFAULT 0,
	bytes_seen INTEGER NOT NULL DEFAULT 0,
	errors INTEGER NOT NULL DEFAULT 0,
	excluded INTEGER NOT NULL DEFAULT 0,
	hashes_reused INTEGER NOT NULL DEFAULT 0,
	message TEXT NOT NULL DEFAULT ''
);
CREATE TABLE files (
	identity TEXT PRIMARY KEY,
	path TEXT NOT NULL,
	size INTEGER NOT NULL,
	modified_at INTEGER NOT NULL,
	physical_size INTEGER NOT NULL,
	link_count INTEGER NOT NULL,
	sha256 TEXT NOT NULL DEFAULT '',
	hash_updated_at INTEGER,
	last_seen_session TEXT,
	FOREIGN KEY(last_seen_session) REFERENCES scan_sessions(id) ON DELETE SET NULL
);
CREATE INDEX files_path_index ON files(path);
CREATE TABLE cleanup_plan (
	path TEXT PRIMARY KEY,
	identity TEXT NOT NULL,
	size INTEGER NOT NULL,
	modified_at INTEGER NOT NULL,
	physical_size INTEGER NOT NULL,
	link_count INTEGER NOT NULL,
	modified_display TEXT NOT NULL,
	advice_level TEXT NOT NULL,
	advice_label TEXT NOT NULL,
	advice_reason TEXT NOT NULL,
	advice_score INTEGER NOT NULL,
	advice_rule_id TEXT NOT NULL,
	advice_category TEXT NOT NULL,
	added_at INTEGER NOT NULL
);
CREATE TABLE cleanup_transactions (
	id TEXT PRIMARY KEY,
	state TEXT NOT NULL,
	started_at INTEGER NOT NULL,
	completed_at INTEGER,
	planned_bytes INTEGER NOT NULL,
	released_bytes INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE cleanup_items (
	transaction_id TEXT NOT NULL,
	path TEXT NOT NULL,
	identity TEXT NOT NULL,
	size INTEGER NOT NULL,
	modified_at INTEGER NOT NULL,
	state TEXT NOT NULL,
	error TEXT NOT NULL DEFAULT '',
	PRIMARY KEY(transaction_id, path),
	FOREIGN KEY(transaction_id) REFERENCES cleanup_transactions(id) ON DELETE CASCADE
);`
	if _, err := tx.Exec(schema); err != nil {
		return fmt.Errorf("创建数据库结构: %w", err)
	}
	if _, err := tx.Exec("PRAGMA user_version = 1"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) migrateV2() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	schema := `
ALTER TABLE cleanup_transactions ADD COLUMN policy_id TEXT NOT NULL DEFAULT 'balanced';
ALTER TABLE cleanup_transactions ADD COLUMN policy_version INTEGER NOT NULL DEFAULT 1;
CREATE TABLE policy_profiles (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	version INTEGER NOT NULL,
	description TEXT NOT NULL,
	built_in INTEGER NOT NULL,
	document TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE governance_events (
	id TEXT PRIMARY KEY,
	event_type TEXT NOT NULL,
	occurred_at INTEGER NOT NULL,
	details TEXT NOT NULL
);
CREATE INDEX governance_events_time_index ON governance_events(occurred_at DESC);`
	if _, err := tx.Exec(schema); err != nil {
		return fmt.Errorf("迁移数据库到 v2: %w", err)
	}
	if _, err := tx.Exec("PRAGMA user_version = 2"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) migrateV3() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	schema := `
ALTER TABLE scan_sessions ADD COLUMN schedule_id TEXT NOT NULL DEFAULT '';
ALTER TABLE scan_sessions ADD COLUMN trigger TEXT NOT NULL DEFAULT 'interactive';
ALTER TABLE scan_sessions ADD COLUMN policy_id TEXT NOT NULL DEFAULT 'balanced';
ALTER TABLE scan_sessions ADD COLUMN policy_version INTEGER NOT NULL DEFAULT 1;
CREATE TABLE scan_schedules (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	root TEXT NOT NULL,
	cadence TEXT NOT NULL,
	hour INTEGER NOT NULL,
	minute INTEGER NOT NULL,
	weekday INTEGER NOT NULL,
	minimum_bytes INTEGER NOT NULL,
	duplicate_minimum_bytes INTEGER NOT NULL,
	result_limit INTEGER NOT NULL,
	excludes_json TEXT NOT NULL,
	enabled INTEGER NOT NULL,
	backend_state TEXT NOT NULL,
	backend_error TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	last_run_at INTEGER
);
CREATE TABLE scan_findings (
	session_id TEXT NOT NULL,
	path TEXT NOT NULL,
	size INTEGER NOT NULL,
	modified_at INTEGER NOT NULL,
	modified_display TEXT NOT NULL,
	advice_level TEXT NOT NULL,
	advice_label TEXT NOT NULL,
	advice_reason TEXT NOT NULL,
	advice_rule_id TEXT NOT NULL,
	advice_category TEXT NOT NULL,
	PRIMARY KEY(session_id, path),
	FOREIGN KEY(session_id) REFERENCES scan_sessions(id) ON DELETE CASCADE
);
CREATE TABLE schedule_leases (
	schedule_id TEXT PRIMARY KEY,
	owner TEXT NOT NULL,
	acquired_at INTEGER NOT NULL
);
CREATE INDEX scan_sessions_schedule_time_index
	ON scan_sessions(schedule_id, started_at DESC);`
	if _, err := tx.Exec(schema); err != nil {
		return fmt.Errorf("迁移数据库到 v3: %w", err)
	}
	if _, err := tx.Exec("PRAGMA user_version = 3"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) migrateV4() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
ALTER TABLE scan_schedules ADD COLUMN max_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scan_schedules ADD COLUMN max_duration_seconds INTEGER NOT NULL DEFAULT 0;`); err != nil {
		return fmt.Errorf("迁移数据库到 v4: %w", err)
	}
	if _, err := tx.Exec("PRAGMA user_version = 4"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) recoverInterrupted() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UnixNano()
	if _, err := tx.Exec(
		`UPDATE scan_sessions SET state = 'failed', completed_at = ?, message = '上次进程中断'
			 WHERE state IN ('walking', 'hashing') AND started_at < ?
			   AND NOT EXISTS (
					SELECT 1 FROM schedule_leases sl
					 WHERE sl.schedule_id = scan_sessions.schedule_id
					   AND scan_sessions.schedule_id <> ''
					   AND sl.acquired_at >= ?
				)`,
		now, time.Now().Add(-scheduleLeaseTTL).UnixNano(), time.Now().Add(-scheduleLeaseTTL).UnixNano(),
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE cleanup_transactions SET state = 'interrupted', completed_at = ?
		 WHERE state IN ('prepared', 'executing') AND started_at < ?`,
		now, time.Now().Add(-scheduleLeaseTTL).UnixNano(),
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`DELETE FROM scan_sessions
		 WHERE completed_at IS NOT NULL AND completed_at < ?`,
		time.Now().Add(-scanSessionRetention).UnixNano(),
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) beginScan(root string) (string, error) {
	return s.beginScanWithContext(root, "", "interactive", balancedPolicy())
}

func (s *Store) beginScanWithContext(root, scheduleID, trigger string, policy Policy) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(
		`INSERT INTO scan_sessions(
		 id, root, state, started_at, schedule_id, trigger, policy_id, policy_version)
		 VALUES(?, ?, 'walking', ?, ?, ?, ?, ?)`,
		id, root, time.Now().UnixNano(), scheduleID, trigger, policy.ID, policy.Version,
	)
	return id, err
}

func (s *Store) setScanState(id, state string) error {
	_, err := s.db.Exec("UPDATE scan_sessions SET state = ? WHERE id = ?", state, id)
	return err
}

func (s *Store) finishScan(id, state, message string, status ScanStatus) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`UPDATE scan_sessions SET state = ?, completed_at = ?, files_seen = ?, bytes_seen = ?,
		 errors = ?, excluded = ?, hashes_reused = ?, message = ? WHERE id = ?`,
		state, time.Now().UnixNano(), status.FilesSeen, status.BytesSeen,
		status.Errors, status.Excluded, status.HashesReused, message, id,
	); err != nil {
		return err
	}
	statement, err := tx.Prepare(
		`INSERT INTO scan_findings(
		 session_id, path, size, modified_at, modified_display, advice_level,
		 advice_label, advice_reason, advice_rule_id, advice_category)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer statement.Close()
	for _, record := range status.Results {
		if _, err := statement.Exec(
			id, record.Path, record.Size, record.ModifiedUnixNano, record.ModifiedAt,
			record.Advice.Level, record.Advice.Label, record.Advice.Reason,
			record.Advice.RuleID, record.Advice.Category,
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(
		`UPDATE scan_schedules SET last_run_at = ? WHERE id =
		 (SELECT schedule_id FROM scan_sessions WHERE id = ?)`,
		time.Now().UnixNano(), id,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) cachedHash(record FileRecord) (string, bool, error) {
	if record.Identity == "" {
		return "", false, nil
	}
	var hash string
	err := s.db.QueryRow(
		`SELECT sha256 FROM files
		 WHERE identity = ? AND size = ? AND modified_at = ? AND sha256 <> ''`,
		record.Identity, record.Size, record.ModifiedUnixNano,
	).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return hash, err == nil, err
}

func (s *Store) duplicateSizes(ctx context.Context, sessionID string, minimum int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT size FROM files
		 WHERE last_seen_session = ? AND size >= ?
		 GROUP BY size HAVING COUNT(*) > 1 ORDER BY size`, sessionID, minimum,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sizes := make([]int64, 0)
	for rows.Next() {
		var size int64
		if err := rows.Scan(&size); err != nil {
			return nil, err
		}
		sizes = append(sizes, size)
	}
	return sizes, rows.Err()
}

func (s *Store) filesBySize(ctx context.Context, sessionID string, size int64) ([]FileRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT identity, path, size, modified_at, physical_size, link_count
		 FROM files WHERE last_seen_session = ? AND size = ? ORDER BY path`, sessionID, size,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]FileRecord, 0)
	for rows.Next() {
		var record FileRecord
		if err := rows.Scan(
			&record.Identity, &record.Path, &record.Size, &record.ModifiedUnixNano,
			&record.PhysicalSize, &record.LinkCount,
		); err != nil {
			return nil, err
		}
		record.ModifiedAt = time.Unix(0, record.ModifiedUnixNano).Format("2006-01-02 15:04")
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) saveFile(record FileRecord, hash, sessionID string) error {
	return s.saveFileHashes(context.Background(), []fileHashRecord{{record: record, hash: hash}}, sessionID)
}

func (s *Store) saveFiles(records []FileRecord, sessionID string) error {
	return s.saveFilesContext(context.Background(), records, sessionID)
}

func (s *Store) saveFilesWithHash(records []FileRecord, hash, sessionID string) error {
	hashed := make([]fileHashRecord, 0, len(records))
	for _, record := range records {
		hashed = append(hashed, fileHashRecord{record: record, hash: hash})
	}
	return s.saveFileHashes(context.Background(), hashed, sessionID)
}

func (s *Store) saveFilesContext(ctx context.Context, records []FileRecord, sessionID string) error {
	hashed := make([]fileHashRecord, 0, len(records))
	for _, record := range records {
		hashed = append(hashed, fileHashRecord{record: record})
	}
	return s.saveFileHashes(ctx, hashed, sessionID)
}

type fileHashRecord struct {
	record FileRecord
	hash   string
}

func (s *Store) saveFileHashes(ctx context.Context, records []fileHashRecord, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statement, err := tx.PrepareContext(ctx,
		`INSERT INTO files(identity, path, size, modified_at, physical_size, link_count,
		 sha256, hash_updated_at, last_seen_session)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(identity) DO UPDATE SET
		 path = excluded.path,
		 sha256 = CASE
		   WHEN files.size = excluded.size AND files.modified_at = excluded.modified_at
		        AND excluded.sha256 = '' THEN files.sha256
		   ELSE excluded.sha256
		 END,
		 hash_updated_at = CASE
		   WHEN files.size = excluded.size AND files.modified_at = excluded.modified_at
		        AND excluded.sha256 = '' THEN files.hash_updated_at
		   ELSE excluded.hash_updated_at
		 END,
		 size = excluded.size,
		 modified_at = excluded.modified_at,
		 physical_size = excluded.physical_size,
		 link_count = excluded.link_count,
		 last_seen_session = excluded.last_seen_session`,
	)
	if err != nil {
		return err
	}
	defer statement.Close()
	for _, item := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		record := item.record
		if record.Identity == "" {
			continue
		}
		var hashUpdated any
		if item.hash != "" {
			hashUpdated = time.Now().UnixNano()
		}
		if _, err := statement.ExecContext(ctx,
			record.Identity, record.Path, record.Size, record.ModifiedUnixNano,
			record.PhysicalSize, record.LinkCount, item.hash, hashUpdated, sessionID,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) replacePlan(records []FileRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM cleanup_plan"); err != nil {
		return err
	}
	statement, err := tx.Prepare(
		`INSERT INTO cleanup_plan(path, identity, size, modified_at, physical_size, link_count,
		 modified_display, advice_level, advice_label, advice_reason, advice_score,
		 advice_rule_id, advice_category, added_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer statement.Close()
	now := time.Now().UnixNano()
	for _, record := range records {
		if _, err := statement.Exec(
			record.Path, record.Identity, record.Size, record.ModifiedUnixNano,
			record.PhysicalSize, record.LinkCount, record.ModifiedAt,
			record.Advice.Level, record.Advice.Label, record.Advice.Reason,
			record.Advice.Score, record.Advice.RuleID, record.Advice.Category, now,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) loadPlan() ([]FileRecord, error) {
	rows, err := s.db.Query(
		`SELECT path, identity, size, modified_at, physical_size, link_count, modified_display,
		 advice_level, advice_label, advice_reason, advice_score, advice_rule_id, advice_category
		 FROM cleanup_plan ORDER BY added_at, path`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]FileRecord, 0)
	for rows.Next() {
		var record FileRecord
		if err := rows.Scan(
			&record.Path, &record.Identity, &record.Size, &record.ModifiedUnixNano,
			&record.PhysicalSize, &record.LinkCount, &record.ModifiedAt,
			&record.Advice.Level, &record.Advice.Label, &record.Advice.Reason,
			&record.Advice.Score, &record.Advice.RuleID, &record.Advice.Category,
		); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) beginCleanup(records []FileRecord) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	policy, err := s.activePolicy()
	if err != nil {
		return "", err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var planned int64
	for _, record := range records {
		planned += record.Size
	}
	if _, err := tx.Exec(
		`INSERT INTO cleanup_transactions(
		 id, state, started_at, planned_bytes, policy_id, policy_version)
		 VALUES(?, 'prepared', ?, ?, ?, ?)`,
		id, time.Now().UnixNano(), planned, policy.ID, policy.Version,
	); err != nil {
		return "", err
	}
	for _, record := range records {
		if _, err := tx.Exec(
			`INSERT INTO cleanup_items(transaction_id, path, identity, size, modified_at, state)
			 VALUES(?, ?, ?, ?, ?, 'pending')`,
			id, record.Path, record.Identity, record.Size, record.ModifiedUnixNano,
		); err != nil {
			return "", err
		}
	}
	if _, err := tx.Exec(
		"UPDATE cleanup_transactions SET state = 'executing' WHERE id = ?", id,
	); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) markCleanupItem(transactionID, path, state, itemError string, released int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`UPDATE cleanup_items SET state = ?, error = ? WHERE transaction_id = ? AND path = ?`,
		state, itemError, transactionID, path,
	); err != nil {
		return err
	}
	if released > 0 {
		if _, err := tx.Exec(
			`UPDATE cleanup_transactions SET released_bytes = released_bytes + ? WHERE id = ?`,
			released, transactionID,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) finishCleanup(id, state string) error {
	_, err := s.db.Exec(
		`UPDATE cleanup_transactions SET state = ?, completed_at = ? WHERE id = ?`,
		state, time.Now().UnixNano(), id,
	)
	return err
}

func (s *Store) cleanupTransaction(id string) (cleanupTransaction, error) {
	var result cleanupTransaction
	err := s.db.QueryRow(
		`SELECT id, state, planned_bytes, released_bytes FROM cleanup_transactions WHERE id = ?`, id,
	).Scan(&result.ID, &result.State, &result.PlannedBytes, &result.ReleasedBytes)
	return result, err
}
