# FFI And C Bindings

This document describes the Elisa core FFI surface for calling native C APIs,
modeling platform-specific ABI details, and checking C-compatible layouts.

The goal is:

> make native interop explicit, layout-checked, target-aware, and small enough to audit.

FFI should not become a hidden C++ bridge in disguise. Elisa code should declare
the native symbols it calls, declare the C-facing layouts it needs, and verify
those layouts against the real C compiler before relying on them.

## Capability Map

Ready today:

- raw native function declarations with `extern`
- raw native variable declarations with `extern name: Type`
- opaque external type declarations with `extern TypeName`
- target-gated declarations with `static if ELISA_TARGET_OS_*`
- symbol-name remapping with `@link_name(...)`
- calling-convention annotations with `@c_abi(...)`, `@callconv(...)`, and `@stdcall`
- WinAPI modeling through `@callconv(winapi)` on Windows-only declarations
- C variadic function declarations with `...`
- C default argument promotion for variadic tail arguments
- blocking/progress annotations with `@blocking` and `@nonblocking`
- C-compatible Elisa structs with `layout c`
- layout introspection with `size_of`, `align_of`, and `offset_of`
- C-header struct layout intent with `@c_bind(...)`
- C-header prefix layout intent with `@c_bind_prefix(...)`
- opaque C type metadata with `@c_opaque(...)`
- C compiler backed layout verification with `-emit c-bind-check`

Not automatic yet:

- full C header parsing into Elisa declarations
- automatic C enum/function/struct generation from headers
- automatic translation of C preprocessor macros
- direct checked C union or bitfield modeling
- automatic allocation wrappers for every `@c_opaque` type
- automatic safe wrappers around raw C pointer APIs

Current workflow:

1. Write the Elisa `extern` functions, variables, opaque types, and `layout c` structs you need.
2. Gate platform-specific declarations with target `static if`.
3. Attach `@c_bind`, `@c_bind_prefix`, or `@c_opaque` metadata where the declaration mirrors a C header type.
4. Run `-emit c-bind-check` for layout-checked structs.
5. Wrap raw FFI in small Elisa functions that expose safer project-level APIs.

## Target-Gated FFI

Use target constants to keep platform-only declarations out of the wrong build.
Declarations inside a false `static if` branch are not emitted or made available
for that target.

```elisa
static if ELISA_TARGET_OS_POSIX:
    extern pthread_mutex_lock(mutex: void&) -> int effects[Sync.Lock]

static elif ELISA_TARGET_OS_WINDOWS:
    @callconv(winapi)
    extern EnterCriticalSection(section: mutable Win32CriticalSection&) -> void effects[Sync.Lock]
```

Common target constants include:

- `ELISA_TARGET_OS_MACOS`
- `ELISA_TARGET_OS_LINUX`
- `ELISA_TARGET_OS_WINDOWS`
- `ELISA_TARGET_OS_FREEBSD`
- `ELISA_TARGET_OS_POSIX`
- `target.os`
- `target.features.posix`

Prefer target-gated binding files over runtime `if` checks for APIs that do not
exist on every platform.

## Native Functions

Use `extern` for functions implemented outside the current Elisa module.

```elisa
@c_abi(c)
extern puts(text: i8&?) -> i32

@link_name(avformat_open_input)
@c_abi(c)
extern avformat_open_input_raw(ps: mutable void&?&, url: i8&?, fmt: void&?, options: void&?) -> i32
```

`extern` means “the symbol exists elsewhere.” It does not mean the call is safe.
Keep raw native declarations close to the binding file and expose a smaller
Elisa wrapper API to the rest of the codebase.

## C Variadic Functions

Use `...` at the end of an `extern` parameter list for C varargs such as
`printf`, `fprintf`, and `snprintf`.

```elisa
@c_abi(c)
extern snprintf(buf: mutable u8&?, size: usize, fmt: u8&, ...) -> int effects[Console.Format]
```

Arguments after the declared fixed parameters are lowered with the C default
argument promotions expected by `va_arg`:

- `bool` is passed as C `int`
- `i8`, `u8`, `i16`, and `u16` are passed as C `int`
- `f32` is passed as C `double`
- `i32`, `u32`, `i64`, `u64`, `f64`, pointer/reference values, and opaque
  handles keep their natural ABI representation

