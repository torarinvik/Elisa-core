# 105 — The EASM safety membrane, end to end

## Frame

EASM is the checked membrane described in [103 — the Elisa↔EASM barrier](103-easm-barrier-three-levels.md):
each descent toward raw machine code forfeits one guarantee explicitly, while the remaining
guarantees are re-established by static checks. In the TAL framing from
[104 — EASM as a Typed Assembly Language](104-easm-as-typed-assembly.md), those checks are the
current concrete shape of a typing judgment over machine state: `Γ ⊢ instr ⇒ Γ'`.

This document is the verifier-side guarantee inventory. It is written for readers who already know
x86-64 assembly and need to know what EASM proves before an instruction stream is allowed through.
Every diagnostic code below was grep-verified in `compiler/src/easm` unless the section explicitly
says otherwise.

## Typed pointer carriers and memory-base provenance

Example: `movq (%rax), %rcx` where `inputs: addr = rax` and `addr: usize`.

Diagnostics: `raw-memory-base`; related role-carrier diagnostics: `raw-segment-selector`,
`raw-indirect-control-target`, `raw-stack-handoff`.

EASM tracks a memory base register back to the parameter that established it. The tracker is
`memoryBaseProvenance`: inputs seed register provenance, and `updateMemoryBaseProvenance` propagates
it through plain register moves and `lea`-derived bases. A raw scalar parameter such as `usize`,
`u64`, or another untyped integer may not become a dereferenced memory base unless the routine
declares the explicit escape hatch `memory.base.untyped`.

Proves: a dereference of a parameter-derived base is anchored in a typed address-space carrier such
as `HostPtr[T]`, `GuestVAddr[T]`, or `NativeMappedGuestPtr[T]`, and simple register shuffling does
not erase that obligation.

Does NOT prove: the concrete runtime address is mapped, non-null, in bounds, or still alive; it also
does not track arbitrary pointer arithmetic once provenance becomes too complex to attribute.

## Typed memory layouts

Example: `movq 48(%r14), %r13` where `%r14` comes from `ctx: HostPtr[GuestInitCtx]`.

Diagnostics: `layout-unknown-field`, `layout-field-width-mismatch`.

When a carrier's element type names a declared `layout`, `checkLayoutAccess` treats a constant
displacement as a field selection. The displacement must land on a declared field, and the
instruction width must match that field's declared width. The verifier-only layout type lowers to
the same ABI shape as an ordinary pointer; the check affects acceptance, not calling convention.

Proves: a simple constant-offset memory access through a layout-typed carrier names a real field of
the declared layout and uses the field at the declared width.

Does NOT prove: semantic invariants of field contents, aliasing between fields, or indexed/symbolic
addressing forms that are not a single static field access.

## Clobber, preserve, and initialization discipline

Example: `addq $1, %rbx` without `clobbers: rbx, flags` and without preserving `%rbx`.

Diagnostics: `register-write-without-clobber`, `cc-clobber-missing`,
`memory-write-without-clobber`, `memory-read-without-clobber`, `implicit-clobber-missing`,
`callee-saved-not-preserved`, `callee-saved-preservation-unproven`,
`register-read-uninitialized`, `implicit-read-uninitialized`, `caller-saved-use-after-call`.

Every explicit register write must be declared as an output or clobber. Memory reads and writes must
be covered by `clobbers: memory`, `memory.read`, or `memory.write` as appropriate. Flag-writing
opcodes require `cc` or `flags`. Implicit machine effects are checked through the opcode rule table:
for example, `cpuid` implicitly reads `%rax/%rcx` and writes result registers; `call` trashes
caller-saved registers and flags. Callee-saved registers need both a preservation contract and, for
returning functions, a proof pattern such as push/pop unless explicitly marked unchecked.

Proves: the declared register, flags, memory, caller-saved, and callee-saved footprint is a
conservative static account of the instruction stream, and registers consumed by explicit or
implicit reads have been established on the current path.

Does NOT prove: arbitrary value equality for preserved registers beyond the implemented push/pop
proof pattern unless the routine uses an unchecked escape hatch; full register-polymorphic calling
convention typing remains a future rung.

## Segment state

Example: `movw %ax, %fs` without `requires: x86_64.segment.write, x86_64.segment.fs`.

Diagnostics: `segment-access-intent-missing`, `segment-register-intent-missing`,
`segment-register-write-intent-missing`, `segment-register-write-without-memory-clobber`,
`segment-transfer-state-unknown`, `segment-register-return-without-lifetime-contract`,
`unsupported-state-assertion`.

Segment override uses, segment register reads, and segment register writes each require explicit
intent. Writing `%fs`/`%gs` also requires a memory clobber because it changes TLS-relative
addressing. After a segment write, control transfer or return requires the abstract segment state to
be re-established with a machine-state assertion such as `state fs: host` or `state fs: guest`, or
with a checked label precondition.

Proves: segment-sensitive code has declared intent, segment register writes are treated as
memory-affecting, and control does not leave a block after an `%fs/%gs` write while the verifier's
segment state is unknown.

Does NOT prove: the OS has installed the intended descriptor/base, or that a raw selector is
semantically valid unless carried by role types such as `GuestFsSelector`/`HostFsSelector` or an
unchecked selector escape hatch.

