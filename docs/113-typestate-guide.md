# 113 — Typestate: a user's guide

> **Substantially implemented** — the core typestate system (state transitions,
> illegal-operation rejection, phantom erasure, `terminal:`, `linear`) is enforced
> today by the semantic analyzer.  The "(design / not yet implemented)" label that
> appeared here previously was wrong.  The remaining gaps are narrow and documented
> in §9.

Cross-references: docs/110 (progressive correctness ladder), docs/111 (ghost models and
typestate design), docs/96 (typestate protocols technical design).

---

## Overview

**Typestate** is a compile-time technique for preventing illegal operation orderings on a value.
Classic examples:

- You cannot read from a file after closing it.
- You cannot finalize a builder twice.
- You cannot send a message on a closed network socket.

Today, Elisa catches these at runtime (via assertions or crashes).  Typestate makes them
**unrepresentable** — the type system refuses to compile code that attempts them.

This guide covers:

1. **The problem** — why operation orderings matter and why refinements aren't enough.
2. **The API sketch** — how `File[Open]` and `File[Closed]` encode state in types.
3. **The runtime cost** — why this is zero-cost (the state index is a phantom type, erased at runtime).
4. **Transition functions** — how `open: Closed -> Open` moves a value from one state to another.
5. **The aliasing caveat** — why mutable references can still break typestate.
6. **Connection to ownership** — how typestate fits alongside Elisa's affine and linear types.

---

## 1. The problem: operation orderings

### 1.1 Why contracts aren't enough

Consider a file API:

```elisa
struct File:
    fd: i32
    # …

def read(f: mutable File&, buf: mutable darray[u8]&) -> i64:
    requires f.is_open     # precondition: file must be open
    # …

def close(f: mutable File&):
    f.is_open = false
    # …

def main():
    f: File = open_file("data.txt")
    data: darray[u8] = []
    read(f, data)
    close(f)
    read(f, data)      # BUG: calling read after close
```

Even with the `requires f.is_open` contract, this code compiles.  Why?

- The compiler cannot prove `f.is_open` is still true after `close(f)` mutates it.
- So it either emits a runtime check (slow) or a `proofLint` warning (ignored).
- At runtime, `read` will fail (or crash) with a cryptic error.

### 1.2 The typestate solution

Typestate tracks **which operations are allowed at each point in a value's lifetime**.  Instead
of a boolean flag `is_open`, the type itself encodes the state:

```elisa
struct File[S]:               # S is a phantom type index
    fd: i32
    # actual fields unchanged

def read(f: mutable File[Open]&, buf: mutable darray[u8]&) -> i64:
    # No requires clause.  The type File[Open] itself proves the precondition.
    # …

def close(f: mutable File[Open]) -> File[Closed]:
    # Consumes File[Open], returns File[Closed]
    # …

def main():
    f: File[Closed] = File(fd: -1)          # file starts closed
    f = open_file("data.txt")               # open() returns File[Open]
    data: darray[u8] = []
    read(f, data)                           # ✓ f is File[Open]
    f = close(f)                            # f is now File[Closed]
    read(f, data)                           # ✗ COMPILE ERROR: f is File[Closed], not File[Open]
```

The illegal operation is **impossible to express** — the type checker rejects it at compile time.

---

## 2. The API sketch

### 2.1 Defining a typestate type

A `typestate` declaration lists the states a value can inhabit:

```elisa
typestate File:
    states: Closed, Open, EndOfFile
```

This desugars to a zero-cost struct with a phantom discriminant:

```elisa
struct File[S | Closed | Open | EndOfFile]:
    fd: i32
    # … other real fields

    derive state: S    # compile-time-only discriminant; erased at runtime
```

### 2.2 Declaring transition functions

A **transition function** moves a value from one state to another:

```elisa
def open(path: cstr) -> File[Open]:
    f: File[Closed] = File(fd: -1)
    f.fd = sys_open(path)      # populate the real field
    return f                    # type-checked as File[Open]

def read(f: mutable File[Open]&, buf: mutable darray[u8]&) -> i64:
    # Only File[Open] can be read.
    return sys_read(f.fd, buf)

def close(f: File[Open]) -> File[Closed]:
    # Consumes File[Open]; caller loses the handle after this call.
    sys_close(f.fd)
    return File(fd: -1)        # type-checked as File[Closed]

def eof(f: mutable File[Open]&) -> mutable File[EndOfFile]&:
    # Mutates in place, transitions from Open to EndOfFile.
    return f  # compiler sees the transition in the return type
```

### 2.3 Syntax sugar

Transition functions can be declared inline in the `typestate` body (sugar; they are plain functions):

```elisa
typestate File:
    states: Closed, Open, EndOfFile

    transition open(path: cstr) -> File[Open]:
        # definition…

    transition close(self: File[Open]) -> File[Closed]:
        # definition…
```

