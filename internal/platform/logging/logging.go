// Package logging builds the structured logger used across the application.
package logging

import (
	"io"
	"log/slog"
)

// New returns a JSON logger writing to w at or above level.
//
// The handler renames slog's "level" key to "severity" and its "msg" key to
// "message", which is the shape Google Cloud Logging parses natively. Without
// this every entry would arrive as unstructured text at default severity,
// making it impossible to filter for errors in the console.
func New(w io.Writer, level slog.Level) *slog.Logger {
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: cloudLoggingKeys,
	})
	return slog.New(handler)
}

func cloudLoggingKeys(groups []string, a slog.Attr) slog.Attr {
	// Only rewrite top-level keys; nested attributes keep their own names.
	if len(groups) > 0 {
		return a
	}

	switch a.Key {
	case slog.LevelKey:
		a.Key = "severity"
	case slog.MessageKey:
		a.Key = "message"
	case slog.TimeKey:
		a.Key = "time"
	}
	return a
}
