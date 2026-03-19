package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/hcl/v2/hclsimple"
)

const ConfigFileName = ".tfr-static.hcl"

// FileConfig represents the HCL config file structure.
type FileConfig struct {
	BaseURL     *string `hcl:"base_url,optional"`
	MainBranch  *string `hcl:"main_branch,optional"`
	OutputDir   *string `hcl:"output_dir,optional"`
	ModulesPath *string `hcl:"modules_path,optional"`
}

// Config holds the resolved configuration for tfr-static.
type Config struct {
	OutputDir   string
	BaseURL     string
	RepoPath    string
	MainBranch  string
	ModulesPath string // Path prefix for modules.v1 in service discovery (default: "/")
}

// LoadFileConfig reads the .tfr-static.hcl config file from the given directory.
// Returns nil (no error) if the file does not exist.
func LoadFileConfig(dir string) (*FileConfig, error) {
	path := filepath.Join(dir, ConfigFileName)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}

	var fc FileConfig
	if err := hclsimple.DecodeFile(path, nil, &fc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &fc, nil
}
