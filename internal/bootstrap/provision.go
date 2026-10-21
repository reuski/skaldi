// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"context"
	"errors"
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
	bunProbeTimeout     = 5 * time.Second
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

	state := loadOrCreateState(cfg)

	ytDlpSelection, bunSelection := fetchLatestVersions(cfg.CacheDir, logger)

	if err := ensureYtDlp(cfg, state, ytDlpSelection, logger); err != nil {
		return fmt.Errorf("yt-dlp provisioning failed: %w", err)
	}

	if err := ensureBun(cfg, state, bunSelection, logger); err != nil {
		return fmt.Errorf("bun provisioning failed: %w", err)
	}

	cleanupLegacyProvisioning(cfg)
	return generateShim(cfg)
}

func ensureYtDlp(cfg *Config, state *State, selection sourceVersion, logger *slog.Logger) error {
	if err := fatalCheckError("yt-dlp", selection); err != nil {
		return err
	}
	if !selection.Latest {
		logger.Warn("latest yt-dlp cannot be guaranteed",
			"version", selection.Version, "error", selection.Err)
	}

	if selection.Version != "" && state.YtDlp == selection.Version && ytDlpRuntimeExists(cfg) {
		if selection.Latest {
			return nil
		}
		return reuseInstalledYtDlp(cfg, state)
	}

	if selection.Version != "" {
		logger.Debug("Installing yt-dlp", "version", selection.Version)
		err := installYtDlp(cfg, selection.Version)
		if err == nil {
			state.YtDlp = selection.Version
			return saveInstalledVersions(cfg, state)
		}
		if !errors.Is(err, errUpstreamUnavailable) {
			return fmt.Errorf("failed to install yt-dlp: %w", err)
		}
		logger.Warn("yt-dlp download unavailable, reusing installed version",
			"version", state.YtDlp, "error", err)
	} else {
		logger.Warn("no yt-dlp version was selected, reusing installed version",
			"error", selection.Err)
	}

	return reuseInstalledYtDlp(cfg, state)
}

func ensureBun(cfg *Config, state *State, selection sourceVersion, logger *slog.Logger) error {
	if err := fatalCheckError("bun", selection); err != nil {
		return err
	}
	if !selection.Latest {
		logger.Warn("latest bun cannot be guaranteed",
			"version", selection.Version, "error", selection.Err)
	}

	if selection.Version != "" && state.Bun == selection.Version && fileExists(cfg.BunPath()) {
		if selection.Latest {
			return nil
		}
		return reuseInstalledBun(cfg, state)
	}

	if selection.Version != "" {
		logger.Debug("Installing bun", "version", selection.Version)
		err := installBun(cfg, selection.Version)
		if err == nil {
			state.Bun = selection.Version
			return saveInstalledVersions(cfg, state)
		}
		if !errors.Is(err, errUpstreamUnavailable) {
			return fmt.Errorf("failed to install bun: %w", err)
		}
		logger.Warn("bun download unavailable, reusing installed version",
			"version", state.Bun, "error", err)
	} else {
		logger.Warn("no bun version was selected, reusing installed version",
			"error", selection.Err)
	}

	return reuseInstalledBun(cfg, state)
}

func fatalCheckError(name string, selection sourceVersion) error {
	if selection.Err == nil || errors.Is(selection.Err, errUpstreamUnavailable) {
		return nil
	}
	return fmt.Errorf("failed to check latest %s version: %w", name, selection.Err)
}

func reuseInstalledYtDlp(cfg *Config, state *State) error {
	if state.YtDlp == "" {
		return errors.New("no previously installed yt-dlp is recorded")
	}
	if !ytDlpRuntimeExists(cfg) {
		return fmt.Errorf("previously installed yt-dlp %s is missing", state.YtDlp)
	}
	if err := validateYtDlpRuntime(cfg.YtDlpPath()); err != nil {
		return fmt.Errorf("previously installed yt-dlp %s failed validation: %w", state.YtDlp, err)
	}
	return nil
}

