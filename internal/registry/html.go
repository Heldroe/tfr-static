package registry

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/Heldroe/tfr-static/internal/git"
	"github.com/Heldroe/tfr-static/internal/module"
	"github.com/yuin/goldmark"
)

// HTMLGenerator creates HTML documentation pages for the registry.
type HTMLGenerator struct {
	Git       *git.Runner
	OutputDir string
	IndexFile string
}

// NewHTMLGenerator creates a new HTMLGenerator.
func NewHTMLGenerator(gitRunner *git.Runner, outputDir, indexFile string) *HTMLGenerator {
	if indexFile == "" {
		indexFile = "index.html"
	}
	return &HTMLGenerator{
		Git:       gitRunner,
		OutputDir: outputDir,
		IndexFile: indexFile,
	}
}

type rootPageData struct {
	Modules []rootModuleEntry
}

type rootModuleEntry struct {
	Path          string
	LatestVersion string
	VersionCount  int
}

type modulePageData struct {
	ModulePath string
	Versions   []string
	ReadmeHTML template.HTML
}

type versionPageData struct {
	ModulePath string
	Version    string
	ArchiveURL string
	ReadmeHTML template.HTML
}

// GenerateAll generates the complete HTML documentation tree.
func (g *HTMLGenerator) GenerateAll(grouped map[string][]module.TagInfo) error {
	// Generate root index
	if err := g.generateRootIndex(grouped); err != nil {
		return fmt.Errorf("generating root index: %w", err)
	}

	// Generate module and version pages
	for modPath, tags := range grouped {
		module.SortVersionsDesc(tags)
		versions := make([]*semver.Version, len(tags))
		for i, t := range tags {
			versions[i] = t.Version
		}

		if err := g.generateModuleIndex(modPath, tags); err != nil {
			return fmt.Errorf("generating module index for %s: %w", modPath, err)
		}

		for _, t := range tags {
			if err := g.generateVersionIndex(t); err != nil {
				return fmt.Errorf("generating version index for %s: %w", t.Tag, err)
			}
		}
	}

	return nil
}

// GenerateForModule generates HTML pages for a single module and updates the root index.
func (g *HTMLGenerator) GenerateForModule(modPath string, tags []module.TagInfo, allGrouped map[string][]module.TagInfo) error {
	module.SortVersionsDesc(tags)

	if err := g.generateModuleIndex(modPath, tags); err != nil {
		return fmt.Errorf("generating module index for %s: %w", modPath, err)
	}

	for _, t := range tags {
		if err := g.generateVersionIndex(t); err != nil {
			return fmt.Errorf("generating version index for %s: %w", t.Tag, err)
		}
	}

	return g.generateRootIndex(allGrouped)
}

