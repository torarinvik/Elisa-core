# 104 — EASM as a Typed Assembly Language

## Why

EASM is, in substance, a flow-sensitive abstract-machine-state checker — which is exactly what a
Typed Assembly Language (TAL: Morrisett/Walker/Crary/Glew) type-checker *is*. We've built it as a
bag of checks rather than a declared type system. The goal of this document is to (a) make that
correspondence explicit, (b) close the gaps where the tracking is partial or local, and (c)
re-found the checker on an explicit typing-context discipline so the result is a *soundness
property* rather than a checklist. "Maximum rigor" = push every guarantee as far toward static,
all-inputs proof as it will go.

## The rigor ladder

1. **Structural** (done) — signatures, clobbers, frame conditions, capabilities.
2. **Typed / TAL** — machine-state safety proven for all inputs: no stuck states, memory/control/
   stack well-typed, ABI preserved. *This document.*
3. **Symbolic equivalence** — prove `reference ≡ target` by SMT over the typed machine semantics
   (static, all-inputs, decidable on a subset). IMPLEMENTED (off by default,
   `ELISA_EASM_LOCKSTEP_SYMBOLIC=1`): both bodies are encoded as bit-precise 64-bit bit-vector terms
   and z3 is asked whether any input makes a declared output register differ. `unsat` ⇒ equivalent for
   every input — strictly stronger than the rung-4 fuzz agreement (e.g. it proves `a+b ≡ b+a` outright,
   which sampling can only fail to refute). Scope is the decidable straight-line GPR + 64-bit-ALU
   subset (movq/addq/subq/andq/xorq/incq/decq, terminated by ret); anything outside it (memory,
   control flow, sub-register writes, segment/stack effects) is reported `lockstep-symbolic-skip`,
   never passed. `sat` ⇒ `lockstep-symbolic-divergence`; solver `unknown` ⇒ decline, never a proof.
4. **Lockstep fuzz** (docs/103 stage 3c) — dynamic equivalence on sampled inputs. The initial
   implementation is off by default (`ELISA_EASM_LOCKSTEP_ORACLE=1`) and only runs for gated x86_64
   safe leaves; it assembles `reference` and `target`, executes deterministic fuzz vectors, and diffs
   observed `rax`/buffer state. A skipped gate is reported explicitly and is not a pass. This is
   coverage-bounded agreement, the residual discharge for whatever tier 3 cannot prove.

Lockstep is the bottom rung (coverage-bounded). Tiers 2–3 should carry as much as possible.

## The correspondence (we are already ~60% of a TAL)

| TAL concept | What EASM already has |
|---|---|
| Register file type Γ at each program point | `machineFactState` (`LiveRegs`, `KnownUInt`, `FS`, `StackMod16`) |
| Typed pointers; typed load/store | `HostPtr[T]`/`GuestVAddr[T]` carriers + `raw-memory-base` rejection |
| Code labels carry types (preconditions) | `LabelContract` preconditions (`fs:guest, rsp:mod16=8`) |
| Stack types (STAL) | stack discipline (`stack: unchanged/synthetic`, `stackMod16`) |
| Definite initialization | `register-read-uninitialized` |
| Callee-saved discipline | `preserves: callee_saved` + `callee-saved-not-preserved` |
| Well-typed ⇒ doesn't get stuck | per-op capability/clobber/effect checks |

The move to full TAL is therefore not a rewrite — it is closing gaps and re-founding on
`Γ ⊢ instr ⇒ Γ'`.

## Upgrade ladder (by rigor-per-effort)

1. **Typed memory layouts — highest leverage; in progress.** A carrier types not just the address
   space and element (`HostPtr[u32]`) but the *record shape behind the pointer*. With a declared
   `layout`, an access `48(%r14)` is checked to hit a valid, correctly-typed, correctly-sized
   field — not "some memory." This directly retires the class we bled on: unrelocated
   `proc_param`/`libcparam` fields, garbage flexible size, AoS-slot over-read, the raw-offset
   `movq 48(%r14), %r13` boot reads. It extends the existing carrier machinery rather than adding a
   new system.
