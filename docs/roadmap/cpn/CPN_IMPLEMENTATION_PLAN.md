# Smart Tokens (Colored Petri Nets - CPN) Implementation Plan

## Executive Summary

This document outlines a comprehensive plan for implementing Smart Tokens (Colored Petri Nets - CPN) in the Go Workflow engine. CPN will enable multiple tokens per place, each carrying data attributes, allowing for batch processing, data-driven routing, and complex synchronization patterns within a single workflow instance.

## Current State Analysis

### Current Implementation
- **Marking Model**: Boolean model - a place either exists in the marking or doesn't
- **Token Storage**: `Marking` stores a set of places `[]Place` - no token counting, no per-token data
- **Storage**: Places are persisted as JSON array `["place1", "place2"]`
- **Transition Logic**: Checks if places exist (boolean), not token counts or attributes
- **Context**: Workflow-level context exists, but not per-token data

### Limitations
- ❌ Cannot have multiple tokens in the same place within one workflow instance
- ❌ Cannot attach data to individual tokens
- ❌ Cannot route tokens based on their attributes
- ❌ Cannot process batches of items atomically within one workflow

## Goals and Objectives

### Primary Goals
1. **Multiple Tokens per Place**: Support multiple tokens in the same place within one workflow instance
2. **Token Attributes**: Each token carries its own data (color) - e.g., `{order_id: "001", amount: 100}`
3. **Data-Driven Routing**: Transitions can evaluate token attributes to route tokens differently
4. **Backward Compatibility**: Existing workflows continue to work without changes
5. **Storage Persistence**: Token data is persisted and recoverable

### Success Criteria
- ✅ Can create multiple tokens in the same place with different attributes
- ✅ Transitions can select and route tokens based on their attributes
- ✅ Storage layer persists and loads token data correctly
- ✅ Existing boolean marking workflows continue to function
- ✅ Expression guards can access token attributes
- ✅ YAML configuration supports token definitions

## Architecture Overview

### Core Components

```
┌─────────────────────────────────────────────────────────┐
│                    Token Model                          │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐ │
│  │   Token      │  │   TokenData  │  │   TokenID    │ │
│  │  (identity)  │  │  (attributes)│  │  (unique ID) │ │
│  └──────────────┘  └──────────────┘  └──────────────┘ │
└─────────────────────────────────────────────────────────┘
                        │
                        ▼
┌─────────────────────────────────────────────────────────┐
│              Enhanced Marking Model                     │
│  ┌──────────────────────────────────────────────────┐  │
│  │  Place → []Token (map[Place][]Token)            │  │
│  │  - Supports multiple tokens per place            │  │
│  │  - Each token has unique ID and data            │  │
│  └──────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────┘
                        │
                        ▼
┌─────────────────────────────────────────────────────────┐
│            Transition Token Selection                   │
│  - Select tokens from input places                      │
│  - Evaluate token attributes in guards                  │
│  - Route tokens to output places                       │
│  - Support token transformation                         │
└─────────────────────────────────────────────────────────┘
                        │
                        ▼
┌─────────────────────────────────────────────────────────┐
│              Storage Layer Extension                    │
│  - Persist token data per place                        │
│  - Load tokens with attributes                         │
│  - Migration support for existing workflows             │
└─────────────────────────────────────────────────────────┘
```

## Implementation Phases

### Phase 1: Core Token Model (Foundation)
**Duration**: 2-3 weeks  
**Priority**: Critical

#### 1.1 Token Data Structure
- [ ] Define `Token` struct with unique ID and data attributes
- [ ] Define `TokenData` type (map[string]interface{}) for token attributes
- [ ] Implement token comparison and equality
- [ ] Add token creation helpers

**Files to Create/Modify:**
- `token.go` (new file)
- `token_test.go` (new file)

**Key Types:**
```go
type TokenID string  // Unique identifier for a token
type TokenData map[string]interface{}  // Token attributes (color)

type Token struct {
    ID   TokenID
    Data TokenData
}
```

#### 1.2 Enhanced Marking Interface
- [ ] Extend `Marking` interface to support tokens
- [ ] Create `CPNMarking` implementation (Colored Petri Net marking)
- [n/a] Maintain backward compatibility with boolean marking - (No need to maintain)
- [ ] Add methods: `AddToken(place, token)`, `RemoveToken(place, tokenID)`, `TokensAt(place)`, `TokenCount(place)`

