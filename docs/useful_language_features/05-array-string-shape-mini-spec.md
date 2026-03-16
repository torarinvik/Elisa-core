# Array and String Shape Mini-Spec

This section proposes a shape/length-typed array and string model for Contextlang.

The goal is the same as the pointer typestate goal:

- keep runtime representation simple and C-like
- keep operations zero-overhead where possible
- move important safety facts into the type system
- avoid borrow checking and lifetime analysis

In short:

> Arrays and strings should carry **shape facts** in the type in the same way pointers carry **validity facts** in the type.

## Current implementation snapshot (March 2026)

The current compiler follows the recommended lightweight model rather than full arithmetic dependence.

Implemented today:

- exact fixed-array typing for `T[N]`
- dynamic shape witnesses for `DArray[T, shape]`, `DStr[shape]`, and `DList[T, shape]`
- non-owning view types `DArrayView[T]`, `DListView[T]`, and `CtxStringView`
- indexing for fixed arrays, dynamic arrays/views, lists/views, strings, and string views
- slice syntax producing view-like results:
    - `T[N]` / `T[N]&` slices lower to `DArrayView[T]`
    - `DArray[T, shape]` and `DArrayView[T]` slices produce `DArrayView[T]`
    - `DList[T, shape]` and `DListView[T]` slices produce `DListView[T]`
    - `DStr[shape]` and `CtxStringView` slices produce `CtxStringView`

Still deferred:

- symbolic arithmetic equality over shape expressions such as `a + b` or `j - i`
- proof terms or solver-backed index constraints

## High-level design split

There are three natural families:

1. **static arrays** with compile-time length
2. **owned dynamic arrays** with type-tracked logical length identity
3. **strings** as specialized byte arrays / byte buffers with length facts

## Candidate surface syntax

I think the cleanest surface story is:

### Static arrays

```context
u8[16]
Node[4]
T[N]
```

This is already very good and should remain the canonical syntax for fixed-length arrays.

### Owned dynamic arrays

I would suggest one of these explicit forms:

```context
DArray[T, n]
OwnedArray[T, n]
```

I slightly prefer:

```context
DArray[T, n]
```

because it makes the “this is dynamic storage” distinction very obvious.

### Strings

For strings I would distinguish between logical string length and raw byte arrays:

```context
u8[N]       # raw fixed byte array
Str[N]      # fixed string / byte-string with known logical length
DStr[n]     # owned dynamic string with tracked logical length
```

If you want maximum minimalism, `Str[N]` and `DStr[n]` can just be library-level wrappers over `u8[N]` and `DArray[u8, n]`.

## Core type constructors

Formally, let the type layer have:

```text
Array(T, N)      fixed-size array, N compile-time known
DArray[T, n]     owned dynamic array, logical length index n
Str[N]           fixed string of logical length N
DStr[n]          owned dynamic string of logical length n
```

Where:

- `N` is a compile-time natural number
- `n` is a shape/length index for dynamic storage

The critical design choice is what kind of thing `n` is.

## Two possible models for dynamic lengths

### Model A — exact dependent runtime index

This is the mathematically strongest version.

```text
DArray[T, n]
```

where `n` is an actual runtime value appearing in the type.

Then you can write signatures like:

```text
push    : DArray[T, n] × T -> DArray[T, n + 1]
concat  : DArray[T, a] × DArray[T, b] -> DArray[T, a + b]
slice   : DArray[T, n] × i × j -> DArray[T, j - i]
```

This is gorgeous.

But it means the compiler must reason about:

- arithmetic normalization
- equality of symbolic expressions
- constraints like `i <= j <= n`
- inference of shape variables

That is a major jump in compiler complexity.

### Model B — lightweight indexed safety (recommended)

This is the version I recommend first.

Instead of giving the typechecker full arithmetic over runtime naturals, give each dynamic shape a tracked index identity.

Conceptually:

```text
DArray[T, shape_id]
```

where `shape_id` is a shape witness / logical length identity.

Operationally:

- the runtime value still contains a `len` field
- the compiler tracks that this particular value currently has some logical length identity
- operations that change length produce a *new* shape identity

