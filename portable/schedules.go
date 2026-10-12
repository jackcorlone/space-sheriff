package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const scheduleLeaseTTL = 6 * time.Hour

var errScheduleBusy = errors.New("该计划已有扫描正在运行")

type ScanSchedule struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Root               string   `json:"root"`
	Cadence            string   `json:"cadence"`
	Hour               int      `json:"hour"`
	Minute             int      `json:"minute"`
	Weekday            int      `json:"weekday"`
	Minimum            int64    `json:"minimum"`
	DuplicateMinimum   int64    `json:"duplicateMinimum"`
	ResultLimit        int      `json:"resultLimit"`
	MaxBytes           int64    `json:"maxBytes,omitempty"`
	MaxDurationSeconds int64    `json:"maxDurationSeconds,omitempty"`
	Excludes           []string `json:"excludes"`
	Enabled            bool     `json:"enabled"`
	BackendState       string   `json:"backendState"`
	BackendError       string   `json:"backendError,omitempty"`
	CreatedAt          int64    `json:"createdAt"`
	UpdatedAt          int64    `json:"updatedAt"`
	LastRunAt          int64    `json:"lastRunAt,omitempty"`
	NextRunAt          int64    `json:"nextRunAt,omitempty"`
	MissedRuns         int      `json:"missedRuns,omitempty"`
	LastRunState       string   `json:"lastRunState,omitempty"`
	LastRunMessage     string   `json:"lastRunMessage,omitempty"`
	DriftState         string   `json:"driftState,omitempty"`
	DriftMessage       string   `json:"driftMessage,omitempty"`
}

type ScheduledScanSummary struct {
	ID            string `json:"id"`
	ScheduleID    string `json:"scheduleId"`
	ScheduleName  string `json:"scheduleName"`
	Root          string `json:"root"`
	State         string `json:"state"`
	Trigger       string `json:"trigger"`
	StartedAt     int64  `json:"startedAt"`
	CompletedAt   int64  `json:"completedAt,omitempty"`
	FilesSeen     int64  `json:"filesSeen"`
	BytesSeen     int64  `json:"bytesSeen"`
	Errors        int64  `json:"errors"`
	ResultCount   int    `json:"resultCount"`
	PolicyID      string `json:"policyId"`
	PolicyVersion int    `json:"policyVersion"`
	Message       string `json:"message,omitempty"`
}

type ScheduledScanDetail struct {
	Summary  ScheduledScanSummary `json:"summary"`
	Findings []FileRecord         `json:"findings"`
}

type ScheduleAlert struct {
	ScheduleID   string `json:"scheduleId"`
	ScheduleName string `json:"scheduleName"`
	Kind         string `json:"kind"`
	Message      string `json:"message"`
	OccurredAt   int64  `json:"occurredAt,omitempty"`
}

type scheduleInspection struct {
	State   string
	Message string
}

type scheduleInspector interface {
	Inspect(ScanSchedule, string, string) (scheduleInspection, error)
}

type scheduleBackend interface {
	Install(ScanSchedule, string, string) error
	Remove(string) error
	Name() string
}

func validateSchedule(schedule *ScanSchedule) error {
	schedule.Name = strings.TrimSpace(schedule.Name)
	if len([]rune(schedule.Name)) < 1 || len([]rune(schedule.Name)) > 80 {
		return fmt.Errorf("计划名称必须为 1 到 80 个字符")
	}
	root, err := normalizeScanRoot(schedule.Root)
	if err != nil {
		return fmt.Errorf("请选择存在的磁盘或文件夹")
	}
	schedule.Root = root
	if schedule.Cadence != "daily" && schedule.Cadence != "weekly" {
		return fmt.Errorf("周期必须是 daily 或 weekly")
	}
	if schedule.Hour < 0 || schedule.Hour > 23 || schedule.Minute < 0 || schedule.Minute > 59 {
		return fmt.Errorf("时间无效")
	}
	if schedule.Weekday < 0 || schedule.Weekday > 6 {
		return fmt.Errorf("星期必须在 0 到 6 之间")
	}
	if schedule.Minimum < 0 {
		return fmt.Errorf("最小文件不能为负数")
	}
	if schedule.DuplicateMinimum < 1024*1024 {
		return fmt.Errorf("重复文件阈值至少为 1 MiB")
	}
	if schedule.ResultLimit < 1 || schedule.ResultLimit > 2000 {
		return fmt.Errorf("结果上限必须在 1 到 2000 之间")
	}
	if err := validateScanBudget(schedule.MaxBytes, schedule.MaxDurationSeconds); err != nil {
		return err
	}
	if len(schedule.Excludes) > 100 {
		return fmt.Errorf("排除规则最多 100 条")
	}
	for index := range schedule.Excludes {
		schedule.Excludes[index] = strings.TrimSpace(schedule.Excludes[index])
		if schedule.Excludes[index] == "" || len([]rune(schedule.Excludes[index])) > 500 {
			return fmt.Errorf("排除规则不能为空且不能超过 500 个字符")
		}
	}
	return nil
}

