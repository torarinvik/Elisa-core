# 114 — Named contracts: a user-facing guide

> **(design / not yet implemented)** — everything in this document describes a planned language
> feature.  No compiler source has been modified.

Cross-references: docs/111 (unified staging plan), docs/109 (unified refinement pipeline),
docs/95 (laws and refinement cheat-sheet).

---

## Overview

**Named contracts** are reusable bundles of preconditions and postconditions.  They let you
write a set of obligations once and apply them to multiple functions by name, avoiding
repetition and improving readability.

Think of named contracts as **proof Lego** — small, composable blocks of proof that you can
snap together to build a more complex correctness story.

### The problem they solve

Without named contracts, if you have two functions that must satisfy the same set of
obligations, you must repeat them:

```elisa
def sort(xs: mutable darray[i64]&):
    ensures IsSorted(xs)
    ensures xs.len == old(xs.len)
    # ... function body ...

def merge(out: mutable darray[i64]&, a: darray[i64], b: darray[i64]):
    ensures IsSorted(out)
    ensures out.len == a.len + b.len
    # ... function body ...
```

If the definition of "sorted" evolves or if a bug fix applies to both, you must change both
functions.  Named contracts eliminate this duplication.

---

## Syntax and declaration

### Basic contract

A **contract** declaration groups related `requires` and `ensure` clauses:

```elisa
contract Sorted(xs: darray[i64]):
    ensure IsSorted(xs)
    ensure xs.len == old(xs.len)
```

A contract:
- Has a name (`Sorted`).
- Takes zero or more parameters (`xs: darray[i64]`), which may be mutable references (`mutable
  darray[i64]&`).
- Contains a list of `ensure` clauses (postconditions).
- Optionally contains `requires` clauses (preconditions).

### Preconditions in contracts

```elisa
contract Partition(lo: i64, hi: i64):
    requires lo < hi
    require lo >= 0

contract BuildList(items: darray[Item], target: mutable darray[Item]&):
    requires target.len == 0
    ensure target.len == old(items.len)
    ensure IsPermutation(target, old(items))
```

---

## Application: the `uses` keyword

To apply a contract to a function, use `uses`:

```elisa
def sort(xs: mutable darray[i64]&):
    uses Sorted(xs)
    # ... function body ...
```

When the compiler sees `uses Sorted(xs)`, it **expands** the contract inline.  This is macro-like
expansion:

```elisa
def sort(xs: mutable darray[i64]&):
    uses Sorted(xs)
    # ↓ expands to:
    # ensure IsSorted(xs)
    # ensure xs.len == old(xs.len)
    # ... function body ...
```

You can use multiple contracts on a single function:

```elisa
def merge(out: mutable darray[i64]&, a: darray[i64], b: darray[i64]):
    uses Partition(a.len, b.len)
    uses Sorted(out)
    # expands to:
    # requires a.len < b.len  (from Partition)
    # requires a.len >= 0     (from Partition)
    # ensure IsSorted(out)    (from Sorted)
    # ensure out.len == old(out.len)  (from Sorted)
```

---

## How contracts work with refinements and obligations

Named contracts are **not** a new obligation machinery.  They are syntactic sugar that expands
into the existing `requires`/`ensure` system described in docs/109.

### Expansion into requires/ensure

After the `uses` directives are expanded, the function's obligations are discharged through the
**same three-tier ladder** as any other `requires`/`ensure` clause:

1. **Fact-lattice + linear prover** — O(1), inline pattern matching
2. **SMT tier** — external z3 solver for complex predicates  
3. **Runtime fallback** — proof-lint warning (hard error under `-strict`)

From the prover's perspective, a contract is transparent:

```elisa
contract SafeIndex(xs: darray[i64], i: i64):
    require i >= 0
    require i < xs.count

def get(xs: darray[i64], i: i64):
    uses SafeIndex(xs, i)
    return xs[i]

# ↓ Discharged as:
# def get(xs: darray[i64], i: i64):
#     requires i >= 0
#     requires i < xs.count
#     return xs[i]
```

See docs/109 for the full discharge-ladder details.

---

## Contracts vs. laws: what is the difference?

| Concept | Purpose | Spelling | Reusability |
|---|---|---|---|
| **law** | A *predicate* — a pure, total boolean function | `law Name(self: T, ...) = <bool-expr>` | Used in type positions (`T is Law`), flow narrows (`if x is Law`), contract clauses (`requires x is Law`) |
| **contract** | A *bundle of obligations* — pre/postconditions for a function | `contract Name(...): requires / ensure ...` | Applied to functions via `uses` |

A **law is a predicate**.  It answers the question: "Does this value satisfy a property?"

