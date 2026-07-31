// Copyright (c) 2025 Ehab Terra
// SPDX-License-Identifier: MIT

package yaml

import (
	"fmt"
	"strings"
	"time"

	"github.com/ehabterra/workflow"
)

// Loader provides functionality to load workflow definitions from YAML configuration.
type Loader struct {
	// EnvBuilder allows customizing the expression evaluation environment
	EnvBuilder func(workflow.Event) map[string]any

	// TxEnvBuilder builds the environment for `tx_guard:` expressions, which are
	// evaluated INSIDE the firing transaction and can therefore query host state
	// as of the state the save is about to be checked against. A definition that
	// uses `tx_guard:` without one fails to load — there would be nothing for the
	// expression to call.
	TxEnvBuilder workflow.TxEnvBuilder
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

// NewLoaderWithTxEnv creates a loader that can compile `tx_guard:` expressions:
// guards evaluated inside the firing transaction, against an environment the
// builder assembles from that transaction.
//
// Definitions loaded this way must be driven with Manager.Execute against a
// workflow.TxScopedStorage backend; see workflow.TxEnvBuilder for the contract
// (including what happens across ErrConflict retries).
func NewLoaderWithTxEnv(txEnvBuilder workflow.TxEnvBuilder) *Loader {
	return &Loader{
		TxEnvBuilder: txEnvBuilder,
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
			for _, reset := range trans.Resets {
				placeSet[reset] = true
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

		// Transaction-scoped guard: evaluated inside the firing transaction, so
		// it can consult host state rather than a value read before the
		// transaction opened. Without a builder there is nothing for it to call,
		// so refuse at load rather than at the first firing.
		if transConfig.TxGuard != "" {
			if l.TxEnvBuilder == nil {
				return nil, fmt.Errorf("transition '%s': tx_guard needs a loader built with NewLoaderWithTxEnv "+
					"(the expression is evaluated inside the firing transaction and has nothing to query without one)",
					transConfig.Name)
			}
			txConstraint, err := workflow.NewTxExpressionConstraint(transConfig.TxGuard, l.TxEnvBuilder)
			if err != nil {
				return nil, fmt.Errorf("failed to create tx_guard constraint for transition '%s': %w", transConfig.Name, err)
			}
			transition.AddConstraint(txConstraint)
			// Structural: the expression is part of the definition fingerprint
			// and is rendered on the diagram, like a plain guard.
			transition.SetMetadata("tx_guard", transConfig.TxGuard)
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

		// OR-input (merge): enabled by any one marked input, consuming it.
		if transConfig.FromAny {
			transition.SetFromAny(true)
		}

		// Reset arcs (cancellation region): places cleared when this
		// transition fires. Place existence is validated by NewDefinition.
		if len(transConfig.Resets) > 0 {
			resets := make([]workflow.Place, len(transConfig.Resets))
			for i, p := range transConfig.Resets {
				resets[i] = workflow.Place(p)
			}
			transition.SetResets(resets...)
		}

		// Dynamic-cardinality joins. Expressions are compiled here, so a
		// malformed count fails the load rather than the first firing.
		if len(transConfig.Require) > 0 {
			reqs, err := requirements(transConfig.Name, transConfig.Require)
			if err != nil {
				return nil, err
			}
			transition.SetRequirements(reqs...)
		}

		// Add transition metadata
		if len(transConfig.Metadata) > 0 {
			for key, value := range transConfig.Metadata {
				transition.SetMetadata(key, value)
			}
		}

		// Declared effects. Order is semantic — it is the execution order — so
		// the slices are carried through as written, never sorted.
		if len(transConfig.Effects) > 0 {
			decls, err := effectDecls(transConfig.Name, "effects", transConfig.Effects)
			if err != nil {
				return nil, err
			}
			transition.SetEffects(decls...)
		}
		if len(transConfig.AfterCommit) > 0 {
			decls, err := effectDecls(transConfig.Name, "after_commit", transConfig.AfterCommit)
			if err != nil {
				return nil, err
			}
			transition.SetAfterCommit(decls...)
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

	// Attach per-place metadata (e.g. diagram_group) so it reaches the diagram
	// renderer, which works off the Definition.
	for p, meta := range placeMetadata {
		definition.SetPlaceMetadata(p, meta)
	}

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

// requirements compiles the declared dynamic-cardinality joins for one
// transition.
func requirements(transition string, cfgs []RequireConfig) ([]workflow.Requirement, error) {
	reqs := make([]workflow.Requirement, len(cfgs))
	for i, c := range cfgs {
		r, err := workflow.NewRequirement(workflow.RequirementSpec{
			Place:    workflow.Place(c.Place),
			Count:    string(c.Count),
			Where:    c.Where,
			Distinct: c.Distinct,
		})
		if err != nil {
			return nil, fmt.Errorf("transition '%s': require[%d]: %w", transition, i, err)
		}
		reqs[i] = r
	}
	return reqs, nil
}

// effectDecls converts declared effects, rejecting an empty name: an effect
// with no name can never resolve, and failing at load beats failing the first
// time that transition fires.
func effectDecls(transition, field string, cfgs []EffectConfig) ([]workflow.EffectDecl, error) {
	decls := make([]workflow.EffectDecl, len(cfgs))
	for i, c := range cfgs {
		if strings.TrimSpace(c.Name) == "" {
			return nil, fmt.Errorf("transition '%s': %s[%d] has an empty name", transition, field, i)
		}
		decls[i] = workflow.EffectDecl{Name: c.Name, Params: c.Params}
	}
	return decls, nil
}
