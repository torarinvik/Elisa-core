# 119 — Expression unification, block/loop expressions, and explicit mutation (`rebind` + capture)

Adopts the best ideas of Elian (the sister language, github.com/torarinvik/Elisa /
"ContextLang") into Elisa, adapted to Elisa's principles: **(1) reject slow, (2) reject
unsafe, (3) make the safe subset ergonomic, (4) escape hatches** — in that order. Elian
proves the ergonomics of value-yielding blocks, loop expressions, and
all-mutation-is-visible dataflow; this doc specifies those features *without* importing
Elian's costs (no user-visible references banned, no silent clone fallback, no
first-class `unit` value).

Builds on: [87] (`changes`/`preserves` frame conditions — the capture list is a local
frame), [86]/[118] (termination provers — capture-free loops hand them purity facts),
docs/74/75 (region inference — block/loop locals are region-scoped by construction),
and the stage1 frontend (every new diagnostic lands there in the same sound-subset,
0-false-positive style).

Everything here is **additive**: existing Elisa code compiles unchanged at every stage.
The only new default-on diagnostic is warning-severity (§8.4), promoted only after a
corpus sweep.

---

## 0. Summary of the six changes

| # | Change | One-liner |
|---|--------|-----------|
| 1 | Expression unification | Every construct has a type; valueless constructs have type `void`; one AST node family, one lowering path |
| 2 | Block expressions | An indented block after `=`/`<-`/`return` is an expression; tail expression is its value; locals die at block end |
| 3 | Loop expressions | `for x in xs \|acc = 0\| -> acc:` — loop-private accumulators, loop yields a value |
| 4 | `if`/`match` expressions | Multi-line branching in value position, everywhere (closes the stage0 expr-match codegen gap) |
| 5 | `rebind` | Blocks update outer mutables only by *yielding* new values; `rebind` names the targets; guaranteed move, never clone |
| 6 | `\|capture\|` sugar | `\|name\|` on a header shadow-captures an outer mutable; desugars to `rebind` threading |

Plus §7 (what becomes banned / idiom table) and §8 (diagnostics, staging, migration).

---

## 1. Change 1 — Expression unification (`void` foundation)

### 1.1 Specification

Every syntactic construct is an **expression with a type**. Constructs that produce no
value have the type `void`. This is primarily an AST/type-system unification: `If`,
`Match`, `For`, `While`, blocks, and calls all inhabit one expression family; "statement"
becomes a *position* (an expression whose value is discarded), not a node kind.

`void` is a **type, not a value**. There is no unit value, no `void` literal, and no way
to observe, store, or transport a `void`. This is a deliberate divergence from Elian,
whose first-class `unit` allows `x = print(...)` — the Scala/Kotlin unit-in-a-collection
bug class. Concretely:

```elisa
x = log_line(msg)          # ERROR E1: binding a void expression
x: void = f()              # ERROR E2: `void` is not a bindable type
xs: darray[void] = []      # ERROR E2
a == b_returns_void()      # ERROR E3: void in operand position
```

### 1.2 Assignment and mutation yield `void`

`=`, `<-`, and every compound assignment (`+=`, `-=`, …) have type `void`. Container
mutators (`push`, `extend`, insert-by-index, …) keep their current types (`void` today).

```elisa
a = (b <- 5)               # ERROR E1: binding a void expression
f(x <- g())                # ERROR E3: void argument — and this is exactly the
                           # hidden-mutation-inside-an-expression shape this whole
                           # design exists to forbid
if x <- next():            # ERROR E3: void condition (kills the C `if (x = 5)` class)
a = b = c                  # ERROR (already illegal; stays illegal)
```

This rule is load-bearing for §5/§6: if `<-` produced a value, mutation could hide inside
argument lists and conditions, and "all mutation is visible" would be false.

### 1.3 Discarded values

An expression in statement position whose type is not `void` is a **discarded value**.

- `void` in statement position: fine, silent (this is every current statement).
- non-`void` in statement position: **warning W1** (`discarded non-void value; use
  '_ = …' to discard intentionally`). Explicit `_ = expr` silences it.
- Error unions remain must-consume (hard error) — unchanged, and W1 never fires where
  the must-consume error already does.

W1 lands warning-severity (§8.4). It catches the one new bug shape this design creates:
writing the value-producing expression and forgetting the `<-`/binding in front of it.

### 1.4 Functions keep explicit `return` — deliberately

