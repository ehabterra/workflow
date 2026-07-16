# Handling OR Semantics in Workflows

> **Update: the engine now expresses OR-inputs natively.** `from_any: true`
> makes a transition enabled by ANY ONE of its `from` places, consuming
> exactly the marked one — and `resets:` clears sibling branches atomically
> when the OR outcome ends parallel work. This example's seven per-stage
> `reject_*` twins collapsed into ONE transition:
>
> ```yaml
> - name: reject_project
>   from: [design_review, qa_testing, security_review, legal_review,
>          qa_complete, security_complete, legal_complete]
>   from_any: true
>   resets: [design_review, qa_testing, security_review, legal_review,
>            qa_complete, security_complete, legal_complete]
>   to: [rejected]
> ```
>
> The engine also ships `Workflow.ApplyAny(ctx, names...)` for the XOR side:
> fire the first of several guarded alternatives the state allows.
>
> **The patterns below remain the right tool for one case**: when each
> source place needs *different* behavior — different guards, different
> outputs, or different audit fields per source. Then one transition per
> source is the honest model, and this document shows how to keep that
> tidy. Everything below reads with that lens.

## The Problem (when per-source behavior differs)

A plain `from: [a, b]` is an **AND-join** — it requires ALL listed places.
`from_any: true` gives you the OR-input. But when "from A" and "from B"
must behave differently, a single transition (either kind) cannot express
the difference, and you want **separate transitions selected by name**:

## Pattern 1: Simple OR (A OR B → C)

### The native form (same behavior from every source)

```yaml
- name: to_c
  from: [a, b]
  from_any: true   # enabled by A OR B, consumes only the marked one
  to: [c]
```

### Separate transitions (different behavior per source)

```yaml
# Create separate transitions for each path
- name: to_c_from_a
  from: [a]
  to: [c]
  metadata:
    source: "a"
    label: "Move to C from A"

- name: to_c_from_b
  from: [b]
  to: [c]
  metadata:
    source: "b"
    label: "Move to C from B"
```

### Application Code

```go
// Check current state and apply appropriate transition
currentPlaces := wf.CurrentPlaces()

if contains(currentPlaces, "a") {
    err := wf.ApplyTransition("to_c_from_a")
} else if contains(currentPlaces, "b") {
    err := wf.ApplyTransition("to_c_from_b")
} else {
    return fmt.Errorf("neither A nor B is present")
}
```

### Helper Function

```go
func TransitionToC(wf *workflow.Workflow) error {
    currentPlaces := wf.CurrentPlaces()
    
    if contains(currentPlaces, "a") {
        return wf.ApplyTransition("to_c_from_a")
    }
    if contains(currentPlaces, "b") {
        return wf.ApplyTransition("to_c_from_b")
    }
    
    return fmt.Errorf("cannot transition to C: neither A nor B present")
}
```

---

## Pattern 2: Multiple OR Paths (A OR B OR C → D)

### Workflow Definition

```yaml
- name: to_d_from_a
  from: [a]
  to: [d]
  guard: "hasRole('role_a')"
  metadata:
    source_type: "a"
    label: "Complete from A"

- name: to_d_from_b
  from: [b]
  to: [d]
  guard: "hasRole('role_b')"
  metadata:
    source_type: "b"
    label: "Complete from B"

- name: to_d_from_c
  from: [c]
  to: [d]
  guard: "hasRole('role_c')"
  metadata:
    source_type: "c"
    label: "Complete from C"
```

### Application Code with Auto-Selection

```go
func CompleteFromAnySource(wf *workflow.Workflow) error {
    currentPlaces := wf.CurrentPlaces()
    
    // Try each transition in order
    transitions := []string{"to_d_from_a", "to_d_from_b", "to_d_from_c"}
    
    for _, transitionName := range transitions {
        if err := wf.CanTransition(transitionName); err == nil {
            return wf.ApplyTransition(transitionName)
        }
    }
    
    return fmt.Errorf("no valid transition to D from current state")
}
```

