package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublish_DryRunAll(t *testing.T) {
	repo := newTestRepo(t)
	outDir := t.TempDir()

	stdout, err := runRoot(t,
		"publish", "--all", "--dry-run",
		"--repo", repo,
		"--output-dir", outDir,
		"--base-url", "https://registry.example.com",
	)
	if err != nil {
		t.Fatalf("publish --all --dry-run failed: %v", err)
	}

	for _, want := range []string{
		"[dry-run] Would publish",
		"hetzner/server",
		"aws/ec2/security-group",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("dry-run output missing %q; got:\n%s", want, stdout)
		}
	}

	// Nothing should be written to the output dir during a dry run.
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("dry-run wrote to output dir; got %d entries", len(entries))
	}
}

func TestPublish_TagWithInvalidationFile(t *testing.T) {
	repo := newTestRepo(t)
	outDir := t.TempDir()
	invFile := filepath.Join(t.TempDir(), "paths.txt")

	_, err := runRoot(t,
		"publish",
		"--tag", "hetzner/server-1.0.0",
		"--repo", repo,
		"--output-dir", outDir,
		"--base-url", "https://registry.example.com",
		"--invalidation-file", invFile,
	)
	if err != nil {
		t.Fatalf("publish --tag failed: %v", err)
	}

	// Archive + download HTML + versions.json should exist.
	for _, rel := range []string{
		"hetzner/server/1.0.0/module.tar.gz",
		"hetzner/server/1.0.0/download",
		"hetzner/server/versions.json",
	} {
		p := filepath.Join(outDir, rel)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected output file %s: %v", rel, err)
		}
	}

	body, err := os.ReadFile(invFile)
	if err != nil {
		t.Fatalf("reading invalidation file: %v", err)
	}
	if !strings.Contains(string(body), "/hetzner/server/versions") {
		t.Errorf("invalidation file missing versions entry; got:\n%s", string(body))
	}
}

func TestPublish_RequiresBaseURL(t *testing.T) {
	repo := newTestRepo(t)
	_, err := runRoot(t,
		"publish", "--all",
		"--repo", repo,
		"--output-dir", t.TempDir(),
	)
	if err == nil {
		t.Fatal("expected error when --base-url is missing")
	}
	if !strings.Contains(err.Error(), "base-url") {
		t.Errorf("expected base-url error, got: %v", err)
	}
}

func TestPublish_MutuallyExclusiveFlags(t *testing.T) {
	repo := newTestRepo(t)
	_, err := runRoot(t,
		"publish",
		"--tag", "hetzner/server-1.0.0",
		"--all",
		"--repo", repo,
		"--base-url", "https://registry.example.com",
	)
	if err == nil {
		t.Fatal("expected error for mutually-exclusive flags")
	}
	// cobra's MarkFlagsMutuallyExclusive produces this wording.
	if !strings.Contains(err.Error(), "mutually exclusive") &&
		!strings.Contains(err.Error(), "none of the others can be") {
		t.Errorf("expected mutually-exclusive error, got: %v", err)
	}
}

func TestPublish_OneRequired(t *testing.T) {
	repo := newTestRepo(t)
	_, err := runRoot(t,
		"publish",
		"--repo", repo,
		"--base-url", "https://registry.example.com",
	)
	if err == nil {
		t.Fatal("expected error when no mode flag is set")
	}
	if !strings.Contains(err.Error(), "at least one") {
		t.Errorf("expected one-required error, got: %v", err)
	}
}

