package git

import (
	"fmt"
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
// tag's commit. It writes to destPath.
func (r *Runner) ArchiveModule(tag, modulePath, destPath string) error {
	absDestPath, err := filepath.Abs(destPath)
	if err != nil {
		return fmt.Errorf("resolving dest path: %w", err)
	}
	// git archive produces a tar.gz of the subtree at the tag
	_, err = r.run("archive",
		"--format=tar.gz",
		"--output="+absDestPath,
		tag,
		"--",
		modulePath,
	)
	return err
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
