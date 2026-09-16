# FFI boundary safety: measured gaps and a grounded proposal

Status: proposal, 2026-09-15. Nothing here is implemented yet. Every claim about
the current surface was measured against both compilers and the real binding
projects on the same day; the numbers are reproducible with the commands in §1.

## 0. Why now

Elisa's FFI is *explicit* (doc 08) and *audited* (doc 31), but it is not yet
*safe*: a correct Elisa program that calls a correctly-declared extern can still
be undefined behaviour, and nothing in the language says which extern parameter
made it so. Rust answers this with `unsafe` blocks that push the whole obligation
onto the reader. We want the opposite: crossing the boundary creates specific,
named obligations, and each one is **proved, checked, or explicitly trusted**,
never merely permitted.

The game-engine direction (an Elisa world over Jolt, Wicked, ozz, Recast,
miniaudio) makes this urgent: that engine is thousands of externs over
C facades, and today every one of them would be written the way iplug2 is
written (§1.2).

## 1. What exists today, measured

### 1.1 Compiler surface

Both compilers accept this extern vocabulary (stage0 enforces the full set;
stage1 checks presence of contracts and `@trusted`, and mirrors the
diagnostics):

| Mechanism | What it gives | What it does **not** give |
|---|---|---|
| `extern Name` opaque type, `@c_opaque(header, ctype)` | nominal handle types; `==` is pointer identity | ownership, release pairing, live/dead state |
| `@link_name`, `@callconv`/`@c_abi`, `@intrinsic`, `...` varargs | correct symbol and calling convention | anything about memory |
| `layout c`, `@c_bind`, `-emit c-bind-check` | layout verified by the target C compiler | field validity, pointer fields' referents |
| `requires` / `ensure` / `uses Contract` on an extern | `requires` discharged at **every call site** (linear prover, then SMT, then runtime check); `ensure` **assumed** downstream once `requires` is proven (`ProofAssumedExtern`, class Boundary) | a vocabulary for memory: nothing relates a pointer to a length, nothing says a pointer is readable, writable, non-overlapping, or still alive |
| `@borrows_return(...)`, `@borrows_return(field, ...)` | provenance of a returned borrow | verification (doc 26 lists it as a known residual: "trusted, unchecked lifetime assertions") |
| `@blocking` / `@nonblocking`, `@reentrant_safe`, `@segment_transition` | progress and segment-owner facts | retention, threading, reentrancy of *callbacks* |
| `@trusted("reason")`, `-strict-externs` | every extern must carry a contract **or** a reason | that the contract *covers* the pointer parameters; presence is the whole check |
| `Unsafe.*` permissions, `-emit unsafe` | every extern call is `Unsafe.RawExtern`, closed over the call graph; casts, arithmetic, stale refs are hard errors | attribution: the report says a function is unsafe, not *which parameter of which extern* is the unproven obligation |
| `GuestVAddr[T]` / `HostPtr[T]` boundary carriers | typed address-space pointers for the emulator | a general foreign-pointer discipline |

### 1.2 How real bindings are actually written

Counted with `grep -rh "^ *extern "` over each project:

| project | externs | params typed `void&?` / `mutable void&?` | externs with any annotation | `view[T]` params | callbacks |
|---|---|---|---|---|---|
| elisa_iplug2 | 1605 | 510 + most of 1245 `mutable` | 0 | 0 | `type Cb = fn(mutable void&?, cstr) -> void` + `user: mutable void&?` |
| elisa-shadps4 | 1918 | 1525 + 109 `void&` | 392 `@callconv`, 60 `@link_name`, 13 `@reentrant_safe`, 5 `@segment_transition` | 0 | same shape |
| elisa-ui | 781 | 52 `void&`; buffers as `usize` + `u8&` | 4 | 0 | same shape |
| elisa-wolf3d | 280 | 21 | 15 `@link_name` | 0 | — |
| Elisa-compiler (stage1) | 267 | opaque `extern LLVM*Ref` handles | 10 `@link_name` | 0 | — |

