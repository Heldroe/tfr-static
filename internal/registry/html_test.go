package registry

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/Heldroe/tfr-static/internal/git"
	"github.com/Heldroe/tfr-static/internal/module"
)

func setupHTMLTestRepo(t *testing.T) (repoPath string, gitRunner *git.Runner) {
	t.Helper()
	tmpDir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = tmpDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
		}
	}

	run("init", "-b", "main")

	// Module with README
	serverDir := filepath.Join(tmpDir, "hetzner", "server")
	os.MkdirAll(serverDir, 0o755)
	os.WriteFile(filepath.Join(serverDir, "main.tf"), []byte(`resource "hetzner_server" "this" {}`), 0o644)
	os.WriteFile(filepath.Join(serverDir, "README.md"), []byte("# Hetzner Server\n\nA **server** module."), 0o644)

	// Module without README
	sgDir := filepath.Join(tmpDir, "aws", "ec2", "security-group")
	os.MkdirAll(sgDir, 0o755)
	os.WriteFile(filepath.Join(sgDir, "main.tf"), []byte(`resource "aws_security_group" "this" {}`), 0o644)

	run("add", ".")
	run("commit", "-m", "initial")
	run("tag", "-a", "hetzner/server-1.0.0", "-m", "v1.0.0")
	run("tag", "-a", "hetzner/server-1.1.0", "-m", "v1.1.0")
	run("tag", "-a", "aws/ec2/security-group-0.0.1", "-m", "v0.0.1")

	return tmpDir, git.NewRunner(tmpDir)
}

func TestHTMLGenerator_GenerateAll(t *testing.T) {
	_, gitRunner := setupHTMLTestRepo(t)
	outputDir := t.TempDir()
	gen := NewHTMLGenerator(gitRunner, outputDir, "index.html")

	grouped := map[string][]module.TagInfo{
		"hetzner/server": {
			{Tag: "hetzner/server-1.1.0", ModulePath: "hetzner/server", Version: semver.MustParse("1.1.0")},
			{Tag: "hetzner/server-1.0.0", ModulePath: "hetzner/server", Version: semver.MustParse("1.0.0")},
		},
		"aws/ec2/security-group": {
			{Tag: "aws/ec2/security-group-0.0.1", ModulePath: "aws/ec2/security-group", Version: semver.MustParse("0.0.1")},
		},
	}

	if err := gen.GenerateAll(grouped); err != nil {
		t.Fatal(err)
	}

	// Root index
	rootIndex := filepath.Join(outputDir, "index.html")
	assertFileContains(t, rootIndex, "Terraform Module Registry")
	assertFileContains(t, rootIndex, "hetzner/server")
	assertFileContains(t, rootIndex, "aws/ec2/security-group")

	// Module index
	modIndex := filepath.Join(outputDir, "hetzner", "server", "index.html")
	assertFileContains(t, modIndex, "hetzner/server")
	assertFileContains(t, modIndex, "1.1.0")
	assertFileContains(t, modIndex, "1.0.0")
	// Should contain rendered README
	assertFileContains(t, modIndex, "Hetzner Server")
	assertFileContains(t, modIndex, "<strong>server</strong>")

	// Version index
	verIndex := filepath.Join(outputDir, "hetzner", "server", "1.0.0", "index.html")
	assertFileContains(t, verIndex, "hetzner/server")
	assertFileContains(t, verIndex, "1.0.0")
	assertFileContains(t, verIndex, "module.tar.gz")
	assertFileContains(t, verIndex, `download="hetzner-server-1.0.0.tar.gz"`)
	// Should contain rendered README for this version
	assertFileContains(t, verIndex, "Hetzner Server")

	// Module without README should not have the readme div content
	sgIndex := filepath.Join(outputDir, "aws", "ec2", "security-group", "index.html")
	assertFileContains(t, sgIndex, "aws/ec2/security-group")
	assertFileNotContains(t, sgIndex, `<div class="readme">`)
}

func TestHTMLGenerator_CustomIndexFile(t *testing.T) {
	_, gitRunner := setupHTMLTestRepo(t)
	outputDir := t.TempDir()
	gen := NewHTMLGenerator(gitRunner, outputDir, "docs.html")

	grouped := map[string][]module.TagInfo{
		"hetzner/server": {
			{Tag: "hetzner/server-1.0.0", ModulePath: "hetzner/server", Version: semver.MustParse("1.0.0")},
		},
	}

	if err := gen.GenerateAll(grouped); err != nil {
		t.Fatal(err)
	}

	// Should use custom filename
	if _, err := os.Stat(filepath.Join(outputDir, "docs.html")); os.IsNotExist(err) {
		t.Error("custom index file not created at root")
	}
	if _, err := os.Stat(filepath.Join(outputDir, "hetzner", "server", "docs.html")); os.IsNotExist(err) {
		t.Error("custom index file not created for module")
	}
	if _, err := os.Stat(filepath.Join(outputDir, "hetzner", "server", "1.0.0", "docs.html")); os.IsNotExist(err) {
		t.Error("custom index file not created for version")
	}

	// Default index.html should NOT exist
	if _, err := os.Stat(filepath.Join(outputDir, "index.html")); !os.IsNotExist(err) {
		t.Error("default index.html should not exist when custom name is used")
	}
}

func TestHTMLGenerator_BackLinks(t *testing.T) {
	_, gitRunner := setupHTMLTestRepo(t)
	outputDir := t.TempDir()
	gen := NewHTMLGenerator(gitRunner, outputDir, "index.html")

	grouped := map[string][]module.TagInfo{
		"aws/ec2/security-group": {
			{Tag: "aws/ec2/security-group-0.0.1", ModulePath: "aws/ec2/security-group", Version: semver.MustParse("0.0.1")},
		},
	}

	if err := gen.GenerateAll(grouped); err != nil {
		t.Fatal(err)
	}

	// Module page should link back to root (3 levels up from aws/ec2/security-group/)
	modIndex := filepath.Join(outputDir, "aws", "ec2", "security-group", "index.html")
	assertFileContains(t, modIndex, `href="../../..">`)

	// Version page should link back to module
	verIndex := filepath.Join(outputDir, "aws", "ec2", "security-group", "0.0.1", "index.html")
	assertFileContains(t, verIndex, `href="../"`)
}

func assertFileContains(t *testing.T, path, substr string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if !strings.Contains(string(data), substr) {
		t.Errorf("%s does not contain %q", filepath.Base(path), substr)
	}
}

func assertFileNotContains(t *testing.T, path, substr string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if strings.Contains(strings.ToLower(string(data)), strings.ToLower(substr)) {
		t.Errorf("%s should not contain %q", filepath.Base(path), substr)
	}
}