So:

```text
resize : DArray[T, shape_in] × usize -> DArray[T, shape_out]
push   : DArray[T, shape_in] × T     -> DArray[T, shape_out]
concat : DArray[T, shape_left] × DArray[T, shape_right] -> DArray[T, shape_result]
```

where `shape_out`, `shape_result` are fresh post-operation shapes.

This avoids requiring the compiler to prove that `shape_result = shape_left + shape_right` at the type-equality level.

In examples below, ASCII witness names such as `shape_in`, `shape_out`, `shape_result`, and `shape_after` are preferred.
Greek-letter shorthands are acceptable as mathematical shorthand, but they are not required in source code.

It preserves the key safety idea:

> after an operation that changes length, the result has a different logical shape than the input.

That already buys a lot.

## Recommended runtime representation

I would keep representation brutally simple.

### Dynamic array representation

```context
repr(c) struct DArray[T]:
    data: T&?
    len: usize
    cap: usize
```

### Dynamic string representation

```context
repr(c) struct DStr:
    data: u8&?
    len: usize
    cap: usize
```

This is excellent because it is:

- trivial to FFI
- trivial to debug
- trivially zero-overhead as data layout
- compatible with the pointer typestate story (`data` is optional storage and must be proven before raw use)

So the type-level safety lives above a very ordinary low-level runtime shape.

## Static arrays

Static arrays are the easiest and strongest part of the design.

### Typing

```text
Γ ⊢ e : Array(T, N)
```

### Indexing rule

```text
Γ ⊢ e : Array(T, N)
Γ ⊢ i : Int
-----------------------------
Γ ⊢ e[i] : T
```

If `i` is a compile-time constant, the compiler may reject out-of-bounds at compile time.

If `i` is dynamic, you can choose one of two policies:

1. unchecked indexing like C
2. checked indexing in safe surface forms, unchecked in explicit low-level forms

For Contextlang, I would keep the low-level spirit and make this a policy choice independent of the type system.

### Array construction

```text
Γ ⊢ e1 : T ... Γ ⊢ eN : T
----------------------------------------
Γ ⊢ [e1, ..., eN] : Array(T, N)
```

## Owned dynamic arrays

### Typing judgment

For the recommended lightweight model:

```text
Γ ⊢ e : DArray[T, shape_id]
```

where `shape_id` is a logical shape witness.

### Length observation

At runtime:

```text
len(e) : usize
```

At the type level, `shape_id` means “the current logical length fact associated with this value”, not necessarily a normalized arithmetic term.

### Resize rule

Resize changes shape identity:

```text
Γ ⊢ a : DArray[T, shape_in]
Γ ⊢ m : usize
--------------------------------
Γ ⊢ resize(a, m) : DArray[T, shape_out]
```

where `shape_out` is fresh.

This matches your idea exactly:

> if you resize, you must construct/cast to a dynamic array of a different length.

The important part is that this cast is **logical and zero-overhead**, not a runtime reinterpretation hack.

### Push / append rule

```text
Γ ⊢ a : DArray[T, shape_in]
Γ ⊢ x : T
--------------------------------
Γ ⊢ push(a, x) : DArray[T, shape_out]
```

Again `shape_out` is fresh, because the logical shape changed.

### Concatenation rule

```text
Γ ⊢ a : DArray[T, shape_left]
Γ ⊢ b : DArray[T, shape_right]
--------------------------------
Γ ⊢ concat(a, b) : DArray[T, shape_result]
```

`shape_result` is fresh.

The exact arithmetic relation can remain part of library semantics/documentation instead of core type equality.

## Strings

Strings should mirror arrays closely.

### Fixed strings

```text
Γ ⊢ s : Str[N]
```

This is useful for:

- string literals
- compile-time known buffers
- APIs where exact fixed size matters

### Dynamic strings

```text
Γ ⊢ s : DStr[shape_id]
```

with exactly the same logical-shape story as `DArray[u8, shape_id]`.

In the current implementation, `DStr[shape_id]` is also paired with a non-owning runtime-backed `CtxStringView` for slicing and view-style APIs.

