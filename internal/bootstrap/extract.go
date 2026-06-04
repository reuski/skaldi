// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func ExtractZipTree(archivePath, destDir string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open zip archive: %w", err)
	}
	defer r.Close()

	root, err := filepath.Abs(destDir)
	if err != nil {
		return err
	}

	for _, f := range r.File {
		path := filepath.Join(root, filepath.FromSlash(f.Name))
		if path != root && !strings.HasPrefix(path, root+string(filepath.Separator)) {
			return fmt.Errorf("zip entry escapes destination: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("zip entry is a symlink: %s", f.Name)
		}
		if err := extractZipFile(f, path); err != nil {
			return err
		}
	}
	return nil
}

func extractZipFile(f *zip.File, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("failed to open file inside zip: %w", err)
	}
	defer rc.Close()

	mode := f.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	outFile, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("failed to create output file %s: %w", path, err)
	}
	if _, err := io.Copy(outFile, rc); err != nil {
		outFile.Close()
		return fmt.Errorf("failed to copy content: %w", err)
	}
	return outFile.Close()
}

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
