# Unified Poststate `ensures` Clauses

This document proposes the **next precision feature** for Contextlang typestate:

> explicit `ensures` summaries for how a call changes the caller-visible state of tracked argument paths.

The key update in this revision is deliberate:

- use **one** poststate mechanism for ordinary named typestates **and** pointer/refstates
- prefer concise refstate spellings like `ensures node => !`
- avoid inventing a separate pointer-only keyword or contract system

This keeps the design orthogonal:

- `ensures job => Ready`
- `ensures sock => Closed`
- `ensures node => !`
- `ensures ptr => &`
- `ensures value => preserve`

This is the concrete follow-up to:

- `14-typestate-system.md`
- `15-typestate-practical-cheat-sheet.md`

---

## 1. Problem statement

Today the compiler is intentionally conservative at ref-call boundaries.

### 1.1 Named-state precision cliff

Given:

```context
struct ParseJob[state Pending | Ready | Failed]:
    stage: mutable int
    checksum: mutable int

    derive state:
        Pending when self.stage == 0
        Ready when self.stage == 1
        Failed when self.stage == 2

def finish_ok(job: any ParseJob[Pending]&) -> void:
    job.checksum <- 7
    job.stage <- 1
```

the caller currently loses precision after:

```context
finish_ok((&job).cast[any ParseJob[Pending]&])
```

because mutation crossed a ref-call boundary and the caller has no poststate summary for the callee.

So the caller sees a safe widened type like:

```context
ParseJob[Pending | Ready | Failed]
```

That is the correct soundness choice **today**.
It is also the most obvious place to win precision back **next**.

### 1.2 Pointer/refstate boilerplate cliff

Today ownership-style helpers often need awkward “return the new pointer state so the caller can reassign” boilerplate:

```context
extern sfree_heap_pair_node(node: heap HeapPairNode&) -> heap HeapPairNode! can[Memory.Release]

node as ! <- sfree_heap_pair_node(node)
```

That works, but it pushes the poststate update into the call site instead of the API contract.

The more direct surface is:

```context
extern sfree_heap_pair_node(node: heap HeapPairNode&) -> void
    can[Memory.Release]
    ensures node => !
```

The caller then writes:

```context
sfree_heap_pair_node(node)
```

and the tracked state of `node` updates automatically at the call boundary.

---

## 2. Non-negotiable semantic rule

This feature must be **statically verified effect typing**, not runtime contract checking.

That means:

- for ordinary user-defined functions, `ensures` clauses must be proven by the compiler
- they must **not** merely mean “assert this at runtime”
- they must **not** become “trust me, crash if wrong” contracts

So if a function says:

```context
def finish_ok(job: any ParseJob[Pending]&) -> void ensures job => Ready:
    ...
```

the compiler should accept that only if every normal return path proves `job` is `Ready`.

And if a function says:

```context
def require_non_null(node: heap HeapPairNode&?) -> void ensures node => &:
    ...
```

the compiler should accept that only if every normal return path proves `node` is non-null.

This is the core design constraint.

---

## 3. Design goals

### Must do

- preserve soundness at call boundaries
- let APIs state exact poststates explicitly
- support the practical cases users actually write
- unify named typestate and refstate summaries under the same mechanism
- compose with named derived states, nested paths, and existing flow refinement
- force callers to handle uncertainty when the poststate is genuinely not exact

### Must not do in the first cut

- require whole-program inference
- infer arbitrary mutation summaries automatically
- add a theorem-prover-like transition language
- invent a separate pointer-only poststate system
- make the typestate model depend on hidden callee body magic
- turn poststates into runtime-only design-by-contract assertions

The whole point is to stay **sound-first, explicit-second, inference-later**.

---

## 4. Preferred source surface

The preferred source-language surface should be a single **`ensures` clause**.

For named typestate:

```context
def finish_ok(job: any ParseJob[Pending]&) -> void ensures job => Ready:
    job.checksum <- 7
    job.stage <- 1
```

For pointer/refstate:

```context
extern sfree_heap_pair_node(node: heap HeapPairNode&) -> void
    can[Memory.Release]
    ensures node => !
```

This says:

> on normal return, the tracked state of the target path rooted at `node` becomes null.

### Why prefer `ensures`?

Because it keeps the poststate summary visibly separate from the parameter type while still reading like a contract.

That gives it four big advantages:

- it scales naturally to **nested paths** like `team.player`
- it scales naturally to **multiple summaries** on one function
- it avoids overloading `Type[...]` syntax with transition semantics
- it avoids inventing a second pointer-specific poststate feature

Internally, named typestates and refstates may still be represented differently.
What is unified is the **user-facing poststate effect system**.

---

## 5. Proposed syntax

### 5.1 Exact named poststate

```context
def finish_ok(job: any ParseJob[Pending]&) -> void ensures job => Ready:
    ...
```

### 5.2 Exact named union poststate

```context
def finish(job: any ParseJob[Pending]&) -> void ensures job => Ready | Failed:
    ...
```

This means the caller should see exactly the union `Ready | Failed` after a normal return.

### 5.3 Refstate poststate

```context
extern sfree_heap_pair_node(node: heap HeapPairNode&) -> void
    can[Memory.Release]
    ensures node => !
```

```context
def require_non_null(node: heap HeapPairNode&?) -> void ensures node => &:
    ...
```

```context
def maybe_invalidate(node: heap HeapPairNode&) -> void ensures node => &?:
    ...
```

The marker on the right-hand side changes only the **refstate** of the tracked target path:

- `&` means proven non-null
- `&?` means nullable / maybe-null
- `!` means null

All other qualifiers are preserved:

- pointee type
- storage (`heap`, `stack`, `any`, etc.)
- mutability qualifier on the pointee

So `ensures node => !` is shorthand for:

> same reference type as `node`, but with refstate `!`.

### 5.4 Preserve current caller-visible state

```context
def bump_score(player: any Player[Alive]&) -> void ensures player => preserve:
    ...
```

This means the call does not invalidate the caller-visible state for that path.

### 5.5 Nested path

```context
def kill_team(team: any Team&) -> void ensures team.player => Dead:
    ...
```

```context
def release_slot(holder: any Owner&) -> void ensures holder.node => !:
    ...
```

### 5.6 Multiple summaries

Single-line form:

```context
def kill_team(team: any Team&) -> void ensures team.player => Dead, team.armor => Destroyed:
    ...
```

Readable multiline form:

```context
def process(team: any Team&, sock: any Socket&) -> void
    ensures team.player => Dead,
            sock => preserve:
    ...
```

---

## 6. Path grammar

The `ensures` target should use a parameter-rooted path grammar:

```text
target-path ::= param
              | param . field
              | param . field . field
              | param [index]
              | param [*]
              | param . field [index] ...
```

### Recommended phase-1 restriction

Even though the long-term grammar can support indexed and wildcard paths, the **first implementation** should probably accept only:

- root parameter names
- dotted struct-field paths rooted at a parameter

So the initial sweet spot is:

- `job`
- `sock`
- `node`
- `team.player`
- `holder.node`

and **not yet**:

- `jobs[*]`
- `jobs[0]`

Those can come later if real use cases demand them.

---

## 7. Semantics

### 7.1 Normal-return only

The summary applies on the continuation path after a **normal** return.

That means the first cut should either:

- support only non-fallible functions, or
- define the summary only for the success path of fallible functions

The simplest first implementation is the second option stated conservatively:

> `ensures` applies to the normal-success continuation path of the call.

### 7.2 Exact named-state summary

```context
ensures job => Ready
```

means:

> after a normal return, replace the caller-visible named state of `job` with `Ready`.

And:

```context
ensures job => Ready | Failed
```

means:

> after a normal return, replace the caller-visible named state of `job` with `Ready | Failed`.

### 7.3 Refstate summary

```context
ensures node => !
```

means:

> after a normal return, replace the caller-visible refstate of `node` with `!`, preserving its pointee type, storage, and mutability qualifiers.

Likewise:

```context
ensures node => &
ensures node => &?
```

mean:

> after a normal return, replace the caller-visible refstate of `node` with `&` or `&?`, again preserving all other qualifiers.

This is intentionally concise because the right-hand side is expressing a **refstate effect**, not a whole-type replacement.

### 7.4 Preserve summary

```context
ensures sock => preserve
```

means:

> preserve the caller’s incoming tracked fact for `sock`.

This is stronger than merely resetting the caller to the declared parameter type.

If the caller proved `Socket[Open]` before the call, `preserve` should keep `Socket[Open]`.
If the caller only knew `heap HeapPairNode&?` before the call, `preserve` should keep that wider refstate.

That is why `preserve` is useful.
It expresses an **identity summary**, not just “no worse than the callee parameter type”.