func (s *Store) saveSchedule(schedule ScanSchedule) (ScanSchedule, error) {
	if err := validateSchedule(&schedule); err != nil {
		return ScanSchedule{}, err
	}
	now := time.Now().UnixNano()
	excludes, err := json.Marshal(schedule.Excludes)
	if err != nil {
		return ScanSchedule{}, err
	}
	if schedule.ID == "" {
		schedule.ID, err = newID()
		if err != nil {
			return ScanSchedule{}, err
		}
		schedule.CreatedAt = now
	}
	schedule.UpdatedAt = now
	if schedule.BackendState == "" {
		schedule.BackendState = "disabled"
	}
	_, err = s.db.Exec(
		`INSERT INTO scan_schedules(
		 id, name, root, cadence, hour, minute, weekday, minimum_bytes,
		 duplicate_minimum_bytes, result_limit, max_bytes, max_duration_seconds,
		 excludes_json, enabled,
		 backend_state, backend_error, created_at, updated_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		 name=excluded.name, root=excluded.root, cadence=excluded.cadence,
		 hour=excluded.hour, minute=excluded.minute, weekday=excluded.weekday,
		 minimum_bytes=excluded.minimum_bytes,
		 duplicate_minimum_bytes=excluded.duplicate_minimum_bytes,
		 result_limit=excluded.result_limit, max_bytes=excluded.max_bytes,
		 max_duration_seconds=excluded.max_duration_seconds,
		 excludes_json=excluded.excludes_json,
		 enabled=excluded.enabled, backend_state=excluded.backend_state,
		 backend_error=excluded.backend_error, updated_at=excluded.updated_at`,
		schedule.ID, schedule.Name, schedule.Root, schedule.Cadence,
		schedule.Hour, schedule.Minute, schedule.Weekday, schedule.Minimum,
		schedule.DuplicateMinimum, schedule.ResultLimit, schedule.MaxBytes,
		schedule.MaxDurationSeconds, string(excludes),
		schedule.Enabled, schedule.BackendState, schedule.BackendError,
		schedule.CreatedAt, schedule.UpdatedAt,
	)
	if err != nil {
		return ScanSchedule{}, err
	}
	return s.schedule(schedule.ID)
}

func scanScheduleRow(row interface{ Scan(...any) error }) (ScanSchedule, error) {
	var schedule ScanSchedule
	var excludes string
	var enabled int
	var lastRun sql.NullInt64
	err := row.Scan(
		&schedule.ID, &schedule.Name, &schedule.Root, &schedule.Cadence,
		&schedule.Hour, &schedule.Minute, &schedule.Weekday, &schedule.Minimum,
		&schedule.DuplicateMinimum, &schedule.ResultLimit, &schedule.MaxBytes,
		&schedule.MaxDurationSeconds, &excludes, &enabled,
		&schedule.BackendState, &schedule.BackendError, &schedule.CreatedAt,
		&schedule.UpdatedAt, &lastRun,
	)
	if err != nil {
		return ScanSchedule{}, err
	}
	schedule.Enabled = enabled != 0
	if lastRun.Valid {
		schedule.LastRunAt = lastRun.Int64
	}
	if err := json.Unmarshal([]byte(excludes), &schedule.Excludes); err != nil {
		return ScanSchedule{}, fmt.Errorf("读取排除规则: %w", err)
	}
	if schedule.Excludes == nil {
		schedule.Excludes = []string{}
	}
	return schedule, nil
}

