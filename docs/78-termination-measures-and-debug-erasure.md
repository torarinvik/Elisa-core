# Termination Measures, the Partiality Effect, and Debug Erasure

This note specifies two related features that share one underlying idea —
*code and obligations that exist for reasoning but not for the shipped machine*:

1. **Well-founded termination measures** (`decreases`) that let the compiler
   *prove* a loop or recursion terminates, replacing the fuel-style
   `ProgressBudget` tick from [Progress Safety](25-progress-safety.md) with a
   real ranking function — and a first-class **partiality effect** (`Partial`)
   for the code that genuinely cannot be proven total.
2. **Erasure tiers** (`debug` blocks and `debug def`, with `ghost` reserved for
   later) that carry checks, contracts, and assertions in development builds and
   vanish under release — giving Design by Contract for free.

> A `decreases` measure is the strongest form of progress evidence: where today
> the loop must *tick a budget*, tomorrow it may *exhibit a measure that cannot
> decrease forever*. The static proof costs nothing at runtime and ships in
> every build mode; only the optional runtime *monitor* is debug-erased.

This document is a design proposal. It extends, and is meant to be read against,
[25 Progress Safety](25-progress-safety.md), [22 Value-Fact Core](22-value-fact-core.md),
[11 Proof-Carrying Views](11-proof-carrying-views-and-optimization-legality.md),
[16 Ref-Parameter Poststate `ensures`](16-ref-parameter-poststate-ensures.md),
and [20 Annotations and Compile-Time Hints](20-annotations-and-compile-time-hints.md).

---

## 0. The distinction that organizes everything

Two mechanisms wear similar syntax and must not be conflated:

| | **Static proof (A)** | **Dynamic monitor (B)** |
|---|---|---|
| What | compiler verifies the measure is well-founded and strictly decreasing | runtime re-checks the measure decreased each step, aborts if not |
| Cost | zero at runtime | one measure re-evaluation per back-edge |
| Build mode | **always on, every build** (a proof is not emitted code) | **debug only, erased in release** |
| Guarantees | the function *cannot* diverge | catches divergence *when it happens*, smarter than fuel |
| Needs | discharge engine (syntactic → fact → solver) | nothing but the measure expression |

A is the silver bullet and answers "can we do *guaranteed-to-terminate*
statically?" — **yes.** B is the pragmatic safety net for the code A cannot
reach. They share the `decreases` surface and compose: a function may be proven
by A (no monitor needed), monitored by B (proof deferred), or declared `Partial`
(neither). Coupling A to debug mode would be a category error — it would let a
release build skip a proof the debug build passed, shipping the *weaker*
guarantee. So: **A is mode-independent; only B is debug-erased.**

---

## Part I — Erasure tiers: `debug`, contracts, and `ghost`

### 1.1 Tiers

| Tier | Present in debug | Present in release | Purity constraint |
|---|---|---|---|
| `normal` | yes | yes | none |
| `debug` | yes | **erased** | observationally pure modulo abort |
| `ghost` *(reserved)* | **never emitted** (compile-time only) | never | fully pure, spec-only |

`debug` is the runtime-checked tier this proposal builds; `ghost` is noted so the
lattice is visible and the keywords are reserved, but is out of scope for v1.

### 1.2 `debug:` blocks and `debug def`

```elisa
def transfer(acct: mutable Account&, amount: i64):
    debug:
        assert amount > 0
        assert acct.balance >= amount
    acct.balance <- acct.balance - amount
```

A `debug:` block is **sugar over `static if debug:`** (the compile-time
conditional machinery already exists as `StaticIfStmt`/`StaticIfDecl`) plus two
extra rules that a bare `static if` does not impose:

1. **Erasure is guaranteed, not incidental.** In a release profile `debug` is the
   compile-time constant `false`; the block is dead-code-eliminated before
   lowering. (`debug` is a builtin `static` predicate, like a target flag.)
2. **Observational purity is enforced by the effect system.** A `debug` block may
   hold only the `Diagnostic.*` and `Abort.Panic` permissions and may *read*
   visible state; it may **not** hold `Memory.*`, `Sync.*`, `Global.Write`,
   `Atomics.*`, mutation of any binding observed outside the block, or any other
   state-changing capability. This is the one rule that makes erasure sound:
   removing a `debug` block cannot change release semantics, because the block
   could not have affected them.

