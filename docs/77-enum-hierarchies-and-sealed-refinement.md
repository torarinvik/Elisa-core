# 77 — Enum hierarchies & sealed refinement (`is`)

> Status: design. **This doc is the source of truth for how an `enum` is split into type-safe
> sub-categories that share one unified arena.** It unifies the `tree`/`node` feature into `enum`,
> builds directly on docs/76 (enum layout & opaque index handles), docs/75 (region-polymorphic
> functions), docs/74 (region-backed packed enums), and the just-shipped first-class column scan
> (`X of .field`, docs/76 §5). It supersedes `tree`/`node` as a *separate* type system construct.

## The decision, in one sentence

**An `enum` may be *refined* into sealed sub-categories with `is` (`enum BinaryExpression is
Expression`); the whole hierarchy rooted at one declaration shares a single region-backed arena and
tag space, so a sub-category is a zero-cost, type-safe restriction of its parent — you get the
unified-arena win of "one giant enum" *and* the type safety of distinct node categories.**

`tree Node:` / `node Expr:` becomes **sugar** that desugars to exactly these `enum … is …` types.
There is one construct, one store path, one exhaustiveness checker.

## Why

Compilers want a unified arena for the AST: one contiguous store, index handles instead of pointers,
cache-friendly, relocation-stable, serializable (docs/76 proves this is the fast path — 0.38 s/22 MB
for the recursive-enum AoS store). The conventional way to get it is **one giant enum**:

```elisa
enum Node:
    Add(left: Node, right: Node)
    IntLit(value: i64)
    Return(value: Node)
    While(cond: Node, body: Node)
    Assign(target: Node, value: Node)
    # ...50 more
```

This is unmanageable and **type-unsafe**: every function takes `Node` and must defensively handle
every case, because the type system cannot say "this function only accepts statements." You lose the
single most useful AST invariant — *what kind of node is allowed here* — exactly when the AST is big
enough to need it.

Sealed refinement keeps the unified arena and recovers the type safety:

```elisa
enum Node: pass                          # the root = the whole node universe (one arena)
    enum Expr is Node:                   # a sub-category: Expr ⊆ Node
        Add(left: Node.Expr, right: Node.Expr)
        IntLit(value: i64)
    enum Statement is Node:
        Return(value: Node.Expr)
        While(cond: Node.Expr, body: Node.Statement)

def check(s: Node.Statement) -> Type:    # accepts ONLY statements — a compile-time guarantee
    match s:
        Return(value): ...
        While(cond, body): ...
```

Prior art: this is the **sealed hierarchy** of Scala 3 / Kotlin / Java 17, and the
**single-Tag-enum + arena** of Zig's AST (`MultiArrayList` + `Ast.Node.Tag`). The novelty here is
doing the sealed sub-category split *over a flat index-handle arena* so that `Node.Statement <:
Node` is a tag-range, not a heap object with a vtable. No production language ships that combination.

## §1 — The surface

### Declaration: `is` for refinement (subset = subtype)

`enum Child is Parent:` declares `Child` as a **sub-category** of `Parent` — `Child`'s cases are a
**subset** of `Parent`'s. The root has no parent; an empty root that only gathers sub-categories is
written `enum Root: pass` (or, sugar permitting, `enum Root:` with only `is`-children).

```elisa
enum Expression: pass
    enum BinaryExpression is Expression:
        Add(left: Expression, right: Expression)
        Mul(left: Expression, right: Expression)
    enum Literal is Expression:
        Int(value: i64)
```

**Deep chains** are just longer `is` relations and read as plain English:

```elisa
enum Statement is Node:
    Return(value: Expression)

enum Assignment is Statement:            # Assignment is Statement is Node
    Let(name: Sym, value: Expression)
    Set(target: Expression, value: Expression)
```

A node may declare **both its own leaf variants and sub-categories**; its membership is the union of
own-leaves ∪ all descendants' leaves (leaves *flow up*). So in:

```elisa
enum Expr is Node:
    IntLit(value: i64)                   # own leaf
    enum BinOp is Expr:                  # sub-category
        Add(l: Expr, r: Expr)
        Mul(l: Expr, r: Expr)
```

`BinOp = {Add, Mul}`, `Expr = {IntLit, Add, Mul}`, and `BinOp <: Expr <: Node`.

### Why `is`, not `extends`

For **product** types (structs/classes) adding a field makes a *subtype* (`Dog extends Animal`: more
fields, fewer instances, `Dog <: Animal`). For **sum** types (enums) adding a case makes a
*supertype* (more alternatives, more instances). So OO `extends` (add ⇒ subtype) points the **opposite
way** for enums, and reusing it invites a systematic reversal error ("I'll add cases to the child").
`is` reads as the Liskov relation and cannot be reversed: **`BinaryExpression is Expression`** can
only mean "a BinaryExpression *is an* Expression," i.e. `BinaryExpression <: Expression`. The
direction rule, stated once:

> **More cases = bigger type = supertype. The subtype is always the smaller case-set. `is` always
> points from the subset up to the union.**

### Sugar: `tree` / `node`

The existing `tree`/`node` block desugars 1:1 and remains as the AST-flavored surface (nesting makes
direction unambiguous — inner is more specific):

| Surface | Desugars to |
|---|---|
| `tree Node:` | `enum Node: pass` |
| `common(…)` / `common:` block | fields hoisted into every leaf record (§4) |
| `node Expr:` | `enum Expr is Node:` |
| nested `node Assignment:` inside `node Statement:` | `enum Assignment is Statement:` |
| a variant line inside a `node` | a leaf variant of that sub-enum |

`Node.Statement` (qualified) and bare `Statement` are the **same type**; the dotted form is scoped
naming.

### Reserved for later: `includes` (bottom-up)

The *inverse* ergonomic — declare a base, then accumulate a richer superset — is a real but secondary
want. If added, it gets its own keyword so the direction stays unambiguous; **never** reuse
`extends`/`is` for it:

```elisa
enum Monochrome:
    Black; White
enum Color includes Monochrome:          # Color ⊇ Monochrome ⟹ Monochrome <: Color
    Red; Green; Blue
```

Ship `is` (top-down refinement) first; add `includes` only if usage demands it.

## §2 — Match & test syntax

`is` is **omitted in match arms** (the arm position already means "test"), and **present in
expression-position tests** (where nothing else signals a test). One rule:

```elisa
match e:                                 # e: Expression
    Add(left, right):   apply(left, right)   #  Name(...)     → destructure a leaf
    BinaryExpression b: fold(b)              #  Name binding  → narrow to a category, bind b: that type
    Literal:            zero                 #  Name          → match, no binding
    _:                  default              #  wildcard

if e is BinaryExpression b:               # expression-position test + flow-narrow → keep `is`
    fold(b)
```

Disambiguation is structural and needs no keyword: `Name(…)` destructures a leaf; `Name binding`
binds a value statically typed `Name` (identical operation whether `Name` is a leaf bound whole or a
sub-category narrowed); `Name` alone matches without binding. Whether `Name` is a leaf or category is
a semantic lookup, not a parse decision. This matches Rust/Swift (no `is` in match) rather than Kotlin
(which keeps it).

`is` therefore appears in exactly two places, with one meaning ("is-a / narrow-to"):
- **declaration:** `enum BinaryExpression is Expression`
- **expression test:** `if e is BinaryExpression b:`

## §3 — Type model

- **Sealed nominal subtyping.** `Node.Statement <: Node` is true; the relation is the existing tree
  `Parent`-walk (`treeCategoryDescendsFrom`, `types_assignability.go`) generalized to
  `EnumType{Parent, Root}`. Deep chains are linear `Parent` walks — no quadratic blow-up.
- **Subtype = subset of cases.** A value of a subtype is always a usable value of the supertype.
- **Widening is free; narrowing is checked.** `f(n: Node)` accepts a `Statement` (upcast, a no-op —
  §4). `g(s: Statement)` rejects a bare `Node`; you narrow with `match`/`if e is …`, which can fail.
- **Variance:** subsumption at the call site only; containers stay invariant (`darray[Node.Expr]`
  invariant, as today). No general covariance.
