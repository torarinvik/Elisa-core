# Concurrency Mini-Spec

This document proposes a concurrency design for Contextlang / `llcontext` where
protocol safety is tracked in the **type system**, not by ad hoc lifetime-flow
analysis.

The core move is:

> make selected concurrency values affine protocol capabilities, give them
> explicit state in their types, and type-check moves through a separate affine
> context.

This is still intentionally not “Rust, but renamed”.

The design keeps:

- explicit runtime carriers
- explicit memory order
- explicit `can[...]` effects
- simple lowering to runtime/ABI helpers

but it stops pretending that one-shot thread/task/guard use can be modeled
cleanly as ordinary copyable values plus increasingly clever path invalidation.

## Design Goals

The first typed concurrency slice should:

- support real low-level threads, pools, locks, condvars, and atomics
- compose with existing storage qualifiers (`any`, `heap`, `stack`, `static`, named regions)
- compose with existing pointer proof state (`&`, `&?`, `!`)
- compose with `can[...]`
- make join/await/unlock/wait protocols statically one-shot
- use a real type discipline for moves instead of flow-only heuristics
- avoid requiring full closure capture analysis or whole-program lifetime inference

## Non-Goals

Still intentionally deferred:

- actors
- async/futures as a core language feature
- built-in channels
- general borrow-checking for all values
- unrestricted references to affine-containing values
- partial-move analysis for arbitrary paths
- per-element ownership for dynamic containers
- full data-race freedom for arbitrary shared heap graphs

## Core Idea

Concurrency should be modeled by six layers.

### 1. Explicit Runtime Carriers

Examples:

- `Thread[T, S]`
- `Task[T, S]`
- `ThreadPool`
- `TaskGroup`
- `Mutex`
- `MutexGuard[S]`
- `CondVar`
- `atomic[T]`

These are ordinary typed values with ordinary lowering.

### 2. Substructural Kind

Every type is either:

- `copy`
- `affine`

`copy` is the default.

Selected concurrency carriers are `affine`, and affine-ness propagates
structurally through aggregates.

### 3. Protocol State In Types

Examples:

- `Thread[T, Joinable]`
- `Task[T, Pending]`
- `MutexGuard[Held]`

This is how the type system knows which operations are legal.

### 4. Effect Families

Examples:

- `Thread.Spawn`
- `Thread.Join`
- `Thread.Detach`
- `Pool.Create`
- `Pool.Submit`
- `Pool.Await`
- `Pool.WaitAll`
- `Pool.Shutdown`
- `Sync.Lock`
- `Sync.Unlock`
- `Sync.Wait`
- `Sync.Notify`
- `Atomics.Load`
- `Atomics.Store`
- `Atomics.Exchange`
- `Atomics.CompareExchange`
- `Atomics.Rmw`
- `Atomics.Fence`

### 5. Transfer Predicates

Compiler-internal predicates still matter:

- `sendable(T)`
- `shareable(T)`
- `atomic_safe(T)`
- `atomic_numeric(T)`

These are currently derived compiler judgments, not source-visible qualifiers or
user-authored traits. They now sit beside a real affine type system, not in
place of one.

### 6. Structural Sugar

Examples:

- `pool workers(8u): ...`
- `submit work(arg)`
- `await task`
- `wait all jobs`
- `lock mu as g: ...`

The sugar should lower to the explicit carriers and operations described below.

## Substructural Typing Core

The language should use two typing environments:

- `Γ` for copyable bindings
- `Δ` for affine bindings

Judgment form:

```text
Γ ; Δ ⊢ e : τ ▷ Δ'
```

Meaning:

- expression `e` has type `τ`
- affine context changes from `Δ` to `Δ'`

This is the important shift:

> thread/task/guard use is not “special flow analysis”; it is typed resource
> consumption.

## Surface Syntax

### Affine Type Declarations

Suggested surface spelling:

```context
affine struct Thread[T, S]
affine struct Task[T, S]
affine struct MutexGuard[S]
```

Exact token order can still be tuned, but the language needs a visible affine
kind marker.

