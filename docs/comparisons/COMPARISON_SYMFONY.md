# Comparison: Our CPN Implementation vs Symfony Workflow Component

This document compares our CPN implementation with [Symfony's Workflow Component](https://github.com/symfony/symfony/tree/8.0/src/Symfony/Component/Workflow), which inspired our initial design.

## Executive Summary

**Symfony Workflow** is a mature, production-ready PHP component for managing state machines and workflows. **Our Go workflow engine** is inspired by Symfony but built on Petri Net foundations with implemented CPN capabilities (data-carrying tokens).

**Key Differences**:
- **Language**: PHP (Symfony) vs Go (ours)
- **Foundation**: State Machine (Symfony) vs Petri Net (ours)
- **Maturity**: Production-ready (Symfony) vs Active development (ours)
- **Focus**: State machine workflows (Symfony) vs CPN/Petri Net workflows (ours)

## Feature Comparison Matrix

| Feature | Symfony Workflow | Ours | Status | Notes |
|---------|----------------|----------|--------|-------|
| **Core Workflow Features** |
| State machine support | ✅ | ✅ | Implemented | We use Petri Net (more powerful) |
| Multiple states | ✅ | ✅ | Implemented | Places in Petri Net |
| Transitions | ✅ | ✅ | Implemented | Same concept |
| Initial state | ✅ | ✅ | Implemented | Initial place |
| **Configuration** |
| YAML configuration | ✅ | ✅ | Implemented | Both support YAML |
| PHP/Code configuration | ✅ | ❌ | Not applicable | Go code instead |
| XML configuration | ✅ | ❌ | Not planned | YAML is sufficient |
| **Guards & Constraints** |
| Guard expressions | ✅ | ✅ | Implemented | expr-lang/expr |
| Event listeners | ✅ | ✅ | Implemented | Event system |
| Transition blockers | ✅ | ✅ | Implemented | Guard constraints |
| **Event System** |
| Workflow events | ✅ | ✅ | Implemented | Before/after transitions |
| Guard events | ✅ | ✅ | Implemented | Guard evaluation |
| Transition events | ✅ | ✅ | Implemented | Transition lifecycle |
| **Workflow Registry** |
| Multiple workflows | ✅ | ✅ | Implemented | Registry pattern |
| Workflow registry | ✅ | ✅ | Implemented | Manager pattern |
| **Marking Store** |
| Single state marking | ✅ | ✅ | Implemented | Boolean marking |
| Multiple state marking | ✅ | ✅ | Implemented | Multiple places |
| Custom marking store | ✅ | ✅ | Implemented | Unified colored-token `Marking` |
| **Visualization** |
| Workflow visualization | ✅ | ✅ | Implemented | Mermaid diagrams |
| Graph generation | ✅ | ✅ | Implemented | Mermaid support |
| **Storage & Persistence** |
| State persistence | ⚠️ | ✅ | Implemented | Symfony: manual, Ours: built-in |
| Custom storage | ⚠️ | ✅ | Implemented | Interface-based |
| SQLite support | ❌ | ✅ | Implemented | We have it |
| PostgreSQL support | ❌ | ✅ | Implemented | We have it |
| Token persistence | ❌ | ✅ | Implemented | Full marking round-trips |
| **Advanced Features** |
| Colored Petri Nets (CPN) | ❌ | ✅ | Implemented | Our key differentiator |
| Multiple tokens per place | ❌ | ✅ | Implemented | CPN feature |
| Token attributes | ❌ | ✅ | Implemented | CPN feature |
| Data-driven routing | ❌ | ✅ | Implemented | Token-aware guards, per-token firing |
| Sub-workflows (HCPN) | ❌ | ⏳ | Future | Planned |
| Parallel transitions | ✅ | ✅ | Implemented | Both support |
| **Workflow Types** |
| State machine | ✅ | ✅ | Implemented | Supported |
| Workflow (multiple states) | ✅ | ✅ | Implemented | Our default |
| **Integration** |
| Framework integration | ✅ | ❌ | Not applicable | Standalone library |
| Doctrine integration | ✅ | ❌ | Not applicable | Our storage interface |
| **History & Audit** |
| Transition history | ⚠️ | ✅ | Implemented | We have built-in history |
| Audit trail | ⚠️ | ✅ | Implemented | History store |
| **Developer Experience** |
| Standalone library | ❌ | ✅ | Yes | Our advantage |
| Type safety | ⚠️ | ✅ | Yes | Go's type system |
| Thread safety | ⚠️ | ✅ | Yes | Go's concurrency |

## Detailed Feature Analysis

### 1. Core Architecture

#### Symfony Workflow
- **Foundation**: State Machine / Workflow (finite state machine)
- **Marking**: Single state or multiple states (but not tokens)
- **Transitions**: Defined between states
- **Integration**: Tightly integrated with Symfony framework

#### Ours
- **Foundation**: Petri Net (mathematically more powerful)
- **Marking**: Places holding tokens — including multiple data-carrying (colored) tokens per place
- **Transitions**: Defined between places
- **Integration**: Standalone library (framework-agnostic)

**Key Difference**: Symfony is state-machine based, we're Petri Net based. Petri Nets are more powerful for parallel processes.

### 2. Configuration

#### Symfony Workflow
```yaml
framework:
    workflows:
        article_publishing:
            type: 'workflow'
            marking_store:
                type: 'method'
                property: 'state'
            supports:
                - App\Entity\Article
            initial_marking: draft
            places:
                - draft
                - review
                - published
            transitions:
                to_review:
                    from: draft
                    to: review
                    guard: "subject.getWordCount() <= 500"
```

#### Ours
```yaml
workflow:
  name: blog_publishing
  initial_marking: draft
  transitions:
    - name: to_review
      from: [draft]
      to: [reviewed]
      guard: "workflow.Context('word_count') <= 500 and hasRole('author')"
```

**Similarities**: Both use YAML, both support guards, both define places/states and transitions.

**Differences**: 
- Symfony requires entity class, we're more flexible
- Symfony uses `subject.getWordCount()`, we use `workflow.Context()`
- Our guards are more expression-based

### 3. Guards & Constraints

#### Symfony Workflow
- **Expression Language**: Symfony Expression Language
- **Guards**: Can block transitions
- **Event Listeners**: Can modify behavior
- **Example**: `guard: "subject.getWordCount() <= 500"`

#### Ours
- **Expression Language**: expr-lang/expr (Go)
- **Guards**: Can block transitions
- **Event Listeners**: Can modify behavior
- **Example**: `guard: "workflow.Context('word_count') <= 500"`

**Similarities**: Both support guard expressions, both can block transitions.

**Advantage (Ours)**: expr-lang/expr is more powerful and type-safe.

### 4. Marking Store

#### Symfony Workflow
- **Single State**: One current state
- **Multiple States**: Can be in multiple states simultaneously
- **Marking Store**: Interface for custom storage
- **Property-based**: Stores state in object property

#### Ours
- **Unified Marking**: One marking supports both boolean (place marked or not) and colored semantics
- **Multiple Places**: Can be in multiple places simultaneously
- **CPN Marking**: Multiple data-carrying tokens per place — implemented
- **Storage Interface**: Pluggable storage backend (full marking round-trips)

**Key Difference**: Symfony's "multiple states" is similar to our "multiple places", but we also have CPN (multiple tokens per place) which Symfony doesn't have.

### 5. Event System

#### Symfony Workflow
```php
$workflow->getEventDispatcher()->addListener(
    'workflow.article_publishing.entered',
    function (Event $event) {
        // Handle event
    }
);
```

#### Ours
```go
wf.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error {
    // Handle event
    return nil
})
```

**Similarities**: Both have event systems, both support workflow lifecycle events.

**Advantage (Ours)**: Type-safe event handling in Go.

### 6. Storage & Persistence

#### Symfony Workflow
- **Manual Persistence**: You persist the entity yourself
- **Doctrine Integration**: Optional Doctrine ORM integration
- **No Built-in Storage**: Relies on framework/ORM

#### Ours
- **Built-in Storage**: Storage interface with SQLite and PostgreSQL implementations
- **Automatic Persistence**: Manager can auto-save; optimistic concurrency via `VersionedStorage`
- **Pluggable**: Can implement custom storage backends

**Advantage (Ours)**: Built-in persistence, not framework-dependent.

### 7. Advanced Features

#### Symfony Workflow
- ❌ **No CPN**: Doesn't support Colored Petri Nets
- ❌ **No Multiple Tokens**: Can't have multiple tokens per place
- ❌ **No Token Attributes**: Tokens don't carry data
- ❌ **No Sub-workflows**: Doesn't support nested workflows

#### Ours
- ✅ **CPN**: Implemented (unified colored-token marking)
- ✅ **Multiple Tokens**: Implemented (multiple data-carrying tokens per place)
- ✅ **Token Attributes**: Implemented (tokens carry data; guards can inspect it)
- ⏳ **Sub-workflows**: Planned (Future)

**Key Advantage**: We ship CPN capabilities that Symfony doesn't have.

## What's Missing from Ours (Compared to Symfony)

### 1. Workflow Validation ✅ (Planned)

**Symfony**: Has workflow validation to ensure definitions are correct.

**Ours**: 
- ⏳ **Workflow Checker**: Planned in roadmap
- ⏳ **Pre-execution validation**: Planned
- ⏳ **Deadlock detection**: Planned

**Status**: Already in our roadmap, not yet implemented.

### 2. Workflow Metadata ✅ (Implemented)

**Symfony**: Supports metadata on places and transitions.

**Ours**: 
- ✅ **Metadata support**: Implemented in YAML
- ✅ **Place metadata**: Supported
- ✅ **Transition metadata**: Supported

**Status**: Already implemented.

### 3. Workflow Dumper ✅ (Implemented)

**Symfony**: Can dump workflow definition to various formats.

**Ours**: 
- ✅ **Mermaid diagrams**: Implemented
- ⏳ **YAML export**: Planned (round-trip)
- ⏳ **PNML export**: Planned

**Status**: Partially implemented, export planned.

### 4. Marking Store Interface ✅ (Implemented)

**Symfony**: Has `MarkingStoreInterface` for custom marking storage.

**Ours**: 
- ✅ **Storage interface**: Implemented
- ✅ **CPN Marking**: Implemented (unified colored-token `Marking`)
- ✅ **Custom marking stores**: `Marking` is an interface; `SetMarking` accepts custom implementations

**Status**: Implemented.

### 5. Workflow Registry ✅ (Implemented)

**Symfony**: `WorkflowRegistry` manages multiple workflows.

**Ours**: 
- ✅ **Registry**: Implemented
- ✅ **Manager**: Implemented
- ✅ **Multiple workflows**: Supported

**Status**: Already implemented.

### 6. Event Dispatcher Integration ❌ (Not Needed)

**Symfony**: Integrates with Symfony EventDispatcher.

**Ours**: 
- ✅ **Event system**: Implemented (standalone)
- ❌ **Framework integration**: Not needed (standalone library)

**Status**: We have events, but don't need framework integration.

### 7. Workflow Type (State Machine vs Workflow) ⚠️ (Partially)

**Symfony**: Supports both "state_machine" and "workflow" types.

**Ours**: 
- ✅ **Workflow type**: Implemented (multiple places)
- ⚠️ **State machine type**: Not explicitly supported (but can be modeled)

**Status**: Can be modeled with single place, but not explicit type.

**Recommendation**: Consider adding explicit state machine support for compatibility.

### 8. Subject-based Workflows ❌ (Different Approach)

**Symfony**: Workflows are applied to "subjects" (entities).

**Ours**: 
- ✅ **Workflow instances**: Standalone (not tied to entities)
- ✅ **Context-based**: Uses context instead of subject properties

**Status**: Different approach - we use workflow instances, not entity-based.

**Note**: This is a design difference, not a missing feature.

### 9. Workflow Name in Events ✅ (Implemented)

**Symfony**: Events include workflow name.

**Ours**: 
- ✅ **Event system**: Implemented
- ✅ **Workflow context**: Available in events

**Status**: Already implemented.

### 10. Transition Metadata ✅ (Implemented)

**Symfony**: Transitions can have metadata.

**Ours**: 
- ✅ **Transition metadata**: Implemented in YAML
- ✅ **Custom fields**: Supported

**Status**: Already implemented.

## What We Have That Symfony Doesn't

### 1. Colored Petri Nets (CPN) ✅
- Multiple tokens per place
- Token attributes (color)
- Data-driven routing (token-aware guards, per-token firing)
- Token transformation (`TransformTokens`)

**Status**: Implemented — see [CPN_GUIDE.md](../guides/CPN_GUIDE.md)

### 2. Built-in Storage ✅
- Storage interface
- SQLite and PostgreSQL implementations
- Automatic persistence with optimistic concurrency (`VersionedStorage`)
- Custom fields support

**Status**: Implemented

### 3. History/Audit Trail ✅
- History store interface
- SQLite history implementation
- Transition records
- Custom fields in history

**Status**: Implemented

### 4. Petri Net Foundation ✅
- Mathematical rigor
- Deadlock detection potential
- Parallel process support
- Token-based semantics

**Status**: Implemented (foundation)

### 5. Standalone Library ✅
- No framework dependency
- Can be used in any Go application
- Pluggable interfaces

**Status**: Implemented

### 6. Long-Running Process Support ✅
- Explicit persistence design
- Resumable instances (load state, continue)
- State versioning (optimistic concurrency)

**Status**: Implemented (transactional atomic-execute helper in progress)

## Recommendations

### High Priority

1. **Workflow Validation** (Already Planned)
   - Add workflow definition validation
   - Pre-execution checks
   - Deadlock detection

2. **State Machine Type Support** (New)
   - Add explicit "state_machine" workflow type
   - Single place enforcement
   - Compatibility with Symfony patterns

3. **YAML Export** (Already Planned)
   - Round-trip YAML (load → modify → export)
   - Workflow definition export

### Medium Priority

4. **Workflow Templates** (Already Planned)
   - Reusable workflow patterns
   - Template system

5. **PNML Export** (Already Planned)
   - Petri Net Markup Language support
   - Interoperability with other tools

### Low Priority

6. **Framework Integration Examples**
   - Show how to integrate with popular Go frameworks
   - Not core feature, but helpful

## Key Insights

### 1. Symfony's Strengths
- **Mature**: Production-ready, battle-tested
- **Framework Integration**: Seamless Symfony integration
- **Simple API**: Easy to use for state machines
- **Good Documentation**: Comprehensive docs

### 2. Our Strengths
- **Petri Net Foundation**: More powerful than state machines
- **CPN Capabilities**: Multiple tokens, data-driven routing
- **Standalone**: No framework dependency
- **Built-in Persistence**: Storage and history included
- **Type Safety**: Go's type system

### 3. Design Philosophy Differences

**Symfony**: 
- Entity-centric (workflows applied to entities)
- Framework-integrated
- State machine focused

**Ours**: 
- Workflow instance-centric (standalone workflows)
- Framework-agnostic
- Petri Net focused (with CPN)

### 4. What We Can Learn

1. **Validation Patterns**: How Symfony validates workflow definitions
2. **Event Patterns**: Symfony's event system patterns
3. **API Design**: Symfony's clean, simple API
4. **Documentation**: Comprehensive documentation approach

## Conclusion

**Symfony Workflow** is a mature, well-designed component that inspired our initial design. **Our Go workflow engine** builds on that inspiration but adds:

1. **Petri Net Foundation**: More powerful than state machines
2. **CPN Capabilities**: Multiple tokens, data-driven routing (implemented)
3. **Built-in Persistence**: Storage (SQLite, PostgreSQL) and history included
4. **Standalone Design**: Framework-agnostic library

**Missing Features** (compared to Symfony):
- ⏳ Workflow validation (planned)
- ⚠️ Explicit state machine type (can be added)
- ⏳ YAML export (planned)

**Recommendation**: 
1. Add workflow validation (already planned)
2. Consider explicit state machine type support
3. Keep deepening the shipped CPN layer (our key differentiator)
4. Study Symfony's validation and event patterns

## References

- [Symfony Workflow Component](https://github.com/symfony/symfony/tree/8.0/src/Symfony/Component/Workflow)
- [Symfony Workflow Documentation](https://symfony.com/doc/current/workflow.html)
- [Our CPN Guide](../guides/CPN_GUIDE.md)
- [Our README](../../README.md)

