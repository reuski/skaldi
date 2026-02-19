// SPDX-License-Identifier: AGPL-3.0-or-later

// Package bootstrap handles dependency provisioning and configuration management.
package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	CacheDir  string
	BinDir    string
	UvBinDir  string
	MpvSocket string
	DataDir   string
}

func LoadConfig() (*Config, error) {
	userCache, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("failed to determine user cache directory: %w", err)
	}

	cacheDir := filepath.Join(userCache, "skaldi")

	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to determine user home directory: %w", err)
		}
		dataDir = filepath.Join(home, ".local", "share")
	}
	historyDir := filepath.Join(dataDir, "skaldi", "history")

	return &Config{
		CacheDir:  cacheDir,
		BinDir:    filepath.Join(cacheDir, "bin"),
		UvBinDir:  filepath.Join(cacheDir, "uv-bin"),
		MpvSocket: filepath.Join(cacheDir, "mpv.sock"),
		DataDir:   historyDir,
	}, nil
}

func (c *Config) UvPath() string {
	return filepath.Join(c.BinDir, "uv")
}

func (c *Config) BunPath() string {
	return filepath.Join(c.BinDir, "bun")
}

func (c *Config) ShimPath() string {
	return filepath.Join(c.BinDir, "yt-dlp")
}

func (c *Config) RealYtDlpPath() string {
	return filepath.Join(c.UvBinDir, "yt-dlp")
}
