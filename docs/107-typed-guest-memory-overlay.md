# 107 — Typed Guest-Memory Overlay (proc_param / mem_param hardening)

## Why

docs/104 increment 1 gave EASM **typed memory layouts**: a declared `layout` types the record
shape behind a `HostPtr[L]` carrier, so a raw assembly access `N(%base)` is checked to land on a
declared field of matching width. That mechanism is good — but it only covers **inline assembly
routines**.

The highest-value remaining fault cluster in the dogfooding PS4 emulator has a *different* shape:
ordinary Elisa code that reads guest memory by computed address —

```elisa
size      := MemoryManager_ReadU64(memory, proc_param + 0)
mem_param := MemoryManager_ReadU64(memory, proc_param + 64)
flexible  := MemoryManager_ReadU64(memory, mem_param + 16)
ext1      := MemoryManager_ReadU64(memory, mem_param + 24)
ext2      := MemoryManager_ReadU64(memory, mem_param + 40)
```

These are `base + offset` reads against the `OrbisProcParam` / `OrbisKernelMemParam` chain in the
emulator's `core/linker.elisa`. They are *exactly* the proc_param/mem_param over-read fault cluster:
nothing checks that `+ 64` lands on a declared field, nothing checks that the structure is even big
enough (`size@0` is read but the under-size guard is hand-written and easy to get wrong), and a
corrupted/unrelocated base flows straight into a wild `ReadU64`.

There is currently **no typed, verifier-checked overlay for this "read a guest struct field through
a base address + offset" pattern**. This document designs one as an *extension* of the existing
`layout` machinery, not a parallel system.

## Goal in one sentence

Let a programmer declare a guest struct once as a `layout`, then read its fields by **name** —
`proc_param.field[mem_param]` — such that every access is statically checked to hit a declared
field within the layout's declared size, while generating *byte-identical* code to today's
`ReadU64(memory, base + offset)`.

## (a) Surface syntax

Reuse the EASM `layout` declaration verbatim, with one addition — an explicit total **size**, so
bounds can be derived:

```
layout OrbisProcParam size 80:
    0  size:      u64
    64 mem_param: u64

layout OrbisKernelMemParam size 48:
    0  size:     u64
    16 flexible: u64
    24 ext1:     u64
    40 ext2:     u64
```

`size N` declares the structure's byte length. (Today layouts have no declared size; field-offset
checking works without it, but *bounds* checking needs it — see (b).) When omitted, size defaults to
`max(field.Offset + field.Width)` — sound for offset checks, but a layout that wants a real
under-size guard must state its size.

In ordinary Elisa, a guest pointer is carried as the same carrier type the EASM side already uses:

```elisa
proc_param: GuestVAddr[OrbisProcParam]
```

and a field read is a **checked accessor** that desugars to today's `ReadU64`:

```elisa
mem_param := proc_param.mem_param[memory]   # -> MemoryManager_ReadU64(memory, base + 64)
```

The `[memory]` index names the `MemoryManager` to read through (the accessor is otherwise a pure
offset). The field name `mem_param` is resolved against the carrier's layout; the access width is
taken from the field's declared type (`u64` -> `ReadU64`).

## (b) Bounds / size guards

Two checks, both static, both derived from the declared `size`:

1. **Offset-in-bounds.** `field.Offset + field.Width <= layout.Size`. A field declared past the
   struct's stated size is rejected at declaration time (`overlay-field-out-of-bounds`).
2. **Access-is-a-field.** Same as docs/104: the named field must exist and the accessor width must
   match the field's declared width. A name that resolves to no field is `overlay-unknown-field`.

