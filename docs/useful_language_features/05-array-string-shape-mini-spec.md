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

- exact fixed-array typing for `array[T, N]` and `T[N]`
- dynamic shape witnesses for `darray[T, shape]`, `dstr[shape]`, and `DList[T, shape]`
- non-owning view types `view[T, begin, end]`, `DListView[T]`, and `sview[begin, end]`
- indexing for fixed arrays, dynamic arrays/views, lists/views, strings, and string views
- slice syntax producing view-like results:
    - `array[T, N]`, `T[N]`, and their non-null references slice to `view[T, start, end]`
    - `darray[T, shape]` and `view[T, begin, end]` slices produce `view[T, start, end]`
    - `DList[T, shape]` and `DListView[T]` slices produce `DListView[T]`
    - `dstr[shape]`, `str[N]`, and `sview[begin, end]` slices produce `sview[start, end]`

The compiler still accepts `string[...]` and `dstring[...]` as compatibility aliases, but `str[...]` and `dstr[...]` are now the canonical user-facing spellings.

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
array[u8, 16]
array[Node, 4]
array[T, N]

# shorthand form still accepted for fixed arrays
u8[16]
```

This is already very good and should remain the canonical syntax for fixed-length arrays.

### Owned dynamic arrays

I would suggest one of these explicit forms:

```context
darray[T, n]
owned_array[T, n]
```

I slightly prefer:

```context
darray[T, n]
```

because it makes the “this is dynamic storage” distinction very obvious.

### Strings

For strings I would distinguish between logical string length and raw byte arrays:

```context
u8[N]             # raw fixed byte array
str[N]            # fixed string / byte-string with known logical length
dstr[n]           # owned dynamic string with tracked logical length
```

If you want maximum minimalism, `str[N]` and `dstr[n]` can just be language-level wrappers over `u8[N]` and `darray[u8, n]`.

## Core type constructors

Formally, let the type layer have:

```text
array[T, N]      fixed-size array, N compile-time known
darray[T, n]     owned dynamic array, logical length index n
str[N]           fixed string of logical length N
dstr[n]          owned dynamic string of logical length n
```

Where:

- `N` is a compile-time natural number
- `n` is a shape/length index for dynamic storage

The critical design choice is what kind of thing `n` is.

## Two possible models for dynamic lengths

### Model A — exact dependent runtime index

This is the mathematically strongest version.

```text
darray[T, n]
```

where `n` is an actual runtime value appearing in the type.

Then you can write signatures like:

```text
push    : darray[T, n] × T -> darray[T, n + 1]
concat  : darray[T, a] × darray[T, b] -> darray[T, a + b]
slice   : darray[T, n] × i × j -> view[T, i, j]
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
darray[T, shape_id]
```

where `shape_id` is a shape witness / logical length identity.

Operationally:

- the runtime value still contains a `len` field
- the compiler tracks that this particular value currently has some logical length identity
- operations that change length produce a *new* shape identity

So:

```text
resize : darray[T, shape_in] × usize -> darray[T, shape_out]
push   : darray[T, shape_in] × T     -> darray[T, shape_out]
concat : darray[T, shape_left] × darray[T, shape_right] -> darray[T, shape_result]
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

User-facing code should talk in terms of the built-in `darray[T, shape]` surface.

Internally, the runtime representation can still stay equivalent to a simple C-like carrier with a data pointer, length, and capacity.

### Dynamic string representation

User-facing code should talk in terms of `str[N]`, `dstr[shape]`, and `sview[begin, end]`.

Internally, the runtime representation can still stay equivalent to a simple C-like byte-buffer carrier.

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
Γ ⊢ e : darray[T, shape_id]
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

Assuming the operation may need to allocate, the most honest surface is a fallible one:

```text
error ShapeOpError:
    AllocationFailed
```

```text
Γ ⊢ a : darray[T, shape_in]
Γ ⊢ m : usize
--------------------------------
Γ ⊢ resize(a, m) : darray[T, shape_out] error[ShapeOpError]
```

where `shape_out` is fresh.

This matches your idea exactly:

> if you resize, you must construct/cast to a dynamic array of a different length.

