package workflow

import (
	"fmt"
)

// Manager handles workflow instances and their persistence
type Manager struct {
	registry *Registry
	storage  Storage

	// Dynamic listeners for all managed workflows
	Listeners map[EventType][]interface{}
}

// NewManager creates a new workflow manager
func NewManager(registry *Registry, storage Storage) *Manager {
	return &Manager{
		registry: registry,
		storage:  storage,
	}
}

// LoadWorkflow loads a workflow instance from storage
func (m *Manager) LoadWorkflow(id string, definition *Definition) (*Workflow, error) {
	// Try to get from registry first
	wf, err := m.registry.Workflow(id)
	if err == nil {
		return wf, nil
	}

	// Load state and context from storage
	places, wfContext, err := m.storage.LoadState(id)
	if err != nil {
		return nil, fmt.Errorf("failed to load workflow state: %w", err)
	}

	// Create new workflow instance
	wf, err = NewWorkflow(id, definition, places[0])
	if err != nil {
		return nil, fmt.Errorf("failed to create workflow: %w", err)
	}
	wf.SetManager(m)
	wf.context = wfContext // Set the loaded context

	// Set the current marking
	wf.Marking().SetPlaces(places)

	// Add to registry
	if err := m.registry.AddWorkflow(wf); err != nil {
		return nil, fmt.Errorf("failed to add workflow to registry: %w", err)
	}
	return wf, nil
}

// SaveWorkflow saves a workflow instance state to storage
func (m *Manager) SaveWorkflow(id string, wf *Workflow) error {
	return m.storage.SaveState(id, wf.Marking().Places(), wf.context)
}

// GetWorkflow gets a workflow instance from the registry or loads it from storage
func (m *Manager) GetWorkflow(id string, definition *Definition) (*Workflow, error) {
	// Try to get from registry first
	wf, err := m.registry.Workflow(id)
	if err == nil {
		return wf, nil
	}

	// If not in registry, load from storage
	return m.LoadWorkflow(id, definition)
}

// CreateWorkflow creates a new workflow instance and saves it to storage
func (m *Manager) CreateWorkflow(id string, definition *Definition, initialPlace Place) (*Workflow, error) {
	wf, err := NewWorkflow(id, definition, initialPlace)
	if err != nil {
		return nil, fmt.Errorf("failed to create workflow: %w", err)
	}
	wf.SetManager(m)

	// Save initial state
	if err := m.storage.SaveState(id, wf.Marking().Places(), wf.context); err != nil {
		return nil, fmt.Errorf("failed to save initial state: %w", err)
	}

	// Add to registry
	if err := m.registry.AddWorkflow(wf); err != nil {
		return nil, fmt.Errorf("failed to add workflow to registry: %w", err)
	}
	return wf, nil
}

// DeleteWorkflow removes a workflow instance and its state
func (m *Manager) DeleteWorkflow(id string) error {
	// Remove from registry (ignore error if workflow not found)
	_ = m.registry.RemoveWorkflow(id)

	// Remove from storage
	return m.storage.DeleteState(id)
}

// AddEventListener adds a dynamic event listener for a specific event type
func (m *Manager) AddEventListener(eventType EventType, listener EventListener) {
	if m.Listeners == nil {
		m.Listeners = make(map[EventType][]interface{})
	}
	m.Listeners[eventType] = append(m.Listeners[eventType], listener)
}

// AddGuardEventListener adds a dynamic guard event listener
func (m *Manager) AddGuardEventListener(listener GuardEventListener) {
	if m.Listeners == nil {
		m.Listeners = make(map[EventType][]interface{})
	}
	m.Listeners[EventGuard] = append(m.Listeners[EventGuard], listener)
}

// RemoveEventListener removes a dynamic event listener
func (m *Manager) RemoveEventListener(eventType EventType, listener interface{}) {
	if m.Listeners == nil {
		return
	}
	listeners := m.Listeners[eventType]
	for i, l := range listeners {
		if &l == &listener {
			m.Listeners[eventType] = append(listeners[:i], listeners[i+1:]...)
			break
		}
	}
}
