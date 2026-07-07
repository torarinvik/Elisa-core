# docs/124 — Algebraic Effects: Capability Handlers with Conserved Rows

Status: DESIGN (nothing implemented)
Depends on: the landed permission/effect surface (`permission` families + `includes`
subsumption, `can[...]` rows, checked `can X as Y:` casts, `trusted` drops,
set-polymorphism via `[permission E]`, `alias` capability sets), errors-as-types
(`error[...]`), regions (docs/72–76), contracts (`requires`/`ensure`/`changes`,
docs/85/87), second-class borrow/escape machinery.

## 0. One-sentence pitch

Effects in Elisa are not a control-flow feature; they are a **checked
dependency-injection and allocation-placement feature** whose rows are **conserved
quantities**: `handle` chooses an implementation and *translates* a function's
`can` row, and only a **proof of containment** may ever shrink it.

## 1. Motivation

Elisa already has the *checking* half of an effect system: `can` rows say what a
function may do, and `main`'s row is the program's authority budget. What is
missing is the *interpretation* half — a way to give a row entry a local,
user-defined implementation without threading a parameter through twenty
signatures by hand.

Classic algebraic-effect languages (Koka, Eff, Effekt, OCaml 5) provide that half
but bundle two things we must not bundle:

1. **Continuation capture.** Handlers that suspend/resume require CPS or stack
   segments. Multi-shot resume duplicates affine values (`lmut`, must-consume
   handles, drained containers) and lets captured frames outlive popped region
   stacks. One-shot dynamic handlers are milder but still non-lexical and still
   region-hostile. Both fail principles 1 and 2 outright.
2. **Discharge-on-handle.** In every mainstream design, handling an effect erases
   it from all outer signatures. `Missile.Launch` handled anywhere in the stack
   becomes invisible above that point — the type system actively hides the most
   important fact about the program. For a permissions-flavored system this is
   disqualifying, not cosmetic.

This document designs the subset that survives: **lexically scoped, second-class
capability passing with tail-resumptive handlers and containment-gated
discharge.** It compiles to plain (usually direct, usually inlined) calls, never
captures a continuation, and never lets a row lie.

## 2. Core model

### 2.1 Permissions gain operations

A `permission` family may declare *operations* — function signatures its members
stand for:

```
permission Log:
    write(line: sview) -> void

permission Clock:
    now() -> u64
```

A permission with no operations is what every permission is today (a pure
marker); everything existing keeps working unchanged.

### 2.2 Using an operation requires the permission

```
def compute(xs: darray[i64]) -> i64 can[Log]:
    Log.write(f"n={xs.count}")
    ...
```

`Log.write(...)` is an *effect operation call*. It is legal only under
`can[Log]` (or a family that `includes` it). Nothing else changes about the
call site — it reads like a module-qualified call.

### 2.3 `handle` binds an implementation

```
handler StderrLog for Log:
    def write(line: sview) -> void can[Term.Write]:
        eprint(line)

def main() can[Term.Write]:
    handle Log with StderrLog{}:
        result = compute(data)
```

A `handler ... for P:` declaration is an ordinary struct plus `impl`-style
conformance to the permission's operation signatures (exact shape TBD in §9; it
may literally reuse `impl`). `handle P with value:` makes that value the
implementation of `P` for the block's dynamic extent.

**There is no `resume` keyword and no reified continuation.** The handler method
body *is* the operation body; control returns to the call site by ordinary
`return`. Tail-resumptive by construction — the entire one-shot-checking problem
of other designs is sidestepped syntactically.

### 2.4 Lowering: hidden capability parameter

`handle` introduces a hidden parameter (a pointer/value of the handler type)
threaded to every `can[P]` callee in the extent; `P.op(...)` is a call through
it. This is exactly the parameter-threading the programmer would write by hand
— done by the compiler, checked by the row system. Zero new runtime machinery.

### 2.5 Static handler identity (a language rule, not an optimization)

Every effect-operation call resolves to a **statically known** handler: the
innermost lexical `handle` for that permission, monomorphized through call
chains (same machinery as generic specialization). Consequences:

