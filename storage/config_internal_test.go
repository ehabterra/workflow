package storage

import (
	"reflect"
	"testing"
)

func TestEncodeContextJSON(t *testing.T) {
	// Empty/nil map encodes to the uniform "{}".
	if got, err := encodeContextJSON(nil); err != nil || got != "{}" {
		t.Fatalf("encodeContextJSON(nil) = %q, %v; want {}", got, err)
	}
	if got, err := encodeContextJSON(map[string]any{"k": "v"}); err != nil || got != `{"k":"v"}` {
		t.Fatalf("encodeContextJSON = %q, %v", got, err)
	}
	// A value JSON cannot represent is an error.
	if _, err := encodeContextJSON(map[string]any{"bad": make(chan int)}); err == nil {
		t.Fatal("encoding a channel should error")
	}
}

func TestDecodeContextJSON(t *testing.T) {
	// nil => empty map.
	if got, err := decodeContextJSON(nil); err != nil || len(got) != 0 {
		t.Fatalf("decodeContextJSON(nil) = %v, %v", got, err)
	}
	// []byte and string forms both decode.
	want := map[string]any{"k": "v"}
	if got, err := decodeContextJSON([]byte(`{"k":"v"}`)); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("decodeContextJSON([]byte) = %v, %v", got, err)
	}
	if got, err := decodeContextJSON(`{"k":"v"}`); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("decodeContextJSON(string) = %v, %v", got, err)
	}
	// Empty string => empty map.
	if got, err := decodeContextJSON(""); err != nil || len(got) != 0 {
		t.Fatalf("decodeContextJSON(\"\") = %v, %v", got, err)
	}
	// Unexpected type => error.
	if _, err := decodeContextJSON(42); err == nil {
		t.Fatal("decodeContextJSON(int) should error")
	}
	// Malformed JSON => error.
	if _, err := decodeContextJSON(`{not json`); err == nil {
		t.Fatal("decodeContextJSON(bad) should error")
	}
}
