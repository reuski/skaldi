// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"context"
	"encoding/json"
	"errors"
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
	URL  string               `json:"url,omitempty"`
	Hits []resolver.SearchHit `json:"hits,omitempty"`
}

type PlaybackRequest struct {
	Action string   `json:"action"`
	Index  int      `json:"index"`
	Value  *float64 `json:"value,omitempty"`
}

type MoveRequest struct {
	From int `json:"from"`
	To   int `json:"to"`
}

type healthResponse struct {
	Status     string `json:"status"`
	Version    string `json:"version"`
	Playback   string `json:"playback"`
	NowPlaying string `json:"now_playing,omitempty"`
	Queue      int    `json:"queue"`
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	snap := s.player.State.Snapshot()

	resp := healthResponse{
		Status:   "ok",
		Version:  s.version,
		Playback: string(snap.Status),
		Queue:    len(snap.Queue),
	}
	if snap.NowPlaying != nil {
		resp.NowPlaying = snap.NowPlaying.Title
		if resp.NowPlaying == "" {
			resp.NowPlaying = snap.NowPlaying.Filename
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
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

	switch {
	case req.URL == "" && len(req.Hits) == 0:
		http.Error(w, "URL or hits are required", http.StatusBadRequest)
		return
	case req.URL != "" && len(req.Hits) > 0:
		http.Error(w, "Provide either url or hits", http.StatusBadRequest)
		return
	}

	var (
		tracks   []resolver.Track
		err      error
		rejected int
	)

	if req.URL != "" {
		tracks, err = s.resolver.Resolve(r.Context(), req.URL)
		if err != nil {
			s.logger.Error("Failed to resolve URL", "url", req.URL, "error", err)
			http.Error(w, fmt.Sprintf("Failed to resolve URL: %v", err), http.StatusInternalServerError)
			return
		}
	} else {
		tracks, rejected = s.resolveQueueHits(r.Context(), req.Hits)
		if len(tracks) == 0 {
			http.Error(w, "No tracks could be queued", http.StatusBadRequest)
			return
		}
	}

	queuedTracks := s.queueTracks(tracks)
	if len(queuedTracks) == 0 {
		http.Error(w, "Failed to enqueue tracks", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":   "queued",
		"count":    len(queuedTracks),
		"rejected": rejected,
		"tracks":   queuedTracks,
	})
}

func (s *Server) resolveQueueHits(ctx context.Context, hits []resolver.SearchHit) ([]resolver.Track, int) {
	tracks := make([]resolver.Track, 0, len(hits))
	rejected := 0
	for _, hit := range hits {
		resolvedTracks, err := s.resolveQueueHit(ctx, hit)
		if err != nil {
			rejected++
			s.logger.Error("Failed to queue search hit", "source", hit.Source, "queue_url", hit.QueueURL, "error", err)
			continue
		}
		tracks = append(tracks, resolvedTracks...)
	}
	return tracks, rejected
}

func (s *Server) resolveQueueHit(ctx context.Context, hit resolver.SearchHit) ([]resolver.Track, error) {
	if hit.Kind == resolver.SearchHitKindAlbum {
		return nil, fmt.Errorf("album hits are not directly queueable")
	}
	if hit.QueueURL == "" {
		return nil, fmt.Errorf("queue_url is required")
	}
	if hit.Source == resolver.SourceSubsonic {
		return s.resolver.Resolve(ctx, hit.QueueURL)
	}

	track, err := queueTrackFromHit(hit)
	if err != nil {
		return nil, err
	}
	return []resolver.Track{track}, nil
}

func queueTrackFromHit(hit resolver.SearchHit) (resolver.Track, error) {
	if hit.Kind == resolver.SearchHitKindAlbum {
		return resolver.Track{}, fmt.Errorf("album hits are not directly queueable")
	}
	if hit.QueueURL == "" {
		return resolver.Track{}, fmt.Errorf("queue_url is required")
	}

	webpageURL := hit.WebpageURL
	if webpageURL == "" {
		webpageURL = hit.QueueURL
	}

	switch hit.Source {
	case resolver.SourceYouTube, resolver.SourceYTMusic:
		return resolver.Track{
			ID:         hit.ID,
			Title:      hit.Title,
			Artist:     hit.Artist,
			Duration:   hit.Duration,
			Uploader:   hit.Artist,
			Thumbnail:  hit.Thumbnail,
			URL:        hit.QueueURL,
			WebpageURL: webpageURL,
			Source:     hit.Source,
		}, nil
	case resolver.SourceSubsonic:
		return resolver.Track{
			ID:         hit.ID,
			Title:      hit.Title,
			Artist:     hit.Artist,
			Duration:   hit.Duration,
			Uploader:   hit.Artist,
			Thumbnail:  hit.Thumbnail,
			URL:        hit.QueueURL,
			WebpageURL: webpageURL,
			Source:     hit.Source,
		}, nil
	default:
		return resolver.Track{}, fmt.Errorf("unsupported search hit source: %s", hit.Source)
	}
}

func (s *Server) queueTracks(tracks []resolver.Track) []resolver.Track {
	queuedTracks := make([]resolver.Track, 0, len(tracks))
	for _, track := range tracks {
		urlToQueue := track.PlayableURL()
		if urlToQueue == "" {
			continue
		}

		s.player.State.StoreMetadata(urlToQueue, track)

		if _, err := s.player.Exec("loadfile", urlToQueue, "append-play"); err != nil {
			s.logger.Error("Failed to enqueue track", "url", urlToQueue, "error", err)
			continue
		}

		safeTrack := track
		if _, ok := resolver.ParseSubsonicURI(track.WebpageURL); ok {
			safeTrack.URL = track.WebpageURL
		}
		queuedTracks = append(queuedTracks, safeTrack)
	}
	return queuedTracks
}

func (s *Server) handleAlbum(w http.ResponseWriter, r *http.Request) {
	rawURL := r.URL.Query().Get("url")
	if rawURL == "" {
		http.Error(w, "url is required", http.StatusBadRequest)
		return
	}

	album, err := s.resolver.Album(r.Context(), rawURL)
	if err != nil {
		if errors.Is(err, resolver.ErrInvalidAlbumRef) {
			http.Error(w, "Invalid album ref", http.StatusBadRequest)
			return
		}
		s.logger.Debug("Failed to resolve album", "url", rawURL, "error", err)
		http.Error(w, "Album not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(album)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	intent, err := resolver.ParseSearchIntent(r.URL.Query().Get("intent"))
	if query == "" {
		http.Error(w, "Query is required", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "intent must be typeahead or results", http.StatusBadRequest)
		return
	}

	resultCh, err := s.resolver.Search(r.Context(), query, intent)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		s.logger.Error("Failed to search", "query", query, "error", err)
		http.Error(w, "Search failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)
	encoder := json.NewEncoder(w)

	for result := range resultCh {
		if err := encoder.Encode(result); err != nil {
			break
		}
		_ = rc.Flush()
	}
}

func (s *Server) handleSubsonicCoverArt(w http.ResponseWriter, r *http.Request) {
	libraryID := r.URL.Query().Get("library")
	coverArtID := r.URL.Query().Get("id")
	if libraryID == "" || coverArtID == "" {
		http.Error(w, "library and id are required", http.StatusBadRequest)
		return
	}

	data, contentType, err := s.resolver.FetchSubsonicCoverArt(r.Context(), libraryID, coverArtID)
	if err != nil {
		s.logger.Debug("Failed to fetch OpenSubsonic cover art", "library", libraryID, "id", coverArtID, "error", err)
		http.Error(w, "Cover art not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data)
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
	case "set_volume":
		if req.Value == nil {
			http.Error(w, "Volume value is required", http.StatusBadRequest)
			return
		}
		err = s.player.SetVolume(*req.Value)
	case "toggle_mute":
		err = s.player.ToggleMute()
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

func (s *Server) handleMove(w http.ResponseWriter, r *http.Request) {
	var req MoveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.From < 0 || req.To < -1 {
		http.Error(w, "Invalid indices", http.StatusBadRequest)
		return
	}

	if req.To >= 0 && req.From == req.To {
		http.Error(w, "Source and destination cannot match", http.StatusBadRequest)
		return
	}

	if _, err := s.player.Exec("playlist-move", req.From, req.To); err != nil {
		s.logger.Error("Failed to move item", "from", req.From, "to", req.To, "error", err)
		http.Error(w, "Move failed", http.StatusInternalServerError)
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
