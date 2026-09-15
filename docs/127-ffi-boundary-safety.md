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

**H7. Presence is not coverage.** `-strict-externs` accepts
`extern memcpy(dst: mutable void&, src: void&, n: usize) requires n > 0`. The
contract is present and says nothing about the two pointers.

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

### 3.2 Foreign resources with linear release (closes H2)

```elisa
extern resource Adsr:
    release elisa_iplug2_adsr_destroy      # void (*)(Adsr*)

extern adsr_create(name: cstr, sustain: bool) -> Adsr        # owned result
extern adsr_process(envelope: mutable Adsr&, level: f64) -> f64
```

Semantics, all built from machinery the language has:

- `Adsr` (bare) is an **owner** of storage class `foreign`; a function returning
  it returns ownership; dropping it calls `release`; passing it by value is a
  `move`. This is the same linear discipline as `Thread[u32, Joinable]`.
- `Adsr&` / `mutable Adsr&` are ordinary borrows, so use-after-release is the
  existing stale-ref error and double release is the existing use-after-move
  error.
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
to the pointer parameter and let the compiler synthesize the bounded surface:

```elisa
@bounds(buf: count)
extern read(fd: i32, buf: mutable u8&, count: usize) -> isize
```

Callers then pass a `mutable view[u8]`; `count` is filled from `.count` and
cannot be supplied by hand. This is Checked C's bounds-safe interface. A
`@bounds`-less pointer-plus-integer extern becomes a `-strict-externs` error
(H7 becomes decidable: every pointer parameter must be a typed handle, a
bounded view, a `@bounds` target, or `@trusted`).

Once views cross, `requires` gains its vocabulary for free:
`requires out.count >= n`, `requires a.count == b.count`. Overlap
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
    retains context until release(result)
    callbacks callback on thread(worker)
```

- The compiler generates the C-ABI trampoline: C sees `void (*)(void*, float)`
  and a `void*`; the cast back to `Listener&` happens once, in generated code,
  under a compiler-owned grant. The 307 hand-written `PointerCast` grants
  disappear from user code.
- `retains X until E` extends the borrow of `X` to the lifetime of `E`, which
  the existing outlives check then enforces: `Listener` storage that dies before
  the `Subscription` is released is the ordinary "borrow outlives storage"
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

## 4. Order of work and what each step buys

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

## 5. Non-goals

- Parsing C headers into declarations. Facades are Elisa-owned; declaring them
  twice is cheap and keeps the contract in Elisa.
- Proving native bodies. `@trusted("reason")` stays the honest spelling for
  what we cannot verify; the goal is that it is the *only* place where trust
  enters, and that the audit lists every such place.
- Sandboxing (RLBox-style process or Wasm isolation). Orthogonal and possible
  later, since `-emit wasm` already exists.
