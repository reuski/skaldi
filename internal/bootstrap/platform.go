package bootstrap

import (
	"fmt"
	"runtime"
)

type PlatformInfo struct {
	UvArtifact  string
	BunArtifact string
}

func GetPlatformInfo() (*PlatformInfo, error) {
	goos := runtime.GOOS
	arch := runtime.GOARCH

	var uv, bun string

	switch goos {
	case "linux":
		switch arch {
		case "amd64":
			uv = "uv-x86_64-unknown-linux-gnu.tar.gz"
			bun = "bun-linux-x64.zip"
		case "arm64":
			uv = "uv-aarch64-unknown-linux-gnu.tar.gz"
			bun = "bun-linux-aarch64.zip"
		default:
			return nil, fmt.Errorf("unsupported linux architecture: %s", arch)
		}
	case "darwin":
		switch arch {
		case "amd64":
			uv = "uv-x86_64-apple-darwin.tar.gz"
			bun = "bun-darwin-x64.zip"
		case "arm64":
			uv = "uv-aarch64-apple-darwin.tar.gz"
			bun = "bun-darwin-aarch64.zip"
		default:
			return nil, fmt.Errorf("unsupported macos architecture: %s", arch)
		}
	default:
		return nil, fmt.Errorf("unsupported operating system: %s", goos)
	}

	return &PlatformInfo{UvArtifact: uv, BunArtifact: bun}, nil
}
