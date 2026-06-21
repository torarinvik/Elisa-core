# 99 — Scoped Proof Automation ("Automation with Walls")

## Problem

Elisa's discharge ladder (tier-1 known-facts + intervals → tier-2 bounded-linear → external SMT)
proves an obligation against *every* fact in scope: the enclosing function's `requires`, immutable-
local defining equalities, branch guards, proven asserts/invariants, range facts. That is great for
ergonomics but bad for **stability**: a proof can pass today and break tomorrow because unrelated code
elsewhere in the function added (or removed) a hypothesis that nudged the solver's search, or because
a new ambient fact made the goal provable for the "wrong" reason. As conformance obligations nest, the
ambient hypothesis set grows and the solver's behavior becomes increasingly sensitive to surrounding
code. We want a way to say: *prove this goal in a closed world over only the facts I cite, and prove
it the same way regardless of what surrounds it.*

## Surface syntax

Scoped proof automation extends the existing `assert COND by:` proof block (Dafny-style) with a
closed-world opener:

```elisa
assert COND by scoped:
    assert(n >= 10)     # a cited intermediate fact (nested assert seed)
    weaken(n)           # a cited lemma: its proven `ensure` enters the closed world
```

* `by:` — the original **open world**. COND is proven from caller facts ∪ block facts. Unchanged.
* `by scoped:` — a **closed world**. COND is proven from the block's *citations only*. Ambient flow
  facts, assert facts, range facts, the enclosing `requires`, and immutable-local equalities are
  **walled out**.

A *citation* is anything established inside the block: a nested `assert(...)` seed, a lemma call whose
proven `ensure` becomes a hypothesis, or a nested `assert … by:`. The block keeps the same verification-
only whitelist as plain `by:` (lemma calls, nested asserts, invariants, static asserts) — it carries no
runtime effect and is erased from codegen; only COND lowers to an ordinary debug-gated assertion.

## Closed-world semantics

A `by scoped:` block is analyzed in a child scope flagged `closedWorld`. Two mechanisms together
implement the wall:

1. **Scope-walled fact channels.** Every fact-gathering chain walk
   (`smtAssertHypotheses`, `smtFlowFactHypotheses`, `lookupRangeFact`) includes the closed-world
   scope's own facts and then **stops** — it does not ascend to the parent. Facts the block establishes
   (cited lemma ensures, nested asserts) are recorded *into* the closed-world scope, so they are
   included; everything in enclosing scopes is excluded. Symbol and refinement *lookup* still ascend
   past the wall, so lemma names and type names resolve normally — only proof **facts** are walled.

2. **Suppressed ambient sources.** Two hypothesis sources are *not* gathered by scope walk — the
   enclosing function's `requires` and the defining equalities of immutable locals are read directly
   from the function declaration / symbol table. While a scoped block is discharged the analyzer sets
   `inClosedWorldProof`, and both sources return empty. So the closed world holds only the citations.

The net hypothesis set for `by scoped:` is: **the facts recorded inside the block, and nothing else.**

## Stability guarantee

> Given a fixed set of citations, a `by scoped:` block's verdict is a function of those citations and
> the goal alone — it is invariant under any change to the surrounding code that does not change the
> citations.

This holds because none of the ambient hypothesis sources can reach the solver: branch guards, sibling
asserts, the enclosing `requires`, and local equalities are all either scope-walled or suppressed. Two
textually-identical scoped blocks with identical citations produce byte-identical SMT queries (the query
cache, keyed on the query text, then guarantees the identical verdict). Refactors that add/remove
unrelated facts cannot perturb a scoped proof.

## Soundness

The wall only ever **removes** hypotheses; it never adds any. Therefore:

* A goal proven in the closed world (from a subset of the available facts) is *a fortiori* provable in
  the open world — scoped proofs never admit anything the open world would reject.
* A goal that needs an omitted fact correctly **declines** (hard error under `-strict`, proof-lint
  otherwise) — the wall is real, demonstrated by a goal that proves with the right citation and declines
  when it is removed.

The verification-only whitelist and codegen erasure are inherited unchanged from `assert … by:`, so the
block can never carry a runtime effect.

## Interaction with the discharge ladder & discharge classes

Scoped mode is **orthogonal to the ladder**: it narrows the hypothesis set, not the tiers. Within the
closed world the same ladder runs — tier-1 linear (`proveRequiresClause`, whose range lookups are
scope-walled) first, then the SMT tier (whose four hypothesis builders are walled/suppressed as above).
A proof is reported with its usual discharge class (`ProofProvenLinear` / `ProofProvenSMT`); the class
records *how* it was proven, while `scoped` records *what world* it was proven in.

## Implementation status (this increment)

Landed:

* AST: `AssertByStmt.Scoped`.
* Parser: `assert COND by scoped:` recognized (`looksLikeAssertByStmt`, `parseAssertByStmt`).
* Unparser: round-trips `by scoped:`.
* Scope: `closedWorld` wall flag; chain walks in `smtAssertHypotheses`, `smtFlowFactHypotheses`,
  `lookupRangeFact` stop at the wall.
* Analyzer: `inClosedWorldProof` suppresses `smtRequiresHypotheses` and
  `smtImmutableLocalHypotheses` during a scoped block.
* Tests: prove-with-citation, decline-when-citation-removed, wall-out-ambient, and an open-world
  control that proves from the same ambient fact (pinning the difference to the wall).

## Next increments

1. **Named lemma-set citations** — a `use LemmaA, LemmaB` citation form (sugar over lemma calls) and a
   diagnostic that *lists which cited lemmas were actually load-bearing*, so an over-broad citation set
   can be trimmed toward a minimal stable kernel.
2. **`proof:` block as a first-class statement** — a standalone closed-world proof region that discharges
   a following obligation, decoupling the citation list from a single `assert`.
3. **`arithmetic` / theory markers** — let a scoped block opt specific background theories (linear
   arithmetic, bitvectors) into the closed world explicitly, rather than implicitly via the translator.
4. **Closed-world for `requires`/`ensure`/conformance obligations** — extend `by scoped:` beyond
   `assert` to contract clauses, where nested-conformance stability matters most.
5. **Citation provenance in diagnostics** — when a scoped proof declines, report the closed-world
   hypothesis set so the missing citation is obvious.