Prefer typed C wrapper functions when the format string is fixed or when the
native API has a complicated platform-specific vararg contract. Raw varargs are
available for ordinary C ABI calls, but they are intentionally explicit at the
binding site.

## Native Variables

Use `extern name: Type` for symbols exported as data rather than functions.

```elisa
@link_name(errno)
extern c_errno: int
```

Extern variables should be rare. Prefer accessor functions when the native API
provides them, especially for thread-local or platform-specific globals.

## Symbol Names

Use `@link_name(...)` when the Elisa declaration name should differ from the
linker symbol.

```elisa
@link_name(cos)
@c_abi(c)
extern c_cos(value: f64) -> f64
```

Use this when:

- the C name conflicts with Elisa naming style
- multiple wrapper functions call one C symbol
- a native symbol has a prefix or suffix that should not leak into callers
- the native symbol is compiler-provided, such as an intrinsic

`@intrinsic(...)` is also accepted for LLVM intrinsic-style extern functions and
sets both intrinsic metadata and link-name metadata.

## Calling Conventions

Use `@c_abi(c)` for ordinary C ABI functions.

```elisa
@c_abi(c)
extern strlen(text: i8&?) -> usize
```

Use `@callconv(...)` only when the platform or library explicitly requires a
non-default convention.

Supported spelling includes:

- `c`
- `cdecl`
- `default`
- `fast`
- `fastcall`
- `cold`
- `stdcall`
- `winapi`

`@callconv(...)`, `@c_abi(...)`, and `@stdcall` work on both extern
declarations and Elisa-defined functions. Use the function form for raw native
callbacks whose entry function can be represented directly in Elisa.

`@stdcall` is accepted as shorthand for `@callconv(stdcall)`.

`winapi` maps to the platform-appropriate ABI. On 32-bit x86 Windows it lowers
to stdcall; on modern 64-bit Windows it uses the platform C/Win64 convention.

```elisa
static if ELISA_TARGET_OS_WINDOWS:
    @callconv(winapi)
    extern Sleep(milliseconds: u32) -> void effects[Thread.Sleep]

    @callconv(winapi)
    def thread_entry(arg: void&) -> u32:
        _ = arg
        return 0
```

Prefer `@c_abi(c)` for portable C libraries.

Native test/executable builds also generate a callback lookup shim for
Elisa-defined functions with explicit calling-convention metadata. The std
concurrency runtime exposes thin wrappers around that generated surface so Elisa
code and platform wrappers can request the raw entry pointer by Elisa name:

```elisa
def native_callback_ptr(name: u8&) -> void&?
def native_callback_call_u32_voidp(name: u8&, arg: void&?, fallback: u32) -> u32
def native_callback_call_i32_voidp(name: u8&, arg: void&?, fallback: i32) -> i32
def native_callback_call_usize_voidp(name: u8&, arg: void&?, fallback: usize) -> usize
def native_callback_call_isize_voidp(name: u8&, arg: void&?, fallback: isize) -> isize
def native_callback_spawn_join_u32_voidp(name: u8&, arg: void&?, fallback: u32) -> u32
def native_callback_context_new_u32_voidp(name: u8&, arg: void&?, fallback: u32) -> mutable heap void&?
def native_callback_context_spawn_join_u32_voidp(ctx: mutable heap void&?, fallback: u32) -> u32
def native_callback_context_join_u32_voidp(handle: uintptr, ctx: mutable heap void&?, fallback: u32) -> u32
def native_callback_context_result_u32(ctx: mutable heap void&?, fallback: u32) -> u32
def native_callback_context_free(ctx: mutable heap void&?) -> void
def spawn_native_callback_u32_voidp(name: u8&, arg: void&?, fallback: u32) -> Thread[u32, Joinable]
def join_native_callback_u32(thread: Thread[u32, Joinable], fallback: u32) -> u32

@callconv(c)
def native_thread_entry(arg: void&) -> u32:
    _ = arg
    return 7

def get_entry() -> void&?:
    return native_callback_ptr("native_thread_entry")

def smoke_call() -> u32:
    return native_callback_call_u32_voidp("native_thread_entry", null, 0)

def smoke_i32_call() -> i32:
    return native_callback_call_i32_voidp("native_i32_entry", null, 0)

def smoke_thread_call() -> u32:
    can Thread.Spawn, Thread.Join:
        return native_callback_spawn_join_u32_voidp("native_thread_entry", null, 0)

def smoke_context_call() -> u32:
    can Memory.Allocate, Memory.Release, Thread.Spawn, Thread.Join, Abort.Panic:
        ctx: mutable heap void&? = native_callback_context_new_u32_voidp("native_thread_entry", null, 0)
        assert ctx != null
        result: u32 = native_callback_context_spawn_join_u32_voidp(ctx, 0)
        native_callback_context_free(ctx)
        return result

def smoke_async_thread_call() -> u32:
    can Memory.Allocate, Memory.Release, Thread.Spawn, Thread.Join, Abort.Panic:
        thread: Thread[u32, Joinable] = spawn_native_callback_u32_voidp("native_thread_entry", null, 0)
        return join_native_callback_u32(move thread, 0)
```