func reuseInstalledBun(cfg *Config, state *State) error {
	if state.Bun == "" {
		return errors.New("no previously installed bun is recorded")
	}
	if !fileExists(cfg.BunPath()) {
		return fmt.Errorf("previously installed bun %s is missing", state.Bun)
	}
	if err := probeBun(cfg.BunPath()); err != nil {
		return fmt.Errorf("previously installed bun %s failed validation: %w", state.Bun, err)
	}
	return nil
}

func saveInstalledVersions(cfg *Config, state *State) error {
	if err := SaveState(cfg.CacheDir, state); err != nil {
		return fmt.Errorf("failed to record installed versions: %w", err)
	}
	return nil
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
	defer func() { _ = os.Remove(archivePath) }()
	if err := DownloadFile(url, archivePath); err != nil {
		return err
	}

	runtimeDir, err := os.MkdirTemp(cfg.BinDir, ".yt-dlp-runtime-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(runtimeDir) }()

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
	defer func() { _ = os.Remove(tmpFile) }()

	if err := DownloadFile(url, tmpFile); err != nil {
		return err
	}

	// Validate on the same filesystem before replacing the working runtime.
	candidate, err := os.CreateTemp(cfg.BinDir, ".bun-candidate-")
	if err != nil {
		return fmt.Errorf("failed to stage bun candidate: %w", err)
	}
	candidatePath := candidate.Name()
	if err := candidate.Close(); err != nil {
		_ = os.Remove(candidatePath)
		return fmt.Errorf("failed to stage bun candidate: %w", err)
	}
	defer func() { _ = os.Remove(candidatePath) }()

	if err := ExtractZip(tmpFile, "bun", candidatePath); err != nil {
		return err
	}
	if err := probeBun(candidatePath); err != nil {
		return err
	}
	if err := os.Rename(candidatePath, cfg.BunPath()); err != nil {
		return fmt.Errorf("failed to install bun: %w", err)
	}
	return nil
}

func probeBun(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), bunProbeTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("bun exceeds %s version check", bunProbeTimeout)
		}
		return fmt.Errorf("bun --version failed: %s: %w", string(output), err)
	}
	return nil
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
	return os.WriteFile(shimPath, []byte(shimContent(ytDlpPath, bunPath)), 0o755)
}

func shimContent(ytDlpPath, bunPath string) string {
	return fmt.Sprintf("#!/bin/sh\nexec \"%s\" --js-runtimes \"bun:%s\" \"$@\"\n", ytDlpPath, bunPath)
}

func writeToolShim(shimPath, toolPath string) error {
	return os.WriteFile(shimPath, []byte(toolShimContent(toolPath)), 0o755)
}

func toolShimContent(toolPath string) string {
	return fmt.Sprintf("#!/bin/sh\nexec \"%s\" \"$@\"\n", toolPath)
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
	if err := writeShim(cfg.ShimPath(), ytDlpPath, cfg.BunPath()); err != nil {
		return err
	}
	if err := writeToolShim(cfg.BunPath(), bunPath); err != nil {
		return err
	}
	return validateYtDlpRuntime(cfg.ShimPath())
}

func cleanupLegacyProvisioning(cfg *Config) {
	_ = os.Remove(filepath.Join(cfg.BinDir, "yt-dlp.bin"))
	_ = os.Remove(filepath.Join(cfg.BinDir, "uv"))
	_ = os.RemoveAll(filepath.Join(cfg.CacheDir, "uv-bin"))
}

func createDirectories(cfg *Config) error {
	for _, dir := range []string{cfg.CacheDir, cfg.BinDir, cfg.AppDataDir, cfg.DataDir} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}
	return nil
}

// loadOrCreateState treats missing or corrupt state as empty rather than fatal,
// so the normal install/reuse path re-establishes a valid recorded version.
func loadOrCreateState(cfg *Config) *State {
	state, err := LoadState(cfg.CacheDir)
	if err != nil {
		return &State{}
	}
	return state
}

func ytDlpRuntimeExists(cfg *Config) bool {
	return fileExists(cfg.YtDlpPath()) && fileExists(filepath.Join(cfg.YtDlpDir(), "_internal"))
}
