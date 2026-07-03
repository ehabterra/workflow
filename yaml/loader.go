package yaml

import (
	"fmt"
	"time"

	"github.com/ehabterra/workflow"
)

// Loader provides functionality to load workflow definitions from YAML configuration.
type Loader struct {
	// EnvBuilder allows customizing the expression evaluation environment
	EnvBuilder func(workflow.Event) map[string]any
}

// NewLoader creates a new YAML loader.
func NewLoader() *Loader {
	return &Loader{}
}

// NewLoaderWithEnv creates a new YAML loader with a custom environment builder for expressions.
func NewLoaderWithEnv(envBuilder func(workflow.Event) map[string]any) *Loader {
	return &Loader{
		EnvBuilder: envBuilder,
	}
}

// LoadDefinition loads a workflow definition from a YAML configuration.
func (l *Loader) LoadDefinition(config *Config) (*workflow.Definition, error) {
	// Extract places
	places := make([]workflow.Place, 0)
	placeMetadata := make(map[workflow.Place]map[string]any)

	// If places are explicitly defined, use them
	if len(config.Workflow.Places) > 0 {
		for _, placeConfig := range config.Workflow.Places {
			places = append(places, workflow.Place(placeConfig.Name))
			if len(placeConfig.Metadata) > 0 {
				placeMetadata[workflow.Place(placeConfig.Name)] = placeConfig.Metadata
			}
		}
	} else {
		// Otherwise, extract from transitions
		placeSet := make(map[string]bool)
		for _, trans := range config.Workflow.Transitions {
			for _, from := range trans.From {
				placeSet[from] = true
			}
			for _, to := range trans.To {
				placeSet[to] = true
			}
		}
		for placeName := range placeSet {
			places = append(places, workflow.Place(placeName))
		}
	}

	// Create transitions
	transitions := make([]workflow.Transition, 0)
	for _, transConfig := range config.Workflow.Transitions {
		fromPlaces := make([]workflow.Place, len(transConfig.From))
		for i, p := range transConfig.From {
			fromPlaces[i] = workflow.Place(p)
		}

		toPlaces := make([]workflow.Place, len(transConfig.To))
		for i, p := range transConfig.To {
			toPlaces[i] = workflow.Place(p)
		}

		transition, err := workflow.NewTransition(transConfig.Name, fromPlaces, toPlaces)
		if err != nil {
			return nil, fmt.Errorf("failed to create transition '%s': %w", transConfig.Name, err)
		}

		// Add guard expression if provided
		if transConfig.Guard != "" {
			var exprConstraint *workflow.ExpressionConstraint
			var err error

			if l.EnvBuilder != nil {
				exprConstraint, err = workflow.NewExpressionConstraintWithEnv(transConfig.Guard, l.EnvBuilder)
			} else {
				exprConstraint, err = workflow.NewExpressionConstraint(transConfig.Guard)
			}

			if err != nil {
				return nil, fmt.Errorf("failed to create expression constraint for transition '%s': %w", transConfig.Name, err)
			}

			transition.AddConstraint(exprConstraint)
			// Store guard string in metadata for diagram generation
			transition.SetMetadata("guard", transConfig.Guard)
		}

		// Add timeout if provided ("after: 72h" — host-driven timers, roadmap M4)
		if transConfig.After != "" {
			d, err := time.ParseDuration(transConfig.After)
			if err != nil {
				return nil, fmt.Errorf("transition '%s': invalid after duration %q: %w", transConfig.Name, transConfig.After, err)
			}
			if d <= 0 {
				return nil, fmt.Errorf("transition '%s': after duration must be positive, got %q", transConfig.Name, transConfig.After)
			}
			transition.SetTimeoutAfter(d)
		}

		// Add transition metadata
		if len(transConfig.Metadata) > 0 {
			for key, value := range transConfig.Metadata {
				transition.SetMetadata(key, value)
			}
		}

		// Store history-related metadata (notes, actor, custom_fields) in metadata
		// These will be used when saving history records
		if transConfig.Notes != "" {
			transition.SetMetadata("history_notes", transConfig.Notes)
		}
		if transConfig.Actor != "" {
			transition.SetMetadata("history_actor", transConfig.Actor)
		}
		if len(transConfig.CustomFields) > 0 {
			transition.SetMetadata("history_custom_fields", transConfig.CustomFields)
		}

		transitions = append(transitions, *transition)
	}

	// Create definition
	definition, err := workflow.NewDefinition(places, transitions)
	if err != nil {
		return nil, fmt.Errorf("failed to create definition: %w", err)
	}

	// Store workflow-level metadata in definition (we'll need to extend Definition for this)
	// For now, we'll handle this when creating the workflow instance

	return definition, nil
}

// LoadWorkflow creates a workflow instance from a YAML configuration.
func (l *Loader) LoadWorkflow(config *Config, workflowID string) (*workflow.Workflow, error) {
	definition, err := l.LoadDefinition(config)
	if err != nil {
		return nil, err
	}

	// Build the initial marking from initial_marking: a place with no declared
	// tokens gets an uncolored presence token; a place with tokens is seeded with
	// exactly those colored tokens.
	initial := workflow.NewMarking(nil)
	for place, tokenList := range config.Workflow.InitialMarking.Places {
		p := workflow.Place(place)
		if len(tokenList) == 0 {
			_ = initial.AddPlace(p)
			continue
		}
		for _, d := range tokenList {
			initial.AddToken(p, workflow.NewToken(workflow.TokenData(d)))
		}
	}

	wf, err := workflow.NewWorkflowFromMarking(workflowID, definition, initial)
	if err != nil {
		return nil, fmt.Errorf("failed to create workflow: %w", err)
	}

	// Inject workflow-level metadata into context
	if len(config.Workflow.Metadata) > 0 {
		for key, value := range config.Workflow.Metadata {
			wf.SetContext(key, value)
		}
	}

	// Inject place metadata into context (with prefix to avoid conflicts)
	// Extract place metadata from config
	placeMetadata := make(map[string]map[string]any)
	for _, placeConfig := range config.Workflow.Places {
		if len(placeConfig.Metadata) > 0 {
			placeMetadata[placeConfig.Name] = placeConfig.Metadata
		}
	}

	// Store place metadata in context with prefix
	if len(placeMetadata) > 0 {
		wf.SetContext("_place_metadata", placeMetadata)
	}

	return wf, nil
}