A `debug def` is a function whose whole body is `debug`-tier:

```elisa
debug def check_well_formed(t: Tree&) -> bool:
    ...                      # may read, assert; no observable effects
```

Because the call must vanish in release, a `debug def` is **statement/assertion
shaped**: it is callable only in void/assertion context, never as a value the
non-debug computation consumes. (A value-returning debug helper would need a
release fallback; we do not allow that in v1 — keep it assertion-shaped.)

### 1.3 Design by Contract, for free

Contracts desugar to `debug` assertions at the obvious program points:

```elisa
def pop(s: mutable Stack&) -> i64
    requires s.len > 0
    ensures  s.len == old(s.len) - 1:
    ...
```

- `requires P` → `debug: assert P` at entry.
- `ensures Q` → `debug: assert Q` at each return; `old(e)` snapshots `e` at entry
  (a `debug`-tier local); `result` binds the return value. Ref post-state in
  `ensures` follows [doc 16](16-ref-parameter-poststate-ensures.md).
- `invariant I` on a loop → `debug: assert I` at the loop head each iteration.

Contracts inherit the `debug` purity rule, so a contract can never itself mutate
program state — a classic DbC footgun closed by construction.

### 1.4 The safety firewall (non-negotiable)

**Debug-erased contracts are a testing aid, not a safety boundary.** Elisa's
identity is *always-on* memory/effect/region safety. Therefore:

- Any property whose violation is undefined behavior, or which guards a
  memory/effect/region invariant, **stays in the always-on type/effect/region
  system** — never in a `debug` contract.
- A lint (`-Wdebug-guards-unsafe`) flags a `debug`/contract assertion that is the
  *only* thing standing between the program and an `Unsafe.*` operation or an
  out-of-bounds/aliasing hazard. If erasing your check can cause UB, it was never
  a `debug` check.

`debug` contracts are for the *additional* logical layer — ordering, numeric
ranges, algebraic laws, structural well-formedness — that is too expensive or
undecidable to verify on every build.

---

## Part II — Termination via well-founded measures

### 2.1 Surface

```elisa
def factorial(n: usize) -> usize decreases n:
    if n == 0:
        return 1
    return n * factorial(n - 1)

def gcd(a: usize, b: usize) -> usize decreases b:
    if b == 0:
        return a
    return gcd(b, a % b)

def size(t: Tree&) -> usize decreases t:          # structural
    match t:
        case Leaf:        return 1
        case Node(l, r):  return 1 + size(l) + size(r)

def ackermann(m: usize, n: usize) -> usize decreases (m, n):   # lexicographic
    if m == 0:        return n + 1
    if n == 0:        return ackermann(m - 1, 1)
    return ackermann(m - 1, ackermann(m, n - 1))
```

Loops take the same clause:

```elisa
def sum(items: Slice[i64]) -> i64:
    total: mutable i64 = 0
    i: mutable usize = 0
    while i < items.len decreases items.len - i:
        total <- total + items.get_unchecked(i)
        i <- i + 1
    return total
```

### 2.2 Well-founded orders

A measure's type must carry a well-founded `≺` (no infinite descending chains).
v1 supports exactly three, and lexicographic products of them:

1. **Bounded numerics** — `usize` (floor `0`), or any integer measure the
   fact core ([22](22-value-fact-core.md)) can prove `≥ 0` at the recursion
   floor. `≺` is `<`. *Signed measures with no provable floor are rejected* —
   they are not well-founded.
2. **Structural (subterm) order** — for a recursively-built value (a region/
   packed tree, [74](74-region-backed-packed-enums.md)), `v ≺ w` iff `v` is a
   strict sub-component of `w`. The canonical source of "smaller" is a `match`
   `case` binding: in `case Node(l, r)`, both `l` and `r` are `≺` the scrutinee.
3. **Lexicographic tuples** — `(x₀, …, xₖ)` ordered left-to-right: strictly
   smaller iff some `xⱼ` strictly decreases and all `xᵢ (i < j)` are equal. Each
   component must itself be well-founded.

