package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSecureRequiresSessionTokenForAPI(t *testing.T) {
	app := &server{token: "expected"}
	handler := app.secure(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	without := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1234/api/status", nil)
	without.Host = "127.0.0.1:1234"
	withoutResult := httptest.NewRecorder()
	handler.ServeHTTP(withoutResult, without)
	if withoutResult.Code != http.StatusForbidden {
		t.Fatalf("without token got %d, want 403", withoutResult.Code)
	}

	with := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1234/api/status", nil)
	with.Host = "127.0.0.1:1234"
	with.Header.Set("X-Space-Sheriff-Token", "expected")
	withResult := httptest.NewRecorder()
	handler.ServeHTTP(withResult, with)
	if withResult.Code != http.StatusNoContent {
		t.Fatalf("with token got %d, want 204", withResult.Code)
	}

	lookalike := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1234/api/status", nil)
	lookalike.Host = "localhost.example:1234"
	lookalike.Header.Set("X-Space-Sheriff-Token", "expected")
	lookalikeResult := httptest.NewRecorder()
	handler.ServeHTTP(lookalikeResult, lookalike)
	if lookalikeResult.Code != http.StatusForbidden {
		t.Fatalf("lookalike host got %d, want 403", lookalikeResult.Code)
	}
}

func TestNewSessionTokenAllowsDeterministicLocalTesting(t *testing.T) {
	t.Setenv("SPACE_SHERIFF_SESSION_TOKEN", "test-token")
	token, err := newSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if token != "test-token" {
		t.Fatalf("got %q, want test-token", token)
	}
}

func TestNewSessionTokenIsRandomByDefault(t *testing.T) {
	t.Setenv("SPACE_SHERIFF_SESSION_TOKEN", "")
	first, err := newSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 64 || first == second {
		t.Fatalf("unexpected tokens %q and %q", first, second)
	}
}

func TestValidateTrashCandidateDetectsChanges(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "candidate.bin")
	if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	record := FileRecord{
		Path:             path,
		Size:             info.Size(),
		ModifiedUnixNano: info.ModTime().UnixNano(),
	}
	if err := validateTrashCandidate(record); err != nil {
		t.Fatalf("unchanged file rejected: %v", err)
	}
	if err := os.WriteFile(path, []byte("changed-size"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateTrashCandidate(record); err == nil {
		t.Fatal("changed file was not rejected")
	}
}

func TestFolderHandlerReturnsAggregates(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	job := &scanJob{
		status: ScanStatus{State: "done", Root: root},
		folders: map[string]FolderRecord{
			root:  {Path: root, Name: root, Size: 12, FileCount: 2},
			child: {Path: child, Name: "child", Size: 8, FileCount: 1},
		},
	}
	app := &server{job: job}
	request := httptest.NewRequest(http.MethodGet, "/api/folders?path="+root, nil)
	result := httptest.NewRecorder()
	app.folders(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", result.Code)
	}
	var view FolderView
	if err := json.NewDecoder(result.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if view.Current.Size != 12 || len(view.Children) != 1 || view.Children[0].Path != child {
		t.Fatalf("unexpected folder view: %+v", view)
	}
}

func TestTrashBatchReportsEachUniqueFailure(t *testing.T) {
	app := &server{job: &scanJob{
		status: ScanStatus{State: "done"},
		known:  make(map[string]FileRecord),
	}}
	body := bytes.NewBufferString(`{"paths":["missing","missing","other"]}`)
	request := httptest.NewRequest(http.MethodPost, "/api/trash-batch", body)
	result := httptest.NewRecorder()
	app.trashBatch(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", result.Code)
	}
	var response struct {
		Released int64         `json:"released"`
		Results  []trashResult `json:"results"`
	}
	if err := json.NewDecoder(result.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Released != 0 || len(response.Results) != 2 {
		t.Fatalf("unexpected batch response: %+v", response)
	}
	for _, item := range response.Results {
		if item.Error == "" {
			t.Fatalf("missing error for %+v", item)
		}
	}
}

func TestTrashBatchKeepsOneDuplicateCopy(t *testing.T) {
	app := &server{job: &scanJob{
		status: ScanStatus{
			State: "done",
			DuplicateGroups: []DuplicateGroup{{
				ID:    "group",
				Files: []FileRecord{{Path: "one"}, {Path: "two"}},
			}},
		},
	}}
	body := bytes.NewBufferString(`{"paths":["one","two"]}`)
	result := httptest.NewRecorder()
	app.trashBatch(result, httptest.NewRequest(http.MethodPost, "/api/trash-batch", body))
	if result.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", result.Code)
	}
}

func TestVersionStatusAndCancelHandlers(t *testing.T) {
	app := &server{}

	versionResult := httptest.NewRecorder()
	app.version(versionResult, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if versionResult.Code != http.StatusOK || !strings.Contains(versionResult.Body.String(), "0.3.0-dev") {
		t.Fatalf("unexpected version response: %d %s", versionResult.Code, versionResult.Body.String())
	}

	statusResult := httptest.NewRecorder()
	app.status(statusResult, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if statusResult.Code != http.StatusOK || !strings.Contains(statusResult.Body.String(), `"state":"idle"`) {
		t.Fatalf("unexpected status response: %d %s", statusResult.Code, statusResult.Body.String())
	}

	cancelled := false
	app.job = &scanJob{cancel: func() { cancelled = true }}
	cancelResult := httptest.NewRecorder()
	app.cancel(cancelResult, httptest.NewRequest(http.MethodPost, "/api/cancel", nil))
	if cancelResult.Code != http.StatusOK || !cancelled {
		t.Fatalf("cancel response: %d, cancelled=%v", cancelResult.Code, cancelled)
	}
}

func TestStartScanValidatesAndCompletes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.bin"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &server{}
	body := bytes.NewBufferString(`{"path":` + strconvQuote(root) + `,"minimum":0,"duplicateMinimum":1,"limit":20,"excludes":[".git"]}`)
	result := httptest.NewRecorder()
	app.startScan(result, httptest.NewRequest(http.MethodPost, "/api/scan", body))
	if result.Code != http.StatusOK {
		t.Fatalf("start scan got %d: %s", result.Code, result.Body.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	for app.job.snapshot().State == "running" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	status := app.job.snapshot()
	if status.State != "done" || status.FilesSeen != 1 {
		t.Fatalf("unexpected completed scan: %+v", status)
	}

	invalidResult := httptest.NewRecorder()
	invalidBody := bytes.NewBufferString(`{"path":"/path/that/does/not/exist"}`)
	app.startScan(invalidResult, httptest.NewRequest(http.MethodPost, "/api/scan", invalidBody))
	if invalidResult.Code != http.StatusBadRequest {
		t.Fatalf("invalid path got %d, want 400", invalidResult.Code)
	}
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func TestTrashOneRejectsUnsafeStates(t *testing.T) {
	app := &server{}
	if _, err := app.trashOne("missing"); err == nil {
		t.Fatal("trash without scan was allowed")
	}

	app.job = &scanJob{
		status: ScanStatus{State: "running"},
		known:  map[string]FileRecord{"file": {Path: "file"}},
	}
	if _, err := app.trashOne("file"); err == nil {
		t.Fatal("trash during scan was allowed")
	}

	app.job.status.State = "done"
	app.job.known["system"] = FileRecord{Path: "system", Advice: Advice{Level: "danger"}}
	if _, err := app.trashOne("unknown"); err == nil {
		t.Fatal("unknown file was allowed")
	}
	if _, err := app.trashOne("system"); err == nil {
		t.Fatal("protected file was allowed")
	}
}

func TestRemoveRecordUpdatesFoldersAndDuplicateGroups(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "one.bin")
	other := filepath.Join(root, "two.bin")
	record := FileRecord{Path: path, Size: 10}
	job := &scanJob{
		status: ScanStatus{
			Root:    root,
			Results: []FileRecord{record},
			DuplicateGroups: []DuplicateGroup{{
				ID: "group", Size: 10, Reclaimable: 10,
				Files: []FileRecord{record, {Path: other, Size: 10}},
			}},
		},
		known:           map[string]FileRecord{path: record, other: {Path: other, Size: 10}},
		folders:         map[string]FolderRecord{root: {Path: root, Size: 20, FileCount: 2}},
		duplicateByPath: map[string]string{path: "group", other: "group"},
	}
	job.removeRecordLocked(record)
	if len(job.status.Results) != 0 || len(job.status.DuplicateGroups) != 0 {
		t.Fatalf("record remained: %+v", job.status)
	}
	if job.folders[root].Size != 10 || job.folders[root].FileCount != 1 {
		t.Fatalf("folder not updated: %+v", job.folders[root])
	}
	if job.duplicateByPath[other] != "" {
		t.Fatal("remaining single file still marked duplicate")
	}
}
