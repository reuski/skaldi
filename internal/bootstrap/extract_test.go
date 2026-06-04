// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractZipTree(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "runtime.zip")
	writeTestZip(t, archivePath, map[string]string{
		"_internal/data.txt": "runtime",
		"yt-dlp_macos":       "executable",
	})

	destDir := t.TempDir()
	if err := ExtractZipTree(archivePath, destDir); err != nil {
		t.Fatalf("ExtractZipTree failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(destDir, "_internal", "data.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "runtime" {
		t.Fatalf("extracted data = %q, want runtime", data)
	}
}

func TestExtractZipTreeRejectsTraversal(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "runtime.zip")
	writeTestZip(t, archivePath, map[string]string{"../outside": "bad"})

	if err := ExtractZipTree(archivePath, t.TempDir()); err == nil {
		t.Fatal("expected traversal error")
	}
}

func writeTestZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()

	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, content := range entries {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o755)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
