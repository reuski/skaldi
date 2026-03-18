// SPDX-License-Identifier: AGPL-3.0-or-later

// Package resolver extracts media metadata using yt-dlp.
package resolver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/reuski/skaldi/internal/bootstrap"
)

const (
	SourceSubsonic = "subsonic"
	SourceYTMusic  = "ytmusic"
	SourceYouTube  = "youtube"
)

type Track struct {
	Title      string  `json:"title"`
	Artist     string  `json:"artist"`
	Duration   float64 `json:"duration"`
	Uploader   string  `json:"uploader"`
	Thumbnail  string  `json:"thumbnail"`
	URL        string  `json:"url,omitempty"`
	WebpageURL string  `json:"webpage_url,omitempty"`
	IsMusic    bool    `json:"is_music,omitempty"`
	Source     string  `json:"source,omitempty"`
}

type SearchResult struct {
	Suggestions []string `json:"suggestions"`
	Tracks      []Track  `json:"tracks"`
}

type ytDlpResponse struct {
	ID             string  `json:"id"`
	Title          string  `json:"title"`
	Artist         string  `json:"artist"`
	Duration       float64 `json:"duration"`
	DurationString string  `json:"duration_string"`
	Uploader       string  `json:"uploader"`
	Thumbnail      string  `json:"thumbnail"`
	Thumbnails     []struct {
		URL    string `json:"url"`
		Height int    `json:"height"`
		Width  int    `json:"width"`
	} `json:"thumbnails"`
	WebpageURL string `json:"webpage_url"`
	URL        string `json:"url"`
	IEKey      string `json:"ie_key"`
	LiveStatus string `json:"live_status"`
}

type Resolver struct {
	cfg           *bootstrap.Config
	subsonic      *SubsonicClient
	suggestClient *http.Client
	warnings      []error
}

func New(cfg *bootstrap.Config) (*Resolver, error) {
	r := &Resolver{
		cfg:           cfg,
		suggestClient: &http.Client{Timeout: 2 * time.Second},
	}

	if cfg == nil {
		return r, nil
	}

	extCfg, err := loadOpenSubsonicConfig(cfg.ConfigPath)
	if err != nil {
		r.warnings = append(r.warnings, fmt.Errorf("opensubsonic disabled: %w", err))
		return r, nil
	}
	if extCfg != nil {
		r.subsonic = NewSubsonicClient(*extCfg)
	}

	return r, nil
}

func (r *Resolver) Warnings() []error {
	if len(r.warnings) == 0 {
		return nil
	}
	out := make([]error, len(r.warnings))
	copy(out, r.warnings)
	return out
}

func (r *Resolver) Suggestions(ctx context.Context, query string) ([]string, error) {
	u := "https://suggestqueries.google.com/complete/search?client=firefox&ds=yt&oe=utf8&q=" + url.QueryEscape(query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := r.suggestClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, err
	}

	var raw []json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	if len(raw) < 2 {
		return []string{}, nil
	}

	var suggestions []string
	if err := json.Unmarshal(raw[1], &suggestions); err != nil {
		return nil, err
	}

	return suggestions, nil
}

func (r *Resolver) Search(ctx context.Context, query string, limit int, mode string) (<-chan SearchResult, error) {
	if limit <= 0 {
		limit = 5
	}

	switch mode {
	case "typeahead":
		return r.searchTypeahead(ctx, query, limit), nil
	case "full":
		return r.searchFull(ctx, query, limit), nil
	default:
		return nil, fmt.Errorf("invalid search mode: %s", mode)
	}
}

func (r *Resolver) searchTypeahead(ctx context.Context, query string, limit int) <-chan SearchResult {
	resultCh := make(chan SearchResult, 2)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		tCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		suggestions, err := r.Suggestions(tCtx, query)
		if err == nil {
			resultCh <- SearchResult{Suggestions: suggestions}
		}
	}()

	if r.subsonic != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
			defer cancel()
			tracks, err := r.subsonic.Search(tCtx, query, limit)
			if err == nil {
				resultCh <- SearchResult{Tracks: tracks}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	return resultCh
}

func (r *Resolver) searchFull(ctx context.Context, query string, limit int) <-chan SearchResult {
	resultCh := make(chan SearchResult, 3)
	var wg sync.WaitGroup

	if r.subsonic != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
			defer cancel()
			tracks, err := r.subsonic.Search(tCtx, query, limit)
			if err == nil && len(tracks) > 0 {
				resultCh <- SearchResult{Tracks: tracks}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		tCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		tracks, err := r.searchMusic(tCtx, query, limit)
		if err == nil && len(tracks) > 0 {
			resultCh <- SearchResult{Tracks: tracks}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		tCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		tracks, err := r.searchYouTube(tCtx, query, limit)
		if err == nil && len(tracks) > 0 {
			resultCh <- SearchResult{Tracks: tracks}
		}
	}()

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	return resultCh
}

func (r *Resolver) searchYouTube(ctx context.Context, query string, limit int) ([]Track, error) {
	searchKey := fmt.Sprintf("ytsearch%d:%s", limit, query)
	args := []string{"--dump-json", "--flat-playlist", "--no-download", "--no-warnings", searchKey}

	cmd := exec.CommandContext(ctx, r.cfg.ShimPath(), args...)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("yt-dlp failed: %w", err)
	}

	tracks, err := parseLines(out)
	if err != nil {
		if isNoTracksError(err) {
			return []Track{}, nil
		}
		return nil, err
	}
	for i := range tracks {
		tracks[i].Source = SourceYouTube
	}
	return tracks, nil
}

