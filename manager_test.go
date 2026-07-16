// Copyright (c) 2025 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ehabterra/workflow"
)

// MockStorage implements the Storage interface for testing. It stores the
// serialized marking (as the real backends do) so loaded state is decoupled from
// the in-memory workflow.
type MockStorage struct {
	states   map[string][]byte
	contexts map[string]map[string]any
	versions map[string]int64
}

func NewMockStorage() *MockStorage {
	return &MockStorage{
		states:   make(map[string][]byte),
		contexts: make(map[string]map[string]any),
		versions: make(map[string]int64),
	}
}

func (m *MockStorage) LoadState(ctx context.Context, id string) (workflow.Marking, map[string]any, int64, error) {
	data, ok := m.states[id]
	if !ok {
		return nil, nil, 0, fmt.Errorf("%w: %s", workflow.ErrWorkflowNotFound, id)
	}
	marking, err := workflow.UnmarshalMarkingJSON(data)
	if err != nil {
		return nil, nil, 0, err
	}
	contextData := m.contexts[id]
	if contextData == nil {
		contextData = map[string]any{}
	}
	return marking, contextData, m.versions[id], nil
}

func (m *MockStorage) SaveState(ctx context.Context, id string, marking workflow.Marking, context map[string]any, expectedVersion int64) (int64, error) {
	if m.versions[id] != expectedVersion {
		return 0, fmt.Errorf("%w: %s (expected version %d)", workflow.ErrConflict, id, expectedVersion)
	}
	data, err := json.Marshal(marking)
	if err != nil {
		return 0, err
	}
	m.states[id] = data
	if context == nil {
		context = map[string]any{}
	}
	m.contexts[id] = context
	m.versions[id] = expectedVersion + 1
	return m.versions[id], nil
}

func (m *MockStorage) DeleteState(ctx context.Context, id string) error {
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
	manager := workflow.NewManager(registry, storage, workflow.WithRegistryCache())

	// Create a simple workflow definition
	places := []workflow.Place{"draft", "review", "published"}
	definition, err := workflow.NewDefinition(places, []workflow.Transition{})
	if err != nil {
		t.Fatalf("Failed to create workflow definition: %v", err)
	}

	// Test creating a new workflow
	id := "test_workflow"
	initialPlace := workflow.Place("draft")
	wf, err := manager.CreateWorkflow(context.Background(), id, definition, initialPlace)
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
	m, _, _, err := storage.LoadState(context.Background(), id)
	if err != nil {
		t.Fatalf("Failed to load workflow state: %v", err)
	}
	states := m.Places()
	if len(states) != 1 || states[0] != initialPlace {
		t.Errorf("Expected initial state to be %v, got %v", initialPlace, states)
	}
}

func TestManager_GetWorkflow(t *testing.T) {
	registry := workflow.NewRegistry()
	storage := NewMockStorage()
	manager := workflow.NewManager(registry, storage, workflow.WithRegistryCache())

	// Create a simple workflow definition
	places := []workflow.Place{"draft", "review", "published"}
	definition, err := workflow.NewDefinition(places, []workflow.Transition{})
	if err != nil {
		t.Fatalf("Failed to create workflow definition: %v", err)
	}

	// Test getting a non-existent workflow
	_, err = manager.GetWorkflow(context.Background(), "non_existent", definition)
	if err == nil {
		t.Error("Expected error when getting non-existent workflow")
	}

	// Create a workflow
	id := "test_workflow"
	initialPlace := workflow.Place("draft")
	wf, err := manager.CreateWorkflow(context.Background(), id, definition, initialPlace)
	if err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}

	// Test getting the workflow
	retrievedWf, err := manager.GetWorkflow(context.Background(), id, definition)
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
	wf, err := manager.CreateWorkflow(context.Background(), id, definition, initialPlace)
	if err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}

	// Change the workflow state
	newPlace := workflow.Place("review")
	wf.Marking().SetPlaces([]workflow.Place{newPlace})

	// Save the workflow
	err = manager.SaveWorkflow(context.Background(), id, wf)
	if err != nil {
		t.Errorf("Failed to save workflow: %v", err)
	}

	// Verify the state was saved
	m, _, _, err := storage.LoadState(context.Background(), id)
	if err != nil {
		t.Fatalf("Failed to load workflow state: %v", err)
	}
	states := m.Places()
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
	_, err = manager.CreateWorkflow(context.Background(), id, definition, initialPlace)
	if err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}

	// Delete the workflow
	err = manager.DeleteWorkflow(context.Background(), id)
	if err != nil {
		t.Errorf("Failed to delete workflow: %v", err)
	}

	// Verify workflow was removed from registry
	_, err = registry.Workflow(id)
	if err == nil {
		t.Error("Expected error when getting deleted workflow from registry")
	}

	// Verify workflow state was removed from storage
	_, _, _, err = storage.LoadState(context.Background(), id)
	if err == nil {
		t.Error("Expected error when getting deleted workflow from storage")
	}
}

