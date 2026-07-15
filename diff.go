package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
)

// definitionShape is the compact structural summary the Manager persists
// alongside the fingerprint (defShapeKey): the sorted place names plus a
// short hash of each transition's structural record, keyed by transition
// name. The fingerprint alone can only say THAT the definition changed; the
// shape is what lets a later mismatch say WHAT changed (see DefinitionDiff),
// without persisting the whole definition.
type definitionShape struct {
	Places      []string          `json:"p"`
	Transitions map[string]string `json:"t"`
}

// shape returns the definition's structural summary. It covers exactly the
// fields Fingerprint hashes — name, inputs, outputs, guard string, resets,
// input mode per transition — so shapes are equal precisely when fingerprints
// are.
func (d *Definition) shape() definitionShape {
	places := placeStrings(d.Places)
	slices.Sort(places)
	transitions := make(map[string]string, len(d.Transitions))
	for i := range d.Transitions {
		t := &d.Transitions[i]
		sum := sha256.Sum256([]byte(transitionRecord(t)))
		transitions[t.Name()] = hex.EncodeToString(sum[:8])
	}
	return definitionShape{Places: places, Transitions: transitions}
}

// DefinitionDiff describes how a currently-supplied definition differs
// structurally from the one a persisted instance was last saved under. It is
// handed to the WithDefinitionMigration handler (via DefinitionMismatch) so
// approving an upgrade can be a POLICY over visible facts — "new transitions
// only, fine" — instead of blind trust in two opaque hashes.
//
// A renamed place appears as one place removed plus one added, with every
// transition that references it in TransitionsChanged — exactly the shape a
// reviewer needs to distinguish a rename from an addition.
type DefinitionDiff struct {
	PlacesAdded   []Place
	PlacesRemoved []Place

	// Transition names present only in the current (added) or only in the
	// stored (removed) definition, and names present in both whose structure
	// — inputs, outputs, guard, resets, input mode — differs (changed).
	TransitionsAdded   []string
	TransitionsRemoved []string
	TransitionsChanged []string
}

// Additive reports whether the change only ADDS structure: no place or
// transition was removed, and no existing transition was rewired. Additive
// changes cannot invalidate a persisted marking, so they are the class a
// migration handler can safely approve mechanically.
func (d *DefinitionDiff) Additive() bool {
	return len(d.PlacesRemoved) == 0 && len(d.TransitionsRemoved) == 0 && len(d.TransitionsChanged) == 0
}

// String renders a compact one-line summary for logs, e.g.
// "+places[archive] +transitions[archive_doc] ~transitions[approve]".
func (d *DefinitionDiff) String() string {
	var parts []string
	section := func(prefix, kind string, names []string) {
		if len(names) > 0 {
			parts = append(parts, fmt.Sprintf("%s%s[%s]", prefix, kind, strings.Join(names, " ")))
		}
	}
	section("+", "places", placeStrings(d.PlacesAdded))
	section("-", "places", placeStrings(d.PlacesRemoved))
	section("+", "transitions", d.TransitionsAdded)
	section("-", "transitions", d.TransitionsRemoved)
	section("~", "transitions", d.TransitionsChanged)
	if len(parts) == 0 {
		return "no structural change"
	}
	return strings.Join(parts, " ")
}

// diffShapes computes the structural difference from a stored shape to the
// current one. All result slices are sorted for deterministic output.
func diffShapes(stored, current definitionShape) *DefinitionDiff {
	diff := &DefinitionDiff{}

	storedPlaces := make(map[string]bool, len(stored.Places))
	for _, p := range stored.Places {
		storedPlaces[p] = true
	}
	currentPlaces := make(map[string]bool, len(current.Places))
	for _, p := range current.Places {
		currentPlaces[p] = true
	}
	for _, p := range current.Places {
		if !storedPlaces[p] {
			diff.PlacesAdded = append(diff.PlacesAdded, Place(p))
		}
	}
	for _, p := range stored.Places {
		if !currentPlaces[p] {
			diff.PlacesRemoved = append(diff.PlacesRemoved, Place(p))
		}
	}

	for name, hash := range current.Transitions {
		storedHash, ok := stored.Transitions[name]
		switch {
		case !ok:
			diff.TransitionsAdded = append(diff.TransitionsAdded, name)
		case storedHash != hash:
			diff.TransitionsChanged = append(diff.TransitionsChanged, name)
		}
	}
	for name := range stored.Transitions {
		if _, ok := current.Transitions[name]; !ok {
			diff.TransitionsRemoved = append(diff.TransitionsRemoved, name)
		}
	}

	slices.Sort(diff.PlacesAdded)
	slices.Sort(diff.PlacesRemoved)
	slices.Sort(diff.TransitionsAdded)
	slices.Sort(diff.TransitionsRemoved)
	slices.Sort(diff.TransitionsChanged)
	return diff
}
