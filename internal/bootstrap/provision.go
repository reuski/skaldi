// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	ytDlpWarmupTimeout  = 30 * time.Second
	ytDlpStartupTimeout = 3 * time.Second
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
			cleanupLegacyProvisioning(cfg)
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

	cleanupLegacyProvisioning(cfg)
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
	archivePath := filepath.Join(cfg.CacheDir, "yt-dlp_download.tmp")
	defer os.Remove(archivePath)
	if err := DownloadFile(url, archivePath); err != nil {
		return err
	}

	runtimeDir, err := os.MkdirTemp(cfg.BinDir, ".yt-dlp-runtime-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(runtimeDir)

	if err := ExtractZipTree(archivePath, runtimeDir); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "_internal")); err != nil {
		return fmt.Errorf("yt-dlp runtime is not unpacked: %w", err)
	}

	executablePath := filepath.Join(runtimeDir, info.YtDlpExecutable)
	ytDlpPath := filepath.Join(runtimeDir, "yt-dlp")
	if err := os.Rename(executablePath, ytDlpPath); err != nil {
		return fmt.Errorf("failed to prepare yt-dlp executable: %w", err)
	}
	if err := os.Chmod(ytDlpPath, 0o755); err != nil {
		return err
	}
	if err := validateYtDlpRuntime(ytDlpPath); err != nil {
		return err
	}

	if err := os.RemoveAll(cfg.YtDlpDir()); err != nil {
		return err
	}
	if err := os.Rename(runtimeDir, cfg.YtDlpDir()); err != nil {
		return err
	}
	return nil
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

func validateYtDlpRuntime(path string) error {
	warmupCtx, cancelWarmup := context.WithTimeout(context.Background(), ytDlpWarmupTimeout)
	defer cancelWarmup()
	if output, err := exec.CommandContext(warmupCtx, path, "--version").CombinedOutput(); err != nil {
		return fmt.Errorf("yt-dlp warm-up failed: %s: %w", string(output), err)
	}

	startupCtx, cancelStartup := context.WithTimeout(context.Background(), ytDlpStartupTimeout)
	defer cancelStartup()
	if output, err := exec.CommandContext(startupCtx, path, "--version").CombinedOutput(); err != nil {
		if startupCtx.Err() != nil {
			return fmt.Errorf("yt-dlp startup exceeds %s performance requirement", ytDlpStartupTimeout)
		}
		return fmt.Errorf("yt-dlp startup check failed: %s: %w", string(output), err)
	}
	return nil
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

func cleanupLegacyProvisioning(cfg *Config) {
	_ = os.Remove(filepath.Join(cfg.BinDir, "yt-dlp.bin"))
	_ = os.Remove(filepath.Join(cfg.BinDir, "uv"))
	_ = os.RemoveAll(filepath.Join(cfg.CacheDir, "uv-bin"))
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
	return state.YtDlp != "" && ytDlpRuntimeExists(cfg) &&
		state.Bun != "" && fileExists(cfg.BunPath())
}

func ytDlpRuntimeExists(cfg *Config) bool {
	return fileExists(cfg.YtDlpPath()) && fileExists(filepath.Join(cfg.YtDlpDir(), "_internal"))
}

func installYtDlpIfNeeded(cfg *Config, state *State, latest *LatestVersions, logger *slog.Logger) error {
	if state.YtDlp != latest.YtDlp || !ytDlpRuntimeExists(cfg) {
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
