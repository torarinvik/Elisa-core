# Iterators And `for in` Mini-Spec

This document proposes the first general iterable surface for Contextlang /
`llcontext`.

The goal is **not** to start with generators, lazy pipelines, or user-authored
iterator traits.

The goal is to generalize the existing range-shaped loop surface:

```context
for index in 0u..<items.len:
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
- auto-parallelization or a `parallel for` surface in this document
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
> future parallel forms should reuse that same iterable model rather than
> inventing a second traversal dialect.

## Surface Syntax

The first surface should support three binder modes.

### Value iteration

```context
for item in items:
    use(item)
```

### Readonly-ref iteration

```context
for ref item in items:
    inspect(item)
```

### Mutable-ref iteration

```context
for mutable ref item in items:
    normalize(item)
```

These forms should all share the same overall `for` shape.

## Loop Destructuring

The loop binder should accept either:

- a plain binding name
- an irrefutable destructuring pattern

That destructuring should reuse the same pattern/binder grammar the language is
already moving toward for statement-oriented binders.

Example shape:

```context
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

```context
for Pair(key, value) in entries:
    ...
```

But this kind of filtering surface should remain deferred:

```context
for Expr.Add(left, right) in nodes:
    ...
```

when `nodes` yields arbitrary `Expr` values. Hidden filtering introduces hidden
control flow, which the language has repeatedly tried to avoid.

## Binder-Mode Semantics

### 1. Value binder

```context
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

```context
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

```context
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

```context
for i in 0u..<len:
    ...
```

Range iteration should remain the simplest category.

Properties:

- ordered
- exact length
- splittable later for parallel work
- value-yielding only in the first slice

Rejected in the first slice:

- `for ref i in 0u..<len:`
- `for mutable ref i in 0u..<len:`

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
- `dstr` may additionally support mutable-ref iteration

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

```context
for ref child in children(node):
    visit(child)
```

and:

```context
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

```context
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

## Future Parallel Forms Must Reuse This Model

When the language later adds a restricted `parallel for` or chunk kernel form,
it should be defined over the same iterable categories and helper-produced
facts.

Recommended later helper surfaces:

- `split_at(...)`
- `chunks_exact(...)`
- `zip_exact(...)`
- `enumerate(...)`

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

The right first iterator feature for `llcontext` is:

- builtin iterable categories first
- `for in` over values, readonly refs, and mutable refs
- loop destructuring using existing binder-pattern machinery
- explicit direct-child iteration for trees
- plain `for` stays sequential
- future parallelism builds on the same iterable model rather than replacing it

That gives the language a large ergonomic win now while staying aligned with
tree-shaped IR/code, proof-oriented legality, and future parallel features.