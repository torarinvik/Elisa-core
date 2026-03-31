# Ref-Parameter Poststate `ensures` Clauses

This document proposes the **next precision feature** for Contextlang typestate:

> explicit `ensures` summaries for how a call changes the typestate of ref arguments.

The goal is narrow and deliberate:

- keep the current soundness model
- recover precision at the place users will feel it most
- avoid full interprocedural typestate inference for now
- keep this as **effect typing**, not runtime design-by-contract

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

This is the core design constraint.

---

## 3. Design goals

### Must do

- preserve soundness at call boundaries
- let APIs state exact ref-argument poststates explicitly
- support the practical cases users actually write
- compose with named derived states, nested paths, and existing flow refinement
- force callers to handle uncertainty when the poststate is genuinely not exact

### Must not do in the first cut

- require whole-program inference
- infer arbitrary mutation summaries automatically
- add a theorem-prover-like transition language
- make the typestate model depend on hidden callee body magic
- turn poststates into runtime-only design-by-contract assertions

The whole point is to stay **sound-first, explicit-second, inference-later**.

---

## 4. Preferred source surface

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

That gives it three big advantages:

- it scales naturally to **nested paths** like `team.player`
- it scales naturally to **multiple summaries** on one function
- it avoids overloading `Type[...]` syntax with transition semantics

Internally, the compiler can still lower this into a call-summary structure very much like the earlier `@poststate(...)` idea. But the **source syntax** should be `ensures`.

---

## 5. Proposed syntax

### Exact poststate

```context
def finish_ok(job: any ParseJob[Pending]&) -> void ensures job => Ready:
    ...
```

### Exact union poststate

```context
def finish(job: any ParseJob[Pending]&) -> void ensures job => Ready | Failed:
    ...
```

This means the caller should see exactly the union `Ready | Failed` after a normal return.

### Preserve current caller-visible state

```context
def bump_score(player: any Player[Alive]&) -> void ensures player => preserve:
    ...
```

This means the call does not invalidate the caller-visible typestate for that path.

### Nested path

```context
def kill_team(team: any Team&) -> void ensures team.player => Dead:
    ...
```

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
- `team.player`

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

The simplest first implementation is the first option.

### 7.2 Exact state summary

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

### 7.3 Preserve summary

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

### 7.4 Why this still forces handling when needed

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

## 8. Branch-sensitive poststates

For APIs where the caller **must inspect an outcome**, the right extension is a branch-sensitive form tied to the return value.

For example:

```context
def try_finish(job: any ParseJob[Pending]&) -> bool
    ensures return true  => job => Ready,
            return false => job => Failed:
    ...
```

Then:

- if the caller ignores the boolean result, they only know `job` is `Ready | Failed`
- if they branch on the result, each branch gets the appropriate exact poststate

This is the mechanism that turns “must handle state” into a static property.

### Recommended rollout

This is probably best treated as a **phase-2 extension**, after unconditional `ensures path => effect` lands.

That keeps the first implementation small while still giving the design a clear path toward enforced handling.

---

## 9. Examples

### 9.1 Parser/job state

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

Now the caller can keep precision after the call without re-proving with `is`.

### 9.2 Resource close

```context
struct Socket[state Open | Closed]:
    fd: mutable int

    derive state:
        Open when self.fd >= 0
        Closed when self.fd < 0

def close_socket(sock: any Socket[Open]&) -> void ensures sock => Closed:
    sock.fd <- -1
```

### 9.3 Preserve state across irrelevant mutation

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

### 9.4 Nested path

```context
repr(c) struct Team:
    player: mutable Player[Alive]
    armor: mutable Armor[Intact]

def kill_team(team: any Team&) -> void ensures team.player => Dead, team.armor => Destroyed:
    team.player.health <- 0
    team.armor.hp <- 0
```

---

## 10. Validation rules

For an `ensures` entry:

```context
ensures target => effect
```

the compiler should enforce all of the following.

### 10.1 Target path must be parameter-rooted

`target` must start at a function parameter name.

Reject:

- locals
- globals
- arbitrary expressions

### 10.2 Target path must resolve to a named-state-bearing type

The projected target type must be a struct instance with a named state family.

Reject:

- plain structs with no named states
- aggregate-state-only carriers
- refs whose pointee path does not end in a named-state-bearing value

### 10.3 `preserve` is exclusive

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

### 10.4 State names must belong to the target’s family

