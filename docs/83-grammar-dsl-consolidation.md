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

## Phase 0 results (2026-06-12) — SML/Lua/Perl oracle

Scope decision: Pascal + ATPL deferred (blocked on the `tree` and
`bundle AllocCtx` removals — separate, large migrations). The oracle for
Phases 1–4 is SML (156 tests), Lua (236), Perl (30), all green before and
after every step below.

**First correction to the plan: Lua uses no grammar DSL at all** — it is a
fully hand-written recursive-descent parser. The effective DSL oracle is
SML + Perl. (Whether Lua *should* migrate to the DSL is a separate question;
as-is it contributes nothing to the feature matrix.)

### Migrations performed (user-code only, zero compiler changes)

1. **Hand-rolled recursion eliminated — SML now has zero self-recursive
   productions.** Audited mechanically (script over every `grammar` block);
   Perl had none to begin with (its one hit was an alias wrapper, not
   recursion). Three SML families migrated:
   - `qualified_type_name_tail` / `qualified_expr_name_tail` (DOT-path
     prepend-recursion) → segment rule + `segment()*` + one host fold
     (`sml_name_path_from_tokens`).
   - `declaration_group_semicolon_continuation` (skip-extra-semicolons
     recursion) → bare token repetition `.SEMICOLON*` in statement position.
   - `sig_where_type_tail` + `sig_where_type_realization(_after_and)` (a
     *parameterized* seed+fold recursion, `atom (WHERE clause (AND clause)*)*`)
     → clause-struct (`SMLWhereTypeClause`) + two `*` repetitions + host
     apply (`apply_sml_where_type_clauses`). Notably **`suffix()` was not
     needed**: collect-with-`*` + host fold is simpler than an op-arm fold
     and kills the accumulator-parameter idiom entirely.
2. **Inline token unions → named `token family`s, 40 sites.**
   `required(.IDENT | .CONSTR_IDENT, …)` ×33 (+1 `lookahead`) →
   `SMLNameIdent`; `.IDENT | .INTEGER` ×4 → `SMLRecordLabel`;
   `.IDENT | .CONSTR_IDENT | .SYMBOL_IDENT` ×2 → `SMLBindableName`.
   Zero inline unions remain in SML/Perl.

### Verified feature matrix

| Feature | Status | Evidence |
|---|---|---|
| `term*` on rule calls | works | `qualified_*_segment()*`, `sig_where_type_and_clause()*` |
| `term*` on bare token terms, statement position | works | `.SEMICOLON*` |
| `term*` binding a darray-returning rule (`darray[darray[T]]`) | works | `sig_where_type_group()*` |
| `token family` in `required()`/`lookahead()` | works | `SMLNameIdent`/`SMLRecordLabel`/`SMLBindableName`, 40 sites |
| `token family` cross-grammar via `uses` | works | families declared in SMLTypeGrammar/SMLDeclGrammar, used in 6 files |
| tokenset in `required()` | works (pre-existing) | `required(SMLOperatorName, …)` |
| `separated … by … until`, infix tables, generic combinators, `|>`, `grammar alias`, `recover` | heavily used (pre-existing) | throughout SML/Perl |
| `suffix()` | exists, **unneeded** for the oracle's folds | sig_where migration |

No position rejecting a tokenset/family ref was found — the "fix any position
that rejects a tokenset ref" line item turned out to be empty for this oracle.

### Findings that adjust the later phases

- **`flatrepeat` is not a redundant spelling of `*`** (the plan's Phase 2
  framing is wrong on this point): `term*` collects into `darray[elem]`,
  while `flatrepeat` *flattens* darray-valued elems into one accumulator
  (`lowerFlatRepeatLoopBody` pushes indexed items). SML has 38 live
  `flatrepeat … until(stop)` uses relying on flattening. Phase 2 must either
  keep `flatrepeat` under a canonical name or give `*` a flatten variant —
  plain deprecation would regress the oracle.
- **The dominant remaining wart is `when(state.current_token().kind == X, a, b)`**
  — ad-hoc token-gated dispatch, used pervasively (SML ~30+ sites) where a
  declarative token-gated choice belongs. The DSL already parses
  `TOKEN? then … else …` (parser: `parseGrammarOptionalTokenGateTerm`) but the
  frontends don't use it. Evaluating a migration (and whether the gate form
  covers rule-call gating, not just token gating) should be added to Phase 1.
- `+` would have expressed both halves of the semicolon migration
  (`.SEMICOLON+`) and the where-group chain; Phase 1's case for `+` is
  confirmed by real sites.

## Phase 1 results (2026-06-12) — ergonomics landed