Elian makes a function body's last expression its return value. Elisa does **not** adopt
this. `def` bodies keep declared return types and explicit `return`:

- `ensure`/`result`-based contracts, the WP engine, and [118]'s ensure-summaries all key
  off explicit returns;
- it makes this entire doc a zero-migration change — no existing function's meaning or
  inferred type can shift;
- Rust demonstrates the block-tail/function-return split is stable and learnable.

Blocks yield their tail expression (§2); functions `return`. The asymmetry is a feature.

### 1.5 Compiler impact

**stage0 (Go)**: collapse the `Stmt`/`Expr` duplication for `If`/`Match` (and the
lowering split that produced the known gap: expression-form `match` with
payload-destructuring arms does not codegen today — see memory note
`integer-match-support` / `stage1-self-hosted-frontend`). One lowering path; the
statement form becomes "lower the expression, discard the (void) value".

**stage1 (Elisa-in-Elisa)**: same unification in `parser_stmt*/parser_expr*`; the
`Ast::Stmt` vs `Ast::Expr` split can be retired incrementally (the parser keeps a `Stmt`
wrapper initially and unifies later — parity harness must stay green at every step).

### 1.6 Benefits

- **One `match`.** The expr/stmt `match` divergence (different parse paths, different
  codegen, one of them broken) disappears structurally rather than by patching.
- **Compositionality for free.** Changes 2–6 need no "what may appear on the right of
  `=`" whitelist; everything may, and the `void` rule rejects what's meaningless.
- **Verifier uniformity.** WP/fact-flow currently handle statement and expression forms
  of branching separately; unification halves that surface.

---

## 2. Change 2 — Block expressions

### 2.1 Syntax

Where a value is expected, a newline + indented block is an expression:

```elisa
NAME [: TYPE] =
    <stmt/expr>*
    <tail-expression>
```

The block's **tail expression** (the last expression, which must be non-`void`) is its
value. Grammar-wise this is: after `=`, `<-` (§5), `return`, or an arm/branch colon, if
the next token is NEWLINE+INDENT, parse a block and treat it as an expression whose type
is the tail expression's type.

Tuple yield binds multiple targets:

```elisa
lo, hi =
    sorted = sort(samples)
    sorted[0], sorted[sorted.count - 1]
```

### 2.2 Scoping and mutation rules

1. **Locals die at block end.** Bindings introduced inside the block are scoped to it.
   Shadowing an outer name is legal (existing Elisa block-scoping semantics).
2. **Outer variables are read-only inside a value block.** Any mutation of a
   non-block-local — direct (`outer <- …`, `outer.field <- …`, `outer[i] <- …`,
   `outer.push(…)`) or through a call (passing a non-block-local as `mutable T&`) —
   is **error E4** unless licensed by `rebind` (§5) or a capture (§6).
3. **Control flow may not jump out of a value block** except via the enclosing
   function's `return`/`raise` (which are typed as diverging, so they don't conflict
   with the tail-value rule). `break`/`continue` targeting a loop *outside* the block:
   error E5. (Loop expressions §3 have their own interior `break`/`continue`.)

Rule 2 is the semantic heart of this doc: **a value block is a pure function of the
outer state it reads**, so the verifier gets "nothing else changed" as a structural
fact — no escape analysis, no havoc over outer locals at the block boundary. This is
what makes blocks compose with [87] frames and the [86]/[118] provers (§6.5).

### 2.3 Before / after

```elisa
# BEFORE — intermediates leak; nothing marks where they stop mattering
scale: f64 = base_scale(cfg)
adjusted: f64 = scale * dpi_factor(screen)
clamped: f64 = min(adjusted, MAX_SCALE)
render(clamped)

# AFTER — locality is structural
clamped: f64 =
    scale = base_scale(cfg)
    adjusted = scale * dpi_factor(screen)
    min(adjusted, MAX_SCALE)
render(clamped)
```

```elisa
# BEFORE — the "mutable temp assigned from branches" pattern
label: mutable sview = ""
if score > 90: label <- "gold"
elif score > 50: label <- "silver"
else: label <- "bronze"

# AFTER — see §4; and label is immutable
label: sview =
    if score > 90: "gold"
    elif score > 50: "silver"
    else: "bronze"
```

### 2.4 Regions