func (r *Resolver) searchMusic(ctx context.Context, query string, limit int) ([]Track, error) {
	musicURL := "https://music.youtube.com/search?q=" + url.QueryEscape(query) + "#songs"
	args := []string{"--dump-json", "--flat-playlist", "--no-download", "--no-warnings"}
	if limit > 0 {
		args = append(args, "--playlist-end", fmt.Sprintf("%d", limit*2))
	}
	args = append(args, musicURL)

	cmd := exec.CommandContext(ctx, r.cfg.ShimPath(), args...)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("yt-dlp music search failed: %w", err)
	}

	tracks, err := parseLines(out)
	if err != nil {
		if isNoTracksError(err) {
			return []Track{}, nil
		}
		return nil, err
	}

	if len(tracks) > limit {
		tracks = tracks[:limit]
	}

	for i := range tracks {
		tracks[i].IsMusic = true
		tracks[i].Source = SourceYTMusic
	}
	r.hydrateMissingDurations(ctx, tracks)

	return tracks, nil
}

func (r *Resolver) Resolve(ctx context.Context, rawURL string) ([]Track, error) {
	if subsonicRef, ok := ParseSubsonicURI(rawURL); ok {
		return r.resolveSubsonicTrack(ctx, subsonicRef)
	}

	args := []string{"--dump-json", "--flat-playlist", "--no-download", "--no-warnings", rawURL}
	cmd := exec.CommandContext(ctx, r.cfg.ShimPath(), args...)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("yt-dlp failed: %w", err)
	}
	return parseLines(out)
}

func (r *Resolver) resolveSubsonicTrack(ctx context.Context, ref SubsonicRef) ([]Track, error) {
	if r.subsonic == nil {
		return nil, fmt.Errorf("opensubsonic source is not configured")
	}
	if ref.LibraryID != r.subsonic.LibraryID() {
		return nil, fmt.Errorf("unknown opensubsonic library: %s", ref.LibraryID)
	}

	streamURL, err := r.subsonic.BuildStreamURL(ref.TrackID)
	if err != nil {
		return nil, err
	}

	track, err := r.subsonic.GetTrack(ctx, ref.TrackID)
	if err != nil {
		track = Track{
			Title:      ref.TrackID,
			Artist:     "OpenSubsonic",
			Uploader:   "OpenSubsonic",
			WebpageURL: BuildSubsonicURI(ref.LibraryID, ref.TrackID),
			Source:     SourceSubsonic,
			IsMusic:    true,
		}
	}

	track.URL = streamURL
	track.WebpageURL = BuildSubsonicURI(ref.LibraryID, ref.TrackID)
	track.Source = SourceSubsonic
	track.IsMusic = true

	return []Track{track}, nil
}

func (r *Resolver) hydrateMissingDurations(ctx context.Context, tracks []Track) {
	const maxConcurrent = 2

	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup

	for i, track := range tracks {
		if track.Duration > 0 || track.WebpageURL == "" {
			continue
		}

		wg.Add(1)
		go func(idx int, raw Track) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			fullCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()

			duration, err := r.fetchTrackDuration(fullCtx, raw.WebpageURL)
			if err != nil {
				return
			}
			if duration > 0 {
				tracks[idx].Duration = duration
			}
		}(i, track)
	}

	wg.Wait()
}

func (r *Resolver) fetchTrackDuration(ctx context.Context, rawURL string) (float64, error) {
	args := []string{"--dump-json", "--no-playlist", "--no-download", "--no-warnings", rawURL}
	cmd := exec.CommandContext(ctx, r.cfg.ShimPath(), args...)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	var resp ytDlpResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return 0, err
	}
	return durationFromResponse(resp), nil
}

func parseLines(data []byte) ([]Track, error) {
	var tracks []Track
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		var resp ytDlpResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			continue
		}

		if resp.IEKey == "YoutubeTab" || resp.LiveStatus == "is_live" || resp.LiveStatus == "was_live" {
			continue
		}

		if t := trackFromResponse(resp); t.WebpageURL != "" {
			tracks = append(tracks, t)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan output: %w", err)
	}
	if len(tracks) == 0 {
		return nil, fmt.Errorf("no tracks found")
	}
	return tracks, nil
}

func isNoTracksError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "no tracks found") || errors.Is(err, context.DeadlineExceeded)
}

func trackFromResponse(r ytDlpResponse) Track {
	artist := r.Artist
	if artist == "" {
		artist = r.Uploader
	}

	url := r.WebpageURL
	if url == "" && r.ID != "" && r.IEKey == "Youtube" {
		url = "https://www.youtube.com/watch?v=" + r.ID
	}

	thumb := r.Thumbnail
	if len(r.Thumbnails) > 0 {
		last := r.Thumbnails[len(r.Thumbnails)-1]
		if last.URL != "" {
			thumb = last.URL
		}
	}

	if thumb == "" && r.ID != "" {
		thumb = fmt.Sprintf("https://i.ytimg.com/vi/%s/hqdefault.jpg", r.ID)
	}

	return Track{
		Title:      r.Title,
		Artist:     artist,
		Duration:   durationFromResponse(r),
		Uploader:   r.Uploader,
		Thumbnail:  thumb,
		WebpageURL: url,
		Source:     SourceYouTube,
	}
}

func durationFromResponse(r ytDlpResponse) float64 {
	if r.Duration > 0 {
		return r.Duration
	}
	if parsed, ok := parseDurationString(r.DurationString); ok {
		return parsed
	}
	return 0
}

func parseDurationString(raw string) (float64, bool) {
	if raw == "" {
		return 0, false
	}

	parts := strings.Split(raw, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}

	total := 0
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			return 0, false
		}
		total = total*60 + value
	}

	return float64(total), true
}
