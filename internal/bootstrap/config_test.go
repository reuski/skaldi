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

	if cfg.UvBinDir == "" {
		t.Error("UvBinDir should not be empty")
	}

	if cfg.MpvSocket == "" {
		t.Error("MpvSocket should not be empty")
	}
}

func TestConfig_Paths(t *testing.T) {
	cfg := &Config{
		CacheDir:  "/tmp/skaldi-test",
		BinDir:    "/tmp/skaldi-test/bin",
		UvBinDir:  "/tmp/skaldi-test/uv-bin",
		MpvSocket: "/tmp/skaldi-test/mpv.sock",
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"UvPath", cfg.UvPath(), "/tmp/skaldi-test/bin/uv"},
		{"BunPath", cfg.BunPath(), "/tmp/skaldi-test/bin/bun"},
		{"ShimPath", cfg.ShimPath(), "/tmp/skaldi-test/bin/yt-dlp"},
		{"RealYtDlpPath", cfg.RealYtDlpPath(), "/tmp/skaldi-test/uv-bin/yt-dlp"},
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
		CacheDir:  "/home/user/.cache/skaldi",
		BinDir:    "/home/user/.cache/skaldi/bin",
		UvBinDir:  "/home/user/.cache/skaldi/uv-bin",
		MpvSocket: "/home/user/.cache/skaldi/mpv.sock",
	}

	if !strings.HasPrefix(cfg.BinDir, cfg.CacheDir) {
		t.Error("BinDir should be inside CacheDir")
	}

	if !strings.HasPrefix(cfg.UvBinDir, cfg.CacheDir) {
		t.Error("UvBinDir should be inside CacheDir")
	}

	if !strings.HasPrefix(cfg.MpvSocket, cfg.CacheDir) {
		t.Error("MpvSocket should be inside CacheDir")
	}

	paths := []string{
		cfg.UvPath(),
		cfg.BunPath(),
		cfg.ShimPath(),
	}

	for _, p := range paths {
		if !strings.HasPrefix(p, cfg.BinDir) {
			t.Errorf("Path %q should be inside BinDir", p)
		}
	}
}
