# docs/101 — EASM composition, platform protocols, and derived unsafe-effects

Goal: make the Elisa assembler (EASM) **composable, platform-polymorphic, and honest about the
effects it leaks** — without weakening a single line of the existing verifier. Every form added here is
**pure front-end lowering**: `fragment`, `protocol`/`impl`, and `template` all desugar to a concrete
per-target instruction stream *before* `verifyFunction` runs, so the liveness / clobber-cross-check /
provenance / segment-state / stack-alignment machinery (`src/easm/easm.go`, `src/backend/easm_clobber_verify.go`)
is unchanged. The net surface **shrinks**: we add 3 keywords and remove more flat vocabulary than we add.

Prior art this builds on: docs/40 (EASM language + verifier surface), docs/63 (effect-permission phases),
docs/89 (effect laws), docs/31 (unsafe report + budget surface).

## The load-bearing invariant

> **Abstractions never reach the verifier; only concrete instructions do.**

`fragment`/`protocol`/`template` are resolved (spliced / specialized / assembled) at compile time into the
same single-target instruction body the verifier already certifies today. If a proposed feature cannot
lower that way, it is out of scope (see Non-goals). This is the identical discipline as the grammar DSL,
the comprehension family, and the contract algebra (docs/100): *desugar into a verified core.*

## Baseline that already exists (do not rebuild)

- A real **verified assembler**: typed signatures, `requires:` capability gating, register liveness, a
  clobber set cross-checked against LLVM MC, typed pointer provenance (`HostPtr[T]`/`GuestVAddr[T]`),
  `state fs:` segment-state tracking, stack-alignment and cc-ordering proofs.
- One **surface syntax** (AT&T only), per-module `target`.
- A working **role-type vocabulary** — 18 of 19 types are used 3–9× each in the emulator (`GuestEntryPoint`,
  `GuestFsSelector`, `GuestStackTop`, …). Only `NativeMappedGuestPtr[T]` is dead (0 uses).
- A rich, in-use **`Unsafe.*` effect family** with `trusted` as the non-propagation firewall:
  `Unsafe.RawExtern` (≈763×, the floor), `Unsafe.SegmentMutation` (≈462×), `Unsafe.GuestSegmentInstall`,
  `Unsafe.StaleRef`, `Unsafe.PointerCast`, `Unsafe.MachineCodeBuilder`, `Unsafe.ExecutableCodePublish`, …
  `trusted Unsafe.X, Unsafe.Y:` is the confirmed block form that absorbs effects so they stop propagating.

So the job is **composition + polymorphism + effect honesty**, not "make it safe."

## 1 — `fragment` + composition  *(splice-before-verify)*  *(PROTOTYPED)*

Implemented in `src/easm/easm.go` (`expandCompositions` / `materializeComposition`) as a pure pre-verify
pass; tests in `src/easm/easm_composition_test.go`. The matrix collapse, the splice, and three "composition
cannot launder an unchecked sequence" negatives (uninitialized read, undeclared clobber, laundered `%fs`
write) all pass against the unchanged verifier.

> **Conditional inputs — RESOLVED.** An input consumed *only* by a conditional fragment is genuinely
> unused in the variants that exclude it. Rather than blanket-suppress the `input-register-unused` check
> (which would mask real bugs), `pruneConditionalInputs` drops that input's *param and binding* from the
> variants that do not read it — so the base variant has a genuinely smaller signature, exactly like the
> hand-written `JumpMainEntry` vs `JumpMainEntryWithFs` pair. An input read by **no** variant is dead, not
> conditional, so it is kept everywhere and still flagged. The read-set is computed with the verifier's own
> `registersReadBy`, so desugar and verifier agree by construction.


A `fragment` is a named, reusable instruction snippet with its own partial contract. It is **not**
independently certified — fragments are spliced into a host routine and the verifier runs on the
**post-splice whole**, so liveness/clobbers/segment-state flow across fragment boundaries exactly as if
the bytes had been written inline.

```easm
fragment setup_sysv_argv(params: HostPtr[void], exit_func: ExitFunction):
    body:
        andq $-16, %rsp
        subq $8, %rsp
        pushq 8(%r10)
        pushq 0(%r10)
        movq %r10, %rdi
        movq %r9, %rsi

fragment install_guest_fs(sel: GuestFsSelector):
    requires: x86_64.segment.fs, x86_64.segment.write, x86_64.segment.persistent
    body:
        movw <sel>, %fs
        state fs: guest
```

