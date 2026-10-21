// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"time"
)

// Only upstream availability failures permit installed-runtime reuse.
var errUpstreamUnavailable = errors.New("upstream unavailable")

var downloadClient = &http.Client{Timeout: 10 * time.Minute}

func DownloadFile(url, filepath string) error {
	out, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", filepath, err)
	}
	defer func() { _ = out.Close() }()

	resp, err := downloadClient.Get(url)
	if err != nil {
		return fmt.Errorf("%w: request for %s: %v", errUpstreamUnavailable, url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: bad status: %s for %s", errUpstreamUnavailable, resp.Status, url)
	}

	if _, err := io.Copy(out, resp.Body); err != nil {
		if isDownloadDestinationError(err) {
			return fmt.Errorf("failed to write data to %s: %w", filepath, err)
		}
		return fmt.Errorf("%w: download of %s: %v", errUpstreamUnavailable, url, err)
	}

	if err := out.Close(); err != nil {
		return fmt.Errorf("failed to write data to %s: %w", filepath, err)
	}
	return nil
}

// os.File write errors are PathErrors; other copy errors came from the response body.
func isDownloadDestinationError(err error) bool {
	var pathErr *fs.PathError
	return errors.As(err, &pathErr)
}

func ConstructYtDlpURL(version, artifactName string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", ytDlpRepo, version, artifactName)
}

func ConstructBunURL(version, artifactName string) string {
	return fmt.Sprintf("https://github.com/oven-sh/bun/releases/download/bun-v%s/%s", version, artifactName)
}
