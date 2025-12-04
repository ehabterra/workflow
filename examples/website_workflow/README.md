# Website Workflow Example

This example demonstrates a website content approval workflow using the workflow package with **YAML configuration**. It includes a web interface for managing website content through various states: draft, review, approved, and published.

## Features

- **YAML-based workflow configuration** - Define workflows declaratively
- Web interface for workflow management
- SQLite storage for workflow persistence (configured via YAML)
- Workflow manager for lifecycle management
- Mermaid diagram visualization
- Transition history tracking with automatic notes/actor handling
- Form validation and error handling
- Clean and responsive UI with Tailwind CSS

## Workflow States

The workflow consists of the following states:

1. **Draft**: Initial state for new content
2. **Review**: Content is under review
3. **Approved**: Content has been approved
4. **Published**: Content is live on the website

## Transitions

- `submit_for_review`: Draft → Review
- `request_changes`: Review → Draft (rejection)
- `approve`: Review → Approved
- `publish`: Approved → Published

## YAML Configuration

The workflow is defined in `workflow.yaml`, which includes:

- **Workflow definition**: Places, transitions, and metadata
- **Storage configuration**: SQLite database settings
- **History defaults**: Default notes and actor for each transition

### Example Configuration

```yaml
workflow:
  name: website_content_approval
  initial_place: draft
  places:
    - name: draft
    - name: review
    - name: approved
    - name: published
  transitions:
    - name: submit_for_review
      from: [draft]
      to: [review]
      notes: "Submitted for review"
      actor: "author"
    - name: publish
      from: [approved]
      to: [published]
      notes: "Published to website"
      actor: "editor"
      custom_fields:
        published_at: "now()"  # Resolved to current timestamp

storage:
  type: sqlite
  table: workflows
  database: "./website_workflow.db"
  custom_fields:
    title: "title TEXT"
    content: "content TEXT"
  # History store configuration (optional)
  history:
    table: transition_history
    # custom_fields:
    #   ip_address: "ip_address TEXT"
```

**Note**: The `notes` and `actor` fields in YAML are optional defaults. They can be overridden at runtime via form input or context values. This provides flexibility:

- Use YAML defaults for documentation and consistency
- Override at runtime for user-specific values

## Prerequisites

- Go 1.16 or later
- SQLite3

## Installation

1. Clone the repository:

    ```bash
    git clone https://github.com/ehabterra/workflow.git
    cd workflow/examples/website_workflow
    ```

2. Install dependencies:

    ```bash
    go mod download
    ```

## Running the Example

1. Start the server:

    ```bash
    go run main.go
    ```

2. Open your browser and navigate to:

    ```url
    http://localhost:8080
    ```

## Usage

1. **Creating Content**:
   - Click "Create New Content"
   - Fill in the content details
   - Submit to create a new workflow instance

2. **Managing Content**:
   - View all content items on the main page
   - Click on a content item to view details
   - Use the available transitions to move content through the workflow
   - View the workflow diagram to understand the process

3. **Viewing History**:
   - Each content item shows its transition history
   - History includes timestamps and transition details

## Implementation Details

### YAML Loader

The example loads the workflow configuration from YAML:

```go
// Load YAML configuration
config, err := yaml.LoadConfig("workflow.yaml")
if err != nil {
    log.Fatalf("Failed to load YAML config: %v", err)
}

// Setup storage and history from YAML config (fully automated)
// This handles: storage builder registration, storage initialization, and history store setup
storageResult, err := yaml.SetupStorageFromConfig(config.Storage)
if err != nil {
    log.Fatalf("Failed to setup storage from config: %v", err)
}

workflowStore := storageResult.Storage
db := storageResult.DB
historyStore := storageResult.HistoryStore  // nil if not configured in YAML

// Create loader and load workflow definition
loader := yaml.NewLoader()
workflowDef, err := loader.LoadDefinition(config)
if err != nil {
    log.Fatalf("Failed to load workflow definition: %v", err)
}
```

**Key Benefits:**

- **Fully Automated**: `SetupStorageFromConfig` handles everything automatically:
  - Storage builder registration
  - Database connection management
  - Storage schema initialization
  - History store setup (if configured in YAML)
- **No Hard Dependencies**: The code doesn't explicitly reference SQLite, database paths, or initialization logic
- **Single Source of Truth**: All configuration (storage, database, history) is in `workflow.yaml`
- **Zero Boilerplate**: No manual database connection, schema initialization, or history store setup needed

### Applying Transitions with History

The example uses `ApplyTransitionWithHistory` helper which automatically handles history records using YAML defaults or runtime overrides:

```go
// Apply transition with history
err = yaml.ApplyTransitionWithHistory(
    wf,
    targetTransition.To(),
    historyStore,
    ctx,
    notes,  // Override notes (empty = use YAML default)
    actor,  // Override actor (empty = use YAML default or context)
    nil,    // Custom fields override
)
```

**Priority order for notes/actor:**

1. Runtime override (function parameter)
2. YAML default (from transition metadata)
3. Context value (for actor only)
4. Empty string

### Storage and History

Storage and history are fully configured via YAML. The `SetupStorageFromConfig` function automatically:

- Registers the appropriate storage builder
- Opens the database connection
- Initializes storage schema
- Sets up history store (if `history` section is present in YAML)

All database connections, schema initialization, and history setup are handled automatically - no manual code needed!

### Web Interface

The web interface is built using:

- Standard Go `html/template` for templating
- Tailwind CSS for styling
- Mermaid.js for workflow visualization

## Project Structure

```sh
website_workflow/
├── main.go           # Main application code
├── workflow.yaml     # YAML workflow configuration
├── templates/        # HTML templates
│   ├── home.html     # Main page template
│   ├── workflow.html # Workflow page template
│   ├── workflow-form.html # Form template
│   └── diagram.html  # Diagram page template
├── website_workflow.db # SQLite database
└── README.md         # This file
```

## Testing

1. Create a new content item
2. Try different transitions
3. Verify the state is persisted
4. Check the transition history
5. View the workflow diagram

## Contributing

Feel free to submit issues and enhancement requests!
