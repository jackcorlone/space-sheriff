package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type scheduleState struct {
	Backend   string                 `json:"backend"`
	Schedules []ScanSchedule         `json:"schedules"`
	Recent    []ScheduledScanSummary `json:"recent"`
	Alerts    []ScheduleAlert        `json:"alerts"`
}

func (s *server) scheduleState() (scheduleState, error) {
	schedules, err := s.store.schedules()
	if err != nil {
		return scheduleState{}, err
	}
	recent, err := s.store.scheduledScanSummaries(20)
	if err != nil {
		return scheduleState{}, err
	}
	alerts := make([]ScheduleAlert, 0)
	now := time.Now()
	inspector, canInspect := s.backend.(scheduleInspector)
	for index := range schedules {
		schedule := &schedules[index]
		if schedule.Enabled {
			schedule.NextRunAt = nextScheduleRun(*schedule, now).UnixNano()
			schedule.MissedRuns = missedScheduleRuns(*schedule, now)
		}
		if latest, latestErr := s.store.latestScheduledScan(schedule.ID); latestErr == nil {
			schedule.LastRunState = latest.State
			schedule.LastRunMessage = latest.Message
			if latest.State == "failed" || latest.State == "budget_exceeded" {
				message := latest.Message
				kind := "failure"
				if latest.State == "budget_exceeded" {
					kind = "budget"
				}
				if message == "" {
					if kind == "budget" {
						message = "最近一次计划扫描达到资源预算"
					} else {
						message = "最近一次计划扫描失败"
					}
				}
				alerts = append(alerts, ScheduleAlert{
					ScheduleID: schedule.ID, ScheduleName: schedule.Name,
					Kind: kind, Message: message, OccurredAt: latest.CompletedAt,
				})
			}
		} else if !errors.Is(latestErr, sql.ErrNoRows) {
			return scheduleState{}, latestErr
		}
		if schedule.MissedRuns > 0 {
			alerts = append(alerts, ScheduleAlert{
				ScheduleID: schedule.ID, ScheduleName: schedule.Name,
				Kind: "missed", Message: fmt.Sprintf("已错过 %d 次计划运行", schedule.MissedRuns),
			})
		}
		if schedule.BackendState == "error" {
			message := schedule.BackendError
			if message == "" {
				message = "系统任务注册失败"
			}
			alerts = append(alerts, ScheduleAlert{
				ScheduleID: schedule.ID, ScheduleName: schedule.Name,
				Kind: "backend", Message: message,
			})
		}
		if canInspect {
			inspection, inspectErr := inspector.Inspect(*schedule, s.executable, s.dataDir)
			if inspectErr != nil && inspection.Message == "" {
				inspection.Message = inspectErr.Error()
			}
			if inspection.State == "" {
				inspection.State = "unknown"
			}
			schedule.DriftState = inspection.State
			schedule.DriftMessage = inspection.Message
			if inspection.State == "missing" || inspection.State == "drifted" || inspection.State == "unknown" {
				message := inspection.Message
				if message == "" {
					message = "系统任务状态需要检查"
				}
				alerts = append(alerts, ScheduleAlert{
					ScheduleID: schedule.ID, ScheduleName: schedule.Name,
					Kind: "drift", Message: message,
				})
			}
		}
	}
	backendName := "不可用"
	if s.backend != nil {
		backendName = s.backend.Name()
	}
	return scheduleState{Backend: backendName, Schedules: schedules, Recent: recent, Alerts: alerts}, nil
}

func (s *server) schedules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	state, err := s.scheduleState()
	if err != nil {
		http.Error(w, "读取定时扫描计划失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, state)
}

func (s *server) saveSchedule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var schedule ScanSchedule
	if !decodeJSON(w, r, &schedule) {
		return
	}
	if schedule.ID != "" {
		if _, err := s.store.schedule(schedule.ID); errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "扫描计划不存在", http.StatusNotFound)
			return
		} else if err != nil {
			http.Error(w, "读取扫描计划失败", http.StatusInternalServerError)
			return
		}
	}
	schedule.BackendState = "disabled"
	schedule.BackendError = ""
	saved, err := s.store.saveSchedule(schedule)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.reconcileSchedule(saved)
	state, err := s.scheduleState()
	if err != nil {
		http.Error(w, "计划已保存但无法刷新状态", http.StatusInternalServerError)
		return
	}
	writeJSON(w, state)
}

