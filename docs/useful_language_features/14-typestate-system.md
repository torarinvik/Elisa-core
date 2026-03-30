# Typestate System Guide

This document describes the **full typestate story** in Contextlang.

It is intentionally broader than the pointer-only typestate documents:

- `02-pointer-typestate-practical.md` focuses on nullable/non-null pointers in practice
- `03-pointer-typestate-formal.md` gives a more formal pointer typing model
- **this document** explains how typestate works across:
  - references and nullness
  - aggregate state parameters
  - named derived states on structs
  - control-flow refinement
  - mutation and post-mutation widening
  - references, aliasing, and call boundaries
  - current compiler behavior and soundness guarantees

In short:

> Contextlang uses typestate as a lightweight proof system for low-level programming.
>
> Some typestate is **explicit in types**.
> Some typestate is **inferred from control flow**.
> Some typestate is **recomputed or widened after mutation**.

---

## 1. Why typestate exists here

Contextlang is trying to get useful safety and optimization facts **without** turning into a full theorem prover.

The design goal is:

- keep values low-level and explicit
- keep layout and mutation predictable
- let the compiler track facts that matter for correctness
- let those facts survive just long enough to be useful
- aggressively drop or widen facts when mutation or aliasing makes them questionable

That produces a family of features that are all “typestate”, even though they show up in different surface forms.

There are three main layers.

### Layer A: reference proof state

References carry nullness/proof state:

```context
T&   # proven non-null
T&?  # may be null
T!   # proven null
```

This is the pointer typestate described in detail in the pointer docs.

### Layer B: aggregate state parameters

Structs can declare positional aggregate state placeholders:

```context
struct Holder[?]:
    value: i32

struct Pair[?, ?]:
    left: i32
    right: i32
```

Those placeholders are filled with reference-state-style markers such as `&`, `?`, and `!` at instantiation time:

```context
Holder[&]
Pair[&, !]
```

This is a compact way to thread state through low-level aggregate types.

### Layer C: named derived state on structs

Structs can also declare a **named state parameter** and define the meaning of each state with a `derive state:` block:

```context
struct Player[state Alive | Dead]:
    health: mutable int

    derive state:
        Alive when self.health > 0
        Dead when self.health <= 0
```

This gives the compiler a type-level model for value states like:

- initialization phases
- protocol phases
- domain states (`Open`, `Closed`, `Alive`, `Dead`)
- structural invariants derived from field values

Named state is the part that behaves most like “ordinary typestate programming”.

---

## 2. The typestate axes at a glance

Here is the practical picture.

| Axis | Surface form | What it means |
| --- | --- | --- |
| Reference nullness | `T&`, `T&?`, `T!` | Proof about whether a reference is usable |
| Reference storage | `any`, `heap`, `stack`, `static`, region names | Provenance/storage class of the referenced object |
| Aggregate state placeholders | `struct X[?]`, `X[&, ?]` | Positional state parameters for aggregate types |
| Named struct state | `struct X[state A | B]`, `X[A]`, `X[A | B]` | Named value states, often derived from fields |
| Flow refinement | `if`, `assert`, `is`, guard fallthrough | Temporary strengthening of typestate facts |
| Post-mutation state | assignment, field mutation, ref-call widening | How facts are updated or invalidated after writes |

These axes are intentionally orthogonal where possible.

Examples:

```context
heap Player[Alive]&
```

This means:

- storage qualifier: `heap`
- value type: `Player[Alive]`
- outer reference state: proven non-null

And:

```context
Handle[&, !]
```

means a type with two aggregate-state slots, instantiated with two positional state markers.

---

## 3. Pointer typestate recap

The pointer layer is the smallest and strictest typestate lattice.

```text
    T&
     \
      T&?
     /
    T!
```

The core rules are:

- `T&` is usable
- `T&?` must be proven before dereference-like use
- `T!` is known null
- `null` may flow to `T&?` and `T!`, but not to `T&`
- casts do not invent stronger proof
- control flow can refine `T&?` into `T&` or `T!`

That layer is already documented in detail elsewhere, so this guide will only mention it when it interacts with named struct state.

---

## 4. Aggregate state parameters

Aggregate state parameters are positional.

```context
struct Holder[?]:
    value: i32

struct Pair[?, ?]:
    left: i32
    right: i32
```

### What they are for

They let the type carry one or more compact state markers without spelling out a named state family.

This is useful when the state itself is low-level and mechanical, for example:

- forwarding reference-state-like markers through wrappers
- tracking orthogonal proof bits in a compact form
- preserving state across generic aggregate boundaries

### Instantiation

```context
Holder[&]
Pair[&, !]
Pair[?, ?]
```

### Important property

These are **not** the same thing as named derived states.

- Aggregate state parameters are **positional**.
- Named derived states are **semantic** and live in a named state family like `Alive | Dead`.

The compiler supports both because they solve different problems.

---

## 5. Named struct states

Named state is declared like this:

```context
struct Player[state Alive | Dead]:
    health: mutable int

    derive state:
        Alive when self.health > 0
        Dead when self.health <= 0
```

This introduces a state family for `Player`:

- `Player[Alive]`
- `Player[Dead]`
- `Player[Alive | Dead]`

### Meaning of the syntax

```context
[state Alive | Dead]
```

means:

- the struct has a named state parameter
- the allowed cases are `Alive` and `Dead`
- the compiler treats that parameter as a type argument internally

The `derive state:` block then gives each state a meaning.

### Requirements enforced by the compiler

A struct with named states must satisfy these rules:

1. If it declares named states, it must have a `derive state:` block.
2. Every declared state must appear in the derive block.
3. Every derive clause must name a declared state.
4. The derive conditions are validated against the struct definition.
5. Named-state generic arguments must use declared cases only.

So this is rejected:

```context
struct Player[state Alive | Dead]:
    health: int

    derive state:
        Alive when self.health > 0
```

because `Dead` is missing.

And this is also rejected:

```context
Player[Ghost]
```

because `Ghost` is not a declared state of `Player`.

---

## 6. State sets and unions

Named struct states are not limited to a single case.

A value may also have a **state set**.

```context
Player[Alive | Dead]
```

This means:

- the value is known to be a `Player`
- but the compiler is not currently allowed to assume a single exact state
- the current proof state is the union of the listed cases

### Why state sets matter

State sets are how the compiler remains sound when control flow or mutation loses precision.

Typical examples:

- values entering a function without a stronger proof
- merges after conditional branches
- values after mutation through an alias or reference call
- values after any operation where the compiler cannot re-prove an exact poststate

### Canonicalization

The compiler canonicalizes state sets according to the declaration order.

If the declared family is:

```context
[state Alive | Dead | Stunned]
```

then a union will be normalized in that order.

This matters because the type system wants a canonical internal representation rather than many syntactic permutations of the same set.

---

## 7. Construction and state inference

Struct literals can participate in state inference.

Example:

```context
struct Player[state Alive | Dead]:
    health: int

    derive state:
        Alive when self.health > 0
        Dead when self.health <= 0

player: Player = Player(5)
```

The literal `Player(5)` can be inferred as `Player[Alive]` because:

- the fields are known at construction time
- the derive conditions can be evaluated against those field values
- exactly one state is proven

### Exact construction

If the expected type is already specific:

```context
player: Player[Alive] = Player(5)
```

the compiler checks that the literal satisfies `Alive`.

### Invalid construction

This is rejected:

```context
player: Player[Alive] = Player(0)
```

because the literal does not satisfy the declared derived state.

### Multiple-match or no-match construction

The compiler also rejects ambiguous or impossible derived-state results if it can prove them.

That means the derive clauses should ideally be:

- exhaustive for intended valid values
- mutually exclusive for intended exact states

If they are not, the compiler will widen or reject depending on what it can prove from the literal.

---

## 8. Refinement with `is`

Named state is refined with the `is` operator.

```context
if player is Player[Alive]:
    return take_alive(player)
return take_dead(player)
```

### True branch

Inside the true branch, `player` is refined to `Player[Alive]`.

### False branch

Inside the false branch, the tested state is subtracted from the current state set.

So if the current type is:

```context
Player[Alive | Dead]
```

then after:

```context
if player is Player[Alive]:
    ...
else:
    ...
```

the else branch sees:

```context
Player[Dead]
```

### General rule

- truthy `is` branch = intersection with the tested state set
- falsy `is` branch = subtraction of the tested state set
- branch joins = union of the surviving branch states

This is the central named-state flow rule.

---

## 9. Control-flow joins

When control flow rejoins, the compiler merges the possible states from each surviving path.

Example:

```context
def update(player: Player[Alive | Dead], cond: bool) -> Player:
    if cond:
        return Player(5)
    return Player(0)
```

The two branches return different exact states:

- one branch yields `Player[Alive]`
- one branch yields `Player[Dead]`

