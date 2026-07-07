# Comparison: Our CPN Implementation vs Petri Flow

This document compares our CPN implementation with [petri_flow](https://github.com/hooopo/petri_flow), a mature Petri Net Workflow Engine for Ruby/Rails.

## Executive Summary

**petri_flow** is a production-ready Ruby gem with comprehensive Petri Net features, web UI, and organizational integration. **Our Go workflow engine** is in active development, with Colored Petri Nets (data-carrying tokens) implemented and a clean, portable design.

**Key Differences**:
- **Language**: Ruby (petri_flow) vs Go (ours)
- **Maturity**: Production-ready (petri_flow) vs Active development (ours)
- **Focus**: Full-featured workflow engine (petri_flow) vs CPN-focused engine library (ours)
- **Architecture**: Rails-integrated (petri_flow) vs Standalone library (ours)

## Feature Comparison Matrix

| Feature | petri_flow | Ours | Status |
|---------|-----------|----------|--------|
| **Core Petri Net Features** |
| Basic workflow definition | ✅ | ✅ | Implemented |
| Sequential transitions | ✅ | ✅ | Implemented |
| Parallel transitions | ✅ | ✅ | Implemented |
| Iterative routing | ✅ | ⏳ | Planned |
| Timed transitions | ✅ | ⏳ | Planned (Timed Petri Nets) |
| Automatic transitions | ✅ | ⏳ | Planned |
| **Colored Petri Nets (CPN)** |
| Multiple tokens per place | ✅ | ✅ | Implemented (unified marking) |
| Token attributes (color) | ✅ | ✅ | Implemented |
| Data-driven routing | ✅ | ✅ | Implemented (token-aware guards, per-token firing) |
| Token transformation | ✅ | ✅ | Implemented (`TransformTokens`) |
| **Hierarchical CPN (HCPN)** |
| Sub-workflow support | ✅ | ⏳ | Planned |
| Nested workflows | ✅ | ⏳ | Planned |
| **Storage & Persistence** |
| SQLite support | ✅ | ✅ | Implemented |
| MySQL support | ✅ | ⏳ | Not planned yet |
| PostgreSQL support | ✅ | ✅ | Implemented |
| Token persistence | ✅ | ✅ | Implemented (full marking round-trips) |
| Long-running process support | ✅ | ✅ | Implemented (persistent state + optimistic concurrency) |
| **Configuration & Definition** |
| YAML configuration | ❌ | ✅ | Implemented |
| Web UI for definition | ✅ | ⏳ | Examples only |
| Graph visualization | ✅ | ✅ | Mermaid diagrams |
| Case/token migration graph | ✅ | ⏳ | Not planned |
| **Guards & Constraints** |
| Guard expressions | ✅ | ✅ | Implemented (expr) |
| Powerful guard language | ✅ | ✅ | expr-lang/expr |
| **Workflow Management** |
| Web admin interface | ✅ | ⏳ | Examples only |
| Case management UI | ✅ | ⏳ | Examples only |
| Workflow definition UI | ✅ | ⏳ | Examples only |
| **Forms & UI** |
| Dynamic form builder | ✅ | ❌ | Not planned |
| Replaceable form system | ✅ | ❌ | Not planned |
| **Organizational Integration** |
| Role-based access | ✅ | ⏳ | Planned |
| Group support | ✅ | ⏳ | Not planned |
| Position support | ✅ | ⏳ | Not planned |
| Department support | ✅ | ⏳ | Not planned |
| Assignment management | ✅ | ⏳ | Not planned |
| **Advanced Features** |
| Workflow history/audit | ✅ | ✅ | Implemented |
| Event system | ✅ | ✅ | Implemented |
| Workflow versioning | ❌ | ⏳ | Planned |
| Message correlation | ❌ | ⏳ | Planned |
| Compensation/rollback | ❌ | ⏳ | Planned |
| **Developer Experience** |
| Standalone library | ❌ | ✅ | Yes |
| Rails integration | ✅ | ❌ | No |
| REST API | ✅ | ⏳ | Examples only |
| Graphviz integration | ✅ | ❌ | Mermaid instead |

## Detailed Feature Analysis

### 1. Core Petri Net Features

#### petri_flow
- ✅ **Full petri net features**: Sequential, parallel, iterative, timed, automatic transitions
- ✅ **Production-tested**: Used in real-world applications
- ✅ **Mature implementation**: Handles edge cases and complex scenarios

#### Ours
- ✅ **Basic features**: Sequential and parallel transitions implemented
- ✅ **CPN**: Colored Petri Nets implemented (see below)
- ⏳ **Advanced features**: Iterative, timed, automatic transitions planned

**Insight**: petri_flow has comprehensive basic features. Our CPN implementation kept the existing boolean-marking functionality intact (it is the single-token special case of the unified marking).

### 2. Colored Petri Nets (CPN)

#### petri_flow
- ✅ **Implemented**: Multiple tokens per place with attributes
- ✅ **Production-ready**: Used in real workflows
- ✅ **Token data**: Tokens carry attributes (color)

#### Ours
- ✅ **Unified marking**: Multiple data-carrying tokens per place; boolean workflows are the single-token special case
- ✅ **Per-token firing**: `ApplyTransitionForToken` moves an individual token; guards can inspect token attributes
- ✅ **Token queries and transformation**: `FindTokens`, `CountTokens`, `AggregateTokens`, `TransformTokens`
- ✅ **Persistence**: The full marking (including token data) round-trips through storage
- ✅ **YAML**: Colored tokens are seeded with the polymorphic `initial_marking` key

See [CPN_GUIDE.md](../guides/CPN_GUIDE.md) for the full guide.

### 3. Hierarchical CPN (HCPN)

#### petri_flow
- ✅ **Sub-workflow support**: Implemented and production-ready
- ✅ **Nested workflows**: Can call sub-workflows from parent workflows

#### Ours
- ⏳ **Planned**: HCPN/sub-workflows are not yet designed or implemented
- ✅ **HCPN-ready architecture**: The unified marking and token model don't block HCPN
- ✅ **No breaking changes expected**: The shipped CPN layer was added without breaking the boolean model

**Insight**: petri_flow's sub-workflow implementation could inform our HCPN design.

### 4. Storage & Persistence

#### petri_flow
- ✅ **Multiple databases**: MySQL, PostgreSQL, SQLite
- ✅ **Token persistence**: Tokens stored in database
- ✅ **Long-running processes**: Handles workflows that run for days/weeks

#### Ours
- ✅ **SQLite**: Implemented
- ✅ **PostgreSQL**: Implemented
- ⏳ **MySQL**: Not planned yet (the interface allows it)
- ✅ **Token persistence**: Implemented — the full marking, including token data, round-trips through storage
- ✅ **Long-running processes**: Persistent state plus built-in optimistic concurrency and transactional building blocks (`RunInTx`, `SaveStateTx`/`LoadStateTx`)

### 5. Configuration & Definition

#### petri_flow
- ❌ **No YAML**: Uses Ruby DSL or database definitions
- ✅ **Web UI**: Visual workflow definition interface
- ✅ **Graph visualization**: Graphviz-based diagrams
- ✅ **Case/token migration graph**: Visualize token movement

#### Ours
- ✅ **YAML configuration**: Implemented and comprehensive (strict loader, polymorphic `initial_marking`)
- ⏳ **Web UI**: Examples only, not production-ready
- ✅ **Mermaid diagrams**: Generate diagrams from definitions
- ❌ **Token migration graph**: Not planned

**Advantage**: Our YAML approach is more portable and version-control friendly.

**Recommendation**: Consider adding token migration visualization for debugging.

### 6. Guards & Constraints

#### petri_flow
- ✅ **Powerful guard expressions**: Custom guard language
- ✅ **Production-tested**: Handles complex guard logic

#### Ours
- ✅ **Expression guards**: Using expr-lang/expr (powerful and flexible)
- ✅ **Token-aware guards**: Implemented — guard events carry the colored tokens at the transition's input places
- ✅ **Workflow context**: Guards can access workflow-level data

**Advantage**: expr-lang/expr is a mature, well-documented expression language.

### 7. Organizational Integration

#### petri_flow
- ✅ **Comprehensive**: Role, group, position, department support
- ✅ **Assignment management**: Powerful assignment system
- ✅ **ActsAsParty**: Flexible party system for organizational structure

#### Ours
- ⏳ **Role-based access**: Planned but not implemented (guards can check `hasRole` today)
- ❌ **Organizational structure**: Not planned
- ❌ **Assignment management**: Not planned

**Insight**: petri_flow's organizational integration is enterprise-focused. Our focus is on the core engine.

**Recommendation**: Keep organizational features as optional extensions, not core features.

### 8. Forms & UI

#### petri_flow
- ✅ **Dynamic form builder**: Built-in form system
- ✅ **Replaceable forms**: Can use custom form systems
- ✅ **Web UI**: Complete web interface for workflow management

#### Ours
- ❌ **Forms**: Not planned (focus on engine, not UI)
- ⏳ **Web UI**: Examples only, not production-ready
- ✅ **Standalone library**: Focus on engine, not UI

**Philosophy Difference**: petri_flow is a full-stack solution. We're building a library.

### 9. Advanced Features

#### petri_flow
- ✅ **Workflow history**: Audit trail
- ✅ **Event system**: Workflow lifecycle hooks
- ❌ **Message correlation**: Not mentioned
- ❌ **Compensation**: Not mentioned

#### Ours
- ✅ **Workflow history**: Implemented
- ✅ **Event system**: Implemented
- ⏳ **Message correlation**: Planned
- ⏳ **Compensation/rollback**: Planned

**Advantage**: We're planning enterprise features (message correlation, compensation) that petri_flow doesn't have.

## Architecture Comparison

### petri_flow Architecture
```
Rails Application
├── Petri Flow Engine (Ruby)
├── Database (MySQL/PostgreSQL)
├── Web UI (Rails views)
├── Form Builder
└── Organizational Integration
```

**Characteristics**:
- Rails-integrated
- Full-stack solution
- Database-centric
- Web UI included

### Our Architecture
```
Go Application
├── Workflow Engine (Go library, colored-token marking)
├── Storage Interface (pluggable)
│   ├── SQLite (implemented)
│   ├── PostgreSQL (implemented)
│   └── Custom implementations
├── YAML Configuration
├── History Store (pluggable)
└── Examples (web UI, etc.)
```

**Characteristics**:
- Standalone library
- Interface-based design
- Portable (YAML)
- Focus on engine, not UI

## Key Insights

### 1. What petri_flow Does Well

1. **Comprehensive Feature Set**: Full petri net features implemented
2. **Production-Ready**: Battle-tested in real applications
3. **Organizational Integration**: Enterprise-ready with role/group support
4. **Web UI**: Complete management interface
5. **Sub-Workflows**: HCPN implemented and working

### 2. What Ours Does Well

1. **CPN Shipped**: Data-carrying tokens, per-token firing, token-aware guards, token queries/transformation
2. **YAML Configuration**: More portable and version-control friendly
3. **Standalone Library**: Can be used in any Go application
4. **Interface-Based Design**: Pluggable storage (SQLite, PostgreSQL) and history
5. **Modern Expression Language**: expr-lang/expr is powerful and well-maintained
6. **Durability**: Persistent state with optimistic concurrency and transactional building blocks

### 3. What We Can Learn from petri_flow

1. **Token Selection Patterns**: How they handle token correlation and selection
2. **Sub-Workflow Implementation**: HCPN patterns and best practices
3. **Assignment Management**: How to handle user/role assignments
4. **Timed Transitions**: Implementation patterns for time-based workflows
5. **Iterative Routing**: How to handle loops and iterations

### 4. What We Should Avoid

1. **Rails Coupling**: Keep our library framework-agnostic
2. **UI in Core**: Keep UI as examples, not core features
3. **Database Lock-in**: Maintain storage interface flexibility

## Recommendations

### Short-Term

1. **Deepen CPN ergonomics** (core CPN is shipped):
   - Study petri_flow's token correlation logic for multi-place token matching
   - Richer token selection strategies

2. **Improve examples**:
   - Production-ready web UI example
   - REST API example

### Medium-Term

1. **Add database support**:
   - MySQL storage implementation (SQLite and PostgreSQL already ship)

2. **Enhance visualization**:
   - Token migration graphs
   - Real-time token visualization

### Long-Term (Future Features)

1. **HCPN Implementation**:
   - Study petri_flow's sub-workflow patterns
   - Implement using reserved interfaces

2. **Advanced Features**:
   - Message correlation
   - Compensation/rollback
   - Workflow versioning

3. **Enterprise Features** (Optional):
   - Role-based access control
   - Assignment management
   - Organizational structure integration

## Conclusion

**petri_flow** is a mature, production-ready workflow engine with comprehensive features. **Our Go workflow engine** is in active development, with CPN implemented and a clean, portable design.

**Key Takeaways**:
1. **Both engines have CPN implemented** - we can still learn from petri_flow's correlation patterns
2. **Our YAML approach is more portable** - better for version control and deployment
3. **Our standalone library design** - more flexible for different use cases
4. **Our HCPN-ready architecture** - designed for future growth (HCPN itself is not yet implemented)
5. **Our long-running process support** - persistent state with optimistic concurrency

**Recommendation**: Study petri_flow's implementation patterns, especially for token correlation and HCPN, while maintaining our design principles (portability, interface-based, standalone library).

## References

- [petri_flow GitHub](https://github.com/hooopo/petri_flow)
- [petri_flow Documentation](https://hooopo.gitbook.io/petri-flow/)
- [Our CPN Guide](../guides/CPN_GUIDE.md)
- [Our README](../../README.md)

