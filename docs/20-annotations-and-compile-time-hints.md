# Annotations and compile-time hints

This note is a practical reference for the annotation surface the current
compiler accepts today.

Unlike some older notes in this folder, this file is not a proposal. It is a
reference for implemented annotations, validation rules, and the main effects
they have on semantic analysis or code generation.

## Struct layout annotations

Struct declarations may carry explicit alignment hints.

```elisa
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

```elisa
@packed_profile(build_heavy)
packed enum Pair:
    common:
        span: int
    Both(left: int, right: int)
```

Current rules:

- `@packed_profile(...)` applies a named packed-layout profile such as `build_heavy`
- supported profiles currently include `canonical`, `retained_reads`, and `build_heavy`
- removed legacy annotations `@packed_abi(...)` and `@packed_prefix(...)` should be migrated to one of the supported `@packed_profile(...)` variants
- these annotations apply to packed enums and related packed/tree lowering surfaces rather than ordinary structs

Packed enum common fields may also choose their storage placement explicitly.

```elisa
@packed_profile(retained_reads)
packed enum Expr:
    common:
        @storage(side_table)
        span: int
        @storage(inline)
        kind: int
    Lit(value: int)
    End
```

Current rules:

- `@storage(inline)` keeps the common field in the inline packed common-field path
- `@storage(side_table)` places the common field in the packed side-table path
- `@storage(...)` is currently supported on `packed enum` `common:` fields
- ordinary struct fields and non-common enum payload fields do not accept `@storage(...)`
- prefer the canonical spellings `inline` and `side_table`; other normalized spellings are compatibility-only
- side-tabled common fields still require a packed profile/backend path that supports side-tabled common storage

## Function codegen annotations

Functions may carry explicit backend and optimizer hints.

```elisa
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
- `@hot` is the strict fast-path contract: it rejects allocation/free and raw-pointer/indirect-dispatch effects transitively
- `@hot(alloc)` keeps hot codegen metadata but explicitly opts into allocation-bearing hot code
- `@cold` takes no arguments, and `@hot`/`@cold` are mutually exclusive
- temperature and recursion annotations propagate through specialization and exported wrapper lowering where applicable

## Performance acknowledgements

Strict performance lints are warnings by default and become errors under
`-Wperf`. Intentional low-level exceptions should use a local trusted permission
block rather than disabling the flag globally.

```elisa
for item in work:
    trusted Perf.HotLoop:
        # deliberately bounded/windowed low-level protocol
        task_group_wait_all(&window)
```

Current rules:

- `trusted Perf.HotLoop:` suppresses loop-based `-Wperf` diagnostics only inside
  that trusted block
- the block is local and greppable; it does not infer a `Perf.HotLoop`
  requirement for the enclosing function
- use it only for named protocols, stress tests, benchmarks, or bounded-window
  concurrency policies whose performance exception is intentional
- prefer satisfying the lint by batching, sharding, collecting handles, or moving
  waits to the batch boundary before adding this acknowledgement

## Guard annotations

Functions that act as proof-producing predicates may carry guard annotations.

```elisa
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

## ABI and extern annotations

Extern declarations and ABI-facing functions accept explicit annotation metadata.

```elisa
@link_name(native_puts)
extern puts(text: u8&) -> int

@intrinsic(llvm.ctpop.i64)
extern popcount64(value: u64) -> u64

@callconv(winapi)
extern winapi(value: i32) -> i32

@c_abi(c)
def c_callback(arg: void&) -> u32:
    _ = arg
    return 0

@stdcall
def stdcall_callback(arg: void&) -> u32:
    _ = arg
    return 0

@c_opaque(windows.h, CRITICAL_SECTION)
extern Win32CriticalSection
```

Current rules:

- `@link_name(name)` on extern function or extern var expects one non-empty symbol name
- `@intrinsic(name)` on extern function expects one non-empty LLVM intrinsic name that starts with `llvm.`
- `@callconv(name)` and `@c_abi(name)` expect exactly one calling-convention name
- `@stdcall` takes no arguments and sets the function calling convention to `stdcall`
- unsupported calling conventions are rejected (for example `vectorcall`)
- `@c_opaque(header, c_type)` on extern type expects exactly two non-empty arguments

Extern progress classification annotations are also part of the extern annotation surface:

```elisa
@blocking
extern waitpid(pid: i32) -> i32

@nonblocking
extern monotonic_time() -> i64
```

Current rules:

- `@blocking` and `@nonblocking` take no arguments
- an extern cannot be both `@blocking` and `@nonblocking`
- `@blocking` adds `Blocking.RawExtern` permission to the extern signature
- `@nonblocking` documents a non-blocking contract and does not add `Blocking.*` permissions

## Main-thread scheduling annotation

Functions can be marked as main-thread entry points for progress-safety checks.

```elisa
extern wait_for_worker() -> void can[Blocking.Wait]

@main_thread
def on_click() -> void:
    wait_for_worker()