The join is:

```context
Player[Alive | Dead]
```

### Why this matters for soundness

A join must never claim a narrower state than the paths justify.

That means:

- joins **union** named states
- joins do **not** pick one branch arbitrarily
- if one branch preserves an exact state and another branch destroys it, the join widens

This is what keeps typestate proofs honest in ordinary branching code.

---

## 10. Mutation semantics

This is the most important part of the current implementation.

Named state is not just refined by reads and tests.
It is also **updated after writes**.

### The design principle

A write can do one of three things to named-state information:

1. **Preserve** it if the write cannot affect the derive conditions
2. **Recompute** it exactly if the new poststate can be proven
3. **Widen** it if mutation makes the exact poststate uncertain

That rule is what makes the system useful without becoming unsound.

---

## 11. Direct field mutation

Consider the canonical example:

```context
struct Player[state Alive | Dead]:
    health: mutable int
    score: mutable int

    derive state:
        Alive when self.health > 0
        Dead when self.health <= 0
```

### Mutation that affects the derived state

```context
def bad(player: mutable Player[Alive]) -> int:
    player.health <- 0
    return take_alive(player)
```

After `player.health <- 0`, the compiler can prove:

```context
player : Player[Dead]
```

So `take_alive(player)` is rejected.

### Mutation that does not affect the derived state

```context
def ok(player: mutable Player[Alive]) -> int:
    player.score <- 1
    return take_alive(player)
```

Because `score` does not appear in the derive conditions, the current state remains:

```context
player : Player[Alive]
```

### Why this rule is good

It gives the compiler a sweet spot:

- it does not throw away useful facts unnecessarily
- but it does not preserve facts that mutation could invalidate

---

## 12. Nested mutation

Typestate updates are not limited to root locals.

If a typestated value is nested inside another tracked value, mutation still updates the nested state.

Example:

```context
repr(c) struct Team:
    player: mutable Player[Alive]

def bad(team: mutable Team) -> int:
    team.player.health <- 0
    return take_alive(team.player)
```

After the nested field write, the compiler updates the tracked type of `team` so that the `player` field no longer remains falsely typed as `Player[Alive]`.

This matters because otherwise nested values would become a loophole:

- the root object would still look “precise”
- but a nested typestated field would actually be stale

The implementation now propagates typestate updates through tracked field paths rather than only root symbols.

---

## 13. Mutation through reference aliases

Direct local mutation is not the only hazard.

This is also important:

```context
def bad(player: mutable Player[Alive]) -> int:
    alias: any Player[Alive]& = (&player).cast[any Player[Alive]&]
    alias.health <- 0
    return take_alive(player)
```

If the compiler only tracked writes on `player` itself, this would be unsound.

The current behavior is:

- immutable ref aliases created from address-of expressions are traced back to the underlying lvalue
- mutation through those aliases updates the same tracked typestate root

So after `alias.health <- 0`, `player` is no longer considered `Player[Alive]`.

---

## 14. Calls through references: conservative widening

This is a crucial soundness rule.

Consider:

```context
def kill(player: any Player[Alive]&) -> void:
    player.health <- 0

def bad(player: mutable Player[Alive]) -> int:
    kill((&player).cast[any Player[Alive]&])
    return take_alive(player)
```

At the call site, the compiler cannot currently prove an exact postcondition for the callee’s mutation behavior.

So after the call, it conservatively widens the caller-visible state.

In practice this means:

```context
player : Player[Alive | Dead]
```

after the call.

### Why widen instead of guessing?

Because the call boundary is a mutation boundary.

Unless the compiler has a reliable effect/poststate summary, it must assume:

- the callee may mutate fields reachable through the reference
- the exact derived state may therefore be unknown afterward

### Current rule

For calls with reference arguments, the compiler conservatively widens named-state information reachable through the passed lvalue path.

That includes wrapper cases such as:

```context
repr(c) struct Team:
    player: mutable Player[Alive]

def kill_team(team: any Team&) -> void:
    team.player.health <- 0

def bad(team: mutable Team) -> int:
    kill_team((&team).cast[any Team&])
    return take_alive(team.player)
```

After the call, the nested `team.player` state is widened to a safe union state rather than preserving a stale exact state.

### What this buys us

This rule is conservative, but sound:

- it may lose precision
- but it avoids lying about post-call state

That is exactly the tradeoff you want in a low-level system without a full mutation-effect language.

---

## 15. What counts as “affecting derived state”

