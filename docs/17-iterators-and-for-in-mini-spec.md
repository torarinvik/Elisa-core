# Iterators And `for in` Mini-Spec

This document proposes the first general iterable surface for Elisa core /
`elisacore`.

It is still mainly a design note for the broader sequential iterable model.
Since it was written, the compiler has also shipped companion implemented
surfaces such as filtered iterable loops, canonical `rev(items)` syntax, and
the explicit pool-scoped `parallel for` feature. Those currently accepted
surfaces are documented in `18-current-surface-ergonomics.md`.

The goal is **not** to start with generators, lazy pipelines, or user-authored
iterator traits.

The goal is to make iterable traversal the ordinary loop style and to
generalize the existing range-shaped loop surface:

```elisa
for index in 0..<items.len:
    ...
```

into this default shape:

```elisa
for item in items:
    ...

for index, item in items.enumerate():
    ...
```

Use iterable sources whenever the program is traversing data: arrays, `darray`,
views, strings, row views, child views, projected tree views, filtered views,
and other compiler-known iterable categories. Numeric ranges remain important,
but they are the fallback for numeric algorithms, index-only loops, explicit
stride/control loops, and cases where no iterable category exists yet. In
ordinary container traversal, prefer `for item in items:` or
`for index, item in items.enumerate():` over spelling the loop as
`for i in 0..<items.count:`.

Current range-shaped loop headers also accept inclusive bounds, descending bounds, and explicit steps:

```elisa
for i in 0..3:
    ...

for i in 0..<10..2:
    ...

for remaining in limit..>0:
    ...
```

Reverse iterable loops use `rev(...)` in source position rather than a special
header prefix. The older `for rev item in items:` spelling is no longer
supported.

```elisa
for item in rev(items):
    ...
```

into a small, explicit iteration model that:

- reduces boilerplate for arrays, views, strings, and tree-shaped data
- composes with existing ref/mutability rules
- keeps tree traversal order explicit
- preserves the legality facts later parallel features will need

## Design Goals

The first iterator slice should:

- support **builtin iterable categories first**
- support `for in` over:
  - values
  - readonly refs
  - mutable refs
- support **loop destructuring** using the same binder/pattern machinery the
  language already prefers elsewhere
- keep **trees** in mind from day one
- keep **parallelism** in mind from day one
- avoid hidden allocation, boxed iterator objects, or trait-object dispatch in
  the first slice
- keep plain `for` sequential and explicit

## Non-Goals

Still intentionally deferred in the first slice:

- user-authored iterator traits or protocols
- generators, `yield`, coroutines, or resumable iterators
- expression-heavy lazy iterator chains as a core feature
- implicit recursive traversal of a bare tree value
- hidden filtering patterns in loop headers
- auto-parallelization or a broader parallel-iterator surface in this document
- consuming iteration of affine containers in the first slice
- dict / ordered-map iteration before the container surface stabilizes

## Core Principle

`for` should be syntax over **compiler-known iterable categories**, not a magic
method-lookup protocol.

That keeps the feature aligned with the language's broader direction:

> explicit control points over hidden control flow.

This matters especially for two already-established design pressures:

1. **trees** — traversal order and traversal scope must stay explicit
2. **parallelism** — future parallel forms need clean legality boundaries,
   exact extents, and alias/mutability facts

So the rule is:

> plain `for` is sequential syntax over builtin iterable categories now, and
> further parallel forms should reuse that same iterable model rather than
> inventing a second traversal dialect.

Idiomatic loop choice follows from that rule:

- use `for item in source:` for ordinary traversal
- use `for index, item in source.enumerate():` when both index and value matter
- use `for ref item in source:` or `for mutable ref item in source:` when the
  body needs references rather than copies
- use `for i in 0..<n:` for numeric ranges, index-only passes, fixed-count
  loops, explicit strides, and temporary gaps where a source has no iterable
  surface yet

## Surface Syntax

The first surface should support three binder modes.

### Value iteration

```elisa
for item in items:
    use(item)
```

### Readonly-ref iteration

```elisa
for ref item in items:
    inspect(item)
```

### Mutable-ref iteration

```elisa
for mutable item in items:
    normalize(item)
```

`mutable` alone denotes a mutable-ref binder — mutating a discarded per-iteration
copy is never the intent. `for mutable ref item in items:` is an accepted, more
explicit alias.

These forms should all share the same overall `for` shape.

## Loop Destructuring

The loop binder should accept either:

- a plain binding name
- an irrefutable destructuring pattern

That destructuring should reuse the same pattern/binder grammar the language is
already moving toward for statement-oriented binders.

Example shape:

```elisa
for Pair(key, value) in entries:
    consume_pair(key, value)

for ref Pair(key, value) in entries:
    inspect_pair(key, value)
```

The important orthogonality rule is:

> loop destructuring must reuse the existing binder/pattern machinery rather
> than inventing a second “loop-only destructuring” subsystem.

### First-slice restriction: irrefutable patterns only

Loop-header destructuring in the first slice should be limited to **irrefutable
patterns** for the yielded item type.

