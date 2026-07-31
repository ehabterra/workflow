# Colored Petri Nets (Tokens)

The workflow engine models state as a **Colored Petri Net (CPN) marking**: every
place holds a multiset of **tokens**, and each token can carry data (its
"color"). A plain boolean workflow is simply the trivial case — places hold a
single *uncolored* token (no id, no data) — so you only reach for tokens when you
actually need data-carrying state such as batch processing.

There is no "CPN mode" to turn on. The token methods are available on every
`Workflow`; using them just adds tokens with data.

## The model at a glance

| Concept | Boolean workflow | Colored Petri Net |
| --- | --- | --- |
| A place is "present" | holds 1 uncolored token | holds ≥ 1 token |
| Token | anonymous marker | `id` + `data` (`TokenData`) |
| Transition firing | move presence | move tokens (data preserved) |
| Cost | one map entry per place | proportional to tokens you create |

Overhead is pay-for-what-you-use: a workflow that never creates a colored token
serializes to the same compact form as before (`["review"]`); the moment you add
token data it serializes to a `{place: [tokens…]}` object.

## Tokens

```go
tok := workflow.NewToken(workflow.TokenData{"order_id": "A", "amount": 100})
tok.ID()                 // unique, generated id
v, ok := tok.Get("amount")
tok2 := tok.With("paid", true) // returns a NEW token; tok is unchanged
```

Tokens are **value types with copy-on-access data** — a token sitting in a
marking can't be mutated by aliasing. Identity is the `id`, not the data: two
tokens with identical data are still distinct.

## Working with tokens on a workflow

```go
wf, _ := workflow.NewWorkflow("batch", def, "pending")

// Seed a place with exactly these tokens (ClearPlace drops the initial
// presence token so it doesn't linger).
wf.ClearPlace("pending")
wf.CreateTokens("pending", []workflow.TokenData{
    {"id": "A", "amount": 100},
    {"id": "B", "amount": 250},
})

wf.GetTokens("pending")   // []Token (copy)
wf.TokenCount("pending")  // 2
wf.AllTokens()            // map[Place][]Token
```

## Firing transitions with tokens

A transition moves the tokens from its input places to its output places, leaving
every other place untouched. Two ways to fire:

```go
// Whole-batch: every token at the input place advances.
wf.ApplyTransition("start")               // pending -> processing (all tokens)

// Per-token: advance exactly one token out of a place holding many.
wf.ApplyTransitionForToken(ctx, "start", someTokenID)
```

Movement rules:

- **Single output place** (the common linear/batch case): consumed tokens keep
  their identities.
- **AND-split** (multiple outputs): each output receives a fresh copy (new id) of
  every consumed token.
- **No colored tokens involved**: falls back to boolean presence, so existing
  boolean workflows behave exactly as before.

## Dynamic-cardinality joins: waiting for a runtime-resolved set

The rules above answer "the transition is enabled, now move the tokens". They do
not answer **"wait until enough tokens are here"** — and how many is *enough* is
usually not a constant. An approval chain's length comes from the record's
value; a shipping batch's size comes from configuration.

`require:` puts that condition in the definition. Per input place it declares how
many tokens are needed (an expression, evaluated at fire time), optionally which
tokens count, and optionally a field they must be distinct by:

```yaml
transitions:
  - name: approve_final
    from: [submitted, approvals]
    to: [approved]
    resets: [approvals]                  # discard what the join did not take
    require:
      - place: approvals
        where: "token.role in chain"     # only chain members count
        distinct: role                   # two approvals from one role are one
        count: "len(chain)"              # `chain` comes from the instance context
```

```go
// The same thing in Go.
final := workflow.MustNewTransition("approve_final",
    []workflow.Place{"submitted", "approvals"}, []workflow.Place{"approved"})
final.SetRequirements(workflow.MustNewRequirement(workflow.RequirementSpec{
    Place: "approvals", Where: "token.role in chain", Distinct: "role", Count: "len(chain)",
}))
```

Both expressions see the workflow context; `where` also sees the token's data as
`token`, and `count` sees every token at the place as `tokens`. `count` must
yield a non-negative integer (a `float64` that came back from JSON storage is
fine — a fractional one is an error).

**Consumption is the part to internalize.** An ordinary input place is drained
when a transition fires. A *required* place is not: firing consumes exactly the
tokens the requirement selected — in the place's own order, so it is
deterministic — and leaves the remainder behind. That is what makes "take a
batch of 10 out of a pool of 50" a definition rather than a loop:

```yaml
  - name: ship_batch
    from: [pool]
    to: [shipped]
    require:
      - place: pool
        count: 10
```

Notes worth knowing before you reach for it:

- An unmet requirement is `ErrNotEnabled`, so `ApplyAny` skips that candidate and
  tries the next — which is how "record progress" and "complete the chain"
  become two transitions with no host-side branch. A requirement that cannot be
  *evaluated* (a broken expression, a count that is not a number) is a hard
  error instead, so a broken definition is never silently skipped.
