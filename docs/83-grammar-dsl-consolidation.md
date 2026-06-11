# 83 — Grammar DSL Consolidation: Perfect, Prune, Finish

## Why

The grammar DSL was the language's founding use case. Now that Elisa is general
purpose, the DSL must earn its keep as the flagship *domain layer*: small,
declarative, and strictly orthogonal to the core language. An audit of the four
real consumers (SML, Pascal, Lua, ATPL frontends — the only in-tree users) shows
a consistent split:

- **The closed, structural parts shine**: infix tables, generic grammar
  combinators (`separated_by[T]`, `recovered[T]`), declarative recovery,
  `grammarenv` + `extend grammar` composition.
- **The open, semantic parts leak**: context-sensitive parsing falls out of the
  DSL into ad-hoc `state.` helper methods, fake `guard()`s, and hand-rolled
  recursion pairs.
- **Some "missing" features already exist but are unused**: `*` postfix
  repetition, tokensets in `required()`/union positions. The frontends predate
  them or never adopted them.

## The orthogonality invariant (non-negotiable)

The DSL is **pure desugaring**: `LowerDecl` turns every grammar declaration into
plain Elisa `FuncDecl`s *before* semantic analysis
(grammar/lower_lowergrammaruntilexpr_to_lowerconcatexpr.go:142). No grammar AST
node survives past lowering; the semantic analyzer and backend have zero
grammar-specific cases. Every change below must preserve this: new forms may
only add parser surface + lowering rules that emit existing core-language
constructs. If a proposed feature needs a semantic-analyzer or backend special
case, it is rejected or redesigned.

## Bucket 1 — Perfect the good parts

These stay and get polished, not extended:

1. **Infix tables** (`infix table X(top): … left/right/nonassoc …`) — the DSL's
   best idea. Precedence ladders read like the language spec. Keep as-is.
2. **Generic grammar combinators** — `grammar f[T](item: grammar -> T, …) -> T`
   gives parser-combinator power with declarative syntax. Keep; ensure error
   messages on misuse are first-class (they lower early, so diagnostics must
   point at the grammar source, not the lowered function).
3. **Declarative recovery** — `recovery Name: message/until/fallback` blocks +
   by-name `recover(Name)` references. Canonical form. The inline
   `recover(message, until(…), fallback)` stays as sugar for one-offs (both
   already share lowering via `resolveGrammarRecoveryPolicy`).
4. **`grammarenv` / `extend grammar` composition** — cross-file mutually
   recursive grammars work today. Keep; fix the boilerplate (Bucket 3 §4).

## Bucket 2 — Remove the bad parts

1. **Redundant repetition spellings.** Today: `repeat`, `flatrepeat`, `list`,
   `while` (immediately rewritten to flatrepeat, token_sets.go:184), `separated`,
   and `*`. Six spellings, overlapping semantics. Target family:
   - `term*` — zero or more (exists).
   - `term+` — one or more (new, trivial lowering: `term` then `term*`).
   - `separated term by sep until(stop)` — delimited lists (exists, keep).
   - `term* while(pred)` — predicate-bounded repetition (new, Bucket 3 §2).
   `repeat`/`flatrepeat`/`list`/bare-`while` become deprecation-warned aliases,
   then hard-removed after frontend migration (standard removal pattern from the
   deprecation roadmap).
2. **`guard()` as a recovery smuggling device.** The Pascal idiom
   `guard(state.current_token().kind in BlockEnd) recover(…)` is ad-hoc parser
   code wearing grammar syntax. Once `term* while(pred)` and checked sync points
   exist, lint this pattern (a guard whose only effect is its attached recover)
   and migrate the frontends off it. `guard()` itself stays — non-consuming
   predicates are legitimate — but its recovery-abuse idiom goes.
3. **Nothing else is dead.** The audit found no unused lowering paths; the
   pruning already done (legacy DSL surface ae795324, filtered views b996cc97)
   removed the genuinely dead surface.

## Bucket 3 — Finish the half-baked parts

In priority order:

1. **Tokenset algebra + adoption.** Tokensets are already legal in `required()`
   and `|` unions, and recursive tokenset-in-tokenset composition works
   (token_sets.go:105-139). Two gaps:
   - **Set operators**: `tokenset A = B + C - D` (union/exclusion of existing
     sets plus literal tokens). Pure normalization-time expansion; no lowering
     change below `resolveGrammarTokenSetStop`.
   - **Migration**: Pascal's four copies of a 57-alternative
     `required(.IDENT | .OBJECT | … | .HARDFLOAT, …)` become one
     `tokenset PascalNameLike` used four times. This is user-code cleanup that
     mostly works *today* — it is also the verification that the feature is
     actually complete (any position that rejects a tokenset ref is a bug to fix).
2. **Predicate-bounded repetition with accumulation.** The single biggest
   boilerplate source: Pascal's hand-rolled recursion pairs
   (`X_groups` / `X_group_and_rest` + `when(state.is_start(), …)` + prepend
   helper) exist because `until(…)` is token-based and `*` has no predicate
   form. Add `term* while(pred)` lowering to a plain loop that pushes into a
   darray (the lowered code is exactly what `lowerFlatRepeatLoopBody` emits,
   with the loop condition swapped for the host predicate). Kills ~10 recursion
   pairs across Pascal alone.
3. **First-class host delegation ("external rules").** Today host escape is
   `expr(host_expression)` per-term, and whole-rule delegation is an `expr()`
   wrapping trick. Make the seam honest:
   ```
   grammar expression -> SMLExpr = state.parse_expression
   ```
   — a rule whose body is a single host function reference, callable from other
   rules like any production. Lowering: a one-line FuncDecl forwarding to the
   host function. This replaces the `dynamic_infix_*` three-helper contortions
   with a declared, typed boundary, and documents *where* the grammar
   deliberately hands off.
4. **`grammarenv` defaulting.** All ~11 clauses are currently mandatory; every
   frontend's env is character-for-character parallel. Default the conventional
   names (`current` → `current_token`, `advance` → `advance_token`,
   `expect`/`expect_kind`/`record_error` → same-named methods, `token_field` →
   `kind`, `eof` → `<TokenKind>.EOF`); a minimal env becomes:
   ```
   grammarenv SMLGrammarEnv over SMLToken using SMLParserState:
       cursor state
       alloc alloc
   ```
   with overrides only when a frontend deviates. Pure parser-side defaulting;
   lowering unchanged.
5. **Dynamic fixity tables (the biggest leak, last).** SML's user-defined
   operator fixity cannot be expressed, forcing the collect-then-rebuild pattern
   (`build_sml_expr_infix_chain` + `consume_sml_infix_operator_token` +
   `is_…_infix_token_here`, duplicated for expressions and patterns). Add a
   runtime-fixity variant of the infix table:
   ```
   dynamic infix table ExprTable(state.lookup_fixity):
       atom = application_expr()
       operator = SMLOperatorName
   ```
   lowering to the standard precedence-climbing loop with binding power fetched
   from the host callback instead of the static table. This is the one item
   with real design risk; it lands only after 1–4 prove out, and only if it
   lowers to plain Elisa like everything else.

## Phases

- **Phase 0 — migrate to what exists** (no compiler changes): Pascal token
  unions → tokensets; audit frontends for `*`-able recursion; fix any position
  that rejects a tokenset ref. Output: smaller frontends + a verified feature
  matrix.
- **Phase 1 — ergonomics**: `+`, `term* while(pred)`, tokenset `+`/`-`
  operators, grammarenv defaults. Each is parser + lowering only.
- **Phase 2 — canonicalize**: deprecation-warn `repeat`/`flatrepeat`/`list`/
  bare-`while`; migrate frontends to the canonical repetition family;
  hard-remove per the standard pattern. Lint the guard-recovery idiom.
- **Phase 3 — the seam**: external rules; migrate `dynamic_infix_*` helpers and
  `expr()`-wrapping tricks onto them.
- **Phase 4 — dynamic fixity tables**: design doc addendum first, then
  implementation; retire the SML chain-builder helpers.

Each phase ends with all four frontends + the golden suite green; the frontends
are the usage oracle — any DSL feature they still can't express cleanly after
Phase 4 is a candidate for *removal*, not further extension.
