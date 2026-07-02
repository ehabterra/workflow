# CPN YAML Quick Reference

Quick reference guide for CPN YAML schema (no backward compatibility).

## Minimal Structure

```yaml
workflow:
  name: workflow_name
  initial_place: place_name
  token_schemas:
    schema_name:
      field: type
  places:
    - name: place_name
      token_schema: schema_name
  transitions:
    - name: transition_name
      from: [place1]
      to: [place2]
      token_transform:
        place2: expression
        place1: null
```

## Token Schema Types

```yaml
token_schemas:
  my_schema:
    field1: integer    # Whole numbers
    field2: number     # Real numbers
    field3: string     # Text
    field4: boolean    # true/false
    # Note: array and object are defined inline in token data
```

## Place Definition

```yaml
places:
  - name: place_name
    token_schema: schema_name      # Required: Must reference token_schemas
    metadata:                      # Optional
      key: value
    initial_tokens:                # Optional: Initial tokens
      - field1: value1
        field2: value2
```

## Transition Definition

```yaml
transitions:
  - name: transition_name
    from: [place1, place2]          # Required: Input places
    to: [place3]                    # Required: Output places
    token_selection: first          # Optional: "first", "all", "filter", "custom"
    token_filter: "condition"      # Optional: If token_selection is "filter"
    token_correlation: "expr"       # Optional: Match tokens from multiple places
    guard: "boolean_expression"     # Optional: Guard condition
    token_transform:                # Required: Transformation per output place
      place3: |
        {
          "field1": place1.token.field1,
          "field2": place1.token.field2 + place2.token.amount
        }
      place1: null                  # Consume token
      place2: null                  # Consume token
```

## Token Selection Strategies

| Strategy | Description | Use Case |
|----------|-------------|----------|
| `first` | Take first token from each input place | Simple transitions |
| `all` | Take all tokens from input places | Batch processing |
| `filter` | Take tokens matching filter | Conditional selection |
| `custom` | User-defined function | Complex logic |

## Expression Variables

### In Guards

```yaml
guard: |
  # Access token from input place
  place_name.token.field >= 100
  
  # Access workflow context
  workflow.Context('max_value') > place_name.token.value
  
  # Access multiple tokens (multi-place)
  place1.token.id == place2.token.id
  
  # Helper functions
  hasRole('manager')
  token.Count('pending') > 10
```

### In Token Correlation

```yaml
token_correlation: |
  place1.token.account_id == place2.token.account_id
```

### In Token Transform

```yaml
token_transform:
  output_place: |
    {
      "field1": input_place.token.field1,
      "field2": input_place.token.field2 + 100,
      "field3": workflow.Context('default_value')
    }
```

## Common Patterns

### Pattern 1: Simple State Machine

```yaml
token_schemas:
  unit: {}

places:
  - name: state1
    token_schema: unit
  - name: state2
    token_schema: unit

transitions:
  - name: move
    from: [state1]
    to: [state2]
    token_selection: first
    token_transform:
      state2: |
        {}
      state1: null
```

### Pattern 2: Data Transformation

```yaml
token_schemas:
  item:
    id: string
    status: string
    value: number

transitions:
  - name: update
    from: [pending]
    to: [processed]
    token_selection: first
    token_transform:
      processed: |
        {
          "id": token.id,
          "status": "processed",
          "value": token.value * 1.1
        }
      pending: null
```

### Pattern 3: Multi-Place (Request Token Model)

```yaml
transitions:
  - name: process
    from: [resources, requests]
    to: [resources]
    token_correlation: |
      resources.token.id == requests.token.resource_id
    guard: |
      requests.token.amount > 0
    token_transform:
      resources: |
        {
          "id": resources.token.id,
          "quantity": resources.token.quantity + requests.token.amount
        }
      requests: null
```

### Pattern 4: Batch Processing

```yaml
transitions:
  - name: process_all
    from: [pending]
    to: [completed]
    token_selection: all
    guard: |
      sum(token.value for token in tokens) < 10000
    token_transform:
      completed: |
        {
          "id": token.id,
          "status": "completed",
          "value": token.value
        }
      pending: null
```

## Required vs Optional

### Required Fields

- `workflow.name`
- `workflow.initial_place`
- `workflow.token_schemas` (at least one)
- `workflow.places` (at least one)
- `workflow.transitions` (at least one)
- `place.name`
- `place.token_schema`
- `transition.name`
- `transition.from` (at least one)
- `transition.to` (at least one)
- `transition.token_transform` (entry for each output place)

### Optional Fields

- `workflow.metadata`
- `place.metadata`
- `place.initial_tokens`
- `transition.token_selection`
- `transition.token_filter`
- `transition.token_correlation`
- `transition.guard`
- `transition.metadata`
- `transition.notes`
- `transition.actor`
- `transition.custom_fields`
- `storage`

## Validation Checklist

- [ ] All place names are unique
- [ ] All transition names are unique
- [ ] All token schema names are unique
- [ ] All `from` and `to` places are defined
- [ ] All `token_schema` references exist in `token_schemas`
- [ ] `initial_place` is defined in places
- [ ] `token_transform` has entry for each output place
- [ ] If `token_selection` is "filter", `token_filter` is provided
- [ ] All expressions are valid (syntax check)

## See Also

- [Complete Schema Documentation](CPN_YAML_SCHEMA.md)
- [Banking System Example](../examples/banking_system/banking_cpn.yaml)
- [Minimal Example](cpn_example_minimal.yaml)

