# CPN Mathematical Concepts Explained (Plain Language)

This document explains the mathematical concepts from the [CPN Tools presentation](https://cpntools.org/wp-content/uploads/2018/01/cpn.pdf) in plain language, using our banking system example.

## Overview

CPN (Colored Petri Nets) uses **Standard ML** syntax to define token types and values. Think of it as a way to give "colors" (data attributes) to tokens, just like we did with `account_id` and `balance` in our banking system.

## 1. Basic Types (The Building Blocks)

CPN supports these basic data types:

### Integers (`int`)
- Whole numbers: `5`, `34234`, `-32423` (note: `~32423` means -32423 in ML)
- **In our banking system**: `account_id: integer` (1-1000)

### Reals (`real`)
- Decimal numbers: `34.34`, `23.0`, `7000.0` (written as `7e3`), `0.04` (written as `4e~2`)
- **In our banking system**: `balance: number` (can be `1500.50`, `-200.00`)

### Strings (`string`)
- Text: `"Hello"`, `"account-001"`
- **In our banking system**: `operation_type: "deposit"` or `"withdraw"`

### Booleans (`bool`)
- True or false: `true`, `false`
- Used in guards: `amount >= 1 and amount <= 5000`

### Unit (`unit`)
- Special type with only one value: `()`
- Represents "black" (uncolored) tokens - tokens without data
- **Not used in our banking system** (all our tokens have data)

## 2. Basic Operators (Doing Math)

### Arithmetic Operators
- `+`, `-`, `*`: Addition, subtraction, multiplication
- `/`: Division for reals
- `div`, `mod`: Integer division and remainder
  - Example: `28 div 10 = 2` (28 divided by 10 = 2)
  - Example: `28 mod 10 = 8` (remainder when dividing 28 by 10)

### Comparison Operators
- `=`, `>`, `<`, `>=`, `<=`, `<>`: Compare values
  - `>=` means "greater than or equal to"
  - `<=` means "less than or equal to"
  - `<>` means "not equal to"

### String Concatenation
- `^`: Join strings together
  - Example: `"AA" ^ "BB" = "AABB"`

**In our banking system**:
```yaml
guard: |
  deposit_requests.token.amount >= 1 and 
  deposit_requests.token.amount <= 5000
```
This uses `>=` and `<=` to check if amount is between 1 and 5000.

## 3. Logical Operators (Making Decisions)

- `not`: Negation (opposite)
  - `not (1=1)` = `false` (because 1=1 is true, so not true = false)

- `andalso`: Logical AND (both must be true)
  - `(1=1) andalso (2>1)` = `true` (both are true)

- `orelse`: Logical OR (at least one must be true)
  - `(1=1) orelse (2>3)` = `true` (first is true)

- `if then else`: Conditional choice
  - `if (1=1) then 3 else 4` = `3` (if condition is true, use first value)

**In our banking system**:
```yaml
guard: |
  deposit_requests.token.amount >= 1 and 
  deposit_requests.token.amount <= 5000
```
This is equivalent to: `(amount >= 1) andalso (amount <= 5000)`

## 4. Color Set Declarations (Defining Token Types)

A **color set** is just a fancy name for a **token type** or **data type**.

### Simple Color Sets
```ml
color I = int;        // I is a type for integers
color S = string;     // S is a type for strings
color B = bool;       // B is a type for booleans
```

**In our banking system** (YAML equivalent):
```yaml
token_schemas:
  account:
    account_id: integer  # Like: color AccountID = int;
    balance: number       # Like: color Balance = real;
```

### Creating Subtypes (Restricted Ranges)

You can limit the possible values:

```ml
color Age = int with 0..130;           // Age must be 0-130
color Temp = int with 30..40;          // Temperature must be 30-40
color Alphabet = string with "a".."z"; // String from "a" to "z"
```

**In our banking system** (conceptual):
```yaml
account_id: integer with 1..1000  # Account IDs 1-1000
amount: number with 1..5000        # Amounts 1-5000 EURO
```

### Creating New Types (Enumerations)

You can create types with specific allowed values:

```ml
color Human = with man | woman | child;
color ThreeColors = with Green | Red | Yellow;
```

**In our banking system**:
```yaml
operation_type: string with "deposit" | "withdraw"
```

## 5. Product Types (Tuples - Multiple Values Together)

A **product** is like a tuple - multiple values grouped together:

```ml
color Coordinates = product I * I * I;  // Three integers: (x, y, z)
color HumanAge = product Human * Age;    // Human and Age together: (man, 50)
```

**Example values**:
- `(1, 2, 3)` - three integers
- `(man, 50)` - a human type and an age

**In our banking system** (conceptual):
```yaml
# We could represent an account as a product:
color Account = product AccountID * Balance;
# Value: (1, 1500.50) means account 1 with balance 1500.50
```

## 6. Record Types (Named Fields)

A **record** is like a struct or object - values with named fields:

```ml
color CoordinatesR = record x:I * y:I * z:I;
color CD = record artists:S * title:S * noftracks:I;
```

**Example values**:
- `{x=1, y=2, z=3}` - coordinates with named fields
- `{artists="Beatles", title="Abbey Road", noftracks=10}` - CD record

**In our banking system** (this is what we use!):
```yaml
token_schemas:
  account:
    account_id: integer
    balance: number
```

This is equivalent to:
```ml
color Account = record account_id:int * balance:real;
```

**Example token value**:
```json
{
  "account_id": 1,
  "balance": 1500.50
}
```

In ML notation: `{account_id=1, balance=1500.50}`

## 7. List Types (Collections)

A **list** is an ordered collection of values:

```ml
color Names = list S;                    // List of strings
color ListOfColors = list ThreeColors;    // List of color values
```

**Example values**:
- `["John", "Liza", "Paul"]` - list of names
- `[Green, Red, Yellow]` - list of colors
- `[]` - empty list

**In our banking system** (conceptual):
```yaml
# If we had multiple accounts in one token:
accounts: list account
# Value: [{account_id=1, balance=0}, {account_id=2, balance=100}]
```

## 8. Operations on Lists and Records

### List Operations
- `[]`: Empty list
- `^^`: Concatenate two lists
  - `[1,2,3] ^^ [4,5]` = `[1,2,3,4,5]`
- `::`: Add element to front of list
  - `"a" :: ["b","c"]` = `["a","b","c"]`

### Record Operations
- `#field`: Extract a field from a record
  - `#x {x=1, y=2}` = `1` (get the x field)

**In our banking system**:
```yaml
token_transform:
  active_accounts: |
    {
      "account_id": active_accounts.token.account_id,  # Extract account_id
      "balance": active_accounts.token.balance + deposit_requests.token.amount
    }
```

This extracts `account_id` and `balance` from the account token, and `amount` from the request token.

## 9. How This Relates to Our Banking System

### Our Account Token Type
```yaml
token_schemas:
  account:
    account_id: integer
    balance: number
```

**In CPN/ML notation**:
```ml
color AccountID = int with 1..1000;
color Balance = real;
color Account = record account_id:AccountID * balance:Balance;
```

**Example token value**:
```ml
{account_id=1, balance=1500.50} : Account
```

### Our Request Token Type
```yaml
token_schemas:
  transaction_request:
    account_id: integer
    amount: number
    operation_type: string
    request_id: string
```

**In CPN/ML notation**:
```ml
color AccountID = int with 1..1000;
color Amount = real with 1.0..5000.0;
color OperationType = string with "deposit" | "withdraw";
color RequestID = string;
color TransactionRequest = record 
  account_id:AccountID * 
  amount:Amount * 
  operation_type:OperationType * 
  request_id:RequestID;
```

**Example token value**:
```ml
{account_id=1, amount=500, operation_type="deposit", request_id="req-001"} 
  : TransactionRequest
```

### Our Guard Expression
```yaml
guard: |
  deposit_requests.token.amount >= 1 and 
  deposit_requests.token.amount <= 5000
```

**In CPN/ML notation**:
```ml
(#amount req) >= 1 andalso (#amount req) <= 5000
```

Where `req` is a variable of type `TransactionRequest`, and `#amount` extracts the amount field.

### Our Token Transformation
```yaml
token_transform:
  active_accounts: |
    {
      "account_id": active_accounts.token.account_id,
      "balance": active_accounts.token.balance + deposit_requests.token.amount
    }
```

**In CPN/ML notation**:
```ml
{account_id = #account_id acc, 
 balance = (#balance acc) + (#amount req)}
```

Where:
- `acc` is the account token (type `Account`)
- `req` is the request token (type `TransactionRequest`)
- `#account_id acc` extracts account_id from account
- `#balance acc` extracts balance from account
- `#amount req` extracts amount from request
- `+` adds balance and amount

## 10. Key Takeaways

1. **Color Sets = Token Types**: In CPN, "color" means "data type" or "token type"

2. **Records = Our Token Data**: We use records (named fields) to represent token attributes

3. **Guards = Boolean Expressions**: Guards use logical operators (`andalso`, `orelse`, `not`) and comparisons

4. **Token Transformation = Record Construction**: We create new records by extracting fields and computing new values

5. **Types Ensure Safety**: By defining types (like `int with 1..1000`), we ensure tokens have valid values

## 11. Common Patterns in Our Banking System

### Pattern 1: Token Correlation
```yaml
token_correlation: |
  active_accounts.token.account_id == deposit_requests.token.account_id
```

**In CPN/ML**:
```ml
(#account_id acc) = (#account_id req)
```

### Pattern 2: Guard Validation
```yaml
guard: |
  deposit_requests.token.amount >= 1 and 
  deposit_requests.token.amount <= 5000
```

**In CPN/ML**:
```ml
(#amount req) >= 1 andalso (#amount req) <= 5000
```

### Pattern 3: Balance Update
```yaml
"balance": active_accounts.token.balance + deposit_requests.token.amount
```

**In CPN/ML**:
```ml
(#balance acc) + (#amount req)
```

## 12. Why This Matters

Understanding CPN math helps you:

1. **Design Better Models**: Know what types of data tokens can carry
2. **Write Correct Guards**: Use proper logical operators
3. **Transform Tokens Correctly**: Extract and combine fields properly
4. **Validate Models**: Ensure token values are within allowed ranges

## References

- [CPN Tools Presentation](https://cpntools.org/wp-content/uploads/2018/01/cpn.pdf) - Original source
- [CPN Tools Website](http://cpntools.org) - Official CPN Tools documentation
- Jensen & Kristensen: "Coloured Petri Nets: Modelling and Validation of Concurrent Systems" - Comprehensive textbook

## Summary

The math in CPN is really about:
- **Types**: Defining what data tokens can carry (like our `account_id` and `balance`)
- **Operators**: Doing math and comparisons (`+`, `-`, `>=`, `<=`)
- **Logic**: Making decisions (`andalso`, `orelse`, `if then else`)
- **Records**: Grouping related data together (like our account token)
- **Extraction**: Getting specific fields from records (`#account_id`, `#balance`)

Think of it as a type-safe way to work with token data, ensuring that:
- Tokens have the right structure
- Guards check the right conditions
- Transformations compute the right values

Our YAML configuration is essentially a more readable way to express these same CPN concepts!

