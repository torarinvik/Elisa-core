# Compiler fixes completed this session

Five real defects, each with a reduced repro in this directory. None could be
committed: git could not read `Go projects/Elisa-core/.git/config` (EPERM) for the
whole second half of the session. All edits are on disk.

## 1. stage0: an explicit target triple inherited the HOST's os/arch
`compiler/src/semantic/target_consts.go` (+ `target_triple_test.go`, NEW)

`-target-triple wasm32-unknown-wasi` compiled as **macOS/arm64**, verified by
reading `static if` answers out of a running wasm instance. Unrecognized triple
components fell back to `runtime.GOOS`/`GOARCH`. `ARENA_BACKEND_WASM_HEAPBASE` was
unreachable dead code as a result, and `riscv64-...` silently got the host arch.

Rule now: the host is a default only when NOTHING was specified.
Regression tests included, with the empty-triple case kept working.
See `nw_wasm32_target_triple_falls_back_to_host.md`.

## 2. runtime: reserve_commit's 256 MiB reservation is not free on wasm
`compiler/runtime/elisacore_std/arena.elisa` (+ regenerated `collections.elisai`)

Documented as costing "address space, not physical memory" -- true with demand
paging, false in linear memory, where `memory.grow` commits immediately. One
`darray` cost 128 MiB; a browser frame grew the heap ~134 MB per call until
`memory.grow` failed.

The reservation is now keyed on `size_of(uintptr)`: 256 MiB on 64-bit, 8 MiB on
32-bit, via `arena_default_reserve_slots()`.

I got this wrong the first time and it is worth recording why. The first version
used `static if ELISA_TARGET_ARCH_WASM`, a predicate added in #1. That made the
CANONICAL RUNTIME require a compiler new enough to define that name -- and the
runtime is compiled by every tool in the tree, including ones pinned to
`$HOME/.elisac/elisac`, which is the OTHER AGENT'S stage0 build. It broke them
with "static if condition must be a compile-time bool". Pointer width is the
property that actually matters, it folds at compile time on every backend, and it
adds no compiler-version coupling at all.

MEASURED, by exporting the helper and reading it back from each target:

    wasm32 : uintptr=4B  reserve=2097152 slots  =   8 MiB
    native : uintptr=8B  reserve=33554432 slots = 256 MiB

and the older shared stage0 compiles the reworked runtime again. The wasm
display-list reduction still decodes correctly after the rework
(pool = "Dual N-BackN =Trialframe-end", step counts 0,11,14,19,19,28).

## 3. stage0: SIGSEGV comparing a `mutable sview&` to a string literal
`compiler/src/backend/llvm_exprs_emitspecializedarenafromviewcall_to_emitunaryexpr.go`

`emitStringViewStaticLiteralEqual` called `LLVMBuildExtractValue` on what is a
POINTER through a ref, and LLVM died inside cgo -- a compiler crash, not a
diagnostic. The correct helper (`emitStringCompareOperandValue`) already existed
and just was not used here. Found by writing `if found == "":` over an out-param
while fixing #4. See `nw_stage0_sview_ref_literal_compare_crash.elisa`.

## 4. stage1: a REDECLARED extern resolved to the first declaration seen
`src/semantic/check_firm_arg_type_mismatch.elisa`

**This was blocking stage1's self-host fixpoint entirely.** stage1's own
`src/backend/llvm_c.elisa` declares `extern getenv(name: cstr)` while its vendored
`elisacore_std/debug_referee.elisa` declares `extern getenv(name: u8&)`. The
self-host build concatenates both, so gen2 died with 14 copies of
`argument 1 to "getenv" expects u8, got cstr` and gen3 == gen4 could never run.

An extern whose declarations disagree is now UNMODELED for this check rather than
pinned to whichever the walk reached first. See
`nw_stage1_redeclared_extern_signature.elisa`.

CORRECTION TO THE ORACLE USED: stage0 COMPILES that shape (exit 0, both
declaration orders), which is what the fix was measured against -- but the object
does not LINK. stage0 mangles the redeclared call as an overload
(`___ovl__getenv__cstr__getenv`) instead of binding the C symbol `getenv`. So
stage0 was only a compile-time oracle here, and there is a SEPARATE stage0 defect
in that shape, unfixed. The real evidence for the stage1 fix is the self-host
fixpoint below: it went from "cannot build gen2 at all" to byte-identical.

