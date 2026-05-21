package history

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readSession(t *testing.T, dir string) []Record {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 session file, got %d (%v)", len(matches), matches)
	}
	f, err := os.Open(matches[0])
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	var records []Record
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		records = append(records, rec)
	}
	return records
}

func waitDrain(t *testing.T, l *Logger) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(l.queue) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("logger did not drain")
}

func TestLogger_SessionLifecycle(t *testing.T) {
	dir := t.TempDir()
	l := New(dir, slog.New(slog.NewTextHandler(os.Stdout, nil)))

	now := time.Now()
	l.Log(Entry{Time: now, Title: "Track A", Artist: "Artist A", URL: "https://example.com/a", Duration: 210})
	l.Log(Entry{Time: now.Add(time.Minute), Title: "Track B"})
	l.Rotate("daily")
	waitDrain(t, l)

	records := readSession(t, dir)
	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d: %+v", len(records), records)
	}
	if records[0].Type != Play || records[0].Title != "Track A" || records[0].Duration != 210 {
		t.Errorf("record[0] = %+v", records[0])
	}
	if records[1].Type != Play || records[1].Title != "Track B" {
		t.Errorf("record[1] = %+v", records[1])
	}
	end := records[2]
	if end.Type != SessionEnd || end.Reason != "daily" || end.Tracks != 2 {
		t.Errorf("session_end = %+v", end)
	}

	l.Close()
}

func TestLogger_RotateWithNoPlays_NoFile(t *testing.T) {
	dir := t.TempDir()
	l := New(dir, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	l.Rotate("daily")
	waitDrain(t, l)

	matches, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(matches) != 0 {
		t.Errorf("expected no files, got %v", matches)
	}
	l.Close()
}

func TestLogger_CloseFinalizesOpenSession(t *testing.T) {
	dir := t.TempDir()
	l := New(dir, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	l.Log(Entry{Time: time.Now(), Title: "Solo"})
	l.Close()

	records := readSession(t, dir)
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d: %+v", len(records), records)
	}
	if records[1].Type != SessionEnd || records[1].Reason != "shutdown" || records[1].Tracks != 1 {
		t.Errorf("session_end = %+v", records[1])
	}
}

func TestLogger_SameDayRotateAppendsToOneFile(t *testing.T) {
	dir := t.TempDir()
	l := New(dir, slog.New(slog.NewTextHandler(os.Stdout, nil)))

	l.Log(Entry{Time: time.Now(), Title: "First"})
	l.Rotate("daily")
	l.Log(Entry{Time: time.Now(), Title: "Second"})
	l.Close()

	records := readSession(t, dir)
	if len(records) != 4 {
		t.Fatalf("expected 4 records, got %d: %+v", len(records), records)
	}
	if records[0].Title != "First" || records[1].Type != SessionEnd || records[1].Reason != "daily" {
		t.Errorf("first session = %+v, %+v", records[0], records[1])
	}
	if records[2].Title != "Second" || records[3].Type != SessionEnd || records[3].Reason != "shutdown" {
		t.Errorf("second session = %+v, %+v", records[2], records[3])
	}
}
