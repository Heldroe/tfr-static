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
	BaseURL              *string `hcl:"base_url,optional"`
	MainBranch           *string `hcl:"main_branch,optional"`
	OutputDir            *string `hcl:"output_dir,optional"`
	ModulesPath          *string `hcl:"modules_path,optional"`
	HTML                 *bool   `hcl:"html,optional"`
	HTMLIndex            *string `hcl:"html_index,optional"`
	Gzip                 *bool   `hcl:"gzip,optional"`
	TerraformDocs        *bool   `hcl:"terraform_docs,optional"`
	InvalidationFile     *string `hcl:"invalidation_file,optional"`
	InvalidationFormat   *string `hcl:"invalidation_format,optional"`
	InvalidationFullURL  *bool   `hcl:"invalidation_full_url,optional"`
	InvalidationBaseURL  *string `hcl:"invalidation_base_url,optional"`
	InvalidationURLEncode *bool  `hcl:"invalidation_url_encode,optional"`
	InvalidationDirs      *bool  `hcl:"invalidation_dirs,optional"`
}

// Config holds the resolved configuration for tfr-static.
type Config struct {
	OutputDir             string
	BaseURL               string
	RepoPath              string
	MainBranch            string
	ModulesPath           string
	HTML                  bool
	HTMLIndex             string
	Gzip                  bool
	TerraformDocs         bool
	InvalidationFile      string
	InvalidationFormat    string
	InvalidationFullURL   bool
	InvalidationBaseURL   string
	InvalidationURLEncode bool
	InvalidationDirs      bool
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
