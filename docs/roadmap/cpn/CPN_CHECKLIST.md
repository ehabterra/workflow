# Smart Tokens (CPN) Implementation Checklist

## Quick Reference

**Goal**: Enable multiple tokens per place with data attributes (Colored Petri Nets)

**Timeline**: 14-19 weeks (3.5-5 months)

**Status**: 📋 Planning Phase

---

## Phase 1: Core Token Model (2-3 weeks) 🔴 Critical

### Token Data Structure
- [ ] Create `token.go` with `Token`, `TokenID`, `TokenData` types
- [ ] Implement token creation, comparison, and equality
- [ ] Add token validation helpers
- [ ] Write unit tests (`token_test.go`)

### Enhanced Marking Interface
- [ ] Extend `Marking` interface with CPN methods:
  - [ ] `TokensAt(place Place) []Token`
  - [ ] `TokenCount(place Place) int`
  - [ ] `AddToken(place Place, token Token) error`
  - [ ] `RemoveToken(place Place, tokenID TokenID) error`
  - [ ] `HasToken(place Place, tokenID TokenID) bool`
- [ ] Create `CPNMarking` implementation
- [ ] Maintain backward compatibility with boolean marking
- [ ] Update `marking_test.go` with CPN tests

### Workflow Token Support
- [ ] Add `IsCPNEnabled() bool` to `Workflow`
- [ ] Add token creation methods: `CreateToken(place, data)`, `CreateTokens(place, []data)`
- [ ] Add token query methods: `GetTokens(place)`, `FindTokens(condition)`
- [ ] Update workflow initialization to support CPN mode
- [ ] Write tests for workflow token operations

**Deliverable**: Token model with marking support, backward compatible

---

## Phase 2: Transition Token Selection (3-4 weeks) 🔴 Critical

### Token Selection in Transitions
- [ ] Add token selection strategy to `Transition`:
  - [ ] `first` - Take first token from each input place
  - [ ] `all` - Take all tokens from input places
  - [ ] `filter` - Take tokens matching filter expression
  - [ ] `custom` - User-defined selection function
- [ ] Implement token filtering based on attributes
- [ ] Add token consumption logic (remove from input places)
- [ ] Write tests for token selection strategies

### Guard Expression Enhancement
- [ ] Extend expression environment with token context:
  - [ ] `token` - Current token being evaluated
  - [ ] `tokens` - All tokens in current selection
  - [ ] Helper functions: `token.Attribute('key')`, `token.Count()`, `token.Filter(condition)`
- [ ] Update `GuardEvent` to include selected tokens
- [ ] Support token-level guards vs workflow-level guards
- [ ] Update `expression.go` and `event.go`
- [ ] Write tests for token-aware expressions

### Transition Application Logic
- [ ] Update `Apply` and `ApplyTransition` to handle tokens:
  - [ ] Select tokens from input places
  - [ ] Validate tokens (guard evaluation)
  - [ ] Transform token data (optional)
  - [ ] Remove tokens from input places
  - [ ] Add tokens to output places
- [ ] Handle backward compatibility (boolean marking)
- [ ] Update `Can` and `CanTransition` for token validation
- [ ] Write comprehensive tests

**Deliverable**: Transitions can select, validate, and route tokens

---

## Phase 3: Storage Layer Extension (2-3 weeks) 🟡 High

### Storage Schema Extension
- [ ] Design token storage schema (JSON in existing table)
- [ ] Add `tokens` column to workflow_states table
- [ ] Create migration helper for existing workflows
- [ ] Support backward compatibility (boolean marking)

### Storage Interface Extension
- [ ] Extend `Storage` interface:
  - [ ] `SaveTokens(id string, tokens map[Place][]Token) error`
  - [ ] `LoadTokens(id string) (map[Place][]Token, error)`
- [ ] Update `SaveState` and `LoadState` to handle tokens
- [ ] Implement in `storage/sqlite.go`
- [ ] Write storage tests

### YAML Storage Configuration
- [ ] Extend YAML storage config for token storage
- [ ] Add token schema definition in YAML
- [ ] Update `yaml/storage_setup.go` and `yaml/storage_sqlite.go`
- [ ] Write YAML storage tests

**Deliverable**: Tokens persist and load correctly from storage

---

## Phase 4: YAML Configuration Support (2 weeks) 🟢 Medium

