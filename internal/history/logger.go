// SPDX-License-Identifier: AGPL-3.0-or-later

// Package history persists per-session playback logs as JSONL.
package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Kind string

const (
	Play       Kind = "play"
	SessionEnd Kind = "session_end"
)

type Record struct {
	Type     Kind      `json:"type"`
	Time     time.Time `json:"time"`
	Title    string    `json:"title,omitempty"`
	Artist   string    `json:"artist,omitempty"`
	URL      string    `json:"url,omitempty"`
	Duration float64   `json:"duration,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	Tracks   int       `json:"tracks,omitempty"`
}

type Entry struct {
	Time     time.Time
	Title    string
	Artist   string
	URL      string
	Duration float64
}

type Logger struct {
	dir    string
	slog   *slog.Logger
	queue  chan Record
	wg     sync.WaitGroup
	file   *os.File
	tracks int
}

func New(dir string, slog *slog.Logger) *Logger {
	l := &Logger{
		dir:   dir,
		slog:  slog,
		queue: make(chan Record, 100),
	}
	l.wg.Add(1)
	go l.run()
	return l
}

func (l *Logger) Log(e Entry) {
	rec := Record{
		Type:     Play,
		Time:     e.Time,
		Title:    e.Title,
		Artist:   e.Artist,
		URL:      e.URL,
		Duration: e.Duration,
	}
	select {
	case l.queue <- rec:
	default:
		l.slog.Warn("History buffer full, dropping play", "title", rec.Title)
	}
}

func (l *Logger) Rotate(reason string) {
	l.queue <- Record{Type: SessionEnd, Time: time.Now(), Reason: reason}
}

func (l *Logger) Close() {
	l.Rotate("shutdown")
	close(l.queue)
	l.wg.Wait()
}

func (l *Logger) run() {
	defer l.wg.Done()
	for rec := range l.queue {
		if err := l.write(rec); err != nil {
			l.slog.Error("History write failed", "error", err)
		}
	}
}

func (l *Logger) write(rec Record) error {
	if rec.Type == SessionEnd {
		return l.close(rec)
	}
	return l.play(rec)
}

func (l *Logger) play(rec Record) error {
	if l.file == nil {
		path := filepath.Join(l.dir, rec.Time.Local().Format("2006-01-02")+".jsonl")
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("open session file: %w", err)
		}
		l.file = f
	}
	if err := l.encode(rec); err != nil {
		return err
	}
	l.tracks++
	return nil
}

func (l *Logger) close(rec Record) error {
	if l.file == nil {
		return nil
	}
	rec.Tracks = l.tracks
	encodeErr := l.encode(rec)
	closeErr := l.file.Close()
	l.file = nil
	l.tracks = 0
	return errors.Join(encodeErr, closeErr)
}

func (l *Logger) encode(rec Record) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal record: %w", err)
	}
	_, err = l.file.Write(append(data, '\n'))
	return err
}
