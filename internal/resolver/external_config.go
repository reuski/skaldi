// SPDX-License-Identifier: AGPL-3.0-or-later

package resolver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
)

var libraryIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type appConfig struct {
	OpenSubsonic openSubsonicConfig `json:"opensubsonic"`
}

type openSubsonicConfig struct {
	Enabled   bool   `json:"enabled"`
	LibraryID string `json:"library_id"`
	BaseURL   string `json:"base_url"`
	Username  string `json:"username"`
	Token     string `json:"token"`
	TimeoutMS int    `json:"timeout_ms"`
}

func loadOpenSubsonicConfig(path string) (*openSubsonicConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	if strings.TrimSpace(string(data)) == "" {
		return nil, nil
	}

	var cfg appConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config JSON at %s: %w", path, err)
	}

	if !cfg.OpenSubsonic.Enabled {
		return nil, nil
	}

	normalized, err := normalizeOpenSubsonicConfig(cfg.OpenSubsonic)
	if err != nil {
		return nil, err
	}

	return &normalized, nil
}

func normalizeOpenSubsonicConfig(cfg openSubsonicConfig) (openSubsonicConfig, error) {
	if cfg.LibraryID == "" {
		return cfg, fmt.Errorf("external opensubsonic config: library_id is required when enabled")
	}
	if !libraryIDPattern.MatchString(cfg.LibraryID) {
		return cfg, fmt.Errorf("external opensubsonic config: library_id must match %s", libraryIDPattern.String())
	}
	if cfg.BaseURL == "" {
		return cfg, fmt.Errorf("external opensubsonic config: base_url is required when enabled")
	}
	if cfg.Username == "" {
		return cfg, fmt.Errorf("external opensubsonic config: username is required when enabled")
	}
	if cfg.Token == "" {
		return cfg, fmt.Errorf("external opensubsonic config: token is required when enabled")
	}
	if cfg.TimeoutMS < 0 {
		return cfg, fmt.Errorf("external opensubsonic config: timeout_ms must be >= 0")
	}

	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if strings.HasSuffix(strings.ToLower(cfg.BaseURL), "/rest") {
		cfg.BaseURL = cfg.BaseURL[:len(cfg.BaseURL)-5]
	}

	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return cfg, fmt.Errorf("external opensubsonic config: base_url must be an absolute URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return cfg, fmt.Errorf("external opensubsonic config: base_url scheme must be http or https")
	}

	if cfg.TimeoutMS == 0 {
		cfg.TimeoutMS = 2500
	}

	return cfg, nil
}
