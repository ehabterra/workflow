# Banking System CPN Model Explanation (True CPN with Request Tokens)

## Overview

This document explains the **True Colored Petri Net (CPN) model** for a simple banking system. This model uses **explicit request tokens** to achieve maximum concurrency, mathematical rigor, and batch processing capabilities.

## System Requirements

- **1000 accounts** numbered from 1 to 1000
- **Operations**: Deposit and Withdraw
- **Amount constraints**: 1-5000 EURO per transaction
- **Negative balance**: Allowed

## Key Improvement: Request Tokens

### Previous Model (State Update Engine)

The initial model relied on **global context** to pass transaction data:
- `workflow.Context('target_account_id')` and `workflow.Context('amount')`
- Transitions acted as database queries/updates
- Concurrency handled by Go engine's internal locking

**Limitations**:
- Not a true CPN flow model
- Cannot easily handle batch processing
- Concurrency guarantees depend on implementation, not PN structure

### Current Model (True CPN Flow Model)

The improved model uses **explicit request tokens**:
- Request tokens flow into transitions alongside account tokens
- Transitions fire only when **both tokens arrive simultaneously**
- PN structure itself enforces serialization per account
- Supports batch processing naturally

**Benefits**:
- ✅ True CPN mathematical rigor
- ✅ PN structure guarantees concurrency isolation
- ✅ Natural batch processing (100 request tokens processed concurrently)
- ✅ Clear token flow visualization

## CPN Model Structure

### 1. Token Schemas (Token "Colors")

#### Account Token Schema

Each account is represented as a **token** with attributes:

```yaml
token_schemas:
  account:
    account_id: integer  # Account number (1-1000)
    balance: number     # Current balance (can be negative)
```

**Example Account Tokens**:
```json
{
  "id": "token-account-1",
  "data": {
    "account_id": 1,
    "balance": 1500.50
  }
}
{
  "id": "token-account-2", 
  "data": {
    "account_id": 2,
    "balance": -200.00  // Negative balance allowed
  }
}
```

#### Transaction Request Token Schema

Each transaction request is a **token** with attributes:

```yaml
token_schemas:
  transaction_request:
    account_id: integer   # Target account number
    amount: number        # Transaction amount
    operation_type: string # "deposit" or "withdraw"
    request_id: string    # Unique request identifier
```

**Example Request Tokens**:
```json
{
  "id": "request-deposit-1",
  "data": {
    "account_id": 1,
    "amount": 500,
    "operation_type": "deposit",
    "request_id": "req-001"
  }
}
{
  "id": "request-withdraw-2",
  "data": {
    "account_id": 2,
    "amount": 300,
    "operation_type": "withdraw",
    "request_id": "req-002"
  }
}
```

### 2. Places

#### Place: `active_accounts`
- Holds all 1000 account tokens
- Token schema: `account`
- All accounts remain in this place throughout their lifecycle
- Tokens are consumed and produced back during operations

#### Place: `deposit_requests`
- Holds incoming deposit request tokens
- Token schema: `transaction_request`
- Request tokens are consumed by `deposit` transition

#### Place: `withdraw_requests`
- Holds incoming withdraw request tokens
- Token schema: `transaction_request`
- Request tokens are consumed by `withdraw` transition

```
┌─────────────────────┐     ┌──────────────────┐     ┌──────────────────┐
│  active_accounts    │     │ deposit_requests │     │withdraw_requests │
│  (1000 tokens)      │     │  (request queue) │     │  (request queue) │
│  ┌────────────────┐ │     │  ┌──────────────┐ │     │  ┌──────────────┐ │
│  │ Token 1: {1, 0}│ │     │  │ Req 1: {1,500}│ │     │  │ Req 1: {2,300}│ │
│  │ Token 2: {2, 0}│ │     │  │ Req 2: {3,200}│ │     │  │ Req 2: {4,100}│ │
│  │ ...            │ │     │  │ ...          │ │     │  │ ...          │ │
│  │ Token 1000:...│ │     │  └──────────────┘ │     │  └──────────────┘ │
│  └────────────────┘ │     └──────────────────┘     └──────────────────┘
└─────────────────────┘
```

### 3. Transitions

#### Transition: `deposit`

**Purpose**: Add money to an account

**Key Feature**: Consumes tokens from **TWO places** simultaneously

**Flow**:
1. **Token Arrival**: Both tokens must arrive:
   - Account token from `active_accounts` (e.g., `{account_id: 1, balance: 0}`)
   - Request token from `deposit_requests` (e.g., `{account_id: 1, amount: 500}`)

2. **Token Correlation**: Match `account_id` between tokens
   ```yaml
   token_correlation: |
     active_accounts.token.account_id == deposit_requests.token.account_id
   ```

