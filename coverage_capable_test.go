package workflow_test

import (
	"context"
	"testing"

	"github.com/ehabterra/workflow"
)

// capableStorage adds the optional ListableStorage and TokenQueryStorage
// capabilities on top of the plain MockStorage, so the Manager's supported
// paths (not just the ErrUnsupported paths) are exercised.
type capableStorage struct {
	*MockStorage
}

func (c *capableStorage) ListIDs(ctx context.Context, opts workflow.ListOptions) ([]string, error) {
	return []string{"a", "b"}, nil
}

func (c *capableStorage) ListPlaceTokens(ctx context.Context, place workflow.Place, opts workflow.ListOptions) ([]workflow.PlacedToken, error) {
	return []workflow.PlacedToken{
		{WorkflowID: "w1", Place: place, Token: workflow.NewToken(workflow.TokenData{"amount": 100.0})},
	}, nil
}

func TestManagerSupportedCapabilities(t *testing.T) {
	ctx := context.Background()
	mgr := workflow.NewManager(workflow.NewRegistry(), &capableStorage{NewMockStorage()})

	ids, err := mgr.ListWorkflowIDs(ctx, workflow.ListOptions{})
	if err != nil {
		t.Fatalf("ListWorkflowIDs = %v, want nil", err)
	}
	if len(ids) != 2 {
		t.Fatalf("ListWorkflowIDs returned %v, want 2 ids", ids)
	}

	toks, err := mgr.ListPlaceTokens(ctx, "payable", workflow.ListOptions{})
	if err != nil {
		t.Fatalf("ListPlaceTokens = %v, want nil", err)
	}
	if len(toks) != 1 || toks[0].Place != "payable" {
		t.Fatalf("ListPlaceTokens returned %#v, want one token in 'payable'", toks)
	}
}

// wrappedMarking is a Marking that is deliberately NOT the internal *marking
// type, so cloneMarking must take its generic (interface-only) fallback path.
type wrappedMarking struct {
	workflow.Marking
}

func (wrappedMarking) AllTokens() map[workflow.Place][]workflow.Token {
	return map[workflow.Place][]workflow.Token{
		"empty":  {}, // exercises the empty-place branch (AddPlace)
		"filled": {workflow.NewToken(workflow.TokenData{"n": 1})},
	}
}

func TestCloneMarkingGenericFallback(t *testing.T) {
	ctx := context.Background()
	store := NewMockStorage()
	mgr := workflow.NewManager(workflow.NewRegistry(), store)

	def, err := workflow.NewDefinition(
		[]workflow.Place{"empty", "filled"},
		[]workflow.Transition{*workflow.MustNewTransition("t", []workflow.Place{"filled"}, []workflow.Place{"empty"})},
	)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.NewWorkflow("wrapped", def, "filled")
	if err != nil {
		t.Fatal(err)
	}
	// Install a non-*marking implementation, then save: snapshotState clones it
	// via cloneMarking's generic fallback.
	if err := wf.SetMarking(wrappedMarking{Marking: workflow.NewMarking(nil)}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveWorkflow(ctx, "wrapped", wf); err != nil {
		t.Fatalf("SaveWorkflow with wrapped marking = %v", err)
	}

	// The persisted state should carry both places (empty via AddPlace, filled
	// via AddToken).
	m, _, _, err := store.LoadState(ctx, "wrapped")
	if err != nil {
		t.Fatal(err)
	}
	if !m.HasPlace("empty") || m.TokenCount("filled") != 1 {
		t.Fatalf("cloned marking lost data: empty=%v filled=%d", m.HasPlace("empty"), m.TokenCount("filled"))
	}
}