The *runtime* under-size guard (the emulator's "if `size < 64` fall back to defaults" pattern) is
expressed declaratively as a per-field **minimum size**: a field may be tagged
`requires size >= N`, meaning "this field is only present when the struct's runtime `size` field is
at least N". The accessor then *must* be dominated by a guard `if proc_param.size[memory] >= N`, or
the verifier emits `overlay-field-needs-size-guard`. This turns the hand-written, easily-forgotten
under-size fallback into a checked obligation.

This is now **built**, in BOTH dominating forms:

- **Positive guard** `if base.size[mem] >= N:` (or `> N`) — `applyOverlaySizeGuardForCondition`
  pushes a `SizeGuardFact` that holds for the branch body.
- **Early-return guard clause** `if base.size[mem] < N: return …` (the idiom the emulator's linker
  actually uses) — `applyOverlayFallthroughGuard` recognizes an `if` whose then-branch definitely
  exits with no elif/else, and pushes the *negation* (`size >= N`) as a fact holding for the rest of
  the enclosing block. Both spellings of the boundary are handled: `< N` negates to `>= N`, `<= N`
  negates to `>= N+1`.

Both forms share one recognizer (`sizeGuardFactForCondition`, parameterized by the truthiness the
fact is read under) and discharge through the same `easm.CheckGuestOverlaySizeGuard` the prototype
defined, so the fact logic is single-sourced. An access to a `requires size >= K` field is discharged
iff a dominating guard proves `size >= K`; a weaker bound is rejected, a non-exiting guard clause is
rejected (control reaches the fall-through on both branches), and no fact leaks past its block.

## (c) Verifier extension vs library — RECOMMENDATION

**Recommendation: a checked-accessor desugaring (codegen helper) backed by the existing layout
declarations, NOT a fresh standalone verifier pass.**

Rationale: the `layout` *declaration* and its field/offset/width model already exist and are already
the authoritative source of truth (docs/104 reuses them for the EASM-side `N(%base)` reads of the
very same proc_param block). Reusing the same declaration for the ordinary-Elisa side means the
assembly boot reads and the high-level linker reads are checked against **one** description — they
cannot drift. The new work is therefore: (1) a small, additive *bounds* check on the layout itself
(`size`), and (2) a desugaring of `base.field[mem]` into the existing `ReadU64(mem, base+offset)`
that *fails to compile* if the field/width/bounds don't check out. A parallel verifier pass would
re-derive the same facts from raw `+ offset` arithmetic — strictly harder (it must recover the
offset and the intended type from integer math) and able to drift from the EASM declaration. Keeping
it a declaration-driven accessor is both simpler and tighter.

## (d) Mapping onto the concrete emulator consumer

The `core/linker.elisa` proc_param / mem_param chain maps directly:

| read today | overlay form |
|---|---|
| `ReadU64(mem, proc_param + 0)`  (size)       | `proc_param.size[mem]`      |
| `ReadU64(mem, proc_param + 64)` (mem_param)  | `proc_param.mem_param[mem]` |
| `ReadU64(mem, mem_param + 0)`   (mem size)   | `mem_param.size[mem]`       |
| `ReadU64(mem, mem_param + 16)`  (flexible)   | `mem_param.flexible[mem]`   |
| `ReadU64(mem, mem_param + 24)`  (ext1)       | `mem_param.ext1[mem]`       |
| `ReadU64(mem, mem_param + 40)`  (ext2)       | `mem_param.ext2[mem]`       |

The under-size fallbacks (`flexible`/`ext1`/`ext2` only valid when `mem_param.size >= 48`) become
`requires size >= 24/32/48` tags, checked against a dominating `if mem_param.size[mem] >= N` guard.
A corrupted offset (`+ 64` typo'd to `+ 65`, or a field added past `size 80`) stops being a silent
wild read and becomes a compile error — the same teeth docs/104 reports for the boot-routine sweep.

## (e) ABI / codegen neutrality

The overlay is a **verifier-and-desugar-only** artifact with no ABI. `base.field[mem]` desugars to
*exactly* the `MemoryManager_ReadU64(mem, base + offset)` call the programmer writes today — same
function, same offset constant, same width. The carrier type `GuestVAddr[L]` lowers identically to
`GuestVAddr[void]` / a raw guest address (docs/104 already establishes this ABI-identity for
`HostPtr[L]`). Therefore: **on the success path, generated code is byte-identical to the current
hand-written reads.** The only observable difference is compile-time rejection of out-of-bounds /
unknown-field / width-mismatched / unguarded accesses. This is the same zero-boot-risk argument the
docs/104 adoptions rely on, and it is what makes adoption safe to do incrementally.

## (f) Staged implementation increments

1. **Layout size + bounds (prototype, this doc).** Add an optional declared `size` to `Layout`;
   add a bounds checker that rejects fields past the size and resolves a named field to its
   offset/width. Pure additive Go in the `easm` package, with a unit test proving an out-of-bounds
   field read is rejected and an in-bounds one is accepted. *Done here.*
2. **`size N` parse.** Extend the `layout` header regex to accept `layout Name size N:` and populate
   `Layout.Size`; default to computed max when absent. Keep all existing layout tests green.
3. **Accessor desugar.** Front-end: parse `base.field[mem]` over a `GuestVAddr[L]` carrier, resolve
   the field against the layout, desugar to `ReadU64/U32/...` by field width. Emit
   `overlay-unknown-field` / `overlay-field-width-mismatch` on failure. No codegen change on success.
   *Done — read form (increment 5a) and now the WRITE form `base.field[mem] <- value` (desugars to
   `MemoryManager_WriteU<N>(mem, base + offset, value)` via `AssignStmt.AsOverlayCall`, same field
   resolution, byte-identical store).*
4. **Size-guard obligation.** Add `requires size >= N` field tags + a dominator check that the
   accessor is guarded by `if base.size[mem] >= N`. Retires the hand-written under-size fallbacks.
5. **In-source `layout` declaration + live adoption in `core/linker.elisa`.** A `.elisa` program
   declares its own carrier layout with a top-level `layout Name [size N]:` block (fields
   `<offset> <name>: <u8|u16|u32|u64> [requires size >= N]`); the analyzer registers it into the
   overlay registry in the early collect pre-pass, so `GuestVAddr[Name]` carriers and their accessors
   resolve against it with NO `AnalyzeOptions.OverlayLayouts` wiring. *The in-source declaration path
   is built and tested end-to-end (read + write desugar to the byte-identical
   MemoryManager_Read/WriteU64 calls; verified through real LLVM codegen).* Still remaining for the
   `core/linker.elisa` migration: connecting increment 4's size-guard discharge to dominating
   `if base.size[mem] >= N:` guards (the dominator→`SizeGuardFact` derivation), so the hand-written
   under-size fallbacks can be deleted rather than duplicated.

Honest status: increments 1, 2, 3 (read **and** write forms), 4 — *including* the
dominator→`SizeGuardFact` derivation in **both** the positive-guard and early-return-guard-clause
forms — and the in-source `layout` declaration path of increment 5 are all real, compiling, tested
slices. A `.elisa` program can now declare its own carrier layout, read/write its fields with no
embedding-side wiring (byte-identical MemoryManager calls, verified through LLVM codegen), and have
`requires size >= N` fields enforced against dominating size guards written either as
`if base.size[mem] >= N:` or as the guard-clause `if base.size[mem] < N: return`. The front-end
machinery is therefore complete; what remains (increment 5 proper) is the mechanical migration of the
emulator's `core/linker.elisa` proc_param/mem_param reads onto the overlay — declaring the two
layouts with their `requires` tags, retyping the carriers as `GuestVAddr[L]`, and deleting the
hand-written field reads and under-size fallbacks now that the obligations are checked. That work
lives in the emulator repo.
