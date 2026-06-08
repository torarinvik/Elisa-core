# Concurrency Mini-Spec

This document proposes a concurrency design for Elisa core / `elisacore` where
strict-mode concurrency is accepted only when the program carries explicit
proof objects for ownership, protected state, protocol state, progress, and
unsafe escape hatches.

The current runtime already has low-level threads, pools, locks, condition
variables, atomics, and progress diagnostics. The strict-mode direction is to
keep those raw pieces available for the runtime and trusted low-level code, but
make ordinary user concurrency go through proof-carrying APIs.

In short:

> concurrency is allowed only as a composition of ownership transfer,
> protected domains, linear protocol states, predicate waits, and progress
> proofs.

## Strict-Mode Contract

Strict mode should guarantee that the safe surface cannot express the common
concurrency bug classes below. When a guarantee depends on the host scheduler,
operating system, or foreign code, strict mode requires a trusted runtime policy
or an explicit unsafe capability rather than silently pretending the guarantee
is local.

### Bug classes strict mode must account for

Correctness and memory safety:

- data races
- aliasing races, where two handles look independent but share the same buffer
- publication races, where a worker observes partially initialized state
- use-after-free or use-after-reset across threads
- stale views, slices, cursors, iterators, or refs after concurrent mutation
- atomicity violations across multi-step transactions
- ordering violations such as consume-before-produce or close-before-flush
- memory-ordering bugs from incorrect acquire/release/fence choices
- torn reads and writes
- ABA bugs in lock-free protocols

Waiting and liveness:

- lost wakeups
- spurious wakeup bugs
- cancellation wakeups missed by sleeping tasks
- deadlocks
- self-deadlocks
- lock-order inversions
- hold-and-wait bugs, where a task blocks while holding a resource needed by
  the unblocker
- priority inversion
- starvation
- livelock
- busy-spin retry loops with no yield, blocking wait, or bounded progress

Task, channel, and lifecycle bugs:

- orphaned tasks
- detached tasks that outlive borrowed state
- join leaks
- cancellation resource leaks
- cancellation interrupting a broken invariant
- cancellation masking in code that must stay responsive
- backpressure and resource blowup from unbounded queues, retries, tasks, or
  buffers
- channel protocol bugs such as send-after-close, receive-after-close, close
  while senders are live, and forgotten receivers
- message ordering bugs where FIFO, priority, or fairness is needed but not
  declared
- barrier and latch bugs such as wrong participant counts or generation races
- unhandled task errors
- error/cancellation state crossing a thread boundary incorrectly

Boundary and performance hazards:

- reentrancy bugs
- callbacks under a protected domain or lock while invariants are broken
- thread-affinity violations for main-thread, UI, GPU, event-loop, or realtime
  state
- blocking in nonblocking, main-thread, executor, realtime, or hot contexts
- sync-over-async deadlocks
- foreign functions that block, spawn, call back, store pointers, or share
  memory without declaring that behavior
- false sharing, lock convoys, excessive contention, and atomic hot-loop
  performance cliffs in strict performance mode

This list is intentionally broad. The point is not that every item needs a
separate feature. The point is that the same few proof axes should cover the
whole list.

## Unified Proof Axes

Strict-mode concurrency uses five orthogonal proof axes.

### 1. Share rights

Every value has a concurrency sharing judgement.

```text
local         cannot cross a task/thread boundary
send          may be moved to another task
share_read    may be shared read-only
share_atomic  may be shared through an atomic protocol
share_locked  may be shared only behind a domain/lock
unsafe_share  requires an explicit unsafe/trusted escape
```

The judgement is derived from type, storage, provenance, aliasing, and
publication facts. It is not a layout attribute. Packedness, region ownership,
and thread-sharing remain separate facts.

### 2. Domains

A domain is the unit of protected state and broken/restored invariants. It is
where strict mode records:

- protected state
- lock rank or ordering policy
- reentrancy policy
- callback policy
- priority policy
- wait predicates
- allowed blocking and cancellation behavior

