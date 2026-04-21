package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Heldroe/tfr-static/cmd"
	"github.com/Heldroe/tfr-static/internal/logging"
)

func main() {
	logger := logging.Default()
	slog.SetDefault(logger)

	ctx := logging.WithLogger(context.Background(), logger)
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd.Execute(ctx)
}
