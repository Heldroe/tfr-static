package git

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNotFound is returned when a tag, path, or object doesn't exist in the
// repository. Callers can use errors.Is(err, git.ErrNotFound) to distinguish
// "missing" from other failures (bad repo, network errors, etc).
var ErrNotFound = errors.New("git: not found")

// notFoundSignals are stderr substrings that git prints when asked about an
// object that doesn't exist. We still require exit code 128 as a precondition.
var notFoundSignals = []string{
	"not a valid object name",
	"unknown revision",
	"does not exist",
	"pathspec",
	"needed a single revision",
}

// cEnv returns an environment for exec'ing git with stable, locale-independent
// output so notFoundSignals matching works regardless of the user's LANG.
func cEnv() []string {
	env := os.Environ()
	return append(env, "LC_ALL=C", "LANG=C")
}

// isGitNotFound returns true if err looks like a "missing object/path" result
// from git (exit code 128 with one of the known stderr signatures).
func isGitNotFound(exitErr *exec.ExitError, output string) bool {
	if exitErr == nil || exitErr.ExitCode() != 128 {
		return false
	}
	lower := strings.ToLower(output)
	for _, s := range notFoundSignals {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

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
func (r *Runner) TopLevel(ctx context.Context) (string, error) {
	return r.run(ctx, "rev-parse", "--show-toplevel")
}

func (r *Runner) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.RepoPath
	cmd.Env = cEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && isGitNotFound(exitErr, trimmed) {
			return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), trimmed, ErrNotFound)
		}
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), trimmed, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ListTags returns all tags in the repository.
func (r *Runner) ListTags(ctx context.Context) ([]string, error) {
	out, err := r.run(ctx, "tag", "--list")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// ListTagsWithPrefix returns tags matching a given prefix.
func (r *Runner) ListTagsWithPrefix(ctx context.Context, prefix string) ([]string, error) {
	out, err := r.run(ctx, "tag", "--list", prefix+"*")
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
func (r *Runner) ArchiveModule(ctx context.Context, tag, modulePath, destPath string) error {
	absDestPath, err := filepath.Abs(destPath)
	if err != nil {
		return fmt.Errorf("resolving dest path: %w", err)
	}
	// Use tag:path syntax so git treats the subtree as the archive root,
	// producing flat files instead of preserving the directory prefix.
	_, err = r.run(ctx,
		"archive",
		"--format=tar.gz",
		"--output="+absDestPath,
		tag+":"+modulePath,
	)
	return err
}

// ExtractModuleAtTag extracts a module directory at a given tag into a
// temporary directory and returns its path. The caller must call the returned
// cleanup function when done. Entry paths are validated to prevent traversal
// outside tmpDir.
func (r *Runner) ExtractModuleAtTag(ctx context.Context, tag, modulePath string) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "tfr-docs-*")
	if err != nil {
		return "", nil, fmt.Errorf("creating temp dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	cmd := exec.CommandContext(ctx, "git", "archive", "--format=tar", tag+":"+modulePath)
	cmd.Dir = r.RepoPath
	cmd.Env = cEnv()

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("creating git stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("starting git archive: %w", err)
	}

	if err := extractTar(stdout, tmpDir); err != nil {
		// Drain/kill git to avoid leaving a zombie before surfacing the error.
		_ = cmd.Wait()
		cleanup()
		return "", nil, fmt.Errorf("extracting archive %s:%s: %w", tag, modulePath, err)
	}
	if err := cmd.Wait(); err != nil {
		cleanup()
		trimmed := strings.TrimSpace(stderr.String())
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && isGitNotFound(exitErr, trimmed) {
			return "", nil, fmt.Errorf("git archive %s:%s: %s: %w", tag, modulePath, trimmed, ErrNotFound)
		}
		return "", nil, fmt.Errorf("git archive %s:%s: %s: %w", tag, modulePath, trimmed, err)
	}

	return tmpDir, cleanup, nil
}

// extractTar reads a tar stream and writes files under destDir. Entries whose
// resolved path escapes destDir are rejected.
func extractTar(r io.Reader, destDir string) error {
	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return fmt.Errorf("resolving dest: %w", err)
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading tar header: %w", err)
		}

		target := filepath.Join(absDest, filepath.FromSlash(hdr.Name))
		rel, err := filepath.Rel(absDest, target)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("tar entry %q escapes destination", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("mkdir parent for %s: %w", target, err)
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode&0o777))
			if err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("copy %s: %w", target, err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("close %s: %w", target, err)
			}
		case tar.TypeSymlink, tar.TypeLink:
			// Skip: terraform-docs doesn't need link types and they are a
			// traversal risk.
			continue
		default:
			// Ignore unknown entry types (pax headers etc).
		}
	}
}

// CurrentBranch returns the current branch name.
func (r *Runner) CurrentBranch(ctx context.Context) (string, error) {
	return r.run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
}

// IsClean returns true if the working tree has no uncommitted changes.
func (r *Runner) IsClean(ctx context.Context) (bool, error) {
	out, err := r.run(ctx, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out == "", nil
}

// Fetch fetches from the default remote.
func (r *Runner) Fetch(ctx context.Context) error {
	_, err := r.run(ctx, "fetch")
	return err
}

// IsUpToDate checks if the local branch is up to date with its remote tracking branch.
func (r *Runner) IsUpToDate(ctx context.Context, branch string) (bool, error) {
	local, err := r.run(ctx, "rev-parse", branch)
	if err != nil {
		return false, err
	}
	remote, err := r.run(ctx, "rev-parse", "origin/"+branch)
	if err != nil {
		// No remote tracking branch — treat as up to date
		return true, nil
	}
	return local == remote, nil
}

// CreateTag creates an annotated tag.
func (r *Runner) CreateTag(ctx context.Context, tag, message string) error {
	_, err := r.run(ctx, "tag", "-a", tag, "-m", message)
	return err
}

// PushTag pushes a single tag to origin.
func (r *Runner) PushTag(ctx context.Context, tag string) error {
	_, err := r.run(ctx, "push", "origin", tag)
	return err
}

// TagExists checks if a tag already exists. Returns (false, nil) only when git
// confirms the tag is absent; other errors (bad repo, I/O) are propagated.
func (r *Runner) TagExists(ctx context.Context, tag string) (bool, error) {
	_, err := r.run(ctx, "rev-parse", "--verify", "refs/tags/"+tag)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return false, err
}

// PathExistsAtTag checks if a path exists in the tree at the given tag.
func (r *Runner) PathExistsAtTag(ctx context.Context, tag, path string) (bool, error) {
	_, err := r.run(ctx, "cat-file", "-e", tag+":"+path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return false, err
}

// ShowFileAtTag returns the contents of a file at a given tag. Returns
// ("", nil) only when git confirms the file is missing; other errors are
// propagated so callers can distinguish "missing" from "broken".
func (r *Runner) ShowFileAtTag(ctx context.Context, tag, path string) (string, error) {
	out, err := r.run(ctx, "show", tag+":"+path)
	if err == nil {
		return out, nil
	}
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	return "", err
}

// ListFilesAtTag lists files under a path at a given tag.
func (r *Runner) ListFilesAtTag(ctx context.Context, tag, path string) ([]string, error) {
	prefix := path
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	out, err := r.run(ctx, "ls-tree", "--name-only", tag, prefix)
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
func (r *Runner) ModuleHasChanges(ctx context.Context, tag, modulePath string) (bool, error) {
	out, err := r.run(ctx, "diff", "--name-only", tag, "HEAD", "--", modulePath)
	if err != nil {
		return false, err
	}
	return out != "", nil
}
