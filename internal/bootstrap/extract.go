// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func ExtractZip(archivePath, fileName, destPath string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open zip archive: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if filepath.Base(f.Name) == fileName {
			rc, err := f.Open()
			if err != nil {
				return fmt.Errorf("failed to open file inside zip: %w", err)
			}
			defer rc.Close()

			outFile, err := os.Create(destPath)
			if err != nil {
				return fmt.Errorf("failed to create output file %s: %w", destPath, err)
			}

			if _, err := io.Copy(outFile, rc); err != nil {
				outFile.Close()
				return fmt.Errorf("failed to copy content: %w", err)
			}
			outFile.Close()

			return os.Chmod(destPath, 0755)
		}
	}

	return fmt.Errorf("file %s not found in archive %s", fileName, archivePath)
}