The six near-identical entry points (`guest_exec_x86_64.easm:142–272`, ~130 lines copy-pasted) collapse to
**one** composed routine whose variants are selected by which fragments are spliced:

```easm
export def jump_main_entry[FS: opt, STACK: opt](entry: GuestEntryPoint, params: HostPtr[void],
        exit_func: ExitFunction) -> void abi c:
    clobbers: rax, rsi, rdi, r9, r10, r11, rsp, cc, memory
    stack: synthetic, aligned 16, noreturn
    control: noreturn, tail_jumps
    requires: control.indirect
    body:
        movq %rdi, %r11
        movq %rsi, %r10
        movq %rdx, %r9
        compose STACK switch_to_stack          # spliced only in the STACK variant
        compose setup_sysv_argv(params, exit_func)
        compose FS install_guest_fs            # spliced only in the FS variant
        jmp *%r11
```

**Rule:** each materialized variant (`jump_main_entry`, `…[FS]`, `…[STACK]`, `…[FS,STACK]`) is verified as a
whole concrete body. A fragment that reads a register an earlier fragment did not establish is a verify
error *in that composition* — there is no way to smuggle an unchecked sequence through composition.

## 2 — `protocol` + `impl … for <platform>`  *(one signature, many bodies)*  *(PROTOTYPED)*

Implemented in `src/easm/easm.go` (`selectPlatformImpls` / `platformMatchesTarget`); tests in
`src/easm/easm_protocol_test.go`. A `protocol` block declares method signatures; a function carries an
optional `for <platform>` tag (bare arch or a `(arch, os, role)` tuple); `selectPlatformImpls` keeps only
the impls matching the build target — **before** `VerifyModule` — so two impls may share a method name
(e.g. `relax for x86_64` / `for aarch64`) without a duplicate-export clash, and an arch-mismatched
instruction is dropped rather than failing verification. Conformance (arity + return type) is checked
against the protocol signature. Surface differs slightly from the sketch below: the impl is an ordinary
`export def … for <platform>`, not a nested `impl` block, which reuses the entire function parser.

> **Role axis deferred.** `arch` and `os` keys are matched against the target triple; a `role` key
> (`host`/`guest`) is not encoded in the triple yet, so `platformKeyMatches` treats it as non-discriminating.
> Build-level role dispatch (needed for the `%fs`-guest / `%gs`-host TlsBase split) is the next increment.


A `protocol` declares a machine operation by signature; `impl … for <platform>` gives a fully-contracted,
fully-verified body per platform. The dispatch axis is a **tuple `(arch × os × role)`**, not just arch —
because the most valuable polymorphism here (host vs guest TLS) is *same arch, different segment register*.

```easm
protocol CycleCounter:
    read_fenced() -> u64

impl CycleCounter for x86_64:
    outputs: ret = rax
    clobbers: rax, rdx, cc, memory
    requires: x86_64.rdtsc, x86_64.sse.lfence
    body:
        lfence
        rdtsc
        shlq $32, %rdx
        orq %rdx, %rax
        lfence

impl CycleCounter for aarch64:
    outputs: ret = rax
    requires: aarch64.cntvct
    body:
        isb
        mrs %x0, cntvct_el0
        isb
```

This collapses the parallel `common_x86_64.easm` / `common_aarch64.easm` files into one protocol with two
impls. Each impl is still concrete and individually verified for its target; the protocol is only a
**selection layer** resolved at target selection — nothing about the verifier or the instruction stream
changes.

The host/guest TLS split becomes a platform-keyed protocol over a new abstract role (`GuestTlsBase` /
`HostTlsBase`) instead of hardcoded `%fs`/`%gs`:

```easm
protocol TlsBase:
    install(base: GuestTlsBase) -> void
    read_self() -> SegmentSelfPointer

impl TlsBase for (x86_64, any, guest):     # PS4 guest TLS lives in %fs
    ...
        movw <base>, %fs
        state fs: guest
impl TlsBase for (x86_64, darwin, host):   # macOS host TLS lives in %gs
    ...
        movw <base>, %gs
        state gs: host
```

## 3 — `template` (typed-hole runtime code-gen)  *(the real safety win)*  *(4a PROTOTYPED)*

