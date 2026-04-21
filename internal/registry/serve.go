package registry

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/Heldroe/tfr-static/internal/git"
	"github.com/Heldroe/tfr-static/internal/logging"
	"github.com/Heldroe/tfr-static/internal/module"
)

// DevServer serves module archives built on the fly from the current working tree.
// Every download request returns the current state of the module directory,
// regardless of which version was requested. This lets developers swap the
// registry domain to localhost and test uncommitted changes without tagging.
type DevServer struct {
	Git         *git.Runner
	RepoRoot    string
	ModulesPath string
	HTMLEnabled bool
	baseTmpl    *template.Template
}

// NewDevServer creates a DevServer.
func NewDevServer(gitRunner *git.Runner, repoRoot, modulesPath string) *DevServer {
	return &DevServer{
		Git:         gitRunner,
		RepoRoot:    repoRoot,
		ModulesPath: normalizeModulesPath(modulesPath),
		baseTmpl:    template.Must(template.New("base").Parse(defaultBaseTemplate)),
	}
}

// Handler returns an http.Handler that implements the dev registry.
//
// Uses Go 1.22 typed patterns (method + path). The `{path...}` wildcard at the
// end captures the module + version portion, which we parse ourselves because
// Go's ServeMux doesn't allow `{name...}` wildcards in the middle of a pattern
// and module paths can contain slashes (e.g. aws/ec2/security-group).
func (s *DevServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mp := strings.TrimSuffix(s.ModulesPath, "/")

	mux.HandleFunc("GET /.well-known/terraform.json", s.handleServiceDiscovery)
	mux.HandleFunc("GET "+mp+"/{path...}", s.dispatchModule)

	return withAccessLog(mux)
}

func withAccessLog(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logging.FromContext(r.Context()).Info("dev request",
			"method", r.Method,
			"path", r.URL.Path,
		)
		h.ServeHTTP(w, r)
	})
}

func (s *DevServer) handleServiceDiscovery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ServiceDiscovery{ModulesV1: s.ModulesPath})
}

