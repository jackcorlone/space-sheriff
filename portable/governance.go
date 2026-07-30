package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

type PolicyState struct {
	ActiveID string   `json:"activeId"`
	Policies []Policy `json:"policies"`
}

type AuditSummary struct {
	ID            string `json:"id"`
	State         string `json:"state"`
	StartedAt     int64  `json:"startedAt"`
	CompletedAt   int64  `json:"completedAt,omitempty"`
	PolicyID      string `json:"policyId"`
	PolicyVersion int    `json:"policyVersion"`
	PlannedBytes  int64  `json:"plannedBytes"`
	ReleasedBytes int64  `json:"releasedBytes"`
	ItemCount     int64  `json:"itemCount"`
	TrashedCount  int64  `json:"trashedCount"`
	FailedCount   int64  `json:"failedCount"`
	ChangedCount  int64  `json:"changedCount"`
	PendingCount  int64  `json:"pendingCount"`
}

type AuditItem struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	ModifiedAt int64  `json:"modifiedAt"`
	State      string `json:"state"`
	Error      string `json:"error,omitempty"`
}

type AuditDetail struct {
	AuditSummary
	Items []AuditItem `json:"items"`
}

type DatabaseHealth struct {
	SchemaVersion       int    `json:"schemaVersion"`
	DatabaseBytes       int64  `json:"databaseBytes"`
	WALBytes            int64  `json:"walBytes"`
	IndexedFiles        int64  `json:"indexedFiles"`
	ScanSessions        int64  `json:"scanSessions"`
	CleanupPlans        int64  `json:"cleanupPlans"`
	CleanupTransactions int64  `json:"cleanupTransactions"`
	GovernanceEvents    int64  `json:"governanceEvents"`
	Integrity           string `json:"integrity"`
}