Implemented in `src/easm/easm.go` (`parseTemplateHeader` / `analyzeTemplates`); tests in
`src/easm/easm_template_test.go`. A `template def name(hole: Type, …):` reads like an ordinary Elisa
function — typed parameters, an indented body, holes referenced by **bare name** (no `<bracket>` sigils).
`analyzeTemplates` records the patch-points (which hole is consumed at which instruction) and enforces the
two safety properties that make runtime code-gen sound: every declared hole is referenced, and a hole's
type fits the operand slot it lands in — a 16-bit selector cannot be baked where a 64-bit address/target
is expected (`template-hole-type-mismatch`). Surface note: the real form drops the `<…>` substitution
sigil shown below in favour of bare-name holes (see §8).

> **Stage 4b — byte assembly *(DONE)*.** `AssembleTemplate` (`src/easm/easm_assemble.go`) assembles the
> body with the trusted `llvm-mc` assembler (never a hand-rolled encoder) and locates each hole's byte
> slot by assembling it zeroed vs. all-ones and diffing — so offsets are *derived* from the encoding, not
> hand-counted. Returns `{bytes, []PatchByte{offset,width}}`. Tested (`easm_assemble_test.go`) against the
> exact known-good 33-byte thunk encoding (`host_fs` → offset 9 ×2, `target` → offset 16 ×8); skips if
> `llvm-mc` is absent. This is the artifact that retires the ~180 hand-encoded bytes in `core/linker.elisa`.
>
> **Runtime fill — DONE.** `InstantiateImage` fills the holed image with concrete (little-endian) values.
> `TestInstantiateMatchesDirectAssembly` proves the pair (assemble-with-holes + fill) is **byte-identical**
> to assembling the body with those values directly — so the whole "assemble once, fill at run time" model
> is correctness-proven against the trusted assembler, end to end, in fast tests.
>
> **Stage 4c (remaining, native-bound):** emit the image + an `instantiate` entry into the program (LLVM IR
> data globals + a fill routine, with Elisa-frontend access), then migrate `core/linker.elisa` to call it.
> This is codegen plumbing whose validation needs a native build + boot — best paired with the gap-3
> validation pass rather than landed unvalidated.


The single most unsafe surface in the emulator is **not** in EASM at all: the HLE boundary thunk is ~180
hand-encoded bytes in `core/linker.elisa` (`full[i] <- 0xXX`, manual ModRM `0xEA`/`0xE2`, offset math
`t = 136 + gs_extra`) — because the thunk is generated *at runtime* with runtime values, and EASM only does
compile-time code with static symbols.

A `template` is a routine the assembler compiles at build time into `{ bytes, patchpoints }`, where each
hole is a **typed** slot the runtime fills:

```easm
template hle_boundary_thunk:
    hole target:  HostCallable          # filled at runtime with the host fn address
    hole host_fs: HostFsSelector
    hole host_gs: HostGsSelector? = 0   # optional; emitted only when provided
    clobbers: r10, r11, memory
    requires: control.indirect, x86_64.segment.fs, x86_64.segment.write, x86_64.segment.restore
    body:
        movw %fs, %r10w
        pushq %r10
        movw <host_fs>, %fs
        when host_gs: movw <host_gs>, %gs   # conditional emission, offsets recomputed by the assembler
        movabs <target>, %r11
        call *%r11
        popq %r10
        movw %r10w, %fs
        ret
```

Runtime side (Elisa) replaces the byte-poking with a typed instantiation:

```elisa
thunk = hle_boundary_thunk.instantiate(target: host_fn, host_fs: sel, host_gs: gs_or_zero)
GuestExec_PublishExecutable(region, thunk.bytes)
```

The assembler — not a human — computes every prefix/ModRM/offset; a `HostFsSelector` hole cannot be filled
with an address, and a `HostCallable` hole cannot be filled with a selector. The whole template is verified
once with holes as symbolic unknowns. This deletes the `linker.elisa` ModRM/offset hazard class entirely.

## 4 — Derived unsafe-effects  *(state the hazard once, in `requires:`)*  *(PROTOTYPED — derivation + surfacing)*

`easm.DerivedEffects` (in `src/easm/easm.go`) is the complete, pure projection from the verified contract
to the caller-facing `Unsafe.*` / `Segment.*` set; tests in `src/easm/easm_effects_test.go`, and it is
surfaced per-export in the EASM report (`FunctionSummary.DerivedEffects`).

