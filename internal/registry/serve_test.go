package registry

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Heldroe/tfr-static/internal/git"
)

func setupDevTestRepo(t *testing.T) (repoPath string, gitRunner *git.Runner) {
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

	// Create modules
	for _, mod := range []struct {
		path    string
		content string
	}{
		{"hetzner/server", `resource "hetzner_server" "this" { name = "test" }`},
		{"aws/ec2/security-group", `resource "aws_security_group" "this" {}`},
	} {
		dir := filepath.Join(tmpDir, mod.path)
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "main.tf"), []byte(mod.content), 0o644)
	}

	run("add", ".")
	run("commit", "-m", "initial")
	run("tag", "-a", "hetzner/server-1.0.0", "-m", "v1.0.0")
	run("tag", "-a", "hetzner/server-1.1.0", "-m", "v1.1.0")

	return tmpDir, git.NewRunner(tmpDir)
}

func TestDevServer_ServiceDiscovery(t *testing.T) {
	repoPath, gitRunner := setupDevTestRepo(t)
	dev := NewDevServer(gitRunner, repoPath, "/")
	srv := httptest.NewServer(dev.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/.well-known/terraform.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var sd ServiceDiscovery
	json.NewDecoder(resp.Body).Decode(&sd)
	if sd.ModulesV1 != "/" {
		t.Errorf("modules.v1 = %q", sd.ModulesV1)
	}
}

func TestDevServer_ServiceDiscovery_CustomPath(t *testing.T) {
	repoPath, gitRunner := setupDevTestRepo(t)
	dev := NewDevServer(gitRunner, repoPath, "/v1/modules")
	srv := httptest.NewServer(dev.Handler())
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/.well-known/terraform.json")
	defer resp.Body.Close()

	var sd ServiceDiscovery
	json.NewDecoder(resp.Body).Decode(&sd)
	if sd.ModulesV1 != "/v1/modules/" {
		t.Errorf("modules.v1 = %q", sd.ModulesV1)
	}
}

func TestDevServer_Versions(t *testing.T) {
	repoPath, gitRunner := setupDevTestRepo(t)
	dev := NewDevServer(gitRunner, repoPath, "/")
	srv := httptest.NewServer(dev.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/hetzner/server/versions.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var mv ModuleVersions
	json.NewDecoder(resp.Body).Decode(&mv)

	if len(mv.Modules) != 1 {
		t.Fatalf("expected 1 module, got %d", len(mv.Modules))
	}

	versions := mv.Modules[0].Versions
	// Should contain real tags (1.0.0, 1.1.0) plus dev versions
	if len(versions) < 3 {
		t.Fatalf("expected at least 3 versions (2 real + dev), got %d", len(versions))
	}

	versionSet := make(map[string]bool)
	for _, v := range versions {
		versionSet[v.Version] = true
	}
	for _, want := range []string{"1.0.0", "1.1.0", "0.0.0-dev", "99999.0.0-dev"} {
		if !versionSet[want] {
			t.Errorf("missing version %q in response", want)
		}
	}
}

func TestDevServer_Versions_NoTags(t *testing.T) {
	repoPath, gitRunner := setupDevTestRepo(t)
	dev := NewDevServer(gitRunner, repoPath, "/")
	srv := httptest.NewServer(dev.Handler())
	defer srv.Close()

	// aws/ec2/security-group has no tags
	resp, _ := http.Get(srv.URL + "/aws/ec2/security-group/versions.json")
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var mv ModuleVersions
	json.NewDecoder(resp.Body).Decode(&mv)

	versions := mv.Modules[0].Versions
	if len(versions) != 2 {
		t.Fatalf("expected 2 dev versions, got %d", len(versions))
	}
}

func TestDevServer_Versions_NonExistentModule(t *testing.T) {
	repoPath, gitRunner := setupDevTestRepo(t)
	dev := NewDevServer(gitRunner, repoPath, "/")
	srv := httptest.NewServer(dev.Handler())
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/nonexistent/module/versions.json")
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestDevServer_Download(t *testing.T) {
	repoPath, gitRunner := setupDevTestRepo(t)
	dev := NewDevServer(gitRunner, repoPath, "/")
	srv := httptest.NewServer(dev.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/hetzner/server/1.0.0/download")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	content := string(body)

	if !strings.Contains(content, `<meta name="terraform-get"`) {
		t.Error("missing terraform-get meta tag")
	}
	if !strings.Contains(content, "hetzner-server-1.0.0.tar.gz") {
		t.Errorf("missing archive reference in:\n%s", content)
	}
}

func TestDevServer_Download_AnyVersion(t *testing.T) {
	repoPath, gitRunner := setupDevTestRepo(t)
	dev := NewDevServer(gitRunner, repoPath, "/")
	srv := httptest.NewServer(dev.Handler())
	defer srv.Close()

	// Should work even for a version that doesn't exist as a tag
	resp, _ := http.Get(srv.URL + "/hetzner/server/99.0.0/download")
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 for any version, got %d", resp.StatusCode)
	}
}

func TestDevServer_Archive(t *testing.T) {
	repoPath, gitRunner := setupDevTestRepo(t)
	dev := NewDevServer(gitRunner, repoPath, "/")
	srv := httptest.NewServer(dev.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/hetzner/server/1.0.0/hetzner-server-1.0.0.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	if ct := resp.Header.Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("Content-Type = %q", ct)
	}

	// Verify it's a valid tar.gz and contains the expected file
	gr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	foundTF := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar reader: %v", err)
		}
		if strings.HasSuffix(hdr.Name, "main.tf") {
			foundTF = true
		}
		// Files should be at the root, no prefix
		if strings.HasPrefix(hdr.Name, "module/") {
			t.Errorf("file %q should not have module/ prefix", hdr.Name)
		}
	}

	if !foundTF {
		t.Error("archive doesn't contain main.tf")
	}
}

func TestDevServer_Archive_ServesWorkingTree(t *testing.T) {
	repoPath, gitRunner := setupDevTestRepo(t)
	dev := NewDevServer(gitRunner, repoPath, "/")
	srv := httptest.NewServer(dev.Handler())
	defer srv.Close()

	// Modify a file in the working tree (uncommitted change)
	tfPath := filepath.Join(repoPath, "hetzner", "server", "main.tf")
	os.WriteFile(tfPath, []byte(`resource "hetzner_server" "this" { name = "modified" }`), 0o644)

	resp, _ := http.Get(srv.URL + "/hetzner/server/1.0.0/hetzner-server-1.0.0.tar.gz")
	defer resp.Body.Close()

	gr, _ := gzip.NewReader(resp.Body)
	defer gr.Close()
	tr := tar.NewReader(gr)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(hdr.Name, "main.tf") {
			content, _ := io.ReadAll(tr)
			if !strings.Contains(string(content), "modified") {
				t.Error("archive doesn't contain uncommitted change")
			}
			return
		}
	}
	t.Error("main.tf not found in archive")
}

