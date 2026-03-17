# Compiler Implementation Plan for Shape-Typed Arrays and Strings

This section turns the array/string design into a concrete compiler roadmap.

The goal is **not** to implement full dependent typing immediately.
The goal is to get the high-value safety properties first while keeping the frontend simple enough to evolve.

## Current implementation snapshot (March 2026)

This document started as a forward-looking roadmap, but several of the earlier phases are now implemented in the compiler.

Implemented today:

- exact fixed-array typing for `array[T, N]` and `T[N]`
- mismatched fixed-array rejection
- compile-time constant out-of-bounds diagnostics for fixed arrays
- lightweight shape witnesses for `darray[T, shape]` and `dstr[shape]`
- fresh post-operation shapes for shape-changing APIs such as `resize`, `push`, `concat`, and `strcat`
- indexing for `darray`, `view`, `dstr`, `str`, and `sview`
- slice syntax for `darray`, `view`, fixed arrays, `dstr`, `str`, and `sview`
- the built-in `view[T, begin, end]`, `dview[T]`, and `sview[begin, end]` surface syntax, with `view[T]` retained as a shorthand for compile-time array views
- pointer arithmetic lowering (`ref + int`, `int + ref`, `ref - int`)
- explicit reference comparisons (`ref == null`, `ref != null`, `ref == ref`)
- end-to-end fixture coverage through the real CLI pipeline for `Code/test_programs/pointer_alloc.llcontext` and `Code/test_programs/shape_ops.llcontext`

The user-facing built-in spellings are lowercase only: use `str[...]` and `dstr[...]`, not legacy aliases like `string[...]` or `dstring[...]`.

Still intentionally deferred:

- symbolic arithmetic normalization for shape expressions such as `a + b` and `j - i`
- proof-carrying index constraints
- a richer algebra of subrange/view identities beyond the current lightweight model

## Guiding implementation principle

Implement this as:

- **exact shape typing for fixed arrays**
- **logical shape identities for dynamic owned arrays/strings**
- **no arithmetic normalization in the first implementation**

In one sentence:

> make shape part of the type, but make shape *equality* cheap.

## Recommended rollout order

There is a very clear best order.

### Phase 1 — strengthen existing fixed arrays

This should be the MVP.

Why first:

- syntax already exists: `T[N]`
- the parser already understands array type forms
- the semantic system already has `ArrayType`
- it gives immediate value with relatively low compiler churn

Ship in this phase:

- treat array length as an exact type-level property everywhere
- reject assigning `T[4]` to `T[5]`
- support exact array literals / construction typing if desired
- improve compile-time constant index diagnostics

Status: complete for exact fixed-array typing, mismatch rejection, and constant-index diagnostics. Exact fixed-array literal typing is still a possible follow-on, but it is no longer blocking the array-shape MVP.

This phase is cheap and gives a lot of shape safety immediately.

### Phase 2 — add owned dynamic array/string surface types

Add syntax and semantic meaning for:

```context
darray[T, n]
dstr[n]
view[T, begin, end]
sview[begin, end]
```

But initially interpret `n` as a **logical shape witness**, not a symbolic arithmetic term.

This phase should focus on:

- syntax
- type representation
- assignability rules
- runtime representation conventions

not on complicated inference.

Status: complete for `darray[T, shape]`, `dstr[shape]`, `view[T, begin, end]`, and `dview[T]` under the current lightweight shape-witness model. The compiler accepts lowercase shape-erasing shorthand such as `darray[T]`, bare `dstr`, and `dview[T]` when code does not need to preserve an explicit logical shape relationship. The older `DList` / `DListView` surface has been removed from the language.

### Phase 3 — teach shape-changing APIs to produce fresh post-state shapes

Once `darray[T, n]` exists, teach the analyzer that specific operations return a *new* shape.

For example:

```text
resize : darray[T, shape_in] × usize -> darray[T, shape_out]
push   : darray[T, shape_in] × T -> darray[T, shape_out]
concat : darray[T, shape_left] × darray[T, shape_right] -> darray[T, shape_result]
```

where `shape_out`, `shape_result` are fresh logical shape identities.

This captures the safety idea without needing arithmetic reasoning.

Status: complete for the currently-known shape-changing APIs in the semantic layer.

