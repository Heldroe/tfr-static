package cmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// newTestRepo creates a git repo with two modules and assorted tags.
// Returns the repo path. The repo is cleaned up via t.TempDir.
func newTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
		}
	}

	run("init", "-b", "main")

	// Module: hetzner/server
	srv := filepath.Join(dir, "hetzner", "server")
	if err := os.MkdirAll(srv, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srv, "main.tf"), []byte(`resource "hetzner_server" "this" {}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Module: aws/ec2/security-group
	sg := filepath.Join(dir, "aws", "ec2", "security-group")
	if err := os.MkdirAll(sg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sg, "main.tf"), []byte(`resource "aws_security_group" "this" {}`), 0o644); err != nil {
		t.Fatal(err)
	}

	run("add", ".")
	run("commit", "-m", "initial")

	run("tag", "-a", "hetzner/server-0.1.0", "-m", "v0.1.0")
	run("tag", "-a", "hetzner/server-1.0.0", "-m", "v1.0.0")
	run("tag", "-a", "aws/ec2/security-group-0.0.1", "-m", "v0.0.1")

	return dir
}

// resetGlobals restores package-level command state between tests. Cobra flag
// globals live in this package, so tests must clear them (including each
// flag's Changed state) to avoid leaking state into the next invocation.
func resetGlobals() {
	publishTag = ""
	publishModule = ""
	publishAll = false
	publishDev = false
	dryRun = false

	serveAddr = "localhost:8080"
	serveDev = false

	pendingOnly = false

	cfg.BaseURL = ""
	cfg.OutputDir = ""
	cfg.RepoPath = ""
	cfg.MainBranch = ""
	cfg.ModulesPath = ""
	cfg.HTML = false
	cfg.HTMLIndex = ""
	cfg.HTMLBase = ""
	cfg.Gzip = false
	cfg.TerraformDocs = false
	cfg.InvalidationFile = ""
	cfg.InvalidationFormat = ""
	cfg.InvalidationFullURL = false
	cfg.InvalidationBaseURL = ""
	cfg.InvalidationURLEncode = false
	cfg.InvalidationDirs = false

	clearCmd(rootCmd)
	for _, c := range rootCmd.Commands() {
		clearCmd(c)
	}
}

func clearCmd(c *cobra.Command) {
	reset := func(f *pflag.Flag) { f.Changed = false }
	c.Flags().VisitAll(reset)
	c.PersistentFlags().VisitAll(reset)
	// Cobra stores the ctx from ExecuteContext on each command it executes and
	// only overwrites it if nil (see command.go:1113). Without this, a cancelled
	// ctx from a previous test run would leak into the next ExecuteContext call.
	c.SetContext(nil)
}

// runRoot invokes rootCmd with the given args and returns captured stdout
// and error. SilenceErrors is set so usage text doesn't clutter the output.
// NOTE: cobra routes user-facing Printf from RunE through the package's fmt.*
// calls (stdout), not cmd.OutOrStdout(), so we also capture os.Stdout.
func runRoot(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	resetGlobals()

	var outBuf, errBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&errBuf)
	rootCmd.SilenceErrors = true
	rootCmd.SetArgs(args)

	// Capture os.Stdout for commands that use fmt.Println directly.
	origStdout := os.Stdout
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatal(pipeErr)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = b.ReadFrom(r)
		done <- b.String()
	}()

	err = rootCmd.ExecuteContext(context.Background())

	w.Close()
	os.Stdout = origStdout
	captured := <-done

	return captured + outBuf.String(), err
}
