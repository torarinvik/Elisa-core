# 126 — `__drop__` destructors: RAII for affine resource types

> Status: **design / RFC.** Proposes a destructor protocol for affine types, run
> implicitly at value death. Builds on docs/68 (region memory model), docs/91
> (global death-time region inference), docs/62 (error unions / affine
> must-consume), docs/111/113 (typestate, linear values), docs/89 (effect laws).
>
> Motivation: regions solve *memory*; they cannot close a file descriptor,
> release a mutex, or free a foreign (FFI) resource. Today every binding is
> either manual-close discipline or full `must_consume` linearity, which taxes
> every `try` propagation path. `__drop__` gives bindings RAII-quality resource
> safety without touching the region thesis or the linear tier.

---

## 1. The three-tier ownership model

| Tier | Marking | Copyable | Cleanup | Example |
|---|---|---|---|---|
| **regular** | (none) | yes | bulk region reclamation, no hooks | `Point`, `darray[i64]`, strings |
| **affine + drop** | declares `__drop__` | no (move-only) | `__drop__` runs implicitly at death | `File`, `Socket`, FFI handles |
| **linear** | `must_consume` (existing) | no | explicit consumption **required**; never implicit | error unions, transactions, `MutexGuard` |

Two rules bind the tiers together:

1. **Declaring `__drop__` on a type makes it affine.** A destructor-bearing
   value cannot be freely copied (a copy would double-close); the compiler
   imposes move-only discipline automatically. This is Rust's "`Drop` types
   can't be `Copy`", stated positively.
2. **Regular types never have destructors and never register.** A region whose
   allocations are all regular dies as one flat reclamation with zero per-object
   work. Whether a region is "cheap to kill" is decidable at compile time from
   the types that flowed into it. This preserves the performance thesis of
   docs/68 — the common case must not grow drop glue.

Linear (`must_consume`) types sit *above* affine: their destruction is a
semantic **decision** (commit or roll back? bind or discard?), so the compiler
refuses to invoke cleanup implicitly. A linear type may offer consuming methods
that internally perform cleanup, but scope exit without consumption stays a
hard error, exactly as today.

## 2. Surface

```elisa
struct File:
    fd: i32
    path: cstr

def __drop__(self: consume File) can[Io.Close]:
    _ = close(self.fd)
```

- `__drop__` is a receiver-scoped function with a consuming receiver and `void`
  result. One per type; declared in the type's defining module.
- **Implicit invocation:** the compiler calls `__drop__` when the value dies —
  on every exit edge of its owning scope (fall-through, `return`, **`try`
  propagation**), or when its owning region/cohort is reclaimed (§4). Reverse
  declaration order within a scope; reverse allocation order within a reclaimed
  region segment.
- **Moves suppress drops.** Passing to a consuming parameter, returning,
  `promote`/`adopt`, or storing into a longer-lived owner transfers the
  obligation; the affine tracker already models this.
- **Explicit early release:** `value.drop()` is an ordinary consuming call —
  legal anywhere a move is; the scope-exit drop is then statically elided.
- **Synthesized drops:** a struct containing affine-drop fields is itself
  affine-drop; its synthesized `__drop__` drops fields in reverse declaration
  order (after the user body, if both exist). Containers of drop types
  (`darray[File]`) drop elements on death. `T?` drops the payload if present.

## 3. Restrictions on `__drop__` bodies

Because drops run on implicit edges, their effects propagate invisibly.

- **No raising.** `__drop__` returns `void`, never `T error[...]`. A cleanup
  that can fail may log, count, or (with the permission) panic — it cannot
  inject an error into a scope that didn't call it. (The C++ throwing-destructor
  trap, closed by construction.) APIs that need fallible teardown expose an
  explicit consuming `close() -> void error[E]`; `__drop__` is the last-resort
  backstop for the paths that didn't call it.
- **Declared effects only, and they propagate.** A function whose scope can
  implicitly drop a `File` must be able to grant `Io.Close` (directly or via a
  surrounding `can` block). The checker knows statically which drops each scope
  may run, so this is ordinary effect accounting; the diagnostic must name the
  value and the implicit edge ("drop of `file` on `try` propagation at …").
