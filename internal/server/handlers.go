// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/reuski/skaldi/internal/resolver"
)

type QueueRequest struct {
	URL string `json:"url"`
}

type PlaybackRequest struct {
	Action string `json:"action"`
	Index  int    `json:"index"`
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	_, _ = w.Write(s.indexHTML)
}

func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
	var req QueueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.URL == "" {
		http.Error(w, "URL is required", http.StatusBadRequest)
		return
	}

	tracks, err := s.resolver.Resolve(r.Context(), req.URL)
	if err != nil {
		s.logger.Error("Failed to resolve URL", "url", req.URL, "error", err)
		http.Error(w, fmt.Sprintf("Failed to resolve URL: %v", err), http.StatusInternalServerError)
		return
	}

	count := 0
	for _, track := range tracks {
		urlToQueue := track.WebpageURL
		if urlToQueue == "" {
			urlToQueue = track.URL
		}
		if urlToQueue == "" {
			continue
		}

		s.player.State.StoreMetadata(urlToQueue, track)

		if _, err := s.player.Exec("loadfile", urlToQueue, "append-play"); err != nil {
			s.logger.Error("Failed to enqueue track", "url", urlToQueue, "error", err)
			continue
		}
		count++
	}

	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "queued",
		"count":  count,
		"tracks": tracks,
	})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	source := r.URL.Query().Get("src")
	if query == "" {
		http.Error(w, "Query is required", http.StatusBadRequest)
		return
	}

	tracks, err := s.resolver.Search(r.Context(), query, 5, source)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		s.logger.Error("Failed to search", "query", query, "error", err)
		http.Error(w, "Search failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tracks)
}

func (s *Server) handlePlayback(w http.ResponseWriter, r *http.Request) {
	var req PlaybackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	var err error
	switch req.Action {
	case "pause":
		_, err = s.player.Exec("set_property", "pause", true)
	case "resume":
		_, err = s.player.Exec("set_property", "pause", false)
	case "skip":
		_, err = s.player.Exec("playlist-next")
	case "previous":
		_, err = s.player.Exec("playlist-prev")
	case "play":
		err = s.player.PlayIndex(req.Index)
	default:
		http.Error(w, "Invalid action", http.StatusBadRequest)
		return
	}

	if err != nil {
		s.logger.Error("Playback action failed", "action", req.Action, "error", err)
		http.Error(w, "Action failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleRemove(w http.ResponseWriter, r *http.Request) {
	indexStr := r.PathValue("index")
	if indexStr == "" {
		http.Error(w, "Index required", http.StatusBadRequest)
		return
	}

	index, err := strconv.Atoi(indexStr)
	if err != nil {
		http.Error(w, "Invalid index", http.StatusBadRequest)
		return
	}

	if _, err := s.player.Exec("playlist-remove", index); err != nil {
		s.logger.Error("Failed to remove item", "index", index, "error", err)
		http.Error(w, "Remove failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20)
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		http.Error(w, "File too large or invalid multipart", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Invalid file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	tempDir := os.TempDir()
	if _, err := os.Stat("/dev/shm"); err == nil {
		tempDir = "/dev/shm"
	}

	safeFilename := filepath.Base(header.Filename)
	dstPath := filepath.Join(tempDir, fmt.Sprintf("skaldi_%d_%s", time.Now().UnixNano(), safeFilename))

	dst, err := os.Create(dstPath)
	if err != nil {
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}

	s.player.RegisterTempFile(dstPath)

	track := resolver.Track{
		Title:    header.Filename,
		Uploader: "Local Upload",
	}
	s.player.State.StoreMetadata(dstPath, track)

	if _, err := s.player.Exec("loadfile", dstPath, "append-play"); err != nil {
		http.Error(w, "Failed to enqueue", http.StatusInternalServerError)
		os.Remove(dstPath)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}