- Every `P.op(...)` compiles to a **direct call**, eligible for inlining. This
  is checkable, so `-Wperf` can enforce it the way scalar-permission enforces
  vectorization.
- Handler-directed specialization is free: `compute` monomorphized under a
  `NullLog` handler (empty `write`) deletes the logging *and the f-string
  formatting feeding it*. Zero-cost instrumentation.
- Runtime handler selection is REJECTED (§7). The escape hatch is an ordinary
  function-value parameter, which already exists.

Where monomorphization would explode (deep generic chains under many distinct
handlers), the fallback is a devirtualizable indirect call through the hidden
parameter — still zero allocation, still no capture — but the *default and the
`-Wperf`-checked path* is direct.

### 2.6 Capabilities are second-class

A capability (the hidden parameter, or any explicit spelling of it we later
add) may be **passed down** the call graph only. It may not be:

- stored in a struct or container,
- returned,
- captured by an escaping closure or spawned task (except §6),
- cast to a raw pointer.

Enforcement reuses the storage-class/escape union walkers built for region refs
and borrows (docs/84 lineage). Same rationale as region refs: second-class-ness
is what makes the lexical scoping sound and the lowering trivial.

## 3. Conserved rows: translate, don't erase

This is the load-bearing departure from every shipped effects design.

### 3.1 The rule

> **`handle` replaces the handled row entry with the handler's own residual
> row. It never erases anything it cannot prove absent.**

A handler's operations have `can` rows like any function. Handling `P` with a
handler whose ops require `can[R1, R2]` rewrites the extent's requirement
`can[P] → can[R1, R2]`.

```
# Handler appends to a darray it owns: ops require can[] — nothing.
handle Log with buffer_log:
    compute(xs)          # can[Log] → can[]      : discharged, TRUTHFULLY

# Handler writes a file: ops require can[Fs.Write].
handle Log with file_log:
    compute(xs)          # can[Log] → can[Fs.Write] : translated, not hidden
```

### 3.2 Discharge is a containment proof, not a policy

A row entry disappears **iff the handler's residual row is empty** — i.e. the
handler's implementation touches nothing but state the handler itself owns
(a local buffer, a local arena, a counter). For the outside world the effect
truly did not happen; the signature saying so is *true*. This is the `runST`
insight applied to effects.

Corollary: an effect that bottoms out in the real world can **never** vanish.
`Missile.Launch`'s handler ultimately calls an extern carrying
`can[Missile.Launch]` (or `Sys.Ioctl`, or whatever the hardware capability is),
so its residual is never empty, so no composition of handlers hides it from any
signature between `main` and the launch site. Grep any frame on the stack and
it is there.

### 3.3 `sticky`: named persistence

Translation is truthful but can *launder the name*: `can[Missile]` handled by
an ioctl-backed handler becomes `can[Sys.Ioctl]` — honest about authority,
silent about meaning. For effects where the name itself is the audit artifact:

```
permission Missile: sticky
    launch(target: Coord) -> void
```

A `sticky` permission survives every `handle`: handlers may layer
interpretation (telemetry, rate limiting, simulation) but the row keeps saying
`can[Missile]` all the way to `main`, alongside any residuals. `Unsafe.*`
should likely be sticky. The single absolution escape remains the existing
`trusted P:` block — loud, greppable, "a human signs here." No new keyword.

### 3.4 Declared at boundaries, inferred within

Automatic translation would let a signature change because a handler three
hops away changed — non-local, violates the locally-explainable discipline. So:

- **Within a function body**, translation is computed by the compiler.
- **At function signatures**, the post-translation row must be *written* and
  the compiler *checks* it (same posture as everything else in Elisa:
  inference inside bodies, explicit contracts at boundaries).

A function whose body does `handle Log with file_log: ...` writes
`can[Fs.Write]` (not `can[Log]`) in its own signature; writing `can[Log]`
there would be an error ("handled here with residual Fs.Write — declare that").

