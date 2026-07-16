// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// benchTimedWorkflow builds a workflow with a handful of independent timed
// transitions, all enabled, for the Due/NextDue benchmarks.
func benchTimedWorkflow(b *testing.B) *Workflow {
	b.Helper()
	const n = 5
	var places []Place
	var trans []Transition
	var starts []Place
	for i := range n {
		p := Place(fmt.Sprintf("p%d", i))
		q := Place(fmt.Sprintf("q%d", i))
		places = append(places, p, q)
		starts = append(starts, p)
		tr := MustNewTransition(fmt.Sprintf("t%d", i), []Place{p}, []Place{q})
		tr.SetTimeoutAfter(time.Duration(i+1) * time.Hour)
		trans = append(trans, *tr)
	}
	def, err := NewDefinition(places, trans)
	if err != nil {
		b.Fatalf("NewDefinition: %v", err)
	}
	wf, err := NewWorkflowFromMarking("bench", def, NewMarking(starts), WithClock(fixedClock(epoch)))
	if err != nil {
		b.Fatalf("NewWorkflowFromMarking: %v", err)
	}
	return wf
}

func BenchmarkDue(b *testing.B) {
	wf := benchTimedWorkflow(b)
	now := epoch.Add(1000 * time.Hour) // everything overdue
	b.ReportAllocs()
	for b.Loop() {
		_ = wf.Due(now)
	}
}

func BenchmarkNextDue(b *testing.B) {
	wf := benchTimedWorkflow(b)
	b.ReportAllocs()
	for b.Loop() {
		_, _ = wf.NextDue()
	}
}

// fixedClock returns a clock function pinned to t.
func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// timedApprovalDef builds submitted --approve--> approved, with a timed
// escalate transition (submitted --escalate--> escalated after d).
func timedApprovalDef(t *testing.T, d time.Duration) *Definition {
	t.Helper()
	approve := MustNewTransition("approve", []Place{"submitted"}, []Place{"approved"})
	escalate := MustNewTransition("escalate", []Place{"submitted"}, []Place{"escalated"})
	escalate.SetTimeoutAfter(d)
	def, err := NewDefinition(
		[]Place{"submitted", "approved", "escalated"},
		[]Transition{*approve, *escalate},
	)
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}
	return def
}

func TestDue_BooleanTimeout(t *testing.T) {
	def := timedApprovalDef(t, 72*time.Hour)
	wf, err := NewWorkflow("wf", def, "submitted", WithClock(fixedClock(epoch)))
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}

	// Nothing is due one minute in.
	if due := wf.Due(epoch.Add(time.Minute)); len(due) != 0 {
		t.Fatalf("Due(t+1m) = %v, want none", transitionNames(due))
	}
	// NextDue is exactly 72h after entry.
	next, ok := wf.NextDue()
	if !ok {
		t.Fatal("NextDue: no deadline, want one")
	}
	if want := epoch.Add(72 * time.Hour); !next.Equal(want) {
		t.Fatalf("NextDue = %v, want %v", next, want)
	}
	// Just before the deadline: not due. At the deadline: due.
	if due := wf.Due(next.Add(-time.Nanosecond)); len(due) != 0 {
		t.Fatalf("Due(just before) = %v, want none", transitionNames(due))
	}
	due := wf.Due(next)
	if len(due) != 1 || due[0].Name() != "escalate" {
		t.Fatalf("Due(at deadline) = %v, want [escalate]", transitionNames(due))
	}
}

