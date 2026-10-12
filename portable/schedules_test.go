package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validTestSchedule(root string) ScanSchedule {
	return ScanSchedule{
		Name: "每周检查", Root: root, Cadence: "weekly", Hour: 10, Minute: 30,
		Weekday: 6, Minimum: 0, DuplicateMinimum: 1024 * 1024,
		ResultLimit: 100, Excludes: []string{".git"}, Enabled: true,
		BackendState: "disabled",
	}
}

func TestScheduleValidationAndPersistence(t *testing.T) {
	store, _ := testStore(t)
	input := validTestSchedule(t.TempDir())
	input.MaxBytes = 256 * 1024 * 1024
	input.MaxDurationSeconds = 90 * 60
	schedule, err := store.saveSchedule(input)
	if err != nil {
		t.Fatal(err)
	}
	if schedule.ID == "" || schedule.BackendState != "disabled" ||
		schedule.MaxBytes != input.MaxBytes || schedule.MaxDurationSeconds != input.MaxDurationSeconds ||
		len(schedule.Excludes) != 1 {
		t.Fatalf("unexpected schedule: %+v", schedule)
	}
	loaded, err := store.schedules()
	if err != nil || len(loaded) != 1 || loaded[0].ID != schedule.ID {
		t.Fatalf("unexpected schedules: %+v, %v", loaded, err)
	}

	invalid := validTestSchedule(schedule.Root)
	invalid.Hour = 24
	if _, err := store.saveSchedule(invalid); err == nil {
		t.Fatal("invalid hour was accepted")
	}
	invalid = validTestSchedule(schedule.Root)
	invalid.DuplicateMinimum = 1
	if _, err := store.saveSchedule(invalid); err == nil {
		t.Fatal("invalid duplicate threshold was accepted")
	}
}

func TestScheduleRunTimesAndMissedRuns(t *testing.T) {
	loc := time.Local
	now := time.Date(2026, time.August, 2, 12, 0, 0, 0, loc)
	daily := validTestSchedule(t.TempDir())
	daily.Cadence = "daily"
	daily.Hour = 10
	daily.Minute = 30
	daily.CreatedAt = time.Date(2026, time.July, 30, 10, 30, 0, 0, loc).UnixNano()
	if got, want := nextScheduleRun(daily, now), time.Date(2026, time.August, 3, 10, 30, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("daily next run = %v, want %v", got, want)
	}
	if got, want := previousScheduleRun(daily, now), time.Date(2026, time.August, 2, 10, 30, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("daily previous run = %v, want %v", got, want)
	}
	if got, want := missedScheduleRuns(daily, now), 3; got != want {
		t.Fatalf("daily missed runs = %d, want %d", got, want)
	}

	weekly := validTestSchedule(t.TempDir())
	weekly.Hour = 10
	weekly.Minute = 30
	weekly.Weekday = 6
	weekly.CreatedAt = time.Date(2026, time.July, 25, 10, 30, 0, 0, loc).UnixNano()
	if got, want := nextScheduleRun(weekly, now), time.Date(2026, time.August, 8, 10, 30, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("weekly next run = %v, want %v", got, want)
	}
	if got, want := previousScheduleRun(weekly, now), time.Date(2026, time.August, 1, 10, 30, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("weekly previous run = %v, want %v", got, want)
	}
	if got, want := missedScheduleRuns(weekly, now), 1; got != want {
		t.Fatalf("weekly missed runs = %d, want %d", got, want)
	}
}

func TestScheduleLeaseRejectsOverlapAndRecoversStaleLease(t *testing.T) {
	store, _ := testStore(t)
	now := time.Now()
	if err := store.acquireScheduleLease("schedule", "first", now); err != nil {
		t.Fatal(err)
	}
	if err := store.acquireScheduleLease("schedule", "second", now); !errors.Is(err, errScheduleBusy) {
		t.Fatalf("overlap got %v, want busy", err)
	}
	if _, err := store.db.Exec(
		"UPDATE schedule_leases SET acquired_at = ? WHERE schedule_id = ?",
		now.Add(-scheduleLeaseTTL-time.Minute).UnixNano(), "schedule",
	); err != nil {
		t.Fatal(err)
	}
	if err := store.acquireScheduleLease("schedule", "second", now); err != nil {
		t.Fatalf("stale lease was not recovered: %v", err)
	}
	refreshedAt := now.Add(time.Minute)
	if err := store.refreshScheduleLease("schedule", "second", refreshedAt); err != nil {
		t.Fatal(err)
	}
	var acquiredAt int64
	if err := store.db.QueryRow(
		"SELECT acquired_at FROM schedule_leases WHERE schedule_id = ?", "schedule",
	).Scan(&acquiredAt); err != nil {
		t.Fatal(err)
	}
	if acquiredAt != refreshedAt.UnixNano() {
		t.Fatalf("lease timestamp = %d, want %d", acquiredAt, refreshedAt.UnixNano())
	}
	if err := store.refreshScheduleLease("schedule", "wrong-owner", time.Now()); !errors.Is(err, errScheduleBusy) {
		t.Fatalf("wrong owner refresh got %v", err)
	}
}

