package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"time"
)

// Middleware wraps an http.Handler with behaviour that applies to every
// request passing through it.
type Middleware func(http.Handler) http.Handler

// Chain wraps h with the given middlewares, outermost first: the request
// enters through middlewares[0] and leaves through h.
func Chain(h http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

// contextKey is unexported so no other package can collide with the keys this
// one stores on a request context.
type contextKey int

const requestIDKey contextKey = iota

const (
	headerRequestID  = "X-Request-Id"
	headerCloudTrace = "X-Cloud-Trace-Context"
)

// RequestID gives every request a stable identifier, stores it on the request
// context and echoes it back in a response header.
//
// An identifier supplied by the caller, or the trace Cloud Run's load balancer
// attaches, is reused in preference to generating a new one, so a single
// customer message can be followed across every service that handles it.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(headerRequestID)
		if id == "" {
			id = traceIDFromCloudHeader(r.Header.Get(headerCloudTrace))
		}
		if id == "" {
			id = newRequestID()
		}

		w.Header().Set(headerRequestID, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// RequestIDFromContext returns the identifier assigned by RequestID, or an
// empty string if the request did not pass through that middleware.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// traceIDFromCloudHeader extracts the trace from Cloud Run's
// "TRACE_ID/SPAN_ID;o=1" header format.
func traceIDFromCloudHeader(header string) string {
	if header == "" {
		return ""
	}
	if i := strings.IndexByte(header, '/'); i >= 0 {
		return header[:i]
	}
	return header
}

func newRequestID() string {
	var b [16]byte
	// crypto/rand.Read always fills b and never reports an error; it panics if
	// the system entropy source is unavailable, which no handler could recover
	// from anyway.
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// RequestLogger logs one entry per completed request.
//
// Requests to quietPaths are logged at debug instead of info unless they fail,
// which keeps continuous health probes out of production logs without hiding
// errors on those paths.
//
// Query strings and bodies are never logged: webhook URLs carry provider
// verification tokens and bodies carry customer messages.
func RequestLogger(logger *slog.Logger, quietPaths ...string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			level := slog.LevelInfo
			switch {
			case rec.status >= http.StatusInternalServerError:
				level = slog.LevelError
			case rec.status >= http.StatusBadRequest:
				level = slog.LevelWarn
			case slices.Contains(quietPaths, r.URL.Path):
				level = slog.LevelDebug
			}

			logger.LogAttrs(r.Context(), level, "request completed",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int("bytes", rec.bytes),
				slog.Duration("duration", time.Since(start)),
				slog.String("request_id", RequestIDFromContext(r.Context())),
				// Behind a proxy this is the proxy's address. Resolving the real
				// client needs a trusted-proxy configuration we do not have yet.
				slog.String("remote_addr", r.RemoteAddr),
			)
		})
	}
}

// Recover turns a panicking handler into a 500 response and one structured log
// entry carrying the stack.
//
// net/http already recovers panics so that one bad request cannot stop the
// server, but it logs unstructured text and closes the connection without a
// response, which the caller sees as a broken connection rather than an error.
func Recover(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				panicValue := recover()
				if panicValue == nil {
					return
				}

				// ErrAbortHandler is the documented way for a handler to abandon
				// a response deliberately. net/http expects to receive it.
				if err, ok := panicValue.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(panicValue)
				}

				logger.LogAttrs(r.Context(), slog.LevelError, "handler panicked",
					slog.Any("panic", panicValue),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("request_id", RequestIDFromContext(r.Context())),
					slog.String("stack", string(debug.Stack())),
				)

				w.WriteHeader(http.StatusInternalServerError)
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// MaxBytes caps the size of a request body. Without a cap, one client can
// stream an unbounded body and exhaust the memory of whichever handler reads
// it. Reads past the limit fail, and MaxBytesReader closes the connection so
// the sender stops.
func MaxBytes(limit int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}

// responseWriter records the status code and response size. http.ResponseWriter
// exposes neither once they have been written, so observing them requires
// wrapping it.
type responseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (w *responseWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.status = status
		w.wroteHeader = true
	}
	// Always delegate, so net/http still reports a superfluous WriteHeader call
	// rather than having the wrapper hide the bug.
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	w.wroteHeader = true
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the writer underneath, so
// flushing, hijacking and per-request deadlines keep working through the
// wrapper. Without it, wrapping silently disables them.
func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
