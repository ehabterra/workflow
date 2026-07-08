// Package workflow is a Petri-net based workflow engine for Go.
//
// A workflow is defined by a set of places (states) and transitions between them.
// The current state of a running instance is its marking — the set of places that
// currently hold a token. Applying a transition consumes tokens from its source
// places and produces tokens in its target places, which makes parallel splits and
// synchronizing joins first-class rather than bolted on.
//
// # Core types
//
//   - [Definition] describes the static shape of a process: its [Place]s and
//     [Transition]s. Build one with [NewDefinition].
//   - [Workflow] is a live instance of a Definition. Create one with [NewWorkflow]
//     (or via a [Manager]) and drive it with Apply / ApplyTransition and their
//     WithContext variants.
//   - [Marking] holds the current places of an instance.
//   - [Registry] is a thread-safe in-memory collection of workflows.
//   - [Manager] coordinates a Registry with a [Storage] backend for persistence.
//
// # Guards and events
//
// Transitions can carry constraints that decide whether they may fire. Guard logic
// is commonly expressed with the expr-lang expression language (see the yaml
// sub-package and [NewExpressionConstraint]). Lifecycle events
// ([EventBeforeTransition], [EventAfterTransition], [EventGuard]) let callers hook
// into transitions; every [Event] carries a [context.Context].
//
// # Persistence
//
// [Storage] and the history store persist workflow state and an audit trail. Both
// interfaces take a context.Context as their first argument so callers can apply
// cancellation and deadlines. SQLite and PostgreSQL implementations ship in the
// storage and history sub-packages (with optimistic concurrency built into the
// contract, a normalized one-row-per-token format, and the [TokenQueryStorage]
// cross-instance read-model); you can supply your own by implementing the
// interfaces — the storagetest sub-package is the conformance suite for it.
//
// [Manager.Execute] is the recommended write path: load fresh, run your
// function, save under the instance's version, and retry the whole cycle on
// conflict — with [WithTxSideEffect] committing audit/outbox writes in the same
// transaction as the save ([WithFireDueTxSideEffect] is the timer-firing
// variant). See docs/guides/PRODUCTION_RECIPES.md for the crash-window and
// idempotency recipes, and docs/BOUNDARIES.md for what the library
// deliberately leaves to the host.
//
// # Configuration and visualization
//
// Workflows can be defined programmatically or loaded from YAML (see the yaml
// sub-package). [Definition.Diagram] renders the structure as a Mermaid
// flowchart, and [Workflow.Diagram] adds the live marking — guards on edges,
// BPMN-style gateway diamonds for splits and merges, reset arcs, timer styling,
// and per-place token badges.
//
// # Status
//
// Implemented and tested: the unified colored-token marking (a place can hold
// multiple data-carrying tokens; simple boolean workflows are the single-token
// special case), per-token transition firing, token-aware guards, reset arcs
// (cancellation regions — a transition clears declared places atomically when
// it fires), OR-input merge transitions (enabled by any one marked input) with
// the ApplyAny XOR-split resolver, host-driven timers (SetTimeoutAfter / Due / Manager.ListDue and
// FireDue — the host owns the clock), SQLite and PostgreSQL storage backends
// with optimistic concurrency built into the Storage contract and
// transactional building blocks, SQLite and PostgreSQL history stores, the
// strict YAML loader with the polymorphic initial_marking key, definition
// fingerprints with structural diffs for the migration hook, token queries
// and aggregation, empty-marking (pool-net) persistence with the
// cross-instance token read-model, and Mermaid diagram generation.
//
// Not yet available (tracked in ROADMAP.md): a signals API, static validation
// (deadlock/soundness checking), hierarchical workflows (HCPN),
// compensation/rollback, and observability hooks.
package workflow