- **No allocation into the dying region.** A drop running during region
  reclamation may not allocate into that region. (Allocating into *other* live
  regions is permitted but discouraged; lint later.)

## 4. Interaction with regions (the load-bearing section)

Reclamation is increasingly implicit (scoped-region implicit destroy, docs/91
death-time cohorts), so drop semantics anchor to **reclamation events**, not to
syntax:

> A drop-typed value registers with its owning region at `new` (or occupies a
> stack slot's drop list if stack-allocated). Its `__drop__` runs immediately
> **before** its memory is reclaimed, however reclamation is triggered:
> implicit scope-exit destroy, an inferred death-point free (docs/91 cohort
> death), explicit `destroy` / `reset`, or a `restore` past its mark — in
> reverse allocation order within the reclaimed segment.

Mechanics:

- **Registration list per region** (and per checkpoint segment): appended at
  allocation of a drop-typed value. Regular types never touch it — zero cost
  when absent. Regions whose static type population contains no drop types
  compile to today's code exactly.
- **`mark`/`restore`:** restoring to a checkpoint runs the registered drops for
  the truncated segment, then truncates the list.
- **`adopt`:** splices the child's registration list into the parent along with
  the memory; obligations transfer, nothing runs.
- **`leak`:** today deliberately abandons a region's must-consume obligation —
  for *memory* that is recoverable; for registered drops it would leak fds. So:
  `leak` is a **compile error** when the region's static drop population is
  non-empty, unless every registered value was consumed first (or a future
  `leak ... including drops` opt-in states the intent).
- **Ordering with docs/91:** a death cohort simply runs its drop list at its
  death point. The RFCs compose; neither depends on the other landing first.

## 5. Diagnostics and tooling

The honest cost of implicit drop is invisibility. Compensate with tooling, not
syntax tax:

- `-emit lowered` (and a future `fmt` annotation mode) shows inserted drop
  points.
- A per-module or per-call-site lint (`@explicit_drop` on a type) for teams
  that want drops written out in hot code.
- The unsafe-audit sidecar (`c-archive`) counts drop-typed exports so C
  consumers know which handles carry teardown obligations.

## 6. What this enables (immediate consumers)

- `elisacore_fileio.elisa`: `File` closes on all paths, including `try`.
- `Own[T]`: a library type — unique handle + dedicated `reserve_commit` region
  + `__drop__ = destroy` — giving malloc/free-style *individual* reclamation
  for large blobs with pages returned to the OS at death (the "4 GB video
  buffer" case), with no second allocator system.
- FFI bindings: SDL windows/textures, MuJoCo `mjData`, sockets — each a struct
  + externs + `__drop__`, and the binding is leak-free by construction.
- Concurrency std: `MutexGuard` stays linear/typestate (unlock is a decision
  point in protocols) but gains a drop-backed convenience wrapper where
  appropriate.

## 7. Phasing

| Phase | Scope |
|---|---|
| D1 | Semantics in stage0: `__drop__` declaration checking, affinity induction, scope-exit/`try`-edge insertion for stack values, effect propagation, diagnostics. No region registration yet (drop types restricted to stack/parameter positions). |
| D2 | Region registration: per-region/segment drop lists; `destroy`/`reset`/`restore`/scope-exit hooks; `adopt` splicing; `leak` interaction. Runtime support in `arena.elisa`. |
| D3 | Synthesized drops for aggregates and containers; `darray[T]`/`dict` element drops. |
| D4 | Stage1 port + parity smokes; differential corpus additions; `Own[T]` + `File` in the std as proving consumers. |
| D5 | docs/91 integration (cohort death lists) when that RFC lands. |

## 8. Open questions

- Spelling: `__drop__` (runtime-dunder convention) vs a `drop` protocol impl.
  This doc assumes `__drop__` to match `__cast__` precedent in the std.
- Should `panic` unwinding run drops? Today panics abort; if that ever changes,
  drop-on-unwind needs a decision. Out of scope here (abort stands).
- Conditional moves (`if c: consume(x)` on one arm): the affine tracker already
  handles branch-dependent consumption for error unions; drops reuse that
  machinery — flag any place it must grow a dynamic drop-flag (Rust's approach)
  and prefer rejecting those programs first (require restructuring) before
  adding runtime flags.
