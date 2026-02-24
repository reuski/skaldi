package history

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLogger(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "skaldi-history-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	l := New(tmpDir, logger)
	defer l.Close()

	now := time.Now()
	entry := Entry{
		Timestamp: now,
		Title:     "Test Track",
		Artist:    "Test Artist",
		SourceURL: "https://example.com/track",
	}

	l.Log(entry)

	// Wait a bit for the background worker to write
	time.Sleep(100 * time.Millisecond)

	filename := "history_" + now.Local().Format("2006-01-02") + ".jsonl"
	path := filepath.Join(tmpDir, filename)

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read history file: %v", err)
	}

	var saved Entry
	if err := json.Unmarshal(content, &saved); err != nil {
		t.Fatalf("failed to unmarshal entry: %v", err)
	}

	if saved.Title != entry.Title {
		t.Errorf("expected title %q, got %q", entry.Title, saved.Title)
	}
	if saved.SourceURL != entry.SourceURL {
		t.Errorf("expected source_url %q, got %q", entry.SourceURL, saved.SourceURL)
	}
}
