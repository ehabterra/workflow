# Workflow Engine Best Practices

## Problems Solved

### 1. **Ambiguous Transitions (Multiple Transitions → Same Destination)**

**Problem:**

```yaml
# BAD: Multiple transitions to [rejected] - which one to use?
- name: reject_from_design
  from: [design_review]
  to: [rejected]
  
- name: reject_from_qa
  from: [qa_testing]
  to: [rejected]
```

When calling `Apply([rejected])`, the engine doesn't know which transition you mean.

**Solution:** Use `ApplyTransition()` with transition names instead of `Apply()` with places.

```go
// OLD WAY (ambiguous when multiple transitions lead to same place)
err := workflow.Apply([]Place{"rejected"})

// NEW WAY (explicit, always unambiguous)
err := workflow.ApplyTransition("reject_design_issues")
```

---

### 2. **Conditional Parallel Branches (Optional Legal Review)**

**Problem:**

```yaml
# If legal review is optional, how do we join?
# - Sometimes need to wait for: QA + Security
# - Sometimes need to wait for: QA + Security + Legal
```

**Solution:** Create TWO different join transitions based on which path was taken.

```yaml
# Join WITHOUT legal (standard path)
- name: approve_standard_reviews
  from: [qa_complete, security_complete]
  to: [approved]
  guard: "hasRole('project_manager')"

# Join WITH legal (extended path) 
- name: approve_all_reviews_including_legal
  from: [qa_complete, security_complete, legal_complete]
  to: [approved]
  guard: "hasRole('project_manager')"
```

Application code determines which path to use:

```go
requiresLegal, _ := wf.Context("requires_legal")

if requiresLegal == true {
    // Wait for all three to complete, then:
    err := wf.ApplyTransition("approve_all_reviews_including_legal")
} else {
    // Wait for QA and Security only, then:
    err := wf.ApplyTransition("approve_standard_reviews")
}
```

---

### 3. **Fork with Conditional Branches**

**Problem:** How to handle "submit to design review, and MAYBE legal review"?

**Solution:** Create separate transitions for different fork paths:

```yaml
# Path 1: Design only
- name: submit_design_review
  from: [development]
  to: [design_review]
  guard: "workflow.Context('requires_legal') != true"

# Path 2: Design AND Legal (parallel fork)
- name: submit_design_and_legal_review
  from: [development]
  to: [design_review, legal_review]
  guard: "workflow.Context('requires_legal') == true"
```

Application code:

```go
requiresLegal, _ := wf.Context("requires_legal")

if requiresLegal == true {
    err := wf.ApplyTransition("submit_design_and_legal_review")
} else {
    err := wf.ApplyTransition("submit_design_review")
}
```

---

## API Usage Guide

### New Transition-by-Name API (Recommended)

```go
// Check if a specific transition is allowed
err := workflow.CanTransition("approve_design")
if err != nil {
    // Transition not allowed
}

// Apply a specific transition by name
err = workflow.ApplyTransition("approve_design")
```

### With Context

```go
ctx := context.Background()
ctx = context.WithValue(ctx, "user", "john@example.com")

err := workflow.CanTransitionWithContext(ctx, "approve_design")
err = workflow.ApplyTransitionWithContext(ctx, "approve_design")
```

### Old Place-based API (Still Supported)

```go
// Only use when there's exactly ONE transition to the target places
err := workflow.Apply([]Place{"approved"})
```

⚠️ **Warning:** If multiple transitions lead to the same destination, `Apply()` will use the first one that passes guards, which may not be what you want.

---

## Workflow Design Patterns

### Pattern 1: Simple Linear Flow

```yaml
places:
  - name: draft
  - name: review
  - name: published

transitions:
  - name: submit
    from: [draft]
    to: [review]
  
  - name: publish
    from: [review]
    to: [published]
```

**Usage:**

```go
wf.ApplyTransition("submit")
wf.ApplyTransition("publish")
```

---

### Pattern 2: Fork (Parallel Split)

```yaml
# One state → Multiple parallel states
- name: start_parallel_reviews
  from: [design_approved]
  to: [qa_testing, security_review]
```

This creates BOTH `qa_testing` AND `security_review` states simultaneously.

---

### Pattern 3: Join (Parallel Merge)

```yaml
# Multiple states → One state (all must be present)
- name: complete_all_reviews
  from: [qa_complete, security_complete]
  to: [approved]
```

This transition can only fire when BOTH `qa_complete` AND `security_complete` are present.

---

### Pattern 4: Conditional Fork + Conditional Join