2. **Explicit Γ + one typing rule per opcode + correct joins.** Reframe `machineFactState` as a
   typing context with a declared transition relation, and add a proper dataflow join at
   control-flow merges (every predecessor must establish a label's precondition). The soundness
   backbone.
3. **Register-polymorphic calling conventions.** Express "preserves all callee-saved" as type
   variables — a routine's type is `∀ρ. (args; ρ) → (ret; ρ)` — so preservation holds by
   parametricity and catches the save-but-restore-wrong-value case a presence checklist misses.
   IMPLEMENTED (off by default, `ELISA_EASM_PRESERVE_PARAMETRIC=1`): each preserved callee-saved
   register's entry value is a symbol `ρ`; the body is symbolically executed over a modeled stack
   (push stores the source term at a decremented rsp offset, pop loads whatever term sits there), and
   z3 is asked whether any input makes the register's exit term differ from `ρ`. `unsat` ⇒ preserved
   for all inputs; `sat` ⇒ `callee-saved-value-unrestored`. This catches interleaved-stack
   misalignment (`push r12; push rdi; pop r12; pop rdi` leaves r12 holding rdi's value) and wrong-slot
   restores that the structural push/pop-ordering check (`calleeSavedPushPopProven`) passes. Modeled
   subset: straight-line push/pop/movq/ALU + ABI-respecting `call`; anything else ⇒
   `preserve-parametric-skip`, never a silent pass.
4. **Existential types for handles.** `*_from_handle` is `∃α. α`; existential typing makes
   "use a handle as the wrong concrete type" unrepresentable. Same consolidation as the
   `handle_as[T]` scatter cluster.

## Maximum rigor: a soundness property

The difference between a bag of checks and a type system is the theorem *well-typed ⇒ can't get
stuck*. Reachable without a proof assistant:

- **Totality of the transition relation:** every allowed opcode has a typing rule; no instruction
  mutates machine state ad-hoc. Auditable today; closes "unknown instruction effect" gaps.
- **Property-test the checker against the relation:** fuzz instruction sequences, run them through
  the abstract `Γ ⊢ instr ⇒ Γ'` and a concrete machine model, assert preservation. The existing
  `@property` muscle pointed at the type system itself.
- **Optional ceiling:** mechanize the ~20-instruction core (Γ, transition, preservation+progress)
  in a proof assistant, as TALx86 did. Our instruction set is far smaller, so it is tractable.

## Status

- Increment 1 (typed memory layouts: `layout` declarations + field offset/width checking on
  layout-typed carriers) — DONE in the `easm` package with tests.
- Emit lowering for layout-typed carriers — DONE: `HostPtr[L]` lowers as a host pointer to bytes,
  ABI-identical to `HostPtr[void]` (the layout is a verifier-only artifact with no ABI).
- First live adoption — DONE: the emulator's `CallGuestInitEntryOnStackWithFsAsm` ctx parameter is
  retyped `HostPtr[GuestInitCtx]`, so its seven raw `N(%r14)` field reads are now type-checked
  (verified clean; a corrupted offset is rejected — the check has teeth). Zero boot risk by the
  ABI-identity above.
- Boot-routine sweep — DONE: the guest main-entry parameter block is declared as `layout
  GuestMainParams` (argc@0/argv@8/entry_addr@272, the authoritative `GuestEntryParams` shape from
  core/guest_exec.elisa), and `params` is retyped `HostPtr[GuestMainParams]` across all six entry
  routines (`EnterMainEntryAsm`, `CallMainEntryAsm`, and the four `JumpMainEntry*` variants). Their
  raw `0/8/272(%base)` reads are now field-checked (verified clean; a corrupted `272→280` offset is
  rejected). Same ABI-identity zero-risk argument as the init-ctx adoption.
- Increment 2, brick 1 (declared transition relation + totality) — DONE: the per-opcode effect
  fragments (capability, flag-write, implicit reads/clobbers, result-defines) are consolidated into
  one declared `opRules` table (`easm_oprules.go`). Two theorems anchor it: **totality** (every
  `allowedOps` entry has a row, plus a runtime `opcode-rule-missing` guard if an allowed op ever
  reaches the walker ruleless) and **consistency** (each row agrees field-for-field with the legacy
  predicate functions, which are themselves cross-checked against LLVM MC). The relation is now a
  declared, auditable artifact rather than an emergent property of the 300-line switch — the stable
  base the remaining increment-2 work rests on.
- Increment 2, brick 2 (dataflow joins at control-flow merges) — DONE: the verifier walks the body
  linearly with one machine state, which is unsound at a merge (a label that is a jump target),
  because the walk only carries the textual-predecessor state and ignores the jump-in edges. The walk
  now records a state snapshot at every jump site and every label entry; `checkMergeConsistency`
  then enforces the join — a register *demanded* after a contract-less merge label (read before being
  rewritten in the block it heads) must be established on EVERY incoming edge (the meet of predecessor
  live-sets), plus the analogous FS-segment-state check. The meet also covers stack `rsp mod 16`
  alignment when the merge-headed block reaches a call or indirect control transfer, and known
  concrete register values when the block consumes them as indirect control targets. The fix an
  author applies is to declare a `labels:` contract, which types the merge and is already enforced
  per-edge (so contract labels are exempt). Additive: it only emits new `merge-state-unsound` /
  `merge-fs-state-unsound` / `merge-stack-alignment-unsound` / `merge-known-value-unsound`
  diagnostics for genuine divergences; the whole existing corpus (incl. the `done:`/`loop:` label
  tests) and the emulator stay clean. Demand-driven scan = no false positives on dead carried
  registers.
- Increment 2, brick 3 (walker Γ mutation driven by `opRules`) — DONE: the walker no longer keeps
  parallel per-opcode effect facts in ad-hoc predicate switches or a separate capability map. Its
  capability, flag-clobber, implicit-read, implicit-clobber, and implicit-result-definition queries
  are thin readers of `opRules`, so the declared transition relation is the enforced state-dynamics
  source of truth.
- Next within increment 2: factor the remaining structural stack/control special cases into named
  transition-rule fragments where useful. Then the symbolic/lockstep equivalence tier (docs/103
  stage 3c).
