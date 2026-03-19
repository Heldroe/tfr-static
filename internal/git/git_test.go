package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func setupRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
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

	// Create some module files
	modDir := filepath.Join(dir, "mymod", "sub")
	os.MkdirAll(modDir, 0o755)
	os.WriteFile(filepath.Join(modDir, "main.tf"), []byte("resource {}"), 0o644)

	run("add", ".")
	run("commit", "-m", "init")
	run("tag", "-a", "mymod/sub-1.0.0", "-m", "v1.0.0")
	run("tag", "-a", "mymod/sub-0.1.0", "-m", "v0.1.0")
	run("tag", "unrelated-tag") // lightweight tag, no annotation

	return dir
}

func TestListTags(t *testing.T) {
	dir := setupRepo(t)
	r := NewRunner(dir)

	tags, err := r.ListTags()
	if err != nil {
		t.Fatal(err)
	}

	if len(tags) != 3 {
		t.Fatalf("expected 3 tags, got %d: %v", len(tags), tags)
	}
}

func TestListTagsWithPrefix(t *testing.T) {
	dir := setupRepo(t)
	r := NewRunner(dir)

	tags, err := r.ListTagsWithPrefix("mymod/sub-")
	if err != nil {
		t.Fatal(err)
	}

	if len(tags) != 2 {
		t.Fatalf("expected 2 tags with prefix, got %d: %v", len(tags), tags)
	}
}

func TestCurrentBranch(t *testing.T) {
	dir := setupRepo(t)
	r := NewRunner(dir)

	branch, err := r.CurrentBranch()
	if err != nil {
		t.Fatal(err)
	}
	if branch != "main" {
		t.Errorf("expected main, got %q", branch)
	}
}

func TestIsClean(t *testing.T) {
	dir := setupRepo(t)
	r := NewRunner(dir)

	clean, err := r.IsClean()
	if err != nil {
		t.Fatal(err)
	}
	if !clean {
		t.Error("expected clean repo")
	}

	// Dirty the repo
	os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("dirty"), 0o644)

	clean, err = r.IsClean()
	if err != nil {
		t.Fatal(err)
	}
	if clean {
		t.Error("expected dirty repo")
	}
}

func TestTagExists(t *testing.T) {
	dir := setupRepo(t)
	r := NewRunner(dir)

	exists, err := r.TagExists("mymod/sub-1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("expected tag to exist")
	}

	exists, err = r.TagExists("nonexistent-99.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("expected tag to not exist")
	}
}

func TestCreateTag(t *testing.T) {
	dir := setupRepo(t)
	r := NewRunner(dir)

	err := r.CreateTag("mymod/sub-2.0.0", "new release")
	if err != nil {
		t.Fatal(err)
	}

	exists, _ := r.TagExists("mymod/sub-2.0.0")
	if !exists {
		t.Error("created tag not found")
	}
}

func TestPathExistsAtTag(t *testing.T) {
	dir := setupRepo(t)
	r := NewRunner(dir)

	exists, err := r.PathExistsAtTag("mymod/sub-1.0.0", "mymod/sub")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("expected path to exist at tag")
	}

	exists, err = r.PathExistsAtTag("mymod/sub-1.0.0", "nonexistent/path")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("expected path to not exist at tag")
	}
}

func TestArchiveModule(t *testing.T) {
	dir := setupRepo(t)
	r := NewRunner(dir)

	outputDir := t.TempDir()
	destPath := filepath.Join(outputDir, "archive.tar.gz")

	err := r.ArchiveModule("mymod/sub-1.0.0", "mymod/sub", destPath)
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Error("archive file is empty")
	}
}

func TestArchiveModuleDeletedInCurrentTree(t *testing.T) {
	dir := setupRepo(t)

	// Delete the module from the working tree after tagging
	os.RemoveAll(filepath.Join(dir, "mymod"))

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		cmd.CombinedOutput()
	}
	run("add", ".")
	run("commit", "-m", "delete module")

	r := NewRunner(dir)
	outputDir := t.TempDir()
	destPath := filepath.Join(outputDir, "archive.tar.gz")

	// Should still work because we archive from the tag's tree
	err := r.ArchiveModule("mymod/sub-1.0.0", "mymod/sub", destPath)
	if err != nil {
		t.Fatalf("archiving deleted module from old tag should work: %v", err)
	}

	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Error("archive file is empty")
	}
}
