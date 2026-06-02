# Effect / Permission Phases (3–5) Mini-Spec

This specifies the remaining effect-system work agreed with the design discussion:
member-granular permission sets with a **subsumption lattice**, **checked `as` casts**
(plus the existing `trusted` drop), and **set-polymorphism** for higher-order functions.
It is the effect-side counterpart to the error-union work (`docs/62`), and deliberately
reuses one set lattice for both.

## Implementation status

- ✅ **Phase 3a** — member-brace sugar (`can[Disk{Read,Write}]`, `error[E{A,B}]`).
- ✅ **Phase 3b** — subsumption-declaring families (`permission IO: includes Disk`),
  transitive grant expansion + unknown/cycle validation.
- ✅ **Phase 4** — checked `can X as Y:` cast (sound iff `Y ≥ X`); `trusted X:` drop
  was already implemented.
- 🔬 **Phase 5** — set-polymorphism. **Measured 2026-06-02: ~80% already implemented.**
  Permission generic params (`def f[permission E](...)`), function types with `can[E]` /
  `error[R]` annotations (`func(T) -> U can[E] error[R]`), call-site inference/binding of
  `E`, union-with-literal addition (`can[E, Term.Write]`), and error propagation through
  function-type params (`try f(x)`) **all already parse, analyze, and check correctly** — the
  earlier "fails to parse" note used the wrong syntax (`(T) -> U` / `[perm E]` instead of
  `func(T) -> U` / `[permission E]`). Two genuine deltas remained:
    - ✅ **Phase 5a — `any`/⊤ escape** (`can[any]`): a `can[any]` grant satisfies every
      requirement; a `can[any]` requirement is satisfied only by `any` (or `trusted`); `any`
      is reserved (cannot be declared, no member access). Implemented + tested.
    - ⏳ **Phase 5b — generic *error*-set param** (`[errorset R]`): the symmetric mirror of
      permission params for error sets. Concrete error sets already flow through `func`-type
      params; only the *polymorphic* error binder is missing. Not started.

## 0. Current state (measured 2026-06-02)

Probed via `elisacore -emit semantic`:

- ✅ **Dotted member granularity** — `can[Disk.Read, Disk.Write]` parses + analyzes.
  Member-level effects already exist via the dotted form (`Perm.Member`).
- ✅ **`trusted X:` drop** — `trusted Disk.Write:` analyzes and drops the effect from the
  signature. **Phase-4 "drop" is already implemented** (it is the existing `trusted` block,
  e.g. `trusted Unsafe.UncheckedIndex:`). No work needed beyond making it the canonical
  drop spelling.
- ✅ `permission Name:` families with members; `can X:` grant blocks.

So Phases 3–5 are deltas on a partially-built system, not a from-scratch build.

## 1. The duality (shared lattice with errors)

Effects and errors use the **same set lattice**, mirror variance:

| | Effects (`can[…]`) | Errors (`error[…]`) |
|---|---|---|
| Direction | **required** (contravariant) | **produced** (covariant) |
| Sound `as` direction | widen to a **superset** capability (Y ⊇ X) | (errors don't use `as`; map via `try/match`) |
| Drop | `trusted X:` | n/a (affine — must consume) |
| Handle/discharge | grant block / propagate | `try` / `match` / `catch` |

A capability/error set is a set of `(Family, Member)` pairs; `Family` bare = all members.
Subtyping = subset. The lattice top is `any`/⊤. This is the same machinery `error[…]`
already uses; Phases 3–5 generalise it to permissions and add casts + polymorphism.

## 2. Phase 3 — member-brace sugar + subsumption families

### 2a. Member-brace sugar (bounded; dotted already works)
```
can[Disk{Read, Write}]      ≡  can[Disk.Read, Disk.Write]
error[E{Bad1, Bad2}]        ≡  error[E.Bad1, E.Bad2]
```
Pure parser desugar — expand `Family{M1, M2, …}` into repeated `Family.M` items.
- Permissions: `parsePermissionRefs` (the `can[…]` parser).
- Errors: `parseErrorSetItem` (the `error[…]` parser).
Emit one ref/tag per member. No semantic change.

### 2b. Subsumption-declaring families
```
permission IO:
    includes Disk, FileSystem        # IO subsumes these whole families
    Spawn                            # ...and may add its own members

permission Opaque:
    pass                             # opaque: subsumes nothing; every `as Opaque` is trusted
```
- Grammar: a family body may contain `includes <Family> (, <Family>)*` lines in addition
  to member declarations.
- Semantic: build a **subsumption relation** `≥` over families. `Y ≥ X` iff `X` (or each
  of its members) is reachable from `Y` through `includes` (transitive), or `Y` is a
  super-family of `X` (a family ⊇ any of its own members). Detect/҂reject cycles.
- The lattice: `set ≤ set'` iff every `(F,M)` in `set` is subsumed by some `(F',M')` in
  `set'` under `≥`. Reuse for both `can[…]` and `error[…]`.

## 3. Phase 4 — `as` (checked) and `trusted` (drop)

Inside a grant block, re-attribute the used capability:
```
can Disk{Read, Write} as IO:    # CHECKED: legal iff IO ≥ Disk{Read,Write}; surfaces as IO
    ...
can Disk{Read, Write} as Disk:  # CHECKED: a family subsumes its members (always legal)
    ...
trusted Disk.Write:             # DROP: not surfaced; the trust marker (already implemented)
    ...
```

**The one rule:** `X as Y` is sound iff `Y ≥ X` in the lattice (Phase 3b). Then it needs no
trust and the compiler verifies it. If `Y ⊉ X`, it is **not expressible** — there is no
`trusted … as`; to expose `X` as an unrelated `Y` you must declare `Y: includes X`, which
makes the cast checked. `trusted X:` (drop) is the only trust operation. So:
- `as` = checked-subsumption ONLY.
- `trusted` = drop ONLY.
- No `as _`, no trusted-casts.

Grammar: extend the `can <set>` grant-block header with an optional `as <Family>`.
Semantic: when `as Y` is present, verify `Y ≥ usedSet`; the block's contribution to the
function's inferred `can[…]` is `Y` (not the used members). Effects are erased — **no
backend work**.

## 4. Phase 5 — set-polymorphism (higher-order functions)

Effect and error sets become **ordinary inferred generic parameters**, bound like type
params and inferred by default:
```
def map[T, U, E, R](xs: darray[T], f: (T) -> U can[E] error[R]) -> darray[U] can[E] error[R]:
    out: darray[U] = []
    for x in xs:
        out.push(try f(x))     # `try` unions f's R into map's R; E flows through
    return out
```

- `E` (effect set) appears only on arrows; `R` (error set) also in result types
  (`U error[R]`). Same binder, same inference, same subsumption-lattice instantiation.
- **Addition** is union-with-a-literal: `can[E, Console]` (own effect + callback's). No row
  polymorphism — **subtraction** is not supported (rare; error-handling combinators use a
  concrete set or `any`).
- `any`/⊤ is the explicit erasure escape (FFI, stored heterogeneous closures, dynamic
  dispatch). Bounded quantification `∀E ≤ IO` falls out of the lattice.
- **Monomorphization**: where the specializer sees the callee, `E`/`R` resolve to concrete
  sets per instantiation — full precision, no separate effect-checking pass, no runtime
  cost. The surface generic is mainly for contracts + separately-compiled functions.

Grammar gaps to close (both currently fail to parse): **function-type effect/error
annotations** (`(T) -> U can[E] error[R]`) and effect/error **generic params** in `[…]`.
Semantic: unify `E`/`R` like type params; instantiate via the lattice at call sites.

## 5. Non-goals / interactions

- Capabilities stay **ambient/non-linear** (a grant block is scoped authority, freely
  usable). Linearity is reserved for values (error unions, owned handles).
- `trusted` remains gated/greppable (EnforceUnsafePermissions) — `as` is the *non*-trusted
  path, so the two must look different (they do: `as` vs `trusted`).
- Effects are erased; only error sets reach codegen (via the error-union value). So Phases
  3–4 are parser+semantic only; Phase 5 touches generics/inference but not the backend
  beyond what error unions already do.

## 6. Suggested order + risk

1. **Brace sugar** (2a) — bounded, no risk, completes the specced surface for both `can`/`error`.
2. **Subsumption families + `as` casts** (3b + Phase 4) — the lattice is the core; casts are
   thin once it exists. Mid-size, parser + semantic.
3. **Set-polymorphism** (Phase 5) — largest; do after the lattice is solid, since
   instantiation rides on it. Lean on monomorphization first; separately-compiled
   polymorphic effects last.

Do not land the lattice/polymorphism under time pressure — they are type-system features
where a rushed change risks unsound subsumption or inference loops.

## 7. Test plan (mirrors existing semantic/parser tests)

- Parser: brace sugar expands to dotted (both `can`/`error`); `permission … includes …`
  parses; grant-block `as Y` parses; function-type `(T)->U can[E] error[R]` parses.
- Semantic: `as Y` accepted iff `Y ≥ X`, rejected otherwise (suggest `includes` or `trusted`);
  `includes` cycles rejected; `trusted X:` drops X from the inferred set; `∀E` instantiation
  unifies to the callee's concrete set; `any` erases.
- Blast radius: re-analyze the emulator codebase after each phase (expect 0 new errors for
  sugar; the lattice may surface real over-/under-declared `can[…]` — audit those).
