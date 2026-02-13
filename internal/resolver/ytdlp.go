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

	"skaldi/internal/bootstrap"
)

type Track struct {
	Title      string  `json:"title"`
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
	Duration   float64 `json:"duration"`
	Uploader   string  `json:"uploader"`
	Thumbnail  string  `json:"thumbnail"`
	WebpageURL string  `json:"webpage_url"`
	URL        string  `json:"url"`
	Type       string  `json:"_type"`
	IEKey      string  `json:"ie_key"`
}

type Resolver struct {
	cfg *bootstrap.Config
}

func New(cfg *bootstrap.Config) *Resolver {
	return &Resolver{cfg: cfg}
}

// Search runs YouTube Music and standard YouTube searches in parallel,
// merges results with YouTube Music first, and deduplicates by video ID.
func (r *Resolver) Search(ctx context.Context, query string, limit int) ([]Track, error) {
	ytMusicURL := "https://music.youtube.com/search?q=" + url.QueryEscape(query) + "#songs"
	ytSearchKey := fmt.Sprintf("ytsearch%d:%s", limit, query)

	type result struct {
		tracks []Track
		err    error
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		music    []Track
		regular  []Track
		firstErr error
	)

	wg.Add(2)

	go func() {
		defer wg.Done()
		tracks, err := r.resolve(ctx, ytMusicURL, limit)
		mu.Lock()
		defer mu.Unlock()
		for i := range tracks {
			tracks[i].IsMusic = true
		}
		music = tracks
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}()

	go func() {
		defer wg.Done()
		tracks, err := r.resolve(ctx, ytSearchKey, 0)
		mu.Lock()
		defer mu.Unlock()
		regular = tracks
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}()

	wg.Wait()

	if len(music) == 0 && len(regular) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, fmt.Errorf("no tracks found")
	}

	return dedup(music, regular), nil
}

func dedup(primary, secondary []Track) []Track {
	seen := make(map[string]struct{}, len(primary))
	out := make([]Track, 0, len(primary)+len(secondary))

	for _, t := range primary {
		id := extractVideoID(t)
		if id != "" {
			seen[id] = struct{}{}
		}
		out = append(out, t)
	}

	for _, t := range secondary {
		id := extractVideoID(t)
		if id != "" {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
		}
		out = append(out, t)
	}

	return out
}

func extractVideoID(t Track) string {
	raw := t.WebpageURL
	if raw == "" {
		raw = t.URL
	}
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if v := u.Query().Get("v"); v != "" {
		return v
	}
	return ""
}

// Resolve extracts track metadata from a direct URL or playlist.
func (r *Resolver) Resolve(ctx context.Context, url string) ([]Track, error) {
	return r.resolve(ctx, url, 0)
}

func (r *Resolver) resolve(ctx context.Context, uri string, limit int) ([]Track, error) {
	args := []string{"--dump-json", "--flat-playlist", "--no-download", "--no-warnings"}
	if limit > 0 {
		args = append(args, "--playlist-end", fmt.Sprintf("%d", limit))
	}
	args = append(args, uri)

	cmd := exec.CommandContext(ctx, r.cfg.ShimPath(), args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("yt-dlp failed: %w (stderr: %s)", err, stderr.String())
	}

	var tracks []Track
	scanner := bufio.NewScanner(&stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		var resp ytDlpResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			continue
		}

		t := Track{
			Title:      resp.Title,
			Duration:   resp.Duration,
			Uploader:   resp.Uploader,
			Thumbnail:  resp.Thumbnail,
			WebpageURL: resp.WebpageURL,
		}

		if t.WebpageURL == "" {
			if resp.URL != "" {
				if resp.IEKey == "Youtube" && len(resp.URL) == 11 {
					t.WebpageURL = "https://www.youtube.com/watch?v=" + resp.URL
				} else {
					t.WebpageURL = resp.URL
				}
			} else if resp.ID != "" {
				if resp.IEKey == "Youtube" {
					t.WebpageURL = "https://www.youtube.com/watch?v=" + resp.ID
				} else {
					t.WebpageURL = resp.ID
				}
			}
		}

		tracks = append(tracks, t)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan output: %w", err)
	}

	if len(tracks) == 0 {
		return nil, fmt.Errorf("no tracks found for URL")
	}

	return tracks, nil
}
