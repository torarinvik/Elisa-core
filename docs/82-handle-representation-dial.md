# 82 — Handle representation dial: `layout(handle: …)`

Status: design locked 2026-06-10. Extends docs/76 (enum layout & handles) with the
`handle:` layout option, and amends docs/81 (tree/enum unification) at its §3b/§3f
plug points. Implementation rides the docs/81 Phase 3 work.

## The decision, in one sentence

**Handle representation is a declaration-site layout option — `layout(handle: u8 | u16 |
u32 | u64 | ptr)` — that changes bytes-per-edge and nothing else: payload declarations,
the opaque-handle rule, region inference, and the docs/81 tag-range codegen are
identical at every setting.**

## Surface

```elisa
enum Node:                            # default: index handle, u32 (docs/76)
    IntLit(value: i64)
    Add(left: Node, right: Node)

enum Node layout(handle: u16):        # dense bounded store — 2 bytes per edge
enum Node layout aos(handle: u64):    # giant store (mode optional when defaulted)
enum Node layout(handle: ptr):        # pointer edges — expert dial (§ptr below)
enum Node layout soa(handle: u32):    # columnar, same dial
```

- `handle:` lives inside the existing `layout` suffix — one vocabulary, no second
  suffix slot, no ordering questions.
- The mode (`aos`/`soa`) may be omitted when options alone are given:
  `layout(handle: u16)` keeps the default mode.
- The Phase-1 `index:` spelling (docs/76) has been removed; use `handle:`.
  Two keys for one dimension was soup; `handle` is the honest name once `ptr`
  joins the value space (a pointer is not an index).

### The invariant: payloads never change across the dial

```elisa
# This line is IDENTICAL at u16, u32, u64, and ptr:
    Add(left: Node, right: Node)

# NEVER this:
    Add(left: Node& @r, right: Node& @r)    # ✗ rejected
```

The child edge is always the bare enum name; `layout` chooses what the edge *is*. The
handle stays opaque at every setting — no arithmetic, no visible integer, no extractable
raw `&`. Opacity is what makes the dial a one-line edit, keeps serialization a
representation no-op (for index handles), and keeps the interior-borrow checker sound.

## Integer widths (`u8`–`u64`)

Unchanged from docs/76: node-index (not byte offset), free null sentinel at the top
value per width, loud overflow panic at the allocation site, auto-narrowing only from
a local `reserve_commit(N)` (never distant usage). The only delta is the spelling
(`handle:` over `index:`).

## `handle: ptr`

What it buys:

1. **No store threading on reads.** An index is meaningless without its store base, so
   consumers of a recursive enum receive an implicit store parameter
   (`computeTransitiveStoreNeeds`). A pointer is self-contained: `eval(e: Node)`
   compiles with zero implicit parameters. Real ABI simplification for libraries
   exposing AST-consuming functions across module boundaries.
2. **Cross-store edges become legal.** An index answers "which row of *my* store"; it
   cannot reference a node in another store. A pointer can — this is the cross-region
   forest case docs/76 reserved `handle: pointer` for, and the principled reason the
   dial exists (not perf: index-AoS 0.38s vs pointer-struct 0.40s is a wash).
3. **Free null from the hardware** (address 0; no reserved top value).
4. One less multiply-add per child hop (`load ptr` vs `base + i*stride`).

What it costs — enforced as compile errors, not footnotes:

1. **Stable-address backing required.** The AoS chunk store never relocates → sound.
   SoA's darray-backed columns relocate → `layout soa(handle: ptr)` is **rejected at
   declaration**.
2. **No freeze/serialize.** Pointers are not position-independent.
   `freeze(move store)` on a ptr-handle store is a compile error naming the one-line
   fix: *"pointer handles are not position-independent; use an index handle
   (`layout(handle: u32)`)"*. The default graduates to serialization for free; the
   expert who took the pointer dial is told exactly what they traded.
3. **Escape checking unchanged.** The pointer is region-allocated; the existing
   outlives/escape machinery applies verbatim because the handle is opaque — it cannot
   be laundered past the checker.

## Rejected: `enum Node[@r]:` with `Node& @r` payloads

Re-affirms docs/76 §"Why not a region parameter on the type"; both reasons bind harder
for hierarchies:

1. **The threading wall.** `Node[r]` as a type forces `[@r]` onto every function
   that touches one, transitively — the exact wall docs/75 region-polymorphism removes.
2. **Provenance splits type identity.** `Node[a]` and `Node[b]` would be byte-identical
   types that can't flow into the same darray/function/match. docs/10 forbids it.

What the want actually resolves to: `new[r] Node.Add(...)` places into a named region;
inference threads it; the explicit-store path (`Node.Store(arena)`, `in store:`)
remains the fully-manual escape hatch. The region is provenance carried by the value,
never a parameter on the type. And raw `Node&` in payloads breaks handle opacity —
killing the one-line dial, free serialization, and borrow-checker soundness at once.

## Composition with docs/81 (why this is a dial, not soup)

The docs/81 codegen primitive never asks what a handle is. Representation differs in
exactly two backend functions; range tests, category narrowing, and upcast-as-no-op are
unchanged:

```go
// The ONLY two representation-aware operations:
func emitHandleToAddr(h llvm.Value, root *EnumType) llvm.Value {
    switch root.HandleKind {
    case HandleU8, HandleU16, HandleU32, HandleU64:
        return gep(storeBase(root), mul(zext(h), stride(root)))  // base + i*stride
    case HandlePtr:
        return h                                                 // it IS the address
    }
}
func nullSentinel(root *EnumType) llvm.Value
    // uN: top value per width; ptr: null — uniform "free absent" everywhere

// Unchanged at every dial:
addr := emitHandleToAddr(e, root)
tag  := emitLoadTag(addr)
cond := emitTagRangeTest(tag, cat.LeafTagLo, cat.LeafTagCount)   // tag - lo <u count
```

`enum Statement is Node` + `layout(handle: u16)` + `if e is Statement s:` stack with no
pairwise special cases.

Plug points in docs/81 Phase 3:
- **3b (record shape):** stride/edge width read `Root.HandleKind`; the dial is a
  root-level fact (a sub-category cannot pick its own width — one store, one shape).
- **3f (store threading):** `computeTransitiveStoreNeeds` skips ptr-handle roots for
  read-only consumers (construction still threads the region).

## Implementation order

1. **DONE (2026-06-10).** `handle:` option parsing on the `layout` suffix (`index:` kept
   as silent alias for now); mode-less `layout(handle: uN)` form (keeps the default
   mode — `validateEnumLayout` accepts `StructLayoutDefault` with a width); formatter
   renders the suffix (it previously dropped `layout` on enums entirely); `handle: ptr`
   is a parse error naming this doc.
2. **DONE (2026-06-10).** Integer widths end-to-end: `lowerPackedEnumType` derives the
   handle's LLVM type from `Root().ResolvedIndexWidthBits()` (root-level fact — the
   whole hierarchy shares it); `coerceValue` gains the integer↔handle boundary resize
   (zext/trunc, no-op at default width), which the existing store ops (`aosRecordPtr`,
   `storeTagAt`, payload reads) already route through; `emitHandleOverflowGuard` traps
   at the allocation site when a narrowed store's index reaches the width's null
   sentinel (u8 chain >254 nodes verified to trap, never wrap). Default-u32 IR is
   unchanged (resize no-ops, guard skipped at ≥32 bits).
   *Scoping note (corrected 2026-06-11):* the dial sets BOTH the value/ABI width and the
   in-record edge width — payload slots are typed by the lowered field types, so a u16
   tree's `Node(left, right)` payload is `{i16, i16}` and the whole row shrinks (16 B at
   u16/u32 vs 24 B at u64 for the canonical Tree; the i64 `Leaf` floors the union).
   Pinned by TestHandleDialShrinksRecordRows. The earlier "word-sized slots" caveat
   described the word-granular read helpers, not the layout — tight-payload was already
   real.
3. **CORE DONE (2026-06-10).** `ptr` kind: the handle is the record address carried as a
   uintptr-width integer (so all width machinery — coercion, switches, phis — applies
   unchanged; `ResolvedIndexWidthBits()` reports 64). Exactly two codegen sites are
   representation-aware, as designed: the AoS alloc emits `ptrtoint(record)` as the
   handle, and `aosRecordPtr` emits a bare `inttoptr` — no store-state call, no
   `ctx_aos_store_record` helper — which every AoS read (tag, payload word, range test)
   routes through. Null sentinel = 0 (hardware null); the overflow guard is skipped
   (an address can't overflow its own width). Compile errors enforce the contract:
   `layout soa(handle: ptr)` rejected (columns relocate), ptr on a non-recursive value
   enum rejected (no store record to point at), and `freeze(move store)` rejected with
   the one-line fix ("use an index handle (`layout(handle: u32)`)").
   **Tail DONE (2026-06-10):** the store-threading seed for ptr-handle enums is
   *construction*, not signature exposure — `computeTransitiveStoreNeeds` seeds ptr
   roots from a syntactic constructs-walk, `injectInferredPackedStoreParams` skips
   ptr roots entirely, matching a storeless ptr handle is legal (codegen uses a
   synthetic undef binding no read path can touch). IR-verified: `total(t: Tree)`
   lowers to `@total(i64)` with zero implicit params; a builder still threads
   region + store. This is the §ptr point-1 ABI win, delivered.
4. **DONE (2026-06-11).** The `-Wperf` width lint fires on constant-bounded constructor
   loops in region-owning functions (suggests u8/u16; any call in the loop mutes it).
   The free-null niche for optional children is implemented for AoS roots: a `Tree?`
   payload field is stored as the BARE handle with the width's null sentinel meaning
   absent (no presence flag in the record — `{handle, handle}` payloads); the generic
   `{bool, handle}` carrier remains the ABI outside the record, converted exactly at
   the constructor-write / payload-read boundary. `index:` has been removed.
   Known gap: an optional-only self-reference (`next: Tree?` with no bare edge) does
   not yet promote the enum to region-backed (computeRecursiveEnumSet counts only bare
   payload types).
