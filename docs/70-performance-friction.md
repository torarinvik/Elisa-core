# 70 — Performance friction: the pit of success for speed

## Implementation status

- ✅ **`@hot` fast contract LANDED** (commit a31a8186): a hot function may use no allocation
  (`Memory.Allocate`/`Release`) and no raw-pointer/indirect-dispatch `Unsafe.*` effect,
  transitively; fast-unsafe (`Unsafe.UncheckedIndex`) and cold branches (`Abort.Panic`) stay
  allowed. Enforced as an effect upper bound at `analyzer_functions.go`.
- ⚠ **`Memory.Heap` split DROPPED.** Stage 1 assumed a malloc path to gate, but all-region
  Elisa has none — `using malloc` normalizes to chained (`region_backing.go`), so
  `Memory.Allocate` *is* the only allocation effect. `@hot` banning it directly is the real
  "no allocation on the hot path" guarantee; a separate `Memory.Heap` would gate nothing.
  The taxonomy table below keeps `Memory.Heap` as a hypothetical for if a raw-heap path is
  ever added.
- ✅ **Pointer-graph lint LANDED** (commit 8b054746): a struct with a self-referential ref
  field carrying no region provenance (raw `heap Node&?` / bare `Node&?`) is warned, pointing
  at the packed-enum/`Store` cure. A `@owner`-tracked self-ref (sound single-region graph) is
  NOT flagged. Opt-out is the new `@intrusive` struct annotation (the "painful but possible"
  acknowledgment) — the runtime's free-list/queue/cache primitives carry it. Caught 9 real
  raw structures on first run.
- ✅ **Mutual-recursion cycle detection LANDED** (commit 615b9cbc / cherry-pick 83972457):
  extends the lint beyond direct self-reference to indirect raw-ref cycles (`A → B → A`) via a
  program-wide `checkPointerGraphCycles` pass (raw-ref digraph + DFS back-edges; `@intrusive`
  nodes cut the graph; `@owner` refs and buffer pointers never create edges). Zero new
  `@intrusive` needed (no latent cross-type raw cycles in stdlib/fixtures).
- ✅ **Allocation-churn lint LANDED** (commit 31760ece): individual `new` boxing inside a
  loop is warned toward batch allocation; precise — `push` accumulation and packed-node sugar
  are not flagged, single `new` outside a loop is silent. Zero noise (nothing in
  stdlib/fixtures uses `new` in a loop).
- ✅ **`-Wperf` graduated strictness LANDED** (commit d1636cdb): the pointer-graph and churn
  lints are warnings by default but `-Wperf` promotes them to hard errors (via
  AnalyzeOptions.EnforcePerfLints + the `perfLint` helper), so shipped code can ban the
  anti-patterns outright — the "illegal" half of the goal.
- The model is now functionally complete: `@hot` (slow effects, always enforced),
  pointer-graph + churn lints (slow structure / alloc pattern), `-Wperf` (enforcement lever).
- **`slow:` quarantine block: DROPPED** — like `Memory.Heap` it was premised on a malloc
  path that doesn't exist; raw-pointer work is already quarantined by `trusted Unsafe.*:`, so
  it would largely duplicate it. Revisit only if a distinct slow-but-safe op appears.

## Thesis

Make **fast, safe code the frictionless default** and **slow, unsafe code painful but
possible**. A beginner writing the obvious thing should get region-allocated, contiguous,
monomorphized, memory-safe code without knowing why it's fast. Reaching for a slow or
unsafe pattern should require visibly opting out — an annotation, an effect in the
signature, or a quarantine block — so it is never the accidental path.

This is the performance analogue of the safety model: just as `trusted Unsafe.*:` makes
memory-unsafe code loud and greppable, slow code should be loud and greppable too.

### What is and isn't catchable

The compiler sees **structure**, not **algorithms**. It can make these painful:

- per-object heap allocation / malloc churn,
- raw pointer graphs (cache-hostile pointer chasing),
- hidden O(n) copies,
- dynamic dispatch / indirection,
- escaping/cross-lifetime references (already caught).

It **cannot** catch an O(n²) loop, a needless re-computation, or a bad cache access
pattern inside otherwise-fine code. So the goal is narrowed and honest: **make the slow
*memory patterns* painful** — the ones beginners fall into without realizing — not to
prove asymptotic performance.

## The half that already exists (the frictionless-fast default)

