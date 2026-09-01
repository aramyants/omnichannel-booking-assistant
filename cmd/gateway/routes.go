package main

import (
	"encoding/json"
	"net/http"
)

// routes builds the gateway's HTTP handler. Channel webhook endpoints will be
// registered here as each provider adapter lands.
func routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)
	return mux
}

type healthResponse struct {
	Status string `json:"status"`
}

// handleHealth reports that the process is running and able to serve requests.
// It deliberately performs no dependency checks: Cloud Run uses this to decide
// whether to route traffic, and failing it because a downstream API is slow
// would take the whole service down instead of degrading one feature.
func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
}
