# Annotations and compile-time hints

This note is a practical reference for the annotation surface the current
compiler accepts today.

Unlike some older notes in this folder, this file is not a proposal. It is a
reference for implemented annotations, validation rules, and the main effects
they have on semantic analysis or code generation.

## Struct layout annotations

Struct declarations may carry explicit alignment hints.

```context
@align(32)
struct Vec4:
    x: f32
    y: f32
    z: f32
    w: f32

@cacheline_aligned
struct Counter:
    value: i64
```

Current rules:

- `@align(N)` requests an explicit byte alignment on the struct layout
- `N` must be a positive power of two
- `@cacheline_aligned` is the shorthand fixed alignment form currently used for 64-byte cacheline alignment
- `@cacheline_aligned` does not take arguments
- the alignment metadata carries through to LLVM allocas, globals, and generated C headers

## Packed-layout annotations

Packed enums accept layout-selection annotations in addition to the ordinary
`packed enum` syntax.

```context
@packed_abi(dense_fixed)
@packed_prefix(common_only)
packed enum Expr:
    common:
        span: int
    Lit(value: int)

@packed_profile(build_heavy)
packed enum Pair:
    common:
        span: int
    Both(left: int, right: int)
```

Current rules:

- `@packed_abi(...)` pins a specific packed lowering ABI
- `@packed_prefix(...)` selects a prefix-word layout policy such as `common_only`
- `@packed_profile(...)` applies a named packed-layout profile such as `build_heavy`
- explicit packed-ABI overrides still win over profile defaults when both are present
- these annotations apply to packed enums and related packed/tree lowering surfaces rather than ordinary structs

## Function codegen annotations

Functions may carry explicit backend and optimizer hints.

```context
@inline(always)
def helper(value: int) -> int:
    return value + 1

@inline(never)
def cold_path() -> int:
    return 0

@norecurse
@hot
def fast_path(value: int) -> int:
    return value

@cold
def slow_path(value: int) -> int:
    return value
```

Current rules:

- `@inline(always)` and `@inline(never)` are the current supported inline modes
- unsupported `@inline(...)` modes are rejected during semantic analysis
- `@norecurse` takes no arguments and lowers to the corresponding LLVM function attribute
- `@hot` and `@cold` take no arguments and are mutually exclusive
- temperature and recursion annotations propagate through specialization and exported wrapper lowering where applicable

## Guard annotations

Functions that act as proof-producing predicates may carry guard annotations.

```context
@guard_nonnull(box)
def has_box(box: heap Box&?) -> bool:
    return box != null

@guard_variant(node, Expr.Int)
def is_int(node: Expr) -> bool:
    return node is Expr.Int
```

Current rules:

- `@guard_nonnull(name)` marks a boolean helper as proving that the named argument is non-null on the true branch
- `@guard_variant(name, Enum.Variant)` marks a boolean helper as proving a packed-variant refinement on the true branch
- these annotations affect caller-side CFG facts and refinement, not just the annotated function body itself
- the named guard target must still line up with the function's declared parameters and return shape

## Branch hints

Branch-probability hints are statement syntax rather than `@` annotations, but
they live in the same compile-time-hint family.

```context
if likely value:
    return 1

while unlikely value:
    return 0
```

Current rules:

- `likely` and `unlikely` are contextual statement hints for `if` and `while`
- the raw condition expression remains the same expression the compiler would analyze without the hint
- current LLVM lowering turns these into branch-weight metadata rather than a different source-level control-flow rule

## Practical caveats

- annotations stack, so a declaration may carry more than one annotation when the combination is valid
- several annotations are intentionally target-specific and may only make sense on one declaration family
- when in doubt, treat annotations as compile-time metadata that refine layout, code generation, or proof flow; they do not create runtime objects on their own