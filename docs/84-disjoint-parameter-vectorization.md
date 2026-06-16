# 84 — Disjoint-Parameter Vectorization

Status: **Increments 1, 2, 3a, and 3b landed; Increment 4 in progress.**
The current implementation derives per-call buffer disjointness, aggregates it into
sound per-callee facts, and emits gated per-parameter LLVM `alias.scope`/`noalias`
metadata for proven-distinct numeric `darray&` element streams under
`ELISACORE_NOALIAS_MUTABLE_REFS=1`. The remaining work before default-on is the
full soundness/perf gate in §4.

This document is the agreed plan from a 4-perspective design debate (minimalist,
provenance-rigorist, LLVM-pragmatist, skeptic). It supersedes the earlier
"needs a whole new language feature" framing: **no new trusted annotation is
required.** Disjointness is *derived* from machinery Elisa already maintains.

## 1. Problem

Factored numeric kernels that take several reference-to-container parameters do
not vectorize at `-O3`:

```elisa
def axpy(y: mutable darray[f64]&, x: darray[f64]&, a: f64) -> void:
    for i in 0..<y.count: y[i] <- y[i] + a * x[i]
```

We want the loop to vectorize without LLVM's runtime alias-guard branches. Today
it cannot, for three compounding reasons:

1. **Header-copy buffer sharing.** A darray is a Go-slice-like 3-word header
   `{data, count, cap}`. `b = a`, by-value pass, and `return a` copy the *header*
   and **share the buffer** (deep copy only via explicit `clone`). So two distinct
   darray values — or two distinct `mutable darray&` parameters — can point at the
   same buffer. A naive param-level `noalias` would be **unsound**: it silently
   miscompiles ordinary safe code (`b = a; axpy(&a, b, k)`).

2. **SROA dissolves the header.** Even where the params *are* disjoint, a
   param-level `noalias` on the header pointer is **optimization-inert at O3**: SROA
   lowers the header to its inner data pointer (`.0.val`), which the header
   attribute says nothing about, so the vectorizer keeps its alias guards.
   (Verified: 20 vector ops + 4 guard branches, identical with/without the stamp,
   on an `@inline(never)` AXPY probe.)

3. **Shared generic accessors.** Element access goes through identity-agnostic
   `get_unchecked[T]`/`set_unchecked[T]` (`p: T& = s.base.cast[T&]; p[i]`), so
   `alias.scope` metadata cannot ride on the access without specialization or
   inlining.

A `noalias` miscompile is **silent** (wrong numbers, no crash). Soundness is
paramount; every decision below is biased toward fail-closed-to-correct.

## 2. Key correctness finding

**Surviving Elisa's mutable-alias checker does NOT prove buffer-disjointness.**

The checker enforces non-aliasing of *references / bindings*. It already kills the
reference-launder family (`f(get_ref(&x), &x)`, mutable-struct-local laundering —
the recent fix commits). But header-copy buffer sharing is **not in its threat
model**: `b = a; kernel(&a, &b)` produces two distinct mutable *values* with one
shared `data` pointer and passes clean. Slice/view-of-parent passed alongside the
parent likewise overlaps.

Therefore the derived disjointness predicate must be:

```
proven_distinct(ai, aj)  ≡  survivedAliasChecker ∧ bufRoot(ai) ⊥ bufRoot(aj)
```

The `bufRoot` buffer-identity analysis is the **missing half** that covers the
header-copy gap — it is *not* a parallel soundness surface competing with the
alias checker.

## 3. Design

### 3.1 One source of truth — a derived predicate, no trusted annotation

Per call site, per **ordered** parameter pair, the front-end computes
`proven_distinct`. The carrier is a buffer-identity union-find, `bufRoot`:

- **fresh-allocation tokens** — `new[r] darray`, `[...]` literal, `clone(e)`,
  comprehension result → a unique token; two distinct tokens never overlap.
- **named owner** — `a`, `&a` → `buf(a)`.
- **header-copy edges** — `b = a`, by-value pass, `return a` union `buf(b)=buf(a)`
  (follows header dataflow, *not* the name; reuse `aliasRootForExpr` +
  `callReturnAliasedRoots` return-isolation `AliasParamIndices` laundering).
