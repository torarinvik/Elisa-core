# FFI And C Bindings

This document describes the practical Elisa core FFI surface for calling native C
libraries and checking C-compatible struct layouts.

The goal is:

> make native interop explicit, layout-checked, and small enough to audit.

FFI should not become a hidden C++ bridge in disguise. Elisa code should declare
the native symbols it calls, declare the C-facing layouts it needs, and verify
those layouts against the real C compiler before relying on them.

## Current Status

Ready today:

- raw native function declarations with `extern`
- symbol and calling-convention annotations such as `@link_name`, `@c_abi`, and `@callconv`
- C-compatible Elisa structs with `layout c`
- layout introspection with `size_of`, `align_of`, and `offset_of`
- C-header layout intent with `@c_bind`
- C-header prefix/field-subset layout intent with `@c_bind_prefix`
- C compiler backed layout verification with `-emit c-bind-check`

Not automatic yet:

- full C header parsing into Elisa declarations
- automatic C enum/function/struct generation from headers
- automatic translation of C preprocessor macros
- automatic safe wrappers around raw C pointer APIs

So the current workflow is semi-automatic:

1. Write the Elisa `extern` functions and `layout c` structs you need.
2. Attach `@c_bind` to structs that mirror C header types.
3. Run `-emit c-bind-check` to prove size, alignment, and offsets match C.
4. Wrap the raw FFI in small Elisa functions that expose safer project-level APIs.

## Native Functions

Use `extern` for functions implemented outside the current Elisa module.

```elisa
@c_abi(c)
extern puts(text: i8&?) -> i32

@link_name(avformat_open_input)
@c_abi(c)
extern avformat_open_input(ps: mutable void&?&, url: i8&?, fmt: void&?, options: void&?) -> i32
```

`extern` means “the symbol exists elsewhere.” It does not mean the call is safe.
Keep raw native declarations close to the binding file and expose a smaller
Elisa wrapper API to the rest of the codebase.

## Symbol Names

Use `@link_name(...)` when the Elisa function name should differ from the linker
symbol.

```elisa
@link_name(cos)
@c_abi(c)
extern c_cos(value: f64) -> f64
```

Use this when:

- the C name conflicts with Elisa naming style
- multiple overload-like Elisa wrappers call one C symbol
- a library symbol has a prefix or suffix that should not leak into callers

## Calling Conventions

Use `@c_abi(c)` for ordinary C ABI functions.

```elisa
@c_abi(c)
extern strlen(text: i8&?) -> usize
```

Use `@callconv(...)` only when the platform or library explicitly requires a
non-default convention.

Supported call-convention spelling includes:

- `c`
- `cdecl`
- `default`
- `fast`
- `fastcall`
- `cold`
- `stdcall`
- `winapi`

Prefer `@c_abi(c)` for portable C libraries.

## Raw Extern Safety

Raw extern calls cross out of Elisa's safety model. Treat them as a narrow,
audited boundary.

Practical rules:

- keep raw declarations in `*_ffi.elisa` or similarly named binding files
- do not spread raw C calls across high-level emulator/library logic
- wrap nullable pointers and integer status codes immediately
- document ownership and lifetime for every pointer returned by C
- prefer opaque `void&?` handles unless the code truly needs field access

If an extern can block, mark that progress behavior explicitly.

```elisa
@blocking
@c_abi(c)
extern read_from_device(fd: i32, buffer: mutable u8&, len: usize) -> isize

@nonblocking
@c_abi(c)
extern clock_ticks() -> u64
```

Unknown externs are not automatically treated as blocking today, so binding
files should be honest about this.

## C-Compatible Structs

Use `layout c` when an Elisa struct is meant to follow C ABI layout rules.

```elisa
struct PacketHeader layout c:
    tag: u32
    size: usize
```

Use layout introspection when local code needs compile-time layout facts.

```elisa
static assert size_of(PacketHeader) == 16
static assert align_of(PacketHeader) == 8
static assert offset_of(PacketHeader, size) == 8
```

Use `@fixed_layout` when the declared field order is intentional and should not
trigger field-reordering padding advice.

```elisa
@fixed_layout
struct PacketHeader layout c:
    tag: u8
    size: u32
```

## Header-Checked Struct Bindings

Use `@c_bind(header, c_type)` on a `layout c` struct to state that the Elisa
layout is intended to mirror a C header type.

```elisa
@c_bind("stddef.h", "struct Header")
struct Header layout c:
    tag: u8
    count: u32
    total: usize
```

The annotation records:

- the header to include in the C probe
- the C type expression to use with `sizeof`, `_Alignof`, and `offsetof`

`@c_bind` requires `layout c`. It rejects generic structs because C layout
verification must target one concrete ABI shape.

Use `@c_bind_prefix(header, c_type)` when Elisa intentionally declares only the
public prefix needed for field access. This is useful for large C library structs
where a few public fields are needed, but modeling the whole type would be noisy
and version-sensitive.