- **Three-level exhaustiveness** (generalizes the per-category vs family-wide split already in
  `visitDomainKeys`):
  - `match` on a leaf-category → its own leaves.
  - `match` on an intermediate category → its own leaves ∪ each child sub-category (discharged by a
    `Child b:` arm or by inlining the child's leaves).
  - `match` on the root → its direct children (category arms) *or* all leaves flattened.
  - Default scope = the static type of the scrutinee. The checker computes the frontier by walking
    `Parent`. Adding a case anywhere makes every ancestor `match` that didn't account for it a
    compile error — the intended payoff.

## §4 — Lowering: one arena per root, category = tag-range

> ⚠️ Honest scoping note (from the design review): the existing `tree` store is **not** a single
> AoS arena — `ensureTreeStoreStateType` builds a carrier over **N per-category tables**. The fast
> AoS store (docs/76, 0.38 s) is the **packed-enum** path. So this is **not** "reuse `TreeStoreType`";
> it is "build the unified single-AoS-arena store on the packed-enum AoS path and port the tree family
> onto it." Budget it as the largest backend item, not plumbing.

**One region-backed AoS store per hierarchy root** (`Node`), not per category. Flat (non-`is`) enums
are the degenerate single-level case of the same store.

**Handle = today's bare opaque index (no widening, no category bits).** The variant tag lives in the
AoS record (field 0, as it already does in the recursive-enum AoS store). Category membership is a
**compile-time-known contiguous tag-range**:

- Number every **leaf** in the root in **hierarchy-grouped order**: all `Expr` leaves get a
  contiguous range, all `Statement` leaves the next, `Assignment`'s range nested inside `Statement`'s.
- **Upcast `Statement → Node` = a typed no-op.** Same index, same store; the static type just widens
  its legal tag-range. Zero instructions.
- **`is Category` / downcast = read the record's tag + one unsigned range check** (`tag - lo < n`).
  Branchless, the `SyntaxKind`-range trick. Note (corrects an earlier overclaim): the tag lives in the
  record, so a *pure* `is` test costs one load — but traversal reads the tag for `match` anyway, so it
  is effectively free in the common path.
- **Handle width is unchanged from docs/76** — default `u32` index, top value the free null sentinel.
  Adding category ranges adds **zero** bits because category is derived from the variant tag, not
  stored separately. (Options that widen the handle or add a category column were rejected: redundant
  with the variant tag, and they would regress overflow/compactness.)

**Field-class layout (resolves the AoS-vs-column tension):**
- **`common(…)` fields → shared column at a fixed offset**, readable from a bare `Node` handle without
  knowing the leaf (the prefix mechanism already implemented for recursive enums). This is what makes
  `n.span` work on any node and makes a `common`-field **column scan dense**.
- **Variant payloads → AoS, per-record.** A column scan `T of .payloadField` over an AoS payload is a
  *strided* read; `layout soa` on the root opts into dense columnar (consistent with docs/76 §5 — AoS
  strided, `soa` dense). Well-formedness rule for `T of .f`: `.f` must exist on every leaf in `T`'s
  tag-range (auto-true for `common`).

**Store threading (docs/74/75) gets simpler within a hierarchy, not eliminated.** A hierarchy that
would have been N mutually-recursive enums needing N threaded stores becomes **one root → one store**,
so `computeTransitiveStoreNeeds` collapses N names to 1. But region-*owning* functions still create
one store **per region instance** (binary-trees builds a fresh tree per loop iteration —
`funcOwnsRegion`; this is what commit `cef50ce7` fixed). So: one store per root *type*, still one per
region *instance*. "Zero threading" was an overclaim.

## §5 — Sealing: closed at the library boundary

The hierarchy is **sealed at the compilation unit / library**, not at the file. You may split
declarations across **as many files as compile together** as you like — that is the motivating
use case and it stays closed-world. What is forbidden is a **downstream, precompiled module adding
new leaves/categories** to someone else's root. Reasons (all three bind):

1. Exhaustiveness checking is only sound closed-world; open extension forces every `match` to carry a
   `default`, destroying the "compiler tells you what you missed" property.
2. Dense contiguous tag-ranges require knowing all leaves at seal time; a foreign leaf breaks
   contiguity or forces a link-time global renumber.
3. The unified arena is one store per root; a foreign leaf has no record shape registered in it.

This is the Java/Kotlin module boundary (looser than Scala's same-file), which is exactly the
multi-file-but-sealed model wanted.

**Escape hatch for genuine plugin extensibility (à la Java `non-sealed`):** make openness *data, not
type* — a sealed leaf carrying a boxed/`dyn` interface payload (`Extern(payload: dyn NodeLike)`). The
99 % case stays fast and sealed; the rare open case is explicit and local.

**Serialization caveat (must bake in now):** the *dense range* used for `is`-checks is recomputed each
compile and may shift when a kind is inserted mid-hierarchy. Any **on-disk / cross-build tag** (AST
caches, incremental builds) must use a **stable** tag — declaration order or an explicit `= N` — never
the positional dense range. Decouple the two.

## §6 — Implementation roadmap (phases)

> **Order note:** rather than collapse `tree` onto `enum` first (the original Phase 0), `enum … is`
> was built as a *parallel* path so it could be developed and tested independently. `tree` is then
> **retired last** (Phase 6) once `enum … is` fully subsumes it. End state: `enum … is` is the only
> construct; the `tree`/`node` keywords are **removed** (not kept as sugar).

### Shipped (type-system front half, all green on `work`)

- **Phase 1 — surface + subtype** (commit `eddb008a`): parse `enum Child is Parent:` + `enum Root:
  pass`; `EnumType.Parent`; `Child <: Parent` in assignability (`enumDescendsFrom`); formatter.
- **Phase 1b — hierarchy match** (commit `93762815`): `EnumType.Children`; `enumDescendantLeaves`
  (leaves flow up); arms may name any refinement (`BinaryExpression.Add` matching an `Expression`);
  exhaustiveness over the descendant-leaf union (`matchCoversAllVariants` + `reportNonExhaustiveMatch`).
- **Phase 2a — tag-range metadata** (commit `e4f03f76`): `assignHierarchyEnumTags` — dense,
  hierarchy-grouped, nested leaf numbering per root; `EnumType.LeafTagLo/LeafTagCount`; unified
  `EnumVariant.Tag`.

### Remaining

| Phase | Delivers | Risk |
|---|---|---|
| 2b | **Surface sugar (parser).** Unqualified leaf arms `Add(...)` (route the existing `MatchStructPattern` at top level of an enum match to variant resolution) + category-only arm `Child b:` + scrutinee refinement-narrowing in the arm body (reuse `bindRefinedExprType`). Still analysis-only. | low |
| 3 | **Unified store + codegen — the runtime gate (sub-sliced below).** Until this lands, hierarchy values do not run. | high |
| 4 | **Hierarchy column scan.** Extend `X of .f` well-formedness to "present on every leaf in T's range" (auto-true for `common`); `common`-field scans dense, payload scans strided / `layout soa`. | medium |
| 5 | **Defaults + value hierarchies.** Recursive hierarchy → unified root store by default; **non-recursive** hierarchy (e.g. colors) → inline `{tag, payload-union}` value, no store; `layout soa` opt-in per root. | medium |
| 6 | **Retire `tree`.** Desugar `tree`/`node` → `enum … is`; migrate `block`/`struct` members + all tree-using code; **delete** the tree parser/AST/`Tree*` type-nodes/`TreeStoreType` and the `tree`/`node` keywords. | medium |

### Phase 3 sub-slices (the unified store + codegen)

3a. **Per-root store identity.** Key the region-backed AoS store by the hierarchy **root**, not per
enum. A leaf constructor (`BinaryExpression.Add(...)`) allocates into the root's store;
`getOrCreateRegionPackedStore`/`lookupPackedStore` resolve via `EnumType.Root` (the topmost `Parent`).
Reuse `packedEnumABIAoS`.

3b. **Record shape = root union.** `ensurePackedEnumRowType` for the root sizes the payload to the
**widest leaf across the whole hierarchy** and places `common` fields at fixed offsets. Every leaf
writes its **unified tag** (Phase 2a) + its payload into that shared shape, so a `Parent` handle can
hold any leaf.

3c. **Construction codegen.** Route leaf construction (qualified, and the Phase-2b unqualified form)
through the root store with the unified tag. Static result type = the leaf's owner enum; the runtime
value = a bare `u32` index into the root store.

3d. **Match dispatch codegen.** `match e` reads the unified tag from the record and switches: leaf
arms = exact tag; category arms = unsigned range check (`tag - lo < count`). Exhaustiveness frontier
already computed (Phase 1b).

3e. **Upcast / `is` / downcast codegen.** Upcast `Child → Parent` = a typed no-op (identical bits).
`if e is X b:` and downcast = read tag + range check, then bind `b` at the narrowed type.

3f. **Store threading per root.** `computeTransitiveStoreNeeds` keyed by **root** — one implicit
store param per root (collapses N-enum mutual recursion to 1); `funcOwnsRegion` per-region-instance
logic unchanged (commit `cef50ce7`).

3g. **`common` reads from any handle.** Reuse the shared-column prefix mechanism; verify it reads
from a bare `Parent` handle across the hierarchy.

## §7 — Open questions (decide before/while building Phase 3)

1. **Non-recursive (value) hierarchies** (colors): inline `{tag, payload-union}` value with **no
   store** vs. always a store. Lean: non-recursive → inline value (no arena); recursive → root store.
   (Phase 5.)
2. **Payload-union sizing** pads to the widest leaf across the whole hierarchy (AoS memory cost);
   `layout soa(sparse)` is the later per-variant-dense answer.
3. **`block`/`struct` tree members** (Phase 6 migration) — give them an enum analogue (a struct-shaped
   leaf / a plain `struct` referenced by a variant payload) or migrate the handful of usages by hand?
4. **Serialization** — does the self-hosting toolchain serialize ASTs (incremental/caches)? If yes,
   stable explicit tags are mandatory before any on-disk use, decoupled from the dense `is`-range (§5).

## Decisions locked in this design

- One construct: `enum` + `is`. `tree`/`node` is **retired** once `enum … is` subsumes it (Phase 6) —
  not kept as permanent sugar. `is` (not `extends`) because sum-type refinement is the *opposite*
  variance from OO `extends`.
- `is` for declaration & expression-test; bare patterns in `match` arms.
- Sealed at the library boundary; multi-file; `non-sealed`-style escape hatch = data, not type.
- One region-backed AoS arena per root; bare index handle; category = compile-time tag-range; upcast
  no-op, downcast = tag read + range check; no handle widening.
- `common` fields shared-column (dense scan); payloads AoS (strided scan, `layout soa` for dense).
- Honest cost: this is a unified-store build on the packed-enum AoS path, not a free consolidation of
  the existing tree store.