- **slice/view → parent edges** — `slice_of(a)`, `a.view`, `split(a)[i]` inherit
  `buf(a)` (a derived view is never disjoint from its parent).
- **opaque param tokens** — a parameter's buffer is opaque; two distinct params are
  disjoint **only** when the current function's own contract already declares them
  so (contract propagation; otherwise un-provable).

`proven_distinct` holds iff the two `bufRoot` sets are provably non-overlapping.

**Polarity is proven-distinct, never absence-of-proof-of-equality.** Self-alias
`f(y, y)`, partial overlap at different offsets of the same root, and any
unanalyzable site → **not distinct** → fail closed.

### 3.2 Backend interface

The front-end hands `emitFunction`, per kernel:

- the **set of ordered param-pairs** that are `proven_distinct`, AND
- a **self-vs-rest noalias bit** per param (does this param's element stream alias
  any *other* reachable store / loaded pointer in the loop?).

Pairwise alone yields only ordering `alias.scope`s; the self bit is what actually
elides LLVM's loop-access-analysis memcheck guard branches. Provenance source
(derived / `Slice.split` / explicit assertion) is **erased** at this boundary — the
backend sees only booleans.

### 3.3 Backend mechanism — inline-then-tag (the only SROA-survivable carrier)

Rejected: header-`restrict` (SROA shreds it; `noalias` is not transitive into the
loaded inner pointer) and scope-token threading (`alias.scope` is *instruction
metadata*, not an SSA value — LLVM ignores a runtime "scope" argument).

Adopted:

1. **Force-inline** the shared `get_unchecked`/`set_unchecked` accessor at the
   element site so the inner-data-pointer load/store is exposed in the kernel body.
2. Stamp **paired `!alias.scope` + `!noalias`** on that load/store: **one domain per
   disjoint-group**, one scope per param, `!noalias` listing the sibling scopes.
3. Emit **`llvm.experimental.noalias.scope.decl`** in the loop preheader to anchor
   scope identity so the metadata survives later inlining of the kernel itself.
4. **Backend assertion:** every tagged access carries *both* metadata lists in the
   *same domain*; otherwise drop all tags for the group (fail-closed-to-correct).

This is the exact mechanism that was blocking "Increment 2," now unblocked, and it
**unifies** the `darray&` and `Slice.split`-band cases — both fail today for the
same shared-accessor reason. Bands get the self-noalias bit for free (`split`
proves disjointness by construction).

**LLVM-mechanics confidence (high, with caveats):** paired `!alias.scope`+`!noalias`
in one domain on inner-ptr loads/stores makes LoopAccessAnalysis mark the dependence
pairs NoAlias and elide the SCEV runtime memcheck — the 4 AXPY guard branches
collapse. **Failure modes the assertion must catch:** (a) emitting `alias.scope`
without the sibling `!noalias` list → all guards kept, *silently*; (b) scopes split
across *different* domains → LV's elision is domain-pairing-sensitive, so one domain
per group is mandatory.

### 3.4 Fail-closed-to-correct, fail-loud-on-perf

- The **derived path never hard-errors.** Ambiguous provenance (phi of two params,
  dynamic `m[i]`/`m[j]` index into `darray[darray]`) → emit no metadata, fall back
  to today's shared `elt` scope. Correct, just unoptimized.
- An **optional `disjoint` keyword** may be added later *only* as a checked
  assertion that hard-errors when `proven_distinct` cannot prove the claim. It is
  never a backend license — it lowers to the *same* derived predicate. This catches
  "I thought these were disjoint" bugs without introducing a trusted annotation.
- `@hot` kernels that fail to stamp emit a **`-Wperf`** diagnostic naming the
  unproven param-pair and pointing at `Slice.split` as the disjoint-by-construction
  remedy. (Reuse the existing auto-vectorization verifier.)

### 3.5 Scope of the win

Census of the project's named kernels — ~4 of 5 families are **whole-array**:

| kernel | shape | path |
|---|---|---|
| AXPY (`y += a*x`) | whole-array, 2 darray& | derived stamp |
| Jacobi / stencil sweep | read-grid → write-grid | derived stamp |
| Stable-Fluids (serial fields) | per-field whole-array | derived stamp |
| NES / Wolf3D framebuffer | scanline writes | derived stamp |
| Stable-Fluids (parallel) | red-black bands | `Slice.split` |

So the transparent `darray&` stamp is the **primary deliverable**; `Slice.split`
bands remain the path for the parallel / dynamic-index case (the `assert_disjoint`
trusted hatch, scoped to dynamic `darray[darray]` indexing only). Serial whole-array
kernels are **not** force-migrated to `Slice`.

## 4. Soundness gate (required before default-on)

1. **Differential bit-identical checksum harness, CI-gated.** Each kernel run at
   `-O0` (no noalias) and `-O3` (stamped); assert bit-identical f64 checksum. Same
   method that validated the parallel fluid solver (bit-identical across
   w = 1/2/4/8). Required kernel set: **AXPY including a deliberately aliased
   `axpy(y, y, k)`** (must be proven-aliased → no stamp), Jacobi sweep, a full
   Stable-Fluids frame, and an aliased stencil call site.
2. **Drift guard.** A CI check that the derived predicate never returns
   `proven_distinct` where a runtime `assert_disjoint(ai, aj)` would trip — closes
   the alias-frontier drift gap that the recent run of alias-launder fix commits
   proves is real.
3. **Ship behind `-fnoalias`**; flip ON-by-default only once both gates are green
   across the full suite. Mirrors the `ELISACORE_NOALIAS_MUTABLE_REFS` opt-in
   precedent.

## 5. Staged increments

- **Increment 1 — DONE (`612ddfbb`).** `ParallelForInfo.BandSourceDisjoint` +
  `DisjointViewCaptures`: proves band-source ⊥ view-captures when each resolves to a
  distinct fresh local. First brick of the `bufRoot` union-find.
- **Increment 2 — DONE (`c8f03b4a`).** Lift the band-only fact to a
  general per-ordered-param-pair `proven_distinct` over the union-find rules in 3.1,
  computed at call sites. Pure analyzer; no codegen yet. Add the self-vs-rest bit.
- **Increment 3a — DONE (`6722804b`).** Aggregate per-call `proven_distinct`
  observations into whole-program per-callee facts by intersecting every direct
  call site and conservatively excluding address-taken/ambiguous callees.
- **Increment 3b — DONE (`6519d98c`).** Consume the booleans at `emitFunction`;
  stamp paired scope/noalias on inner-ptr load/store, anchor scopes with
  `noalias.scope.decl`, and keep the feature guarded by
  `ELISACORE_NOALIAS_MUTABLE_REFS=1`.
- **Increment 4 — IN PROGRESS.** Differential O0-vs-O3 checksum harness + drift
  guard over the §4 kernel set. CI-wire. The bit-identical gate now covers AXPY,
  Jacobi/stencil, an explicitly aliased stencil, and a small multi-field
  fluid-frame-style update in `TestDisjointParamVectorizationBitIdentical`.
- **Increment 5 — flip default / optional keyword.** Default-on once green;
  optionally add the `disjoint` checked-assertion keyword + `-Wperf` hint.

## 6. Rejected alternatives (recorded so they aren't re-litigated)

- **Param-level `noalias` on the header** — sound but O3-inert (SROA). Shipped as
  the opt-in `-fnoalias` stamp; kept for `-O0`/scalar refs, not the vectorization
  path.
- **Standalone `disjoint`/`restrict` keyword the backend trusts** — re-introduces C
  `restrict`'s unverified-promise → silent-miscompile failure mode, and duplicates a
  soundness surface that drifts from the alias checker (the fix-commit history is
  the evidence). Demoted to assertion-only.
- **Per-call specialized accessor clones** — combinatorial code-size blowup across
  (T × group-shape × which-param) and must inline anyway. Inline-then-tag dominates.
- **Threading an alias-scope token as a runtime argument** — `alias.scope` is
  instruction metadata, not an SSA value; LLVM silently ignores it.
