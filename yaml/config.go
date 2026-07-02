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
	Name         string             `yaml:"name"`
	InitialPlace string             `yaml:"initial_place"`
	Metadata     map[string]any     `yaml:"metadata,omitempty"`
	Places       []PlaceConfig      `yaml:"places,omitempty"`
	Transitions  []TransitionConfig `yaml:"transitions"`
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
// prevents typos and not-yet-implemented features (for example the planned
// CPN token keys) from being accepted and then quietly dropped.
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

	if c.Workflow.InitialPlace == "" {
		return fmt.Errorf("initial_place is required")
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

	// Validate initial place exists
	if !placeSet[c.Workflow.InitialPlace] {
		return fmt.Errorf("initial_place '%s' is not defined in places", c.Workflow.InitialPlace)
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