### Move

Affine values are consumed with an explicit `move`.

```context
result: i64 = join(move thread)
task_group_add(&jobs, move task)
```

For copyable values, `move` is allowed but semantically inert.

### Destructuring

To extract affine fields, the whole aggregate must be consumed explicitly.

Implemented spellings:

```context
move holder as Holder(thread, count)
return join(move thread)
```

```context
move job as Job.Run(thread, priority)
```

```context
move node in store as Expr.Add(left, right)
```

Nested payload destructuring is also supported:

```context
move node in store as Expr.Add(Expr.Int(value), rhs)
```

Pattern payloads may omit field names positionally when the order is obvious,
or use named payloads when that reads better.

The semantic rule is:

> partial by-value field extraction from an affine-containing aggregate is not
> allowed in the first slice.

## Structural Affine Rule

The following are `affine` by declaration:

- `Thread[T, S]`
- `Task[T, S]`
- `MutexGuard[S]`

And the following become `affine` structurally:

- any struct with an affine field
- any enum with an affine payload
- any fixed array whose element type is affine

So if:

```context
struct Holder:
    thread: Thread[i64, Joinable]
    count: i64
```

then `Holder` is affine too.

## First-Slice Restrictions

To keep phase 1 tractable, impose these rules.

### 1. No references to affine-containing values

If `T` contains affine values structurally, reject:

- `T&`
- `T&?`
- `T!`
- `&value`

Examples:

```context
def bad(arg: Holder&) -> void:   # reject
    pass

alias: Holder& = &holder         # reject
```

### 2. No affine global storage

If `T` contains affine values structurally, reject:

- `global x: T`
- `extern x: T`

until there is a stronger whole-program ownership model.

### 3. No partial moves initially

Reject direct by-value field extraction from an affine-containing aggregate:

```context
join(holder.thread)   # reject in phase 1
```

Instead:

```context
move holder as Holder(thread, count)
join(move thread)
```

### 4. Indexed affine values are conservative

If `items` has an affine element type, then `move items[i]` is treated as
consuming the containing root `items`, not one independently tracked slot.

This avoids fake precision before the language has a real container ownership
story.

### 5. Packed enums do not carry affine payloads in phase 1

Packed enums currently reject:

- affine common fields
- affine payloads
- aggregates containing affine values

## Protocol State Types

Implemented marker types:

```context
type Joinable
type Pending
type Held
```

There is no need for `Joined`, `Awaited`, or `Unlocked` result types in the
first slice because the consuming operations simply destroy the capability.

## Runtime Carriers

```context
affine struct Thread[T, S]
affine struct Task[T, S]
struct ThreadPool
struct TaskGroup

struct Mutex
affine struct MutexGuard[S]
struct CondVar

struct atomic[T]
```

User-defined affine structs are also supported:

```context
affine struct WorkerLease:
    raw: mutable uintptr
```

## Core Operations

### Threads

```context
extern spawn1[A, R, permission P](fn: func(A) -> R can[P], arg: A) -> Thread[R, Joinable] can[Thread.Spawn]
extern join[R](t: move Thread[R, Joinable]) -> R can[Thread.Join]
extern detach[R](t: move Thread[R, Joinable]) -> void can[Thread.Detach]
```

### Pools and Tasks

```context
extern pool_new(threads: usize) -> ThreadPool can[Pool.Create]
extern pool_shutdown(pool: ThreadPool&) -> void can[Pool.Shutdown]

extern pool_submit1[A, R, permission P](pool: ThreadPool&, fn: func(A) -> R can[P], arg: A) -> Task[R, Pending] can[Pool.Submit]
extern pool_await[R](task: move Task[R, Pending]) -> R can[Pool.Await]

extern task_group_new() -> TaskGroup
extern task_group_add[T](group: TaskGroup&, task: move Task[T, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void can[Pool.WaitAll]
```

### Locks and Condvars

