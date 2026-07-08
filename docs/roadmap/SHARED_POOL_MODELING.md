# Design note — modeling a shared batch/pool across many workflow instances

**Status:** open discussion (no code committed). Graduated from
[`FRICTION.md`](./FRICTION.md) entry #3 (`batch_control` anchor).

**Owners / participants:** Ehab + Claude.

---

## 1. The concrete problem

The dogfood has two nets:

- **expense approval** — one instance *per expense* (`draft → … → approved → paid`).
- **payment batch** — **one** long-lived instance for the whole system
  (`examples/expense_approval/payment.yaml`). Every approved expense becomes a
  **colored token** in the single `payable` place carrying
  `{expense_id, amount, submitter}`; a batch run fires `pay` (auto, `amount ≤ 5000`)
  or `release` (manual, guard-held) to move it to `paid_out`.

The coupling between the two nets is **not in the model** — it is hand-coded in
`app.go` as a process manager: `EnqueueApproved` (expense→`payable`) and
`RunBatch → mark_paid` (payment→expense).

Two symptoms surfaced this design question:

1. **`batch_control`** — an always-marked place wired to *no transition*, holding
   one permanent presence token, existing only because the engine rejects a
   persisted marking with zero marked places (`loaded state has no places`). A
   pure persistence smell (FRICTION #3).
2. **"One cell can't hold all this state."** The single `payable` place holds
   *every* pending expense token in *one* marking, in *one* instance. That is one
   unbounded, hot, single-writer cell.

## 2. Sharpened problem statement

> Many independent producer instances (expenses) must hand work to a shared,
> long-lived consumer (the batch payer), which applies a **global policy** (the
> `≤ 5000` guard / manual release) across items from all producers.

Two axes matter, and they pull in opposite directions:

- **Theoretical faithfulness** — what is the *correct* Petri-net / workflow
  construct for shared cross-instance state? (See the research summary, §5.)
- **Operational scalability** — a single shared place/marking is:
  - **one unbounded marking** — the consumer instance's persisted state grows
    with the pending count (cf. Temporal `continue-as-new`, which exists because
    one entity's history cannot grow without bound);
  - **one optimistic-lock unit** — *every* producer writes the *same* instance,
    so producers serialize and contend (this is the same single-writer row that
    produced the `LoadVersionedState` lost-update bug in M5.1);
  - **one blast radius** — corruption or a poison token affects the whole pool.

**Key finding: theoretical correctness ≠ operational scalability.** The rcwf-net
"static place shared by cases" is *correct* for reasoning about soundness, but it
says nothing about write contention or unbounded state. The scalable answers
*distribute* the state and reconstruct the pool as a *view* or a *queue*.

## 3. Options

### A. Singleton pool net *(current)*
One long-lived instance; one `payable` place holds all tokens.
- **Pros:** simplest; the whole pool is one place you can query/iterate; batch run
  is trivial; matches the CPN "fusion place" / aggregator intuition; the pool is
  visible as first-class net state (nice diagram).
- **Cons:** one **fat, unbounded marking**; **single-writer contention** — all
  approvals write one instance (lock retries under load); needs the
  `batch_control` anchor; a hot instance is a hard scaling ceiling; single blast
  radius.
- **Verdict:** acceptable only at low volume (the dogfood). Honest demo choice.

### B. Per-instance state + aggregate-on-read *(no stored pool)*
Each expense owns its payment state (either payment places inside the expense net,
or a small payment child per expense). The "pool" is a **query / read-model** — a
cross-instance token/state query for "everything in `payable`", not a stored
place. The batch run = *query all pending* + fire each.
- **Pros:** **no fat cell, no shared hot row, no contention** — writes are
  naturally partitioned per expense; no anchor; each instance stays tiny; scales
  horizontally. This is the rcwf **case-isolation** idea plus CQRS read-model.
- **Cons:** the pool is a *derived view*, not first-class state; the global policy
  (guard) is applied at batch/query time rather than enforced structurally;
  "pay these 100 atomically as one unit" is harder; needs an efficient
  cross-instance index/query (partially exists via `AggregateTokens`).
- **Verdict:** the scalable, PN-native default.

### C. Proclets (first-class interaction channels)
Model expense and payment as interacting **proclet** classes; approval sends a
performative to the payment proclet (correlated by id; class broadcast/multicast
for one-to-many).
- **Pros:** theoretically the cleanest model of *interacting cases*; both sides
  stay small; many-to-one is native.
- **Cons:** no mainstream implementation; large, exotic build; overkill for this
  library's stage.
- **Verdict:** reference model, not a near-term build.

### D. Sharded / batch-keyed pools
Not one payment instance but one **per batch window** (`payment-2026-07-08`) or per
shard. Tokens land in the current batch; each pool is bounded and short-lived.
- **Pros:** bounds the fat-cell and contention problem (N shards → N× less
  contention, each marking bounded); batches become first-class, archivable
  objects; matches real "daily payment run" semantics.
- **Cons:** routing logic (which shard?); still a mini-pool per shard (same
  pattern, smaller); "all pending across shards" needs fan-out queries.
- **Verdict:** strong when *batches themselves* are domain objects; composes with B.

### E. Externalize to a queue / outbox *(pool ≠ net)*
The pool is really a message queue. Approved expenses publish to a queue/outbox;
the batch consumer drains it. The engine coordinates via saga/process-manager; the
pool is infrastructure, not net state.
- **Pros:** queues are *built* for fan-in, backpressure, at-least-once, scaling;
  no fat marking; no engine contention; the guard-hold becomes a filter/DLQ.
  Honest about what the pool actually is.
- **Cons:** leaves the Petri-net model (arguably correct — the pool was never a
  workflow); needs queue/outbox integration; loses the "one net" elegance and the
  pool diagram.
- **Verdict:** the honest high-throughput answer.

### F. Native rcwf static / shared place in the engine
Build the library feature: a place shared across instances, with engine-managed
concurrency (shared marking cell + locking, or a separate shared-state store).
- **Pros:** first-class shared pool; the model stays a net; theoretically grounded
  (rcwf static places, CPN global fusion).
- **Cons:** **largest build**; a shared cell *still* hits the one-cell
  contention/fat-state problem unless it **shards internally** — which is the tell
  that the single shared place was never the scalable unit. Reopens concurrency,
  locking, and cross-instance transaction questions.
- **Verdict:** only if the library commits to native shared state — and only sharded.

## 4. Comparison

| # | Option | State locality | Scales | Anchor needed | Build cost | Pool is… |
|---|--------|----------------|--------|---------------|------------|----------|
| A | Singleton pool net *(current)* | 1 fat cell | ❌ | yes (smell) | none | net state |
| B | Per-instance + aggregate-on-read | distributed, tiny | ✅ | no | small | a read-model |
| C | Proclets | distributed | ✅ | no | large | interactions |
| D | Sharded batch pools | bounded/shard | ✅ | per-shard | small–med | net state (many) |
| E | Queue / outbox | in the queue | ✅✅ | no | med | infrastructure |
| F | Native shared place | 1 shared cell | ❌ unless sharded | maybe | largest | net state |

## 5. Research summary (theory + standards + engines)

Full cited version in chat; condensed:

- **Substitution transitions** = refinement (1 super-node → 1 subpage, socket/port
  interface). **NOT** for shared state → HCPN nesting is the *wrong* tool for a
  shared many-to-one pool.
- **Fusion places** (CPN) = the shared-state construct *within one model*.
- **Resource-Constrained WF-nets** use **static places** to model *global
  resources shared by cases* — the closest classic construct to "N cases share one
  pool" (but classic resources are *durable*; `payable` is a *buffer*, tokens are
  created/consumed).
- **Proclets** (van der Aalst & Barthelmess) = first-class *interacting cases*,
  with broadcast to a whole class — the faithful model of inter-case interaction.
- **Soundness / empty marking:** a WF-net has one source + one sink and terminates
  with a token *in the sink* — a per-**case** property. The pool is **not** a
  WF-net (cyclic, long-lived, no single start/end), so an **empty marking is a
  valid state**. An anchor token purely to keep a marking non-empty is a
  **persistence smell**, not a modeling requirement.
- **Engines converge on singleton-aggregator + coordination, none use an anchor:**
  Camunda **Message Aggregation** (many producers → one long-lived instance by
  correlation key); Temporal **entity/actor workflow** (long-lived stateful
  singleton, signals, `continue-as-new` — sits idle/empty with no anchor); DDD
  **process manager / saga** (coordinate multiple aggregates; `app.go` already
  *is* one).

### References
- Jensen & Kristensen — Coloured Petri Nets (hierarchical CPN; fusion vs
  substitution): <https://home.hvl.no/ansatte/lmkr/talks/lmk_hierarchicalcpns_pn2014.pdf>
- Resource Workflow Nets (static/resource places): <https://www.scitepress.org/Papers/2007/24210/24210.pdf>
- Dynamic Soundness in Resource-Constrained WF-nets: <https://www.researchgate.net/publication/220703010_Dynamic_Soundness_in_Resource-Constrained_Workflow_Nets>
- van der Aalst — Verification of Workflow Nets (soundness, source/sink): <https://www.vdaalst.com/publications/p44.pdf>
- van der Aalst & Barthelmess — Proclets: <https://www.vdaalst.rwth-aachen.de/publications/p106.pdf>
- Camunda 8 — Message aggregation: <https://docs.camunda.io/docs/components/concepts/message-aggregation/>
- Temporal — entity workflows: <https://temporal.io/blog/entity-workflow-loyalty-points>
- Process managers & sagas (DDD): <https://hosseinnejati.medium.com/process-managers-and-sagas-in-ddd-coordinating-long-running-workflows-61193672ad5c>
- ISO/IEC 15909-2 primer / PNML: <https://www.pnml.org/papers/pnnl76.pdf>

## 6. Recommendation (tiered by scale)

1. **Now / dogfood:** keep **A**, but fix empty-marking persistence so
   `batch_control` disappears (FRICTION #3). It is demo-scale — say so in the
   example README.
2. **Default for real use:** **B** (per-instance state + aggregate-on-read),
   composing **D** (sharded batch pools) when batches are first-class domain
   objects. Requires one library capability: an efficient **cross-instance
   token/state query (read-model)** — partially present via `AggregateTokens`.
3. **High throughput:** **E** (queue/outbox), with the `app.go` process manager
   formalized as a named saga.
4. **F** (native shared place) only if the library commits to native shared state
   — and only sharded.

## 7. Open follow-up questions

- **Q1.** Is a *batch* a first-class domain object here (auditable, re-runnable,
  archivable)? If yes → **D**. If the batch is just "whatever is pending now" →
  **B** with an ad-hoc query.
- **Q2.** What is the realistic pending-pool size / approval rate? Tens (A is
  fine) vs thousands concurrent (B/E needed)?
- **Q3.** Must the global policy (guard) be *enforced structurally* (in the net)
  or is *applied at batch time* (in a query/consumer) acceptable? Structural
  enforcement pushes toward a shared place; batch-time is fine for B/E.
- **Q4.** Does the library want a first-class **cross-instance read-model / token
  index** as a feature (enables B and D cleanly)? This may be the highest-leverage
  library addition surfaced by the dogfood.
- **Q5.** Should the engine ever model **infrastructure queues** (E), or stay a
  pure state machine and leave queues to the host? Answer sets the boundary of the
  library.
- **Q6.** Regardless of pool design: fix the empty-marking persistence invariant
  now (independent, small) — agreed? It kills `batch_control` and unblocks A/D.

## 8. Feedback / decisions log

- 2026-07-08 — Ehab: "one marking for the whole batch — one cell can't hold all
  this state." → reframed the whole question around *state locality/scalability*,
  not just theoretical correctness; added the operational axis (§2) and options
  B/D/E as the distributed answers.
- 2026-07-08 — Ehab: "a child `token_states` table should solve this" →
  confirmed as the storage foundation that unlocks B; caveat recorded that the
  table fixes the fat blob + read-model but **not** write contention on its own
  (contention is set by *ownership + version granularity*, not by where the
  bytes live).
- 2026-07-08 — **DECISION: pursue "B via storage-only" (see §9).** The net,
  YAML, and firing API do **not** change; only the persistence layer evolves
  (blob marking → normalized `token_states` rows). `batch_control` is removed by
  relaxing the empty-marking check. Contention is left at the current
  per-instance-version level for now (simple flavor); the token-delta / per-row
  variant is deferred until measurement shows it's needed (YAGNI). Concurrency
  ceiling (§7 Q2) still to be confirmed to lock simple-vs-delta.
- 2026-07-08 — **SHIPPED: step 1 of §9 (empty-marking fix + anchor removal).**
  Empty markings now construct (`NewWorkflowFromMarking`), create
  (`Manager.CreateWorkflowFromMarking`), save, and load; YAML may omit
  `initial_marking`; the storagetest kit gained `EmptyMarkingRoundTrip`. The
  dogfood deleted `batch_control` from `payment.yaml` and its migration hook
  grew real place-removal logic (`migratePaymentAnchor` rewrites stored
  markings; covered by `TestPaymentAnchorMigration`). FRICTION #3 closed.
- 2026-07-08 — **SHIPPED: step 2 of §9 (token normalization, SIMPLE flavor).**
  Both SQL backends now persist the marking as one row per token in a child
  table and blank the `state` blob; the cross-instance read-model landed as
  `workflow.TokenQueryStorage` / `Manager.ListPlaceTokens`. Q2 was answered
  implicitly ("simple flavor is fine"): concurrency stays per-instance
  whole-marking overwrite; §9.5 (delta) remains deferred. Deviations from
  the sketch are recorded in §9.6. **This doc's open work is done** — what
  remains live is only the Q1/Q3 modeling choice (singleton vs per-instance
  tokens), which the read-model makes freely swappable later.
- _(add decisions here as they are made)_

---

## 9. Chosen direction — "B via storage-only" (sketch)

**Principle:** the workflow model is untouched — same nets, same YAML, same
`fire`/`Execute` API, same `Marking` type in memory. Everything below changes
inside the **persistence layer** (the `storage` package + the manager's
load/save wiring). Today a whole marking is serialized as JSON into one
`state TEXT` column, one row per instance, guarded by one `version` (see
`storage/sqlite.go`). We normalize the token payload out of that blob into a
child table.

### 9.1 `token_states` schema

The engine's token is `Token{ id TokenID, data TokenData }` where `TokenData =
map[string]any`; `ID()` is empty for an uncolored presence token. A marking is a
multiset of tokens per place, so the full marking is reconstructable from token
rows alone — the instance row keeps only version + context.

```sql
-- instance table (existing, trimmed): tokens no longer live in `state`
--   workflow_states(id TEXT PK, context TEXT, version INTEGER, ...)
--   (the `state` JSON column for tokens is dropped; see migration 9.4)

CREATE TABLE token_states (
    seq         INTEGER PRIMARY KEY,            -- surrogate row id
                                                --   SQLite: INTEGER PRIMARY KEY autoincrements
                                                --   Postgres: BIGINT GENERATED ALWAYS AS IDENTITY
    workflow_id TEXT    NOT NULL,               -- FK -> workflow_states(id)
    place       TEXT    NOT NULL,               -- which place holds this token
    token_id    TEXT    NOT NULL DEFAULT '',    -- Token.ID(); '' = uncolored presence token
    data        TEXT    NOT NULL DEFAULT '{}',  -- JSON of TokenData ('{}' = no colored payload)
    UNIQUE (workflow_id, place, token_id)       -- colored tokens unique by id within a place;
                                                --   presence token is one-per-place ('' sentinel)
);

-- load one instance's marking:
CREATE INDEX idx_token_states_wf    ON token_states (workflow_id);
-- THE read-model: the cross-instance pool as a query (RunBatch, dashboards):
CREATE INDEX idx_token_states_place ON token_states (place, workflow_id);
```

Notes:
- A boolean/presence-only net (the expense net) just gets one row per marked
  place — a handful of rows, no downside.
- `data` stays opaque JSON (matches how colored tokens already serialize); no
  per-field columns, so arbitrary `TokenData` is preserved.
- If a place must hold *multiple* uncolored tokens, drop the `UNIQUE` constraint
  and rely on `seq`; the engine's presence model is one-per-place, so the
  constraint documents and enforces that.

### 9.2 Load / Save changes (SIMPLE flavor — model & concurrency unchanged)

`LoadState(id)` — reconstruct the marking from rows, in ONE snapshot to avoid the
read-skew that caused the M5.1 `LoadVersionedState` lost-update bug:

```
BEGIN (repeatable-read / single tx)
  SELECT version, context FROM workflow_states WHERE id = ?
  SELECT place, token_id, data FROM token_states WHERE workflow_id = ?
COMMIT
-> rebuild Marking by adding one token per row
```

`SaveState(id, marking, ctx, version)` — keep the current **whole-marking
overwrite** contract and the **per-instance optimistic version** (so the atomic
`load → fire → save` guarantee is byte-for-byte the same):

```
BEGIN
  UPDATE workflow_states
     SET version = version + 1, context = ?
   WHERE id = ? AND version = ?          -- 0 rows affected => ErrConflict (unchanged)
  DELETE FROM token_states WHERE workflow_id = ?
  INSERT INTO token_states (...) VALUES  -- one row per token in the new marking
    ... (bulk)
COMMIT
```

The delete+reinsert keeps it dead simple and correct; at moderate write rates the
row churn is negligible. Contention is exactly what it is today (one version per
instance) — acceptable per the YAGNI decision.

### 9.3 Read-model bonus (B, for free, with the singleton kept)

`RunBatch` no longer loads the payment instance's whole marking; it queries the
pool directly:

```sql
SELECT workflow_id, place, token_id, data
  FROM token_states
 WHERE place = 'payable';          -- every pending expense token, across the system
```

That is option B's aggregate-on-read — delivered without changing the net. (If we
later move tokens onto each expense instance instead of the singleton, the *same
query* still returns the pool; that migration is invisible to `RunBatch`.)

### 9.4 Empty marking, atomicity, migration

- **Empty marking:** ✅ shipped ahead of the storage change (see §8, 2026-07-08).
  An instance with zero marked places is valid: the `len(places) == 0 ->
  ErrInvalidWorkflow` check in `manager.go` is gone and `batch_control` is
  deleted from `payment.yaml` (FRICTION #3). Under `token_states` this becomes
  "zero rows is valid" for free.
- **Atomicity:** instance row + token rows must be written in one transaction; the
  version check stays on the instance row. The conformance kit
  (`Versioned/LoadIsAtomicSnapshot`, and a new `Tokens/*`) must cover a token-row
  save under concurrent writers.
- **Migration:** one-time backfill — for each instance, `UnmarshalMarkingJSON` the
  old `state`, explode tokens into `token_states` rows, then drop the tokens from
  `state` (pre-1.0: no back-compat required, but ship the backfill helper).

### 9.5 Deferred — DELTA flavor (only if contention shows up)

To remove the single-writer bottleneck at high fan-in, change the save path from
whole-marking overwrite to **token deltas**: the manager emits "added token X to
`payable`, removed token Y" and the store does targeted `INSERT`/`DELETE`, with
concurrency scoped **per token row** (an append of a new token conflicts with no
one). This touches the `Storage` interface and the manager save path — but still
**not** the net, YAML, or firing API. Deferred until measurement (Q2) justifies it.

### 9.6 As shipped (2026-07-08) — deltas from the sketch above

The implementation (storage/tokens.go + both backends) follows §9.1–§9.4 with
these deliberate deviations:

- **Table name**: `<state_table>_tokens` derived from the configured state
  table (two nets sharing one DB with different state tables don't collide),
  overridable via `storage.WithTokenTable(name)`; the empty name **disables**
  normalization entirely (legacy blob mode — the escape hatch for
  hand-managed schemas).
- **Row payload**: the `token` column stores the FULL token JSON — id, data,
  and `enteredAt` — not just `TokenData`: the entry timestamp must survive
  for timers to restore. `token_id` stays as a queryable projection.
- **No UNIQUE constraint**: the engine's marking is a true multiset
  (`AddToken` accepts several anonymous colored tokens in one place), so
  storage must not impose an identity constraint the model lacks; `seq` is
  the row identity and scan order.
- **Load is ONE statement**, not a two-query transaction: the token rows
  LEFT JOIN into the instance-row query, so the snapshot is atomic even at
  READ COMMITTED — stronger and simpler than the sketch's BEGIN/COMMIT.
- **Compatibility is self-healing**: a save through the token table blanks
  the `state` blob, so a NON-empty blob always marks a legacy (or
  downgraded-writer) row whose blob is authoritative on load; the next save
  normalizes it. `BackfillTokenStates` (both backends) migrates eagerly —
  the dogfood calls it at boot — and skips instances a concurrent save beat
  it to, without bumping versions.
- **Read-model API**: `workflow.TokenQueryStorage` (optional capability,
  like `ListableStorage`/`DueStorage`) with `Manager.ListPlaceTokens`
  exposed on the manager; conformance covered by the kit's
  `Tokens/ListPlaceTokens`. The dogfood's `RunBatch` still loads the
  singleton — it must fire `pay` per token through the net anyway — but the
  pool is now equally answerable by one query, which is exactly what makes
  the Q1/Q3 singleton-vs-per-instance choice swappable later.

