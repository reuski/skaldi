// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
)

func Run(logger *slog.Logger) error {
	if err := CheckPrerequisites(); err != nil {
		return fmt.Errorf("prerequisites check failed: %w", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("config load failed: %w", err)
	}

	if err := createDirectories(cfg); err != nil {
		return err
	}

	if !cfg.Provision {
		return useSystemTools(cfg, logger)
	}

	state, err := loadOrCreateState(cfg)
	if err != nil {
		return err
	}

	latest, err := FetchLatestVersions(cfg.CacheDir, logger)
	if err != nil {
		if canUseInstalledVersions(state, cfg) {
			logger.Debug("GitHub API unavailable, using installed versions")
			return generateShim(cfg)
		}
		return fmt.Errorf("failed to fetch latest versions (first run requires network): %w", err)
	}

	if err := installYtDlpIfNeeded(cfg, state, latest, logger); err != nil {
		return err
	}

	if err := installBunIfNeeded(cfg, state, latest, logger); err != nil {
		return err
	}

	return generateShim(cfg)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func installYtDlp(cfg *Config, version string) error {
	info, err := GetPlatformInfo()
	if err != nil {
		return err
	}

	url := ConstructYtDlpURL(version, info.YtDlpArtifact)
	if err := DownloadFile(url, cfg.YtDlpPath()); err != nil {
		return err
	}
	return os.Chmod(cfg.YtDlpPath(), 0o755)
}

func installBun(cfg *Config, version string) error {
	info, err := GetPlatformInfo()
	if err != nil {
		return err
	}

	url := ConstructBunURL(version, info.BunArtifact)
	tmpFile := filepath.Join(cfg.CacheDir, "bun_download.tmp")
	defer os.Remove(tmpFile)

	if err := DownloadFile(url, tmpFile); err != nil {
		return err
	}
	return ExtractZip(tmpFile, "bun", cfg.BunPath())
}

func generateShim(cfg *Config) error {
	return writeShim(cfg.ShimPath(), cfg.YtDlpPath(), cfg.BunPath())
}

func writeShim(shimPath, ytDlpPath, bunPath string) error {
	shimContent := fmt.Sprintf("#!/bin/sh\nexec \"%s\" --js-runtimes \"bun:%s\" \"$@\"\n",
		ytDlpPath, bunPath)

	return os.WriteFile(shimPath, []byte(shimContent), 0o755)
}

func useSystemTools(cfg *Config, logger *slog.Logger) error {
	ytDlpPath, err := exec.LookPath("yt-dlp")
	if err != nil {
		return fmt.Errorf("provisioning disabled but yt-dlp not found in PATH: %w", err)
	}
	bunPath, err := exec.LookPath("bun")
	if err != nil {
		return fmt.Errorf("provisioning disabled but bun not found in PATH: %w", err)
	}

	logger.Debug("Using system tools", "yt-dlp", ytDlpPath, "bun", bunPath)
	return writeShim(cfg.ShimPath(), ytDlpPath, bunPath)
}

func createDirectories(cfg *Config) error {
	for _, dir := range []string{cfg.CacheDir, cfg.BinDir, cfg.DataDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}
	return nil
}

func loadOrCreateState(cfg *Config) (*State, error) {
	state, err := LoadState(cfg.CacheDir)
	if err != nil {
		state = &State{}
	}
	return state, nil
}

func canUseInstalledVersions(state *State, cfg *Config) bool {
	return state.YtDlp != "" && fileExists(cfg.YtDlpPath()) &&
		state.Bun != "" && fileExists(cfg.BunPath())
}

func installYtDlpIfNeeded(cfg *Config, state *State, latest *LatestVersions, logger *slog.Logger) error {
	if state.YtDlp != latest.YtDlp || !fileExists(cfg.YtDlpPath()) {
		logger.Debug("Installing yt-dlp", "version", latest.YtDlp)
		if err := installYtDlp(cfg, latest.YtDlp); err != nil {
			return fmt.Errorf("failed to install yt-dlp: %w", err)
		}
		state.YtDlp = latest.YtDlp
		_ = SaveState(cfg.CacheDir, state)
	}
	return nil
}

func installBunIfNeeded(cfg *Config, state *State, latest *LatestVersions, logger *slog.Logger) error {
	if state.Bun != latest.Bun || !fileExists(cfg.BunPath()) {
		logger.Debug("Installing bun", "version", latest.Bun)
		if err := installBun(cfg, latest.Bun); err != nil {
			return fmt.Errorf("failed to install bun: %w", err)
		}
		state.Bun = latest.Bun
		_ = SaveState(cfg.CacheDir, state)
	}
	return nil
}
