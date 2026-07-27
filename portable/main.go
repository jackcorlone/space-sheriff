package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
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

var version = "0.2.0-dev"

type server struct {
	mu    sync.Mutex
	job   *scanJob
	token string
}

func main() {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	staticFiles, _ := fs.Sub(webFiles, "web")
	token, err := newSessionToken()
	if err != nil {
		log.Fatal(err)
	}
	app := &server{token: token}
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
	mux.HandleFunc("/api/quit", app.quit)
	handler := app.secure(mux)
	url := "http://" + listener.Addr().String() + "/?token=" + app.token

	go func() {
		time.Sleep(250 * time.Millisecond)
		_ = openBrowser(url)
	}()
	if err := http.Serve(listener, handler); err != nil {
		log.Fatal(err)
	}
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
		if r.Method == http.MethodPost && r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
			return
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024)).Decode(value); err != nil {
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
		Path    string `json:"path"`
		Minimum int64  `json:"minimum"`
		Limit   int    `json:"limit"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	root, err := filepath.Abs(filepath.Clean(request.Path))
	if err != nil {
		http.Error(w, "路径无效", http.StatusBadRequest)
		return
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		http.Error(w, "请选择存在的磁盘或文件夹", http.StatusBadRequest)
		return
	}
	if request.Minimum < 0 {
		request.Minimum = 0
	}
	if request.Limit < 1 || request.Limit > 5000 {
		request.Limit = 2000
	}

	s.mu.Lock()
	if s.job != nil && s.job.snapshot().State == "running" {
		s.mu.Unlock()
		http.Error(w, "已有扫描任务正在运行", http.StatusConflict)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &scanJob{
		status:  ScanStatus{State: "running", Root: root},
		started: time.Now(),
		cancel:  cancel,
		known:   make(map[string]FileRecord),
		folders: make(map[string]FolderRecord),
	}
	s.job = job
	s.mu.Unlock()
	go job.run(ctx, root, request.Minimum, request.Limit)
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
	record, err := s.trashOne(request.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]string{"released": strconv.FormatInt(record.Size, 10)})
}

type trashResult struct {
	Path     string `json:"path"`
	Released int64  `json:"released"`
	Error    string `json:"error,omitempty"`
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
	seen := make(map[string]bool)
	results := make([]trashResult, 0, len(request.Paths))
	var released int64
	for _, path := range request.Paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		record, err := s.trashOne(path)
		result := trashResult{Path: path}
		if err != nil {
			result.Error = err.Error()
		} else {
			result.Released = record.Size
			released += record.Size
		}
		results = append(results, result)
	}
	writeJSON(w, map[string]any{"released": released, "results": results})
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
