// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type LatestVersions struct {
	Uv  string
	Bun string
}

type githubRelease struct {
	TagName string `json:"tag_name"`
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

const versionCacheTTL = 24 * time.Hour

func FetchLatestVersions(cacheDir string, logger interface{ Debug(string, ...any) }) (*LatestVersions, error) {
	cv, _ := LoadCachedVersions(cacheDir)
	if cv != nil && time.Since(cv.CheckedAt) < versionCacheTTL {
		if logger != nil {
			logger.Debug("Using cached version check", "age", time.Since(cv.CheckedAt).Round(time.Minute))
		}
		return &cv.Versions, nil
	}

	if logger != nil {
		logger.Debug("Fetching latest versions from GitHub API")
	}

	uv, err := fetchLatestTag("astral-sh", "uv")
	if err != nil {
		if cv != nil {
			return &cv.Versions, nil
		}
		return nil, fmt.Errorf("uv: %w", err)
	}

	bun, err := fetchLatestTag("oven-sh", "bun")
	if err != nil {
		if cv != nil {
			return &cv.Versions, nil
		}
		return nil, fmt.Errorf("bun: %w", err)
	}
	bun = strings.TrimPrefix(bun, "bun-v")

	versions := &LatestVersions{Uv: uv, Bun: bun}
	_ = SaveCachedVersions(cacheDir, &CachedVersions{
		Versions:  *versions,
		CheckedAt: time.Now(),
	})

	return versions, nil
}

func fetchLatestTag(owner, repo string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %s for %s/%s", resp.Status, owner, repo)
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("empty tag_name for %s/%s", owner, repo)
	}
	return rel.TagName, nil
}