**Files to Modify:**
- `marking.go` - Extend interface, add CPNMarking
- `marking_test.go` - Add CPN tests

**Key Methods:**
```go
type Marking interface {
    // Existing methods...
    Places() []Place
    HasPlace(place Place) bool
    
    // New CPN methods (optional, for CPN-enabled workflows)
    TokensAt(place Place) []Token
    TokenCount(place Place) int
    AddToken(place Place, token Token) error
    RemoveToken(place Place, tokenID TokenID) error
    HasToken(place Place, tokenID TokenID) bool
}
```

#### 1.3 Workflow Token Support
- [ ] Add token creation methods to `Workflow`
- [ ] Add token query methods
- [ ] Update workflow initialization to support CPN mode
- [ ] Add workflow-level flag: `IsCPNEnabled() bool`

**Files to Modify:**
- `workflow.go`
- `workflow_test.go`

### Phase 2: Transition Token Selection (Core Logic)
**Duration**: 3-4 weeks  
**Priority**: Critical

#### 2.1 Token Selection in Transitions
- [ ] Extend `Transition` to support token selection logic
- [ ] Implement token filtering based on attributes
- [ ] Add token selection strategies (first, all, filter, custom)
- [ ] Support token consumption (remove from input places)

**Files to Modify:**
- `transition.go`
- `transition_test.go`

**Key Concepts:**
- **Token Selection**: Which tokens to consume from input places
- **Token Routing**: Which tokens go to which output places
- **Token Transformation**: Modify token data during transition

#### 2.2 Guard Expression Enhancement
- [ ] Extend expression environment to access token attributes
- [ ] Add helper functions: `token.Attribute('key')`, `token.Count()`, `token.Filter(condition)`
- [ ] Support token-level guards vs workflow-level guards
- [ ] Update `GuardEvent` to include selected tokens

**Files to Modify:**
- `expression.go`
- `event.go`
- `yaml/transition.go` (for YAML support)

**Example Guard:**
```yaml
guard: "token.Attribute('amount') > 1000 and hasRole('manager')"
```

#### 2.3 Transition Application Logic
- [ ] Update `Apply` and `ApplyTransition` to handle tokens
- [ ] Implement token consumption from input places
- [ ] Implement token production to output places
- [ ] Support token data transformation during transition
- [ ] Handle backward compatibility (boolean marking)
- [ ] **Automatic state persistence** after each transition (critical for long-running processes)
- [ ] Error handling with state rollback on failure

**Files to Modify:**
- `workflow.go` - `Apply`, `ApplyTransition`, `Can`, `CanTransition`
- `manager.go` - Add auto-save logic

**Logic Flow:**
1. Select tokens from input places (based on selection strategy)
2. Validate tokens (guard evaluation per token or batch)
3. Transform token data (optional)
4. Remove tokens from input places
5. Add tokens to output places
6. **Save state to storage** (atomic operation)
7. If save fails, rollback token changes

**Persistence Pattern**:
```go
func (w *Workflow) ApplyTransitionWithContext(ctx context.Context, transitionName string) error {
    w.mu.Lock()
    defer w.mu.Unlock()
    
    // ... transition logic ...
    
    // CRITICAL: Persist state after successful transition
    if w.manager != nil {
        if err := w.manager.SaveWorkflowWithTokens(w.name, w); err != nil {
            // Rollback: restore previous marking
            w.marking = previousMarking
            return fmt.Errorf("failed to persist workflow state: %w", err)
        }
    }
    
    return nil
}
```

### Phase 3: Storage Layer Extension
**Duration**: 2-3 weeks  
**Priority**: High

#### 3.1 Storage Schema Extension
- [ ] Design token storage schema
- [ ] Add `tokens` column (JSON) to store tokens per place
- [ ] Support migration from boolean marking to CPN marking
- [ ] Maintain backward compatibility

**Files to Modify:**
- `storage/sqlite.go`
- `storage/sqlite_test.go`