func TestManager_LoadWorkflow(t *testing.T) {
	registry := workflow.NewRegistry()
	storage := NewMockStorage()
	manager := workflow.NewManager(registry, storage, workflow.WithRegistryCache())

	// Create a simple workflow definition
	places := []workflow.Place{"draft", "review", "published"}
	definition, err := workflow.NewDefinition(places, []workflow.Transition{})
	if err != nil {
		t.Fatalf("Failed to create workflow definition: %v", err)
	}

	// Test loading a non-existent workflow
	_, err = manager.LoadWorkflow(context.Background(), "non_existent", definition)
	if err == nil {
		t.Error("Expected error when loading non-existent workflow")
	}

	// Create a workflow and save its state
	id := "test_workflow"
	initialPlace := workflow.Place("draft")
	_, err = storage.SaveState(context.Background(), id, workflow.NewMarking([]workflow.Place{initialPlace}), seedContext(definition, nil), 0)
	if err != nil {
		t.Fatalf("Failed to save workflow state: %v", err)
	}

	// Load the workflow
	wf, err := manager.LoadWorkflow(context.Background(), id, definition)
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
	manager := workflow.NewManager(registry, storage, workflow.WithRegistryCache())

	places := []workflow.Place{"draft", "review", "published"}
	definition, err := workflow.NewDefinition(places, []workflow.Transition{})
	if err != nil {
		t.Fatalf("Failed to create workflow definition: %v", err)
	}

	// Test loading workflow that exists in registry (should return from registry)
	id := "test_workflow"
	initialPlace := workflow.Place("draft")
	wf, err := manager.CreateWorkflow(context.Background(), id, definition, initialPlace)
	if err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}

	// Load should return from registry, not storage
	loadedWf, err := manager.LoadWorkflow(context.Background(), id, definition)
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
	_, err = storage2.SaveState(context.Background(), "empty_workflow", workflow.NewMarking(nil), nil, 0)
	if err != nil {
		t.Fatalf("Failed to save empty state: %v", err)
	}

	_, err = manager2.LoadWorkflow(context.Background(), "empty_workflow", definition)
	if err == nil {
		t.Error("Expected error when loading workflow with empty places")
	}

	// Test loading workflow with context
	storage3 := NewMockStorage()
	manager3 := workflow.NewManager(workflow.NewRegistry(), storage3)

	contextData := map[string]any{
		"key1": "value1",
		"key2": 42,
		"key3": true,
	}
	_, err = storage3.SaveState(context.Background(), "workflow_with_context", workflow.NewMarking([]workflow.Place{initialPlace}), seedContext(definition, contextData), 0)
	if err != nil {
		t.Fatalf("Failed to save workflow with context: %v", err)
	}

	loadedWf3, err := manager3.LoadWorkflow(context.Background(), "workflow_with_context", definition)
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
	manager := workflow.NewManager(registry, storage, workflow.WithRegistryCache())

	places := []workflow.Place{"draft", "review", "published"}
	definition, err := workflow.NewDefinition(places, []workflow.Transition{})
	if err != nil {
		t.Fatalf("Failed to create workflow definition: %v", err)
	}

	// Test creating workflow with invalid initial place
	_, err = manager.CreateWorkflow(context.Background(), "invalid", definition, workflow.Place("invalid_place"))
	if err == nil {
		t.Error("Expected error when creating workflow with invalid initial place")
	}

	// Test creating workflow when storage fails (simulate with error storage)
	errorStorage := &ErrorStorage{}
	manager2 := workflow.NewManager(workflow.NewRegistry(), errorStorage)

	_, err = manager2.CreateWorkflow(context.Background(), "error_test", definition, workflow.Place("draft"))
	if err == nil {
		t.Error("Expected error when storage fails")
	}

	// Test creating workflow when registry fails (simulate duplicate)
	wf1, err := manager.CreateWorkflow(context.Background(), "duplicate_test", definition, workflow.Place("draft"))
	if err != nil {
		t.Fatalf("Failed to create first workflow: %v", err)
	}

	// Try to create duplicate (should fail in registry)
	_, err = manager.CreateWorkflow(context.Background(), "duplicate_test", definition, workflow.Place("draft"))
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

	if manager.ListenerCount(workflow.EventBeforeTransition) != 1 {
		t.Errorf("AddEventListener() listener count = %d, want 1", manager.ListenerCount(workflow.EventBeforeTransition))
	}

	// Test adding multiple listeners
	listener2 := func(event workflow.Event) error { return nil }
	manager.AddEventListener(workflow.EventBeforeTransition, listener2)

	if manager.ListenerCount(workflow.EventBeforeTransition) != 2 {
		t.Errorf("AddEventListener() listener count = %d, want 2", manager.ListenerCount(workflow.EventBeforeTransition))
	}

	// Test adding listener for different event type
	listener3 := func(event workflow.Event) error { return nil }
	manager.AddEventListener(workflow.EventAfterTransition, listener3)

	if manager.ListenerCount(workflow.EventAfterTransition) != 1 {
		t.Errorf("AddEventListener() listener count for EventAfterTransition = %d, want 1", manager.ListenerCount(workflow.EventAfterTransition))
	}
	if manager.ListenerCount(workflow.EventBeforeTransition) != 2 {
		t.Errorf("AddEventListener() listener count for EventBeforeTransition = %d, want 2", manager.ListenerCount(workflow.EventBeforeTransition))
	}
}

