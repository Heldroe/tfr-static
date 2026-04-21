package registry

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Heldroe/tfr-static/internal/docs"
	"github.com/Heldroe/tfr-static/internal/git"
	"github.com/Heldroe/tfr-static/internal/module"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

//go:embed templates/base.html
var defaultBaseTemplate string

// ReadmeReader returns the raw markdown content of a README for a given module path
// and git tag. The tag is used by git-based readers to read from the correct commit.
// Filesystem-based readers ignore the tag parameter.
type ReadmeReader func(modulePath, tag string) string

// GitReadmeReader returns a ReadmeReader that reads READMEs from the git tag
// specified in each call.
func GitReadmeReader(gitRunner *git.Runner) ReadmeReader {
	return func(modulePath, tag string) string {
		return gitReadmeContent(gitRunner, tag, modulePath)
	}
}

func gitReadmeContent(gitRunner *git.Runner, tag, modulePath string) string {
	content, err := gitRunner.ShowFileAtTag(tag, modulePath+"/README.md")
	if err != nil || content == "" {
		content, err = gitRunner.ShowFileAtTag(tag, modulePath+"/readme.md")
		if err != nil || content == "" {
			return ""
		}
	}
	return content
}

// FilesystemReadmeReader returns a ReadmeReader that reads READMEs from the filesystem.
// The tag parameter is ignored.
func FilesystemReadmeReader(repoRoot string) ReadmeReader {
	return func(modulePath, tag string) string {
		dir := filepath.Join(repoRoot, modulePath)
		content, err := os.ReadFile(filepath.Join(dir, "README.md"))
		if err != nil {
			content, err = os.ReadFile(filepath.Join(dir, "readme.md"))
			if err != nil {
				return ""
			}
		}
		return string(content)
	}
}

// EnrichedReadmeReader wraps a base ReadmeReader and enriches the output
// with terraform-docs generated documentation. Module .tf files are read from
// the git tag (so each version gets its own docs), while .terraform-docs.yml
// config is read from the current filesystem (so config changes are retroactive).
func EnrichedReadmeReader(base ReadmeReader, repoRoot string, gitRunner *git.Runner) ReadmeReader {
	return func(modulePath, tag string) string {
		readme := base(modulePath, tag)
		configDir := filepath.Join(repoRoot, modulePath)

		extractDir, cleanup, err := gitRunner.ExtractModuleAtTag(tag, modulePath)
		if err != nil {
			return readme
		}
		defer cleanup()

		docsOutput, err := docs.GenerateWithConfig(extractDir, configDir)
		if err != nil || docsOutput == "" {
			return readme
		}
		return docs.InjectIntoReadme(readme, docsOutput)
	}
}

type basePage struct {
	Title   string
	Content template.HTML
}

// HTMLGenerator creates HTML documentation pages for the registry.
type HTMLGenerator struct {
	Git          *git.Runner
	OutputDir    string
	IndexFile    string
	ReadmeReader ReadmeReader
	baseTmpl     *template.Template
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
		baseTmpl:  template.Must(template.New("base").Parse(defaultBaseTemplate)),
	}
}

// LoadBaseTemplate loads a custom base HTML template from a file path.
// The template must contain {{.Title}} and {{.Content}} placeholders.
func (g *HTMLGenerator) LoadBaseTemplate(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading base template %s: %w", path, err)
	}
	tmpl, err := template.New("base").Parse(string(content))
	if err != nil {
		return fmt.Errorf("parsing base template %s: %w", path, err)
	}
	g.baseTmpl = tmpl
	return nil
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
	ModulePath          string
	Version             string
	ArchiveURL          string
	ArchiveDownloadName string
	ReadmeHTML          template.HTML
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

// GenerateForModule generates HTML pages for a single module (module index +
// every version page) and updates the root index.
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

