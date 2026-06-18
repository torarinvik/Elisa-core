# 91 — Global death-time region inference

> Status: **design / RFC.** Proposes moving region *inference* from lexical-scope-driven
> to a whole-program **death-time** model. Builds on docs/68 (region memory model),
> docs/71 (multi-stack regions), docs/70 (perf friction). Nothing here changes the
> explicit `region r:` primitive — it changes what the compiler infers when you DON'T
> write one.

## 1. The target model (user's framing)

- Regions are tracked **globally**, across the whole program's control/data flow.
- A region is a **death cohort**: all heap objects that become dead *at the same time*
  share a region. Membership is decided by **when an object dies, not where it was
  created**.
- A region owns **one or more bump-stacks**. Every growable gets its own stack; the
  stack count is whatever avoids fragmenting a single stack (docs/71, already built).
- Stacks are **pooled and reused**: when a region dies, its stacks return to a pool and
  the next region that needs stacks draws from it.

```
@r1            @r2          @r3
[ growA ]      [ growC ]    [ growD ]
[ growB ]                   [ fixed… ]
[ fixed… ]
   │ dies at t1   │ dies t2    │ dies t3
   ▼              ▼            ▼
   └──── stacks returned to pool, reused by the next cohort ────►
```

## 2. Where this sits relative to docs/68

docs/68's thesis is deliberate: **the region is an explicit primitive**, not an automatic
per-object inference, and not a tracing GC. This RFC does **not** retract that. It refines
the *inference* path (`new T(...)` with no annotation; `__auto_N` scope wrapping) so that
un-annotated allocation is grouped by **global death time** instead of by **lexical scope**.

- Explicit `region r:`, `new[r]`, `@r`, `promote`/`adopt`, backing strategies — **unchanged**.
  They remain the control surface and the escape hatch.
- The change is: when the compiler infers, it does whole-program lifetime analysis and frees
  each cohort **at its death point**, which may be mid-function, not just at scope exit.

This keeps docs/68's "reason about a handful of lifetimes" property for code that uses
explicit regions, while making inferred memory tighter (earlier reclamation, less bloat) and
matching the user's mental model.

**Honest framing:** this is essentially *region inference in the MLKit lineage* (group
allocations by inferred lifetime, free the region when the lifetime ends), restricted to the
inferred subset and layered on Elisa's existing multi-stack + escape machinery. MLKit's known
failure mode — *region explosion*, where inference can't prove early death so a region lives
far too long — is the chief risk and is called out in §6.

## 3. What already exists (reuse, don't rebuild)

- **Multi-stack assignment (docs/71, landed):** `assignRegionStacks` already partitions a
  region's allocations into K parallel stacks by interval-graph coloring of crossing
  lifetimes — growables get their own stack, fixed/reserved share stack 0, with an over-split
  cap. **This is the per-region death-partitioning the user wants, already implemented — just
  scoped to one syntactic region today.**
- **Escape / region-provenance analysis:** tracks where values flow and rejects escapes; the
  interprocedural region-poly threading (docs/75, S1–S3) already moves a region across calls.
- **Backing strategies + reset/destroy:** the runtime can free a region's stacks at a point;
  `reserve_commit` keeps pages hot across resets.

The missing pieces are (a) a **global death-point analysis**, (b) using it to define region
**boundaries** (not just stacks within a syntactic region), and (c) a **stack pool**.

## 4. The core analysis — global death points

For every heap allocation site (and every value derived from it), compute its **death point**:
the earliest program point after which the value is provably never used again, on **all**
control-flow paths, **interprocedurally**.

- This is **liveness extended to heap allocations**, plus the existing escape facts: a value
  that escapes via return/param/global has its death point pushed out to the caller (the
  region-poly threading already computes this direction).
- **Path divergence** (A dies on the `then` path, lives on the `else`) → conservatively take
  the **later** death (the join), so a cohort never frees a still-live object. Over-approx =
  memory held longer, never a UAF.
- **Loops:** an allocation live across a back-edge dies at the loop's exit cohort, not per
  iteration, unless provably dead before the back-edge (then it's a per-iteration cohort —
  exactly `reset frame` today, inferred).
- **Dynamic/data-dependent death** (death depends on a runtime condition we can't pin
  statically) → the allocation joins the nearest enclosing statically-known cohort. This is the
  soundness floor: when in doubt, free later.

## 5. From death points to regions and stacks

1. **Cohort partition.** Group allocation sites whose death points coincide (or are forced to
   merge by path divergence) into death cohorts. Each cohort = one inferred region.
2. **Stacks within a cohort.** Run the *existing* `assignRegionStacks` coloring on the cohort:
   growables → own stacks, fixed → shared, over-split cap applies. No new code for this layer.
