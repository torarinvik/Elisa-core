# 81 — `tree`/`enum … is` unification roadmap

Status: design (2026-06-10). Unification is the declared end-state in docs/77 §6 ("Phase 6
— Retire `tree`") and docs/75 §1 ("tree reframed as sugar over region-poly returns").
This note records the strategy for getting from the current two-subsystem state to one.

---

## §0 — Current state

### What docs/77 has landed on `work`

| Phase | Commits | Delivers |
|---|---|---|
| 1 | `eddb008a` | `enum Child is Parent:` parse; `EnumType.Parent`; `Child <: Parent` (`enumDescendsFrom`) in assignability; formatter |
| 1b | `93762815` | `EnumType.Children`; `enumDescendantLeaves` (leaves flow up); exhaustiveness over descendant union |
| 2a | `e4f03f76` | `assignHierarchyEnumTags` — dense, hierarchy-grouped leaf numbering; `EnumType.LeafTagLo/LeafTagCount`; unified `EnumVariant.Tag` |
| 2b (partial) | `6018181b`, `b4ecdacf`, `91a8898e` | arm narrowing for refinement arms; `is` refinement-variant test; bare-category `is` test (analysis) |
| 3 (core) | `ad249c42`, `00414c70`, `1a6d2974`, `b3975ffb` | value hierarchies RUN (inline); recursive hierarchies RUN (one store per root, `packedStoreKey = Root().Name`); cross-function per-root threading; `common(...)` on hierarchies |
| 3e (this doc) | — | bare-category `is` CODEGEN: `emitTagRangeTest` (`tag - lo <u count`) + `emitEnumCategoryIsTest` |

So §2 below (status as of first draft) is partly shipped already. Remaining: category
match arms (`Statement s:`), the `is`-binding form, Phase 4 column scan, Phase 5
defaults, Phase 6 tree retirement, docs/82 handle dial.

**UPDATE 2026-06-10: Phase 6 EXECUTED (commits 17a99a76, dea019b9, 55099e6f) — `tree`/`node`
retired, 16 816 lines deleted.** No parse-time desugar was needed: no tracked `.elisa` used
trees, and breaking the untracked frontends was accepted. `tree`/`attribute` declarations are
now parse errors pointing here; `visit`, tree `fold`, rewrite-default, `treeview`,
`children()`/`kind`, tree freeze/clone, and `@derive(parse_builder)` are gone with them.
Sequence rewrite, fold comprehensions, grammar lowering, and the packed-enum machinery are
unaffected. docs/82 (handle dial incl. `ptr`) also landed. The one-construct end state holds:
`enum … is`. Phases 4 (column scan) and 5 (defaults/value-hierarchy tails) remain as
enum-side feature work, no longer blocked on tree parity.

### The two parallel subsystems (the problem)

`tree`/`node` and `enum … is` are entirely separate stacks right now:

```
tree/node surface                   enum … is surface
────────────────                    ─────────────────
TreeDecl / TreeBlockDecl (AST)      EnumDecl (AST, existing)
TreeType / TreeCategoryType         EnumType{Parent, Root, LeafTagLo, …}
TreeStoreType (per-category tables) no store yet (Phase 3 = the gap)
TreeLayout (4 modes: PerVariant,    layout soa / RecursivePlain flags
  CatUnion, AOS, SOA)               on EnumType
TreeVariantViewType                 PackedVariantViewType
treeCategoryDescendsFrom()          enumDescendsFrom()
analyzer_decl_trees.go (518 L)      analyzer_decl_enums.go (partial)
tree_store_implicit.go (535 L)      analyzer_packed_store_implicit.go
llvm_tree_*.go (~5 272 L backend)   llvm packed-enum backend (existing)
```

The `tree` store is **not** a single AoS arena: `ensureTreeStoreStateType` builds
a carrier over **N per-category tables** (`TreeLayoutCategoryUnion` default). The fast
unified-AoS path (`TreeLayoutAOS`) is the **packed-enum** path
(`packedEnumABIAoS`/`RecursivePlain`).

Therefore Phase 3 is not "bridge tree's store to enum" — it is "build the unified
single-AoS-arena store on the packed-enum path and let the enum hierarchy use it."
The tree backend code is a **reference for correctness**, not a foundation to reuse.

---

## §1 — The unification critical path

```
2b  Surface sugar (analysis-only, no runtime)
3   Unified store + codegen  ← load-bearing gate; nothing runs until here
4   Hierarchy column scan
5   Value hierarchies (non-recursive = inline value, no store)
6   Retire tree              ← delete ~6 000 lines
```

No phase depends on completing the tree retirement, so 2b–5 can land incrementally
while the tree system keeps running. Phase 6 is the final delete.

---

## §2 — Phase 2b: surface sugar (analysis-only)

Pre-requisite for Phase 3 codegen correctness, low risk.

**2b-1. Unqualified leaf arms.** In a `match` on a hierarchy type, allow bare `Add(l,
r):` without the `BinaryExpression.` qualifier. Route the existing
`MatchStructPattern` at the top level of an enum match to variant resolution via
`EnumType.Root.AllVariants`. Ambiguity rule: if a bare name matches a variant in the
scrutinee's type, it is that leaf; otherwise it is a sub-category arm (2b-2) or a
binding.

**2b-2. Category arm `Child b:`.** A name that resolves to a sub-enum of the
scrutinee's type (and is followed by a binding identifier) narrows the scrutinee to
that sub-category and binds `b` at the narrowed type. Reuse `bindRefinedExprType` (the
guard-condition narrowing machinery already used by `if e is …`).

**2b-3. Scrutinee narrowing in arm bodies.** After entering a `BinaryExpression b:`
arm the static type of `b` inside the body is `BinaryExpression`, not the wider
`Expression`. This piggybacks on the existing `bindRefinedExprType` path.

---

## §3 — Phase 3: unified store + codegen

This is the largest single item. The sub-slices in implementation order:

### 3a — Per-root store identity

**Change:** `getOrCreateRegionPackedStore` / `lookupPackedStore` (semantic) resolve by
**hierarchy root** (`EnumType.Root`), not by the enum itself. A leaf constructor
`BinaryExpression.Add(…)` asks for the root's store; a handle of type
`BinaryExpression` and a handle of type `Expression` index the same runtime store.

**Code target:** `analyzer_packed_store_implicit.go`. The existing `RecursivePlain`
shortcut already does `lookupPackedStore` on a flat recursive enum — extend it to
walk `EnumType.Root` first. The store key becomes `Root.Name`.

**Test gate:** `BinaryExpression.Add(l, r)` in a region-poly function compiles to a
`store.put()` call against the root store (no change to the ABI wire format, just the
key).

### 3b — Record shape = root union

**Change:** `ensurePackedEnumRowType` (backend) for the root sizes the record payload
to the **widest leaf across the whole hierarchy** (not just this enum's variants).
`common` fields are placed at fixed offsets (as they already are for flat packed enums).

Walk: `EnumType.Root → AllVariants()` (the `enumDescendantLeaves` list, already
computed in Phase 1b). The widest leaf across the tree determines `maxPayloadBytes`.

**Layout note:** AoS is the default (same as `RecursivePlain`). `layout soa` on the
root is the opt-in for column-dense access (Phase 4).

### 3c — Construction codegen

A leaf constructor `BinaryExpression.Add(l, r)` emits against the root store with the
unified leaf tag from Phase 2a (`EnumVariant.Tag`). Static result type = the leaf's
declaring enum (`BinaryExpression`); runtime value = bare `u32` index into the root
store. No change to the register/handle width.

The unqualified form (Phase 2b-1) routes through the same path after the name is
resolved to the leaf.

### 3d — Match dispatch codegen

`match e` (where `e: Expression`) reads the unified tag from record field 0 (same
offset as today's flat packed enum) and switches:
- **Leaf arm** `Add(l, r):` → exact tag match.
- **Category arm** `BinaryExpression b:` → unsigned range check
  `tag - LeafTagLo < LeafTagCount` (the Phase 2a range, branchless).
- **`_:`** → default.

This extends the existing packed-enum match codegen (already handles tag-switched
dispatch) by adding the range-check branch type. The existing
`emitPackedEnumMatchExpr` (or equivalent) gains a new arm kind.

### 3e — Upcast / `is` / downcast codegen

**Upcast `BinaryExpression → Expression`:** typed no-op (same bits, no instruction).
The backend emits the wider type annotation but no conversion. This is already
implicit in handles today (a `u32` is a `u32`).

**`if e is BinaryExpression b:`** (expression-position):
1. Load tag from the record (1 load, likely already in a register from a nearby match).
2. Range check `tag - LeafTagLo < LeafTagCount` for `BinaryExpression`'s range.
3. Bind `b` at type `BinaryExpression` in the branch body.

Reuse the existing `OptionalBindExpr` lowering where the condition is a range check
rather than a null check. The `FromIs: true` path from docs/80 Phase A already threads
the bind into the branch body — the only delta is the condition computation.

### 3f — Store threading per root

`computeTransitiveStoreNeeds` (semantic) uses the **root** as the store key. N
mutually-recursive `enum … is` types that all share one root → one implicit store
parameter instead of N. `funcOwnsRegion` per-region-instance logic is unchanged
(commit `cef50ce7` already fixed this for flat recursive enums; it applies unchanged
because the root is still one type).

### 3g — `common` reads from any handle

A `common` field `span` declared on the root `Expression` must be readable from a
bare `Expression` handle without knowing the leaf. The shared-column prefix mechanism
already works for flat packed enums (the tag is at offset 0, common fields follow at
fixed offsets). Verify it works across the hierarchy depth via a test with a two-level
chain and a `common`-field read on the root type.

---

## §4 — Phase 4: hierarchy column scan

`T of .field` well-formedness extended to "`.field` must exist on every leaf in `T`'s
tag-range." Auto-true for `common` fields. For payload fields, auto-true only if every
leaf in `T`'s range has that field (warn otherwise). `layout soa` on the root makes the
column dense; payload scans over AoS are strided (documented trade-off).

No new runtime mechanism: the existing `of .field` scan already works on flat packed
enums; this is a well-formedness gate + range-aware stride computation.

---

## §5 — Phase 5: value hierarchies

A hierarchy where no variant payload contains a **self-referential handle** (no leaf
field of type `Node` where `Node` is an ancestor) → the hierarchy is non-recursive
and needs **no store**. It is an inline `{tag, payload-union}` value, exactly like a
plain flat enum.

Detection: DFS on variant payloads from the root; if no payload reaches the root type,
it is a value hierarchy.

Layout:
- Non-recursive → inline `{u32 tag, payload union}` value (no store, no region
  threading).
- Recursive → root-store (Phase 3 path).

`RecursivePlain` detection already does this DFS for flat enums
(`analyzer_decl_types.go:126`). Generalize to walk descendants.

---

## §6 — Phase 6: retire `tree`

The design decision in docs/77 §6 is "delete `tree`/`node`, not keep as sugar." This
note confirms that decision: the surface is small and its mappings are mechanical.

### Migration map: tree → enum is

| `tree` concept | `enum … is` equivalent |
|---|---|
| `tree Node:` | `enum Node: pass` |
| `node Expr:` inside `tree Node:` | `enum Expr is Node:` |
| nested `node Assignment:` inside `node Statement:` | `enum Assignment is Statement:` |
| variant line inside a `node` | a leaf variant of that sub-enum |
| `common(span: Span, …)` | `common(span: Span, …)` on the root (field already exists on `EnumType.Common`) |
| `block Block:` (struct-like member) | inline `struct`; reference by a plain variant payload (`Block(fields: …)`) |
| `struct ElseIf:` member | same as `block`: plain leaf variant with named fields |
| `KindType` (const enum of tag names) | `EnumVariant.Tag` (u32); no separate const enum; the `kind` field read maps to a tag read |
| `TreeLayout{PerVariantRows, CatUnion}` | removed (Phase 3 AoS is the only layout) |
| `TreeLayout{AOS, SOA}` | `layout soa` opt-in (Phase 4); default = AoS |
| `freeze(move store)` | `freeze(move store)` on the packed store (already exists for packed enums) |
| `T of .field` column scan | `T of .field` (Phase 4) |
| `treeCategoryDescendsFrom` | `enumDescendsFrom` |

**`block`/`struct` members**: `TreeBlockType`/`TreeStructType` have no usage in any
`.elisa` source file (only in Go test type construction). Retirement is a hand-migration
of those Go test fixtures plus deletion of the AST nodes.

### Files to delete in Phase 6

**AST:**
- `ast.TreeDecl`, `ast.TreeBlockDecl`, `ast.TreeCategoryDecl`, `ast.TreeStructDecl`,
  `ast.TreeMemberDecl` (in `ast_node_to_structdecl.go`)

**Semantic:**
- `types_tree.go` (130 L): `TreeType`, `TreeCategoryType`, `TreeNodeType`,
  `TreeStoreType`, `TreeBlockType`, `TreeStructType`, `TreeVariantViewType`,
  `FrozenTreeRowsViewType`, `TreeLayout`, `TreeFieldTemperature`
- `tree_store_implicit.go` (535 L)
- `analyzer_decl_trees.go` (518 L)
- `types_tree_surface.go`
- `tree_attributes.go`
- `analyzer_flow_tree_visit_core.go`
- `treeCategoryDescendsFrom` from `types_assignability.go`
- `TreeCategoryType`/`TreeNodeType`/`TreeBlockType`/`TreeStructType` branches across
  all `switch t.(type)` dispatches (types_compare.go, types_stringers.go, typeid.go,
  static_reflection.go, analyzer_type_traversal.go, analyzer_substitute_types.go, …)

**Backend:**
- `llvm_tree_runtime_*.go` (~2 646 L)
- `llvm_tree_freeze.go` (624 L)
- `llvm_tree_fold_*.go` (~1 284 L)
- `llvm_tree_attribute.go` (566 L)
- `llvm_tree_switch_helpers.go` (28 L)
- `llvm_generatellvmir_to_flattenllvmtreememberdecls.go`
- Tree-specific branches in `llvm_bodies_*` and `llvm_exprs_*` files (scattered)

**Parser:**
- `parser_tree_test.go` (tree parser tests)
- Tree production in `parser_parsequalifieddeclname_to_parsefuncgenericparams.go`

Estimated deletion: **~7 500–9 000 lines** (semantic ~1 800 + backend ~5 200 + parser
~500). The remaining packed-enum backend handles hierarchies transparently.

### Migration order for Phase 6

1. Add a `@deprecated` annotation on `tree`/`node` keywords (one-sprint warning period).
2. Desugar `tree`/`node` at parse time into `enum … is` AST nodes (keeping semantic
   behaviour identical via Phase 3 store path).
3. Verify full test suite green on the desugared path.
4. Delete the old AST nodes, semantic types, and backend files.
5. Migrate Go test fixtures that build `TreeBlockType`/`TreeStructType` directly into
   equivalent `EnumType` constructions.

---

## §7 — Decisions to lock before Phase 3

1. **`block`/`struct` member migration strategy.** The two options are: (a) introduce a
   struct-shaped leaf variant in `enum … is` as the analogue, or (b) hand-migrate the
   handful of test usages and drop the feature. Given zero `.elisa` usage, **(b) is
   preferred**: fewer surface concepts, cleaner retirement. Confirm before Phase 6.

2. **`kind` field surface.** The tree system exposes a synthetic `kind` field on every
   node (backed by the const-enum KindType). `enum … is` has no equivalent sugar;
   callers read the tag via a match arm. Decision needed: provide `n.kind` sugar over
   the tag on the root type (consistent with tree usage) or drop it. Lean: provide it
   as a read-only field of type `u32` on the root — one line in `analyzeFieldExpr`,
   no new type node.

3. **`layout aos` default vs opt-in.** Phase 3 defaults to AoS (matching
   `RecursivePlain`). If any existing tree hierarchy has an explicit `layout
   per_variant_rows`, it must either be migrated to AoS or kept as `layout soa` — the
   per-variant layout has no enum equivalent and won't be ported. Check before Phase 6.

4. **Serialization stability.** If the self-hosting toolchain ever caches compiled
   ASTs, the dense tag range is not stable across insertions. Decide before Phase 3
   lands in the self-hosting path whether to decouple the dense range from an
   on-disk stable tag (`= N` explicit assignment). Not urgent for pure-compilation
   workloads.