> **Enforcement is a coordinated migration, not a compiler flip.** The backend
> (`easmDeclaredEffectPermissions`) already *enforces* a subset (`Unsafe.SegmentMutation` + `Segment.*`) by
> requiring the matching Elisa extern's `can[...]` to expose it. Widening enforcement onto the full derived
> set would force every emulator extern declaration to add the new permissions or fail to build — so the
> full set is computed and **surfaced** now (for honesty and migration planning), and the enforcement
> widening is staged with the emulator-side extern updates. Derivation is the feature; enforcement is the
> rollout.


Today an EASM routine can declare capabilities **twice** — the header `can[...]` (`fn.Effects`) and the
`requires:` section — and both flatten into the same backend permission list (`llvm_easm.go:225`, `:235`).
That is the redundancy. The fix is **not** to delete a path; it is to make the caller-facing `Unsafe.*`
effect a *derived projection* of the verified `requires:` set + instruction analysis. The author states each
hazard once, in the bottom-up contract the verifier already checks, and the propagated effect cannot drift
from what the instructions actually do.

Derivation (extends the existing extern → `Unsafe.RawExtern` rule):

| EASM `requires:` (verified, instruction-gated) | Derived caller-facing effect |
|---|---|
| *any export — floor* | `Unsafe.RawExtern` |
| `x86_64.segment.write` | `Unsafe.SegmentMutation` |
| `…write` + persistent `state fs: guest` | `+ Unsafe.GuestSegmentInstall`, `+ Segment.Guest` |
| `…write` + `x86_64.segment.restore` | `Segment.Host` (restored on exit; no install leak) |
| `control.indirect` with untyped target | `Unsafe.IndirectCall` |
| `memory.base.untyped` / raw store | `Unsafe.PointerCast` |
| `template` instantiate | `Unsafe.MachineCodeBuilder`, `Unsafe.ExecutableCodePublish` |
| `x86_64.atomic.rmw` | `Unsafe.RawExtern` *(floor; no finer effect today)* |

A direct Elisa caller must hold the derived effect:

```elisa
def step() -> void:
    can Unsafe.SegmentMutation, Unsafe.GuestSegmentInstall:
        ElisaTls_LoadGuestFsSelectorAsm(sel)   # derived effects, forced acknowledgment
```

`trusted` is the deliberate firewall — a vetted wrapper absorbs the effect so its own callers do not inherit
it (this already works; the design only makes the *source* of the effect derived):

```elisa
def TLS_InstallGuestSegment() -> FsGuard:
    trusted Unsafe.SegmentMutation, Unsafe.GuestSegmentInstall:   # I vouch; stop propagation here
        TLS_InstallPrimaryTcbBase()
    return FsGuard(true)
```

Decision: **stay within `Unsafe.*`; do not add a parallel `Asm.*` family.** Any raw machine access is
unsafe by definition; granularity comes from the dotted suffix (`Unsafe.RawExtern` is the honest floor a
benign `pause`/`rdtsc` carries). A second family would re-create the two-doors redundancy this section
removes. The header `can[...]` form is retained only as an escape hatch for declaring an effect the
instructions do not imply; it is no longer the default authoring path.

## 5 — Remove / simplify ledger (evidence-backed)

| Change | Evidence | Action |
|---|---|---|
| Remove `NativeMappedGuestPtr[T]` | 0 emulator uses (others 3–9 each) | delete dead role type |
| Collapse 4-way ABI fiction | `easmCallConv` maps `c`/`sysv`/`sysv_x86_64`/`ps4_sysv` → all to `"c"` (`llvm_easm.go:322`) | one canonical tag, or make `ps4_sysv` carry real lowering; today decorative + misleading |
| De-redundant capability authoring | `can[...]` + `requires:` both feed one permission list | `requires:` is the source of truth; `Unsafe.*` is **derived** from it (§4); `can[...]` demoted to escape hatch |
| `requires:` surface *(optional, cosmetic)* | 45 flat tokens mix hardware caps / intent / `*.unchecked` overrides | could split into `uses:`/`intent:`/`unchecked:`; low priority, leave flat for now |

Composition (§1) and protocols (§2) additionally *delete duplication* (6 entry variants → 1; 2 arch files → 1
protocol), and templates (§3) delete ~180 lines of hand-bytes — simplification by construction, not by flag.