**Schema Design:**
```sql
-- Option 1: Add tokens column to existing table
ALTER TABLE workflow_states ADD COLUMN tokens TEXT;

-- Option 2: Separate tokens table (for scalability)
CREATE TABLE workflow_tokens (
    workflow_id TEXT,
    place TEXT,
    token_id TEXT,
    token_data TEXT,  -- JSON
    PRIMARY KEY (workflow_id, place, token_id)
);
```

#### 3.2 Storage Interface Extension
- [ ] Extend `Storage` interface to support tokens
- [ ] Implement `SaveStateWithTokens` and `LoadStateWithTokens` methods (atomic save/load)
- [ ] Update `SaveState` and `LoadState` to handle tokens (backward compatible)
- [ ] Add migration helper for existing workflows
- [ ] Design HCPN-ready interface (reserved methods, not implemented)
- [ ] Add version tracking for schema migrations

**Files to Modify:**
- `workflow.go` - Storage interface
- `storage/sqlite.go` - Implementation

**Storage Interface Design (HCPN-Ready)**:
```go
type Storage interface {
    // Existing methods (backward compatible)
    LoadState(id string) (places []Place, context map[string]interface{}, err error)
    SaveState(id string, places []Place, context map[string]interface{}) error
    DeleteState(id string) error
    
    // CPN methods (Phase 3)
    SaveStateWithTokens(id string, places []Place, context map[string]interface{}, tokens map[Place][]Token) error
    LoadStateWithTokens(id string) (places []Place, context map[string]interface{}, tokens map[Place][]Token, err error)
    
    // HCPN methods (Future - interface reserved, implementation deferred)
    // These methods exist in interface but return "not implemented" errors
    // CreateSubWorkflow(parentID string, subDefinition *Definition, token Token) (string, error)
    // LinkSubWorkflow(parentID, childID string) error
    // GetSubWorkflows(parentID string) ([]string, error)
    // GetParentWorkflow(childID string) (string, error)
}
```

**Implementation Notes**:
- `SaveState` and `LoadState` maintain backward compatibility (tokens optional)
- `SaveStateWithTokens` saves everything atomically (critical for long-running processes)
- HCPN methods return `ErrNotImplemented` until Phase 7+
- Version field in schema supports future migrations

#### 3.3 YAML Storage Configuration
- [ ] Extend YAML storage config to support token storage
- [ ] Add token schema definition in YAML
- [ ] Support token field definitions

**Files to Modify:**
- `yaml/storage_setup.go`
- `yaml/storage_sqlite.go`

### Phase 4: YAML Configuration Support
**Duration**: 2 weeks  
**Priority**: Medium

#### 4.1 YAML Token Definition
- [ ] Add token schema definition to YAML
- [ ] Support token creation in YAML
- [ ] Add token selection strategies in transitions
- [ ] Support token transformation expressions

**Files to Modify:**
- `yaml/config.go`
- `yaml/loader.go`
- `yaml/template.go`

**YAML Example:**
```yaml
workflow:
  name: order_batch
  cpn_enabled: true
  token_schema:
    order_id: string
    amount: number
    customer: string
  
  transitions:
    - name: route_by_amount
      from: [pending]
      to: [auto_approve, manager_approval]
      token_selection: filter  # first, all, filter, custom
      token_filter: "token.amount > 1000"
      token_routing:
        - condition: "token.amount <= 1000"
          to: auto_approve
        - condition: "token.amount > 1000"
          to: manager_approval
```

#### 4.2 YAML Loader Updates
- [ ] Parse token schema from YAML
- [ ] Create tokens from YAML definitions
- [ ] Load token data from storage
- [ ] Validate token schemas

**Files to Modify:**
- `yaml/loader.go`
- `yaml/loader_test.go`

### Phase 5: Advanced Features
**Duration**: 3-4 weeks  
**Priority**: Medium

#### 5.1 Token Transformation
- [ ] Support token data modification during transitions
- [ ] Add transformation expressions
- [ ] Support token merging/splitting

**Files to Create/Modify:**
- `token_transformation.go` (new)
- `transition.go`

#### 5.2 Batch Operations
- [ ] Support batch token creation
- [ ] Batch transition application
- [ ] Atomic batch operations

**Files to Modify:**
- `workflow.go`
- `manager.go`