`Unsafe.*` grants inside those projects: `PointerCast` 307, `SegmentMutation`
247, `AssumeProgress` 95, `StaleRef` 80, `MutableGlobal` 23. The 307 pointer
casts are overwhelmingly the `user: void&?` round trip in callbacks.

`@trusted` and extern `requires` are used **zero** times outside the compiler
test suites. `-strict-externs` is on in no project build.

### 1.3 The holes, each with a program that compiles clean today

**H1. Handle confusion.** iplug2's `adsr_process(envelope: mutable void&?, …)`
and `web_view_evaluate_javascript(view: mutable void&?, …)` accept each other's
handles. The language *has* the fix (`extern Adsr` gives a nominal type;
`opaque_handle_eq.elisa` proves `OpaqueFile?` works end to end), but doc 08's
own type-mapping table recommends `void&?` for "pointer / opaque handle", so
every project followed it.

**H2. Unpaired ownership.** `adsr_create(...) -> mutable void&?` and
`adsr_destroy(envelope: mutable void&?)`. Nothing prevents destroy-twice,
use-after-destroy, or never-destroy. `@c_opaque` generates alloc/free/size
helpers but no linear discipline. Elisa already has `move` and typestate
(`Thread[u32, Joinable]`, doc 111) and never applies them here.

**H3. Pointer and length are strangers.** Probe (stage0, 2026-09-15):

```elisa
extern take_view(xs: view[f32]) -> usize
extern take_raw(p: f32&, n: usize) -> usize

def main() -> i64:
    a: mutable darray[f32] = []
    a.push(1.0)
    s: view[f32] = a[0:1]
    n: usize = take_view(s) + take_raw(&a[0], 99)
    return n.i64()
```

lowers to

```llvm
declare i64 @take_view(%DynArrayView)
declare i64 @take_raw(ptr, i64)
  call i64 @take_view(%DynArrayView %s8)
  call i64 @take_raw(ptr %idx.ptr, i64 99)
```

with no diagnostic. `take_raw` is handed a 1-element buffer and told it has 99.
And `take_view` is *not* a C-callable signature: the view is passed as Elisa's
`{ptr, len}` aggregate by value, so a bounded buffer cannot cross to C at all
today. This is the single most consequential gap: without it, `requires` has
nothing to say about buffers, which is why nobody writes extern contracts.

**H4. Untyped callback context.** `type PresetLoadCallback = fn(mutable void&?, cstr) -> void`
plus `user: mutable void&?`. The Elisa side casts `void&?` back to its state
type under `can Unsafe.PointerCast` on every entry. Nothing ties the lifetime of
that state to the period C may call back; nothing records which thread calls.
Doc 08 lists closure trampolines as future work.

**H5. Retention is unexpressible.** An async API (`upload_begin(data, len)`)
keeps reading `data` after the call returns. The only lifetime annotation,
`@borrows_return`, covers the return direction and is unverified. There is no
spelling for "the callee retains this argument until X".

**H6. `ensure` is assumed, not checked.** `TestExternEnsureAssumedAtCallSite`
pins that an extern's `ensure` becomes a downstream fact. For scalar facts this
is the right default; for a fact that guards memory (`ensure result <= count`
on `read`) a lying library turns safe Elisa code into UB with no runtime stop.
RLBox's lesson (below) is that data *coming from* the library is tainted until
validated.

**H7. Presence is not coverage.** `-strict-externs` accepted
`extern memcpy(dst: mutable void&, src: void&, n: usize) requires n > 0`. The
contract was present and said nothing about the two pointers. (Closed by D1 and
D12, 2026-09-15.)

**H8. Attribution.** `-emit unsafe` reports `RawExtern` per function. The
engineer reading the audit cannot see *which* obligation is undischarged.

## 2. What the literature settled

Only the results that change the design; venues given where I am confident.

