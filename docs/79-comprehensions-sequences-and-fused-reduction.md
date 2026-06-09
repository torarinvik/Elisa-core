# Comprehensions, the Lazy Sequence, and Fused Reduction

This note specifies a single data-shaping layer for Elisa built on **one
concept** — a lazy sequence — with three faces:

1. a **comprehension surface** (`[…]` / `{…}` / `(… with acc =)`) for building
   collections and folding to scalars, concise and lambda-free;
2. a **lazy sequence protocol** (the engine) that **fuses** an arbitrary chain
   of transforms into a single loop with no intermediate allocation; and
3. a curated library of **named consumers** (`sum`, `max`, `all`, `join`, …) that
   are ordinary functions over that sequence, not compiler magic.

> There is one moving part — a lazy sequence. Comprehensions are sugar over it,
> reducers are functions over it, and `by par` / `by simd` are how you ask the
> compiler to run the *same* sequence across cores or vector lanes. The fused
> loop the comprehension lowers to is precisely the loop LLVM can vectorize —
> so the most readable spelling is also the fastest.

Design proposal. Read against [17 Iterators & for-in](17-iterators-and-for-in-mini-spec.md),
[19 Static Interfaces, Extension Methods, UFCS](19-static-interfaces-extension-methods-and-ufcs.md),
[68 Region Memory Model](68-region-memory-model.md),
[70 Performance Friction](70-performance-friction.md),
[78 Termination Measures](78-termination-measures-and-debug-erasure.md), and the
concurrency mini-spec [09](09-concurrency-mini-spec.md). It supersedes the
sequential `fold` combinator and absorbs `each` / `reduce` into a marked surface.

---

## 0. The one concept

A **sequence** yields elements until exhausted. Everything in this document is
either a *source* of one, an *adapter* that wraps one lazily, or a *consumer*
that drains one. The comprehension is sugar that the compiler expands into
exactly these pieces; the pieces fuse; the fused result is a single loop.

```text
source ── adapter* ── consumer
 darray    map         collect[darray]   →  building a collection
 Slice     filter      collect[set]
 range     enumerate   collect[dict]
 dict      zip         sum / max / all / fold / …   →  folding to a scalar
           take/let
```

The user mostly writes comprehensions and consumers; adapters appear as the
`for`/`if`/`let` clauses or as named sources. No `.iter()`, no `.collect()`
ceremony, no method chains.

---

## Part I — The comprehension family (surface)

One skeleton — `(binding,)* body  for bindings  [if …]` — with the **delimiter
choosing the result** and one extra clause (`with acc =`) for the fold:

```elisa
[ x*x        for x in xs if p ]                  # → darray   (body = element)
{ x*x        for x in xs if p }                  # → set      (body = element)
{ x: f(x)    for x in xs if p }                  # → dict     (body = key:value)
( acc + x    for x in xs if p  with acc = 0 )    # → scalar   (body = next accumulator)
( x*x        for x in xs if p )                  # → lazy generator (drained by a consumer)
```

The head is a comma-separated list whose items before `for` are **per-element
bindings**, with the last item the **body**:

> Every head item with a top-level `=` is a binding (`name [: T] = expr`); the
> single item with no top-level `=` is the body.

This is unambiguous because `=` is only a binding marker in Elisa (assignment is
`<-`, equality `==`), so a body expression never carries a top-level `=`. The
`=`/no-`=` split is also the visual cue — helpers carry `=`, the result doesn't.
It retires the mid-clause `let`: helpers move to the head, in front of the body.

```elisa
[ y = f(x), g(y)                  for x in src if y > 10 ]      # one binding + body
[ y = f(x), n: i64 = len(y), h(y, n)  for x in src if n > 0 ]  # typed, multiple
[ g(x)                            for x in src ]               # zero bindings = today's form
```

Clauses:

- head bindings `name [: T] = expr` — name an intermediate; keep multi-stage in
  **one** comprehension (one fused pass) instead of nesting. Each is recomputed
  per element (pure `let` semantics), evaluated left-to-right; later bindings and
  the body and `if` may reference earlier ones.
- `for x in src` — bind each element. `for k, x in src` destructures (e.g. index
  + value from an enumerated source). Multiple `for` clauses nest (Cartesian).
- `if p` — filter on the bound variables and head bindings.
- `with acc = seed` — *fold only*: declares the accumulator and its seed; the
  body computes the next accumulator. Multiple seeds allowed:
  `( … with lo = +inf, hi = -inf )` (body is a tuple matching the seeds).

Parser rule (no special-casing): split the head on **depth-0 commas** (commas
inside `()`/`[]`/`{}` don't count); an item with a **depth-0 `=`** is a binding,
the single item without one is the body. This needs no tuple caveat — Elisa tuple
*values* are already parenthesized (`(a, b)`), exactly as in Python, so a tuple
body is `(a, b)` by the universal rule. The same depth-0-`=` test distinguishes a
typed binding `n: i64 = len(y)` from a dict body `x: y` (binding has the `=`).
"All bindings, no body" is a clean error.

### Desugaring

Each form expands to a loop pushing into a fresh, region-placed collection (the
collection's region follows the binding annotation / auto-region per
[doc 68 §7](68-region-memory-model.md)), or accumulating a scalar:

```elisa
[ y = f(x), e for x in c if p ]
        ⟶  r: darray[_] = []                     # auto-region
            for x in c:
                y = f(x)
                if p: r.push(e)
            r

( e for x in c if p with acc = i )
        ⟶  acc: mutable _ = i
            for x in c:
                if p: acc <- e
            acc
```

`{…}` / `{k:v …}` are identical with a `set`/`dict` sink. The expansion *is* the
fused loop — there is no intermediate sequence value in the common case.

---

## Part II — The lazy sequence protocol (engine) and the fusion guarantee

The surface above can be a pure syntactic lowering (no runtime iterator). The
**protocol** matters when a sequence is passed as a *value* — to a named consumer
(`max(gen)`) or a user function. We choose first-class sequences (Path (a) from
the design discussion) so reducers are ordinary, user-extensible functions.

```elisa
interface Sequence[T]:
    def next(self: mutable Self&) -> T?      # yield a value, or `none` when drained
```

- **Sources** implement it (or yield it via `seq(x)`): `darray`, `Slice`, ranges,
  `dict` (as key/value pairs). `for x in s` already consumes this protocol
  ([doc 17](17-iterators-and-for-in-mini-spec.md)).
- **Adapters** are tiny **stack structs** wrapping an upstream sequence:

  ```elisa
  struct Map[I, F]:  inner: mutable I,  f: F
  def next(m: mutable Map[I, F]&) -> T?:
      v: T? = next(m.inner)
      return none if v == none else some(m.f(v.value))
  ```

  `Filter`, `Enumerate`, `Zip`, `Take`, `Windows` follow. **No adapter
  allocates** — they compose by value on the stack.
- **Consumers** drain: `collect[C]`, and the reducers of Part III.

### Fusion guarantee

Because adapters are by-value generic structs whose `next` is small, the
monomorphized chain inlines into one loop. This is a **specified guarantee**, not
a hope:

> A comprehension, or a bare adapter chain drained by a single consumer, lowers
> to one loop over the source with **zero heap allocation except the final sink**.
> `[y = f(x), g(y) for x in src if p(y)]` and the hand-written
> `for x in src { y = f(x); if p(y): r.push(g(y)) }` emit identical IR.

A `-emit fusion` audit (mirroring `-emit progress`, [doc 25](25-progress-safety.md))
reports, per comprehension, the fused loop and any allocation that survived — so
a fusion regression is visible, not silent.

---

## Part III — Named consumers (the curated library)

Reducers are **stdlib functions over `Sequence[T]`**, not language surface. They
earn a name only when they (a) name a common monoid, (b) **short-circuit**, or
(c) encapsulate a subtlety. The blessed set:

```elisa
sum  product  min  max  count            # monoids (some need empty-handling)
any  all  find  first  contains          # short-circuiting (exploit laziness)
join                                      # separator logic — not a clean fold
collect[C]                                # build any container C
```

```elisa
biggest = max(score(p) for p in players)          # ① encapsulates empty-seed → Option/non-empty
ok      = all(v >= 0 for v in row)                # ② stops at the first false
hits    = count(x for x in xs if x == target)     # ③ clarity-name for a fold
joined  = join(", ", name(u) for u in users)      # ① separator-between, not a plain fold
```

`min`/`max` over an empty sequence return `T?` (or demand non-empty); `any`/`all`/
`find`/`first` **stop early** — a capability the inline `with acc =` fold cannot
express. Anything outside the curated set stays inline `(… with acc =)`. Users
may define their own consumer (`def median[S: Sequence[f64]](s: S) -> f64`) — it
is not special; it is a function over the protocol.

> **Implementation status (Part III).** **P8** lands the first two named consumers,
> `sum x in xs [where p]` and `product x in xs [where p]`, as members of the existing
> query-expression family (alongside `any`/`all`/`first`/`count`/`each`) rather than as
> functions over a not-yet-built `Sequence` protocol — they fold the optionally-filtered
> numeric elements with + (unit 0) / * (unit 1), result type = element type. `where` is
> optional for these reducer kinds. No generic-numeric-trait machinery is needed because the
> query lowers to a monomorphic loop at the use site. `min`/`max` are deferred: they need the
> empty-sequence → `T?` semantics this section calls out (mirroring `first`'s optional result).

---

## Part IV — Performance: fusion → vectorization → parallelism

Performance is the point, and the design is built so the readable form is the
fast form ([doc 70](70-performance-friction.md)). Three escalating levels, one
surface:

### 1. Fused scalar loop (default)

Part II already removes intermediate collections. The fused loop carries the
backend wins already proven on the Stable-Fluids kernels — FMA contraction,
host-CPU targeting, reciprocal hoisting, and scalar-darray header/element
**noalias** (the base-pointer hoist that lets LLVM treat element accesses as
non-aliasing). A clean fused comprehension over a `darray`/`Slice` is therefore
already a candidate for LLVM auto-vectorization with no extra work.

### 2. `by simd` — vectorize one thread's loop

```elisa
ys  = [ a*x + b  for x in xs by simd ]                     # elementwise: vector lanes
dot = ( acc + a[i]*b[i]  for i in 0..<n with acc = 0.0 by simd )   # horizontal reduction
```

`by simd` asks the compiler to lower the fused loop to vector width: an
elementwise map/filter becomes lane-parallel; a fold becomes a **vector
accumulator** with a horizontal combine at the end. Preconditions, checked:

- element type is a vectorizable scalar; body has no loop-carried dependency
  other than the named reduction;
- a **fold under `by simd` reorders the combine**, so the operator must be
  associative. For floating-point this means opting into reassociation
  (fast-math) — `by simd` on an FP reduction *is that opt-in*, and it is explicit
  precisely so the rounding change is visible. (Same contract as `by par`.)

v1 may implement `by simd` as "emit vectorization-friendly IR + force the LLVM
loop-vectorizer + **fail the build if it did not vectorize**" (no silent scalar
fallback), growing later to explicit vector-type lowering for portable widths.

### 3. `by par` — across cores

```elisa
total = ( acc + x  for x in xs  with acc = 0  by par )    # parallel associative reduce
out   = [ heavy(x)  for x in xs by par ]                  # parallel map
```

`by par` does **not** introduce new runtime — it lowers onto the existing,
TSan-clean data-parallel machinery: `parallel for band in slice(xs)` over
disjoint `Slice` bands, with private partials for reductions (the `each` / `reduce`
path, persistent thread pool, [doc 09](09-concurrency-mini-spec.md)). Composes
with level 2: `by par simd` partitions across cores *and* vectorizes each band.

The associativity contract is shared by `by par` and `by simd` and stated once:
**a marked reduction promises its combine is associative; the compiler may
reorder it.** A sequential `(… with acc =)` makes no such promise and is a strict
left fold. The two are visibly different at the call site — no silent
parallelization, no nondeterminism.

---

## Part V — Deprecations (what we remove)

| Symbol | Status | Why |
|---|---|---|
| `fold(da, init, f)` *(sequential combinator)* | **deprecate** | dominated: simple folds → `(… with acc =)`; complex steps → a plain `for` loop. The lambda-taking generic was the awkward middle. |
| `each(s, body, w)` / `reduce(s, id, op, w)` | **keep, re-surface** | distinct capability (parallel/associative). Become the lowering target for `by par`; the bare functions remain for hand-tuned use. |
| `fold` / `visit` / `rewrite` *(keyword catamorphisms)* | **keep, unrelated** | typed recursion over enum hierarchies ([76](76-enum-layout-and-handles.md)/[77](77-enum-hierarchies-and-sealed-refinement.md)), not sequences. |
| list comprehension `[e for x in c if p]` | **keep, extend** | already shipped; gains `let`, `{…}`/`{k:v}` siblings, `(… with acc =)`, `by par`/`by simd`. |

Deprecation follows the project's standard path (warned alias → migration →
removal), as with `.ref` / `.specialize` / `@method`.

