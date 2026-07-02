package yaml

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/history"
)

// ApplyTransitionWithHistory applies a transition and saves history using metadata from the transition.
// This is the legacy version that uses target places. For new code, prefer ApplyTransitionByNameWithHistory.
func ApplyTransitionWithHistory(
	wf *workflow.Workflow,
	to []workflow.Place,
	historyStore history.HistoryStore,
	ctx context.Context,
	overrideNotes string,
	overrideActor string,
	overrideCustomFields map[string]any,
) error {
	// Get current places before transition
	currentPlaces := wf.Marking().Places()
	if len(currentPlaces) == 0 {
		return fmt.Errorf("workflow has no current places")
	}

	// Find the transition being applied
	definition := wf.Definition()
	var transition *workflow.Transition
	for _, t := range definition.AllTransitions() {
		// Check if this transition matches
		fromMatch := true
		for _, fromPlace := range t.From() {
			found := slices.Contains(currentPlaces, fromPlace)
			if !found {
				fromMatch = false
				break
			}
		}

		toMatch := len(t.To()) == len(to)
		if toMatch {
			for i, toPlace := range t.To() {
				if toPlace != to[i] {
					toMatch = false
					break
				}
			}
		}

		if fromMatch && toMatch {
			transition = &t
			break
		}
	}

	if transition == nil {
		return fmt.Errorf("%w: for places %v -> %v", workflow.ErrTransitionNotFound, currentPlaces, to)
	}

	// Apply the transition
	if err := wf.ApplyWithContext(ctx, to); err != nil {
		return fmt.Errorf("failed to apply transition: %w", err)
	}

	return saveTransitionHistory(wf, transition, currentPlaces, to, historyStore, ctx, overrideNotes, overrideActor, overrideCustomFields)
}

// ApplyTransitionByNameWithHistory applies a transition by name and saves history using metadata from the transition.
// This is the recommended approach for new code as it avoids ambiguity when multiple transitions lead to the same destination.
func ApplyTransitionByNameWithHistory(
	wf *workflow.Workflow,
	transitionName string,
	historyStore history.HistoryStore,
	ctx context.Context,
	overrideNotes string,
	overrideActor string,
	overrideCustomFields map[string]any,
) error {
	// Get current places before transition
	currentPlaces := wf.Marking().Places()
	if len(currentPlaces) == 0 {
		return fmt.Errorf("workflow has no current places")
	}

	// Find the transition by name
	definition := wf.Definition()
	var transition *workflow.Transition
	for _, t := range definition.AllTransitions() {
		if t.Name() == transitionName {
			transition = &t
			break
		}
	}

	if transition == nil {
		return fmt.Errorf("transition %s not found", transitionName)
	}

	// Apply the transition using the new transition-by-name API
	if err := wf.ApplyTransitionWithContext(ctx, transitionName); err != nil {
		return fmt.Errorf("failed to apply transition: %w", err)
	}

	// Get target places after transition
	toPlaces := transition.To()

	return saveTransitionHistory(wf, transition, currentPlaces, toPlaces, historyStore, ctx, overrideNotes, overrideActor, overrideCustomFields)
}

// saveTransitionHistory saves the transition history record (shared by both Apply methods)
func saveTransitionHistory(
	wf *workflow.Workflow,
	transition *workflow.Transition,
	currentPlaces []workflow.Place,
	toPlaces []workflow.Place,
	historyStore history.HistoryStore,
	ctx context.Context,
	overrideNotes string,
	overrideActor string,
	overrideCustomFields map[string]any,
) error {

	// Prepare history record
	// Store primary state (first place) for backward compatibility
	fromState := ""
	if len(currentPlaces) > 0 {
		fromState = string(currentPlaces[0]) // Use first place as primary state
	}
	toState := ""
	if len(toPlaces) > 0 {
		toState = string(toPlaces[0]) // Use first place as primary state
	}

	record := &history.TransitionRecord{
		WorkflowID: wf.Name(),
		FromState:  fromState,
		ToState:    toState,
		Transition: transition.Name(),
	}

	// Store full arrays in custom fields for parallel workflows
	// This preserves all places when multiple states are active simultaneously
	fromStates := make([]string, len(currentPlaces))
	for i, place := range currentPlaces {
		fromStates[i] = string(place)
	}
	toStates := make([]string, len(toPlaces))
	for i, place := range toPlaces {
		toStates[i] = string(place)
	}

	// Set CreatedAt from context or use current time
	if timestamp, ok := ctx.Value("timestamp").(time.Time); ok {
		record.CreatedAt = timestamp
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}

	// Get notes (override > transition metadata > empty)
	notes := overrideNotes
	if notes == "" {
		if notesMeta, ok := transition.Metadata("history_notes"); ok {
			if notesStr, ok := notesMeta.(string); ok {
				notes = notesStr
			}
		}
	}
	record.Notes = notes

	// Get actor (override > transition metadata > context > empty)
	actor := overrideActor
	if actor == "" {
		if actorMeta, ok := transition.Metadata("history_actor"); ok {
			if actorStr, ok := actorMeta.(string); ok {
				actor = actorStr
			}
		}
	}
	if actor == "" {
		// Try to get actor from context - check both string key and typed keys
		actor = getStringFromContext(ctx, "actor")
	}
	record.Actor = actor

	// Get custom fields (merge: override > transition metadata > context)
	customFields := make(map[string]any)

	// Store full state arrays for parallel workflows
	// This preserves all places when multiple states are active simultaneously
	// Always store arrays for consistency (even if single place)
	// This makes querying easier and provides complete history information
	customFields["from_states"] = fromStates
	customFields["to_states"] = toStates

	// Start with transition metadata
	if customFieldsMeta, ok := transition.Metadata("history_custom_fields"); ok {
		if cfMap, ok := customFieldsMeta.(map[string]any); ok {
			for k, v := range cfMap {
				// Resolve template variables
				resolved := ResolveTemplateValue(v, ctx, wf)
				customFields[k] = resolved
			}
		}
	}

	// Merge with context custom fields
	if ctxCustomFields, ok := ctx.Value("custom_fields").(map[string]any); ok {
		for k, v := range ctxCustomFields {
			// Resolve template variables
			resolved := ResolveTemplateValue(v, ctx, wf)
			customFields[k] = resolved
		}
	}

	// Override with provided custom fields (these are already resolved by caller if needed)
	for k, v := range overrideCustomFields {
		customFields[k] = v
	}

	if len(customFields) > 0 {
		record.CustomFields = customFields
	}

	// Save history
	if historyStore != nil {
		if err := historyStore.SaveTransition(ctx, record); err != nil {
			// Log error but don't fail the transition
			// In production, you might want to handle this differently
			return fmt.Errorf("failed to save history (transition succeeded): %w", err)
		}
	}

	return nil
}
