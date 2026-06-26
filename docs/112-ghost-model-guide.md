# 112 — Ghost model guide: abstract specification state

> **Implementation status** — the core ghost-model features (ghost field declaration,
> layout erasure, read/write rejection in real code, struct invariants, and
> contract-position access via `requires`/`ensure`/`where`/`assert`/`invariant`) are
> **fully implemented** in the semantic analyzer. See the implementation status table at
> the bottom of this document for exact per-feature status. Design-only features are
> marked explicitly.

Cross-references: docs/109 (unified refinement pipeline), docs/110 (progressive correctness
ladder), docs/111 (ghost models, typestate, and named contracts: design and staging plan).

---

## Overview

A **ghost model field** is an abstract logical state associated with a struct that exists
purely for specification and verification. Ghost fields carry no runtime bytes; they are
completely erased from the compiled program's memory layout, pointer arithmetic, and code
generation. They allow you to describe the logical behavior of a data structure in terms
of a simpler mathematical model while the real implementation uses complex optimizations.

### Why ghost models?

Consider a dynamic array backed by a contiguous buffer with explicit head/tail pointers:

```elisa
struct RingBuf[T]:
    head: usize
    tail: usize
    buf:  darray[T]
```

Today you can write contracts on concrete fields:

```elisa
def push(self: mutable RingBuf[T]&, v: T):
    requires (tail + 1) % buf.cap != head   # buffer not full
    ensure   tail == old(tail) + 1          # tail advanced by 1
```

But contracts over **concrete state** are fragile:
- They expose implementation details (head/tail pointers).
- They become unwieldy for complex data structures (e.g., b-trees, hash tables).
- They don't describe the *logical* effect: "I added an element."

With ghost models, you describe the logical behavior directly:

```elisa
struct RingBuf[T]:
    head: usize
    tail: usize
    buf:  darray[T]

    ghost model: seq[T]           # abstract sequence of elements
    ghost invariant:
        model == extractRange(buf, head, tail)  # (design pseudocode)

def push(self: mutable RingBuf[T]&, v: T):
    ensure self.model == old(self.model) ++ [v]  # logical: appended one element
```

The `model` field:
- Carries abstract logical content (a mathematical sequence).
- Appears only in `requires`, `ensure`, `ghost invariant`, and proof contexts.
- Is **completely erased** from runtime layout and codegen.
- Lets callers reason about *what* happens (append) without *how* it happens (buffer rotation).

---

## Syntax and declaration

### Ghost field declaration

Inside a `struct` body, declare ghost fields with the `ghost` keyword:

```elisa
struct Stack[T]:
    items: darray[T]
    
    ghost model: seq[T]           # abstract stack contents
```

Ghost fields follow the same type syntax as regular fields. Common choices:
- `seq[T]` — mathematical sequences (ordered, duplicates allowed)
- `set[T]` — mathematical sets (unordered, no duplicates)
- `dict[K, V]` — mathematical mappings
- Custom abstract types (records of logical properties)

### Ghost invariants

A ghost invariant is a predicate that relates the ghost model to the concrete fields.
It must hold at every public function boundary (entry and exit).

```elisa
struct Stack[T]:
    items: darray[T]
    
    ghost model: seq[T]
    ghost invariant:
        model == items[0..<items.count]  # model is exactly the live items
```

The invariant is written as a boolean expression and may reference:
- Concrete fields (`items.count`)
- Ghost fields (`model`)
- Logical operators and mathematical predicates

Multiple invariants can be stacked:

```elisa
ghost invariant:
    model == extractRange(buf, head, tail)
ghost invariant:
    head <= tail                          # monotonic; logical consistency
```

### Ghost-local variables in specifications

Inside `requires`, `ensure`, `is Law`, and `ghost invariant` blocks, you can introduce
temporary proof witnesses using `ghost let`:

```elisa
def merge(out: mutable darray[i64]&, a: darray[i64], b: darray[i64]):
    requires IsSorted(a.model) and IsSorted(b.model)
    ensure   IsSorted(out.model)
    ensure   ghost let perm = Permutation(out.model, a.model ++ b.model):
        perm.holds  # witness exists proving the property
```

`ghost let` bindings exist only in the proof obligations; they produce no code.

---

## Worked example: a verified stack

Here is a complete example showing ghost models in action:

```elisa
struct Stack[T]:
    items: darray[T]
    max_depth: usize
    
    ghost model: seq[T]
    ghost invariant:
        model == items[0..<items.count]
    ghost invariant:
        items.count <= max_depth

def init[T](max_depth: usize) -> Stack[T]:
    return Stack(
        items: [],
        max_depth: max_depth
        # ghost model is implicitly [] (derived from invariant)
    )

def push[T](self: mutable Stack[T]&, v: T):
    requires self.items.count < self.max_depth
    ensure   self.model == old(self.model) ++ [v]
    
    self.items.push(v)
    # Ghost invariant holds: model is now items[0..<count]

def pop[T](self: mutable Stack[T]&) -> T:
    requires self.items.count > 0
    ensure   old(self.model) == self.model ++ [result]
    
    let v = self.items.pop()
    # Ghost invariant holds: model shrank by one element
    return v

def peek[T](self: Stack[T]&) -> T:
    requires self.items.count > 0
    ensure   result == self.model[self.items.count - 1]
    
    return self.items[self.items.count - 1]
```

### Key observations

1. **Ghost `model` appears only in specs**: The field is declared with `ghost` and
   referenced only in `requires`, `ensure`, and `ghost invariant` blocks.

2. **Invariant derives `model`**: The struct literal `Stack(...)` doesn't need to
   explicitly set `model`. The compiler derives it from the `ghost invariant`.

3. **Logical descriptions are clearer**: `self.model == old(self.model) ++ [v]`
   (semantic: appended one element) is easier to read than
   `self.items.count == old(self.items.count) + 1` (syntactic: count incremented).

4. **Implementation changes don't break contracts**: If you later optimize the stack
   to use a growth factor instead of push-by-one, the logical contract (`model == ... ++ [v]`)
   stays valid as long as the ghost invariant still holds.

---

## Erasure rules

Ghost fields are completely erased from the runtime representation.

| Context | Rule |
|---|---|
| `sizeof(S)` / `alignof(S)` | Ghost fields are invisible. The struct size is determined by concrete fields only. |
| Struct literal `S(...)` | Ghost fields are not positional arguments. They are derived automatically from the ghost invariant. |
| Field access `s.model` | **Legal only** inside `requires`, `ensure`, `where`, `ghost invariant`, and `is Law` predicates. A compile error elsewhere (e.g., in regular function bodies). |
| Code generation | Ghost fields produce no LLVM IR. No memory is allocated, no loads/stores are emitted. |
| Pointer arithmetic | `&s.model` is a compile error (ghost fields have no address). Field offsets ignore ghost fields. |
| Debug builds | Optionally, ghost invariants can be materialized as runtime assertions behind a `-fghost-check` flag (design; the invariant must be computable in Elisa without external SMT). |

### Size example

```elisa
struct Concrete:
    x: i64           # 8 bytes
    y: i32           # 4 bytes
    padding          # 4 bytes (alignment)
# sizeof(Concrete) == 16

struct WithGhost:
    x: i64           # 8 bytes
    y: i32           # 4 bytes
    padding          # 4 bytes (alignment)
    ghost model: seq[i64]  # zero bytes

# sizeof(WithGhost) == 16  (same as Concrete)
```

---

## Obligation lowering and verification

When a function accepts or returns a struct with a `ghost invariant`, the compiler
automatically seeds and discharges ghost-invariant obligations.

### Precondition (entry)

At function entry, the compiler **assumes** the ghost invariant holds for all incoming
structs. This is seeded into the proof's fact lattice as a refinement fact:

```
entry:  assume ghost_inv(param_s)
```

This reuses the mechanism from docs/109 §3 (`seedWhereRefinementFact`).

### Postcondition (exit)

At every return point, the compiler **proves** that the ghost invariant holds in the
result state:

```
exit:   prove  ghost_inv(result_s)
```

This travels through the discharge ladder (docs/109 §4):

```
flow analysis  →  linear prover  →  SMT tier  →  runtime fallback
```

### The discharge ladder

Ghost-invariant obligations are boolean predicates, just like any `requires` or `ensure`.
They are discharged through the same three-tier system:

1. **Fact lattice + linear prover** — pattern-matches arithmetic relations and equalities.
   Fast, no external process.

2. **SMT tier** — if the linear prover is uncertain, serializes the invariant + current
   facts into SMT-LIB2 and spawns a `z3` subprocess (gated by `--enable-smt`).

3. **Runtime fallback** — if both tiers are uncertain, the obligation becomes a
   `proofLint` warning (runtime assertion in debug builds). Under `--strict` it is
   a hard compile error.

### Example obligation flow