const scheduleColumns = `id, name, root, cadence, hour, minute, weekday,
	minimum_bytes, duplicate_minimum_bytes, result_limit, max_bytes,
	max_duration_seconds, excludes_json, enabled,
	backend_state, backend_error, created_at, updated_at, last_run_at`

func (s *Store) schedule(id string) (ScanSchedule, error) {
	return scanScheduleRow(s.db.QueryRow(
		"SELECT "+scheduleColumns+" FROM scan_schedules WHERE id = ?", id,
	))
}

func (s *Store) schedules() ([]ScanSchedule, error) {
	rows, err := s.db.Query(
		"SELECT " + scheduleColumns + " FROM scan_schedules ORDER BY created_at, id",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ScanSchedule, 0)
	for rows.Next() {
		schedule, err := scanScheduleRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, schedule)
	}
	return result, rows.Err()
}

func (s *Store) setScheduleBackend(id, state, message string) error {
	if len(message) > 1000 {
		message = message[:1000]
	}
	_, err := s.db.Exec(
		`UPDATE scan_schedules SET backend_state = ?, backend_error = ?, updated_at = ?
		 WHERE id = ?`,
		state, message, time.Now().UnixNano(), id,
	)
	return err
}

func (s *Store) setScheduleEnabled(id string, enabled bool) error {
	result, err := s.db.Exec(
		`UPDATE scan_schedules SET enabled = ?, updated_at = ? WHERE id = ?`,
		enabled, time.Now().UnixNano(), id,
	)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) deleteSchedule(id string) error {
	result, err := s.db.Exec("DELETE FROM scan_schedules WHERE id = ?", id)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) acquireScheduleLease(id, owner string, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		"DELETE FROM schedule_leases WHERE acquired_at < ?",
		now.Add(-scheduleLeaseTTL).UnixNano(),
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		"INSERT INTO schedule_leases(schedule_id, owner, acquired_at) VALUES(?, ?, ?)",
		id, owner, now.UnixNano(),
	); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return errScheduleBusy
		}
		return err
	}
	return tx.Commit()
}

func (s *Store) releaseScheduleLease(id, owner string) error {
	_, err := s.db.Exec(
		"DELETE FROM schedule_leases WHERE schedule_id = ? AND owner = ?", id, owner,
	)
	return err
}

func (s *Store) refreshScheduleLease(id, owner string, now time.Time) error {
	result, err := s.db.Exec(
		`UPDATE schedule_leases SET acquired_at = ?
		 WHERE schedule_id = ? AND owner = ?`,
		now.UnixNano(), id, owner,
	)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return errScheduleBusy
	}
	return nil
}

