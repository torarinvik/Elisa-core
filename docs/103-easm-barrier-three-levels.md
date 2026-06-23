# 103 — The Elisa↔EASM barrier: a three-level "no surprises" assembler

## Thesis

The boundary between verified Elisa and raw machine code is not a wall where verification
stops — it is a **contract membrane** where verification *continues by other means*. Every bug
we hit at this boundary has the same shape: a guarantee Elisa enforces everywhere else
silently evaporates the moment a value crosses into asm. The design goal is to make each
descent an *explicit, checked* loss of one specific guarantee, and to shrink the irreducibly
unsafe core to a single audited nub.

## The guarantee gradient (three levels)

The levels are not "more/less abstract" — each step down forfeits *one* guarantee and keeps
the rest:

| Guarantee | L1 Elisa | L2 virtual-ISA | L3 machine |
|---|---|---|---|
| Type safety (typed regs/mem) | ✅ | ✅ | ❌ raw bytes |
| Effects tracked (`can`/`trusted`) | ✅ | ✅ on ops | re-checked vs instructions |
| Pointer provenance (`Guest`/`Host`) | ✅ | ✅ | ❌ |
| Flags explicit | n/a | ✅ typed value | ❌ implicit EFLAGS |
| No clobber bugs | ✅ | ✅ structurally | ❌ reappears (mechanically re-derived) |
| Frame conditions (`changes`/`reads`) | ✅ | ✅ | declared + checked |
| Termination (`decreases`) | ✅ | ✅ | runtime watchdog |

Hazards become *unrepresentable* at the level above where they live.

## Canonical surface

EASM uses Elisa's real grammar, not a look-alike DSL:

- `asm def name(...) -> T:` — parallels `def` (Elisa uses `def`, never `fn`).
- Colon-blocks and indentation, never braces — **except** `bytes { ... }` (verbatim foreign
  quotation) and `clobber { ... }` (a set literal — Elisa already uses `{}` for sets).
- Effects use `can`/`trusted` directly — no separate `effects` keyword.
- Mutability on the binding: `dst: mutable Host[u8]` (pointee-writable), parallel to `mutable T&`.
- Contract clauses are *leading body statements* (reusing the existing Elisa rule), ordered
  frame/logic first (`requires`/`changes`/`reads`/`ensures`), effects last (`can`/`trusted`).

```elisa
asm def memcpy_fast(dst: mutable Host[u8], src: Host[u8], n: usize) -> void:
    changes dst[0..n]
    reads   src[0..n]
    ensures dst[0..n] == old(src[0..n])
    can     Unsafe.RawWrite

    reference:                              # portable, obviously-correct L2 — this IS the spec
        i: mutable usize = 0
        while i < n decreases n - i:
            store(dst.offset(i), load[u8](src.offset(i)))
            i <- i + 1

    target X86_64 lockstep reference:       # hand-tuned L3, fuzzed against `reference`
        in      dst -> rdi, src -> rsi, n -> rcx
        clobber { flags }
        bytes   { rep movsb }               # bit-identical state on every input, or build fails
```

`lockstep reference` is a *proof obligation*: the L3 block is fuzzed against the named
`reference` via the `@lockstep` oracle; the build fails on any divergence in declared
`changes`/`ensures` state. That gives the transitive chain **L1 ⊨ L2 ⊨ L3**: `reference` is
checked against the L1 contract, the bytes are checked against `reference`. You ship the
fastest possible machine code and *prove* it equals the version you can read.

Note: the standalone `.easm` file form (everything is already assembly) keeps `export def`;
`asm def` is the spelling for an asm routine appearing *inline in `.elisa` source*.

## What was already built (verified, not assumed)

The existing `compiler/src/easm` package already implements most of the membrane:

- Typed address-space carriers `HostPtr[T]` / `GuestVAddr[T]` / `NativeMappedGuestPtr[T]` **with
  enforcement** (`raw-memory-base`: a raw scalar used as a memory base is rejected).
- Colon-block contracts: `inputs`/`outputs`/`clobbers`/`preserves`/`stack`/`control`/`requires`.
- Derived **and propagated** `Unsafe.*` effects (`DerivedEffects`), plus `can Unsafe.*` headers.
- Exhaustive clobber/effect discipline against the *instructions*: `register-write-without-clobber`,
  `cc-clobber-missing`, `memory-write/read-without-clobber`, `implicit-clobber-missing`,
  `callee-saved-not-preserved`, `segment-*-intent-missing`, stack-effect checks.
- Composition (fragments), platform protocols, templates with typed holes, llvm-mc assembly.

## Staging

- **Stage 1 — frame conditions `changes`/`reads` — DONE.** Per-buffer memory frame contracts that
  upgrade the coarse `clobbers: memory` into a precise per-carrier guarantee. Every memory access
  in a routine that declares a frame must route through a named carrier parameter; a stray write
  through any other carrier is rejected (`frame-write-outside-changes` / `frame-read-outside-reads`
  / `frame-unknown-carrier`). Opt-in: a routine that declares neither clause is governed only by
  the coarse clobbers, exactly as before. Reuses the existing register→carrier provenance walk;
  exact because x86 instructions carry at most one memory operand. Adopted on the emulator's
  `LoadOrbisFpStateAsm` (`reads`) and `AtomicExchangeU32Asm` (`changes`).
- **Stage 2 — `ensures` functional contracts.** Discharge against machine semantics via the
  existing ladder (affine → linear → SMT). Needs a small machine-state model.
- **Stage 3 — `reference` + `target … lockstep …`.** Translation validation via the `@lockstep`
  state-level oracle (commit `bdda4456c`) + `@property` fuzz harness. The big new guarantee; needs
  an execution oracle (the emulator itself is an x86 engine — reuse it).
- **Stage 4 — the L2 typed-SSA ISA.** Typed virtual registers, `Flag`/`Carry` as first-class
  values, deterministic non-optimizing lowering to L3 validated per-routine by Stage 3. Model on
  QBE / C-- / Cranelift CLIF, *not* LLVM IR (reject poison/undef; expose flags and exact width).
  Translation validation means the allocator/lowering can be wrong-but-caught, never wrong-but-shipped.
- **Stage 5 — inline `asm def` in `.elisa` source.** Main-compiler parser learns the `asm def`
  block and hands the body to the easm package.
