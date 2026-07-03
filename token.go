package workflow

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"time"
)

// TokenID uniquely identifies a token within a workflow instance.
type TokenID string

// TokenData is the data a token carries — its "color" in Colored Petri Net terms.
// It is an arbitrary set of key/value attributes (for example
// {"order_id": "001", "amount": 100}).
type TokenData map[string]any

// Token is a data-carrying marker that occupies a place in a Colored Petri Net.
//
// Tokens are value types: copy them freely. A token's data is only reachable
// through methods that return copies, and the mutating helpers (With, WithData)
// return a new token rather than changing the receiver, so a token in a marking
// cannot be altered by aliasing. Token identity is the ID, not the data — two
// tokens with identical data are still distinct tokens.
//
// Note: because a Token holds a map, Tokens are not comparable with ==; use Equal.
type Token struct {
	id   TokenID
	data TokenData
	// enteredAt records when the token was produced into its current place. It
	// is stamped by the engine during firing only when the workflow's definition
	// has timed transitions (see Transition.SetTimeoutAfter); for every other
	// workflow it stays the zero Time and never touches the wire format. It is
	// the reference point the Due API measures a transition's timeout against.
	enteredAt time.Time
}

// NewToken creates a token carrying a copy of data and a freshly generated,
// unique ID.
func NewToken(data TokenData) Token {
	return Token{id: newTokenID(), data: cloneTokenData(data)}
}

// NewTokenWithID creates a token with an explicit ID. It is useful in tests and
// when reconstructing tokens loaded from storage.
func NewTokenWithID(id TokenID, data TokenData) Token {
	return Token{id: id, data: cloneTokenData(data)}
}

// ID returns the token's unique identifier.
func (t Token) ID() TokenID { return t.id }

// EnteredAt returns when the token was produced into its current place, and
// whether that time is known. It is the zero Time (ok=false) for tokens in
// workflows without timed transitions, and for state persisted before timer
// support existed.
func (t Token) EnteredAt() (time.Time, bool) {
	return t.enteredAt, !t.enteredAt.IsZero()
}

// withEnteredAt returns a copy of the token stamped with the given entry time.
// It is engine-internal: enteredAt is not part of a token's identity or data.
func (t Token) withEnteredAt(at time.Time) Token {
	t.enteredAt = at
	return t
}

// Data returns a copy of the token's data. Mutating the result does not affect
// the token.
func (t Token) Data() TokenData { return cloneTokenData(t.data) }

// Get returns the value stored under key and whether it was present.
func (t Token) Get(key string) (any, bool) {
	v, ok := t.data[key]
	return v, ok
}

// With returns a copy of the token with key set to value. The receiver is left
// unchanged.
func (t Token) With(key string, value any) Token {
	d := cloneTokenData(t.data)
	if d == nil {
		d = TokenData{}
	}
	d[key] = value
	return Token{id: t.id, data: d, enteredAt: t.enteredAt}
}

// WithData returns a copy of the token whose data is replaced by a copy of the
// given data, keeping the same ID. The receiver is left unchanged.
func (t Token) WithData(data TokenData) Token {
	return Token{id: t.id, data: cloneTokenData(data), enteredAt: t.enteredAt}
}

// Equal reports whether two tokens have the same ID (token identity).
func (t Token) Equal(other Token) bool { return t.id == other.id }

// Validate reports whether the token is well-formed.
func (t Token) Validate() error {
	if t.id == "" {
		return fmt.Errorf("%w: empty ID", ErrInvalidToken)
	}
	return nil
}

// String implements fmt.Stringer.
func (t Token) String() string {
	return fmt.Sprintf("Token(%s)", t.id)
}

type tokenJSON struct {
	ID        TokenID    `json:"id"`
	Data      TokenData  `json:"data,omitempty"`
	EnteredAt *time.Time `json:"enteredAt,omitempty"`
}

// MarshalJSON implements json.Marshaler.
func (t Token) MarshalJSON() ([]byte, error) {
	tj := tokenJSON{ID: t.id, Data: t.data}
	if !t.enteredAt.IsZero() {
		at := t.enteredAt.UTC()
		tj.EnteredAt = &at
	}
	return json.Marshal(tj)
}

// UnmarshalJSON implements json.Unmarshaler.
func (t *Token) UnmarshalJSON(b []byte) error {
	var tj tokenJSON
	if err := json.Unmarshal(b, &tj); err != nil {
		return err
	}
	t.id = tj.ID
	t.data = tj.Data
	if tj.EnteredAt != nil {
		t.enteredAt = tj.EnteredAt.UTC()
	}
	return nil
}

// cloneTokenData returns a shallow copy of d (nil-safe). Nested reference values
// are shared; token data is expected to hold scalar/JSON-like values.
func cloneTokenData(d TokenData) TokenData {
	if d == nil {
		return nil
	}
	return maps.Clone(d)
}

// newTokenID returns a cryptographically-random 96-bit hex identifier.
func newTokenID() TokenID {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read only fails if the system source is unavailable, which
		// is not a recoverable condition for the caller.
		panic(fmt.Sprintf("workflow: failed to generate token ID: %v", err))
	}
	return TokenID(hex.EncodeToString(b[:]))
}
