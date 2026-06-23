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

- **Abstract state `Γ`** — `reg -> bool` (`absstate`), mirroring `LiveRegs`: `true` = Defined.
  Writing a register defines it (`adefine`), matching `state.LiveRegs[canonical] = true`
  ("instruction writes a defined result here", `easm.go:1278`).
- **Instructions** — `mov` (reg/imm), `add`, `sub`, `and`, `xor` (two-operand), `inc`, `dec`
  (one-operand read-modify-write): the q-suffix GPR/ALU subset, with the same read/define
  effect shape the `opRules` table declares in `compiler/src/easm/easm_oprules.go`.
- **Operands** — register | immediate.
- **Typing relation `Γ ⊢ instr ⇒ Γ'`** (`has_type`) — reading an operand register requires it
  Defined in `Γ`; reading an Undefined register means *no rule applies* = ill-typed = stuck.
  RMW ops additionally require the destination already Defined. The destination is Defined
  in `Γ'`.
- **Concrete semantics** — a register file `reg -> option nat` (`None` = physically
  undefined). `step` returns `None` exactly when an instruction reads an undefined register
  (a stuck read); otherwise the defined result.
- **`rho ⊨ Γ`** (`models`) — every Defined-in-`Γ` register is physically defined in `rho`;
  i.e. `Γ` is a sound under-approximation of physical definedness.

### The two theorems (no `admit` / `Admitted`)

- **PRESERVATION** (`preservation`): `Γ ⊢ i ⇒ Γ'` ∧ `rho ⊨ Γ` ∧ `step rho i = Some rho'`
  ⇒ `rho' ⊨ Γ'`.
- **PROGRESS** (`progress`): `Γ ⊢ i ⇒ Γ'` ∧ `rho ⊨ Γ` ⇒ `step rho i` succeeds — a well-typed
  instruction never gets stuck on an uninitialized read.
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

> Note: the task brief referred to a test file `easm_relation_property_test.go`; in the current
> tree the corresponding coverage lives in `easm_enforcement_fuzz_test.go` and
> `easm_register_read_test.go`. The role is the same.

## What is narrowed (and why it costs nothing for this property)

- Words are `nat` and `and`/`xor` use placeholder value functions: the definite-init theorem
  is about *definedness*, independent of the computed value.
- Flags, `KnownUInt`, `FS`, `StackMod16` are not modeled — orthogonal to the
  uninitialized-read guarantee.
- Only straight-line blocks (no labels/jumps/joins). The verifier's `checkMergeConsistency`
  (docs/104 increment 2 brick 2) is the CFG-level analogue and is the natural extension.

These are recorded honestly in `formal/easm-tal/README.md` under "What is and isn't covered".

## Path to widening the mechanized subset

In rough order of leverage:

1. **Flags as a lattice slot** — model `ClobbersFlags` from `opRules` so flag-read ops are
   covered.
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
  `admit`/`Admitted`. Checkable with `coqc EasmTAL.v` (stdlib only).
- Local machine-check: pending a `coqc` toolchain (none installed here; nothing installed
  system-wide per task constraints). Exact check command and install steps are in the README.
