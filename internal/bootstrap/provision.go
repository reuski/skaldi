package bootstrap

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func Run(logger *slog.Logger) error {
	if err := CheckPrerequisites(); err != nil {
		return fmt.Errorf("prerequisites check failed: %w", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("config load failed: %w", err)
	}

	for _, dir := range []string{cfg.CacheDir, cfg.BinDir, cfg.UvBinDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	state, err := LoadState(cfg.CacheDir)
	if err != nil {
		state = &State{}
	}

	latest, err := FetchLatestVersions()
	if err != nil {
		if state.Uv != "" && fileExists(cfg.UvPath()) {
			logger.Debug("GitHub API unavailable, using installed versions")
			return generateShim(cfg)
		}
		return fmt.Errorf("failed to fetch latest versions (first run requires network): %w", err)
	}

	if state.Uv != latest.Uv || !fileExists(cfg.UvPath()) {
		logger.Debug("Installing uv", "version", latest.Uv)
		if err := installUv(cfg, latest.Uv); err != nil {
			return fmt.Errorf("failed to install uv: %w", err)
		}
		state.Uv = latest.Uv
		_ = SaveState(cfg.CacheDir, state)
	}

	if state.Bun != latest.Bun || !fileExists(cfg.BunPath()) {
		logger.Debug("Installing bun", "version", latest.Bun)
		if err := installBun(cfg, latest.Bun); err != nil {
			return fmt.Errorf("failed to install bun: %w", err)
		}
		state.Bun = latest.Bun
		_ = SaveState(cfg.CacheDir, state)
	}

	if err := upgradeYtDlp(cfg, state, logger); err != nil {
		return fmt.Errorf("failed to upgrade yt-dlp: %w", err)
	}

	return generateShim(cfg)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func installUv(cfg *Config, version string) error {
	info, err := GetPlatformInfo()
	if err != nil {
		return err
	}

	url := ConstructUvURL(version, info.UvArtifact)
	tmpFile := filepath.Join(cfg.CacheDir, "uv_download.tmp")
	defer os.Remove(tmpFile)

	if err := DownloadFile(url, tmpFile); err != nil {
		return err
	}
	return ExtractTarGz(tmpFile, "uv", cfg.UvPath())
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

func upgradeYtDlp(cfg *Config, state *State, logger *slog.Logger) error {
	logger.Debug("Installing/upgrading yt-dlp")
	cmd := exec.Command(cfg.UvPath(), "tool", "install", "--force", "yt-dlp[default]")
	cmd.Env = append(os.Environ(), "UV_TOOL_BIN_DIR="+cfg.UvBinDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("uv install failed: %s: %w", string(output), err)
	}

	version, err := getYtDlpVersion(cfg)
	if err == nil {
		state.YtDlp = version
		_ = SaveState(cfg.CacheDir, state)
	}
	return nil
}

func getYtDlpVersion(cfg *Config) (string, error) {
	cmd := exec.Command(cfg.RealYtDlpPath(), "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

func generateShim(cfg *Config) error {
	shimContent := fmt.Sprintf("#!/bin/sh\nexec \"%s\" --js-runtimes \"bun:%s\" \"$@\"\n",
		cfg.RealYtDlpPath(), cfg.BunPath())

	return os.WriteFile(cfg.ShimPath(), []byte(shimContent), 0755)
}
