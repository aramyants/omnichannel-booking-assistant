package main

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/httpserver"
)

// maxRequestBody caps every request body. Provider webhooks are small; the
// largest documented payload across the channels we target is well under this.
const maxRequestBody = 1 << 20 // 1 MiB

const (
	// healthPath is logged at debug rather than info. Cloud Run probes it
	// continuously and those entries would otherwise bury real traffic.
	healthPath = "/health"

	// TelegramWebhookPath is where Telegram delivers updates. It is exported
	// because the same value is registered with Telegram at startup, and the
	// two must not be allowed to drift apart.
	TelegramWebhookPath = "/webhooks/telegram"
)

// gateway carries the dependencies the HTTP layer needs. Channel handlers are
// nil when that channel is not configured, and their routes are then not
// served at all rather than served and failing.
type gateway struct {
	logger   *slog.Logger
	version  string
	telegram http.Handler
}

func (g *gateway) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET "+healthPath, handleHealth(g.version))

	if g.telegram != nil {
		mux.Handle("POST "+TelegramWebhookPath, g.telegram)
	}

	// Outermost first. RequestID runs before the logger so every entry carries
	// a correlation id, and the logger runs outside Recover so a panic is still
	// reported as a completed request with status 500.
	return httpserver.Chain(mux,
		httpserver.RequestID,
		httpserver.RequestLogger(g.logger, healthPath),
		httpserver.Recover(g.logger),
		httpserver.MaxBytes(maxRequestBody),
	)
}

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// handleHealth reports that the process is running and able to serve requests.
//
// It deliberately performs no dependency checks. Cloud Run uses this endpoint
// to decide whether to route traffic, so failing it because Altegio or the AI
// provider is slow would take the whole service down instead of degrading the
// one feature that depends on them.
func handleHealth(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok", Version: version})
	}
}
