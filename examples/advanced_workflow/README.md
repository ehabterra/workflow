# Advanced Workflow Example

This example demonstrates **all advanced features** of the workflow system through a comprehensive project management workflow.

```mermaid
stateDiagram-v2
    direction TB
    classDef currentPlace font-weight:bold,stroke-width:4px
    planning
    budget_review
    budget_approved
    design_review
    development
    qa_testing
    security_review
    legal_review
    qa_complete
    security_complete
    legal_complete
    approved
    deployed
    rejected
    planning --> budget_review : <span class="transition-label" data-transition-name="submit_budget" data-guard="hasRole(&#39;project_manager&#39;)" data-guard-simplified="role: project_manager">submit_budget</span>
    budget_review --> budget_approved : <span class="transition-label" data-transition-name="approve_budget" data-guard="hasRole(&#39;project_manager&#39;) or hasRole(&#39;admin&#39;) or hasRole(&#39;finance_manager&#39;)" data-guard-simplified="role: project_manager or role: admin or role: finance_manager">approve_budget</span>
    budget_review --> planning : <span class="transition-label" data-transition-name="reject_budget" data-guard="hasRole(&#39;project_manager&#39;) or hasRole(&#39;admin&#39;) or hasRole(&#39;finance_manager&#39;)" data-guard-simplified="role: project_manager or role: admin or role: finance_manager">reject_budget</span>
    budget_approved --> development : <span class="transition-label" data-transition-name="start_development" data-guard="hasRole(&#39;project_manager&#39;) and workflow.Context(&#39;team_size&#39;) &lt;= 10" data-guard-simplified="role: project_manager and team_size &lt;= 10">start_development</span>
    development --> design_review : <span class="transition-label" data-transition-name="submit_design_review" data-guard="(hasRole(&#39;developer&#39;) or hasRole(&#39;team_lead&#39;)) and workflow.Context(&#39;requires_legal&#39;) != true" data-guard-simplified="(role: developer or role: team_lead) and requires_legal != true">submit_design_review</span>
    state submit_design_and_legal_review_fork <<fork>>
    development --> submit_design_and_legal_review_fork : <span class="transition-label" data-transition-name="submit_design_and_legal_review" data-guard="(hasRole(&#39;developer&#39;) or hasRole(&#39;team_lead&#39;)) and workflow.Context(&#39;requires_legal&#39;) == true" data-guard-simplified="(role: developer or role: team_lead) and requires_legal == true">submit_design_and_legal_review</span>
    submit_design_and_legal_review_fork --> design_review
    submit_design_and_legal_review_fork --> legal_review
    state approve_design_fork <<fork>>
    design_review --> approve_design_fork : <span class="transition-label" data-transition-name="approve_design" data-guard="(hasRole(&#39;design_lead&#39;) or hasRole(&#39;designer&#39;)) and (inPlace(&#39;legal_complete&#39;) or workflow.Context(&#39;requires_legal&#39;) != true)" data-guard-simplified="(role: design_lead or role: designer) and (place: legal_complete or requires_legal != true)">approve_design</span>
    approve_design_fork --> qa_testing
    approve_design_fork --> security_review
    design_review --> development : <span class="transition-label" data-transition-name="reject_design" data-guard="hasRole(&#39;design_lead&#39;) or hasRole(&#39;designer&#39;)" data-guard-simplified="role: design_lead or role: designer">reject_design</span>
    qa_testing --> qa_complete : <span class="transition-label" data-transition-name="complete_qa" data-guard="(hasRole(&#39;qa_lead&#39;) or hasRole(&#39;tester&#39;)) and workflow.Context(&#39;test_coverage&#39;) &gt;= 80" data-guard-simplified="(role: qa_lead or role: tester) and test_coverage &gt;= 80">complete_qa</span>
    security_review --> security_complete : <span class="transition-label" data-transition-name="complete_security_review" data-guard="hasRole(&#39;security_lead&#39;) or hasRole(&#39;security_analyst&#39;)" data-guard-simplified="role: security_lead or role: security_analyst">complete_security_review</span>
    legal_review --> legal_complete : <span class="transition-label" data-transition-name="complete_legal_review" data-guard="hasRole(&#39;legal_advisor&#39;) or hasRole(&#39;lawyer&#39;)" data-guard-simplified="role: legal_advisor or role: lawyer">complete_legal_review</span>
    state approve_standard_reviews_join <<join>>
    qa_complete --> approve_standard_reviews_join : <span class="transition-label" data-transition-name="approve_standard_reviews" data-guard="(hasRole(&#39;project_manager&#39;) or hasRole(&#39;admin&#39;)) and workflow.Context(&#39;requires_legal&#39;) != true" data-guard-simplified="(role: project_manager or role: admin) and requires_legal != true">approve_standard_reviews</span>
    security_complete --> approve_standard_reviews_join : <span class="transition-label" data-transition-name="approve_standard_reviews" data-guard="(hasRole(&#39;project_manager&#39;) or hasRole(&#39;admin&#39;)) and workflow.Context(&#39;requires_legal&#39;) != true" data-guard-simplified="(role: project_manager or role: admin) and requires_legal != true">approve_standard_reviews</span>
    approve_standard_reviews_join --> approved
    state approve_all_reviews_including_legal_join <<join>>
    qa_complete --> approve_all_reviews_including_legal_join : <span class="transition-label" data-transition-name="approve_all_reviews_including_legal" data-guard="(hasRole(&#39;project_manager&#39;) or hasRole(&#39;admin&#39;)) and workflow.Context(&#39;requires_legal&#39;) == true" data-guard-simplified="(role: project_manager or role: admin) and requires_legal == true">approve_all_reviews_including_legal</span>
    security_complete --> approve_all_reviews_including_legal_join : <span class="transition-label" data-transition-name="approve_all_reviews_including_legal" data-guard="(hasRole(&#39;project_manager&#39;) or hasRole(&#39;admin&#39;)) and workflow.Context(&#39;requires_legal&#39;) == true" data-guard-simplified="(role: project_manager or role: admin) and requires_legal == true">approve_all_reviews_including_legal</span>
    legal_complete --> approve_all_reviews_including_legal_join : <span class="transition-label" data-transition-name="approve_all_reviews_including_legal" data-guard="(hasRole(&#39;project_manager&#39;) or hasRole(&#39;admin&#39;)) and workflow.Context(&#39;requires_legal&#39;) == true" data-guard-simplified="(role: project_manager or role: admin) and requires_legal == true">approve_all_reviews_including_legal</span>
    approve_all_reviews_including_legal_join --> approved
    approved --> deployment_ready : <span class="transition-label" data-transition-name="mark_deployment_ready" data-guard="hasRole(&#39;project_manager&#39;) or hasRole(&#39;admin&#39;)" data-guard-simplified="role: project_manager or role: admin">mark_deployment_ready</span>
    deployment_ready --> deployed : <span class="transition-label" data-transition-name="deploy" data-guard="hasRole(&#39;devops&#39;) or hasRole(&#39;admin&#39;)" data-guard-simplified="role: devops or role: admin">deploy</span>
    design_review --> rejected : <span class="transition-label" data-transition-name="reject_design_issues" data-guard="hasRole(&#39;admin&#39;) or hasRole(&#39;project_manager&#39;)" data-guard-simplified="role: admin or role: project_manager">reject_design_issues</span>
    qa_testing --> rejected : <span class="transition-label" data-transition-name="reject_qa_failures" data-guard="hasRole(&#39;admin&#39;) or hasRole(&#39;project_manager&#39;)" data-guard-simplified="role: admin or role: project_manager">reject_qa_failures</span>
    security_review --> rejected : <span class="transition-label" data-transition-name="reject_security_vulnerabilities" data-guard="hasRole(&#39;admin&#39;) or hasRole(&#39;project_manager&#39;)" data-guard-simplified="role: admin or role: project_manager">reject_security_vulnerabilities</span>
    legal_review --> rejected : <span class="transition-label" data-transition-name="reject_legal_compliance" data-guard="hasRole(&#39;admin&#39;) or hasRole(&#39;project_manager&#39;)" data-guard-simplified="role: admin or role: project_manager">reject_legal_compliance</span>
    qa_complete --> rejected : <span class="transition-label" data-transition-name="reject_after_qa_complete" data-guard="hasRole(&#39;admin&#39;) or hasRole(&#39;project_manager&#39;)" data-guard-simplified="role: admin or role: project_manager">reject_after_qa_complete</span>
    security_complete --> rejected : <span class="transition-label" data-transition-name="reject_after_security_complete" data-guard="hasRole(&#39;admin&#39;) or hasRole(&#39;project_manager&#39;)" data-guard-simplified="role: admin or role: project_manager">reject_after_security_complete</span>
    legal_complete --> rejected : <span class="transition-label" data-transition-name="reject_after_legal_complete" data-guard="hasRole(&#39;admin&#39;) or hasRole(&#39;project_manager&#39;)" data-guard-simplified="role: admin or role: project_manager">reject_after_legal_complete</span>
    rejected --> planning : <span class="transition-label" data-transition-name="restart_from_rejected" data-guard="hasRole(&#39;project_manager&#39;)" data-guard-simplified="role: project_manager">restart_from_rejected</span>

    %% Current places
    class planning currentPlace

    %% Initial place
    [*] --> planning
```

