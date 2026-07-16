package history

import (
	"testing"
	"time"
)

func TestDecodeTime(t *testing.T) {
	ref := time.Date(2026, 7, 16, 10, 30, 0, 0, time.UTC)
	rfc := ref.Format(time.RFC3339)

	// time.Time passes through (PostgreSQL path).
	if got := decodeTime(ref); !got.Equal(ref) {
		t.Fatalf("decodeTime(time.Time) = %v, want %v", got, ref)
	}
	// string is parsed (some SQLite driver configurations).
	if got := decodeTime(rfc); !got.Equal(ref) {
		t.Fatalf("decodeTime(string) = %v, want %v", got, ref)
	}
	// []byte is parsed (SQLite TEXT column).
	if got := decodeTime([]byte(rfc)); !got.Equal(ref) {
		t.Fatalf("decodeTime([]byte) = %v, want %v", got, ref)
	}
	// Unknown types decode to the zero time rather than panicking.
	if got := decodeTime(12345); !got.IsZero() {
		t.Fatalf("decodeTime(int) = %v, want zero time", got)
	}
	// An unparseable string yields the zero time (parse error swallowed).
	if got := decodeTime("not-a-timestamp"); !got.IsZero() {
		t.Fatalf("decodeTime(bad string) = %v, want zero time", got)
	}
}
