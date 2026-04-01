package registry

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestGzipTextFiles(t *testing.T) {
	dir := t.TempDir()

	// Create text files
	os.MkdirAll(filepath.Join(dir, "mod", "1.0.0"), 0o755)
	os.WriteFile(filepath.Join(dir, "mod", "versions.json"), []byte(`{"modules":[]}`), 0o644)
	os.WriteFile(filepath.Join(dir, "mod", "1.0.0", "download"), []byte(`<html>test</html>`), 0o644)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte(`<html>root</html>`), 0o644)

	// Create a .tar.gz file (should be skipped)
	os.WriteFile(filepath.Join(dir, "mod", "1.0.0", "module.tar.gz"), []byte("fake archive"), 0o644)

	count, err := GzipTextFiles(dir)
	if err != nil {
		t.Fatal(err)
	}

	if count != 3 {
		t.Errorf("expected 3 compressed files, got %d", count)
	}

	// Verify text files are valid gzip
	for _, path := range []string{
		filepath.Join(dir, "mod", "versions.json"),
		filepath.Join(dir, "mod", "1.0.0", "download"),
		filepath.Join(dir, "index.html"),
	} {
		content := readGzipped(t, path)
		if len(content) == 0 {
			t.Errorf("%s: decompressed content is empty", path)
		}
	}

	// Verify versions.json decompresses to original content
	got := readGzipped(t, filepath.Join(dir, "mod", "versions.json"))
	if got != `{"modules":[]}` {
		t.Errorf("versions.json content = %q", got)
	}

	// Verify .tar.gz was NOT modified (still readable as plain text)
	data, _ := os.ReadFile(filepath.Join(dir, "mod", "1.0.0", "module.tar.gz"))
	if string(data) != "fake archive" {
		t.Error(".tar.gz file was modified")
	}
}

func readGzipped(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s: not valid gzip: %v", path, err)
	}
	defer gr.Close()
	out, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("%s: reading gzip: %v", path, err)
	}
	return string(out)
}
