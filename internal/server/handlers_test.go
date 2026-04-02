// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reuski/skaldi/internal/bootstrap"
	"github.com/reuski/skaldi/internal/player"
	"github.com/reuski/skaldi/internal/resolver"
)

func setupTestServer(t *testing.T) (*Server, *player.Manager) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &bootstrap.Config{
		CacheDir:   t.TempDir(),
		BinDir:     t.TempDir(),
		UvBinDir:   t.TempDir(),
		MpvSocket:  t.TempDir() + "/mpv.sock",
		ConfigPath: t.TempDir() + "/config.json",
	}

	p := player.NewManager(cfg, logger)
	r, err := resolver.New(cfg)
	if err != nil {
		t.Fatalf("resolver.New failed: %v", err)
	}
	indexHTML := []byte("<html><body>Test</body></html>")

	s := New(logger, p, r, indexHTML, 0)
	return s, p
}

func TestHandleIndex(t *testing.T) {
	s, _ := setupTestServer(t)

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{
			name:       "root_path",
			path:       "/",
			wantStatus: http.StatusOK,
			wantBody:   "Test",
		},
		{
			name:       "non_root_path",
			path:       "/other",
			wantStatus: http.StatusNotFound,
			wantBody:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rr := httptest.NewRecorder()

			s.handleIndex(rr, req)

			if rr.Code != tc.wantStatus {
				t.Errorf("Status = %d, want %d", rr.Code, tc.wantStatus)
			}

			if tc.wantBody != "" && !strings.Contains(rr.Body.String(), tc.wantBody) {
				t.Errorf("Body does not contain %q", tc.wantBody)
			}

			contentType := rr.Header().Get("Content-Type")
			if tc.wantStatus == http.StatusOK && contentType != "text/html" {
				t.Errorf("Content-Type = %q, want text/html", contentType)
			}
		})
	}
}