### 7.5 Target path only, not all aliases

This point is crucial.

If a call says:

```context
ensures node => !
```

then the guaranteed poststate applies to the **tracked target path** `node`.

It does **not** mean that all possible aliases are automatically also known to be `!`.

If the call may invalidate other overlapping aliases, the compiler must handle them conservatively according to the alias model.

### 7.6 Why this still forces handling when needed

If the callee only guarantees a union:

```context
def finish(job: any ParseJob[Pending]&) -> void ensures job => Ready | Failed:
    ...
```

then the caller only gets:

```context
job : ParseJob[Ready | Failed]
```

after the call.

So if they need `Ready`, they must still narrow with `is`.

That is exactly the behavior we want:

- exact poststates remove unnecessary re-checks
- non-exact poststates still force handling through the type system

---

## 8. Examples

### 8.1 Parser/job state

```context
struct ParseJob[state Pending | Ready | Failed]:
    stage: mutable int
    checksum: mutable int

    derive state:
        Pending when self.stage == 0
        Ready when self.stage == 1
        Failed when self.stage == 2

def finish_ok(job: any ParseJob[Pending]&) -> void ensures job => Ready:
    job.checksum <- 7
    job.stage <- 1
```

### 8.2 Resource close

```context
struct Socket[state Open | Closed]:
    fd: mutable int

    derive state:
        Open when self.fd >= 0
        Closed when self.fd < 0

def close_socket(sock: any Socket[Open]&) -> void ensures sock => Closed:
    sock.fd <- -1
```

### 8.3 Free helper with refstate postcondition

```context
extern sfree_heap_pair_node(node: heap HeapPairNode&) -> void
    can[Memory.Release]
    ensures node => !
```

Now the caller writes:

```context
sfree_heap_pair_node(node)
```

instead of:

```context
node as ! <- sfree_heap_pair_node(node)
```

### 8.4 Require/prove non-null

```context
def require_non_null(node: heap HeapPairNode&?) -> void can[Abort.Panic] ensures node => &:
    assert node != null
```

This is a useful example of a call that does **not** change the pointer value, but does strengthen the caller-visible refstate on normal return.

### 8.5 Preserve state across irrelevant mutation

```context
struct Player[state Alive | Dead]:
    health: mutable int
    score: mutable int

    derive state:
        Alive when self.health > 0
        Dead when self.health <= 0

def bump_score(player: any Player[Alive]&) -> void ensures player => preserve:
    player.score <- player.score + 1
```

### 8.6 Nested path

```context
struct Team:
    player: mutable Player[Alive]
    slot: mutable heap HeapPairNode&?

def kill_team(team: any Team&) -> void ensures team.player => Dead:
    team.player.health <- 0

def clear_slot(team: any Team&) -> void ensures team.slot => !:
    team.slot <- null
```

---

## 9. Validation rules

For an `ensures` entry:

```context
ensures target => effect
```

the compiler should enforce all of the following.

### 9.1 Target path must be parameter-rooted

`target` must start at a function parameter name.

Reject:

- locals
- globals
- arbitrary expressions

### 9.2 Target path must resolve to a trackable poststate target

The projected target type must resolve to one of:

- a named-state-bearing value type, or
- a ref type

Reject:

- plain structs with no named states when using named-state effects
- aggregate-state-only carriers when using named-state effects
- non-ref targets when using refstate effects like `!`

### 9.3 Effect kind must match target kind

These are valid:

```context
ensures job => Ready
ensures sock => preserve
ensures node => !
ensures maybe_node => &
```

These are invalid:

```context
ensures job => !          # if job is not a ref type
ensures node => Ready     # if node is just a ref, not a named-state-bearing pointee path
```

### 9.4 `preserve` is exclusive

These are valid:

```context
ensures sock => preserve
ensures job => Ready
ensures node => !
```

These are invalid:

```context
ensures job => preserve | Ready
ensures node => preserve | !
```

### 9.5 Named-state cases must belong to the target family

If `job` is a `ParseJob[...]`, then:

```context
ensures job => Ready
```

is fine, but:

```context
ensures job => Closed
```

must be rejected.

### 9.6 Refstate effects may only use refstate markers

Phase 1 should only allow:

- `&`
- `&?`
- `!`

on the right-hand side for refstate effects.

This keeps the feature tight and orthogonal.

