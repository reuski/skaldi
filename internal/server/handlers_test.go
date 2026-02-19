// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
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
		CacheDir:  t.TempDir(),
		BinDir:    t.TempDir(),
		UvBinDir:  t.TempDir(),
		MpvSocket: t.TempDir() + "/mpv.sock",
	}

	p := player.NewManager(cfg, logger)
	r := resolver.New(cfg)
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

func TestNew(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &bootstrap.Config{CacheDir: t.TempDir()}
	p := player.NewManager(cfg, logger)
	r := resolver.New(cfg)
	indexHTML := []byte("<html>Test</html>")

	s := New(logger, p, r, indexHTML, 8080)

	if s == nil {
		t.Fatal("New() returned nil")
	}

	if s.logger != logger {
		t.Error("Logger not set correctly")
	}

	if s.player != p {
		t.Error("Player not set correctly")
	}

	if s.resolver != r {
		t.Error("Resolver not set correctly")
	}

	if !bytes.Equal(s.indexHTML, indexHTML) {
		t.Error("indexHTML not set correctly")
	}

	if s.broadcaster == nil {
		t.Error("Broadcaster not initialized")
	}

	if s.server == nil {
		t.Error("Server not initialized")
	}
}