Example design syntax:

```elisa
domain AccountDomain:
    protects Account.balance, Account.audit_log
    lock_rank 20
    reentrant false
    callbacks closed
    wait_policy FIFO
```

Inside a domain, an invariant may be temporarily broken. On exit, the domain
must be restored. Unknown callbacks, cancellation points, and blocking waits are
therefore rejected while a non-reentrant domain is open unless the domain
declares a policy that makes them safe.

### 3. Linear protocol states

Typestate is the local protocol carrier. It is ideal for finite state machines
where values must be consumed exactly once or only used in a particular phase.

Examples:

```text
Thread[T, Joinable] -> join -> T
Task[T, Pending] -> await/cancel -> consumed
MutexGuard[Held] -> unlock -> consumed
Promise[T, Pending] -> complete -> Promise[T, Ready]
Sender[T, Open] -> close -> Sender[T, Closed]
QueueSlot[q, Reserved] -> send -> consumed
Txn[Open] -> commit/rollback -> consumed
```

Typestate should not try to encode every global property as type parameters.
Instead, typestate proves the local protocol phase, while domains, permissions,
and progress summaries prove why a transition is globally legal.

### 4. Predicate waits

Raw wait/notify is a legacy low-level surface. Strict safe code should wait on a
predicate over protected domain state.

Design syntax:

```elisa
condition NotEmpty(q: Queue) in q.domain:
    q.items.count > 0 or q.closed

def pop(q: mutable Queue[Job]&) -> Job?:
    await q.NotEmpty:
        if q.items.count > 0:
            return q.items.pop_front()
        return null
```

The language/runtime operation is:

```text
check predicate
if false: atomically register waiter and release domain
sleep or yield according to policy
reacquire domain
recheck predicate
```

That single rule removes lost wakeups and spurious wakeup bugs from safe code.
Cancellation-aware waits additionally register cancellation with the same
predicate wait, so cancellation cannot be missed while the task sleeps.

### 5. Progress obligations

Concurrent loops, retry loops, waits, blocking calls, and recursive cycles carry
progress obligations. Accepted evidence includes:

- a bounded counter that decreases
- a finite iterator
- a fair blocking wait
- a yield point
- a cancellation check
- a deadline or progress budget
- a trusted local escape hatch

Current progress checking is documented in `25-progress-safety.md`. Strict
concurrency should reuse that machinery rather than inventing a second liveness
system.

## Strict Safe Surface Versus Legacy Raw Surface

The raw primitives remain useful for the runtime, tests, and hand-written
low-level wrappers, but strict-mode user code should prefer the proof-carrying
surface.

Legacy raw calls include:

- `spawn_raw`
- `join_raw`
- `detach_raw`
- direct `spawn1` / `pool_submit1` when a structured scope would work
- `cond_wait`
- `notify_one`
- `notify_all`
- manual atomics outside a protocol wrapper
- unbounded queues or detached workers without a declared lifetime owner

Migration direction:

- use `nursery:` / pool scopes / task groups for ordinary fan-out
- keep escaped tasks as linear `Thread[T, Joinable]` or `Task[T, Pending]`
- use predicate waits instead of raw condition variables
- use bounded channels/queues with capacity tokens
- wrap raw atomics in a named protocol type
- keep low-level implementations in narrow `trusted` blocks or explicit
  unsafe-permission code

The compiler reports these raw calls as deprecations/warnings by default. The
semantic analyzer's `EnforceStrictConcurrency` option, exposed on the CLI as
`-Wconcurrency`, promotes that same diagnostic set to hard errors, letting
projects audit gradually and then turn on the proof-carrying policy for shipped
strict-mode code. In full strict mode, the raw surface should require explicit
unsafe or trusted authority unless the call is inside the trusted runtime
standard library.

## Examples Of The Intended Strict Shape

### Structured task scope

```elisa
def compile_both(left: Module, right: Module) -> Pair[IR, IR]:
    nursery:
        left_task = spawn lower(left)
        right_task = spawn lower(right)
        return Pair(await left_task, await right_task)
```