The important part is that this cast is **logical and zero-overhead**, not a runtime reinterpretation hack.

### Push / append rule

```text
Γ ⊢ a : darray[T, shape_in]
Γ ⊢ x : T
--------------------------------
Γ ⊢ push(a, x) : darray[T, shape_out] error[ShapeOpError]
```

Again `shape_out` is fresh, because the logical shape changed.

### Concatenation rule

```text
Γ ⊢ a : darray[T, shape_left]
Γ ⊢ b : darray[T, shape_right]
--------------------------------
Γ ⊢ concat(a, b) : darray[T, shape_result] error[ShapeOpError]
```

`shape_result` is fresh.

The exact arithmetic relation can remain part of library semantics/documentation instead of core type equality.

## Strings

Strings should mirror arrays closely.

### Fixed strings

```text
Γ ⊢ s : str[N]
```

This is useful for:

- string literals
- compile-time known buffers
- APIs where exact fixed size matters

### Dynamic strings

```text
Γ ⊢ s : dstr[shape_id]
```

with exactly the same logical-shape story as `darray[u8, shape_id]`.

In the current implementation, `dstr[shape_id]` and `str[N]` are also paired with a non-owning runtime-backed `sview[begin, end]` surface for slicing and view-style APIs.

### Relationship to byte arrays

You can define:

```text
str[N]  ≈ array[u8, N+1]  # if you include trailing zero in representation
str[N]  ≈ array[u8, N]    # if logical string length excludes terminator and representation policy is separate
```

I would keep the logical length separate from terminator policy.

That is:

- `str[N]` means logical content length `N`
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

- `view[T, begin, end]` is the preferred surface for dynamic-array and fixed-array slice results
- `DListView[T]` remains the explicit Stage 1 typed-list view surface for the older list runtime helpers
- `sview[begin, end]` is the preferred surface for string slices and runtime-backed string views

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

Add a runtime carrier equivalent to a C-like growable array with a data pointer, length, and capacity.

Then expose compiler-level logical shape wrappers incrementally.

At first, this can even be mostly API-discipline plus type wrappers.

Status: implemented for `darray`, `dstring`, `DList`, and the current runtime bridge.

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
append : array[T, n] × T -> array[T, n+1]
concat : str[A] × str[B] -> str[A+B]
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
darray[T, n]
dstr[n]
view[T, begin, end]
sview[begin, end]
```

as logical length-indexed owned containers.

### And define core APIs like

```text
resize      : darray[T, shape_in] × usize -> darray[T, shape_out] error[ShapeOpError]
push        : darray[T, shape_in] × T -> darray[T, shape_out] error[ShapeOpError]
append_many : darray[T, shape_in] × darray[T, chunk] -> darray[T, shape_out] error[ShapeOpError]
truncate    : darray[T, shape_in] × usize -> darray[T, shape_out]
clear       : darray[T, shape_in] -> darray[T, shape_out]
concat      : dstr[shape_left] × dstr[shape_right] -> dstr[shape_result] error[ShapeOpError]
```

String indexing now yields `char`. Cast explicitly when you want an integer code unit:

```context
def first_char(text: str[4]) -> char:
    return text[0]

def first_code(text: str[4]) -> i64:
    return text[0].i64()
```

Current meaning note: today `char` is best understood as the element/code-unit type yielded by `str`, `dstr`, and `sview` indexing. In the current implementation it lowers like an `i64`, but user-facing code should treat it as a distinct scalar rather than “just an integer with a funny hat”.

That also leaves room for future extensions such as encoding-qualified character forms if the language eventually wants to distinguish byte-oriented characters from wider text elements.

where each shape-changing operation returns a new logical shape, and allocation-sensitive ones can report failure explicitly instead of silently smuggling it through null/sentinel conventions.

That gives you the flavor you want:

- low-level representation
- no borrow checker
- no lifetime analysis
- dependent-style safety facts exactly where they matter

## Best concise slogan

If pointers are typestated by validity, arrays and strings should be typestated by shape.

That gives Contextlang a very crisp identity:

> C-like memory model, with lightweight dependent typing for validity and shape.
