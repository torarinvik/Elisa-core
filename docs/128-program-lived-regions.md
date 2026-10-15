# 128 — Program-lived named regions

> Status: **design, approved direction (option b).** A region declared once per program
> that lives until the program ends, named in types like any other region and checked by the
> same outlives lattice. Motivating use: a type fact for "this view points into data that
> lives for the whole run" (e.g. a compiler's source text), without threading a region
> parameter through every signature. Builds on docs/68 (region model), docs/75
> (region-polymorphic functions), docs/91 (global death-time inference).

## 1. Syntax

A top-level declaration:

```
global region source
```

- `global region NAME` declares a **program-lived region** `NAME` in the current module.
  No body, no backing clause in v1 (the backing is the runtime's program arena, §5).
- `NAME` is used exactly like any region name: `sview @source`, `darray[T] @source`,
  `Node[source]`, `new[source] T(...)`, a region argument at a call `f[source](...)`.
- Visibility follows the module's `public:` / `private:` sections; another module names it
  qualified (`Mod::source`) or through `using`.
- A program-lived region is not a binder: it cannot appear in `[@r]` parameter lists and
  cannot be shadowed by a parameter or `region r:` block of the same name (error).

## 2. Outlives rules

Let `G` be the set of program-lived regions. For any region `r`:

1. `g ⊒ r` for every `g ∈ G` and every region `r` (function region parameter, `region r:`
   block, inferred `__auto_N`, container region): a program-lived region outlives
   everything that can be named.
2. Two program-lived regions outlive each other (`g1 ⊒ g2` and `g2 ⊒ g1`): they die
   together at program end.
3. No other region outlives a program-lived one: `r ⊒ g` holds only when `r ∈ G`.

So a value **in** `g` may be stored anywhere (it is never freed first), and a slot **typed**
`@g` may receive only values whose every live dependency is program-lived (rule 3).
Nothing else changes: rules 1–3 are new cases of the existing `regionOutlives` lattice, not a
separate check.

## 3. Checking

- Every existing store check consults the lattice, so it covers program-lived regions with
  no new diagnostic: assignment `x.f <- v`, struct literal fields, container growth
  (`push`/`extend`/`insert`), call arguments through store summaries, returns typed
  `@g`, and region-param binding at calls (`f[source](..)` binds `@r := source`).
- A value's provenance is program-lived only when proven: a literal, a view of a value
  already in `g`, an allocation `new[g]`, or a region-polymorphic call whose result region
  was bound to `g`. Anything else keeps its own dependencies and is rejected by rule 3.
- Generic instantiation substitutes `g` like any region argument (`Node[source]`), for
  struct **and enum** region parameters; binders of such instances carry `g` as provenance.
- There is no unchecked escape hatch: storing a shorter-lived view into an `@g` slot is an
  error unless the user copies it into `g` first (`new[g]` / a `g`-bound copy).

## 4. Interface (.elisair) recording

- The module's export table gains a `GlobalRegion` row: name, owning module, visibility.
- Types that mention the region record it by qualified name (`Mod::source`), so an importer
  resolves it to the same program-lived region; a mismatch (unknown name) is an iface error,
  never a silent fallback to a local region.
- Struct / enum region parameters are recorded as today for structs (`RegionParams`), now
  also for enums; instantiations record their region arguments.

## 5. Runtime

The program arena: allocated on first use, never reset, released by the OS at exit. A
`new[g]` or a `g`-bound region-polymorphic call allocates there. Nothing is freed early, so
rule 1 is sound by construction. Memory held for the whole run is the explicit cost the
declaration states.

## 6. Relation to global death-time inference (docs/91)

- docs/91 infers *death cohorts* for un-annotated allocation. A program-lived region is an
  *explicit* cohort whose death point is fixed at program end: inference never moves it,
  never frees it early, and never merges an inferred cohort into it implicitly.
- In the death-time lattice it is the top element. An inferred cohort whose death point is
  "program end" may be reported (`--explain regions`) as a candidate for a declared
  program-lived region, but is not converted automatically.
- Nothing here depends on docs/91 landing; both use the same outlives lattice.

## 7. Extension points (kept open)

- Region **relations** beyond "lives to the end" (e.g. a declared ordering between two
  named long-lived regions) slot into §2 as additional lattice facts.
- A backing clause (`global region source: reserve_commit(1 GiB)`) can be added to §1 without
  changing the checking rules.
- Verification: the lattice facts are explicit, so a prover can consume them as axioms.

## 8. Addendum — declared store region on store-backed enums (approved)

A store-backed enum (a recursive plain enum, or a sealed hierarchy root with a `layout`
suffix) normally places its node store in whatever region the construction site infers. A
root may instead declare the store's region:

```
enum Node layout(handle: u32) @source:
    pass
```

- The suffix is allowed only on a hierarchy root or a non-hierarchy store-backed enum, and
  only with a program-lived region (v1). Refinements (`enum Expr is Node`) inherit it.
- Every construction of a variant allocates its row in that region's store, whatever the
  ambient region; the backend uses the program arena of §5.
- **Binder provenance.** A value of such a type is a handle into a program-lived store. It
  counts as program-lived for view provenance **only when every payload field of every
  variant (and every common field) is itself program-lived**: scalars, other such handles,
  or region-carrying types whose region is program-lived (`sview @source`,
  `darray[T] @source` with T satisfying the same rule). Otherwise the binder keeps its
  current, unrecorded status and stores of it are judged as before.
- Payload stores are checked by rule 3 (§2) at the constructor, so a declared store never
  holds a shorter-lived view.

Cost: the store and everything its payloads point to live until exit. A compiler that
declares its AST this way keeps the whole AST for the whole compile; the probe report
records the peak-RSS change.

## 9. Open: merge with `perm`

stage0 already has a builtin program-lifetime ambient region `perm` (`in perm:`). It is not
a declared region and is not part of the §2 lattice; region-polymorphic calls inside
`in perm:` bind to it. Declared program-lived regions reuse that binding rule. Merging the
two (making `perm` a predeclared `global region perm`) is deferred.

Deferred: enum region **parameters** (`enum E[@r]`) — not needed once the store region is
declared; added when a failing case needs them.