Block locals are region-scoped by construction: a container built inside a block and
*not* reaching the tail expression dies with the block (existing multi-stack region
machinery, docs/74/75 — a block is just a scope). A container that *is* the tail value
threads outward exactly like a `return` from a region-poly builder; no new region rules
are required, but the block boundary is a new, earlier death point the inference may
exploit.

---

## 3. Change 3 — Loop expressions and loop-private state

### 3.1 Syntax

Two orthogonal additions to `for`/`while` headers:

```elisa
for PAT in ITER |DECLS| -> YIELD:      # loop expression
while COND |DECLS|:                     # loop-private state, statement position
for PAT in ITER |DECLS|:                # loop-private state, statement position
```

`DECLS` is a comma list where each element is either:
- `name = init` — a **fresh mutable** scoped to the loop (initialized once, before the
  first iteration; `while` conditions may reference it), or
- a bare `name` — a **capture** of an outer mutable (§6).

`-> YIELD` (loop expression form only): `YIELD` is one or more expressions over the
loop's `DECLS` names (and enclosing scope); when the loop finishes — normally or via
`break` — the loop expression's value is `YIELD` evaluated against the final state of
the `DECLS`. A loop without `->` has type `void` (statement loop).

`break`/`continue` work unchanged inside loop expressions; `break` yields the current
accumulator state (this is what makes early-exit folds natural — no sentinel needed).

**Spelling — same-line and block-RHS are equivalent.** A loop expression may be written
inline in value position or as an indented block RHS; both lower identically:

```elisa
result: i64 = for x in xs |acc = 0| -> acc:   # same-line: the loop keyword IS a value atom
    acc <- acc + x

result: i64 =                                  # block-RHS: the loop starts on the next
    for x in xs |acc = 0| -> acc:              #   indented line (also valid for `while`)
        acc <- acc + x
```

`for`/`while` are recognized as value-expression atoms (like `machine from`/`match`), so the
inline form works at any `=`/`<-`/`return`-of-local site. A loop in value position without a
`-> YIELD` is a hard error (a statement loop has type `void` and is not a value).

### 3.2 Before / after

```elisa
# BEFORE — accumulator outlives the loop, stays mutable forever
sum: mutable i64 = 0
for x in xs:
    sum <- sum + x
use(sum)                     # nothing prevents a later accidental `sum <- 0`

# AFTER
sum: i64 =
    for x in xs |acc = 0| -> acc:
        acc <- acc + x
use(sum)                     # immutable; acc never existed out here
```

```elisa
# BEFORE — index leaks
i: mutable i64 = 0
while i < n:
    process(i)
    i <- i + 1

# AFTER — i is born and dies with the loop
while i < n |i = 0|:
    process(i)
    i <- i + 1
```

```elisa
# Multi-accumulator, one pass
evens, odds =
    for x in xs |e = 0, o = 0| -> e, o:
        if x % 2 == 0: e <- e + 1
        else: o <- o + 1

# Early-exit search (break yields current state)
found: bool =
    for x in xs |ok = false| -> ok:
        if x == needle:
            ok <- true
            break
```

### 3.3 Relationship to comprehensions

Comprehensions ([79] family) remain the idiom for *build a collection from a source*;
loop expressions cover what comprehensions structurally can't: multi-accumulator folds,
effects in the body, early exit, `while`-shaped iteration. The existing fold
comprehension (`[f(acc, x) for x in xs from acc = init]`-style desugar) is subsumable by
loop expressions long-term, but is NOT deprecated by this doc — revisit only after loop
expressions have corpus mileage. The `-Wperf`/vectorization story ([scalar-permission])
applies to loop-expression bodies identically to today's loops (same lowered form).

### 3.4 Scoping rules