```elisa
@c_bind_prefix("libavformat/avformat.h", "AVFormatContext")
struct AVFormatContextPublicPrefix layout c:
    av_class: void&?
    iformat: void&?
    oformat: void&?
    priv_data: void&?
    pb: void&?
    ctx_flags: s32
    nb_streams: u32
```

Prefix checks compare every declared Elisa field offset against `offsetof` on
the full C type. They intentionally do not require Elisa's prefix size/alignment
to equal the full C struct size/alignment.

## Running Layout Verification

Run:

```sh
go run ./src -emit c-bind-check path/to/bindings.elisa
```

from the `compiler` directory, or run the built `elisacore` binary with the same
arguments.

The checker:

1. analyzes Elisa code and finds `@c_bind` structs
2. generates a temporary C probe
3. includes the requested headers
4. prints `sizeof`, `_Alignof`, and `offsetof` values from C
5. compares them against Elisa's `layout c` model
6. exits non-zero if any size, alignment, or field offset differs

Example success:

```text
c-bind-check: Header matches struct Header from /tmp/fixture.h (size=16 align=8 fields=3)
```

Example failure:

```text
error: C binding layout check failed:
Header.count: offset mismatch Elisa=4 C=0
Header.tag: offset mismatch Elisa=0 C=4
```

The checker uses `CC` when set, otherwise `cc`. It also appends flags from
`CPPFLAGS` and `CFLAGS`, which is useful for package-manager headers.

```sh
CPPFLAGS="$(pkg-config --cflags libavformat libavcodec libavutil)" \
CC=clang \
go run ./src -emit c-bind-check path/to/bindings.elisa
```

## Header Paths

Header strings are emitted into the generated C probe as includes.

```elisa
@c_bind("stddef.h", "struct Header")
```

becomes:

```c
#include <stddef.h>
```

Absolute paths become quoted includes.

```elisa
@c_bind("/tmp/vendor/header.h", "struct Header")
```

becomes:

```c
#include "/tmp/vendor/header.h"
```

For project-local or third-party include directories, prefer setting `CC` or the
compiler environment so the C compiler can find the same headers that the native
build uses.

## FFmpeg-Style Bindings

For libraries like FFmpeg, prefer opaque handles unless field access is truly
needed.

Good first binding shape:

```elisa
@c_abi(c)
extern avformat_open_input(ps: mutable void&?&, url: i8&?, fmt: void&?, options: void&?) -> i32

@c_abi(c)
extern avformat_find_stream_info(ctx: void&?, options: void&?) -> i32

@c_abi(c)
extern avformat_close_input(ps: mutable void&?&) -> void
```

Then wrap those calls in Elisa-owned handle tables and status-code conversion.

Only bind public C struct fields when:

- the field is documented as public ABI/API
- the field exists in installed headers on target platforms
- `@c_bind` and `-emit c-bind-check` pass
- or `@c_bind_prefix` passes when only a public prefix is declared
- the wrapper still handles version drift gracefully

For FFmpeg specifically, many structs are large and version-sensitive. Prefer
public accessor functions and opaque pointers where possible.

## Recommended Binding File Pattern

Use a layered split:

```text
library_ffi.elisa      raw externs, c_bind structs, constants
library_source.elisa   Elisa-owned handles/state over the raw FFI
library.elisa          public project API
library_harness.elisa  semantic/native smoke tests
```

This keeps native risk contained. Most code should import the project API, not
the raw FFI declarations.

## Type Mapping Guidelines

Recommended C mappings:

| C concept | Elisa spelling |
| --- | --- |
| `int32_t` | `i32` |
| `uint32_t` | `u32` |
| `int64_t` | `i64` |
| `uint64_t` | `u64` |
| `size_t` | `usize` |
| pointer / opaque handle | `void&?` |
| nullable typed pointer | `T&?` |
| mutable out pointer | `mutable T&` or `mutable T&?&` depending on shape |
| C string input | `i8&?` |

Be conservative with:

- C `bool`, because representation must match the header and platform
- bitfields, because the current checker verifies ordinary field offsets, not
bit-level packing
- unions, because Elisa does not yet have a direct checked C union binding story
- flexible array members, because tail storage needs a separate ownership model

## What “Set” Means Right Now

We are set for:

- direct C calls through explicit externs
- native library integration without a custom C++ bridge for simple function APIs
- layout-checked C-compatible structs when manually declared in Elisa
- CI-style checks that catch accidental C layout drift

We still need future work for:

- automatic header import / declaration generation
- C enum and macro extraction
- union and bitfield modeling
- richer linker/include flag plumbing for `c-bind-check`
- generated safe wrappers around common C patterns

The important thing is that the foundation now fails closed: once a struct is
declared with `@c_bind`, layout mismatches are detected by the same C compiler
that will build the native dependency.
