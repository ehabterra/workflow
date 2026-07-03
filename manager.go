package workflow

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

// defFingerprintKey is the reserved context key under which the Manager persists
// a workflow definition's fingerprint. It is written into the stored context on
// save and stripped from the live context on load, so it never appears in a
// workflow's user-visible context or guard environment.
const defFingerprintKey = "__workflow_def_fingerprint"

// DefinitionMigrationFunc is called by the Manager when a persisted instance's
// stored definition fingerprint differs from the definition supplied to load it.
// Returning nil lets the load proceed (the caller has confirmed the change is
// safe, or has migrated the marking); returning an error aborts the load with
// that error. storedFingerprint is empty for instances saved before fingerprints
// were recorded.
type DefinitionMigrationFunc func(ctx context.Context, id, storedFingerprint, currentFingerprint string) error

// Manager handles workflow instances and their persistence.
//
// The Manager reserves the context key "__workflow_def_fingerprint" for the
// definition fingerprint it stamps on every save: a user value stored under
// that key is overwritten on save and stripped on load.
type Manager struct {
	registry *Registry
	storage  Storage

	// listeners holds the dynamic listeners for all managed workflows. It is
	// concurrency-safe: listeners may be added or removed while managed
	// workflows fire transitions on other goroutines.
	listeners listenerSet

	// useCache controls whether loaded instances are cached in the registry.
	// When false, every GetWorkflow/LoadWorkflow reads fresh from storage — the
	// correct mode for multi-replica deployments where a cached copy could be
	// stale versus the database.
	useCache bool

	// onDefinitionMismatch, if set, is consulted when a loaded instance's stored
	// fingerprint differs from the definition's; nil means mismatches are errors.
	onDefinitionMismatch DefinitionMigrationFunc
}

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

// WithoutRegistryCache makes the Manager load every instance fresh from storage
// instead of serving a cached copy from the registry. Use it in multi-replica
// deployments, where a process-local cache can serve state that another replica
// has already advanced; optimistic concurrency (ErrConflict) then protects saves.
func WithoutRegistryCache() ManagerOption {
	return func(m *Manager) { m.useCache = false }
}

// WithDefinitionMigration installs a handler consulted when a persisted
// instance's definition fingerprint differs from the definition supplied to load
// it. Without it, such a load fails with ErrDefinitionMismatch.
func WithDefinitionMigration(fn DefinitionMigrationFunc) ManagerOption {
	return func(m *Manager) { m.onDefinitionMismatch = fn }
}

