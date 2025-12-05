package workflow_test

import (
	"fmt"
	"testing"

	"github.com/ehabterra/workflow"
)

// MockStorage implements the Storage interface for testing
type MockStorage struct {
	states   map[string][]workflow.Place
	contexts map[string]map[string]interface{}
}

func NewMockStorage() *MockStorage {
	return &MockStorage{
		states:   make(map[string][]workflow.Place),
		contexts: make(map[string]map[string]interface{}),
	}
}

func (m *MockStorage) LoadState(id string) ([]workflow.Place, map[string]interface{}, error) {
	states, ok := m.states[id]
	if !ok {
		return nil, nil, fmt.Errorf("workflow not found")
	}
	ctx := m.contexts[id]
	if ctx == nil {
		ctx = map[string]interface{}{}
	}
	return states, ctx, nil
}

func (m *MockStorage) SaveState(id string, places []workflow.Place, context map[string]interface{}) error {
	m.states[id] = places
	if context == nil {
		context = map[string]interface{}{}
	}
	m.contexts[id] = context
	return nil
}

func (m *MockStorage) DeleteState(id string) error {
	delete(m.states, id)
	delete(m.contexts, id)
	return nil
}

func TestNewManager(t *testing.T) {
	registry := workflow.NewRegistry()
	storage := NewMockStorage()
	manager := workflow.NewManager(registry, storage)

	// Verify manager was created (can't access unexported fields from external test package)
	if manager == nil {
		t.Error("Expected manager to be non-nil")
	}
}

func TestManager_CreateWorkflow(t *testing.T) {
	registry := workflow.NewRegistry()
	storage := NewMockStorage()
	manager := workflow.NewManager(registry, storage)

	// Create a simple workflow definition
	places := []workflow.Place{"draft", "review", "published"}
	definition, err := workflow.NewDefinition(places, []workflow.Transition{})
	if err != nil {
		t.Fatalf("Failed to create workflow definition: %v", err)
	}

	// Test creating a new workflow
	id := "test_workflow"
	initialPlace := workflow.Place("draft")
	wf, err := manager.CreateWorkflow(id, definition, initialPlace)
	if err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}

	// Verify workflow was created correctly
	if wf.Name() != id {
		t.Errorf("Expected workflow name to be %s, got %s", id, wf.Name())
	}

	// Verify workflow was added to registry
	registryWf, err := registry.Workflow(id)
	if err != nil {
		t.Errorf("Workflow not found in registry: %v", err)
	}
	if registryWf != wf {
		t.Errorf("Expected workflow in registry to be %v, got %v", wf, registryWf)
	}

	// Verify initial state was saved
	states, _, err := storage.LoadState(id)
	if err != nil {
		t.Errorf("Failed to load workflow state: %v", err)
	}
	if len(states) != 1 || states[0] != initialPlace {
		t.Errorf("Expected initial state to be %v, got %v", initialPlace, states)
	}
}

func TestManager_GetWorkflow(t *testing.T) {
	registry := workflow.NewRegistry()
	storage := NewMockStorage()
	manager := workflow.NewManager(registry, storage)

	// Create a simple workflow definition
	places := []workflow.Place{"draft", "review", "published"}
	definition, err := workflow.NewDefinition(places, []workflow.Transition{})
	if err != nil {
		t.Fatalf("Failed to create workflow definition: %v", err)
	}

	// Test getting a non-existent workflow
	_, err = manager.GetWorkflow("non_existent", definition)
	if err == nil {
		t.Error("Expected error when getting non-existent workflow")
	}

	// Create a workflow
	id := "test_workflow"
	initialPlace := workflow.Place("draft")
	wf, err := manager.CreateWorkflow(id, definition, initialPlace)
	if err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}

	// Test getting the workflow
	retrievedWf, err := manager.GetWorkflow(id, definition)
	if err != nil {
		t.Errorf("Failed to get workflow: %v", err)
	}
	if retrievedWf != wf {
		t.Errorf("Expected workflow to be %v, got %v", wf, retrievedWf)
	}
}

func TestManager_SaveWorkflow(t *testing.T) {
	registry := workflow.NewRegistry()
	storage := NewMockStorage()
	manager := workflow.NewManager(registry, storage)

	// Create a simple workflow definition
	places := []workflow.Place{"draft", "review", "published"}
	definition, err := workflow.NewDefinition(places, []workflow.Transition{})
	if err != nil {
		t.Fatalf("Failed to create workflow definition: %v", err)
	}

	// Create a workflow
	id := "test_workflow"
	initialPlace := workflow.Place("draft")
	wf, err := manager.CreateWorkflow(id, definition, initialPlace)
	if err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}

	// Change the workflow state
	newPlace := workflow.Place("review")
	wf.Marking().SetPlaces([]workflow.Place{newPlace})

	// Save the workflow
	err = manager.SaveWorkflow(id, wf)
	if err != nil {
		t.Errorf("Failed to save workflow: %v", err)
	}

	// Verify the state was saved
	states, _, err := storage.LoadState(id)
	if err != nil {
		t.Errorf("Failed to load workflow state: %v", err)
	}
	if len(states) != 1 || states[0] != newPlace {
		t.Errorf("Expected state to be %v, got %v", newPlace, states)
	}
}