## Features Demonstrated

### 1. Multi-State Workflows

The workflow supports being in **multiple states simultaneously**. For example:

- `approve_design` transition moves from `design_review` to both `qa_testing` and `security_review` in parallel
- The workflow can be in multiple places at once, enabling parallel processing paths

**Example in YAML:**

```yaml
- name: approve_design
  from: [design_review]
  to: [qa_testing, security_review]  # Fork: moves to multiple states
```

### 2. Custom Fields in Storage

Custom fields are stored in the database alongside workflow state:

- Project information: `title`, `description`, `project_type`, `priority`, `budget`
- Development fields: `design_version`, `test_coverage`, `bugs_found`
- Deployment fields: `deployment_version`, `deployment_url`

**Example in YAML:**

```yaml
storage:
  custom_fields:
    title: "title TEXT NOT NULL"
    budget: "budget REAL"
    team_size: "team_size INTEGER DEFAULT 0"
```

### 3. Custom Fields in History

History records can store custom fields specific to each transition:

- Design review: `design_score`, `reviewer_notes`
- QA testing: `test_coverage`, `bugs_found`, `qa_report_url`
- Security review: `security_score`, `vulnerabilities_found`
- Deployment: `deployment_version`, `deployment_environment`, `rollback_plan`

**Example in YAML:**