```yaml
# Fork based on condition
- name: fork_standard
  from: [start]
  to: [qa, security]
  guard: "workflow.Context('needs_legal') != true"

- name: fork_with_legal
  from: [start]
  to: [qa, security, legal]
  guard: "workflow.Context('needs_legal') == true"

# Join based on which fork was taken
- name: join_standard
  from: [qa, security]
  to: [done]

- name: join_with_legal
  from: [qa, security, legal]
  to: [done]
```

---

### Pattern 5: Exclusive Choice (OR)

```yaml
# Approval OR rejection (only one will happen)
- name: approve
  from: [review]
  to: [approved]
  guard: "workflow.Context('score') >= 80"

- name: reject
  from: [review]
  to: [rejected]
  guard: "workflow.Context('score') < 80"
```

---

### Pattern 6: Loop/Cycle

```yaml
# Rejection sends back to earlier state
- name: request_changes
  from: [review]
  to: [draft]
  metadata:
    requires_reason: true
```

---

## Transition Naming Conventions

### Good Names (Descriptive, Action-Oriented)

- `submit_for_review`
- `approve_design`
- `reject_due_to_security_issues`
- `deploy_to_production`
- `restart_from_rejected`
- `complete_qa_testing`

### Bad Names (Vague, State-like)

- ❌ `to_review` (sounds like a place, not an action)
- ❌ `reject` (too generic when you have multiple rejection types)
- ❌ `next` (meaningless)
- ❌ `transition1` (non-descriptive)

---

## Guards Best Practices

### Role-based Guards

```yaml
guard: "hasRole('admin') or hasRole('project_manager')"
```

### Context-based Guards

```yaml
guard: "workflow.Context('budget') >= 10000"
guard: "workflow.Context('test_coverage') >= 80"
guard: "workflow.Context('requires_legal') == true"
```

### Combined Guards

```yaml
guard: "hasRole('qa_lead') and workflow.Context('test_coverage') >= 80 and workflow.Context('bugs_found') == 0"
```

### State-checking Guards (for complex flows)

```yaml
# Only allow design approval if legal is complete (when required)
guard: "(workflow.Context('requires_legal') != true) or (workflow.Context('requires_legal') == true and inPlace('legal_complete'))"
```

---

## Migration Guide: Old Code → New Code

### Before (Ambiguous)

```go
// Get enabled transitions, find the one by name, then apply by place
enabled, _ := wf.EnabledTransitions()
var targetTransition *workflow.Transition
for _, t := range enabled {
    if t.Name() == "approve_design" {
        targetTransition = &t
        break
    }
}
if targetTransition != nil {
    wf.Apply(targetTransition.To())
}
```

### After (Clean)

```go
// Just apply by name
err := wf.ApplyTransition("approve_design")
if err != nil {
    // Handle error
}
```

---

## Complete Example: Conditional Legal Review

### Workflow Definition

```yaml
transitions:
  # Fork into design review (conditional legal)
  - name: submit_design_only
    from: [development]
    to: [design_review]
    guard: "workflow.Context('requires_legal') != true"
  
  - name: submit_design_and_legal
    from: [development]
    to: [design_review, legal_review]
    guard: "workflow.Context('requires_legal') == true"
  
  # Design approved → Fork into QA and Security
  - name: approve_design
    from: [design_review]
    to: [qa_testing, security_review]
  
  # Complete reviews
  - name: complete_qa
    from: [qa_testing]
    to: [qa_complete]
  
  - name: complete_security
    from: [security_review]
    to: [security_complete]
  
  - name: complete_legal
    from: [legal_review]
    to: [legal_complete]
  
  # Join transitions (conditional)
  - name: approve_standard
    from: [qa_complete, security_complete]
    to: [approved]
  
  - name: approve_with_legal
    from: [qa_complete, security_complete, legal_complete]
    to: [approved]
```

### Application Code

```go
package main

import (
    "github.com/ehabterra/workflow"
)

func submitForReview(wf *workflow.Workflow) error {
    requiresLegal, _ := wf.Context("requires_legal")
    
    if requiresLegal == true {
        return wf.ApplyTransition("submit_design_and_legal")
    }
    return wf.ApplyTransition("submit_design_only")
}

func approveAllReviews(wf *workflow.Workflow) error {
    requiresLegal, _ := wf.Context("requires_legal")
    
    // Check that all required reviews are complete
    currentPlaces := wf.CurrentPlaces()
    
    hasQA := contains(currentPlaces, "qa_complete")
    hasSecurity := contains(currentPlaces, "security_complete")
    hasLegal := contains(currentPlaces, "legal_complete")
    
    if !hasQA || !hasSecurity {
        return errors.New("QA and Security must be complete")
    }
    
    if requiresLegal == true {
        if !hasLegal {
            return errors.New("Legal review must be complete")
        }
        return wf.ApplyTransition("approve_with_legal")
    }
    
    return wf.ApplyTransition("approve_standard")
}

func contains(places []workflow.Place, place workflow.Place) bool {
    for _, p := range places {
        if p == place {
            return true
        }
    }
    return false
}
```