func TestManager_DeleteWorkflow(t *testing.T) {
	registry := workflow.NewRegistry()
	storage := NewMockStorage()
	manager := workflow.NewManager(registry, storage)

	// Create a simple workflow definition
	places := []workflow.Place{"draft", "review", "published"}
	definition, err := workflow.NewDefinition(places, []workflow.Transition{})
	if err != nil {
		t.Fatalf("Failed to create workflow definition: %v", err)
	}

	// Create a workflow
	id := "test_workflow"
	initialPlace := workflow.Place("draft")
	_, err = manager.CreateWorkflow(id, definition, initialPlace)
	if err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}

	// Delete the workflow
	err = manager.DeleteWorkflow(id)
	if err != nil {
		t.Errorf("Failed to delete workflow: %v", err)
	}

	// Verify workflow was removed from registry
	_, err = registry.Workflow(id)
	if err == nil {
		t.Error("Expected error when getting deleted workflow from registry")
	}

	// Verify workflow state was removed from storage
	_, _, err = storage.LoadState(id)
	if err == nil {
		t.Error("Expected error when getting deleted workflow from storage")
	}
}

func TestManager_LoadWorkflow(t *testing.T) {
	registry := workflow.NewRegistry()
	storage := NewMockStorage()
	manager := workflow.NewManager(registry, storage)

	// Create a simple workflow definition
	places := []workflow.Place{"draft", "review", "published"}
	definition, err := workflow.NewDefinition(places, []workflow.Transition{})
	if err != nil {
		t.Fatalf("Failed to create workflow definition: %v", err)
	}

	// Test loading a non-existent workflow
	_, err = manager.LoadWorkflow("non_existent", definition)
	if err == nil {
		t.Error("Expected error when loading non-existent workflow")
	}

	// Create a workflow and save its state
	id := "test_workflow"
	initialPlace := workflow.Place("draft")
	err = storage.SaveState(id, []workflow.Place{initialPlace}, nil)
	if err != nil {
		t.Fatalf("Failed to save workflow state: %v", err)
	}

	// Load the workflow
	wf, err := manager.LoadWorkflow(id, definition)
	if err != nil {
		t.Errorf("Failed to load workflow: %v", err)
	}

	// Verify workflow was loaded correctly
	if wf.Name() != id {
		t.Errorf("Expected workflow name to be %s, got %s", id, wf.Name())
	}

	// Verify workflow was added to registry
	registryWf, err := registry.Workflow(id)
	if err != nil {
		t.Errorf("Workflow not found in registry: %v", err)
	}
	if registryWf != wf {
		t.Errorf("Expected workflow in registry to be %v, got %v", wf, registryWf)
	}

	// Verify workflow state was loaded correctly
	places = wf.Marking().Places()
	if len(places) != 1 || places[0] != initialPlace {
		t.Errorf("Expected workflow state to be %v, got %v", initialPlace, places)
	}
}

func TestManager_LoadWorkflow_EdgeCases(t *testing.T) {
	registry := workflow.NewRegistry()
	storage := NewMockStorage()
	manager := workflow.NewManager(registry, storage)

	places := []workflow.Place{"draft", "review", "published"}
	definition, err := workflow.NewDefinition(places, []workflow.Transition{})
	if err != nil {
		t.Fatalf("Failed to create workflow definition: %v", err)
	}

	// Test loading workflow that exists in registry (should return from registry)
	id := "test_workflow"
	initialPlace := workflow.Place("draft")
	wf, err := manager.CreateWorkflow(id, definition, initialPlace)
	if err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}

	// Load should return from registry, not storage
	loadedWf, err := manager.LoadWorkflow(id, definition)
	if err != nil {
		t.Errorf("Failed to load workflow: %v", err)
	}
	if loadedWf != wf {
		t.Errorf("Expected workflow from registry, got different instance")
	}

	// Test loading workflow with empty places slice (edge case)
	storage2 := NewMockStorage()
	manager2 := workflow.NewManager(workflow.NewRegistry(), storage2)

	// Save empty state (should cause error when loading)
	err = storage2.SaveState("empty_workflow", []workflow.Place{}, nil)
	if err != nil {
		t.Fatalf("Failed to save empty state: %v", err)
	}

	_, err = manager2.LoadWorkflow("empty_workflow", definition)
	if err == nil {
		t.Error("Expected error when loading workflow with empty places")
	}

	// Test loading workflow with context
	storage3 := NewMockStorage()
	manager3 := workflow.NewManager(workflow.NewRegistry(), storage3)

	contextData := map[string]interface{}{
		"key1": "value1",
		"key2": 42,
		"key3": true,
	}
	err = storage3.SaveState("workflow_with_context", []workflow.Place{initialPlace}, contextData)
	if err != nil {
		t.Fatalf("Failed to save workflow with context: %v", err)
	}

	loadedWf3, err := manager3.LoadWorkflow("workflow_with_context", definition)
	if err != nil {
		t.Errorf("Failed to load workflow with context: %v", err)
	}

	// Verify context was loaded
	for k, v := range contextData {
		val, ok := loadedWf3.Context(k)
		if !ok {
			t.Errorf("Context key %q not found", k)
		}
		if val != v {
			t.Errorf("Context key %q = %v, want %v", k, val, v)
		}
	}
}

