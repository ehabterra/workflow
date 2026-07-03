package workflow

import (
	"context"
	"fmt"
)

// Manager handles workflow instances and their persistence
type Manager struct {
	registry *Registry
	storage  Storage

	// listeners holds the dynamic listeners for all managed workflows. It is
	// concurrency-safe: listeners may be added or removed while managed
	// workflows fire transitions on other goroutines.
	listeners listenerSet
}

// NewManager creates a new workflow manager
func NewManager(registry *Registry, storage Storage) *Manager {
	return &Manager{
		registry: registry,
		storage:  storage,
	}
}

// LoadWorkflow loads a workflow instance from storage
func (m *Manager) LoadWorkflow(ctx context.Context, id string, definition *Definition) (*Workflow, error) {
	// Try to get from registry first
	wf, err := m.registry.Workflow(id)
	if err == nil {
		return wf, nil
	}

	// Load state and context from storage, using the versioned path when the
	// backend supports optimistic concurrency so we can track the loaded version.
	var (
		loaded    Marking
		wfContext map[string]any
		version   int64
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

	// Create new workflow instance
	wf, err = NewWorkflow(id, definition, places[0])
	if err != nil {
		return nil, fmt.Errorf("failed to create workflow: %w", err)
	}
	wf.SetManager(m)
	wf.context = wfContext // Set the loaded context
	wf.setVersion(version) // Track the loaded concurrency version (0 if unversioned)

	// Adopt the full loaded marking (preserves colored tokens, not just presence).
	if err := wf.SetMarking(loaded); err != nil {
		return nil, fmt.Errorf("failed to set loaded marking: %w", err)
	}

	// Add to registry
	if err := m.registry.AddWorkflow(wf); err != nil {
		return nil, fmt.Errorf("failed to add workflow to registry: %w", err)
	}
	return wf, nil
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

// GetWorkflow gets a workflow instance from the registry or loads it from storage
func (m *Manager) GetWorkflow(ctx context.Context, id string, definition *Definition) (*Workflow, error) {
	// Try to get from registry first
	wf, err := m.registry.Workflow(id)
	if err == nil {
		return wf, nil
	}

	// If not in registry, load from storage
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
	if vs, ok := m.storage.(VersionedStorage); ok {
		newVersion, err := vs.SaveVersionedState(ctx, id, marking, ctxData, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to save initial state: %w", err)
		}
		wf.setVersion(newVersion)
	} else if err := m.storage.SaveState(ctx, id, marking, ctxData); err != nil {
		return nil, fmt.Errorf("failed to save initial state: %w", err)
	}

	// Add to registry
	if err := m.registry.AddWorkflow(wf); err != nil {
		return nil, fmt.Errorf("failed to add workflow to registry: %w", err)
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