The nursery owns both tasks. A task cannot be forgotten, and borrowed state
cannot outlive the nursery.

Escaping the scope is still possible, but the handle stays linear:

```elisa
def start_worker(arg: WorkerArg) -> Thread[Result, Joinable]:
    return spawn1(worker, move arg)

def finish_worker(t: Thread[Result, Joinable]) -> Result:
    return join(move t)
```

### Predicate wait queue

```elisa
domain QueueDomain:
    protects Queue.items, Queue.closed
    lock_rank 30
    reentrant false
    wait_policy FIFO

condition NotEmpty(q: Queue) in q.domain:
    q.items.count > 0 or q.closed

condition NotFull(q: Queue) in q.domain:
    q.items.count < q.capacity or q.closed

linear struct QueueSlot[Q]

def reserve_slot[T](q: mutable Queue[T]&) -> QueueSlot[q]:
    await q.NotFull:
        if q.closed:
            raise QueueClosed
        return QueueSlot[q]()

def send[T](q: mutable Queue[T]&, slot: QueueSlot[q], item: T):
    do q.domain:
        _ = move slot
        q.items.push(item)

def recv[T](q: mutable Queue[T]&) -> T?:
    await q.NotEmpty:
        if q.items.count > 0:
            return q.items.pop_front()
        return null
```

This one abstraction carries proofs for race freedom, lost-wakeup freedom,
spurious-wakeup handling, bounded buffering, close protocol, and FIFO wait
policy.

### Atomic protocol wrapper

Raw atomics are too easy to misuse in strict code. A protocol wrapper should
carry the memory-ordering proof.

```elisa
struct OnceCell[T, S]:
    storage: mutable T
    ready: mutable atomic[bool]

def publish[T](cell: mutable OnceCell[T, Empty]&, value: T) -> OnceCell[T, Ready]:
    cell.storage <- value
    store(&cell.ready, true, MemoryOrder.Release)
    return move cell as OnceCell[T, Ready]

def get[T](cell: OnceCell[T, Ready]&) -> T:
    assert load(&cell.ready, MemoryOrder.Acquire)
    return cell.storage
```

Callers use `OnceCell[T, Ready]`; they do not casually choose relaxed memory
orders at every call site.

### Non-reentrant domain and callback control

```elisa
domain AccountDomain:
    protects Account.balance, Account.audit_log
    reentrant false
    callbacks closed

def withdraw(account: mutable Account&, amount: i64, callback: func() -> void):
    do account.domain:
        account.balance <- account.balance - amount
        account.audit_log.push(Withdraw(amount))

    callback()
```

The callback runs after the domain invariant is restored. Calling it inside the
domain would be rejected in strict mode because it could re-enter account state.

### Cancellation and linear cleanup

```elisa
def write_file(path: cstr):
    cancel_scope:
        file: File[Open] = open(path)

        on_cancel:
            close(move file)

        data = await network_read() cancel_point
        write(file, data)
        close(move file)
```

The cancellation point is accepted because every cancellation path consumes the
linear file handle.

## Relationship To The Older Affine Slice

The older affine-thread model below remains valid as the local protocol-state
slice. The update in this section broadens the strict-mode story around it:

- typestate and affine values carry local protocol facts
- domains carry protected-state and ordering facts
- predicate waits carry wait/wakeup facts
- progress summaries carry liveness facts
- permissions and trusted blocks carry authority and escape hatches

That is the intended unified model.

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

### 4. Permission Families

Concurrency authority is represented as permission/capability families. Older text and declaration rows may still use `effects[...]`, but the preferred user-facing vocabulary is permission/capability authority granted by `can ...:` blocks.

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
- `Unsafe.ThreadShare` for deliberately crossing a thread boundary with non-static reference-bearing payloads before a stronger proof exists

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

- `pool workers(8): ...`
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

```elisa
affine struct Thread[T, S]
affine struct Task[T, S]
affine struct MutexGuard[S]
```