func TestHandlePlayback_InvalidMethod(t *testing.T) {
	s, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/playback", nil)
	rr := httptest.NewRecorder()

	s.handlePlayback(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandlePlayback_InvalidBody(t *testing.T) {
	s, _ := setupTestServer(t)

	tests := []struct {
		name string
		body string
	}{
		{
			name: "empty_body",
			body: "",
		},
		{
			name: "invalid_json",
			body: "not json",
		},
		{
			name: "malformed_json",
			body: `{"action": }`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/playback", strings.NewReader(tc.body))
			rr := httptest.NewRecorder()

			s.handlePlayback(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestHandlePlayback_InvalidAction(t *testing.T) {
	s, _ := setupTestServer(t)

	body := `{"action": "invalid_action"}`
	req := httptest.NewRequest(http.MethodPost, "/playback", strings.NewReader(body))
	rr := httptest.NewRecorder()

	s.handlePlayback(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleSearch_InvalidIntent(t *testing.T) {
	s, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/search?q=test&intent=legacy", nil)
	rr := httptest.NewRecorder()

	s.handleSearch(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleSearch_StreamsCanonicalBatchesWithoutExternalLibrary(t *testing.T) {
	s := setupSearchServer(t, false)

	req := httptest.NewRequest(http.MethodGet, "/search?q=test%20song&intent=results", nil)
	rr := httptest.NewRecorder()

	s.handleSearch(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d", rr.Code, http.StatusOK)
	}

	batches := decodeSearchBatches(t, rr.Body.String())
	if len(batches) < 2 {
		t.Fatalf("batch count = %d, want at least 2", len(batches))
	}

	seen := make(map[resolver.SearchBucket]bool)
	for _, batch := range batches {
		if batch.Intent != resolver.SearchIntentResults {
			t.Fatalf("intent = %q, want %q", batch.Intent, resolver.SearchIntentResults)
		}
		if batch.Bucket == "" {
			t.Fatal("bucket should not be empty")
		}
		seen[batch.Bucket] = true
	}

	if seen[resolver.SearchBucketExternal] {
		t.Fatal("did not expect external bucket without OpenSubsonic config")
	}
	if !seen[resolver.SearchBucketYouTube] {
		t.Fatal("expected youtube bucket")
	}
	if !seen[resolver.SearchBucketYTMusic] {
		t.Fatal("expected ytmusic bucket")
	}
}

func TestHandleQueue_InvalidMethod(t *testing.T) {
	s, _ := setupTestServer(t)

	tests := []struct {
		name string
		body string
		want int
	}{
		{
			name: "empty_body",
			body: "",
			want: http.StatusBadRequest,
		},
		{
			name: "invalid_json",
			body: "not json",
			want: http.StatusBadRequest,
		},
		{
			name: "empty_url",
			body: `{"url": ""}`,
			want: http.StatusBadRequest,
		},
		{
			name: "missing_url_field",
			body: `{"other": "value"}`,
			want: http.StatusBadRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/queue", strings.NewReader(tc.body))
			rr := httptest.NewRecorder()

			s.handleQueue(rr, req)

			if rr.Code != tc.want {
				t.Errorf("Status = %d, want %d", rr.Code, tc.want)
			}
		})
	}
}

func TestHandleRemove_InvalidIndex(t *testing.T) {
	s, _ := setupTestServer(t)

	tests := []struct {
		name  string
		index string
		want  int
	}{
		{
			name:  "empty_index",
			index: "",
			want:  http.StatusBadRequest,
		},
		{
			name:  "non_numeric",
			index: "abc",
			want:  http.StatusBadRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodDelete, "/queue/"+tc.index, nil)
			req.SetPathValue("index", tc.index)
			rr := httptest.NewRecorder()

			s.handleRemove(rr, req)

			if rr.Code != tc.want {
				t.Errorf("Status = %d, want %d", rr.Code, tc.want)
			}
		})
	}
}

func TestHandleRemove_MissingPathValue(t *testing.T) {
	s, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodDelete, "/queue/", nil)
	rr := httptest.NewRecorder()

	s.handleRemove(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleMove_InvalidBody(t *testing.T) {
	s, _ := setupTestServer(t)

	tests := []struct {
		name string
		body string
	}{
		{
			name: "empty_body",
			body: "",
		},
		{
			name: "invalid_json",
			body: "not json",
		},
		{
			name: "malformed_json",
			body: `{"from": }`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/queue/move", strings.NewReader(tc.body))
			rr := httptest.NewRecorder()

			s.handleMove(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestHandleMove_InvalidIndices(t *testing.T) {
	s, _ := setupTestServer(t)

	tests := []struct {
		name string
		body string
	}{
		{
			name: "negative_from",
			body: `{"from":-1,"to":2}`,
		},
		{
			name: "negative_to",
			body: `{"from":2,"to":-2}`,
		},
		{
			name: "same_indices",
			body: `{"from":2,"to":2}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/queue/move", strings.NewReader(tc.body))
			rr := httptest.NewRecorder()

			s.handleMove(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("Status = %d, want %d", rr.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestQueueRequest_Unmarshal(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantURL string
		wantErr bool
	}{
		{
			name:    "valid",
			json:    `{"url": "https://example.com"}`,
			wantURL: "https://example.com",
			wantErr: false,
		},
		{
			name:    "empty_url",
			json:    `{"url": ""}`,
			wantURL: "",
			wantErr: false,
		},
		{
			name:    "missing_url",
			json:    `{}`,
			wantURL: "",
			wantErr: false,
		},
		{
			name:    "invalid_json",
			json:    `{"url":`,
			wantURL: "",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var req QueueRequest
			err := json.Unmarshal([]byte(tc.json), &req)

			if tc.wantErr && err == nil {
				t.Error("Expected error but got none")
			}

			if !tc.wantErr && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tc.wantErr && req.URL != tc.wantURL {
				t.Errorf("URL = %q, want %q", req.URL, tc.wantURL)
			}
		})
	}
}

func TestPlaybackRequest_Unmarshal(t *testing.T) {
	tests := []struct {
		name       string
		json       string
		wantAction string
		wantErr    bool
	}{
		{
			name:       "pause",
			json:       `{"action": "pause"}`,
			wantAction: "pause",
			wantErr:    false,
		},
		{
			name:       "resume",
			json:       `{"action": "resume"}`,
			wantAction: "resume",
			wantErr:    false,
		},
		{
			name:       "skip",
			json:       `{"action": "skip"}`,
			wantAction: "skip",
			wantErr:    false,
		},
		{
			name:       "previous",
			json:       `{"action": "previous"}`,
			wantAction: "previous",
			wantErr:    false,
		},
		{
			name:       "empty_action",
			json:       `{"action": ""}`,
			wantAction: "",
			wantErr:    false,
		},
		{
			name:       "invalid_json",
			json:       `{"action":`,
			wantAction: "",
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var req PlaybackRequest
			err := json.Unmarshal([]byte(tc.json), &req)

			if tc.wantErr && err == nil {
				t.Error("Expected error but got none")
			}

			if !tc.wantErr && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tc.wantErr && req.Action != tc.wantAction {
				t.Errorf("Action = %q, want %q", req.Action, tc.wantAction)
			}
		})
	}
}

func TestMoveRequest_Unmarshal(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr bool
	}{
		{
			name:    "valid",
			json:    `{"from":1,"to":3}`,
			wantErr: false,
		},
		{
			name:    "missing_fields",
			json:    `{}`,
			wantErr: false,
		},
		{
			name:    "invalid_json",
			json:    `{"from":`,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var req MoveRequest
			err := json.Unmarshal([]byte(tc.json), &req)

			if tc.wantErr && err == nil {
				t.Error("Expected error but got none")
			}

			if !tc.wantErr && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

func TestNew(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &bootstrap.Config{CacheDir: t.TempDir(), ConfigPath: t.TempDir() + "/config.json"}
	p := player.NewManager(cfg, logger)
	r, err := resolver.New(cfg)
	if err != nil {
		t.Fatalf("resolver.New failed: %v", err)
	}
	indexHTML := []byte("<html>Test</html>")

	s := New(logger, p, r, indexHTML, 8080)

	if s == nil {
		t.Fatal("New() returned nil")
	} else if s.logger != logger {
		t.Error("Logger not set correctly")
	} else if s.player != p {
		t.Error("Player not set correctly")
	} else if s.resolver != r {
		t.Error("Resolver not set correctly")
	} else if !bytes.Equal(s.indexHTML, indexHTML) {
		t.Error("indexHTML not set correctly")
	} else if s.broadcaster == nil {
		t.Error("Broadcaster not initialized")
	} else if s.server == nil {
		t.Error("Server not initialized")
	}
}

func setupSearchServer(t *testing.T, withLibrary bool) *Server {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cacheDir := t.TempDir()
	binDir := t.TempDir()
	cfg := &bootstrap.Config{
		CacheDir:   cacheDir,
		BinDir:     binDir,
		UvBinDir:   t.TempDir(),
		MpvSocket:  filepath.Join(t.TempDir(), "mpv.sock"),
		ConfigPath: filepath.Join(t.TempDir(), "config.json"),
	}

	if withLibrary {
		subsonicServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"subsonic-response":{"status":"ok","searchResult3":{"song":[{"id":"lib-1","title":"Library Song","artist":"Library Artist","duration":180}]}}}`))
		}))
		t.Cleanup(subsonicServer.Close)
		if err := os.WriteFile(cfg.ConfigPath, []byte(`{
  "opensubsonic": {
    "enabled": true,
    "library_id": "personal",
    "base_url": "`+subsonicServer.URL+`",
    "username": "alice",
    "token": "secret"
  }
}`), 0o644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
	}

	writeExecutable(t, cfg.ShimPath(), `#!/bin/sh
last=""
for arg in "$@"; do
  last="$arg"
done
case "$last" in
  ytsearch8:*)
    printf '%s\n' '{"id":"shared-1","title":"Test Song","uploader":"Loose Channel","webpage_url":"https://www.youtube.com/watch?v=shared-1","ie_key":"Youtube"}'
    ;;
  https://music.youtube.com/search\?q=*)
    sleep 0.05
    printf '%s\n' '{"id":"shared-1","title":"Test Song","artist":"Precise Artist","duration":201,"thumbnail":"https://img.example/shared-1.jpg","webpage_url":"https://music.youtube.com/watch?v=shared-1","ie_key":"Youtube"}'
    printf '%s\n' '{"id":"music-only-2","title":"Other Song","artist":"Other Artist","duration":202,"thumbnail":"https://img.example/music-only-2.jpg","webpage_url":"https://music.youtube.com/watch?v=music-only-2","ie_key":"Youtube"}'
    ;;
esac
`)

	p := player.NewManager(cfg, logger)
	r, err := resolver.New(cfg)
	if err != nil {
		t.Fatalf("resolver.New failed: %v", err)
	}
	return New(logger, p, r, []byte("<html>Test</html>"), 0)
}

func decodeSearchBatches(t *testing.T, body string) []resolver.SearchBatch {
	t.Helper()

	var batches []resolver.SearchBatch
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var batch resolver.SearchBatch
		if err := json.Unmarshal([]byte(line), &batch); err != nil {
			t.Fatalf("Unmarshal failed: %v", err)
		}
		batches = append(batches, batch)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("Scanner failed: %v", err)
	}
	return batches
}

func writeExecutable(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
}
