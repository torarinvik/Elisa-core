# Verification foundation: hardening done + prerequisites for F*/Dafny-scale growth

This note records (a) the soundness/extensibility audit of the verifier, (b) the hardening
landed in this pass, and (c) the explicit prerequisites to satisfy *before* building the next
verification layer, so we don't paint ourselves into a corner.

## Audit conclusion

The expensive-to-retrofit soundness foundations are **clean** — leave them as they are:
- **Termination gating**: a recursive `ghost`/`lemma`/`law`'s facts enter SMT only behind a
  *verified* `decreases` measure; `decreases *` cannot grant it (`analyzer_termination.go`,
  `analyzer_lemma.go`). This is the core F*/Dafny soundness invariant.
- **Integer model**: mathematical `Int` constrained to each type's true range, with exact
  two's-complement wrap emitted whenever arithmetic isn't provably in range, backed by an
  overflow-checked interval prover (`wrapMachineArith`, `provablyNoArithWrap`,
  `boundAffine`). Prove-no-overflow when possible, model-exact-wrap otherwise, decline else.
- **Discharge ladder**: only SMT `unsat` concludes "proven"; `unknown`/`sat`/timeout/no-solver
  all fall back to a runtime check or hard error (`smtCheckQuery`, `smt.go`).
- **Escape hatches**: `trusted`, extern `ensure` (assumed only after its `requires` is proven),
  `-permissive` (downgrades to a *runtime check*, never "assume true"). All loud/greppable/scoped.
- **Lemmas, ghost, proof blocks, induction, modular verification** (verify against callee specs,
  not bodies): the right bones, extend freely.

## Hardening landed this pass

1. **Spec-clause purity** — `requires`/`ensure` clause expressions are now checked side-effect-free
   (same effect-set mechanism as `law` bodies), closing the spec-call determinism gap where a
   non-deterministic spec call could be modeled more strongly than reality.
   (`analyzer_functions.go::analyzeSpecClauseExpr`, `spec_purity_test.go`.)
2. **Precondition vacuity** — a provably-contradictory `requires` conjunction (SMT `unsat`) is now
   flagged via `proofLint` (warning; hard error under `-strict`), so a `requires false` can no
   longer silently make postconditions vacuous. Only `unsat` flags — no false positives.
   (`analyzer_smt_discharge.go::checkRequiresVacuity`, `vacuity_test.go`.)
3. **Structurally complete VC IR** — `vcFormula`/`vcTerm` gained first-class `vcQuant`
   (forall/exists + triggers) and `vcApply` (uninterpreted application) nodes with smart
   constructors, substitution, and byte-identical SMT emission. The IR is no longer opaque to
   quantifiers/equality/predicates. (`analyzer_vc_ir.go`, `vc_ir_test.go`.)
4. **Unified fact representation** — per-scope range facts and flow-local assert facts now sit
   behind ONE `hypothesisFact` interface with ONE renderer (`renderHypothesisFacts`) and ONE
   invalidation predicate (`factInvalidatedBy`), replacing divergent collect/invalidate logic.
   (`analyzer_fact.go`.)

All behavior-preserving except (1)/(2) which add diagnostics; full suite green.

## Prerequisites for the NEXT verification push (do at the start of that work, not speculatively)

- **Finish fact unification.** Guard/narrowing facts (`GuardFactSet`/`RefinementFacts`) are NOT yet
  behind `hypothesisFact` — they remain a third mechanism. Fold them in (interface + the single
  collector + `factInvalidatedBy`) before adding any new fact kind, so invalidation stays uniform.
  Divergent invalidation is the load-bearing retrofit risk.
- **Route the AST quantifier/equality/predicate path through the new VC nodes.** The `vcQuant`/
  `vcApply` nodes exist and lower correctly but the AST→SMT path (`boolTerm`) still builds the old
  opaque strings; reroute it so WP/trigger management can actually see inside quantifiers.
- **Heap/framing is the one real frontier — design before building.** `changes`/`preserves` is a
  path-set algebra (`analyzer_frame_changes.go`) entirely DISJOINT from the proposition/SMT layer.
  A richer heap logic (aliasable mutable graphs, recursive heap predicates, `modifies`-as-formula)
  is *new construction*, and it is the one place the region/ownership model and a logical heap
  could fight each other. Write a design note reconciling regions+ownership with the heap model
  BEFORE committing to dynamic-frames or separation-logic-style reasoning.
- **Additive (not corners), defer freely**: SMT datatype theory for enums/structs (today
  uninterpreted projection symbols), and generalized sequence/set/map theories (today
  integer-element-only `(Array …)`). These extend within the SMT translator without rework.
