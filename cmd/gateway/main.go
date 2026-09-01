// Command gateway is the HTTP entry point for the booking assistant. It
// receives channel webhooks, normalises them and hands them to asynchronous
// processing.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/config"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/httpserver"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/logging"
)

// version identifies the running build. It is overwritten at link time with
// -ldflags "-X main.version=<revision>" and stays "dev" for local builds.
var version = "dev"

func main() {
	// Install a structured logger before anything else can fail. Configuration
	// loading is itself a source of fatal errors, and those entries need the
	// same JSON shape as the rest of the process or they cannot be alerted on.
	slog.SetDefault(logging.New(os.Stdout, slog.LevelInfo))

	if err := run(); err != nil {
		slog.Error("gateway exited with an error", "error", err)
		os.Exit(1)
	}
}

// run holds the real body of main. main itself only translates an error into an
// exit code, because os.Exit skips deferred functions and would otherwise
// silently drop cleanup.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := logging.New(os.Stdout, cfg.LogLevel).With("version", version)

	// Replace the bootstrap logger with the configured one. Everything that
	// reaches for slog directly, including the standard library's log package,
	// then writes in the same format at the same level.
	slog.SetDefault(logger)

	// NotifyContext cancels ctx when the process is asked to stop. Cloud Run
	// sends SIGTERM before reclaiming an instance; Ctrl-C sends SIGINT.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, err := httpserver.New(ctx, cfg.Addr(), routes(logger, version), logger, cfg.ShutdownTimeout)
	if err != nil {
		return err
	}

	logger.Info("gateway starting", "env", string(cfg.Env), "addr", srv.Addr())

	return srv.Run(ctx)
}
