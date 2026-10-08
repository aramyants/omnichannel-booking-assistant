// Package httpserver runs an HTTP server with explicit timeouts and a bounded
// graceful shutdown. It knows nothing about routes; callers supply the handler.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Timeouts applied to every connection. Go's zero-value http.Server has none
// of these, which leaves it vulnerable to clients that open a connection and
// then send data arbitrarily slowly.
const (
	// readHeaderTimeout caps how long a client may take to send request
	// headers. This is the single most important defence against slow-client
	// attacks holding connections open.
	readHeaderTimeout = 5 * time.Second

	// readTimeout caps headers plus body.
	readTimeout = 15 * time.Second

	// writeTimeout caps how long a handler may take to write its response. It
	// must exceed the slowest legitimate handler or responses are truncated.
	// Customer turns allow two minutes for model/tool work. Leave time for
	// transport feedback and claim cleanup before writing the webhook response.
	writeTimeout = 3 * time.Minute

	// idleTimeout caps how long a keep-alive connection may sit unused.
	idleTimeout = 60 * time.Second
)

// Server owns a listener and the http.Server bound to it.
type Server struct {
	srv             *http.Server
	listener        net.Listener
	logger          *slog.Logger
	shutdownTimeout time.Duration
	cancelRequests  context.CancelFunc
}

// New binds addr and returns a Server ready to run.
//
// The listener is opened here rather than in Run so that an unavailable port
// fails immediately and so that Addr reports the resolved port, which matters
// when addr requests port 0. ctx bounds the bind itself; it does not control
// the lifetime of the returned server, which Run governs.
func New(ctx context.Context, addr string, handler http.Handler, logger *slog.Logger, shutdownTimeout time.Duration) (*Server, error) {
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}
	requestCtx, cancelRequests := context.WithCancel(context.WithoutCancel(ctx))

	return &Server{
		srv: &http.Server{
			Handler:           handler,
			BaseContext:       func(net.Listener) context.Context { return requestCtx },
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
			ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		},
		listener:        listener,
		logger:          logger,
		shutdownTimeout: shutdownTimeout,
		cancelRequests:  cancelRequests,
	}, nil
}

// Addr returns the address the server is bound to, with the port resolved.
func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

// Run serves requests until ctx is cancelled or the server fails.
//
// On cancellation it stops accepting new connections, cancels in-flight work
// and waits up to the configured shutdown timeout for handlers to clean up and
// return a retryable response. It returns nil when that drain finishes cleanly.
func (s *Server) Run(ctx context.Context) error {
	defer s.cancelRequests()
	// Buffered so the goroutine can always send and exit, even if nobody is
	// left to receive. An unbuffered channel here would leak the goroutine.
	serveErr := make(chan error, 1)

	go func() {
		// Serve returns ErrServerClosed on a deliberate shutdown, which is a
		// normal outcome rather than a failure.
		if err := s.srv.Serve(s.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("serve: %w", err)
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	s.logger.Info("draining connections", "timeout", s.shutdownTimeout.String())
	// Cloud Run allows ten seconds after SIGTERM. Slow provider calls must
	// observe cancellation now, leaving the drain window for durable cleanup.
	s.cancelRequests()

	// ctx is already cancelled, so it cannot bound the drain. WithoutCancel
	// keeps any values carried on ctx while dropping its cancellation.
	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.shutdownTimeout)
	defer cancel()

	if err := s.srv.Shutdown(drainCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	s.logger.Info("server stopped")
	return nil
}