- **Checked C** (Tarditi et al., Microsoft; SecDev 2018 and later): bounds
  annotations on *C prototypes* — `_Array_ptr<T> p : count(n)` — plus
  "bounds-safe interfaces" that let unmodified C callers keep raw pointers while
  checked callers get bounded ones. This is exactly H3's fix: annotate the
  existing prototype, synthesize the bounded view.
- **Cyclone** (Jim, Morrisett et al., USENIX ATC 2002): region-typed and fat
  pointers in a C dialect; showed that "which storage does this pointer come
  from" must be in the type, which is doc 26's Axis B.
- **RLBox** (Narayan et al., USENIX Security 2020): wrap every library return in
  a `tainted<T>` that cannot flow into indices, branches, or memory until an
  explicit validator runs. Cheap, mechanical, and it caught real Firefox bugs.
  Fixes H6.
- **RefinedC** (Sammler et al., PLDI 2021), **Low\*** (Protzenko et al., ICFP
  2017), **Melocoton** (Gäher et al., OOPSLA 2023, OCaml/C interop logic),
  **VeriFFI** (Korkut, Stark, Appel): full verification of the foreign side is
  possible but is a *per-library* proof effort. They justify having a
  **proven** evidence tier, not making it the default.
- **Empirical Rust FFI studies** (e.g. Li et al., "An Empirical Study of Rust
  FFI" family, 2022–2024): the dominant real-world UB classes are
  ownership/deallocator mismatch, dangling across the boundary, and unchecked
  lengths/enums from C — precisely H2, H5, H3, H6. Missing ABI and layout
  bugs, which Elisa already checks, are a minority.
- **Swift / Zig interop**: both make the bounded-buffer-to-C bridge a *type
  conversion* at the call site (`withUnsafeBufferPointer`, `[*c]T` with
  `.ptr/.len`), not an annotation. Elisa can do better by making it a
  declaration-site fact (§3.3) so the call site stays plain.

## 3. Proposal

The organizing rule, agreed in the design session:

> **Types describe who owns and may mutate memory. FFI contracts describe the
> temporal and protocol behaviour that ownership alone cannot express.**

### 3.0 A correction to the session's spelling

The session proposed `unique view[T]` / `shared view[T]`. Under doc 26 a
`view` *is* a borrow by definition, so "unique view" would contradict the
vocabulary the compiler already enforces. Foreign ownership belongs on doc 26's
axes, not as an adjective on `view`:

| Axis | today | add |
|---|---|---|
| A — shape | `view[T]` borrow, `darray[T]` owner | unchanged |
| B — storage class | `stack`, `region R`, `heap`, `static` | **`foreign`**: storage owned by a native allocator and released by a declared native function |

So a C-owned buffer is `foreign darray[f32]` (an owner whose storage class is
foreign), and lending it to C is the ordinary `view[f32]` / `mutable view[f32]`
borrow. `shared` maps onto the refcount opt-in doc 26 already reserves. This
keeps every existing borrow rule (a borrow may not outlive its storage) applying
unchanged across the boundary.

### 3.1 Typed handles by default (closes H1)

- Doc 08's mapping table changes: "pointer / opaque handle" → `extern Name`
  plus `Name` / `Name?` / `mutable Name&`; `void&?` becomes the documented
  exception for genuinely untyped C APIs.
- `-strict-externs` gains a rule: a `void&?` / `mutable void&?` parameter or
  return on an extern is an error unless the extern is `@trusted`.
- Migration is mechanical for Elisa-owned C facades (iplug2, the engine
  bridge): give each facade struct its own incomplete C type and mirror it with
  `extern`. Estimated by count: ~1755 parameters across iplug2 and shadps4, all
  in binding files.

### 3.2 Foreign resources: `extern resource` + `__drop__` (closes H2)

Doc 126 already defines the destructor protocol: declaring `__drop__` on a type
makes it affine (move-only), and the compiler runs it on every exit edge of the
owning scope, including `try` propagation. A foreign resource is exactly that,
with the body being the native release call:

```elisa
extern resource Adsr

def __drop__(self: consume Adsr) -> void:
    elisa_iplug2_adsr_destroy(self)

extern elisa_iplug2_adsr_create(name: cstr, sustain: bool) -> Adsr?     # owned result
extern elisa_iplug2_adsr_destroy(envelope: consume Adsr) -> void
extern elisa_iplug2_adsr_process(envelope: mutable Adsr&, level: f64) -> f64
```

Rules:

- `extern resource X` is an opaque handle type (like `extern X` today) whose
  bare spelling `X` is an **owner** of storage class `foreign`. Declaring it
  **requires** a `__drop__` in the same module; a missing one is a hard error
  (D2 in §3.7). A plain `extern X` stays a non-owning handle for APIs where
  ownership lives elsewhere, and cannot be returned from a `-> X` extern
  that is not `@trusted` (D3).
- `X?` from a constructor is the ordinary optional; `else raise` is the
  idiomatic unwrap. `X&` / `mutable X&` are ordinary borrows, so use after
  drop is the existing stale-ref error and a second consuming call is the
  existing use-after-move error.
- An extern parameter `consume X` transfers ownership to C and suppresses the
  scope-exit drop (doc 126's move rule). `__drop__` is normally the only such
  call; a second consuming extern (`SDL_FreeSurface` next to a `__drop__` that
  also frees) is allowed but each value can reach only one of them.
- State machines beyond live/dead use doc 111 typestate: `Socket[Open]`,
  `Socket[Closed]`; the `T[?]` spelling and its lowering already exist in both
  compilers (the call-site state check is a known stage1 gap and must be closed
  as part of this).
- `@c_opaque` continues to supply size/align for by-value opaque storage.

### 3.3 Bounded buffers across the boundary (closes H3, unlocks contracts)

Two forms, both declaration-site, so call sites stay `f(samples)`:

**Native form.** A `view[T]` / `mutable view[T]` parameter on a C-ABI extern
lowers to **two C arguments in place**, pointer then length:

```elisa
extern scale_samples(samples: mutable view[f32], gain: f32) -> void
# C: void scale_samples(float *samples, size_t count, float gain);
```

**Legacy form.** For prototypes that already exist, bind the length parameter
to the pointer parameter (pairs: pointer, length) and let the compiler synthesize
the bounded surface:

```elisa
@bounds(buf, count)
extern read(fd: i32, buf: mutable u8&, count: usize) -> isize
```

Callers then pass a `mutable view[u8]`; `count` is filled from the view's
`.len` and cannot be supplied by hand. This is Checked C's bounds-safe interface. A
`@bounds`-less pointer-plus-integer extern becomes a `-strict-externs` error
(H7 becomes decidable: every pointer parameter must be a typed handle, a
bounded view, a `@bounds` target, or `@trusted`).

Once views cross, `requires` gains its vocabulary for free:
`requires out.len >= n`, `requires a.len == b.len`. Overlap
(`noalias`) uses the provenance facts doc 11 already tracks internally.

### 3.4 Inbound data is tainted until validated (closes H6)

- An extern `ensure` that a downstream *memory* obligation depends on (an index,
  a length, a discriminant) is **checked at runtime** in checked builds instead
  of assumed; assumption is what `@trusted` buys. Scalar compares cost nothing
  measurable next to a native call.
- Returned enums are constructed through a validator, never reinterpreted:
  `DeviceState.from_c(raw) -> DeviceState error[ForeignEnum]`. Elisa's
  error-union surface (doc 65) is the natural carrier.
- Returned lengths that describe returned pointers are declared together:
  `-> view[u8]` under §3.3 (pointer then length as two C return channels, or an
  out-parameter bound with `@bounds`).

### 3.5 Typed callback context and retention (closes H4, H5)

```elisa
extern subscribe(callback: fn(Listener&, f32) -> void, context: Listener&)
    -> Subscription
    retains context until drop(result)
    callbacks callback on thread(worker)
```

- The compiler generates the C-ABI trampoline: C sees `void (*)(void*, float)`
  and a `void*`; the cast back to `Listener&` happens once, in generated code,
  under a compiler-owned grant. The 307 hand-written `PointerCast` grants
  disappear from user code.
- `retains X until E` extends the borrow of `X` to the lifetime of `E`, which
  the existing outlives check then enforces: `Listener` storage that dies before
  the `Subscription` is dropped is the ordinary "borrow outlives storage"
  error, now reported at the boundary.
- `callbacks … on thread(worker)` feeds the sendability check (doc 09; the
  generic-instantiation residual in doc 26 applies here too and should close
  with it). `on thread(caller)` and `reentrant` cover the other C conventions.
- `retains X for call` is the default for every borrow parameter and needs no
  spelling.

### 3.6 Evidence, attribution, and the gate (closes H7, H8)

Each obligation created by an extern's signature carries one of four evidence
tags the analyzer already distinguishes for `requires`:

| tag | meaning | source |
|---|---|---|
| proven | discharged statically | `ProofProvenLinear` / `ProofProvenSMT` |
| checked | runtime check emitted | `ProofRuntime` |
| assumed | native side promised, unverified | `ProofAssumedExtern` (only under `@trusted`) |
| untyped | no obligation could be formed | new; the thing `-strict-externs` rejects |

`-emit unsafe` gains a per-extern section: one line per parameter and return,
its obligation kind (handle, owned, view, bounds, retention, callback), and its
tag. `-strict-externs` fails on any `untyped` row. This is the report an
engine team reviews before shipping a binding family.

### 3.7 The guarantees, stated as diagnostics

Changing the spellings is not the point; the point is what the compiler now
refuses, and how clearly it says why. Each guarantee below is a diagnostic
both compilers must emit byte-identically (the parity rule), with a
differential fixture that must be rejected and a runtime fixture that must
pass. Wording follows the house style of the existing extern messages.

| id | guarantee | message (`error:` prefix, `file:line:col` as today) |
|---|---|---|
| D1 | no untyped handles at the boundary | `extern function "adsr_process" parameter "envelope" is an untyped pointer (mutable void&?); declare an opaque handle with `extern Name` and use it instead of void, or mark the extern @trusted("reason")` (LANDED in both compilers; the return form reads `returns an untyped pointer (…)`) |
| D2 | every resource has a destructor | `extern resource "SdlTexture" declares no `__drop__`; add `def __drop__(self: consume SdlTexture)` in this module so the native handle is released on every exit path` |
| D3 | ownership of a return is declared | `extern "SDL_CreateTexture" returns non-owning handle "SdlTexture" from a constructor; return `SdlTexture` (owned) or `SdlTexture&` (borrowed from a parameter via @borrows_return)` |
| D4 | no use after native release | `"tex" was consumed by "SDL_DestroyTexture" at 41:5 and is used again here; the native object is already released` (the existing use-after-move message, with the consuming extern named) |
| D5 | pointer parameters carry bounds | `extern function "read" parameter "buf" is a pointer with no bounds; declare it as mutable view[u8], bind a length with @bounds(buf, <length>), or mark the extern @trusted("reason")` (LANDED in both compilers: fires for a scalar reference beside an integer parameter; a struct reference is one object and a lone scalar reference is an out-parameter, so neither fires) |
| D6 | a bound length is never hand-typed | `argument "count" of "read" is supplied by @bounds(buf, count) from buf.len; remove the explicit argument` |
| D7 | a view crossing to C is contiguous and sized | `cannot pass "s" to C-ABI extern "take_view": view[f32] over a strided/packed source has no (pointer, length) form; copy it first` |
| D8 | inbound facts that guard memory are checked | `ensure on extern "getcwd" guards memory (result.len < buffer.len) and is not assumed; a runtime check is emitted, or mark the extern @trusted("reason") to assume it` (a note, not an error, so the audit can list it) |
| D9 | foreign enum values are validated | `extern "device_state" returns i32 used as DeviceState; construct it through DeviceState.from_c(...) so out-of-range values are rejected` |
| D10 | retained borrows outlive their retainer | `"plugin_state" is retained by "web_view_set_callbacks" until drop("view") but its storage ends at 88:1, before "view" is dropped at 102:1` (the existing outlives-storage message, extended with the retention edge) |
| D11 | callback thread matches sendability | `callback "on_message" runs on thread(worker) but its context "PluginState" is not sendable; add Unsafe.ThreadShare or make the context sendable` |
| D12 | `@trusted` is the only entry point for trust | `extern function "memcpy" pointer parameter "dest" is not covered by its contract; under -strict-externs every pointer parameter must be named by a `requires`/`ensure` clause, be an opaque handle, or the extern must be @trusted("reason")` (LANDED in both compilers, one per uncovered parameter at the parameter; will widen to bounded views and @bounds targets with step 2) |

What this buys the engine team: `-strict-externs` on a binding family either
passes, or every failure names the extern, the parameter, and the one-line
fix. The unsafe audit (§3.6) lists every `@trusted` reason and every D8
runtime check, and nothing else is trusted anywhere.

## 4. Order of work and what each step buys

Status 2026-09-15: **step 2 landed** in both compilers: a `view[T]` parameter of
a `@callconv(c)` extern lowers to (pointer, length) in place, and
`@bounds(ptr, len)` binds a legacy prototype's pair into one view with the C
order preserved (stage0 `fec94067`, `cd5a3ea8`; gate
`test/parity/extern_view_split_smoke.sh`: declarations byte-identical, two
runtime fixtures through libc `strnlen`, seven byte-identical rejections
including D6). D5 landed the same day (stage0 `analyzer_extern_pointer_discipline.go`,
gate `extern_discipline_smoke.sh`, fourteen cases).

Status 2026-09-15: **step 1 landed** in both compilers (stage0 `4ea78cf2`,
stage1 `c520364f`), gated by `test/parity/extern_discipline_smoke.sh` (nine
cases, byte-identical stderr). Landing it also exposed and fixed two unrelated
stage1 gaps that blocked self-hosting: modules declared once per `static if`
branch (`static_if_module_smoke.sh`) and nested-module const reads
(`nested_module_const.elisa`, stage1 `07dfa7a9`).

1. **Doc 08 mapping table + `-strict-externs` `void&?` rule** (§3.1). One
   analyzer rule in each compiler, one doc edit. Catches H1 immediately in
   every project that turns the gate on.
2. **Bounded views across the boundary** (§3.3). Backend: split-lowering of
   view params on C-ABI externs in both compilers, byte-identical IR as the
   parity gate; analyzer: `@bounds`. This is the step that makes extern
   `requires` worth writing.
3. **Foreign resources** (§3.2). Mostly reuse: `move`, typestate, stale-ref.
   The stage1 typestate call-site check must land here.
4. **Tainted inbound data** (§3.4). A policy flip on `ensure` for memory-
   bearing facts plus validators for enums.
5. **Callback trampolines and retention** (§3.5). The largest piece; also the
   one the engine cannot ship without.
6. **Audit surface** (§3.6). Reporting over 1–5.

Each step is measured the way this repo measures everything: a differential
fixture that stage0 and stage1 must reject identically, a runtime fixture that
must produce the same bytes, and the existing `-emit unsafe` corpus probe kept
at EXTRA = 0.

## 5. Worked examples from the projects, before and after

Every "before" is copied from a project as it is today.

**wolf3d window / renderer / texture** (`port/wolf_sdl.elisa:1706`), three
SDL objects sharing one type, none ever destroyed:

```elisa
win: void& = SDL_CreateWindow("Elisa Wolfenstein 3D".cast[u8&], 0x2FFF0000, 0x2FFF0000, 640, 480, 0x2004)
ren: void& = SDL_CreateRenderer(win, -1, 0x6)
tex: void& = SDL_CreateTexture(ren, 0x16161804, 1, VIEWWIDTH, SCREENHEIGHT)
```

```elisa
error VideoError:
    NoWindow
    NoRenderer
    NoTexture

extern resource SdlWindow
extern resource SdlRenderer
extern resource SdlTexture
def __drop__(self: consume SdlWindow) -> void:   SDL_DestroyWindow(self)
def __drop__(self: consume SdlRenderer) -> void: SDL_DestroyRenderer(self)
def __drop__(self: consume SdlTexture) -> void:  SDL_DestroyTexture(self)

extern SDL_CreateWindow(title: cstr, x: i32, y: i32, w: i32, h: i32, flags: u32) -> SdlWindow?
extern SDL_CreateRenderer(window: SdlWindow&, index: i32, flags: u32) -> SdlRenderer?
extern SDL_CreateTexture(renderer: SdlRenderer&, format: u32, access: i32, w: i32, h: i32) -> SdlTexture?

win: SdlWindow = SDL_CreateWindow("Elisa Wolfenstein 3D", CENTERED, CENTERED, 640, 480, ALLOW_HIGHDPI) else raise VideoError.NoWindow
ren: SdlRenderer = SDL_CreateRenderer(win, -1, ACCELERATED | PRESENTVSYNC) else raise VideoError.NoRenderer
tex: SdlTexture = SDL_CreateTexture(ren, RGB888, STREAMING, VIEWWIDTH, SCREENHEIGHT) else raise VideoError.NoTexture
```

Passing `ren` where a texture is expected is a type error; scope exit drops
the three in reverse order; a manual `SDL_DestroyRenderer(ren)` followed by a
use is D4.

**wolf3d palette** (`cpp-bridge/elisa_wolf3d_main.elisa:155`), pointer and
count typed by hand:

```elisa
_ = wolf3d_sdl_set_palette_colors(palette, (&game_palette_bytes[0]).cast[mutable u8&], 0, 256)
```

```elisa
extern wolf3d_sdl_set_palette_colors(palette: mutable SdlPalette&, colors: view[SdlColor], first: i32) -> i32
    requires first >= 0 and first.usize() + colors.len <= palette.ncolors.usize()

_ = wolf3d_sdl_set_palette_colors(palette, game_palette, 0)
```

For a prototype we do not own: `@bounds(colors, ncolors)` on the original
SDL declaration, and D6 forbids typing `ncolors` by hand.

**elisa-ui Android text** (`ui_android_controls.elisa:214`), the manual
bounds dance repeated in every text extern:

```elisa
safe: sview = UiCore::bounded_text(text)
return if safe.len <= 0 or safe.data == null
elisa_android_controls_set_text(handle, safe.data[0], safe.len.usize())
```

```elisa
extern elisa_android_controls_set_text(handle: AndroidControl&, text: sview) -> void
elisa_android_controls_set_text(handle, UiCore::bounded_text(text))
```

**stage1 LLVM verifier message** (`src/driver/elisac.elisa:450`), a cast for
the out-pointer and a dispose on each exit path:

```elisa
verification_message: mutable cstr = zeroed
verification_pointer: void& = (&verification_message).cast[void&] can Unsafe.PointerCast
verification_failed: i32 = LLVMVerifyModule(module_handle, LLVM_VERIFY_RETURN_STATUS_ACTION, verification_pointer)
if verification_failed != 0:
    ...
    LLVMDisposeMessage(verification_message)
    return 2
LLVMDisposeMessage(verification_message)
```

```elisa
extern resource LlvmMessage
def __drop__(self: consume LlvmMessage) -> void:
    LLVMDisposeMessage(self)

extern LLVMVerifyModule(m: LLVMModuleRef, action: i32, out message: LlvmMessage?) -> i32

verdict: Verification =
    match LLVMVerifyModule(module_handle, LLVM_VERIFY_RETURN_STATUS_ACTION, out message):
        0: Verification.Ok
        _: Verification.Invalid(message else "")
```

**stage1 host CPU strings** (`codegen_target_machine.elisa:148`), one
variable that is sometimes a literal and sometimes LLVM-owned:

```elisa
cpu: mutable cstr = ""
if host_tuned:
    cpu <- LLVMGetHostCPUName()
machine: LLVMTargetMachineRef = LLVMCreateTargetMachine(target, triple, cpu, features, 0, 0, 0)
LLVMDisposeMessage(cpu) if host_tuned
```

```elisa
extern LLVMGetHostCPUName() -> LlvmMessage
host_cpu: LlvmMessage? = LLVMGetHostCPUName() if host_tuned else null
machine: LLVMTargetMachineRef = LLVMCreateTargetMachine(target, triple, host_cpu else "", features, 0, 0, 0)
```

An owned `LlvmMessage` lends itself as a `cstr` borrow; a literal is a
`static cstr`. The "dispose only if we allocated" condition has nowhere to
exist.

**stage1 `getenv` / `getcwd`** (`src/driver/project_paths.elisa:70`), three
casts, a double lookup, and a repeated buffer size:

```elisa
key: cstr = (&name[0.usize()]).cast[cstr] can Unsafe.PointerCast
if project_getenv(key) is real:
    exported: cstr = project_getenv(key).cast[cstr] can Unsafe.PointerCast
    bytes_extend_cstr(&logical, exported)
buffer: mutable darray[u8] = []
buffer.resize(4096)
target: void& = (&buffer[0.usize()]).cast[void&] can Unsafe.PointerCast
_ = project_getcwd(target, 4096.usize())
```

```elisa
@link_name("getenv")
extern project_getenv(name: cstr) -> static cstr?          # borrowed from environ
@link_name("getcwd")
extern project_getcwd(buffer: mutable view[u8]) -> cstr?
    ensure result == null or result.len < buffer.len       # D8: checked, not assumed

if project_getenv("PWD") is exported:
    bytes_extend_cstr(&logical, exported)
buffer: mutable darray[u8] = zeroed(4096)
_ = project_getcwd(buffer)
```

**iplug2 web view callbacks** (`bindings/iplug2_preset_controls.elisa:5`),
untyped `user` pointer cast back in every callback body:

```elisa
type WebViewMessageCallback = fn(mutable void&?, cstr) -> void
extern elisa_iplug2_web_view_set_callbacks(view: mutable void&?, user: mutable void&?,
    ready: WebViewReadyCallback?, message: WebViewMessageCallback?, ...)
```

```elisa
extern resource WebView
def __drop__(self: consume WebView) -> void:
    elisa_iplug2_web_view_destroy(self)

extern elisa_iplug2_web_view_set_callbacks[S](view: mutable WebView&, context: S&,
        ready: fn(S&) -> void?,
        message: fn(S&, sview) -> void?)
    retains context until drop(view)
    callbacks ready, message on thread(main)

def on_message(state: PluginState&, text: sview) -> void:
    state.log(text)

elisa_iplug2_web_view_set_callbacks(view, plugin_state, null, on_message)
```

The one cast lives in the generated trampoline. A `PluginState` that dies
before the view is D10, reported at this line.

**shadps4 memcpy** (`core/guest_exec.elisa:277`), a length related to
neither pointer:

```elisa
_ = ge_raw_memcpy(dst.cast[mutable void&], src, n)
```

```elisa
@link_name(memcpy)
extern ge_raw_memcpy(dest: mutable view[u8], src: view[u8]) -> void
    requires dest.len >= src.len
    requires disjoint(dest, src)

ge_raw_memcpy(dst_bytes, src_bytes)
```

## 6. Non-goals

- Parsing C headers into declarations. Facades are Elisa-owned; declaring them
  twice is cheap and keeps the contract in Elisa.
- Proving native bodies. `@trusted("reason")` stays the honest spelling for
  what we cannot verify; the goal is that it is the *only* place where trust
  enters, and that the audit lists every such place.
- Sandboxing (RLBox-style process or Wasm isolation). Orthogonal and possible
  later, since `-emit wasm` already exists.
