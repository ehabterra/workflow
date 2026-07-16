// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package storage

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func TestClampUnixNano(t *testing.T) {
	// A normal instant round-trips exactly.
	normal := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := clampUnixNano(normal); got != normal.UnixNano() {
		t.Errorf("clampUnixNano(normal) = %d, want %d", got, normal.UnixNano())
	}
	// Beyond the UnixNano ceiling saturates to MaxInt64 instead of wrapping.
	if got := clampUnixNano(time.Date(2266, 1, 1, 0, 0, 0, 0, time.UTC)); got != math.MaxInt64 {
		t.Errorf("clampUnixNano(2266) = %d, want MaxInt64", got)
	}
	// Below the floor saturates to MinInt64.
	if got := clampUnixNano(time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC)); got != math.MinInt64 {
		t.Errorf("clampUnixNano(1000) = %d, want MinInt64", got)
	}
	// dueValueSQLite uses it: a far-future due encodes to the saturated value.
	far := time.Date(2266, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := dueValueSQLite(&far); got != int64(math.MaxInt64) {
		t.Errorf("dueValueSQLite(far) = %v, want MaxInt64", got)
	}
	if dueValueSQLite(nil) != nil {
		t.Error("dueValueSQLite(nil) should be nil")
	}
}

func TestDueIndexDDL_Disabled(t *testing.T) {
	cfg := defaultConfig()
	cfg.dueColumn = ""
	if ddl := cfg.dueIndexDDL(); ddl != "" {
		t.Errorf("dueIndexDDL with empty dueColumn = %q, want empty", ddl)
	}
	cfg.dueColumn = "due_at"
	if ddl := cfg.dueIndexDDL(); ddl == "" {
		t.Error("dueIndexDDL with a due column should be non-empty")
	}
}

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
