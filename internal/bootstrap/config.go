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
}

func LoadConfig() (*Config, error) {
	userCache, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("failed to determine user cache directory: %w", err)
	}

	cacheDir := filepath.Join(userCache, "jukebox")

	return &Config{
		CacheDir:  cacheDir,
		BinDir:    filepath.Join(cacheDir, "bin"),
		UvBinDir:  filepath.Join(cacheDir, "uv-bin"),
		MpvSocket: filepath.Join(cacheDir, "mpv.sock"),
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
