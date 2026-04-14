package cmd

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/spf13/cobra"
	"github.com/Heldroe/tfr-static/internal/git"
	"github.com/Heldroe/tfr-static/internal/registry"
)

var (
	serveAddr string
	serveDev  bool
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve the registry over HTTP",
	Long: `Start a local HTTP server for the registry.

In default mode, serves the generated static files from the output directory.

In dev mode (--dev), dynamically serves modules from the current working tree.
Every download returns the current state of the module directory, including
uncommitted changes, regardless of which version was requested. This lets you
swap the registry domain to localhost and test local changes without tagging.`,
	RunE: runServe,
}

func init() {
	defaultAddr := "localhost:8080"
	if env := os.Getenv("TFR_ADDR"); env != "" {
		defaultAddr = env
	}
	serveCmd.Flags().StringVar(&serveAddr, "addr", defaultAddr, "address to listen on")
	serveCmd.Flags().BoolVar(&serveDev, "dev", false, "dev mode: serve current working tree for all versions")
	serveCmd.Flags().Bool("html", false, "enable HTML documentation pages in dev mode")
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	if serveDev {
		return runServeDev()
	}
	return runServeStatic()
}

func runServeStatic() error {
	log.Printf("Serving static registry from %s on %s", cfg.OutputDir, serveAddr)
	return http.ListenAndServe(serveAddr, http.FileServer(http.Dir(cfg.OutputDir)))
}

func runServeDev() error {
	gitRunner := git.NewRunner(cfg.RepoPath)

	repoRoot, err := gitRunner.TopLevel()
	if err != nil {
		return fmt.Errorf("resolving repository root: %w", err)
	}

	dev := registry.NewDevServer(gitRunner, repoRoot, cfg.ModulesPath)
	dev.HTMLEnabled = true // always show HTML in dev mode

	log.Printf("Dev registry serving from %s on %s", repoRoot, serveAddr)
	log.Printf("Modules are served from the current working tree (including uncommitted changes)")
	log.Printf("All version requests return the current code regardless of version")
	log.Printf("HTML documentation enabled")
	return http.ListenAndServe(serveAddr, dev.Handler())
}