The generated direct-call lookup currently covers simple scalar callback shapes
with a `void&` argument and `u32`, `i32`, `usize`, or `isize` return values. The
async thread-entry adapter is currently implemented for `void& -> u32`, which is
the shape needed for the first thread-entry path. The
`native_callback_call_u32_voidp`,
`native_callback_spawn_join_u32_voidp`, and the context helpers exist primarily
for smoke tests and generated adapter checks. The context helpers
model the lifecycle needed by generated trampolines: allocate callback state,
execute it on a real host thread, read the result, and release the state.
The `spawn_native_callback_u32_voidp` and `join_native_callback_u32` wrappers
use the generated typed start/join pair and return a normal Elisa `Thread`
handle for the supported callback shape. Captured closures still need broader
typed trampoline/context generation before this is a full high-level threading
surface.

## Progress And Blocking

Raw extern calls can block even when the type signature does not show it. Mark
that behavior explicitly.

```elisa
@blocking
@c_abi(c)
extern read_from_device(fd: i32, buffer: mutable u8&, len: usize) -> isize

@nonblocking
@c_abi(c)
extern clock_ticks() -> u64
```

`@blocking` adds `Blocking.RawExtern` to the extern function. `@nonblocking` is
a contract/documentation marker and does not add blocking permissions.

Unknown externs are not automatically treated as blocking today, so binding
files should be honest about this.

## Opaque C Types

Use an `extern` type when C owns the concrete layout and Elisa should only pass
references or pointers to it.

```elisa
extern AVFormatContext

@c_abi(c)
extern avformat_close_input(ctx: mutable AVFormatContext&?&) -> void
```

Use `@c_opaque(header, c_type)` when the opaque type corresponds to a concrete
C header type whose size/layout is intentionally hidden from Elisa source.

```elisa
static if ELISA_TARGET_OS_WINDOWS:
    @c_opaque(windows.h, CRITICAL_SECTION)
    extern Win32CriticalSection

    @c_opaque(windows.h, CONDITION_VARIABLE)
    extern Win32ConditionVariable

    @callconv(winapi)
    extern EnterCriticalSection(section: mutable Win32CriticalSection&) -> void effects[Sync.Lock]
```

`@c_opaque` records:

- the C header to include when generating native probes or shims
- the C type expression to use for `sizeof`, `_Alignof`, and pointer signatures

Opaque C types are intentionally incomplete in Elisa. They are suitable for
reference/pointer FFI and platform-gated declarations. Native test/executable
builds automatically generate a small C runtime registry for every active
`@c_opaque` type. The registry exports:

```elisa
extern elisa_c_opaque_alloc(type_name: u8&) -> mutable heap void&? effects[Memory.Allocate]
extern elisa_c_opaque_free(ptr: heap void&) -> void effects[Memory.Release]
extern elisa_c_opaque_size(type_name: u8&) -> usize
extern elisa_c_opaque_align(type_name: u8&) -> usize
```

The generated registry accepts either the Elisa type name or the C type
expression as `type_name`, includes the recorded C headers, and uses the target C
compiler to compute `sizeof` and `_Alignof`. Use this when Elisa needs to own an
SDK-sized opaque value but should not know the field layout.

