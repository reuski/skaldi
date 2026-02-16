// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type State struct {
	Uv    string `json:"uv"`
	Bun   string `json:"bun"`
	YtDlp string `json:"yt-dlp"`
}

type CachedVersions struct {
	Versions  LatestVersions `json:"versions"`
	CheckedAt time.Time      `json:"checked_at"`
}

func LoadState(cacheDir string) (*State, error) {
	data, err := os.ReadFile(filepath.Join(cacheDir, "versions.json"))
	if os.IsNotExist(err) {
		return &State{}, nil
	}
	if err != nil {
		return nil, err
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func SaveState(cacheDir string, s *State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cacheDir, "versions.json"), data, 0o644)
}

func LoadCachedVersions(cacheDir string) (*CachedVersions, error) {
	data, err := os.ReadFile(filepath.Join(cacheDir, "version-check.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var cv CachedVersions
	if err := json.Unmarshal(data, &cv); err != nil {
		return nil, err
	}
	return &cv, nil
}

func SaveCachedVersions(cacheDir string, cv *CachedVersions) error {
	data, err := json.MarshalIndent(cv, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cacheDir, "version-check.json"), data, 0o644)
}
