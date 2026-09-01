package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// capturingLogger returns a logger writing JSON into buf at debug level, so
// tests can assert on the fields an entry carries.
func capturingLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func decodeEntries(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()

	var entries []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func TestChainAppliesOutermostFirst(t *testing.T) {
	var order []string

	mark := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	handler := Chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { order = append(order, "handler") }),
		mark("first"), mark("second"),
	)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	want := []string{"first", "second", "handler"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestRequestID(t *testing.T) {
	tests := map[string]struct {
		headers map[string]string
		want    string
	}{
		"reuses a caller supplied id": {
			headers: map[string]string{headerRequestID: "abc-123"},
			want:    "abc-123",
		},
		"takes the trace from the cloud run header": {
			headers: map[string]string{headerCloudTrace: "105445aa7843bc8b/1;o=1"},
			want:    "105445aa7843bc8b",
		},
		"accepts a cloud trace header without a span": {
			headers: map[string]string{headerCloudTrace: "105445aa7843bc8b"},
			want:    "105445aa7843bc8b",
		},
		"prefers the request id over the trace": {
			headers: map[string]string{headerRequestID: "abc-123", headerCloudTrace: "trace/1"},
			want:    "abc-123",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var seen string
			handler := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				seen = RequestIDFromContext(r.Context())
			}))

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if seen != tt.want {
				t.Errorf("context id = %q, want %q", seen, tt.want)
			}
			if got := rec.Header().Get(headerRequestID); got != tt.want {
				t.Errorf("response header = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRequestIDGeneratesWhenAbsent(t *testing.T) {
	var first, second string
	handler := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if first == "" {
			first = RequestIDFromContext(r.Context())
			return
		}
		second = RequestIDFromContext(r.Context())
	}))

	for range 2 {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	}

	if len(first) != 32 {
		t.Errorf("generated id = %q, want 32 hex characters", first)
	}
	if first == second {
		t.Error("two requests were given the same generated id")
	}
}

func TestRequestIDFromContextWithoutMiddleware(t *testing.T) {
	if got := RequestIDFromContext(t.Context()); got != "" {
		t.Errorf("RequestIDFromContext = %q, want empty string", got)
	}
}

func TestRecoverReturns500AndLogsStack(t *testing.T) {
	var buf bytes.Buffer

	handler := Recover(capturingLogger(&buf))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	entries := decodeEntries(t, &buf)
	if len(entries) != 1 {
		t.Fatalf("logged %d entries, want 1", len(entries))
	}
	entry := entries[0]
	if entry["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", entry["level"])
	}
	if entry["panic"] != "boom" {
		t.Errorf("panic = %v, want boom", entry["panic"])
	}
	if stack, _ := entry["stack"].(string); stack == "" {
		t.Error("entry carries no stack")
	}
}

// TestRecoverRepanicsOnAbort guards the one panic value that must not be
// swallowed: net/http uses it to signal a deliberately abandoned response.
func TestRecoverRepanicsOnAbort(t *testing.T) {
	var buf bytes.Buffer

	handler := Recover(capturingLogger(&buf))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		switch v := recover().(type) {
		case nil:
			t.Error("ErrAbortHandler was swallowed, want it to propagate")
		case error:
			if !errors.Is(v, http.ErrAbortHandler) {
				t.Errorf("propagated %v, want http.ErrAbortHandler", v)
			}
		default:
			t.Errorf("propagated %v, want http.ErrAbortHandler", v)
		}
		if buf.Len() != 0 {
			t.Errorf("a deliberate abort was logged as a panic: %s", buf.String())
		}
	}()

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
}

func TestRequestLoggerRecordsOutcome(t *testing.T) {
	var buf bytes.Buffer

	handler := Chain(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}),
		RequestID,
		RequestLogger(capturingLogger(&buf)),
	)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook/telegram", nil))

	entries := decodeEntries(t, &buf)
	if len(entries) != 1 {
		t.Fatalf("logged %d entries, want 1", len(entries))
	}
	entry := entries[0]

	if entry["method"] != http.MethodPost {
		t.Errorf("method = %v, want POST", entry["method"])
	}
	if entry["path"] != "/webhook/telegram" {
		t.Errorf("path = %v, want /webhook/telegram", entry["path"])
	}
	if entry["status"] != float64(http.StatusCreated) {
		t.Errorf("status = %v, want 201", entry["status"])
	}
	if entry["bytes"] != float64(len(`{"ok":true}`)) {
		t.Errorf("bytes = %v, want %d", entry["bytes"], len(`{"ok":true}`))
	}
	if id, _ := entry["request_id"].(string); id == "" {
		t.Error("entry carries no request_id")
	}
}

// TestRequestLoggerNeverLogsQueryStrings guards a deliberate omission: webhook
// URLs carry provider verification tokens.
func TestRequestLoggerNeverLogsQueryStrings(t *testing.T) {
	var buf bytes.Buffer

	handler := RequestLogger(capturingLogger(&buf))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/hook?verify_token=supersecret", nil))

	if strings.Contains(buf.String(), "supersecret") {
		t.Errorf("query string leaked into the log: %s", buf.String())
	}
}

func TestRequestLoggerLevels(t *testing.T) {
	tests := map[string]struct {
		status int
		path   string
		want   string
	}{
		"success is info":             {status: http.StatusOK, path: "/webhook", want: "INFO"},
		"client error is warn":        {status: http.StatusBadRequest, path: "/webhook", want: "WARN"},
		"server error is error":       {status: http.StatusBadGateway, path: "/webhook", want: "ERROR"},
		"quiet path is debug":         {status: http.StatusOK, path: "/health", want: "DEBUG"},
		"quiet path error still errs": {status: http.StatusInternalServerError, path: "/health", want: "ERROR"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer

			handler := RequestLogger(capturingLogger(&buf), "/health")(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tt.status)
				}),
			)
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, nil))

			entries := decodeEntries(t, &buf)
			if len(entries) != 1 {
				t.Fatalf("logged %d entries, want 1", len(entries))
			}
			if entries[0]["level"] != tt.want {
				t.Errorf("level = %v, want %v", entries[0]["level"], tt.want)
			}
		})
	}
}

func TestMaxBytesRejectsOversizedBody(t *testing.T) {
	const limit = 16

	readErr := make(chan error, 1)
	handler := MaxBytes(limit)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		readErr <- err
	}))

	body := strings.NewReader(strings.Repeat("x", limit+1))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", body))

	err := <-readErr
	if err == nil {
		t.Fatal("reading an oversized body succeeded, want an error")
	}

	var maxErr *http.MaxBytesError
	if !errors.As(err, &maxErr) {
		t.Errorf("error = %v, want *http.MaxBytesError", err)
	}
}

func TestMaxBytesAllowsBodyAtLimit(t *testing.T) {
	const limit = 16

	readErr := make(chan error, 1)
	handler := MaxBytes(limit)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		readErr <- err
	}))

	body := strings.NewReader(strings.Repeat("x", limit))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", body))

	if err := <-readErr; err != nil {
		t.Errorf("reading a body at the limit failed: %v", err)
	}
}

// TestResponseWriterSupportsFlushing guards the Unwrap method: without it,
// wrapping the writer silently disables streaming for every handler.
func TestResponseWriterSupportsFlushing(t *testing.T) {
	var buf bytes.Buffer

	flushed := make(chan bool, 1)
	handler := RequestLogger(capturingLogger(&buf))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("partial"))
		flushed <- http.NewResponseController(w).Flush() == nil
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	if !<-flushed {
		t.Error("Flush through the wrapper failed, so Unwrap is not being honoured")
	}
}
