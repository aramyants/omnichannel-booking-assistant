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

// healthPath is logged at debug rather than info. Cloud Run probes it
// continuously and those entries would otherwise bury real traffic.
const healthPath = "/health"

// routes builds the gateway's HTTP handler. Channel webhook endpoints will be
// registered here as each provider adapter lands.
func routes(logger *slog.Logger, version string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET "+healthPath, handleHealth(version))

	// Outermost first. RequestID runs before the logger so every entry carries
	// a correlation id, and the logger runs outside Recover so a panic is still
	// reported as a completed request with status 500.
	return httpserver.Chain(mux,
		httpserver.RequestID,
		httpserver.RequestLogger(logger, healthPath),
		httpserver.Recover(logger),
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
