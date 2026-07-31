// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/ehabterra/workflow"
)

// approvalNet is the shape issue #34 exists for: a state place (`submitted`)
// joined with a POOL place (`approvals`) that accumulates one colored token per
// approval. How many approvals are needed is not in the net — it is
// len(required_roles), a value the host resolves from the record at fire time.
//
//	approve_final : [submitted, approvals] -> [approved]   require over approvals
//	approve_partial: [submitted] -> [submitted]            the "not yet" branch
//
// ApplyAny(final, partial) then routes itself: final is simply not enabled
// until the chain is satisfied, so no host-side satisfaction check exists.
func approvalNet(t *testing.T) *workflow.Definition {
	t.Helper()

	final := workflow.MustNewTransition("approve_final",
		[]workflow.Place{"submitted", "approvals"}, []workflow.Place{"approved"})
	final.SetRequirements(workflow.MustNewRequirement(workflow.RequirementSpec{
		Place:    "approvals",
		Where:    "token.role in required_roles",
		Distinct: "role",
		Count:    "len(required_roles)",
	}))
	// Leftovers — approvals from roles outside the chain — are discarded with
	// the same firing rather than lingering in the pool.
	final.SetResets("approvals")

	partial := workflow.MustNewTransition("approve_partial",
		[]workflow.Place{"submitted"}, []workflow.Place{"submitted"})

	def, err := workflow.NewDefinition(
		[]workflow.Place{"submitted", "approvals", "approved"},
		[]workflow.Transition{*final, *partial},
	)
	if err != nil {
		t.Fatal(err)
	}
	return def
}

func approvalWorkflow(t *testing.T, chain ...string) *workflow.Workflow {
	t.Helper()
	wf, err := workflow.NewWorkflow("req-1", approvalNet(t), "submitted")
	if err != nil {
		t.Fatal(err)
	}
	roles := make([]any, len(chain))
	for i, r := range chain {
		roles[i] = r
	}
	wf.SetContext("required_roles", roles)
	return wf
}

func approve(t *testing.T, wf *workflow.Workflow, role, by string) {
	t.Helper()
	if _, err := wf.CreateToken("approvals", workflow.TokenData{"role": role, "by": by}); err != nil {
		t.Fatal(err)
	}
}

// TestRequirementChainSatisfaction is the headline: the transition waits for a
// set whose size is only known at runtime, with no host-side satisfaction check.
func TestRequirementChainSatisfaction(t *testing.T) {
	ctx := context.Background()
	wf := approvalWorkflow(t, "manager", "finance")

	// Nothing approved yet: the pool is empty, so the AND-join is not even
	// structurally enabled.
	if err := wf.CanTransition("approve_final"); !errors.Is(err, workflow.ErrNotEnabled) {
		t.Fatalf("empty pool: want ErrNotEnabled, got %v", err)
	}

	// One of two approvals: the pool IS marked, so only the requirement can
	// distinguish "some approvals" from "the chain is satisfied".
	approve(t, wf, "manager", "alice")
	err := wf.CanTransition("approve_final")
	if !errors.Is(err, workflow.ErrNotEnabled) {
		t.Fatalf("half a chain: want ErrNotEnabled, got %v", err)
	}
	if !strings.Contains(err.Error(), "len(required_roles)") {
		t.Errorf("the rejection should name the unmet requirement, got %q", err)
	}

	fired, err := wf.ApplyAny(ctx, "approve_final", "approve_partial")
	if err != nil {
		t.Fatal(err)
	}
	if fired != "approve_partial" {
		t.Fatalf("with an unsatisfied chain ApplyAny must fall through to the partial branch, got %q", fired)
	}

	// The second approval completes the chain.
	approve(t, wf, "finance", "bob")
	fired, err = wf.ApplyAny(ctx, "approve_final", "approve_partial")
	if err != nil {
		t.Fatal(err)
	}
	if fired != "approve_final" {
		t.Fatalf("satisfied chain: want approve_final, got %q", fired)
	}
	if places := wf.CurrentPlaces(); len(places) != 1 || places[0] != "approved" {
		t.Fatalf("want [approved], got %v", places)
	}
	if n := wf.TokenCount("approvals"); n != 0 {
		t.Errorf("the reset arc should have emptied the pool, %d left", n)
	}
}

