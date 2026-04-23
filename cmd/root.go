package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/Heldroe/tfr-static/internal/config"
	"github.com/Heldroe/tfr-static/internal/git"
)

var cfg config.Config

// Version is set at build time via ldflags:
//
//	go build -ldflags "-X github.com/Heldroe/tfr-static/cmd.Version=1.0.0"
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:   "tfr-static",
	Short: "Static Terraform module registry generator",
	Long: fmt.Sprintf(`tfr-static %s — Static Terraform module registry generator

Generates static files for hosting a Terraform module registry
on object storage (e.g. S3). It uses git tags as the source of truth for
module versions and generates registry-protocol-compliant files.`, Version),
	Version:           Version,
	PersistentPreRunE: loadConfig,
	SilenceUsage:      true,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfg.OutputDir, "output-dir", "", "output directory for generated files")
	rootCmd.PersistentFlags().StringVar(&cfg.BaseURL, "base-url", "", "base URL for the registry (e.g. https://registry.example.com)")
	rootCmd.PersistentFlags().StringVar(&cfg.RepoPath, "repo", "", "path to the git repository")
	rootCmd.PersistentFlags().StringVar(&cfg.MainBranch, "main-branch", "", "expected main branch name")
	rootCmd.PersistentFlags().StringVar(&cfg.ModulesPath, "modules-path", "", "path prefix for modules.v1 in service discovery")
	rootCmd.PersistentFlags().StringVar(&cfg.Namespace, "namespace", "", "default namespace for auto-derived registry paths (default \"modules\")")
}

// loadConfig applies configuration with precedence:
// CLI flags > env vars > config file > defaults
func loadConfig(cmd *cobra.Command, args []string) error {
	// Resolve repo path first (needed to find the config file)
	cfg.RepoPath = resolveValue(
		flagIfChanged(cmd, "repo"),
		os.Getenv("TFR_REPO_PATH"),
		nil,
		".",
	)

	// Resolve the git repo root to find the config file
	repoRoot, err := git.NewRunner(cfg.RepoPath).TopLevel()
	if err != nil {
		// Not inside a git repo — skip config file loading
		repoRoot = cfg.RepoPath
	}

	// Load config file from the repo root
	fileCfg, err := config.LoadFileConfig(repoRoot)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	var fileBaseURL, fileMainBranch, fileOutputDir, fileModulesPath, fileHTMLIndex, fileNamespace *string
	var fileInvalidationFile, fileInvalidationFormat, fileInvalidationBaseURL, fileHTMLBase *string
	var fileHTML, fileGzip, fileTerraformDocs *bool
	var fileInvalidationFullURL, fileInvalidationURLEncode, fileInvalidationDirs *bool
	if fileCfg != nil {
		fileBaseURL = fileCfg.BaseURL
		fileMainBranch = fileCfg.MainBranch
		fileOutputDir = fileCfg.OutputDir
		fileModulesPath = fileCfg.ModulesPath
		fileHTMLIndex = fileCfg.HTMLIndex
		fileHTML = fileCfg.HTML
		fileGzip = fileCfg.Gzip
		fileTerraformDocs = fileCfg.TerraformDocs
		fileInvalidationFile = fileCfg.InvalidationFile
		fileInvalidationFormat = fileCfg.InvalidationFormat
		fileInvalidationFullURL = fileCfg.InvalidationFullURL
		fileInvalidationBaseURL = fileCfg.InvalidationBaseURL
		fileInvalidationURLEncode = fileCfg.InvalidationURLEncode
		fileInvalidationDirs = fileCfg.InvalidationDirs
		fileHTMLBase = fileCfg.HTMLBase
		fileNamespace = fileCfg.Namespace
	}

	cfg.BaseURL = resolveValue(
		flagIfChanged(cmd, "base-url"),
		os.Getenv("TFR_BASE_URL"),
		fileBaseURL,
		"",
	)
	cfg.MainBranch = resolveValue(
		flagIfChanged(cmd, "main-branch"),
		os.Getenv("TFR_MAIN_BRANCH"),
		fileMainBranch,
		"main",
	)
	cfg.OutputDir = resolveValue(
		flagIfChanged(cmd, "output-dir"),
		os.Getenv("TFR_OUTPUT_DIR"),
		fileOutputDir,
		"target",
	)
	cfg.ModulesPath = resolveValue(
		flagIfChanged(cmd, "modules-path"),
		os.Getenv("TFR_MODULES_PATH"),
		fileModulesPath,
		"/",
	)
	cfg.HTML = resolveBoolValue(
		boolFlagIfChanged(cmd, "html"),
		os.Getenv("TFR_HTML"),
		fileHTML,
		false,
	)
	cfg.HTMLIndex = resolveValue(
		flagIfChanged(cmd, "html-index"),
		os.Getenv("TFR_HTML_INDEX"),
		fileHTMLIndex,
		"index.html",
	)
	cfg.Gzip = resolveBoolValue(
		boolFlagIfChanged(cmd, "gzip"),
		os.Getenv("TFR_GZIP"),
		fileGzip,
		false,
	)
	cfg.TerraformDocs = resolveBoolValue(
		boolFlagIfChanged(cmd, "terraform-docs"),
		os.Getenv("TFR_TERRAFORM_DOCS"),
		fileTerraformDocs,
		false,
	)
	cfg.InvalidationFile = resolveValue(
		flagIfChanged(cmd, "invalidation-file"),
		os.Getenv("TFR_INVALIDATION_FILE"),
		fileInvalidationFile,
		"",
	)
	cfg.InvalidationFormat = resolveValue(
		flagIfChanged(cmd, "invalidation-format"),
		os.Getenv("TFR_INVALIDATION_FORMAT"),
		fileInvalidationFormat,
		"txt",
	)
	cfg.InvalidationFullURL = resolveBoolValue(
		boolFlagIfChanged(cmd, "invalidation-full-url"),
		os.Getenv("TFR_INVALIDATION_FULL_URL"),
		fileInvalidationFullURL,
		false,
	)
	cfg.InvalidationBaseURL = resolveValue(
		flagIfChanged(cmd, "invalidation-base-url"),
		os.Getenv("TFR_INVALIDATION_BASE_URL"),
		fileInvalidationBaseURL,
		"",
	)
	cfg.InvalidationURLEncode = resolveBoolValue(
		boolFlagIfChanged(cmd, "invalidation-url-encode"),
		os.Getenv("TFR_INVALIDATION_URL_ENCODE"),
		fileInvalidationURLEncode,
		false,
	)
	cfg.InvalidationDirs = resolveBoolValue(
		boolFlagIfChanged(cmd, "invalidation-dirs"),
		os.Getenv("TFR_INVALIDATION_DIRS"),
		fileInvalidationDirs,
		false,
	)
	cfg.HTMLBase = resolveValue(
		flagIfChanged(cmd, "html-base"),
		os.Getenv("TFR_HTML_BASE"),
		fileHTMLBase,
		"",
	)
	cfg.Namespace = resolveValue(
		flagIfChanged(cmd, "namespace"),
		os.Getenv("TFR_NAMESPACE"),
		fileNamespace,
		"modules",
	)

	cfg.ModuleMappings = make(map[string]string)
	if fileCfg != nil {
		for _, m := range fileCfg.Modules {
			cfg.ModuleMappings[m.DirPath] = m.RegistryPath
		}
	}

	return nil
}