func TestManager_CreateWorkflow_EdgeCases(t *testing.T) {
	registry := workflow.NewRegistry()
	storage := NewMockStorage()
	manager := workflow.NewManager(registry, storage)

	places := []workflow.Place{"draft", "review", "published"}
	definition, err := workflow.NewDefinition(places, []workflow.Transition{})
	if err != nil {
		t.Fatalf("Failed to create workflow definition: %v", err)
	}

	// Test creating workflow with invalid initial place
	_, err = manager.CreateWorkflow("invalid", definition, workflow.Place("invalid_place"))
	if err == nil {
		t.Error("Expected error when creating workflow with invalid initial place")
	}

	// Test creating workflow when storage fails (simulate with error storage)
	errorStorage := &ErrorStorage{}
	manager2 := workflow.NewManager(workflow.NewRegistry(), errorStorage)

	_, err = manager2.CreateWorkflow("error_test", definition, workflow.Place("draft"))
	if err == nil {
		t.Error("Expected error when storage fails")
	}

	// Test creating workflow when registry fails (simulate duplicate)
	wf1, err := manager.CreateWorkflow("duplicate_test", definition, workflow.Place("draft"))
	if err != nil {
		t.Fatalf("Failed to create first workflow: %v", err)
	}

	// Try to create duplicate (should fail in registry)
	_, err = manager.CreateWorkflow("duplicate_test", definition, workflow.Place("draft"))
	if err == nil {
		t.Error("Expected error when creating duplicate workflow")
	}

	// Verify first workflow still exists
	wf1Check, err := registry.Workflow("duplicate_test")
	if err != nil {
		t.Errorf("First workflow should still exist: %v", err)
	}
	if wf1Check != wf1 {
		t.Error("First workflow instance should be unchanged")
	}
}

func TestManager_AddEventListener(t *testing.T) {
	registry := workflow.NewRegistry()
	storage := NewMockStorage()
	manager := workflow.NewManager(registry, storage)

	// Test adding listener when Listeners is nil
	listener1 := func(event workflow.Event) error { return nil }
	manager.AddEventListener(workflow.EventBeforeTransition, listener1)

	if manager.Listeners == nil {
		t.Error("AddEventListener() did not initialize Listeners map")
	}
	if len(manager.Listeners[workflow.EventBeforeTransition]) != 1 {
		t.Errorf("AddEventListener() listener count = %d, want 1", len(manager.Listeners[workflow.EventBeforeTransition]))
	}

	// Test adding multiple listeners
	listener2 := func(event workflow.Event) error { return nil }
	manager.AddEventListener(workflow.EventBeforeTransition, listener2)

	if len(manager.Listeners[workflow.EventBeforeTransition]) != 2 {
		t.Errorf("AddEventListener() listener count = %d, want 2", len(manager.Listeners[workflow.EventBeforeTransition]))
	}

	// Test adding listener for different event type
	listener3 := func(event workflow.Event) error { return nil }
	manager.AddEventListener(workflow.EventAfterTransition, listener3)

	if len(manager.Listeners[workflow.EventAfterTransition]) != 1 {
		t.Errorf("AddEventListener() listener count for EventAfterTransition = %d, want 1", len(manager.Listeners[workflow.EventAfterTransition]))
	}
	if len(manager.Listeners[workflow.EventBeforeTransition]) != 2 {
		t.Errorf("AddEventListener() listener count for EventBeforeTransition = %d, want 2", len(manager.Listeners[workflow.EventBeforeTransition]))
	}
}