func TestDue_FiringResetsDeadline(t *testing.T) {
	// A two-hop timed chain: a --t1(1h)--> b --t2(2h)--> c. After firing t1 at
	// epoch+1h, t2's deadline must be measured from epoch+1h, not epoch.
	t1 := MustNewTransition("t1", []Place{"a"}, []Place{"b"})
	t1.SetTimeoutAfter(time.Hour)
	t2 := MustNewTransition("t2", []Place{"b"}, []Place{"c"})
	t2.SetTimeoutAfter(2 * time.Hour)
	def, err := NewDefinition([]Place{"a", "b", "c"}, []Transition{*t1, *t2})
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}

	fireTime := epoch.Add(time.Hour)
	wf, err := NewWorkflow("wf", def, "a", WithClock(fixedClock(fireTime)))
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}
	if err := wf.ApplyTransition("t1"); err != nil {
		t.Fatalf("ApplyTransition(t1): %v", err)
	}

	next, ok := wf.NextDue()
	if !ok {
		t.Fatal("NextDue after t1: none, want t2 deadline")
	}
	// t2 entered b at fireTime; deadline is fireTime + 2h.
	if want := fireTime.Add(2 * time.Hour); !next.Equal(want) {
		t.Fatalf("t2 deadline = %v, want %v (measured from entry into b)", next, want)
	}
}

func TestDue_ANDJoinUsesLatestEntry(t *testing.T) {
	// join needs both legal and finance; its deadline runs from whichever arrived
	// last. We stamp finance later by firing into it after the workflow starts.
	approveLegal := MustNewTransition("legal", []Place{"start"}, []Place{"legal_ok"})
	toFinance := MustNewTransition("to_finance", []Place{"legal_ok"}, []Place{"finance_ok"})
	join := MustNewTransition("join", []Place{"start2", "finance_ok"}, []Place{"done"})
	join.SetTimeoutAfter(time.Hour)
	def, err := NewDefinition(
		[]Place{"start", "legal_ok", "start2", "finance_ok", "done"},
		[]Transition{*approveLegal, *toFinance, *join},
	)
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}

	m := NewMarking([]Place{"start", "start2"})
	clock := &steppedClock{now: epoch}
	wf, err := NewWorkflowFromMarking("wf", def, m, WithClock(clock.fn()))
	if err != nil {
		t.Fatalf("NewWorkflowFromMarking: %v", err)
	}
	// start & start2 stamped at epoch. Advance, then move a token into finance_ok
	// at epoch+3h so the join's latest input entry is epoch+3h.
	clock.now = epoch.Add(3 * time.Hour)
	if err := wf.ApplyTransition("legal"); err != nil {
		t.Fatalf("legal: %v", err)
	}
	if err := wf.ApplyTransition("to_finance"); err != nil {
		t.Fatalf("to_finance: %v", err)
	}

	next, ok := wf.NextDue()
	if !ok {
		t.Fatal("NextDue: none, want join deadline")
	}
	if want := epoch.Add(3 * time.Hour).Add(time.Hour); !next.Equal(want) {
		t.Fatalf("join deadline = %v, want %v (latest input entry + 1h)", next, want)
	}
}

func TestDue_NoTimersIsInert(t *testing.T) {
	approve := MustNewTransition("approve", []Place{"submitted"}, []Place{"approved"})
	def, err := NewDefinition([]Place{"submitted", "approved"}, []Transition{*approve})
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}
	wf, err := NewWorkflow("wf", def, "submitted")
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}
	if due := wf.Due(epoch.Add(1000 * time.Hour)); len(due) != 0 {
		t.Fatalf("Due on timer-free workflow = %v, want none", transitionNames(due))
	}
	if _, ok := wf.NextDue(); ok {
		t.Fatal("NextDue on timer-free workflow returned a deadline, want none")
	}
	// A timer-free workflow must still serialize to the compact place-array form.
	b, err := json.Marshal(wf.marking)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(b); got != `["submitted"]` {
		t.Fatalf("timer-free marking = %s, want [\"submitted\"] (compact form preserved)", got)
	}
}

func TestEnteredAt_SerializationRoundTrip(t *testing.T) {
	def := timedApprovalDef(t, time.Hour)
	wf, err := NewWorkflow("wf", def, "submitted", WithClock(fixedClock(epoch)))
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}
	b, err := json.Marshal(wf.marking)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	m, err := UnmarshalMarkingJSON(b)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	toks := m.TokensAt("submitted")
	if len(toks) != 1 {
		t.Fatalf("TokensAt(submitted) = %d tokens, want 1", len(toks))
	}
	at, ok := toks[0].EnteredAt()
	if !ok {
		t.Fatal("round-tripped token lost its entry time")
	}
	if !at.Equal(epoch) {
		t.Fatalf("entry time = %v, want %v", at, epoch)
	}
}

