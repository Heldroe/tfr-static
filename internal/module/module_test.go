package module

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Masterminds/semver/v3"
)

func TestParseTag(t *testing.T) {
	tests := []struct {
		name       string
		tag        string
		wantPath   string
		wantVer    string
		wantErr    bool
	}{
		{
			name:     "simple module",
			tag:      "hetzner/server-1.0.0",
			wantPath: "hetzner/server",
			wantVer:  "1.0.0",
		},
		{
			name:     "nested module",
			tag:      "aws/ec2/alb-0.0.1",
			wantPath: "aws/ec2/alb",
			wantVer:  "0.0.1",
		},
		{
			name:     "module with dash in name",
			tag:      "aws/ec2/security-group-2.1.0",
			wantPath: "aws/ec2/security-group",
			wantVer:  "2.1.0",
		},
		{
			name:     "deeply nested with dashes",
			tag:      "my-org/my-module/sub-module-10.20.30",
			wantPath: "my-org/my-module/sub-module",
			wantVer:  "10.20.30",
		},
		{
			name:     "pre-release version",
			tag:      "hetzner/server-1.0.0-rc.1",
			wantPath: "hetzner/server",
			wantVer:  "1.0.0-rc.1",
		},
		{
			name:     "build metadata",
			tag:      "hetzner/server-1.0.0+build.123",
			wantPath: "hetzner/server",
			wantVer:  "1.0.0+build.123",
		},
		{
			name:    "no version",
			tag:     "hetzner/server",
			wantErr: true,
		},
		{
			name:    "empty string",
			tag:     "",
			wantErr: true,
		},
		{
			name:    "just a version",
			tag:     "-1.0.0",
			wantErr: true,
		},
		{
			name:    "v prefix on version (invalid strict semver)",
			tag:     "hetzner/server-v1.0.0",
			wantErr: true,
		},
		{
			name:    "random string",
			tag:     "not-a-module-tag",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParseTag(tt.tag)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseTag(%q) expected error, got path=%q version=%s", tt.tag, info.ModulePath, info.Version)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTag(%q) unexpected error: %v", tt.tag, err)
			}
			if info.ModulePath != tt.wantPath {
				t.Errorf("ParseTag(%q) path = %q, want %q", tt.tag, info.ModulePath, tt.wantPath)
			}
			if info.Version.Original() != tt.wantVer {
				t.Errorf("ParseTag(%q) version = %q, want %q", tt.tag, info.Version.Original(), tt.wantVer)
			}
			if info.Tag != tt.tag {
				t.Errorf("ParseTag(%q) tag = %q, want %q", tt.tag, info.Tag, tt.tag)
			}
		})
	}
}

func TestFormatTag(t *testing.T) {
	v := semver.MustParse("1.2.3")
	got := FormatTag("aws/ec2/security-group", v)
	want := "aws/ec2/security-group-1.2.3"
	if got != want {
		t.Errorf("FormatTag() = %q, want %q", got, want)
	}
}

func TestParseTagRoundTrip(t *testing.T) {
	tags := []string{
		"hetzner/server-1.0.0",
		"aws/ec2/security-group-2.1.0",
		"my-org/my-module/sub-module-10.20.30",
	}
	for _, tag := range tags {
		info, err := ParseTag(tag)
		if err != nil {
			t.Fatalf("ParseTag(%q) error: %v", tag, err)
		}
		roundTripped := FormatTag(info.ModulePath, info.Version)
		if roundTripped != tag {
			t.Errorf("round trip: %q -> %q", tag, roundTripped)
		}
	}
}