```yaml
history:
  custom_fields:
    design_score: "design_score INTEGER"
    test_coverage: "test_coverage INTEGER"
    deployment_version: "deployment_version TEXT"
```

### 4. Metadata at All Levels

#### Workflow-Level Metadata

```yaml
workflow:
  metadata:
    title: "Advanced Project Management Workflow"
    version: "2.0"
    category: "project_management"
```

#### Place-Level Metadata

```yaml
places:
  - name: design_review
    metadata:
      description: "Design team is reviewing"
      requires_approval: true
      team: "design"
      color: "#9B59B6"
```

#### Transition-Level Metadata

```yaml
transitions:
  - name: start_development
    metadata:
      icon: "rocket"
      label: "Start Development"
      priority: "high"
      estimated_duration: "2 weeks"
```

### 5. Guard Expressions

Guard expressions control when transitions are allowed using the [expr-lang/expr](https://github.com/expr-lang/expr) language:

**Role-based guards:**

```yaml
guard: "hasRole('project_manager')"
guard: "hasRole('qa_lead') or hasRole('tester')"
```

**Context-based guards:**

```yaml
guard: "workflow.Context('team_size') <= 10"
guard: "workflow.Context('test_coverage') >= 80"
```

**Complex guards:**

```yaml
guard: "hasRole('project_manager') and workflow.Context('team_size') <= 10"
```

### 6. Template Values

Custom fields can use template values that are resolved at runtime:

**Built-in functions:**

```yaml
custom_fields:
  started_at: "now()"  # Resolved to current timestamp (RFC3339)
```

**Context variables:**

```yaml
custom_fields:
  development_methodology: "{{methodology}}"  # From workflow context
  team_lead: "{{team_lead}}"                 # From workflow context
  user: "{{user}}"                            # From request context
```

**Nested properties:**

```yaml
custom_fields:
  ip_address: "{{request.ip}}"  # From context object
```

**Template resolution order:**

1. `now()` → current timestamp
2. `{{variable}}` → from context: `ctx.Value("variable")` or `wf.Context("variable")`
3. `{{object.property}}` → nested property access

### 7. Parallel Transitions

The workflow demonstrates parallel processing:

- After design approval, QA testing and security review happen in parallel
- Both must complete before the project can be approved
- Legal review is optional and can run in parallel with other reviews

### 8. Complex Workflow Paths

- **Forks**: Single state → multiple states (parallel processing)
- **Joins**: Multiple states → single state (synchronization)
- **Loops**: Rejected projects can restart from planning
- **Conditional paths**: Legal review only if `requires_legal == true`

## Running the Example

### Prerequisites

- Go 1.21 or later
- SQLite3 (included with Go SQLite driver)

### Setup

1. **Navigate to the example directory:**

   ```bash
   cd examples/advanced_workflow
   ```

2. **Install dependencies:**

   ```bash
   go mod download
   ```

3. **Run the server:**

   ```bash
   go run main.go
   ```

4. **Open in browser:**

   ```text
   http://localhost:8080
   ```

### Usage

1. **Create a new project:**

   - Click "New Project"
   - Fill in project details
   - Submit budget for approval (moves to `budget_review` state)
   - Approve budget (moves to `budget_approved` state, required for `start_development`)
   - Set `team_size` ≤ 10 to satisfy guard expression

2. **Navigate through the workflow:**

   - Start development (requires workflow to be in `budget_approved` state and `team_size <= 10`)
   - Submit for design review
   - Approve design (creates parallel paths to QA and Security)
   - Complete QA testing (requires `test_coverage >= 80`)
   - Complete security review
   - Deploy to production

3. **View metadata:**

   - Click "Metadata" link to see all workflow, place, and transition metadata

4. **View diagram:**

   - Click "Diagram" link to see the complete workflow visualization

5. **Test guard expressions:**

   - Try transitions without meeting guard conditions
   - See guard error messages displayed in the UI

## YAML Configuration Structure

### Complete Workflow Definition

```yaml
workflow:
  name: advanced_project_management
  initial_place: planning
  metadata:
    # Workflow-level metadata
  places:
    - name: planning
      metadata:
        # Place-level metadata
  transitions:
    - name: start_development
      from: [planning]
      to: [development]
      guard: "hasRole('project_manager')"
      metadata:
        # Transition-level metadata
      notes: "Default notes for history"
      actor: "Default actor for history"
      custom_fields:
        # Custom fields for history records
        started_at: "now()"
        methodology: "{{methodology}}"

storage:
  type: sqlite
  table: projects
  custom_fields:
    # Custom fields for storage
  history:
    table: project_history
    custom_fields:
      # Custom fields for history
```

## Key Concepts

### Multi-State Workflows

- A workflow can be in multiple places simultaneously
- Transitions can move from/to multiple places
- Useful for parallel processing and synchronization

### Custom Fields

- **Storage custom fields**: Stored in the main workflow table
- **History custom fields**: Stored in the history table
- Both support any SQL column type
- History custom fields can use template values

### Metadata

- Metadata is application-specific data that doesn't affect workflow logic
- Available at workflow, place, and transition levels
- Useful for UI hints, icons, descriptions, etc.
- Accessible via the YAML config and workflow API

### Guard Expressions

- Use expr-lang/expr for powerful conditional logic
- Access workflow context: `workflow.Context('key')`
- Use helper functions: `hasRole('role')`, `hasPermission('perm')`
- Must return boolean (true = allowed, false = blocked)

### Template Values

- Resolved at transition time
- Support `now()` for timestamps
- Support `{{variable}}` for context values
- Support `{{object.property}}` for nested access
- Used in transition `custom_fields` for history records

## Example Workflow Paths

### Standard Path

1. `planning` → `budget_review` (submit budget for approval)
2. `budget_review` → `budget_approved` (approve budget)
3. `budget_approved` → `development` (start development)
4. `development` → `design_review`
5. `design_review` → `qa_testing` + `security_review` (parallel fork)
6. `qa_testing` → `qa_complete` (join preparation)
7. `security_review` → `security_complete` (join preparation)
8. `qa_complete` + `security_complete` → `approved` (join)
9. `approved` → `deployed`

### With Legal Review

1. `planning` → `development`
2. `development` → `legal_review` (if `requires_legal == true`)
3. `legal_review` → `approved`
4. `approved` → `deployed`

### Rejection Path

1. Any review state → `rejected`
2. `rejected` → `planning` (restart)

## Database Schema

### Projects Table (Storage)

```sql
CREATE TABLE projects (
  id TEXT PRIMARY KEY,
  state TEXT NOT NULL,
  title TEXT NOT NULL,
  description TEXT,
  project_type TEXT,
  priority TEXT,
  budget REAL,
  team_size INTEGER DEFAULT 0,
  -- ... other custom fields
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### Project History Table

```sql
CREATE TABLE project_history (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  workflow_id TEXT NOT NULL,
  from_state TEXT NOT NULL,
  to_state TEXT NOT NULL,
  transition TEXT NOT NULL,
  notes TEXT,
  actor TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  -- Custom fields from history config
  design_score INTEGER,
  test_coverage INTEGER,
  deployment_version TEXT,
  -- ... other custom fields
);
```

## Advanced Features Summary

| Feature | Description | Example |
|---------|-------------|---------|
| **Multi-States** | Workflow in multiple places | `to: [qa_testing, security_review]` |
| **Storage Custom Fields** | Store data with workflow | `title`, `budget`, `team_size` |
| **History Custom Fields** | Store transition-specific data | `design_score`, `test_coverage` |
| **Workflow Metadata** | Application-level metadata | `version`, `category` |
| **Place Metadata** | State-level metadata | `team`, `color`, `icon` |
| **Transition Metadata** | Action-level metadata | `icon`, `label`, `priority` |
| **Guard Expressions** | Conditional transitions | `hasRole('admin') and workflow.Context('approved')` |
| **Template Values** | Dynamic field resolution | `now()`, `{{variable}}`, `{{object.property}}` |
| **Parallel Paths** | Fork/join patterns | Multiple states simultaneously |
| **Conditional Paths** | Optional transitions | Legal review only if needed |

## Troubleshooting

### Guard Expression Errors

- Check that required context values are set
- Verify role assignments: `wf.SetContext("roles", []string{"project_manager"})`
- Test expressions in the UI - errors are displayed

### Template Value Resolution

- Ensure values are in context: `ctx.Value("variable")` or `wf.Context("variable")`
- Use `yaml.WithTemplateValue()` to add values to context
- Check nested access: `{{request.ip}}` requires `request` object in context

### Multi-State Issues

- Verify transitions allow multiple target places
- Check that all required states are reached before joins
- Use workflow marking to see current states

## Next Steps

- Explore the YAML configuration to understand the structure
- Modify guard expressions to test different scenarios
- Add custom fields to track additional project data
- Extend metadata to customize the UI further
- Create your own workflow based on this example

## See Also

- [Main Workflow README](../../README.md)
- [YAML Configuration Guide](../../yaml/README.md)
- [Other Examples](../)
