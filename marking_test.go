package workflow_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/ehabterra/workflow"
)

// sortPlaces returns a sorted copy of the given places so assertions can be
// made order-independent (Marking.Places() returns places sorted alphabetically).
func sortPlaces(places []workflow.Place) []workflow.Place {
	out := make([]workflow.Place, len(places))
	copy(out, places)
	slices.Sort(out)
	return out
}

func TestNewMarking(t *testing.T) {
	tests := []struct {
		name      string
		places    []workflow.Place
		wantCount int
	}{
		{
			name:      "empty marking",
			places:    []workflow.Place{},
			wantCount: 0,
		},
		{
			name:      "single place",
			places:    []workflow.Place{"start"},
			wantCount: 1,
		},
		{
			name:      "multiple places",
			places:    []workflow.Place{"start", "middle", "end"},
			wantCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marking := workflow.NewMarking(tt.places)
			got := marking.Places()
			if len(got) != tt.wantCount {
				t.Errorf("NewMarking() count = %v, want %v", len(got), tt.wantCount)
			}
		})
	}
}

func TestDefaultMarking_places(t *testing.T) {
	initialplaces := []workflow.Place{"start", "middle"}
	marking := workflow.NewMarking(initialplaces)
	got := marking.Places()
	if !reflect.DeepEqual(got, sortPlaces(initialplaces)) {
		t.Errorf("places() = %v, want %v", got, sortPlaces(initialplaces))
	}
}

func TestDefaultMarking_places_Copy(t *testing.T) {
	initialplaces := []workflow.Place{"start", "middle"}
	marking := workflow.NewMarking(initialplaces)
	places := marking.Places()
	if len(places) != len(initialplaces) {
		t.Errorf("places() count = %v, want %v", len(places), len(initialplaces))
	}

	// Verify that the returned slice is a copy
	originalplaces := marking.Places()
	originalplaces[0] = "modified"
	if marking.Places()[0] == "modified" {
		t.Error("places() returned slice is not a copy")
	}
}

func TestDefaultMarking_Setplaces(t *testing.T) {
	initialplaces := []workflow.Place{"start", "middle"}
	marking := workflow.NewMarking(initialplaces)

	// Test setting new places
	newplaces := []workflow.Place{"end"}
	marking.SetPlaces(newplaces)

	got := marking.Places()
	if !reflect.DeepEqual(got, newplaces) {
		t.Errorf("places() = %v, want %v", got, newplaces)
	}
}

func TestDefaultMarking_Setplaces_Empty(t *testing.T) {
	initialplaces := []workflow.Place{"start", "middle"}
	marking := workflow.NewMarking(initialplaces)

	// Test setting empty places
	marking.SetPlaces([]workflow.Place{})

	got := marking.Places()
	if len(got) != 0 {
		t.Errorf("places() = %v, want empty slice", got)
	}
}

func TestDefaultMarking_Setplaces_Nil(t *testing.T) {
	initialplaces := []workflow.Place{"start", "middle"}
	marking := workflow.NewMarking(initialplaces)

	// Test setting nil places
	marking.SetPlaces(nil)

	got := marking.Places()
	if len(got) != 0 {
		t.Errorf("places() = %v, want empty slice", got)
	}
}

func TestDefaultMarking_MarshalJSON(t *testing.T) {
	initialplaces := []workflow.Place{"start", "middle"}
	marking := workflow.NewMarking(initialplaces)

	// Test marshaling
	data, err := json.Marshal(marking)
	if err != nil {
		t.Errorf("MarshalJSON() error = %v", err)
	}

	// Test unmarshaling using the helper function
	newMarking, err := workflow.UnmarshalMarkingJSON(data)
	if err != nil {
		t.Errorf("UnmarshalMarkingJSON() error = %v", err)
	}

	got := newMarking.Places()
	if !reflect.DeepEqual(got, sortPlaces(initialplaces)) {
		t.Errorf("places() = %v, want %v", got, sortPlaces(initialplaces))
	}
}

