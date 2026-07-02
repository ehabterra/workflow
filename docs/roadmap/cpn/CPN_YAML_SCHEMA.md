# CPN YAML Schema Specification

This document defines the complete YAML schema for Colored Petri Net (CPN) workflows. This schema is **CPN-first** and does not support backward compatibility with boolean marking workflows.

## Schema Overview

```yaml
workflow:
  name: string                    # Required: Workflow name
  initial_place: string           # Required: Starting place
  token_schemas:                  # Required: Token type definitions
    schema_name:
      field_name: field_type
  metadata:                       # Optional: Workflow-level metadata
    key: value
  places:                         # Required: Place definitions
    - name: string
      token_schema: string        # Required: Token schema name
      metadata:                   # Optional: Place metadata
        key: value
  transitions:                    # Required: Transition definitions
    - name: string
      from: [string]              # Required: Input places
      to: [string]                # Required: Output places
      token_selection: string     # Optional: Selection strategy
      token_correlation: string   # Optional: Correlation expression
      guard: string               # Optional: Guard expression
      token_transform:            # Required: Token transformation
        place_name: expression    # Transform for each output place
      metadata:                   # Optional: Transition metadata
        key: value
      notes: string               # Optional: History notes
      actor: string               # Optional: History actor
      custom_fields:              # Optional: History custom fields
        key: value

storage:                          # Optional: Storage configuration
  type: string
  # ... storage-specific fields
```

## Complete Schema Definition

### Root Level

```yaml
workflow: WorkflowConfig          # Required: Workflow definition
storage: StorageConfig            # Optional: Storage configuration
```

### WorkflowConfig

```yaml
workflow:
  name: string                    # Required: Unique workflow name
  initial_place: string           # Required: Name of initial place
  token_schemas:                  # Required: Token schema definitions
    schema_name: TokenSchema
  metadata:                       # Optional: Workflow metadata (injected into context)
    key: any
  places:                         # Required: Place definitions
    - PlaceConfig
  transitions:                    # Required: Transition definitions
    - TransitionConfig
```

### TokenSchema

Defines the structure (fields and types) of tokens.

```yaml
token_schemas:
  schema_name:                    # Schema identifier
    field_name: field_type       # Field definition
    
# Field Types:
# - integer: Integer number
# - number: Real number (float)
# - string: Text string
# - boolean: true/false
# - array: List of values
# - object: Nested object (map)
```

**Example**:
```yaml
token_schemas:
  account:
    account_id: integer
    balance: number
    status: string
  
  transaction_request:
    account_id: integer
    amount: number
    operation_type: string
    request_id: string
    timestamp: string
```

### PlaceConfig

Defines a place in the workflow.

```yaml
places:
  - name: string                  # Required: Place name (unique)
    token_schema: string          # Required: Token schema name (from token_schemas)
    metadata:                     # Optional: Place metadata
      key: any
    initial_tokens:               # Optional: Initial tokens in this place
      - TokenData
```

**Example**:
```yaml
places:
  - name: active_accounts
    token_schema: account
    metadata:
      description: "All active accounts"
      capacity: "unlimited"
    initial_tokens:
      - account_id: 1
        balance: 0
        status: "active"
      - account_id: 2
        balance: 0
        status: "active"
  
  - name: deposit_requests
    token_schema: transaction_request
    metadata:
      description: "Queue of deposit requests"
```

### TransitionConfig

Defines a transition between places.

```yaml
transitions:
  - name: string                  # Required: Transition name (unique)
    from: [string]                # Required: Input place names (one or more)
    to: [string]                  # Required: Output place names (one or more)
    token_selection: string       # Optional: Token selection strategy
                                  # Values: "first", "all", "filter", "custom"
    token_filter: string          # Optional: Filter expression (if token_selection is "filter")
    token_correlation: string     # Optional: Correlation expression for multi-place transitions
    guard: string                 # Optional: Guard expression (must evaluate to boolean)
    token_transform:              # Required: Token transformation per output place
      place_name: string         # Expression that returns token data (or null to consume)
    metadata:                     # Optional: Transition metadata
      key: any
    notes: string                 # Optional: Default notes for history
    actor: string                 # Optional: Default actor for history
    custom_fields:                # Optional: Default custom fields for history
      key: any
```