func TestDue_UnstampedTokenNotRetroactivelyDue(t *testing.T) {
	// Simulate pre-timer persisted state: a marking whose token has no entry time,
	// loaded into a workflow whose definition since gained a timer.
	def := timedApprovalDef(t, time.Hour)
	legacy := NewMarking([]Place{"submitted"}) // {} token, zero enteredAt
	wf, err := NewWorkflowFromMarking("wf", def, legacy, WithClock(fixedClock(epoch)))
	if err != nil {
		t.Fatalf("NewWorkflowFromMarking: %v", err)
	}
	// Construction stamps the initial marking, so overwrite with the raw legacy
	// marking as a loader would via SetMarking (no stamping).
	if err := wf.SetMarking(NewMarking([]Place{"submitted"})); err != nil {
		t.Fatalf("SetMarking: %v", err)
	}
	if due := wf.Due(epoch.Add(1000 * time.Hour)); len(due) != 0 {
		t.Fatalf("Due on unstamped legacy token = %v, want none (not retroactively due)", transitionNames(due))
	}
	if _, ok := wf.NextDue(); ok {
		t.Fatal("NextDue on unstamped legacy token returned a deadline, want none")
	}
}

func TestNewWorkflowFromMarking_PreservesPersistedStamps(t *testing.T) {
	// A stamped marking marshalled and reloaded must keep its running timer when
	// adopted via NewWorkflowFromMarking — the workflow must not reset enteredAt.
	def := timedApprovalDef(t, 72*time.Hour)
	seed, err := NewWorkflow("wf", def, "submitted", WithClock(fixedClock(epoch)))
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}
	blob, err := json.Marshal(seed.Marking())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	restored, err := UnmarshalMarkingJSON(blob)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Construct with a clock a year later: the persisted stamp (epoch) must win.
	muchLater := epoch.Add(365 * 24 * time.Hour)
	wf, err := NewWorkflowFromMarking("wf", def, restored, WithClock(fixedClock(muchLater)))
	if err != nil {
		t.Fatalf("NewWorkflowFromMarking: %v", err)
	}
	toks := wf.GetTokens("submitted")
	if len(toks) != 1 {
		t.Fatalf("TokensAt(submitted) = %d, want 1", len(toks))
	}
	at, ok := toks[0].EnteredAt()
	if !ok || !at.Equal(epoch) {
		t.Fatalf("enteredAt = %v (ok=%v), want %v (original stamp preserved)", at, ok, epoch)
	}
	// The 72h deadline is measured from the original entry, so it is already
	// elapsed a year later.
	if due := wf.Due(muchLater); len(due) != 1 || due[0].Name() != "escalate" {
		t.Fatalf("Due(muchLater) = %v, want [escalate] (elapsed timer restored)", transitionNames(due))
	}
	if next, ok := wf.NextDue(); !ok || !next.Equal(epoch.Add(72*time.Hour)) {
		t.Fatalf("NextDue = %v (ok=%v), want %v", next, ok, epoch.Add(72*time.Hour))
	}
}

func TestProduce_BooleanPresenceIdempotentWithTimers(t *testing.T) {
	// An unrelated timed transition turns on entry-time stamping; two untimed
	// transitions both feeding place c must still leave exactly one token at c,
	// exactly as the timer-free definition would.
	t1 := MustNewTransition("t1", []Place{"a"}, []Place{"c"})
	t2 := MustNewTransition("t2", []Place{"b"}, []Place{"c"})
	timer := MustNewTransition("timer", []Place{"x"}, []Place{"y"})
	timer.SetTimeoutAfter(time.Hour)
	def, err := NewDefinition(
		[]Place{"a", "b", "c", "x", "y"},
		[]Transition{*t1, *t2, *timer},
	)
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}
	wf, err := NewWorkflowFromMarking("wf", def, NewMarking([]Place{"a", "b"}), WithClock(fixedClock(epoch)))
	if err != nil {
		t.Fatalf("NewWorkflowFromMarking: %v", err)
	}
	if err := wf.ApplyTransition("t1"); err != nil {
		t.Fatalf("ApplyTransition(t1): %v", err)
	}
	if err := wf.ApplyTransition("t2"); err != nil {
		t.Fatalf("ApplyTransition(t2): %v", err)
	}
	if got := wf.TokenCount("c"); got != 1 {
		t.Fatalf("TokenCount(c) = %d, want 1 (boolean presence stays idempotent)", got)
	}
}

