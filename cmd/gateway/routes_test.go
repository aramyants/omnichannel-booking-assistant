package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testRoutes() http.Handler {
	return (&gateway{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), version: "test"}).routes()
}

func TestHandleHealth(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	testRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got, want := rec.Header().Get("Content-Type"), "application/json"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}

	var body healthResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want %q", body.Status, "ok")
	}
	if body.Version != "test" {
		t.Errorf("version = %q, want %q", body.Version, "test")
	}
}

func TestHealthRejectsNonGET(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()

	testRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestUnknownRouteIsNotFound(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/nope", nil)
	rec := httptest.NewRecorder()

	testRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// TestEveryResponseCarriesARequestID checks that the middleware chain is
// actually wired into the mux, not just defined.
func TestEveryResponseCarriesARequestID(t *testing.T) {
	rec := httptest.NewRecorder()
	testRoutes().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health", nil))

	if got := rec.Header().Get("X-Request-Id"); got == "" {
		t.Error("response carries no X-Request-Id header")
	}
}

func TestRequestsAreLogged(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	rec := httptest.NewRecorder()
	gw := &gateway{logger: logger, version: "test"}
	gw.routes().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health", nil))

	if !strings.Contains(buf.String(), "request completed") {
		t.Errorf("no request log entry was written: %s", buf.String())
	}
}

// TestTelegramRouteIsAbsentWhenTheChannelIsOff checks that an unconfigured
// channel serves nothing, rather than serving an endpoint that fails.
func TestTelegramRouteIsAbsentWhenTheChannelIsOff(t *testing.T) {
	rec := httptest.NewRecorder()
	testRoutes().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/telegram", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestTelegramRouteIsServedWhenConfigured(t *testing.T) {
	gw := &gateway{
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		version: "test",
		telegram: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}

	rec := httptest.NewRecorder()
	gw.routes().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/telegram", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