### Phase 4 — optional symbolic arithmetic later

Only later, if it proves worthwhile, add exact arithmetic shape forms like:

```text
n + 1
a + b
j - i
```

This should be an explicit later milestone, not part of the initial implementation.

## Concrete compiler plan by subsystem

## 1. AST plan

### Phase 1 AST changes

Fixed arrays may not need major AST changes if current `ArrayType` already stores:

- element type
- size expression

But the AST should preserve whether the size expression is:

- a compile-time constant literal
- a named constant
- a general expression

If not already represented cleanly, this is the moment to formalize that.

### Phase 2 AST additions

For dynamic arrays/strings, add distinct type nodes rather than overloading `GenericType` too much semantically.

Recommended AST additions:

```text
DynArrayType {
    Elem TypeExpr
    Shape TypeExpr or ShapeExpr
}

DynStringType {
    Shape TypeExpr or ShapeExpr
}
```

Why distinct nodes are better than generic-only treatment:

- clearer semantic handling
- clearer diagnostics
- easier to special-case shape evolution later
- avoids burying important language concepts in generic sugar

If you want lighter syntax implementation at first, parsing them as special generic names is acceptable, but semantically they should become dedicated internal forms.

## 2. Parser plan

### Phase 1 parser work

Very small.

Likely just:

- keep `T[N]`
- ensure array size expressions are preserved accurately
- optionally add array literal parsing if desired

### Phase 2 parser work

Teach the parser to recognize:

```context
darray[T, n]
dstr[n]
view[T, begin, end]
sview[begin, end]
```

Recommendation:

- parse them first as special named/generic forms
- lower them into dedicated AST nodes during parse or semantic resolution

That keeps syntax work small.

### Syntax recommendation

I would use:

```context
darray[T, n]
dstr[n]
view[T, begin, end]
sview[begin, end]
```

not something more magical.

This keeps the feature visually explicit and predictable.

## 3. Semantic type representation plan

### Phase 1 semantic work

Strengthen existing fixed arrays.

Current semantic `ArrayType` likely already contains:

- element type
- size summary

Improve it to distinguish exact shape identity more rigorously.

Recommended representation:

```text
ArrayType {
    Elem Type
    Size ShapeTerm
}
```

Where `ShapeTerm` in phase 1 can simply be:

- integer literal
- resolved named constant
- opaque textual fallback for diagnostics

### Phase 2 semantic additions

Add explicit semantic types:

```text
DynArrayType {
    Elem Type
    Shape ShapeWitness
}

DynStringType {
    Shape ShapeWitness
}
```

Where `ShapeWitness` is intentionally lightweight.

Recommended initial `ShapeWitness` forms:

```text
ConstShape(n)
NamedShape(name)
FreshShape(id)
OpaqueShape(text)
```

This is enough to express:

- exact fixed lengths
- named lengths
- post-operation fresh dynamic lengths
- diagnostics for unresolved/opaque forms

without symbolic arithmetic.

## 4. Assignability and equality rules

### Fixed arrays

Require exact equality of:

- element type
- shape term

So:

```text
Array(T, 4)  ≠ Array(T, 5)
Array(T, N)  = Array(T, N)
```

### Dynamic owned arrays

Require exact equality of:

- element type
- shape witness

So after a resize-like operation, the returned value is *not* interchangeable with the input type unless explicitly rebound.

That is a feature, not a bug.

It is exactly the safety guarantee you asked for.

## 5. Builtin/API knowledge plan

To make dynamic shapes useful without deep dependence, the analyzer should learn a small number of shape-transforming operations specially.

Example categories:

- `resize`
- `push`
- `append_many`
- `concat`
- `strcat`
- `truncate`
- `clear`

The easiest implementation strategy is:

- keep a table of known shape-transforming functions
- when the analyzer sees those calls, synthesize a fresh result shape witness

This is similar in spirit to how many compilers treat intrinsics specially.

It is much cheaper than making every function truly dependently typed.

## 6. String plan

Strings should reuse the same machinery as arrays as much as possible.

Recommended internal rule:

- `dstr[shape_id]` is semantically very close to `DynArrayType{Elem: u8, Shape: shape_id}`
- `str[N]` is semantically very close to `array[u8, N]` plus string-specific intent