### 2.3 The static obligation (A)

For a function `f(p…) decreases m`, for **every** recursive call `f(q…)` reachable
in the body, under the path condition `φ` guarding that call, the checker must
discharge:

```text
φ  ⟹  m[q…] ≺ m[p…]
```

For a loop `while c decreases m`, with `m₀` the measure at the head and `m₁` its
value at the next head:

```text
(c ∧ body)  ⟹  m₁ ≺ m₀        and   m is bounded below
```

A discharged loop **satisfies the Progress-Safety obligation from
[doc 25](25-progress-safety.md) directly** — the measure *is* the local progress
evidence, replacing `progress_tick`. No budget, no fuel.

**Mutual recursion** (a recursion SCC) shares one measure across the cycle —
typically a lexicographic or tagged value — and every cross-edge must strictly
decrease it. The components may differ per function as long as they project into
the common order.

### 2.4 Discharge engine — cheap first, solver last

The obligation `φ ⟹ m₁ ≺ m₀` is attempted in ascending cost; the first that
succeeds wins. This staging is what keeps the common case free.

| Stage | Handles | Mechanism | Cost |
|---|---|---|---|
| **S0 syntactic** | structural `case` subterms; `for x in finite`; `bound − i` with `i += k>0`, `bound` loop-invariant | AST/typestate pattern match | ~0, always on |
| **S1 fact** | linear integer measures: `i' = i+1 ∧ i<n ⟹ n−i' < n−i` | reuse [value-fact core](22-value-fact-core.md) range/monotonicity facts | cheap |
| **S2 solver** | nonlinear, lexicographic, data-dependent measures | arithmetic decision procedure, behind a flag | heavy, opt-in |
| **escape** | genuinely partial / unprovable | `can[Partial]` or `trusted Unsafe.AssumeProgress` | n/a |

S0+S1 are expected to cover the large majority of real code with **zero
annotation and zero runtime cost**. S2 exists for the hard 20% and need not be in
v1.

### 2.5 Inference — the easy cases need no `decreases`

When the measure is forced, the programmer writes nothing; the checker infers and
discharges at S0:

- `for x in xs:` over a finite collection (`darray`, `Slice`, range, iterator
  with a known length) → measure = remaining elements. **Auto.**
- `while i < n:` with `i` strictly increasing by a positive constant and `n`
  loop-invariant → measure = `n − i`. **Auto.**
- **Structural recursion**: every recursive call passes a `case`-bound strict
  subterm of a parameter → measure = that parameter under subterm order. **Auto.**

Anything outside these surfaces a single, specific diagnostic asking for a
`decreases` clause or a partiality acknowledgment — never a wall of obligations.

### 2.6 `Partial` — divergence as a first-class effect

Some computations are *inherently* non-total: servers, REPLs, `while not eof()`,
fixpoint iterations with no a-priori bound, the persistent pool worker loop that
runs until shutdown. No measure exists. The honest answer is a **permission**,
catalogued as `Progress.Partial` (usable as the alias `Partial`):

```elisa
def serve(sock: Socket&) -> void can[Partial]:
    while true:
        handle(accept(sock))
```

Semantics — divergence is contagious, exactly as in an effect lattice (cf. F★'s
`Tot` ⊑ `Div`):

- A function may carry `Partial` iff it may fail to terminate. A function that is
  inferred-total or `decreases`-proven does **not** carry it.
- `Partial` **propagates to callers** through the normal permission machinery: if
  you call a `Partial` function and cannot prove your own progress independently,
  you are `Partial` too. This is the point — "might not terminate" is visible up
  the call graph, like any other capability in `can[...]`.
- **Ordering:** total ⊑ partial. A total function is usable wherever `Partial` is
  permitted; not conversely.
- **Contexts may forbid it:** `@main_thread`, real-time, or a strict profile can
  reject `Partial` in their reachable set, just as they reject `Blocking.*`
  (doc 25).

#### Relationship to the existing escape hatches

[Doc 25](25-progress-safety.md) already ships two `trusted` escapes. They remain,
and now sit in a clear hierarchy:

| Form | Meaning | Caller-visible? |
|---|---|---|
| `decreases m` (proven) | total, *proven* | no — total |
| inferred total | total, *proven* | no — total |
| `trusted Unsafe.AssumeProgress:` | "I privately vouch it progresses" | **no** — discharged locally |
| `can[Partial]` | "this is partial — callers should know" | **yes** — propagates |
| `trusted Unsafe.NonProgress:` | "intentionally spins forever" | no — discharged locally |

`AssumeProgress` is the *unchecked, non-propagating* vouch; `Partial` is the
*honest, propagating* confession. Many current `AssumeProgress` sites are really
one or the other — e.g. the pool worker loop is genuinely `Partial`; a
`bound − i` loop the checker couldn't yet see is a candidate for `decreases`.

---

## Part III — The debug monitor (B) and how A/B compose

In a **debug** profile, a `decreases m` clause *additionally* lowers to a runtime
guard (this is the part that uses Part I's erasure):

```text
# conceptual lowering of `while c decreases m:`  (debug only)
__m_prev <- m
while c:
    <body>
    __m_now <- m
    debug: assert well_founded_lt(__m_now, __m_prev)   # else abort with diagnostic
    __m_prev <- __m_now
```

Recursive functions snapshot the measure at entry and assert `m[args'] ≺ m_entry`
before each recursive call. The abort diagnostic is structured and specific:

```text
termination monitor: measure `items.len - i` did not decrease
  at sum (file.elisa:42), iteration 813
  previous = 7   current = 7   order = usize(<)
  (i unchanged this iteration?)
```

This is the **smarter-than-fuel watchdog**: it fires only on *actual* lack of
progress, never on an arbitrary step budget, so it has no false "ran out at step
10,001" failures.

**Composition rule:**

- Proven by A → no monitor needed (but B may still be emitted in debug as
  defense-in-depth; controlled by a flag).
- Has a `decreases` clause but proof deferred (S2 off, or annotated
  `decreases … unchecked`) → **B in debug, nothing in release.** Honest: release
  may hang; debug catches it.
- `can[Partial]` → no A, no B by default; the programmer may still opt into a
  `decreases … monitor` purely for debug observation.

The gradient — *prove what you can (A), monitor what you can't (B), confess the
rest (`Partial`)* — is gradual totality: like gradual typing, but for
termination. A program can live anywhere on it and tighten over time.

---

## Part IV — Implementation plan (phased)

Each phase is independently shippable and leaves the tree green.

**Phase 0 — erasure tier.** `debug:` blocks + `debug def`. Lean on existing
`StaticIf` lowering with `debug` as a builtin `static` predicate (release ⇒
`false` ⇒ DCE). Add the purity check (effect-system restriction to
`Diagnostic.*`/`Abort.Panic` + reads). Add the `Diagnostic` permission family
(or, minimally, reuse `Abort.Panic` + `Console.*` until a dedicated family
lands). *No termination yet.*

**Phase 1 — contracts + the monitor (B).** `requires`/`ensures`/`invariant` and
`old(...)`/`result` desugaring to Phase-0 asserts. Parse `decreases` on
functions and loops; lower to the debug monitor. Add the `Partial`
(`Progress.Partial`) permission and make it propagate. This alone is a strict
upgrade over the `ProgressBudget` tick and ships fast — no prover required.

**Phase 2 — static discharge S0.** Syntactic/structural acceptance: `for`-in
over finite collections, structural `case` recursion, `bound − i` counting loops.
These become *provably total with zero annotation and zero runtime cost*, and the
Progress-Safety obligation (doc 25) is discharged by the measure. Inference per
§2.5.

**Phase 3 — fact-backed discharge S1.** Route linear-integer obligations through
the [value-fact core](22-value-fact-core.md). Most hand-written `decreases n` /
`decreases n − i` on integer measures now discharge statically.

**Phase 4 — solver discharge S2 (optional).** Arithmetic decision procedure for
nonlinear, lexicographic, mutual-recursion, and data-dependent measures, behind a
flag. Lexicographic tuples and SCC measures fully supported here.

**Migration.** Existing `trusted Unsafe.AssumeProgress` / `progress_tick` sites
keep working. Offer a codemod/lint that proposes `decreases` (when S0/S1 can
discharge) or `can[Partial]` (when not). The persistent pool worker loop is the
canonical `Partial` example; a `bound − i` runtime loop is the canonical
`decreases` example.

---

## Part V — Grammar additions

```text
FuncDecl   := 'debug'? 'static'? 'def' Name '(' Params ')' RetType? Measure? Contract* ':' Block
Measure    := 'decreases' MeasureExpr ('unchecked')?           # 'unchecked' = monitor-only, defer A
MeasureExpr:= Expr                                             # scalar, well-founded type
            | '(' Expr (',' Expr)+ ')'                         # lexicographic tuple
Contract   := 'requires' Expr
            | 'ensures'  Expr                                  # may use old(e), result
LoopStmt   := 'while' Expr Measure? ':' Block
            | 'for' Pattern 'in' Expr Measure? ':' Block       # measure usually inferred
            | 'while' Expr 'invariant' Expr Measure? ':' Block
DebugStmt  := 'debug' ':' Block
```

Reserved (not yet active): `ghost`.

## Part VI — Permission catalog additions

Extends [doc 30](30-builtin-permission-catalog.md):

```text
Progress
  + Progress.Partial          # may-not-terminate; propagates; alias `Partial`

Diagnostic                    # new family for debug-tier observation
  + Diagnostic.Assert
  + Diagnostic.Log
```

`Diagnostic.*` is the *only* state-touching family a `debug` block may hold
(besides `Abort.Panic`), and it is itself erased with the block in release.

---

## Part VII — Worked examples

**Auto-total, zero annotation (S0):**

```elisa
def contains(xs: Slice[i64], target: i64) -> bool:
    for x in xs:                      # finite ⇒ measure inferred, proven, no runtime cost
        if x == target: return true
    return false
```

**Annotated, statically proven (S1):**

```elisa
def binary_search(xs: Slice[i64], target: i64) -> bool:
    lo: mutable usize = 0
    hi: mutable usize = xs.len
    while lo < hi decreases hi - lo:  # fact core: hi-lo strictly shrinks each branch
        mid: usize = lo + (hi - lo) / 2
        if xs.get_unchecked(mid) == target: return true
        if xs.get_unchecked(mid) < target:  lo <- mid + 1
        else:                                hi <- mid
    return false
```

**Honestly partial:**

```elisa
def event_loop(q: Queue&) -> void can[Partial]:     # no measure exists; callers see Partial
    while true:
        dispatch(q.dequeue_blocking())
```

**Monitor-only, proof deferred:**

```elisa
def newton(x0: f64, f: Fn) -> f64 decreases residual(x0, f) unchecked:
    # nonlinear measure; S2 off. Debug build asserts residual shrinks each step;
    # release runs unguarded. Honest: this is monitored, not proven.
    ...
```

**Contract + measure together:**

```elisa
def drain(s: mutable Stack&) -> void
    ensures s.len == 0
    decreases s.len:                 # proven total (S1); ensures checked in debug
    while s.len > 0 decreases s.len:
        _ = pop(s)
```

---

## Part VIII — Open questions

1. **`Partial` vs `@main_thread`.** Should `@main_thread` forbid `Partial` by
   default (symmetry with `Blocking.*`), or only under a strict profile?
2. **Defense-in-depth default.** When A proves a measure, should B still emit in
   debug? Proposed: off by default, `-fmonitor-proven` to force.
3. **`old(...)` capture cost.** `ensures` snapshots may be non-trivial (deep
   copies). Restrict `old` to copyable/scalar expressions in v1?
4. **Structural order for non-region values.** §2.2(2) is crisp for region/packed
   trees. Do we extend subterm order to ordinary owned aggregates, or require a
   numeric/`size()` measure there?
5. **`ghost` activation.** When (if) `ghost` lands, does it subsume
   compile-time-only spec functions used inside `requires`/`ensures`?
6. **Effect-family interaction.** Should `Partial` join a `Total`-by-default
   grant alias so strict profiles can write `can[Realtime]` = "no `Partial`, no
   `Blocking`, no `Unsafe`"?
