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

func TestHTMLGenerator_GenerateForVersion(t *testing.T) {
	_, gitRunner := setupHTMLTestRepo(t)
	outputDir := t.TempDir()
	gen := NewHTMLGenerator(gitRunner, outputDir, "index.html")

	newTag := module.TagInfo{
		Tag: "hetzner/server-2.0.0", ModulePath: "hetzner/server", Version: semver.MustParse("2.0.0"),
	}
	moduleTags := []module.TagInfo{
		newTag,
		{Tag: "hetzner/server-1.1.0", ModulePath: "hetzner/server", Version: semver.MustParse("1.1.0")},
		{Tag: "hetzner/server-1.0.0", ModulePath: "hetzner/server", Version: semver.MustParse("1.0.0")},
	}
	allGrouped := map[string][]module.TagInfo{
		"hetzner/server": moduleTags,
		"aws/ec2/security-group": {
			{Tag: "aws/ec2/security-group-0.0.1", ModulePath: "aws/ec2/security-group", Version: semver.MustParse("0.0.1")},
		},
	}

	if err := gen.GenerateForVersion(newTag, moduleTags, allGrouped); err != nil {
		t.Fatal(err)
	}

	// Root index should list all modules
	rootIndex := filepath.Join(outputDir, "index.html")
	assertFileContains(t, rootIndex, "hetzner/server")
	assertFileContains(t, rootIndex, "aws/ec2/security-group")

	// Module index should list all versions including the new one
	modIndex := filepath.Join(outputDir, "hetzner", "server", "index.html")
	assertFileContains(t, modIndex, "2.0.0")
	assertFileContains(t, modIndex, "1.1.0")
	assertFileContains(t, modIndex, "1.0.0")

	// New version page should exist
	verIndex := filepath.Join(outputDir, "hetzner", "server", "2.0.0", "index.html")
	assertFileContains(t, verIndex, "2.0.0")
	assertFileContains(t, verIndex, "hetzner/server")

	// Other modules' pages should NOT be generated
	sgIndex := filepath.Join(outputDir, "aws", "ec2", "security-group", "index.html")
	if _, err := os.Stat(sgIndex); !os.IsNotExist(err) {
		t.Error("should not generate HTML for unrelated modules")
	}

	// Other versions' pages should NOT be generated
	oldVerIndex := filepath.Join(outputDir, "hetzner", "server", "1.0.0", "index.html")
	if _, err := os.Stat(oldVerIndex); !os.IsNotExist(err) {
		t.Error("should not generate HTML for other versions of the same module")
	}
}

func TestHTMLGenerator_GenerateForModule(t *testing.T) {
	_, gitRunner := setupHTMLTestRepo(t)
	outputDir := t.TempDir()
	gen := NewHTMLGenerator(gitRunner, outputDir, "index.html")

	moduleTags := []module.TagInfo{
		{Tag: "hetzner/server-1.1.0", ModulePath: "hetzner/server", Version: semver.MustParse("1.1.0")},
		{Tag: "hetzner/server-1.0.0", ModulePath: "hetzner/server", Version: semver.MustParse("1.0.0")},
	}
	allGrouped := map[string][]module.TagInfo{
		"hetzner/server": moduleTags,
		"aws/ec2/security-group": {
			{Tag: "aws/ec2/security-group-0.0.1", ModulePath: "aws/ec2/security-group", Version: semver.MustParse("0.0.1")},
		},
	}

	if err := gen.GenerateForModule("hetzner/server", moduleTags, allGrouped); err != nil {
		t.Fatal(err)
	}

	// Root index should list all modules
	rootIndex := filepath.Join(outputDir, "index.html")
	assertFileContains(t, rootIndex, "hetzner/server")
	assertFileContains(t, rootIndex, "aws/ec2/security-group")

	// Module index should list all versions
	modIndex := filepath.Join(outputDir, "hetzner", "server", "index.html")
	assertFileContains(t, modIndex, "1.1.0")
	assertFileContains(t, modIndex, "1.0.0")

	// Each version page should exist
	for _, v := range []string{"1.1.0", "1.0.0"} {
		verIndex := filepath.Join(outputDir, "hetzner", "server", v, "index.html")
		assertFileContains(t, verIndex, v)
	}

	// Other modules' pages should NOT be generated
	sgIndex := filepath.Join(outputDir, "aws", "ec2", "security-group", "index.html")
	if _, err := os.Stat(sgIndex); !os.IsNotExist(err) {
		t.Error("should not generate HTML for unrelated modules")
	}
}

