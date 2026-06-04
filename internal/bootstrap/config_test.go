// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.CacheDir == "" {
		t.Error("CacheDir should not be empty")
	}

	if cfg.BinDir == "" {
		t.Error("BinDir should not be empty")
	}

	if cfg.MpvSocket == "" {
		t.Error("MpvSocket should not be empty")
	}

	if cfg.ConfigPath == "" {
		t.Error("ConfigPath should not be empty")
	}
}

func TestConfig_Paths(t *testing.T) {
	cfg := &Config{
		CacheDir:   "/tmp/skaldi-test",
		BinDir:     "/tmp/skaldi-test/bin",
		MpvSocket:  "/tmp/skaldi-test/mpv.sock",
		ConfigPath: "/tmp/skaldi-test-config/skaldi/config.json",
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"BunPath", cfg.BunPath(), "/tmp/skaldi-test/bin/bun"},
		{"YtDlpPath", cfg.YtDlpPath(), "/tmp/skaldi-test/bin/yt-dlp.bin"},
		{"ShimPath", cfg.ShimPath(), "/tmp/skaldi-test/bin/yt-dlp"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				want := strings.ReplaceAll(tt.want, "/", string(filepath.Separator))
				if !strings.EqualFold(tt.got, want) {
					t.Errorf("%s = %q, want %q", tt.name, tt.got, want)
				}
			} else {
				if tt.got != tt.want {
					t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
				}
			}
		})
	}
}

func TestConfig_PathStructure(t *testing.T) {
	cfg := &Config{
		CacheDir:   "/home/user/.cache/skaldi",
		BinDir:     "/home/user/.cache/skaldi/bin",
		MpvSocket:  "/home/user/.cache/skaldi/mpv.sock",
		ConfigPath: "/home/user/.config/skaldi/config.json",
	}

	if !strings.HasPrefix(cfg.BinDir, cfg.CacheDir) {
		t.Error("BinDir should be inside CacheDir")
	}

	if !strings.HasPrefix(cfg.MpvSocket, cfg.CacheDir) {
		t.Error("MpvSocket should be inside CacheDir")
	}

	paths := []string{
		cfg.YtDlpPath(),
		cfg.BunPath(),
		cfg.ShimPath(),
	}

	for _, p := range paths {
		if !strings.HasPrefix(p, cfg.BinDir) {
			t.Errorf("Path %q should be inside BinDir", p)
		}
	}
}