// TestRequirementDistinctDeDuplicates: two approvals from the SAME role are one
// approval. Without this the count is satisfiable by one enthusiastic approver.
func TestRequirementDistinctDeDuplicates(t *testing.T) {
	wf := approvalWorkflow(t, "manager", "finance")
	approve(t, wf, "manager", "alice")
	approve(t, wf, "manager", "alice-again")

	if err := wf.CanTransition("approve_final"); !errors.Is(err, workflow.ErrNotEnabled) {
		t.Fatalf("two approvals from one role must not satisfy a two-role chain, got %v", err)
	}

	approve(t, wf, "finance", "bob")
	if err := wf.CanTransition("approve_final"); err != nil {
		t.Fatalf("distinct roles satisfied: %v", err)
	}
}

// TestRequirementWhereExcludesOutOfChainTokens: chain membership is structural.
// A token whose role is not in the chain can never count toward the join, so
// "is this actor even a required approver?" stops being a check the host has to
// remember to write.
func TestRequirementWhereExcludesOutOfChainTokens(t *testing.T) {
	wf := approvalWorkflow(t, "manager", "finance")
	approve(t, wf, "manager", "alice")
	approve(t, wf, "intern", "mallory")
	approve(t, wf, "", "roleless")

	if err := wf.CanTransition("approve_final"); !errors.Is(err, workflow.ErrNotEnabled) {
		t.Fatalf("out-of-chain approvals must not satisfy the join, got %v", err)
	}

	approve(t, wf, "finance", "bob")
	if err := wf.ApplyTransition("approve_final"); err != nil {
		t.Fatal(err)
	}
	// The two chain approvals flowed to the output; the reset arc discarded the
	// two that never counted.
	if n := len(wf.GetTokens("approved")); n != 2 {
		t.Fatalf("want the 2 counted approvals at approved, got %d", n)
	}
}

// poolNet is the mechanics case: take exactly N from a pool, leave the rest.
func poolNet(t *testing.T, count string) *workflow.Definition {
	t.Helper()
	ship := workflow.MustNewTransition("ship", []workflow.Place{"pool"}, []workflow.Place{"shipped"})
	ship.SetRequirements(workflow.MustNewRequirement(workflow.RequirementSpec{Place: "pool", Count: count}))
	def, err := workflow.NewDefinition(
		[]workflow.Place{"pool", "shipped"},
		[]workflow.Transition{*ship},
	)
	if err != nil {
		t.Fatal(err)
	}
	return def
}