### 9.7 The body must prove the summary

For ordinary user-defined functions, the compiler must prove the summary.

That means:

- exact named-state `ensures` must be proven on every normal return path
- exact refstate `ensures` must be proven on every normal return path
- `preserve` must be justified by the absence of relevant mutation hazards for that path
- if the body cannot justify the clause, reject the function

This is the rule that keeps `ensures` in the effect-typing lane instead of the design-by-contract lane.

---

## 10. How caller-side application should work

Today the call-site rule is effectively:

> if a ref argument may let the callee mutate tracked state, widen that caller-visible path conservatively.

The new rule should become:

1. Start from the current conservative widening behavior.
2. For each `ensures` target reached by the call:
   - `preserve` means **do not widen that path**.
   - explicit named-state cases mean **replace that path with the declared named state set**.
   - explicit refstate markers mean **replace that path’s refstate with the declared marker**, preserving the other qualifiers.
3. If overlapping or aliasing summaries disagree, merge conservatively.

That last part matters.

---

## 11. Aliasing and overlap rules

This is where soundness earns its lunch.

### 11.1 Ensured + unensured overlapping refs

If the same caller-visible path may be reachable through:

- one ensured ref argument, and
- another unannotated / unensured ref argument,

then the unensured path still forces conservative fallback.

In other words:

> `ensures` should only recover precision when it dominates all relevant mutation summaries for that path.

### 11.2 Two ensured overlapping refs with different named outcomes

If both may alias and they disagree:

```text
ensures a => Ready
ensures b => Failed
```

then the caller-visible result for an overlapping named-state path must conservatively join to:

```text
Ready | Failed
```

### 11.3 Two ensured overlapping refs with different refstate outcomes

If both may alias and they disagree:

```text
ensures a => !
ensures b => &
```

then the compiler must **not** over-sharpen.

Phase 1 should fall back conservatively.
If a merge is needed for the same tracked path and neither summary dominates, the safe default is the least precise assignable refstate, typically `&?`, or the existing conservative alias fallback.

### 11.4 `!` applies to the target path, not all aliases

If one ensured path says:

```text
ensures node => !
```

that does **not** mean every alias automatically becomes `!`.

For overlapping aliases, the compiler should:

- preserve the exact `!` fact for the target path when safe
- conservatively widen, invalidate, or otherwise avoid sharpening overlapping aliases unless the alias analysis proves that is sound

This matters especially for free/release-style APIs.

### 11.5 `preserve` mixed with exact poststate

If one aliasing path says `preserve` and another says `Dead` or `!`, then the exact summary only wins if overlap analysis proves it truly governs that path.

Otherwise, fall back conservatively.

The core rule is:

> `ensures` can sharpen the existing rule, but must never beat a stronger aliasing hazard than it actually summarizes.

---

## 12. Branch-sensitive poststates

For APIs where the caller **must inspect an outcome**, the right extension is a branch-sensitive form tied to the return value.

For example:

```context
def try_finish(job: any ParseJob[Pending]&) -> bool
    ensures return true  => job => Ready,
            return false => job => Failed:
    ...
```

And similarly for refstates:

```context
def maybe_release(node: heap HeapPairNode&) -> bool
    ensures return true  => node => !,
            return false => node => preserve:
    ...
```

This is probably best treated as a **phase-2 extension**, after unconditional `ensures path => effect` lands.

---

## 13. Recommended phase-1 scope

To keep the first implementation tight and high-confidence, I would intentionally restrict it to:

- function and extern-function `ensures` clauses only
- parameter-rooted struct field paths only
- named-state-bearing targets and ref-typed targets only
- named-state effects, refstate effects, and `preserve`
- normal-return semantics only
- statically validated summaries for user-defined functions
- conservative fallback whenever alias overlap is unclear

I would explicitly **not** do these yet:

- wildcard/indexed poststate paths
- per-error-path summaries
- branch-sensitive `return true/false => ...` clauses in phase 1
- full whole-type poststate replacement syntax for ref effects
- automatic interprocedural inference

That is enough to solve the pain exposed by both motivating examples without making the feature sprawl.

---

## 14. Suggested implementation shape

### Parser / AST

This design requires a small grammar hook after the return type / permission list and before the trailing `:`.

The parser should support something in this family:

```text
def ... -> Ret ensures path => effect, path => effect:
```

The path representation itself can still reuse the same structural idea as the existing annotation-path parser:

- `job`
- `team.player`
- later, possibly `items[*]`

So the syntax changes are source-level, but the internal path model can stay lightweight.

### Semantic signature model

Add a call-summary structure to `FuncType`, conceptually something like:

```text
FuncPoststateEffect {
    ParamIndex
    Path
    Mode           # preserve | named-state | refstate
    StateCases      # when named-state
    RefStateMarker  # when refstate: &, &?, or !
}
```

The path representation can reuse the existing `borrowReturnAnnotationStep` shape.

### Function-body validation

For ordinary `def` functions:

- validate each `ensures` target and effect kind
- validate each normal return path against the summary
- reject the function if the summary is not statically provable

For `extern` functions:

- validate only the target-path and target-kind surface
- trust the declared summary the same way other extern summaries are trusted

### Call-site flow application

The current widening hook already lives in the call-side flow logic.

The first implementation should extend that path so it:

- checks for a matching poststate effect on the callee
- applies `preserve`, exact named-state replacement, or exact refstate replacement when safe
- falls back to the current conservative behavior otherwise

The natural files to touch are:

- `compiler/src/parser/parser_statements.go`
- `compiler/src/parser/parser_typestate_test.go`
- `compiler/src/ast/ast.go`
- `compiler/src/semantic/analyzer.go`
- `compiler/src/semantic/analyzer_flow.go`
- `compiler/src/semantic/analyzer_expr.go`
- `compiler/test/semantic/semantic_test.go`

---

## 15. Test matrix to add when implementing

At minimum, implementation should add coverage for:

1. **Parse / validation**
   - `ensures job => Ready` accepted
   - `ensures node => !` accepted
   - unknown parameter rejected
   - non-stateful target rejected for named-state effects
   - non-ref target rejected for refstate effects
   - invalid state name rejected
   - invalid refstate marker rejected
   - `preserve` mixed with state cases rejected
   - multiple `ensures` targets accepted

2. **Caller precision recovery**
   - parser/job `Pending -> Ready`
   - socket `Open -> Closed`
   - free helper `node -> !`
   - non-null helper `node -> &`
   - nested `team.player -> Dead`
   - nested `holder.node -> !`
   - preserve across unrelated mutation helper

3. **Conservative fallback**
   - overlapping ensured + unensured alias still widens
   - conflicting ensured named states union conservatively
   - conflicting ensured refstates fall back conservatively
   - `!` does not magically make unrelated aliases `!`
   - unsupported path forms still widen

4. **Body validation**
   - wrong named poststate rejected
   - wrong refstate poststate rejected
   - invalid `preserve` body rejected after relevant mutation
   - non-provable exact poststate rejected

5. **Phase-2 branch-sensitive follow-up**
   - `return true => job => Ready`
   - `return false => job => Failed`
   - `return true => node => !`
   - ignored result yields the joined state
   - checked result narrows each branch appropriately

---

## 16. Why this is the right next step

Because it directly attacks the biggest current poststate precision cliffs while staying aligned with the rest of the design:

- explicit
- local
- predictable
- sound
- statically checked
- incrementally implementable
- orthogonal across named typestate and pointer/refstate

It is also a good fit for the examples we already have:

- `ParseJob[Pending | Ready | Failed]`
- `Socket[Open | Closed]`
- `ScratchBuffer[Uninitialized | Initialized]`
- free/release helpers like `sfree_heap_pair_node`
- nested wrapper paths like `team.player` and `holder.node`

In short:

> do not guess poststates interprocedurally yet; let APIs state them explicitly with `ensures`, prove those summaries statically for ordinary functions, and use the same mechanism for both named typestate and concise refstate effects like `ensures node => !`.

That is a very good next trade.

This document proposes the **next precision feature** for Contextlang typestate:

> explicit summaries for how a call changes the typestate of ref arguments.

The goal is narrow and deliberate:

- keep the current soundness model
- recover precision at the place users will feel it most
- avoid full interprocedural typestate inference for now

This is the concrete follow-up to:

- `14-typestate-system.md`
- `15-typestate-practical-cheat-sheet.md`

---

## 1. Problem statement

Today the compiler is intentionally conservative at ref-call boundaries.

Given:

```context
struct ParseJob[state Pending | Ready | Failed]:
    stage: mutable int
    checksum: mutable int

    derive state:
        Pending when self.stage == 0
        Ready when self.stage == 1
        Failed when self.stage == 2

def finish_ok(job: any ParseJob[Pending]&) -> void:
    job.checksum <- 7
    job.stage <- 1
```

