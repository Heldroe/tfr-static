package registry

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GzipTextFiles walks the output directory and gzip-compresses all files
// except .tar.gz archives (which are already compressed). Files are compressed
// in-place, keeping their original names. This is intended for pre-compressed
// uploads to S3/GCS with Content-Encoding: gzip.
func GzipTextFiles(outputDir string) (int, error) {
	count := 0
	err := filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(info.Name(), ".tar.gz") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		var buf bytes.Buffer
		gw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if err != nil {
			return fmt.Errorf("creating gzip writer: %w", err)
		}
		if _, err := gw.Write(data); err != nil {
			return fmt.Errorf("gzip writing %s: %w", path, err)
		}
		if err := gw.Close(); err != nil {
			return fmt.Errorf("gzip closing %s: %w", path, err)
		}

		if err := os.WriteFile(path, buf.Bytes(), info.Mode()); err != nil {
			return fmt.Errorf("writing gzipped %s: %w", path, err)
		}
		count++
		return nil
	})
	return count, err
}
