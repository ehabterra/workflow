> ⚠️ **HISTORICAL PLANNING DOCUMENT — DO NOT USE AS A REFERENCE FOR THE SHIPPED API.**
> The YAML in this example (`banking_cpn.yaml`) uses a **proposed schema**
> (`cpn_enabled`, `token_schemas`, `initial_place`, ...) that was **never implemented**;
> the strict YAML loader will **reject it with an error**. The shipped Colored Petri Net
> mechanism is the polymorphic `initial_marking` key — see
> [`docs/guides/CPN_GUIDE.md`](../../../guides/CPN_GUIDE.md) and the runnable
> [`examples/cpn_batch_processing`](../../../../examples/cpn_batch_processing) example.

# Banking System CPN Model (True CPN with Request Tokens)

This example demonstrates a **True Colored Petri Net (CPN) model** for a simple banking system using **explicit request tokens** for maximum concurrency, mathematical rigor, and batch processing capabilities.

## Requirements

- **1000 accounts** numbered from 1 to 1000
- **Operations**: Deposit and Withdraw
- **Amount constraints**: 1-5000 EURO per transaction
- **Negative balance**: Allowed

## Key Improvement: Request Token Model

This model uses **explicit request tokens** instead of global context, providing:

- ✅ **True CPN mathematical rigor**: Transitions fire when tokens from multiple places arrive
- ✅ **PN structure guarantees**: Concurrency isolation enforced by token consumption
- ✅ **Natural batch processing**: Multiple request tokens processed concurrently
- ✅ **Clear token flow**: Request lifecycle is explicit and visualizable

## CPN Model Design

### Token Schemas

#### Account Token
Each account is represented as a **token** with attributes:

```json
{
  "account_id": 1,
  "balance": 1500.50
}
```

#### Transaction Request Token
Each transaction request is a **token** with attributes:

```json
{
  "account_id": 1,
  "amount": 500,
  "operation_type": "deposit",
  "request_id": "req-001"
}
```

### Places

- **`active_accounts`**: Holds all 1000 account tokens
- **`deposit_requests`**: Queue of deposit request tokens
- **`withdraw_requests`**: Queue of withdraw request tokens

### Transitions

1. **`deposit`**: Add money to an account
   - **Consumes**: Account token from `active_accounts` + Request token from `deposit_requests`
   - **Token Correlation**: Matches `account_id` between tokens
   - **Guard**: Validates amount (1-5000)
   - **Transform**: Updates account balance, consumes request token

2. **`withdraw`**: Remove money from an account
   - **Consumes**: Account token from `active_accounts` + Request token from `withdraw_requests`
   - **Token Correlation**: Matches `account_id` between tokens
   - **Guard**: Validates amount (1-5000)
   - **Transform**: Updates account balance, consumes request token

## Visual Model

```
                    ┌──────────────────┐
                    │ active_accounts  │
                    │  (1000 tokens)  │
                    └────────┬─────────┘
                             │
        ┌────────────────────┼────────────────────┐
        │                    │                    │
┌───────▼────────┐   ┌───────▼────────┐         │
│deposit_requests│   │withdraw_requests│         │
│  (request queue)│   │  (request queue)│         │
└───────┬────────┘   └───────┬────────┘         │
        │                    │                    │
        └──────────┬─────────┘                    │
                   │                               │
            ┌──────▼────────┐       ┌──────▼────────┐
            │   deposit     │       │   withdraw    │
            │               │       │               │
            │ Correlation:  │       │ Correlation:  │
            │ acc.id==req.id│       │ acc.id==req.id│
            │               │       │               │
            │ Guard:        │       │ Guard:        │
            │ 1≤amt≤5000    │       │ 1≤amt≤5000    │
            │               │       │               │
            │ Transform:    │       │ Transform:    │
            │ bal += amt    │       │ bal -= amt    │
            │ req → null    │       │ req → null    │
            └───────┬───────┘       └───────┬───────┘
                    │                      │
                    └──────────┬───────────┘
                               │
                    ┌──────────▼──────────┐
                    │  active_accounts    │
                    │  (updated tokens)   │
                    └─────────────────────┘
```

## Files

- **`banking_cpn.yaml`**: YAML workflow configuration
- **`CPN_MODEL_EXPLANATION.md`**: Detailed explanation of the CPN model
- **`main.go`**: Go code example (conceptual)
- **`README.md`**: This file

## Usage Example

### Initialize Accounts

```go
// Create workflow
wf := manager.CreateWorkflow("banking-system-1", definition, "active_accounts")

// Initialize 1000 account tokens
for i := 1; i <= 1000; i++ {
    token := Token{
        ID: TokenID(fmt.Sprintf("account-%d", i)),
        Data: TokenData{
            "account_id": i,
            "balance": 0.0,
        },
    }
    wf.CreateToken("active_accounts", token)
}
```

### Deposit Operation (Request Token Model)

```go
// Create deposit request token
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

// Transition fires automatically when:
// 1. Account token available (account_id=1)
// 2. Request token available (account_id=1)
// 3. Token correlation matches
// 4. Guard passes (amount validation)
// Result: Account 1 balance updated from 0 to 500, request consumed
```

### Withdraw Operation (Request Token Model)

```go
// Create withdraw request token
requestToken := Token{
    ID: TokenID("req-withdraw-1"),
    Data: TokenData{
        "account_id": 1,
        "amount": 300,
        "operation_type": "withdraw",
        "request_id": "req-002",
    },
}
wf.CreateToken("withdraw_requests", requestToken)

// Transition fires automatically
// Result: Account 1 balance updated from 500 to 200, request consumed
```

### Batch Processing (100 Concurrent Deposits)

```go
// Create 100 deposit request tokens
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

// Transitions fire concurrently:
// - Each pairs one account token with one request token
// - Different accounts process simultaneously
// - Same account serialized by token consumption
```

### Invalid Operation (Guard Fails)

```go
// Create request with invalid amount
requestToken := Token{
    ID: TokenID("req-deposit-invalid"),
    Data: TokenData{
        "account_id": 1,
        "amount": 6000,  // Exceeds max (5000)
        "operation_type": "deposit",
        "request_id": "req-invalid",
    },
}
wf.CreateToken("deposit_requests", requestToken)

// Transition fires but guard fails
// Result: Request token remains in queue (guard blocks transition)
```

## Key CPN Concepts

1. **Multi-Place Token Consumption**: Transitions consume tokens from multiple places simultaneously
2. **Token Correlation**: Match tokens from different places by attributes (`account_id`)
3. **Guard Constraints**: Validate amount (1-5000)
4. **Token Transformation**: Update account balance, consume request token
5. **Concurrency Isolation**: PN structure enforces serialization per account via token consumption
6. **Batch Processing**: Multiple request tokens processed concurrently

## Implementation Status

This example is **conceptual** and requires CPN implementation (Phase 1-6 of the implementation plan).

Current status:
- ✅ CPN model design complete
- ✅ YAML configuration defined
- ⏳ CPN implementation in progress
- ⏳ Token support pending
- ⏳ Token selection pending
- ⏳ Token transformation pending

## Advantages of Request Token Model

1. **True CPN Mathematical Rigor**: Transitions fire only when tokens from all input places arrive
2. **PN Structure Guarantees**: Concurrency isolation enforced by token consumption (not implementation locking)
3. **Natural Batch Processing**: Multiple request tokens queued and processed concurrently
4. **Clear Token Flow**: Request lifecycle is explicit and visualizable
5. **Scalability**: Can handle thousands of concurrent requests with proper serialization per account
6. **Mathematical Guarantees**: PN structure provides concurrency guarantees independent of implementation