If `job` is a `ParseJob[...]`, then:

```context
ensures job => Ready
```

is fine, but:

```context
ensures job => Closed
```

must be rejected.

### 10.5 The body must prove the summary

For ordinary user-defined functions, the compiler must prove the summary.

That means:

- exact `ensures` must be proven on every normal return path
- `preserve` must be justified by the absence of relevant mutation hazards for that path
- if the body cannot justify the clause, reject the function

This is the rule that keeps `ensures` in the effect-typing lane instead of the design-by-contract lane.

---

## 11. How caller-side application should work

Today the call-site rule is effectively:

> if a ref argument may let the callee mutate named-state-bearing data, widen that caller-visible path conservatively.

The new rule should become:

1. Start from the current conservative widening behavior.
2. For each `ensures` target reached by the call:
   - `preserve` means **do not widen that path**.
   - explicit state cases mean **replace that path with the declared state set**.
3. If overlapping or aliasing summaries disagree, merge conservatively.

That last part matters.

---

## 12. Aliasing and overlap rules

This is where soundness earns its lunch.

### 12.1 Ensured + unensured overlapping refs

If the same caller-visible path may be reachable through:

- one ensured ref argument, and
- another unannotated/unensured ref argument,

then the unensured path still forces conservative widening.

In other words:

> `ensures` should only recover precision when it dominates all relevant mutation summaries for that path.

### 12.2 Two ensured overlapping refs with different exact outcomes

If both may alias and they disagree:

```text
ensures a => Ready
ensures b => Failed
```

then the caller-visible result for an overlapping root/path must conservatively join to:

```text
Ready | Failed
```

### 12.3 `preserve` mixed with exact poststate

If one aliasing path says `preserve` and another says `Dead`, then `Dead` only wins if the overlap analysis proves the `Dead` summary is the one that actually governs that path.

Otherwise, fall back to union or ordinary widening.

The core rule is:

> `ensures` can sharpen the existing rule, but must never beat a stronger aliasing hazard than it actually summarizes.

---

## 13. Recommended phase-1 scope

To keep the first implementation tight and high-confidence, I would intentionally restrict it to:

- function and extern-function `ensures` clauses only
- parameter-rooted struct field paths only
- named-state-bearing struct targets only
- normal-return semantics only
- statically validated summaries for user-defined functions
- conservative fallback whenever alias overlap is unclear

I would explicitly **not** do these yet:

- wildcard/indexed poststate paths
- per-error-path summaries
- branch-sensitive `return true/false => ...` clauses in phase 1
- generic transition tables like “Alive stays Alive, Dead stays Dead”
- automatic interprocedural inference

That is enough to solve the pain exposed by the motivating examples without making the feature sprawl.

---

## 14. Suggested implementation shape

### Parser / AST

This design requires a small new grammar hook after the return type / permission list and before the trailing `:`.

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
- reject the function if the summary is not statically provable

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

- `compiler/src/parser/parser.go`
- `compiler/src/parser/parser_typestate_test.go`
- `compiler/src/ast/ast.go`
- `compiler/src/semantic/analyzer.go`
- `compiler/src/semantic/analyzer_flow.go`
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
   - overlapping ensured + unensured alias still widens
   - conflicting ensured alias cases union conservatively
   - unsupported path forms still widen

4. **Body validation**
   - wrong ensured poststate rejected
   - invalid `preserve` body rejected after relevant mutation
   - non-provable exact poststate rejected

5. **Phase-2 branch-sensitive follow-up**
   - `return true => job => Ready`
   - `return false => job => Failed`
   - ignored result yields the joined state
   - checked result narrows each branch appropriately

---

## 16. Why this is the right next step

Because it directly attacks the biggest current typestate precision cliff while staying aligned with the rest of the design:

- explicit
- local
- predictable
- sound
- statically checked
- incrementally implementable

It is also a good fit for the examples we already have:

- `ParseJob[Pending | Ready | Failed]`
- `Socket[Open | Closed]`
- `ScratchBuffer[Uninitialized | Initialized]`
- nested wrapper paths like `team.player`

In short:

> do not guess poststates interprocedurally yet; let APIs state them explicitly with `ensures`, prove those summaries statically, and use them to avoid needless widening at ref-call boundaries.

That is a very good next trade.# Ref-Parameter Poststate `ensures` Clauses

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
repr(c) struct Team:
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