func poolWorkflow(t *testing.T, count string, n int) *workflow.Workflow {
	t.Helper()
	m := workflow.NewMarking(nil)
	for i := range n {
		m.AddToken("pool", workflow.NewToken(workflow.TokenData{"order": i}))
	}
	wf, err := workflow.NewWorkflowFromMarking("batch", poolNet(t, count), m)
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

// TestRequirementConsumesExactlyNAndLeavesRemainder pins the consumption half:
// an ordinary input place is drained, a required one is not.
func TestRequirementConsumesExactlyNAndLeavesRemainder(t *testing.T) {
	wf := poolWorkflow(t, "batch_size", 5)
	wf.SetContext("batch_size", 2)

	before := wf.GetTokens("pool")
	if err := wf.ApplyTransition("ship"); err != nil {
		t.Fatal(err)
	}

	shipped := wf.GetTokens("shipped")
	if len(shipped) != 2 {
		t.Fatalf("want 2 tokens shipped, got %d", len(shipped))
	}
	left := wf.GetTokens("pool")
	if len(left) != 3 {
		t.Fatalf("want 3 tokens left in the pool, got %d", len(left))
	}

	// Selection is deterministic — the place's own order — and the survivors
	// keep theirs, so a second firing takes the next two and not a reshuffle.
	if shipped[0].ID() != before[0].ID() || shipped[1].ID() != before[1].ID() {
		t.Errorf("want the first two pool tokens consumed, got %v", shipped)
	}
	for i, tok := range left {
		if tok.ID() != before[i+2].ID() {
			t.Fatalf("survivor %d out of order: want %s, got %s", i, before[i+2].ID(), tok.ID())
		}
	}

	if err := wf.ApplyTransition("ship"); err != nil {
		t.Fatal(err)
	}
	if n := len(wf.GetTokens("pool")); n != 1 {
		t.Fatalf("after a second batch want 1 left, got %d", n)
	}
	// The pool still holds a token, so the place stays marked — but one is not
	// enough for another batch.
	if err := wf.ApplyTransition("ship"); !errors.Is(err, workflow.ErrNotEnabled) {
		t.Fatalf("want ErrNotEnabled with an under-full pool, got %v", err)
	}
}

// TestRequirementCountFromPersistedContext: a count that arrives back from
// storage as a JSON float64 must behave exactly as the int did before the save.
func TestRequirementCountFromPersistedContext(t *testing.T) {
	wf := poolWorkflow(t, "batch_size", 3)
	wf.SetContext("batch_size", float64(2)) // what JSON storage hands back

	if err := wf.ApplyTransition("ship"); err != nil {
		t.Fatalf("float64 count: %v", err)
	}
	if n := len(wf.GetTokens("shipped")); n != 2 {
		t.Fatalf("want 2 shipped, got %d", n)
	}
}

// TestRequirementCountExpressionFaultIsNotJustNotEnabled: a count that cannot be
// evaluated is a definition/environment fault. It must NOT look like "not yet",
// or ApplyAny would quietly skip past a broken definition.
func TestRequirementCountExpressionFaultIsNotJustNotEnabled(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count any
	}{
		{"not a number", "batch_size"},
		{"fractional", 2.5},
		{"negative", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf := poolWorkflow(t, "batch_size", 3)
			wf.SetContext("batch_size", tc.count)

			err := wf.ApplyTransition("ship")
			if err == nil {
				t.Fatal("want an error")
			}
			if errors.Is(err, workflow.ErrTransitionNotAllowed) {
				t.Fatalf("a broken count must not read as a blocked transition: %v", err)
			}
			if !errors.Is(err, workflow.ErrInvalidExpression) {
				t.Fatalf("want ErrInvalidExpression, got %v", err)
			}
		})
	}
}

// TestRequirementCountAcceptsEveryIntegerKind: a count reaches the engine as
// whatever type the host put in the context, so every integer kind has to
// behave the same. Anything that is not a whole, in-range, non-negative number
// is a fault.
func TestRequirementCountAcceptsEveryIntegerKind(t *testing.T) {
	good := map[string]any{
		"int":     2,
		"int8":    int8(2),
		"int16":   int16(2),
		"int32":   int32(2),
		"int64":   int64(2),
		"uint":    uint(2),
		"uint8":   uint8(2),
		"uint16":  uint16(2),
		"uint32":  uint32(2),
		"uint64":  uint64(2),
		"float32": float32(2),
		"float64": float64(2),
	}
	for name, v := range good {
		t.Run(name, func(t *testing.T) {
			wf := poolWorkflow(t, "batch_size", 3)
			wf.SetContext("batch_size", v)
			if err := wf.ApplyTransition("ship"); err != nil {
				t.Fatalf("count as %s: %v", name, err)
			}
			if n := len(wf.GetTokens("shipped")); n != 2 {
				t.Fatalf("want 2 shipped, got %d", n)
			}
		})
	}

	bad := map[string]any{
		"out of int range": uint64(math.MaxUint64),
		"boolean":          true,
		"nil":              nil,
	}
	for name, v := range bad {
		t.Run(name, func(t *testing.T) {
			wf := poolWorkflow(t, "batch_size", 3)
			wf.SetContext("batch_size", v)
			if err := wf.ApplyTransition("ship"); !errors.Is(err, workflow.ErrInvalidExpression) {
				t.Fatalf("want ErrInvalidExpression, got %v", err)
			}
		})
	}
}