That means:

- destructuring a known aggregate shape is allowed
- variant-filtering or pattern-rejecting loop headers are deferred

So this kind of thing is fine when the item type is known to match:

```elisa
for Pair(key, value) in entries:
    ...
```

But this kind of filtering surface should remain deferred:

```elisa
for Expr.Add(left, right) in nodes:
    ...
```

when `nodes` yields arbitrary `Expr` values. Hidden filtering introduces hidden
control flow, which the language has repeatedly tried to avoid.

## Binder-Mode Semantics

### 1. Value binder

```elisa
for item in items:
    ...
```

This binds each yielded element as an ordinary by-value local.

In the first slice:

- this should be accepted for copyable yielded element types
- for builtin scalar-yield categories like ranges, this is the natural form
- consuming iteration of affine elements should remain deferred until there is
  an explicit consuming loop surface such as a future `for move item in items:`

### 2. Readonly-ref binder

```elisa
for ref item in items:
    ...
```

This binds each element as a readonly reference.

Conceptually, the item type seen in the body is:

```text
T&
```

This form is valid only when the iterable category can yield stable element
locations.

### 3. Mutable-ref binder

```elisa
for mutable ref item in items:
    ...
```

This binds each element as a writable reference.

Conceptually, the item type seen in the body is:

```text
mutable T&
```

This form is valid only when:

- the iterable category supports mutable element access
- the loop source is reached through an exclusive mutable path
- yielding each element as `mutable T&` is sound for that category

## Builtin Iterable Categories

The first slice should stay compiler-known and builtin.

### 1. Ranges

Examples:

```elisa
for i in 0..<len:
    ...
```

Range iteration should remain the simplest numeric category, not the default
way to traverse containers. Reach for it when the counter itself is the data,
when the loop needs custom bounds/stride, or when no iterable source exists.
When walking a collection, prefer the collection's iterable surface.

Properties:

- ordered
- exact length
- splittable later for parallel work
- value-yielding only in the first slice

Rejected in the first slice:

- `for ref i in 0..<len:`
- `for mutable ref i in 0..<len:`

because ranges do not represent stable element storage.

### 2. Fixed arrays, dynamic arrays, and views

This category should cover the language's linear element containers first:

- fixed arrays
- shape-typed arrays
- dynamic arrays / `darray`
- readonly and mutable views

Properties:

- ordered by index
- element extent known at loop entry
- contiguous / unit-stride when the underlying category already guarantees it
- candidate for future splitting/chunking forms

Supported forms:

- `for item in items:`
- `for ref item in items:`
- `for mutable ref item in items:` when the root is exclusively mutable

### 3. Strings and string views

String-like categories should participate in the same model.

Recommended first shape:

- `str` / `sview` iterate `char` in code-unit order
- `cstr` may additionally support mutable-ref iteration

Properties:

- ordered
- exact length in the current indexing model
- readonly by default unless the underlying string category is mutable

### 4. Tree child iterables

Trees must be considered explicitly in the iterator design.

But the right rule is conservative:

> a bare tree value is **not** implicitly iterable in the first slice.

Why not?

Because `for node in tree_value:` is ambiguous:

- direct children?
- preorder?
- postorder?
- breadth-first?
- only recursive child fields, or every stored payload field?

That ambiguity is exactly the kind of hidden control-flow choice the language
should avoid.

So the first tree-aware category should be an **explicit child iterable** such
as:

```elisa
for ref child in children(node):
    visit(child)
```

and:

```elisa
for mutable ref child in children(node):
    rewrite(child)
```

#### `children(node)` semantics

The first tree helper should mean:

- iterate the **direct children** of the active tree node
- no hidden recursion
- stable order defined by the tree declaration / payload order
- array child fields expand in index order at their lexical position
- non-child scalar payload fields do not participate

This gives tree users a real ergonomic win without forcing a hidden traversal
policy into the language.

When every structural child edge has the same item type, `children(node)` yields
that type directly. Exact members may therefore keep precise child element
types:

```elisa
for child in children(binary):
    total <- total + child.span
```

Mixed child categories need an explicit widening cast on the source value so the
result sequence has one common item type:

```elisa
for child in children(stmt.cast[Lua.Node]):
    total <- total + child.kind.i64()
```

The `children(...)` carrier also keeps the widened source node available through
`.node` when code needs to recover that exact root value explicitly.

```elisa
def root_of(stmt: Lua.Stmt) -> Lua.Node:
    return children(stmt.cast[Lua.Node]).node
```

Current rules:

- `children(node)` requires at least one structural child edge
- all structural child payloads must have the same item type unless the source is explicitly widened first
- `children(stmt.cast[Lua.Node])` is the canonical mixed-child form when a statement can own expressions, blocks, and sibling statements
- `children(expr).node` returns the source node value carried by that child view; this is most relevant after an explicit widening cast such as `children(stmt.cast[Lua.Node]).node`
- legacy override syntax such as `children(stmt to Lua.Node)` has been removed; use an explicit cast like `children(stmt.cast[Lua.Node])`
- incompatible overrides are rejected rather than silently dropping non-matching children
- explicit `link` payloads are not part of `children(...)`

