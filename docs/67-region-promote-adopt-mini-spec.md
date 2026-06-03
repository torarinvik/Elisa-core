# Region promotion and adoption (`promote` / `adopt`) — mini-spec

> Part of the region memory model. See [68-region-memory-model.md](68-region-memory-model.md)
> for the one-allocator thesis, backing strategies, and the canonical `@r`
> provenance notation this spec uses.

Status: design, pre-implementation. This spec pins down the *semantics* of moving
region-backed data from a shorter-lived region into a longer-lived one. It does
not introduce a backend yet; it fixes the surface, the type rule, and the
dependency-set effect so the two operations can't be conflated during
implementation.

It builds directly on the existing region model in
[08-region-checkpoints.md](08-region-checkpoints.md) and the dependency-set model
in [10-orthogonality-packed-enums-regions-and-affine-concurrency.md](10-orthogonality-packed-enums-regions-and-affine-concurrency.md)
(`deps(v) = { (region, generation), ... }`), and on region parameters — both the inferred generic form
(`def fill[region r](out: mutable darray[u8] @r)`, already runtime-tested, where
`r` is inferred from a region-backed argument) and the explicit
`Arena`-value-parameter form (`def make(owner: Arena) -> Expr @owner`,
[docs/01](01-memory-layout-syntax.md)) used when the caller supplies the
destination region and gets data back in it.

---

## 1. The decision: two operations, not one

There are two genuinely different things "move this into a longer-lived region"
can mean. They have different cost, consume different things, and need different
backing support. We give them **different verbs** so the cost is legible at the
call site — in a language whose whole point is auditable memory behavior, an O(n)
copy and an O(1) pointer splice must not share a spelling.

| | `promote v into r` | `adopt child into parent` |
|---|---|---|
| Granularity | one **value** | one **whole region** |
| Cost | O(size of `v`), copies bytes | O(1), splices chunk lists |
| Consumes | nothing (rebinds `v`) | the `child` region owner (affine) |
| Source must be live | yes (reads `v`) | yes (`child` is being absorbed) |
| Backing family | none | `child` + `parent` share a compatible backing family |
| Permission | none (safe) | none (safe) |
| Ships on today's substrate | yes | no — gated on backing-strategy work |

**Doctrine — how to choose:**

- Keep one small thing out of a big scratch arena, then free the arena → `promote`.
- Hand an entire built arena to a longer-lived owner → `adopt`.
- Want *zero-copy* handoff of a single large value? **Give it its own region and
  `adopt` that region.** Value-granularity is always a copy; region-granularity is
  always the zero-copy splice. There is deliberately no third hybrid operation.

That last rule is the important one. In a bump arena a single allocation shares a
chunk with everything allocated around it, so you can never hand off *just* one
value zero-copy without also handing off its chunk-mates. Rather than invent a
fragile "sole-chunk-owner" special case, we say: if you want zero-copy at value
granularity, express it as region granularity — put the value in a dedicated
region and adopt the region.

---

## 2. Form 1 — `promote <value> into <region>`

Copy-promote. Relocate a value's own storage into a longer-lived region and
re-type its provenance.

### 2.1 Surface

Statement form (the common case — rebinds the name in place):

```elisa
promote arr into r          # arr was `T @scratch`; afterwards arr is `T @r`
```

Expression form (for temporaries / direct binding):

```elisa
keeper: darray[int] @r = promote build_scratch() into r
```

The statement `promote v into r` is sugar for `v = promote v into r` with `v`'s
provenance updated. After either form, the **old** `@scratch` value is consumed:
the pre-promotion binding is stale and any later use of it is the ordinary
use-after-move error.

### 2.2 Semantics

1. Allocate fresh storage for `v`'s representation in `r`.
2. Bitwise-relocate `v`'s own backing storage from the source region into `r`.
3. Re-type the result to `@r` and rebind. The source copy is consumed.

`promote` relocates **only the value's own backing storage** — it is shallow. It
does not chase interior references into the source region.

### 2.3 Type rule (the soundness condition)

`promote v into r` is legal iff the value's **only** dependency on the source
region is its own backing storage (which promote relocates).

In dependency-set terms: let `s` be the source region. `promote` rebases `v`'s
backing dependency `(s, g) → (r, g_now)`. After that rebase, if `deps(v)` still
contains any `(s, _)` — i.e. some *field or element* of `v` references `s`
independently of the backing — the value is **not self-contained** and `promote`
is a compile error:

```
promote: value contains interior references into source region "scratch";
promote relocates only the value's own storage (deep/graph promotion is not supported)
```

The existing structural deps already distinguish a container's *backing*
dependency from dependencies carried by its *elements/fields*
([10 §"Region dependency tracking should be structural"](10-orthogonality-packed-enums-regions-and-affine-concurrency.md)),
so this check needs no new analysis — only a `rebase` of the backing dep plus a
residual-dependency scan.

Concretely: `darray[int] @scratch` promotes (elements are plain `int`).
`darray[Node @scratch] @scratch` does **not** (elements hold `@scratch` refs that
would dangle once `scratch` dies).

### 2.4 Liveness, escape, permission, cost

- **Liveness:** the source region must be live at the promote point (you are
  copying out of it). Trivially true when promoting a `@scratch` value inside
  `scratch`'s own scope.
- **Escape:** `promote` changes nothing about escape. The result lands wherever
  `r` lives. If `r` is a *region parameter*, the result escapes the call by the
  existing return-provenance / outlives rules — no new escape rule (see §6).
- **Permission:** none. `promote` is a sound copy; it is fully safe.
- **Cost:** O(size of relocated storage), visible from the verb.

---

## 3. Form 2 — `adopt <child-region> into <parent-region>`

Merge/splice. Absorb an entire region's live storage into a longer-lived region,
zero-copy, consuming the child.

### 3.1 Surface

```elisa
adopt work into perm        # `work`'s storage now belongs to `perm`; `work` is consumed
```

### 3.2 Semantics

1. Transfer ownership of all of `child`'s live backing chunks to `parent`.
2. Cancel `child`'s cleanup. Its must-consume obligation is **discharged by the
   adopt** (it is *transferred*, not dropped — which is why `adopt` is safe while
   `leak` is `Unsafe.Leak`).
3. Consume the `child` region owner (affine). After `adopt`, `child` is dead: any
   `new[child] …`, `mark child …`, `restore … from child-checkpoint`, `reset
   child`, `destroy child`, `leak child`, or other use of `child` is a
   use-after-consume error. `child`'s outstanding marks are invalidated.
4. Rebase every dependency naming `child` to name `parent` (see §4).

### 3.3 Generations

Adopted allocations enter `parent` at `parent`'s **current generation** at the
adopt point. Consequences, all consistent with the existing checkpoint model:

- A later `restore parent from <mark taken before the adopt>` invalidates the
  adopted values (they are newer than that mark) — correct, because rewinding
  `parent` below the adopt point reclaims the spliced-in chunks.
- `reset parent` / `destroy parent` invalidate them (they depend on `parent`).
- Marks taken on `parent` *after* the adopt behave normally.

### 3.4 Scoped child: discharge interaction

Inside a scoped region, `adopt` satisfies the block-exit discharge that
[08](08-region-checkpoints.md) would otherwise fulfill by destroying the region:

```elisa
region work(64 * MiB):
    ast = build_ast(work)
    adopt work into perm     # discharges work's must-consume; suppresses the implicit destroy
# block exit: nothing to do — work was already consumed by adopt
```

No double free: the implicit scope-exit destroy is suppressed once `adopt` has
consumed the owner.

### 3.5 Backing-family requirement (the one real dependency)

`adopt` requires `child` and `parent` to share a **compatible backing family** —
you can only splice block chains between regions whose backing supports transfer.
Two `chained` arenas: yes (concatenate the linked block lists). Two
`reserve_commit` regions: yes. A `chained` child into a `reserve_commit` parent (or
vice-versa): no — the storage shapes don't compose. See
[68-region-memory-model.md §3](68-region-memory-model.md) for the strategy set.

