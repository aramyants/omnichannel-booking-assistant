// Command gateway is the HTTP entry point for the booking assistant. It
// receives channel webhooks, normalises them and hands them to the application.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/messaging/telegram"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/config"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/httpserver"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/logging"
)

// version identifies the running build. It is overwritten at link time with
// -ldflags "-X main.version=<revision>" and stays "dev" for local builds.
var version = "dev"

// webhookRegistrationTimeout bounds the calls that tell providers where to
// deliver updates. They run during startup, so they must not be able to hang it.
const webhookRegistrationTimeout = 15 * time.Second

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

	gw := &gateway{logger: logger, version: version}
	senders := make(map[messaging.Provider]conversation.Sender)

	var telegramClient *telegram.Client
	if cfg.Telegram.Enabled() {
		var opts []telegram.ClientOption
		if cfg.Telegram.APIBaseURL != "" {
			opts = append(opts, telegram.WithBaseURL(cfg.Telegram.APIBaseURL))
			logger.Warn("using a non-default telegram api host", "host", cfg.Telegram.APIBaseURL)
		}
		telegramClient = telegram.NewClient(cfg.Telegram.BotToken, opts...)
		senders[messaging.ProviderTelegram] = telegramClient
	}

	conversations := conversation.NewService(senders, logger)

	if cfg.Telegram.Enabled() {
		gw.telegram = telegram.NewHandler(
			telegram.NewWebhook(cfg.Telegram.WebhookSecret),
			conversations,
			logger,
		)
	}

	srv, err := httpserver.New(ctx, cfg.Addr(), gw.routes(), logger, cfg.ShutdownTimeout)
	if err != nil {
		return err
	}

	// Registration happens after the listener is bound but before requests are
	// served. The socket already accepts connections at this point, so a
	// delivery arriving in the gap waits in the backlog rather than failing.
	if telegramClient != nil {
		registerTelegramWebhook(ctx, telegramClient, cfg, logger)
	}

	logger.Info("gateway starting",
		"env", string(cfg.Env),
		"addr", srv.Addr(),
		"channels", enabledChannels(cfg),
	)

	return srv.Run(ctx)
}

// registerTelegramWebhook points Telegram at this deployment.
//
// A failure here is logged rather than fatal. The webhook is very likely still
// registered from a previous deployment, so refusing to start over a transient
// Telegram outage would take a working service down.
func registerTelegramWebhook(ctx context.Context, client *telegram.Client, cfg config.Config, logger *slog.Logger) {
	if cfg.PublicBaseURL == "" {
		logger.Warn("not registering the telegram webhook because PUBLIC_BASE_URL is unset")
		return
	}

	callbackURL := cfg.PublicBaseURL + TelegramWebhookPath

	ctx, cancel := context.WithTimeout(ctx, webhookRegistrationTimeout)
	defer cancel()

	if err := client.SetWebhook(ctx, callbackURL, cfg.Telegram.WebhookSecret); err != nil {
		logger.Error("could not register the telegram webhook",
			"error", err, "callback_url", callbackURL)
		return
	}

	logger.Info("registered the telegram webhook", "callback_url", callbackURL)
}

func enabledChannels(cfg config.Config) []string {
	channels := []string{}
	if cfg.Telegram.Enabled() {
		channels = append(channels, string(messaging.ProviderTelegram))
	}
	return channels
}
