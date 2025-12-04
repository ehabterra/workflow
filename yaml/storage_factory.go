package yaml

import (
	"fmt"

	"github.com/ehabterra/workflow"
)

// StorageConfigBuilder is an interface for building storage instances from YAML configuration.
// Each storage implementation (SQLite, PostgreSQL, MongoDB, Redis, etc.) should implement this interface.
type StorageConfigBuilder interface {
	// Type returns the storage type identifier (e.g., "sqlite", "postgres", "mongodb", "redis")
	Type() string

	// Build creates a workflow.Storage instance from the raw YAML configuration data.
	// The config parameter contains the raw YAML data for the storage section.
	// Returns the Storage instance and any initialization schema/commands needed.
	Build(config map[string]interface{}) (workflow.Storage, *StorageInit, error)
}

// StorageInit contains information needed to initialize the storage (e.g., SQL schema).
type StorageInit struct {
	// Schema contains initialization SQL or commands (e.g., CREATE TABLE statements)
	Schema string

	// InitFunc is an optional function to run after schema initialization
	InitFunc func() error
}

// StorageFactory manages storage config builders and creates storage instances from YAML config.
type StorageFactory struct {
	builders map[string]StorageConfigBuilder
}

// NewStorageFactory creates a new storage factory.
func NewStorageFactory() *StorageFactory {
	return &StorageFactory{
		builders: make(map[string]StorageConfigBuilder),
	}
}

// Register registers a storage config builder.
func (f *StorageFactory) Register(builder StorageConfigBuilder) {
	if builder == nil {
		panic("storage builder cannot be nil")
	}
	f.builders[builder.Type()] = builder
}

// Build creates a storage instance from YAML configuration.
func (f *StorageFactory) Build(config *StorageConfig) (workflow.Storage, *StorageInit, error) {
	if config == nil {
		return nil, nil, fmt.Errorf("storage config is nil")
	}

	if config.Type == "" {
		return nil, nil, fmt.Errorf("storage type is required")
	}

	builder, ok := f.builders[config.Type]
	if !ok {
		return nil, nil, fmt.Errorf("unknown storage type: %s (registered types: %v)", config.Type, f.RegisteredTypes())
	}

	// Convert StorageConfig to raw map for builder
	rawConfig := config.ToMap()

	return builder.Build(rawConfig)
}

// RegisteredTypes returns a list of registered storage types.
func (f *StorageFactory) RegisteredTypes() []string {
	types := make([]string, 0, len(f.builders))
	for t := range f.builders {
		types = append(types, t)
	}
	return types
}

// DefaultFactory is the default global factory instance.
var DefaultFactory = NewStorageFactory()

// RegisterStorageBuilder registers a storage builder with the default factory.
func RegisterStorageBuilder(builder StorageConfigBuilder) {
	DefaultFactory.Register(builder)
}

// BuildStorage creates a storage instance using the default factory.
func BuildStorage(config *StorageConfig) (workflow.Storage, *StorageInit, error) {
	return DefaultFactory.Build(config)
}
