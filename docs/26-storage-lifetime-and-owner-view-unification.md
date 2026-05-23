# Storage, Lifetime, and the Owner/View Unification

## Why this document exists

Three safety features landed recently — zeroed/invalid-zero definite assignment,
darray-region escape, and the buffer-overcast/unbounded-cstr warning — each as a
separate ad-hoc pass. They all turn out to be corollaries of **one** invariant
that the type system is not yet enforcing directly. This document unifies them,
fixes a real lie in the current type vocabulary (`static u8&` used for
region-lifetime data), and shows that the same axis already drives thread safety
(`sendable`) and optimization legality. The motivating workload is the PS4
emulator, where laundering region bytes as `static u8&` is the dangling-borrow
class we keep chasing.

## The two orthogonal axes

The confusion in the current vocabulary comes from packing three independent
properties into single type names. Pull them apart into two axes.

### Axis A — ownership shape (the object vs. its storage)

> A resource is two things: the object and the storage that backs it. A view is
> an object that borrows someone else's storage. An owner bundles both.

| role | of bytes / `u8` | of elements `T` |
|---|---|---|
| borrow of 1 | `u8&` | `T&` |
| borrow of N, **bounded** (carries length) | `sview` ≡ `view[u8]` | `view[T]` / `darrayview[T]` |
| borrow, **unbounded** (nul-terminated) | `cstr` | — |
| owner, **fixed size** (size known at compile time) | `sstr` ≡ `u8[N]` | `array[T, N]` / `T[N]` |
| owner, **dynamic** (size known at runtime) | `dstr` | `darray[T]` |

A string is just the `u8` specialization of an array, with a length/terminator
invariant. `sstr` *is* `array[u8, N]`; `dstr` *is* `darray[u8]` + that invariant.
We are not adding a parallel string subsystem — we add string semantics on top of
the array owners that already exist.

### Axis B — storage class = lifetime (where the house stands, how long)

This already exists partly in the compiler as `RefStorage`
(`Any/Heap/Stack/Static`) and as the storage qualifiers in doc 04 / doc 09.

| qualifier | storage | lives until |
|---|---|---|
| `stack` | call frame | enclosing scope ends |
| `region R` | a named bucket of heap | region `R` is reset/freed (bulk) |
| `heap` | individual heap allocation | dropped (RAII) |
| `static` | program image / globals | program exit |

A **region** is a sub-kind of heap with bulk free. A borrow *reports* the Axis-B
of whatever it points at; an owner *chooses* the Axis-B of its backing storage.

### The key correction

`static` is a **lifetime**, not a shape and not "unbounded."

- `static T&` = a borrow whose lifetime is the whole program (`const char*` to a
  literal). Bounded-ness is independent: `static sview` is long-lived **and**
  bounded; perfectly legal.
- Unboundedness is `cstr`'s property alone, at any lifetime. The buffer-overcast
  warning must key on **unboundedness**, never on `static`.

The current compiler conflates these: `isUnboundedStringRefType` keys on
`RefStorageStatic + u8`, treating "static" as if it meant "unbounded C-string."
That is the naming bug to fix.

## The single safety invariant

Everything reduces to one rule:

> **A borrow may not outlive the storage it points into.**

- `static` borrows: storage never dies → free to return/store anywhere.
- `region R` / `stack` borrows: bounded by R's / the scope's lifetime → returning
  one past its scope is an error.
- `copy` / `clone` are the **only** sanctioned way to extend a lifetime. They do
  not relabel a borrow with a longer lifetime (a lie); they build a *new owner*
  with a longer-lived storage class, and that owner's borrow is then legal.

The three landed features are corollaries:

- **zeroed/invalid-zero**: a borrow whose storage is the zero value points at no
  live storage → violates the invariant at use.
