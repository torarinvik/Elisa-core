# C header emit contract (`-emit header`)

This document captures the current implemented behavior of C header emission.

## Command

```sh
go run ./src -emit header path/to/module.elisa
go run ./src -emit header -o module.h path/to/module.elisa
```

## Top-level header shape

Current generated header includes:

- include guard derived from source filename
- `#include <stdint.h>`
- `extern "C"` block for C++ consumers
- forward typedef declarations for public aggregate types
- aggregate struct definitions
- exported global declarations
- exported function prototypes

## Export source inputs

Header emission is driven by semantic export metadata:

- exported types
- exported functions and their parameter/return types
- exported globals

## Aggregate dependency ordering

Public aggregate structs are topologically ordered by strong by-value dependencies. Recursive by-value dependency cycles fail header generation with an explicit error.

## Type support and limits

Supported C-declarable categories:

- builtin scalar types
- references (pointer form)
- fixed-size arrays with compile-time size
- public exported struct-like aggregates

Unsupported or unresolved categories fail generation with explicit diagnostics.

## Builtin C type mapping

Current builtin mapping includes:

- `i8` -> `int8_t`
- `u8` -> `uint8_t`
- `i16` -> `int16_t`
- `u16` -> `uint16_t`
- `i32` -> `int32_t`
- `u32` -> `uint32_t`
- `i64` -> `int64_t`
- `u64` -> `uint64_t`
- `f32` -> `float`
- `f64` -> `double`
- `int` and `isize` -> `intptr_t`
- `usize` and `uintptr` -> `uintptr_t`
- `void` -> `void`
- `char` -> `int64_t` (current implementation mapping)

## Alignment attributes

When requested alignment metadata exists on emitted struct/global types, header declarations include:

```c
__attribute__((aligned(N)))
```

## Exported globals and functions

- exported globals are emitted as `extern` declarations
- exported functions are emitted as C prototypes
- zero-parameter functions emit `void` parameter lists

## Elisa example

```elisa
struct Vec2i:
    x: i32
    y: i32

export type Vec2i as Vec2i

def vec2i_add_impl(left: Vec2i, right: Vec2i) -> Vec2i:
    return Vec2i(x: left.x + right.x, y: left.y + right.y)

export func vec2i_add(left: Vec2i, right: Vec2i) -> Vec2i = vec2i_add_impl
```

Generated header includes a public `Vec2i` struct and `vec2i_add` prototype.

## Related docs

- `docs/07-export-and-c-abi.md`
- `docs/36-c-archive-output-surface.md`
- `docs/49-c-bind-layout-check-and-json-manifest-surface.md`