### 3.5 Interaction with the existing lattice

- `includes` subsumption applies to residuals: a handler may declare its
  residual at a coarser family; the widening is the existing checked
  `can X as Y:` logic, never trusted.
- Set-polymorphism composes: a higher-order fn `def run[permission E](f: func() -> void can[E]) -> void can[E]`
  is unchanged; handling inside `run` translates `E`'s *known* entries only —
  a generic `E` cannot be handled (you don't know its ops), which falls out
  naturally.

## 4. Region-bearing capabilities: allocation as an effect

The genuinely novel piece — possible because no other effects language has
Elisa's region model.

A capability may **carry a region**; `@handler` is a **region parameter**,
declared in the operation's generic bracket like any region-polymorphic
function, and referenced in the types that live in it:

```
permission Alloc:
    buffer[@handler](n: usize) -> darray[u8] @handler
```

The bracket declares the region parameter; `@handler` in the return type is an
ordinary use of it — "allocated in the region the handler was constructed
over." Same surface as region-poly fns (docs/74), so no new type-system notion:
the handler *binds* the region argument once at the handle site instead of the
caller passing it at every call. At the handle site:

```
handle Alloc with ArenaAlloc(frame_region):
    ast = parse(source)     # everything parse allocates via Alloc lives in frame_region
```

- **Safety:** results are `@r`-typed at the handler's region; the existing
  escape checker stops them from outliving it. "Callee allocated into my frame
  arena and returned it upward" is a compile error, not a UAF.
- **Performance:** this promotes the cross-fn lifetime-inference problem
  (region-return-inference, S0–S3 staging) from inference to a *declared,
  checked contract*: the callee says `can[Alloc]`, the caller picks the arena.
  Retarget an entire subsystem's allocation (scratch arena in the hot loop,
  perm region at startup) by swapping the handle site, touching nothing else.
- **Containment bonus:** an `Alloc` handler over a region local to the handle
  block has empty residual → the whole subtree is provably allocation-silent
  to the outside. Scratch-memory purity, checked.

This is Phase 3 and the riskiest part; it lands on the region-poly `Arena&`
threading that already exists (docs/74/75) — the hidden capability parameter
*is* an `Arena&` plus a vtable-free op set.

## 5. Operations carry contracts

Operation signatures may carry `requires` / `ensure` / `changes`. Every handler
must **satisfy** them (behavioral subtyping: handler may weaken requires,
strengthen ensures), discharged by the existing WP/SMT tiers:

```
permission Clock:
    now() -> u64
        ensure result >= old(result)      # monotonic; every handler proves or tests it
```

- Callers verify against the *operation's* contract once; swapping handlers
  cannot weaken what was proven.
- `changes nothing` on an operation gives **checked purity through an effect**:
  a `can[Log]` function still participates in PURE-region reasoning if
  `Log.write` is frame-pure. Frame conditions (docs/87) compose directly.
- `@property` fuzz can synthesize adversarial handlers (a jumping Clock, a
  failing Alloc) against these contracts — mocking and fault injection with no
  framework.

## 6. Concurrency

A capability crossing into `spawn` / nursery bodies is the one channel effects
have into fearless concurrency. Rule:

- By default a capability may **not** enter a spawn body (second-class capture
  ban, §2.6). Each task writes its own `handle`.
- A handler type may be declared `share`; it must then prove its state
  race-free under the existing shared-data machinery. Only `share` handlers'
  capabilities may cross.

A `handle` block is thereby also a **local grant with an implementation
attached** — which addresses the "cross-family member-combo grants need local
member-set grant aliases" gap from the emulator work: the local set is the
handler's op set.

## 7. Principled declines (record in this doc, cite forever)

1. **`resume`, first-class continuations, multi-shot handlers.** Break regions
   (captured frames outlive popped stacks) and affinity (resuming twice
   duplicates linear values). Escape hatch: `machine over` (docs/123) — the
   defunctionalized form of the same computations, with the state the checker
   must reason about syntactically visible.
