package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Runner executes git commands against a repository.
type Runner struct {
	RepoPath string
}

// NewRunner creates a Runner for the given repository path.
func NewRunner(repoPath string) *Runner {
	return &Runner{RepoPath: repoPath}
}

// TopLevel returns the absolute path to the repository root.
// This resolves the actual git worktree root, which matters when RepoPath
// is "." or a subdirectory.
func (r *Runner) TopLevel() (string, error) {
	return r.run("rev-parse", "--show-toplevel")
}

func (r *Runner) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.RepoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ListTags returns all tags in the repository.
func (r *Runner) ListTags() ([]string, error) {
	out, err := r.run("tag", "--list")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// ListTagsWithPrefix returns tags matching a given prefix.
func (r *Runner) ListTagsWithPrefix(prefix string) ([]string, error) {
	out, err := r.run("tag", "--list", prefix+"*")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// ArchiveModule creates a tar.gz archive of the module directory at the given
// tag's commit. It writes to destPath. Files are placed at the root of the
// archive (no directory prefix).
func (r *Runner) ArchiveModule(tag, modulePath, destPath string) error {
	absDestPath, err := filepath.Abs(destPath)
	if err != nil {
		return fmt.Errorf("resolving dest path: %w", err)
	}
	// Use tag:path syntax so git treats the subtree as the archive root,
	// producing flat files instead of preserving the directory prefix.
	_, err = r.run("archive",
		"--format=tar.gz",
		"--output="+absDestPath,
		tag+":"+modulePath,
	)
	return err
}

// ExtractModuleAtTag extracts a module directory at a given tag into a
// temporary directory and returns its path. The caller must call the returned
// cleanup function when done.
func (r *Runner) ExtractModuleAtTag(tag, modulePath string) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "tfr-docs-*")
	if err != nil {
		return "", nil, fmt.Errorf("creating temp dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("creating extract dir: %w", err)
	}

	gitCmd := exec.Command("git", "archive", tag+":"+modulePath)
	gitCmd.Dir = r.RepoPath

	tarCmd := exec.Command("tar", "-xf", "-", "-C", tmpDir)

	pipe, err := gitCmd.StdoutPipe()
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("creating pipe: %w", err)
	}
	tarCmd.Stdin = pipe

	var tarErr bytes.Buffer
	tarCmd.Stderr = &tarErr

	if err := tarCmd.Start(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("starting tar: %w", err)
	}
	if err := gitCmd.Run(); err != nil {
		tarCmd.Wait()
		cleanup()
		return "", nil, fmt.Errorf("git archive %s:%s: %w", tag, modulePath, err)
	}
	if err := tarCmd.Wait(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("extracting archive: %s: %w", strings.TrimSpace(tarErr.String()), err)
	}

	return tmpDir, cleanup, nil
}

// CurrentBranch returns the current branch name.
func (r *Runner) CurrentBranch() (string, error) {
	return r.run("rev-parse", "--abbrev-ref", "HEAD")
}

// IsClean returns true if the working tree has no uncommitted changes.
func (r *Runner) IsClean() (bool, error) {
	out, err := r.run("status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out == "", nil
}

// Fetch fetches from the default remote.
func (r *Runner) Fetch() error {
	_, err := r.run("fetch")
	return err
}

// IsUpToDate checks if the local branch is up to date with its remote tracking branch.
func (r *Runner) IsUpToDate(branch string) (bool, error) {
	local, err := r.run("rev-parse", branch)
	if err != nil {
		return false, err
	}
	remote, err := r.run("rev-parse", "origin/"+branch)
	if err != nil {
		// No remote tracking branch — treat as up to date
		return true, nil
	}
	return local == remote, nil
}

// CreateTag creates an annotated tag.
func (r *Runner) CreateTag(tag, message string) error {
	_, err := r.run("tag", "-a", tag, "-m", message)
	return err
}

// PushTag pushes a single tag to origin.
func (r *Runner) PushTag(tag string) error {
	_, err := r.run("push", "origin", tag)
	return err
}

// TagExists checks if a tag already exists.
func (r *Runner) TagExists(tag string) (bool, error) {
	_, err := r.run("rev-parse", "--verify", "refs/tags/"+tag)
	if err != nil {
		if strings.Contains(err.Error(), "fatal") {
			return false, nil
		}
		return false, nil
	}
	return true, nil
}

// PathExistsAtTag checks if a path exists in the tree at the given tag.
func (r *Runner) PathExistsAtTag(tag, path string) (bool, error) {
	_, err := r.run("cat-file", "-e", tag+":"+path)
	if err != nil {
		return false, nil
	}
	return true, nil
}

// ShowFileAtTag returns the contents of a file at a given tag.
// Returns empty string and no error if the file does not exist.
func (r *Runner) ShowFileAtTag(tag, path string) (string, error) {
	out, err := r.run("show", tag+":"+path)
	if err != nil {
		return "", nil
	}
	return out, nil
}

// ListFilesAtTag lists files under a path at a given tag.
func (r *Runner) ListFilesAtTag(tag, path string) ([]string, error) {
	prefix := path
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	out, err := r.run("ls-tree", "--name-only", tag, prefix)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// ModuleHasChanges reports whether the given module path has changes
// between the specified tag and HEAD.
func (r *Runner) ModuleHasChanges(tag, modulePath string) (bool, error) {
	out, err := r.run("diff", "--name-only", tag, "HEAD", "--", modulePath)
	if err != nil {
		return false, err
	}
	return out != "", nil
}
