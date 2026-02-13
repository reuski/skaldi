package bootstrap

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func ExtractTarGz(archivePath, fileName, destPath string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open archive: %w", err)
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read error: %w", err)
		}

		if filepath.Base(header.Name) == fileName {
			outFile, err := os.Create(destPath)
			if err != nil {
				return fmt.Errorf("failed to create output file %s: %w", destPath, err)
			}

			if _, err := io.Copy(outFile, tr); err != nil {
				outFile.Close()
				return fmt.Errorf("failed to copy content: %w", err)
			}
			outFile.Close()

			return os.Chmod(destPath, 0755)
		}
	}

	return fmt.Errorf("file %s not found in archive %s", fileName, archivePath)
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