#### 5.3 Manager Persistence Updates
- [ ] Add `SaveWorkflowWithTokens` method (atomic save of state + tokens)
- [ ] Update `LoadWorkflow` to restore token state
- [ ] Add automatic persistence after transitions (configurable)
- [ ] Support lazy loading (workflows loaded on-demand from storage)
- [ ] Add error recovery (resume from last persisted state)

**Files to Modify:**
- `manager.go` - Add persistence methods
- `workflow.go` - Integrate auto-save

**Manager Methods**:
```go
// SaveWorkflowWithTokens saves workflow state and tokens atomically
func (m *Manager) SaveWorkflowWithTokens(id string, wf *Workflow) error {
    tokens := wf.Marking().AllTokens() // Get all tokens from marking
    return m.storage.SaveStateWithTokens(
        id,
        wf.Marking().Places(),
        wf.context,
        tokens,
    )
}

// LoadWorkflowWithTokens loads workflow with token state restored
func (m *Manager) LoadWorkflowWithTokens(id string, definition *Definition) (*Workflow, error) {
    places, context, tokens, err := m.storage.LoadStateWithTokens(id)
    if err != nil {
        return nil, err
    }
    
    wf, err := NewWorkflow(id, definition, places[0])
    wf.context = context
    wf.SetMarking(NewCPNMarkingFromTokens(tokens)) // Restore tokens
    
    return wf, nil
}
```

#### 5.4 Token Queries
- [ ] Add query methods: `FindTokens(condition)`, `CountTokens(condition)`
- [ ] Support token aggregation (sum, avg, etc.)
- [ ] Add token iteration helpers

**Files to Modify:**
- `workflow.go`
- `marking.go`

#### 5.5 Synchronization Patterns
- [ ] Support "wait for all tokens" synchronization
- [ ] Implement token counting guards (e.g., "all 10 tokens must be in approved")
- [ ] Support workflow-level aggregation in guards (sum, count, avg across tokens)
- [ ] Add compensation patterns (rollback when one token fails)

**Files to Modify:**
- `expression.go` - Add aggregation functions
- `transition.go` - Support synchronization guards
- `workflow.go` - Token counting and aggregation

**Synchronization Examples**:
```yaml
# Wait for all tokens before proceeding
transition:
  name: ship_batch
  from: [approved]
  to: [shipped]
  guard: "token.Count('approved') == workflow.Context('expected_count')"

# Aggregate check across all tokens
transition:
  name: finalize_batch
  from: [reviewed]
  to: [completed]
  guard: "sum(token.amount for token in tokens) < workflow.Context('max_budget')"
```

### Phase 6: Testing and Documentation
**Duration**: 2-3 weeks  
**Priority**: High

#### 6.1 Comprehensive Testing
- [ ] Unit tests for token model
- [ ] Integration tests for CPN workflows
- [ ] Migration tests (boolean → CPN)
- [ ] Performance tests (batch operations)
- [ ] Backward compatibility tests

**Files to Create:**
- `token_test.go`
- `cpn_workflow_test.go`
- `cpn_migration_test.go`
- `cpn_performance_test.go`

#### 6.2 Documentation
- [ ] Update README with CPN examples
- [ ] Add CPN best practices guide
- [ ] Create migration guide
- [ ] Add API documentation
- [ ] Create tutorial examples

**Files to Create/Modify:**
- `README.md` - Add CPN section
- `CPN_GUIDE.md` - Comprehensive guide
- `CPN_MIGRATION.md` - Migration instructions
- `examples/cpn_batch_processing/` - Example project

## Technical Design Decisions

### Decision 1: Backward Compatibility Strategy
**Decision**: Support both boolean and CPN marking modes
**Rationale**: 
- Existing workflows must continue to work
- Gradual migration path for users
- No breaking changes

**Implementation**:
- Detect marking type at runtime
- Boolean marking: `Marking.Places()` returns places, `TokensAt()` returns empty
- CPN marking: Full token support

### Decision 2: Token Storage Strategy
**Decision**: Store tokens as JSON in existing table (Phase 1), with option for separate table (Phase 2)
**Rationale**:
- Simpler initial implementation
- Easier migration
- Can optimize later if needed

**Schema**:
```json
{
  "pending": [
    {"id": "token-1", "data": {"order_id": "001", "amount": 100}},
    {"id": "token-2", "data": {"order_id": "002", "amount": 500}}
  ],
  "review": [
    {"id": "token-3", "data": {"order_id": "003", "amount": 50}}
  ]
}
```

