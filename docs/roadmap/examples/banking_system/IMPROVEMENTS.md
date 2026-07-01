# Banking System CPN Model: Improvements from Request Token Model

## Summary

The banking system CPN model has been enhanced from a **State Update Engine** (context-based) to a **True CPN Flow Model** (request token-based), providing maximum concurrency, mathematical rigor, and batch processing capabilities.

## Key Improvements

### 1. Explicit Request Tokens

**Before (Context-Based)**:
- Transaction data passed via `workflow.Context('target_account_id')` and `workflow.Context('amount')`
- Transitions acted as database queries/updates
- Concurrency handled by Go engine's internal locking

**After (Request Token Model)**:
- Transaction requests are **explicit tokens** in `deposit_requests` and `withdraw_requests` places
- Transitions consume tokens from **multiple places** simultaneously
- PN structure itself enforces serialization per account

### 2. Multi-Place Token Consumption

**Before**:
```yaml
from: [active_accounts]
to: [active_accounts]
token_selection: filter
token_filter: "token.account_id == workflow.Context('target_account_id')"
```

**After**:
```yaml
from: [active_accounts, deposit_requests]
to: [active_accounts]
token_correlation: |
  active_accounts.token.account_id == deposit_requests.token.account_id
```

### 3. Token Correlation

**New Feature**: Transitions match tokens from different places by attributes:
- Account token: `{account_id: 1, balance: 0}`
- Request token: `{account_id: 1, amount: 500}`
- Correlation: `1 == 1` ✅

### 4. Request Token Consumption

**New Feature**: Request tokens are consumed (not produced back):
```yaml
token_transform:
  active_accounts: |
    {
      "account_id": active_accounts.token.account_id,
      "balance": active_accounts.token.balance + deposit_requests.token.amount
    }
  deposit_requests: null  # Consumed, not produced
```

## Benefits

### 1. True CPN Mathematical Rigor

- Transitions fire only when tokens from **all input places** arrive simultaneously
- PN structure provides mathematical guarantees
- Not dependent on implementation-level locking

### 2. Concurrency Isolation

**Before**: Concurrency handled by Go engine's internal locking mechanism

**After**: PN structure **guarantees serialization** per account:
- Account token consumption prevents concurrent updates
- If two requests target same account, first transition consumes account token
- Second transition must wait for updated token to be produced
- **PN structure enforces serialization**, not implementation code

### 3. Natural Batch Processing

**Before**: Difficult to process multiple requests simultaneously

**After**: Natural batch processing:
```go
// Create 100 deposit request tokens
for i := 1; i <= 100; i++ {
    wf.CreateToken("deposit_requests", createDepositRequest(i, 100))
}

// Transitions fire concurrently:
// - Each pairs one account token with one request token
// - Different accounts can process simultaneously
// - Same account serialized by token consumption
```

### 4. Clear Token Flow Visualization

**Before**: Hard to visualize request lifecycle (implicit via context)

**After**: Explicit token flow:
- Request tokens visible in `deposit_requests` place
- Token correlation visible in transition
- Request consumption visible in transformation

### 5. Scalability

- Can handle thousands of concurrent requests
- Each account isolated by token consumption
- Batch processing of 100+ requests naturally

## Comparison Table

| Aspect | Context-Based Model | Request Token Model |
|--------|-------------------|-------------------|
| **Token Flow** | Implicit (via context) | Explicit (request tokens) |
| **Concurrency** | Engine-level locking | PN structure guarantees |
| **Batch Processing** | Difficult | Natural (queue requests) |
| **Mathematical Rigor** | State update engine | True CPN flow model |
| **Visualization** | Hard to visualize | Clear token flow |
| **Serialization** | Implementation-dependent | PN structure enforces |
| **Request Lifecycle** | Hidden in context | Explicit token lifecycle |

## Example: Concurrent Updates to Same Account

### Scenario
Two deposit requests for Account 1 arrive simultaneously:
- Request 1: `{account_id: 1, amount: 500}`
- Request 2: `{account_id: 1, amount: 300}`

### Process (Request Token Model)

1. **Initial State**:
   - `active_accounts`: `{account_id: 1, balance: 0}`
   - `deposit_requests`: `[{account_id: 1, amount: 500}, {account_id: 1, amount: 300}]`

2. **First Transition Fires**:
   - Consumes: `{account_id: 1, balance: 0}` + `{account_id: 1, amount: 500}`
   - Produces: `{account_id: 1, balance: 500}`
   - Consumes: `{account_id: 1, amount: 500}` (request token)

3. **Second Transition Fires** (after first completes):
   - Consumes: `{account_id: 1, balance: 500}` + `{account_id: 1, amount: 300}`
   - Produces: `{account_id: 1, balance: 800}`
   - Consumes: `{account_id: 1, amount: 300}` (request token)

4. **Final State**:
   - `active_accounts`: `{account_id: 1, balance: 800}`
   - `deposit_requests`: `[]` (both requests consumed)

**Key Point**: PN structure **guarantees serialization** - account token consumption prevents concurrent updates to the same account.

## Implementation Impact

### YAML Configuration Changes

1. **Multiple Token Schemas**:
   ```yaml
   token_schemas:
     account: {...}
     transaction_request: {...}
   ```

2. **Multiple Places**:
   ```yaml
   places:
     - name: active_accounts
     - name: deposit_requests
     - name: withdraw_requests
   ```

3. **Multi-Place Transitions**:
   ```yaml
   from: [active_accounts, deposit_requests]
   token_correlation: ...
   ```

4. **Token Transformation for Multiple Places**:
   ```yaml
   token_transform:
     active_accounts: {...}
     deposit_requests: null
   ```

### Code Changes

**Before**:
```go
wf.SetContext("target_account_id", 1)
wf.SetContext("amount", 500)
wf.ApplyTransition("deposit")
```

**After**:
```go
requestToken := Token{
    Data: TokenData{
        "account_id": 1,
        "amount": 500,
        "operation_type": "deposit",
        "request_id": "req-001",
    },
}
wf.CreateToken("deposit_requests", requestToken)
// Transition fires automatically when both tokens available
```

## Conclusion

The request token model transforms the banking system from a **State Update Engine** to a **True CPN Flow Model**, providing:

- ✅ Mathematical rigor through PN structure
- ✅ Concurrency isolation via token consumption
- ✅ Natural batch processing capabilities
- ✅ Clear token flow visualization
- ✅ Scalability for thousands of concurrent requests

This improvement makes the model suitable for large-scale, concurrent batch processing while maintaining mathematical guarantees through the PN structure itself.

