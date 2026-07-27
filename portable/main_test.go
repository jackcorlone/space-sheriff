package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
