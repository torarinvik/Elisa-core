# 87 — Stage 3: frame conditions (`changes` / `preserves`)

Status: **design + brick 1 landing.** Implements docs/85 Stage 3 / §7: `changes S` is the
primitive (a function writes at most the places in `S`, transitively through aliases);
`preserves Y ≡ Y ∩ changes(f) = ∅` is derived. This is the **frame** discharge class (§4) —
discharged by the mutation/alias analysis, never `prove`-class, never a value premise.

## 1. Why `changes` and not `preserves`

`preserves <everything but X>` is the frame problem: whole-call-graph, alias-sensitive, and
brittle to new fields (add a field, every `preserves` silently widens its obligation). So the
primitive is the **upper bound on mutation**: `changes S` says writes land only in `S`. Adding
a field doesn't change what an existing `changes S` permits. `preserves Y` is then just the
query `Y ∩ changes(f) = ∅` — robust by construction (docs/85 §7).

## 2. Surface syntax

A post-signature clause, parallel to `ensures` (and after it):

```elisa
def clip_move(r: mutable Render&, xmove: i32, ymove: i32) changes r.px, r.py:
    r.px <- r.px + xmove        # ok — r.px ∈ changes set
    r.py <- r.py + ymove        # ok
    # r.health <- 0             # COMPILE ERROR: writes r.health outside its `changes` set
```

`changes` takes a comma-separated list of **param-rooted paths** (`EnsuresPath{Root, Fields}`,
reused verbatim). The eventual `law Frame: changes self.px, self.py` + `fulfills` forms (§13)
are a later brick; the direct clause is the substrate and carries the headline value on its own.

## 3. The soundness obligation (what "check `changes S`" means)

`changes S` is a claim that **every** write to caller-visible state lands in `S`. To check it
soundly the analyzer must catch *all* write channels — missing one would wrongly accept
(unsound). It may over-report (a false positive is safe, just annoying). The caller-visible
write channels:

1. **Direct assignment through a ref param** — `r.px <- v` where `r` is a `mutable T&` param.
   (A by-value param write mutates a local copy — not caller-visible — never a violation.)
2. **Passing a ref-param place to a MUTABLE-ref callee argument** — `f(&r.health)` or
   `f(r)` where `f` takes `mutable T&`: the callee may write that place, so it counts as a
   write to it. An *immutable* borrow argument is not a write (reuses the brick-3
   immutable-borrow / `=> preserve` signal — [[contract-algebra-laws]]).
3. **Global writes** — out of scope for brick 1 (documented limitation, §6).

A write to place `p` (rootParamIndex + field steps) is **covered** iff some declared path has
the same param root and is a *prefix* of `p`: declared `r.px` covers `r.px` and `r.px.sub`;
declared `r` (bare) covers everything under `r`. An uncovered write — or a write to a ref
param not named in `S` at all — is the error.

## 4. Brick plan

1. **87-1 — the `changes` clause + intraprocedural enforcement. [this brick]** Parse the
   clause; resolve each path against a ref param + its field chain; enforce channels 1 and 2
   over field paths within the body. Sound (catches all caller-visible writes via the two
   channels); field-granular (an index step `r.arr[i]` is covered at `r.arr` granularity).
2. **87-2 — `preserves Y` (derived). [LANDED]** `preserves r.health` is the dual blacklist:
   a write that *overlaps* a preserved place (either path a prefix of the other) is an error,
   over both channels. `changes`+`preserves` may coexist; a place in both is a conflict
   (the §7 `Y ∩ changes(f) = ∅` consistency check). Clause parses after `changes`.
3. **87-3 — interprocedural `changes` summaries.** A callee's own `changes` set refines
   channel 2: passing `r.x` to a callee that `changes self.a` only writes `r.x.a`, not all of
   `r.x`. Removes brick-1 conservatism; rides the disjoint-param/alias substrate (docs/84).
4. **87-4 — frame laws + `fulfills`.** `law MovesPlayerOnly: changes self.px, self.py` (a
   non-bool, frame-class law body) + `def f(...): fulfills r is MovesPlayerOnly`. Needs the
   discharge-class machinery (§5) — the first non-value law class.

## 5. Discharge classes (deferred to 87-4)

docs/85 §4 gives every law a class (value/frame/effect/shape/measure) so a uniform `is`/`law`
surface never implies uniform reliability. Today every law is implicitly value-class (pure
bool, fact-lattice discharge). `changes` as a *clause* (brick 1) needs no class machinery; only
when `changes` becomes a *law body* (87-4) does the frame class need first-class representation.

## 6. Honest limitations of brick 1

- **Globals not tracked** — a `changes`-annotated function that writes a module global isn't
  flagged yet (channel 3). Sound for the param-frame; documented gap.
- **Field granularity** — `r.arr[i] <- v` is checked at `r.arr` (declaring `changes r.arr`
  permits writing any element). Per-element frames are not expressible.
- **Conservative callee channel** — any mutable-ref argument of a param-rooted place counts as
  a write to the whole place, even if the callee writes only a sub-field. 87-3 refines this.
- **No interprocedural propagation** — `changes` is checked at the declaring function only; it
  is not yet *consumed* to discharge a caller's frame. (87-3.)
