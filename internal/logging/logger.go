// Package logging configures the structured logger used across tfr-static.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

// New returns a *slog.Logger that writes to w. It emits text for a tty-like
// sink and JSON otherwise. The level is read from TFR_LOG_LEVEL (debug, info,
// warn, error); unknown values fall back to info.
func New(w io.Writer) *slog.Logger {
	level := parseLevel(os.Getenv("TFR_LOG_LEVEL"))
	handlerOpts := &slog.HandlerOptions{Level: level}

	if isTerminal(w) {
		return slog.New(slog.NewTextHandler(w, handlerOpts))
	}
	return slog.New(slog.NewJSONHandler(w, handlerOpts))
}

// Default returns a logger that writes text to stderr.
func Default() *slog.Logger {
	return New(os.Stderr)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

type ctxKey struct{}

// WithLogger attaches logger to ctx.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, logger)
}

// FromContext returns the logger attached to ctx, or a default one if none is
// attached.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return Default()
}
