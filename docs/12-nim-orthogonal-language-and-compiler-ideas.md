# Ideas From Nim That Look Orthogonal To `elisacore`

This note surveys the Nim compiler and standard language implementation for
ideas that look **useful**, **portable in spirit**, and **orthogonal** to the
current `elisacore` direction.

The goal is **not** to copy Nim wholesale, and not to import features that are
already close to work `elisacore` has explicitly chosen to center, such as:

- pointer provenance and typestate as first-class source concepts
- region checkpoints and region-aware storage
- proof-carrying views / optimization legality
- packed layout work
- explicit interface / dependency / foreign-source project metadata

Instead, this document focuses on features and implementation techniques that
would complement those systems.

## Selection Criteria

An idea made the shortlist if most of the following were true:

- it solves a problem `elisacore` will plausibly face soon
- it is mostly orthogonal to the current language core
- it can start as an internal compiler technique before becoming surface syntax
- it composes with existing design notes rather than replacing them
- Nim has a concrete implementation worth studying, not only a user-facing feature

## Best Candidates To Borrow

### 1. Guard/Fact Reasoning As A Reusable Internal Proof Engine

Nim has a surprisingly rich internal implication engine for facts learned from
conditions such as equality, inequality, set-membership, nil checks, and simple
arithmetic rearrangements.

Relevant Nim sources:

- `Code/Nim-devel/compiler/guards.nim`
- `proveLe*`
- `checkFieldAccess*`

Why this is attractive for `elisacore`:

- it is highly orthogonal to source syntax
- it helps prove legality rather than adding new user obligations
- it can discharge facts such as:
  - branch-local not-null knowledge
  - simple index/bounds relationships
  - discriminant/variant field-access legality
  - packed-view branch safety
- it aligns especially well with
  `docs/useful_language_features/11-proof-carrying-views-and-optimization-legality.md`

Why it is worth stealing:

Nim’s implementation is not just a generic “constant folder”. It keeps a small
model of facts, canonicalizes arithmetic expressions, and answers implication
queries conservatively. That is exactly the kind of internal engine that can
help `elisacore` prove optimization and safety facts without exposing theorem
proving syntax to users.

Recommended adaptation:

- keep it compiler-internal first
- model only a small fact vocabulary initially:
  - equality
  - non-null
  - simple `<=`/`<`
  - variant-tag membership
- use it in diagnostics and optimization legality before using it for source
  acceptance rules

### 2. Union-Find Graph Partitions For Alias/Mutation Tracking

Nim’s borrow checking and cursor inference are built around a graph-partitioning
pass over variables using a union-find structure with path compression.

Relevant Nim sources:

- `Code/Nim-devel/compiler/varpartitions.nim`
- `computeGraphPartitions*`
- `checkBorrowedLocations*`

Why this is attractive for `elisacore`:

- it gives a cheap, practical middle-end approximation for alias classes
- it tracks whether a graph is mutated, connected to a parameter, or borrowed
- it records live ranges and mutation timing, not only type-level relationships
- it is a great complement to explicit provenance/effect systems

Why it is worth stealing:

`elisacore` already has stronger source-level concepts than Nim in some areas,
but that does not automatically give the compiler a convenient internal graph of
"what can still alias or get mutated together across this function body".
Nim’s pass is a pragmatic answer to exactly that problem.

Recommended adaptation:

- use a similar graph partition for view alias classes and mutation regions
- feed it into:
  - borrow diagnostics
  - noalias/disjointness metadata decisions
  - cleanup elision safety
  - proof-carrying optimization legality facts

### 3. A Small Graph-Free Dataflow IR

Nim lowers AST bodies into a linear instruction stream of just a few control
primitives (`goto`, `loop`, `fork`, `def`, `use`) before dataflow analysis.

Relevant Nim sources:

- `Code/Nim-devel/compiler/dfa.nim`
- `constructCfg*`