// TestRequirementExpressionRuntimeFaults: an expression that compiles but blows
// up at fire time is still a fault, not a "not yet".
func TestRequirementExpressionRuntimeFaults(t *testing.T) {
	cases := map[string]workflow.RequirementSpec{
		"count faults":     {Place: "pool", Count: "tokens[99]"},
		"where faults":     {Place: "pool", Count: "1", Where: "tokens[99] == 1"},
		"where not a bool": {Place: "pool", Count: "1", Where: "token.order"},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			ship := workflow.MustNewTransition("ship", []workflow.Place{"pool"}, []workflow.Place{"shipped"})
			ship.SetRequirements(workflow.MustNewRequirement(spec))
			def, err := workflow.NewDefinition(
				[]workflow.Place{"pool", "shipped"}, []workflow.Transition{*ship})
			if err != nil {
				t.Fatal(err)
			}
			m := workflow.NewMarking(nil)
			m.AddToken("pool", workflow.NewToken(workflow.TokenData{"order": 1}))
			wf, err := workflow.NewWorkflowFromMarking("b", def, m)
			if err != nil {
				t.Fatal(err)
			}

			// Every enablement path must report it, not just the firing one.
			if _, err := wf.EnabledTransitions(); !errors.Is(err, workflow.ErrInvalidExpression) {
				t.Errorf("EnabledTransitions: want ErrInvalidExpression, got %v", err)
			}
			if err := wf.ApplyTransition("ship"); !errors.Is(err, workflow.ErrInvalidExpression) {
				t.Errorf("ApplyTransition: want ErrInvalidExpression, got %v", err)
			}
			if err := wf.Apply([]workflow.Place{"shipped"}); !errors.Is(err, workflow.ErrInvalidExpression) {
				t.Errorf("Apply: want ErrInvalidExpression, got %v", err)
			}
		})
	}
}

// TestRequirementWhereOnUncoloredToken: a required place may still hold the
// uncolored presence token a boolean net starts with. It carries no data, so a
// where clause simply never matches it — it must not fault the evaluation.
func TestRequirementWhereOnUncoloredToken(t *testing.T) {
	ship := workflow.MustNewTransition("ship", []workflow.Place{"pool"}, []workflow.Place{"shipped"})
	ship.SetRequirements(workflow.MustNewRequirement(workflow.RequirementSpec{
		Place: "pool", Count: "1", Where: "token.ready == true",
	}))
	def, err := workflow.NewDefinition(
		[]workflow.Place{"pool", "shipped"}, []workflow.Transition{*ship})
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.NewWorkflow("b", def, "pool") // a bare presence token
	if err != nil {
		t.Fatal(err)
	}

	if err := wf.ApplyTransition("ship"); !errors.Is(err, workflow.ErrNotEnabled) {
		t.Fatalf("a presence token cannot match a where clause: %v", err)
	}
	if _, err := wf.CreateToken("pool", workflow.TokenData{"ready": true}); err != nil {
		t.Fatal(err)
	}
	if err := wf.ApplyTransition("ship"); err != nil {
		t.Fatal(err)
	}
	// Only the matching token left; the presence token stays behind, which is
	// why a pool place should not be seeded with one.
	if n := wf.TokenCount("pool"); n != 1 {
		t.Fatalf("want the presence token left behind, got %d tokens", n)
	}
}

func TestMustNewRequirementPanicsOnMalformed(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("want a panic")
		}
	}()
	workflow.MustNewRequirement(workflow.RequirementSpec{Place: "p", Count: "len("})
}

// TestRequirementZeroCountConsumesNothing: a count of zero is met by an empty
// selection, so the required place keeps everything it holds.
func TestRequirementZeroCountConsumesNothing(t *testing.T) {
	wf := poolWorkflow(t, "0", 2)
	if err := wf.ApplyTransition("ship"); err != nil {
		t.Fatal(err)
	}
	if n := len(wf.GetTokens("pool")); n != 2 {
		t.Fatalf("a zero count consumes nothing, want 2 left, got %d", n)
	}
	if !wf.Marking().HasPlace("shipped") {
		t.Error("the transition still fired, so its output must be marked")
	}
}

