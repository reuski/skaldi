// SPDX-License-Identifier: AGPL-3.0-or-later

// Package bootstrap handles dependency provisioning and configuration management.
package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

const defaultPort = 8080

type Config struct {
	CacheDir   string
	BinDir     string
	MpvSocket  string
	DataDir    string
	ConfigPath string
	Port       int
	Provision  bool
}

type Settings struct {
	Server struct {
		Port int `json:"port"`
	} `json:"server"`
	Provision *bool `json:"provision"`
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

	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to determine user home directory: %w", err)
		}
		configDir = filepath.Join(home, ".config")
	}
	appConfigPath := filepath.Join(configDir, "skaldi", "config.json")
	if envPath := os.Getenv("SKALDI_CONFIG"); envPath != "" {
		appConfigPath = envPath
	}

	settings, err := loadSettings(appConfigPath)
	if err != nil {
		return nil, err
	}

	return &Config{
		CacheDir:   cacheDir,
		BinDir:     filepath.Join(cacheDir, "bin"),
		MpvSocket:  filepath.Join(cacheDir, "mpv.sock"),
		DataDir:    historyDir,
		ConfigPath: appConfigPath,
		Port:       resolvePort(settings),
		Provision:  resolveProvision(settings),
	}, nil
}

func loadSettings(path string) (Settings, error) {
	var s Settings
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return s, fmt.Errorf("failed to read config %s: %w", path, err)
	}
	if len(data) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("invalid config JSON at %s: %w", path, err)
	}
	return s, nil
}

func resolvePort(s Settings) int {
	if env := os.Getenv("SKALDI_PORT"); env != "" {
		if p, err := strconv.Atoi(env); err == nil && p > 0 {
			return p
		}
	}
	if s.Server.Port > 0 {
		return s.Server.Port
	}
	return defaultPort
}

func resolveProvision(s Settings) bool {
	if env, ok := os.LookupEnv("SKALDI_PROVISION"); ok {
		switch env {
		case "0", "false", "no", "off":
			return false
		case "1", "true", "yes", "on":
			return true
		}
	}
	if s.Provision != nil {
		return *s.Provision
	}
	return true
}

func (c *Config) BunPath() string {
	return filepath.Join(c.BinDir, "bun")
}

func (c *Config) YtDlpDir() string {
	return filepath.Join(c.BinDir, "yt-dlp-runtime")
}

func (c *Config) YtDlpPath() string {
	return filepath.Join(c.YtDlpDir(), "yt-dlp")
}

func (c *Config) ShimPath() string {
	return filepath.Join(c.BinDir, "yt-dlp")
}
