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
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Artist     string  `json:"artist"`
	Duration   float64 `json:"duration"`
	Uploader   string  `json:"uploader"`
	Thumbnail  string  `json:"thumbnail"`
	Thumbnails []struct {
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
		return nil, err
	}
	if extCfg != nil {
		r.subsonic = NewSubsonicClient(*extCfg)
	}

	return r, nil
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

func (r *Resolver) Search(ctx context.Context, query string, limit int, mode string) (SearchResult, error) {
	if limit <= 0 {
		limit = 5
	}

	switch mode {
	case "typeahead":
		return r.searchTypeahead(ctx, query, limit), nil
	case "full":
		return r.searchFull(ctx, query, limit), nil
	default:
		return SearchResult{}, fmt.Errorf("invalid search mode: %s", mode)
	}
}

func (r *Resolver) searchTypeahead(ctx context.Context, query string, limit int) SearchResult {
	result := SearchResult{}
	var wg sync.WaitGroup
	var mu sync.Mutex

	wg.Add(1)
	go func() {
		defer wg.Done()
		tCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		suggestions, err := r.Suggestions(tCtx, query)
		if err == nil {
			mu.Lock()
			result.Suggestions = suggestions
			mu.Unlock()
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
				mu.Lock()
				result.Tracks = append(result.Tracks, tracks...)
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	return result
}

func (r *Resolver) searchFull(ctx context.Context, query string, limit int) SearchResult {
	type sourceResult struct {
		source string
		tracks []Track
	}

	resultCh := make(chan sourceResult, 3)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		if r.subsonic == nil {
			resultCh <- sourceResult{source: SourceSubsonic}
			return
		}
		tCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		defer cancel()
		tracks, err := r.subsonic.Search(tCtx, query, limit)
		if err != nil {
			resultCh <- sourceResult{source: SourceSubsonic}
			return
		}
		resultCh <- sourceResult{source: SourceSubsonic, tracks: tracks}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		tCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		tracks, err := r.searchMusic(tCtx, query, limit)
		if err != nil {
			resultCh <- sourceResult{source: SourceYTMusic}
			return
		}
		resultCh <- sourceResult{source: SourceYTMusic, tracks: tracks}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		tCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		tracks, err := r.searchYouTube(tCtx, query, limit)
		if err != nil {
			resultCh <- sourceResult{source: SourceYouTube}
			return
		}
		resultCh <- sourceResult{source: SourceYouTube, tracks: tracks}
	}()

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	bySource := map[string][]Track{}
	for item := range resultCh {
		bySource[item.source] = item.tracks
	}

	return SearchResult{
		Tracks: mergeTracks(
			bySource[SourceSubsonic],
			bySource[SourceYTMusic],
			bySource[SourceYouTube],
		),
	}
}

func mergeTracks(groups ...[]Track) []Track {
	merged := make([]Track, 0)
	seen := make(map[string]struct{})

	for _, group := range groups {
		for _, track := range group {
			key := dedupeKey(track)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, track)
		}
	}

	return merged
}

func dedupeKey(track Track) string {
	if ref, ok := ParseSubsonicURI(firstNonEmpty(track.WebpageURL, track.URL)); ok {
		return "sub:" + ref.LibraryID + ":" + ref.TrackID
	}

	videoID := youtubeVideoID(firstNonEmpty(track.WebpageURL, track.URL))
	if videoID != "" {
		return "yt:" + videoID
	}

	artist := normalizeSpace(firstNonEmpty(track.Artist, track.Uploader))
	title := normalizeSpace(track.Title)
	if title == "" {
		title = normalizeSpace(firstNonEmpty(track.WebpageURL, track.URL))
	}
	return "meta:" + title + "|" + artist
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func normalizeSpace(v string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(v)), " "))
}

func youtubeVideoID(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	host := strings.ToLower(u.Host)
	if strings.Contains(host, "youtu.be") {
		return strings.TrimPrefix(u.Path, "/")
	}
	if strings.Contains(host, "youtube.com") || strings.Contains(host, "music.youtube.com") {
		if id := u.Query().Get("v"); id != "" {
			return id
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 2 && parts[0] == "shorts" {
			return parts[1]
		}
	}

	return ""
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

	var wg sync.WaitGroup
	enrichedTracks := make([]Track, len(tracks))

	for i, t := range tracks {
		wg.Add(1)
		go func(idx int, track Track) {
			defer wg.Done()

			track.IsMusic = true
			track.Source = SourceYTMusic
			enrichedTracks[idx] = track

			if track.WebpageURL == "" {
				return
			}

			fullCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			fullMeta, err := r.fetchMetadata(fullCtx, track.WebpageURL)
			if err == nil {
				fullMeta.IsMusic = true
				fullMeta.Source = SourceYTMusic
				enrichedTracks[idx] = fullMeta
			}
		}(i, t)
	}
	wg.Wait()

	return enrichedTracks, nil
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

func (r *Resolver) fetchMetadata(ctx context.Context, rawURL string) (Track, error) {
	args := []string{"--dump-json", "--no-playlist", "--no-download", "--no-warnings", rawURL}
	cmd := exec.CommandContext(ctx, r.cfg.ShimPath(), args...)
	out, err := cmd.Output()
	if err != nil {
		return Track{}, err
	}

	var resp ytDlpResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return Track{}, err
	}
	return trackFromResponse(resp), nil
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
		Duration:   r.Duration,
		Uploader:   r.Uploader,
		Thumbnail:  thumb,
		WebpageURL: url,
		Source:     SourceYouTube,
	}
}