```context
extern mutex_lock(mu: Mutex&) -> MutexGuard[Held] can[Sync.Lock]
extern mutex_unlock(g: move MutexGuard[Held]) -> void can[Sync.Unlock]
extern cond_wait(cv: CondVar&, g: move MutexGuard[Held]) -> MutexGuard[Held] can[Sync.Wait]
extern notify_one(cv: CondVar&) -> void can[Sync.Notify]
extern notify_all(cv: CondVar&) -> void can[Sync.Notify]
```

This gives the language a type-level account of:

- join exactly once
- detach exactly once
- await exactly once
- unlock exactly once
- `cond_wait` consumes the old guard and returns a fresh held guard

## Formal Typing View

### Kinds

Let:

```text
k ∈ { copy, affine }
```

and every type carry a kind `kind(τ)`.

### Contexts

```text
Γ  copyable bindings
Δ  affine bindings
```

### Variable Rules

Copyable variable use:

```text
x : τ ∈ Γ
------------------------
Γ ; Δ ⊢ x : τ ▷ Δ
```

Affine variable move:

```text
x : τ ∈ Δ
------------------------
Γ ; Δ ⊢ move x : τ ▷ Δ \ {x}
```

Plain use of an affine binding in a by-value position is rejected:

```text
x : τ ∈ Δ
kind(τ) = affine
------------------------
Γ ; Δ ⊬ x : τ
```

### Aggregate Construction

If a constructor argument has affine kind, it must be moved and removed from `Δ`.

Example rule sketch for struct construction:

```text
Γ ; Δ0 ⊢ e1 : τ1 ▷ Δ1
Γ ; Δ1 ⊢ e2 : τ2 ▷ Δ2
...
Struct(F1:τ1, F2:τ2, ...) well-formed
--------------------------------------
Γ ; Δ0 ⊢ Struct(e1, e2, ...) : Struct(...) ▷ Δn
```

If `τ1` is affine, then `e1` must itself consume the relevant binding.

### Whole-Value Rule

In phase 1:

```text
if kind(Struct(...)) = affine
then by-value field projection is not a typing form
```

So:

```text
Γ ; Δ ⊬ holder.thread : Thread[i64, Joinable]
```

when `holder : Holder` and `Holder` is affine.

Instead, the language must destructure `move holder`.

### Structural Kind Rule

```text
kind(Thread[T, S]) = affine
kind(Task[T, S]) = affine
kind(MutexGuard[S]) = affine
```

For aggregates:

```text
if any field/payload/element has kind affine
then the aggregate kind is affine
```

### Sendable Is Separate

Affine does **not** imply sendable.

For submission/spawn:

```text
sendable(A)
sendable(R)
Γ ; Δ ⊢ fn : func(A) -> R ▷ Δ
Γ ; Δ ⊢ arg : A ▷ Δ'
---------------------------------------------
Γ ; Δ ⊢ spawn1(fn, arg) : Thread[R, Joinable] ▷ Δ'
```

This keeps transfer rules orthogonal to move rules.

In the current implementation, `sendable/shareable` remain internal predicates
checked at transfer seams such as `spawn1(...)` and `pool_submit1(...)`. They
are not yet source-visible generic constraints.

## `sendable(T)` in the First Slice

### Allowed by Default

- integers, bools, chars
- fixed arrays of sendable elements
- structs whose fields are all sendable
- enums whose payloads are all sendable
- `static T&` and `static T&?`
- values depending only on frozen packed stores
- explicit runtime carriers the compiler blesses as thread-safe to transfer
- `atomic[T]` where `atomic_safe(T)` holds

### Rejected by Default

- `stack T&`, `stack T&?`
- named-region refs
- plain `T&`, `T&?`
- mutable `heap T&`, `heap T&?`
- values depending on `Store[Local]`
- `MutexGuard[Held]`

The affine system handles one-shot protocol values; `sendable(T)` still decides
which data may cross thread boundaries at all.

## Publication And Packed Stores

Packed stores are stateful capabilities:

```context
Expr.Store[Local]
Expr.Store[Frozen]
```

Constructor and publication surface:

```context
store: Expr.Store[Local] = Expr.Store(owner)
frozen: Expr.Store[Frozen] = freeze(move store)
```

Rules:

- `Expr.Store(owner)` returns `Expr.Store[Local]`
- `new[store] Expr.Variant(...)` requires `store : Expr.Store[Local]`
- `match node in store:` accepts either `Expr.Store[Local]` or `Expr.Store[Frozen]`
- `move/open/view ... as Expr.Variant(...)` accept the same nested payload-pattern grammar as `match`
- packed-store provenance is structural and recursive through aggregates,
  arrays, views, helper returns, and destructuring binders
- `freeze(move store)` remaps nested dependencies structurally from
  `Expr.Store[Local]` to `Expr.Store[Frozen]`
- values depending on `Expr.Store[Local]` are not sendable/shareable
- values depending only on `Expr.Store[Frozen]` may be shared if their payload
  shape is otherwise shareable

### Opaque Borrow Contracts

Extern helpers can carry provenance through explicit contracts:

```context
@borrows_return(path)
@borrows_return_field(field, path, ...)
@borrows_return_rebased(path)
@borrows_return_field_rebased(field, path, ...)
```

Meaning:

- `@borrows_return(path)` preserves exact provenance from the source path
- `@borrows_return_field(...)` attaches exact provenance to struct return field
  paths such as `meta.items`
- `@borrows_return_rebased(path)` preserves provenance but collapses indexed
  element state to wildcard element state
- `@borrows_return_field_rebased(...)` does the same for struct return field
  paths such as `meta.items`

The rebased forms are provenance contracts, not slice-offset or length proofs.

## Atomics

Atomics stay explicit and low-level.

```context
enum MemoryOrder:
    Relaxed
    Acquire
    Release
    AcqRel
    SeqCst
```

```context
extern load[T](slot: atomic[T]&, order: MemoryOrder) -> T can[Atomics.Load]
extern store[T](slot: atomic[T]&, value: T, order: MemoryOrder) -> void can[Atomics.Store]
extern exchange[T](slot: atomic[T]&, value: T, order: MemoryOrder) -> T can[Atomics.Exchange]
extern compare_exchange[T](slot: atomic[T]&, expected: T, desired: T, success: MemoryOrder, failure: MemoryOrder) -> bool can[Atomics.CompareExchange]

extern fetch_add[T](slot: atomic[T]&, value: T, order: MemoryOrder) -> T can[Atomics.Rmw]
extern fetch_sub[T](slot: atomic[T]&, value: T, order: MemoryOrder) -> T can[Atomics.Rmw]
extern fetch_or[T](slot: atomic[T]&, value: T, order: MemoryOrder) -> T can[Atomics.Rmw]
extern fetch_and[T](slot: atomic[T]&, value: T, order: MemoryOrder) -> T can[Atomics.Rmw]
extern fetch_xor[T](slot: atomic[T]&, value: T, order: MemoryOrder) -> T can[Atomics.Rmw]

extern fence(order: MemoryOrder) -> void can[Atomics.Fence]
```

`atomic_safe(T)` and `atomic_numeric(T)` remain compiler predicates.

## Structured Syntax

### Pool Scope

```context
pool workers(8u):
    ...
```

desugars to:

- `workers: ThreadPool = pool_new(8u)`
- block body
- guaranteed `pool_shutdown(&workers)` on every exit path

### Submission

Inside an active pool:

```context
t: Task[i64, Pending] = submit work(arg)
```

desugars to:

```context
t: Task[i64, Pending] = pool_submit1(&workers, work, arg)
```

### Await

```context
result: i64 = await task
```

desugars to:

```context
result: i64 = pool_await(move task)
```

### Wait-All

```context
wait all jobs
```

desugars to:

```context
task_group_wait_all(&jobs)
```

### Lock Scope

```context
lock mu as g:
    body
```

desugars conceptually to:

- `g: MutexGuard[Held] = mutex_lock(&mu)`
- body
- guaranteed `mutex_unlock(move g)` on each exit path

`cond_wait` rebinding remains explicit:

```context
lock box.mu as g:
    while not box.has_value:
        g <- cond_wait(&box.cv, move g)
```

## Example Patterns

### 1. Join Exactly Once

```context
def run_one() -> i64 can[Thread.Spawn, Thread.Join]:
    t: Thread[i64, Joinable] = spawn1(work, 7)
    return join(move t)
```

### 2. Whole-Value Destructuring of an Affine Aggregate

```context
struct Holder:
    thread: Thread[i64, Joinable]
    count: i64

def run(holder: move Holder) -> i64 can[Thread.Join]:
    move holder as Holder(thread, count)
    _ = count
    return join(move thread)
```

This is preferred over field-by-field partial moves.

### 2.5 Nested Packed Pattern Destructuring

```context
packed enum Expr:
    Int(value: int)
    Add(left: Expr, right: Expr)

def left_value(node: Expr, store: Expr.Store[Frozen]) -> int:
    open node as Expr.Add(Expr.Int(value), rhs):
        _ = rhs
        return value
    return 0
```

This uses the same recursive payload-pattern surface as `match`, but on the
statement-oriented packed destructuring forms.

### 3. Pool-Scoped Tasks

```context
def parallel_sum(data: static i32&, mid: usize, len: usize) -> i64 can[Pool.Submit, Pool.Await, Atomics.Rmw, Atomics.Load]:
    total: atomic[i64] = atomic[i64](0)

    pool workers(8u):
        left: Task[i64, Pending] = submit sum_chunk(Chunk(0u, mid, data, &total))
        right: Task[i64, Pending] = submit sum_chunk(Chunk(mid, len, data, &total))

        _ = await left
        _ = await right

        return total.load(MemoryOrder.Relaxed)
```

### 4. Task Groups

```context
def build_index(paths: static PathJob&, count: usize) -> void can[Pool.Submit, Pool.WaitAll]:
    pool workers(8u):
        jobs: TaskGroup = task_group_new()

        i: mutable usize = 0
        while i < count:
            t: Task[void, Pending] = submit parse_and_index(paths[i])
            task_group_add(&jobs, move t)
            i <- i + 1u

        wait all jobs
```

### 5. Rejected Affine-Containing Ref

```context
struct Holder:
    thread: Thread[i64, Joinable]

def bad(arg: Holder&) -> void:
    pass
```

Rejected because refs to affine-containing values are not supported in phase 1.

### 6. Rejected Stack Submission

```context
struct BadJob:
    ptr: stack i64&

def bad() -> i64 can[Pool.Submit, Pool.Await]:
    local: i64 = 7

    pool workers(4u):
        t: Task[i64, Pending] = submit use_bad(BadJob(&local))
        return await t
```

Rejected because `BadJob` is not `sendable`.

## Lowering Model

The key lowering fact is:

> affine/protocol typing is compile-time only; runtime carriers remain plain
> ABI-lowerable values.

### Erased State Parameters

`Thread[T, Joinable]` and `Thread[T, Joined]` do not need distinct runtime
representations if joined/detached threads are consumed rather than stored.

The same is true for:

- `Task[T, Pending]`
- `MutexGuard[Held]`

### Move

`move` is erased after type checking.

It affects only:

- whether a source binding remains in `Δ`
- whether a capability may be used again

### Sugar

- `await task` lowers to `pool_await(move task)`
- `lock mu as g:` lowers to lock/acquire CFG plus guaranteed `mutex_unlock(move g)`
- `wait all jobs` lowers to `task_group_wait_all(&jobs)`

## Staging Recommendation

Implement in this order:

1. add `affine` kind
2. add `move`
3. make `Thread`, `Task`, and `MutexGuard` affine
4. derive affine-ness structurally through aggregates
5. reject refs/globals of affine-containing values
6. reject partial moves initially
7. add explicit destructuring for affine aggregates
8. add visible protocol-state parameters where they buy clarity
9. keep `sendable(T)` / `shareable(T)` orthogonal

This yields a real type-system foundation for concurrency protocols without
requiring a full general-purpose borrow checker.