Whether you expose them as separate semantic types or thin wrappers is mostly an ergonomics decision.

My preference:

- keep separate semantic string types for diagnostics and language clarity
- internally reuse array/shape machinery as much as possible

## 7. Diagnostics plan

This feature will only feel good if diagnostics are excellent.

Important errors to support clearly:

- mismatched fixed lengths
- using an old dynamic-shape value where a post-resize shape is required
- indexing with compile-time out-of-bounds constants
- illegal implicit weakening/forgetting of shape facts

Example good diagnostic style:

```text
cannot assign darray[u8, row] to darray[u8, shape_after]
note: resize returns a fresh logical shape for shape_out
```

And when the mismatch comes from comparing two separate fresh-producing calls, a second note should explain that they do not unify implicitly:

```text
argument 2 to "same" expects darray[i32, shape_after#1], got darray[i32, shape_after#2]
note: grow returns a fresh logical shape for shape_after
note: separate calls that produce fresh shapes do not share the same logical shape identity
```

That kind of message teaches the model, not just the failure.

## 8. Testing plan

The test plan should also be phased.

### Phase 1 tests

- exact equality for fixed arrays
- rejecting mismatched fixed lengths
- constant-index bounds diagnostics
- fixed-array literals/initialization if added

Current coverage includes exact fixed-array equality, mismatch rejection, and constant out-of-bounds diagnostics.

### Phase 2 tests

- parsing `darray[T, n]`, `dstr[n]`, `view[T, begin, end]`, and `sview[begin, end]`
- type equality for dynamic shape witnesses
- shape witness preservation across plain assignment

Current coverage includes parsing and semantic checks for `darray`, `dstr`, `view`, `sview`, and their runtime-bridge behavior.

### Phase 3 tests

- `resize` returns a new shape witness
- `push` returns a new shape witness
- `concat` returns a fresh result witness
- old-shape values rejected where new-shape values are expected

Current coverage includes fresh-shape behavior for shape-changing APIs and regression tests around runtime-backed indexing, slicing, string helpers, pointer arithmetic, and reference comparisons.

### Current end-to-end coverage

Beyond semantic/backend unit regressions, the compiler now also has CLI-level fixture tests that compile real source files from `Code/test_programs/`:

- `pointer_alloc.llcontext`
- `shape_ops.llcontext`

Those tests exercise the actual include expansion, parse, semantic, and emit pipeline for LLVM IR, and also cover bitcode/object emission for a fixture program.

### Example regression style

```context
error ShapeOpError:
    AllocationFailed

def grow(a: darray[u8, row]) -> darray[u8, shape_after] error[ShapeOpError]:
    return try resize(a, 16)
```

and:

```context
def bad(a: darray[u8, row]) -> darray[u8, row] error[ShapeOpError]:
    return try resize(a, 16)   # should still fail if resize returns fresh shape_after
```

That is exactly the kind of “dependent-ish” safety check the language should advertise.

## 9. Example/runtime plan

Do not rewrite all runtime code around dynamic shape typing immediately.

Recommended rollout is now:

1. keep the low-level runtime representations plain and representation-first
2. add shape-typed wrapper layers at the container/string API boundary
3. only push shape typing deeper into the runtime if the model continues to pay for itself

### Current integration status

The codebase is already following this staged approach:

- low-level stage 0 runtime code still uses representation-first types such as `DynArray[T]`, `StringBuilder`, `StringView`, and raw `u8&` string values
- `arena.llcontext` now exposes shape-typed append helpers such as `arena_da_append` and `arena_da_append_many`
- `contextlang_runtime.llcontext` stage 1 wrappers now expose typed logical APIs such as `ctx_stage1rt_concat2`, `ctx_stage1rt_string_slice`, and the `ctx_stage1rt_string_view*` helpers for string subviews
- `arena.llcontext` now also exposes typed non-owning `dview[T]` helpers such as `arena_da_view`, `arena_da_view_slice`, and `arena_da_view_get`
- the semantic layer bridges these wrappers back onto the underlying runtime representations rather than forcing an immediate full runtime rewrite

For example, the stage 1 wrappers now look like this:

```context
error RuntimeError:
    AllocationFailed

def ctx_stage1rt_concat2(lhs: dstr[shape_left], rhs: dstr[shape_right]) -> dstr[shape_result] error[RuntimeError]:
    text: dstr[shape_result] = ctx_stage0_concat2(lhs, rhs) else raise RuntimeError.AllocationFailed
    return text

def ctx_stage1rt_string_view(value: dstr[shape_in], start: i64, end: i64) -> StringView:
    return ctx_stage0_string_view(value, start, end)

def ctx_stage1rt_string_from_view(view: StringView) -> dstr[shape_out]:
    return ctx_stage0_string_view_copy(view)
```

And the arena-backed container helpers now look like this:

```context
def arena_da_append[T](a: Arena&, da: darray[T, shape_in]&, item: T) -> darray[T, shape_out]& error[RuntimeError]:
    # implementation mutates storage/capacity as needed
    return da

def arena_da_append_many[T](a: Arena&, da: darray[T, shape_in]&, new_items: T&, new_items_count: usize) -> darray[T, shape_out]& error[RuntimeError]:
    # implementation grows/copies as needed
    return da

def arena_da_view[T](da: darray[T, shape_in]&, start: usize, end: usize) -> dview[T]:
    # implementation creates a typed runtime-backed non-owning view
    return DynArrayView(null, 0, sizeof(T))
```

So the public runtime-facing layer carries logical shape transitions, while the lower-level implementation stays close to the original low-level representation.

More concretely, the current semantic bridge is intentionally narrow and wrapper-oriented:

- `dstr[shape_id]` is allowed to flow across the runtime boundary as raw `u8&` / `u8&?` string values
- `darray[T, shape_id]` can likewise ride on the existing `DynArray[T]` representation for arena-backed helpers
- `dview[T]` is allowed to flow across the runtime boundary as `DynArrayView`
- `sview[begin, end]` / `StringView` is allowed to flow across the runtime boundary as the raw string-view carrier

Legacy raw list wrappers and carriers have been removed entirely. The older typed `DList` / `DListView` surface is also gone; dynamic collection work now goes through lowercase `darray`, compile-time `view`, and runtime `dview` only.

That means the typechecker can track logical shape states at the wrapper/API level while still reusing the existing low-level runtime layouts internally, and the public wrappers can expose typed `error[...]` returns when allocation or growth may fail.

This keeps the experimental surface high-value while preserving the zero-overhead, low-level runtime core.

## 10. Recommended MVP boundary

If I were choosing the concrete first implementation boundary, it would be:

### MVP

- exact fixed-array typing for `T[N]`
- improved constant-index checking
- syntax support for `darray[T, n]`, `dstr[n]`, `view[T, begin, end]`, and `sview[begin, end]`
- semantic representation for dynamic shape witnesses
- no arithmetic shape expressions yet
- no full generic dependent inference

Status: this MVP boundary has effectively been reached and pushed beyond. The current compiler also includes runtime-backed indexing/slicing, `view` / `dview` / `sview` surface types, string helper lowering, pointer arithmetic, reference comparisons, and CLI-level fixture tests.

String indexing now yields `char`; convert explicitly with `.i64()` when you want a numeric code unit.

At the moment, `char` should be read as the string element/code-unit type produced by `str`, `dstr`, and `sview` indexing. That keeps the current model simple and low-level while still leaving the door open to future encoding-qualified character forms if the language later wants to distinguish ASCII-like byte chars from wider text elements.

### First post-MVP

- known builtins/API table for shape-changing operations
- fresh witness generation on `resize` / `push` / `concat`
- stronger diagnostics

Status: the builtins/API knowledge and fresh witness generation are in place for the currently-supported surface. Better mismatch notes and explanation-oriented diagnostics are still worth improving.

### Later

- arithmetic shape expressions
- richer subrange/view story
- optional proofs for index constraints

This remains the right bucket for future work.

## Best practical recommendation

If I had to turn all this into one concrete engineering instruction, it would be:

> implement exact fixed arrays first, then implement dynamic arrays/strings as low-level runtime structs with lightweight logical shape witnesses, and only later consider symbolic arithmetic on shapes.

That gives you the dependent-style safety you want while keeping the compiler tractable.

## Next collection candidate — dictionary MVP (`dict[dstr, V]` first)