3. **Free at the death point.** Emit the cohort's `reset`/`destroy` at its death point — which
   may be mid-function (the advance over today's scope-exit-only freeing). This is where
   soundness is won or lost; it must be driven by the §4 analysis, ASan-validated.
4. **Stack pool (idea 3).** A thread-local pool of bump-stacks. A cohort draws K stacks on
   entry and returns them on death; the next cohort reuses them. This is the docs/68 `scratch`
   strategy generalized to the inferred default. Pool entries keyed by backing family + size
   class so a returned `reserve_commit` reservation is reused with pages hot.

## 6. Risks (this is the dangerous reframe)

- **Soundness / UAF (highest).** A death point computed too early frees a live object →
  use-after-free. Every increment must be ASan/`-fbounds-check`-validated, and the analysis
  must be conservative (fail to *later* death, never earlier). The existing escape checker is
  the backstop: an object that escapes must not be in a cohort that frees before its escape
  target.
- **Region explosion (MLKit's classic).** If inference can't prove early death, cohorts merge
  upward and memory is held to program end — silently worse than the scope model. Need a
  `-Wperf` diagnostic when an allocation's inferred cohort is much longer-lived than its
  syntactic scope, pointing the user at an explicit `region`.
- **Interprocedural cost / precision.** Whole-program death analysis is expensive and
  imprecise across separate compilation. Likely needs summaries (per-function death/escape
  signatures) rather than a true whole-program fixpoint.
- **Predictability.** docs/68 sells "a handful of arena lifetimes you can reason about."
  Fully-automatic cohorts are less predictable; the explicit `region r:` escape valve and a
  `--explain regions` dump that shows the inferred cohorts + free points are mitigations.

## 7. Staging (smallest-sound-first, each ASan-gated)

| Stage | Delivers | Risk |
|---|---|---|
| **G0 — death-point analysis (read-only)** | Compute + expose each allocation's inferred death point and cohort via `--explain regions`. No codegen change; pure analysis + a dump. Lets us *see* the cohorts the model would form and validate the analysis against real programs before trusting it. | low |
| **G1 — intra-function early free** | For a single function, free an inferred cohort at its death point inside the function (not just at body exit) when the analysis proves it dead. Reuse `assignRegionStacks` for stacks. ASan-validate. | high |
| **G2 — stack pool** | Thread-local pool; cohorts draw/return stacks. Realizes idea 3. Self-contained; can land independently. | medium |
| **G3 — interprocedural cohorts** | Death points that cross calls, via per-function escape/death summaries; merge cohorts across the call graph. | high |
| **G4 — diagnostics** | `-Wperf` region-explosion warning; `--explain regions` cohort/free-point report (built early in G0, refined here). | low |

Land **G0 first** — it is the foundation and is non-destructive: we get to inspect the
inferred death cohorts for real code and confirm the analysis is sound and tight *before* any
allocation is freed earlier. G2 (the stack pool) can proceed in parallel since it's
independent of the death analysis.

## 8. Design decisions

1. **Hybrid vs. fully-automatic — DECIDED: HYBRID.** Explicit `region r:`/`@r`/`new[r]`/
   `promote`/`adopt` stay first-class control + escape hatch; global death-time inference
   applies **only to un-annotated allocation**. Preserves docs/68's thesis and gives a manual
   override when inference is too conservative (the region-explosion mitigation).
2. **Analysis scope — DECIDED: WHOLE-PROGRAM FIXPOINT.** Most precise cohorts. Compatible with
   Elisa's build model: the compiler already ingests the whole program as one unit (include
   expansion → single compilation unit → native exe), so there is no separate-compilation
   constraint to honor. G0 starts intra-function (read-only, tractable) and G3 generalizes to
   the whole-program fixpoint over the call graph.
3. **Default backing for inferred cohorts (open, pick at G2).** Stay `chained` (docs/68 §3.1)
   or move inferred growables to `reserve_commit` for stable interior refs + page reuse in the
   pool?
4. **Predictability budget (open, tune at G1).** Conservative "free at nearest enclosing scope
   where provably dead" (close to today) vs. aggressive "free at exact last-use" (tighter,
   more analysis-fragile). Start conservative; tighten with G0 evidence.

## 9. Recommendation

Build **G0 (death-point analysis + `--explain regions` dump)** first: it's low-risk, it makes
the whole model *observable*, and it's the prerequisite for everything else. In parallel, build
**G2 (stack pool)** since it's independent and directly delivers the user's idea 3. Defer the
behavior-changing early-free (G1) and interprocedural cohorts (G3) until G0's analysis is
proven sound and tight on real programs. Resolve §8 decisions 1–2 before G1.
