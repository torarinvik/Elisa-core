# 65 — Error unions: capabilities and where they're used

Summary of the error-union system as it stands after the "error handling everywhere"
arc. Error unions are Elisa's typed, exhaustively-checked error channel — closer to a
checked-exception/`Result` hybrid, but with a key difference from Rust's single-`E`
`Result`: **error sets mix and match**.

## Capabilities (all runtime-verified)

### Declaring sets
```elisa
error FooError:
    Bad1
    Bad2
error BarError:
    Bad3
    Bad4(n: u32)        # payload-carrying variant
```

### Function signatures — precise, mixed, brace-subset
```elisa
def foo(num: u32) -> i64 error[FooError{Bad1}, BarError{Bad3, Bad4}]:
    ...
```
- A union **combines tags from several sets** (`FooError` + `BarError`).
- **Brace-subsets** select exact variants: `FooError{Bad1}` is *only* `Bad1`.
- Membership is **precise**: this union carries exactly `{Bad1, Bad3, Bad4}`. `FooError.Bad2`
  cannot occur — raising or matching it is a compile error, and a `catch` is exhaustive over
  just those three (never forced to handle `Bad2`). A brace-subset that is a *whole* family
  canonicalizes to the family name in diagnostics.
- `void error[E]`, value-carrying `T error[E]`, and payload-carrying variants all work.

### Raising
```elisa
raise FooError.Bad1
raise BarError.Bad4(num)        # payload; builds the value in the (possibly combined) set's layout
```

### Propagating — `try` (with subset widening)
```elisa
def caller() -> i64 error[FooError{Bad1}, BarError{Bad3, Bad4}]:
    v: i64 = try foo()          # widens a sub-set into the combined set; payloads survive
    return v
```

### Handling — `catch` / `match … as`
```elisa
catch foo():
    n:                          # success arm (binds the ok value)
        use(n)
    FooError.Bad1:
        ...
    BarError.Bad4(num):         # payload binding
        log(num)
    error e:                    # optional catch-all (binds the whole error)
        ...
```
`match <expr> as ok:` is sugar for the same, with `ok:` / `else:` arms. A statement-position
`catch` may use control-flow arms (`return`/`raise`); a value-position one must yield a common
type across arms.

### Affine: must-consume
A dropped error-union value is rejected — `f()` as a bare statement, or `_ = f()`, errors unless
the union is `try`-propagated or `catch`-handled.

## Where it's used (emulator loader — the real-code showcase)

The loader uses three layered, mixed error sets, each scoped to its concern:

| Layer (file) | Set | Notes |
|---|---|---|
| ELF/SELF parse + I/O (`core/loader/elf.elisa`) | `ElfError` | `FileNotOpen`, `NotElf`, `SeekFailed`, `ShortRead` |
| Image mapping (`core/module.elisa`) | `ModuleError` | payloaded: `MapFailed(vaddr)`, `ProtectFailed(vaddr)`, … |
| `.eh_frame_hdr` decode (`core/loader/dwarf.elisa`) | `DwarfError` | payloaded: `BadVersion(version)`, `UnsupportedEncoding(encoding)`, … |
| Relocation write (`core/linker.elisa`) | `RelocError` | `WriteUnlanded(addr)`, caught record-and-continue |

The do-both functions (`Module_LoadImageSegment` / `LoadElfSegments` / `LoadElfFromHostFile`)
return the combined `error[ModuleError, ElfError]` and `try`-widen each sub-set in; the load
entry point catches across both sets; DWARF/reloc failures are caught locally for graceful or
record-and-continue handling.

## Where it is intentionally NOT used

Error unions fit **internal, multi-mode, propagate-on-failure** operations. They are *not* used for:
- **guest-ABI** functions (kernel libs, media libs) that must return `s32`/error codes to guest code;
- **binary** operations where a `bool` carries everything (`io_file` seek/flush/…);
- **lookups / absence** (`T?` optional is correct — symbol resolution, mount resolution, handle tables);
- **record-and-aggregate** failure collectors (the relocation pass's `last_failed_*`);
- **hot paths** where the extra value would cost (the deferred-relocation fast path stays `bool`).

## Compiler fixes made during this arc (all with regression tests)

1. Statement-position block-form `catch` followed by another statement (parser).
2. `void error[E]` catch — unconditional success-payload extract crashed (backend).
3. `void error[E]` over a **payloaded** set — value/return paths emitted a bare code into a
   struct-typed function (backend).
4. Raising a payload-carrying tag into a **mixed/combined** set (over-strict `SameType` guard).
5. Payload-aware cross-set `try`-widening (`remapErrorCode` relocates payload fields).
6. `catch` arm payload binding `E.Bad2(x):` (earlier phase).

Plus the surface feature **grant aliases** (`grant Name = ref, ref`) for local `can` blocks,
and the precise brace-subset membership tests.