The "easy way" already lands on fast+safe; this design only adds teeth to the slow path.
Inventory (do not rebuild):

- **Inference-by-default** (`docs/69` work, commits 8caae435/d6a475fe): a bare allocation
  infers a per-function region — region allocation with zero annotation.
- **Region memory model** (`docs/68`): region is the allocation primitive; lifetime = the
  region, not the object. Backing strategies (chained/fixed/reserve_commit) give bounded,
  predictable, zero-copy-growth allocation.
- **Store/Handle unification** (`docs/69`): handle-into-store (`darray`/`Deque`/packed/
  pool) is first-class and ergonomic — the cache-friendly alternative to pointer chasing.
- **Monomorphized static interfaces** + **parametric impls**: zero-cost abstraction, no
  vtables. There is no dynamic dispatch to accidentally pay for.
- **Value semantics + region escape analysis**: cross-lifetime aliasing and
  use-after-free are already compile errors (`region_containers.go`,
  `analyzer_flow_borrowed_owner_*`).
- **Unsafe quarantine**: raw pointers require `heap T&` / `T&?` and `trusted Unsafe.*:`
  blocks (`analyzer_builtins.go:37` registers 20+ `Unsafe.*` members).

So the remaining work is entirely on the **slow/unsafe side**: make those patterns loud,
bannable, and teach the fast alternative.

## The cost taxonomy (what we make legible)

Two existing effect families and the proposed additions:

| Family.Member        | Status   | Meaning / cost                                            |
|----------------------|----------|-----------------------------------------------------------|
| `Memory.Allocate`    | exists   | allocation — TODAY lumps region + heap together           |
| `Memory.Release`     | exists   | deallocation                                              |
| `Memory.Heap` (new)  | proposed | individual / malloc-backed allocation (the slow kind)     |
| `Unsafe.*`           | exists   | raw pointers, casts, unchecked index, leak, alias, …      |
| `Perf.Indirect` (new, later) | proposed | dynamic dispatch / indirect call in a hot context |
| `Perf.Copy` (new, later)     | proposed | a large by-value copy where a ref/handle would do |

The key move: **`Memory.Allocate` stays the cheap, near-invisible default (region/arena),
and the slow kind (`Memory.Heap`) is split out as a distinct, louder effect.** Because
effects propagate to signatures, any function that mallocs advertises it in its type.

## Mechanisms

### 1. Cost in the effect system — heap-vs-region split (foundation)

Register `Memory.Heap` (extend `registerBuiltinPermission("Memory", …)` at
`analyzer_builtins.go:26`). Route the malloc-backed allocator (`region r(N) using malloc`,
and any global-heap boxing) through `Memory.Heap` instead of plain `Memory.Allocate`.
Region/arena allocation keeps requiring only `Memory.Allocate` (which inference-by-default
grants implicitly).

Result: `can Memory.Heap` appears in exactly the signatures that do slow allocation —
visible at the boundary, propagated to callers. The cheap path is unchanged and unadorned.

Design decision: **subsumption.** `Memory.Heap` should imply `Memory.Allocate` (heap is a
kind of allocation) so a `can Memory.Allocate` caller isn't surprised — this needs the
effect-subsumption families noted in the effect-system gap analysis. If subsumption isn't
ready, treat them as siblings and require both at the malloc site.

### 2. `@hot` — the fast contract (keystone)

A function/block annotation that **forbids the slow effects in its body**: `Memory.Heap`,
`Unsafe.*` (configurable subset), and later `Perf.*`. The compiler rejects any such effect
under `@hot`, transitively (a `@hot` function may only call functions provably free of the
banned effects).

```elisa
@hot
def integrate(bodies: mutable darray[Body]&, dt: f32):
    for b in bodies:          # region/handle work: fine
        b.pos <- b.pos + b.vel * dt
    # x: Node& = new[heap] Node(...)   # ← compile error under @hot: Memory.Heap forbidden
```

A beginner marks the inner loop `@hot` and **cannot** accidentally make it allocate or
chase raw pointers. Frictionless if unused; a hard, local guarantee when used. This is the
cleanest embodiment of "the hot path is enforced-fast." Implementation reuses the existing
effect-checking machinery — `@hot` is an effect *upper bound* (the complement of `can`).

### 3. `slow:` quarantine blocks (mirror of `trusted:`)

