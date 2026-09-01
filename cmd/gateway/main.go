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

func main() {
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

	logger := logging.New(os.Stdout, cfg.LogLevel)

	// NotifyContext cancels ctx when the process is asked to stop. Cloud Run
	// sends SIGTERM before reclaiming an instance; Ctrl-C sends SIGINT.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, err := httpserver.New(cfg.Addr(), routes(), logger, cfg.ShutdownTimeout)
	if err != nil {
		return err
	}

	logger.Info("gateway starting", "env", string(cfg.Env), "addr", srv.Addr())

	return srv.Run(ctx)
}
