# docs/121 — Flow-Checked Loops: strict-mode control-flow constraints

Status: PLAN
Depends on: docs/119 (scoped-loop headers), docs/118 (ensure summaries), progress-safety system, `-Wperf` graduation model.

## 0. Thesis

Messy scanner/parser loops are hidden state machines: untyped flag bindings
(`depth == 0`, `in_string`), cursor mutation scattered across branches, and
divergent exit shapes. The pilot (Elisa-compiler `fa8e348`, `read_fstring`)
proved the clean shape — typed mode enum in the scoped-loop header + one
top-level `match` — needs **no new syntax** and is **bit-exact zero-overhead**
(same token checksums vs stage0, lexer bench unchanged at ~290 MB/s).

Therefore this is a **lint tier, not a language construct**. All six rules are
front-end structural checks on the AST: they never change codegen, so zero
overhead is satisfied by construction. The one common denominator the rules
enforce: **decide (pure reads) then act (one mutation, tail position)**.

Rejected alternatives (do not revisit): `scan while` keyword;
`emit`/`transition`/`fail` verb vocabulary (aliases for `return`/`<-`/`raise`);
"no nested if" absolutism; user-facing `@max_flow_complexity(N)` knobs
(thresholds stay compiler-internal; the only user surface is the grant).

## 1. The rules

| # | Rule | Trigger | Tier |
|---|------|---------|------|
| R1 | Nesting budget | conditional nesting > 2 inside a loop body; a `match` arm's single trailing `if` is exempt | strict error |
| R2 | State-flag ban | loop-carried int/bool binding that is literal-equality-compared in branch conditions AND reassigned in ≥ 2 branches | strict error |
| R3 | Duplicated advance | same mutating call on the same loop-carried binding in > K=4 branch sites, or any occurrence not in tail position of its branch | strict error |
| R4 | Exit budget | > 2 non-`raise` exit kinds from one loop with differing result constructor shapes | strict error |
| R5 | Progress obligation | a `while` body path that neither advances a cursor, strictly changes loop state, nor exits | strict error (extends existing progress-safety) |
| R6 | Single mutation per binding per path | same loop-carried binding assigned ≥ 2× on one path through the body | `-Wflow` warning only |

Escape hatch for R1–R4, R6: a **`can ComplexFlow:`** grant (bare family, like
`can Scalar`). R5 keeps its existing hatch `can Unsafe.AssumeProgress:`.
Grants are visible in source → hatch density is greppable.

Graduation: strict default → error; `-permissive` → warning; grant → silent.
Rollout starts warnings-only behind `-Wflow` (§6).

## 2. Infrastructure (Phase 0)

New file `compiler/src/semantic/analyzer_flow_complexity.go`. Plumbing mirrors
the perf-lint family exactly:

1. **Options.** Add `AnalyzeOptions.FlowLintMode` (enum: `off | warn | strict`)
   next to `EnforcePerfLints` (semantic/analyzer.go:711 area); copy to an
   `Analyzer.flowLintMode` field where `enforcePerfLints` is copied
   (analyzer.go:859). Default for Phase A: `warn` only when `-Wflow` given;
   Phase C flips default to `strict` with `-permissive` downgrading to `warn`
   (wire beside `-strict`/`-permissive` parsing in
   main_main_to_aggregatestateplaceholders.go:358-363 AND the parallel parser
   in project_system_consts_to_loadresolvedproject.go:450 — both, or project
   builds silently diverge from CLI builds).
2. **Emit wrapper.** `(a *Analyzer) flowLint(pos, format, ...)` modeled on
   `perfLint` (analyzer_pointer_graph_lint.go:12): `errorf` when strict,
   `warnf` when warn, no-op when off. All six rules emit only through this.
3. **Grant.** Register `ComplexFlow` as a known bare permission family
   (wherever `Scalar` is declared valid — follow the family registry consulted
   by analyzer_effects.go / permission validation). Track an
   `a.complexFlowGrantDepth` incremented on `*ast.CanStmt` whose refs satisfy
   `permissionRefsContain(refs, "ComplexFlow", "")`
   (helper at progress_safety.go:233) — same shape as the backend's
   `scalarGrantDepth` (llvm_bodies_..._clonepackedviewbindingmap.go:63,
   maintained at llvm_bodies_...emitstmt.go:1018), but semantic-side.
   Depth > 0 ⇒ R1/R2/R3/R4/R6 are no-ops.