func TestHTMLGenerator_WithFilesystemReader(t *testing.T) {
	repoPath, gitRunner := setupHTMLTestRepo(t)
	outputDir := t.TempDir()
	gen := NewHTMLGenerator(gitRunner, outputDir, "index.html")
	gen.ReadmeReader = FilesystemReadmeReader(repoPath)

	grouped := map[string][]module.TagInfo{
		"hetzner/server": {
			{Tag: "hetzner/server-1.0.0", ModulePath: "hetzner/server", Version: semver.MustParse("1.0.0")},
		},
	}

	if err := gen.GenerateAll(grouped); err != nil {
		t.Fatal(err)
	}

	modIndex := filepath.Join(outputDir, "hetzner", "server", "index.html")
	assertFileContains(t, modIndex, "Hetzner Server")
	assertFileContains(t, modIndex, "<strong>server</strong>")
}

func TestHTMLGenerator_VersionReadmeFromTag(t *testing.T) {
	// Verify that version pages read README from their specific tag, not the latest.
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

	modDir := filepath.Join(tmpDir, "mymod")
	os.MkdirAll(modDir, 0o755)
	os.WriteFile(filepath.Join(modDir, "main.tf"), []byte(`resource "null" "x" {}`), 0o644)
	os.WriteFile(filepath.Join(modDir, "README.md"), []byte("# Version ONE readme"), 0o644)
	run("add", ".")
	run("commit", "-m", "v1")
	run("tag", "-a", "mymod-1.0.0", "-m", "v1.0.0")

	// Update README and tag v2
	os.WriteFile(filepath.Join(modDir, "README.md"), []byte("# Version TWO readme"), 0o644)
	run("add", ".")
	run("commit", "-m", "v2")
	run("tag", "-a", "mymod-2.0.0", "-m", "v2.0.0")

	gitRunner := git.NewRunner(tmpDir)
	outputDir := t.TempDir()
	gen := NewHTMLGenerator(gitRunner, outputDir, "index.html")
	gen.ReadmeReader = GitReadmeReader(gitRunner)

	grouped := map[string][]module.TagInfo{
		"mymod": {
			{Tag: "mymod-2.0.0", ModulePath: "mymod", Version: semver.MustParse("2.0.0")},
			{Tag: "mymod-1.0.0", ModulePath: "mymod", Version: semver.MustParse("1.0.0")},
		},
	}

	if err := gen.GenerateAll(grouped); err != nil {
		t.Fatal(err)
	}

	// v1 version page should have "Version ONE"
	v1Index := filepath.Join(outputDir, "mymod", "1.0.0", "index.html")
	assertFileContains(t, v1Index, "Version ONE")
	assertFileNotContains(t, v1Index, "Version TWO")

	// v2 version page should have "Version TWO"
	v2Index := filepath.Join(outputDir, "mymod", "2.0.0", "index.html")
	assertFileContains(t, v2Index, "Version TWO")
	assertFileNotContains(t, v2Index, "Version ONE")

	// Module index (latest) should have "Version TWO"
	modIndex := filepath.Join(outputDir, "mymod", "index.html")
	assertFileContains(t, modIndex, "Version TWO")
}

func TestHTMLGenerator_DevVersionIncluded(t *testing.T) {
	repoPath, gitRunner := setupHTMLTestRepo(t)
	outputDir := t.TempDir()
	gen := NewHTMLGenerator(gitRunner, outputDir, "index.html")
	gen.ReadmeReader = FilesystemReadmeReader(repoPath)

	// Simulate what publish --dev does: real tags + dev entry
	grouped := map[string][]module.TagInfo{
		"hetzner/server": {
			{Tag: "hetzner/server-1.1.0", ModulePath: "hetzner/server", Version: semver.MustParse("1.1.0")},
			{Tag: "hetzner/server-1.0.0", ModulePath: "hetzner/server", Version: semver.MustParse("1.0.0")},
			{Tag: "hetzner/server-0.0.0-dev", ModulePath: "hetzner/server", Version: semver.MustParse("0.0.0-dev")},
		},
	}

	if err := gen.GenerateAll(grouped); err != nil {
		t.Fatal(err)
	}

	// Root index should list the module
	rootIndex := filepath.Join(outputDir, "index.html")
	assertFileContains(t, rootIndex, "hetzner/server")
	assertFileContains(t, rootIndex, "3") // 3 versions

	// Module page should list dev version
	modIndex := filepath.Join(outputDir, "hetzner", "server", "index.html")
	assertFileContains(t, modIndex, "0.0.0-dev")
	assertFileContains(t, modIndex, "1.1.0")

	// Dev version page should exist
	devIndex := filepath.Join(outputDir, "hetzner", "server", "0.0.0-dev", "index.html")
	assertFileContains(t, devIndex, "0.0.0-dev")
	assertFileContains(t, devIndex, "hetzner/server")
}

func TestRenderMarkdown_Tables(t *testing.T) {
	input := "| Name | Type |\n|------|------|\n| foo | string |"
	result := string(renderMarkdown(input))
	if !strings.Contains(result, "<table>") {
		t.Errorf("expected <table> in rendered output, got:\n%s", result)
	}
	if !strings.Contains(result, "<td>foo</td>") {
		t.Errorf("expected table cell with 'foo', got:\n%s", result)
	}
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