the caller currently loses precision after:

```context
finish_ok((&job).cast[any ParseJob[Pending]&])
```

because mutation crossed a ref-call boundary and the caller has no poststate summary for the callee.

So the caller sees a safe widened type like:

```context
ParseJob[Pending | Ready | Failed]
```

That is the correct soundness choice **today**.
It is also the most obvious place to win precision back **next**.

---

## 2. Design goals

### Must do

- preserve soundness at call boundaries
- let APIs state exact ref-argument poststates explicitly
- support the practical cases users actually write
- compose with named derived states, nested paths, and existing flow refinement
- fit the current annotation-heavy incremental implementation style

### Must not do in the first cut

- require whole-program inference
- infer arbitrary mutation summaries automatically
- add a theorem-prover-like transition language
- make the typestate model depend on hidden callee body magic

The entire point is to stay **sound-first, explicit-second, inference-later**.

---

## 3. Recommended first-cut surface

The preferred source-language surface should be an **`ensures` clause**:

```context
def finish_ok(job: any ParseJob[Pending]&) -> void ensures job => Ready:
    job.checksum <- 7
    job.stage <- 1
```

This says:

> on normal return, the ref target rooted at `job` is known to be in state `Ready`.

### Why prefer `ensures`?

Because it keeps the poststate summary visibly separate from the parameter type while still reading like a contract.

That gives it three big advantages over annotation-first or type-embedded spellings:

- it scales naturally to **nested paths** like `team.player`
- it scales naturally to **multiple summaries** on one function
- it avoids overloading `Type[...]` syntax with transition semantics

The compiler can still represent this internally as a normal poststate-effect summary, and an implementation may even temporarily lower it into an annotation-like internal form. But the **source syntax** should be `ensures`.

---

## 4. Proposed syntax

### Exact poststate

```context
ensures job => Ready
```

### Exact union poststate

```context
ensures job => Ready | Failed
```

This means the caller should see exactly the union `Ready | Failed` after a normal return.

### Preserve current caller-visible state

```context
ensures sock => preserve
```

This means the call does not invalidate the caller-visible typestate for `sock`.

### Nested path

```context
ensures team.player => Dead
```

This means the summary applies to a field path rooted at a ref parameter, not necessarily the whole parameter root.

### Multiple summaries

Single-line form:

```context
def kill_team(team: any Team&) -> void ensures team.player => Dead, team.armor => Destroyed:
    ...
```

Readable multiline form:

```context
def kill_team(team: any Team&) -> void
    ensures team.player => Dead,
            team.armor  => Destroyed:
    ...
```

---

## 5. Path grammar

The `ensures` target should use a parameter-rooted path grammar:

```text
target-path ::= param
              | param . field
              | param . field . field
              | param [index]
              | param [*]
              | param . field [index] ...
```

### Recommended phase-1 restriction

Even though the long-term grammar can support indexed and wildcard paths, the **first implementation** should probably accept only:

- root parameter names
- dotted struct-field paths rooted at a parameter

So the initial sweet spot is:

- `job`
- `sock`
- `team.player`

and **not yet**:

- `jobs[*]`
- `jobs[0]`

Those can come later if real use cases demand them.

---

## 6. Semantics

### 6.1 Normal-return only

The summary applies on the continuation path after a **normal** return.

That means the first cut should either:

- support only non-fallible functions, or
- define the summary only for the success path of fallible functions

The simplest first implementation is the first option.

### 6.2 Exact state summary

```context
ensures job => Ready
```

means:

> after a normal return, replace the caller-visible state of `job` with `Ready`.

And:

```context
ensures job => Ready | Failed
```

means:

> after a normal return, replace the caller-visible state of `job` with `Ready | Failed`.

### 6.3 Preserve summary

```context
ensures sock => preserve
```

means:

> preserve the caller’s incoming typestate fact for `sock`.

This is stronger than merely resetting the caller to the declared parameter type.

If the caller proved `Socket[Open]` before the call, `preserve` should keep `Socket[Open]`.
If the caller only knew `Socket[Open | Closed]`, `preserve` should keep that wider state.

That is why `preserve` is useful.
It expresses an **identity summary**, not just “no worse than the callee parameter type”.

---

## 7. Examples

### 7.1 Parser/job state

