# 65 — Error unions: capabilities and where they're used

Summary of the error-union system as it stands after the "error handling everywhere"
arc. Error unions are Elisa's typed, exhaustively-checked error channel — closer to a
checked-exception/`Result` hybrid, but with a key difference from Rust's single-`E`
`Result`: **error sets mix and match**.

## Capabilities (all runtime-verified)

### Declaring sets
```elisa
error LexError:
    IndentError
    IllegalCharError
error ParseError:
    IllegalExpression
    LexerError(LexError)       # bare positional payload (matched by position)
    Detail(span: Span)         # named payload also allowed
```
Payload fields may be **bare positional types** (`LexerError(LexError)`) or **named**
(`Detail(span: Span)`) — error payloads are matched by position, so the name is optional.

### Function signatures — items in `error[...]`
A union **combines tags from several sets**. Each item is one of:

| Form | Meaning |
|---|---|
| `Variant` (bare name) | the single variant `Variant`, searched across **all** error sets |
| `Family.Variant` | that specific qualified variant |
| `Family` (bare, names a set) | the **whole** family (all its variants) |
| `*Family` | explicit whole-family spread — only *needed* when a name is both a set and a variant |
| `Family{A, B}` | **brace-subset**: exactly the listed variants |

```elisa
# Mix variants by short name across sets — family-qualified only on conflict:
def parse(...) -> Program error[IndentError, LexerError]:  # -> error[LexError.IndentError, ParseError.LexerError]
    ...

# Whole families and brace-subsets still work:
def foo(num: u32) -> i64 error[FooError{Bad1}, BarError{Bad3, Bad4}]:
    ...
```
- A **bare name** resolves as a whole family if it names a declared set, otherwise as the
  single variant looked up across every set. A bare variant present in **two or more** sets is
  **ambiguous** → a compile error telling you to qualify it as `Family.Variant`. Use `*Family`
  to force the whole-family reading when a name is both a set and a variant.
- **Brace-subsets** select exact variants: `FooError{Bad1}` is *only* `Bad1`.
- Membership is **precise**: a union carries exactly its listed variants; an unlisted variant
  cannot occur — raising or matching it is a compile error, and a `catch` is exhaustive over
  just the carried variants. A selection that covers a *whole* family canonicalizes to the
  family name in diagnostics.
- `void error[E]`, value-carrying `T error[E]`, and payload-carrying variants all work.
- Errors are part of the **type system**: an error union `T error[E]` is a first-class type,
  nameable with a `type` alias — `type Result = i64 error[LexError]`.

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

Plus the surface feature **capability aliases** (`alias Name = ref, ref`) for `can` clauses,
and the precise brace-subset membership tests.
