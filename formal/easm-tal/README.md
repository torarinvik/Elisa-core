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
| `absstate` record: `aregs : reg -> bool` + `aflags : bool` + `amsize : nat` | `machineFactState.LiveRegs` (`compiler/src/easm/easm.go`), a finite map register→Defined, plus a flags-defined slot tracking `opRules.ClobbersFlags`, plus a guest-carrier known-minimum-size fact (the `SizeGuardFact` lower bound, `easm_guest_overlay.go`) |
| `Iread d off w` typing: `off + w <= amsize g` | `easm.CheckGuestOverlaySizeGuard` (`easm_guest_overlay.go`): a `requires size >= N` field read is discharged iff a dominating `SizeGuardFact` proves `size >= N`; else `overlay-field-needs-size-guard` |
| `Iguard n` / `arefine_size g n` (refine `amsize` to `max(amsize, N)`) | a dominating `if base.size[mem] >= N:` / early-return `if base.size[mem] < N: return` guard pushing a `SizeGuardFact{AtLeast: N}` (`applyOverlaySizeGuardForCondition` / `applyOverlayFallthroughGuard`) |
| `ameet` `amsize` = `Nat.min` | the conservative join: a size lower bound survives a merge only as strong as the weaker incoming edge |
| ALU ops define the flags slot (`adefine_cc`); `mov` preserves it (`adefine`) | `opRule.ClobbersFlags` (`easm_oprules.go`) / `instructionClobbersFlags` (`easm.go:3013`): add/sub/and/xor/inc/dec set it, mov does not |
| `adefine g d` (write defines dst) | `state.LiveRegs[canonical] = true` — *"instruction writes a defined result here"* (`easm.go:1278`) |
| `op_ok g (OReg r) = g r` must hold to read | the `register-read-uninitialized` check (`easm.go:1212–1215`): a read register must be in `LiveRegs` (or preserved) |
| `has_type` rules per opcode (mov/add/sub/and/xor/inc/dec) | per-op effect signature in `opRules` (`compiler/src/easm/easm_oprules.go`): which registers are implicitly read, which destination is a defined result |
| read-modify-write reads dst (`g d = true` premise on add/sub/and/xor/inc/dec) | `implicitUses` / operand-read tracking for ALU ops in the verifier walk |
| `seq_type` (list induction) | the linear body walk in `verifyFunction` carrying one `machineFactState` |
| reading Undefined = no typing rule applies = STUCK | the verifier emits an error and refuses the block |

The flags slot (`aflags`) is the first widening from docs/106's path: it tracks
EFLAGS definedness exactly as `opRules.ClobbersFlags` declares — ALU ops define
it, mov preserves it — and `preservation` shows that abstract fact is sound
w.r.t. the concrete machine's physical flags. The remaining omitted lattice
components (`KnownUInt`, `FS`, `StackMod16`) are orthogonal to the definite-init
safety theorem and are deliberately not modeled — see below.

**This correspondence is machine-tested, not just asserted.** `compiler/src/easm/easm_coq_fidelity_test.go`
re-implements the *exact* Coq relation in Go (`coqOpOk`/`coqHasType`/`coqAdefine`/`coqSeqType`,
transcribed line-for-line from `EasmTAL.v` with citations) and fuzzes 6000 straight-line bodies over
the modeled subset against the real verifier (`Parse`). It asserts the two halves of the
correspondence continuously: (1) the Coq relation gets **stuck** (a read with no applicable typing
rule) **iff** the verifier emits `register-read-uninitialized` — the *progress* correspondence; and
(2) on acceptance the final Coq abstract state (which registers are Defined) **equals** the verifier's
`LiveRegs` over the modeled registers — the *preservation/state* correspondence. If the Go walk and
the Coq model ever drift on this subset, the test fails. The Go mirror also carries the extended
relation's flags slot (`coqState.flags`, defined by ALU ops, preserved by mov) and asserts its own
flags invariant on every body; flags definedness is *not* cross-checked against the verifier because
`machineFactState` exposes no flags-defined fact (it separately enforces only that flag-clobbering
ops list `cc`, the `cc-clobber-missing` check) and the modeled subset has no flag-reading op — so
there is no observable verifier state to compare against (documented in the test header).