Exact token order can still be tuned, but the language needs a visible affine
kind marker.

### Move

Affine values are consumed with an explicit `move`.

```elisa
result: i64 = join(move thread)
task_group_add(&jobs, move task)
```

For copyable values, `move` is allowed but semantically inert.

### Destructuring

To extract affine fields, the whole aggregate must be consumed explicitly.

Implemented spellings:

```elisa
move holder as Holder(thread, count)
return join(move thread)
```

```elisa
move job as Job.Run(thread, priority)
```

```elisa
move node in store as Expr.Add(left, right)
```

Nested payload destructuring is also supported:

```elisa
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

```elisa
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

```elisa
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

```elisa
join(holder.thread)   # reject in phase 1
```

Instead:

```elisa
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

```elisa
type Joinable
type Pending
type Held
```

There is no need for `Joined`, `Awaited`, or `Unlocked` result types in the
first slice because the consuming operations simply destroy the capability.

## Runtime Carriers

```elisa
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

```elisa
affine struct WorkerLease:
    raw: mutable uintptr
```

## Core Operations

### Threads

```elisa
extern spawn1[A, R, permission P](fn: func(A) -> R can[P], arg: A) -> Thread[R, Joinable] can[Thread.Spawn]
extern join[R](t: move Thread[R, Joinable]) -> R can[Thread.Join]
extern detach[R](t: move Thread[R, Joinable]) -> void can[Thread.Detach]
```

### Pools and Tasks

```elisa
extern pool_new(threads: usize) -> ThreadPool can[Pool.Create]
extern pool_shutdown(pool: ThreadPool&) -> void can[Pool.Shutdown]

extern pool_submit1[A, R, permission P](pool: ThreadPool&, fn: func(A) -> R can[P], arg: A) -> Task[R, Pending] can[Pool.Submit]
extern pool_await[R](task: move Task[R, Pending]) -> R can[Pool.Await]