NOTE: this break PRE-DATED the session's other work -- `debug_referee.elisa` and
`elisac.elisa` were last modified 2026-08-25 16:41, hours before any edit here.

## 5. stage1: a f64 const with an EXPRESSION initializer read back as 0.0
`src/backend/codegen_struct_reg.elisa`, `codegen_expr_idents_binary.elisa`,
`codegen_declare.elisa`, `codegen_type_tables.elisa`, `llvm_c.elisa`, and both
StructTable literals.

`fold_consts` ran a FLOAT const through the INTEGER folder, marked it resolved, and
left its inlined source text empty -- so the use site emitted
`LLVMConstRealOfString("")`, which is 0.0, with no diagnostic. The most serious
defect in the set: a silent wrong answer that moved the n-back board's left edge
from x=286 to x=0 and was only caught by hand-decoding a display list.

Float consts now fold in the float domain (`fold_const_float_expr`), with leaves
parsed by LLVM itself (`ConstRealOfString` -> `ConstRealGetDouble`) so the bits
match a literal of the same text. Nine expression shapes now agree between the two
compilers: `W - S`, `(W - S) / 2.0`, `(W - S) * HALF`, `W * HALF`, `W + S`,
`W - S - S`, `-W`, `IW.f64() - S`, `((W-S)/2.0)+HALF`.

Hardened alongside it: `fold_const_atom` no longer returns `const_values` for a
const that is not integer/bool. "Resolved" never meant "has an integer value", and
returning 0 for a float or cstr const there is the same silent-zero failure.

## 6. stage1: `const cstr` globals declined every function that read them
Same files as #5, plus a `const_cstr_text` column.

`const GREETING: cstr = "..."` left the const unresolved, so passing it to a
function declined the whole unit. It now carries its literal text and the use site
materializes a global string, yielding the same pointer an inline literal emits.
This was the last thing keeping nw-core's `config` suite off stage1
(`STAGE0_ONLY` in scripts/check.sh). VERIFIED: stage1 now prints
"hello from a const global", matching stage0.

The first attempt did NOT work, and the reason is worth keeping: registration
early-returns when a const is already registered, refreshing only its TYPE. Const
registration runs REPEATEDLY (static-if selection re-registers as it iterates to a
fixpoint), and the first pass can see an unresolved annotation -- so the text
carrier recorded on that pass was empty and then frozen. The early-return path now
refreshes the text carriers for exactly the reason it already refreshed the type.

## 7. stage1: the target-predefine table did not grow every const column

`src/backend/codegen_struct_reg.elisa` (`register_target_consts`)