func (s *Store) ensureBuiltInPolicies() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UnixNano()
	for _, policy := range builtInPolicies {
		document, err := json.Marshal(policy)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO policy_profiles(id, name, version, description, built_in, document, created_at, updated_at)
			 VALUES(?, ?, ?, ?, 1, ?, ?, ?)
			 ON CONFLICT(id) DO UPDATE SET name = excluded.name, version = excluded.version,
			 description = excluded.description, built_in = 1, document = excluded.document,
			 updated_at = excluded.updated_at`,
			policy.ID, policy.Name, policy.Version, policy.Description, string(document), now, now,
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(
		`INSERT INTO settings(key, value) VALUES('active_policy_id', 'balanced')
		 ON CONFLICT(key) DO NOTHING`,
	); err != nil {
		return err
	}
	var activeID string
	if err := tx.QueryRow(
		"SELECT value FROM settings WHERE key = 'active_policy_id'",
	).Scan(&activeID); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM policy_profiles WHERE id = ?", activeID).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if _, err := tx.Exec(
			"UPDATE settings SET value = 'balanced' WHERE key = 'active_policy_id'",
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) policies() (PolicyState, error) {
	var state PolicyState
	if err := s.db.QueryRow(
		"SELECT value FROM settings WHERE key = 'active_policy_id'",
	).Scan(&state.ActiveID); err != nil {
		return PolicyState{}, err
	}
	rows, err := s.db.Query(
		"SELECT document FROM policy_profiles ORDER BY built_in DESC, name, id",
	)
	if err != nil {
		return PolicyState{}, err
	}
	defer rows.Close()
	state.Policies = make([]Policy, 0)
	for rows.Next() {
		var document string
		if err := rows.Scan(&document); err != nil {
			return PolicyState{}, err
		}
		var policy Policy
		if err := json.Unmarshal([]byte(document), &policy); err != nil {
			return PolicyState{}, fmt.Errorf("读取策略文档: %w", err)
		}
		state.Policies = append(state.Policies, policy)
	}
	return state, rows.Err()
}

func (s *Store) activePolicy() (Policy, error) {
	var document string
	err := s.db.QueryRow(
		`SELECT p.document FROM policy_profiles p
		 JOIN settings s ON s.value = p.id
		 WHERE s.key = 'active_policy_id'`,
	).Scan(&document)
	if err != nil {
		return Policy{}, err
	}
	var policy Policy
	if err := json.Unmarshal([]byte(document), &policy); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

func (s *Store) activatePolicy(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow("SELECT COUNT(*) FROM policy_profiles WHERE id = ?", id).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return fmt.Errorf("策略不存在")
	}
	if _, err := tx.Exec(
		"UPDATE settings SET value = ? WHERE key = 'active_policy_id'", id,
	); err != nil {
		return err
	}
	if err := insertGovernanceEvent(tx, "policy_activated", map[string]any{"id": id}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) importPolicy(policy Policy) error {
	if err := validatePolicy(policy); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var builtIn bool
	var currentVersion int
	err = tx.QueryRow(
		"SELECT built_in, version FROM policy_profiles WHERE id = ?", policy.ID,
	).Scan(&builtIn, &currentVersion)
	switch {
	case err == nil && builtIn:
		return fmt.Errorf("不能覆盖内置策略")
	case err == nil && policy.Version <= currentVersion:
		return fmt.Errorf("新策略版本必须高于已保存的版本 %d", currentVersion)
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return err
	}
	policy.BuiltIn = false
	document, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	now := time.Now().UnixNano()
	if _, err := tx.Exec(
		`INSERT INTO policy_profiles(id, name, version, description, built_in, document, created_at, updated_at)
		 VALUES(?, ?, ?, ?, 0, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET name = excluded.name, version = excluded.version,
		 description = excluded.description, document = excluded.document, updated_at = excluded.updated_at`,
		policy.ID, policy.Name, policy.Version, policy.Description, string(document), now, now,
	); err != nil {
		return err
	}
	if err := insertGovernanceEvent(
		tx, "policy_imported", map[string]any{"id": policy.ID, "version": policy.Version},
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) recordGovernanceEvent(eventType string, details any) error {
	return insertGovernanceEvent(s.db, eventType, details)
}

type sqlExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func insertGovernanceEvent(executor sqlExecutor, eventType string, details any) error {
	id, err := newID()
	if err != nil {
		return err
	}
	document, err := json.Marshal(details)
	if err != nil {
		return err
	}
	if len(document) > 4096 {
		return fmt.Errorf("治理事件详情过长")
	}
	_, err = executor.Exec(
		`INSERT INTO governance_events(id, event_type, occurred_at, details)
		 VALUES(?, ?, ?, ?)`,
		id, eventType, time.Now().UnixNano(), string(document),
	)
	return err
}

func (s *Store) auditSummaries(limit int) ([]AuditSummary, error) {
	rows, err := s.db.Query(
		`SELECT t.id, t.state, t.started_at, COALESCE(t.completed_at, 0),
		 t.policy_id, t.policy_version, t.planned_bytes, t.released_bytes,
		 COUNT(i.path),
		 COALESCE(SUM(CASE WHEN i.state = 'trashed' THEN 1 ELSE 0 END), 0),
		 COALESCE(SUM(CASE WHEN i.state = 'failed' THEN 1 ELSE 0 END), 0),
		 COALESCE(SUM(CASE WHEN i.state = 'skipped_changed' THEN 1 ELSE 0 END), 0),
		 COALESCE(SUM(CASE WHEN i.state = 'pending' THEN 1 ELSE 0 END), 0)
		 FROM cleanup_transactions t
		 LEFT JOIN cleanup_items i ON i.transaction_id = t.id
		 GROUP BY t.id
		 ORDER BY t.started_at DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	summaries := make([]AuditSummary, 0)
	for rows.Next() {
		var summary AuditSummary
		if err := rows.Scan(
			&summary.ID, &summary.State, &summary.StartedAt, &summary.CompletedAt,
			&summary.PolicyID, &summary.PolicyVersion, &summary.PlannedBytes,
			&summary.ReleasedBytes, &summary.ItemCount, &summary.TrashedCount,
			&summary.FailedCount, &summary.ChangedCount, &summary.PendingCount,
		); err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

func (s *Store) auditDetail(id string) (AuditDetail, error) {
	var detail AuditDetail
	err := s.db.QueryRow(
		`SELECT id, state, started_at, COALESCE(completed_at, 0), policy_id,
		 policy_version, planned_bytes, released_bytes
		 FROM cleanup_transactions WHERE id = ?`, id,
	).Scan(
		&detail.ID, &detail.State, &detail.StartedAt, &detail.CompletedAt,
		&detail.PolicyID, &detail.PolicyVersion, &detail.PlannedBytes, &detail.ReleasedBytes,
	)
	if err != nil {
		return AuditDetail{}, err
	}
	rows, err := s.db.Query(
		`SELECT path, size, modified_at, state, error FROM cleanup_items
		 WHERE transaction_id = ? ORDER BY path`, id,
	)
	if err != nil {
		return AuditDetail{}, err
	}
	defer rows.Close()
	detail.Items = make([]AuditItem, 0)
	for rows.Next() {
		var item AuditItem
		if err := rows.Scan(
			&item.Path, &item.Size, &item.ModifiedAt, &item.State, &item.Error,
		); err != nil {
			return AuditDetail{}, err
		}
		detail.Items = append(detail.Items, item)
		switch item.State {
		case "trashed":
			detail.TrashedCount++
		case "failed":
			detail.FailedCount++
		case "skipped_changed":
			detail.ChangedCount++
		case "pending":
			detail.PendingCount++
		}
	}
	detail.ItemCount = int64(len(detail.Items))
	return detail, rows.Err()
}

func (s *Store) databaseHealth() (DatabaseHealth, error) {
	var health DatabaseHealth
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&health.SchemaVersion); err != nil {
		return DatabaseHealth{}, err
	}
	for query, target := range map[string]*int64{
		"SELECT COUNT(*) FROM files":                &health.IndexedFiles,
		"SELECT COUNT(*) FROM scan_sessions":        &health.ScanSessions,
		"SELECT COUNT(*) FROM cleanup_plan":         &health.CleanupPlans,
		"SELECT COUNT(*) FROM cleanup_transactions": &health.CleanupTransactions,
		"SELECT COUNT(*) FROM governance_events":    &health.GovernanceEvents,
	} {
		if err := s.db.QueryRow(query).Scan(target); err != nil {
			return DatabaseHealth{}, err
		}
	}
	if err := s.db.QueryRow("PRAGMA quick_check").Scan(&health.Integrity); err != nil {
		return DatabaseHealth{}, err
	}
	health.DatabaseBytes = fileSize(s.path)
	health.WALBytes = fileSize(s.path + "-wal")
	return health, nil
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func (s *Store) maintain(action string) error {
	switch action {
	case "checkpoint":
	case "optimize":
		if _, err := s.db.Exec("PRAGMA optimize"); err != nil {
			return err
		}
	default:
		return fmt.Errorf("不支持的维护动作")
	}
	var busy, logFrames, checkpointed int
	if err := s.db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(
		&busy, &logFrames, &checkpointed,
	); err != nil {
		return err
	}
	if busy != 0 {
		return fmt.Errorf("数据库正在忙，请稍后重试")
	}
	return s.recordGovernanceEvent(
		"database_maintenance",
		map[string]any{"action": action, "logFrames": logFrames, "checkpointed": checkpointed},
	)
}