The next natural runtime-backed collection after `darray` / `view` is a dictionary.

The long-term surface goal is:

```context
dict[K, V]
```

where the key type `K` and value type `V` may differ.

That means values like these should be ordinary and well-typed:

```context
dict[dstr, Expr]
dict[u64, dstr]
dict[TokenKind, i32]
```

The recommended MVP, however, is intentionally narrower:

```context
dict[dstr, V]
```

That first slice is enough to support parser-/compiler-style maps, symbol tables, JSON-ish objects, and other string-keyed transient structures without committing the runtime to full heterogeneous key support immediately.

### Surface/runtime split

The language-level type should still be presented as:

```context
dict[K, V]
```

But the first runtime bridge should be specialized to string keys.

Recommended internal split:

- surface semantic type: `dict[K, V]`
- first runtime carrier: `DynDict[V]`
- first bridge: `dict[dstr, V] <-> DynDict[V]`

This matches the current style where:

- `darray[T, shape]` bridges to `DynArray[T]`
- `dview[T]` bridges to `DynArrayView`
- `dstr[shape]` bridges to raw `u8&`
- `sview[begin, end]` bridges to `StringView`

The important idea is that the user-facing type stays general even if the first runtime bridge is intentionally narrow.

### Runtime carrier shape

The first dictionary runtime should use a flat open-addressed table with inline buckets, not node-per-entry allocation.

Conceptually:

```text
DynDict[V] {
    items: any DictBucket[V]&?
    count: usize
    used: usize
    capacity: usize
    arena: any Arena&?
}
```

and:

```text
DictBucket[V] {
    state: u8          # 0 = empty, 1 = full, 2 = tombstone
    hash: u64
    key_data: any u8&?
    key_len: i64
    value: V
}
```

The current runtime uses plain low-level carriers for dynamic arrays and string views, and the dictionary should follow the same rule:

> keep the carrier simple and contiguous; put safety and intent in the type layer and helper semantics.

### Why this layout is the right first one

The dictionary should not allocate one heap node per entry.

Instead:

- the bucket array is contiguous
- probing happens in-place
- insert usually mutates an existing allocation
- allocation only happens when:
    - the dict is first created
    - the bucket array grows / rehashes
    - a new string key must be copied into owned storage