```context
struct ParseJob[state Pending | Ready | Failed]:
    stage: mutable int
    checksum: mutable int

    derive state:
        Pending when self.stage == 0
        Ready when self.stage == 1
        Failed when self.stage == 2

def finish_ok(job: any ParseJob[Pending]&) -> void:
    ensures job => Ready:
    job.checksum <- 7
    job.stage <- 1
```

Now the caller can keep precision after the call without re-proving with `is`.

### 7.2 Resource close

```context
struct Socket[state Open | Closed]:
    fd: mutable int

    derive state:
        Open when self.fd >= 0
        Closed when self.fd < 0

def close_socket(sock: any Socket[Open]&) -> void:
    ensures sock => Closed:
    sock.fd <- -1
```

### 7.3 Preserve state across irrelevant mutation

```context
struct Player[state Alive | Dead]:
    health: mutable int
    score: mutable int

    derive state:
        Alive when self.health > 0
        Dead when self.health <= 0

def bump_score(player: any Player[Alive]&) -> void:
    ensures player => preserve:
    player.score <- player.score + 1
```

### 7.4 Nested path

```context
struct Team:
    player: mutable Player[Alive]

def kill_team(team: any Team&) -> void:
    ensures team.player => Dead:
    team.player.health <- 0
```

This is especially valuable because the current widening logic already understands nested tracked paths.

---

## 8. Validation rules

For an `ensures` entry:

```context
ensures target => effect
```

the compiler should enforce all of the following.

### 8.1 Target path must be parameter-rooted

`target` must start at a function parameter name.

Reject:

- locals
- globals
- arbitrary expressions

### 8.2 Target path must resolve to a named-state-bearing type

The projected target type must be a struct instance with a named state family.

Reject:

- plain structs with no named states
- aggregate-state-only carriers
- refs whose pointee path does not end in a named-state-bearing value

### 8.3 `preserve` is exclusive

These are valid:

```context
ensures sock => preserve
ensures job => Ready
ensures job => Ready | Failed
```

This is invalid:

```context
ensures job => preserve | Ready
```

or any equivalent mixed spelling.

### 8.4 State names must belong to the target’s family

If `job` is a `ParseJob[...]`, then:

```context
ensures job => Ready
```

is fine, but:

```context
ensures job => Closed
```

must be rejected.

### 8.5 The clause should allow multiple target summaries

This is important.

Unlike a whole-parameter inline transition syntax, `ensures` should naturally support **multiple target summaries**, because real functions may need more than one:

```context
def process(team: any Team&, sock: any Socket&) -> void
    ensures team.player => Dead,
            sock => preserve:
    ...
```

---

## 9. How caller-side application should work

Today the call-site rule is effectively:

> if a ref argument may let the callee mutate named-state-bearing data, widen that caller-visible path conservatively.

The new rule should become:

1. Start from the current conservative widening behavior.
2. For each annotated ref target reached by the call:
   - `preserve` means **do not widen that path**.
   - explicit state cases mean **replace that path with the annotated state set**.
3. If overlapping or aliasing summaries disagree, merge conservatively.

That last part matters.

---

## 10. Aliasing and overlap rules

This is where soundness earns its lunch.

### 10.1 Annotated + unannotated overlapping refs

If the same caller-visible path may be reachable through:

- one annotated ref argument, and
- another unannotated ref argument,

then the unannotated path still forces conservative widening.

In other words:

> annotations should only recover precision when they dominate all relevant mutation summaries for that path.

### 10.2 Two annotated overlapping refs with different exact outcomes

If both may alias and they disagree:

```text
ensures a => Ready
ensures b => Failed
```

then the caller-visible result for an overlapping root/path must conservatively join to:

```text
Ready | Failed
```

### 10.3 `preserve` mixed with exact poststate

If one aliasing path says `preserve` and another says `Dead`, then `Dead` only wins if the overlap analysis proves the `Dead` summary is the one that actually governs that path.

Otherwise, fall back to union or ordinary widening.

The core rule is:

> `ensures` clauses can sharpen the existing rule, but must never beat a stronger aliasing hazard than they actually summarize.

---

## 11. What `preserve` should mean for user-defined functions

`preserve` should not be blind trust.

For user-defined functions, the compiler should validate it.

### Recommended phase-1 validation rule

Treat `ensures path => preserve` as valid only if every normal-return path avoids any **named-state-relevant mutation hazard** for that path, including:

- direct writes that affect the derive conditions
- alias writes through local ref aliases rooted at that path
- passing the path into an unannotated mutating ref call

This conservative checker is enough to make `preserve` genuinely useful without requiring symbolic state transformers.

---

## 12. What exact poststate should mean for user-defined functions

For user-defined functions, exact summaries should also be validated rather than trusted.

### Recommended phase-1 validation rule

At each normal return, the analyzer must be able to prove that the target path is assignable to the annotated state set.

So for:

```context
def finish_ok(job: any ParseJob[Pending]&) -> void ensures job => Ready:
    job.stage <- 1
```

the body is accepted only if every return path proves `job` is `Ready`.

If one return path yields `Ready` and another yields `Failed`, then the annotation must be:

```context
ensures job => Ready | Failed
```

or it must be rejected.

---

## 13. Phase-1 scope recommendation

To keep the first implementation tight and high-confidence, I would intentionally restrict it to:

- function and extern-function `ensures` clauses only
- parameter-rooted struct field paths only
- named-state-bearing struct targets only
- normal-return semantics only
- validated summaries for user-defined functions
- conservative fallback whenever alias overlap is unclear

I would explicitly **not** do these yet:

- wildcard/indexed poststate paths
- per-error-path or per-`else` summaries
- generic transition tables like “Alive stays Alive, Dead stays Dead”
- automatic interprocedural inference
- trusting unchecked user annotations on ordinary functions

That is enough to solve the pain exposed by the motivating examples without making the feature sprawl.

---

## 14. Suggested implementation shape

### Parser / AST

This design does require a small new grammar hook after the return type / permission list and before the trailing `:`.

The parser should support something in this family:

```text
def ... -> Ret ensures path => effect, path => effect:
```

The path representation itself can still reuse the same structural idea as the existing annotation-path parser:

- `job`
- `team.player`
- later, possibly `items[*]`

So the syntax changes are source-level, but the internal path model can stay lightweight.

### Semantic signature model

Add a call-summary structure to `FuncType`, conceptually something like:

```text
FuncPoststateEffect {
    ParamIndex
    Path
    Mode        # preserve or exact
    StateCases  # when exact
}
```

The path representation can reuse the existing `borrowReturnAnnotationStep` shape.

### Function-body validation

For ordinary `def` functions:

- validate each `ensures` target and state family
- validate each normal return path against the summary

For `extern` functions:

- validate only the target-path and state-family surface
- trust the declared summary the same way other extern summaries are trusted

### Call-site flow application

The current widening hook already lives in the call-side flow logic.

The first implementation should extend that path so it:

- checks for a matching poststate effect on the callee
- applies `preserve` or exact state replacement when safe
- falls back to the current widening behavior otherwise

The natural files to touch are:

- `compiler/src/semantic/analyzer.go`
- `compiler/src/semantic/analyzer_flow.go`
- `compiler/src/parser/parser_typestate_test.go`
- `compiler/test/semantic/semantic_test.go`

---

## 15. Test matrix to add when implementing

At minimum, implementation should add coverage for:

1. **Parse / validation**
    - `ensures job => Ready` accepted
   - unknown parameter rejected
   - non-stateful target rejected
   - invalid state name rejected
   - `preserve` mixed with state cases rejected
    - multiple `ensures` targets accepted

2. **Caller precision recovery**
   - parser/job `Pending -> Ready`
   - socket `Open -> Closed`
   - nested `team.player -> Dead`
   - preserve across unrelated mutation helper

3. **Conservative fallback**
   - overlapping annotated + unannotated alias still widens
   - conflicting annotated alias cases union conservatively
   - unsupported path forms still widen

4. **Body validation**
   - wrong annotated poststate rejected
   - invalid `preserve` body rejected after relevant mutation

---

## 16. Why this is the right next step

Because it directly attacks the biggest current typestate precision cliff while staying aligned with the rest of the design:

- explicit
- local
- predictable
- sound
- incrementally implementable

It is also a good fit for the examples we already have:

- `ParseJob[Pending | Ready | Failed]`
- `Socket[Open | Closed]`
- `ScratchBuffer[Uninitialized | Initialized]`
- nested wrapper paths like `team.player`

In short:

> do not guess poststates interprocedurally yet; let APIs state them explicitly with `ensures`, validate them, and use them to avoid needless widening at ref-call boundaries.

That is a very good next trade.