// flagIfChanged returns a pointer to the flag value if it was explicitly set
// by the user, or nil otherwise.
func flagIfChanged(cmd *cobra.Command, name string) *string {
	f := cmd.Flags().Lookup(name)
	if f == nil {
		return nil
	}
	// Check the full chain: the command itself and all parents
	if f.Changed {
		v := f.Value.String()
		return &v
	}
	return nil
}

// boolFlagIfChanged returns a pointer to the flag's bool value if it was
// explicitly set by the user, or nil otherwise.
func boolFlagIfChanged(cmd *cobra.Command, name string) *bool {
	f := cmd.Flags().Lookup(name)
	if f == nil {
		return nil
	}
	if f.Changed {
		v, err := strconv.ParseBool(f.Value.String())
		if err != nil {
			return nil
		}
		return &v
	}
	return nil
}

// resolveBoolValue picks the first set value in precedence order:
// CLI flag > env var > config file > default.
func resolveBoolValue(flag *bool, envVal string, fileVal *bool, defaultVal bool) bool {
	if flag != nil {
		return *flag
	}
	if envVal != "" {
		v, err := strconv.ParseBool(envVal)
		if err == nil {
			return v
		}
	}
	if fileVal != nil {
		return *fileVal
	}
	return defaultVal
}

// resolveValue picks the first non-empty value in precedence order:
// CLI flag (pointer, nil if not set) > env var > config file (pointer) > default.
func resolveValue(flag *string, envVal string, fileVal *string, defaultVal string) string {
	if flag != nil && *flag != "" {
		return *flag
	}
	if envVal != "" {
		return envVal
	}
	if fileVal != nil && *fileVal != "" {
		return *fileVal
	}
	return defaultVal
}