---

## Summary of Best Practices

1. ✅ **Use `ApplyTransition(name)` instead of `Apply(places)`** when there are multiple transitions to the same destination

2. ✅ **Use descriptive transition names** that indicate the action and context (e.g., `reject_due_to_security_issues` not just `reject`)

3. ✅ **Handle conditional parallel branches** with separate join transitions for each path

4. ✅ **Use guards effectively** to control which transitions are available based on roles and context

5. ✅ **Create separate fork transitions** when you need conditional parallel paths

6. ✅ **Store workflow state in context** (like `requires_legal`) to make routing decisions

7. ✅ **Design transitions to be self-documenting** through metadata, notes, and custom fields

8. ✅ **Use transition metadata** to provide UI hints (icons, labels, priorities, etc.)

9. ✅ **Avoid ambiguity** - if two transitions have the same from/to places, they should have different guards or you should use transition names

10. ✅ **Test all paths** through conditional branches to ensure joins work correctly

---

## When to Use Each API

| Scenario | Use This |
|----------|----------|
| Multiple transitions to same destination | `ApplyTransition(name)` |
| Conditional parallel branches | `ApplyTransition(name)` + separate joins |
| Simple linear flow with unique destinations | `Apply(places)` is OK |
| Building a UI with action buttons | `ApplyTransition(name)` |
| Need to distinguish rejection reasons | `ApplyTransition("reject_security_issues")` |
| Checking if specific action is allowed | `CanTransition(name)` |

---

## Anti-Patterns to Avoid

### ❌ Anti-Pattern 1: Generic Rejection Transition

```yaml
# BAD: Can't distinguish why it was rejected
- name: reject
  from: [any_state]
  to: [rejected]
```

### ✅ Better: Specific Rejection Transitions

```yaml
- name: reject_design_issues
  from: [design_review]
  to: [rejected]
  metadata:
    rejection_type: "design"

- name: reject_security_vulnerabilities
  from: [security_review]
  to: [rejected]
  metadata:
    rejection_type: "security"
```

---

### ❌ Anti-Pattern 2: Using Place Names for Routing Logic

```go
// BAD: Checking place names to determine what to do
currentPlaces := wf.CurrentPlaces()
if currentPlaces[0] == "qa_complete" {
    wf.Apply([]Place{"approved"})
}
```

### ✅ Better: Use Transition Names

```go
// GOOD: Let the workflow engine handle routing
err := wf.ApplyTransition("approve_all_reviews")
```

---

### ❌ Anti-Pattern 3: Single Join for Conditional Branches

```yaml
# BAD: This join requires ALL three, but legal is optional!
- name: approve_all
  from: [qa_complete, security_complete, legal_complete]
  to: [approved]
```

### ✅ Better: Separate Joins

```yaml
# GOOD: Two joins for two different paths
- name: approve_standard
  from: [qa_complete, security_complete]
  to: [approved]

- name: approve_with_legal
  from: [qa_complete, security_complete, legal_complete]
  to: [approved]
```

---

## Questions & Answers

**Q: When should I use `Apply(places)` vs `ApplyTransition(name)`?**

A: Use `ApplyTransition(name)` whenever:

- Multiple transitions lead to the same destination
- You need to distinguish between different reasons for the same state change
- You're building a UI where users select actions (not states)
- You want explicit, self-documenting code

Use `Apply(places)` only when:

- There's exactly one transition to those places from current state
- You're doing simple, unambiguous state changes

### Q: How do I handle OR semantics? (e.g., transition from A OR B to C)

A: Create separate transitions:

```yaml
- name: to_c_from_a
  from: [a]
  to: [c]

- name: to_c_from_b
  from: [b]
  to: [c]
```

Then use: `wf.ApplyTransition("to_c_from_a")` or `wf.ApplyTransition("to_c_from_b")`

### Q: What if I have 10 states that can all reject to the same place?

A: Create 10 separate rejection transitions with descriptive names. This:

- Makes it clear WHY each rejection happened
- Allows different metadata/custom fields for each
- Lets you distinguish rejection types in history/reporting
- Makes the code self-documenting

### Q: Can guards return partial matches?

A: No. Guards are boolean - they either allow the transition or block it. If you need complex routing logic, create multiple transitions with different guards.

---

## Additional Resources

- See `workflow_improved.yaml` for a complete example
- Check the test files for more usage patterns
- Review the API documentation for all available methods