Native libraries may still provide their own allocator/free APIs, and project
wrappers may still store opaque native pointers as `void&?` when ownership lives
elsewhere.

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

1. analyzes Elisa code and finds `@c_bind` and `@c_bind_prefix` structs
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

For project-local or third-party include directories, prefer setting `CC`,
`CPPFLAGS`, and `CFLAGS` so the C compiler can find the same headers that the
native build uses.

## Type Mapping Guidelines

Recommended C mappings:

| C concept | Elisa spelling |
| --- | --- |
| `int` | `int` when host ABI-sized, otherwise prefer fixed width |
| `int32_t` | `i32` |
| `uint32_t` | `u32` |
| `int64_t` | `i64` |
| `uint64_t` | `u64` |
| `size_t` | `usize` |
| `ssize_t` | `isize` |
| pointer / opaque handle | `void&?` |
| non-null opaque pointer | `void&` |
| nullable typed pointer | `T&?` |
| mutable out pointer | `mutable T&` or `mutable T&?&` depending on shape |
| C string input | `i8&?` or `u8&?` according to API convention |
| callback pointer | `func(...) -> ...`; put `@callconv(...)` on Elisa-defined callback entry functions when ABI-specific |
| SDK-owned opaque struct | `@c_opaque(...) extern TypeName` plus references |

Be conservative with:

- C `bool`, because representation must match the header and platform
- bitfields, because the current checker verifies ordinary field offsets, not
  bit-level packing
- unions, because Elisa does not yet have a direct checked C union binding story
- flexible array members, because tail storage needs a separate ownership model
- varargs, because wrapper functions are usually safer and easier to type-check
- callbacks that require closure state, because those still need an explicit
  trampoline/context adapter

## C Library Pattern

For C libraries, prefer direct FFI plus Elisa wrappers.

```text
library_ffi.elisa      raw externs, c_bind structs, c_opaque types, constants
library_source.elisa   Elisa-owned handles/state over the raw FFI
library.elisa          public project API
library_harness.elisa  semantic/native smoke tests
```

Most code should import the public project API, not the raw declarations.

## C++ Library Pattern

Do not bind C++ ABI directly unless the ABI is intentionally C-compatible. For
external C++ libraries, create or generate a C API facade, then bind that facade
from Elisa.

```text
native/library_bridge.cpp   C++ implementation details
native/library_bridge.h     extern "C" API
library_ffi.elisa           Elisa declarations for the C facade
library.elisa               safe Elisa wrapper
```

This avoids depending on name mangling, STL layout, exceptions, RTTI, and C++ ABI
compatibility across compilers.

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

## Raw Extern Safety

Raw extern calls cross out of Elisa's safety model. Treat them as a narrow,
audited boundary.

Practical rules:

- keep raw declarations in `*_ffi.elisa` or similarly named binding files
- do not spread raw C calls across high-level emulator/library logic
- wrap nullable pointers and integer status codes immediately
- document ownership and lifetime for every pointer returned by C
- prefer opaque `void&?` or `@c_opaque` handles unless field access is required
- use target `static if` instead of declaring unavailable platform APIs globally
- run a native smoke test for every new binding family

## What “Set” Means Right Now

We are set for:

- direct C calls through explicit externs
- platform-specific declarations guarded by target `static if`
- WinAPI-style declarations using `@callconv(winapi)`
- native library integration without a custom C++ bridge for simple C APIs
- C varargs declarations with default argument promotion for the variadic tail
- opaque native handles and opaque C type metadata
- generated alloc/free/size/align helpers for active `@c_opaque` types
- layout-checked C-compatible structs when manually declared in Elisa
- CI-style checks that catch accidental C layout drift

We still need future work for:

- automatic header import / declaration generation
- C enum and macro extraction
- union and bitfield modeling
- richer linker/include flag plumbing for `c-bind-check`
- generated safe wrappers around common C patterns
- direct closure/callback trampoline generation for APIs that need captured
  callback state or per-call adapter functions

The important thing is that the foundation now fails closed: once a struct is
declared with `@c_bind`, layout mismatches are detected by the same C compiler
that will build the native dependency, and once a platform API is behind target
`static if`, it cannot leak into the wrong target build.