## Frame conditions: `changes:` and `reads:`

Example: `movq (%rsi), %rax` in a routine that declares `reads: src` but `%rsi` comes from `secret`.

Diagnostics: `frame-write-outside-changes`, `frame-read-outside-reads`, `frame-unknown-carrier`.

`checkFrameConditions` upgrades coarse `clobbers: memory` into per-carrier frame conditions. If a
routine declares `changes:` or `reads:`, every attributable memory access through a parameter-derived
carrier must be authorized by those clauses. A write must go through a carrier named in `changes:`;
a read must go through a carrier named in either `reads:` or `changes:`. Clause names are checked
against real parameters.

Proves: for memory operands whose base can be traced to a named parameter carrier, the access stays
inside the declared read/write frame.

Does NOT prove: byte-range bounds inside `dst[0..n]` style text today; unattributable scratch
pointers fall back to coarse memory clobber discipline.

## Label contracts and dataflow joins

Example: `jmp done` where `labels: done requires fs:guest, r12` but the current state has unknown
`fs` or no established `%r12`.

Diagnostics: `label-precondition-unsatisfied`; label-contract shape diagnostics include
`invalid-label-contract`, `duplicate-label-contract`, `label-contract-without-label`,
`empty-label-precondition`, `unsupported-label-precondition`; merge diagnostics include
`merge-state-unsound`, `merge-fs-state-unsound`, `merge-stack-alignment-unsound`,
`merge-known-value-unsound`.

Labels may carry preconditions over machine facts: currently `%fs` state, `%rsp mod 16`, and live
GPRs. Every branch into a contracted label must satisfy that label's type. For contract-less merge
labels, `checkMergeConsistency` computes whether the linear walk is relying on facts that are not
established on every incoming edge: live registers demanded after the label, `%fs` state, stack
alignment when needed, and known concrete indirect-control-target values.

Proves: a jump target is entered only under its declared machine-state preconditions, and untyped
merge points cannot silently inherit facts from only one predecessor when later code relies on them.

Does NOT prove: arbitrary control-flow invariants or path-sensitive value relations beyond the
implemented live-register, segment-state, stack-alignment, and known-value facts.

## Declared opcode transition relation and totality

Example: an opcode in `allowedOps` reaches the walker without a row in `opRules`.

Diagnostics: `opcode-rule-missing`; related rule-driven diagnostics include `missing-capability`,
`cc-clobber-missing`, `implicit-read-uninitialized`, and `implicit-clobber-missing`.

`opRules` is the declared per-opcode effect signature: required capability, flag writes, implicit
reads, implicit clobbers, and whether implicit clobbers are defined results. The verifier has a
runtime guard for rule gaps, and tests assert totality over `allowedOps` plus consistency with the
legacy effect predicates that are cross-checked against LLVM MC.

Proves: every accepted opcode has an auditable abstract effect signature, so the checker is not
silently mutating `Γ` through an unrecorded special case.

Does NOT prove: complete instruction semantics. Current rows describe safety-relevant effects, not
full arithmetic/data semantics; factoring remaining stack/control special cases into named
transition fragments is still future work.

## Lockstep structural certification

Example: `target X86_64 lockstep fast_ref:` when the only allowed spec block name is `reference`.

Diagnostics: `lockstep-unknown-reference`, `lockstep-missing-reference`, `lockstep-divergence`.

The parser accepts a `reference:` body and `target <arch> lockstep reference:` bodies. Structural
certification checks that a lockstep target refers to the routine's real `reference:` block, and that
the reference exists. Each sub-body is still verified by the full EASM machinery: clobbers, frame
conditions, capabilities, pointer provenance, and the rest of this document.

Proves: the optimized target is structurally tied to the intended reference block, neither the
reference nor the target bypasses static verification, and when the oracle runs successfully, sampled
reference/target executions do not diverge on the state the oracle checks.

Does NOT prove: all-inputs observational equivalence. The grep-verified `lockstep-divergence`
diagnostic is oracle/fuzz evidence, not a symbolic proof; skipped or out-of-model oracle cases still
fall back to structural certification plus independent static verification.

## Known gaps and future rungs

The next rigor rungs are the same ones called out in [104](104-easm-as-typed-assembly.md):

- symbolic equivalence: prove `reference ≡ target` statically over typed machine semantics for the
  decidable subset;
- dynamic lockstep oracle: broaden the sampled `@lockstep` execution check and its
  `lockstep-divergence` coverage for cases symbolic equivalence does not discharge;
- per-opcode dynamics refactor: continue moving stack/control mutations into named transition-rule
  fragments so `Γ ⊢ instr ⇒ Γ'` is the only state transition surface;
- register-polymorphic calling conventions: express callee-saved preservation by parametricity
  rather than mostly by named preservation checks and push/pop proof patterns;
- existential handles: make opaque handle use type-directed instead of relying on scattered
  role-name checks;
- richer memory facts: byte ranges, aliasing, and value invariants for frame/layout-typed memory.

The boundary today is therefore strong but deliberately named: EASM proves static safety facts about
typed carriers, declared effects, initialized machine state, segment state, frame footprints,
control-flow joins, opcode effect totality, and lockstep structure. It does not yet prove full
functional equivalence or full machine semantics.