Two defects in one function, both surfaced by RUNNING THE PARITY GATE rather than
by reasoning:

  * It seeds ELISA_TARGET_* predefines by pushing to the const columns, and it was
    NOT pushing to the three columns added by #5 and #6. Every column is indexed
    by the same `const_index`, so a short column is an out-of-bounds read waiting
    for the first target predicate that reaches that branch. Both push sites now
    write all ten columns; verified by counting them.

  * It had no WASM predicates. They are now registered present-but-FALSE (stage1
    emits only for the host, so the VALUE is 0, but the name should exist for
    parity with stage0 after #1). NOTE: the runtime no longer DEPENDS on this --
    see the correction under #2 -- so this is parity, not a load-bearing fix.

## 8. Vendored runtime drift

stage1 vendors Elisa-core's stdlib under `elisacore_std/`, and #2 changed
`arena.elisa` on the canonical side only. `scripts/check_runtime_drift.sh` caught
it as the gate's very first check. `arena.elisa` and `collections.elisai` are
re-synced; the guard reports "vendored == canonical (31 files)".

This is why the parity gate matters as a gate: nothing else in the session would
have caught #7 or #8, and #7 in particular is the kind of latent out-of-bounds
that surfaces much later as an unrelated crash.

## 9. stage1: `-O1` was missing from the project.json reader

`src/driver/project.elisa` (`parse_opt_level`)

It accepted opt digits 0, 2 and 3 -- not 1. So a `project.json` carrying
`{"opt":"O1"}` was rejected by stage1 and accepted by stage0: the exact mirror of
the stage0 CLI gap this port already fixed when -O1 support was added. One line.

Found by the parity gate (`project_report_smoke.sh`, `err/badopt`, 87 passed /
4 failed, "exit status stage0=0 stage1=1").

## 9b. ...and its error MESSAGE still said O0, O2, or O3

Accepting `-O1` was only half of #9. `project.elisa:1610` still printed
"(expected O0, O2, or O3)" while stage0 printed "(expected O0, O1, O2, or O3)",
and project_report_smoke.sh compares the two messages VERBATIM. So the check kept
failing after the behaviour was fixed -- with a diff that was now purely
cosmetic, which is exactly the kind of failure that gets waved through.

Fixing the behaviour without fixing the text it advertises leaves the compiler
lying about what it accepts. Both now say the same thing, and the smoke reports
46 passed / 0 failed.

## 10. A test fixture that had quietly inverted

`test/parity/project_report_smoke.sh`

That same `badopt` fixture used `{"opt":"O1"}` as its example of an INVALID option.
Once -O1 became valid, the fixture was asserting that a VALID option must be
rejected -- so it was pinning the bug in #9 in place rather than catching it, and
only surfaced at all because the two compilers disagreed. Changed to "O9".

Worth noting as a class: adding a feature can silently invert a negative test.
Nothing flags a fixture whose premise has expired.

## 11. A test script broke on paths containing spaces

`test/parity/parser_ast_smoke.sh`

`$ELISACORE_BIN` was unquoted, so a compiler path with spaces word-split and the
script tried to execute `/Users/torarinvikbjarko/Documents/Coding` -- exit 126,
"Permission denied", reported by the gate as a 0-second FAIL. This repo's path has
spaces in three components, so the check could never have passed here. Quoted.

## 12. stage1: `\xHH` and `\uXXXX` string escapes were never decoded

`src/backend/codegen_env.elisa` (`decode_string_escapes`, plus new
`hex_digit_value` and `push_utf8`)

The decoder knew `\n \t \r \0 \\ \" \'` and treated everything else as unknown --
pushing the backslash verbatim and advancing one byte. So `"\x41\x42"` was EIGHT
characters instead of "AB".

This is the worst class of defect: not a decline, a SILENT WRONG ANSWER. The
program compiles on both compilers and they are simply fed different bytes, so a
fixture that spells a byte with `\xHH` disagrees across stages for reasons that
have nothing to do with the code under test.

It was the ONLY mismatch in the 145-program differential corpus (stage0=4,
stage1=14). Verified fixed across every escape form, dumped as raw bytes and
diffed against stage0: `\x` lower and upper hex, `\u` producing 1, 2 and 3 UTF-8
bytes, the classic escapes (unregressed), and a bare `%`. All IDENTICAL.

Malformed escapes keep their source text and clear `all_known` rather than
guessing a value.

## 13. The differential corpus was sweeping repro/

`test/parity/differential_corpus.sh`

`CORPUS_DIRS` includes all of `$ELISA_CORE`, so the sweep picked up
`$ELISA_CORE/repro/` -- 21 files with `def main`, most of them curated BECAUSE
stage1 disagrees with stage0. A ratchet whose premise is "stage1 matches stage0"
was being enforced over the one directory guaranteed to violate it, so every
repro filed broke the gate. That punishes filing repros.

Measured: of the 1 mismatch and 11 declines, ALL TWELVE were repro/ files, and
the mismatch was #12 above -- a bug this directory had documented since
2026-08-25 while nothing failed on it. `repro/` is now excluded; a repro that
declines is a filed bug, not a regression.

Judgement call, written up in repro/CORPUS_POLLUTION.md with the alternative
(keep it and re-baseline) and why it was not chosen.

## 14. stage1: field-chain type inference was EXPONENTIAL

`src/semantic/resolve_types_infer.elisa` (the `Expr.Field` arm of
`infer_expression_type`)

The arm walked `object` TWICE -- once via `infer_expression_type` at the top, and
again via `structural_type_id_of` below, which recurses as well. The second walk
is only reached when the first did NOT resolve to a Named struct, so a chain that
RESOLVES costs one walk per link and a chain that FAILS costs two: 2^n.

    links      before        after
       20       66 ms        13 ms
       25     1715 ms        13 ms
       30      ~25 s         13 ms
      200        --          15 ms
      400     ~2e91 s        20 ms

Flat, not merely faster -- the growth is gone. The internal oracle's own
`chain.elisa` (400 links) through parse_report: never finished -> 326 ms.

A prefix that is itself an unresolvable field access cannot resolve structurally
either, so the second walk is skipped for that case ALONE. Field access through a
reference parameter is an `Ident`, not a `Field`, and keeps the structural path.

THIS UNBLOCKED THE GATE'S LAST CHECK. `semantic_internal_diff` had never once
completed; it now reports **0/3178 mismatches (baseline 0)**.

NOT caused by this session -- the defect is in field-chain recovery and nothing
in the const/escape/extern/target work touches it. It became REACHABLE only
because fixing the stage0 sview crash (#3) let parse_report build at all. Before
that the check died at tool-build time, so it had been "green" by never running a
single row.

## Note on the parity gate's compiler pin

`test/breadth/run.sh` defaults to `ELISAC=$HOME/.elisac/elisac` -- the SHARED
stage0, not the one under test. With that binary the breadth baseline SIGSEGVs,
because the shared build predates fix #3 (the `mutable sview&` vs literal crash)
and `test/breadth/parse_report.elisa` contains that shape.

Run it against the compiler under test and it passes:

    ELISAC=$ELISA_CORE/compiler/bin/elisac-nw  test/breadth/run.sh --baseline ...
    breadth baseline OK: 355 files unchanged

So that FAIL was not a regression from this session's work -- it is the shared
binary hitting a stage0 crash this session FIXES. Worth making the runner honour
ELISACORE_BIN like the rest of the gate does, so it cannot silently test a
different compiler than the one being gated.
## Verified

  * stage0 `go test ./src/...`: ALL GREEN (src 363s, semantic 26s, and
    backend/easm/lexer/parser/smt/interpreter/frontendir).
  * The target-triple tests FAIL against the old behaviour exactly as claimed --
    negative control run explicitly, not assumed.
  * stage1 SELF-HOST FIXPOINT GREEN, twice:
        stage A OK: 4/4 (fixed blockers still fixed)
        stage B OK: gen2 compiled the compiler into gen3
        stage C OK: gen3.o == gen4.o byte-identical (FIXPOINT)
    Before #4 it could not build gen2 AT ALL, so the fixpoint had not been running.
  * stage1 FULL PARITY GATE: 187 ok / 1 FAIL, up from 185/3 when this started.
    The remaining FAIL was project_report_smoke, fixed by #9b and confirmed at
    46 passed / 0 failed against a side-built binary. One long check
    (semantic_internal_diff) was still running when this was written.
  * DIFFERENTIAL CORPUS: 145 programs, 122 match. Its 1 mismatch and 11 declines
    were ALL repro/ files -- see #12 and #13.
  * Float folding is BIT-IDENTICAL to stage0 over 0.1+0.2, 1.0/3.0, 1e300*10
    (overflow to inf), 1e-300/10 (underflow), 0.0-0.0, 1.0/3.0*3.0 -- compared as
    raw i64 bit patterns, not formatted text.
  * String escapes are BIT-IDENTICAL to stage0 over ten forms: \x lower/upper
    hex, \u producing 1/2/3 UTF-8 bytes, the classic escapes, and a bare `%`.
  * The arena reservation folds per pointer width, measured by exporting the
    helper: wasm32 uintptr=4B -> 8 MiB; native uintptr=8B -> 256 MiB. The older
    SHARED stage0 compiles the reworked runtime again.
  * All fixed repros re-run together on one binary and agree with stage0.

## Not fixed

`print(42)` still declines on stage1 (`print__i64`, `print__u32` monomorphizations).
Declines are down to 5 from the 40 recorded earlier, so the earlier generic work
helped, but the remaining ones are in the monomorphization path and were not traced
to a cause. It does not block the port -- the differential sweeps already avoid it.

