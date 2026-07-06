# docs/120 — Declared `lmut` threading and multi-place assignment

Status: §1 (multi-place `<-`), §2 (declared threading, erased), §3 (rebind call sites + must-use) LANDED in stage0. §4: stage1 already parses all three forms (named tuple types, lmut prefix, rebind — no new work needed). §5 dogfood DONE — verdict below.

## §5 dogfood verdict (stage1 parser, cf293e9)

Converted: `decl()` (value+thread form; 1 call site) and `parse_decl_unit()` (void
form; 5 call sites incl. recursion). Both read well — the rebind line at the
boundary is a genuine manifest, and `return parser` at each early exit makes the
thread-back points visible.

Evaluated and REJECTED: `stmt()` — its 7 call sites live in EXPRESSION position
(`if parser.stmt() is s:`, `elif … is`, two bare discards). The rebind form forces
condition restructuring and unused bindings there. Rule of thumb for the style
guide: **declare threading for statement-position boundaries whose results are
bound; keep expression-position helpers silent-lmut.** Also: the postfix
`return if COND` guard cannot thread (its desugared return is bare) — a declaring
function spells such exits as explicit `if COND: return <thread>`.

Dogfood also hardened §2 itself: returns nested in match-EXPRESSION arms carry the
obligation (generic walk, lambdas excluded), and multi-pattern arms share a body so
the rewrite is once-per-node (9f2c7311).

## §6 — Thread slots: the manifest as dataflow (LANDED)

The user-driven extension of §1: block-level mutation manifests without capture
headers. In a multi-place `<-`, a slot naming its own target — the bare binding, or
a (chained) mutating call on it — is a THREAD SLOT:

	parser, out <-
	    if d_opt is d:
	        parser, out.push(d)
	    else:
	        parser.advance(), out

A mutating call "yields" its receiver notationally (the §2 erased-return model
extended from function returns to expression slots): the effect executes in place,
the slot erases, no assignment is emitted — zero overhead with value-semantics
reading. The target list is the mutation manifest; the branches are dataflow.
Slots whose root is not their target stay VALUE slots with §1 simultaneous
semantics, and both kinds mix freely (`p, count <- p.advance(), count + 1`).

Rules:
- **Arm consistency**: every branch must classify slot i the same way. The bare
  target binding is NEUTRAL (thread-erase ≡ self-assign no-op) and matches either.
- **No cross-reads**: a value slot may not be rooted at a binding a thread slot of
  the same assignment mutates — bind it before the `<-`.
- **All-thread form**: when every slot threads, nothing remains to bind and the
  construct desugars to a plain statement-if (E4 does not apply at all).
- **Mixed form**: thread effects hoist into the branch blocks as statements; the
  thread targets join those blocks' `Captures`, making "the target list is the
  capture header" literal (E10 still validates each is a mutable binding).

Implementation: parser/thread_slots.go (classification, cross-read guard, the
statement-form and mixed-form desugars), hooked from both multi-place entry points
(the all-bare-ident tuple-reassign path and the place-list path). Goldens:
src/thread_slots_runtime_test.go. Dogfooded: stage1's parse_decl_unit runs the
flagship form in production parsing (self-resolve 0, 28/28 smokes).

Still open (§7 candidate): the strictness tier — in a declaring function (or under
an opt-in flag), any outer-state mutation not at a manifest point (`<-`, rebind,
claimed call, thread slot, capture) becomes an error steering to these forms.

Implementation notes vs the original design:
- Return-tuple fields are NAMED (`(ch: char, lexer: lmut Lexer)`) — tuple types in
  Elisa require field names, and the name doubles as the param match.
- §3 call sites work by CLAIMS: the parser drops a bare rebind target that names an
  argument/receiver root of a direct-call RHS and records it as an LmutRebindClaim
  on the CallExpr; the semantic layer validates claims against the callee's
  LmutThreadSlots in both directions (must-use + no-false-claims), which makes the
  syntactic matching sound — a wrong guess is rejected, never misbound.

## Motivation

docs/119 established `rebind` (explicit mutation threading for bindings) and the lmut
work established `lmut T` (linear-mutable parameter mode, codegen-identical to
`mutable T&`, checked for call-site non-aliasing). This doc completes the style the
language wants to encourage — mutation that is explicit and trackable at the
granularity where explicitness pays:

```
def advance_char(lexer: lmut Lexer) -> (char, lmut Lexer):
    if lexer.is_eof():
        NULL_CHAR, lexer
    else:
        ch: char = lexer.current_char()
        lexer.position <- lexer.position + 1
        lexer.line, lexer.column <-
            if ch == '\n':
                lexer.line + 1, 1
            else:
                lexer.line, lexer.column + 1
        ch, lexer

rebind character, lexer = lexer.advance_char()
```

Three pieces, in dependency order.

## §1 — Multi-place assignment (LANDED)

```
place1, place2 <- <tuple-valued expr>
```

Several PLACES (field paths, index targets, locals — anything `<-` already accepts)
updated from one tuple-valued RHS, typically a value-if. The branches stay pure
values; mutation happens at exactly one visible `<-`. The sibling of `rebind` for
places rather than bindings.

Semantics:
- **Simultaneous assignment**: the RHS is fully evaluated before any place is
  written, so `a.x, a.y <- a.y, a.x` is a correct swap.
- Every existing `<-` check (mutability, struct invariants, named-state transitions,
  E4 value-block rules) applies to each individual place unchanged.

Implementation (pure parser desugar, mirrors `parseRebindStmt`): the RHS binds to
fresh temps via the docs/119 §2 temp-tuple bind; one `place <- temp` per target rides
`pendingStmts`. Disambiguation: a comma after a statement-leading expression is a
place separator ONLY when a top-level `<-` follows on the same line
(`multiPlaceArrowAhead`, a pure token scan) — a bare `expr, expr` line remains a
value block's tuple tail (which rebind/if-value branches yield; the first
implementation hijacked those and broke every tuple tail inside value blocks).

Files: `parser/rebind.go` (`parseMultiPlaceAssignStmt`, `multiPlaceArrowAhead`),
`parser/parser_statements_...` (TOKEN_COMMA case in `parseExprOrAssignStmt`).
Goldens: `src/multi_place_assign_runtime_test.go`.

## §2 — Declared threading: `lmut` in return position (DESIGN)

```
def advance_char(lexer: lmut Lexer) -> (char, lmut Lexer):
```

**The `lmut T` return slot is notation, not a value slot.** It is a declared thread:
the checker enforces it, codegen erases it. The emitted function returns only `char`
and takes the lexer as the same exclusive mutable reference as today. Rationale:
a real tuple return would copy a darray-owning struct (or demand the affine
move-in-place lowering on every return path) and change the ABI — pure ceremony
bought with real cost, violating the zero-overhead principle. Erasure buys the
manifest for free; the equivalence is already empirically pinned (lmut ≡ mutable&
byte-for-byte).

Checker rules (what makes the manifest TRUE rather than decorative):
1. Each `lmut T` return entry must name-match an `lmut` parameter (by type; with two
   same-typed lmut params, by position).
2. Every return path must thread every declared-lmut param exactly once, in tail
   position (`ch, lexer` / `NULL_CHAR, lexer`). Anything else in that slot is an
   error.
3. The threaded name must be the parameter itself — not a copy, not a field.

## §3 — `rebind` call sites for declared-threading functions (DESIGN)

```
rebind character, lexer = lexer.advance_char()
```

- Rebind targets match the return tuple POSITIONALLY (value slots and lmut slots in
  declared order). The lmut-slot target must be **the same binding** passed as the
  lmut argument — receiving into a different name would be a real move and break the
  erasure. Desugar: the lmut slots erase; the remaining value slots bind exactly as
  today's `rebind`; the call itself is an ordinary lmut call.
- **Must-use**: if a function DECLARES the explicit form, a direct call must use the
  `rebind` form (an ordinary call-and-ignore is an error). This is the trackability
  payoff: the signature opts the function into manifest-required.

## Tiering (unchanged from the lmut design)

Ceremony is opt-in per function, preserving the two-tier story:
- `def advance(lx: lmut Lexer) -> char` — silent thread, hot path (188 call sites),
  call sites unchanged.
- `def parse_statement(p: lmut Parser) -> (Stmt, lmut Parser)` — declared thread,
  coarse boundary, call sites must `rebind`.

Same mode, same codegen; one bit of signature ceremony chooses visibility.

## Rollout

1. §1 multi-place `<-` — DONE (stage0).
2. §2 erased return notation + threading checker (stage0).
3. §3 rebind call-site form + must-use rule (stage0).
4. Port §1–§3 syntax to stage1 (lexer/parser; checker where feasible).
5. Dogfood: flip a handful of coarse Parser boundaries (`parse_statement`,
   `parse_decl`) to the declared form; keep `advance`/`accept`/`expect` silent;
   evaluate readability on real code before writing the style guide.