`DECLS` names shadow outer names for the extent of the loop (header, body, `YIELD`).
Mutating a `name = init` decl is always legal (it's loop-local). The §2.2 rule applies to
the loop body when the loop is a *loop expression*: outer mutation requires capture.
Statement-position loops (no `->`) keep today's semantics entirely — outer mutation in a
plain `for`/`while` statement stays legal (§7 makes some of it *unidiomatic*, not
illegal).

---

## 4. Change 4 — `if`/`match` as expressions (multi-line)

Mostly falls out of §1+§2; specified for completeness.

### 4.1 Rules

- Every branch/arm is a block expression; all tails must unify to one type `T` (or all
  be statement-position `void`). Mismatch: **error E6** with both types and both lines.
- An `if` expression **must** have an `else` (otherwise it has no value on the false
  path): **error E7**. Statement `if` needs no `else`, as today.
- A `match` expression must be exhaustive — the existing exhaustiveness machinery
  applies; arms may be blocks with payload destructuring (this is the previously broken
  stage0 path — §1.5 closes it by construction).
- Diverging arms (`return`/`raise`) unify with anything (standard bottom-type rule; the
  ternary `A if C else B` already behaves this way for values).

### 4.2 Before / after

```elisa
# BEFORE — statement-form match + mutable temp (the documented workaround
# for the expr-match codegen gap; memory: construct_type_name in resolve)
name: mutable sview = ""
match type:
    Expr.Ident(n, line): name <- n
    _: name <- ""

# AFTER
name: sview = match type:
    Expr.Ident(n, line): n
    _: ""
```

The one-line ternary (`A if C else B`) is unchanged and remains idiomatic for scalar
picks; multi-line `if` expressions are for when branches have real bodies.

---

## 5. Change 5 — `rebind`: explicit mutation threading (the primitive)

### 5.1 The idea (Elian's core, minus its costs)

Elian's discipline: a block cannot write outer state; it *yields* updated values, and the
caller *rebinds* — `snake <- snake.move(5, 4)`, never `move(snake, 5, 4)` with a hidden
write. All mutation is visible at the binding site. Elian pays for this by banning
references entirely and cloning when its "Auto-Rust" analysis can't move — a silent
performance cliff Elisa's principle #1 forbids.

Elisa adopts the *discipline* as a scoped construct with a *guaranteed-move* semantics:

```elisa
rebind TARGET[, TARGET]* [, NEW_BINDING]* =
    <block whose tail is a tuple matching the target list>
```

### 5.2 Rules

1. **Targets are whole variables** — `rebind pos`, never `rebind pos.x`. Field paths
   reintroduce partial-move bookkeeping (may the block still read `pos.y`?) for no
   expressive gain: build the updated whole value inside. If field-threading proves to
   be a recurring pain, add it later as sugar over whole-variable rebind, not as a
   primitive.
2. **Targets must be existing `mutable` bindings** in scope (error E8 otherwise —
   this is what makes rebind-vs-fresh lexically unconfusable, the failure mode of
   Elian's `(a, c=) |a,b| <-` punctuation forms, which this design explicitly rejects).
3. **New bindings** in the target list are ordinary fresh bindings (require `name` or
   `name: T`); they receive the corresponding tuple element.
4. **Move-in guarantee.** Each target is *moved* into the block. Using the target's
   name inside the block after it has been consumed into the new value is a
   use-after-move error (existing affine machinery). On exit, the yielded value is
   *moved* back into the binding. **Never a clone** — if the compiler cannot arrange
   the move (it always can: the binding is dead across the block by rule), that is a
   compiler bug, not a fallback. This single rule is the anti-Elian guarantee.
5. The block obeys §2.2 (no other outer mutation).
6. `rebind` and `|capture|` (§6) may **not** appear on the same construct (error E9).
   Mixing them is precisely how Elian arrived at its unreadable corner.

### 5.3 Before / after

```elisa
# BEFORE — call-site-invisible mutation
apply_impulse(pos, v)                     # mutates pos via `mutable Vec2&`; the
                                          # reader must open apply_impulse to know

# AFTER — the update is the binding
rebind pos, applied: f32 =
    if v > MAX_STEP:
        Vec2(pos.x + MAX_STEP, pos.y), MAX_STEP
    else:
        Vec2(pos.x + v, pos.y), v
```

`grep rebind` now answers "where does this function update outer state through a value
block" — a real review workflow, and the reason a keyword beats any sigil here (house
style: Elisa spends keywords where Elian spends punctuation — `changes`, `requires`,
`guard`, postfix `if`, `can`).

### 5.4 What `rebind` does NOT change

`mutable T&` parameters remain the idiom for *functions* that mutate (the whole
[canonical-mutable-borrow-syntax] / alias-checker stack is untouched). `rebind` governs
*value blocks inside a function body*. §7.3 adds an optional lint for ref-param-vs-rebind
style at function granularity; it is advisory, off by default.

---

## 6. Change 6 — `|capture|` header sugar

> **Implementation decision (landed): capture is E4-LICENSING, not move-shadow.** The
> §6.1 move-shadow desugar below is the original design; the shipped implementation
> instead records captures on `ExprBlock.Captures` and exempts them from E4, letting the
> body mutate them *in place*. This was chosen after confirming the in-place form is
> strictly at least as capable AND safe: field mutation (`w.f <- …`), mutating-method
> calls (`w.push(x)`), and whole-value replacement (`w <- combine(w, x)`) all work in
> place and clone-free, and alias-freedom still holds (E4 forbids taking an escaping
> mutable ref to any non-captured outer, and a captured var is mutated by name, never
> aliased). Move-shadow would add **zero** capability or safety while forcing moves/copies
> (a principle-#1 regression) and requiring either unsound body-identifier rewriting or
> whole-statement LHS coordination. It is therefore **deliberately not implemented**; the
> explicit `rebind` (§5) remains available when a caller genuinely wants the move form.

> **Scope decision (landed): captures live on LOOP headers only — not on bare blocks or
> `if`/`match`.** Inside a loop's `for … |…|:` / `while … |…|:` the `|…|` sits in an
> unambiguous grammar slot. On an `if` condition or a bare block, a `|caps|` header
> collides with a bitwise `|` and would need invasive changes to the core expression
> parser to disambiguate — AND it is redundant with `rebind`: a value-yielding
> conditional that updates outer state is written with `rebind` over an `if`-expression
> (each branch yields the new outer value(s) + the produced value). That IS the §5.3
> clamp, verified end-to-end: `rebind pos, applied: i64 = if v > max: Vec2{…}, max else:
> Vec2{…}, v`. So `rebind` is the explicit, unambiguous form for conditionals/blocks and
> `|capture|` is the loop-only convenience. (Dogfooding gap #2 — resolved this way rather
> than by adding if/block-capture parsing.)

### 6.1 Syntax and desugaring

A bare `name` in a `|...|` header (block §2, loop §3, `if`/`match` branch §4) is a
**capture** of an in-scope `mutable` binding. Semantics, by desugaring to §5:

```elisa
count: i64 =
    for job in q |n = 0, world, stats| -> n:
        world.advance(job)
        stats.record(job.size)
        n <- n + 1
```

desugars to (conceptually):

```elisa
rebind world, stats, count: i64 =
    for job in q |n = 0, __world = move world, __stats = move stats| -> __world, __stats, n:
        __world.advance(job)
        __stats.record(job.size)
        n <- n + 1
```

i.e.: move the outer binding in as a loop/block-local mutable shadow (same name — the
outer name is shadowed, so there is no way to alias it), permit `<-`/mutating calls on
the shadow, append the shadow to the yield tuple, rebind on exit. **The verifier and
backend only ever see the `rebind` form.** One semantics, two spellings.

On `if` expressions, a shared header captures for both branches:

```elisa
applied: f32 =
    if v > MAX_STEP |pos|:
        pos.x <- pos.x + MAX_STEP
        MAX_STEP
    else:                      # pos is captured for the whole if — both arms may
        pos.x <- pos.x + v     # mutate it; arms that don't touch it yield it unchanged
        v
```

(Per-branch capture lists are NOT supported — one header per construct. Divergent
per-branch mutation sets are a smell; use `rebind` explicitly if branches genuinely
differ.)

### 6.2 Enforcement — mutation AND mutating calls

Inside a value block/loop, for any non-local `x`:

| Operation | not captured | captured |
|---|---|---|
| read `x`, `x.field`, `x[i]` | ok | ok |
| `x <- …`, `x.f <- …`, `x[i] <- …`, `x.push(…)` | **E4** | ok |
| call passing `x` as `mutable T&` | **E4** | ok |
| call passing `x` by value / immutable `&` | ok | ok |

The mutating-call row is what closes Elian's actual complaint (`bad_stuff(foo)` hiding a
write): inside value blocks, that call shape is a compile error unless the header
licenses it. Detection reuses the existing call-signature knowledge (same machinery as
the alias/borrow checkers); for calls whose signature is unknown (extern without decls),
be conservative: **E4**. Escape hatch, per Elisa principle #4: the function-level
`can Unsafe.*` idiom does not apply here — the escape hatch is simply "don't use a value
block"; statement-position code keeps today's freedom.

### 6.3 Capture rules

- Capture target must be an existing `mutable` binding: error E10 (immutable) / E11
  (undefined).
- A captured name is moved in — the §5.4 move guarantee applies identically; captures
  can never clone.
- Duplicate names in one header: error. A `name = init` decl and a capture of the same
  name in one header: error (shadow ambiguity).

### 6.4 Frame-condition integration ([87])

A capture list is a **local frame**. Two obligations, both discharged by the existing
`changes` machinery:

1. Every captured (or `rebind`-targeted) variable that is itself a `mutable T&`
   parameter, or reaches one, must lie within the enclosing function's `changes` clause
   (when one is declared). Subset check; violation is the existing frame error.
2. A value block/loop with an **empty** capture set is *provably pure over outer state*
   — a structural fact handed to the WP engine, loop-invariant inference, and the
   [86]/[118] termination provers (a pure-over-outer-state loop body means the measure
   can only change through the loop's own header decls — exactly the shape [118]'s
   summaries want).

### 6.5 Before / after (the full composition)

```elisa
# BEFORE — three mutables alive across 30 lines; who mutates what is archaeology
world_dirty: mutable bool = false
total: mutable i64 = 0
count: mutable i64 = 0
for job in q:
    world.advance(job)              # mutates world (invisible here)
    stats.record(job.size)          # mutates stats (invisible here)
    total <- total + job.size
    count <- count + 1
    if job.urgent: world_dirty <- true

# AFTER — loop signature IS the mutation contract
total, count, dirty =
    for job in q |t = 0, n = 0, d = false, world, stats| -> t, n, d:
        world.advance(job)          # licensed: captured
        stats.record(job.size)      # licensed: captured
        t <- t + job.size
        n <- n + 1
        if job.urgent: d <- true
```

Reading the header tells you everything the loop can do to the world.

---

## 7. What is banned, what is deprecated-by-idiom, and the new idioms

### 7.1 Hard errors (new code shapes that never existed — nothing existing breaks)

| Banned shape | Error | Why |
|---|---|---|
| Binding/storing/passing a `void` expression | E1–E3 | no unit value (§1.1) |
| Assignment/mutation as a value (`f(x <- g())`, `if x <- …`) | E3 | mutation must not hide in expressions (§1.2) |
| Mutating a non-captured outer variable inside a value block/loop | E4 | the core soundness rule (§2.2/§6.2) |
| Mutating call on a non-captured outer variable inside a value block | E4 | Elian's `bad_stuff(foo)` complaint, enforced (§6.2) |
| `break`/`continue` out of a value block to an outer loop | E5 | blocks are expressions with one exit value |
| `if` expression without `else`; branch/arm type mismatch | E6/E7 | totality of value production |
| `rebind` of a field path, an immutable, or an undefined name | E8 | whole-variable moves only; rebind ≠ bind |
| `rebind` + `\|capture\|` on one construct | E9 | the Elian punctuation-soup corner, rejected wholesale |
| Capturing an immutable/undefined binding | E10/E11 | captures are mutable shadows |

### 7.2 Warning (default-on after corpus validation, §8.4)

| Shape | Warning | Fix |
|---|---|---|
| Discarded non-void value in statement position | W1 | `_ = expr` or actually use it |

### 7.3 Deprecated-by-idiom (legal forever; linted only under an opt-in `-Wstyle` tier)

These stay legal — statement-position code keeps today's semantics — but stop being the
*idiomatic* spelling, and an opt-in lint can nudge:

| Old way | New idiomatic way |
|---|---|
| `acc: mutable T = init` + statement loop + read after | loop expression `\|acc = init\| -> acc` (§3.2) |
| mutable index leaked past a `while` | `while cond \|i = 0\|:` (§3.2) |
| mutable temp assigned from `if`/`match` branches | `if`/`match` expression (§4.2) |
| helper function or leaked intermediates for a multi-step init | block expression (§2.3) |
| `do:` expression blocks (pre-119 stage0 form) | bare block form; in argument/element positions (where bare can't apply) bind to a local first. Parser emits a deprecation notice on `do:` |
| statement `match` + mutable result as the expr-match workaround | `match` expression (§4.2) — the workaround's reason (codegen gap) is gone |
| `mutable T&` out-param on a *small result* (scalar/pair) where the ref exists only to return a second value | return a tuple / `rebind` at the caller |

Explicitly NOT deprecated: `mutable T&` parameters as such (they are the zero-cost
mutation ABI and the region/`@r` machinery rides on them), container in-place mutation in
statement position, `for`/`while` statements, the scalar ternary.

### 7.4 The new-code idiom, in one example

```elisa
def settle(accounts: darray[Account], ledger: mutable Ledger&) -> i64
    changes ledger:
    total: i64 =
        for a in accounts |sum = 0, ledger| -> sum:    # local frame ⊆ `changes ledger` ✓
            ledger.post(a.pending)
            sum <- sum + a.pending
    ledger.close(total)
    return total
```

Every mutable fact is in a signature: the function's frame (`changes ledger`), the
loop's frame (`|… ledger|`), the accumulator's extent (`|sum = 0| -> sum`), and `total`
is immutable. Nothing requires reading a body to know what changes.

---

## 8. Implementation plan

### 8.0 Implementation status (stage0) — LANDED

All six changes plus E4 and W1 are implemented in the stage0 compiler and locked with
runtime goldens + semantic/parser tests; zero regressions across stage1, wolf3d,
shadps4, nes.

- **§1 void unbindable** (E1/E2) — `analyzer_void_binding.go`.
- **§2 block expressions** — bare `x =` NEWLINE+INDENT blocks; `do:` deprecated.
- **§4 if/match expressions** (E6 via TernaryExpr unification, E7 mandatory else) —
  `parser/if_expression.go`; trailing `if`/block-`match` is the block value.
- **§3 loop headers** — `parser/loop_header.go`; yield/statement forms, break-yields-state.
- **§5 `rebind`** — `parser/rebind.go`; bare target = existing mutable (reassign; E8 =
  the existing reassign-undefined/immutable error), `name: T` = fresh binding. Desugars
  onto a temp-tuple bind + per-target reassign/decl (guaranteed-move via the affine
  reassignment path). Single-target binds the scalar directly.
- **E4 value-block purity** — `semantic/analyzer_value_block_e4.go`; direct writes to an
  outer binding inside a value block are rejected (the mutating-CALL half is deferred).
- **§6 `|capture|`** — landed as the **E4-licensing (in-place) form**, NOT the §6.1
  move-shadow desugar. A captured mutable is recorded on `ExprBlock.Captures` and exempt
  from E4; E10/E11 require it to be an existing mutable. This is sound with zero AST
  rewriting and no LHS-binding coordination, and delivers the headline benefit (the
  header is the loop's mutation contract). The move-in/out shadow semantics of §6.1
  remain available explicitly via `rebind` (§5). The mutating-call licensing row of §6.2
  lands with the deferred call-side E4 half.
- **W1 discarded value** — opt-in `-Wunused` (`semantic/analyzer_discarded_value.go`).

- **Mutating-call half of E4/§6.2** — LANDED. Inside a value block, passing an outer
  binding by `mutable T&` (incl. a mutating method receiver `outer.push(v)`) is E4,
  reusing the analyzer's resolved param mutability + a value-block allowed-name stack.
- **§6.4 frame integration** — LANDED. Obligation 1 (a captured `mutable T&` place
  mutated in the block must lie in the enclosing `changes` clause) is enforced for free:
  the block's statements analyze normally, so the write reaches the existing frame
  check. Obligation 2 (an empty-capture block is pure over outer state) is exposed as
  `exprBlockPureOverOuter` for the provers; the per-variable fact model already
  preserves untouched-outer facts across such a block, so the guarantee holds by
  construction.
- **`func`→`fn` type display** — LANDED (FuncType.String() + diagnosticTypeString).
- **stage1-frontend parity** — LANDED (Elisa-compiler repo): all five forms parse+resolve.
- **LSP `rebind` token** — LANDED (Elisa-LSP repo).

Remaining (future): the §6.1 move-shadow desugar if the alias-free guarantee is ever
wanted for captures (the E4-licensing form is sound today); an LSP capture-list hover.

### 8.1 Landing order (each step independently shippable, parity-gated)

1. **§1 void unification** — stage0 type-checker rule (`void` unbindable, E1–E3,
   assignments typed void), AST unification of `If`/`Match`. Closes the expr-match
   codegen gap as a side effect. Corpus must be bit-identical (no legal program changes
   meaning).
2. **§2 block expressions** — parser (NEWLINE+INDENT after `=` in value position),
   scope rule E4 (read-only outer, no licenses yet), region-scope wiring.
3. **§4 if/match expressions** — mostly free after 1+2; E6/E7; goldens for arm-block
   destructuring (the previously broken path).
4. **§3 loop headers** — `|name = init|` decls (statement + expression forms), `->`
   yield, `break`-yields-current-state; `-Wperf` parity on lowered form.
5. **§5 `rebind`** — target-list parsing, whole-variable rule E8, move-in/move-out on
   the existing affine machinery, the never-clone guarantee as an assertion in codegen.
6. **§6 capture sugar** — desugar to 5; mutating-call detection (reuse alias-checker
   call knowledge); E9–E11; [87] subset obligation + purity fact export.
7. **W1 discard warning** — last, after the sweep (§8.4).

stage1 frontend tracks each step with the same diagnostics in sound-subset form
(0 false positives across frontend + stdlib + wolf3d + shadps4 — any error-severity
finding on the corpus is a checker bug, since the corpus compiles on stage0). LSP:
semantic tokens for `rebind` and `|…|` headers; the capture list is a hover surface
("this loop mutates: world, stats").

### 8.2 Grammar deltas (informal)

```
value_block   := NEWLINE INDENT stmt* tail_expr NEWLINE DEDENT
init_target   := "rebind" name ("," name)* ("," binding)*  |  binding_list
binding       := name [":" type]
loop_header   := ("for" pat "in" expr | "while" expr | "until" expr)
                 ["|" decl ("," decl)* "|"] ["->" expr_list] ":"
decl          := name "=" expr        # fresh loop-local mutable
               | name                 # capture (mutable shadow)
if_header     := "if" expr ["|" name ("," name)* "|"] ":"    # capture applies to all arms
```

Parse ambiguity notes: `|` in header position is unambiguous today (no prefix-`|`
expression form; the lambda family uses different introducers — verify against
`looks_like_lambda`/`lambda_signature_probe` probes and add a
`looks_like_capture_header` probe with the same backtracking discipline). `rebind` is a
new keyword — corpus grep for `rebind` as an identifier before claiming it
(contextual-keyword fallback if hits exist; cf. the [postfix-guard] footgun note).

### 8.3 Verifier obligations (new, all discharged by existing machinery)

- Block purity: value block without licenses mutates nothing outer — structural, free.
- Rebind move: target dead across block (affine), yielded value moved (region/owner).
- Capture ⊆ `changes` (when declared): subset check in the [87] frame pass.
- Loop-expression accumulators: `-> acc` gives the WP engine the loop's *entire*
  observable effect as a value — invariant candidates come from the yield shape.

### 8.4 W1 promotion gate

Land W1 (discarded non-void) silent-off; sweep the four corpora; publish the count.
Promote to default-warning only if real-signal ratio is high (expected: near-zero hits
in existing code because current statements are void-typed; hits appear only as new
expression forms get adopted, which is exactly when the warning is wanted).

### 8.5 Explicitly rejected Elian features (for the record, with reasons)

- **First-class `unit` value** — §1.1; bug class outweighs generic-code convenience.
- **Function tail-expression return** — §1.4; contracts/WP key off `return`.
- **`(a, c=) |a,b| <-` mixed punctuated target lists** — §5.2(2)/E9; positional
  rebind-vs-bind is a silent-transposition machine (Elian's own examples misspell
  their variable names across these forms).
- **`setup:/loop:/done:` long-form loops** — the header form subsumes it with less
  ceremony.
- **Auto-Rust move→borrow→clone fallback** — the clone tier is a silent performance
  cliff (principle #1); Elisa's regions + guaranteed-move `rebind` cover the space
  with no fallback.
- **Banning references** — `mutable T&` + the alias/borrow checker stack is
  strictly more expressive at equal safety; Elian's ban is its workaround for not
  having those checkers.

---

## 9. Benefits, consolidated

1. **Ergonomics** (principle #3): kills the four most common mutable-boilerplate
   patterns (branch-assigned temp, leaked accumulator, leaked index, helper-fn-for-init).
2. **Safety** (principle #2): value blocks make "what does this code mutate" a header
   read, not archaeology; the mutating-call rule surfaces hidden writes; `void`
   discipline kills the `if (x = 5)` and unit-value bug classes; accumulators can't be
   corrupted after their loop.
3. **Performance** (principle #1): zero new runtime cost anywhere — blocks/loops lower
   to exactly today's code; `rebind`/capture are moves by guarantee; earlier deaths for
   block locals can only help region inference.
4. **Verification**: purity and frame facts become structural; the expr-match gap
   closes; loop effects are reified as yield values the WP engine can quantify over.
5. **Compiler simplification**: one branching AST family, one lowering path, less
   stage0/stage1 surface to keep in parity.
