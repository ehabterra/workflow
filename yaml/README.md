# YAML Configuration Support

The workflow package now supports loading workflow definitions from YAML configuration files, making it easy to define workflows declaratively without writing Go code.

## Features

- ✅ **Expression-based Guards**: Use [expr-lang/expr](https://github.com/expr-lang/expr) for powerful guard expressions
- ✅ **Metadata Support**: Add metadata at workflow, place, and transition levels
- ✅ **Context Injection**: Metadata automatically injected into workflow context
- ✅ **History Integration**: Configure default notes, actor, and custom fields for transitions
- ✅ **Storage Configuration**: Define storage settings directly in YAML

## Quick Start

### 1. Create a YAML Configuration

```yaml
workflow:
  name: blog_publishing
  initial_marking: draft
  
  metadata:
    title: "Blog Publishing Workflow"
    version: "1.0"
  
  transitions:
    - name: to_review
      from: [draft]
      to: [reviewed]
      guard: "workflow.Context('word_count') <= 500 and hasRole('author')"
      notes: "Submitted for review"
      actor: "author"
      custom_fields:
        submission_method: "web"
    
    - name: publish
      from: [reviewed]
      to: [published]
      guard: "hasRole('editor') or hasRole('admin')"
      notes: "Published by editor"
      actor: "editor"

storage:
  type: sqlite
  table: workflow_states
  custom_fields:
    title: "title TEXT"
    author: "author TEXT"
```

### 2. Load and Use

```go
package main

import (
    "github.com/ehabterra/workflow"
    "github.com/ehabterra/workflow/yaml"
)

func main() {
    // Load configuration
    config, err := yaml.LoadConfig("workflow.yaml")
    if err != nil {
        panic(err)
    }

    // Create loader
    loader := yaml.NewLoader()

    // Create workflow instance
    wf, err := loader.LoadWorkflow(config, "blog-post-1")
    if err != nil {
        panic(err)
    }

    // Set context for expression evaluation
    wf.SetContext("word_count", 450)
    wf.SetContext("roles", []string{"author"})

    // Apply transition (guard will be evaluated)
    err = wf.Apply([]workflow.Place{"reviewed"})
    if err != nil {
        // Transition blocked by guard
        panic(err)
    }
}
```

## Configuration Reference

### Workflow Configuration

```yaml
workflow:
  name: string              # Required: Workflow name
  initial_marking: <place | [places] | {place: [tokens]}>  # Required: starting marking
  metadata:                 # Optional: Workflow-level metadata (injected into context)
    key: value
  places:                   # Optional: Explicit place definitions
    - name: string
      metadata:             # Optional: Place-level metadata
        key: value
  transitions:             # Required: List of transitions
    - name: string
      from: [string]        # Required: Source places
      to: [string]          # Required: Target places
      guard: string         # Optional: Expression guard
      after: string         # Optional: Timeout duration, e.g. "72h" (host-driven timers)
      from_any: bool        # Optional: OR-input — enabled by ANY one marked input, consuming only it
      resets: [string]      # Optional: Places cleared when this transition fires (cancellation region)
      require:              # Optional: Dynamic-cardinality joins over input places
        - place: string     #   Required: which input place's tokens are counted
          count: string|int #   Required: how many are needed (expression, or a literal)
          where: string     #   Optional: per-token predicate; `token` is the token's data
          distinct: string  #   Optional: token field the counted tokens must be unique by
      effects:              # Optional: effects run INSIDE the state-save transaction, in order
        - name: string
          params:
            key: value
      after_commit:         # Optional: effects run only after the transaction commits (at-least-once)
        - name: string
          params:
            key: value
      metadata:             # Optional: Transition metadata
        key: value
      notes: string         # Optional: Default notes for history
      actor: string         # Optional: Default actor for history
      custom_fields:        # Optional: Default custom fields for history
        key: value
```

### Storage Configuration Format

```yaml
storage:
  type: string              # Required: "sqlite", "postgres", etc.
  table: string             # Optional: Table name (default: "workflow_states")
  id_column: string         # Optional: ID column name (default: "id")
  state_column: string      # Optional: State column name (default: "state")
  custom_fields:            # Optional: Custom field definitions
    field_name: "SQL_TYPE"
  database: string          # Optional: Database connection string
  options:                  # Optional: Additional options
    key: value
```

## Expression Guards

Guards use the [expr-lang/expr](https://github.com/expr-lang/expr) expression language. Expressions must return a boolean value.

### Available Variables

- `workflow` - The workflow instance
- `transition` - The transition name (string)
- `from` - Source places ([]Place)
- `to` - Target places ([]Place)
- All workflow context values are available directly by key

### Helper Functions

- `hasRole(role string) bool` - Check if workflow has a role
- `hasPermission(permission string) bool` - Check if workflow has a permission
- `in(value, list) bool` - Check if value is in list

### Examples

```yaml
# Simple role check
guard: "hasRole('admin')"

# Multiple conditions
guard: "workflow.Context('amount') > 1000 and hasRole('manager')"

# Complex logic
guard: "hasRole('editor') or (hasRole('author') and workflow.Context('word_count') <= 500)"

# Using context values
guard: "workflow.Context('status') == 'active' and workflow.Context('priority') > 5"
```

### Custom Environment Builder

You can provide a custom environment builder for expressions:

```go
loader := yaml.NewLoaderWithEnv(func(event workflow.Event) map[string]interface{} {
    env := make(map[string]interface{})
    
    // Add custom variables
    env["user"] = getUserFromContext(event)
    env["request"] = getRequestFromContext(event)
    
    // Add custom functions
    env["isBusinessHours"] = func() bool {
        // Your logic here
        return true
    }
    
    return env
})
```

## Metadata

### Workflow Metadata

Workflow-level metadata is automatically injected into the workflow context:

```yaml
workflow:
  metadata:
    title: "My Workflow"
    version: "1.0"
```

Access in code:

```go
title, _ := wf.Context("title")  // "My Workflow"
```

**⚠️ Reserved Keys:** None. All workflow metadata keys are available for your use.

### Place Metadata

Place metadata is stored in context with prefix `_place_metadata`:

```yaml
places:
  - name: draft
    metadata:
      max_words: 500
```

Access in code:

```go
placeMeta, _ := wf.Context("_place_metadata")
metaMap := placeMeta.(map[string]map[string]interface{})
maxWords := metaMap["draft"]["max_words"]
```

**⚠️ Reserved Keys:** None. All place metadata keys are available for your use.

### Transition Metadata

Transition metadata is available in expressions and can be accessed via the transition object:

```yaml
transitions:
  - name: publish
    metadata:
      priority: 0.5
      icon: "check-circle"
```

**⚠️ Reserved Keys in Transition Metadata:**

The following keys are reserved and used internally by the system. **Do not use these keys** in your transition `metadata` section:

- **`guard`** - Used internally to store the guard expression string (for diagram generation)
- **`history_notes`** - Used internally to store default notes (use `notes` field in YAML instead)
- **`history_actor`** - Used internally to store default actor (use `actor` field in YAML instead)
- **`history_custom_fields`** - Used internally to store default custom fields (use `custom_fields` field in YAML instead)

These reserved keys are automatically set from the `notes`, `actor`, and `custom_fields` fields in your YAML configuration. You should use the YAML fields directly, not set them in metadata.

## History Integration

Transitions can define default values for history records:

```yaml
transitions:
  - name: publish
    from: [reviewed]
    to: [published]
    notes: "Published by editor"
    actor: "editor"
    custom_fields:
      publish_time: "now()"  # Resolved to current timestamp
      ip_address: "{{request.ip}}"  # Resolved from context
```

**Template Resolution:**

Custom field values support template variables that are resolved at runtime:

- **`"now()"`** - Resolved to current timestamp in RFC3339 format
- **`"{{variable}}"`** - Resolved from context: `ctx.Value("variable")` or `wf.Context("variable")`
- **`"{{object.property}}"`** - Nested property access: `ctx.Value("object").(map[string]interface{})["property"]`
- **Mixed templates** - `"User: {{user}}"` resolves to `"User: alice"` if `user="alice"` in context

**⚠️ Reserved Keys in History Custom Fields:**

The following keys are automatically added to history custom fields and should not be used in your configuration:

- **`from_states`** - Automatically set to array of source places (e.g., `["draft"]` or `["qa_testing", "security_review"]` for parallel workflows)
- **`to_states`** - Automatically set to array of target places (e.g., `["reviewed"]` or `["approved"]`)

These keys preserve complete state information for parallel workflows where a workflow can be in multiple places simultaneously.

**Example with context:**

```go
ctx := context.WithValue(context.Background(), "request", map[string]interface{}{
    "ip": "192.168.1.1",
})
ctx = context.WithValue(ctx, "reason", "Insufficient information")

// Template variables in custom_fields will be resolved automatically
yaml.ApplyTransitionWithHistory(wf, []workflow.Place{"rejected"}, historyStore, ctx, "", "", nil)
```

**⚠️ Reserved Context Keys:**

The following context keys are used internally and should be avoided:

- **`custom_fields`** - Used internally to merge custom fields from context
- **`timestamp`** - Used internally to set custom CreatedAt timestamp for history records
- **`actor`** - Used as fallback if transition's `actor` field is empty (you can use this, but prefer setting `actor` in YAML)

Use the helper function to apply transitions with history:

```go
import "github.com/ehabterra/workflow/yaml"

// Use WithTemplateValue for template resolution
ctx := yaml.WithTemplateValue(context.Background(), "actor", "current-user")
ctx = yaml.WithTemplateValue(ctx, "request", map[string]interface{}{
    "ip": "192.168.1.1",
})

err := yaml.ApplyTransitionWithHistory(
    wf,
    []workflow.Place{"published"},
    historyStore,
    ctx,
    "",  // Override notes (empty = use default)
    "",  // Override actor (empty = use default or context)
    nil, // Override custom fields (nil = use defaults)
)
```

**Recommended:** Use `yaml.WithTemplateValue()` to add values to context for template resolution, as it ensures proper string key handling.

## Storage Configuration

Storage configuration uses a **factory pattern** with pluggable builders. This allows each storage type (SQLite, PostgreSQL, MongoDB, Redis, etc.) to define its own configuration structure.

### YAML Configuration

The storage config is generic - each storage type defines its own fields:

```yaml
# SQLite example
storage:
  type: sqlite
  table: workflow_states
  id_column: id
  state_column: state
  custom_fields:
    title: "title TEXT"
    author: "author TEXT"
  database: ":memory:"

# MongoDB example (would be handled by MongoDB builder)
# storage:
#   type: mongodb
#   collection: workflow_states
#   database: workflow_db
#   custom_fields:
#     - title
#     - author
#   connection:
#     uri: "mongodb://localhost:27017"
```

### Using Storage Factory

```go
import (
    "database/sql"
    "github.com/ehabterra/workflow/yaml"
    _ "github.com/mattn/go-sqlite3"
)

// Register SQLite builder (typically done at app startup)
dbProvider := func() (*sql.DB, error) {
    return sql.Open("sqlite3", "workflow.db")
}
builder := yaml.NewSQLiteStorageBuilder(dbProvider)
yaml.RegisterStorageBuilder(builder)

// Load config and build storage
config, _ := yaml.LoadConfig("workflow.yaml")
store, init, err := yaml.BuildStorage(config.Storage)
if err != nil {
    panic(err)
}

// Initialize storage (e.g., create tables)
if init.InitFunc != nil {
    if err := init.InitFunc(); err != nil {
        panic(err)
    }
}

// Use storage with workflow manager
registry := workflow.NewRegistry()
manager := workflow.NewManager(registry, store)
```

### Creating Custom Storage Builders

To add support for a new storage type, implement the `StorageConfigBuilder` interface:

```go
type MyStorageBuilder struct{}

func (b *MyStorageBuilder) Type() string {
    return "mystorage"
}

func (b *MyStorageBuilder) Build(config map[string]interface{}) (workflow.Storage, *yaml.StorageInit, error) {
    // Parse config map
    // Create storage instance
    // Return storage and initialization info
    return storage, &yaml.StorageInit{
        Schema: "CREATE TABLE...",
        InitFunc: func() error { /* init logic */ },
    }, nil
}

// Register it
yaml.RegisterStorageBuilder(&MyStorageBuilder{})
```

This design allows:

- ✅ **Extensibility**: Add new storage types without modifying core code
- ✅ **Type Safety**: Each builder validates its own config structure
- ✅ **Flexibility**: SQL and NoSQL can have completely different configs
- ✅ **Loose Coupling**: YAML loader doesn't need to know about specific storage types

## Complete Example

See `example.yaml` for a complete example configuration file.

## Testing

Run the tests:

```bash
go test ./yaml/...
```