```

Current rules:

- `@main_thread` takes no arguments
- reachable `Blocking.*` paths from a `@main_thread` function are progress errors
- intentional blocking on main thread should be wrapped in a local trusted block such as `trusted Unsafe.BlockMain:`

For full progress-safety behavior, see [25-progress-safety.md](/Users/torarinvikbjarko/Documents/Coding%20Projects/Go%20projects/Elisa-core/docs/25-progress-safety.md).

## Extension-method, visibility, and constructor annotations

Current surface includes method-style extension hooks, internal visibility markers, and type constructor hooks.

```elisa
struct IndexMap[K, T]:
    marker: u8


extern count[K, T](items: IndexMap[K, T]&) -> usize

@internal
def hidden_identity(value: i64) -> i64:
    return value

struct Span:
    start: i64
    finish: i64

@init(Span)
def make_span(start: i64, finish: i64 = 0) -> Span:
    return Span{start, finish}

def build(start: i64) -> Span:
    return Span(start:)
```

Current rules:

- `@method` takes no arguments and requires at least one receiver parameter
- `@method` works on both `def` and `extern` functions and enables receiver-call syntax
- `@internal` takes no arguments and marks internal-only surface
- `@init` accepts zero arguments or one return-type name
- `@init` function must be non-generic, non-variadic, and return a concrete struct type
- when `@init(TypeName)` is used, the named type must exist, be a concrete struct, and match the function return type
- paren constructor syntax `TypeName(...)` lowers through the registered `@init` hook

## Test and benchmark annotations

The language surface includes runner-facing function markers for tests, benches,
fixtures, and skip controls.

```elisa
@test
def alpha_case() -> void:
    pass

@bench
def hot_loop() -> void:
    pass

@fixture
def shared_seed() -> int:
    return 7

@skip(todo)
@test
def beta_case() -> void:
    pass
```

Current rules:

- `@test`, `@bench`, and `@fixture` mark functions for list and runner surfaces (`-emit tests`, `-emit benches`, `-emit fixtures`, `-emit test`, `-emit test-runner`)
- `@test` and `@bench` functions are validated as runner entry functions and must return `void`
- skip markers such as `@skip(...)` or `@ignore` are accepted and used by runner surfaces to exclude selected cases

## Boundary pointer annotations

Boundary pointer annotations mark function parameters that intentionally carry
address-space pointers across unsafe ABI edges.

```elisa
@boundary_pointer_args(mutex)
def posix_pthread_mutex_lock(mutex: GuestVAddr[void]) -> int:
    trusted Unsafe.GuestHostPointerCast:
        slot: mutable void&?&? = mutex.cast[mutable void&?&?]
        _ = slot
    return 0
```

Current rules:

- `@boundary_pointer_args(...)` expects at least one parameter name
- each listed name must refer to an existing parameter
- each listed parameter must use a typed address-space pointer carrier, such as `GuestVAddr[T]`, `HostPtr[T]`, or `NativeMappedGuestPtr[T]`
- host reference shapes (for example `mutable void&?&?`) and raw integers (`uintptr`) are rejected at the boundary marker
- converting a boundary carrier to host-reference form still requires explicit unsafe grant, for example `trusted Unsafe.GuestHostPointerCast:`

## Async-entry and segment-owner annotations

Segment-owner safety and async-entry validation are annotation-driven.

```elisa
@async_entry
@segment_establishing
@reentrant_safe
def alarm_handler() -> void:
    return

@segment_transition(guest)
extern load_guest() -> void can[Unsafe.SegmentMutation, Segment.Guest]
```

Current rules:

- `@async_entry`, `@segment_agnostic`, `@segment_establishing`, and `@reentrant_safe` take no arguments
- `@segment_transition` requires exactly one target owner argument: `host` or `guest`
- async-entry functions must explicitly handle segment-owner assumptions and reentrant safety
- externs that mutate active segment state require explicit `@segment_transition(...)` so owner transitions are typed contracts

For complete segment-owner behavior and permission examples, see [27-segment-owner-safety-surface.md](/Users/torarinvikbjarko/Documents/Coding%20Projects/Go%20projects/Elisa-core/docs/27-segment-owner-safety-surface.md).

## Branch hints

Branch-probability hints are statement syntax rather than `@` annotations, but
they live in the same compile-time-hint family.

```elisa
if likely value:
    return 1

while unlikely value:
    return 0
```

Current rules:

- `likely` and `unlikely` are contextual statement hints for `if` and `while`
- the raw condition expression remains the same expression the compiler would analyze without the hint
- branch hints do not combine with optional-bind or pattern-binder `if` forms; use an ordinary boolean condition when a hint is needed
- current LLVM lowering turns these into branch-weight metadata rather than a different source-level control-flow rule

## Practical caveats

- annotations stack, so a declaration may carry more than one annotation when the combination is valid
- several annotations are intentionally target-specific and may only make sense on one declaration family
- when in doubt, treat annotations as compile-time metadata that refine layout, code generation, or proof flow; they do not create runtime objects on their own