func TestDiscoverModules(t *testing.T) {
	// Create a temp directory structure
	tmpDir := t.TempDir()

	// Create module directories with .tf files
	dirs := []struct {
		path  string
		files []string
	}{
		{path: "hetzner/server", files: []string{"main.tf"}},
		{path: "hetzner/network", files: []string{"main.tf", "variables.tf"}},
		{path: "aws/ec2/alb", files: []string{"main.tf"}},
		{path: "aws/ec2/security-group", files: []string{"main.tf"}},
		{path: "aws/iam/user", files: []string{"main.tf"}},
		// aws/ec2 itself is also a module (nested case)
		{path: "aws/ec2", files: []string{"main.tf"}},
		// A directory without .tf files — not a module
		{path: "docs", files: []string{"readme.md"}},
		// Root level .tf files — should be excluded
		{path: ".", files: []string{"root.tf"}},
		// Hidden directory — should be skipped
		{path: ".terraform/modules", files: []string{"cache.tf"}},
	}

	for _, d := range dirs {
		dirPath := filepath.Join(tmpDir, d.path)
		if err := os.MkdirAll(dirPath, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range d.files {
			if err := os.WriteFile(filepath.Join(dirPath, f), []byte("# test"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	modules, err := DiscoverModules(tmpDir)
	if err != nil {
		t.Fatalf("DiscoverModules() error: %v", err)
	}

	want := []string{
		"aws/ec2",
		"aws/ec2/alb",
		"aws/ec2/security-group",
		"aws/iam/user",
		"hetzner/network",
		"hetzner/server",
	}

	if len(modules) != len(want) {
		var got []string
		for _, m := range modules {
			got = append(got, m.Path)
		}
		t.Fatalf("DiscoverModules() got %v, want %v", got, want)
	}

	for i, m := range modules {
		if m.Path != want[i] {
			t.Errorf("module[%d] = %q, want %q", i, m.Path, want[i])
		}
	}
}

func TestLatestVersion(t *testing.T) {
	tests := []struct {
		name     string
		tags     []TagInfo
		wantVer  string
		wantNil  bool
	}{
		{
			name:    "empty list",
			tags:    nil,
			wantNil: true,
		},
		{
			name: "single version",
			tags: []TagInfo{
				{Version: semver.MustParse("1.0.0")},
			},
			wantVer: "1.0.0",
		},
		{
			name: "handles semver ordering correctly (1.11 > 1.9)",
			tags: []TagInfo{
				{Version: semver.MustParse("1.9.0")},
				{Version: semver.MustParse("1.11.0")},
				{Version: semver.MustParse("1.2.0")},
			},
			wantVer: "1.11.0",
		},
		{
			name: "mixed versions",
			tags: []TagInfo{
				{Version: semver.MustParse("0.1.0")},
				{Version: semver.MustParse("2.0.0")},
				{Version: semver.MustParse("1.9.1")},
				{Version: semver.MustParse("0.0.1")},
			},
			wantVer: "2.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := LatestVersion(tt.tags)
			if tt.wantNil {
				if result != nil {
					t.Errorf("expected nil, got %s", result.Version)
				}
				return
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if result.Version.Original() != tt.wantVer {
				t.Errorf("got %s, want %s", result.Version, tt.wantVer)
			}
		})
	}
}

func TestNextVersion(t *testing.T) {
	tests := []struct {
		current string
		bump    string
		want    string
	}{
		{"1.0.0", "patch", "1.0.1"},
		{"1.0.0", "minor", "1.1.0"},
		{"1.0.0", "major", "2.0.0"},
		{"0.0.0", "patch", "0.0.1"},
		{"0.0.0", "minor", "0.1.0"},
		{"0.0.0", "major", "1.0.0"},
		{"1.9.1", "patch", "1.9.2"},
		{"1.9.1", "minor", "1.10.0"},
		{"1.9.1", "major", "2.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.current+"_"+tt.bump, func(t *testing.T) {
			v := semver.MustParse(tt.current)
			got, err := NextVersion(v, tt.bump)
			if err != nil {
				t.Fatal(err)
			}
			if got.Original() != tt.want {
				t.Errorf("NextVersion(%s, %s) = %s, want %s", tt.current, tt.bump, got, tt.want)
			}
		})
	}
}

func TestNextVersionInvalidBump(t *testing.T) {
	v := semver.MustParse("1.0.0")
	_, err := NextVersion(v, "invalid")
	if err == nil {
		t.Error("expected error for invalid bump type")
	}
}

func TestParseAllTags(t *testing.T) {
	raw := []string{
		"hetzner/server-1.0.0",
		"hetzner/server-0.1.0",
		"not-a-valid-tag",
		"v2.0.0",
		"aws/ec2/alb-0.0.1",
		"some-random-text",
	}

	result := ParseAllTags(raw)
	if len(result) != 3 {
		t.Fatalf("expected 3 parsed tags, got %d", len(result))
	}
}

func TestGroupTagsByModule(t *testing.T) {
	tags := []TagInfo{
		{ModulePath: "a", Version: semver.MustParse("1.0.0")},
		{ModulePath: "a", Version: semver.MustParse("1.1.0")},
		{ModulePath: "b", Version: semver.MustParse("0.1.0")},
	}

	grouped := GroupTagsByModule(tags)
	if len(grouped["a"]) != 2 {
		t.Errorf("expected 2 tags for module a, got %d", len(grouped["a"]))
	}
	if len(grouped["b"]) != 1 {
		t.Errorf("expected 1 tag for module b, got %d", len(grouped["b"]))
	}
}

func TestSortVersions(t *testing.T) {
	tags := []TagInfo{
		{Version: semver.MustParse("1.0.0")},
		{Version: semver.MustParse("0.1.0")},
		{Version: semver.MustParse("2.0.0")},
		{Version: semver.MustParse("1.11.0")},
		{Version: semver.MustParse("1.9.0")},
	}

	SortVersionsAsc(tags)
	expected := []string{"0.1.0", "1.0.0", "1.9.0", "1.11.0", "2.0.0"}
	for i, tag := range tags {
		if tag.Version.Original() != expected[i] {
			t.Errorf("SortVersionsAsc: position %d = %s, want %s", i, tag.Version, expected[i])
		}
	}

	SortVersionsDesc(tags)
	expectedDesc := []string{"2.0.0", "1.11.0", "1.9.0", "1.0.0", "0.1.0"}
	for i, tag := range tags {
		if tag.Version.Original() != expectedDesc[i] {
			t.Errorf("SortVersionsDesc: position %d = %s, want %s", i, tag.Version, expectedDesc[i])
		}
	}
}

func TestFilterTagsForModule(t *testing.T) {
	tags := []TagInfo{
		{ModulePath: "a/b", Version: semver.MustParse("1.0.0")},
		{ModulePath: "a/b/c", Version: semver.MustParse("1.0.0")},
		{ModulePath: "a/b", Version: semver.MustParse("2.0.0")},
		{ModulePath: "x/y", Version: semver.MustParse("1.0.0")},
	}

	result := FilterTagsForModule(tags, "a/b")
	if len(result) != 2 {
		t.Errorf("expected 2 tags, got %d", len(result))
	}

	// Ensure it doesn't match partial prefixes
	result = FilterTagsForModule(tags, "a")
	if len(result) != 0 {
		t.Errorf("expected 0 tags for partial match, got %d", len(result))
	}
}