- **darray-region escape**: returning a `darray` grown in `region R` past R's
  reset is a borrow (the darray's interior pointer) outliving its storage.
- **buffer-overcast / unbounded-cstr**: erases the length of a bounded borrow,
  letting reads run past the storage the index proves live.

## `copy` vs `clone`: the lifetime bridge

| op | produces | storage class | size requirement |
|---|---|---|---|
| `copy` | fixed-size owner (`array[T,N]` / `sstr`) | **stack** (enclosing scope) | size **statically known** |
| `clone` | dynamic owner (`darray[T]` / `dstr`) | **region** (active `in <owner>:`) | runtime size OK |

Rules:

- **`copy` is static-size-only.** Copying a runtime-length view to the stack is a
  **compile error that points to `clone`** (no VLAs → no unbounded stack growth,
  the same hazard class the overcast warning guards).
- **`clone` requires an active region** (`in <owner>:`); its house is built in
  that region, never via raw `malloc`.
- Cloning/copying a **view** yields the **owner of what it views** — never another
  view. `clone[sview]` → `dstr` (owned), not an `sview` (borrowed). This is the
  correction that motivated the whole redesign: making a view "own" its bytes
  while keeping the view type is the dangling lie.

This also turns the buffer-overcast warning from a dead-end nag into a **cure**:
the sanctioned way to persist a bounded string is `clone` it into a `dstr`, not
cast it to an unbounded `static u8&`.

## The lie this fixes (motivating example)

`emulator.elisa`, `Emulator_Substr` and its siblings:

```
out: mutable darray[u8] = []
in owner:                       # owner = global emulator_arena, a REGION
    ... out.push(...)
return out[0].ref[static u8&]   # claims program-long lifetime
```

Those bytes live in `emulator_arena` (region lifetime, reset on
`Emulator_ResetArena()`), but the signature stamps them `static u8&`. The label
is `static`; the truth is `region<emulator_arena>`. Under the unified model this
cast is **illegal** (region borrow ≠ static borrow), and the fix is to return an
owned `dstr` — exactly the persist primitive `clone` provides.

## Thread safety falls out of the same axis

Doc 09's `sendable(T)` already keys on Axis B:

- sendable: `static T&`, frozen-store-derived values, plain data.
- not sendable: `stack T&`, named-region refs, plain `T&`, mutable `heap T&`.

So unifying lifetimes **is** strengthening thread safety — the borrow's storage
class is what decides whether it may cross a thread boundary. Once Axis B is a
first-class part of the borrow type and the outlives-storage check is real,
`sendable` becomes a direct read of the qualifier instead of a separate
heuristic. A `dstr` produced by `clone` into a thread-shared region is sendable
exactly when that region outlives the thread — same invariant, same machinery.

## Optimization legality from the same axis

Storage class is also escape/aliasing information:

- A `stack`-owner that is never borrowed past its scope cannot alias heap → safe
  to keep in registers / SROA, no escape.
- A `static` borrow is immutable-provenance and never freed → hoistable loads,
  no reload across calls.
- A `region R` owner's bytes are stable until R resets → views into it are valid
  to cache across calls that don't touch R (doc 11, proof-carrying views).

"Safety that makes things fast": the same qualifier that proves the borrow safe
also proves the alias/escape facts an optimizer needs.

## Implementation ladder

Each slice is independently shippable.

### Step 1 — `copy`/`clone` array symmetry (no lifetime work)

Owners already exist. Wire:
- `copy(view[T] | array[T,N])` → `array[T,N]` (stack), static-size-only.
- `clone(view[T] | darray[T] | array[T,N])` → `darray[T]` (region) — already
  deep-copies; formalize the result type as the dynamic owner.

Validates Axis A on the simpler half before touching strings or lifetimes.

### Step 2 — strings as the `u8` specialization

- `sview` ≡ `view[u8]` (drop the bespoke `SViewType`; make it sugar).
- Introduce `dstr` (owned dynamic string ≡ `darray[u8]` + nul/length invariant)
  and `sstr` ≡ `u8[N]`.
- `copy[sview]` → `sstr`; `clone[sview]` → `dstr`.
- Reclassify `DStrType`: today it is surfaced everywhere as `cstr` (a borrow), so
  repoint the *name* `cstr` at the unbounded borrow and free `dstr` for the owner.

### Step 3 — lifetime on the borrow + the outlives-storage check

- Make Axis B a checked part of every borrow type (`RefType.Storage/Region`
  already carries the data; add the lifetime check).
- Then the `emulator.elisa` `static u8&` lie becomes a compile error, fixed by
  returning `dstr`.
- Fold the three existing ad-hoc passes into corollaries of the one invariant.
- `sendable` becomes a direct read of the qualifier (doc 09 alignment).

Step 3 is where the real safety lands; steps 1–2 are derisking groundwork.

## Decisions locked in

1. `static` is a pure lifetime qualifier; `cstr` is the unbounded borrow; the
   overcast warning keys on unboundedness, not `static`.
2. `sview` = `view[u8]`; strings ride the array machinery.
3. `copy` → fixed-size stack owner, static-size-only (error-to-`clone`).
   `clone` → dynamic region owner, requires active region.
4. Cloning/copying a view yields the **owner**, never a view.
5. `dstr` immutable owned snapshot first; growable (push/append, carrying a
   region handle like `darray`) is a later extension.

## Regions make refcounting opt-out, not default

A consequence worth stating: with regions + borrows as the default model, shared
reference counting (`Rc`/`Arc`, `shared_ptr`) stops being a core mechanism.
Refcounting answers "when is this object freed?" for shared, non-nesting
lifetimes; regions answer it structurally — many objects share the region's
lifetime and are freed in bulk, so the classic `Rc` use case (a graph/tree of
nodes pointing at shared data with no clear owner) becomes "put it in a region,
free the region at once." Views provide the sharing (unlimited aliasing), the
outlives invariant provides safety, and there is no per-object count. Regions are
strictly better than `Rc` for cyclic data (no leak, no `Weak`) and have zero
per-object / atomic overhead.

Refcounting is NOT eliminated, only demoted to a rare opt-in library type, for the
genuine cases regions don't cover: non-nesting data-dependent individual
lifetimes, incremental reclamation under churn in a long-lived region, and true
cross-thread shared *ownership*. Shared non-memory resource cleanup is handled by
the affine-capability axis (doc 09), not regions. Net: regions are static
per-phase lifetime management; refcounting is dynamic per-object — and per-phase
covers the overwhelming majority of systems/compiler/emulator code.

## Implementation status (this branch)

- **Step 1 — `copy`/`clone` array symmetry: DONE.** `clone(view|array|darray) ->
  darray[T]` already existed; added `copy[array[T,N]](src)` = fixed-size stack
  owner, static-size-only, runtime-length sources error-to-`clone`. Semantic +
  backend + tests.
- **Step 2 — strings as u8 owners: DONE (core).** `dstr` introduced as the u8
  specialization of `darray` (same representation/ops, displays as `dstr`).
  `clone[dstr](sview)` / `clone[darray[u8]](sview)` deep-copies a *bounded* view's
  bytes into the region — the sanctioned persist the overcast warning points at.
  Deferred: `cstr -> dstr` (needs runtime strlen; the unbounded path) and the
  emulator migration off `static u8&` (needs string ops on `dstr` + Step 3 graduating to error).
- **Arena tail-growth fast path: DONE.** `arena_realloc` now grows a block in
  place when it sits at the region's bump cursor (zero copy); the relocation
  fallback still moves the buffer, so interior borrows across a non-tail grow must
  be invalidated by the front end (tracked).
- **Step 3 — outlives check: FIRST SLICE DONE.** A cast that widens a borrow to a
  longer-lived storage class (explicit `stack->static`, or a provably
  local/region-rooted borrow cast to `static`) requires the `Unsafe.PointerCast`
  opt-out, integrated with the existing unsafe-cast warn + permission-inference
  passes. Honest cases (string literal, `static`-param reborrow) are not flagged.
  Enforcement runs in real builds (emit/native/server set
  `EnforceUnsafePermissions`), currently at WARNING level + signature propagation,
  not hard error. Remaining: graduate to error after the emulator migrates;
  return-site outlives check; darray relocation borrow-invalidation;
  signed-index lower-bound check (see audit follow-ups / task list).