func TestExecuteSchedulePersistsReadOnlyFindings(t *testing.T) {
	store, _ := testStore(t)
	root := t.TempDir()
	path := filepath.Join(root, "large.bin")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	schedule, err := store.saveSchedule(validTestSchedule(root))
	if err != nil {
		t.Fatal(err)
	}
	job, err := executeSchedule(store, schedule, "scheduled", nil)
	if err != nil {
		t.Fatal(err)
	}
	status := job.snapshot()
	if status.State != "done" || status.FilesSeen != 1 || len(status.Results) != 1 {
		t.Fatalf("unexpected status: %+v", status)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("scheduled scan modified source file: %v", err)
	}
	summaries, err := store.scheduledScanSummaries(20)
	if err != nil || len(summaries) != 1 || summaries[0].ResultCount != 1 {
		t.Fatalf("unexpected summaries: %+v, %v", summaries, err)
	}
	detail, err := store.scheduledScanDetail(summaries[0].ID)
	expectedPath := filepath.Join(schedule.Root, filepath.Base(path))
	if err != nil || len(detail.Findings) != 1 || detail.Findings[0].Path != expectedPath {
		t.Fatalf("unexpected detail: %+v, %v", detail, err)
	}
	plan, err := store.loadPlan()
	if err != nil || len(plan) != 0 {
		t.Fatalf("scheduled scan changed cleanup plan: %+v, %v", plan, err)
	}
}

func TestExecuteScheduleRecordsBudgetExceeded(t *testing.T) {
	store, _ := testStore(t)
	root := t.TempDir()
	path := filepath.Join(root, "large.bin")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	scheduleInput := validTestSchedule(root)
	scheduleInput.MaxBytes = 1
	schedule, err := store.saveSchedule(scheduleInput)
	if err != nil {
		t.Fatal(err)
	}
	job, err := executeSchedule(store, schedule, "scheduled", nil)
	if err != nil {
		t.Fatal(err)
	}
	if status := job.snapshot(); status.State != "budget_exceeded" || status.BudgetExceeded == "" {
		t.Fatalf("unexpected budget status: %+v", status)
	}
	summaries, err := store.scheduledScanSummaries(20)
	if err != nil || len(summaries) != 1 || summaries[0].State != "budget_exceeded" {
		t.Fatalf("budget state was not persisted: %+v, %v", summaries, err)
	}
	state, err := (&server{store: store}).scheduleState()
	if err != nil {
		t.Fatal(err)
	}
	budgetAlert := false
	for _, alert := range state.Alerts {
		if alert.Kind == "budget" && alert.ScheduleID == schedule.ID {
			budgetAlert = true
		}
	}
	if !budgetAlert {
		t.Fatalf("budget exhaustion was not surfaced as a schedule alert: %+v", state.Alerts)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("scheduled budget scan modified source file: %v", err)
	}
}

type fakeScheduleBackend struct {
	installed []string
	removed   []string
	err       error
}

func (f *fakeScheduleBackend) Install(schedule ScanSchedule, _, _ string) error {
	f.installed = append(f.installed, schedule.ID)
	return f.err
}

func (f *fakeScheduleBackend) Remove(id string) error {
	f.removed = append(f.removed, id)
	return f.err
}

func (*fakeScheduleBackend) Name() string {
	return "测试调度器"
}

type inspectingScheduleBackend struct {
	fakeScheduleBackend
	inspection scheduleInspection
}

func (f *inspectingScheduleBackend) Install(schedule ScanSchedule, executable, dataDir string) error {
	f.fakeScheduleBackend.installed = append(f.fakeScheduleBackend.installed, schedule.ID)
	f.inspection = scheduleInspection{State: "ok"}
	return f.fakeScheduleBackend.err
}

func (f *inspectingScheduleBackend) Inspect(ScanSchedule, string, string) (scheduleInspection, error) {
	return f.inspection, nil
}