// TestRequirementEnabledTransitionsFilters: the enablement view agrees with what
// firing would actually do.
func TestRequirementEnabledTransitionsFilters(t *testing.T) {
	wf := approvalWorkflow(t, "manager", "finance")
	approve(t, wf, "manager", "alice")

	names := func() []string {
		ts, err := wf.EnabledTransitions()
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(ts))
		for i := range ts {
			out[i] = ts[i].Name()
		}
		return out
	}

	if got := names(); len(got) != 1 || got[0] != "approve_partial" {
		t.Fatalf("want only approve_partial enabled, got %v", got)
	}
	approve(t, wf, "finance", "bob")
	if got := names(); len(got) != 2 {
		t.Fatalf("want both branches enabled once the chain is satisfied, got %v", got)
	}
}

// TestRequirementAfterEventReportsConsumedTokensOnly: listeners must see the
// tokens the firing actually took, not everything that was in the place.
func TestRequirementAfterEventReportsConsumedTokensOnly(t *testing.T) {
	wf := poolWorkflow(t, "2", 5)
	var seen int
	wf.AddEventListener(workflow.EventAfterTransition, func(ev workflow.Event) error {
		seen = len(ev.Tokens())
		return nil
	})
	if err := wf.ApplyTransition("ship"); err != nil {
		t.Fatal(err)
	}
	if seen != 2 {
		t.Fatalf("after-event should report the 2 consumed tokens, got %d", seen)
	}
}