```elisa
struct RingBuf[T]:
    head: usize
    tail: usize
    buf:  darray[T]
    
    ghost model: seq[T]
    ghost invariant:
        model == extractRange(buf, head, tail)

def push(self: mutable RingBuf[T]&, v: T):
    requires self.items.count < self.cap
    ensure   self.model == old(self.model) ++ [v]
```

At the **entry** of `push`:
- Compiler seeds: `ghost_inv(self) = (self.model == extractRange(self.buf, self.head, self.tail))`
- This fact is available to discharge the `ensure` clauses.

At the **exit** of `push`:
- After `self.buf.push(v)` and head/tail updates, the compiler must prove:
  - `ghost_inv(self)` — the invariant still holds
  - `self.model == old(self.model) ++ [v]` — the explicit ensure clause
- Both obligations go through the discharge ladder.

---

## Ghost-state framing across function calls

### The framing problem

When function `f` calls `g` and `g` modifies an argument `self`, the caller's ghost-model
facts for `self` become stale. Example:

```elisa
def f():
    var s: Stack[i64] = ...
    # Fact: s.model == [1, 2, 3]
    
    g(mutable s&)
    # Now what is s.model? The facts don't say.
    # g might have modified s.

def g(self: mutable Stack[i64]&):
    self.push(42)
```

### Solution: explicit frame conditions

**(design)** The compiler uses **explicit frame conditions** to
manage ghost-state invalidation. A function that mutates a ghost field must declare
this via a `changes` clause (docs/87 frame conditions):

```elisa
def push(self: mutable Stack[T]&, v: T):
    changes self.model     # callee promises it may modify self.model
    ensure  self.model == old(self.model) ++ [v]
```

When the caller sees `changes self.model`, it invalidates the ghost-model fact from its
lattice. After the call, the caller must re-establish the fact from the callee's
postcondition:

```elisa
def f():
    var s: Stack[i64] = ...
    # Fact: s.model == [1, 2, 3]
    
    push(mutable s&, 42)
    # "changes s.model" invalidates the old fact
    # New fact: s.model == [1, 2, 3, 42] (from the ensure clause)
```

This is a safe-default approach. It requires explicit annotation from the callee but
is straightforward and composable.

### Future optimization: ghost-frame inference

**(research / stage 3+, not yet designed)** A more advanced option is to **infer** which ghost fields
a call touches by scanning the callee's postcondition and ghost invariant. Only ghost
fields explicitly mentioned in those clauses would be invalidated. This reduces annotation
burden but requires an interprocedural analysis pass not yet designed.

---

## Interaction with other features

### Ghost models + refinement types

Ghost fields integrate naturally with Elisa's refinement typing (docs/109). A ghost
invariant is itself a refinement predicate and flows through the same obligation pipeline.

Example:

```elisa
struct ValidatedList:
    items: darray[i64]
    
    ghost model: seq[i64]
    ghost invariant:
        IsSorted(model) and
        model == items[0..<items.count]

def bsearch(self: ValidatedList&, target: i64) -> i64 where result >= 0 and result < self.items.count:
    # Precondition: ghost_inv(self) is seeded (model is sorted)
    # Postcondition: must prove result >= 0 and result < self.items.count
```

The ghost invariant provides facts that help discharge both the explicit `where` predicate
and the internal search logic.

### Ghost models + named contracts

**(design, docs/111)** Ghost fields can appear in named contracts:

```elisa
contract QueueInvariant[T](q: RingBuf[T]&):
    ensure q.model == old(q.model) ++ [last_pushed]

def enqueue[T](self: mutable RingBuf[T]&, v: T):
    uses QueueInvariant(self, v)
```

When a contract references a ghost field, it is expanded inline (like a macro) into
the function's spec before discharge.

### Ghost models + typestate

**(design, docs/111)** A ghost invariant can mention a typestate index:

```elisa
typestate Queue[T]:
    states: Empty, HasItems, Closed
    
    ghost model: seq[T]
    ghost invariant:
        state == Empty => model.len == 0

def dequeue[T](self: mutable Queue[T, HasItems]&) -> T:
    transition HasItems -> (if result is Some then HasItems else Empty)
    ensure      self.model == old(self.model)[1..]  # removed head
```

The invariant enforces that the logical state is consistent with the typestate index.

---

## Common patterns

### Pattern 1: Abstract sequence model

Use `seq[T]` to describe an ordered, duplicate-allowing sequence:

```elisa
struct Queue[T]:
    buf: darray[T]
    head: usize
    tail: usize
    
    ghost model: seq[T]
    ghost invariant:
        model == (
            if head <= tail then buf[head..<tail]
            else buf[head..] ++ buf[0..<tail]
        )

def enqueue(self: mutable Queue[T]&, v: T):
    ensure self.model == old(self.model) ++ [v]
```

