package storage

import (
	"reflect"
	"testing"
)

func TestFirstField(t *testing.T) {
	cases := map[string]string{
		"amount INTEGER NOT NULL": "amount",
		"title TEXT":              "title",
		"col":                     "col",
		"tab\tTEXT":               "tab",
	}
	for in, want := range cases {
		if got := firstField(in); got != want {
			t.Errorf("firstField(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPgPlaceholders(t *testing.T) {
	got := pgPlaceholders(3)
	want := []string{"$1", "$2", "$3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pgPlaceholders(3) = %v, want %v", got, want)
	}
	if len(pgPlaceholders(0)) != 0 {
		t.Errorf("pgPlaceholders(0) should be empty")
	}
}

func TestEncodeValue_SQLite(t *testing.T) {
	if got := encodeValue(nil, false); got != nil {
		t.Errorf("absent = %v, want nil", got)
	}
	if got := encodeValue(nil, true); got != nil {
		t.Errorf("nil value = %v, want nil", got)
	}
	if got := encodeValue(true, true); got != 1 {
		t.Errorf("bool true = %v, want 1", got)
	}
	if got := encodeValue(false, true); got != 0 {
		t.Errorf("bool false = %v, want 0", got)
	}
	if got := encodeValue([]string{"a", "b"}, true); got != `["a","b"]` {
		t.Errorf("slice = %v, want JSON string", got)
	}
	if got := encodeValue("plain", true); got != "plain" {
		t.Errorf("string passthrough = %v, want plain", got)
	}
}

func TestEncodeValue_Postgres(t *testing.T) {
	// Postgres keeps native booleans.
	if got := encodeValuePg(true, true); got != true {
		t.Errorf("pg bool = %v, want true (native)", got)
	}
	if got := encodeValuePg(map[string]any{"k": "v"}, true); got != `{"k":"v"}` {
		t.Errorf("pg map = %v, want JSON string", got)
	}
	if got := encodeValuePg(nil, false); got != nil {
		t.Errorf("pg absent = %v, want nil", got)
	}
}

func TestDecodeValue(t *testing.T) {
	if got := decodeValue(`["a","b"]`); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("json string array = %v, want []string", got)
	}
	if got := decodeValue(`{"k":"v"}`); !reflect.DeepEqual(got, map[string]any{"k": "v"}) {
		t.Errorf("json object = %v, want map", got)
	}
	if got := decodeValue("plain"); got != "plain" {
		t.Errorf("plain string = %v, want plain", got)
	}
	if got := decodeValue(42); got != 42 {
		t.Errorf("non-string passthrough = %v, want 42", got)
	}
	if got := decodeValue(""); got != "" {
		t.Errorf("empty string = %v, want empty", got)
	}
}

func TestOptionsAffectSchema(t *testing.T) {
	cfg := defaultConfig()
	for _, opt := range []Option{
		WithTable("t"),
		WithIDColumn("wid"),
		WithStateColumn("st"),
		WithVersionColumn("ver"),
		WithCustomFields(map[string]string{"a": "a TEXT"}),
	} {
		opt(&cfg)
	}
	if cfg.table != "t" || cfg.idColumn != "wid" || cfg.stateColumn != "st" || cfg.versionColumn != "ver" {
		t.Fatalf("options not applied: %+v", cfg)
	}
	if cfg.customFields["a"] != "a TEXT" {
		t.Fatalf("custom fields not applied: %+v", cfg.customFields)
	}
}