The compiler currently treats a mutation as relevant to derived state when the mutated path overlaps with a field path used in a `derive state:` condition.

Examples:

```context
Alive when self.health > 0
```

means writes to:

- `self.health`
- or a larger path whose prefix includes `health`

are considered relevant.

For nested predicates such as:

```context
Open when self.socket.fd >= 0
```

writes anywhere under `self.socket...` are conservatively treated as potentially relevant.

### Exact vs conservative behavior

- direct single-field writes can sometimes be re-evaluated exactly
- deeper or more complex writes are widened conservatively when exact proof is not available

This is intentional.

The guiding rule is:

> if exact proof is easy and trustworthy, use it;
> otherwise widen.

---

## 16. Interaction with `is` after mutation

A nice way to understand the system is this timeline:

1. construct or receive a value with some state set
2. narrow it using `is`
3. mutate it
4. lose or recompute precision
5. narrow it again if needed

Example:

```context
def route(player: mutable Player) -> int:
    if player is Player[Alive]:
        player.health <- 0
    if player is Player[Alive]:
        return 1
    return 0
```

The first `is` narrows.
The mutation invalidates that exact proof.
The second `is` must re-establish it.

That is the intended programming model.

Refinement is not a permanent entitlement.
Mutation can revoke it.

---

## 17. Interaction with references and nullness

These layers stack.

Example:

```context
player_ref: any Player[Alive]&?
```

This carries two independent facts:

- the reference itself may be null
- if non-null, it points to a `Player` whose current value-state is `Alive`

Before field access, you still need the reference proof:

```context
if player_ref != null:
    return player_ref.health
```

But after mutation through the reference, the pointee value-state may widen or change even though the outer reference remains non-null.

So think of these as separate proof layers:

- **outer ref state** = pointer usability
- **inner named state** = value protocol/invariant state

---

## 18. Interaction with aggregate state parameters

Named states and aggregate state placeholders can coexist.

The compiler models them as separate concerns:

- aggregate state markers are positional state slots
- named state is an explicit generic state family attached to the struct

In implementation terms, named state is carried as a generic argument for the struct instance, while aggregate state may additionally wrap the type in an aggregate-state carrier.

That means a type can carry both:

- a named semantic state family
- one or more orthogonal low-level state markers

The important takeaway is that these are not competing mechanisms.
They are different layers of typestate.

---

## 19. Soundness strategy

The current soundness strategy is simple and deliberate.

### The compiler may strengthen state only when justified by:

- the declared type
- a struct literal whose fields prove a derived state
- a flow refinement like `is`
- a direct mutation whose poststate can be re-evaluated exactly

### The compiler must widen or invalidate state when:

- branches rejoin with different surviving states
- a relevant mutation destroys exact proof
- a mutation happens through a reference alias
- a call receives a reference that may let the callee mutate the value

### The compiler must not:

- preserve exact derived-state facts after relevant mutation without proof
- pretend aliases do not alias
- guess an exact post-call typestate in the absence of a postcondition summary

That philosophy is the heart of the system.

It is intentionally **sound-first, precision-second** at mutation boundaries.

---

## 20. What the compiler tracks internally

At a high level, the implementation uses a few different representations.

### Pointer/ref states

Reference nullness is modeled with internal ref-state enums and ref types.

### Named struct states

Named state uses internal state-case and state-set types, conceptually like:

- a single-case state type
- a set/union state type

The compiler has helper operations for:

- canonicalization
- intersection
- subtraction
- union/merge
- assignability

### Tracked specialized value types

During semantic analysis, the compiler maintains tracked types for values that become more precise than their declared type.

This is how it can remember facts like:

- a local variable currently has `Player[Alive]`
- a nested field currently has `Player[Dead]`
- a wrapper’s tracked field type changed after mutation

### Flow refinements

Control-flow refinements are attached to expression keys and merged across branches.

For named-state refinement:

- `is` truthy branches intersect the state set
- falsy branches subtract from it
- joins merge back to unions

### Mutation updates

Assignments now update tracked typestate not only at the root variable level but also along nested tracked paths and through immutable address-of aliases.

That is the key implementation detail that keeps nested derived state from going stale.

---

## 21. Current implementation map

If you want to read the implementation, these are the most relevant places.

### Parser

- `compiler/src/parser/parser.go`
- `compiler/src/parser/parser_expr.go`
- `compiler/src/parser/parser_typestate_test.go`

These handle:

- aggregate state placeholder syntax
- named state declaration syntax
- `derive state:` parsing
- state-set parsing like `Player[Alive | Dead]`

### Semantic type model

- `compiler/src/semantic/types.go`
- `compiler/src/semantic/types_compare.go`
- `compiler/src/semantic/analyzer_types.go`

These define:

- named state case/set types
- aggregate state wrappers
- assignability and merge rules
- generic binding for state parameters

### Flow and mutation logic

- `compiler/src/semantic/analyzer_flow.go`
- `compiler/src/semantic/analyzer_expr.go`

These handle:

- `is` refinement
- branch joins
- mutation-time recomputation/widening
- nested-path updates
- conservative invalidation after ref calls

### Backend and interpreter

- `compiler/src/backend/llvm_support.go`
- `compiler/src/backend/llvm_exprs.go`
- `compiler/src/interpreter/interpreter.go`

These carry the semantic understanding through lowering and execution-oriented components.

### Tests

- `compiler/test/semantic/semantic_test.go`
- `compiler/src/parser/parser_typestate_test.go`

Look there for examples covering:

- derived-state literals
- `is` narrowing
- post-mutation invalidation
- nested mutation
- alias mutation
- ref-call widening

---

## 22. Practical programming guidance

### Prefer named states when the meaning matters

Use named states for semantic invariants:

```context
Socket[Open]
Socket[Closed]
Parser[Ready]
Parser[Failed]
```

### Prefer aggregate placeholders when the state is mechanical

Use positional aggregate states when you just need orthogonal state markers threaded through low-level wrappers.

### Keep derive conditions simple

The best derive conditions are:

- field-local
- obvious
- cheap to reason about
- mutually exclusive when exact states matter

Good:

```context
Alive when self.health > 0
Dead when self.health <= 0
```

Riskier:

```context
Ready when complicated_call(self.x, self.y, self.z)
```

Simple field expressions make exact post-mutation inference much more reliable and predictable.

### Re-prove after mutation

If code mutates a typestated value, expect to either:

- get a new exact state if the compiler can prove one
- or re-test with `is`

### Expect widening across ref calls

If you pass a typestated value by reference into a function call, assume the caller-visible typestate may widen afterward unless the API is explicitly structured to avoid that.

That is the current conservative rule.

---

## 23. Limitations and future directions

The current system is intentionally conservative at some boundaries.

### Current limitations

- there is no first-class user syntax for ref-parameter poststate summaries
- the compiler does not currently infer rich interprocedural typestate postconditions across arbitrary calls
- reference calls therefore widen named-state facts conservatively
- exact post-mutation inference is strongest for simple direct field writes

### Plausible future extensions

A future system could add things like:

- function annotations describing poststate of ref parameters
- explicit protocol transitions on refs
- stronger interprocedural mutation summaries
- deeper exact recomputation rules for more complex derive expressions

But the current system deliberately stops short of that in order to stay simple and predictable.

---

## 24. Canonical examples

### Simple construction and narrowing

```context
struct Player[state Alive | Dead]:
    health: mutable int

    derive state:
        Alive when self.health > 0
        Dead when self.health <= 0

def take_alive(player: Player[Alive]) -> int:
    return player.health

def route(player: Player) -> int:
    if player is Player[Alive]:
        return take_alive(player)
    return 0
```

### Exact post-mutation update

```context
def kill_local(player: mutable Player[Alive]) -> void:
    player.health <- 0
    # player is now Player[Dead]
```

### Unrelated-field preservation

```context
struct Player[state Alive | Dead]:
    health: mutable int
    score: mutable int

    derive state:
        Alive when self.health > 0
        Dead when self.health <= 0

def bump_score(player: mutable Player[Alive]) -> int:
    player.score <- player.score + 1
    return player.health
```

### Conservative widening after ref call

```context
def poke(player: any Player[Alive]&) -> void:
    player.health <- 0

def use_after_call(player: mutable Player[Alive]) -> int:
    poke((&player).cast[any Player[Alive]&])

    # caller-visible state is widened conservatively
    if player is Player[Alive]:
        return player.health
    return 0
```

---

## 25. The short version

If you want the whole feature set in one sentence:

> Contextlang typestate is a layered proof system: references track usability, structs can carry positional or named state, control flow narrows those states, and mutation either recomputes them exactly or widens them conservatively when proof is no longer justified.

And if you want the operational slogan:

> **Narrow when reading, widen when mutation makes certainty unsafe.**
