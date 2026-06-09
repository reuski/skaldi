// SPDX-License-Identifier: AGPL-3.0-or-later

// Package update replaces the running binary with the latest GitHub release.
package update

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const repo = "reuski/skaldi"

var httpClient = &http.Client{Timeout: 30 * time.Second}

func Run(current string, log *slog.Logger) error {
	latest, err := latestTag()
	if err != nil {
		return err
	}
	if !isNewer(latest, current) {
		log.Info("Up to date", "version", current)
		return nil
	}
	return apply(latest, log)
}

func apply(latest string, log *slog.Logger) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}

	asset := fmt.Sprintf("skaldi-%s-%s", runtime.GOOS, runtime.GOARCH)
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, latest, asset)
	log.Info("Downloading update", "version", latest, "asset", asset)

	tmp, err := download(url, exe)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace binary: %w", err)
	}

	log.Info("Updated", "to", latest)
	return nil
}

func download(url, exe string) (string, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", url, resp.Status)
	}

	tmp, err := os.CreateTemp(filepath.Dir(exe), ".skaldi-update-*")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

func latestTag() (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequest(http.MethodGet, url, nil)
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
		return "", fmt.Errorf("GitHub API returned %s for %s", resp.Status, repo)
	}

	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("empty tag_name for %s", repo)
	}
	return rel.TagName, nil
}

func isNewer(latest, current string) bool {
	if current == "" || current == "dev" {
		return true
	}
	lv, rv := parseSemver(latest), parseSemver(current)
	for i := range lv {
		if lv[i] != rv[i] {
			return lv[i] > rv[i]
		}
	}
	return false
}

func parseSemver(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	for i, part := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(part)
	}
	return out
}
