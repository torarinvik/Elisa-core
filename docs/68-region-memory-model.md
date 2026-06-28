# Region memory model — the one-allocator thesis, backing strategies, and provenance

This is the keystone document for Elisa Core's dynamic-memory model. It defines
the single primitive (the region), how regions get their storage (backing
strategies), how lifetimes are written in types (provenance notation), and how
values move between regions ([`promote` / `adopt`](67-region-promote-adopt-mini-spec.md)).

It supersedes the surface details in [08-region-checkpoints.md](08-region-checkpoints.md)
(now a sub-spec for `mark`/`restore`/`reset`) and fixes the canonical notation
used across [01](01-memory-layout-syntax.md), [22](22-value-fact-core.md), and
[18](18-current-surface-ergonomics.md).

> **Default allocation (2026-06).** Heap allocation is `new T(...)` — bare, with
> no annotation. The compiler infers the region: it threads one in from the
> caller when the value is returned (the function becomes region-polymorphic,
> [docs/75](75-region-polymorphic-functions.md)), or uses the innermost active
> region otherwise. Write `new[r] T(...)` only when you want to target a *named*
> region explicitly. `new[auto]` is deprecated; write bare `new` instead. The
> `in auto:` block is also deprecated: inference no longer needs it; remove the
> block, or use an explicit `region NAME(size):` when you want a scope you
> control. Container literals (`darray[T] = []`, `dict = {}`) infer their region
> the same way. Examples below that show `new[auto]` / `in auto:` predate this
> and read identically with the bare forms.

---

## 1. Thesis

**The region is the single building block for all dynamic allocation — and it is a
concrete primitive, not a generic allocator interface.**

Two deliberate divergences:

- **From Rust:** the unit of lifetime is the *region*, not the individual object.
  You reason about a handful of arena lifetimes, not per-object borrows.
- **From Zig/Odin:** we do **not** thread a generic `Allocator` interface through
  the program. A region exposes a fixed, narrow op-set; richer allocators (pools,
  free-lists, the atlas allocator) are ordinary *libraries built on a region*, not
  implementations of a universal vtable. The narrow `push`/checkpoint interface is
  the win precisely because it removes "did I have to free this?" from every
  caller's head.

Consequence we accept on purpose: **there is no safe escape hatch to raw `malloc`
outside a region.** Raw allocation is either a `malloc`-backed region (still a
region) or it is `Unsafe.*`. Because every dynamic value is therefore a region
client, the compiler's lifetime analysis (region provenance) covers pools, dicts,
strings, and dynamic arrays for free. One lifetime system, not two.

---

## 2. Storage model and layering

Three storage classes:

- **static** — globals, program-lifetime data
- **stack** — locals and call frames (the original lifetime-bundler; unchanged)
- **region** — *all* dynamic allocation

```
  libraries (no compiler magic):  pools · free-lists · dynamic arrays · dicts
                                   · strings · atlas allocator …
        ▲   every one of these just takes a region
        │
  region   ← the ONE thing the compiler understands for dynamic lifetimes
            ops:  new[r]  ·  mark / restore  ·  reset  ·  destroy  ·  adopt
            +  a backing strategy (§3)
        ▲
        │
  platform memory (thin, trusted):  reserve/commit virtual pages · a fixed
                                    buffer · malloc
```

The bottom layer is small, platform-specific, and trusted (`VirtualAlloc` on
Windows, `mmap`/`mprotect` on POSIX). Everything above it is portable and safe.

---

## 3. Backing strategies

A region's *backing strategy* decides where its bytes come from and how it grows.
It is a **small closed set**, selected at declaration with `using`:

```elisa
region r(N):                       # default backing (see §3.1)
region r(N) using fixed:           # one buffer of N, panic on overflow
region r(N) using chained:         # linked list of blocks, grow by chaining
region r(N) using reserve_commit:  # reserve N virtual, commit pages on demand
region r(N) using scratch:         # draw from the thread-local scratch pool
```

| strategy | growth | pointer stability | contiguous | use it for |
|---|---|---|---|---|
| `fixed` | none — **panic on overflow** | stable | yes | known-bounded scratch; "fail loud if I mis-sized it" |
| `chained` | link another block | individual allocations stable; a single array **can't span blocks** | per-block | node graphs, linked structures, general growth without a huge reservation |
| `reserve_commit` | commit more pages | **fully stable + contiguous** | yes | growable arrays, "don't know the size but want stable interior pointers" |
| `scratch` | (pooled) | stable within the block | per the pool | hot, purely-internal temporaries |

This ladder is intentionally the same progression as the canonical arena exercises
(fixed → chained → reserve+commit).

### 3.1 The default

