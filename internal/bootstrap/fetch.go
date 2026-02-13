package bootstrap

import (
	"fmt"
	"io"
	"net/http"
	"os"
)

func DownloadFile(url, filepath string) error {
	out, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", filepath, err)
	}
	defer out.Close()

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("http request failed for %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s for %s", resp.Status, url)
	}

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write data to %s: %w", filepath, err)
	}
	return nil
}

func ConstructUvURL(version, artifactName string) string {
	return fmt.Sprintf("https://github.com/astral-sh/uv/releases/download/%s/%s", version, artifactName)
}

func ConstructBunURL(version, artifactName string) string {
	return fmt.Sprintf("https://github.com/oven-sh/bun/releases/download/bun-v%s/%s", version, artifactName)
}
