package cmd

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// startServe runs `serve` on a random free port in the background, waits for
// the listener to accept, and returns the address plus a shutdown func.
func startServe(t *testing.T, args ...string) (addr string, shutdown func()) {
	t.Helper()
	resetGlobals()

	// Bind a free port up front, then close it so serve can claim it.
	// Small race window, but avoids flakiness of "retry until up".
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	addr = "127.0.0.1:" + strconv.Itoa(port)

	args = append(args, "--addr", addr)

	rootCmd.SetArgs(append([]string{"serve"}, args...))
	rootCmd.SilenceErrors = true
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- rootCmd.ExecuteContext(ctx)
	}()

	// Wait for the server to accept connections.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	return addr, func() {
		cancel()
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			t.Error("serve did not shut down within timeout")
		}
	}
}

func TestServe_DevHTMLDisabled(t *testing.T) {
	repo := newTestRepo(t)

	addr, stop := startServe(t,
		"--dev",
		"--html=false",
		"--repo", repo,
		"--base-url", "http://localhost",
	)
	defer stop()

	// When --html=false, requests for HTML pages on an existing module should 404.
	resp, err := http.Get("http://" + addr + "/hetzner/server/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 with html disabled, got %d", resp.StatusCode)
	}
}

func TestServe_DevHTMLDefault(t *testing.T) {
	repo := newTestRepo(t)

	addr, stop := startServe(t,
		"--dev",
		"--repo", repo,
		"--base-url", "http://localhost",
	)
	defer stop()

	// Default in dev mode is html=true; root page should render HTML.
	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 at root, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("expected HTML content-type, got %q", ct)
	}
}

func TestServe_StaticFiles(t *testing.T) {
	repo := newTestRepo(t)
	outDir := t.TempDir()

	// Publish first so there's something to serve.
	if _, err := runRoot(t,
		"publish", "--tag", "hetzner/server-1.0.0",
		"--repo", repo,
		"--output-dir", outDir,
		"--base-url", "https://registry.example.com",
	); err != nil {
		t.Fatalf("publish setup failed: %v", err)
	}

	addr, stop := startServe(t,
		"--repo", repo,
		"--output-dir", outDir,
		"--base-url", "https://registry.example.com",
	)
	defer stop()

	resp, err := http.Get("http://" + addr + "/hetzner/server/versions.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestServe_DevServiceDiscovery(t *testing.T) {
	repo := newTestRepo(t)

	addr, stop := startServe(t,
		"--dev",
		"--repo", repo,
		"--base-url", "http://localhost",
	)
	defer stop()

	resp, err := http.Get("http://" + addr + "/.well-known/terraform.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