func TestDevServer_Archive_NonExistentModule(t *testing.T) {
	repoPath, gitRunner := setupDevTestRepo(t)
	dev := NewDevServer(gitRunner, repoPath, "/")
	srv := httptest.NewServer(dev.Handler())
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/nonexistent/1.0.0/nonexistent-1.0.0.tar.gz")
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestDevServer_FullFlow(t *testing.T) {
	repoPath, gitRunner := setupDevTestRepo(t)
	dev := NewDevServer(gitRunner, repoPath, "/")
	srv := httptest.NewServer(dev.Handler())
	defer srv.Close()

	// 1. Service discovery
	resp, _ := http.Get(srv.URL + "/.well-known/terraform.json")
	var sd ServiceDiscovery
	json.NewDecoder(resp.Body).Decode(&sd)
	resp.Body.Close()

	// 2. List versions
	resp, _ = http.Get(srv.URL + sd.ModulesV1 + "hetzner/server/versions.json")
	var mv ModuleVersions
	json.NewDecoder(resp.Body).Decode(&mv)
	resp.Body.Close()

	if len(mv.Modules[0].Versions) == 0 {
		t.Fatal("no versions returned")
	}

	// Pick first real version
	version := mv.Modules[0].Versions[0].Version

	// 3. Get download page
	resp, _ = http.Get(srv.URL + sd.ModulesV1 + "hetzner/server/" + version + "/download")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), ".tar.gz") {
		t.Fatal("download page doesn't contain archive link")
	}

	// 4. Download archive
	archiveURL := extractTerraformGetURL(string(body))
	if archiveURL == "" {
		t.Fatal("could not extract terraform-get URL from download page")
	}

	resp, _ = http.Get(archiveURL)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("archive download status = %d", resp.StatusCode)
	}

	// Verify valid gzip
	gr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	gr.Close()
}

func extractTerraformGetURL(html string) string {
	const marker = `content="`
	idx := strings.Index(html, marker)
	if idx == -1 {
		return ""
	}
	start := idx + len(marker)
	end := strings.Index(html[start:], `"`)
	if end == -1 {
		return ""
	}
	return html[start : start+end]
}

func TestBuildArchiveFromWorkTree(t *testing.T) {
	dir := t.TempDir()
	modDir := filepath.Join(dir, "my", "module")
	os.MkdirAll(modDir, 0o755)
	os.WriteFile(filepath.Join(modDir, "main.tf"), []byte("resource {}"), 0o644)
	os.WriteFile(filepath.Join(modDir, "variables.tf"), []byte("variable {}"), 0o644)
	// Hidden file should be skipped
	os.WriteFile(filepath.Join(modDir, ".hidden"), []byte("secret"), 0o644)

	pr, pw := io.Pipe()
	go func() {
		err := buildArchiveFromWorkTree(dir, "my/module", pw)
		pw.CloseWithError(err)
	}()

	gr, _ := gzip.NewReader(pr)
	tr := tar.NewReader(gr)

	files := make(map[string]bool)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		files[hdr.Name] = true
	}

	if !files["main.tf"] {
		t.Error("missing main.tf")
	}
	if !files["variables.tf"] {
		t.Error("missing variables.tf")
	}
	if files[".hidden"] {
		t.Error(".hidden should be skipped")
	}
}
