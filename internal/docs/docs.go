package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/terraform-docs/terraform-docs/format"
	"github.com/terraform-docs/terraform-docs/print"
	"github.com/terraform-docs/terraform-docs/terraform"
)

const (
	beginMarker = "<!-- BEGIN_TF_DOCS -->"
	endMarker   = "<!-- END_TF_DOCS -->"
)

// Generate produces terraform-docs markdown table output for a module directory.
// It looks for .terraform-docs.yml in moduleDir itself.
func Generate(moduleDir string) (string, error) {
	return GenerateWithConfig(moduleDir, moduleDir)
}

// GenerateWithConfig produces terraform-docs markdown table output.
// moduleDir contains the .tf files to document. configDir is where to look
// for .terraform-docs.yml/.yaml configuration.
func GenerateWithConfig(moduleDir, configDir string) (string, error) {
	if !hasTFFiles(moduleDir) {
		return "", nil
	}

	config := print.DefaultConfig()
	config.ModuleRoot = moduleDir
	config.Formatter = "markdown table"

	if cfgFile := findConfig(configDir); cfgFile != "" {
		if fileCfg, err := print.ReadConfig(filepath.Dir(cfgFile), filepath.Base(cfgFile)); err == nil {
			fileCfg.ModuleRoot = moduleDir
			if fileCfg.Formatter == "" {
				fileCfg.Formatter = "markdown table"
			}
			config = fileCfg
		}
	}

	config.Parse()

	module, err := terraform.LoadWithOptions(config)
	if err != nil {
		return "", fmt.Errorf("loading terraform module from %s: %w", moduleDir, err)
	}

	formatter, err := format.New(config)
	if err != nil {
		return "", fmt.Errorf("creating formatter: %w", err)
	}

	if err := formatter.Generate(module); err != nil {
		return "", fmt.Errorf("generating docs: %w", err)
	}

	return strings.TrimSpace(formatter.Content()), nil
}

// InjectIntoReadme injects terraform-docs output into a README string.
// If the README contains BEGIN/END markers, content between them is replaced.
// Otherwise the docs are appended.
func InjectIntoReadme(readme, docs string) string {
	if docs == "" {
		return readme
	}

	beginIdx := strings.Index(readme, beginMarker)
	endIdx := strings.Index(readme, endMarker)

	if beginIdx != -1 && endIdx != -1 && endIdx > beginIdx {
		return readme[:beginIdx+len(beginMarker)] + "\n" + docs + "\n" + readme[endIdx:]
	}

	if readme == "" {
		return docs
	}
	return readme + "\n\n" + docs
}

func findConfig(configDir string) string {
	home, _ := os.UserHomeDir()

	dirs := []string{
		configDir,
		filepath.Join(configDir, ".config"),
	}
	if cwd, err := os.Getwd(); err == nil {
		dirs = append(dirs, cwd, filepath.Join(cwd, ".config"))
	}
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".tfdocs.d"))
	}

	for _, dir := range dirs {
		for _, name := range []string{".terraform-docs.yml", ".terraform-docs.yaml"} {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

func hasTFFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".tf" {
			return true
		}
	}
	return false
}
