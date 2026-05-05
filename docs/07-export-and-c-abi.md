# Export and C ABI Mini-Spec

This document proposes an explicit `export` feature for Elisa core so concrete functions and C-compatible struct layouts can be exposed to C with stable names and predictable calling conventions.

The main goal is:

> let Elisa core code compile into object files that can be linked into C programs **without** relying on mangled internal specialization names as the public interface.

## Core principle

`extern` means “this thing exists outside the current module”.

`export` should mean:

> “this thing is intentionally part of the module’s stable C ABI surface”.

That means `export` should be:

- explicit
- concrete
- wrapper-oriented
- strict about ABI compatibility

## The first ABI truth: functions and globals have symbols, types do not

At the linker level:

- functions have symbol names
- globals have symbol names
- types do **not** have symbol names

So the design should distinguish between:

### `export type`

This means:

- materialize a concrete instantiation
- validate that it is C-ABI-compatible
- give it a stable header/debug name

It does **not** mean “emit a linker symbol for the type”.

### `export func`

This means:

- materialize a concrete callable instantiation
- generate a stable external symbol name
- emit a C-compatible wrapper if needed

### `export global`

This means:

- emit a stable external symbol for a concrete global value
- validate that its type is C-ABI-compatible

## Design recommendation

Do **not** treat `export` as “rename the compiler’s internal mangled symbol”.

Instead, treat it as:

> generate a thin stable C-facing ABI wrapper around the compiler’s internal implementation symbol.

That preserves freedom for the compiler to keep using mangled/internal names for:

- generic specialization
- backend uniqueness
- future optimization changes
- internal helper lowering

and still provide a clean public symbol such as `vec2i_add`.

## Proposed surface syntax

### Exporting a concrete type instantiation

```elisa
struct Vec[T]:
    x: T
    y: T

export type Vec[i32] as Vec2i
```

Meaning:

- instantiate `Vec[T]` with `i32`
- verify the concrete layout is C-compatible
- expose that instantiated layout as `Vec2i` in generated C headers

### Exporting a concrete function with an explicit ABI-facing surface

```elisa
def vec_add[T](left: Vec[T], right: Vec[T]) -> Vec[T]:
    return Vec[T](left.x + right.x, left.y + right.y)

export func vec2i_add(left: Vec2i, right: Vec2i) -> Vec2i = vec_add[i32]
```

Meaning:

- define the public ABI in the exported declaration itself
- instantiate `vec_add[T]` with `i32`
- generate a stable public symbol named `vec2i_add`
- validate that the explicit exported signature matches the concrete target exactly

This explicit form should be the primary surface syntax because it makes the ABI-facing contract obvious in the source.

### Optional shorthand sugar

The earlier alias-style shorthand can still be treated as future sugar over the explicit form:

```elisa
export func vec_add[i32] as vec2i_add
```

Conceptually, that is just shorthand for:

```elisa
export func vec2i_add(left: Vec2i, right: Vec2i) -> Vec2i = vec_add[i32]
```

### Exporting a monomorphic function directly

For non-generic functions, a shorthand attribute form is also reasonable:

```elisa
@export("vec2i_len")
def vec2i_len(v: Vec2i) -> i32:
    return v.x + v.y
```

This is optional sugar; the core feature should still be the explicit `export func public_name(...) -> Return = target` form.

### Exporting a global

```elisa
global MAGIC: i32 = 1337

export global MAGIC as ctx_magic
```

This should only be allowed for concrete C-ABI-compatible types.

## Concrete example

```elisa
struct Vec[T]:
    x: T
    y: T

export type Vec[i32] as Vec2i

def vec_add[T](left: Vec[T], right: Vec[T]) -> Vec[T]:
    return Vec[T](left.x + right.x, left.y + right.y)

def dot[T](left: Vec[T], right: Vec[T]) -> T:
    return left.x * right.x + left.y * right.y

export func vec2i_add(left: Vec2i, right: Vec2i) -> Vec2i = vec_add[i32]
export func vec2i_dot(left: Vec2i, right: Vec2i) -> i32 = dot[i32]
```

Generated C header sketch:

```c
typedef struct Vec2i {
    int32_t x;
    int32_t y;
} Vec2i;

Vec2i vec2i_add(Vec2i left, Vec2i right);
int32_t vec2i_dot(Vec2i left, Vec2i right);
```

## What should be allowed at the C ABI boundary

The compiler should implement a strict predicate like:

$$
\mathrm{isCABICompatible}(T)
$$

and reject `export` declarations whose concrete ABI surface is not compatible.

### Safe initial subset

These are good MVP candidates:

- fixed-width integers: `i8`, `i16`, `i32`, `i64`, `u8`, `u16`, `u32`, `u64`
- `char` (current byte/code-unit scalar)
- pointers / references lowered as raw pointers when the lifetime/ownership contract is intentionally C-facing rather than a Elisa core-only proof such as `stack T&`
- structs whose fields are all themselves C-ABI-compatible
- opaque handle-like pointers once explicit opaque/exported types exist

For exported **function boundaries**, be stricter than the general field rule:

- top-level fixed arrays should be rejected as parameters and returns
- if array-shaped data must cross the ABI, wrap it in an explicit C-facing struct

### Allow later or with caution

- `bool` — possible, but worth pinning to a documented C representation
- `usize` / `isize` / `uintptr` — useful, but platform-dependent in generated headers
- packed/aligned layout modifiers — valid in principle, but header generation and layout validation must agree exactly

### Reject initially

These should **not** be exported directly in the first version:

- `darray[...]`
- `cstr[...]`
- `view[...]`
- `sview[...]`
- shape-typed logical container wrappers in general

