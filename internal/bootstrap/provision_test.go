// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupLegacyProvisioning(t *testing.T) {
	cacheDir := t.TempDir()
	cfg := &Config{
		CacheDir: cacheDir,
		BinDir:   filepath.Join(cacheDir, "bin"),
	}
	for _, path := range []string{
		filepath.Join(cfg.BinDir, "yt-dlp.bin"),
		filepath.Join(cfg.BinDir, "uv"),
		filepath.Join(cacheDir, "uv-bin", "yt-dlp"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("legacy"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cleanupLegacyProvisioning(cfg)

	for _, path := range []string{
		filepath.Join(cfg.BinDir, "yt-dlp.bin"),
		filepath.Join(cfg.BinDir, "uv"),
		filepath.Join(cacheDir, "uv-bin"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("legacy path still exists: %s", path)
		}
	}
}