func (s *DevServer) dispatchModule(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	switch {
	case strings.HasSuffix(path, "/versions.json"), strings.HasSuffix(path, "/versions"):
		modulePath := strings.TrimSuffix(path, "/versions.json")
		modulePath = strings.TrimSuffix(modulePath, "/versions")
		s.handleVersions(w, r, modulePath)
	case strings.HasSuffix(path, "/download"):
		modulePath, version, ok := splitModuleVersion(strings.TrimSuffix(path, "/download"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		s.handleDownload(w, r, modulePath, version)
	case strings.HasSuffix(path, "/"+archiveFilename):
		modulePath, version, ok := splitModuleVersion(strings.TrimSuffix(path, "/"+archiveFilename))
		if !ok {
			http.NotFound(w, r)
			return
		}
		s.handleArchive(w, r, modulePath, version)
	default:
		if s.HTMLEnabled {
			s.handleHTMLPage(w, r, path)
		} else {
			http.NotFound(w, r)
		}
	}
}

// splitModuleVersion splits a `{module}/{version}` path into its parts.
// Returns ok=false if the path has no slash.
func splitModuleVersion(p string) (modulePath, version string, ok bool) {
	i := strings.LastIndex(p, "/")
	if i == -1 {
		return "", "", false
	}
	return p[:i], p[i+1:], true
}

func (s *DevServer) handleVersions(w http.ResponseWriter, r *http.Request, modulePath string) {
	logger := logging.FromContext(r.Context())

	if !s.moduleExists(modulePath) {
		logger.Warn("module not found in working tree", "module", modulePath)
		http.NotFound(w, r)
		return
	}

	grouped, _ := module.LoadAll(r.Context(), s.Git)
	var entries []VersionEntry
	for _, t := range grouped[modulePath] {
		entries = append(entries, VersionEntry{Version: t.Version.Original()})
	}

	// Always include a dev version so new modules without tags still work.
	// Use 0.0.0-dev which is lower than any real version, and also add
	// a very high version so "latest" constraints resolve to it.
	entries = append(entries,
		VersionEntry{Version: "0.0.0-dev"},
		VersionEntry{Version: "99999.0.0-dev"},
	)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ModuleVersions{
		Modules: []ModuleVersionList{{Versions: entries}},
	})
}

func (s *DevServer) handleDownload(w http.ResponseWriter, r *http.Request, modulePath, version string) {
	logger := logging.FromContext(r.Context())

	if !s.moduleExists(modulePath) {
		logger.Warn("module not found in working tree", "module", modulePath)
		http.NotFound(w, r)
		return
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	archiveURL := fmt.Sprintf("%s://%s%s%s/%s/%s", scheme, r.Host, s.ModulesPath, modulePath, version, archiveFilename)

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

func (s *DevServer) handleArchive(w http.ResponseWriter, r *http.Request, modulePath, version string) {
	logger := logging.FromContext(r.Context())

	if !s.moduleExists(modulePath) {
		logger.Warn("module not found in working tree", "module", modulePath)
		http.NotFound(w, r)
		return
	}

	logger.Info("building archive from working tree", "module", modulePath)

	descriptiveName := descriptiveArchiveName(modulePath, version)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, descriptiveName))
	w.Header().Set("Content-Type", "application/gzip")
	if err := buildArchiveFromWorkTree(s.RepoRoot, modulePath, w); err != nil {
		logger.Error("building archive failed", "module", modulePath, "err", err)
		http.Error(w, "failed to build archive", http.StatusInternalServerError)
		return
	}
}

func (s *DevServer) handleHTMLPage(w http.ResponseWriter, r *http.Request, path string) {
	path = strings.TrimSuffix(path, "/")
	reader := FilesystemReadmeReader(s.RepoRoot)

	if path == "" {
		modules, err := module.DiscoverModules(s.RepoRoot)
		if err != nil {
			http.Error(w, "failed to discover modules", http.StatusInternalServerError)
			return
		}
		grouped, _ := module.LoadAll(r.Context(), s.Git)
		entries := make([]rootModuleEntry, 0, len(modules))
		for _, m := range modules {
			moduleTags := grouped[m.Path]
			latestStr := "0.0.0-dev"
			if latest := module.LatestVersion(moduleTags); latest != nil {
				latestStr = latest.Version.Original()
			}
			entries = append(entries, rootModuleEntry{
				Path:          m.Path,
				LatestVersion: latestStr,
				VersionCount:  len(moduleTags) + 1, // +1 for dev
			})
		}
		s.renderPage(w, "Terraform Module Registry", rootTmpl, rootPageData{Modules: entries})
		return
	}

	// Version page: last segment is a valid semver and the module directory exists.
	if lastSlash := strings.LastIndex(path, "/"); lastSlash != -1 {
		possibleModule := path[:lastSlash]
		possibleVersion := path[lastSlash+1:]
		if _, err := semver.StrictNewVersion(possibleVersion); err == nil && s.moduleExists(possibleModule) {
			readmeHTML := renderMarkdown(reader(r.Context(), possibleModule, ""))
			s.renderPage(w, possibleModule+" "+possibleVersion, versionTmpl, versionPageData{
				ModulePath:          possibleModule,
				Version:             possibleVersion,
				ArchiveURL:          archiveFilename,
				ArchiveDownloadName: descriptiveArchiveName(possibleModule, possibleVersion),
				ReadmeHTML:          readmeHTML,
			})
			return
		}
	}

	if s.moduleExists(path) {
		grouped, _ := module.LoadAll(r.Context(), s.Git)
		moduleTags := grouped[path]
		module.SortVersionsDesc(moduleTags)

		versions := make([]string, 0, len(moduleTags)+1)
		for _, t := range moduleTags {
			versions = append(versions, t.Version.Original())
		}
		versions = append(versions, "0.0.0-dev")

		readmeHTML := renderMarkdown(reader(r.Context(), path, ""))
		s.renderPage(w, path, moduleTmpl, modulePageData{
			ModulePath: path,
			Versions:   versions,
			ReadmeHTML: readmeHTML,
		})
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