extern task_group_new() -> TaskGroup
extern task_group_add[T](group: TaskGroup&, task: move Task[T, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void can[Pool.WaitAll]
```

### Locks and Condvars

```elisa
extern mutex_lock(mu: Mutex&) -> MutexGuard[Held] can[Sync.Lock]
extern mutex_unlock(g: move MutexGuard[Held]) -> void can[Sync.Unlock]
extern cond_wait(cv: CondVar&, g: move MutexGuard[Held]) -> MutexGuard[Held] can[Sync.Wait]
extern notify_one(cv: CondVar&) -> void can[Sync.Notify]
extern notify_all(cv: CondVar&) -> void can[Sync.Notify]
```

Source-level statement sugar is also available for condition-variable notification:

```elisa
notify one cv
notify all cv
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

```elisa
Expr.Store[Local]
Expr.Store[Frozen]
```

Constructor and publication surface:

```elisa
store: Expr.Store[Local] = Expr.Store(owner)
frozen: Expr.Store[Frozen] = freeze(move store)
```

Rules:

- `Expr.Store(owner)` returns `Expr.Store[Local]`
- `new[store] Expr.Variant(...)` requires `store : Expr.Store[Local]`
- `match node in store:` accepts either `Expr.Store[Local]` or `Expr.Store[Frozen]`
- `move ... as Expr.Variant(...)` and `if ... as Expr.Variant(...)` accept the same nested payload-pattern grammar as `match`
- packed-store provenance is structural and recursive through aggregates,
  arrays, views, helper returns, and destructuring binders
- `freeze(move store)` remaps nested dependencies structurally from
  `Expr.Store[Local]` to `Expr.Store[Frozen]`
- values depending on `Expr.Store[Local]` are not sendable/shareable
- values depending only on `Expr.Store[Frozen]` may be shared if their payload
  shape is otherwise shareable

### Opaque Borrow Contracts

Extern helpers can carry provenance through explicit contracts:

```elisa
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

```elisa
enum MemoryOrder:
    Relaxed
    Acquire
    Release
    AcqRel
    SeqCst
```

```elisa
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

```elisa
pool workers(8):
    ...
```

desugars to:

- `workers: ThreadPool = pool_new(8)`
- block body
- guaranteed `pool_shutdown(&workers)` on every exit path

### Submission

Inside an active pool:

```elisa
t: Task[i64, Pending] = submit work(arg)
```

desugars to:

```elisa
t: Task[i64, Pending] = pool_submit1(&workers, work, arg)
```

When the target pool should be named explicitly, use the bracketed form:

```elisa
t: Task[i64, Pending] = submit[pool] work(arg)
```

which desugars to:

```elisa
t: Task[i64, Pending] = pool_submit1(&pool, work, arg)
```

### Await

```elisa
result: i64 = await task
```

desugars to:

```elisa
result: i64 = pool_await(move task)
```

`await` is the consuming completion surface for a pending task handle. After
`await task`, that task handle has been moved into the await operation and may
not be used again.

### Wait-All

```elisa
wait all jobs
```

desugars to:

```elisa
task_group_wait_all(&jobs)
```

`wait all jobs` is the completion surface for a task group that has accumulated
pending tasks. A task group holding pending tasks must be satisfied with
`wait all group` before scope exit; leaving such a group live is rejected in the
same way dropping a pending `Task[..., Pending]` is rejected.

### Lock Scope

```elisa
lock mu as g:
    body
```

desugars conceptually to:

- `g: MutexGuard[Held] = mutex_lock(&mu)`
- body
- guaranteed `mutex_unlock(move g)` on each exit path

`cond_wait` rebinding remains explicit:

```elisa
lock box.mu as g:
    while not box.has_value:
        g <- cond_wait(&box.cv, move g)
```

## Example Patterns

### 1. Join Exactly Once

```elisa
def run_one() -> i64 can[Thread.Spawn, Thread.Join]:
    t: Thread[i64, Joinable] = spawn1(work, 7)
    return join(move t)
```

### 2. Whole-Value Destructuring of an Affine Aggregate

```elisa
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

```elisa
packed enum Expr:
    Int(value: int)
    Add(left: Expr, right: Expr)

def left_value(node: Expr, store: Expr.Store[Frozen]) -> int:
    if node as Expr.Add(Expr.Int(value), rhs):
        _ = rhs
        return value
    return 0
```

This uses the same recursive payload-pattern surface as `match`, but on the
statement-oriented packed destructuring forms.

### 3. Pool-Scoped Tasks

```elisa
def parallel_sum(data: static i32&, mid: usize, len: usize) -> i64 can[Pool.Submit, Pool.Await, Atomics.Rmw, Atomics.Load]:
    total: atomic[i64] = atomic[i64](0)

    pool workers(8):
        left: Task[i64, Pending] = submit sum_chunk(Chunk(0, mid, data, &total))
        right: Task[i64, Pending] = submit sum_chunk(Chunk(mid, len, data, &total))

        _ = await left
        _ = await right

        return total.load(MemoryOrder.Relaxed)
```

### 4. Task Groups

```elisa
def build_index(paths: static PathJob&, count: usize) -> void can[Pool.Submit, Pool.WaitAll]:
    pool workers(8):
        jobs: TaskGroup = task_group_new()

        i: mutable usize = 0
        while i < count:
            t: Task[void, Pending] = submit parse_and_index(paths[i])
            task_group_add(&jobs, move t)
            i <- i + 1

        wait all jobs
```

### 5. Rejected Affine-Containing Ref

```elisa
struct Holder:
    thread: Thread[i64, Joinable]

def bad(arg: Holder&) -> void:
    pass
```

Rejected because refs to affine-containing values are not supported in phase 1.

### 6. Rejected Stack Submission

```elisa
struct BadJob:
    ptr: stack i64&

def bad() -> i64 can[Pool.Submit, Pool.Await]:
    local: i64 = 7

    pool workers(4):
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
- pending task handles must be consumed, typically by `await task` or by moving them into a `TaskGroup`
- task groups with pending tasks must be completed with `wait all group` before scope exit

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
