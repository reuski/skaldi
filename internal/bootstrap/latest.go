// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Upstream recommends nightly for streaming-site fixes.
const ytDlpRepo = "yt-dlp/yt-dlp-nightly-builds"

const bunRepo = "oven-sh/bun"

// Ignore stable-channel caches when selecting nightly downloads.
const versionChannel = "nightly"

type LatestVersions struct {
	YtDlp string
	Bun   string
}

// Latest is true only for a fresh upstream check; Err retains failed-check provenance.
type sourceVersion struct {
	Version string
	Latest  bool
	Err     error
}

type githubRelease struct {
	TagName string `json:"tag_name"`
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

func fetchLatestVersions(cacheDir string, logger interface {
	Debug(string, ...any)
	Warn(string, ...any)
}) (sourceVersion, sourceVersion) {
	cached := cachedNightlyVersions(cacheDir)

	if logger != nil {
		logger.Debug("Fetching latest versions from GitHub API")
	}

	ytDlpTag, ytDlpErr := fetchLatestTag(ytDlpRepo)
	bunTag, bunErr := fetchLatestTag(bunRepo)
	if bunErr == nil {
		bunTag = strings.TrimPrefix(bunTag, "bun-v")
	}

	var cachedYtDlp, cachedBun string
	if cached != nil {
		cachedYtDlp, cachedBun = cached.Versions.YtDlp, cached.Versions.Bun
	}

	ytDlp := selectSourceVersion(ytDlpTag, ytDlpErr, cachedYtDlp)
	bun := selectSourceVersion(bunTag, bunErr, cachedBun)

	// Only complete checks share a cache timestamp; cache versions are not installed versions.
	if ytDlp.Latest && bun.Latest {
		err := SaveCachedVersions(cacheDir, &CachedVersions{
			Versions:  LatestVersions{YtDlp: ytDlp.Version, Bun: bun.Version},
			Channel:   versionChannel,
			CheckedAt: time.Now(),
		})
		if err != nil && logger != nil {
			logger.Warn("failed to record version check", "error", err)
		}
	}

	return ytDlp, bun
}

func selectSourceVersion(tag string, err error, cached string) sourceVersion {
	if err == nil {
		return sourceVersion{Version: tag, Latest: true}
	}
	if !errors.Is(err, errUpstreamUnavailable) {
		return sourceVersion{Err: err}
	}
	return sourceVersion{Version: cached, Err: err}
}

func cachedNightlyVersions(cacheDir string) *CachedVersions {
	cv, err := LoadCachedVersions(cacheDir)
	if err != nil || cv == nil || cv.Channel != versionChannel {
		return nil
	}
	return cv
}

func fetchLatestTag(repo string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: request for %s: %v", errUpstreamUnavailable, repo, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: GitHub API returned %s for %s", errUpstreamUnavailable, resp.Status, repo)
	}

	// Unreadable responses allow offline reuse; malformed release metadata remains fatal.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%w: reading response for %s: %v", errUpstreamUnavailable, repo, err)
	}

	var rel githubRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", fmt.Errorf("malformed release metadata for %s: %w", repo, err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("empty tag_name for %s", repo)
	}
	return rel.TagName, nil
}
