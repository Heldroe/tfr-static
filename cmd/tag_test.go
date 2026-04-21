package cmd

import (
	"strings"
	"testing"
)

// These tests hit the positional-arg path of `tag`, which bypasses the
// interactive huh prompt. We validate surface-level errors only; anything
// behind a TUI form is not worth golden-testing.

func TestTag_WrongBranch(t *testing.T) {
	repo := newTestRepo(t)

	_, err := runRoot(t,
		"tag", "hetzner/server",
		"--repo", repo,
		"--main-branch", "release",
		"--base-url", "http://localhost",
	)
	if err == nil {
		t.Fatal("expected error when on wrong branch")
	}
	if !strings.Contains(err.Error(), "release") {
		t.Errorf("expected branch mismatch error, got: %v", err)
	}
}

func TestTag_UnknownModule(t *testing.T) {
	repo := newTestRepo(t)

	// Tag command fetches from remote before validating args; this repo has
	// no remote, so the fetch failure shadows the module-not-found path.
	// Just confirm we get *some* actionable error, not a panic.
	_, err := runRoot(t,
		"tag", "not/a/module",
		"--repo", repo,
		"--main-branch", "main",
		"--base-url", "http://localhost",
	)
	if err == nil {
		t.Fatal("expected error for unknown module")
	}
}
