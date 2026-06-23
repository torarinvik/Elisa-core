# EASM TAL core — mechanized progress + preservation + merge soundness

This directory contains a machine-checkable Coq formalization of the **core of the
EASM transition relation**: the straight-line GPR/ALU subset that the Go verifier
tracks for definite-initialization ("liveness"). It proves the two standard
type-safety metatheorems — **PROGRESS** and **PRESERVATION** — their lift to whole
instruction sequences, and **MERGE SOUNDNESS** (the dataflow join at control-flow
merges), so the soundness of the EASM definite-init check — including its merge
handling — is a machine-checked theorem rather than only a property-tested one.

- `EasmTAL.v` — the entire development: model + all theorems, no `admit`/`Admitted`/`sorry`.

## Correspondence to the Go verifier (the source of truth)

The Go verifier is authoritative; the Coq model is faithful to it, not the reverse.

| Coq (`EasmTAL.v`) | Go verifier |
|---|---|
| `absstate = reg -> bool` | `machineFactState.LiveRegs` (`compiler/src/easm/easm.go:133`), a finite map register→Defined |
| `adefine g d` (write defines dst) | `state.LiveRegs[canonical] = true` — *"instruction writes a defined result here"* (`easm.go:1278`) |
| `op_ok g (OReg r) = g r` must hold to read | the `register-read-uninitialized` check (`easm.go:1212–1215`): a read register must be in `LiveRegs` (or preserved) |
| `has_type` rules per opcode (mov/add/sub/and/xor/inc/dec) | per-op effect signature in `opRules` (`compiler/src/easm/easm_oprules.go`): which registers are implicitly read, which destination is a defined result |
| read-modify-write reads dst (`g d = true` premise on add/sub/and/xor/inc/dec) | `implicitUses` / operand-read tracking for ALU ops in the verifier walk |
| `seq_type` (list induction) | the linear body walk in `verifyFunction` carrying one `machineFactState` |
| reading Undefined = no typing rule applies = STUCK | the verifier emits an error and refuses the block |

The omitted lattice components (`KnownUInt`, `FS`, `StackMod16`) are orthogonal to
the definite-init safety theorem and are deliberately not modeled — see below.

## The two theorems

- **`progress`** — if `Gamma |- i => Gamma'` and `rho |= Gamma` (every Defined-in-Gamma
  register is physically defined in `rho`), then `step rho i` succeeds. A well-typed
  instruction never reads an undefined register (no stuck-by-uninitialized-read).
- **`preservation`** — if `Gamma |- i => Gamma'`, `rho |= Gamma`, and `step rho i = Some rho'`,
  then `rho' |= Gamma'`. Abstract definedness stays a sound under-approximation of physical
  definedness across a step.
- **`seq_safety`** / **`no_stuck`** / **`no_stuck_from_empty`** — the lift to sequences:
  a well-typed straight-line block run from a modeling state always runs to completion
  (`run` never returns `None`) and ends in a state modeling the final context. This is the
  headline *well-typed ⇒ can't get stuck*.
- **`merge_soundness`** (+ `ameet_lb_l/r`, `meet_demanded_on_all_preds`, `ameet_glb`,
  `models_meet_l/r`) — the dataflow join at control-flow merges. At a label reached from several
  predecessors, the verifier types the continuation under the **meet** (pointwise intersection) of
  the predecessor states. `merge_soundness` proves this is sound: from a concrete machine that
  arrived via *either* predecessor, the meet-typed continuation runs without getting stuck.
  `meet_demanded_on_all_preds` is the exact fact `checkMergeConsistency` relies on — a register the
  continuation reads (Defined in the meet) is established on **every** incoming edge; `ameet_glb`
  shows the meet loses no information soundness would let it keep. Mechanizes the soundness of the
  `merge-state-unsound` check (`compiler/src/easm/easm_oprules.go`).

## How to check the proofs

```sh
cd formal/easm-tal
rocq compile EasmTAL.v   # succeeds silently with no warnings/errors if the proofs check
# (equivalently, the compat binary: coqc EasmTAL.v)
```

The checker needs only the standard library (`Stdlib.Lists.List`, `Stdlib.Arith.PeanoNat`);
no mathlib / opam packages / external solvers.

### Local verification status

**Machine-checked** with **The Rocq Prover, version 9.1.1** (`rocq compile EasmTAL.v` exits 0,
silently, producing `EasmTAL.vo`). `progress`, `preservation`, their lift to straight-line
sequences (`seq_safety`, `no_stuck`), and `merge_soundness` (the dataflow join) are verified
theorems with no `admit`/`Admitted`/axioms.

Imports use the Rocq 9 `Stdlib.*` namespace. On Coq ≤ 8.x use `Coq.*` instead (the only change
needed); Rocq ≥ 9.0 is the supported toolchain. The proofs use only standard, robust tactics
(`inversion`, `destruct ... eqn:`, `discriminate`, `eauto`, list induction).

## What is and isn't covered

**Covered:**
- The definite-initialization lattice (`LiveRegs`) — the heart of the
  `register-read-uninitialized` guarantee.
- mov(reg/imm), add, sub, and, xor (two-operand), inc, dec (one-operand RMW) — the
  q-suffix GPR/ALU subset.
- Operand = register | immediate.
- Straight-line (acyclic, no labels/jumps) sequences via list induction.
- The **dataflow join at control-flow merges** (`merge_soundness`): typing a post-merge block
  under the meet of predecessor states is sound on every incoming edge — the soundness of
  `checkMergeConsistency`. (The full CFG/fixpoint is still future work; what is proven is the join's
  soundness, which is the load-bearing part.)
- All metatheorems with no axioms or `Admitted`.

**Not covered (deliberate narrowing — documented so it is honest):**
- **Concrete bit-width / exact ALU semantics.** Words are `nat`; `and`/`xor` use a
  placeholder binary function. The safety theorem is about *definedness*, which is
  independent of the computed value, so this loses nothing for the property proven.
- **Flags (EFLAGS).** add/sub/and/xor clobber flags in the real ISA and `opRules` records
  `ClobbersFlags`, but flags are not part of the definite-init lattice, so they are elided.
- **`KnownUInt` value tracking, `FS` segment state, `StackMod16`** — separate facts in
  `machineFactState`; orthogonal to progress/preservation for uninitialized reads.
- **Full control flow / labels / CFG fixpoint.** The *soundness of the merge join* is now proven
  (`merge_soundness`, see above), but the surrounding machinery — a control-flow graph, label
  contracts, and the iterate-to-fixpoint over back-edges — is still modeled only implicitly (the
  meet is taken as given two predecessor states). Modeling the CFG and proving the fixpoint
  terminates + is sound is the natural next step.
- **Capabilities, clobbers, frame conditions, calling-convention preservation.** These are
  other rungs of the rigor ladder (docs/104), not the definite-init core.

## How to extend it

1. **More opcodes:** add a constructor to `instr`, a `has_type` rule, and a `step` clause,
   then add the two new cases to `progress` and `preservation` (each is one `destruct` +
   `apply models_define`). The proofs are structured so new RMW/def-only ops are mechanical.
2. **Flags as a lattice element:** extend `absstate`/`rfile` to carry a flags slot and thread
   it through; the models relation gains a flags conjunct.
3. **Control flow:** generalize `seq_type` to a labeled CFG and add a `join` (pointwise `&&`
   on `absstate`); prove the join is a lower bound and re-run the sequence argument per edge.
   This is exactly the Go-side `checkMergeConsistency` made provable.
4. **Value tracking (`KnownUInt`):** add an abstract-value lattice and a refined models
   relation; preservation then also shows known values stay consistent.
