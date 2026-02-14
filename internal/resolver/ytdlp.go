// Package resolver extracts media metadata using yt-dlp.
package resolver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os/exec"
	"sync"
	"time"

	"skaldi/internal/bootstrap"
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
	WebpageURL string  `json:"webpage_url"`
	URL        string  `json:"url"`
	IEKey      string  `json:"ie_key"`
}

type playlistWrapper struct {
	Entries []ytDlpResponse `json:"entries"`
}

type Resolver struct {
	cfg *bootstrap.Config
}

func New(cfg *bootstrap.Config) *Resolver {
	return &Resolver{cfg: cfg}
}

func (r *Resolver) Search(ctx context.Context, query string, limit int) ([]Track, error) {
	musicURL := "https://music.youtube.com/search?q=" + url.QueryEscape(query) + "#songs"
	searchKey := fmt.Sprintf("ytsearch%d:%s", limit, query)

	resultCh := make(chan []Track, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		tracks, err := r.search(ctx, musicURL, limit)
		if err != nil {
			slog.Debug("YT Music search failed", "error", err)
			return
		}
		if len(tracks) == 0 {
			return
		}
		for i := range tracks {
			tracks[i].IsMusic = true
		}
		resultCh <- tracks
	}()

	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		tracks, err := r.search(ctx, searchKey, 0)
		if err != nil {
			slog.Debug("YouTube search failed", "error", err)
			return
		}
		if len(tracks) == 0 {
			return
		}
		resultCh <- tracks
	}()

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	var music, regular []Track
	for tracks := range resultCh {
		if len(tracks) > 0 && tracks[0].IsMusic {
			music = tracks
		} else {
			regular = tracks
		}
	}

	if len(music) == 0 && len(regular) == 0 {
		return nil, fmt.Errorf("no tracks found")
	}

	return dedup(music, regular), nil
}

func (r *Resolver) search(ctx context.Context, uri string, limit int) ([]Track, error) {
	args := []string{"--dump-json", "--no-download", "--no-warnings"}
	if limit > 0 {
		args = append(args, "--playlist-end", fmt.Sprintf("%d", limit))
	}
	args = append(args, uri)

	cmd := exec.CommandContext(ctx, r.cfg.ShimPath(), args...)
	out, err := cmd.CombinedOutput()

	if len(out) > 0 {
		var wrapper playlistWrapper
		if json.Unmarshal(out, &wrapper) == nil && len(wrapper.Entries) > 0 {
			return entriesToTracks(wrapper.Entries), nil
		}

		if tracks, parseErr := parseLines(out); parseErr == nil && len(tracks) > 0 {
			return tracks, nil
		}
	}

	if err != nil {
		return nil, fmt.Errorf("yt-dlp failed: %w", err)
	}

	return nil, fmt.Errorf("no tracks found")
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

func parseLines(data []byte) ([]Track, error) {
	var tracks []Track
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		var resp ytDlpResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
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

func entriesToTracks(entries []ytDlpResponse) []Track {
	tracks := make([]Track, 0, len(entries))
	for _, e := range entries {
		if t := trackFromResponse(e); t.WebpageURL != "" {
			tracks = append(tracks, t)
		}
	}
	return tracks
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

	return Track{
		Title:      r.Title,
		Artist:     artist,
		Duration:   r.Duration,
		Uploader:   r.Uploader,
		Thumbnail:  r.Thumbnail,
		WebpageURL: url,
	}
}

func dedup(primary, secondary []Track) []Track {
	seen := make(map[string]struct{}, len(primary))
	out := make([]Track, 0, len(primary)+len(secondary))

	for _, t := range primary {
		if id := extractVideoID(t.WebpageURL); id != "" {
			seen[id] = struct{}{}
		}
		out = append(out, t)
	}

	for _, t := range secondary {
		if id := extractVideoID(t.WebpageURL); id != "" {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
		}
		out = append(out, t)
	}

	return out
}

func extractVideoID(urlStr string) string {
	if urlStr == "" {
		return ""
	}
	u, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}
	return u.Query().Get("v")
}