#### Deferred tree traversals

These should stay explicit and deferred or library-level for now:

- `preorder(root)`
- `postorder(root)`
- `bfs(root)`
- filtered/tree-pattern iteration

Those are valuable, but they should be introduced as explicit traversal choices,
not hidden behind bare `for in` over a tree root.

## Loop Borrowing And Invalidation Rules

The loop source expression should be evaluated once before the loop starts.

Then the loop should hold whatever access mode is required for the chosen
binder form.

### For `ref` and `mutable ref`

The source iterable is borrowed for the duration of the loop.

That means in the first slice:

- structural mutation of the same root during the loop is rejected
- resizing/reallocation of the same root during the loop is rejected
- aliasing writes that would invalidate yielded refs are rejected

### For value iteration

Even value iteration should stay conservative at first.

Recommended first rule:

- structural mutation of the same root during iteration is rejected in phase 1

That keeps lowering and legality simpler until a more precise iterator
invalidation model exists.

## Destructuring And Binder Modes

Binder modes should apply to the loop binder as a whole.

Examples:

```elisa
for Pair(key, value) in entries:
    ...

for ref Pair(key, value) in entries:
    ...

for mutable ref Pair(key, value) in entries:
    ...
```

The intended interpretation is:

- value form binds the leaves by value
- `ref` form binds the leaves as readonly refs where that is valid
- `mutable ref` form binds the leaves as mutable refs where that is valid

If a user wants copies from a ref-based loop, they can copy explicitly inside
the body.

## Parallelism Hooks

This iterator design should be built with future data-parallel forms in mind,
even though plain `for` remains sequential.

The builtin iterable categories should therefore carry or imply compiler-known
facts such as:

- `ordered`
- `exact_len`
- `contiguous`
- `unit_stride`
- `disjoint_elements`
- `splittable`

Not every category has every fact.

### Category expectations

#### Ranges

- ordered
- exact length
- splittable

#### Linear arrays / views / darrays

- ordered
- exact length at loop entry
- often contiguous / unit-stride
- future splittable category when split/chunk helpers prove disjointness

#### Strings

- ordered
- exact length in the current code-unit model
- readonly by default unless the category itself is mutable

#### Tree child iterables

- ordered
- exact length for the active node
- **not contiguous**
- **not splittable by default**

This distinction matters. Trees are first-class citizens in the iteration
model, but they are not secretly linear buffers.

That is exactly why tree iteration must be explicit about traversal shape.

## Further Parallel Forms Must Reuse This Model

The current compiler already has an explicit pool-scoped `parallel for`
surface over frozen packed stores and readonly exact-extent views. If the
language later adds more general chunked, zipped, or iterator-oriented
parallel forms, they should still be defined over the same iterable
categories and helper-produced facts.

Recommended later helper surfaces:

- `split_at(source, index)` / `source.split_at(index)`
- `chunks_exact(source, width)` / `source.chunks_exact(width)`
- `enumerate(source)` / `source.enumerate()`
- `readonly(source)` / `source.readonly()`
- `any(source)` / `source.any()`
- `all(source)` / `source.all()`
- `reduce_sum(source, callback)` / `source.reduce_sum(callback)`
- `zip_exact(...)`

These are good follow-ons because they can produce optimization legality facts
without inventing target-specific pragmas or a separate parallel-iterator
dialect.

The design rule should remain:

> source chooses explicit traversal structure; compiler proves legality; backend
> chooses profitability and lowering.

## Lowering Model

The first lowering model should stay simple and predictable.

### Ranges

Lower to explicit arithmetic induction-variable loops.

### Linear collections

Lower to explicit index/cursor loops over the known collection representation.

### `ref` / `mutable ref`

Lower to rebinding a reference to the current element slot each iteration.

### Trees

Lower `children(node)` to an explicit child cursor/view over the active node's
direct child slots.

Important invariant:

> tree iteration must not lower through hidden recursion or heap-allocated
> iterator objects in the first slice.

## Staging Recommendation

### Stage 1

Implement together:

- builtin iterable categories for:
  - ranges
  - arrays/views/darrays
  - strings
  - explicit tree child iterables
- binder modes:
  - value
  - `ref`
  - `mutable ref`
- irrefutable loop destructuring

### Stage 2

Add stronger helper surfaces that also serve future optimization work:

- exact chunking
- explicit splitting
- zip/equal-extent helpers
- enumeration helpers

### Stage 3

Add one narrow explicit data-parallel loop or kernel form that reuses the same
iterable categories and legality facts.

## Summary

The right first iterator feature for `elisacore` is:

- builtin iterable categories first
- `for in` over values, readonly refs, and mutable refs
- loop destructuring using existing binder-pattern machinery
- explicit direct-child iteration for trees
- plain `for` stays sequential
- future parallelism builds on the same iterable model rather than replacing it

That gives the language a large ergonomic win now while staying aligned with
tree-shaped IR/code, proof-oriented legality, and future parallel features.
