package workflow

import (
	"context"
	"errors"
	"fmt"
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

// Manager handles workflow instances and their persistence
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

// loadFromStorage builds a workflow instance from persisted state without
// touching the registry. It validates every loaded place against the definition
// and verifies the definition fingerprint. Execute uses it so each retry runs
// against fresh, validated state.
func (m *Manager) loadFromStorage(ctx context.Context, id string, definition *Definition) (*Workflow, error) {
	// Load state and context from storage, using the versioned path when the
	// backend supports optimistic concurrency so we can track the loaded version.
	var (
		loaded    Marking
		wfContext map[string]any
		version   int64
		err       error
	)
	if vs, ok := m.storage.(VersionedStorage); ok {
		loaded, wfContext, version, err = vs.LoadVersionedState(ctx, id)
	} else {
		loaded, wfContext, err = m.storage.LoadState(ctx, id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load workflow state: %w", err)
	}

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

	// Verify the definition fingerprint, then strip it from the context so it
	// never reaches the workflow's user-visible context or guard environment.
	if err := m.checkFingerprint(ctx, id, definition, wfContext); err != nil {
		return nil, err
	}
	delete(wfContext, defFingerprintKey)

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

// checkFingerprint compares the stored definition fingerprint against the
// supplied definition. A missing stored fingerprint (pre-fingerprint instance)
// passes. A mismatch is an error unless a migration handler approves it.
func (m *Manager) checkFingerprint(ctx context.Context, id string, definition *Definition, wfContext map[string]any) error {
	stored, ok := wfContext[defFingerprintKey].(string)
	if !ok || stored == "" {
		return nil // legacy instance saved before fingerprints; nothing to compare
	}
	current := definition.Fingerprint()
	if stored == current {
		return nil
	}
	if m.onDefinitionMismatch != nil {
		return m.onDefinitionMismatch(ctx, id, stored, current)
	}
	return fmt.Errorf("%w: instance %q stored fingerprint %s, definition fingerprint %s", ErrDefinitionMismatch, id, stored, current)
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
	if vs, ok := m.storage.(VersionedStorage); ok {
		newVersion, err := vs.SaveVersionedState(ctx, id, marking, ctxData, wf.Version())
		if err != nil {
			return err
		}
		wf.setVersion(newVersion)
		return nil
	}
	return m.storage.SaveState(ctx, id, marking, ctxData)
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
// Note on side effects: fn (and any listeners it triggers) may run more than
// once across retries, and a listener's external side effects are not rolled
// back by a later conflict. Make side effects idempotent, or perform them after
// Execute returns.
func (m *Manager) Execute(ctx context.Context, id string, definition *Definition, fn func(*Workflow) error) error {
	const maxAttempts = 5
	var lastErr error
	for range maxAttempts {
		wf, err := m.loadFromStorage(ctx, id, definition)
		if err != nil {
			return err
		}
		if err := fn(wf); err != nil {
			return err
		}
		marking, ctxData := wf.snapshotState()
		ctxData = contextForSave(ctxData, definition)
		if vs, ok := m.storage.(VersionedStorage); ok {
			if _, err := vs.SaveVersionedState(ctx, id, marking, ctxData, wf.Version()); err != nil {
				if errors.Is(err, ErrConflict) {
					lastErr = err
					continue // reload fresh and retry
				}
				return err
			}
		} else if err := m.storage.SaveState(ctx, id, marking, ctxData); err != nil {
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
	if m.useCache {
		if wf, err := m.registry.Workflow(id); err == nil {
			return wf, nil
		}
	}
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
	// fails with ErrConflict if a workflow with this id already exists.
	marking, ctxData := wf.snapshotState()
	ctxData = contextForSave(ctxData, definition)
	if vs, ok := m.storage.(VersionedStorage); ok {
		newVersion, err := vs.SaveVersionedState(ctx, id, marking, ctxData, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to save initial state: %w", err)
		}
		wf.setVersion(newVersion)
	} else if err := m.storage.SaveState(ctx, id, marking, ctxData); err != nil {
		return nil, fmt.Errorf("failed to save initial state: %w", err)
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
// ErrInvalidWorkflow if the storage backend does not implement ListableStorage.
func (m *Manager) ListWorkflowIDs(ctx context.Context, opts ListOptions) ([]string, error) {
	ls, ok := m.storage.(ListableStorage)
	if !ok {
		return nil, fmt.Errorf("%w: storage backend does not implement ListableStorage", ErrInvalidWorkflow)
	}
	return ls.ListIDs(ctx, opts)
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
