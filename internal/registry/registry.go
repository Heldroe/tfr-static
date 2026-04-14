package registry

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/Heldroe/tfr-static/internal/git"
	"github.com/Heldroe/tfr-static/internal/module"
)

// VersionEntry represents a single version in the versions.json file.
type VersionEntry struct {
	Version string `json:"version"`
}

// ModuleVersions is the response format for the versions endpoint.
type ModuleVersions struct {
	Modules []ModuleVersionList `json:"modules"`
}

// ModuleVersionList contains the versions for a single module.
type ModuleVersionList struct {
	Versions []VersionEntry `json:"versions"`
}

// ServiceDiscovery represents the .well-known/terraform.json file.
type ServiceDiscovery struct {
	ModulesV1 string `json:"modules.v1"`
}

// Publisher generates static registry files.
type Publisher struct {
	Git         *git.Runner
	OutputDir   string
	BaseURL     string
	ModulesPath string
}

// NewPublisher creates a new Publisher.
func NewPublisher(gitRunner *git.Runner, outputDir, baseURL, modulesPath string) *Publisher {
	// Ensure modules path has leading and trailing slashes
	if modulesPath == "" {
		modulesPath = "/"
	}
	if !strings.HasPrefix(modulesPath, "/") {
		modulesPath = "/" + modulesPath
	}
	if !strings.HasSuffix(modulesPath, "/") {
		modulesPath = modulesPath + "/"
	}

	return &Publisher{
		Git:         gitRunner,
		OutputDir:   outputDir,
		BaseURL:     strings.TrimRight(baseURL, "/"),
		ModulesPath: modulesPath,
	}
}

// GenerateServiceDiscovery creates the .well-known/terraform.json file.
func (p *Publisher) GenerateServiceDiscovery() error {
	wellKnownDir := filepath.Join(p.OutputDir, ".well-known")
	if err := os.MkdirAll(wellKnownDir, 0o755); err != nil {
		return fmt.Errorf("creating .well-known directory: %w", err)
	}

	sd := ServiceDiscovery{
		ModulesV1: p.ModulesPath,
	}

	data, err := json.MarshalIndent(sd, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling terraform.json: %w", err)
	}

	path := filepath.Join(wellKnownDir, "terraform.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing terraform.json: %w", err)
	}

	return nil
}

// archiveName returns the archive filename for a module version.
func archiveName(modulePath string, version *semver.Version) string {
	return "module.tar.gz"
}

// descriptiveArchiveName returns a human-friendly archive filename (e.g. "hetzner-server-1.0.0.tar.gz")
// for use in download attributes and Content-Disposition headers.
func descriptiveArchiveName(modulePath string, version *semver.Version) string {
	safePath := strings.ReplaceAll(modulePath, "/", "-")
	return fmt.Sprintf("%s-%s.tar.gz", safePath, version.Original())
}

// PublishVersion generates all files for a single module version.
// It creates: the archive, the download HTML, and returns the version string.
// The caller is responsible for generating the versions.json after all versions are published.
func (p *Publisher) PublishVersion(tag module.TagInfo) error {
	versionDir := filepath.Join(p.OutputDir, tag.ModulePath, tag.Version.Original())
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		return fmt.Errorf("creating version directory: %w", err)
	}

	// Generate archive
	archiveFile := archiveName(tag.ModulePath, tag.Version)
	archivePath := filepath.Join(versionDir, archiveFile)
	if err := p.Git.ArchiveModule(tag.Tag, tag.ModulePath, archivePath); err != nil {
		return fmt.Errorf("archiving module %s at %s: %w", tag.ModulePath, tag.Tag, err)
	}

	// Generate download HTML
	archiveURL := fmt.Sprintf("%s/%s/%s/%s",
		p.BaseURL, tag.ModulePath, tag.Version.Original(), archiveFile)
	downloadHTML := generateDownloadHTML(archiveURL)
	downloadPath := filepath.Join(versionDir, "download")
	if err := os.WriteFile(downloadPath, []byte(downloadHTML), 0o644); err != nil {
		return fmt.Errorf("writing download file: %w", err)
	}

	return nil
}

// PublishVersionFromWorkTree generates all files for a module version using
// the current filesystem (working tree) instead of a git tag.
func (p *Publisher) PublishVersionFromWorkTree(repoRoot, modulePath string, version *semver.Version) error {
	versionDir := filepath.Join(p.OutputDir, modulePath, version.Original())
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		return fmt.Errorf("creating version directory: %w", err)
	}

	// Generate archive from working tree
	archiveFile := archiveName(modulePath, version)
	archivePath := filepath.Join(versionDir, archiveFile)
	f, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("creating archive file: %w", err)
	}
	if err := buildArchiveFromWorkTree(repoRoot, modulePath, f); err != nil {
		f.Close()
		return fmt.Errorf("building archive for %s: %w", modulePath, err)
	}
	f.Close()

	// Generate download HTML
	archiveURL := fmt.Sprintf("%s/%s/%s/%s",
		p.BaseURL, modulePath, version.Original(), archiveFile)
	downloadHTML := generateDownloadHTML(archiveURL)
	downloadPath := filepath.Join(versionDir, "download")
	if err := os.WriteFile(downloadPath, []byte(downloadHTML), 0o644); err != nil {
		return fmt.Errorf("writing download file: %w", err)
	}

	return nil
}

// GenerateVersionsJSON creates the versions.json file for a module.
func (p *Publisher) GenerateVersionsJSON(modulePath string, versions []*semver.Version) error {
	moduleDir := filepath.Join(p.OutputDir, modulePath)
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		return fmt.Errorf("creating module directory: %w", err)
	}

	// Sort versions descending (newest first) for the listing
	sortedVersions := make([]*semver.Version, len(versions))
	copy(sortedVersions, versions)
	sortVersionsDesc(sortedVersions)

	entries := make([]VersionEntry, len(sortedVersions))
	for i, v := range sortedVersions {
		entries[i] = VersionEntry{Version: v.Original()}
	}

	mv := ModuleVersions{
		Modules: []ModuleVersionList{
			{Versions: entries},
		},
	}

	data, err := json.MarshalIndent(mv, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling versions.json: %w", err)
	}

	path := filepath.Join(moduleDir, "versions.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing versions.json: %w", err)
	}

	return nil
}

// InvalidationPaths returns the CDN paths that should be invalidated after publishing.
func InvalidationPaths(modulePath string, version *semver.Version) []string {
	return []string{
		fmt.Sprintf("/%s/versions.json", modulePath),
		fmt.Sprintf("/%s/%s/download", modulePath, version.Original()),
	}
}

func generateDownloadHTML(archiveURL string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
<meta name="terraform-get" content="%s" />
</head>
<body></body>
</html>
`, html.EscapeString(archiveURL))
}

func sortVersionsDesc(versions []*semver.Version) {
	for i := 0; i < len(versions); i++ {
		for j := i + 1; j < len(versions); j++ {
			if versions[j].GreaterThan(versions[i]) {
				versions[i], versions[j] = versions[j], versions[i]
			}
		}
	}
}