func TestCreateToken_StampsTimedPlace(t *testing.T) {
	// t: seeded --escalate(1h)--> done. A token seeded via CreateToken into the
	// timed input place must carry an entry time so the deadline runs.
	escalate := MustNewTransition("escalate", []Place{"seeded"}, []Place{"done"})
	escalate.SetTimeoutAfter(time.Hour)
	def, err := NewDefinition([]Place{"seeded", "done"}, []Transition{*escalate})
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}
	// Start the workflow elsewhere so CreateToken (not construction) does the stamp.
	wf, err := NewWorkflowFromMarking("wf", def, NewMarking([]Place{"done"}), WithClock(fixedClock(epoch)))
	if err != nil {
		t.Fatalf("NewWorkflowFromMarking: %v", err)
	}
	wf.ClearPlace("done")
	if _, err := wf.CreateToken("seeded", TokenData{"id": "1"}); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	next, ok := wf.NextDue()
	if !ok || !next.Equal(epoch.Add(time.Hour)) {
		t.Fatalf("NextDue = %v (ok=%v), want %v (entry+timeout)", next, ok, epoch.Add(time.Hour))
	}

	// Adding a second token later must not cancel the running deadline: the
	// earliest entry still governs, and both tokens are stamped (so the place
	// stays 'known').
	wf.setClock(fixedClock(epoch.Add(30 * time.Minute)))
	if _, err := wf.CreateToken("seeded", TokenData{"id": "2"}); err != nil {
		t.Fatalf("CreateToken(second): %v", err)
	}
	next, ok = wf.NextDue()
	if !ok || !next.Equal(epoch.Add(time.Hour)) {
		t.Fatalf("NextDue after second token = %v (ok=%v), want %v (deadline unchanged)", next, ok, epoch.Add(time.Hour))
	}
}

func TestDefinitionTransition_MutationApplies(t *testing.T) {
	// def.Transition(name) must return a pointer into the definition, so a timeout
	// set on it after construction actually drives the engine.
	t1 := MustNewTransition("t1", []Place{"a"}, []Place{"b"})
	t2 := MustNewTransition("t2", []Place{"b"}, []Place{"c"})
	def, err := NewDefinition([]Place{"a", "b", "c"}, []Transition{*t1, *t2})
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}
	fireTime := epoch.Add(time.Hour)
	wf, err := NewWorkflow("wf", def, "a", WithClock(fixedClock(fireTime)))
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}
	// Set the timeout AFTER construction, through the definition pointer.
	tr := def.Transition("t2")
	if tr == nil {
		t.Fatal("def.Transition(t2) = nil")
	}
	tr.SetTimeoutAfter(time.Hour)
	if d, ok := def.Transition("t2").TimeoutAfter(); !ok || d != time.Hour {
		t.Fatalf("TimeoutAfter after mutation = %v,%v, want 1h,true (pointer mutation applied)", d, ok)
	}
	// Fire t1: produce() sees the (now) timed definition and stamps b's token.
	if err := wf.ApplyTransition("t1"); err != nil {
		t.Fatalf("ApplyTransition(t1): %v", err)
	}
	next, ok := wf.NextDue()
	if !ok || !next.Equal(fireTime.Add(time.Hour)) {
		t.Fatalf("NextDue = %v (ok=%v), want %v (timer set via pointer works)", next, ok, fireTime.Add(time.Hour))
	}
}

func transitionNames(ts []Transition) []string {
	out := make([]string, len(ts))
	for i := range ts {
		out[i] = ts[i].Name()
	}
	return out
}

// steppedClock is a mutable clock for tests that advance time between steps.
type steppedClock struct{ now time.Time }

func (c *steppedClock) fn() func() time.Time { return func() time.Time { return c.now } }