// NewManager creates a new workflow manager.
func NewManager(registry *Registry, storage Storage, opts ...ManagerOption) *Manager {
	m := &Manager{
		registry: registry,
		storage:  storage,
		useCache: true,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// LoadWorkflow loads a workflow instance from storage. When registry caching is
// enabled (the default) a cached instance is returned if present; otherwise the
// instance is loaded fresh from storage and cached.
func (m *Manager) LoadWorkflow(ctx context.Context, id string, definition *Definition) (*Workflow, error) {
	if m.useCache {
		if wf, err := m.registry.Workflow(id); err == nil {
			// The definition check must hold on cache hits too: a cached
			// instance built from a different definition is exactly as unsafe
			// as loading a persisted one against it.
			if err := checkCachedDefinition(wf, definition); err != nil {
				return nil, err
			}
			return wf, nil
		}
	}

	wf, err := m.loadFromStorage(ctx, id, definition)
	if err != nil {
		return nil, err
	}

	if m.useCache {
		if err := m.registry.AddWorkflow(wf); err != nil {
			return nil, fmt.Errorf("failed to add workflow to registry: %w", err)
		}
	}
	return wf, nil
}

// checkCachedDefinition verifies a registry-cached instance was built from the
// same definition the caller supplied (pointer fast path, fingerprint slow
// path). Unlike a storage load there is no migration path here — the cached
// instance is live, so a mismatch is always an error.
func checkCachedDefinition(wf *Workflow, definition *Definition) error {
	cached := wf.Definition()
	if cached == definition {
		return nil
	}
	if cached != nil && definition != nil && cached.Fingerprint() == definition.Fingerprint() {
		return nil
	}
	return fmt.Errorf("%w: cached instance %q was built from a different definition", ErrDefinitionMismatch, wf.Name())
}

// loadFromStorage builds a workflow instance from persisted state without
// touching the registry. It verifies the definition fingerprint (consulting the
// migration handler on mismatch) and then validates every loaded place against
// the definition. Execute uses it so each retry runs against fresh, validated
// state.
func (m *Manager) loadFromStorage(ctx context.Context, id string, definition *Definition) (*Workflow, error) {
	loaded, wfContext, version, err := m.readState(ctx, id)
	if err != nil {
		return nil, err
	}

	// Verify the definition fingerprint FIRST — before validating places — so a
	// mismatch consults the migration handler even when the stale marking
	// references places the new definition no longer has (the very case
	// migration exists for). After the handler approves, reload: a handler that
	// migrated the persisted state expects the load to observe its rewrite, not
	// clobber it with the pre-migration snapshot on the next save.
	migrated, err := m.checkFingerprint(ctx, id, definition, wfContext)
	if err != nil {
		return nil, err
	}
	if migrated {
		if loaded, wfContext, version, err = m.readState(ctx, id); err != nil {
			return nil, fmt.Errorf("reloading after definition migration: %w", err)
		}
	}
	// Strip the fingerprint so it never reaches the workflow's user-visible
	// context or guard environment.
	delete(wfContext, defFingerprintKey)

	// Validate that the loaded marking has at least one place.
	places := loaded.Places()
	if len(places) == 0 {
		return nil, fmt.Errorf("%w: loaded state has no places", ErrInvalidWorkflow)
	}

	// Validate EVERY loaded place against the definition, not just the first:
	// a stale marking referencing a place removed from the definition must fail
	// loudly rather than load into an instance that can never fire.
	for _, p := range places {
		if !definition.Place(p) {
			return nil, fmt.Errorf("%w: loaded marking references place %q not in the definition", ErrDefinitionMismatch, p)
		}
	}

	wf, err := NewWorkflow(id, definition, places[0])
	if err != nil {
		return nil, fmt.Errorf("failed to create workflow: %w", err)
	}
	wf.SetManager(m)
	wf.context = wfContext // Set the loaded context (fingerprint already stripped)
	wf.setVersion(version) // Track the loaded concurrency version (0 if unversioned)

	// Adopt the full loaded marking (preserves colored tokens, not just presence).
	if err := wf.SetMarking(loaded); err != nil {
		return nil, fmt.Errorf("failed to set loaded marking: %w", err)
	}
	return wf, nil
}

// readState loads a workflow's persisted marking, context, and version, using
// the versioned path when the backend supports optimistic concurrency.
func (m *Manager) readState(ctx context.Context, id string) (Marking, map[string]any, int64, error) {
	if vs, ok := m.storage.(VersionedStorage); ok {
		loaded, wfContext, version, err := vs.LoadVersionedState(ctx, id)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("failed to load workflow state: %w", err)
		}
		return loaded, wfContext, version, nil
	}
	loaded, wfContext, err := m.storage.LoadState(ctx, id)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to load workflow state: %w", err)
	}
	return loaded, wfContext, 0, nil
}

