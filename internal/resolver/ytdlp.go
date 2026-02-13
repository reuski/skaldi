package resolver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"skaldi/internal/bootstrap"
)

type Track struct {
	Title      string  `json:"title"`
	Duration   float64 `json:"duration"`
	Uploader   string  `json:"uploader"`
	Thumbnail  string  `json:"thumbnail"`
	URL        string  `json:"url,omitempty"`
	WebpageURL string  `json:"webpage_url,omitempty"`
	IsLive     bool    `json:"is_live,omitempty"`
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

func (r *Resolver) Resolve(ctx context.Context, url string) ([]Track, error) {
	cmd := exec.CommandContext(ctx, r.cfg.ShimPath(),
		"--dump-json", "--flat-playlist", "--no-download", "--no-warnings", url)

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