func TestManager_AddGuardEventListener(t *testing.T) {
	registry := workflow.NewRegistry()
	storage := NewMockStorage()
	manager := workflow.NewManager(registry, storage)

	// Test adding guard listener when Listeners is nil
	guardListener1 := func(event *workflow.GuardEvent) error { return nil }
	manager.AddGuardEventListener(guardListener1)

	if manager.ListenerCount(workflow.EventGuard) != 1 {
		t.Errorf("AddGuardEventListener() listener count = %d, want 1", manager.ListenerCount(workflow.EventGuard))
	}

	// Test adding multiple guard listeners
	guardListener2 := func(event *workflow.GuardEvent) error { return nil }
	manager.AddGuardEventListener(guardListener2)

	if manager.ListenerCount(workflow.EventGuard) != 2 {
		t.Errorf("AddGuardEventListener() listener count = %d, want 2", manager.ListenerCount(workflow.EventGuard))
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

	if manager.ListenerCount(workflow.EventBeforeTransition) != 3 {
		t.Fatalf("Expected 3 listeners, got %d", manager.ListenerCount(workflow.EventBeforeTransition))
	}

	// Remove middle listener using handle
	manager.RemoveListener(handle2)

	if manager.ListenerCount(workflow.EventBeforeTransition) != 2 {
		t.Errorf("RemoveListener() listener count = %d, want 2", manager.ListenerCount(workflow.EventBeforeTransition))
	}

	// Remove last listener
	manager.RemoveListener(handle3)

	if manager.ListenerCount(workflow.EventBeforeTransition) != 1 {
		t.Errorf("RemoveListener() listener count = %d, want 1", manager.ListenerCount(workflow.EventBeforeTransition))
	}

	// Remove first listener
	manager.RemoveListener(handle1)

	if manager.ListenerCount(workflow.EventBeforeTransition) != 0 {
		t.Errorf("RemoveListener() listener count = %d, want 0", manager.ListenerCount(workflow.EventBeforeTransition))
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

	if manager.ListenerCount(workflow.EventBeforeTransition) != 3 {
		t.Fatalf("Expected 3 listeners, got %d", manager.ListenerCount(workflow.EventBeforeTransition))
	}

	// Remove middle listener using its handle
	manager.RemoveListener(handle2)

	if manager.ListenerCount(workflow.EventBeforeTransition) != 2 {
		t.Errorf("RemoveListener() listener count = %d, want 2", manager.ListenerCount(workflow.EventBeforeTransition))
	}

	// Test removing with nil handle (should not panic)
	manager.RemoveListener(nil)

	if manager.ListenerCount(workflow.EventBeforeTransition) != 2 {
		t.Errorf("RemoveListener() listener count = %d, want 2", manager.ListenerCount(workflow.EventBeforeTransition))
	}

	// Remove remaining listeners
	manager.RemoveListener(handle1)
	manager.RemoveListener(handle3)

	if manager.ListenerCount(workflow.EventBeforeTransition) != 0 {
		t.Errorf("RemoveListener() listener count = %d, want 0", manager.ListenerCount(workflow.EventBeforeTransition))
	}
}

func TestManager_RemoveListener_EdgeCases(t *testing.T) {
	registry := workflow.NewRegistry()
	storage := NewMockStorage()
	manager := workflow.NewManager(registry, storage)
	manager2 := workflow.NewManager(registry, storage)

	// Test removing handle from different manager
	handle1 := manager.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })
	handle2 := manager2.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })

	// Try to remove handle2 (from manager2) from manager1 - should not remove anything
	manager.RemoveListener(handle2)
	if manager.ListenerCount(workflow.EventBeforeTransition) != 1 {
		t.Errorf("RemoveListener() with wrong owner should not remove listener, got %d", manager.ListenerCount(workflow.EventBeforeTransition))
	}

	// Clean up
	manager.RemoveListener(handle1)
}