**Key Fields Explained**:

1. **token_selection**: Strategy for selecting tokens from input places
   - `"first"`: Take first token from each input place
   - `"all"`: Take all tokens from input places
   - `"filter"`: Take tokens matching `token_filter` expression
   - `"custom"`: User-defined selection function

2. **token_correlation**: Expression to match tokens from different input places
   - Used when transition consumes from multiple places
   - Example: `active_accounts.token.account_id == deposit_requests.token.account_id`

3. **token_transform**: Defines how tokens are transformed for each output place
   - Key is output place name
   - Value is expression that returns token data (JSON object)
   - Use `null` to consume token (not produce to output place)

### StorageConfig

Storage configuration (unchanged from current schema).

```yaml
storage:
  type: string                    # Required: "sqlite", "postgres", "mysql", etc.
  # ... storage-specific fields
```

## Expression Language

All expressions use [expr-lang/expr](https://github.com/expr-lang/expr) syntax.

### Available Variables in Expressions

#### In Guards

```yaml
guard: |
  # Access token attributes from input places
  deposit_requests.token.amount >= 1
  
  # Access workflow context
  workflow.Context('max_amount') >= deposit_requests.token.amount
  
  # Access multiple tokens (if multiple input places)
  active_accounts.token.account_id == deposit_requests.token.account_id
  
  # Helper functions
  hasRole('manager')
  token.Count('pending') > 10
```

**Available Variables**:

- `{place_name}.token.{field}`: Access token field from input place
- `workflow.Context('key')`: Access workflow context
- `token`: Current token being evaluated (if single token)
- `tokens`: All tokens in current selection (if multiple tokens)

#### In Token Correlation

```yaml
token_correlation: |
  active_accounts.token.account_id == deposit_requests.token.account_id
```

**Available Variables**:
- `{place_name}.token.{field}`: Access token field from each input place

#### In Token Transform

```yaml
token_transform:
  active_accounts: |
    {
      "account_id": active_accounts.token.account_id,
      "balance": active_accounts.token.balance + deposit_requests.token.amount,
      "status": "active"
    }
  deposit_requests: null  # Consume token (don't produce)
```

**Available Variables**:
- `{place_name}.token.{field}`: Access token field from input places
- `workflow.Context('key')`: Access workflow context

## Complete Example

```yaml
# Banking System CPN Workflow
workflow:
  name: banking_system
  initial_place: active_accounts
  
  # Define token schemas
  token_schemas:
    account:
      account_id: integer
      balance: number
      status: string
    
    transaction_request:
      account_id: integer
      amount: number
      operation_type: string
      request_id: string
      timestamp: string
  
  # Workflow metadata
  metadata:
    system_name: "Banking System"
    max_accounts: 1000
    min_amount: 1
    max_amount: 5000
  
  # Define places
  places:
    - name: active_accounts
      token_schema: account
      metadata:
        description: "All active accounts"
        capacity: "unlimited"
    
    - name: deposit_requests
      token_schema: transaction_request
      metadata:
        description: "Queue of deposit requests"
    
    - name: withdraw_requests
      token_schema: transaction_request
      metadata:
        description: "Queue of withdraw requests"
  
  # Define transitions
  transitions:
    # Deposit operation
    - name: deposit
      from: [active_accounts, deposit_requests]
      to: [active_accounts]
      token_selection: filter
      token_filter: |
        active_accounts.token.account_id == deposit_requests.token.account_id
      token_correlation: |
        active_accounts.token.account_id == deposit_requests.token.account_id
      guard: |
        deposit_requests.token.amount >= 1 and 
        deposit_requests.token.amount <= 5000
      token_transform:
        active_accounts: |
          {
            "account_id": active_accounts.token.account_id,
            "balance": active_accounts.token.balance + deposit_requests.token.amount,
            "status": active_accounts.token.status
          }
        deposit_requests: null
      metadata:
        operation_type: "deposit"
      notes: "Deposit operation"
      custom_fields:
        operation: "deposit"
        amount: "{{deposit_requests.token.amount}}"
        account_id: "{{active_accounts.token.account_id}}"
    
    # Withdraw operation
    - name: withdraw
      from: [active_accounts, withdraw_requests]
      to: [active_accounts]
      token_selection: filter
      token_filter: |
        active_accounts.token.account_id == withdraw_requests.token.account_id
      token_correlation: |
        active_accounts.token.account_id == withdraw_requests.token.account_id
      guard: |
        withdraw_requests.token.amount >= 1 and 
        withdraw_requests.token.amount <= 5000
      token_transform:
        active_accounts: |
          {
            "account_id": active_accounts.token.account_id,
            "balance": active_accounts.token.balance - withdraw_requests.token.amount,
            "status": active_accounts.token.status
          }
        withdraw_requests: null
      metadata:
        operation_type: "withdraw"
      notes: "Withdraw operation"

# Storage configuration
storage:
  type: sqlite
  table: banking_accounts
  id_column: workflow_id
  state_column: state
  database: "banking_system.db"
  custom_fields:
    system_version: "system_version TEXT"
    last_operation_time: "last_operation_time TIMESTAMP"
```

## Field Type Reference

### Basic Types

| Type | Description | Example Values |
|------|-------------|----------------|
| `integer` | Whole numbers | `1`, `100`, `-5` |
| `number` | Real numbers | `1.5`, `100.99`, `-5.0` |
| `string` | Text | `"hello"`, `"account-001"` |
| `boolean` | True/false | `true`, `false` |

### Complex Types

| Type | Description | Example |
|------|-------------|---------|
| `array` | List of values | `[1, 2, 3]`, `["a", "b"]` |
| `object` | Nested object | `{"key": "value"}` |

**Note**: Complex types are defined inline in token data, not in schema.

## Token Selection Strategies

### 1. `first` - Take First Token

Takes the first token from each input place.

```yaml
token_selection: first
```

**Use Case**: Simple transitions that process one token at a time.

### 2. `all` - Take All Tokens

Takes all tokens from input places.

```yaml
token_selection: all
```

**Use Case**: Batch processing, synchronization points.

### 3. `filter` - Filter Tokens

Takes tokens matching the filter expression.

```yaml
token_selection: filter
token_filter: |
  token.amount > 1000
```

**Use Case**: Conditional token selection based on attributes.

### 4. `custom` - Custom Selection

User-defined selection function (implemented in Go).

```yaml
token_selection: custom
token_selector: "MyCustomSelector"  # Function name
```

**Use Case**: Complex selection logic that can't be expressed in expressions.

## Token Transformation

Token transformation defines how tokens are modified when moving through transitions.

### Single Place Transformation

```yaml
token_transform:
  output_place: |
    {
      "field1": input_place.token.field1 + 1,
      "field2": "updated_value"
    }
```

### Multi-Place Transformation

```yaml
token_transform:
  output_place_1: |
    {
      "field1": place1.token.field1,
      "field2": place1.token.field2 + place2.token.amount
    }
  output_place_2: |
    {
      "field1": place2.token.field1,
      "field2": "transformed"
    }
  input_place_3: null  # Consume token (don't produce)
```

### Consuming Tokens

Use `null` to consume a token without producing it to any output place:

```yaml
token_transform:
  output_place: |
    {
      "field": input_place.token.field
    }
  request_place: null  # Consume request token
```

## Validation Rules

### Required Fields

1. **Workflow**:
   - `name`: Required
   - `initial_place`: Required
   - `token_schemas`: Required (at least one)
   - `places`: Required (at least one)
   - `transitions`: Required (at least one)

2. **TokenSchema**:
   - At least one field required
   - Field names must be valid identifiers

3. **PlaceConfig**:
   - `name`: Required
   - `token_schema`: Required (must reference defined schema)

4. **TransitionConfig**:
   - `name`: Required
   - `from`: Required (at least one place)
   - `to`: Required (at least one place)
   - `token_transform`: Required (must have entry for each output place)

### Validation Rules

1. **Place Names**: Must be unique
2. **Transition Names**: Must be unique
3. **Token Schema Names**: Must be unique
4. **Place References**: All places in `from` and `to` must be defined
5. **Token Schema References**: All `token_schema` references must exist
6. **Initial Place**: Must be defined in places
7. **Token Transform**: Must have entry for each output place (or `null` to consume)

## Migration from Boolean Marking

If migrating from boolean marking, convert as follows:

**Before (Boolean Marking)**:
```yaml
workflow:
  name: simple_workflow
  initial_place: draft
  transitions:
    - name: submit
      from: [draft]
      to: [reviewed]
```

**After (CPN)**:
```yaml
workflow:
  name: simple_workflow
  initial_place: draft
  
  # Define a simple token schema (or use unit token)
  token_schemas:
    unit: {}  # Empty schema for unit tokens
  
  places:
    - name: draft
      token_schema: unit
    - name: reviewed
      token_schema: unit
  
  transitions:
    - name: submit
      from: [draft]
      to: [reviewed]
      token_selection: first
      token_transform:
        reviewed: |
          {}  # Unit token (empty object)
        draft: null  # Consume from input
```

## Helper Functions

### Token Functions

- `token.Count(place)`: Count tokens in place
- `token.Attribute('key')`: Get token attribute
- `token.Filter(condition)`: Filter tokens by condition

### Workflow Functions

- `workflow.Context('key')`: Get workflow context value
- `hasRole('role')`: Check if user has role
- `inPlace('place')`: Check if workflow is in place

## Examples

### Example 1: Simple State Machine (Unit Tokens)

```yaml
workflow:
  name: document_approval
  initial_place: draft
  
  token_schemas:
    unit: {}  # Unit token (no data)
  
  places:
    - name: draft
      token_schema: unit
    - name: reviewed
      token_schema: unit
    - name: approved
      token_schema: unit
  
  transitions:
    - name: submit
      from: [draft]
      to: [reviewed]
      token_selection: first
      token_transform:
        reviewed: |
          {}
        draft: null
    
    - name: approve
      from: [reviewed]
      to: [approved]
      guard: "hasRole('manager')"
      token_selection: first
      token_transform:
        approved: |
          {}
        reviewed: null
```

### Example 2: Batch Processing (Multiple Tokens)

```yaml
workflow:
  name: order_batch
  initial_place: pending
  
  token_schemas:
    order:
      order_id: string
      amount: number
      customer: string
  
  places:
    - name: pending
      token_schema: order
    - name: approved
      token_schema: order
    - name: rejected
      token_schema: order
  
  transitions:
    - name: approve_batch
      from: [pending]
      to: [approved]
      token_selection: all  # Process all tokens
      guard: |
        sum(token.amount for token in tokens) < 10000
      token_transform:
        approved: |
          {
            "order_id": token.order_id,
            "amount": token.amount,
            "customer": token.customer,
            "status": "approved"
          }
        pending: null
```

### Example 3: Multi-Place Transition (Request Token Model)

```yaml
workflow:
  name: banking_system
  initial_place: active_accounts
  
  token_schemas:
    account:
      account_id: integer
      balance: number
    
    request:
      account_id: integer
      amount: number
      type: string
  
  places:
    - name: active_accounts
      token_schema: account
    - name: requests
      token_schema: request
  
  transitions:
    - name: process_request
      from: [active_accounts, requests]
      to: [active_accounts]
      token_correlation: |
        active_accounts.token.account_id == requests.token.account_id
      guard: |
        requests.token.amount > 0
      token_transform:
        active_accounts: |
          {
            "account_id": active_accounts.token.account_id,
            "balance": active_accounts.token.balance + requests.token.amount
          }
        requests: null
```

## Schema Version

This schema is for **CPN workflows only**. It does not support boolean marking workflows.

**Version**: `1.0.0-cpn`
**Date**: 2024

## References

- [CPN Implementation Plan](../CPN_IMPLEMENTATION_PLAN.md)
- [Banking System Example](../examples/banking_system/banking_cpn.yaml)
- [expr-lang/expr Documentation](https://github.com/expr-lang/expr)