---

## 3. Zero-cost phantom state

The `derive state: S` discriminant is **erased from the runtime representation**.

| Aspect | Rule |
|---|---|
| **Memory layout** | `sizeof(File[Open])` == `sizeof(File[Closed])` — the state index is not stored. |
| **Runtime code** | No state-check bytecode; the state is a compile-time proof, not a runtime field. |
| **ABI** | `File[Open]` and `File[Closed]` pass over function boundaries with the same C ABI. |
| **Optimization** | Release builds have zero overhead; debug builds may optionally check the state at boundaries (under `-fdebug-typestate`). |

**Key insight:** typestate is a **phantom type** — it shapes what operations are legal without changing
the runtime footprint.  You get compile-time safety with zero cost.

---

## 4. Transition functions

### 4.1 Ownership transitions

The simplest case: a transition function **consumes** the input and **returns** the new state:

```elisa
def close(f: File[Open]) -> File[Closed]:
    # After close(f) is called, f is moved out (consumed).
    # The caller can no longer use f.
    sys_close(f.fd)
    return File(fd: -1)
```

This uses Elisa's affine-type system: once `f` is passed to `close`, the caller has no reference
to it anymore.  The transition is enforced by the type signature and the borrow checker.

### 4.2 In-place transitions via mutable reference

A transition can also mutate in place:

```elisa
def read_until_eof(f: mutable File[Open]&) -> mutable File[EndOfFile]&:
    # Read until EOF; transition to EndOfFile state while holding the borrow.
    # The return type tells the caller: "you still have a reference, but the state changed."
    while true:
        buf: darray[u8] = [0; 4096]
        n = sys_read(f.fd, buf)
        if n == 0:
            break
    return f
```

The return type `mutable File[EndOfFile]&` proves to the caller that `f` is now in the
`EndOfFile` state.

### 4.3 Postcondition discharge

Internally, a transition function's return type is lowered to an `ensure` postcondition:

```elisa
def close(f: File[Open]) -> File[Closed]:
    # Lowered to:
    # def close(f: File[Open]) -> File[Closed]:
    #     ensure result is File[Closed]
    #     …
```

The compiler verifies that every return path yields the declared state.  If a path returns
`File[Open]` instead, it's a type error.

---

## 5. The aliasing caveat

Typestate is sound when a value has a **single owner** or a **single mutable borrower**.  But
aliasing can break the guarantee.

### 5.1 The problem

```elisa
def buggy(f: mutable File[Open]&):
    # Imagine f could be aliased by another reference.
    f.fd = -1                      # we "close" the file by setting fd to invalid
    # Now f is logically File[Closed], but the type still says File[Open].
    # If the alias observes f later, it might try to read from fd=-1.
```

### 5.2 How Elisa prevents it

Elisa's **mutable-borrow checker** already prevents two simultaneous `mutable` references to the same
location.  So if `f` is borrowed `mutable File[Open]&`, no other mutable reference to `f` can exist.

This means:

- A transition function holds the **only mutable borrow**.
- No second borrower can witness the state mid-transition.
- Immutable borrows (`File[Open]&` without `mutable`) are read-only and see only the pre-transition state.

**Current limitation:** if a transition function stores `f` into a shared data structure that is
also readable by the caller, the caller could later observe the state change out-of-order.
This is addressed in docs/111 §2.2 as a future tightening of the storage-class UNION check.
Until then, the existing mutable-alias checker provides a conservative guard.
See `TestTypestateAliasMutationTransitionsRoot` for the alias-through-ref case that IS caught.

---

## 6. Typestate and ownership

### 6.1 Affine types

Typestate plays well with Elisa's affine-ownership system:

```elisa
typestate Builder[T]:
    states: Open, Sealed

    transition seal(self: Builder[T][Open]) -> Builder[T][Sealed]:
        # Consumes self (affine: can only be used once).
        # Returns Sealed version.
        return self

    transition finish(self: Builder[T][Sealed]) -> T:
        # Final transition: consumes the builder entirely.
        # Returns the built value.
        return self.finalize()
```

A `Builder` that must be consumed exactly once can be encoded:

```elisa
typestate Builder[T]:
    linear                  # this builder must be consumed exactly once
    states: Open, Sealed, Consumed

    transition seal(self: Builder[T][Open]) -> Builder[T][Sealed]:
        return self

    transition finish(self: Builder[T][Sealed]) -> (T, Builder[T][Consumed]):
        # Returns both the result and a Consumed marker.
        # The Consumed marker may not be used further (affine gate).
        result = self.finalize()
        return (result, Builder(…))
```

The `linear` flag adds an extra affine gate: failing to call `finish` is a compile error.

### 6.2 Region-owned values

Typestate and region ownership are independent:

```elisa
struct Conn[region R, state S]:
    sock: i32
    buf: darray[u8] in R

def recv(c: mutable Conn[_, Open]&, buf: mutable darray[u8]&) -> i64:
    # Conn is region-polymorphic (in R) and state-polymorphic (in S).
    # The underscore means "infer the region".  The state is explicit.
    return sys_recv(c.sock, buf)
```

Both parameters are inferred separately: the region (where `buf` lives) and the state (which operations are allowed).

---

## 7. Worked example: a typed state machine

Here is a complete sketch of a request-handler state machine:

```elisa
typestate Request[T]:
    states: Created, Sent, Received, Processed

def new[T]() -> Request[T][Created]:
    return Request(…)

def send[T](req: Request[T][Created]) -> Request[T][Sent]:
    # Transition: Created → Sent
    sys_send(req)
    return req

def recv[T](req: mutable Request[T][Sent]&) -> mutable Request[T][Received]&:
    # Transition: Sent → Received (in place)
    response = sys_recv()
    req.result = response
    return req

def process[T](req: mutable Request[T][Received]&) -> mutable Request[T][Processed]&:
    # Transition: Received → Processed (in place)
    req.output = transform(req.result)
    return req

def extract[T](req: Request[T][Processed]) -> T:
    # Final extraction: consume Processed request, return output.
    return req.output

def workflow[T](data: T) -> T:
    req = new[T]()                          # Created
    req = send(req)                         # Sent
    req = recv(req)                         # Received
    req = process(req)                      # Processed
    result = extract(req)
    # If you call recv(req) again here: ✗ COMPILE ERROR
    //   recv expects Request[T][Received], got Request[T][Processed]
    return result
```

Each transition is type-checked.  Illegal orderings (e.g., `process` before `recv`) are caught
at compile time.

---

## 8. Relationship to other correctness features

### 8.1 vs. Contracts (Level 3)

| Aspect | Contracts | Typestate |
|---|---|---|
| **Verification** | Runtime checks + SMT discharge; can be ignored in release builds | Compile-time type check; zero runtime cost |
| **Expressiveness** | Any boolean predicate (e.g., `x > 0`) | Only state orderings |
| **Overhead** | Non-zero if not proved (and `-strict` is off) | Zero |
| **Composability** | Stacks with `where`, `ensure`, frame conditions | Orthogonal; can be combined with contracts |

**When to use typestate:** forbidden operation orderings are the primary risk.

**When to use contracts:** numeric ranges, complex invariants, or algebraic properties.

### 8.2 vs. Effect families (Level 5)

Effect families (`can`, `forbids`) describe which side effects a function permits.  Typestate
describes which **states** a value can inhabit.

```elisa
typestate Conn:
    states: Closed, Open

def read(c: mutable Conn[Open]&) can Read:
    # Two independent facts:
    # 1. c must be in Open state (typestate).
    # 2. The function performs a Read effect (effect system).
```

They orthogonally constrain different aspects of program behavior.

### 8.3 vs. Affine ownership

Affine types ensure values are used **at most once**; typestate ensures they are used **in the right order**.

```elisa
typestate Token:
    states: Fresh, Spent

def consume(t: Token[Fresh]) -> Token[Spent]:
    # Affine: once you pass t to consume, you can't use t again.
    # Typestate: when you get it back, you know it's Spent.
    return t
```

Both mechanisms are sound in combination.

---

## 9. Current status and staged rollout

The staged rollout from docs/111 §5 and implementation status (as of 2026-06):

| Stage | Deliverables | Status |
|---|---|---|
| **S0** | `typestate` keyword; desugaring to `struct[state]` + phantom discriminant; `is State` operator for state checks | **DONE** |
| **S1** | Transition functions (`transition` keyword); `ensures p => NewState` postcondition discharge; illegal-transition hard errors; `terminal:` keyword | **DONE** |
| **S2** | Affine typestate (`linear` flag); leak detection at scope exit; double-transition error | **DONE** |
| **S3** | Protocol composition (multiple independent state indices in one struct) | planned |

### Known narrow gaps (not yet enforced)

- **Linear by-value consume in callee**: passing a `linear typestate` value by move into a
  callee that does `pass` (without itself consuming the linear param) leaves the callee's own
  linear param un-discharged.  Workaround: the callee must either transition to a used state
  or declare `ensures t => <final-state>` via a mutable ref.
- **Shared-structure aliasing**: see §5.2 above.

---

## 10. Next steps

- Read docs/96 for the technical protocol design (state machines, dispatch rules, soundness invariants).
- Read docs/111 for the integration with ghost models and named contracts.
- Read docs/110 for where typestate fits in the correctness ladder.

The `typestate` keyword is the recommended way to model state-dependent APIs today.
The refinement system (`where` + `requires` + `ensure`) remains available for numeric/algebraic
invariants that typestate does not cover.