Why this is attractive for `elisacore`:

- it is smaller and easier to reason about than a fully general CFG framework
- it is useful for move analysis, liveness, must-def/use checks, and simple
  legality queries
- it sits naturally between AST and backend lowering

Why it is worth stealing:

`elisacore` is starting to accumulate analyses that care about control flow:

- proof-carrying optimization legality
- foreign/native interop correctness
- destructor / cleanup placement
- branch-local ownership or packed-view facts

A tiny graph-free IR is a good fit for this stage of the compiler: strong enough
to support nontrivial analysis, but not a full IR rewrite.

Recommended adaptation:

- build a minimal per-function middle-end instruction stream
- keep the instruction vocabulary tiny at first
- use it for liveness / mutation / must-destroy queries before trying to make
  it the universal optimizer IR

### 4. Automatic Sink / Consume Inference

Nim has a small pass that detects places where a parameter should become a sink
parameter based on how values flow into assignments, constructors, and sink
calls.

Relevant Nim sources:

- `Code/Nim-devel/compiler/sinkparameter_inference.nim`
- `checkForSink*`

Why this is attractive for `elisacore`:

- it reduces annotation burden without weakening the core model
- it is orthogonal to regions, typestate, and permissions
- it can begin as a hint/warning system before becoming an inferred rewrite

Why it is worth stealing:

If `elisacore` keeps explicit move/affine concepts, users may still end up
writing a lot of obvious consume annotations. Nim shows a small and practical
way to infer “this parameter is effectively consumed” from usage patterns.

Recommended adaptation:

- start with diagnostics only: “this parameter is only ever consumed”
- later allow opt-in inference for private/local functions
- do not infer across ABI boundaries initially

### 5. Compiler-Generated Cleanup/Move Hooks For Aggregate Types

Nim synthesizes and/or forwards type-bound operations like destroy, sink, copy,
dup, and moved-state handling for aggregates, sequences, refs, and closures.

Relevant Nim sources:

- `Code/Nim-devel/compiler/liftdestructors.nim`
- `createTypeBoundOps*`
- `fillBody`
- `fillSeqOp`
- `atomicRefOp`
- `considerInferDupFromCopy`

Why this is attractive for `elisacore`:

- it gives a disciplined place for cleanup synthesis
- it scales better than hand-special-casing every aggregate lowering path
- it naturally exposes optimization opportunities such as redundant cleanup
  elimination

Why it is worth stealing:

This is not about copying Nim’s exact hook surface. It is about copying the
idea that a compiler can synthesize structured move/destroy behavior once per
type family and let later passes reason about it uniformly.

Recommended adaptation:

- start with compiler-internal synthesized cleanup plans rather than
  user-overridable hooks
- use it first for:
  - aggregates with affine members
  - region-tied temporary values
  - foreign/native ownership handoff wrappers

### 6. Return-Alias / Isolation Checking

Nim has an isolation analysis that asks whether a return value can alias an
input or whether an expression is “safe enough” to be treated as isolated.

Relevant Nim sources:

- `Code/Nim-devel/compiler/isolation_check.nim`
- `canAlias*`
- `checkIsolate*`

Why this is attractive for `elisacore`:

- it complements explicit provenance rather than replacing it
- it is directly relevant for noalias/disjointness reasoning
- it is useful for safe API design even if the source language never spells
  “isolated” explicitly

Recommended adaptation:

- use it internally for view-producing and ownership-transferring APIs
- integrate it with optimization-fact derivation and foreign-call contracts

## Good Secondary Candidates

### 7. SCC-Based Reordering For Declarations And Includes

Nim computes declaration dependencies, expands includes, builds a graph, and
uses Tarjan SCCs to reorder declarations conservatively.

Relevant Nim sources:

- `Code/Nim-devel/compiler/reorder.nim`
- `reorder*`

Why it is attractive for `elisacore`:

- it could simplify ordering constraints inside a file or module bundle
- it is a pragmatic companion to the new interface/dependency work already in
  `elisacore`
- it helps split user ordering from compiler ordering concerns

This is useful, but less urgent than the analysis ideas above.

### 8. Rule-Based AST Rewriting

Nim’s term-rewriting support uses structural patterns, binding, and alias-aware
rule application for macro rewriting.

Relevant Nim sources:

- `Code/Nim-devel/compiler/patterns.nim`
- `applyRule*`

Why it is attractive for `elisacore`:

- it could become a compact internal lowering/optimization rule engine
- it is especially interesting for repetitive packed/view rewrite rules

Why it is risky:

- exposed as a full user macro system, it is much larger than what
  `elisacore` currently needs
- internal-only rewrite infrastructure is likely the right first use

### 9. Generator / Coroutine Lowering To Explicit State Machines

Nim’s closure-iterator lowering is a detailed worked example of transforming
yielding control flow—especially through `try`/`except`/`finally`—into an
explicit state machine.

Relevant Nim sources:

- `Code/Nim-devel/compiler/closureiters.nim`
- `transformClosureIteratorBody`

Why it is attractive for `elisacore`:

- if `elisacore` eventually wants generators, streaming parsers, or resumable
  iterators, this is one of the best references in the repo
- the treatment of exceptions/finally during suspension is especially valuable

Why it is lower priority:

- it is orthogonal, but not yet obviously demanded by the current roadmap
- it likely wants the small dataflow/middle-end infrastructure first

## Things That Look Less Compelling To Copy Directly

### Lexer / parser architecture

Nim’s lexer and parser are competent and worth reading, but they do not look as
orthogonal or as high-leverage for `elisacore` right now as the semantic and
middle-end ideas above.

### Full macro / template system surface

Nim’s macro power is impressive, but importing it would change the character of
`elisacore` dramatically. The internal rewrite techniques are more reusable than
the exposed surface feature set.

### Copying ARC/ORC directly

Nim’s ARC/ORC implementation details are interesting, but `elisacore` already
has its own ownership/provenance direction. The better lesson is the cleanup
synthesis and analysis structure, not the exact memory-management model.

## Recommended Order For `elisacore`

If these ideas are adopted incrementally, the best order is probably:

1. **Guard/fact reasoning**
   - immediate payoff for diagnostics and optimization legality
2. **Graph partitions for alias/mutation**
   - strong complement to provenance and view facts
3. **Small graph-free dataflow IR**
   - gives a stable place for more analyses to live
4. **Return-alias / isolation checks**
   - useful for API reasoning and noalias decisions
5. **Sink/consume inference**
   - reduces annotation burden later
6. **Cleanup/move synthesis**
   - best introduced once the middle-end analysis exists
7. **Reordering / rewrite engine / generators**
   - valuable, but not first-wave work

## How These Fit Existing `elisacore` Notes

These Nim ideas align well with the current note set:

- pointer typestate notes benefit from guard/fact reasoning
- region checkpoints benefit from better liveness/mutation analysis
- packed-orthogonality work benefits from precise discriminant and alias facts
- proof-carrying optimization legality benefits from a stronger internal fact
  engine and dataflow layer
- export / C ABI work benefits from better internal ownership-transfer and
  foreign-call alias reasoning

The strongest Nim takeaway is therefore not a flashy source-language feature.

It is this:

> a lot of the next leverage for `elisacore` is likely in **small, explicit,
> compiler-internal reasoning engines** rather than large new surface syntax.

## Summary

The most promising orthogonal ideas to borrow from Nim are:

- a guard/fact implication engine
- union-find graph partitions for alias/mutation tracking
- a tiny graph-free dataflow IR
- return-alias / isolation checking
- sink/consume inference
- compiler-generated cleanup/move synthesis

These are attractive because they strengthen `elisacore`’s implementation model
without forcing it to abandon its current language direction.