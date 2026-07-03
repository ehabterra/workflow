# CPN Batch Processing Example

Demonstrates the Colored Petri Net (CPN) features of the workflow engine using a
batch of orders that flow `pending → processing → done`. Each order is a
**token** carrying its own data (`id`, `amount`).

Run it:

```bash
go run ./examples/cpn_batch_processing/
```

## What it shows

| Feature | API |
| --- | --- |
| Seed data-carrying tokens | `wf.CreateTokens("pending", []workflow.TokenData{...})` |
| Start a place from a clean slate | `wf.ClearPlace("pending")` |
| Aggregate over a numeric field | `wf.AggregateTokens(nil, "amount")` |
| Rewrite matching tokens' data | `wf.TransformTokens(place, pred, fn)` |
| Advance one token out of a batch | `wf.ApplyTransitionForToken(ctx, "start", id)` |
| Advance every token at once | `wf.ApplyTransition("start")` |
| Read tokens at a place | `wf.GetTokens("done")` |

A plain (boolean) workflow is just the special case where places hold a single
uncolored token — the same `ApplyTransition` calls work unchanged. See
[docs/guides/CPN_GUIDE.md](../../docs/guides/CPN_GUIDE.md) for the full model.

## Declarative equivalent

The same initial batch can be declared in YAML with `initial_marking` (its map
form seeds colored tokens per place):

```yaml
workflow:
  name: batch
  initial_marking:
    pending:
      - {id: "A", amount: 100}
      - {id: "B", amount: 250}
      - {id: "C", amount: 40}
  places: [{name: pending}, {name: processing}, {name: done}]
  transitions:
    - {name: start,  from: [pending],    to: [processing]}
    - {name: finish, from: [processing], to: [done]}
```
