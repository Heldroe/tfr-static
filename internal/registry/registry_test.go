package registry

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/Heldroe/tfr-static/internal/git"
	"github.com/Heldroe/tfr-static/internal/module"
)

// setupTestRepo creates a git repo with a module and tags it.
func setupTestRepo(t *testing.T) (repoPath string, cleanup func()) {
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

	// Create module: hetzner/server
	serverDir := filepath.Join(tmpDir, "hetzner", "server")
	os.MkdirAll(serverDir, 0o755)
	os.WriteFile(filepath.Join(serverDir, "main.tf"), []byte(`resource "hetzner_server" "this" {}`), 0o644)

	// Create module: aws/ec2/security-group (has dash in name)
	sgDir := filepath.Join(tmpDir, "aws", "ec2", "security-group")
	os.MkdirAll(sgDir, 0o755)
	os.WriteFile(filepath.Join(sgDir, "main.tf"), []byte(`resource "aws_security_group" "this" {}`), 0o644)

	// Create parent module: aws/ec2 (nested modules test)
	os.WriteFile(filepath.Join(tmpDir, "aws", "ec2", "main.tf"), []byte(`module "ec2" {}`), 0o644)

	run("add", ".")
	run("commit", "-m", "initial")

	// Create tags
	run("tag", "-a", "hetzner/server-0.1.0", "-m", "v0.1.0")
	run("tag", "-a", "hetzner/server-0.2.0", "-m", "v0.2.0")
	run("tag", "-a", "hetzner/server-1.0.0", "-m", "v1.0.0")
	run("tag", "-a", "aws/ec2/security-group-0.0.1", "-m", "v0.0.1")
	run("tag", "-a", "aws/ec2-1.0.0", "-m", "v1.0.0")

	return tmpDir, func() {}
}

func TestPublishVersion(t *testing.T) {
	repoPath, cleanup := setupTestRepo(t)
	defer cleanup()

	outputDir := t.TempDir()
	gitRunner := git.NewRunner(repoPath)
	pub := NewPublisher(gitRunner, outputDir, "https://registry.example.com", "/")

	tag := module.TagInfo{
		Tag:        "hetzner/server-1.0.0",
		ModulePath: "hetzner/server",
		Version:    semver.MustParse("1.0.0"),
	}

	if err := pub.PublishVersion(tag); err != nil {
		t.Fatalf("PublishVersion() error: %v", err)
	}

	// Check archive exists
	archivePath := filepath.Join(outputDir, "hetzner/server/1.0.0/module.tar.gz")
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		t.Error("archive file not created")
	}

	// Check download file exists and contains correct content
	downloadPath := filepath.Join(outputDir, "hetzner/server/1.0.0/download")
	data, err := os.ReadFile(downloadPath)
	if err != nil {
		t.Fatalf("reading download file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, `<meta name="terraform-get"`) {
		t.Error("download file missing terraform-get meta tag")
	}
	expectedURL := "https://registry.example.com/hetzner/server/1.0.0/module.tar.gz"
	if !strings.Contains(content, expectedURL) {
		t.Errorf("download file doesn't contain expected URL %q, got:\n%s", expectedURL, content)
	}
}

func TestPublishVersionDashInModuleName(t *testing.T) {
	repoPath, cleanup := setupTestRepo(t)
	defer cleanup()

	outputDir := t.TempDir()
	gitRunner := git.NewRunner(repoPath)
	pub := NewPublisher(gitRunner, outputDir, "https://registry.example.com", "/")

	tag := module.TagInfo{
		Tag:        "aws/ec2/security-group-0.0.1",
		ModulePath: "aws/ec2/security-group",
		Version:    semver.MustParse("0.0.1"),
	}

	if err := pub.PublishVersion(tag); err != nil {
		t.Fatalf("PublishVersion() error: %v", err)
	}

	archivePath := filepath.Join(outputDir, "aws/ec2/security-group/0.0.1/module.tar.gz")
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		t.Error("archive file not created for dash-named module")
	}
}

func TestPublishNestedModule(t *testing.T) {
	repoPath, cleanup := setupTestRepo(t)
	defer cleanup()

	outputDir := t.TempDir()
	gitRunner := git.NewRunner(repoPath)
	pub := NewPublisher(gitRunner, outputDir, "https://registry.example.com", "/")

	// Publish the parent module aws/ec2
	tag := module.TagInfo{
		Tag:        "aws/ec2-1.0.0",
		ModulePath: "aws/ec2",
		Version:    semver.MustParse("1.0.0"),
	}

	if err := pub.PublishVersion(tag); err != nil {
		t.Fatalf("PublishVersion() error: %v", err)
	}

	archivePath := filepath.Join(outputDir, "aws/ec2/1.0.0/module.tar.gz")
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		t.Error("archive file not created for nested parent module")
	}
}

