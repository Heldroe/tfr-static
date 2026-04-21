package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseInvalidationFormat(t *testing.T) {
	tests := []struct {
		input   string
		want    InvalidationFormat
		wantErr bool
	}{
		{"json", InvalidationFormatJSON, false},
		{"JSON", InvalidationFormatJSON, false},
		{"txt", InvalidationFormatTxt, false},
		{"cloudfront", InvalidationFormatCloudFront, false},
		{"CloudFront", InvalidationFormatCloudFront, false},
		{"xml", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseInvalidationFormat(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseInvalidationFormat(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseInvalidationFormat(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestWriteInvalidationFile_JSON(t *testing.T) {
	paths := []string{
		"/hetzner/server/versions",
		"/hetzner/server/1.0.0/download",
	}

	outFile := filepath.Join(t.TempDir(), "invalidation.json")
	if err := WriteInvalidationFile(paths, outFile, InvalidationFormatJSON); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(got))
	}
	if got[0] != paths[0] || got[1] != paths[1] {
		t.Errorf("got %v, want %v", got, paths)
	}
}

func TestWriteInvalidationFile_Txt(t *testing.T) {
	paths := []string{
		"/hetzner/server/versions",
		"/hetzner/server/1.0.0/download",
		"/aws/ec2/versions",
	}

	outFile := filepath.Join(t.TempDir(), "invalidation.txt")
	if err := WriteInvalidationFile(paths, outFile, InvalidationFormatTxt); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}
	for i, want := range paths {
		if lines[i] != want {
			t.Errorf("line %d = %q, want %q", i, lines[i], want)
		}
	}
}

func TestWriteInvalidationFile_CloudFront(t *testing.T) {
	paths := []string{
		"/hetzner/server/versions",
		"/hetzner/server/1.0.0/download",
	}

	outFile := filepath.Join(t.TempDir(), "invalidation.json")
	if err := WriteInvalidationFile(paths, outFile, InvalidationFormatCloudFront); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}

	var batch cloudFrontInvalidationBatch
	if err := json.Unmarshal(data, &batch); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if batch.Paths.Quantity != 2 {
		t.Errorf("Quantity = %d, want 2", batch.Paths.Quantity)
	}
	if len(batch.Paths.Items) != 2 {
		t.Fatalf("Items length = %d, want 2", len(batch.Paths.Items))
	}
	if batch.Paths.Items[0] != paths[0] || batch.Paths.Items[1] != paths[1] {
		t.Errorf("Items = %v, want %v", batch.Paths.Items, paths)
	}
	if !strings.HasPrefix(batch.CallerReference, "tfr-static-") {
		t.Errorf("CallerReference = %q, want prefix 'tfr-static-'", batch.CallerReference)
	}
}