Reason:

These are language-level logical/container abstractions, not simple C ABI contracts.

The same guideline applies to the internal CamelCase runtime carriers such as `DynArray`, `DynArrayView`, and `StringView`: they are implementation details, not the preferred public ABI contract.

If code needs to cross the ABI boundary with container-like data, it should do so through explicit C-facing bridge types such as:

```elisa
struct BytesView:
    data: u8&
    len: usize
```

## Generic export rule

Only **concrete instantiations** should be exportable.

This is good:

```elisa
export type Vec[i32] as Vec2i
export func vec2i_add(left: Vec2i, right: Vec2i) -> Vec2i = vec_add[i32]
```

This should be rejected:

```elisa
export type Vec[T] as Vec
export func vec_add[T] as vec_add
```

because C has no type-parameterized ABI surface.

The same rule now applies to pointer-qualifier generics.

This is fine:

```elisa
struct Handle[refstorage store, refstate state]:
    ptr: store u8&[state]

export type Handle[heap, &] as HeapHandle
```

But exporting an unresolved qualifier parameter is still invalid:

```elisa
export type Handle[store, state] as HandleGeneric   # reject
```

because the C ABI surface must see a fully concrete pointer representation, not an abstract storage/state family.

## ABI wrapper model

The compiler’s internal implementation may still use mangled names such as:

```text
vec_add__i32__impl42
```

but the exported object file should expose stable wrapper symbols such as:

```text
vec2i_add
```

That wrapper may be as small as a direct tail call or forwarding stub.

This keeps the public ABI stable even if the internal specialization strategy changes later.

## Header generation

The compiler-side export table can now also drive a generated C header.

Current workflow:

```text
elisacore -emit obj -o math2d.o math2d.elisa
elisacore -emit header -o math2d.h math2d.elisa
```

The generated header should include:

- typedefs for exported concrete struct types
- prototypes for exported functions
- declarations for exported globals
- standard integer includes when needed (for example `<stdint.h>`)

## Ownership and lifetime honesty

The first version should be conservative.

If an exported function returns pointers or accepts pointer-owning contracts, the ABI surface must be explicit about ownership. C will not preserve Elisa core-level invariants automatically.

Practical recommendation for MVP:

- prefer exported values and POD-style structs
- avoid exposing arena-backed, `stack T&`-style borrowed, scratch-backed, or otherwise lifetime-sensitive pointer returns directly
- push richer ownership semantics to a later phase once the ABI annotations story exists

## Validation rule for exported types

For an exported concrete struct type, the compiler should validate at least:

- field order is fixed and known
- field alignments are known and consistent with chosen ABI rules
- field types are recursively C-ABI-compatible
- generic parameters are fully substituted before export

That includes fully substituting any `refstorage` and `refstate` parameters that appear inside field types such as `store T&[state]`.

For example:

```elisa
struct Pair[T]:
    left: T
    right: T

export type Pair[i64] as Pair64
```

should succeed.

But this should fail:

```elisa
struct Bad:
    text: cstr[row]

export type Bad as Bad
```

because `cstr[row]` is not a stable C ABI field type in the recommended first version.

## MVP proposal

The first useful version of `export` should support:

### Syntax

- `export type ConcreteType as Name`
- `export func public_name(params...) -> Return = ConcreteFunction`
- optionally `export global Name as symbol_name`

### Semantics

- concrete only
- wrapper-based symbol emission
- strict C ABI compatibility validation
- stable exported names independent of internal mangling

### Output

- object file emission through the existing object pipeline
- later header generation using the same semantic export table

## End-to-end “does it hold water?” validation plan

The feature should be considered real only when the repository can do the following:

1. compile a `.elisa` file with exported concrete types/functions to an object file
2. emit a matching C header
3. compile a small C file that includes the generated header
4. link the C file against the produced object file
5. run the resulting executable and verify the result

### Suggested first validation fixture

Elisa core module:

```elisa
struct Vec[T]:
    x: T
    y: T

export type Vec[i32] as Vec2i

def vec_add[T](left: Vec[T], right: Vec[T]) -> Vec[T]:
    return Vec[T](left.x + right.x, left.y + right.y)

export func vec2i_add(left: Vec2i, right: Vec2i) -> Vec2i = vec_add[i32]
```

C harness sketch:

```c
#include "vec2i.h"
#include <assert.h>

int main(void) {
    Vec2i a = {1, 2};
    Vec2i b = {3, 4};
    Vec2i c = vec2i_add(a, b);
    assert(c.x == 4);
    assert(c.y == 6);
    return 0;
}
```

That is the right first test because it exercises:

- concrete generic type export
- concrete generic function export
- by-value struct ABI
- symbol naming
- actual linking from C

## Recommended rollout order

### Phase 1 — export metadata only

- parse and represent `export type` / `export func`
- validate concrete instantiations
- reject non-C-compatible exports clearly

### Phase 2 — object-level symbol wrappers

- generate exported wrapper functions/globals
- keep internal mangled implementation names untouched

### Phase 3 — header emission

- emit stable C headers for exported ABI surface

### Phase 4 — end-to-end C harness tests

- add repository-level tests that compile C code against generated outputs

## Honest recommendation

This feature is worth doing, but only if it stays disciplined.

The best rule is:

> `export` should expose a deliberately small, concrete, C-ABI-stable surface — not attempt to make every rich Elisa core type automatically appear natural in C.

That means explicit wrappers are a feature, not a failure.

Done this way, `export` becomes a very strong capability:

- generics stay ergonomic inside Elisa core
- C sees only stable concrete ABI shapes
- mangling stops leaking into the public interface
- object-file emission already in the compiler becomes immediately more useful