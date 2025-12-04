package workflow_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/ehabterra/workflow"
)

func TestNewRegistry(t *testing.T) {
	registry := workflow.NewRegistry()
	if registry == nil {
		t.Error("NewRegistry() returned nil")
	}
}

func TestRegistry_AddWorkflow(t *testing.T) {
	tests := []struct {
		name     string
		workflow *workflow.Workflow
		wantErr  bool
	}{
		{
			name:     "add valid workflow",
			workflow: createTestWorkflow(t, "test1"),
			wantErr:  false,
		},
		{
			name:     "add nil workflow",
			workflow: nil,
			wantErr:  true,
		},
		{
			name:     "add duplicate workflow",
			workflow: createTestWorkflow(t, "test1"),
			wantErr:  true,
		},
	}

	registry := workflow.NewRegistry()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := registry.AddWorkflow(tt.workflow)
			if (err != nil) != tt.wantErr {
				t.Errorf("AddWorkflow() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRegistry_Workflow(t *testing.T) {
	tests := []struct {
		name     string
		workflow *workflow.Workflow
		getName  string
		wantErr  bool
	}{
		{
			name:     "get existing workflow",
			workflow: createTestWorkflow(t, "test1"),
			getName:  "test1",
			wantErr:  false,
		},
		{
			name:     "get non-existent workflow",
			workflow: createTestWorkflow(t, "test1"),
			getName:  "test2",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := workflow.NewRegistry()

			if tt.workflow != nil {
				err := registry.AddWorkflow(tt.workflow)
				if err != nil {
					t.Fatalf("failed to add workflow: %v", err)
				}
			}

			got, err := registry.Workflow(tt.getName)
			if (err != nil) != tt.wantErr {
				t.Errorf("Workflow() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr && got != nil {
				name := got.Name()
				if name != tt.getName {
					t.Errorf("Workflow() name = %v, want %v", name, tt.getName)
				}
			}
		})
	}
}

func TestRegistry_RemoveWorkflow(t *testing.T) {
	tests := []struct {
		name       string
		workflow   *workflow.Workflow
		removeName string
		wantErr    bool
	}{
		{
			name:       "remove existing workflow",
			workflow:   createTestWorkflow(t, "test1"),
			removeName: "test1",
			wantErr:    false,
		},
		{
			name:       "remove non-existent workflow",
			workflow:   createTestWorkflow(t, "test1"),
			removeName: "test2",
			wantErr:    true,
		},
	}

	registry := workflow.NewRegistry()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.workflow != nil {
				err := registry.AddWorkflow(tt.workflow)
				if err != nil {
					t.Fatalf("failed to add workflow: %v", err)
				}
			}

			err := registry.RemoveWorkflow(tt.removeName)
			if (err != nil) != tt.wantErr {
				t.Errorf("RemoveWorkflow() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr {
				// Verify workflow was removed
				_, err := registry.Workflow(tt.removeName)
				if err == nil {
					t.Error("RemoveWorkflow() workflow still exists")
				}
			}
		})
	}
}

func TestRegistry_ListWorkflows(t *testing.T) {
	tests := []struct {
		name      string
		workflows []*workflow.Workflow
		want      []string
	}{
		{
			name:      "empty registry",
			workflows: []*workflow.Workflow{},
			want:      []string{},
		},
		{
			name:      "single workflow",
			workflows: []*workflow.Workflow{createTestWorkflow(t, "test1")},
			want:      []string{"test1"},
		},
		{
			name: "multiple workflows",
			workflows: []*workflow.Workflow{
				createTestWorkflow(t, "test1"),
				createTestWorkflow(t, "test2"),
				createTestWorkflow(t, "test3"),
			},
			want: []string{"test1", "test2", "test3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := workflow.NewRegistry()

			// Add workflows
			for _, wf := range tt.workflows {
				err := registry.AddWorkflow(wf)
				if err != nil {
					t.Fatalf("failed to add workflow: %v", err)
				}
			}

			got := registry.ListWorkflows()
			if len(got) != len(tt.want) {
				t.Errorf("ListWorkflows() count = %v, want %v", len(got), len(tt.want))
			}

			// Create a map of expected names for easy lookup
			wantMap := make(map[string]bool)
			for _, name := range tt.want {
				wantMap[name] = true
			}

			// Check that all returned names are expected
			for _, name := range got {
				if !wantMap[name] {
					t.Errorf("ListWorkflows() unexpected name %v", name)
				}
			}
		})
	}
}