func TestManager_AddGuardEventListener(t *testing.T) {
	registry := workflow.NewRegistry()
	storage := NewMockStorage()
	manager := workflow.NewManager(registry, storage)

	// Test adding guard listener when Listeners is nil
	guardListener1 := func(event *workflow.GuardEvent) error { return nil }
	manager.AddGuardEventListener(guardListener1)

	if manager.Listeners == nil {
		t.Error("AddGuardEventListener() did not initialize Listeners map")
	}
	if len(manager.Listeners[workflow.EventGuard]) != 1 {
		t.Errorf("AddGuardEventListener() listener count = %d, want 1", len(manager.Listeners[workflow.EventGuard]))
	}

	// Test adding multiple guard listeners
	guardListener2 := func(event *workflow.GuardEvent) error { return nil }
	manager.AddGuardEventListener(guardListener2)

	if len(manager.Listeners[workflow.EventGuard]) != 2 {
		t.Errorf("AddGuardEventListener() listener count = %d, want 2", len(manager.Listeners[workflow.EventGuard]))
	}
}

func TestManager_RemoveListener(t *testing.T) {
	registry := workflow.NewRegistry()
	storage := NewMockStorage()
	manager := workflow.NewManager(registry, storage)

	// Add listeners and get handles
	handle1 := manager.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })
	handle2 := manager.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })
	handle3 := manager.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })

	if len(manager.Listeners[workflow.EventBeforeTransition]) != 3 {
		t.Fatalf("Expected 3 listeners, got %d", len(manager.Listeners[workflow.EventBeforeTransition]))
	}

	// Remove middle listener using handle
	manager.RemoveListener(handle2)

	if len(manager.Listeners[workflow.EventBeforeTransition]) != 2 {
		t.Errorf("RemoveListener() listener count = %d, want 2", len(manager.Listeners[workflow.EventBeforeTransition]))
	}

	// Remove last listener
	manager.RemoveListener(handle3)

	if len(manager.Listeners[workflow.EventBeforeTransition]) != 1 {
		t.Errorf("RemoveListener() listener count = %d, want 1", len(manager.Listeners[workflow.EventBeforeTransition]))
	}

	// Remove first listener
	manager.RemoveListener(handle1)

	if len(manager.Listeners[workflow.EventBeforeTransition]) != 0 {
		t.Errorf("RemoveListener() listener count = %d, want 0", len(manager.Listeners[workflow.EventBeforeTransition]))
	}

	// Test removing with nil handle (should not panic)
	manager.RemoveListener(nil)

	// Test removing same handle twice (should not panic)
	handle4 := manager.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })
	manager.RemoveListener(handle4)
	manager.RemoveListener(handle4) // Should be safe to call twice
}

func TestManager_RemoveEventListener(t *testing.T) {
	registry := workflow.NewRegistry()
	storage := NewMockStorage()
	manager := workflow.NewManager(registry, storage)

	// Test removing when Listeners is nil (should not panic)
	handle0 := manager.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })
	manager.RemoveListener(handle0)

	// Add listeners and store handles
	listener1 := func(event workflow.Event) error { return nil }
	listener2 := func(event workflow.Event) error { return nil }
	listener3 := func(event workflow.Event) error { return nil }

	handle1 := manager.AddEventListener(workflow.EventBeforeTransition, listener1)
	handle2 := manager.AddEventListener(workflow.EventBeforeTransition, listener2)
	handle3 := manager.AddEventListener(workflow.EventBeforeTransition, listener3)

	if len(manager.Listeners[workflow.EventBeforeTransition]) != 3 {
		t.Fatalf("Expected 3 listeners, got %d", len(manager.Listeners[workflow.EventBeforeTransition]))
	}

	// Remove middle listener using its handle
	manager.RemoveListener(handle2)

	if len(manager.Listeners[workflow.EventBeforeTransition]) != 2 {
		t.Errorf("RemoveListener() listener count = %d, want 2", len(manager.Listeners[workflow.EventBeforeTransition]))
	}

	// Test removing with nil handle (should not panic)
	manager.RemoveListener(nil)

	if len(manager.Listeners[workflow.EventBeforeTransition]) != 2 {
		t.Errorf("RemoveListener() listener count = %d, want 2", len(manager.Listeners[workflow.EventBeforeTransition]))
	}

	// Remove remaining listeners
	manager.RemoveListener(handle1)
	manager.RemoveListener(handle3)

	if len(manager.Listeners[workflow.EventBeforeTransition]) != 0 {
		t.Errorf("RemoveListener() listener count = %d, want 0", len(manager.Listeners[workflow.EventBeforeTransition]))
	}
}

// ErrorStorage is a mock storage that always returns an error
type ErrorStorage struct{}

func (e *ErrorStorage) LoadState(id string) ([]workflow.Place, map[string]interface{}, error) {
	return nil, nil, fmt.Errorf("storage error")
}

func (e *ErrorStorage) SaveState(id string, places []workflow.Place, context map[string]interface{}) error {
	return fmt.Errorf("storage error")
}

func (e *ErrorStorage) DeleteState(id string) error {
	return fmt.Errorf("storage error")
}
