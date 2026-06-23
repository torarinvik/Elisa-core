# 108 — Typed Guest-Memory Table Overlay (fixed-stride entry reads)

## Why

docs/107 gave Elisa a **typed single-struct guest-memory overlay**: a `GuestVAddr[L]` carrier reads
named fields of *one* struct via `base.field[mem]`, which the analyzer desugars to the exact
`MemoryManager_ReadU<N>(mem, base + offset)` the programmer writes by hand today — with the field
resolved against a declared `layout L` (unknown-field / out-of-bounds / width-mismatch all caught
statically).

The next-highest-value fault shape in the dogfooding PS4 emulator is a **fixed-stride array of guest
structs read by index** — an ELF symbol table (`Elf64_Sym`, 24 bytes/entry) or relocation table
(`Elf64_Rela`, 24 bytes/entry):

```elisa
name_off  := MemoryManager_ReadU32(memory, symtab + i*24 + 0)
value     := MemoryManager_ReadU64(memory, symtab + i*24 + 8)
size      := MemoryManager_ReadU64(memory, symtab + i*24 + 16)
```

The docs/107 single-struct overlay **cannot express the `i*stride` indexing** — its accessor is
`base.field[mem]` with no per-element displacement. So the hand-written `+ i*24 + off` arithmetic
stays unchecked: a field offset that runs past the entry stride silently bleeds into the next
record, and a stride literal that drifts from the real `sizeof(Elf64_Sym)` is invisible. This is a
classic over-read surface (a bad index runs off the table, a bad offset straddles two entries).

This document adds a **fixed-stride table overlay** as a minimal extension of docs/107 — same
carrier, same registry, same `AsOverlayCall` lowering hook, same diagnostic family.

## Surface syntax

A layout gains an optional **stride** in its header:

```
layout Elf64Sym stride 24:
    0  name:  u32
    8  value: u64
    16 size:  u64
```

`stride N` and `size N` are **mutually exclusive** header forms. `size N` (docs/107 increment 2)
declares the byte size of a single struct; `stride N` declares the per-element byte size of a
**packed array of records**. A layout written with neither falls back to field-derived bounds, as
before. (Parsed by the EASM layout header — the same registry docs/107 reads from; see
*Status* for where the in-source `layout` declaration lands.)

The accessor for reading entry *i*'s field is a **two-operand index**:

```elisa
def sym_value(table: GuestVAddr[Elf64Sym], memory: uintptr, i: u64) -> u64:
    return table.value[memory, i]
```

`table` is a `GuestVAddr[Elf64Sym]` carrier whose layout declares a stride, `memory` is the
`MemoryManager` to read through, and `i` is the entry index. This mirrors the docs/107 read form
`base.field[mem]` with the entry index added as a second subscript.

## Desugar

`table.value[memory, i]` over a stride-24 layout where `value` is at offset 8 lowers to:

```elisa
MemoryManager_ReadU64(memory, table.cast[uintptr]() + i*24 + 8)
```

The address is built as `((base.cast[uintptr]) + (i * stride)) + offset`:

- `i * stride` is the **dynamic** entry displacement,
- `+ offset` is the **static** field offset within an entry (omitted when the field is at offset 0),
- the `cast[uintptr]` on the carrier is the docs/107 §(e) ABI-neutral no-op (a guest carrier already
  lowers to a raw guest address).

The desugar reuses `IndexExpr.AsOverlayCall`: the analyzer stamps the lowered `CallExpr` onto the
index node, and the backend emits that call instead of an index — byte-identical to the hand-written
table read. The backend hook is **unchanged from docs/107** (it just emits whatever `AsOverlayCall`
carries), so the stride arithmetic flows through with no new codegen.

### Mechanics

- AST: `IndexExpr.Index2` holds the optional second subscript. It is non-nil **only** for this
  accessor; the analyzer rejects any other two-operand index (`overlay-table-index-invalid`).
- Parser: the postfix `[` handler accepts a trailing `, <expr>` into `Index2`. A field-access
  operand `x.field[...]` is explicitly excluded from generic-function value specialization (`fn[A,B]`)
  so `table.field[mem, i]` routes to the index form, not a `SpecializeExpr`.
- Analyzer: `tryDesugarGuestOverlayTableIndex` recognizes `FieldExpr` object + bare-ident `mem` +
  `Index2`, requires a registered `GuestVAddr[L]` carrier whose layout has `Stride > 0`, resolves the
  field against the stride, and builds the lowered call.

## What is checked statically vs left to runtime

