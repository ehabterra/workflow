package yaml

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config represents the complete YAML workflow configuration.
type Config struct {
	Workflow WorkflowConfig `yaml:"workflow"`
	Storage  *StorageConfig `yaml:"storage,omitempty"`
}

// WorkflowConfig defines the workflow structure.
type WorkflowConfig struct {
	Name           string               `yaml:"name"`
	InitialMarking InitialMarkingConfig `yaml:"initial_marking"`
	Metadata       map[string]any       `yaml:"metadata,omitempty"`
	Places         []PlaceConfig        `yaml:"places,omitempty"`
	Transitions    []TransitionConfig   `yaml:"transitions"`
}

// InitialMarkingConfig declares a workflow's starting marking. It accepts three
// YAML forms, so the simple case stays a one-liner and the Colored Petri Net case
// is a single coherent construct:
//
//	initial_marking: draft                 # one place, an uncolored presence token
//	initial_marking: [draft, needs_legal]  # several presence places
//	initial_marking:                       # data-carrying (colored) tokens per place
//	  pending:
//	    - {order_id: "001", amount: 100}
//	    - {order_id: "002", amount: 250}
//
// A place with a nil/empty token list gets a single uncolored presence token
// (the boolean case); a place with tokens is seeded with exactly those tokens.
type InitialMarkingConfig struct {
	// Places maps each initial place to its colored tokens (nil = presence token).
	Places map[string][]map[string]any
}

// UnmarshalYAML accepts a scalar place name, a sequence of place names, or a
// mapping of place name to token list.
func (im *InitialMarkingConfig) UnmarshalYAML(value *yaml.Node) error {
	im.Places = make(map[string][]map[string]any)
	switch value.Kind {
	case yaml.ScalarNode:
		var name string
		if err := value.Decode(&name); err != nil {
			return err
		}
		if name != "" { // a bare/null value leaves Places empty; Validate reports it
			im.Places[name] = nil
		}
	case yaml.SequenceNode:
		var names []string
		if err := value.Decode(&names); err != nil {
			return err
		}
		for _, n := range names {
			im.Places[n] = nil
		}
	case yaml.MappingNode:
		if err := value.Decode(&im.Places); err != nil {
			return err
		}
	default:
		return fmt.Errorf("initial_marking must be a place name, a list of place names, or a map of place to tokens")
	}
	return nil
}

// PlaceConfig defines a place with optional metadata.
type PlaceConfig struct {
	Name     string         `yaml:"name"`
	Metadata map[string]any `yaml:"metadata,omitempty"`
}

// TransitionConfig defines a transition with guards, metadata, and history fields.
type TransitionConfig struct {
	Name         string         `yaml:"name"`
	From         []string       `yaml:"from"`
	To           []string       `yaml:"to"`
	Guard        string         `yaml:"guard,omitempty"`         // Expression string
	Metadata     map[string]any `yaml:"metadata,omitempty"`      // Transition metadata
	Notes        string         `yaml:"notes,omitempty"`         // Default notes for history
	Actor        string         `yaml:"actor,omitempty"`         // Default actor for history
	CustomFields map[string]any `yaml:"custom_fields,omitempty"` // Default custom fields for history
}

// StorageConfig defines generic storage configuration.
// The actual structure is storage-type specific and handled by StorageConfigBuilder implementations.
// This uses a custom unmarshaler to capture all fields except "type" into the Config map.
type StorageConfig struct {
	Type string `yaml:"type"` // "sqlite", "postgres", "mongodb", "redis", etc.

	// Raw configuration data - structure depends on storage type
	// This allows each storage implementation to define its own config structure
	Config map[string]any `yaml:",inline"`
}

// UnmarshalYAML implements custom YAML unmarshaling to capture all fields.
func (sc *StorageConfig) UnmarshalYAML(unmarshal func(any) error) error {
	// First, unmarshal into a map to get all fields
	var raw map[string]any
	if err := unmarshal(&raw); err != nil {
		return err
	}

	// Extract type
	if t, ok := raw["type"].(string); ok {
		sc.Type = t
		delete(raw, "type")
	}

	// Everything else goes into Config
	sc.Config = raw
	return nil
}

// ToMap converts StorageConfig to a raw map for builder consumption.
func (sc *StorageConfig) ToMap() map[string]any {
	if sc == nil {
		return nil
	}

	result := make(map[string]any)
	result["type"] = sc.Type

	// Merge all config fields
	for k, v := range sc.Config {
		result[k] = v
	}

	return result
}

// LoadConfig loads a workflow configuration from a YAML file.
func LoadConfig(filename string) (*Config, error) {
	// The filename is a developer-supplied path to a trusted workflow definition,
	// not attacker-controlled input; reading it directly is intended.
	data, err := os.ReadFile(filename) // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	return LoadConfigFromBytes(data)
}

// LoadConfigFromBytes loads a workflow configuration from YAML bytes.
//
// Decoding is strict: any key that is not part of the schema causes an error
// (reported with its line number) rather than being silently ignored. This
// prevents typos and not-yet-implemented features from being accepted and then
// quietly dropped. Colored Petri Net tokens are declared with the supported
// initial_tokens key (see WorkflowConfig.InitialTokens).
func LoadConfigFromBytes(data []byte) (*Config, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var config Config
	if err := dec.Decode(&config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &config, nil
}

// Validate validates the configuration.
func (c *Config) Validate() error {
	if c.Workflow.Name == "" {
		return fmt.Errorf("workflow name is required")
	}

	if len(c.Workflow.InitialMarking.Places) == 0 {
		return fmt.Errorf("initial_marking is required")
	}

	if len(c.Workflow.Transitions) == 0 {
		return fmt.Errorf("at least one transition is required")
	}

	// Validate transitions
	placeSet := make(map[string]bool)

	// Collect places from places array
	for _, place := range c.Workflow.Places {
		placeSet[place.Name] = true
	}

	// Collect places from transitions if not explicitly defined
	if len(c.Workflow.Places) == 0 {
		for _, trans := range c.Workflow.Transitions {
			for _, from := range trans.From {
				placeSet[from] = true
			}
			for _, to := range trans.To {
				placeSet[to] = true
			}
		}
	}

	// Validate every initial_marking place is defined.
	for place := range c.Workflow.InitialMarking.Places {
		if !placeSet[place] {
			return fmt.Errorf("initial_marking references undefined place '%s'", place)
		}
	}

	// Validate transitions reference valid places
	for _, trans := range c.Workflow.Transitions {
		if trans.Name == "" {
			return fmt.Errorf("transition name cannot be empty")
		}

		if len(trans.From) == 0 {
			return fmt.Errorf("transition '%s' must have at least one 'from' place", trans.Name)
		}

		if len(trans.To) == 0 {
			return fmt.Errorf("transition '%s' must have at least one 'to' place", trans.Name)
		}

		for _, from := range trans.From {
			if !placeSet[from] {
				return fmt.Errorf("transition '%s' references undefined place '%s'", trans.Name, from)
			}
		}

		for _, to := range trans.To {
			if !placeSet[to] {
				return fmt.Errorf("transition '%s' references undefined place '%s'", trans.Name, to)
			}
		}
	}

	return nil
}
