# 106 — A mechanized core of the EASM transition relation

## Why

docs/104 ("EASM as a Typed Assembly Language") names an *optional ceiling*: mechanize the
small instruction core — `Γ`, the transition relation, and the
**progress + preservation** metatheorems — in a proof assistant, "as TALx86 did." Our
instruction set is far smaller than x86, so this is tractable. This document records the
formalization that realizes that ceiling for the definite-initialization core.

The deliverable lives under `formal/easm-tal/` (`EasmTAL.v` + `README.md`). It is a
self-contained Coq development that does **not** touch the Go compiler; the Go verifier
remains the source of truth, and the Coq model is checked *against* it by correspondence.

## What is formalized

The verifier tracks, per program point, a finite map register → `{Undefined | Defined}` —
`machineFactState.LiveRegs` in `compiler/src/easm/easm.go`. The single most important safety
property riding on that map is: **a well-typed block never reads an uninitialized register**
(the `register-read-uninitialized` diagnostic, `easm.go:1212–1215`). That is precisely a
definite-initialization type system, and definite-init type systems have textbook
progress/preservation theorems. We mechanize exactly that slice.

### The model (faithful to the verifier)

- **Abstract state `Γ`** — a record `{ aregs : reg -> bool; aflags : bool }` (`absstate`).
  `aregs` mirrors `LiveRegs` (`true` = Defined); writing a register defines it (`adefine`),
  matching `state.LiveRegs[canonical] = true` ("instruction writes a defined result here",
  `easm.go:1278`). `aflags` is the **EFLAGS-defined slot** — the first widening from this
  document's path. It is DEFINED by ALU ops and PRESERVED by mov, mirroring
  `opRules.ClobbersFlags` (`easm_oprules.go` / `instructionClobbersFlags`, `easm.go:3013`):
  add/sub/and/xor/inc/dec carry `ClobbersFlags=true`, mov does not.
- **Instructions** — `mov` (reg/imm), `add`, `sub`, `and`, `xor` (two-operand), `inc`, `dec`
  (one-operand read-modify-write): the q-suffix GPR/ALU subset, with the same read/define
  effect shape the `opRules` table declares in `compiler/src/easm/easm_oprules.go`.
- **Operands** — register | immediate.
- **Typing relation `Γ ⊢ instr ⇒ Γ'`** (`has_type`) — reading an operand register requires it
  Defined in `Γ`; reading an Undefined register means *no rule applies* = ill-typed = stuck.
  RMW ops additionally require the destination already Defined. The destination is Defined
  in `Γ'`.
- **Concrete semantics** — a machine `{ rregs : reg -> option nat; rflags : option unit }`
  (`None` = physically undefined). `step` returns `None` exactly when an instruction reads an
  undefined register (a stuck read); otherwise the defined result. ALU ops set `rflags` to
  `Some tt` (flags now physically defined); mov leaves `rflags` untouched. The flags VALUE is
  `unit` because the modeled subset has no flag-reading op — only definedness is observable.
- **`rho ⊨ Γ`** (`models`) — every Defined-in-`Γ` register is physically defined in `rho`,
  AND if `aflags Γ = true` the machine's flags are physically defined; i.e. `Γ` is a sound
  under-approximation of physical definedness on both slots.

### The theorems (no `admit` / `Admitted`)

- **PRESERVATION** (`preservation`): `Γ ⊢ i ⇒ Γ'` ∧ `rho ⊨ Γ` ∧ `step rho i = Some rho'`
  ⇒ `rho' ⊨ Γ'`.
- **PROGRESS** (`progress`): `Γ ⊢ i ⇒ Γ'` ∧ `rho ⊨ Γ` ⇒ `step rho i` succeeds — a well-typed
  instruction never gets stuck on an uninitialized read.
- **FLAGS FAITHFULNESS** (`alu_defines_flags`, `mov_preserves_flags`): an ALU op defines the
  flags slot in the post-state (`aflags Γ' = true`) and mov preserves it
  (`aflags Γ' = aflags Γ`) — exactly what `opRules.ClobbersFlags` declares. `preservation`
  additionally shows this abstract flags fact is sound w.r.t. the machine's physical flags.
- **MERGE SOUNDNESS** (`merge_soundness`): typing a post-merge block under the meet `ameet g1 g2`
  (pointwise `&&`) of predecessor states is sound from a machine that arrived via *either*
  predecessor. `meet_demanded_on_all_preds` is the exact fact the Go `checkMergeConsistency` relies
  on (a register read after the merge is established on every incoming edge); `ameet_glb` shows the
  meet is the greatest lower bound (no information lost beyond what soundness forces).
- **Sequence lift** (`seq_safety`, `no_stuck`, `no_stuck_from_empty`): by list induction, a
  well-typed straight-line block run from a modeling state runs to completion and ends in a
  state modeling the final context. This is the headline *well-typed ⇒ can't get stuck*.

## Relationship to the property test and the declared relation

This sits at the top of docs/104's "maximum rigor" sub-ladder, above the two existing rungs:

1. **Totality + consistency of `opRules`** (docs/104 increment 2 brick 1, `easm_oprules.go`,
   `easm_oprules_test.go`): every allowed opcode has a declared rule and it agrees
   field-for-field with the legacy predicates (themselves cross-checked vs LLVM MC). This
   makes the *relation declared and auditable*.