func (g *HTMLGenerator) generateRootIndex(grouped map[string][]module.TagInfo) error {
	var entries []rootModuleEntry
	for modPath, tags := range grouped {
		latest := module.LatestVersion(tags)
		latestStr := ""
		if latest != nil {
			latestStr = latest.Version.Original()
		}
		entries = append(entries, rootModuleEntry{
			Path:          modPath,
			LatestVersion: latestStr,
			VersionCount:  len(tags),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})

	data := rootPageData{Modules: entries}
	return g.writeTemplate(filepath.Join(g.OutputDir, g.IndexFile), rootTmpl, data)
}

func (g *HTMLGenerator) generateModuleIndex(modPath string, tags []module.TagInfo) error {
	versions := make([]string, len(tags))
	for i, t := range tags {
		versions[i] = t.Version.Original()
	}

	// Try to get README from the latest version's tag
	var readmeHTML template.HTML
	if len(tags) > 0 {
		readmeHTML = g.renderReadme(tags[0].Tag, modPath)
	}

	data := modulePageData{
		ModulePath: modPath,
		Versions:   versions,
		ReadmeHTML: readmeHTML,
	}
	dir := filepath.Join(g.OutputDir, modPath)
	return g.writeTemplate(filepath.Join(dir, g.IndexFile), moduleTmpl, data)
}

func (g *HTMLGenerator) generateVersionIndex(tag module.TagInfo) error {
	archiveFile := archiveName(tag.ModulePath, tag.Version)
	archiveURL := fmt.Sprintf("%s/%s", tag.Version.Original(), archiveFile)

	readmeHTML := g.renderReadme(tag.Tag, tag.ModulePath)

	data := versionPageData{
		ModulePath: tag.ModulePath,
		Version:    tag.Version.Original(),
		ArchiveURL: archiveURL,
		ReadmeHTML: readmeHTML,
	}
	dir := filepath.Join(g.OutputDir, tag.ModulePath, tag.Version.Original())
	return g.writeTemplate(filepath.Join(dir, g.IndexFile), versionTmpl, data)
}

func (g *HTMLGenerator) renderReadme(tag, modPath string) template.HTML {
	content, err := g.Git.ShowFileAtTag(tag, modPath+"/README.md")
	if err != nil || content == "" {
		// Try lowercase
		content, err = g.Git.ShowFileAtTag(tag, modPath+"/readme.md")
		if err != nil || content == "" {
			return ""
		}
	}

	var buf bytes.Buffer
	if err := goldmark.Convert([]byte(content), &buf); err != nil {
		return ""
	}
	return template.HTML(buf.String())
}

func (g *HTMLGenerator) writeTemplate(path string, tmpl *template.Template, data any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func pathDepth(p string) int {
	return strings.Count(p, "/")
}

func relRoot(modPath string) string {
	depth := pathDepth(modPath) + 1 // +1 for the file itself being in the module dir
	if depth <= 0 {
		return "."
	}
	parts := make([]string, depth)
	for i := range parts {
		parts[i] = ".."
	}
	return strings.Join(parts, "/")
}

func relRootVersion(modPath string) string {
	depth := pathDepth(modPath) + 2 // module depth + version dir
	parts := make([]string, depth)
	for i := range parts {
		parts[i] = ".."
	}
	return strings.Join(parts, "/")
}

var rootTmpl = template.Must(template.New("root").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Terraform Module Registry</title>
<style>` + cssStyles + `</style>
</head>
<body>
<div class="container">
<h1>Terraform Module Registry</h1>
<table>
<thead>
<tr><th>Module</th><th>Latest Version</th><th>Versions</th></tr>
</thead>
<tbody>
{{range .Modules}}
<tr>
<td><a href="{{.Path}}/">{{.Path}}</a></td>
<td>{{if .LatestVersion}}{{.LatestVersion}}{{else}}-{{end}}</td>
<td>{{.VersionCount}}</td>
</tr>
{{end}}
</tbody>
</table>
</div>
</body>
</html>
`))

var moduleTmpl = template.Must(template.New("module").Funcs(template.FuncMap{
	"relRoot": relRoot,
}).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.ModulePath}}</title>
<style>` + cssStyles + `</style>
</head>
<body>
<div class="container">
<p><a href="{{relRoot .ModulePath}}">← Back to registry</a></p>
<h1>{{.ModulePath}}</h1>
<h2>Versions</h2>
<ul>
{{range .Versions}}
<li><a href="{{.}}/">{{.}}</a></li>
{{end}}
</ul>
{{if .ReadmeHTML}}
<hr>
<div class="readme">{{.ReadmeHTML}}</div>
{{end}}
</div>
</body>
</html>
`))

var versionTmpl = template.Must(template.New("version").Funcs(template.FuncMap{
	"relRootVersion": relRootVersion,
}).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.ModulePath}} {{.Version}}</title>
<style>` + cssStyles + `</style>
</head>
<body>
<div class="container">
<p><a href="../">← {{.ModulePath}}</a> · <a href="{{relRootVersion .ModulePath}}">Registry</a></p>
<h1>{{.ModulePath}} <span class="version">{{.Version}}</span></h1>
<p>Download: <a href="{{.ArchiveURL}}">{{.ArchiveURL}}</a></p>
{{if .ReadmeHTML}}
<hr>
<div class="readme">{{.ReadmeHTML}}</div>
{{end}}
</div>
</body>
</html>
`))

const cssStyles = `
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; margin: 0; padding: 0; color: #24292e; }
.container { max-width: 900px; margin: 0 auto; padding: 2rem; }
h1 { border-bottom: 1px solid #e1e4e8; padding-bottom: 0.5rem; }
.version { color: #6a737d; font-weight: normal; }
a { color: #0366d6; text-decoration: none; }
a:hover { text-decoration: underline; }
table { width: 100%; border-collapse: collapse; }
th, td { text-align: left; padding: 0.5rem 1rem; border-bottom: 1px solid #e1e4e8; }
th { background: #f6f8fa; }
ul { list-style: none; padding: 0; }
ul li { padding: 0.3rem 0; }
hr { border: none; border-top: 1px solid #e1e4e8; margin: 2rem 0; }
.readme { line-height: 1.6; }
.readme pre { background: #f6f8fa; padding: 1rem; overflow-x: auto; border-radius: 6px; }
.readme code { background: #f6f8fa; padding: 0.2em 0.4em; border-radius: 3px; font-size: 85%; }
.readme pre code { background: none; padding: 0; font-size: 100%; }
`