3. **Guard Validation**: Check `amount >= 1 AND amount <= 5000`
   ```yaml
   guard: |
     deposit_requests.token.amount >= 1 and 
     deposit_requests.token.amount <= 5000
   ```

4. **Token Transformation**:
   - **Account Token**: Update balance → `balance = balance + amount`
   - **Request Token**: Consumed (not produced back)

5. **Token Production**:
   - Updated account token → `active_accounts`
   - Request token → consumed (null)

**Token Transformation**:
```yaml
token_transform:
  active_accounts: |
    {
      "account_id": active_accounts.token.account_id,
      "balance": active_accounts.token.balance + deposit_requests.token.amount
    }
  deposit_requests: null  # Consumed, not produced
```

#### Transition: `withdraw`

**Purpose**: Remove money from an account

**Flow**: Same as deposit, but:
- Consumes from `active_accounts` and `withdraw_requests`
- Updates balance: `balance = balance - amount` (can go negative)

## CPN Model Diagram (True CPN Flow)

```
                    ┌──────────────────┐
                    │ active_accounts  │
                    │  (1000 tokens)  │
                    └────────┬─────────┘
                             │
                             │
        ┌────────────────────┼────────────────────┐
        │                    │                    │
        │                    │                    │
┌───────▼────────┐   ┌───────▼────────┐   ┌───────▼────────┐
│deposit_requests│   │withdraw_requests│   │                │
│  (request queue)│   │  (request queue)│   │                │
└───────┬────────┘   └───────┬────────┘   │                │
        │                    │              │                │
        │                    │              │                │
        └──────────┬─────────┘              │                │
                   │                        │                │
            ┌──────▼────────┐       ┌──────▼────────┐       │
            │   deposit     │       │   withdraw    │       │
            │               │       │               │       │
            │ Correlation:  │       │ Correlation:  │       │
            │ acc.id==req.id│       │ acc.id==req.id│       │
            │               │       │               │       │
            │ Guard:        │       │ Guard:        │       │
            │ 1≤amt≤5000    │       │ 1≤amt≤5000    │       │
            │               │       │               │       │
            │ Transform:    │       │ Transform:    │       │
            │ bal += amt    │       │ bal -= amt    │       │
            │ req → null    │       │ req → null    │       │
            └───────┬───────┘       └───────┬───────┘       │
                    │                      │                │
                    └──────────┬───────────┘                │
                               │                            │
                    ┌──────────▼──────────┐                │
                    │  active_accounts    │                │
                    │  (updated tokens)   │                │
                    └─────────────────────┘                │
```

## Workflow Execution Examples

### Initial State

**Workflow Instance**: `banking-system-1`

**Context**:
```json
{
  "system_name": "Simple Banking System (True CPN Model)",
  "max_accounts": 1000,
  "min_amount": 1,
  "max_amount": 5000
}
```

**Marking**:
```json
{
  "active_accounts": [
    {"id": "t1", "data": {"account_id": 1, "balance": 0}},
    {"id": "t2", "data": {"account_id": 2, "balance": 0}},
    ...
    {"id": "t1000", "data": {"account_id": 1000, "balance": 0}}
  ],
  "deposit_requests": [],
  "withdraw_requests": []
}
```

### Example 1: Deposit 500 EURO to Account 1

**Step 1**: Create deposit request token
```go
requestToken := Token{
    ID: TokenID("req-deposit-1"),
    Data: TokenData{
        "account_id": 1,
        "amount": 500,
        "operation_type": "deposit",
        "request_id": "req-001",
    },
}
wf.CreateToken("deposit_requests", requestToken)
```

**Step 2**: Transition fires automatically when both tokens available
- Account token: `{account_id: 1, balance: 0}` from `active_accounts`
- Request token: `{account_id: 1, amount: 500}` from `deposit_requests`

**Process**:
1. **Token Correlation**: `1 == 1` ✅
2. **Guard Evaluation**: `500 >= 1 AND 500 <= 5000` ✅
3. **Token Transformation**:
   - Account: `{account_id: 1, balance: 0}` → `{account_id: 1, balance: 500}`
   - Request: Consumed (null)
4. **Token Production**:
   - Updated account token → `active_accounts`
   - Request token → consumed

**Result**: Account 1 balance = 500, request token consumed

### Example 2: Batch Processing (100 Concurrent Deposits)

**Step 1**: Create 100 deposit request tokens
```go
for i := 1; i <= 100; i++ {
    requestToken := Token{
        ID: TokenID(fmt.Sprintf("req-deposit-%d", i)),
        Data: TokenData{
            "account_id": i,
            "amount": 100,
            "operation_type": "deposit",
            "request_id": fmt.Sprintf("req-%03d", i),
        },
    }
    wf.CreateToken("deposit_requests", requestToken)
}
```