func TestGenerateVersionsJSON(t *testing.T) {
	outputDir := t.TempDir()
	pub := NewPublisher(nil, outputDir, "https://registry.example.com", "/")

	versions := []*semver.Version{
		semver.MustParse("0.1.0"),
		semver.MustParse("1.0.0"),
		semver.MustParse("0.2.0"),
		semver.MustParse("1.11.0"),
		semver.MustParse("1.9.0"),
	}

	if err := pub.GenerateVersionsJSON("hetzner/server", versions); err != nil {
		t.Fatalf("GenerateVersionsJSON() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outputDir, "hetzner/server/versions.json"))
	if err != nil {
		t.Fatal(err)
	}

	var mv ModuleVersions
	if err := json.Unmarshal(data, &mv); err != nil {
		t.Fatalf("unmarshaling versions.json: %v", err)
	}

	if len(mv.Modules) != 1 {
		t.Fatalf("expected 1 module entry, got %d", len(mv.Modules))
	}

	versionList := mv.Modules[0].Versions
	if len(versionList) != 5 {
		t.Fatalf("expected 5 versions, got %d", len(versionList))
	}

	// Should be sorted descending
	expected := []string{"1.11.0", "1.9.0", "1.0.0", "0.2.0", "0.1.0"}
	for i, entry := range versionList {
		if entry.Version != expected[i] {
			t.Errorf("version[%d] = %q, want %q", i, entry.Version, expected[i])
		}
	}
}

func TestInvalidationPaths(t *testing.T) {
	paths := InvalidationPaths("hetzner/server", semver.MustParse("1.0.0"))
	if len(paths) != 2 {
		t.Fatalf("expected 2 invalidation paths, got %d", len(paths))
	}
	if paths[0] != "/hetzner/server/versions.json" {
		t.Errorf("paths[0] = %q", paths[0])
	}
	if paths[1] != "/hetzner/server/1.0.0/download" {
		t.Errorf("paths[1] = %q", paths[1])
	}
}

func TestGenerateDownloadHTML(t *testing.T) {
	html := generateDownloadHTML("https://example.com/mod/1.0.0/mod-1.0.0.tar.gz")
	if !strings.Contains(html, `<meta name="terraform-get"`) {
		t.Error("missing terraform-get meta tag")
	}
	if !strings.Contains(html, "https://example.com/mod/1.0.0/mod-1.0.0.tar.gz") {
		t.Error("missing archive URL")
	}
}

func TestGenerateDownloadHTMLEscapesURL(t *testing.T) {
	html := generateDownloadHTML("https://example.com/mod?a=1&b=2")
	if !strings.Contains(html, "&amp;") {
		t.Error("URL not properly HTML-escaped")
	}
}

func TestArchiveName(t *testing.T) {
	tests := []struct {
		modulePath string
		version    string
	}{
		{"hetzner/server", "1.0.0"},
		{"aws/ec2/security-group", "0.0.1"},
		{"simple", "2.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.modulePath, func(t *testing.T) {
			got := archiveName(tt.modulePath, semver.MustParse(tt.version))
			if got != "module.tar.gz" {
				t.Errorf("archiveName(%q, %q) = %q, want %q", tt.modulePath, tt.version, got, "module.tar.gz")
			}
		})
	}
}

func TestDescriptiveArchiveName(t *testing.T) {
	tests := []struct {
		modulePath string
		version    string
		want       string
	}{
		{"hetzner/server", "1.0.0", "hetzner-server-1.0.0.tar.gz"},
		{"aws/ec2/security-group", "0.0.1", "aws-ec2-security-group-0.0.1.tar.gz"},
		{"simple", "2.0.0", "simple-2.0.0.tar.gz"},
	}

	for _, tt := range tests {
		t.Run(tt.modulePath, func(t *testing.T) {
			got := descriptiveArchiveName(tt.modulePath, semver.MustParse(tt.version))
			if got != tt.want {
				t.Errorf("descriptiveArchiveName(%q, %q) = %q, want %q", tt.modulePath, tt.version, got, tt.want)
			}
		})
	}
}

func TestEndToEndPublishAndVersions(t *testing.T) {
	repoPath, cleanup := setupTestRepo(t)
	defer cleanup()

	outputDir := t.TempDir()
	gitRunner := git.NewRunner(repoPath)
	pub := NewPublisher(gitRunner, outputDir, "https://cdn.example.com", "/")

	// Publish all versions of hetzner/server
	tags := []module.TagInfo{
		{Tag: "hetzner/server-0.1.0", ModulePath: "hetzner/server", Version: semver.MustParse("0.1.0")},
		{Tag: "hetzner/server-0.2.0", ModulePath: "hetzner/server", Version: semver.MustParse("0.2.0")},
		{Tag: "hetzner/server-1.0.0", ModulePath: "hetzner/server", Version: semver.MustParse("1.0.0")},
	}

	var versions []*semver.Version
	for _, tag := range tags {
		if err := pub.PublishVersion(tag); err != nil {
			t.Fatalf("PublishVersion(%s) error: %v", tag.Tag, err)
		}
		versions = append(versions, tag.Version)
	}

	if err := pub.GenerateVersionsJSON("hetzner/server", versions); err != nil {
		t.Fatal(err)
	}

	// Verify all 3 version directories exist
	for _, v := range []string{"0.1.0", "0.2.0", "1.0.0"} {
		dir := filepath.Join(outputDir, "hetzner/server", v)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Errorf("version directory %q not created", dir)
		}
	}

	// Verify versions.json
	data, err := os.ReadFile(filepath.Join(outputDir, "hetzner/server/versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mv ModuleVersions
	json.Unmarshal(data, &mv)
	if len(mv.Modules[0].Versions) != 3 {
		t.Errorf("expected 3 versions in versions.json, got %d", len(mv.Modules[0].Versions))
	}
}

func TestGenerateServiceDiscovery_DefaultPath(t *testing.T) {
	outputDir := t.TempDir()
	pub := NewPublisher(nil, outputDir, "https://registry.example.com", "/")

	if err := pub.GenerateServiceDiscovery(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(outputDir, ".well-known", "terraform.json"))
	if err != nil {
		t.Fatal(err)
	}

	var sd ServiceDiscovery
	if err := json.Unmarshal(data, &sd); err != nil {
		t.Fatalf("unmarshaling terraform.json: %v", err)
	}

	if sd.ModulesV1 != "/" {
		t.Errorf("modules.v1 = %q, want %q", sd.ModulesV1, "/")
	}
}

func TestGenerateServiceDiscovery_CustomPath(t *testing.T) {
	outputDir := t.TempDir()
	pub := NewPublisher(nil, outputDir, "https://registry.example.com", "/v1/modules")

	if err := pub.GenerateServiceDiscovery(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(outputDir, ".well-known", "terraform.json"))
	if err != nil {
		t.Fatal(err)
	}

	var sd ServiceDiscovery
	json.Unmarshal(data, &sd)

	if sd.ModulesV1 != "/v1/modules/" {
		t.Errorf("modules.v1 = %q, want %q", sd.ModulesV1, "/v1/modules/")
	}
}

func TestPublishVersionFromWorkTree(t *testing.T) {
	repoPath, cleanup := setupTestRepo(t)
	defer cleanup()

	outputDir := t.TempDir()
	pub := NewPublisher(nil, outputDir, "https://registry.example.com", "/")

	version := semver.MustParse("0.0.0-dev")
	if err := pub.PublishVersionFromWorkTree(repoPath, "hetzner/server", version); err != nil {
		t.Fatalf("PublishVersionFromWorkTree() error: %v", err)
	}

	// Check archive exists
	archivePath := filepath.Join(outputDir, "hetzner/server/0.0.0-dev/module.tar.gz")
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		t.Error("archive file not created")
	}

	// Check download file
	downloadPath := filepath.Join(outputDir, "hetzner/server/0.0.0-dev/download")
	data, err := os.ReadFile(downloadPath)
	if err != nil {
		t.Fatalf("reading download file: %v", err)
	}
	if !strings.Contains(string(data), "0.0.0-dev") {
		t.Error("download file should reference 0.0.0-dev version")
	}
}

func TestModulesPathNormalization(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/", "/"},
		{"", "/"},
		{"/v1/modules", "/v1/modules/"},
		{"/v1/modules/", "/v1/modules/"},
		{"v1/modules", "/v1/modules/"},
		{"v1/modules/", "/v1/modules/"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			pub := NewPublisher(nil, t.TempDir(), "https://example.com", tt.input)
			if pub.ModulesPath != tt.want {
				t.Errorf("ModulesPath = %q, want %q", pub.ModulesPath, tt.want)
			}
		})
	}
}