---

## Part VI — Interactions

- **Regions ([68](68-region-memory-model.md)).** A `collect`/`[…]`/`{…}` sink
  allocates its result into the binding's region (`xs: darray[T] @r = […]`) or an
  auto-region. Adapters allocate nothing, so an N-stage chain adds **zero**
  intermediate region pressure — lazy sequences are *more* arena-friendly than
  eager pipelines.
- **Effects ([63](63-effect-permission-phases-mini-spec.md)).** A body/predicate
  carrying permissions propagates them to the consumer:
  `sum(read(p) for p in ps)` requires `can` for `read`'s effects at the call site.
  No special-casing.
- **Totality ([78](78-termination-measures-and-debug-erasure.md)).** A
  comprehension over a finite source is **provably total** (the source length is
  the measure — discharged at S0, zero annotation). A comprehension over an
  unbounded generator with no `take`/bound is honestly `can[Partial]`. `take(n)`
  is itself a termination witness.
- **Parallel ↔ Slice.** `by par` reuses the `Slice` split / disjoint-band borrow
  checking already in place, so the safety proof (race-freedom by construction)
  carries over unchanged.

---

## Part VII — Grammar additions

```text
Comprehension := '[' Head CompClauses ']'                       # darray
               | '{' Head CompClauses '}'                       # set
               | '{' Head ':' Body CompClauses '}'              # dict (Head = key, Body = value)
               | '(' Head CompClauses [WithClause] [Mode] ')'   # generator | fold

Head          := (Binding ',')* Body          # bindings (top-level '='), then the body
Binding       := Name [':' Type] '=' Expr
Body          := Expr                          # the sole item with no top-level '='

CompClauses   := 'for' Pattern 'in' Expr (CompClause)*
CompClause    := 'for' Pattern 'in' Expr
               | 'if' Expr
WithClause    := 'with' AccDecl (',' AccDecl)*
AccDecl       := Name '=' Expr
Mode          := 'by' ('par' | 'simd' | 'par' 'simd')
```

