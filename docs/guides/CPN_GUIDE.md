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

Use `SelectTokens(place, pred)` to choose which token(s) to advance:

```go
highValue := wf.SelectTokens("pending", func(t workflow.Token) bool {
    v, _ := t.Get("amount"); return v.(float64) >= 100
})
for _, t := range highValue {
    wf.ApplyTransitionForToken(ctx, "start", t.ID())
}
```

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

Seed colored tokens with `initial_tokens`, keyed by place:

```yaml
workflow:
  name: batch
  initial_place: pending
  places: [{name: pending}, {name: processing}, {name: done}]
  initial_tokens:
    pending:
      - {order_id: "001", amount: 100}
      - {order_id: "002", amount: 250}
  transitions:
    - {name: start,  from: [pending],    to: [processing]}
    - {name: finish, from: [processing], to: [done]}
```

Each listed place is seeded with exactly the declared tokens. Unknown keys are
still rejected by strict decoding, so typos fail loudly.

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
