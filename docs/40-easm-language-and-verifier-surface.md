# EASM language and verifier surface

This note documents current EASM source surface and verifier behavior used by
Elisa-core.

## File skeleton

```easm
module smoke
target x86_64

export def easm_pause() -> void abi c:
    clobbers: cc, memory
    stack: unchanged
    control: returns
    requires: x86_64.sse.pause
    body:
        pause
        ret
```

Current top-level forms:

- `module <name>`
- `target <target>`
- `export def <name>(params...) -> <type> abi <abi>:`

## Function contract sections

Current section headers:

- `facts:`
- `inputs:`
- `outputs:`
- `labels:`
- `clobbers:`
- `preserves:`
- `stack:`
- `control:`
- `requires:`
- `body:`

## Memory effects

Memory effects are declared in `clobbers:`.

- `memory` declares broad memory access and covers both reads and writes.
- `memory.read` declares loads only.
- `memory.write` declares stores only.

The verifier treats `lea` as address computation, not memory access. A
read-modify-write instruction such as `addq $1, (%rdi)` requires read coverage
and write coverage, either via broad `memory` or both direction-specific atoms.

Memory base registers that come directly from input parameters must use a typed
address-space carrier such as `HostPtr[T]`, `GuestVAddr[T]`, or
`NativeMappedGuestPtr[T]`. Raw scalar bases such as `uintptr` are rejected unless
the function declares `requires: memory.base.untyped` after a manual proof.

## Inputs and outputs

Input binding surface:

```easm
inputs: params = rdi, exit_func = rsi
```

Output binding surface:

```easm
outputs: ret = rax
```

Current rule:

- EASM v1 output binding supports `ret` output name

## Body surface

Body accepts instruction lines, labels, and pseudo state assertions.

```easm
body:
    movq %rdi, %rax
loop:
    cmpq $0, %rax
    je done
    decq %rax
    jmp loop
done:
    ret
```

State assertion forms in body:

- `state ...`
- `fact ...`
- `assume ...`

## Label contracts

Label preconditions are declared in `labels:` and checked at jump sites.

```easm
labels:
    host_entry: fs: host
```

## Allowed stack contract atoms

- `unchanged`
- `aligned`
- `16`
- `synthetic`
- `switches`
- `owns`
- `noreturn`
- `probed`

## Allowed control contract atoms

- `returns`
- `noreturn`
- `tail_jumps`
- `may_fault`

## Allowed `requires` capability atoms

- `aarch64.cntvct`
- `aarch64.platform_register.x18`
- `aarch64.yield`
- `callee_saved.preservation.unchecked`
- `call.caller_saved_liveness.unchecked`
- `call.return_address_choreography.unchecked`
- `compare.signed`
- `compare.unsigned`
- `control.direct`
- `control.indirect`
- `control.poison_target.unchecked`
- `control.target.untyped`
- `control.tiny_target.unchecked`
- `debug.trap`
- `fixed_address`
- `frame_pointer.handoff.unchecked`
- `input.unused`
- `immediate.truncation`
- `memory.base.untyped`
- `operand_size.inferred`
- `pic`
- `relocation.symbol`
- `return.register.preinitialized`
- `riscv.reserved_registers`
- `stack.call_alignment.unchecked`
- `stack.entry_pop.unchecked`
- `stack.owner.untyped`
- `x86_64.atomic.rmw`
- `x86_64.cpuid`
- `x86_64.fpu_control`
- `x86_64.legacy_high_byte`
- `x86_64.rdtsc`
- `x86_64.rdtsc.unordered`
- `x86_64.segment`
- `x86_64.segment.fs`
- `x86_64.segment.gs`
- `x86_64.segment.persistent`
- `x86_64.segment.restore`
- `x86_64.segment.selector.untyped`
- `x86_64.segment.state.unchecked`
- `x86_64.segment.write`
- `x86_64.simd_state`
- `x86_64.sse.lfence`
- `x86_64.sse.pause`

## Signature type surface

Current signature types include scalar types plus role types and typed
address-space carriers.

Role-type examples:

- `GuestEntryPoint`
- `GuestCallable`
- `GuestThreadEntry`
- `GuestThreadArg`
- `GuestThreadResult`
- `GuestPC`
- `HostCallable`
- `NativeCallable`
- `ExitFunction`
- `GuestStackTop`
- `GuestFsSelector`
- `HostFsSelector`
- `PublishedExecutableAddr`
- `WritableExecutableAddr`
- `HostStackPointer`
- `SegmentSelfPointer`
- `HostThreadId`
- `SignalContextPtr`
- `MachineContextPtr`

