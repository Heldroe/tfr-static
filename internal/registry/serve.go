package registry

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/Heldroe/tfr-static/internal/git"
	"github.com/Heldroe/tfr-static/internal/module"
)

// DevServer serves module archives built on the fly from the current working tree.
// Every download request returns the current state of the module directory,
// regardless of which version was requested. This lets developers swap the
// registry domain to localhost and test uncommitted changes without tagging.
type DevServer struct {
	Git            *git.Runner
	RepoRoot       string
	ModulesPath    string
	HTMLEnabled    bool
	baseTmpl       *template.Template
	namespace      string
	moduleMappings map[string]string
	// regToDirPath maps 3-segment registry paths back to directory paths
	regToDirPath map[string]string
	// dirToRegPath maps directory paths to 3-segment registry paths
	dirToRegPath map[string]string
}

// NewDevServer creates a DevServer.
func NewDevServer(gitRunner *git.Runner, repoRoot, modulesPath, namespace string, moduleMappings map[string]string) (*DevServer, error) {
	modulesPath = normalizeModulesPath(modulesPath)
	if moduleMappings == nil {
		moduleMappings = map[string]string{}
	}

	s := &DevServer{
		Git:            gitRunner,
		RepoRoot:       repoRoot,
		ModulesPath:    modulesPath,
		baseTmpl:       template.Must(template.New("base").Parse(defaultBaseTemplate)),
		namespace:      namespace,
		moduleMappings: moduleMappings,
		regToDirPath:   make(map[string]string),
		dirToRegPath:   make(map[string]string),
	}
	if err := s.buildPathMaps(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *DevServer) buildPathMaps() error {
	modules, err := module.DiscoverModules(s.RepoRoot)
	if err != nil {
		return fmt.Errorf("discovering modules: %w", err)
	}

	dirPaths := make([]string, len(modules))
	for i, m := range modules {
		dirPaths[i] = m.Path
	}
	if err := module.DetectAmbiguities(dirPaths, s.namespace, s.moduleMappings); err != nil {
		return err
	}

	for _, m := range modules {
		regPath, _, err := module.RegistryPath(m.Path, s.namespace, s.moduleMappings)
		if err != nil {
			log.Printf("[dev] skipping module %q: %v", m.Path, err)
			continue
		}
		s.regToDirPath[regPath] = m.Path
		s.dirToRegPath[m.Path] = regPath
	}
	return nil
}

// resolveDirPath converts a registry path from a URL to the filesystem directory path.
// If the path is already a known directory path, it returns it directly.
func (s *DevServer) resolveDirPath(registryPath string) (string, bool) {
	if dirPath, ok := s.regToDirPath[registryPath]; ok {
		return dirPath, true
	}
	if s.moduleExists(registryPath) {
		return registryPath, true
	}
	return "", false
}

// Handler returns an http.Handler that implements the dev registry.
func (s *DevServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/terraform.json", s.handleServiceDiscovery)
	mux.HandleFunc("/", s.handleModule)
	return mux
}

func (s *DevServer) handleServiceDiscovery(w http.ResponseWriter, r *http.Request) {
	log.Printf("[dev] %s %s", r.Method, r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ServiceDiscovery{ModulesV1: s.ModulesPath})
}

func (s *DevServer) handleModule(w http.ResponseWriter, r *http.Request) {
	log.Printf("[dev] %s %s", r.Method, r.URL.Path)
	path := strings.TrimPrefix(r.URL.Path, s.ModulesPath)
	path = strings.TrimPrefix(path, "/")

	switch {
	case strings.HasSuffix(path, "/versions"):
		s.handleVersions(w, r, path)
	case strings.HasSuffix(path, "/download"):
		s.handleDownload(w, r, path)
	case strings.HasSuffix(path, ".tar.gz"):
		s.handleArchive(w, r, path)
	default:
		if s.HTMLEnabled {
			s.handleHTMLPage(w, r, path)
		} else {
			http.NotFound(w, r)
		}
	}
}

func (s *DevServer) handleVersions(w http.ResponseWriter, r *http.Request, path string) {
	registryPath := strings.TrimSuffix(path, "/versions")

	dirPath, ok := s.resolveDirPath(registryPath)
	if !ok {
		log.Printf("[dev] module %q not found in working tree", registryPath)
		http.NotFound(w, r)
		return
	}

	tags, _ := s.Git.ListTags()
	allParsed := module.ParseAllTags(tags)
	moduleTags := module.FilterTagsForModule(allParsed, dirPath)

	var entries []VersionEntry
	for _, t := range moduleTags {
		entries = append(entries, VersionEntry{Version: t.Version.Original()})
	}

	// Always include a dev version so new modules without tags still work.
	entries = append(entries,
		VersionEntry{Version: "0.0.0-dev"},
		VersionEntry{Version: "99999.0.0-dev"},
	)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ModuleVersions{
		Modules: []ModuleVersionList{{Versions: entries}},
	})
}