// TestRequirementConcurrentFiringsDoNotDoubleConsume: the selection is
// re-resolved under the write lock, so two racing firings split the pool rather
// than both taking the same tokens.
func TestRequirementConcurrentFiringsDoNotDoubleConsume(t *testing.T) {
	wf := poolWorkflow(t, "2", 6)

	var wg sync.WaitGroup
	var mu sync.Mutex
	fired := 0
	for range 3 {
		wg.Go(func() {
			if err := wf.ApplyTransition("ship"); err == nil {
				mu.Lock()
				fired++
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	if fired != 3 {
		t.Fatalf("three batches of two fit in a pool of six, %d fired", fired)
	}
	if n := len(wf.GetTokens("pool")); n != 0 {
		t.Fatalf("pool should be empty, %d left", n)
	}
	if n := len(wf.GetTokens("shipped")); n != 6 {
		t.Fatalf("every token must be accounted for, got %d shipped", n)
	}
}

// --- construction and validation ---

func TestNewRequirementRejectsMalformedDeclarations(t *testing.T) {
	cases := map[string]workflow.RequirementSpec{
		"no place":  {Count: "1"},
		"no count":  {Place: "pool", Count: "  "},
		"bad count": {Place: "pool", Count: "len("},
		"bad where": {Place: "pool", Count: "1", Where: "token."},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := workflow.NewRequirement(spec); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

func TestDefinitionRejectsIncoherentRequirements(t *testing.T) {
	req := func(p workflow.Place) workflow.Requirement {
		return workflow.MustNewRequirement(workflow.RequirementSpec{Place: p, Count: "1"})
	}
	places := []workflow.Place{"a", "b", "c"}

	t.Run("place is not an input", func(t *testing.T) {
		tr := workflow.MustNewTransition("t", []workflow.Place{"a"}, []workflow.Place{"c"})
		tr.SetRequirements(req("b"))
		if _, err := workflow.NewDefinition(places, []workflow.Transition{*tr}); err == nil {
			t.Fatal("a requirement over a place the transition does not consume must be rejected")
		}
	})

	t.Run("duplicate place", func(t *testing.T) {
		tr := workflow.MustNewTransition("t", []workflow.Place{"a"}, []workflow.Place{"c"})
		tr.SetRequirements(req("a"), req("a"))
		if _, err := workflow.NewDefinition(places, []workflow.Transition{*tr}); err == nil {
			t.Fatal("two requirements on one place would be two selectors; must be rejected")
		}
	})

	t.Run("with from_any", func(t *testing.T) {
		tr := workflow.MustNewTransition("t", []workflow.Place{"a", "b"}, []workflow.Place{"c"})
		tr.SetFromAny(true)
		tr.SetRequirements(req("a"))
		if _, err := workflow.NewDefinition(places, []workflow.Transition{*tr}); err == nil {
			t.Fatal("from_any and require both resolve the input; the combination must be rejected")
		}
	})
}

// TestRequirementRejectsPerTokenFiring: the requirement already selects the
// tokens, so naming one would be a second, conflicting selector.
func TestRequirementRejectsPerTokenFiring(t *testing.T) {
	wf := poolWorkflow(t, "1", 2)
	tok := wf.GetTokens("pool")[0]
	err := wf.ApplyTransitionForToken(context.Background(), "ship", tok.ID())
	if !errors.Is(err, workflow.ErrInvalidTransition) {
		t.Fatalf("want ErrInvalidTransition, got %v", err)
	}
}

func TestSetRequirementsCopiesAndClears(t *testing.T) {
	tr := workflow.MustNewTransition("t", []workflow.Place{"a"}, []workflow.Place{"b"})
	reqs := []workflow.Requirement{workflow.MustNewRequirement(workflow.RequirementSpec{Place: "a", Count: "1"})}
	tr.SetRequirements(reqs...)

	reqs[0] = workflow.MustNewRequirement(workflow.RequirementSpec{Place: "a", Count: "99"})
	if got := tr.Requirements()[0].Spec().Count; got != "1" {
		t.Errorf("the transition must not alias the caller's slice, got count %q", got)
	}

	tr.SetRequirements()
	if tr.Requirements() != nil {
		t.Error("SetRequirements() with no arguments should clear")
	}
}

// --- fingerprint / diff / diagram ---

func requirementDef(t *testing.T, reqs ...workflow.Requirement) *workflow.Definition {
	t.Helper()
	tr := workflow.MustNewTransition("ship", []workflow.Place{"pool", "aux"}, []workflow.Place{"shipped"})
	tr.SetRequirements(reqs...)
	def, err := workflow.NewDefinition(
		[]workflow.Place{"pool", "aux", "shipped"},
		[]workflow.Transition{*tr},
	)
	if err != nil {
		t.Fatal(err)
	}
	return def
}

// TestRequirementIsFingerprinted: a requirement decides when the net can fire,
// so it is structure — changing one must invalidate persisted instances, and
// NOT declaring one must leave the old fingerprint untouched.
func TestRequirementIsFingerprinted(t *testing.T) {
	base := workflow.RequirementSpec{Place: "pool", Count: "2"}
	fp := func(specs ...workflow.RequirementSpec) string {
		reqs := make([]workflow.Requirement, len(specs))
		for i, s := range specs {
			reqs[i] = workflow.MustNewRequirement(s)
		}
		return requirementDef(t, reqs...).Fingerprint()
	}

	none, first, second := fp(), fp(base), fp(base)
	if first == none {
		t.Fatal("adding a requirement must move the fingerprint")
	}
	if first != second {
		t.Fatal("fingerprints must be stable")
	}

	for name, changed := range map[string]workflow.RequirementSpec{
		"count":    {Place: "pool", Count: "3"},
		"place":    {Place: "aux", Count: "2"},
		"where":    {Place: "pool", Count: "2", Where: "token.ok"},
		"distinct": {Place: "pool", Count: "2", Distinct: "role"},
	} {
		if fp(changed) == fp(base) {
			t.Errorf("changing %s must move the fingerprint", name)
		}
	}

	// Requirements are a conjunction over distinct places, so declaration order
	// carries no meaning and must not change the hash.
	other := workflow.RequirementSpec{Place: "aux", Count: "1"}
	if fp(base, other) != fp(other, base) {
		t.Error("requirement order must not affect the fingerprint")
	}
}

// TestRequirementDiagramShowsTheJoin: the arity is an expression, not a number
// of arcs, so a diagram that omits it misstates when the transition can fire.
func TestRequirementDiagramShowsTheJoin(t *testing.T) {
	def := requirementDef(t, workflow.MustNewRequirement(workflow.RequirementSpec{
		Place: "pool", Count: "len(chain)", Distinct: "role", Where: "token.role in chain",
	}))
	diagram := def.Diagram()
	// `>` renders as Mermaid's own numeric entity (`#62;`, no ampersand) — see
	// escapeMermaidLabel; it is not an HTML entity and must not be "fixed".
	for _, want := range []string{"pool #62;= len(chain)", "distinct by role", "where token.role in chain"} {
		if !strings.Contains(diagram, want) {
			t.Errorf("diagram should mention %q:\n%s", want, diagram)
		}
	}
}