func TestPublish_ModuleMode(t *testing.T) {
	repo := newTestRepo(t)
	outDir := t.TempDir()

	_, err := runRoot(t,
		"publish", "--module", "hetzner/server",
		"--repo", repo,
		"--output-dir", outDir,
		"--base-url", "https://registry.example.com",
		"--gzip",
	)
	if err != nil {
		t.Fatalf("publish --module failed: %v", err)
	}

	// Both tagged versions of hetzner/server should be materialized.
	for _, rel := range []string{
		"hetzner/server/0.1.0/module.tar.gz",
		"hetzner/server/1.0.0/module.tar.gz",
		"hetzner/server/versions.json",
	} {
		if _, err := os.Stat(filepath.Join(outDir, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	// --gzip rewrites text files in place with gzip content. Verify the magic bytes.
	body, err := os.ReadFile(filepath.Join(outDir, "hetzner/server/versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(body) < 2 || body[0] != 0x1f || body[1] != 0x8b {
		t.Error("expected versions.json to be gzip-compressed in place")
	}
}

func TestPublish_DevMode(t *testing.T) {
	repo := newTestRepo(t)
	outDir := t.TempDir()

	_, err := runRoot(t,
		"publish", "--dev",
		"--repo", repo,
		"--output-dir", outDir,
		"--base-url", "http://localhost",
	)
	if err != nil {
		t.Fatalf("publish --dev failed: %v", err)
	}

	// Dev mode publishes every discovered module as 0.0.0-dev from the working tree.
	for _, rel := range []string{
		"hetzner/server/0.0.0-dev/module.tar.gz",
		"aws/ec2/security-group/0.0.0-dev/module.tar.gz",
	} {
		if _, err := os.Stat(filepath.Join(outDir, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
}

func TestPublish_DevModeFilteredModule(t *testing.T) {
	repo := newTestRepo(t)
	outDir := t.TempDir()

	_, err := runRoot(t,
		"publish", "--dev", "--module", "hetzner/server",
		"--repo", repo,
		"--output-dir", outDir,
		"--base-url", "http://localhost",
	)
	if err != nil {
		t.Fatalf("publish --dev --module failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outDir, "hetzner/server/0.0.0-dev/module.tar.gz")); err != nil {
		t.Errorf("expected hetzner/server dev archive: %v", err)
	}
	// The other module should NOT have been published.
	if _, err := os.Stat(filepath.Join(outDir, "aws/ec2/security-group")); err == nil {
		t.Error("aws/ec2/security-group should not be published when filtered")
	}
}

func TestPublish_InvalidationFullURL(t *testing.T) {
	repo := newTestRepo(t)
	outDir := t.TempDir()
	invFile := filepath.Join(t.TempDir(), "paths.txt")

	_, err := runRoot(t,
		"publish",
		"--tag", "hetzner/server-1.0.0",
		"--repo", repo,
		"--output-dir", outDir,
		"--base-url", "https://registry.example.com",
		"--invalidation-file", invFile,
		"--invalidation-full-url",
	)
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	body, err := os.ReadFile(invFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "https://registry.example.com/") {
		t.Errorf("expected full URLs in invalidation file, got:\n%s", string(body))
	}
}

func TestPublish_InvalidationBaseURLRequiresFullURL(t *testing.T) {
	repo := newTestRepo(t)
	_, err := runRoot(t,
		"publish", "--all",
		"--repo", repo,
		"--output-dir", t.TempDir(),
		"--base-url", "https://registry.example.com",
		"--invalidation-file", filepath.Join(t.TempDir(), "x.txt"),
		"--invalidation-base-url", "https://cdn.example.com",
	)
	if err == nil {
		t.Fatal("expected error when invalidation-base-url without --invalidation-full-url")
	}
	if !strings.Contains(err.Error(), "invalidation_full_url") {
		t.Errorf("expected full_url error, got: %v", err)
	}
}

func TestPublish_AllWithHTML(t *testing.T) {
	repo := newTestRepo(t)
	outDir := t.TempDir()

	_, err := runRoot(t,
		"publish", "--all",
		"--repo", repo,
		"--output-dir", outDir,
		"--base-url", "https://registry.example.com",
		"--html",
	)
	if err != nil {
		t.Fatalf("publish --all --html failed: %v", err)
	}

	// HTML index pages should exist at the module root and for each version.
	for _, rel := range []string{
		"hetzner/server/index.html",
		"hetzner/server/1.0.0/index.html",
	} {
		body, err := os.ReadFile(filepath.Join(outDir, rel))
		if err != nil {
			t.Errorf("missing %s: %v", rel, err)
			continue
		}
		if !strings.Contains(string(body), "<html") {
			t.Errorf("%s does not look like HTML", rel)
		}
	}
}

func TestPublish_DryRunSingleTag(t *testing.T) {
	repo := newTestRepo(t)
	outDir := t.TempDir()

	stdout, err := runRoot(t,
		"publish",
		"--tag", "hetzner/server-1.0.0",
		"--dry-run",
		"--repo", repo,
		"--output-dir", outDir,
		"--base-url", "https://registry.example.com",
	)
	if err != nil {
		t.Fatalf("dry-run single tag failed: %v", err)
	}

	if !strings.Contains(stdout, "hetzner/server") {
		t.Errorf("expected module path in output; got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "1.0.0") {
		t.Errorf("expected version in output; got:\n%s", stdout)
	}
}