func TestManager_LoadWorkflow_EmptyPlaces(t *testing.T) {
	registry := workflow.NewRegistry()
	storage := NewMockStorage()
	manager := workflow.NewManager(registry, storage)

	def, err := workflow.NewDefinition([]workflow.Place{"start", "end"}, []workflow.Transition{})
	if err != nil {
		t.Fatalf("NewDefinition() failed: %v", err)
	}

	// Save state with no marked places — valid for a pure token-pool net,
	// whose places are all legitimately empty between batches.
	_, err = storage.SaveState(context.Background(), "empty_places", workflow.NewMarking(nil), seedContext(def, nil), 0)
	if err != nil {
		t.Fatalf("SaveState() failed: %v", err)
	}

	// The empty marking loads as-is (it used to be rejected with
	// "loaded state has no places").
	wf, err := manager.LoadWorkflow(context.Background(), "empty_places", def)
	if err != nil {
		t.Fatalf("LoadWorkflow() with an empty marking should succeed, got: %v", err)
	}
	if places := wf.Marking().Places(); len(places) != 0 {
		t.Errorf("expected an empty marking, got %v", places)
	}
}

// ErrorStorage is a mock storage that always returns an error
type ErrorStorage struct{}

func (e *ErrorStorage) LoadState(ctx context.Context, id string) (workflow.Marking, map[string]any, int64, error) {
	return nil, nil, 0, fmt.Errorf("storage error")
}

func (e *ErrorStorage) SaveState(ctx context.Context, id string, marking workflow.Marking, context map[string]any, expectedVersion int64) (int64, error) {
	return 0, fmt.Errorf("storage error")
}

func (e *ErrorStorage) DeleteState(ctx context.Context, id string) error {
	return fmt.Errorf("storage error")
}

// seedContext returns ctxData stamped with the definition fingerprint under
// the Manager's reserved key, the way every Manager save writes it — required
// for a directly-seeded row to pass the strict load check.
func seedContext(def *workflow.Definition, ctxData map[string]any) map[string]any {
	if ctxData == nil {
		ctxData = map[string]any{}
	}
	ctxData["__workflow_def_fingerprint"] = def.Fingerprint()
	return ctxData
}
