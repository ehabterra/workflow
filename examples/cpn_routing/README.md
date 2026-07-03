# CPN Guard-Based Routing Example

Routes a batch of orders to `approved` or `review` **purely by YAML-declared
guards** that inspect each token's own data (`token.amount`). No routing logic
lives in Go — the guards decide.

Run it:

```bash
go run ./examples/cpn_routing/
```

Output:

```
Received 4 orders at 'pending'.
Routing each order — the transition guards decide, not this program:
  A-1  amount 500   → auto-approved
  A-2  amount 1500  → manual review
  A-3  amount 250   → auto-approved
  A-4  amount 9000  → manual review

Result: approved=2  review=2  pending=0
```

## How it works

The workflow is loaded entirely from [`workflow.yaml`](./workflow.yaml). Orders
are seeded as colored tokens via `initial_marking`, and two transitions leave
`pending`, each with a guard on the token being fired:

```yaml
initial_marking:
  pending:
    - {order_id: "A-1", amount: 500}
    - {order_id: "A-2", amount: 1500}
transitions:
  - {name: auto_approve,  from: [pending], to: [approved], guard: "token.amount <= 1000"}
  - {name: manual_review, from: [pending], to: [review],   guard: "token.amount > 1000"}
```

`ApplyTransitionForToken(ctx, name, id)` fires a transition **only if its guard
accepts that token** — a blocked guard returns an error and leaves the token in
place, so the program simply tries each candidate transition in turn:

```go
switch {
case wf.ApplyTransitionForToken(ctx, "auto_approve", tok.ID()) == nil:
    // guard token.amount <= 1000 passed
case wf.ApplyTransitionForToken(ctx, "manual_review", tok.ID()) == nil:
    // guard token.amount > 1000 passed
}
```

Inside a guard expression, the token being fired is `token` (a data map), so
`token.amount` reads that order's amount. See
[docs/guides/CPN_GUIDE.md](../../docs/guides/CPN_GUIDE.md#token-aware-guards-and-events).