func (s *DevServer) handleDownload(w http.ResponseWriter, r *http.Request, path string) {
	// path: {registry_path}/{version}/download
	withoutDownload := strings.TrimSuffix(path, "/download")
	lastSlash := strings.LastIndex(withoutDownload, "/")
	if lastSlash == -1 {
		http.NotFound(w, r)
		return
	}
	registryPath := withoutDownload[:lastSlash]
	version := withoutDownload[lastSlash+1:]

	if _, ok := s.resolveDirPath(registryPath); !ok {
		log.Printf("[dev] module %q not found in working tree", registryPath)
		http.NotFound(w, r)
		return
	}

	// Point to the archive endpoint on this same server (URL uses registry path)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	archiveFile := archiveNameFromParts(registryPath, version)
	archiveURL := fmt.Sprintf("%s://%s%s%s/%s/%s", scheme, host, s.ModulesPath, registryPath, version, archiveFile)

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
<meta name="terraform-get" content="%s" />
</head>
<body></body>
</html>
`, archiveURL)
}

func (s *DevServer) handleArchive(w http.ResponseWriter, r *http.Request, path string) {
	// path: {registry_path}/{version}/{archive}.tar.gz
	lastSlash := strings.LastIndex(path, "/")
	if lastSlash == -1 {
		http.NotFound(w, r)
		return
	}
	withoutFile := path[:lastSlash]
	// withoutFile is {registry_path}/{version}, strip the version
	versionSlash := strings.LastIndex(withoutFile, "/")
	if versionSlash == -1 {
		http.NotFound(w, r)
		return
	}
	registryPath := withoutFile[:versionSlash]

	dirPath, ok := s.resolveDirPath(registryPath)
	if !ok {
		log.Printf("[dev] module %q not found in working tree", registryPath)
		http.NotFound(w, r)
		return
	}

	log.Printf("[dev] building archive for %s from working tree", dirPath)

	descriptiveName := descriptiveArchiveNameFromParts(registryPath, withoutFile[versionSlash+1:])
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, descriptiveName))
	w.Header().Set("Content-Type", "application/gzip")
	if err := buildArchiveFromWorkTree(s.RepoRoot, dirPath, w); err != nil {
		log.Printf("[dev] error building archive: %v", err)
		http.Error(w, "failed to build archive", http.StatusInternalServerError)
		return
	}
}

func (s *DevServer) handleHTMLPage(w http.ResponseWriter, r *http.Request, path string) {
	path = strings.TrimSuffix(path, "/")

	reader := FilesystemReadmeReader(s.RepoRoot)

	// Root page: list all modules
	if path == "" {
		tags, _ := s.Git.ListTags()
		allParsed := module.ParseAllTags(tags)

		var entries []rootModuleEntry
		for dirPath, regPath := range s.dirToRegPath {
			moduleTags := module.FilterTagsForModule(allParsed, dirPath)
			latest := module.LatestVersion(moduleTags)
			latestStr := "0.0.0-dev"
			if latest != nil {
				latestStr = latest.Version.Original()
			}
			entries = append(entries, rootModuleEntry{
				Path:          regPath,
				LatestVersion: latestStr,
				VersionCount:  len(moduleTags) + 1, // +1 for dev
			})
		}
		s.renderPage(w, "Terraform Module Registry", rootTmpl, rootPageData{Modules: entries})
		return
	}

	// Check if this is a version page (last segment is a semver or "0.0.0-dev")
	lastSlash := strings.LastIndex(path, "/")
	if lastSlash != -1 {
		possibleModule := path[:lastSlash]
		possibleVersion := path[lastSlash+1:]
		if _, err := semver.StrictNewVersion(possibleVersion); err == nil {
			if dirPath, ok := s.resolveDirPath(possibleModule); ok {
				readmeHTML := renderMarkdown(reader(dirPath, ""))
				archiveFile := "module.tar.gz"
				data := versionPageData{
					ModulePath:          possibleModule,
					Version:             possibleVersion,
					ArchiveURL:          archiveFile,
					ArchiveDownloadName: descriptiveArchiveNameFromParts(possibleModule, possibleVersion),
					ReadmeHTML:          readmeHTML,
					SourceURL:           r.Host + "/" + possibleModule,
				}
				s.renderPage(w, possibleModule+" "+possibleVersion, versionTmpl, data)
				return
			}
		}
	}

	// Module page
	if dirPath, ok := s.resolveDirPath(path); ok {
		tags, _ := s.Git.ListTags()
		allParsed := module.ParseAllTags(tags)
		moduleTags := module.FilterTagsForModule(allParsed, dirPath)
		module.SortVersionsDesc(moduleTags)

		var versions []string
		for _, t := range moduleTags {
			versions = append(versions, t.Version.Original())
		}
		versions = append(versions, "0.0.0-dev")

		latestVersion := "0.0.0-dev"
		if len(moduleTags) > 0 {
			latestVersion = moduleTags[0].Version.Original()
		}

		readmeHTML := renderMarkdown(reader(dirPath, ""))
		data := modulePageData{
			ModulePath:    path,
			Versions:      versions,
			ReadmeHTML:    readmeHTML,
			SourceURL:     r.Host + "/" + path,
			LatestVersion: latestVersion,
		}
		s.renderPage(w, path, moduleTmpl, data)
		return
	}

	http.NotFound(w, r)
}

func (s *DevServer) renderPage(w http.ResponseWriter, title string, contentTmpl *template.Template, data any) {
	var contentBuf bytes.Buffer
	if err := contentTmpl.Execute(&contentBuf, data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	var pageBuf bytes.Buffer
	if err := s.baseTmpl.Execute(&pageBuf, basePage{
		Title:   title,
		Content: template.HTML(contentBuf.String()),
	}); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.Write(pageBuf.Bytes())
}

func (s *DevServer) moduleExists(modulePath string) bool {
	dir := filepath.Join(s.RepoRoot, modulePath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tf") {
			return true
		}
	}
	return false
}

// archiveNameFromParts builds an archive filename without requiring a semver object.
func archiveNameFromParts(modulePath, version string) string {
	return "module.tar.gz"
}

// descriptiveArchiveNameFromParts returns a human-friendly archive filename
// for use in Content-Disposition headers.
func descriptiveArchiveNameFromParts(modulePath, version string) string {
	safePath := strings.ReplaceAll(modulePath, "/", "-")
	return fmt.Sprintf("%s-%s.tar.gz", safePath, version)
}

// buildArchiveFromWorkTree creates a tar.gz of the module directory from the
// filesystem (not from git), so uncommitted changes are included.
func buildArchiveFromWorkTree(repoRoot, modulePath string, w io.Writer) error {
	moduleDir := filepath.Join(repoRoot, modulePath)

	gw := gzip.NewWriter(w)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	return filepath.Walk(moduleDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden files/dirs
		if strings.HasPrefix(info.Name(), ".") && info.Name() != "." {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, err := filepath.Rel(moduleDir, path)
		if err != nil {
			return err
		}

		tarPath := filepath.ToSlash(relPath)
		if info.IsDir() {
			tarPath += "/"
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = tarPath

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(tw, f)
		return err
	})
}