2. **Property/fuzz testing the checker** (`easm_enforcement_fuzz_test.go`,
   `easm_register_read_test.go`): randomized instruction sequences exercise the abstract walk
   and assert the enforcement holds on sampled inputs. This is *all-sampled-inputs* evidence.
3. **Mechanized progress + preservation** (this document, `formal/easm-tal/EasmTAL.v`): the
   same property, but proven for *all* inputs once and for all. Where the fuzz test samples,
   the Coq proof quantifies universally.

The three are complementary: `opRules` declares the relation the verifier enforces, the fuzz
tests check that the *Go implementation* matches the relation on samples, and the Coq proof
shows the *relation itself* is sound (admits no stuck states). A drift between the Go walk and
`opRules` is caught by the consistency test; an unsoundness in the relation's design would be
caught here.

### Closing the Go↔Coq fidelity gap (the model *is* the verifier, tested)

A proof about a model is only as good as the model's fidelity to the code. Rather than leave the
correspondence table above as documentation, `compiler/src/easm/easm_coq_fidelity_test.go` makes it a
continuously-checked invariant. It re-implements the **exact** Coq relation in Go —
`coqOpOk`/`coqHasType`/`coqAdefine`/`coqSeqType`, transcribed line-for-line from `EasmTAL.v` with
per-definition citations — then fuzzes 6000 straight-line bodies over the modeled
mov/add/sub/and/xor/inc/dec subset through the *real* verifier (`Parse`) and asserts:

1. **Progress correspondence** — the Coq `seq_type` relation gets *stuck* (a read with no applicable
   typing rule) **iff** the verifier emits `register-read-uninitialized`.
2. **Preservation/state correspondence** — on acceptance, the final Coq abstract state (the set of
   Defined registers) **equals** the verifier's `LiveRegs` over the modeled registers.

If the Go walk and the mechanized model ever diverge on this subset, the test fails — a mutation
check (faulting the mirror so `add` no longer reads its destination) confirms it has teeth. The
self-zeroing idiom (`xorq/subq %r,%r`, which the verifier defines without reading the dst — a
value-tracking optimization *outside* the definite-init RMW rule the Coq model encodes) is excluded
from the fuzzer so the correspondence stays exact rather than spuriously diverging.

> Note: the original task brief referred to a test file `easm_relation_property_test.go`; that file
> now exists (sampled checker-vs-concrete-machine agreement), and `easm_coq_fidelity_test.go` adds the
> tighter checker-vs-*mechanized-relation* correspondence described here. Earlier sampled coverage also
> lives in `easm_enforcement_fuzz_test.go` and `easm_register_read_test.go`.

## What is narrowed (and why it costs nothing for this property)

- Words are `nat` and `and`/`xor` use placeholder value functions: the definite-init theorem
  is about *definedness*, independent of the computed value.
- Flags *definedness* IS now modeled (`aflags`); flag *values* and flag-reading ops are not —
  the modeled subset has no `jcc`/`setcc`/`cmov`, so only definedness is observable. `KnownUInt`,
  `FS`, `StackMod16` remain unmodeled — orthogonal to the uninitialized-read guarantee.
- Only straight-line blocks (no labels/jumps/joins). The verifier's `checkMergeConsistency`
  (docs/104 increment 2 brick 2) is the CFG-level analogue and is the natural extension.

These are recorded honestly in `formal/easm-tal/README.md` under "What is and isn't covered".

## Path to widening the mechanized subset

In rough order of leverage:

1. **Flags as a lattice slot** — *done.* `aflags` models `ClobbersFlags` from `opRules`: ALU
   ops define the flags slot, mov preserves it, the meet extends pointwise, and `preservation`
   shows it sound vs. the concrete flags (`alu_defines_flags` / `mov_preserves_flags`). The
   remaining step is a flag-READING op (`jcc`/`cmov`) whose rule requires `aflags Γ = true`.
2. **Control flow + dataflow join** — generalize the sequence relation to a labeled CFG with a
   pointwise-`&&` meet at merges; prove the join is a lower bound and lift progress/
   preservation per edge. This makes `checkMergeConsistency` a proven theorem rather than a
   diagnostic.
3. **Value tracking (`KnownUInt`)** — an abstract-value lattice with a refined models relation,
   so the known-value facts the verifier uses (e.g. non-canonical-address rejection) are also
   sound.
4. **Typed memory / layouts and capabilities** — the higher rungs of docs/104, each a separate
   formalization effort.

## Status

- `formal/easm-tal/EasmTAL.v` — model + `progress` + `preservation` + sequence safety, no
  `admit`/`Admitted`. Checked with `rocq compile EasmTAL.v` (stdlib only).
- Local machine-check: **DONE** — verified with The Rocq Prover 9.1.1 (`rocq compile EasmTAL.v`
  exits 0, silent, produces `EasmTAL.vo`). The definite-initialization soundness of the EASM core
  is now a machine-checked theorem, not only property-tested. Imports use the Rocq 9 `Stdlib.*`
  namespace; see the README for the toolchain note and the `make` target.
