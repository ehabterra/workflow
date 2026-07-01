package workflow

import (
	"fmt"
)

// Transition represents a transition between places in the workflow
type Transition struct {
	name        string
	from        []Place
	to          []Place
	metadata    map[string]any
	constraints []Constraint
}

// Constraint represents a validation constraint for a transition
type Constraint interface {
	Validate(Event) error
}

// NewTransition creates a new transition
func NewTransition(name string, from []Place, to []Place) (*Transition, error) {
	if name == "" {
		return nil, fmt.Errorf("transition name cannot be empty")
	}

	if len(from) == 0 {
		return nil, fmt.Errorf("transition must have at least one 'from' place")
	}

	if len(to) == 0 {
		return nil, fmt.Errorf("transition must have at least one 'to' place")
	}

	// Check for duplicate places in from
	fromSet := make(map[Place]bool)
	for _, place := range from {
		if fromSet[place] {
			return nil, fmt.Errorf("duplicate 'from' place: %s", place)
		}
		fromSet[place] = true
	}

	// Check for duplicate places in to
	toSet := make(map[Place]bool)
	for _, place := range to {
		if toSet[place] {
			return nil, fmt.Errorf("duplicate 'to' place: %s", place)
		}
		toSet[place] = true
	}

	return &Transition{
		name:        name,
		from:        from,
		to:          to,
		metadata:    make(map[string]any),
		constraints: make([]Constraint, 0),
	}, nil
}

// Name returns the transition name
func (t *Transition) Name() string {
	return t.name
}

// From returns the source places of the transition
func (t *Transition) From() []Place {
	// Return a copy to prevent external modification
	fromCopy := make([]Place, len(t.from))
	copy(fromCopy, t.from)
	return fromCopy
}

// To returns the target places of the transition
func (t *Transition) To() []Place {
	// Return a copy to prevent external modification
	toCopy := make([]Place, len(t.to))
	copy(toCopy, t.to)
	return toCopy
}

// AddConstraint adds a constraint to the transition
func (t *Transition) AddConstraint(constraint Constraint) {
	t.constraints = append(t.constraints, constraint)
}

// SetMetadata sets metadata for the transition
func (t *Transition) SetMetadata(key string, value any) {
	t.metadata[key] = value
}

// Metadata returns the value for the given key from the transition metadata
func (t *Transition) Metadata(key string) (any, bool) {
	value, ok := t.metadata[key]
	return value, ok
}

// validate validates the transition against all constraints (internal method)
func (t *Transition) validate(event Event) error {
	for _, constraint := range t.constraints {
		if err := constraint.Validate(event); err != nil {
			return err
		}
	}
	return nil
}

// MustNewTransition is a helper that creates a new transition and panics on error.
// This is useful for defining transitions in a declarative way.
func MustNewTransition(name string, from []Place, to []Place) *Transition {
	t, err := NewTransition(name, from, to)
	if err != nil {
		panic(err)
	}
	return t
}