4. **Call site.** `a.checkFlowComplexity(fn)` appended to the per-function
   analysis tail in semantic/analyzer_functions.go:204-210, next to
   `checkAllocationChurn`. It walks loops with `forEachFirstLoopBody`
   (analyzer_loop_perf_lint.go:8) — but note that helper stops at the FIRST
   loop; flow rules must recurse into nested loops, so add a
   `forEachLoopBody` variant that continues descending (inner loop bodies are
   analyzed as their own loops; an inner loop counts as ONE statement of the
   outer body for R1 depth).
5. **Loop model.** One shared pre-pass per loop builds a `loopFlowInfo`:
   - `carried`: the loop-carried mutable bindings = `WhileStmt.Captures`
     (ast_structtestexpr_to_pos.go:355) ∪ header-decl fresh bindings. Header
     decls arrive desugared (parser/loop_header.go `wrapLoopHeader`): statement
     form ⇒ VarDecls immediately preceding the loop inside the synthesized
     block; value form ⇒ VarDecls inside the ExprBlock wrapping the loop.
     Detect both: "VarDecl in the same synthetic scope whose value is read or
     assigned inside the loop". If the desugar makes this ambiguous, add a
     `FromLoopHeader bool` (or the loop's pointer) marker on VarDecl in the
     parser — small AST change, do it rather than heuristics.
   - `branches`: the branch tree of the body — each `if/elif/else` arm and
     each `match` arm is a branch site with a stable index/position.
   - `exits`: every `return`/`break`/`raise`/fall-through with its position
     and, for `return`/`break`, the result expression.
   - `mutations`: per carried binding, the list of (branch site, statement,
     kind: assign | lmut-rebind `x <- x.f()` | mutating method via `&`/lmut).
   Applies to `WhileStmt`, `ForStmt`, `IterForStmt`
   (handlers: analyzer_flow.go:864, analyzer_flow_loop_stmts.go:186/:503).
   `ParallelForStmt` is exempt (already constrained by disjointness rules).

## 3. Per-rule algorithms

### R2 — state-flag ban (build FIRST; highest precision)

For each carried binding `b` of integer or bool type:

- `cmpSites(b)` = branch-condition expressions where `b` is compared with
  `==`/`!=` (or is the whole condition / `not b`, for bools) against an
  integer/bool **literal**. Only conditions of `if`/`elif` and `match`
  scrutinee literal arms count — comparisons in ordinary expressions don't.
- `assignBranches(b)` = distinct branch sites containing a mutation of `b`.
- **Flag** when `|cmpSites| ≥ 1 ∧ |assignBranches| ≥ 2` for bools, and
  `|cmpSites| ≥ 2 ∧ |assignBranches| ≥ 2` for ints (the extra site keeps
  `depth == 0` used once as a guard legal — a counter with one boundary check
  is fine; `depth` compared in two places dispatching different behavior is a
  discriminant).
- **Exemptions:** binding is the loop's `decreases` measure; all assignments
  are monotone steps (reuse `assignIsMonotoneStep`/`augIsMonotoneStep`,
  progress_safety.go:190/:204) AND all comparisons are inequalities — that's a
  counter, never flag it. This keeps the rewritten `read_fstring`'s `depth`
  legal.
- **Diagnostic** (the hint text is the steering mechanism — LLMs regenerate
  from it, so it must contain the full recipe):

```
error: loop-carried flag `in_string` encodes an untyped state machine
  [-Wflow] compared against literals here and here, reassigned in 2 branches
  fix: name the states and dispatch on them:
      const enum ScanMode of u8:
          <StateA>    # in_string == false
          <StateB>    # in_string == true
      while COND |…, mode: ScanMode = ScanMode.<StateA>|:
          match mode: …
  escape hatch: wrap the loop in `can ComplexFlow:`
```

  Naming: enumerate the distinct literal values compared against; if 2+ flags
  interact (both flagged in the same loop), say "these N flags together encode
  up to 2^N states — one enum replaces all of them" and list the flags.

### R3 — duplicated advance / tail position

- Group mutations by key `(root carried binding, method name)` where the call
  is an lmut rebind (`lexer <- lexer.advance_char()`) or a mutating method on
  a `mutable&` carried binding. Root = the binding itself (not fields).
- Count **distinct branch sites**; two calls in the same straight-line branch
  (e.g. deliberate advance-twice) count once — R6 covers intra-branch doubles.
- Flag when sites > K=4, **or** when any site is not the branch's final
  mutating statement of that binding (tail-position violation) AND sites ≥ 3.
  The compound condition avoids flagging tiny 2-branch loops where a mid-branch
  advance is obviously fine.
- Exemption: a multi-width advance helper (`advance_chars(n)`) counts as the
  same key as `advance_char` ONLY if both appear; message then suggests
  unifying on the width-parameterized form ("decide width, advance once").

### R1 — nesting budget

- Walk the body counting conditional depth: `+1` per `if`/`elif` chain (a
  chain is ONE level regardless of elif count), `+1` per `match`.
- **Carve-out:** inside a `match` arm, one trailing `if`/`if-else` whose
  branches contain no further conditionals does not increment depth. "Trailing"
  = the last statement of the arm, or the only statement.
- Nested loops reset the counter (the inner loop is its own jurisdiction).
- Flag at depth > 2. Diagnostic names the outermost offending chain and gives
  the two standard fixes: extract a named function, or (if R2 also fired on
  this loop — cross-reference the shared `loopFlowInfo`) the enum-state
  rewrite. When R1 and R2 both fire on one loop, emit ONE combined diagnostic
  (the R2 message), not two.

### R4 — exit budget

- Classify exits from `loopFlowInfo.exits`:
  - `raise` — exempt, never counts (error unions already force handling).
  - `return EXPR` — shape = head constructor of EXPR (call target name for
    `Token(…)`, literal kind for `-1`, binding for `value`).
  - `break` / `break VALUE` — shape = value's head or "void break".
  - loop-condition fall-through (+ `else:` value) — one shape.
- Flag when > 2 distinct non-raise exit kinds AND ≥ 2 distinct shapes among
  `return`/`break` values. All-same-shape (every exit builds
  `Token(FString, …)`) passes at any count.
- Sentinel sharpening: if ≥ 2 exit shapes are integer literals (the `-1` vs
  `0` case), the hint explicitly says "distinct sentinel literals — return
  `T or ErrorSet` and `raise` instead".

### R5 — progress default-on for `while`

Extends the existing system rather than a new engine:

- Today: `recordProgressLoopObligation` (progress_safety.go:87) /
  `finishProgressLoopObligation` (:117) produce a per-function "no progress
  evidence" diagnostic gated by `a.enforceUnsafePermissions` (:78), with
  `isCountingLoop` (:134) discharging counted loops and
  `Unsafe.AssumeProgress` exempting (:92, :245).
- Change 1 — **per-path, not per-loop**: for loops with a `loopFlowInfo`,
  check each root-level branch path: it must contain (a) a mutation of a
  carried binding, or (b) an exit, or (c) a call already known to advance a
  cursor. Diagnostic points at the exact branch ("branch 2 neither advances,
  changes state, nor exits") instead of the whole loop.
- Change 2 — **callee evidence via docs/118 summaries**: `advance_char`'s
  body does `position <- position + 1`; the interprocedural-termination
  ensure-summary machinery (docs/118) already extracts `old()`-based
  monotone facts. Consume: a call on a carried lmut binding whose callee
  summary implies `result.position > old(self.position)` (given the loop's
  `not is_end_of_source()` guard) is progress evidence. Where no summary
  exists, an explicit `ensure result.position > old(lexer.position) or
  lexer.is_end_of_source()` on the helper discharges it — and stage1's
  `advance_char` should gain exactly that ensure as part of calibration.
- Change 3 — gate by `flowLintMode` (not `enforceUnsafePermissions`) so it
  graduates with the rest of this family. Keep `for` loops out (structurally
  bounded already, handled by existing termination checks).
- State-change evidence must be **non-cyclic** to count on its own: `mode <- X`
  counts only if some path from state X exits or advances (cheap fixpoint over
  the ≤ dozen mode values when the state is an R2-shaped enum; if the state
  graph is not analyzable, a bare state change still counts — soundness of
  termination stays the prover's job, this rule only catches the obvious
  `pass`-branch hang).

### R6 — single mutation per binding per path (`-Wflow` only, never strict in v1)

- For each carried binding: enumerate root-level paths (product of sequential
  branch points, capped — bail silently past 64 paths); flag a path with ≥ 2
  mutations of the same binding **when the branch points are sequential
  siblings** (the order-coupled `if …: count+=1` then `if …: count-=1` case).
  Two mutations in the SAME straight-line block are fine (deliberate
  double-advance is R3/R5's business).
- Hint: "compute the delta in a match expression, apply once".

## 4. Testing

Per rule, in `semantic/flow_complexity_<rule>_test.go`, using the established
two-options pattern (disjoint_param_perf_lint_test.go:18 shape,
`analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics` +
`allDiagnostics`):

1. **Positive**: the "banned" example from the design discussion (all six
   before/after pairs become fixtures verbatim) — warning under
   `FlowLintMode: warn`, error under `strict`, silent under `off` and under
   `can ComplexFlow:`.
2. **Negative (false-positive guards)** — these are the acceptance bar:
   - rewritten `read_fstring` shape (mode enum + depth counter) passes ALL
     rules;
   - plain counters (`i <- i + 1` with `<` bound) pass R2;
   - `read_char`/`read_string`-class simple loops pass everything;
   - a 2-branch loop with one mid-branch advance passes R3;
   - multi-exit loop where every exit is `raise` or one constructor passes R4.
3. **Combined-diagnostic test**: R1+R2 on one loop ⇒ exactly one message.
4. **End-to-end CLI** (`flow_lint_cli_test.go`, modeled on
   wperf_scalar_permission_test.go:14): `-Wflow` stderr contains
   `warning [-Wflow]`; strict mode exit code nonzero; grant silences.
5. **Ensure-summary test for R5**: helper with the `ensure`-based advance is
   accepted as progress evidence; same helper without it is not.
6. Hook into `make test-semantic` (these are plain semantic tests, ~10s tier).

## 4b. Phase A calibration results (LANDED)

Ran `-Wflow` over the full stage1 tree (36 files). First pass: **28 warnings**. Every one was a
false-positive *class*, not a genuine bug (stage1 was already hand-converted to clean scoped
loops), so each was eliminated by a principled exemption — not by weakening the rule:

1. **Balanced counters (R2, 15 hits — `depth`/`type_depth`).** A bracket/nesting counter steps
   both directions (`depth+1`/`depth-1`) and is checked `depth == 0` in several places, clearing
   the int thresholds. Fix: `bindingIsMonotoneCounter` exempts a binding whose every mutation is a
   ±constant step, INCLUDING bidirectional — the mark of a real discriminant is assigning distinct
   constants (`state <- 2`), never a step.
2. **Sticky latches (R2, 5 hits — `saw_explicit_arg`/`terminated`).** A bool set `true` in several
   branches and never reset is a one-way fact, not a toggled machine; an enum rewrite is wrong
   advice. Fix: `bindingIsStickyLatch` exempts a bool assigned only ONE literal across the body. A
   genuine flag (`in_string`) is assigned both `true` and `false`.
3. **Per-arm scanners (R3, 6 hits — including the `read_fstring` REWRITE itself).** The original R3
   "advance in > K arms" trigger fired on any typed-mode scanner, which advances once per match arm
   — the exact shape R2 pushes toward. Contradictory guidance. Fix: **dropped the arm-count trigger
   entirely**; R3 now flags only an *unguarded repeated advance* — two advances of the same cursor
   in one block with no `if`/`match`/`return`/`break`/`continue` between them (the real double-skip
   bug). The escape-consumes-two idiom (`advance; if at_end: return; advance`) is guard-separated
   and passes.
4. **Threaded accumulators (R3, 2 hits — `table`).** `table <- walk_expression(entry.key, table,…)`
   threads state as a NON-receiver argument, syntactically resembling a cursor advance. Fix:
   `advanceOfAssign` requires the binding to be the call's RECEIVER (`lexer <- lexer.advance_char()`),
   not an incidental argument.

Second pass after the four fixes: **0 warnings on stage1** — a precise lint over already-clean code
correctly finds nothing, while the unit tests (`flow_complexity_test.go`) prove both rules fire on
the genuine patterns (`in_string` toggle, back-to-back double-advance). Each exemption has a locked
negative-test fixture. Broad corpus (shadps4/wolf3d `while`-loop files) is a supplementary sweep.

Wiring note: `-Wflow`/`-Wflow-strict` are parsed in BOTH the single-file CLI and the project-system
CLI. The declarative manifest `[warnings] flow` field is deferred to Phase C (when the default flips
and manifest control becomes meaningful) — there is no divergence risk while the lints are
off-by-default.

## 5. Calibration (the gate before any default flips)

1. Run `-Wflow` over the **stage1 tree** (36 files — the tree was just
   hand-converted to scoped loops, so it's a strong "should mostly pass"
   corpus) + the wolf3d and shadPS4 frontends.
2. Budget: **< 5 warnings across stage1**, each either a genuine improvement
   (fix it) or a named false-positive class (tune the rule; add the fixture
   as a negative test). R2's int threshold (`|cmpSites| ≥ 2`) and R3's K=4 /
   tail-position compound are the two knobs most likely to need adjustment.
3. Instrument: a `-Wflow-stats` debug flag printing rule-hit counts per file,
   so tuning is data-driven rather than anecdotal.
4. Add stage1's `advance_char`-family `ensure` annotations (R5 Change 2).

## 6. Rollout

- **Phase A** (P0 + R2 + R3): warnings behind `-Wflow`. Calibrate per §5.
- **Phase B** (R1 + R4 + R5): still `-Wflow`. Second calibration sweep.
- **Phase C**: default flips to strict error; `-permissive` → warning;
  `-Wflow` becomes a no-op alias. Stage1 must be 0-warnings-or-hatched first.
  Gate: full smoke suite + `make test-full` + lexer bench unchanged.
- **Phase D**: R6 lands, permanently warn-tier. Revisit promotion only with
  fresh data.
- **Phase E** (later): stage1 port of the checker into the self-hosted
  semantic pass (diagnostics already reach the LSP, so IDE surfacing is free
  as soon as stage0 emits them; the port follows the usual stage1-parity
  process). Not a blocker for A–D.

Estimated sizes: P0 ~200 LoC; R2 ~250 + tests; R3 ~150; R1 ~120; R4 ~180;
R5 ~200 (mostly inside progress_safety.go); R6 ~100. Each phase is an
independent commit with its own tests; nothing blocks on anything outside its
phase except the shared `loopFlowInfo` pre-pass (P0).

## 7. Risks / open questions

- **Header-decl recovery post-desugar** (§2.5): if VarDecl provenance is
  ambiguous, the parser marker is mandatory — mis-classifying an ordinary
  local as loop-carried would produce nonsense R2/R6 hits.
- **R5 false positives on effectful-call loops** (loop bodies that only call
  `send`/`write` style functions with no cursor): the "callee mutates a
  carried binding" clause covers most; anything else is what
  `can Unsafe.AssumeProgress` is for. Watch the count in calibration.
- **Grant granularity**: `can ComplexFlow:` wraps a block, so it can scope to
  ONE loop rather than the whole function — document that as the recommended
  usage so hatches stay narrow.
- **Diagnostic fatigue**: one loop can trip R1+R2+R3 simultaneously. The
  combined-diagnostic rule (§3-R1) must cover R3 too: if R2 fires, suppress
  R1/R3 on the same loop — the enum rewrite fixes all three.
- **Interpreter/consteval paths**: rules run in `checkFlowComplexity` on the
  analyzer only; `-emit test` and consteval are unaffected by construction,
  but add one smoke to prove strict mode doesn't reject the stdlib.
