package workflow

import (
	"encoding/json"
	"fmt"
)

// Marking represents the current state of a workflow
type Marking interface {
	// Places returns the current places
	Places() []Place
	// SetPlaces sets the current places
	SetPlaces(places []Place)
	// HasPlace checks if a place exists
	HasPlace(place Place) bool
	// AddPlace adds a place
	AddPlace(place Place) error
	// RemovePlace removes a place
	RemovePlace(place Place) error
}

// marking implements the Marking interface
type marking struct {
	places []Place
}

// NewMarking creates a new marking instance
func NewMarking(places []Place) Marking {
	// Create a copy of the places slice to prevent external modification
	placesCopy := make([]Place, len(places))
	copy(placesCopy, places)

	return &marking{
		places: placesCopy,
	}
}

// Places returns a copy of the current places in the marking
func (m *marking) Places() []Place {
	// Return a copy to prevent external modification
	placesCopy := make([]Place, len(m.places))
	copy(placesCopy, m.places)
	return placesCopy
}

// SetPlaces sets the places in the marking
func (m *marking) SetPlaces(places []Place) {
	// Create a copy of the places slice to prevent external modification
	placesCopy := make([]Place, len(places))
	copy(placesCopy, places)
	m.places = placesCopy
}

// HasPlace checks if a place exists
func (m *marking) HasPlace(place Place) bool {
	for _, s := range m.places {
		if s == place {
			return true
		}
	}
	return false
}

// AddPlace adds a place
func (m *marking) AddPlace(place Place) error {
	if m.HasPlace(place) {
		return nil
	}
	m.places = append(m.places, place)
	return nil
}

// RemovePlace removes a place
func (m *marking) RemovePlace(place Place) error {
	for i, s := range m.places {
		if s == place {
			m.places = append(m.places[:i], m.places[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrPlaceNotFound, place)
}

// MarshalJSON implements json.Marshaler
func (m *marking) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.places)
}

// UnmarshalJSON implements json.Unmarshaler
func (m *marking) UnmarshalJSON(data []byte) error {
	var places []Place
	if err := json.Unmarshal(data, &places); err != nil {
		return err
	}
	m.places = places
	return nil
}

// UnmarshalMarkingJSON unmarshals JSON data into a Marking interface
func UnmarshalMarkingJSON(data []byte) (Marking, error) {
	var places []Place
	if err := json.Unmarshal(data, &places); err != nil {
		return nil, err
	}
	return NewMarking(places), nil
}
