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

## §7 — The linear model: mutation IS reassignment (DESIGN)

The unifying principle behind §1–§6, stated directly: an `lmut` value has **affine
value semantics at the source level and in-place mutation at the machine level.**

- Source level: the value threads through the code as a chain of reassignments —
  every mutation names the value on the left of a `<-`, so its whole transformation
  history is readable in the text. `parser <- … <- …`.
- Machine level: because the value is linear (one live binding, each reassignment
  targeting the same place), the compiler erases the "move" and mutates in place.
  Zero copies, proven byte-identical IR.

The bridging invariant: **nothing is mutated without being reassigned.** The only
ways to mutate an `lmut` value are `x.field <- v` (direct place write), or a call
that names it: `x <- x.method(…)` / `t, b <- f(…, t, …, b, …)` (§8). A bare
`x.method()` / `f(x)` that mutates `x` without reassigning it is the "silent
mutation" the design eliminates.

Earlier drafts (a §6 heuristic "flag mutations buried in asymmetric conditionals")
were abandoned: measured across the stage1 frontend they over-fired on legitimate
sequential multi-param code (974 → 322 → 60 sites, still mostly legitimate), because
"a conditional mutates two params" is normal imperative code, not a separable
antipattern. The linear model replaces the heuristic with a uniform rule — a
mutation is a reassignment or it is an error — which needs no conditional analysis.

## §8 — The arg-manifest form (LANDED)

The spelling that makes a mutating call a reassignment:

    x <- x.push(v)                       # single target
    table, bound <- walk(value, table, bound)   # multi target

A `<-` (reassign, not declare) whose RHS is a **void** call, where every target is a
mutable binding passed to the call (as an argument or the receiver). The `<-`
targets are a self-documenting manifest of what the call mutates — replacing a
"# mutates table, bound" comment — and the statement **erases** to just the call
(its references do the in-place mutation), so it is zero-overhead: proven to emit
byte-identical IR to the bare call.

Recognized in the semantic pass (analyzer_value_block_e4.go `isLmutArgManifest` /
`namesAreMutableArgRoots`): the void-return check disambiguates a manifest
(`bound <- bound.push(v)`) from an ordinary value-reassign
(`variants <- parser.enum_variant_block()`), and from a real tuple destructure
(`a, b <- swap(a, b)`). The recognized statement is marked `ArgManifest` on the
TupleBindStmt / AssignStmt; codegen and the interpreter emit only the call.
Goldens: src/lmut_arg_manifest_runtime_test.go.

Next: §9 — the uniform enforcement (a bare `lmut`-mutating call/pass is an error
steering to the §8 reassignment), landed together with the frontend conversion so
the tree stays green.

## §9 — Loop-expression threading (LANDED — composes from existing machinery)

A loop is itself a link in the linear dataflow chain. A `while cond |p, …|:` capture
header (docs/119 §6, already implemented) threads the outer `lmut` bindings through
the loop: each iteration's §8 arg-manifest reassignment mutates them in place, and the
header writes the final state back to the outer binding.

    def skip_ws(p: lmut Parser) -> void:
        while p.more() |p|:
            p <- p.advance()          # §8 arg-manifest, licensed by the |p| capture

    def collect(p: lmut Parser, items: lmut List) -> void:
        while p.more() |p, items|:
            p <- p.advance()
            items <- items.push(p.pos)

    # caller manifests the whole call (§8):
    p <- skip_ws(p)
    p, items <- collect(p, items)

No new machinery: the loop capture header (docs/119 §6) + the §8 arg-manifest
reassignment + the §8 caller manifest compose to give full loop-level threading, all
in-place and zero-overhead. Verified running: src/lmut_loop_thread_runtime_test.go.

Spelling notes vs the original sketch (`while cond |p| -> p:` returning `p`):
- Elisa function bodies require an explicit `return` (no bare trailing-expression
  return — `def f() -> i64: 5` is an error), and the loop-expression *value* form binds
  via `x =` NEWLINE INDENT `loop` (docs/119 §3), not an inline `= while …` / `return
  while …`. So the value/return-threading variant is spelled with the block-RHS binding.
- The simplest form (used above) is a **void** function that threads in place via the
  capture; the caller manifests with `p <- f(p)`. This avoids a declared-threading
  return entirely and is the recommended shape.
- Remaining polish (not blocking): claiming a declared-threading call inside a loop body
  (`item, p <- p.next()`) still needs the `rebind` spelling (§3), and the inline
  `= while`/`return while` sugar is unimplemented (the block-RHS form is the spelling).