// checkFingerprint compares the stored definition fingerprint against the
// supplied definition. A missing stored fingerprint (pre-fingerprint instance)
// passes. A mismatch is an error unless a migration handler approves it; the
// returned bool reports whether a handler was consulted and approved (the
// caller then reloads state, since the handler may have rewritten it).
func (m *Manager) checkFingerprint(ctx context.Context, id string, definition *Definition, wfContext map[string]any) (migrated bool, err error) {
	stored, ok := wfContext[defFingerprintKey].(string)
	if !ok || stored == "" {
		return false, nil // legacy instance saved before fingerprints; nothing to compare
	}
	current := definition.Fingerprint()
	if stored == current {
		return false, nil
	}
	if m.onDefinitionMismatch != nil {
		if err := m.onDefinitionMismatch(ctx, id, stored, current); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, fmt.Errorf("%w: instance %q stored fingerprint %s, definition fingerprint %s", ErrDefinitionMismatch, id, stored, current)
}

// contextForSave returns a copy of ctxData stamped with the definition
// fingerprint, ready to hand to storage. The live workflow context is never
// mutated (the caller passes a snapshot copy).
func contextForSave(ctxData map[string]any, definition *Definition) map[string]any {
	if ctxData == nil {
		ctxData = make(map[string]any, 1)
	}
	if definition != nil {
		ctxData[defFingerprintKey] = definition.Fingerprint()
	}
	return ctxData
}

// SaveWorkflow saves a workflow instance state to storage.
//
// When the backend is a VersionedStorage, the save is guarded by the workflow's
// current version: if another writer saved first, it returns ErrConflict and the
// workflow's version is left unchanged so the caller can reload and retry.
func (m *Manager) SaveWorkflow(ctx context.Context, id string, wf *Workflow) error {
	// Snapshot marking and context under the workflow's lock: handing the live
	// state to the storage layer would race concurrent transitions and
	// SetContext calls while it marshals.
	marking, ctxData := wf.snapshotState()
	ctxData = contextForSave(ctxData, wf.Definition())
	due := m.dueForSave(wf.Definition(), marking)
	newVersion, versioned, err := m.persistState(ctx, id, marking, ctxData, wf.Version(), due)
	if err != nil {
		return err
	}
	if versioned {
		wf.setVersion(newVersion)
	}
	return nil
}

// dueForSave computes an instance's next-due wall-clock time from the same
// marking snapshot that is about to be persisted, returning nil when no timer is
// running. Deriving it from the persisted snapshot (rather than the live
// workflow) keeps the stored due index consistent with the stored marking.
//
// It returns nil immediately when the backend does not maintain a due index (not
// a DueStorage), avoiding a wasted deadline scan on every save for backends that
// would ignore the result anyway.
func (m *Manager) dueForSave(definition *Definition, marking Marking) *time.Time {
	if _, ok := m.storage.(DueStorage); !ok {
		return nil
	}
	if t, ok := nextDue(definition, marking); ok {
		return &t
	}
	return nil
}

// persistState saves a marking and context, selecting the highest capability the
// backend supports and always maintaining the due index when the backend is a
// DueStorage — so the index can never go stale, whatever save path a caller
// takes. It returns the new version and whether the backend is versioned (an
// unversioned backend reports version 0).
func (m *Manager) persistState(ctx context.Context, id string, marking Marking, ctxData map[string]any, expectedVersion int64, due *time.Time) (newVersion int64, versioned bool, err error) {
	switch s := m.storage.(type) {
	case DueStorage:
		v, err := s.SaveVersionedStateWithDue(ctx, id, marking, ctxData, expectedVersion, due)
		return v, true, err
	case VersionedStorage:
		v, err := s.SaveVersionedState(ctx, id, marking, ctxData, expectedVersion)
		return v, true, err
	default:
		return 0, false, s.SaveState(ctx, id, marking, ctxData)
	}
}

// ExecuteOption configures a single Manager.Execute call.
type ExecuteOption func(*executeConfig)

type executeConfig struct {
	effects     []TxSideEffect
	maxAttempts int
}

// WithMaxRetries sets how many times Execute retries the whole
// load-fn-save cycle when the save hits an optimistic-concurrency conflict.
// The default is 5. Raise it for instances with many concurrent writers.
func WithMaxRetries(attempts int) ExecuteOption {
	return func(c *executeConfig) {
		if attempts > 0 {
			c.maxAttempts = attempts
		}
	}
}

// WithTxSideEffect registers a write committed atomically with the state save —
// the crash-consistent way to append an audit/history record or an outbox row
// for a firing. Requires the Manager's storage to implement
// TransactionalStorage (the SQLite and Postgres backends do); Execute fails
// otherwise rather than silently losing atomicity.
//
// Effects registered here differ from listener side effects: an effect shares
// the save's transaction (state and effect commit or roll back together), while
// a listener's external side effects happen immediately and are not undone by a
// later conflict or crash. The canonical use is a history record built inside
// fn (capturing the fired transition) and written by the effect:
//
//	var record *history.TransitionRecord
//	err := mgr.Execute(ctx, id, def,
//	    func(wf *workflow.Workflow) error {
//	        if err := wf.ApplyTransition("approve"); err != nil {
//	            return err
//	        }
//	        record = &history.TransitionRecord{WorkflowID: id, Transition: "approve", ...}
//	        return nil
//	    },
//	    workflow.WithTxSideEffect(func(ctx context.Context, tx any) error {
//	        return hist.SaveTransitionTx(ctx, tx.(*sql.Tx), record)
//	    }),
//	)
func WithTxSideEffect(effect TxSideEffect) ExecuteOption {
	return func(c *executeConfig) { c.effects = append(c.effects, effect) }
}

// Execute atomically advances a persisted instance: it loads the instance fresh
// from storage, runs fn against it (fn typically applies one or more
// transitions), and saves it back under optimistic concurrency. If the save
// conflicts with a concurrent writer (ErrConflict), the whole cycle retries on
// fresh state up to a bounded number of times.
//
// This is the recommended way to react to an external event (a webhook, a UI
// action, a timer) because it removes the load / fire / save / retry boilerplate
// and always operates on current state — even with registry caching disabled or
// across replicas. fn must be safe to re-run, since a conflict re-invokes it on
// reloaded state; a transition that is no longer enabled on reload returns
// ErrNotEnabled from within fn, which Execute surfaces to the caller.
//
// Side-effect semantics: fn (and any listeners it triggers) may run more than
// once across retries, and a listener's external side effects are not rolled
// back by a later conflict — make them idempotent, or perform them after
// Execute returns. Writes that must be crash-consistent with the state change
// (history records, outbox rows) belong in a WithTxSideEffect option, which
// commits them in the same transaction as the save.
func (m *Manager) Execute(ctx context.Context, id string, definition *Definition, fn func(*Workflow) error, opts ...ExecuteOption) error {
	var cfg executeConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	// Atomic side effects need transactional support; fail loudly rather than
	// silently dropping the atomicity guarantee.
	var ts TransactionalStorage
	var tds TransactionalDueStorage
	if len(cfg.effects) > 0 {
		var ok bool
		if ts, ok = m.storage.(TransactionalStorage); !ok {
			return fmt.Errorf("WithTxSideEffect requires a TransactionalStorage backend: %w", errors.ErrUnsupported)
		}
		// A backend that maintains a due index but cannot update it inside the
		// state+effect transaction would leave the index silently corrupt for a
		// timed definition (state and effect commit, but the due column is not
		// touched). Fail loudly rather than drift — mirroring the missing-
		// TransactionalStorage error above.
		if _, isDue := m.storage.(DueStorage); isDue {
			if tds, ok = m.storage.(TransactionalDueStorage); !ok && definitionHasTimers(definition) {
				return fmt.Errorf("WithTxSideEffect on a DueStorage backend with a timed definition requires a TransactionalDueStorage backend "+
					"(implement SaveVersionedStateInTxWithDue so the due index commits atomically with state and effects): %w", errors.ErrUnsupported)
			}
		}
	}

	maxAttempts := cfg.maxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	var lastErr error
	for attempt := range maxAttempts {
		// A conflict storm must not outlive the caller: honor cancellation even
		// if the storage backend ignores ctx.
		if err := ctx.Err(); err != nil {
			return err
		}
		// Back off with jitter before each retry so N concurrent writers on one
		// instance de-synchronize instead of starving each other out.
		if attempt > 0 {
			jitter := time.Duration(rand.Int64N(int64(4 * time.Millisecond)))
			time.Sleep(time.Duration(attempt)*2*time.Millisecond + jitter)
		}

		wf, err := m.loadFromStorage(ctx, id, definition)
		if err != nil {
			return err
		}
		if err := fn(wf); err != nil {
			return err
		}
		marking, ctxData := wf.snapshotState()
		ctxData = contextForSave(ctxData, definition)
		due := m.dueForSave(definition, marking)

		switch {
		case ts != nil:
			// Keep the due index current even on the transactional path (state +
			// side effect commit together) when the backend supports it. A partial
			// due backend (DueStorage but not TransactionalDueStorage) with a timed
			// definition was already rejected before the loop.
			if tds != nil {
				_, err = tds.SaveVersionedStateInTxWithDue(ctx, id, marking, ctxData, wf.Version(), due, cfg.effects...)
			} else {
				_, err = ts.SaveVersionedStateInTx(ctx, id, marking, ctxData, wf.Version(), cfg.effects...)
			}
		default:
			_, _, err = m.persistState(ctx, id, marking, ctxData, wf.Version(), due)
		}
		if err != nil {
			if errors.Is(err, ErrConflict) {
				lastErr = err
				continue // reload fresh and retry
			}
			return err
		}

		// Keep the registry from serving a now-stale cached copy.
		if m.useCache {
			_ = m.registry.RemoveWorkflow(id)
		}
		return nil
	}
	return fmt.Errorf("%w: giving up after %d attempts", lastErr, maxAttempts)
}

// GetWorkflow gets a workflow instance from the registry or loads it from
// storage. With registry caching disabled (WithoutRegistryCache) it always
// loads fresh.
func (m *Manager) GetWorkflow(ctx context.Context, id string, definition *Definition) (*Workflow, error) {
	return m.LoadWorkflow(ctx, id, definition)
}

// CreateWorkflow creates a new workflow instance and saves it to storage
func (m *Manager) CreateWorkflow(ctx context.Context, id string, definition *Definition, initialPlace Place) (*Workflow, error) {
	wf, err := NewWorkflow(id, definition, initialPlace)
	if err != nil {
		return nil, fmt.Errorf("failed to create workflow: %w", err)
	}
	wf.SetManager(m)

	// Save initial state. With a versioned backend this inserts at version 1 and
	// fails with ErrConflict if a workflow with this id already exists. A
	// timer-bearing workflow's initial marking is already stamped, so its first
	// deadline is indexed from creation.
	marking, ctxData := wf.snapshotState()
	ctxData = contextForSave(ctxData, definition)
	due := m.dueForSave(definition, marking)
	newVersion, versioned, err := m.persistState(ctx, id, marking, ctxData, 0, due)
	if err != nil {
		return nil, fmt.Errorf("failed to save initial state: %w", err)
	}
	if versioned {
		wf.setVersion(newVersion)
	}

	if m.useCache {
		if err := m.registry.AddWorkflow(wf); err != nil {
			return nil, fmt.Errorf("failed to add workflow to registry: %w", err)
		}
	}
	return wf, nil
}

// DeleteWorkflow removes a workflow instance and its state
func (m *Manager) DeleteWorkflow(ctx context.Context, id string) error {
	// Remove from registry (ignore error if workflow not found)
	_ = m.registry.RemoveWorkflow(id)

	// Remove from storage
	return m.storage.DeleteState(ctx, id)
}

// EvictWorkflow drops a workflow from the in-memory registry cache without
// touching storage. Call it after saving when a long-lived Manager would
// otherwise accumulate instances or serve a stale cached copy; a subsequent
// GetWorkflow/LoadWorkflow then reads fresh from storage. With registry caching
// disabled it is a no-op.
func (m *Manager) EvictWorkflow(id string) {
	_ = m.registry.RemoveWorkflow(id)
}

// ListWorkflowIDs returns the IDs of persisted workflows, ordered by ID, using
// the backend's ListableStorage support. It returns an error wrapping
// errors.ErrUnsupported if the storage backend does not implement
// ListableStorage.
func (m *Manager) ListWorkflowIDs(ctx context.Context, opts ListOptions) ([]string, error) {
	ls, ok := m.storage.(ListableStorage)
	if !ok {
		return nil, fmt.Errorf("storage backend does not implement ListableStorage: %w", errors.ErrUnsupported)
	}
	return ls.ListIDs(ctx, opts)
}

// ListDue returns the IDs of persisted instances whose next-due time is at or
// before `before`, ordered by due time ascending — the instances a host cron
// should advance with FireDue. A zero limit means no limit; drain a batch with
// FireDue before rescanning, or page by raising `before`.
//
// It requires a DueStorage backend (the SQLite and Postgres backends qualify)
// and returns an error wrapping errors.ErrUnsupported otherwise. This is the
// scan half of the host-driven timer model: pass the host's own clock as
// `before` (typically time.Now) so the whole fleet's deadlines are evaluated
// against one authoritative clock.
func (m *Manager) ListDue(ctx context.Context, before time.Time, limit int) ([]string, error) {
	ds, ok := m.storage.(DueStorage)
	if !ok {
		return nil, fmt.Errorf("storage backend does not implement DueStorage: %w", errors.ErrUnsupported)
	}
	return ds.ListDue(ctx, before, limit)
}

// maxFireDueSteps bounds how many transitions a single FireDue advances — a
// safety valve against a pathological self-re-enabling timer. Because
// SetTimeoutAfter only records positive timeouts, a fired transition's next
// deadline is strictly later than now, so a real definition terminates a firing
// pass long before this bound.
const maxFireDueSteps = 10000

// errFireDueNoSave is an internal sentinel the FireDue fn returns to tell Execute
// "nothing changed worth persisting" — Execute aborts the attempt without saving
// (and without retrying, since it is not ErrConflict), and FireDue translates it
// back to a successful no-op. It is never returned to callers.
var errFireDueNoSave = errors.New("firedue: no save needed")

// FireDue advances a persisted instance by firing every transition whose timer
// has elapsed as of now, returning the names of the transitions that actually
// fired, in firing order.
//
// It is the per-instance half of the host-driven timer model (M4): a host cron
// finds due instances with Manager.ListDue and calls FireDue on each, so a
// fleet-wide "escalate if not approved in 3 days" needs no internal scheduler.
// The state lives in the database and the clock lives in the host, which makes
// the whole mechanism restart-safe by construction.
//
// FireDue loads the instance fresh and pins the workflow clock to now, so tokens
// produced by the firing are stamped with the host's evaluation time and every
// downstream deadline is measured from it (deterministic and testable with a
// fixed clock). It then fires due transitions one at a time, re-evaluating the
// due set after each firing because firing changes the marking; a due transition
// whose guard rejects it — or that an earlier firing in the same pass has since
// disabled — is skipped rather than treated as an error, so only an unexpected
// error aborts.
//
// The save runs under the same optimistic-concurrency retry loop as Execute, so
// several hosts scanning the same fleet cannot clobber each other. FireDue is
// idempotent: once nothing is overdue it fires nothing, and after a firing that
// leaves no running timer the instance drops out of ListDue.
//
// Extra ExecuteOptions (e.g. WithMaxRetries, WithTxSideEffect) are forwarded to
// the underlying Execute.
func (m *Manager) FireDue(ctx context.Context, id string, definition *Definition, now time.Time, opts ...ExecuteOption) ([]string, error) {
	var fired []string
	err := m.Execute(ctx, id, definition, func(wf *Workflow) error {
		fired = nil // Execute may re-run fn on a conflict retry; start clean.
		wf.setClock(func() time.Time { return now })
		for range maxFireDueSteps {
			// Fire the first due transition that is actually allowed, then
			// re-evaluate (firing changed the marking). When no due transition
			// fires — the set is empty, or every member is guard-blocked or was
			// disabled earlier in this pass — the pass is done.
			progressed := false
			for _, t := range wf.Due(now) {
				err := wf.ApplyTransitionWithContext(ctx, t.Name())
				if err != nil {
					if errors.Is(err, ErrTransitionNotAllowed) {
						continue // blocked/disabled: skip, not an error.
					}
					return err
				}
				fired = append(fired, t.Name())
				progressed = true
				break
			}
			if !progressed {
				break
			}
		}
		if len(fired) == 0 {
			// Nothing fired. Skip the save (no pointless version bump) only when
			// the stored due index already agrees with the live marking — i.e. the
			// instance is legitimately due-but-blocked: a timer is still running and
			// its deadline is at or before now. If instead no timer is running, or
			// the next deadline is in the future, the stored index is stale relative
			// to the marking (e.g. a bypass save), so persist to let it self-heal.
			if next, ok := wf.NextDue(); ok && !next.After(now) {
				return errFireDueNoSave
			}
		}
		return nil
	}, opts...)
	if err != nil && !errors.Is(err, errFireDueNoSave) {
		return nil, err
	}
	return fired, nil
}

// AddEventListener adds a dynamic event listener for a specific event type
// It returns a handle that can be used to remove the listener later
func (m *Manager) AddEventListener(eventType EventType, listener EventListener) *ListenerHandle {
	return m.listeners.add(eventType, listener, m)
}

// AddGuardEventListener adds a dynamic guard event listener
// It returns a handle that can be used to remove the listener later
func (m *Manager) AddGuardEventListener(listener GuardEventListener) *ListenerHandle {
	return m.listeners.add(EventGuard, listener, m)
}

// RemoveListener removes a listener using its handle
// This is the recommended way to remove listeners as it's reliable and efficient
func (m *Manager) RemoveListener(handle *ListenerHandle) {
	if handle == nil || handle.owner != m {
		return
	}
	m.listeners.remove(handle)
}

// ListenerCount returns the number of listeners registered for eventType.
func (m *Manager) ListenerCount(eventType EventType) int {
	return m.listeners.count(eventType)
}
