package workflow

import (
	"fmt"
	"sync"
)

// Registry manages multiple workflows
type Registry struct {
	workflows map[string]*Workflow
	mu        sync.RWMutex
}

// NewRegistry creates a new workflow registry
func NewRegistry() *Registry {
	return &Registry{
		workflows: make(map[string]*Workflow),
	}
}

// AddWorkflow adds a workflow to the registry
func (r *Registry) AddWorkflow(wf *Workflow) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if wf == nil {
		return fmt.Errorf("%w: workflow cannot be nil", ErrInvalidWorkflow)
	}

	name := wf.Name()
	if _, exists := r.workflows[name]; exists {
		return fmt.Errorf("%w: %s", ErrWorkflowExists, name)
	}

	r.workflows[name] = wf
	return nil
}

// Workflow returns a workflow by name
func (r *Registry) Workflow(name string) (*Workflow, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if wf, ok := r.workflows[name]; ok {
		return wf, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrWorkflowNotFound, name)
}

// RemoveWorkflow removes a workflow from the registry
func (r *Registry) RemoveWorkflow(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.workflows[name]; !exists {
		return fmt.Errorf("%w: %s", ErrWorkflowNotFound, name)
	}

	delete(r.workflows, name)
	return nil
}

// ListWorkflows returns a list of all workflow names
func (r *Registry) ListWorkflows() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.workflows))
	for name := range r.workflows {
		names = append(names, name)
	}
	return names
}

// HasWorkflow checks if a workflow exists
func (r *Registry) HasWorkflow(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.workflows[name]
	return exists
}