## 6 — Untouched (the safety core)

The entire verifier (liveness, LLVM clobber cross-check, provenance, segment-state, stack-alignment,
cc-ordering); single AT&T syntax; the 18 live role types; `state fs:` + the affine `FsGuard` pairing; and
the `*.unchecked` / `*.untyped` escape hatches (explicit danger is principle 4 working as designed). We do
**not** auto-infer `clobbers:` — the stated clobber set is a human contract the LLVM cross-check validates
against; the ceremony is the point.

## 7 — Staging

1. **`fragment` + composition** — *DONE.* Splice-before-verify, variant matrix, conditional-input pruning; verifier unchanged.
1b. **Backend emission** — *DONE (by construction).* `emitEASMModule` iterates `module.Functions`, which `Parse` already replaces with the materialized variants; variants carry the source signature (regression-guarded by `TestComposeVariantsPreserveSignatureForEmission`). No variant-specific emission code.
2. **`protocol`/`for <platform>`** — *DONE (arch/os).* Collapses the arch pairs. Role axis + `GuestTlsBase`/`HostTlsBase` deferred to the build-level role increment.
3. **Derived unsafe-effects (§4)** — *DONE (derivation + surfacing).* Full `DerivedEffects` projection + report field; widening enforcement onto the full set is staged with emulator extern updates.
4a. **`template` typed-holes (surface + model)** — *DONE.* Native `template def` surface, patch-point model, hole-type safety check.
4b. **`template` byte assembly** — *DONE.* `AssembleTemplate` via `llvm-mc` + zero/ones diff → `{bytes, patchpoints}`.
4b′. **runtime fill** — *DONE.* `InstantiateImage`; proven byte-identical to direct assembly end-to-end.
4c. **emit + migrate `linker.elisa`** — remaining (native-bound): lower the image to Elisa data + `instantiate` in the backend, then retire the hand-bytes.

## 8 — Native surface: feel, not bolted on

Design goal (carried by the template surface in §3): EASM should read like Elisa, not like a separate
assembler config bolted alongside it. The template form already demonstrates the target feel — `template
def name(hole: Type, …):`, an indented body, holes referenced by bare name. The legacy export form still
reads as a config block, and the gap is worth closing incrementally (pure parser sugar, verifier
unchanged):

| Bolted-on today | Native direction | Status |
|---|---|---|
| `can[Unsafe.X, Unsafe.Y]` (bracket list) | `can Unsafe.X, Unsafe.Y` (Elisa's `can`) | **DONE** (`nativeCanRE`; `can[…]` kept) |
| fragment holes `<sel>` | bare-name holes (as templates already use), `<…>` kept as alias | **DONE** (`substituteParams`, `%`/`$`-guarded) |
| `export def f() -> u16 abi c:` then `outputs:/clobbers:/stack:/control:/requires:` sections, then `body:` | inline contracts (`requires …` like Elisa) and an indented body with no `body:` marker | **DONE** (`inlineContract`; `body:`/`section:` kept) |
| `inputs: p = rdi` register binding soup | a single inline annotation, not a section block | **DONE** (`inputs p = rdi` inline) |

These are surface migrations: every form still lowers to the same `Function`/`Template` the verifier and
backend already consume, so none of them touch the safety core. A fully native routine now reads:

```
export def read_gs() -> u16 abi c:
    outputs ret = rax
    stack unchanged
    control returns
    requires x86_64.segment.gs
    movw %gs, %ax
    ret
```

The legacy `body:` + `section:` (with multi-line continuation) forms still parse identically — confirmed by
the full `easm` suite — so the working `.easm` corpus is undisturbed. The inline `requires` lowers to the
same verified `Function` (omitting it still flags the `%gs` read), so the native surface is sugar, not a
second semantics. §8 is complete.

## Non-goals

- **Arch-neutral instruction bodies** (one body lowering to x86 *or* ARM). That is an IR / intrinsics layer,
  not an assembler; it destroys the exact-encoding control that is EASM's reason to exist. Polymorphism is
  *one signature, many concrete bodies* (§2) — never one abstract body.
- **A parallel `Asm.*` effect family** (§4) — granularity lives in the `Unsafe.*` suffix.
- **Inferred `clobbers:`** (§6) — keep the stated contract.