### Decision 3: Token Selection Strategy
**Decision**: Support multiple selection strategies
**Rationale**: Different use cases need different approaches

**Strategies**:
- `first`: Take first token from each input place
- `all`: Take all tokens from input places
- `filter`: Take tokens matching filter expression
- `custom`: User-defined selection function

### Decision 4: Expression Environment
**Decision**: Extend expression environment with token context
**Rationale**: Guards need to evaluate token attributes

**Available Variables**:
- `token` - Current token being evaluated
- `tokens` - All tokens in current selection
- `workflow` - Workflow instance (existing)
- `transition` - Transition name (existing)

### Decision 5: Dual-State Architecture (Workflow vs Token State)
**Decision**: Workflow instances maintain their own global state separate from token state
**Rationale**: Essential for batch processing, coordination, and enterprise workflows

**Architecture**:
- **Workflow Instance State (Global)**: 
  - `context` map: Shared data for all tokens (batch_id, warehouse, owner, etc.)
  - `marking`: Token positions (which places have tokens)
  - Lifecycle state: status, created_at, completed_at
  - Process-level rules and constraints
  
- **Token State (Local)**:
  - `TokenData`: Individual token attributes (order_id, amount, customer, etc.)
  - Token-specific routing decisions
  - Per-token history and metadata

**Why This Separation is Critical**:
1. **Concurrency without Conflict**: Multiple tokens process in parallel while sharing global rules
2. **Dynamic Decisions**: Transitions can check both global context AND token data
3. **Synchronization**: Workflow-level state coordinates related tokens (e.g., "wait for all 10 orders")
4. **Compensation**: Global state tracks historical audit log for rollback scenarios
5. **Resource Management**: Shared resources (budget, approval queue) managed at workflow level

**Example**:
```go
// Workflow-level state (shared by all tokens)
wf.SetContext("batch_id", "B001")
wf.SetContext("total_budget", 10000)
wf.SetContext("owner", "Alice")

// Token-level state (individual)
token1 := Token{Data: TokenData{"order_id": "001", "amount": 100}}
token2 := Token{Data: TokenData{"order_id": "002", "amount": 5000}}

// Guard can check both:
// "token.amount > 1000 AND workflow.Context('owner') == 'Alice'"
```

### Decision 6: Tokens as Data Elements (CPN) vs Sub-Processes (HCPN)
**Decision**: In CPN implementation, tokens are data-carrying elements, NOT separate workflow instances
**Rationale**: Efficiency, simplicity, and proper separation of concerns

**CPN Model (This Implementation)**:
- **Tokens are data packets**: Carry attributes (color) that move within one workflow instance
- **Purpose**: Batch processing with low overhead (100 orders in one instance, not 100 instances)
- **Benefits**: Shared context, atomic operations, efficient storage, single workflow ID to track

**HCPN Model (Future Consideration)**:
- **Tokens trigger sub-processes**: When token hits special place, creates nested workflow instance
- **Purpose**: Modularity, reusability, complexity management
- **Use Case**: Main workflow "Onboard Employee" → token triggers "Background Check" sub-workflow
- **Note**: HCPN is mentioned in README but is out of scope for initial CPN implementation

**Key Distinction**:
```
CPN (This Plan):           HCPN (Future):
┌─────────────────┐       ┌─────────────────┐
│ Workflow Instance│       │ Workflow Instance│
│  ├─ Token 1      │       │  ├─ Token 1      │
│  ├─ Token 2      │       │  └─ [Sub-Process]│
│  └─ Token 3      │       │      └─ Nested   │
└─────────────────┘       │        Workflow  │
                          └─────────────────┘
```

**Folder-File Analogy**:
- **Folder (Workflow Instance)**: Manages overall structure, high-level decisions, shared context
- **File (Token)**: Individual unit of work with its own data, follows data-driven routing
- **Different Flows**: Tokens can take different paths based on attributes, but share workflow-level coordination

### Decision 7: Long-Running Process Support
**Decision**: Design CPN implementation to support workflows that run for hours, days, or weeks
**Rationale**: Enterprise workflows often require human approval, external API calls, or scheduled delays