Head classification: split on depth-0 commas; an item with a depth-0 `=` is a
`Binding`, the lone item without is the `Body`. No tuple caveat — tuple values
are parenthesized (`(a, b)`) as everywhere else in Elisa, so a tuple body never
introduces a depth-0 comma.

Consumers (`sum`, `max`, `collect[C]`, …) are ordinary call syntax over a
generator argument; `by par` / `by simd` may also follow a consumer’s generator
(`sum(e for x in c by par)`).

---

## Part VIII — Implementation plan (phased)

Each phase ships independently and leaves the tree green.

> **Implementation status (Phase 1, near-complete).** DONE: fold/reduce comprehension
> `(… with acc =)` (`19a2fc54`); dict `{k:v for …}` (`f146802f`); set `{x for …}`
> (`61e6df47`); and comma-head bindings on **all** forms — fold (`af0592e4`),
> list/dict/set (`e14ddc18`). The fold lowers as a pure parser desugar to an
> `ExprBlock`; dict/set extend `ListComprehensionExpr` (`Key` field / `Set` flag) and
> lower to a fused `d.put`/`s.add` loop; head bindings ride a `Bindings` field
> (untyped inside `{…}` to dodge the dict-key `:` ambiguity, typed elsewhere). List
> comprehension pre-existed. Each shipped with a runtime smoke + Go test; full suite
> green. The `fold` combinator deprecation also landed (`7fd98a3e`, via a general
> `@deprecated` annotation). Then Phases 2–4 (lazy `Sequence` protocol + reducers,
> `by par`, `by simd`).
>
> **Vectorization status (verified, `240fcbb9`).** The fusion guarantee already pays
> off at `-O3` *without* any `by simd` marker: fold reductions (f64/i64) and maps
> into a preallocated darray auto-vectorize today (`<2 x double>` +
> `llvm.vector.reduce.fadd`). The one gap was a map building a **fresh** darray via
> `push` — the per-element capacity-check + conditional `arena_realloc` is a
> loop-carried control dependency the vectorizer can't cross, so the build loop
> stayed scalar. **P2a fixes it:** a no-filter list comprehension over a darray
> identifier source now lowers to a presized indexed-store loop
> (`result.resize(src.count); for i: result[i] <- value`), which vectorizes like the
> preallocated case. Filtered / range / non-darray sources keep the push fallback.
> (Caveat for whoever inspects this: at `-O3`, internal functions with no retained
> root are DCE'd, so dump IR through an `export func` wrapper or the module looks
> empty — an earlier pass mis-concluded "nothing vectorizes" from empty dumps.)
> **P2b** extends the indexed-store lowering to range sources
> (`[v for i in start..<end]`, simple bounds): `result.resize((end-start) if end>start
> else 0); for i: result[i-start] <- value`. **P3a** adds a global `-ffast-math` flag
> that unlocks the reassociated *tree* reduction for FP folds (otherwise the default is
> the bit-exact ordered reduction). **P3b** lands the Part IV verifier as a default
> `-Wperf` warning: each indexed-store build loop is tagged with `!llvm.loop`
> `elisa.autovec.expected` metadata, and a post-optimization scan warns for any marked
> loop left without `llvm.loop.isvectorized`. It is inlining-robust (marker rides in IR)
> and false-positive-free (LLVM stamps `isvectorized` on the vector body *and* the scalar
> remainder), and is gated to call-free bodies to stay low-noise.
> **P4** adds the per-fold `by simd` marker: `( acc + x … with acc = 0.0 by simd )` scopes
> the reassociated tree reduction to that one fold (emitting its accumulator update under
> full fast-math FP) without the program-wide `-ffast-math`. Implemented as
> `AssignStmt.FastMath` + a `fastMathScope` counter the backend's FP chokepoint honors.
> **P5** adds the per-fold `by par` marker: `( acc + x … with acc = 0 by par )` lowers an
> associative-combine fold to a contention-free parallel reduction, by desugaring to the
> existing runtime `reduce(slice(&src), seed, op)` combinator (private partials per band,
> merged). Gated to `acc <op> x` with op ∈ {+, *} over a darray source (no filter/bindings/
> range) so the parallel reordering is never silently incorrect — anything else is a parser
> error. This completes the Part IV perf markers (`by simd` + `by par`) for folds. **P5b** lifts
> the `by par` identity-transform restriction via a fused `map_reduce` combinator, so
> `acc + x*x` / `acc + f(x)` (dot products, sum of squares) parallelize too. **P6** extends
> `by simd` to list-map comprehensions (full fast-math on the body, reusing the `fastMathScope`).
> **P7** completes the surface with `by par` on maps: `[f(x) for x in src by par]` is an
> analyzer-side desugar to a presized output filled by the runtime `par_map` combinator over
> disjoint bands. It is unblocked by a new conservative `semantic.Type → ast.TypeExpr` converter
> (needed to declare `out: darray[U]` where U is the analyzer-computed element type); a type it
> can't faithfully rebuild yields a clear error, and since the synthesis is re-analyzed, any
> inaccuracy is an ordinary type error rather than a silent miscompile. **The Part IV perf markers
> (`by simd` + `by par`) are now complete across both folds and maps.**