// GenerateForVersion generates the HTML page for a single new version,
// updates the module index (to show it in the version list), and updates the
// root index.
func (g *HTMLGenerator) GenerateForVersion(tag module.TagInfo, moduleTags []module.TagInfo, allGrouped map[string][]module.TagInfo) error {
	module.SortVersionsDesc(moduleTags)

	if err := g.generateVersionIndex(tag); err != nil {
		return fmt.Errorf("generating version index for %s: %w", tag.Tag, err)
	}

	if err := g.generateModuleIndex(tag.ModulePath, moduleTags); err != nil {
		return fmt.Errorf("generating module index for %s: %w", tag.ModulePath, err)
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
	return g.writePage(filepath.Join(g.OutputDir, g.IndexFile), "Terraform Module Registry", rootTmpl, data)
}

func (g *HTMLGenerator) generateModuleIndex(modPath string, tags []module.TagInfo) error {
	versions := make([]string, len(tags))
	for i, t := range tags {
		versions[i] = t.Version.Original()
	}

	// Module index uses the latest (first after sort) tag's README
	var readmeHTML template.HTML
	if len(tags) > 0 {
		readmeHTML = renderMarkdown(g.readReadme(modPath, tags[0].Tag))
	}

	data := modulePageData{
		ModulePath: modPath,
		Versions:   versions,
		ReadmeHTML: readmeHTML,
	}
	dir := filepath.Join(g.OutputDir, modPath)
	return g.writePage(filepath.Join(dir, g.IndexFile), modPath, moduleTmpl, data)
}

func (g *HTMLGenerator) generateVersionIndex(tag module.TagInfo) error {
	archiveFile := archiveName(tag.ModulePath, tag.Version)
	archiveURL := archiveFile

	// Each version page reads README from its own tag
	readmeHTML := renderMarkdown(g.readReadme(tag.ModulePath, tag.Tag))

	data := versionPageData{
		ModulePath:          tag.ModulePath,
		Version:             tag.Version.Original(),
		ArchiveURL:          archiveURL,
		ArchiveDownloadName: descriptiveArchiveName(tag.ModulePath, tag.Version),
		ReadmeHTML:          readmeHTML,
	}
	dir := filepath.Join(g.OutputDir, tag.ModulePath, tag.Version.Original())
	return g.writePage(filepath.Join(dir, g.IndexFile), tag.ModulePath+" "+tag.Version.Original(), versionTmpl, data)
}

// readReadme returns the raw README content for a module, using the ReadmeReader
// if set, otherwise falling back to reading directly from the git tag.
func (g *HTMLGenerator) readReadme(modulePath, tag string) string {
	if g.ReadmeReader != nil {
		return g.ReadmeReader(modulePath, tag)
	}
	return gitReadmeContent(g.Git, tag, modulePath)
}

var md = goldmark.New(
	goldmark.WithExtensions(extension.Table),
)

func renderMarkdown(content string) template.HTML {
	if content == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := md.Convert([]byte(content), &buf); err != nil {
		return ""
	}
	return template.HTML(buf.String())
}

func (g *HTMLGenerator) writePage(path, title string, contentTmpl *template.Template, data any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var contentBuf bytes.Buffer
	if err := contentTmpl.Execute(&contentBuf, data); err != nil {
		return err
	}
	var pageBuf bytes.Buffer
	if err := g.baseTmpl.Execute(&pageBuf, basePage{
		Title:   title,
		Content: template.HTML(contentBuf.String()),
	}); err != nil {
		return err
	}
	return os.WriteFile(path, pageBuf.Bytes(), 0o644)
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

var rootTmpl = template.Must(template.New("root").Parse(`<h1>Terraform Module Registry</h1>
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
`))

var moduleTmpl = template.Must(template.New("module").Funcs(template.FuncMap{
	"relRoot": relRoot,
}).Parse(`<p><a href="{{relRoot .ModulePath}}">← Back to registry</a></p>
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
`))

var versionTmpl = template.Must(template.New("version").Funcs(template.FuncMap{
	"relRootVersion": relRootVersion,
}).Parse(`<p><a href="../">← {{.ModulePath}}</a> · <a href="{{relRootVersion .ModulePath}}">Registry</a></p>
<h1>{{.ModulePath}} <span class="version">{{.Version}}</span></h1>
<p>Download: <a href="{{.ArchiveURL}}" download="{{.ArchiveDownloadName}}">{{.ArchiveURL}}</a></p>
{{if .ReadmeHTML}}
<hr>
<div class="readme">{{.ReadmeHTML}}</div>
{{end}}
`))