**Key Requirements**:
1. **State Persistence**: All workflow state (marking, context, tokens) must be persisted after every transition
2. **Resumability**: Workflow can be loaded from storage and resumed after server restart
3. **Token Persistence**: Token data must survive process restarts
4. **Versioning**: Support schema migrations for token data structure changes
5. **Async Operations**: Support external operations that complete asynchronously

**Implementation Strategy**:
- **Automatic Persistence**: Manager automatically saves state after each transition
- **Lazy Loading**: Workflows loaded on-demand from storage (not all in memory)
- **Token Storage**: Tokens stored as JSON in database, loaded with workflow state
- **State Snapshots**: Full state saved, not incremental (simpler, more reliable)

### Decision 8: HCPN-Ready Architecture
**Decision**: Design interfaces and storage to support future Hierarchical CPN (sub-processes) without breaking changes
**Rationale**: HCPN is a natural evolution, architecture should accommodate it

**HCPN Requirements (Future)**:
1. **Sub-Process Invocation**: Token arrival at special place triggers nested workflow
2. **Parent-Child Relationship**: Track which workflow instance spawned which sub-workflow
3. **Token Passing**: Pass token data to sub-workflow, receive result back
4. **Sub-Process Completion**: Parent workflow resumes when sub-process completes
5. **Nested State Management**: Each level maintains its own state independently

**Design for HCPN Compatibility**:

**1. Storage Interface Extension (HCPN-Ready)**:
```go
type Storage interface {
    // Existing methods...
    LoadState(id string) (places []Place, context map[string]interface{}, err error)
    SaveState(id string, places []Place, context map[string]interface{}) error
    
    // CPN methods (Phase 3)
    LoadTokens(id string) (map[Place][]Token, error)
    SaveTokens(id string, tokens map[Place][]Token) error
    
    // HCPN methods (Future - reserved interface, not implemented yet)
    // CreateSubWorkflow(parentID string, subDefinition *Definition, token Token) (string, error)
    // LinkSubWorkflow(parentID, childID string) error
    // GetSubWorkflows(parentID string) ([]string, error)
    // GetParentWorkflow(childID string) (string, error)
}
```

**2. Workflow Interface Extension (HCPN-Ready)**:
```go
type Workflow struct {
    // Existing fields...
    name         string
    definition   *Definition
    marking      Marking
    context      map[string]interface{}
    
    // HCPN fields (Future - reserved, nil for CPN)
    parentID     *string  // nil if root workflow
    subWorkflows []string // IDs of spawned sub-workflows (empty for CPN)
    subProcessPlace *Place // Place that triggered sub-process (nil for CPN)
}
```

**3. Place Type Extension (HCPN-Ready)**:
```go
// Place can be extended to support sub-process invocation
type PlaceType string

const (
    PlaceTypeNormal    PlaceType = "normal"     // Regular place (CPN)
    PlaceTypeSubProcess PlaceType = "subprocess" // Triggers sub-workflow (HCPN)
)

// Future: Definition.PlaceType(place Place) PlaceType
// Future: When token arrives at subprocess place, invoke sub-workflow
```

**4. Token Passing Strategy (HCPN-Ready)**:
```go
// Token structure supports sub-process invocation
type Token struct {
    ID   TokenID
    Data TokenData
    
    // HCPN fields (Future - reserved, nil for CPN)
    SubWorkflowID *string  // If token is waiting for sub-process
    ParentTokenID *TokenID // If token came from parent workflow
}
```

**Migration Path to HCPN**:
1. **Phase 1-6 (Current Plan)**: Implement CPN with reserved fields (nil/empty)
2. **Future Phase 7**: Add sub-process place type to Definition
3. **Future Phase 8**: Implement sub-process invocation in transitions
4. **Future Phase 9**: Add parent-child tracking in storage
5. **Future Phase 10**: Support token passing between parent and child

**Benefits of This Design**:
- **No Breaking Changes**: CPN implementation doesn't block HCPN
- **Clear Extension Points**: Interfaces clearly show where HCPN will extend
- **Storage Ready**: Database schema can accommodate parent-child relationships
- **Backward Compatible**: Existing CPN workflows continue to work

## Long-Running Process Implementation Details

### State Persistence Strategy

