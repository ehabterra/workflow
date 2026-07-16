// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow_test

import (
	"errors"
	"testing"

	"github.com/ehabterra/workflow"
)

func TestMarkingTokenViewHelpers(t *testing.T) {
	m := workflow.NewMarking(nil)
	tok := workflow.NewToken(workflow.TokenData{"k": "v"})
	m.AddToken("p", tok)

	if !m.HasToken("p", tok.ID()) {
		t.Fatalf("HasToken = false, want true for %s", tok.ID())
	}
	if m.HasToken("p", "missing") {
		t.Fatal("HasToken returned true for a missing ID")
	}
	if m.HasToken("other", tok.ID()) {
		t.Fatal("HasToken returned true for the wrong place")
	}

	all := m.AllTokens()
	if len(all["p"]) != 1 || all["p"][0].ID() != tok.ID() {
		t.Fatalf("AllTokens = %#v, want the single token at p", all)
	}
}

func TestMarkingRemoveToken(t *testing.T) {
	m := workflow.NewMarking(nil)
	a := workflow.NewToken(workflow.TokenData{"n": 1})
	b := workflow.NewToken(workflow.TokenData{"n": 2})
	m.AddToken("p", a)
	m.AddToken("p", b)

	if err := m.RemoveToken("p", a.ID()); err != nil {
		t.Fatalf("RemoveToken(a) = %v, want nil", err)
	}
	if m.HasToken("p", a.ID()) {
		t.Fatal("token a still present after removal")
	}

	// Removing the last token drops the place entirely.
	if err := m.RemoveToken("p", b.ID()); err != nil {
		t.Fatalf("RemoveToken(b) = %v, want nil", err)
	}
	if m.HasPlace("p") {
		t.Fatal("place p should be gone once its last token is removed")
	}

	// Removing a token that is not there returns ErrTokenNotFound.
	err := m.RemoveToken("p", "nope")
	if !errors.Is(err, workflow.ErrTokenNotFound) {
		t.Fatalf("RemoveToken(missing) = %v, want ErrTokenNotFound", err)
	}
}
