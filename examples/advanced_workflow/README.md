# Advanced Workflow Example

This example demonstrates **all advanced features** of the workflow system through a comprehensive project management workflow.

```mermaid
flowchart TD
    subgraph grp_Finance ["Finance"]
        p_budget_review(["budget review"])
        p_budget_approved(["budget approved"])
    end
    subgraph grp_Design ["Design"]
        p_design_review(["design review"])
    end
    subgraph grp_QA ["QA"]
        p_qa_testing(["qa testing"])
        p_qa_complete(["qa complete"])
    end
    subgraph grp_Security ["Security"]
        p_security_review(["security review"])
        p_security_complete(["security complete"])
    end
    subgraph grp_Legal ["Legal"]
        p_legal_review(["legal review"])
        p_legal_complete(["legal complete"])
    end
    p_planning(["planning"])
    p_development(["development"])
    p_approved(["approved"])
    p_deployment_ready(["deployment ready"])
    p_deployed(["deployed"])
    p_rejected(["rejected"])
    class p_planning place
    class p_budget_review place
    class p_budget_approved place
    class p_design_review place
    class p_development place
    class p_qa_testing place
    class p_security_review place
    class p_legal_review place
    class p_qa_complete place
    class p_security_complete place
    class p_legal_complete place
    class p_approved place
    class p_deployment_ready place
    class p_deployed terminal
    class p_rejected place
    t_submit_budget["submit budget"]
    class t_submit_budget action
    p_planning --> t_submit_budget
    t_submit_budget -->|"❰ role project_manager ❱"| p_budget_review
    t_approve_budget["approve budget"]
    class t_approve_budget person
    p_budget_review --> t_approve_budget
    t_approve_budget -->|"❰ role project_manager or role admin or role finance_manager ❱"| p_budget_approved
    t_reject_budget["reject budget"]
    class t_reject_budget person
    p_budget_review --> t_reject_budget
    t_reject_budget -->|"❰ role project_manager or role admin or role finance_manager ❱"| p_planning
    t_start_development["start development"]
    class t_start_development action
    p_budget_approved --> t_start_development
    t_start_development -->|"❰ role project_manager and team_size ≤ 10 ❱"| p_development
    t_submit_design_review["submit design review"]
    class t_submit_design_review action
    p_development --> t_submit_design_review
    t_submit_design_review -->|"❰ (role developer or role team_lead) and requires_legal != true ❱"| p_design_review
    t_submit_design_and_legal_review["submit design and legal review"]
    class t_submit_design_and_legal_review action
    p_development --> t_submit_design_and_legal_review
    f_submit_design_and_legal_review{"+"}
    class f_submit_design_and_legal_review gateway
    t_submit_design_and_legal_review -->|"❰ (role developer or role team_lead) and requires_legal = true ❱"| f_submit_design_and_legal_review
    f_submit_design_and_legal_review --> p_design_review
    f_submit_design_and_legal_review --> p_legal_review
    t_approve_design["approve design"]
    class t_approve_design person
    p_design_review --> t_approve_design
    f_approve_design{"+"}
    class f_approve_design gateway
    t_approve_design -->|"❰ (role design_lead or role designer) and (inPlace(#39;legal_complete#39;) or requires_legal != true) ❱"| f_approve_design
    f_approve_design --> p_qa_testing
    f_approve_design --> p_security_review
    t_reject_design["reject design"]
    class t_reject_design person
    p_design_review --> t_reject_design
    t_reject_design -->|"❰ role design_lead or role designer ❱"| p_development
    t_complete_qa["complete qa"]
    class t_complete_qa action
    p_qa_testing --> t_complete_qa
    t_complete_qa -->|"❰ (role qa_lead or role tester) and getContext(#39;test_coverage#39;, 0) ≥ 80 ❱"| p_qa_complete
    t_complete_security_review["complete security review"]
    class t_complete_security_review action
    p_security_review --> t_complete_security_review
    t_complete_security_review -->|"❰ role security_lead or role security_analyst ❱"| p_security_complete
    t_complete_legal_review["complete legal review"]
    class t_complete_legal_review action
    p_legal_review --> t_complete_legal_review
    t_complete_legal_review -->|"❰ role legal_advisor or role lawyer ❱"| p_legal_complete
    t_approve_standard_reviews["approve standard reviews"]
    class t_approve_standard_reviews person
    j_approve_standard_reviews{"+"}
    class j_approve_standard_reviews gateway
    p_qa_complete --> j_approve_standard_reviews
    p_security_complete --> j_approve_standard_reviews
    j_approve_standard_reviews --> t_approve_standard_reviews
    t_approve_standard_reviews -->|"❰ (role project_manager or role admin) and requires_legal != true ❱"| p_approved
    t_approve_all_reviews_including_legal["approve all reviews including legal"]
    class t_approve_all_reviews_including_legal person
    j_approve_all_reviews_including_legal{"+"}
    class j_approve_all_reviews_including_legal gateway
    p_qa_complete --> j_approve_all_reviews_including_legal
    p_security_complete --> j_approve_all_reviews_including_legal
    p_legal_complete --> j_approve_all_reviews_including_legal
    j_approve_all_reviews_including_legal --> t_approve_all_reviews_including_legal
    t_approve_all_reviews_including_legal -->|"❰ (role project_manager or role admin) and requires_legal = true ❱"| p_approved
    t_mark_deployment_ready["mark deployment ready"]
    class t_mark_deployment_ready action
    p_approved --> t_mark_deployment_ready
    t_mark_deployment_ready -->|"❰ role project_manager or role admin ❱"| p_deployment_ready
    t_deploy["deploy"]
    class t_deploy action
    p_deployment_ready --> t_deploy
    t_deploy -->|"❰ role devops or role admin ❱"| p_deployed
    t_reject_project["reject project"]
    class t_reject_project person
    j_reject_project{"×"}
    class j_reject_project gateway
    p_design_review --> j_reject_project
    p_qa_testing --> j_reject_project
    p_security_review --> j_reject_project
    p_legal_review --> j_reject_project
    p_qa_complete --> j_reject_project
    p_security_complete --> j_reject_project
    p_legal_complete --> j_reject_project
    j_reject_project --> t_reject_project
    t_reject_project -->|"❰ role admin or role project_manager ❱"| p_rejected
    t_reject_project -. cancels .-> p_design_review
    t_reject_project -. cancels .-> p_qa_testing
    t_reject_project -. cancels .-> p_security_review
    t_reject_project -. cancels .-> p_legal_review
    t_reject_project -. cancels .-> p_qa_complete
    t_reject_project -. cancels .-> p_security_complete
    t_reject_project -. cancels .-> p_legal_complete
    t_restart_from_rejected["restart from rejected"]
    class t_restart_from_rejected person
    p_rejected --> t_restart_from_rejected
    t_restart_from_rejected -->|"❰ role project_manager ❱"| p_planning
    classDef place fill:#FFFFFF,stroke:#6B7280,stroke-width:1px,color:#111827
    classDef current fill:#DCFCE7,stroke:#15803D,stroke-width:3px,color:#14532D,font-weight:bold
    classDef terminal fill:#F3F4F6,stroke:#6B7280,stroke-dasharray:3 3,color:#374151
    classDef action fill:#1D4ED8,stroke:#1E3A8A,color:#FFFFFF
    classDef person fill:#15803D,stroke:#14532D,color:#FFFFFF
    classDef auto fill:#E0F2FE,stroke:#0369A1,color:#0C4A6E
    classDef timer fill:#FEF3C7,stroke:#B45309,color:#92400E
    classDef startMarker fill:#111827,stroke:#111827,color:#111827
    classDef gateway fill:#F8FAFC,stroke:#334155,stroke-width:2px,color:#334155,font-weight:bold
    linkStyle 48 stroke:#B91C1C
    linkStyle 49 stroke:#B91C1C
    linkStyle 50 stroke:#B91C1C
    linkStyle 51 stroke:#B91C1C
    linkStyle 52 stroke:#B91C1C
    linkStyle 53 stroke:#B91C1C
    linkStyle 54 stroke:#B91C1C
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

- **Forks**: Single state → multiple states (parallel processing, the ◇+ gateway)
- **Joins**: Multiple states → single state (synchronization, ◇+ again)
- **Loops**: Rejected projects can restart from planning
- **Conditional paths**: Legal review only if `requires_legal == true`

### 9. OR-input rejection with cancellation (one transition, no twins)

`reject_project` is a single **OR-input** transition (`from_any: true`): it is
enabled by whichever review place holds the project and consumes exactly that
one — the ◇× exclusive gateway in the diagram. Its **reset arcs** (`resets:`)
clear every sibling review token atomically, so rejecting from QA also cancels
an in-flight security or legal review. This replaced seven copy-paste
`reject_*` transitions, one per stage.

### 10. Library-rendered diagrams with team lanes and a direction switcher

The `/diagram` page renders `Definition.Diagram()` — the same definition the
engine fires, so it can never drift. Places carry a `diagram_group` metadata
key mapping each review team to a boxed **lane**; human decisions carry
`diagram_class: person` for actor-typed coloring; and the page's **flow
switcher** re-renders top-down / left-right / bottom-up / right-left
(`Diagram(workflow.DiagramDirectionLeftRight)`).

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
  initial_marking: planning
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