**Step 2**: Transitions fire concurrently
- Each `deposit` transition pairs one account token with one request token
- 100 transitions can fire simultaneously (different accounts)
- PN structure ensures each account token is consumed by only one transition

**Result**: All 100 deposits processed concurrently, each account updated atomically

### Example 3: Concurrent Updates to Same Account (Serialization)

**Scenario**: Two deposit requests for Account 1 arrive simultaneously

**Request 1**: `{account_id: 1, amount: 500}`
**Request 2**: `{account_id: 1, amount: 300}`

**Process**:
1. Both request tokens in `deposit_requests` place
2. Account token `{account_id: 1, balance: 0}` in `active_accounts`
3. **First transition fires**:
   - Consumes account token `{account_id: 1, balance: 0}`
   - Consumes request token `{account_id: 1, amount: 500}`
   - Produces account token `{account_id: 1, balance: 500}`
4. **Second transition fires** (after first completes):
   - Consumes account token `{account_id: 1, balance: 500}`
   - Consumes request token `{account_id: 1, amount: 300}`
   - Produces account token `{account_id: 1, balance: 800}`

**Key Point**: PN structure **guarantees serialization** - account token consumption prevents concurrent updates to the same account.

## Key CPN Concepts Demonstrated

### 1. Multi-Place Token Consumption
- Transitions consume tokens from **multiple places** simultaneously
- Both tokens must be available for transition to fire

### 2. Token Correlation
- Match tokens from different places by attributes
- `active_accounts.token.account_id == deposit_requests.token.account_id`

### 3. Guard Constraints
- Validate transaction amount: `1 <= amount <= 5000`
- Prevents invalid operations

### 4. Token Transformation
- Update account token data during transition
- Consume request token (null output)

### 5. Concurrency Isolation
- PN structure enforces serialization per account
- Account token consumption prevents concurrent updates

## Advantages of Request Token Model

### 1. True CPN Mathematical Rigor
- Transitions fire only when tokens from all input places arrive
- PN structure provides mathematical guarantees
- Not dependent on implementation-level locking

### 2. Natural Batch Processing
- Multiple request tokens can be queued
- Transitions fire concurrently for different accounts
- Single workflow instance processes all requests

### 3. Concurrency Isolation
- Account token consumption prevents concurrent updates
- PN structure guarantees serialization per account
- No need for external locking mechanisms

### 4. Clear Token Flow
- Request tokens flow through the system
- Easy to visualize and debug
- Request lifecycle is explicit

### 5. Scalability
- Can handle thousands of concurrent requests
- Each account isolated by token consumption
- Batch processing of 100+ requests naturally

## Comparison: Context-Based vs Request Token Model

| Aspect | Context-Based Model | Request Token Model |
|--------|-------------------|-------------------|
| **Token Flow** | Implicit (via context) | Explicit (request tokens) |
| **Concurrency** | Engine-level locking | PN structure guarantees |
| **Batch Processing** | Difficult | Natural (queue requests) |
| **Mathematical Rigor** | State update engine | True CPN flow model |
| **Visualization** | Hard to visualize | Clear token flow |
| **Serialization** | Implementation-dependent | PN structure enforces |

## Implementation Notes

### Request Token Creation

```go
// Create deposit request
requestToken := Token{
    ID: TokenID(fmt.Sprintf("req-deposit-%s", requestID)),
    Data: TokenData{
        "account_id": targetAccountID,
        "amount": amount,
        "operation_type": "deposit",
        "request_id": requestID,
    },
}
wf.CreateToken("deposit_requests", requestToken)

// Transition fires automatically when:
// 1. Account token available in active_accounts
// 2. Request token available in deposit_requests
// 3. Token correlation matches (account_id)
// 4. Guard passes (amount validation)
```

### Batch Processing

```go
// Create 100 deposit requests
for i := 1; i <= 100; i++ {
    wf.CreateToken("deposit_requests", createDepositRequest(i, 100))
}

// Transitions fire concurrently:
// - Each pairs one account token with one request token
// - Different accounts can process simultaneously
// - Same account serialized by token consumption
```

## Extensions (Future)

Possible enhancements:
- **Transaction History**: Add history tracking to request tokens
- **Request Priority**: Add priority attribute to request tokens
- **Request Timeout**: Add timeout mechanism for stale requests
- **Transfer Operation**: Move tokens between accounts (two account tokens + one request token)
- **Account Creation/Closure**: Add/remove account tokens dynamically
