package id

import (
	"slices"
	"testing"
	"time"
)

func TestNewIsUnique(t *testing.T) {
	const count = 10000

	seen := make(map[string]struct{}, count)
	for range count {
		generated := New()
		if _, clash := seen[generated]; clash {
			t.Fatalf("generated %q twice", generated)
		}
		seen[generated] = struct{}{}
	}
}

// TestNewSortsChronologically is the reason for choosing UUIDv7 over v4:
// identifiers ordered as strings are also ordered by creation time, so records
// read back in order without a secondary index.
func TestNewSortsChronologically(t *testing.T) {
	var generated []string
	for range 5 {
		generated = append(generated, New())
		// UUIDv7 carries millisecond precision, so distinct timestamps need a
		// millisecond between them.
		time.Sleep(2 * time.Millisecond)
	}

	if !slices.IsSorted(generated) {
		t.Errorf("identifiers are not in creation order: %v", generated)
	}
}
