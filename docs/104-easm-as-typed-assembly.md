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
   (static, all-inputs, decidable on a subset).
4. **Lockstep fuzz** (docs/103 stage 3c) — dynamic equivalence on sampled inputs; the residual
   discharge for whatever tier 3 cannot prove.

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
- Next within increment 2: per-opcode *state-transition* rules (drive the walker's Γ mutation off
  the table rather than the ad-hoc switch) and dataflow joins at control-flow merges (every
  predecessor must establish a label's precondition; post-label state is the meet). Then the
  symbolic/lockstep equivalence tier (docs/103 stage 3c).
