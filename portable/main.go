package main

import (
	"context"
	"embed"
	"encoding/json"
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

type server struct {
	mu  sync.Mutex
	job *scanJob
}

func main() {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	staticFiles, _ := fs.Sub(webFiles, "web")
	app := &server{}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(staticFiles)))
	mux.HandleFunc("/api/roots", app.roots)
	mux.HandleFunc("/api/scan", app.startScan)
	mux.HandleFunc("/api/status", app.status)
	mux.HandleFunc("/api/cancel", app.cancel)
	mux.HandleFunc("/api/trash", app.trash)
	mux.HandleFunc("/api/quit", app.quit)
	handler := app.secure(mux)
	url := "http://" + listener.Addr().String()

	go func() {
		time.Sleep(250 * time.Millisecond)
		_ = openBrowser(url)
	}()
	if err := http.Serve(listener, handler); err != nil {
		log.Fatal(err)
	}
}

func (s *server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if !strings.HasPrefix(host, "127.0.0.1:") && !strings.HasPrefix(host, "localhost:") {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost && r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'")
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

func (s *server) cancel(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	job := s.job
	s.mu.Unlock()
	if job != nil {
		job.cancel()
	}
	writeJSON(w, map[string]bool{"cancelled": true})
}

func (s *server) trash(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Path string `json:"path"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	s.mu.Lock()
	job := s.job
	s.mu.Unlock()
	if job == nil {
		http.Error(w, "请先扫描", http.StatusBadRequest)
		return
	}
	job.mu.RLock()
	record, known := job.known[request.Path]
	job.mu.RUnlock()
	if !known {
		http.Error(w, "只能清理当前扫描结果中的文件", http.StatusForbidden)
		return
	}
	if record.Advice.Level == "danger" {
		http.Error(w, "受保护文件不能清理", http.StatusForbidden)
		return
	}
	if err := moveToTrash(record.Path); err != nil {
		http.Error(w, "移入回收站失败："+err.Error(), http.StatusInternalServerError)
		return
	}
	job.mu.Lock()
	delete(job.known, record.Path)
	for index := range job.status.Results {
		if job.status.Results[index].Path == record.Path {
			job.status.Results = append(job.status.Results[:index], job.status.Results[index+1:]...)
			break
		}
	}
	job.mu.Unlock()
	writeJSON(w, map[string]string{"released": strconv.FormatInt(record.Size, 10)})
}

func (s *server) quit(w http.ResponseWriter, r *http.Request) {
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