### Pattern 2: Set-based invariant

Use `set[K]` to describe membership without order:

```elisa
struct HashSet[K]:
    table: darray[list[K]]
    count: usize
    
    ghost model: set[K]
    ghost invariant:
        model == unionOfAllBuckets(table)

def insert(self: mutable HashSet[K]&, k: K):
    ensure self.model == old(self.model) | {k}  # set union
```

### Pattern 3: Mapping invariant

Use `dict[K, V]` to describe associations:

```elisa
struct Cache[K, V]:
    entries: darray[Entry[K, V]]
    
    ghost model: dict[K, V]
    ghost invariant:
        model == asDict(entries)

def get(self: Cache[K, V]&, k: K) -> Option[V]:
    ensure
        ghost let has_k = k in old(self.model):
        has_k => result == Some(old(self.model)[k])
```

### Pattern 4: Logical properties

Use predicates to capture higher-level invariants:

```elisa
struct SortedList:
    items: darray[i64]
    
    ghost model: seq[i64]
    ghost invariant:
        model == items[0..<items.count] and
        IsSorted(model)

def insert(self: mutable SortedList&, v: i64):
    requires IsSorted(old(self.model))
    ensure   IsSorted(self.model)
```

---

## Implementation status

| Feature | Status | Notes |
|---|---|---|
| Ghost field declaration (`ghost name: T`) | **Implemented** | `analyzer_decl_structs.go`; recorded in `GhostFieldOrder` |
| Layout erasure (ghost → zero bytes) | **Implemented** | Ghost fields stripped from concrete field list before layout |
| Read rejection in real code (`s.ghost` → error) | **Implemented** | `analyzer_expr_projection_*.go`; `ghostReadAllowed` gate |
| Write rejection in real code (`s.ghost <- v` → error) | **Implemented** | `analyzer_expr_mutation_refs.go`; fixed on this branch |
| Ghost field readable in `requires` | **Implemented** | `ghostReadAllowed` raised in `analyzer_functions.go` |
| Ghost field readable in `ensure` | **Implemented** | `ghostReadAllowed` raised in `analyzer_functions.go` |
| Ghost field readable in `where` param/return refinements | **Implemented** | `ghostReadAllowed` raised in `analyzer_where_refinements.go` |
| Ghost field readable in `assert` | **Implemented** | `ghostReadAllowed` raised in `analyzer_flow.go` |
| Ghost field readable in in-body `invariant` | **Implemented** | `ghostReadAllowed` raised in `analyzer_flow.go` |
| Struct invariant referencing ghost field | **Implemented** | `ghostReadAllowed` raised during invariant analysis in `analyzer_decl_structs.go` |
| Ghost invariant discharged under `-strict` + SMT | **Implemented** | Seeded as SMT fact; shared discharge ladder (docs/109) |
| Ghost field in struct literal → rejected | **Implemented** | Ghost fields absent from concrete field set; struct literal lookup fails |
| Ghost field default value → rejected | **Implemented** | Explicit check in `analyzer_decl_structs.go` |
| Ghost-local variables in specs (`ghost let`) | Design | docs/111 §1.5 |
| Explicit ghost-frame conditions (`changes self.model`) | Design | docs/111 §1.6, Option A; docs/87 |
| Ghost-frame inference | Research | docs/111 §1.6, Option B |
| Debug `-fghost-check` materialization | Design | docs/111 §1.3, §5 |

---

## Summary

Ghost models are abstract logical states that:
- Live **only** in specifications, contracts, and invariants.
- Are **completely erased** from runtime layout and code.
- **Simplify** contracts by describing logical behavior instead of implementation details.
- **Flow through** the same refinement pipeline (docs/109) as `requires`, `ensure`, and `where`.

They answer the question: *How do I write clear, maintainable contracts for complex data structures?*

The answer is to split the state into two views:
1. **Concrete fields** — how the data is actually stored (implementation).
2. **Ghost model** — what the data logically means (specification).

The ghost invariant links them together, letting the compiler and verifier reason at both levels.

---

## See also

- **docs/109** — Unified refinement pipeline: the shared discharge machinery for all proof obligations.
- **docs/110** — Progressive correctness ladder: the SMT and linear-prover tiers.
- **docs/111** — Ghost models, typestate, and named contracts: design and staged implementation plan.
- **docs/87** — Frame conditions: `changes` and `preserves` clauses for tracking mutation.
- **docs/97** — Named composable contracts: the `contract` and `uses` syntax.