All four items shipped (parser + lowering only; the pure-desugaring invariant
holds — no semantic/backend changes anywhere):

1. **`term+` one-or-more** (`GrammarRepeatTerm.MinOne`). Postfix `+` after any
   term, with optional `until(stop)`. Disambiguation vs binary concat `a() + b()`:
   `+` is postfix iff the next token cannot start a term (newline, `)`, `|`,
   `|>`, `?`, `->`, `until`, `recover`); concat parsing yields in that case.
   Lowers to the existing list loop plus `matched = count != 0` — zero items
   fails the attempt uncommitted (cursor already restored). FIRST-set facts:
   `+` is nullable only if its elem is. SML migrated:
   `.SEMICOLON .SEMICOLON*` → `.SEMICOLON+`.
2. **Tokenset `+`/`-` operators** (`GrammarTokenSetDecl.Excluded`).
   `tokenset A = B + C - D` in the `=` form (`,`/`|`/`+` all union; `-`
   excludes); block form accepts `- ITEM` lines. Difference resolves
   recursively (excluded items may be whole sets) and applies after union;
   only token kinds / leaf refs are excludable. SML proof: 8 sync sets
   collapsed to one-liners, e.g. `ValPatternSync = DeclarationSync + EQ`,
   `DatatypeDeclGroupStop = DatatypeConstructorSync - AND`, including a pure
   alias `FunClauseGroupSync = DeclarationSync`.
3. **grammarenv defaulting.** Conventional clauses now default:
   `token_kind` → `<over-type>Kind` (name convention), `eof` →
   `<token_kind>.EOF`, `token_field` → `kind`, `current` → `current_token`,
   `advance` → `advance_token`, `expect` → `expect`, `expect_kind` →
   `expect_kind`, `record_error` → `record_parse_error` (the observed oracle
   convention, not `record_error` as originally drafted). Both SML and Perl
   envs shrank from 11 clauses to `cursor state` + `alloc alloc`.
4. **Token-gate evaluation (finding).** The parsed-but-unused
   `TOKEN? then … else …` gate is NOT the replacement for the ~80
   `when(state.current_token().kind == X, a, b)` sites: the gate *consumes*
   the token, the when-sites *peek*. The correct existing construct is ordered
   `choice:` (attempt + rollback) — verified by migrating the two worst
   nested chains (`type_var_sequence`, `sig_expr` 3-deep) to `choice:`;
   suites green. The remaining ~76 sites are mechanical; bulk migration moves
   to Phase 2 alongside a lint that flags peeking when()-gates.

Verified after each item: SML 156, Lua 236, Perl 30, full `go test ./...`.
New compiler tests: plus-postfix parse/disambiguation, tokenset
union/difference lowering, grammarenv-defaults lowering.

## Phase 2 results (2026-06-12) — canonicalization

1. **Legacy repetition spellings deprecation-warned.** `repeat …`/`repeat(…)`,
   `list(…)`, and the bracketed `[term] while tok in tokens != […]` form now
   emit parse-time deprecation notices pointing at the canonical family
   (`term*`, `term+`, `separated … by … until`, `flatrepeat` for flattening).
   This added the parser's first non-fatal diagnostics channel
   (`Parser.Notices()`, printed by the driver alongside semantic notices).
   The frontends had **zero** uses of any legacy spelling — only compiler
   tests exercise them (kept, as deprecation-period coverage). Hard removal
   can follow the standard pattern in a later pass.
2. **`flatrepeat` kept as canonical.** Per the Phase 0 finding it is the
   flatten form, not a duplicate spelling; it does not warn.
3. **when()-gate bulk migration: 45 of 80 SML sites → ordered `choice:`**
   (2 nested chains by hand in Phase 1, 43 single-level scripted). The script
   migrated only the provably-safe shape: gate kind == the called rule's
   *bare leading token match* (rules leading with `required(.K)` were excluded
   — required records an error instead of failing the attempt, so choice
   would change recovery behavior). Host-fn else-arms were wrapped in
   `expr(…)`. Perl had zero gate sites. The 35 remaining SML sites are
   legitimately context-sensitive (tokenset-membership conditions, multi-token
   lookahead, non-first-token dispatch) and stay as `when()` — they are the
   honest residue, not migration debt. The planned "peeking-gate lint" was
   dropped: with the mechanical sites migrated, a lint would only nag the
   legitimate residue.
4. **`term* while(pred)` deferred.** Its motivating user is Pascal's
   predicate-bounded recursion, and Pascal is out of the oracle until the
   tree/AllocCtx revival. Building it now would violate
   frontends-are-the-oracle; revisit when Pascal returns.