**After Every Transition**:
```go
// In workflow.ApplyTransitionWithContext
func (w *Workflow) ApplyTransitionWithContext(ctx context.Context, transitionName string) error {
    // ... transition logic ...
    
    // CRITICAL: Save state after transition
    if w.manager != nil {
        if err := w.manager.SaveWorkflow(w.name, w); err != nil {
            return fmt.Errorf("failed to persist workflow state: %w", err)
        }
    }
    
    return nil
}
```

**Token Persistence**:
```go
// Extended Storage interface
type Storage interface {
    // ... existing methods ...
    
    // Save tokens alongside state
    SaveStateWithTokens(id string, places []Place, context map[string]interface{}, tokens map[Place][]Token) error
    
    // Load tokens with state
    LoadStateWithTokens(id string) (places []Place, context map[string]interface{}, tokens map[Place][]Token, err error)
}
```

**Storage Schema for Long-Running Processes**:
```sql
CREATE TABLE workflow_states (
    id TEXT PRIMARY KEY,
    state TEXT NOT NULL,           -- JSON array of places
    context TEXT,                   -- JSON object of workflow context
    tokens TEXT,                    -- JSON object: {place: [tokens]}
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    version INTEGER DEFAULT 1,      -- For schema migrations
    
    -- HCPN fields (reserved for future)
    parent_id TEXT,                  -- NULL for root workflows
    sub_process_place TEXT           -- NULL for non-sub-process places
);

CREATE INDEX idx_workflow_parent ON workflow_states(parent_id);
CREATE INDEX idx_workflow_updated ON workflow_states(updated_at);
```

### Resumability Design

**Workflow Loading**:
```go
// Manager.LoadWorkflow already supports this pattern
func (m *Manager) LoadWorkflow(id string, definition *Definition) (*Workflow, error) {
    // 1. Try registry (in-memory)
    if wf, err := m.registry.Workflow(id); err == nil {
        return wf, nil
    }
    
    // 2. Load from storage (long-running process resume)
    places, context, tokens, err := m.storage.LoadStateWithTokens(id)
    if err != nil {
        return nil, err
    }
    
    // 3. Reconstruct workflow with tokens
    wf, err := NewWorkflow(id, definition, places[0])
    wf.context = context
    wf.SetMarking(NewCPNMarking(tokens)) // Restore token state
    
    return wf, nil
}
```

**State Consistency**:
- **Atomic Saves**: Each transition saves complete state atomically
- **Idempotent Loads**: Loading same workflow multiple times produces same result
- **Version Tracking**: Schema version stored to support migrations

### Async Operation Support

**External Operation Pattern**:
```go
// Workflow can pause waiting for external event
wf.SetContext("awaiting_payment", true)
wf.SetContext("payment_callback_id", "callback-123")

// Later, external system calls back
wf.SetContext("payment_status", "completed")
wf.SetContext("awaiting_payment", false)

// Workflow can now proceed
wf.ApplyTransition("process_payment")
```

**Timer/Delay Support (Future)**:
- Store scheduled transition time in context
- External scheduler (cron/job queue) triggers transition
- Workflow remains in storage between scheduled events

## Migration Path

### For Existing Workflows
1. **Automatic Detection**: System detects boolean marking
2. **No Changes Required**: Existing workflows continue to work
3. **Opt-in CPN**: Enable CPN mode when creating new workflows or migrating

### Migration Steps
1. Enable CPN mode on workflow definition
2. Convert boolean marking to CPN marking (one token per place)
3. Update transitions to use token selection (if needed)
4. Test and validate
5. Deploy

## Risk Assessment

### High Risk
- **Breaking Changes**: Mitigated by backward compatibility
- **Performance**: Token operations may be slower - need benchmarking
- **Storage Size**: Token data increases storage - need optimization

### Medium Risk
- **Complexity**: CPN adds complexity - mitigated by good documentation
- **Migration**: Users need to understand migration - provide clear guide

### Low Risk
- **Testing**: Comprehensive test coverage required
- **Documentation**: Clear examples needed

## Success Metrics

### Functional Metrics
- ✅ Can create 100+ tokens in one place
- ✅ Transitions can route tokens based on attributes
- ✅ Storage persists and loads tokens correctly
- ✅ Expression guards evaluate token attributes
- ✅ YAML configuration supports CPN

