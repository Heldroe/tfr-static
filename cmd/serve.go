package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/Heldroe/tfr-static/internal/git"
	"github.com/Heldroe/tfr-static/internal/logging"
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
		return runServeDev(cmd)
	}
	return runServeStatic(cmd.Context())
}

func runServeStatic(ctx context.Context) error {
	logger := logging.FromContext(ctx)
	logger.Info("serving static registry", "dir", cfg.OutputDir, "addr", serveAddr)
	return runHTTPServer(ctx, serveAddr, http.FileServer(http.Dir(cfg.OutputDir)))
}

func runServeDev(cmd *cobra.Command) error {
	ctx := cmd.Context()
	logger := logging.FromContext(ctx)
	gitRunner := git.NewRunner(cfg.RepoPath)

	repoRoot, err := gitRunner.TopLevel(ctx)
	if err != nil {
		return fmt.Errorf("resolving repository root: %w", err)
	}

	// Default HTML to true in dev mode, but let the user turn it off with --html=false.
	htmlEnabled := true
	if f := cmd.Flags().Lookup("html"); f != nil && f.Changed {
		htmlEnabled = cfg.HTML
	}

	dev := registry.NewDevServer(gitRunner, repoRoot, cfg.ModulesPath)
	dev.HTMLEnabled = htmlEnabled

	logger.Info("dev registry ready",
		"repo", repoRoot,
		"addr", serveAddr,
		"html", htmlEnabled,
	)
	logger.Info("modules are served from the current working tree (including uncommitted changes)")
	return runHTTPServer(ctx, serveAddr, dev.Handler())
}

func runHTTPServer(ctx context.Context, addr string, handler http.Handler) error {
	logger := logging.FromContext(ctx)
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	errCh := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutting down HTTP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}
