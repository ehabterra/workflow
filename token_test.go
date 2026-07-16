// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/ehabterra/workflow"
)

func TestNewToken_UniqueIDs(t *testing.T) {
	a := workflow.NewToken(workflow.TokenData{"x": 1})
	b := workflow.NewToken(workflow.TokenData{"x": 1})
	if a.ID() == "" || b.ID() == "" {
		t.Fatal("tokens must have non-empty IDs")
	}
	if a.ID() == b.ID() {
		t.Fatal("distinct tokens must have distinct IDs")
	}
	if a.Equal(b) {
		t.Fatal("tokens with same data but different IDs are not equal")
	}
	if !a.Equal(workflow.NewTokenWithID(a.ID(), nil)) {
		t.Fatal("Equal must compare by ID")
	}
}

func TestToken_DataIsCopied(t *testing.T) {
	src := workflow.TokenData{"amount": 100}
	tok := workflow.NewToken(src)

	// Mutating the source after construction must not affect the token.
	src["amount"] = 999
	if v, _ := tok.Get("amount"); v != 100 {
		t.Fatalf("token data aliased its source: got %v, want 100", v)
	}

	// Mutating the returned Data() copy must not affect the token.
	d := tok.Data()
	d["amount"] = 42
	if v, _ := tok.Get("amount"); v != 100 {
		t.Fatalf("Data() returned an aliased map: got %v, want 100", v)
	}
}

func TestToken_WithIsImmutable(t *testing.T) {
	tok := workflow.NewToken(workflow.TokenData{"a": 1})
	tok2 := tok.With("b", 2)

	if _, ok := tok.Get("b"); ok {
		t.Fatal("With must not mutate the receiver")
	}
	if v, _ := tok2.Get("b"); v != 2 {
		t.Fatalf("With result missing new key: got %v", v)
	}
	if v, _ := tok2.Get("a"); v != 1 {
		t.Fatalf("With dropped existing key: got %v", v)
	}
	if tok.ID() != tok2.ID() {
		t.Fatal("With must preserve the token ID")
	}
}

func TestToken_Validate(t *testing.T) {
	if err := workflow.NewToken(nil).Validate(); err != nil {
		t.Fatalf("generated token should be valid: %v", err)
	}
	if err := workflow.NewTokenWithID("", nil).Validate(); !errors.Is(err, workflow.ErrInvalidToken) {
		t.Fatalf("empty-ID token err = %v, want ErrInvalidToken", err)
	}
}

func TestToken_JSONRoundTrip(t *testing.T) {
	tok := workflow.NewTokenWithID("tok-1", workflow.TokenData{"order_id": "001", "amount": float64(100)})

	b, err := json.Marshal(tok)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got workflow.Token
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID() != "tok-1" {
		t.Fatalf("id round-trip: got %q", got.ID())
	}
	if v, _ := got.Get("order_id"); v != "001" {
		t.Fatalf("data round-trip: got %v", v)
	}
	if v, _ := got.Get("amount"); v != float64(100) {
		t.Fatalf("amount round-trip: got %v", v)
	}
}

func TestToken_SliceOfTokensJSON(t *testing.T) {
	tokens := []workflow.Token{
		workflow.NewTokenWithID("a", workflow.TokenData{"n": float64(1)}),
		workflow.NewTokenWithID("b", nil),
	}
	b, err := json.Marshal(tokens)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got []workflow.Token
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 || got[0].ID() != "a" || got[1].ID() != "b" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}