A companion test, `compiler/src/easm/easm_coq_memstate_fidelity_test.go`, cross-checks the docs/107
**guest-read** layer against its Go authority. The guest read is not part of the instruction walk the
first fuzzer drives — it is a separate accessor checker (`easm.CheckGuestOverlaySizeGuard`) — so this
test mirrors the Coq read rule directly: a set of dominating `SizeGuardFact`s induces the Coq known
minimum `amsize = max_i K_i`, and the `T_read` premise `off+w <= amsize` holds **iff** the Go checker
emits no `overlay-field-needs-size-guard`. It fuzzes 8000 fact-set/requirement pairs over that iff,
and 4000 more pinning the `Nat.min` merge join (a post-merge read is backed only by the weaker edge).
So the mechanized read rule and the real Go discharge stay in lockstep too.

(The
self-zeroing idiom `xorq/subq %r,%r`,
which the verifier defines without reading the dst — a value-tracking optimization outside the
definite-init RMW rule — is excluded from the fuzzer so the correspondence stays exact.)

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
- **Guest-memory state / guest-read safety** (`read_in_bounds`, `read_never_stuck`,
  `unguarded_read_rejected`) — the docs/107 typed guest-memory overlay. `amsize` is the carrier's
  known-MINIMUM runtime size; a guest field read `Iread d off w` is well-typed only when its byte
  span `[off, off+w)` is covered (`off+w <= amsize`), strengthened by a size guard `Iguard n`
  (`arefine_size`, `amsize := max(amsize, N)`). `read_in_bounds` proves a typed read never over-reads
  (`off+w <= rsize`); `unguarded_read_rejected` proves an unguarded read (`amsize < off+w`) has NO
  typing rule — the formal analogue of `easm.CheckGuestOverlaySizeGuard` discharging /rejecting a
  `requires size >= N` obligation. The merge join takes `Nat.min` of the two known minimums
  (`ameet_msize_lb_l/r`, `ameet_msize_glb`), the conservative size join. `seq_safety`/`no_stuck`
  carry a `guards_backed` hypothesis (every guard's branch is the runtime-taken one); `rsize` is
  step-invariant (`step_preserves_rsize`).

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
- **Flags (EFLAGS) definedness** as a lattice slot (`aflags`): ALU ops
  (add/sub/and/xor/inc/dec) DEFINE the flags slot, mov PRESERVES it — exactly
  mirroring `opRules.ClobbersFlags`. `alu_defines_flags` and
  `mov_preserves_flags` pin the abstract behavior; `preservation` shows it is
  sound w.r.t. the concrete machine's physical flags; the meet (`ameet`)
  extends pointwise to the flags slot (`ameet_flags_lb_l/r`, `ameet_flags_glb`).
- **Guest-memory state + guest-read safety** (docs/107) as a lattice slot (`amsize`, the carrier's
  known-minimum runtime size): a guest field read (`Iread`) is well-typed only when its byte span is
  covered by a guard-established minimum (`Iguard`/`arefine_size`). `read_in_bounds` /
  `unguarded_read_rejected` are the formal image of `easm.CheckGuestOverlaySizeGuard`; the meet takes
  `Nat.min` (`ameet_msize_lb_l/r`, `ameet_msize_glb`).
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
- **Flag-READING operations / flag VALUES.** The flags slot tracks *definedness*
  only (`aflags` / concrete `rflags : option unit`), because the modeled subset
  has no flag-reading op (no `jcc`/`setcc`/`cmov`). Conditional jumps are
  control transfers handled outside this straight-line core; when they enter the
  model, the flags slot is the precondition they will read. Concrete flag bits
  (CF/ZF/SF/…​) and their computed values are likewise not modeled — only whether
  the condition codes are established, which is the verifier-relevant fact.
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
2. **Flags as a lattice element:** *(done)* `absstate` and `rfile` carry a flags slot threaded
   through `has_type`/`step`; `models` gained a flags conjunct. Extending this further means
   adding a flag-reading op (e.g. `jcc`/`cmov`) whose typing rule requires `aflags g = true`.
3. **Control flow:** generalize `seq_type` to a labeled CFG and add a `join` (pointwise `&&`
   on `absstate`); prove the join is a lower bound and re-run the sequence argument per edge.
   This is exactly the Go-side `checkMergeConsistency` made provable.
4. **Value tracking (`KnownUInt`):** add an abstract-value lattice and a refined models
   relation; preservation then also shows known values stay consistent.
