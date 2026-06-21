# 94 — Decode firewall: bit-level refinement reasoning

## Status
Implemented. Closes the prover gaps that stood between Elisa's existing refinement-type machinery
(laws, `is`-applied refinement types, `array[T, N]` index-bounds elision — docs/80, 85, 86) and a
practical "typed instruction-format firewall" for emulator/decoder code: raw `u32` words enter at
the boundary; after decode, register/immediate fields carry proven refinements, so register-file
indexing needs no runtime bounds check and the rest stays range- and format-safe.

The type system already had everything; the gaps were all in the SMT discharge tier
(`analyzer_smt_discharge.go`, `bitwiseTerm`). All four fixes are sound *over-approximations or exact
models* — the prover concludes only on `unsat` of the negated goal, so each can only decline a proof,
never fabricate one. Each ships with a positive and a soundness-negative regression test
(`smt_discharge_test.go`), and an end-to-end runnable demo (`decode_firewall_demo.elisa` +
`decode_firewall_runtime_test.go`).

## Gaps closed

### Gap 1 — operand-independent mask bound (runtime bit positions)
`(word >> sh) & C` for a non-negative constant `C` is in `[0, C]` regardless of `X = word >> sh` —
the result's set bits are a subset of `C`'s. Previously `bitwiseTerm` required the masked operand to
be modelable, so a **runtime** shift (`sh` not a compile-time constant) lost the bound. Now, when the
operand's exact term is unavailable, `X & C` is modeled as a fresh integer constrained to `[0, C]`
(`maskBoundAux`) — a sound over-approximation. Result: `(word >> sh) & 0xF` proves `< 16` for any
runtime `sh`. This is what lets one runtime-position field decoder serve all positions, so a generic
`extract[lo, width]` (which can't prove at definition because the symbolic shift is unmodelable) is
**not needed** — concrete per-width helpers with a constant mask cover the decoder vocabulary.

### Gap 3 — signed bitwise / sign-extension
`bitwiseTerm` gated to unsigned results. Now a signed result reads back via the exact two's-complement
value (`smtBitvectorRead`: `bv2nat(b) - 2^W * topbit(b)`), and a signed `>>` uses `bvashr` (arithmetic)
to match the LLVM `AShr` codegen. The sign-extension idiom `(field << k) >> k` proves its exact signed
range (a 12-bit field → `[-2048, 2047]`).

### Gap 4 — sign-reinterpret cast
`termEnv`'s cast case only modeled same-signedness widening, so the `(u32 field).i32()` in a
sign-extension declined. Added: unsigned→strictly-wider-signed is the identity (source is non-negative
and fits), and same-width sign reinterpretation is the exact bitcast (modeled via `smtBitvectorRead`).
A wrapping reinterpret (a `u32 > i32max` claimed non-negative) is still correctly refused.

### Gap 2 — generic parametric extractor: intentionally not built
A truly generic `extract[lo, width]` cannot prove at its definition (the symbolic `1 << width` /
`>> lo` are outside the constant-shift fragment, and modeling variable shifts collides with the
LLVM-poison-for-shift≥width soundness gate). Gap 1 removes the need: runtime *positions* are handled,
and the small finite set of field *widths* is covered by concrete per-width helpers. Building symbolic
variable-shift reasoning was judged not worth the soundness surface for zero additional capability.

## The firewall, end to end (all proves under `-strict`)
```elisa
law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
law SBounded(self: i32, lo: i32, hi: i32) = self >= lo and self <= hi
type RegIndex = u32 is InRange[0, 31]

def decode_rd(word: u32)  -> RegIndex = (word >> 7) & 0x1F            # mask ⇒ InRange[0,31]
def decode_imm_i(word: u32) -> i32 is SBounded[-2048, 2047]:         # sign-extend ⇒ signed range
    return ((((word >> 20) & 0xFFF).i32()) << 20) >> 20
def read_reg(regs: array[u64, 32]&, r: RegIndex) -> u64 = regs[r]    # refined index ⇒ no bounds check
```

## Real-world validation (NES emulator dogfood)
The full 1311-line MOS 6502 CPU + PPU + APU (`nes_emulator/elisa/cpu6502.elisa`) compiles under
`-strict` with **zero** "could not be proven" or bounds diagnostics — every memory access
(`array[u8, 65536]` indexed by `u16`), cycle-table lookup (`array[u8, 256]` by opcode), and status-flag
mask is statically proven, no runtime checks, no `Abort` effect threaded through. It also runs
**11/11** tests green including the bit-exact `nestest_trace` CPU validation against the golden log.
This both confirms the firewall pattern holds on real code and verifies the Gap 1/3/4 changes regress
neither static verification nor runtime behaviour. (The 6502 uses fixed `array[T, N]`; the darray
index-elision below extends the same payoff to length-bounded dynamic arrays.)

## Dynamic-array index elision (immutable darray + length precondition)
The refined-index → no-bounds-check bridge originally covered only fixed `array[T, N]`. It now also
covers an **immutable** `darray[T]` whose length is pinned by a live `requires <darray>.count >= K`
precondition: a refinement-typed index proven in `[0, hi]` with `K > hi` indexes it with no runtime
check. Immutability is the soundness precondition — a `mutable` darray could `pop`/`clear` and shrink
its count below `K`, so those (and an insufficient or absent `requires`) correctly keep the check.

## Soundness summary
- Gap 1: over-approximating `X & C` to `[0, C]` widens the value's feasible region; a goal unsat over
  the superset is unsat over the actual values (sound). Negative test: `& 0xF` must not prove `< 8`.
- Gap 3/4: exact two's-complement models (`smtBitvectorRead`, `bvashr`, the same-width bitcast) — equal
  to the machine value, not approximations. Negative tests: a sign-extension must not prove `>= 0`; a
  wrapping reinterpret must not prove `>= 0`.
- All gated behind the existing `unsat`-only conclusion and the per-query timeout (hard cases →
  Unknown → runtime fallback), so cost never threatens soundness.