### Relationship to byte arrays

You can define:

```text
Str[N]  ≈ Array(u8, N+1)  # if you include trailing zero in representation
Str[N]  ≈ Array(u8, N)    # if logical string length excludes terminator and representation policy is separate
```

I would keep the logical length separate from terminator policy.

That is:

- `Str[N]` means logical content length `N`
- whether a trailing `0` exists is a representation convention, not the type-level meaning

That keeps the model cleaner.

## Do you still need spans/views?

In the original design discussion, the answer here was “not immediately.”

That has since changed in the implementation: view types are now part of the practical surface because they are the simplest zero-copy result for slices and subranges.

If owned arrays and strings already carry length, you can get very far without separate view types.

That said, I would frame it like this:

- **first implementation:** no spans/views required
- **long-term:** likely still useful for subranges and non-owning windows

Current status:

- `DArrayView[T]` exists for dynamic-array and fixed-array slice results
- `DListView[T]` exists and is also exposed as the alias `view[T]`
- `CtxStringView` exists for string slices and runtime-backed string views

So I agree with your instinct as an implementation priority:

> do not start with spans.

But I would not bake in the claim that they will never be needed.

## Formal safety intent

The array/string equivalent of the pointer theorem-of-intent is:

> If a program type-checks, shape-changing operations produce new logical shapes explicitly, and fixed-size shape facts are never silently forgotten.

That is the key idea.

In other words:

- pointers track **validity facts**
- arrays/strings track **shape facts**

This is a very coherent type story.

## Cheap-and-practical implementation ladder

I would implement this in the following order.

### Stage 1 — strengthen static arrays

Do this first.

- keep `T[N]`
- make the type checker treat the length as fully part of the type
- add better compile-time constant index checking
- allow array literals and exact array-type matching

This is high value and relatively cheap.

Status: implemented, including exact fixed-array typing and constant compile-time out-of-bounds diagnostics.

### Stage 2 — library/runtime-owned dynamic arrays

Add a runtime struct like:

```context
repr(c) struct DArray[T]:
    data: T&?
    len: usize
    cap: usize
```

Then expose compiler-level logical shape wrappers incrementally.

At first, this can even be mostly API-discipline plus type wrappers.

Status: implemented for `DArray`, `DStr`, `DList`, and the current runtime bridge.

### Stage 3 — logical post-operation shape change

Teach the typechecker that operations like:

- `resize`
- `push`
- `append_many`
- `truncate`
- `clear`
- `concat`
- `strcat`

produce a new logical shape identity.

This gets you the safety effect you want without arithmetic normalization.

Status: implemented for the current known shape-changing APIs.

### Stage 4 — optional arithmetic indexing

Only if the language really wants it later, add exact arithmetic forms like:

```text
append : Array(T, n) × T -> Array(T, n+1)
concat : Str[A] × Str[B] -> Str[A+B]
```

This is beautiful, but it should be a later stage, not the starting point.

Status: still deferred, which is intentional.

## Recommended surface-language summary

If I had to propose the most practical version today, it would be:

### Keep

```context
T[N]
```

for static arrays.

### Add

```context
DArray[T, n]
DStr[n]
```

as logical length-indexed owned containers.

### And define core APIs like

```text
resize      : DArray[T, shape_in] × usize -> DArray[T, shape_out]
push        : DArray[T, shape_in] × T -> DArray[T, shape_out]
append_many : DArray[T, shape_in] × DArray[T, chunk] -> DArray[T, shape_out]
truncate    : DArray[T, shape_in] × usize -> DArray[T, shape_out]
clear       : DArray[T, shape_in] -> DArray[T, shape_out]
concat      : DStr[shape_left] × DStr[shape_right] -> DStr[shape_result]
```

where each shape-changing operation returns a new logical shape.

That gives you the flavor you want:

- low-level representation
- no borrow checker
- no lifetime analysis
- dependent-style safety facts exactly where they matter

## Best concise slogan

If pointers are typestated by validity, arrays and strings should be typestated by shape.

That gives Contextlang a very crisp identity:

> C-like memory model, with lightweight dependent typing for validity and shape.
