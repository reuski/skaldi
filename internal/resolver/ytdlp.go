// SPDX-License-Identifier: AGPL-3.0-or-later

// Package resolver extracts media metadata using yt-dlp.
package resolver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"sync"
	"time"

	"github.com/reuski/skaldi/internal/bootstrap"
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
	cfg *bootstrap.Config
}

func New(cfg *bootstrap.Config) *Resolver {
	return &Resolver{cfg: cfg}
}

func (r *Resolver) Search(ctx context.Context, query string, limit int, source string) ([]Track, error) {
	if source == "music" {
		return r.searchMusic(ctx, query, limit)
	}
	return r.searchYouTube(ctx, query, limit)
}

func (r *Resolver) searchYouTube(ctx context.Context, query string, limit int) ([]Track, error) {
	searchKey := fmt.Sprintf("ytsearch%d:%s", limit, query)
	args := []string{"--dump-json", "--flat-playlist", "--no-download", "--no-warnings", searchKey}

	cmd := exec.CommandContext(ctx, r.cfg.ShimPath(), args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp failed: %w", err)
	}

	return parseLines(out)
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
		return nil, fmt.Errorf("yt-dlp music search failed: %w", err)
	}

	tracks, err := parseLines(out)
	if err != nil {
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
			enrichedTracks[idx] = track

			if track.WebpageURL == "" {
				return
			}

			fullCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			fullMeta, err := r.fetchMetadata(fullCtx, track.WebpageURL)
			if err == nil {
				enrichedTracks[idx] = fullMeta
				enrichedTracks[idx].IsMusic = true
			}
		}(i, t)
	}
	wg.Wait()
	return enrichedTracks, nil
}

func (r *Resolver) Resolve(ctx context.Context, url string) ([]Track, error) {
	args := []string{"--dump-json", "--flat-playlist", "--no-download", "--no-warnings", url}
	cmd := exec.CommandContext(ctx, r.cfg.ShimPath(), args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp failed: %w", err)
	}
	return parseLines(out)
}

func (r *Resolver) fetchMetadata(ctx context.Context, url string) (Track, error) {
	args := []string{"--dump-json", "--no-playlist", "--no-download", "--no-warnings", url}
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
	}
}