This mirrors the performance model used by modern flat hash maps (including Rust's standard-library style hash tables) far better than a linked-node design.

### Allocation and ownership rule for `dstr` keys

The best first ownership rule is:

> **borrow for lookup, own on successful new insert**

Operationally:

- `get` / `contains` / `remove` take a borrowed key and do not allocate
- `put` hashes and probes using the borrowed key
- if the key already exists, update the existing bucket in place
- if the key is new, copy its bytes into dictionary-owned storage and install the new bucket

For the MVP, the copied string bytes should go into an arena/region when one is attached to the dictionary.

That gives the right cost profile for compiler-style workloads:

- lookups: no allocation
- repeated insertion of existing keys: no allocation
- insertion of a truly new key: one cheap arena copy, not a general-purpose `malloc`

### Arena interaction

The first runtime should support an explicit arena-backed mode.

Recommended rule:

- if `arena != null`, newly-owned string keys are copied into that arena
- if `arena == null`, later work may choose a heap-backed ownership policy, but that is not required for the first slice

For transient parser/compiler maps, arena-backed ownership is the preferred default.

This implies an important resizing rule:

- rehash/grow allocates a fresh bucket array
- if the bucket array itself is arena-backed, old bucket arrays become dead space until arena reset/destroy

That is acceptable for transient workloads and consistent with the current arena model.

To make this predictable, the API should expose `reserve` early.

### Probing strategy

Recommended first implementation:

- open addressing
- power-of-two capacities
- linear probing
- cached per-bucket 64-bit hashes

More sophisticated schemes like Robin Hood probing or SwissTable-style control bytes are reasonable later, but they are not required to make the first slice useful.

Suggested load policy:

- grow when `used` exceeds roughly 75% of `capacity`
- track `used = live + tombstones`
- rebuild if tombstones accumulate badly, even when `count` is lower

This is simple, robust, and more than adequate for the first compiler-integrated version.

### Hash and equality hooks

The generic surface `dict[K, V]` needs one key-specific hash/equality pair per supported key family.

For the first slice, the compiler should treat these as built-in runtime bridge hooks rather than user-defined traits.

For `dict[dstr, V]`, the required key operations are:

```text
dict_hash_dstr(key: dstr) -> u64
dict_eq_dstr(bucket_key_data: any u8&?, bucket_key_len: i64, probe_key: dstr) -> bool
```

Required semantics:

- hash uses the key bytes and length, not pointer identity
- equality checks pointer equality as a fast path, then length equality, then byte equality
- bucket comparison must not call `strlen`; the stored `key_len` is authoritative

The first built-in key-family table should be small and explicit:

- `dstr`
- integer keys (later)
- `bool` (later)
- enum keys without payloads or fully-hashable enums (later)

The MVP should not attempt arbitrary user-defined hash/equality derivation yet.

### Recommended helper surface

To match the current `arena_da_*` / `ctx_stage1rt_*` naming style, the first dict helpers should be arena-oriented.

Recommended helper family:

```text
arena_dict_new[V]
arena_dict_reserve[V]
arena_dict_get[V]
arena_dict_put[V]
arena_dict_contains[V]
arena_dict_remove[V]
arena_dict_clear[V]
```

Suggested first signatures:

```text
arena_dict_new[V](a: Arena&, initial_capacity: usize) -> dict[dstr, V]

arena_dict_reserve[V](a: Arena&, m: dict[dstr, V]&, min_capacity: usize) -> void error[RuntimeError]

arena_dict_get[V](m: dict[dstr, V]&, key: dstr) -> any V&?

arena_dict_put[V](a: Arena&, m: dict[dstr, V]&, key: dstr, value: V) -> any V& error[RuntimeError]

arena_dict_contains[V](m: dict[dstr, V]&, key: dstr) -> bool

arena_dict_remove[V](m: dict[dstr, V]&, key: dstr) -> bool

arena_dict_clear[V](m: dict[dstr, V]&) -> void
```

Notes:

- `get` returns a nullable reference to the stored value slot
- `put` returns a non-null reference to the installed/stored value slot
- `put` and `reserve` are fallible because they may need to grow the bucket array or copy a new key
- `remove` returns whether an entry was present

The signatures intentionally use mutable-reference style container mutation rather than returning a new logical shape-bearing dictionary value. That keeps the first slice consistent with how arena-backed runtime containers already behave operationally.

### Semantic/runtime bridge plan

The bridge should be modeled like the existing container bridges.

Recommended first bridge classification:

```text
dict[dstr, V] <-> DynDict[V]
```

That implies later additions in the semantic bridge layer analogous to:

- `runtimeBridgeDArrayDynArray`
- `runtimeBridgeDArrayViewDynArrayView`
- `runtimeBridgeDStrU8Ref`

with a new family such as:

```text
runtimeBridgeDictDynDict
```

where compatibility requires:

- the source dict key type is exactly `dstr`
- the value type matches the runtime carrier's type argument exactly

This should stay deliberately narrow until the runtime has real key-family hook support for more than strings.

### Recommended MVP boundary

The best first end-to-end dictionary milestone is:

- parse and type-check `dict[K, V]`
- semantically accept only `dict[dstr, V]` for runtime-backed operations at first
- add the `DynDict[V]` runtime carrier as a built-in compiler-known struct
- implement `arena_dict_new`, `reserve`, `get`, `put`, `contains`, and `remove`
- lower dict operations through helper calls rather than inline probing logic in the compiler backend

That keeps the backend simple and matches the current strategy already used for strings and arena-backed dynamic arrays.

### What should remain deferred

The following should be treated as later phases, not part of the first dict landing:

- heap-backed persistent dict ownership mode
- user-defined hash/equality derivation
- mixed runtime key types inside one dictionary instance
- more advanced probing schemes
- dict views / iterators / ordered maps
- shape- or capacity-indexed dict types

### Concise implementation slogan

If `darray` is “contiguous storage plus logical shape”, then the first dict should be:

> a flat open-addressed table plus borrowed lookup / owned-on-insert string keys.

That is the best fit for the current Contextlang runtime style and the compiler-like workloads the language is clearly growing toward.