### Alternative: Explicit Selection

```go
func CompleteFromCurrentState(wf *workflow.Workflow) error {
    currentPlaces := wf.CurrentPlaces()
    
    // Explicitly check each state
    switch {
    case contains(currentPlaces, "a"):
        return wf.ApplyTransition("to_d_from_a")
    case contains(currentPlaces, "b"):
        return wf.ApplyTransition("to_d_from_b")
    case contains(currentPlaces, "c"):
        return wf.ApplyTransition("to_d_from_c")
    default:
        return fmt.Errorf("cannot complete: not in A, B, or C")
    }
}
```

---

## Pattern 3: OR with Different Guards

When different source states have different requirements:

```yaml
# From A: requires approval
- name: complete_from_a
  from: [a]
  to: [done]
  guard: "hasRole('approver') and workflow.Context('approved') == true"
  metadata:
    requires_approval: true

# From B: no approval needed
- name: complete_from_b
  from: [b]
  to: [done]
  guard: "hasRole('user')"
  metadata:
    requires_approval: false
```

**Benefit:** Each path can have different guards, making the workflow more flexible.

---

## Pattern 4: OR with Different Custom Fields

Different transitions can have different custom fields:

```yaml
# Rejection from design review
- name: reject_from_design
  from: [design_review]
  to: [rejected]
  custom_fields:
    rejection_reason: "{{rejection_reason}}"
    rejection_type: "design"
    design_score: "{{design_score}}"

# Rejection from QA testing
- name: reject_from_qa
  from: [qa_testing]
  to: [rejected]
  custom_fields:
    rejection_reason: "{{rejection_reason}}"
    rejection_type: "qa"
    test_coverage: "{{test_coverage}}"
    bugs_found: "{{bugs_found}}"
```

**Benefit:** History records capture different information based on the source state.

---

## Pattern 5: OR with Context-Based Selection

Use workflow context to determine which transition to use:

```yaml
# Standard path
- name: approve_standard
  from: [review]
  to: [approved]
  guard: "workflow.Context('path_type') == 'standard'"

# Express path
- name: approve_express
  from: [review]
  to: [approved]
  guard: "workflow.Context('path_type') == 'express'"
```

### Application Code

```go
// Set context to determine path
wf.SetContext("path_type", "express")

// Apply transition (guard will validate context)
err := wf.ApplyTransition("approve_express")
```

---

## Pattern 6: Complex OR (Multiple States, Same Action)

When many states can perform the same action:

### Workflow Definition

```yaml
# Rejection from any review state
- name: reject_from_design
  from: [design_review]
  to: [rejected]
  metadata:
    rejection_source: "design"

- name: reject_from_qa
  from: [qa_testing]
  to: [rejected]
  metadata:
    rejection_source: "qa"

- name: reject_from_security
  from: [security_review]
  to: [rejected]
  metadata:
    rejection_source: "security"

- name: reject_from_legal
  from: [legal_review]
  to: [rejected]
  metadata:
    rejection_source: "legal"
```

### Smart Helper Function

```go
func RejectFromCurrentState(wf *workflow.Workflow, reason string) error {
    currentPlaces := wf.CurrentPlaces()
    ctx := context.WithValue(context.Background(), "rejection_reason", reason)
    
    // Map of state -> transition name
    rejectionMap := map[workflow.Place]string{
        "design_review":   "reject_from_design",
        "qa_testing":      "reject_from_qa",
        "security_review": "reject_from_security",
        "legal_review":    "reject_from_legal",
    }
    
    // Find which state we're in and apply corresponding transition
    for place, transitionName := range rejectionMap {
        if contains(currentPlaces, place) {
            return wf.ApplyTransitionWithContext(ctx, transitionName)
        }
    }
    
    return fmt.Errorf("cannot reject: not in a review state")
}
```

---

## Pattern 7: OR with Priority/Precedence

When multiple OR conditions could match, define precedence:

```yaml
# High priority: from urgent state
- name: process_urgent
  from: [urgent]
  to: [processing]
  guard: "workflow.Context('priority') == 'urgent'"
  metadata:
    priority: "high"

# Normal priority: from normal state
- name: process_normal
  from: [normal]
  to: [processing]
  guard: "workflow.Context('priority') != 'urgent'"
  metadata:
    priority: "normal"
```

### Application Code with Priority

```go
func ProcessWithPriority(wf *workflow.Workflow) error {
    priority, _ := wf.Context("priority")
    currentPlaces := wf.CurrentPlaces()
    
    // Check urgent first (higher priority)
    if priority == "urgent" && contains(currentPlaces, "urgent") {
        return wf.ApplyTransition("process_urgent")
    }
    
    // Fall back to normal
    if contains(currentPlaces, "normal") {
        return wf.ApplyTransition("process_normal")
    }
    
    return fmt.Errorf("cannot process: no valid state")
}
```

---

## Naming Conventions

### ✅ Good Transition Names

```yaml
# Descriptive: includes source and action
- name: approve_from_design_review
- name: reject_from_qa_testing
- name: complete_from_urgent_state
- name: escalate_from_normal_queue
```

### ❌ Bad Transition Names

```yaml
# Vague: doesn't indicate source
- name: approve
- name: reject
- name: complete
- name: transition_1
```

### Best Practice: Include Source in Name

```yaml
# Pattern: {action}_from_{source}
- name: approve_from_design
- name: reject_from_qa
- name: complete_from_review
```

---

## Real-World Example: Rejection from Multiple States

From your `workflow_improved.yaml`:

```yaml
# Each rejection is explicit about its source
- name: reject_design_issues
  from: [design_review]
  to: [rejected]
  metadata:
    rejection_type: "design"
    label: "Reject (Design Issues)"

- name: reject_qa_failures
  from: [qa_testing]
  to: [rejected]
  metadata:
    rejection_type: "qa"
    label: "Reject (QA Failures)"

- name: reject_security_vulnerabilities
  from: [security_review]
  to: [rejected]
  metadata:
    rejection_type: "security"
    label: "Reject (Security Vulnerabilities)"
```

### Application Code

```go
func HandleRejection(wf *workflow.Workflow, reason string) error {
    currentPlaces := wf.CurrentPlaces()
    ctx := context.WithValue(context.Background(), "rejection_reason", reason)
    
    // Determine which rejection transition to use
    if contains(currentPlaces, "design_review") {
        return wf.ApplyTransitionWithContext(ctx, "reject_design_issues")
    }
    if contains(currentPlaces, "qa_testing") {
        return wf.ApplyTransitionWithContext(ctx, "reject_qa_failures")
    }
    if contains(currentPlaces, "security_review") {
        return wf.ApplyTransitionWithContext(ctx, "reject_security_vulnerabilities")
    }
    
    return fmt.Errorf("cannot reject: not in a reviewable state")
}
```

---

## Best Practices Summary

### 1. ✅ Create Separate Transitions

**For each OR path, create a separate transition:**
```yaml
- name: action_from_a
  from: [a]
  to: [target]

- name: action_from_b
  from: [b]
  to: [target]
```

### 2. ✅ Use Descriptive Names

**Include the source in the transition name:**
```yaml
# Good
- name: approve_from_design_review

# Bad
- name: approve
```

### 3. ✅ Use Helper Functions

**Create helper functions to select the right transition:**
```go
func ApplyActionFromCurrentState(wf *workflow.Workflow) error {
    // Logic to determine which transition to use
}
```

### 4. ✅ Leverage Guards

**Use guards to add conditions to each OR path:**
```yaml
- name: action_from_a
  from: [a]
  to: [target]
  guard: "hasRole('role_a')"
```

### 5. ✅ Use Metadata

**Add metadata to distinguish transitions:**
```yaml
- name: action_from_a
  from: [a]
  to: [target]
  metadata:
    source: "a"
    label: "Action from A"
```

### 6. ✅ Different Custom Fields