Until backing strategies land (see [future work](#11-non-goals--future-work)),
`adopt` is **restricted to same-family child into parent**, and any other pairing
is:

```
adopt: region "work" (reserve_commit) and "perm" (chained) have incompatible
       backing families; cannot adopt
```

This is the explicit hand-off point between this spec and the backing-strategy
work: `adopt`'s legality check is `compatible_backing(child, parent)`, which under
`chained`/`reserve_commit` is a block-chain splice.

### 3.6 Permission, cost

- **Permission:** none. `adopt` is a sound ownership transfer (obligation moves,
  it is not dropped), so it is safe — unlike `leak`.
- **Cost:** O(1) in the data (a chunk-list splice), independent of bytes held.

---

## 4. The shared primitive: dependency rebase

Both forms need the same dependency-set operation, which mirrors the existing
invalidation walk (`invalidateRegionDependencyInState`) but **re-keys** instead of
marking invalid:

```
rebase(deps, from_region, to_region, gen):
    for each (from_region, g) in deps:      replace with (to_region, gen)
    recurse through fields / elements / payloads
```

- `promote v into r`: `rebase(deps(v's backing), scratch, r, g_now)`, then assert
  no residual `scratch` dep (§2.3).
- `adopt child into parent`: `rebase` over *all* tracked values, `child → parent`
  at `parent`'s current generation (§3.3).

This is the only genuinely new piece of static machinery either form requires;
everything else (owner consumption via `recordAffineConsumption`, scope-exit
discharge, generation tracking, residual-dep scanning) already exists.

---

## 5. Why the "obvious" single-value zero-copy adopt is *not* a third operation

It is tempting to want `adopt arr into bar` to mean "zero-copy, just this one
value." It can't, in general: `arr` shares its chunk with whatever else was
allocated around it in the source arena, so handing off `arr`'s bytes hands off
its neighbors too — defeating the point of freeing the scratch. The two coherent
points in the design space are exactly Form 1 (copy one value) and Form 2 (splice
a whole region); the "zero-copy single value" wish is served by *making that value
its own region* and using Form 2. See the doctrine in §1.

---

## 6. Composition with region parameters and escape

Neither form introduces an escape rule. A promoted/adopted result escapes a
function iff its destination region outlives the call — which, for a **region
parameter**, it does. The corrected shape of the motivating example:

```elisa
def build(dst: Arena) -> darray[int] @dst:
    region scratch(50000):
        arr: mutable darray[int] @scratch = []
        ... heavy scratch work ...
        promote arr into dst      # copy the keeper into the caller's region (scratch still live)
    # scratch freed here → the 50 KB of scratch garbage reclaimed early
    arr.push(34)                  # arr is `@dst` now
    return arr                    # `@dst` outlives the call → sound
```

The only change from the naive attempt is structural: **the long-lived region is a
parameter, not a function-local region.** A function-local region is destroyed at
function exit, so returning anything `@that-region` dangles — and the analyzer
already rejects it (`cannot return reference: region dependency facts include
local region "scratch"`). Region parameters are the supported way for
region-backed data to escape a call; `promote`/`adopt` only decide *which* region
the data ends up in.

Bulk handoff, same principle, zero-copy:

```elisa
def parse(perm: Arena, src: cstr) -> Ast @perm:
    region work(64 * MiB) using chained:
        ast: Ast @work = build_ast(work, src)    # 64 MB of nodes in work
        adopt work into perm                      # O(1): splice work into perm, rebase ast → Ast @perm
    return ast                                    # Ast @perm outlives the call → sound
```

---

## 7. Accepted examples

```elisa
# Promote a small keeper out of a big scratch arena into the caller's region
def summarize(keep: Arena, input: cstr) -> Summary @keep:
    region scratch(1 * MiB) using scratch:
        doc = parse_into(scratch, input)        # big, all in scratch
        s: Summary @scratch = extract(doc)      # small
        promote s into keep                     # copy the keeper across
    # scratch dropped: 1 MB reclaimed; s is now Summary @keep
    return s
```

```elisa
# Adopt a whole staging region into the caller's long-lived arena
def load_assets(world: Arena, paths: darray[cstr]) -> AssetTable @world:
    region staging(128 * MiB) using chained:
        table: AssetTable @staging = decode_all(staging, paths)
        adopt staging into world      # hand the whole decoded arena to `world`
    return table                      # rebased to AssetTable @world
```

---

## 8. Rejected examples (with intended diagnostics)

```elisa
# The original motivating example — rejected on three independent counts.
def foo() -> darray[int] @bar:       # ERROR: `bar` is function-local; result would dangle
    region bar(100):
        region scratch(50000):
            arr: mutable darray[int] @scratch = []
            arr.reserve(100)
        adopt arr into bar           # ERROR: `arr` is a value, not a region (use `promote`);
                                     #        and `scratch` is already destroyed here
        arr.push([25, 10, 34])
        return arr                   # ERROR: returns `@bar`, a local region
```

```elisa
# Promote of a non-self-contained value — rejected.
region scratch(4096):
    nodes: mutable darray[Node @scratch] @scratch = []
    promote nodes into keep
    # ERROR: promote: value contains interior references into source region "scratch"
    #        (elements are `Node @scratch`); deep/graph promotion is not supported
```

```elisa
# Use of an adopted (consumed) region — rejected.
region work(1024):
    x = new[work] 1
    adopt work into perm
    y = new[work] 2          # ERROR: region "work" consumed by `adopt into perm`
```

```elisa
# Adopt across incompatible backing families — rejected.
region staging(1 * MiB) using reserve_commit:
    ...
    adopt staging into perm  # ERROR: region "staging" (reserve_commit) and "perm"
                             #        (chained) have incompatible backing families
```

---

## 9. Grammar additions

```
PromoteStmt := "promote" Expr "into" RegionRef
PromoteExpr := "promote" Expr "into" RegionRef          # expression form
AdoptStmt   := "adopt"   RegionRef "into" RegionRef
```

`RegionRef` is an existing region name, region parameter, or visible `Arena`
value ([docs/01](01-memory-layout-syntax.md)). `promote`'s first operand is any
expression of a region-backed type; `adopt`'s operands are both region owners
(named regions or owner-parameters). The two are disambiguated by operand kind (value vs
region), and the verb already tells the reader which is which.

---

## 10. Lowering / implementation sketch

**Form 1 `promote` (ships on today's substrate):**

- Static: add `rebaseRegionDependencyInState` (mirror of the invalidator, §4);
  apply it to the value's backing dep; scan for residual source-region deps and
  error if any; re-type the binding to `@r`; mark the old binding consumed.
- Runtime: allocate in `r` + bitwise copy + fresh header. A new
  `arena_promote(dst, src_ptr, size)` helper in `arena.elisa`, or just reuse the
  existing alloc + copy path. No new backing support needed.

**Form 2 `adopt` (gated on backing strategies):**

- Static: `rebase` over all tracked values (`child → parent` at parent's current
  generation, §3.3); `recordAffineConsumption(child, "adopt into parent")`;
  suppress the scoped-region implicit destroy; invalidate `child`'s marks; check
  backing-family compatibility.
- Runtime: `arena_adopt(parent, child)` — splice `child`'s chunk list onto
  `parent`'s and clear `child`'s list so its (now suppressed) cleanup is a no-op.
  Sits alongside `arena_snapshot` / `arena_rewind` / `arena_reset`.

---

## 11. Implementation order

1. **`promote` first.** It is sound, safe, backing-agnostic, and sits on the
   existing deps machinery + one new `rebase` walk. It is also the missing verb in
   the motivating example, so it delivers the ergonomics immediately.
2. **Backing strategies** (separate work — see [68](68-region-memory-model.md)) —
   prerequisite for `adopt`'s chain splice.
3. **`adopt`** on top of (2), starting within one backing family and widening as
   more families gain compatible chain transfer.

---

## 12. Non-goals / future work

- **Deep / graph promotion.** Promoting a value whose interior references point
  into the source region (recursively relocating the reachable graph) is out of
  scope; such values are rejected (§2.3). Revisit only if a real workload needs it.
- **Promoting a value that itself owns a nested region.** Rejected for now; the
  interaction of value-copy with a contained region owner is unspecified.
- **Partial region adoption.** `adopt` is whole-region by definition; there is no
  "adopt these N allocations." Use `promote` for value granularity.
- **`freeze` / region access-states.** Interaction of `promote`/`adopt` with a
  frozen/published region (for cross-thread sharing) is deferred to the region
  access-state work; this spec assumes mutable, thread-local regions.
- **Cross-thread region move.** Moving a region owner to another thread is a
  separate concern from adopting one region into another on the same thread.

---

## 13. Open question — naming

The one decision most worth revisiting is the verbs. This spec uses **`promote`**
(value copy) and **`adopt`** (region splice) to keep the cost model legible: two
verbs, two costs. The alternative is a single overloaded `adopt … into …`
disambiguated by operand kind — closer to the original sketch, but it hides an
O(n) vs O(1) difference behind one spelling, which cuts against the language's
"visible memory behavior" stance. Recorded here so the call is explicit rather
than incidental.
