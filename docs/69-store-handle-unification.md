# 69 — Store / Handle unification (shared surface, distinct backings)

## Goal

Give the four "handle-into-store" abstractions one **shared user-facing surface** while
keeping their representations and safety models distinct. The surfaces today:

| Surface       | Handle                                   | Backing                      | Safety model                          |
|---------------|------------------------------------------|------------------------------|---------------------------------------|
| `darray[T]`   | `usize` index (raw ptr + count + cap)    | arena realloc, 2× growth     | region escape (compile-time)          |
| `dict[K,V]`   | key (5-field header, linear probing)     | arena realloc                | region escape                         |
| packed store  | encoded `uintptr` (region_idx≪32 \| off) | region chain + SoA columns   | freeze / publish gate                 |
| `Pooled[T]`   | raw `heap T&` (affine)                    | free-list in an arena        | affine consume + interior-borrow taint|

**Chosen shape (user decision):** *shared surface, distinct backings.* NOT one concrete
mega-type. The design principle from `docs/10` is load-bearing:

> packedness is layout, regions/stores are provenance, and affine protocol state is usage.
> None of those should silently imply the others.

So the unification is a **protocol/interface** all four satisfy, with layout / provenance /
usage staying independent dimensions underneath. A `Store[T, Backing]` mega-type is
rejected precisely because it would make layout imply provenance imply usage.

## The surface

Elisa's `static interface` mechanism is the right tool: associated types + methods,
`impl X for Type`, generic bounds `[S: Store]`, associated-type projection `S.Handle`,
fully monomorphized (zero-overhead static dispatch). See
`compiler/src/semantic/static_interfaces.go`, `analyzer_generics.go`.

Bounds are **bare names** (`[S: Store]`, parser stores `InterfaceBound string`), so the
element type CANNOT be an interface type parameter (`[S: Store[i64]]` is unsupported).
Therefore element type is an **associated type**, not a parameter:

```elisa
static interface Store:
    type Elem
    type Handle
    def store_get(self&, h: Handle) -> Elem&        # the defining op: handle -> value
    def store_count(self&) -> usize

# Generic code over ANY store:
def total[S: Store](s: S&) -> i64:                  # S.Elem must be i64-ish in practice
    ...
```

The **core** surface is deliberately minimal — `store_get(handle) -> Elem&` + `store_count`
— because that is the only thing genuinely common to all four ("a handle resolves to a
value, and there is a size"). The **differing** operations stay off the core protocol and
either remain on the concrete types or move to sub-protocols later:

- `add`/`push`/`insert`: darray appends (returns index), dict needs a key, pool `acquire`
  returns an *affine* handle, packed `move … as …` returns an encoded handle. Different
  signatures and ownership → a `GrowableStore`/`KeyedStore`/`PoolStore` sub-protocol, not
  the core.
- `remove`: only some backings; ownership differs (affine consume for pool).

### How each backing maps onto the core

| Backing      | `type Elem` | `type Handle`        | `store_get`                         | `store_count`     |
|--------------|-------------|----------------------|-------------------------------------|-------------------|
| `darray[T]`  | `T`         | `usize`              | `items[h]`                          | `count` field     |
| `dict[K,V]`  | `V`         | `K`                  | `arena_dict_get(self, h)`           | `count` field     |
| packed store | enum `E`    | encoded `uintptr`    | `ctx_packed_store_decode(h)` + cast | column length     |
| `Pooled`-pool| `T`         | `Pooled[T]` (affine) | `h.ptr`                             | live-slot count   |

Note the handle types are genuinely different (index / key / encoded word / affine
pointer) — which is exactly why `Handle` must be an associated type and why a single
concrete handle representation was rejected.

## BLOCKER (prerequisite): parametric / blanket impls

The static-interface impl registry keys impls by **exact concrete-type identity**:

```
StaticImplLookupKey(iface, recv) = iface + "|" + fmt.Sprintf("%T:%s", recv, recv.String())
```

(`compiler/src/semantic/static_interfaces.go:61-71`). Lookup is an exact map hit. So:

- `impl Store for darray[i64]` registers under key `Store|*DArrayType:darray[i64]` and only
  matches a concrete `darray[i64]`.
- `impl Store for darray[T]` would register under `…:darray[T]` and **never match** a
  concrete `darray[i64]` — there is no type-parameter unification at lookup time.

**Consequence:** you cannot write ONE impl of `Store` that covers `darray[T]` for all `T`.
A user-level `Store` interface over the generic containers is **not expressible today**.

This is the true first build. Two ways forward:

1. **Parametric (blanket) impls** — extend the impl system so `impl Store for darray[T]`
   matches any `darray[·]` via type-param unification at lookup, with `Elem`/`Handle`
   associated types resolved under the matched substitution. This is the general fix and
   independently useful (blanket impls are broadly valuable). Largest, but unlocks the
   whole surface and is the principled answer.

2. **Compiler-internal Store protocol** — since all four backings are compiler *builtins*,
   the shared surface could be provided by the compiler directly (every container gets
   `.get(h)` / `.count` / handle iteration) without a user-written `impl`. Cheaper to ship,
   but NOT user-extensible (a user `struct` can't become a `Store`). Reading (i) of "shared
   surface"; loses the extensibility that a real interface gives.

**Recommendation:** parametric impls (path 1). It is the load-bearing prerequisite, makes
the `Store` surface a real user-extensible interface, and is a generally valuable language
feature beyond this unification. Path 2 is the fallback if a built-in-only surface is
deemed sufficient.

## Staged plan

0. ✅ Map the four backings + the interface mechanism + identify the blocker (this doc).
1. **Parametric impls** for static interfaces: type-param unification in
   `LookupStaticImpl`, associated-type resolution under the matched substitution, monomorph
   at the bound call site. Land with `impl`-for-`darray[T]` tests independent of `Store`.
2. Define `static interface Store` (core: `Elem`/`Handle`/`store_get`/`store_count`) in the
   stdlib; `impl Store for darray[T]` as the first backing (Handle = usize). Generic
   `[S: Store]` test that consumes a darray.
3. `impl Store for dict[K,V]` (Handle = K), then the pool and packed store (their Handle
   types carry the affine / encoded representations; safety models stay on the concrete
   types, surfaced via sub-protocols).
4. Sub-protocols for the differing ops (`GrowableStore.add`, `KeyedStore`, `PoolStore` with
   affine acquire/release) once the core is proven.

## Non-goals / preserved invariants

- Do **not** collapse the safety models. Affine consume (pool) and freeze/publish (packed)
  stay distinct; the core `Store` protocol is read/size only and says nothing about usage.
- Do **not** unify the handle representation. `Handle` is associated, per-backing.
- Region provenance (`@r`, escape checks) is unchanged — it is the "provenance" dimension
  and stays orthogonal to the `Store` (layout) surface.