A **contract is a set of obligations**.  It answers: "What must be true before and after this
function runs?"

### Example: laws and contracts together

```elisa
# A law — a pure predicate.
law IsSorted(self: darray[i64]) =
    forall i: 0 <= i and i < self.len - 1 => self[i] <= self[i+1]

# A contract — bundles obligations about a function's behavior.
contract Sorted(xs: darray[i64]):
    ensure IsSorted(xs)
    ensure xs.len == old(xs.len)

# Apply the contract to a function.
def sort(xs: mutable darray[i64]&):
    uses Sorted(xs)
    # The function must prove:
    # - IsSorted(xs) after execution
    # - xs.len hasn't changed
```

You define the **law once** and then use it in many places:
- In type annotations: `type SortedArray = darray[i64] is IsSorted`
- In flow: `if arr is IsSorted: ...`
- In contracts: `contract …: ensure IsSorted(xs)`

The **contract** binds those laws together with additional obligations for a specific function.

---

## Ghost models in contracts

Named contracts can reference **ghost fields** — abstract spec-only state that is erased at
runtime (docs/111 §1).

```elisa
struct RingBuf[T]:
    head: usize
    tail: usize
    buf: darray[T]

    ghost model: seq[T]
    ghost invariant:
        model == buf[head..<tail]

contract Push(q: mutable RingBuf[T]&, v: T):
    ensure q.model == old(q.model) ++ [v]
    ensure q.len == old(q.len) + 1

def push(q: mutable RingBuf[T]&, v: T):
    uses Push(q, v)
    # ... implementation ...
```

The ghost field `model` exists only for proofs.  The contract can refer to it because `uses` is
a spec context, and ghost-field erasure rules (docs/111 §1.3) apply.

---

## Parameter binding in contracts

Contract parameters are bound at the `uses` site.  This works exactly like UFCS and type casting:

```elisa
contract SafeSlice(arr: darray[i64], start: i64, end: i64):
    requires 0 <= start and start <= end
    requires end <= arr.len

def slice_and_sum(arr: darray[i64], s: i64, e: i64) -> i64:
    uses SafeSlice(arr, s, e)
    # The compiler binds:
    #   arr -> the local arr param
    #   start -> the local s param
    #   end -> the local e param
    # Then expands:
    #   requires 0 <= s and s <= e
    #   requires e <= arr.len
```

Parameters may be mutable references:

```elisa
contract Initialized(target: mutable darray[Item]&):
    requires target.len == 0

def build(items: darray[Item], target: mutable darray[Item]&):
    uses Initialized(target)
    # target is bound to the mutable param
```

---

## Design patterns

### Reusable preconditions

Group common input checks into a contract:

```elisa
contract ValidRange(start: i64, end: i64):
    requires start >= 0
    requires end <= MAX_LEN
    requires start < end

def process(start: i64, end: i64):
    uses ValidRange(start, end)
    # ... process the range ...
```

### State-change witnesses

Document what a function changes and how:

```elisa
contract PopsFront[T](queue: mutable RingBuf[T]&):
    require queue.len > 0
    ensure queue.len == old(queue.len) - 1
    ensure old(queue.model)[1..] == queue.model

def pop_front(q: mutable RingBuf[T]&) -> T:
    uses PopsFront(q)
    # ... implementation ...
```

### Composition of multiple obligations

Layer contracts to build up complex correctness:

```elisa
contract ValidInput(xs: darray[i64]):
    requires xs.len > 0
    requires IsSorted(xs)

contract ValidOutput(xs: darray[i64]):
    ensure IsSorted(xs)
    ensure xs.len == old(xs.len)

def dedup(xs: mutable darray[i64]&):
    uses ValidInput(xs)
    uses ValidOutput(xs)
    # Must satisfy both contracts
```

---

## Staged rollout (implementation plan)

Named contracts are planned for implementation in stages (docs/111 §3.1):

| Stage | Feature | Status |
|---|---|---|
| **S0** | `contract` declaration + `uses` inline expansion | (design / not yet implemented) |
| **S1** | `@satisfies(ContractName)` conformance checking | (design / not yet implemented) |
| **S2** | Parameterized contracts with quantified predicates | (design / deferred) |

In S0, all contract expansion is macro-like: the clauses are inlined and discharged through the
standard refinement ladder (docs/109).  No new prover machinery.

---

## See also

- **docs/95** — Laws and refinement types cheat-sheet; understand the difference between laws
  (predicates) and refinements (types).
- **docs/109** — Unified refinement pipeline; how `requires`/`ensure` discharge.
- **docs/111** — Unified staging plan for ghost models, typestate, and named contracts.