func TestMarking_Hasplace(t *testing.T) {
	tests := []struct {
		name   string
		places []workflow.Place
		check  workflow.Place
		want   bool
	}{
		{
			name:   "place exists",
			places: []workflow.Place{"start", "middle"},
			check:  "start",
			want:   true,
		},
		{
			name:   "place does not exist",
			places: []workflow.Place{"start", "middle"},
			check:  "end",
			want:   false,
		},
		{
			name:   "empty marking",
			places: []workflow.Place{},
			check:  "start",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marking := workflow.NewMarking(tt.places)
			got := marking.HasPlace(tt.check)
			if got != tt.want {
				t.Errorf("Hasplace() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMarking_Addplace(t *testing.T) {
	tests := []struct {
		name    string
		initial []workflow.Place
		add     workflow.Place
		want    []workflow.Place
	}{
		{
			name:    "add to empty marking",
			initial: []workflow.Place{},
			add:     "start",
			want:    []workflow.Place{"start"},
		},
		{
			name:    "add new place",
			initial: []workflow.Place{"start"},
			add:     "middle",
			want:    []workflow.Place{"start", "middle"},
		},
		{
			name:    "add existing place",
			initial: []workflow.Place{"start"},
			add:     "start",
			want:    []workflow.Place{"start"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marking := workflow.NewMarking(tt.initial)
			err := marking.AddPlace(tt.add)
			if err != nil {
				t.Errorf("Addplace() error = %v", err)
			}
			got := marking.Places()
			if len(got) != len(tt.want) {
				t.Errorf("Addplace() count = %v, want %v", len(got), len(tt.want))
			}
		})
	}
}

func TestMarking_Removeplace(t *testing.T) {
	tests := []struct {
		name    string
		initial []workflow.Place
		remove  workflow.Place
		want    []workflow.Place
		wantErr bool
	}{
		{
			name:    "remove existing place",
			initial: []workflow.Place{"start", "middle"},
			remove:  "start",
			want:    []workflow.Place{"middle"},
			wantErr: false,
		},
		{
			name:    "remove non-existent place",
			initial: []workflow.Place{"start"},
			remove:  "middle",
			want:    []workflow.Place{"start"},
			wantErr: true,
		},
		{
			name:    "remove from empty marking",
			initial: []workflow.Place{},
			remove:  "start",
			want:    []workflow.Place{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marking := workflow.NewMarking(tt.initial)
			err := marking.RemovePlace(tt.remove)
			if (err != nil) != tt.wantErr {
				t.Errorf("Removeplace() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				got := marking.Places()
				if len(got) != len(tt.want) {
					t.Errorf("Removeplace() count = %v, want %v", len(got), len(tt.want))
				}
			}
		})
	}
}

func TestMarking_JSON(t *testing.T) {
	tests := []struct {
		name   string
		places []workflow.Place
	}{
		{
			name:   "empty marking",
			places: []workflow.Place{},
		},
		{
			name:   "single place",
			places: []workflow.Place{"start"},
		},
		{
			name:   "multiple places",
			places: []workflow.Place{"start", "middle", "end"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marking := workflow.NewMarking(tt.places)

			// Test marshaling
			data, err := json.Marshal(marking)
			if err != nil {
				t.Errorf("MarshalJSON() error = %v", err)
			}

			// Test unmarshaling using the helper function
			newMarking, err := workflow.UnmarshalMarkingJSON(data)
			if err != nil {
				t.Errorf("UnmarshalMarkingJSON() error = %v", err)
			}

			// Compare places
			got := newMarking.Places()
			if !reflect.DeepEqual(got, sortPlaces(tt.places)) {
				t.Errorf("JSON roundtrip = %v, want %v", got, sortPlaces(tt.places))
			}
		})
	}
}
