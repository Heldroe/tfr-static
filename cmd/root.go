package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/Heldroe/tfr-static/internal/config"
	"github.com/Heldroe/tfr-static/internal/git"
)

var cfg config.Config

var rootCmd = &cobra.Command{
	Use:   "tfrs",
	Short: "Static Terraform module registry generator",
	Long: `tfrs generates static files for hosting a Terraform module registry
on object storage (e.g. S3). It uses git tags as the source of truth for
module versions and generates registry-protocol-compliant files.`,
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

	var fileBaseURL, fileMainBranch, fileOutputDir, fileModulesPath *string
	if fileCfg != nil {
		fileBaseURL = fileCfg.BaseURL
		fileMainBranch = fileCfg.MainBranch
		fileOutputDir = fileCfg.OutputDir
		fileModulesPath = fileCfg.ModulesPath
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
