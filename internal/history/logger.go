// SPDX-License-Identifier: AGPL-3.0-or-later

package history

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Entry struct {
	Timestamp time.Time `json:"timestamp"`
	Title     string    `json:"title,omitempty"`
	Artist    string    `json:"artist,omitempty"`
	SourceURL string    `json:"source_url,omitempty"`
}

type Logger struct {
	dataDir string
	logger  *slog.Logger
	entries chan Entry
	wg      sync.WaitGroup
}

func New(dataDir string, logger *slog.Logger) *Logger {
	l := &Logger{
		dataDir: dataDir,
		logger:  logger,
		entries: make(chan Entry, 100),
	}
	l.wg.Add(1)
	go l.run()
	return l
}

func (l *Logger) Log(e Entry) {
	select {
	case l.entries <- e:
	default:
		l.logger.Warn("History buffer full, dropping entry", "title", e.Title)
	}
}

func (l *Logger) Close() {
	close(l.entries)
	l.wg.Wait()
}

func (l *Logger) run() {
	defer l.wg.Done()

	for entry := range l.entries {
		if err := l.write(entry); err != nil {
			l.logger.Error("Failed to write history entry", "error", err)
		}
	}
}

func (l *Logger) write(e Entry) error {
	filename := fmt.Sprintf("history_%s.jsonl", e.Timestamp.Format("2006-01-02"))
	path := filepath.Join(l.dataDir, filename)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open history file: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("failed to marshal entry: %w", err)
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write entry: %w", err)
	}
	return nil
}