func (s *Store) scheduledScanSummaries(limit int) ([]ScheduledScanSummary, error) {
	rows, err := s.db.Query(
		`SELECT ss.id, ss.schedule_id, COALESCE(sc.name, '已删除计划'), ss.root,
		 ss.state, ss.trigger, ss.started_at, ss.completed_at, ss.files_seen,
		 ss.bytes_seen, ss.errors,
		 (SELECT COUNT(*) FROM scan_findings sf WHERE sf.session_id = ss.id),
		 ss.policy_id, ss.policy_version, ss.message
		 FROM scan_sessions ss LEFT JOIN scan_schedules sc ON sc.id = ss.schedule_id
		 WHERE ss.trigger IN ('scheduled', 'manual_schedule')
		 ORDER BY ss.started_at DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ScheduledScanSummary, 0)
	for rows.Next() {
		var item ScheduledScanSummary
		var completed sql.NullInt64
		if err := rows.Scan(
			&item.ID, &item.ScheduleID, &item.ScheduleName, &item.Root,
			&item.State, &item.Trigger, &item.StartedAt, &completed, &item.FilesSeen,
			&item.BytesSeen, &item.Errors, &item.ResultCount, &item.PolicyID,
			&item.PolicyVersion, &item.Message,
		); err != nil {
			return nil, err
		}
		if completed.Valid {
			item.CompletedAt = completed.Int64
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) latestScheduledScan(scheduleID string) (ScheduledScanSummary, error) {
	var item ScheduledScanSummary
	var completed sql.NullInt64
	err := s.db.QueryRow(
		`SELECT ss.id, ss.schedule_id, COALESCE(sc.name, '已删除计划'), ss.root,
			 ss.state, ss.trigger, ss.started_at, ss.completed_at, ss.files_seen,
			 ss.bytes_seen, ss.errors,
			 (SELECT COUNT(*) FROM scan_findings sf WHERE sf.session_id = ss.id),
			 ss.policy_id, ss.policy_version, ss.message
			 FROM scan_sessions ss LEFT JOIN scan_schedules sc ON sc.id = ss.schedule_id
			 WHERE ss.schedule_id = ? AND ss.trigger IN ('scheduled', 'manual_schedule')
			 ORDER BY ss.started_at DESC LIMIT 1`, scheduleID,
	).Scan(
		&item.ID, &item.ScheduleID, &item.ScheduleName, &item.Root,
		&item.State, &item.Trigger, &item.StartedAt, &completed, &item.FilesSeen,
		&item.BytesSeen, &item.Errors, &item.ResultCount, &item.PolicyID,
		&item.PolicyVersion, &item.Message,
	)
	if err != nil {
		return item, err
	}
	if completed.Valid {
		item.CompletedAt = completed.Int64
	}
	return item, nil
}

func nextScheduleRun(schedule ScanSchedule, now time.Time) time.Time {
	now = now.In(time.Local)
	candidate := time.Date(now.Year(), now.Month(), now.Day(), schedule.Hour, schedule.Minute, 0, 0, now.Location())
	if schedule.Cadence == "weekly" {
		days := (schedule.Weekday - int(candidate.Weekday()) + 7) % 7
		candidate = candidate.AddDate(0, 0, days)
		if !candidate.After(now) {
			candidate = candidate.AddDate(0, 0, 7)
		}
		return candidate
	}
	if !candidate.After(now) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}

func previousScheduleRun(schedule ScanSchedule, now time.Time) time.Time {
	now = now.In(time.Local)
	candidate := time.Date(now.Year(), now.Month(), now.Day(), schedule.Hour, schedule.Minute, 0, 0, now.Location())
	if schedule.Cadence == "weekly" {
		days := (int(candidate.Weekday()) - schedule.Weekday + 7) % 7
		candidate = candidate.AddDate(0, 0, -days)
		if candidate.After(now) {
			candidate = candidate.AddDate(0, 0, -7)
		}
		return candidate
	}
	if candidate.After(now) {
		candidate = candidate.AddDate(0, 0, -1)
	}
	return candidate
}

func missedScheduleRuns(schedule ScanSchedule, now time.Time) int {
	if !schedule.Enabled {
		return 0
	}
	baseline := schedule.CreatedAt
	if schedule.UpdatedAt > baseline {
		baseline = schedule.UpdatedAt
	}
	if schedule.LastRunAt > baseline {
		baseline = schedule.LastRunAt
	}
	if baseline == 0 {
		return 0
	}
	first := nextScheduleRun(schedule, time.Unix(0, baseline))
	last := previousScheduleRun(schedule, now)
	if first.After(last) {
		return 0
	}
	count := 0
	for due := first; !due.After(last) && count < 10000; {
		count++
		if schedule.Cadence == "weekly" {
			due = due.AddDate(0, 0, 7)
		} else {
			due = due.AddDate(0, 0, 1)
		}
	}
	return count
}

func (s *Store) scheduledScanDetail(id string) (ScheduledScanDetail, error) {
	var detail ScheduledScanDetail
	var completed sql.NullInt64
	err := s.db.QueryRow(
		`SELECT ss.id, ss.schedule_id, COALESCE(sc.name, '已删除计划'), ss.root,
		 ss.state, ss.trigger, ss.started_at, ss.completed_at, ss.files_seen,
		 ss.bytes_seen, ss.errors,
		 (SELECT COUNT(*) FROM scan_findings sf WHERE sf.session_id = ss.id),
		 ss.policy_id, ss.policy_version, ss.message
		 FROM scan_sessions ss LEFT JOIN scan_schedules sc ON sc.id = ss.schedule_id
		 WHERE ss.id = ? AND ss.trigger IN ('scheduled', 'manual_schedule')`, id,
	).Scan(
		&detail.Summary.ID, &detail.Summary.ScheduleID, &detail.Summary.ScheduleName,
		&detail.Summary.Root, &detail.Summary.State, &detail.Summary.Trigger,
		&detail.Summary.StartedAt, &completed, &detail.Summary.FilesSeen,
		&detail.Summary.BytesSeen, &detail.Summary.Errors, &detail.Summary.ResultCount,
		&detail.Summary.PolicyID, &detail.Summary.PolicyVersion, &detail.Summary.Message,
	)
	if err != nil {
		return detail, err
	}
	if completed.Valid {
		detail.Summary.CompletedAt = completed.Int64
	}
	rows, err := s.db.Query(
		`SELECT path, size, modified_at, modified_display, advice_level, advice_label,
		 advice_reason, advice_rule_id, advice_category
		 FROM scan_findings WHERE session_id = ? ORDER BY size DESC, path`, id,
	)
	if err != nil {
		return detail, err
	}
	defer rows.Close()
	detail.Findings = make([]FileRecord, 0)
	for rows.Next() {
		var record FileRecord
		if err := rows.Scan(
			&record.Path, &record.Size, &record.ModifiedUnixNano, &record.ModifiedAt,
			&record.Advice.Level, &record.Advice.Label, &record.Advice.Reason,
			&record.Advice.RuleID, &record.Advice.Category,
		); err != nil {
			return detail, err
		}
		detail.Findings = append(detail.Findings, record)
	}
	return detail, rows.Err()
}

func executeSchedule(
	store *Store, schedule ScanSchedule, trigger string, onReady func(*scanJob),
) (*scanJob, error) {
	if !schedule.Enabled && trigger == "scheduled" {
		return nil, fmt.Errorf("计划已停用")
	}
	if err := validateScanBudget(schedule.MaxBytes, schedule.MaxDurationSeconds); err != nil {
		return nil, err
	}
	owner, err := newID()
	if err != nil {
		return nil, err
	}
	if err := store.acquireScheduleLease(schedule.ID, owner, time.Now()); err != nil {
		return nil, err
	}
	defer store.releaseScheduleLease(schedule.ID, owner)
	policy, err := store.activePolicy()
	if err != nil {
		return nil, err
	}
	sessionID, err := store.beginScanWithContext(schedule.Root, schedule.ID, trigger, policy)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &scanJob{
		status: ScanStatus{
			State: "running", Phase: "scanning", Root: schedule.Root,
			PolicyID: policy.ID, PolicyVersion: policy.Version,
		},
		started: time.Now(), cancel: cancel, known: make(map[string]FileRecord),
		folders: make(map[string]FolderRecord), duplicateByPath: make(map[string]string),
		store: store, sessionID: sessionID, done: make(chan struct{}),
	}
	heartbeatStop := make(chan struct{})
	heartbeatStopped := make(chan struct{})
	heartbeatError := make(chan error, 1)
	go func() {
		defer close(heartbeatStopped)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				if err := store.refreshScheduleLease(schedule.ID, owner, now); err != nil {
					heartbeatError <- err
					cancel()
					return
				}
			case <-heartbeatStop:
				return
			}
		}
	}()
	if onReady != nil {
		onReady(job)
	}
	job.run(ctx, schedule.Root, ScanOptions{
		Minimum: schedule.Minimum, DuplicateMinimum: schedule.DuplicateMinimum,
		Limit: schedule.ResultLimit, Excludes: schedule.Excludes, Policy: policy,
		MaxBytes:    schedule.MaxBytes,
		MaxDuration: time.Duration(schedule.MaxDurationSeconds) * time.Second,
	})
	close(heartbeatStop)
	<-heartbeatStopped
	select {
	case err := <-heartbeatError:
		return job, fmt.Errorf("续订计划租约失败: %w", err)
	default:
	}
	status := job.snapshot()
	if status.State == "error" {
		return job, fmt.Errorf("%s", status.Message)
	}
	return job, nil
}