**Phase 1 — surface + sinks (sequential, no protocol value).** Parse `{…}` set,
`{k:v …}` dict, `(… with acc =)` fold, and the comma-separated **head bindings**
(`name [:T] = e, … , body`); lower all comprehensions to fused loops directly
(pure syntactic desugaring, no runtime sequence). Deprecate the `fold` combinator.
This alone delivers the whole concise surface and the fusion guarantee for
comprehension-shaped code.

**Phase 2 — the `Sequence` protocol + named consumers.** Introduce the protocol,
adapters (`map`/`filter`/`enumerate`/`zip`/`take`), and the curated reducers as
generic functions; make bare generators first-class values so `max(g)` /
user-defined consumers work, with monomorphized fusion. `-emit fusion` audit.

**Phase 3 — `by par`.** Lower marked reductions/maps onto the existing
`parallel for band` / `Slice` / private-partials machinery. Associativity
contract surfaced; sequential vs parallel kept visibly distinct.

**Phase 4 — `by simd`.** Force the LLVM loop-vectorizer on the fused loop;
build-fails if a `by simd` loop did not vectorize. Horizontal-reduction lowering
for folds (with explicit FP-reassociation opt-in). Later: explicit portable
vector-type lowering.

---

## Part IX — Open questions

1. **`by simd` failure mode.** Hard build error if not vectorized, or a
   downgradable `-Wsimd`? (Proposed: error under a perf profile, warn otherwise.)
