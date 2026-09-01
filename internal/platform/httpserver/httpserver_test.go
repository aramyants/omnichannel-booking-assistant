package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewRejectsUnavailableAddress(t *testing.T) {
	_, err := New("127.0.0.1:99999", http.NewServeMux(), discardLogger(), time.Second)
	if err == nil {
		t.Fatal("New() succeeded on an invalid port, want error")
	}
}

func TestRunServesAndStopsOnCancel(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	srv, err := New("127.0.0.1:0", mux, discardLogger(), 5*time.Second)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(ctx) }()

	resp, err := http.Get("http://" + srv.Addr() + "/ping")
	if err != nil {
		t.Fatalf("GET /ping returned error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	cancel()

	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("Run() returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after context cancellation")
	}
}

// TestRunDrainsInFlightRequest is the reason graceful shutdown exists: a
// request already being served must finish rather than have its connection cut.
func TestRunDrainsInFlightRequest(t *testing.T) {
	released := make(chan struct{})
	started := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-released
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("finished"))
	})

	srv, err := New("127.0.0.1:0", mux, discardLogger(), 5*time.Second)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(ctx) }()

	type result struct {
		body string
		err  error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + srv.Addr() + "/slow")
		if err != nil {
			got <- result{err: err}
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		got <- result{body: string(body), err: err}
	}()

	<-started // the handler is now mid-flight
	cancel()  // ask the server to shut down
	close(released)

	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("in-flight request failed during shutdown: %v", r.err)
		}
		if r.body != "finished" {
			t.Errorf("body = %q, want %q", r.body, "finished")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight request never completed")
	}

	if err := <-runErr; err != nil {
		t.Errorf("Run() returned error: %v", err)
	}
}