**Each transition can have different custom fields:**
```yaml
- name: reject_from_design
  custom_fields:
    design_score: "{{design_score}}"
    
- name: reject_from_qa
  custom_fields:
    test_coverage: "{{test_coverage}}"
```

---

## Common Pitfalls to Avoid

### ❌ Pitfall 1: Trying to Use AND for OR

```yaml
# WRONG: This requires BOTH a AND b
- name: to_c
  from: [a, b]
  to: [c]
```

**Fix:** Create separate transitions.

### ❌ Pitfall 2: Generic Transition Names

```yaml
# WRONG: Can't tell which source
- name: reject
  from: [design_review]
  to: [rejected]
```

**Fix:** Use descriptive names like `reject_from_design`.

### ❌ Pitfall 3: Not Using Transition Names

```go
// WRONG: Ambiguous when multiple transitions exist
err := wf.Apply([]workflow.Place{"rejected"})
```

**Fix:** Use `ApplyTransition("reject_from_design")`.

### ❌ Pitfall 4: Hardcoding State Checks

```go
// WRONG: Hardcoded logic
if state == "a" {
    wf.ApplyTransition("to_c_from_a")
}
```

**Fix:** Use helper functions that check current places dynamically.

---

## Performance Considerations

### Transition Lookup

- **O(n) complexity:** Finding transition by name requires looping through transitions
- **Impact:** Negligible for typical workflows (< 100 transitions)
- **Optimization:** Can cache transition lookup if needed

### Multiple Transitions

- **Storage:** Each OR path requires a separate transition definition
- **Impact:** Minimal - transitions are small objects
- **Benefit:** Better clarity and maintainability

---

## Testing OR Patterns

### Test Each OR Path

```go
func TestORPattern(t *testing.T) {
    // Test path A
    wf1, _ := NewWorkflow("test1", def, "a")
    err := wf1.ApplyTransition("to_c_from_a")
    assert.NoError(t, err)
    assert.Contains(t, wf1.CurrentPlaces(), "c")
    
    // Test path B
    wf2, _ := NewWorkflow("test2", def, "b")
    err = wf2.ApplyTransition("to_c_from_b")
    assert.NoError(t, err)
    assert.Contains(t, wf2.CurrentPlaces(), "c")
}
```

### Test Helper Functions

```go
func TestHelperFunction(t *testing.T) {
    wf, _ := NewWorkflow("test", def, "a")
    
    err := TransitionToC(wf)
    assert.NoError(t, err)
    assert.Contains(t, wf.CurrentPlaces(), "c")
}
```

---

## Summary

### The Rule

**When you need "A OR B → C":**

1. ✅ Create separate transitions: `to_c_from_a` and `to_c_from_b`
2. ✅ Use descriptive names that include the source
3. ✅ Use `ApplyTransition(name)` to select the correct one
4. ✅ Create helper functions for complex OR logic
5. ✅ Leverage guards and metadata for different requirements

### Benefits

- ✅ **Clear and explicit:** No ambiguity about which path is taken
- ✅ **Flexible:** Each path can have different guards, custom fields, metadata
- ✅ **Maintainable:** Easy to understand and modify
- ✅ **Testable:** Each path can be tested independently
- ✅ **Traceable:** History records show exactly which path was taken

---

## Quick Reference

| Scenario | Pattern | Example |
|----------|---------|---------|
| Simple OR (A or B → C) | Separate transitions | `to_c_from_a`, `to_c_from_b` |
| Multiple OR paths | Multiple transitions | `to_d_from_a`, `to_d_from_b`, `to_d_from_c` |
| OR with different guards | Separate transitions with guards | `approve_standard`, `approve_express` |
| OR with different fields | Separate transitions with custom_fields | `reject_from_design`, `reject_from_qa` |
| Complex OR (many states) | Helper function + transition map | `RejectFromCurrentState()` |

---

**Remember:** The workflow engine uses AND logic for `from` fields. To achieve OR semantics, create separate transitions and use transition names to select the correct one. This pattern is explicit, maintainable, and flexible.