### YAML Token Definition
- [ ] Add `cpn_enabled: true` flag to workflow config
- [ ] Add `token_schema` definition:
  ```yaml
  token_schema:
    order_id: string
    amount: number
    customer: string
  ```
- [ ] Add token selection strategies to transitions:
  ```yaml
  token_selection: filter
  token_filter: "token.amount > 1000"
  ```
- [ ] Add token routing configuration:
  ```yaml
  token_routing:
    - condition: "token.amount <= 1000"
      to: auto_approve
    - condition: "token.amount > 1000"
      to: manager_approval
  ```

### YAML Loader Updates
- [ ] Parse token schema from YAML (`yaml/config.go`)
- [ ] Create tokens from YAML definitions (`yaml/loader.go`)
- [ ] Load token data from storage
- [ ] Validate token schemas
- [ ] Write YAML loader tests

**Deliverable**: YAML configuration supports CPN workflows

---

## Phase 5: Advanced Features (3-4 weeks) 🟢 Medium

### Token Transformation
- [ ] Support token data modification during transitions
- [ ] Add transformation expressions: `token_transform: "token.amount * 1.1"`
- [ ] Support token merging/splitting
- [ ] Create `token_transformation.go`
- [ ] Write transformation tests

### Batch Operations
- [ ] Support batch token creation: `CreateTokens(place, []TokenData)`
- [ ] Batch transition application
- [ ] Atomic batch operations
- [ ] Update `workflow.go` and `manager.go`
- [ ] Write batch operation tests

### Token Queries
- [ ] Add query methods:
  - [ ] `FindTokens(place, condition) []Token`
  - [ ] `CountTokens(place, condition) int`
  - [ ] `AggregateTokens(place, function) interface{}`
- [ ] Support token aggregation (sum, avg, min, max)
- [ ] Add token iteration helpers
- [ ] Write query tests

**Deliverable**: Advanced CPN features for complex workflows

---

## Phase 6: Testing and Documentation (2-3 weeks) 🟡 High

### Comprehensive Testing
- [ ] Unit tests for token model (90%+ coverage)
- [ ] Integration tests for CPN workflows
- [ ] Migration tests (boolean → CPN)
- [ ] Performance tests (batch operations with 100+ tokens)
- [ ] Backward compatibility tests (all existing tests pass)
- [ ] Concurrent access tests

### Documentation
- [ ] Update `README.md` with CPN section
- [ ] Create `CPN_GUIDE.md` with:
  - [ ] Overview and concepts
  - [ ] Usage examples
  - [ ] Best practices
  - [ ] Common patterns
- [ ] Create `CPN_MIGRATION.md` with migration steps
- [ ] Add API documentation (godoc comments)
- [ ] Create example project: `examples/cpn_batch_processing/`
- [ ] Add CPN examples to existing examples

**Deliverable**: Well-tested and documented CPN feature

---

## Quick Start Tasks (First Week)

1. **Day 1-2**: Create token model (`token.go`, `token_test.go`)
2. **Day 3-4**: Extend marking interface (`marking.go`)
3. **Day 5**: Add workflow token support (`workflow.go`)
4. **Weekend**: Review and refine design

---

## Key Design Decisions

- ✅ **Backward Compatibility**: Support both boolean and CPN marking
- ✅ **Token Storage**: JSON in existing table (can optimize later)
- ✅ **Token Selection**: Multiple strategies (first, all, filter, custom)
- ✅ **Expression Environment**: Extend with token context

---

## Success Criteria

### Functional
- [x] Can create 100+ tokens in one place
- [ ] Transitions route tokens based on attributes
- [ ] Storage persists and loads tokens correctly
- [ ] Expression guards evaluate token attributes
- [ ] YAML configuration supports CPN

### Performance
- [ ] Token creation: < 1ms per token
- [ ] Transition with 100 tokens: < 100ms
- [ ] Storage save/load: < 50ms for 100 tokens

### Quality
- [ ] Test coverage: > 90%
- [ ] Backward compatibility: 100% existing tests pass
- [ ] Documentation: All features documented

---

## Notes

- Start with Phase 1 and get it reviewed before proceeding
- Maintain backward compatibility throughout
- Write tests as you go, not at the end
- Document design decisions in code comments
- Regular code reviews recommended

---

**Last Updated**: [Current Date]  
**Status**: Planning Complete, Ready for Implementation