func (s *server) reconcileSchedule(schedule ScanSchedule) {
	if s.backend == nil {
		_ = s.store.setScheduleBackend(schedule.ID, "error", "当前平台没有可用的任务注册器")
		return
	}
	var err error
	state := "registered"
	if schedule.Enabled {
		err = s.backend.Install(schedule, s.executable, s.dataDir)
	} else {
		err = s.backend.Remove(schedule.ID)
		state = "disabled"
	}
	if err != nil {
		_ = s.store.setScheduleBackend(schedule.ID, "error", err.Error())
		return
	}
	_ = s.store.setScheduleBackend(schedule.ID, state, "")
}

func (s *server) repairSchedule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		ID string `json:"id"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	schedule, err := s.store.schedule(request.ID)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "扫描计划不存在", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "读取扫描计划失败", http.StatusInternalServerError)
		return
	}
	if s.backend == nil {
		http.Error(w, "当前平台没有可用的任务注册器", http.StatusServiceUnavailable)
		return
	}
	s.reconcileSchedule(schedule)
	state, err := s.scheduleState()
	if err != nil {
		http.Error(w, "修复后无法刷新状态", http.StatusInternalServerError)
		return
	}
	writeJSON(w, state)
}

func (s *server) toggleSchedule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.store.setScheduleEnabled(request.ID, request.Enabled); errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "扫描计划不存在", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "更新扫描计划失败", http.StatusInternalServerError)
		return
	}
	schedule, err := s.store.schedule(request.ID)
	if err != nil {
		http.Error(w, "读取扫描计划失败", http.StatusInternalServerError)
		return
	}
	s.reconcileSchedule(schedule)
	state, _ := s.scheduleState()
	writeJSON(w, state)
}

func (s *server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		ID string `json:"id"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	schedule, err := s.store.schedule(request.ID)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "扫描计划不存在", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "读取扫描计划失败", http.StatusInternalServerError)
		return
	}
	if s.backend != nil {
		if err := s.backend.Remove(schedule.ID); err != nil {
			_ = s.store.setScheduleBackend(schedule.ID, "error", err.Error())
			http.Error(w, "移除系统任务失败："+err.Error(), http.StatusConflict)
			return
		}
	}
	if err := s.store.deleteSchedule(schedule.ID); err != nil {
		http.Error(w, "删除扫描计划失败", http.StatusInternalServerError)
		return
	}
	state, _ := s.scheduleState()
	writeJSON(w, state)
}

func (s *server) runSchedule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		ID string `json:"id"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	schedule, err := s.store.schedule(request.ID)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "扫描计划不存在", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "读取扫描计划失败", http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	if s.job != nil && s.job.snapshot().State == "running" {
		s.mu.Unlock()
		http.Error(w, "已有扫描任务正在运行", http.StatusConflict)
		return
	}
	_, cancel := context.WithCancel(context.Background())
	s.job = &scanJob{
		status:  ScanStatus{State: "running", Phase: "scheduled", Root: schedule.Root},
		started: time.Now(), cancel: cancel,
	}
	s.mu.Unlock()
	go func() {
		job, runErr := executeSchedule(s.store, schedule, "manual_schedule", func(ready *scanJob) {
			s.mu.Lock()
			s.job = ready
			s.mu.Unlock()
		})
		if runErr != nil {
			if job == nil {
				job = &scanJob{
					status: ScanStatus{
						State: "error", Phase: "done", Root: schedule.Root,
						Message: runErr.Error(),
					},
					started: time.Now(), cancel: func() {},
				}
			} else {
				job.mu.Lock()
				job.status.State = "error"
				job.status.Message = runErr.Error()
				job.mu.Unlock()
			}
		}
		s.mu.Lock()
		s.job = job
		s.mu.Unlock()
	}()
	writeJSON(w, map[string]bool{"started": true})
}

func (s *server) scheduledScans(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if id := r.URL.Query().Get("id"); id != "" {
		detail, err := s.store.scheduledScanDetail(id)
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "扫描记录不存在", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "读取扫描记录失败", http.StatusInternalServerError)
			return
		}
		writeJSON(w, detail)
		return
	}
	limit := 20
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			http.Error(w, "limit 必须在 1 到 100 之间", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	result, err := s.store.scheduledScanSummaries(limit)
	if err != nil {
		http.Error(w, "读取扫描记录失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, result)
}