**Static (compile-time):**

- **field-in-stride** — `offset + width <= stride`. A field that ends past the entry stride is
  rejected as `overlay-field-exceeds-stride` (the over-read into the next entry). This is the
  stride-array counterpart of docs/107's struct-size bound.
- **unknown-field** — `overlay-unknown-field`, as docs/107.
- **width has a ReadU<N> form** — a non-1/2/4/8 width is `overlay-field-width-mismatch`, as docs/107.
- **two-operand-index validity** — a two-operand index is accepted *only* as this table accessor;
  anything else is `overlay-table-index-invalid`.

**Left to runtime (caller's responsibility):** the **entry-count bound** `i < entry_count`. The
table's element count is dynamic (read from ELF section headers at load time), so it is not knowable
statically. The accessor checks that a field lands *within* an entry; it does **not** check that
entry *i* is within the table. A dominating `if i < count:` guard is the caller's obligation today,
exactly as the raw `+ i*24` code requires it today — the overlay does not weaken that, but in this
prototype it does not yet *enforce* it either.

### Future work — `requires count > i` obligation

docs/107 increment 4 discharges a `requires size >= N` field obligation against a dominating
`if size >= N:` guard. The analogous table obligation — a `requires count > i`-style annotation
discharged by a dominating `if i < count:` guard — would close the entry-count hole the same way.
That reuses the existing size-guard dataflow (dominating-guard fact derivation) but needs the count
value threaded as a sibling of the carrier, which is a larger change than this prototype. It is left
as documented future work; the prototype keeps to the static field/stride checks.

### Future work — write form

This prototype is **read-only**: `table.field[mem, i]` lowers to `MemoryManager_ReadU<N>`. The write
form `table.field[mem, i] = v -> MemoryManager_WriteU<N>(mem, base + i*stride + offset, v)` is a
mechanical mirror (docs/107 already has the single-struct write path via `AssignStmt.AsOverlayCall`)
but is not wired here — guest tables (symbol/relocation tables) are overwhelmingly read surfaces, so
the read path captures the fault cluster.

## How it composes with docs/107

| | docs/107 single-struct | docs/108 fixed-stride table |
|---|---|---|
| Carrier | `GuestVAddr[L]` | `GuestVAddr[L]` (same) |
| Layout header | `layout L size N:` (or size-less) | `layout L stride N:` |
| Accessor | `base.field[mem]` | `table.field[mem, i]` |
| Address | `base + offset` | `base + i*stride + offset` |
| Bound | `offset+width <= size` | `offset+width <= stride` |
| Lowering hook | `IndexExpr.AsOverlayCall` | `IndexExpr.AsOverlayCall` (same) |
| Backend | unchanged | unchanged |

The two forms are disjoint: a layout has either a `size` (single struct) or a `stride` (table). The
single-operand accessor desugar bails when `Index2` is set, and the table desugar requires
`Stride > 0`, so neither path can fire on the other's input. Both resolve fields against the **same**
`easm.Layout` description the EASM-side `N(%base)` reads use, so boot-assembly reads, high-level
single-struct reads, and high-level table reads cannot drift.

## Status

Prototype, in the spirit of docs/107 increment 1 — minimal, correct, tested, honestly scoped.

**Landed:**

- `easm.Layout.Stride` + `layout Name stride N:` header parsing (EASM registry).
- `IndexExpr.Index2` AST field (+ clone support) and the parser two-operand index form, with the
  field-access / generic-specialization disambiguation.
- `tryDesugarGuestOverlayTableIndex` analyzer desugar to `ReadU<N>(mem, base + i*stride + offset)`,
  with `overlay-field-exceeds-stride` / `overlay-unknown-field` / `overlay-field-width-mismatch` /
  `overlay-table-index-invalid` diagnostics.
- Backend reuse of the docs/107 `AsOverlayCall` hook (no codegen change).
- Tests: EASM stride-header parse; semantic desugar + zero-offset + rejection (field-past-stride,
  unknown-field, two-operand-over-non-table); backend LLVM IR verification of the `i*24 + 8`
  address math and the `MemoryManager_ReadU64` call.

**Not yet (documented above):** runtime `requires count > i` obligation; the write form; in-source
`layout` declaration wiring through the CLI (today, as in docs/107 increment 5a, the overlay layout
registry is supplied via `AnalyzeOptions.OverlayLayouts` / the EASM module, not yet from in-source
Elisa `layout` decls — when that lands for docs/107 it carries docs/108's `stride` for free, since
the header is parsed by the same regex).