Address-space carrier examples:

- `GuestVAddr[T]`
- `HostPtr[T]`
- `NativeMappedGuestPtr[T]`

## Verifier issue code catalog

Current EASM verifier and parser can report these issue codes:

- `ambiguous-operand-size`
- `call-immediately-before-ret`
- `call-stack-alignment-unknown`
- `call-stack-misaligned`
- `call-without-stack-contract`
- `callee-saved-not-preserved`
- `callee-saved-preservation-unproven`
- `caller-saved-use-after-call`
- `cc-clobber-missing`
- `conflicting-control-contract`
- `direct-control-intent-missing`
- `direction-flag-not-restored`
- `duplicate-contract-atom`
- `duplicate-export`
- `duplicate-input-binding`
- `duplicate-label`
- `duplicate-label-contract`
- `duplicate-output-binding`
- `duplicate-param`
- `empty-label-precondition`
- `entry-stack-pop`
- `guest-entry-call-mangles-stack`
- `hard-coded-address`
- `high-byte-register`
- `immediate-truncation`
- `implicit-clobber-missing`
- `indirect-control-intent-missing`
- `input-register-unused`
- `invalid-clobber-register`
- `invalid-export`
- `invalid-input-binding`
- `invalid-label-contract`
- `invalid-output-binding`
- `invalid-param`
- `invalid-preserve-register`
- `invalid-register-binding`
- `invalid-signature-type`
- `label-contract-without-label`
- `label-precondition-unsatisfied`
- `large-stack-adjust-without-probe`
- `may-fault-without-faulting-op`
- `memory-read-without-clobber`
- `memory-write-without-clobber`
- `missing-body`
- `missing-capability`
- `missing-control-contract`
- `missing-input-binding`
- `missing-name`
- `missing-return-output`
- `missing-section`
- `missing-stack-contract`
- `noreturn-can-return`
- `noreturn-jump-without-tail-contract`
- `noreturn-missing-terminal`
- `partial-register-return`
- `poison-indirect-control-target`
- `poison-return-target`
- `preserve-without-clobber`
- `raw-indirect-control-target`
- `raw-memory-base`
- `raw-segment-selector`
- `raw-stack-handoff`
- `rdtsc-without-fence`
- `read-failed`
- `register-target-mismatch`
- `register-read-uninitialized`
- `register-write-without-clobber`
- `reserved-register-use`
- `return-not-terminal`
- `return-register-mismatch`
- `return-register-not-written`
- `returning-stack-leak`
- `returning-unqualified-jump`
- `returns-missing-ret`
- `scan-failed`
- `segment-access-intent-missing`
- `segment-register-intent-missing`
- `segment-register-return-without-lifetime-contract`
- `segment-register-write-intent-missing`
- `segment-register-write-without-memory-clobber`
- `segment-transfer-state-unknown`
- `signed-branch-intent-missing`
- `stack-effect-undeclared`
- `stack-pointer-write-unchanged`
- `stack-switch-without-ownership-contract`
- `stack-without-memory-clobber`
- `stale-flags-branch`
- `symbol-relocation-intent-missing`
- `tail-jump-frame-pointer-clobber`
- `tail-jumps-without-jump`
- `target-capability-mismatch`
- `tiny-indirect-control-target`
- `tiny-return-target`
- `unexpected-top-level`
- `unknown-abi`
- `unknown-control-contract`
- `unknown-input-binding`
- `unknown-output-binding`
- `unknown-require-capability`
- `unknown-stack-contract`
- `unsigned-branch-intent-missing`
- `unsupported-entry-fact`
- `unsupported-instruction`
- `unsupported-label-precondition`
- `unsupported-state-assertion`
- `void-return-output`

## Related surfaces

- project report integration: [39-project-native-lint-surfaces.md](/Users/torarinvikbjarko/Documents/Coding%20Projects/Go%20projects/Elisa-core/docs/39-project-native-lint-surfaces.md)
- unsafe summary integration (`EASM.Requires.*`): [31-unsafe-report-and-budget-surface.md](/Users/torarinvikbjarko/Documents/Coding%20Projects/Go%20projects/Elisa-core/docs/31-unsafe-report-and-budget-surface.md)