Exactly the `trusted Unsafe.*:` pattern, for cost. Slow operations (heap alloc, raw
pointer graph traversal, heavy copy) must sit inside a `slow:` block, making them
syntactically visible and greppable. `@hot` simply forbids `slow:` blocks in its body.

```elisa
slow:
    legacy: Node&? = malloc_node()   # loud, intentional, reviewable
```

The teaching value: a beginner typing `slow` is being told, in the grammar, to question
what they're doing.

### 4. Pointer-graph + churn lints (tactical teeth)

Static analyzer passes (the precedent is `analyzer_sentinel_index.go`):

- **Pointer-graph lint**: a struct with ≥2 raw `heap T&?` fields, or one recursive through
  raw refs (not handles), or building a cyclic raw-ref graph → *"this is a pointer-chasing
  graph; prefer a packed enum or a `Store` handle"* with the alternative shown.
- **Churn lint**: `new` / heap allocation inside a loop → *"per-iteration allocation; use a
  builder, a preallocated `Store`, or hoist the region."*

Warnings by default; errors under the strict flag (below). Note: a single-region `@r`
ref-graph is **not** flagged — it is sound (shared lifetime). Only *raw / cross-lifetime*
graphs are.

### 5. Graduated strictness + teaching errors

- `-Wperf` (or a project setting) selects the severity: **off/warn while prototyping,
  error in production.** Fast iteration stays loose; shipped code gets strict.
- **Every friction diagnostic names the fast path.** Hitting `Memory.Heap`-required prints
  "region allocation is cheaper; use `in r:` or rely on inference." A pointer-graph lint
  links to the `Store`/packed idiom. The beginner is *redirected*, not just blocked — this
  is the actual pit-of-success mechanism.

## How they compose — a worked example

```elisa
# Frictionless fast+safe (no annotations needed):
def build_scene() -> darray[Entity]:        # inference gives a region; contiguous; safe
    es: mutable darray[Entity] = []
    for i in 0..<1000: es.push(Entity(i))
    return promote es into caller_region     # O(n) copy is a VISIBLE verb, not hidden

# Enforced-fast inner loop:
@hot
def step(es: mutable darray[Entity]&, dt: f32):
    for e in es: e.advance(dt)               # compiler guarantees: no heap, no raw chase

# Opt-in slow, loud and quarantined:
def load_legacy() -> RawGraph can Memory.Heap:   # advertised in the signature
    slow:
        return build_raw_pointer_graph()         # greppable, reviewable, never accidental
```

The fast path carries no ceremony; every slow choice is a word you had to type.

## Staged implementation plan

1. **`Memory.Heap` effect** + route malloc/global-heap allocation through it (region alloc
   unchanged). Subsumption `Heap ⊑ Allocate` if available. *(Foundation; small, additive.)*
2. **`@hot` contract**: parse the annotation, enforce the banned-effect upper bound via the
   existing effect checker, with a teaching diagnostic. *(Keystone.)*
3. **`slow:` block**: grammar + grant the cost effects within; `@hot` forbids it.
4. **Pointer-graph lint** + **churn lint**, warning-level, with fix-it text.
5. **`-Wperf` graduated severity** wiring + the teaching-error pass over all the above.
6. *(Later)* `Perf.Indirect` / `Perf.Copy`, SoA-layout nudges.

Each stage is independently shippable and the fast path stays untouched throughout.

## Non-goals / caveats

- **Not algorithmic.** We nudge memory patterns, not big-O. Say so in the docs so the
  guarantee isn't oversold.
- **The fast path must stay ceremony-free.** All friction lands on slow *operations*; if
  the common case ever needs a new annotation, the design has failed.
- **Prototyping must stay fluid** — hence graduated strictness; experiments run loose,
  shipped code runs strict.
- **`@hot` is structural, not a benchmark.** It guarantees no banned *effects*, not that
  the code is actually fast — a `@hot` O(n²) loop still compiles. It removes accidental
  *overhead*, not bad algorithms.

## Open questions

- Does `Memory.Heap` subsume `Memory.Allocate`, or are they siblings? (Depends on
  subsumption-family support.)
- Should `@hot` ban *all* `Unsafe.*` or a configurable subset (raw deref yes, but maybe
  `Unsafe.UncheckedIndex` is *desired* in hot code)?
- Is `slow:` a new keyword, or sugar for an effect-grant block (`can Memory.Heap:` already
  grants — `slow:` could be the cost-family analogue)?
- Default severity: warn or off? (Leaning warn, so the nudge is present but non-blocking.)
