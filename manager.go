package workflow

import (
	"context"
	"fmt"
	"sync/atomic"
)

// Manager handles workflow instances and their persistence
type Manager struct {
	registry *Registry
	storage  Storage

	// Dynamic listeners for all managed workflows
	Listeners map[EventType][]any

	// Handle tracking for reliable listener removal
	listenerHandles map[uint64]int // handle ID -> index in slice
	nextHandleID    uint64         // atomic counter for unique handle IDs
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
		places    []Place
		wfContext map[string]any
		version   int64
	)
	if vs, ok := m.storage.(VersionedStorage); ok {
		places, wfContext, version, err = vs.LoadVersionedState(ctx, id)
	} else {
		places, wfContext, err = m.storage.LoadState(ctx, id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load workflow state: %w", err)
	}

	// Validate that places slice is not empty
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

	// Set the current marking
	wf.Marking().SetPlaces(places)

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
	if vs, ok := m.storage.(VersionedStorage); ok {
		newVersion, err := vs.SaveVersionedState(ctx, id, wf.Marking().Places(), wf.context, wf.Version())
		if err != nil {
			return err
		}
		wf.setVersion(newVersion)
		return nil
	}
	return m.storage.SaveState(ctx, id, wf.Marking().Places(), wf.context)
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
	if vs, ok := m.storage.(VersionedStorage); ok {
		newVersion, err := vs.SaveVersionedState(ctx, id, wf.Marking().Places(), wf.context, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to save initial state: %w", err)
		}
		wf.setVersion(newVersion)
	} else if err := m.storage.SaveState(ctx, id, wf.Marking().Places(), wf.context); err != nil {
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
	if m.Listeners == nil {
		m.Listeners = make(map[EventType][]any)
	}
	if m.listenerHandles == nil {
		m.listenerHandles = make(map[uint64]int)
	}

	handleID := atomic.AddUint64(&m.nextHandleID, 1)
	index := len(m.Listeners[eventType])
	m.Listeners[eventType] = append(m.Listeners[eventType], listener)
	m.listenerHandles[handleID] = index

	return &ListenerHandle{
		id:        handleID,
		eventType: eventType,
		owner:     m,
	}
}

// AddGuardEventListener adds a dynamic guard event listener
// It returns a handle that can be used to remove the listener later
func (m *Manager) AddGuardEventListener(listener GuardEventListener) *ListenerHandle {
	if m.Listeners == nil {
		m.Listeners = make(map[EventType][]any)
	}
	if m.listenerHandles == nil {
		m.listenerHandles = make(map[uint64]int)
	}

	handleID := atomic.AddUint64(&m.nextHandleID, 1)
	index := len(m.Listeners[EventGuard])
	m.Listeners[EventGuard] = append(m.Listeners[EventGuard], listener)
	m.listenerHandles[handleID] = index

	return &ListenerHandle{
		id:        handleID,
		eventType: EventGuard,
		owner:     m,
	}
}

// RemoveListener removes a listener using its handle
// This is the recommended way to remove listeners as it's reliable and efficient
func (m *Manager) RemoveListener(handle *ListenerHandle) {
	if m.Listeners == nil || m.listenerHandles == nil || handle == nil {
		return
	}

	// Verify the handle belongs to this manager
	if handle.owner != m {
		return
	}

	index, ok := m.listenerHandles[handle.id]
	if !ok {
		return // Handle not found
	}

	listeners := m.Listeners[handle.eventType]
	if index >= len(listeners) {
		return // Index out of bounds
	}

	// Remove from slice
	m.Listeners[handle.eventType] = append(listeners[:index], listeners[index+1:]...)

	// Update indices for handles after the removed one
	for id, idx := range m.listenerHandles {
		if idx > index {
			m.listenerHandles[id] = idx - 1
		}
	}

	delete(m.listenerHandles, handle.id)
}