- The selection is re-resolved under the write lock immediately before the move,
  so concurrent firings split a pool rather than double-consuming it.
- `require` cannot be combined with `from_any`, two requirements cannot target
  the same place, and a requirement's place must be one of the transition's own
  inputs. Each rule keeps exactly one selector per input; `NewDefinition`
  enforces them.
- `ApplyTransitionForToken` is rejected on a transition with requirements — the
  requirement already chooses the tokens.
- Reset arcs still clear **whole** places, so a place that is both required and
  reset ends up empty regardless of what the join took.
- Requirements are part of `Definition.Fingerprint()` and are rendered on the
  transition in `Diagram()`. They are *not* evaluated by the `Due` API, exactly
  like guards: a due transition still has to be enabled when it fires.

Use `SelectTokens(place, pred)` to choose which token(s) to advance:

```go
highValue := wf.SelectTokens("pending", func(t workflow.Token) bool {
    v, _ := t.Get("amount"); return v.(float64) >= 100
})
for _, t := range highValue {
    wf.ApplyTransitionForToken(ctx, "start", t.ID())
}
```

## Token-aware guards and events

Guards and event listeners can see the token(s) being moved. During per-token
firing the token's data is exposed to guard expressions as `token`, so a
transition can route or gate on the token itself:

```yaml
transitions:
  - name: auto_approve
    from: [pending]
    to: [approved]
    guard: "token.amount <= 1000"   # only small orders auto-approve
```

```go
// token.amount <= 1000 → advances; otherwise ErrTransitionNotAllowed
err := wf.ApplyTransitionForToken(ctx, "auto_approve", tok.ID())
```

Event listeners receive the involved tokens via `event.Tokens()` — one for
per-token firing, or all the moved colored tokens for a whole-marking firing —
which is what a history/audit listener records:

```go
wf.AddEventListener(workflow.EventAfterTransition, func(e workflow.Event) error {
    for _, t := range e.Tokens() {
        log.Printf("moved token %s: %v", t.ID(), t.Data())
    }
    return nil
})
```

In the guard environment, a single token is `token` (a data map, so
`token.amount` works) and all involved tokens are `tokens`.

## Queries, aggregation, transformation

These operate on colored tokens (uncolored presence tokens are skipped):

```go
wf.CountTokens(pred)                 // total matching colored tokens
wf.FindTokens(pred)                  // map[Place][]Token, grouped by place

agg := wf.AggregateTokens(nil, "amount")
// agg.Count, agg.Sum, agg.Min, agg.Max, agg.Avg  (numeric field; non-numeric ignored)

wf.TransformTokens("pending",
    func(t workflow.Token) bool { v,_ := t.Get("amount"); return v.(float64) >= 100 },
    func(t workflow.Token) workflow.TokenData {
        d := t.Data(); d["amount"] = d["amount"].(float64) * 1.1; return d
    },
) // rewrites matching tokens' data, preserving identity; returns count changed
```

## Declaring tokens in YAML

The starting state is declared with `initial_marking`, which accepts three forms.
The simple case is a one-liner; colored tokens use the map form:

```yaml
# boolean shorthand — one presence token
initial_marking: draft

# several presence places
initial_marking: [design, legal]

# Colored Petri Net — data-carrying tokens per place
workflow:
  name: batch
  initial_marking:
    pending:
      - {order_id: "001", amount: 100}
      - {order_id: "002", amount: 250}
  places: [{name: pending}, {name: processing}, {name: done}]
  transitions:
    - {name: start,  from: [pending],    to: [processing]}
    - {name: finish, from: [processing], to: [done]}
```

A place with no listed tokens gets a single uncolored presence token; a place
with tokens is seeded with exactly those. Unknown keys are still rejected by
strict decoding, so typos fail loudly.

## Persistence

Storage persists the **whole marking**, so colored tokens round-trip. The wire
format is adaptive:

- Simple boolean workflow → compact place array `["review"]`.
- Colored tokens → `{"pending": [{"id":"…","data":{…}}]}`.

Old state written as a place array still loads, so **no data migration is
required** when upgrading.

## Migration notes for custom backends

If you implement `workflow.Storage` yourself, the interface now persists a
`workflow.Marking` instead of `[]workflow.Place`:

```go
// before
SaveState(ctx, id string, places []workflow.Place, ctx map[string]any) error
// now
SaveState(ctx, id string, marking workflow.Marking, ctx map[string]any) error
```

Marshal the marking with `json.Marshal(marking)` and restore it with
`workflow.UnmarshalMarkingJSON(data)`. The bundled SQLite and Postgres backends
already do this, and the `storagetest` conformance kit includes a colored-token
round-trip both backends pass.