**Default backing is `chained`.** Rationale: it grows safely without a per-region
virtual reservation, keeps individual allocations stable, and matches the existing
runtime. `reserve_commit` is the explicit upgrade when you want a *contiguous,
relocation-free* array; `fixed` is the explicit "bound it and panic" choice.

> Decision recorded here so it is explicit rather than incidental. Revisitable —
> the main alternative is "`reserve_commit` by default on 64-bit," which gives
> stable contiguous arrays for free but makes pointer-stability platform-dependent
> and so muddies the typed rule in §4.

### 3.2 `reserve_commit` in depth

This is the strategy that makes stable growable arrays possible, so it's worth
understanding. It rests on one fact: **the addresses your program uses are not the
same thing as physical RAM.**

A process has a vast *virtual address space* (≈256 TiB on 48-bit x86-64); physical
RAM is tiny by comparison. The CPU's MMU translates virtual → physical per access,
in pages (usually 4 KiB). Handing out address *numbers* is nearly free; backing
them with RAM is the expensive part. `reserve_commit` splits those:

- **Reserve** — claim a contiguous range of addresses ("these are mine, don't hand
  them out"). No RAM. Nearly free. Touching reserved-but-uncommitted memory faults.
- **Commit** — back a sub-range with real pages. Now it's readable/writable; RAM is
  charged only for what you commit (physical pages arrive on first touch).

```
reservation: 64 GiB of contiguous virtual addresses (claimed up front, ~free)
┌───────────────────────────────────────────────────────────────────────┐
│ committed (real RAM)        │  reserved, not committed (no RAM yet)     │
│ ████████████████░░░░░░░░░░░░│░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░ │
│                 ▲ bump pointer                                          │
└───────────────────────────────────────────────────────────────────────┘
 base (never moves)
```

- **create:** reserve the whole capacity, commit the first chunk, bump pointer at base.
- **push:** advance the bump pointer; if it crosses into uncommitted space, commit the next chunk, continue.
- **reset:** rewind the bump pointer to base (optionally decommit to return RAM; usually keep pages for speed).
- **destroy:** release the whole reservation in one call.

Because the entire range is reserved contiguously up front and **the base never
moves**, growth never relocates existing data — it just commits more pages at higher
addresses. That is what gives stable interior pointers (§4) and a dynamic array with
an absurd cap that only consumes RAM for what it holds.

Tradeoffs: commit works at page granularity (don't use it for thousands of tiny
regions); a reservation carries a little OS bookkeeping; and it's a *strategy you
select* because not every target offers it.

---

## 4. Pointer stability is a typed property

Whether taking an interior reference into a container and then growing it is legal
depends on the **backing strategy**, and the compiler knows it:

- **`reserve_commit`-backed** container → growth commits pages, never relocates →
  `push`/`reserve`/`extend` **do not** invalidate interior references.
- **`chained`-backed** → individual allocations are stable, but a *contiguous* array
  can't grow across a block boundary, so growing it relocates → interior refs into
  it **are** invalidated on growth.
- **`fixed`-backed** → stable, never grows (overflow panics).

```elisa
region big(64 * GiB) using reserve_commit:
    xs: mutable darray[Entity] @big = []
    xs.push(Entity{ hp: 100 })
    e0: Entity& @big = &xs[0]          # interior ref
    for i in 0 ..< 1_000_000:
        xs.push(Entity{ hp: i })       # commits pages — base & data never move
    e0.hp = 50                         # ✅ valid: reserve_commit is stable
```

```elisa
region tmp(1 * MiB) using chained:
    xs: mutable darray[Entity] @tmp = []
    xs.push(Entity{ hp: 100 })
    e0: Entity& @tmp = &xs[0]
    xs.push(Entity{ hp: 1 })           # may relocate across a block boundary
    e0.hp = 50    # ✗ compile error: `e0` invalidated by `push` on relocatable `xs`
```

Net effect: the common arena case ends up *less* restricted than a one-size
invalidation rule, while staying sound — better than `std::vector` (no silent
invalidation) and better than a blanket "growth always invalidates" rule.

---

## 5. Provenance notation

"Which region does this value live in?" is written **one way**, by position:

- **Declare** a region parameter in the bracket list, beside type parameters:
  `[@r]`.
- **Use** provenance with the `@r` suffix, on *any* type:
  `i32& @r`, `Entity& @r`, `darray[T] @r`, `Level @r`, `Box[i64] @r`.
- **Struct fields** reference the struct's own region parameter by name: `@owner`.

```elisa
struct Level[@owner]:            # declare the region parameter
    name:     str
    geometry: darray[Triangle] @owner   # fields tie provenance to it
    entities: Pooled[Entity]   @owner

def build(dst: Arena) -> darray[int] @dst:   # @ on a function return
    ...

lvl: Level @loading                    # @ at a use site
e0:  Entity& @big                      # @ on a reference
```

The only struct form is `struct Level[@owner]:`. Region parameters live in
the bracket list beside type parameters, each prefixed with `region`, and
**generalize** to several regions:

```elisa
struct Edge[@a, @b]:        # endpoints in different regions
    from: Node& @a
    to:   Node& @b
```

Encoding the region as a *single type parameter* is what makes `Level @loading`
well-typed and lets `adopt`/`promote` rebase an entire struct by rewriting one
parameter (§6).

**Three notations this rule replaces** (see [§7](#7-migration)):

- the region *prefix* on references — `scratch i32&` becomes `i32& @scratch`;
- region arguments in *brackets* at use sites — `Expr[owner]` / `Box[T, owner]`
  become `Expr @owner` / `Box[T] @owner`. Brackets carry *type* args only;
- the *expression-owner* form — `xs = [1, 2, 3] in owner` becomes
  `xs: darray[int] @owner = [1, 2, 3]`, or relies on the ambient scope below.

**Orthogonal, unchanged:** storage qualifiers stay as prefixes (`heap T&`,
`static T&`, `stack T&` — these are storage classes, not regions); and the ambient
**`in owner:` scope block** stays — it is a control construct that sets a default
owner for a span of allocations, not a type notation.

---

## 6. Moving between regions: `promote` and `adopt`

Two operations, two costs, two spellings — full semantics in
[67-region-promote-adopt-mini-spec.md](67-region-promote-adopt-mini-spec.md):

- **`promote <value> into <region>`** — copy a value into a longer-lived region and
  re-type its provenance. O(size), safe, needs no backing support. It's the
  *retrofit* tool: the arena-native default is to pass the destination region down
  and write keepers straight into it.
- **`adopt <child-region> into <parent>`** — splice an entire child region into a
  parent. O(1) (a chain concatenation), consumes the child owner, safe (the
  must-consume obligation *transfers*, unlike `leak`). Requires a **compatible
  backing family** (e.g. `chained↔chained`).

Doctrine: want zero-copy for a *single* value? Give it its own region and `adopt`
that region — value granularity is always a copy, region granularity is always the
splice, no hybrid.

---

## 7. Migration

Breaking changes, applied as one coordinated sweep:

| was | becomes |
|---|---|
| `region r(N) using malloc` | `region r(N) using fixed`/`chained`/`reserve_commit` (or `using malloc` retained only as a `chained` block source) |
| `scratch i32&` (region prefix on ref) | `i32& @scratch` |
| `Expr[owner]`, `Box[i64, scratch]` (region in brackets at use site) | `Expr @owner`, `Box[i64] @scratch` |
| `owner Expr&?` (field, region prefix) | `Expr&? @owner` |
| `struct X in owner:` | `struct X[@owner]:` — `in owner` declaration sugar **removed** |
| `xs = [1, 2, 3] in owner` (expression owner) | `xs: darray[int] @owner = [1, 2, 3]` |

Unchanged: `new[r] x`; `mark`/`restore`/`reset`/`destroy`/`leak`; storage-class
prefixes (`heap`/`static`/`stack`); the `in owner:` scope block.

---

## 8. What the compiler enforces

With this model, the analyzer guarantees — statically — that:

- every `@r` value is reclaimed exactly once, by its region's
  `destroy`/`reset`/scope-exit; no per-allocation `free`, no leak.
- an interior reference is rejected the moment its backing *can* relocate, and
  accepted when the backing strategy makes it stable (§4).
- a value cannot escape into a region that doesn't outlive it (returning into a
  function-local region is an error; region/`Arena` parameters are how data
  escapes).
- a `reset`/`restore` of one region cannot invalidate another region's data
  (region identities are static), so the C "scratch aliasing" hazard does not
  arise.

The two bugs even seasoned arena programmers still hit by hand — returning a
pointer into popped scratch, and accidentally overlapping two lifetimes — are both
compile errors here. Arena discipline, with the footguns enforced.

---

## 9. Worked examples

### 9.1 Baseline — pass an arena, allocate, never free

```elisa
def join_words(dst: Arena, words: darray[str], sep: str) -> str @dst:
    out: mutable str @dst = ""
    first: mutable bool = true
    for w in words:
        if not first:
            out = str_concat(dst, out, sep)
        out = str_concat(dst, out, w)
        first = false
    return out

def main() -> i32:
    region app(1 * MiB):
        words: darray[str] @app = ["arena", "region", "lifetime"]
        print(join_words(app, words, ", "))
    # `app` destroyed → words + result reclaimed in one shot, zero free calls
    return 0
```

### 9.2 Keep a small result out of a big scratch

```elisa
def top_token(dst: Arena, text: str) -> Token @dst:
    region scratch(1 * MiB) using scratch:
        doc:  Ast @scratch  = parse(scratch, text)        # big, all in scratch
        best: Token @scratch = hottest_token(doc)
        return Token{ name: str_copy(dst, best.name),     # write the keeper into dst
                      weight: best.weight }
    # scratch reclaimed; the returned Token lives in dst
```

### 9.3 Pointer stability + a pool with generational handles

```elisa
def entity_demo() -> void:
    region world(8 * GiB) using reserve_commit:
        pool: mutable Pooled[Entity] @world = pool_new(world)

        a: Handle[Entity] = pool.alloc(Entity{ hp: 100 })
        pool.release(a)                                   # slot → free list
        c: Handle[Entity] = pool.alloc(Entity{ hp: 60 })  # reuses a's slot, new generation

        if pool.get(a) == null:                           # ✅ stale handle → null (gen mismatch)
            print("a is correctly dead")
        print(pool.get(c).hp)
    # whole pool + free list reclaimed with `world`
```

### 9.4 Last boss — a frame loop: permanent + per-frame + scratch + bulk `adopt`

```elisa
struct Level[@owner]:
    name:     str
    geometry: darray[Triangle] @owner
    entities: Pooled[Entity]   @owner

# Build a level in a throwaway region, then hand the whole region to the caller
# zero-copy. The scratch parse tree never escapes.
def load_level(dst: Arena, path: str) -> Level @dst:
    region loading(2 * GiB) using chained:
        raw: Bytes @loading = read_file(loading, path)
        region work(256 * MiB) using scratch:
            doc: Ast @work = parse_level(work, raw)
            lvl: Level @loading = build_level(loading, doc)   # geometry + entities in `loading`
        # `work` reclaimed; `lvl` + its data live in `loading`
        adopt loading into dst        # O(1): splice loading's blocks into dst
        return lvl                    # rebased Level @dst, survives the call

def game() -> i32:
    region perm(16 * GiB) using reserve_commit:          # whole-program lifetime
        region frame(1 * GiB) using reserve_commit:      # one frame's scratch

            level: Level @perm = load_level(perm, "e1m1.level")

            running: mutable bool = true
            while running:
                reset frame                              # per-frame null-GC: O(1) reclaim
                events: darray[Event] @frame = poll_events(frame)
                for ev in events:
                    if ev.kind == EventKind.Spawn:
                        level.entities.alloc(
                            Entity{ name: str_copy(perm, ev.name), hp: 100 })  # survivor → perm
                    if ev.kind == EventKind.Quit:
                        running = false
                physics_step(level, frame)               # each system gets its own non-aliasing scratch
                render(level, frame)
        return 0
    # `perm` destroyed → the entire game's memory released in one shot, no leak-walking

def physics_step(level: Level @perm, frame: Arena) -> void:
    # `frame` is passed in; our own region is a distinct identity, so resetting it
    # can never pop `frame`'s allocations — no get_scratch(conflicts) dance needed.
    region tmp(16 * MiB) using scratch:
        pairs: darray[Pair] @tmp = broad_phase(tmp, level)
        for p in pairs:
            resolve(p)
```

(`str_copy`, `parse_level`, `poll_events`, etc. are illustrative stand-ins.)

---

## 10. Relationship to other documents

- [08-region-checkpoints.md](08-region-checkpoints.md) — `mark`/`restore`/`reset`/
  `leak` and checkpoint blocks. Still current; this doc supplies the backing +
  notation it assumed.
- [67-region-promote-adopt-mini-spec.md](67-region-promote-adopt-mini-spec.md) —
  full `promote`/`adopt` semantics.
- [01-memory-layout-syntax.md](01-memory-layout-syntax.md) — physical layout +
  owner-region struct parameters (canonical notation per §5 here).
- [10-orthogonality-packed-enums-regions-and-affine-concurrency.md](10-orthogonality-packed-enums-regions-and-affine-concurrency.md)
  — the `deps(v)` provenance model the backing rules build on.
- [09-concurrency-mini-spec.md](09-concurrency-mini-spec.md) — sendability /
  frozen publication; region-per-thread ownership.

## 11. Deferred

Deep (graph) `promote`; promoting a value that itself owns a region; region
access-states / `freeze`-for-regions (cross-thread sharing); generic sendability
(audit #8); page-guard-on-pop runtime hardening; FFI `@borrows_return*` stays
trusted (correctly — it's the raw boundary).