2. **Multiple accumulators** body shape — tuple (`( (a,b) for … with x=…, y=… )`)
   vs named assignment. Tuple is simplest; revisit if it reads poorly.
3. **`dict`/`set` first-class status.** Set comprehensions need a first-class
   `set`; confirm or define one alongside the existing `dict`.
4. **Generator as a stored value.** Do we allow `g = (e for x in c)` bound to a
   local and consumed later, or only consumed in the same expression? Storing it
   commits to the protocol being a nameable type.
5. **`by par` default worker count** — inherit `perf_cores()` (as `each`/`reduce`
   do), or require an explicit count for `by par` on small inputs?
6. **Keyword vs `from`.** `with acc = 0` vs `from acc = 0` for the fold seed.
   *(Decided: `with`.)*
7. **Bare (unparenthesized) tuples.** v1 keeps tuple *values* parenthesized
   (`(a, b)`), matching the rest of Elisa and Python, so the head needs no special
   rule. *If* bare tuples are ever adopted language-wide, the comprehension stays
   unambiguous under one asymmetry: a **binding RHS stops at the next depth-0
   comma** (so a tuple binding-value keeps parens, `y = (a, b)`), while the
   **body absorbs all trailing commas up to `for`** (so a bare-tuple body works,
   `[a, b for x in c]`). An item starting `IDENT [:T] =` is always a binding
   (assignment is not an expression, so a body can never start that way); the
   maximal binding prefix ends where the body begins. Note the real cost of bare
   tuples is *language-wide* (call args `f(a, b)`, literals `[a, b]`/`{a, b}`),
   not the comprehension — which is why parenthesized tuples are the v1 default.
