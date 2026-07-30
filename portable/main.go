package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web/*
var webFiles embed.FS

var version = "0.6.0-dev"

type server struct {
	mu         sync.Mutex
	job        *scanJob
	token      string
	store      *Store
	backend    scheduleBackend
	executable string
	dataDir    string
}

func main() {
	if err := runApplication(os.Args[1:]); err != nil {
		log.Printf("%v", err)
		if errors.Is(err, errScheduleBusy) {
			os.Exit(3)
		}
		os.Exit(1)
	}
}

func runApplication(arguments []string) error {
	var scheduledID, dataDirOverride string
	for index := 0; index < len(arguments); index++ {
		switch arguments[index] {
		case "--scheduled-scan":
			if index+1 >= len(arguments) {
				return fmt.Errorf("--scheduled-scan 缺少计划 ID")
			}
			index++
			scheduledID = arguments[index]
		case "--data-dir":
			if index+1 >= len(arguments) {
				return fmt.Errorf("--data-dir 缺少路径")
			}
			index++
			dataDirOverride = arguments[index]
		default:
			return fmt.Errorf("未知参数：%s", arguments[index])
		}
	}
	dataDir, err := defaultDataDir()
	if err != nil {
		return err
	}
	if dataDirOverride != "" {
		dataDir, err = filepath.Abs(dataDirOverride)
		if err != nil {
			return err
		}
	}
	store, err := openStore(filepath.Join(dataDir, "index.db"))
	if err != nil {
		return fmt.Errorf("打开本地索引失败: %w", err)
	}
	defer store.Close()
	if scheduledID != "" {
		schedule, err := store.schedule(scheduledID)
		if err != nil {
			return fmt.Errorf("读取扫描计划失败: %w", err)
		}
		job, err := executeSchedule(store, schedule, "scheduled", nil)
		if err != nil {
			return err
		}
		status := job.snapshot()
		log.Printf("定时扫描完成：%d 个文件，%d 个结果", status.FilesSeen, len(status.Results))
		return nil
	}
	return runServer(store, dataDir)
}

func runServer(store *Store, dataDir string) error {
	port := os.Getenv("SPACE_SHERIFF_PORT")
	if port == "" {
		port = "0"
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		return err
	}
	staticFiles, _ := fs.Sub(webFiles, "web")
	token, err := newSessionToken()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}
	app := &server{
		token: token, store: store, backend: platformScheduleBackend{},
		executable: executable, dataDir: dataDir,
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(staticFiles)))
	mux.HandleFunc("/api/version", app.version)
	mux.HandleFunc("/api/roots", app.roots)
	mux.HandleFunc("/api/scan", app.startScan)
	mux.HandleFunc("/api/status", app.status)
	mux.HandleFunc("/api/folders", app.folders)
	mux.HandleFunc("/api/cancel", app.cancel)
	mux.HandleFunc("/api/trash", app.trash)
	mux.HandleFunc("/api/trash-batch", app.trashBatch)
	mux.HandleFunc("/api/plan", app.plan)
	mux.HandleFunc("/api/policies", app.policies)
	mux.HandleFunc("/api/policies/activate", app.activatePolicy)
	mux.HandleFunc("/api/policies/import", app.importPolicy)
	mux.HandleFunc("/api/audit", app.audit)
	mux.HandleFunc("/api/maintenance", app.maintenance)
	mux.HandleFunc("/api/schedules", app.schedules)
	mux.HandleFunc("/api/schedules/save", app.saveSchedule)
	mux.HandleFunc("/api/schedules/toggle", app.toggleSchedule)
	mux.HandleFunc("/api/schedules/run", app.runSchedule)
	mux.HandleFunc("/api/schedules/delete", app.deleteSchedule)
	mux.HandleFunc("/api/scheduled-scans", app.scheduledScans)
	mux.HandleFunc("/api/quit", app.quit)
	handler := app.secure(mux)
	url := "http://" + listener.Addr().String() + "/?token=" + app.token
	log.Printf("本地界面: %s", url)

	if os.Getenv("SPACE_SHERIFF_NO_BROWSER") != "1" {
		go func() {
			time.Sleep(250 * time.Millisecond)
			_ = openBrowser(url)
		}()
	}
	if err := http.Serve(listener, handler); err != nil {
		return err
	}
	return nil
}

func newSessionToken() (string, error) {
	if token := os.Getenv("SPACE_SHERIFF_SESSION_TOKEN"); token != "" {
		return token, nil
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(tokenBytes), nil
}

func (s *server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil || (host != "127.0.0.1" && host != "localhost") {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") &&
			subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Space-Sheriff-Token")), []byte(s.token)) != 1 {
			http.Error(w, "invalid session", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost {
			mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mediaType != "application/json" {
				http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		http.Error(w, "请求格式无效", http.StatusBadRequest)
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		http.Error(w, "请求格式无效", http.StatusBadRequest)
		return false
	}
	return true
}

func (s *server) version(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, map[string]string{"version": version})
}

func (s *server) roots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, availableRoots())
}

func (s *server) startScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		Path             string   `json:"path"`
		Minimum          int64    `json:"minimum"`
		DuplicateMinimum int64    `json:"duplicateMinimum"`
		Limit            int      `json:"limit"`
		Excludes         []string `json:"excludes"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	root, err := normalizeScanRoot(request.Path)
	if err != nil {
		http.Error(w, "请选择存在的磁盘或文件夹", http.StatusBadRequest)
		return
	}
	if request.Minimum < 0 {
		request.Minimum = 0
	}
	if request.Limit < 1 || request.Limit > 5000 {
		request.Limit = 2000
	}
	if request.DuplicateMinimum < 1024*1024 {
		request.DuplicateMinimum = 1024 * 1024
	}
	if len(request.Excludes) > 100 {
		http.Error(w, "排除规则最多 100 条", http.StatusBadRequest)
		return
	}
	policy := balancedPolicy()
	if s.store != nil {
		policy, err = s.store.activePolicy()
		if err != nil {
			http.Error(w, "无法读取活动策略", http.StatusInternalServerError)
			return
		}
	}

	s.mu.Lock()
	if s.job != nil && s.job.snapshot().State == "running" {
		s.mu.Unlock()
		http.Error(w, "已有扫描任务正在运行", http.StatusConflict)
		return
	}
	sessionID := ""
	if s.store != nil {
		sessionID, err = s.store.beginScan(root)
		if err != nil {
			s.mu.Unlock()
			http.Error(w, "无法创建扫描会话", http.StatusInternalServerError)
			return
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &scanJob{
		status: ScanStatus{
			State: "running", Phase: "scanning", Root: root,
			PolicyID: policy.ID, PolicyVersion: policy.Version,
		},
		started:         time.Now(),
		cancel:          cancel,
		known:           make(map[string]FileRecord),
		folders:         make(map[string]FolderRecord),
		duplicateByPath: make(map[string]string),
		store:           s.store,
		sessionID:       sessionID,
	}
	s.job = job
	s.mu.Unlock()
	go job.run(ctx, root, ScanOptions{
		Minimum:          request.Minimum,
		DuplicateMinimum: request.DuplicateMinimum,
		Limit:            request.Limit,
		Excludes:         request.Excludes,
		Policy:           policy,
	})
	writeJSON(w, map[string]bool{"started": true})
}

func (s *server) status(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	job := s.job
	s.mu.Unlock()
	if job == nil {
		writeJSON(w, ScanStatus{State: "idle"})
		return
	}
	writeJSON(w, job.snapshot())
}

func (s *server) folders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	job := s.job
	s.mu.Unlock()
	if job == nil {
		http.Error(w, "请先扫描", http.StatusBadRequest)
		return
	}
	path, err := filepath.Abs(filepath.Clean(r.URL.Query().Get("path")))
	if err != nil {
		http.Error(w, "路径无效", http.StatusBadRequest)
		return
	}
	view, ok := job.folderView(path)
	if !ok {
		http.Error(w, "目录不在当前扫描结果中", http.StatusNotFound)
		return
	}
	writeJSON(w, view)
}

func (s *server) cancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	job := s.job
	s.mu.Unlock()
	if job != nil {
		job.cancel()
	}
	writeJSON(w, map[string]bool{"cancelled": true})
}

func (s *server) trash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		Path string `json:"path"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	transactionID, released, results, err := s.executeCleanup([]string{request.Path})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if results[0].Error != "" {
		http.Error(w, results[0].Error, http.StatusConflict)
		return
	}
	writeJSON(w, map[string]string{
		"released":      strconv.FormatInt(released, 10),
		"transactionId": transactionID,
	})
}

type trashResult struct {
	Path       string `json:"path"`
	Released   int64  `json:"released"`
	State      string `json:"state"`
	Error      string `json:"error,omitempty"`
	AuditError string `json:"auditError,omitempty"`
}

func (s *server) trashBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		Paths []string `json:"paths"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if len(request.Paths) < 1 || len(request.Paths) > 500 {
		http.Error(w, "每次请选择 1 到 500 个文件", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	job := s.job
	s.mu.Unlock()
	if job != nil && job.wouldRemoveEveryDuplicate(request.Paths) {
		http.Error(w, "清理计划必须为每组重复文件至少保留一个副本", http.StatusBadRequest)
		return
	}
	transactionID, released, results, err := s.executeCleanup(request.Paths)
	if err != nil {
		http.Error(w, "无法创建清理事务："+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"transactionId": transactionID,
		"released":      released,
		"results":       results,
	})
}

func (s *server) executeCleanup(paths []string) (string, int64, []trashResult, error) {
	seen := make(map[string]bool)
	unique := make([]string, 0, len(paths))
	records := make([]FileRecord, 0, len(paths))
	s.mu.Lock()
	job := s.job
	s.mu.Unlock()
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		unique = append(unique, path)
		record := FileRecord{Path: path}
		if job != nil {
			job.mu.RLock()
			knownRecord, ok := job.known[path]
			job.mu.RUnlock()
			if ok {
				record = knownRecord
			}
		}
		records = append(records, record)
	}
	transactionID := ""
	var err error
	if s.store != nil {
		transactionID, err = s.store.beginCleanup(records)
		if err != nil {
			return "", 0, nil, err
		}
	}
	results := make([]trashResult, 0, len(unique))
	var released int64
	successful := make(map[string]bool)
	for _, path := range unique {
		record, trashErr := s.trashOne(path)
		result := trashResult{Path: path, State: "failed"}
		if trashErr != nil {
			result.Error = trashErr.Error()
			if strings.Contains(result.Error, "发生变化") || strings.Contains(result.Error, "类型已经变化") {
				result.State = "skipped_changed"
			}
		} else {
			result.State = "trashed"
			result.Released = record.Size
			released += record.Size
			successful[path] = true
		}
		if s.store != nil {
			if logErr := s.store.markCleanupItem(
				transactionID, path, result.State, result.Error, result.Released,
			); logErr != nil {
				result.AuditError = "写入逐项审计日志失败：" + logErr.Error()
			}
		}
		results = append(results, result)
	}
	if s.store != nil {
		state := "completed"
		if len(successful) == 0 {
			state = "failed"
		} else if len(successful) != len(unique) {
			state = "partial"
		}
		if err := s.store.finishCleanup(transactionID, state); err != nil {
			results[0].AuditError = "完成清理事务日志失败：" + err.Error()
			return transactionID, released, results, nil
		}
		if len(successful) > 0 {
			plan, err := s.store.loadPlan()
			if err != nil {
				results[0].AuditError = "更新持久化计划失败：" + err.Error()
				return transactionID, released, results, nil
			}
			remaining := plan[:0]
			for _, record := range plan {
				if !successful[record.Path] {
					remaining = append(remaining, record)
				}
			}
			if err := s.store.replacePlan(remaining); err != nil {
				results[0].AuditError = "更新持久化计划失败：" + err.Error()
			}
		}
	}
	return transactionID, released, results, nil
}

func (s *server) trashOne(path string) (FileRecord, error) {
	s.mu.Lock()
	job := s.job
	s.mu.Unlock()
	if job == nil {
		return FileRecord{}, fmt.Errorf("请先扫描")
	}
	job.mu.RLock()
	state := job.status.State
	record, known := job.known[path]
	job.mu.RUnlock()
	if state == "running" {
		return FileRecord{}, fmt.Errorf("扫描完成后才能清理")
	}
	if !known {
		return FileRecord{}, fmt.Errorf("只能清理当前扫描结果中的文件")
	}
	if record.Advice.Level == "danger" {
		return FileRecord{}, fmt.Errorf("受保护文件不能清理")
	}
	if err := validateTrashCandidate(record); err != nil {
		return FileRecord{}, err
	}
	if err := moveToTrash(record.Path); err != nil {
		return FileRecord{}, fmt.Errorf("移入回收站失败：%w", err)
	}
	job.mu.Lock()
	job.removeRecordLocked(record)
	job.mu.Unlock()
	return record, nil
}

func validateTrashCandidate(record FileRecord) error {
	info, err := os.Lstat(record.Path)
	if err != nil {
		return fmt.Errorf("文件已不存在或无法读取，请重新扫描")
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("文件类型已经变化，请重新扫描")
	}
	if info.Size() != record.Size || info.ModTime().UnixNano() != record.ModifiedUnixNano {
		return fmt.Errorf("文件在扫描后发生变化，已阻止清理，请重新扫描")
	}
	return nil
}

func (s *server) plan(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		if r.Method == http.MethodGet {
			writeJSON(w, []FileRecord{})
			return
		}
		http.Error(w, "本地索引不可用", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		records, err := s.store.loadPlan()
		if err != nil {
			http.Error(w, "读取清理计划失败", http.StatusInternalServerError)
			return
		}
		writeJSON(w, records)
	case http.MethodPost:
		var request struct {
			Paths []string `json:"paths"`
		}
		if !decodeJSON(w, r, &request) {
			return
		}
		if len(request.Paths) > 500 {
			http.Error(w, "清理计划最多 500 个文件", http.StatusBadRequest)
			return
		}
		existing, err := s.store.loadPlan()
		if err != nil {
			http.Error(w, "读取清理计划失败", http.StatusInternalServerError)
			return
		}
		known := make(map[string]FileRecord, len(existing))
		for _, record := range existing {
			known[record.Path] = record
		}
		s.mu.Lock()
		job := s.job
		s.mu.Unlock()
		if job != nil {
			job.mu.RLock()
			for path, record := range job.known {
				known[path] = record
			}
			job.mu.RUnlock()
		}
		records := make([]FileRecord, 0, len(request.Paths))
		seen := make(map[string]bool)
		for _, path := range request.Paths {
			record, ok := known[path]
			if !ok || seen[path] || record.Advice.Level == "danger" {
				http.Error(w, "计划包含未知、重复或受保护的文件", http.StatusBadRequest)
				return
			}
			seen[path] = true
			records = append(records, record)
		}
		if job != nil && job.wouldRemoveEveryDuplicate(request.Paths) {
			http.Error(w, "清理计划必须为每组重复文件至少保留一个副本", http.StatusBadRequest)
			return
		}
		if err := s.store.replacePlan(records); err != nil {
			http.Error(w, "保存清理计划失败", http.StatusInternalServerError)
			return
		}
		writeJSON(w, records)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) policies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	state, err := s.store.policies()
	if err != nil {
		http.Error(w, "读取策略失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, state)
}

func (s *server) activatePolicy(w http.ResponseWriter, r *http.Request) {
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
	if err := s.store.activatePolicy(request.ID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	state, err := s.store.policies()
	if err != nil {
		http.Error(w, "读取策略失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, state)
}

func (s *server) importPolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 512*1024))
	if err != nil {
		http.Error(w, "策略请求过大", http.StatusBadRequest)
		return
	}
	policy, err := decodePolicy(data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.importPolicy(policy); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, policy)
}

func (s *server) audit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if id := r.URL.Query().Get("id"); id != "" {
		detail, err := s.store.auditDetail(id)
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "审计事务不存在", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "读取审计详情失败", http.StatusInternalServerError)
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
	summaries, err := s.store.auditSummaries(limit)
	if err != nil {
		http.Error(w, "读取审计记录失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, summaries)
}

func (s *server) maintenance(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		health, err := s.store.databaseHealth()
		if err != nil {
			http.Error(w, "读取数据库健康状态失败", http.StatusInternalServerError)
			return
		}
		writeJSON(w, health)
	case http.MethodPost:
		s.mu.Lock()
		job := s.job
		s.mu.Unlock()
		if job != nil && job.snapshot().State == "running" {
			http.Error(w, "扫描期间不能执行数据库维护", http.StatusConflict)
			return
		}
		var request struct {
			Action string `json:"action"`
		}
		if !decodeJSON(w, r, &request) {
			return
		}
		if err := s.store.maintain(request.Action); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		health, err := s.store.databaseHealth()
		if err != nil {
			http.Error(w, "维护完成但无法读取健康状态", http.StatusInternalServerError)
			return
		}
		writeJSON(w, health)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (j *scanJob) removeRecordLocked(record FileRecord) {
	delete(j.known, record.Path)
	for index := range j.status.Results {
		if j.status.Results[index].Path == record.Path {
			j.status.Results = append(j.status.Results[:index], j.status.Results[index+1:]...)
			break
		}
	}
	directory := filepath.Dir(record.Path)
	for {
		folder, ok := j.folders[directory]
		if !ok {
			break
		}
		folder.Size -= record.Size
		folder.FileCount--
		if folder.Size < 0 {
			folder.Size = 0
		}
		if folder.FileCount < 0 {
			folder.FileCount = 0
		}
		j.folders[directory] = folder
		if directory == j.status.Root {
			break
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	groupID := j.duplicateByPath[record.Path]
	delete(j.duplicateByPath, record.Path)
	if groupID == "" {
		return
	}
	for index := range j.status.DuplicateGroups {
		group := &j.status.DuplicateGroups[index]
		if group.ID != groupID {
			continue
		}
		for fileIndex := range group.Files {
			if group.Files[fileIndex].Path == record.Path {
				group.Files = append(group.Files[:fileIndex], group.Files[fileIndex+1:]...)
				break
			}
		}
		if len(group.Files) < 2 {
			if len(group.Files) == 1 {
				delete(j.duplicateByPath, group.Files[0].Path)
			}
			j.status.DuplicateGroups = append(j.status.DuplicateGroups[:index], j.status.DuplicateGroups[index+1:]...)
		} else {
			group.Reclaimable = group.Size * int64(len(group.Files)-1)
		}
		break
	}
}

func (j *scanJob) wouldRemoveEveryDuplicate(paths []string) bool {
	j.mu.RLock()
	defer j.mu.RUnlock()
	selected := make(map[string]bool, len(paths))
	for _, path := range paths {
		selected[path] = true
	}
	for _, group := range j.status.DuplicateGroups {
		remaining := 0
		for _, record := range group.Files {
			if !selected[record.Path] {
				remaining++
			}
		}
		if remaining == 0 {
			return true
		}
	}
	return false
}

func (s *server) quit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, map[string]bool{"closing": true})
	go func() {
		time.Sleep(200 * time.Millisecond)
		os.Exit(0)
	}()
}

func init() {
	log.SetPrefix("SpaceSheriff: ")
	log.SetFlags(0)
}