func TestScheduleHandlersExposeRegistrationFailure(t *testing.T) {
	store, _ := testStore(t)
	backend := &fakeScheduleBackend{err: errors.New("registration denied")}
	app := &server{
		store: store, backend: backend, executable: "/tmp/Space Sheriff",
		dataDir: t.TempDir(),
	}
	schedule := validTestSchedule(t.TempDir())
	body, _ := json.Marshal(schedule)
	result := httptest.NewRecorder()
	app.saveSchedule(
		result,
		httptest.NewRequest(http.MethodPost, "/api/schedules/save", bytes.NewReader(body)),
	)
	if result.Code != http.StatusOK {
		t.Fatalf("save got %d: %s", result.Code, result.Body.String())
	}
	var state scheduleState
	if err := json.NewDecoder(result.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if len(state.Schedules) != 1 || state.Schedules[0].BackendState != "error" ||
		state.Schedules[0].BackendError == "" || len(backend.installed) != 1 {
		t.Fatalf("registration failure was hidden: %+v", state)
	}
}

func TestScheduleStateExposesFailureMissedAndDriftAlerts(t *testing.T) {
	store, _ := testStore(t)
	root := t.TempDir()
	schedule := validTestSchedule(root)
	now := time.Now()
	due := now.Add(-time.Hour)
	schedule.CreatedAt = now.Add(-48 * time.Hour).UnixNano()
	schedule.Hour = due.Hour()
	schedule.Minute = due.Minute()
	schedule.Weekday = int(due.Weekday())
	schedule, err := store.saveSchedule(schedule)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(
		"UPDATE scan_schedules SET created_at = ?, updated_at = ? WHERE id = ?",
		now.Add(-48*time.Hour).UnixNano(), now.Add(-48*time.Hour).UnixNano(), schedule.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(
		`INSERT INTO scan_sessions(
			id, root, state, started_at, completed_at, schedule_id, trigger,
			policy_id, policy_version, message)
			VALUES('failed-run', ?, 'failed', ?, ?, ?, 'scheduled', 'balanced', 1, '磁盘不可用')`,
		root, due.UnixNano(), now.UnixNano(), schedule.ID,
	); err != nil {
		t.Fatal(err)
	}
	backend := &inspectingScheduleBackend{inspection: scheduleInspection{
		State: "drifted", Message: "系统任务参数已被修改",
	}}
	app := &server{store: store, backend: backend}
	result := httptest.NewRecorder()
	app.schedules(result, httptest.NewRequest(http.MethodGet, "/api/schedules", nil))
	if result.Code != http.StatusOK {
		t.Fatalf("list got %d: %s", result.Code, result.Body.String())
	}
	var state scheduleState
	if err := json.NewDecoder(result.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if len(state.Schedules) != 1 || state.Schedules[0].LastRunState != "failed" ||
		state.Schedules[0].DriftState != "drifted" || state.Schedules[0].MissedRuns == 0 {
		t.Fatalf("schedule health was not exposed: %+v", state.Schedules)
	}
	if len(state.Alerts) < 3 {
		t.Fatalf("expected failure, missed and drift alerts: %+v", state.Alerts)
	}
}

func TestRepairScheduleReconcilesBackend(t *testing.T) {
	store, _ := testStore(t)
	schedule, err := store.saveSchedule(validTestSchedule(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	backend := &inspectingScheduleBackend{inspection: scheduleInspection{
		State: "missing", Message: "系统任务不存在",
	}}
	app := &server{
		store: store, backend: backend, executable: "/tmp/SpaceSheriff", dataDir: t.TempDir(),
	}
	result := httptest.NewRecorder()
	app.repairSchedule(
		result,
		httptest.NewRequest(http.MethodPost, "/api/schedules/repair", bytes.NewBufferString(`{"id":"`+schedule.ID+`"}`)),
	)
	if result.Code != http.StatusOK || len(backend.installed) != 1 {
		t.Fatalf("repair got %d, backend=%+v: %s", result.Code, backend, result.Body.String())
	}
	var state scheduleState
	if err := json.NewDecoder(result.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if len(state.Schedules) != 1 || state.Schedules[0].DriftState != "ok" {
		t.Fatalf("repair did not clear drift: %+v", state.Schedules)
	}
}

func TestScheduleHandlersLifecycle(t *testing.T) {
	store, _ := testStore(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report.bin"), []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &fakeScheduleBackend{}
	app := &server{
		store: store, backend: backend, executable: "/tmp/SpaceSheriff",
		dataDir: t.TempDir(),
	}
	schedule, err := store.saveSchedule(validTestSchedule(root))
	if err != nil {
		t.Fatal(err)
	}

	listResult := httptest.NewRecorder()
	app.schedules(listResult, httptest.NewRequest(http.MethodGet, "/api/schedules", nil))
	if listResult.Code != http.StatusOK {
		t.Fatalf("list got %d: %s", listResult.Code, listResult.Body.String())
	}

	toggleBody := bytes.NewBufferString(`{"id":"` + schedule.ID + `","enabled":false}`)
	toggleResult := httptest.NewRecorder()
	app.toggleSchedule(
		toggleResult,
		httptest.NewRequest(http.MethodPost, "/api/schedules/toggle", toggleBody),
	)
	if toggleResult.Code != http.StatusOK || len(backend.removed) != 1 {
		t.Fatalf("toggle got %d, backend=%+v", toggleResult.Code, backend)
	}

	runBody := bytes.NewBufferString(`{"id":"` + schedule.ID + `"}`)
	runResult := httptest.NewRecorder()
	app.runSchedule(
		runResult,
		httptest.NewRequest(http.MethodPost, "/api/schedules/run", runBody),
	)
	if runResult.Code != http.StatusOK {
		t.Fatalf("run got %d: %s", runResult.Code, runResult.Body.String())
	}
	var summaries []ScheduledScanSummary
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		summaries, err = store.scheduledScanSummaries(20)
		if err == nil && len(summaries) == 1 && summaries[0].State == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(summaries) != 1 || summaries[0].State != "completed" {
		t.Fatalf("manual schedule did not finish: %+v, %v", summaries, err)
	}

	detailResult := httptest.NewRecorder()
	app.scheduledScans(
		detailResult,
		httptest.NewRequest(http.MethodGet, "/api/scheduled-scans?id="+summaries[0].ID, nil),
	)
	if detailResult.Code != http.StatusOK {
		t.Fatalf("detail got %d: %s", detailResult.Code, detailResult.Body.String())
	}
	badLimitResult := httptest.NewRecorder()
	app.scheduledScans(
		badLimitResult,
		httptest.NewRequest(http.MethodGet, "/api/scheduled-scans?limit=0", nil),
	)
	if badLimitResult.Code != http.StatusBadRequest {
		t.Fatalf("bad limit got %d", badLimitResult.Code)
	}

	deleteBody := bytes.NewBufferString(`{"id":"` + schedule.ID + `"}`)
	deleteResult := httptest.NewRecorder()
	app.deleteSchedule(
		deleteResult,
		httptest.NewRequest(http.MethodPost, "/api/schedules/delete", deleteBody),
	)
	if deleteResult.Code != http.StatusOK || len(backend.removed) != 2 {
		t.Fatalf("delete got %d, backend=%+v", deleteResult.Code, backend)
	}
}

func TestRunApplicationScheduledModeAndArgumentErrors(t *testing.T) {
	if err := runApplication([]string{"--unknown"}); err == nil {
		t.Fatal("unknown argument was accepted")
	}
	if err := runApplication([]string{"--scheduled-scan"}); err == nil {
		t.Fatal("missing schedule id was accepted")
	}
	dataDir := t.TempDir()
	store, err := openStore(filepath.Join(dataDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.bin"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	schedule, err := store.saveSchedule(validTestSchedule(root))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runApplication([]string{
		"--scheduled-scan", schedule.ID, "--data-dir", dataDir,
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(filepath.Join(dataDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	summaries, err := reopened.scheduledScanSummaries(20)
	if err != nil || len(summaries) != 1 || summaries[0].Trigger != "scheduled" {
		t.Fatalf("unexpected headless result: %+v, %v", summaries, err)
	}
}

func TestRunScheduleReportsLeaseConflict(t *testing.T) {
	store, _ := testStore(t)
	schedule, err := store.saveSchedule(validTestSchedule(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.acquireScheduleLease(schedule.ID, "other-process", time.Now()); err != nil {
		t.Fatal(err)
	}
	app := &server{store: store}
	result := httptest.NewRecorder()
	app.runSchedule(
		result,
		httptest.NewRequest(
			http.MethodPost, "/api/schedules/run",
			bytes.NewBufferString(`{"id":"`+schedule.ID+`"}`),
		),
	)
	if result.Code != http.StatusOK {
		t.Fatalf("run got %d: %s", result.Code, result.Body.String())
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		app.mu.Lock()
		job := app.job
		app.mu.Unlock()
		if job != nil && job.snapshot().State == "error" {
			if !strings.Contains(job.snapshot().Message, "已有扫描") {
				t.Fatalf("unexpected conflict message: %+v", job.snapshot())
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("lease conflict was not exposed as an error")
}
