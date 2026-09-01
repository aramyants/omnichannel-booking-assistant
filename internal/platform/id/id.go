// Package id generates the identifiers this system assigns to its own records.
//
// Identifiers are UUIDv7, which embeds a millisecond timestamp in its leading
// bits. That makes them sort chronologically as strings, so records read back
// in creation order without a secondary index, and documents written to a
// key-ordered store land near each other instead of scattering.
package id

import "github.com/google/uuid"

// New returns a new time-ordered identifier.
//
// Generation cannot meaningfully fail: uuid.Must panics only if the system
// entropy source is unavailable, which is not a condition any caller could
// recover from.
func New() string {
	return uuid.Must(uuid.NewV7()).String()
}