func TestRegistry_HasWorkflow(t *testing.T) {
	tests := []struct {
		name      string
		workflow  *workflow.Workflow
		checkName string
		want      bool
	}{
		{
			name:      "workflow exists",
			workflow:  createTestWorkflow(t, "test1"),
			checkName: "test1",
			want:      true,
		},
		{
			name:      "workflow does not exist",
			workflow:  createTestWorkflow(t, "test1"),
			checkName: "test2",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := workflow.NewRegistry()

			if tt.workflow != nil {
				err := registry.AddWorkflow(tt.workflow)
				if err != nil {
					t.Fatalf("failed to add workflow: %v", err)
				}
			}

			got := registry.HasWorkflow(tt.checkName)
			if got != tt.want {
				t.Errorf("HasWorkflow() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	registry := workflow.NewRegistry()

	// Add some initial workflows
	for i := 0; i < 5; i++ {
		wf := createTestWorkflow(t, fmt.Sprintf("workflow-%d", i))
		err := registry.AddWorkflow(wf)
		if err != nil {
			t.Fatalf("failed to add workflow: %v", err)
		}
	}

	// Test concurrent reads
	t.Run("concurrent reads", func(t *testing.T) {
		const numGoroutines = 10
		const numReads = 100

		var wg sync.WaitGroup
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < numReads; j++ {
					// Random workflow name
					name := fmt.Sprintf("workflow-%d", j%5)
					_, err := registry.Workflow(name)
					if err != nil && j < 5 { // First 5 should exist
						t.Errorf("unexpected error reading workflow %s: %v", name, err)
					}

					// Test HasWorkflow
					exists := registry.HasWorkflow(name)
					if j < 5 && !exists {
						t.Errorf("workflow %s should exist", name)
					}

					// Test ListWorkflows
					list := registry.ListWorkflows()
					if len(list) != 5 {
						t.Errorf("expected 5 workflows, got %d", len(list))
					}
				}
			}()
		}
		wg.Wait()
	})

	// Test concurrent reads and writes
	t.Run("concurrent reads and writes", func(t *testing.T) {
		const numReaders = 5
		const numWriters = 3
		const numOperations = 50

		var wg sync.WaitGroup

		// Start readers
		for i := 0; i < numReaders; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < numOperations; j++ {
					name := fmt.Sprintf("workflow-%d", j%5)
					_, _ = registry.Workflow(name) // Ignore error in concurrent test
					registry.HasWorkflow(name)
					registry.ListWorkflows()
				}
			}()
		}

		// Start writers
		for i := 0; i < numWriters; i++ {
			wg.Add(1)
			go func(writerID int) {
				defer wg.Done()
				for j := 0; j < numOperations; j++ {
					// Add new workflow
					name := fmt.Sprintf("concurrent-workflow-%d-%d", writerID, j)
					wf := createTestWorkflow(t, name)
					if err := registry.AddWorkflow(wf); err != nil {
						t.Errorf("Failed to add workflow: %v", err)
					}

					// Remove it
					if err := registry.RemoveWorkflow(name); err != nil {
						t.Errorf("Failed to remove workflow: %v", err)
					}
				}
			}(i)
		}

		wg.Wait()

		// Verify final state
		list := registry.ListWorkflows()
		if len(list) != 5 {
			t.Errorf("expected 5 workflows after concurrent operations, got %d", len(list))
		}
	})
}

// Helper function to create a test workflow
func createTestWorkflow(t *testing.T, name string) *workflow.Workflow {
	t.Helper()
	def, err := workflow.NewDefinition(
		[]workflow.Place{"start", "end"},
		[]workflow.Transition{},
	)
	if err != nil {
		t.Fatalf("failed to create definition: %v", err)
	}
	wf, err := workflow.NewWorkflow(name, def, "start")
	if err != nil {
		t.Fatalf("failed to create workflow: %v", err)
	}
	return wf
}