2. **Dynamic handler search / runtime handler swap.** Destroys the direct-call
   guarantee (§2.5) and makes rows non-local. Escape hatch: an ordinary
   function-value or struct parameter.
3. **First-class capabilities** (stored, returned, in containers). Breaks
   lexical scoping and the trivial lowering. Same posture as region refs.
4. **Discharge-on-handle** (the mainstream semantics). Rows must not lie;
   discharge only by containment proof (§3.2) or explicit `trusted`.
5. **Effect row inference across signatures.** Rows stay written at boundaries
   (they already are); inference is intra-body only (§3.4).
6. **Exception-flavored abortive handlers.** Already exist as errors-as-types
   (`error[...]`, try/raise/catch). Do not rebuild.
7. **Generator/nondeterminism effects.** Covered by `machine` and
   comprehensions.

## 8. Phasing

- **P1 — operations + handle + lowering.** `permission` ops, `handler ... for P`,
  `handle P with v:`, hidden-param threading, monomorphized direct calls.
  Row semantics: translation per §3.1 with residuals, `sticky`, boundary
  declaration rule §3.4. No regions, no contracts. Pilot: a `Log` capability
  through one stage1 subsystem, old-vs-new binary diff for zero-cost proof.
- **P2 — second-class enforcement + perf gate.** Escape walkers extended to
  capabilities; `-Wperf` direct-call check; NullLog deletion benchmark.
- **P3 — region-bearing capabilities.** `@handler` results, ArenaAlloc,
  escape integration. Riskiest; gate on the region-return-inference groundwork.
- **P4 — operation contracts.** Handler conformance via WP/SMT; `changes` on
  ops; `@property` adversarial handlers.
- **P5 — `share` handlers** for spawn bodies; retire the emulator grant-alias
  workaround.

Each phase lands with its ergonomics or not at all (a restriction that ships
before its ergonomics is just friction).

## 9. Open questions (settle before P1 parsing)

1. **Handler declaration shape.** New `handler T for P:` keyword vs reusing
   `impl P for T:`-style conformance vs plain struct + structural check at the
   `handle` site. Leaning: reuse whatever `impl`/interface machinery is
   cheapest; the `handle` site is the only new syntax that must exist.
2. **Spelling of the op call.** `Log.write(x)` reads as module-qualified call —
   good — but must not collide with an actual module named `Log`. Options:
   permissions and modules share a namespace with a collision error (simplest),
   or a distinguishing sigil (worse ergonomics, avoid if possible).
3. **Residual display.** Diagnostics should show the translation chain
   (`can[Log] handled by file_log → can[Fs.Write]`) or debugging boundary-row
   errors will be miserable.
4. **RESOLVED:** `@handler` is a region *parameter* declared in the operation's
   generic bracket (`buffer[@handler](n: usize) -> darray[u8] @handler`), not a
   bare result annotation — reuses the region-poly fn surface; the handle site
   binds the region argument. Remaining sub-question for P3: whether the
   permission itself may also be region-parametric (`permission Alloc[@r]:`)
   when several ops share one region, or per-op brackets suffice.
5. **Multiple simultaneous handles of the same permission** (shadowing): allow
   with innermost-wins (lexical, predictable) — but confirm no interaction
   surprise with monomorphization caching.
6. **Interaction with `alias` capability sets**: does `alias Frontend = Log,
   Clock` admit `handle Frontend with h:` (h implements all ops)? Probably yes
   later, not P1.

## 10. What this buys, in one table

| Property | Mainstream effects | This design |
|---|---|---|
| Runtime cost | evidence lookup / stack capture | hidden param, direct call, often inlined |
| `Missile.Launch` visible at `main` | no (erased on handle) | always (residuals + sticky) |
| Regions/affine safe | no (continuations) | yes (no capture exists) |
| Allocation placement | n/a | handler-carried regions, checked escapes |
| Verified handler behavior | n/a | op contracts, SMT-discharged |
| Mocking/replay | via handlers | same, plus contract-driven fuzz + deterministic-by-construction main |