### Performance Metrics
- Token creation: < 1ms per token
- Transition with 100 tokens: < 100ms
- Storage save/load: < 50ms for 100 tokens

### Quality Metrics
- Test coverage: > 90%
- Backward compatibility: 100% of existing tests pass
- Documentation: All features documented with examples

## Timeline Estimate

**Total Duration**: 14-19 weeks (3.5-5 months)

- Phase 1: 2-3 weeks
- Phase 2: 3-4 weeks
- Phase 3: 2-3 weeks
- Phase 4: 2 weeks
- Phase 5: 3-4 weeks
- Phase 6: 2-3 weeks

**Recommended Approach**: 
- Phases 1-3 are critical and should be done sequentially
- Phases 4-5 can be done in parallel after Phase 3
- Phase 6 should be ongoing throughout all phases

## Next Steps

1. **Review and Approve Plan**: Get stakeholder approval
2. **Set Up Development Branch**: Create `feature/cpn-tokens` branch
3. **Start Phase 1**: Begin with token model implementation
4. **Regular Reviews**: Weekly progress reviews
5. **Incremental Releases**: Release features incrementally for feedback

## Open Questions

1. **Token ID Generation**: UUID vs sequential vs user-provided?
2. **Token Immutability**: Should tokens be immutable or mutable?
3. **Token Versioning**: Do we need token version history?
4. **Concurrent Token Access**: How to handle concurrent token modifications?
5. **Token Limits**: Should we limit tokens per place?

## Summary: Long-Running Processes & HCPN Readiness

### Key Design Principles

**1. Long-Running Process Support**:
- ✅ **Automatic Persistence**: State saved after every transition (atomic operation)
- ✅ **Resumability**: Workflows can be loaded from storage and resumed after restart
- ✅ **Token Persistence**: Token data survives process restarts
- ✅ **Lazy Loading**: Workflows loaded on-demand, not all in memory
- ✅ **Error Recovery**: Resume from last persisted state on failure

**2. HCPN-Ready Architecture**:
- ✅ **Reserved Interfaces**: Storage and Workflow interfaces include HCPN methods (not implemented)
- ✅ **Reserved Fields**: Token and Workflow structs have HCPN fields (nil/empty for CPN)
- ✅ **Schema Ready**: Database schema includes parent-child relationship columns
- ✅ **No Breaking Changes**: CPN implementation doesn't block future HCPN
- ✅ **Clear Extension Points**: Interfaces show exactly where HCPN will extend

**3. Implementation Strategy**:
- **Phase 1-6**: Implement CPN with HCPN-ready interfaces (reserved, not implemented)
- **Future Phase 7+**: Implement HCPN features using reserved interfaces
- **Backward Compatible**: All CPN workflows continue to work when HCPN is added

**4. Critical Implementation Details**:
- **Atomic Saves**: `SaveStateWithTokens` saves everything in one transaction
- **State Restoration**: `LoadStateWithTokens` restores complete workflow state
- **Error Handling**: Rollback on persistence failure
- **Version Tracking**: Schema version supports future migrations

**5. Storage Schema**:
```sql
-- Supports both CPN (current) and HCPN (future)
CREATE TABLE workflow_states (
    id TEXT PRIMARY KEY,
    state TEXT NOT NULL,           -- Places (CPN)
    context TEXT,                   -- Workflow context (CPN)
    tokens TEXT,                    -- Token data (CPN)
    created_at TIMESTAMP,
    updated_at TIMESTAMP,
    version INTEGER DEFAULT 1,      -- Schema version
    
    -- HCPN fields (reserved, NULL for CPN)
    parent_id TEXT,                  -- Parent workflow ID
    sub_process_place TEXT           -- Sub-process trigger place
);
```

**6. Migration Path**:
- **CPN → HCPN**: No breaking changes, just enable HCPN features
- **Schema Migration**: Version field allows safe schema updates
- **Data Migration**: Existing CPN workflows automatically compatible

This design ensures that CPN implementation is production-ready for long-running processes and provides a clear, non-breaking path to HCPN in the future.

## References

- [Colored Petri Nets - Wikipedia](https://en.wikipedia.org/wiki/Colored_Petri_net)
- [CPN Tools Documentation](http://cpntools.org/)
- Current README.md - CPN section (lines 197-253)
- Existing codebase structure and patterns

