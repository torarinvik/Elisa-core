# Concurrency mini-spec

This document proposes an enhanced first concurrency surface for Contextlang / `llcontext`.

The design goal is to keep concurrency **orthogonal** to the language's existing model:

- explicit syntax over hidden runtime magic
- low-level runtime/library primitives over actor / async / channel special cases
- effect declarations for observable thread/synchronization operations
- types and lightweight proofs over borrow-checker-style whole-program ownership analysis
- concise syntax sugar only when it desugars cleanly back into ordinary typed carriers and statements

In short:

> add generic atomics, typed tasks, first-class thread pools, affine protocol capabilities, structured synchronization scopes, and compiler-checked cross-thread transfer rules without turning the language into Rust-lite.

## Design goals

The first concurrency slice should:

- support real low-level threaded and pooled work
- compose with existing storage qualifiers (`any`, `heap`, `stack`, `static`, named regions)
- compose with existing pointer proof state (`&`, `&?`, `!`)
- compose with existing permission/effect tracking (`can[...]`)
- express join/await/lock protocols as typed one-shot capabilities where that buys safety
- keep task/thread lifetime explicit in source
- keep memory-order semantics explicit in source
- allow concise syntax for common parallel patterns
- avoid requiring a borrow checker or closure-capture ownership analysis

## Non-goals for the first slice

The first concurrency slice should **not** try to solve everything.

Intentionally deferred:

- built-in actors
- built-in async / await in the promise/future/executor sense
- built-in channels as a core language primitive
- Rust-like move/borrow analysis
- full data-race freedom for arbitrary shared heap graphs
- a mandatory user-facing `owned` / `shared` / `publish` model
- field annotations like `@guarded_by(mu)` in the first landing
- unrestricted closure capture in pool or thread submission syntax

Those may become worthwhile later, but they should not be prerequisites for useful basic threading and work scheduling.

## Core principle

Concurrency should be modeled as five mostly-independent layers.

### 1. Explicit runtime carriers

Examples:

- `Thread[T]`
- `Task[T]`
- `ThreadPool`
- `TaskGroup`
- `Mutex`
- `MutexGuard`
- `CondVar`
- `atomic[T]`

These are runtime/library-shaped values, not magical compiler ghosts.

### 2. Protocol typestate via affine capabilities

Some concurrency carriers should also be treated as **protocol capabilities**.

That means the type system tracks not just what kind of value something is, but also which operations remain legal on it.

Examples:

- `Thread[T]` means a joinable/detachable thread capability
- `Task[T]` means an awaitable task capability
- `MutexGuard` means a currently-held lock capability

The important design choice is that this typestate should be expressed mostly through:

- affine / one-shot use of selected carriers
- consuming operations such as `join`, `detach`, `await`, and `unlock`
- internal proofs such as `guard_holds(g, mu)` and `active_pool(p)`

rather than through a large visible `owned` / `move` / `shared` surface syntax.

This keeps concurrency typestate close to the language's existing proof-oriented style: resource protocols are typed, but the whole language does not become an ownership calculus.

### 3. Effect families

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
- `Sync.Wait`
- `Sync.Notify`
- `Atomics.Load`
- `Atomics.Store`
- `Atomics.Exchange`
- `Atomics.CompareExchange`
- `Atomics.Rmw`
- `Atomics.Fence`

This matches the existing `can[...]` model and keeps observable concurrency operations explicit.

### 4. Lightweight compiler predicates and proofs

The compiler should internally track predicates such as:

- `sendable(T)` — may a value of type `T` cross a thread or task boundary by value?
- `shareable(T)` — may aliases of type `T` be visible to multiple threads concurrently?
- `atomic_safe(T)` — may `T` be stored in `atomic[T]`?
- `atomic_numeric(T)` — may `T` participate in arithmetic RMW operations such as `fetch_add`?
- `joinable(t)` — does thread handle `t` still represent a one-shot join/detach capability?
- `awaitable(task)` — does task handle `task` still represent a one-shot await capability?
- `guard_holds(g, mu)` — does guard `g` currently prove exclusive access via mutex `mu`?
- `active_pool(p)` — is pool `p` the implicit submission target in the current syntactic scope?
- `pool_open(p)` — may pool `p` still accept submissions in the current flow?

The important design point is that these begin as **compiler-internal checks**, not necessarily user-written type qualifiers.

That keeps the surface syntax small while still allowing the compiler to reject the most dangerous cases.

### 5. Concise structural syntax

Examples:

- `pool workers(8u): ...`
- `submit work(arg)`
- `submit[workers] work(arg)`
- `await task`
- `wait all jobs`
- restricted `parallel for`
- `lock mu as g: ...`

The intent is to let common concurrent patterns look lightweight **without** introducing hidden scheduling semantics or general closure magic.

## Proposed surface syntax

## Permission families

Recommended builtin families:

```context
permission Thread:
    Spawn
    Join
    Detach

permission Pool:
    Create
    Submit
    Await
    WaitAll
    Shutdown

permission Sync:
    Lock
    Unlock
    Wait
    Notify

permission Atomics:
    Load
    Store
    Exchange
    CompareExchange
    Rmw
    Fence
```

Whether every operation ultimately needs all of these as explicit source-level declarations can be tuned later, but this is the right semantic shape.

## Thread and task carriers

Threads and pool tasks should both be plain typed handles.

```context
repr(c) struct Thread[T]
repr(c) struct Task[T]
repr(c) struct ThreadPool
repr(c) struct TaskGroup
```

## Concurrency typestate as protocol capability

The elegant way to push the type system further is **not** to assign every value in the program a concurrency ownership class.

Instead, the language should use typestate primarily for **resource protocols**.

That means:

- pointer/storage typestate answers where data lives and how refs may be used
- concurrency typestate answers which operations remain legal on a thread/task/guard/pool capability

Recommended rule of thumb:

> use concurrency typestate for resources and synchronization protocols, not for arbitrary dynamic scheduler state.

In practice that means the first version should model things like:

- whether a thread handle may still be `join`ed or `detach`ed
- whether a task handle may still be `await`ed
- whether a guard still proves the mutex is held
- whether a pool scope currently provides an implicit active submission target

but it should **not** try to encode things like:

- whether a task has already completed at runtime
- whether a thread is currently running vs parked
- whether a particular compare-exchange will succeed

Those are dynamic runtime facts, not good first-class static typestate.

### Recommended affine carriers

The first concurrency slice should treat at least these carriers as affine / one-shot protocol capabilities:

- `Thread[T]`
- `Task[T]`
- `MutexGuard`

It may later become useful to apply similar treatment to `TaskGroup` or explicit pool shutdown tokens, but the initial payoff is strongest for threads, tasks, and guards.

### Why affine use is the right fit

Affine use gives the language most of the value of typestate without forcing a full Rust-like ownership model.

It allows the compiler to reject mistakes such as:

- joining the same thread twice
- detaching after join
- awaiting the same task twice
- unlocking the same guard twice
- using a consumed guard after `cond_wait(...)` without rebinding the new returned guard

That is exactly the sort of proof-native safety improvement this language can exploit elegantly.

### Carrier-state summary

Recommended mental model:

- `Thread[T]` = joinable or detachable thread capability
- `Task[T]` = awaitable task capability
- `MutexGuard` = lock-held proof capability
- scoped `pool workers(...):` = introduces both a real `ThreadPool` carrier and an `active_pool(workers)` proof for the block

This avoids spelling states in types like `Thread[T, Joinable]` unless later experience proves those extra visible state parameters are worth the noise.

### Raw thread operations

```context
extern spawn1[A, R](fn: func(A) -> R, arg: A) -> Thread[R] can[Thread.Spawn]
extern join[R](t: Thread[R]) -> R can[Thread.Join]
extern detach[R](t: Thread[R]) -> void can[Thread.Detach]
```

### Pool operations

```context
extern pool_new(threads: usize) -> ThreadPool can[Pool.Create]
extern pool_shutdown(pool: ThreadPool&) -> void can[Pool.Shutdown]

extern pool_submit1[A, R](pool: ThreadPool&, fn: func(A) -> R, arg: A) -> Task[R] can[Pool.Submit]
extern pool_await[R](task: Task[R]) -> R can[Pool.Await]

extern task_group_new() -> TaskGroup
extern task_group_add[T](group: TaskGroup&, task: Task[T]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void can[Pool.WaitAll]
```

These handles are:

- explicit in source
- easy to lower into runtime APIs
- orthogonal to async/futures/executors
- pleasant targets for syntax sugar

They should also carry **protocol meaning**:

- `Thread[T]` is consumed by `join` or `detach`
- `Task[T]` is consumed by `pool_await`
- `MutexGuard` is consumed by `mutex_unlock` and by the input side of `cond_wait`

That makes these APIs typestate-carrying without requiring a giant explicit state-parameter system.

### Typing rule

For `spawn1(fn, arg)` or `pool_submit1(pool, fn, arg)` to be legal:

- the argument type `A` must satisfy `sendable(A)`
- the result type `R` must satisfy `sendable(R)`
- the callee's declared permission set still applies normally

This is the first and most important cross-thread/task safety rule.

### Consumption rule

The compiler should additionally treat selected operations as consuming protocol capabilities.

Recommended first rules:

- `join(t)` consumes `t`
- `detach(t)` consumes `t`
- `pool_await(task)` consumes `task`
- `mutex_unlock(g)` consumes `g`
- `cond_wait(cv, g)` consumes the old `g` and returns a fresh guard

So code such as “join twice”, “await twice”, or “use old guard after wait” becomes a natural static error.

## Generic atomics from day one

Atomics should be generic immediately at the source level.

```context
repr(c) struct atomic[T]
```

This keeps the language surface small and expressive while still allowing the compiler to restrict which `T` values are actually legal inside an atomic.

### Memory order

Use the standard C11 / LLVM memory orders directly:

```context
enum MemoryOrder:
    Relaxed
    Acquire
    Release
    AcqRel
    SeqCst
```

The language should not invent a second naming scheme here.

### Primitive operations

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

Method sugar is also desirable:

```context
counter.fetch_add(1u64, MemoryOrder.Relaxed)
head.compare_exchange(old, next, MemoryOrder.Release, MemoryOrder.Relaxed)
total.load(MemoryOrder.Acquire)
```

### `atomic_safe(T)` initial rule

Recommended initial allow-set:

- integers
- bool
- char
- payloadless enums
- refs / nullable refs whose representation is word-sized and explicitly allowed by the implementation

Recommended initial reject-set:

- arbitrary structs
- dynamic containers
- strings and views as aggregate values
- region marks/checkpoints
- values whose representation or semantics would imply hidden locking or tearing hazards

### `atomic_numeric(T)` initial rule

Recommended initial allow-set:

- signed integers
- unsigned integers
- possibly pointer-like refs for `fetch_add`/`fetch_sub` only if the language explicitly wants atomic pointer arithmetic

Address-based `wait` / `wake` style primitives may be added later once the basic atomic layer exists.

## Thread pools as first-class language-facing carriers

Thread pools should be a central part of the design, not a mere afterthought.

The language should therefore provide both:

- ordinary explicit pool APIs
- structural syntax for scoped pool usage and task submission

### Scoped pool syntax

Recommended statement form:

```context
pool workers(8u):
    ...
```

Operational meaning:

- create a `ThreadPool`
- bind it as `workers`
- make it the implicit active pool in the block
- shut it down on every exit path from the block

This should also introduce an internal `active_pool(workers)` proof and, while the pool is live, a `pool_open(workers)` proof for submission operations in the block.

This is to pools what `region scratch(...)` is to arenas/regions: structured setup/teardown with a real bound value.

### Explicit long-lived pools

The explicit value form must still exist:

```context
workers: ThreadPool = pool_new(8u)
...
pool_shutdown(&workers)
```

The scoped syntax is sugar, not a separate runtime model.

## Task submission and waiting sugar

### Submission inside an active pool

Inside a `pool workers(...):` scope:

```context
t: Task[i64] = submit work(arg)
```

desugars to:

```context
t: Task[i64] = pool_submit1(workers, work, arg)
```

### Submission to an explicit pool

Outside an active pool, or when multiple pools exist:

```context
t: Task[i64] = submit[workers] work(arg)
```

### Await syntax

```context
result: i64 = await task
```

desugars to:

```context
result: i64 = pool_await(task)
```

This is intentionally *not* async/await in the promise/future sense. It is just typed task-join sugar.

Because `Task[T]` is an affine awaitable capability, `await task` should consume `task` exactly once.

### Wait-all syntax

```context
wait all jobs
```

desugars to:

```context
task_group_wait_all(jobs)
```

## Mutexes and guards

Mutexes should remain runtime/library carriers, but the language should provide **scope syntax** for them.

### Runtime carriers

```context
repr(c) struct Mutex
repr(c) struct MutexGuard
repr(c) struct CondVar

extern mutex_lock(mu: Mutex&) -> MutexGuard can[Sync.Lock]
extern mutex_unlock(g: MutexGuard) -> void can[Sync.Unlock]
extern cond_wait(cv: CondVar&, g: MutexGuard) -> MutexGuard can[Sync.Wait]
extern notify_one(cv: CondVar&) -> void can[Sync.Notify]
extern notify_all(cv: CondVar&) -> void can[Sync.Notify]
```

### Statement-form lock scope

Recommended syntax:

```context
lock expr as guard_name:
    ...
```

Example:

```context
lock q.mu as g:
    q.count <- q.count + 1
```

Operational meaning:

- evaluate the mutex expression once
- lock it at block entry
- bind the resulting guard to `guard_name`
- automatically unlock on every exit path from the block

This fits the existing language style better than forcing all users to manually write raw `lock`/`unlock` calls.

### Why statement syntax matters

Without statement syntax, code becomes error-prone quickly:

- easy to forget `unlock` on early return
- easy to duplicate unlock logic on every branch
- harder to reason about because the scope is implicit

A lock block is a good example of syntax earning its keep.

## Condition-variable wait

A wait operation should consume and return the guard.

```context
extern cond_wait(cv: CondVar&, g: MutexGuard) -> MutexGuard can[Sync.Wait]
```

This is the right shape because waiting:

- releases the mutex before sleeping
- reacquires the mutex before returning

Making that visible in the type shape is better than hiding it in comments.

This is one of the clearest wins from concurrency typestate:

- before the call, `g` proves the lock is held
- during the wait, that proof is temporarily surrendered
- after the call returns, the new `g` re-establishes the proof

That matches the actual mutex protocol exactly.

Example:

```context
lock box.mu as g:
    while not box.has_value:
        g <- cond_wait(&box.cv, g)
    return box.value
```

## Thread-local globals

`threadlocal` should be a built-in global storage modifier.

Example:

```context
threadlocal global mutable scratch_rng_state: u64 = 88172645463325252u64
```

This is both useful and orthogonal:

- no ownership system required
- no actor model required
- directly maps onto platform/runtime TLS support

## Compiler-internal transfer and sharing rules

The key safety rule is:

> thread creation, task submission, and other cross-thread publication points consult compiler-internal `sendable(T)` / `shareable(T)` predicates.

These predicates do **not** need to be user-facing syntax in the first version.

## `sendable(T)` in the first version

Recommended initial rule set.

### Allowed by default

- integers, floats, bools, chars
- fixed arrays whose element types are sendable
- structs whose fields are all sendable
- enums whose payloads are all sendable
- `static T&` and `static T&?`
- thread handles, task handles, pool handles, and synchronization carriers themselves
- `atomic[T]` when `atomic_safe(T)` holds

### Rejected by default

- `stack T&`, `stack T&?`
- named-region refs such as `scratch T&`, `parse Node&`, etc.
- `any T&`, `any T&?`
- plain mutable `heap T&` and `heap T&?`
- region checkpoints / marks
- mutex guards

This deliberately rejects many subtle cases early.

### Why reject raw mutable heap refs initially?

Because once mutable heap references can freely cross threads, the language needs either:

- a uniqueness / transfer discipline
- a publication discipline
- or a synchronized-wrapper proof system strong enough to justify concurrent access

That is larger than the first concurrency slice should attempt.

So the first rule should be conservative:

> plain mutable heap-backed references do not cross threads or pool tasks by default.

## `shareable(T)` in the first version

`shareable(T)` matters when the same object may be visible from multiple threads concurrently.

Recommended initial rule set:

### Shareable by default

- `static` immutable data
- `atomic[T]` where `atomic_safe(T)` holds
- mutexes / condvars / semaphores / barriers
- thread-safe runtime carrier types explicitly blessed by the compiler

### Not shareable by default

- raw `any` refs
- stack refs
- named-region refs
- mutable heap refs
- mutable dynamic container carriers unless wrapped in synchronization

## Readonly publication

Readonly publication is important, but it should be staged.

The eventual direction should likely involve either:

- `freeze(value)`
- or a dedicated readonly publication helper that yields `dview[T]`, `sview`, or other readonly carriers

However, this is better treated as **stage 2 or 3**, not as a prerequisite for stage 1.

That means the first version can already be useful without introducing `shared` or `publish` as source keywords.

## Guard proofs and protected access

The first landing does **not** need field-level `@guarded_by(mu)` annotations.

That feature is powerful, but compiler-expensive.

### Recommended first approach

Keep the first proof model simple:

- `lock ... as g:` proves that a guard `g` exists
- `g` should be treated as an affine capability, not a copyable token
- APIs may explicitly require a guard where appropriate
- later, a helper form such as `access(g, &field)` may yield a temporary proof-qualified reference

This is a better first step than pervasive field annotations because it puts the protocol proof on the operation boundary, where the compiler can reason about it most clearly.

### Preferred future direction

If the design later wants even more proof power, it should build outward from guards and capabilities rather than from global ownership categories.

Good candidates:

- helper forms like `access(g, &field)`
- proof-qualified protected references
- readonly publication facts tied to atomic acquire/release edges

Less attractive first candidates:

- blanket `shared` qualifiers on ordinary values
- mandatory move syntax on unrelated code
- typestating arbitrary runtime scheduler phases

Possible later shape:

```context
s: guarded QueueState& = access(g, &q.state)
```

This would fit the language's existing “proof-like state in the type” style, but it should be a later improvement, not part of the minimum viable lock/pool model.

## Restricted `parallel for`

If the language wants a higher-level piece of sugar early, the best candidate is a **restricted** `parallel for`.

Recommended shape:

```context
parallel for chunk in split_range(0u, count, 4096u):
    process_chunk(ChunkJob(chunk.begin, chunk.end, data, &total))
```

Design constraints:

- lowers to repeated `submit` into the active pool
- implicitly creates a `TaskGroup`
- ends with an implicit `wait all`
- only allowed when the loop body can be lowered into an ordinary function call / submission form
- does **not** imply arbitrary closure capture

This keeps the sugar pleasant while preserving the language's explicitness.

## Example patterns

## 1. Generic atomic counter

```context
repr(c) struct atomic[T]

enum MemoryOrder:
    Relaxed
    Acquire
    Release
    AcqRel
    SeqCst

extern fetch_add[T](slot: atomic[T]&, value: T, order: MemoryOrder) -> T can[Atomics.Rmw]

global mutable next_id: atomic[u64] = atomic[u64](1u64)

def alloc_id() -> u64 can[Atomics.Rmw]:
    return fetch_add(&next_id, 1u64, MemoryOrder.Relaxed)
```

Or with method sugar:

```context
def alloc_id() -> u64 can[Atomics.Rmw]:
    return next_id.fetch_add(1u64, MemoryOrder.Relaxed)
```

## 2. Generic atomic pointer / nullable ref

```context
repr(c) struct JobNode:
    next: heap JobNode&?
    value: i64

repr(c) struct WorkStack:
    head: atomic[heap JobNode&?]

extern compare_exchange[T](slot: atomic[T]&, expected: T, desired: T, success: MemoryOrder, failure: MemoryOrder) -> bool can[Atomics.CompareExchange]
extern load[T](slot: atomic[T]&, order: MemoryOrder) -> T can[Atomics.Load]

def try_push(stack: WorkStack&, node: heap JobNode&) -> bool can[Atomics.Load, Atomics.CompareExchange]:
    old_head: heap JobNode&? = load(&stack.head, MemoryOrder.Acquire)
    node.next <- old_head
    return compare_exchange(&stack.head, old_head, node, MemoryOrder.Release, MemoryOrder.Relaxed)
```

## 3. Pool-scoped parallel sum

```context
repr(c) struct atomic[T]
repr(c) struct ThreadPool
repr(c) struct Task[T]

repr(c) struct Chunk:
    begin: usize
    end: usize
    data: static i32&
    out: atomic[i64]&

def sum_chunk(job: Chunk) -> i64 can[Atomics.Rmw]:
    i: mutable usize = job.begin
    local: mutable i64 = 0

    while i < job.end:
        local <- local + job.data[i].i64()
        i <- i + 1u

    discard job.out.fetch_add(local, MemoryOrder.Relaxed)
    return local

def parallel_sum(data: static i32&, mid: usize, len: usize) -> i64 can[Pool, Atomics]:
    total: atomic[i64] = atomic[i64](0)

    pool workers(8u):
        left: Task[i64] = submit sum_chunk(Chunk(0u, mid, data, &total))
        right: Task[i64] = submit sum_chunk(Chunk(mid, len, data, &total))

        discard await left
        discard await right

        return total.load(MemoryOrder.Relaxed)
```

## 4. Explicit long-lived pool

```context
def compile_many(files: static FileJob&, count: usize) -> void can[Pool]:
    workers: ThreadPool = pool_new(12u)

    i: mutable usize = 0
    while i < count:
        task: Task[int] = submit[workers] compile_one(files[i])
        discard await task
        i <- i + 1u

    pool_shutdown(&workers)
```

## 5. Task groups

```context
def build_index(paths: static PathJob&, count: usize) -> void can[Pool]:
    pool workers(8u):
        jobs: TaskGroup = task_group_new()

        i: mutable usize = 0
        while i < count:
            t: Task[void] = submit parse_and_index(paths[i])
            task_group_add(&jobs, t)
            i <- i + 1u

        wait all jobs
```

## 6. Pools and lock scopes together

```context
repr(c) struct SharedLog:
    mu: Mutex
    count: mutable usize

def worker(log: SharedLog&) can[Sync.Lock, Sync.Unlock]:
    lock log.mu as g:
        log.count <- log.count + 1u

def run_workers(log: SharedLog&) can[Pool, Sync]:
    pool workers(4u):
        a: Task[void] = submit worker(log)
        b: Task[void] = submit worker(log)
        c: Task[void] = submit worker(log)

        discard await a
        discard await b
        discard await c
```

## 7. Mailbox wait loop

```context
repr(c) struct Mailbox:
    mu: Mutex
    cv: CondVar
    has_value: mutable bool
    value: mutable i32

def recv(box: Mailbox&) -> i32 can[Sync]:
    lock box.mu as g:
        while not box.has_value:
            g <- cond_wait(&box.cv, g)

        box.has_value <- false
        return box.value
```

## 8. Restricted `parallel for`

```context
def parallel_sum2(data: static i32&, count: usize) -> i64 can[Pool, Atomics]:
    total: atomic[i64] = atomic[i64](0)

    pool workers(8u):
        parallel for chunk in split_range(0u, count, 4096u):
            sum_chunk(Chunk(chunk.begin, chunk.end, data, &total))

        return total.load(MemoryOrder.Relaxed)
```

## 9. Rejected stack-ref submission

```context
repr(c) struct BadJob:
    ptr: stack i64&

def use_bad(job: BadJob) -> i64:
    return job.ptr[0u]

def bad() -> i64 can[Pool]:
    local: i64 = 7

    pool workers(4u):
        t: Task[i64] = submit use_bad(BadJob(&local))
        return await t
```

This should be rejected because `BadJob` is not `sendable`.

## 10. Rejected region-backed submission

```context
repr(c) struct ParseJob:
    node: scratch Node&

def parse_one(job: ParseJob) -> i64:
    return job.node.value

def also_bad() -> i64 can[Pool]:
    region scratch(1024u)
    node: scratch Node& = new[scratch] Node(123)

    pool workers(4u):
        t: Task[i64] = submit parse_one(ParseJob(node))
        return await t
```

This should also be rejected because named-region refs must not silently cross task/thread boundaries.

## Lowering model

This proposal lowers cleanly onto a platform/runtime layer.

### Threads

- `spawn1` lowers to a runtime thread creation helper or direct C ABI thunk
- `join` lowers to runtime join
- `detach` lowers to runtime detach

### Pools and tasks

- `pool workers(n):` lowers to `pool_new(n)` plus guaranteed `pool_shutdown(&workers)` on scope exit
- `submit work(arg)` lowers to `pool_submit1(active_pool, work, arg)`
- `submit[workers] work(arg)` lowers to `pool_submit1(workers, work, arg)`
- `await task` lowers to `pool_await(task)`
- `wait all group` lowers to `task_group_wait_all(group)`
- restricted `parallel for` lowers to repeated submit + implicit task group + wait-all

### Lock blocks

A statement like:

```context
lock mu as g:
    body
```

can lower roughly as:

```text
g = mutex_lock(&mu)
try:
    body
finally:
    mutex_unlock(g)
```

except expressed in the compiler's normal structured CFG lowering rather than a source-level `try/finally` construct.

### Atomics

Atomic operations lower directly to:

- LLVM atomic instructions where appropriate
- or runtime intrinsics/helpers with explicit memory orders

### Thread-local globals

Thread-local globals lower onto target TLS support.

## What remains library-level

The following should remain mostly library/runtime concepts:

- mutex types themselves
- rwlocks
- condvars
- semaphores
- barriers
- thread pools as runtime engines beneath the source sugar
- lock-free queues / stacks / work-stealing runtimes

The language should only provide the minimum structure needed to make those libraries safer, clearer, and more concise.

## Deferred extensions

The following extensions are intentionally deferred.

## `freeze` / readonly publication

Useful later when the language wants to bless a path from mutable owned data to shareable readonly views.

## user-visible `shared`

Potentially useful later, but not required for the first slice if `sendable(T)` / `shareable(T)` stay compiler-internal.

## `owned` / `move`

Potentially useful later for unique mutable transfer across threads, but should not block the first implementation.

## `@guarded_by(mu)`

A valuable future direction once guard proofs and protected field access have a stable shape.

## address-based wait / wake

Likely worth adding after the basic atomic layer exists.

## unrestricted closure capture

Possible later, but not needed to get useful thread and pool programming immediately.

## lock-free publication primitives

Possible later surface ideas:

- `publish`
- hazard-pointer / epoch helpers
- `Arc`-style library conventions

But not required for the first thread/pool/mutex/atomic landing.

## Recommended implementation order

### Stage 1 — minimum viable concurrency

Implement:

- builtin permission families for `Thread`, `Pool`, `Sync`, and `Atomics`
- runtime carriers: `Thread`, `Task`, `ThreadPool`, `TaskGroup`, `Mutex`, `MutexGuard`, `CondVar`, `atomic[T]`
- `spawn1`, `join`, `detach`
- `pool_new`, `pool_shutdown`, `pool_submit1`, `pool_await`
- `task_group_new`, `task_group_add`, `task_group_wait_all`
- `MemoryOrder`
- `lock ... as ...:` statement form
- `pool name(n):` statement form
- `submit`, `submit[pool]`, `await`, `wait all`
- guard-consuming `cond_wait`
- `threadlocal` globals
- internal `sendable(T)`, `shareable(T)`, `atomic_safe(T)`, and `atomic_numeric(T)` checks
- affine consumption checks for `Thread[T]`, `Task[T]`, and `MutexGuard`
- internal proofs such as `joinable(t)`, `awaitable(task)`, `guard_holds(g, mu)`, `active_pool(p)`, and `pool_open(p)`

This stage already gives real value.

### Stage 2 — more concise structured parallelism

Add one or more of:

- restricted `parallel for`
- helper forms like `access(g, &field)`
- stronger protocol-proof diagnostics around consumed handles/guards
- stronger diagnostics around unsynchronized mutable global access
- polished atomic method syntax if not already present

### Stage 3 — readonly publication

Add:

- `freeze` or equivalent readonly publication
- broader compiler-known `shareable(T)` rules for readonly views and strings

### Stage 4 — optional richer ownership/publication syntax

Only if still justified, add:

- `owned`
- `move`
- user-visible `shared`
- field-level guarded annotations such as `@guarded_by`

This staging keeps the language practical while avoiding a premature leap into Rust-like ownership machinery.

## Recommended slogan

If the design needs one compact summary, it is this:

> Contextlang concurrency should be built from generic atomics, typed threads/tasks/pools, explicit synchronization scopes, concise scheduling sugar, and lightweight compiler proofs about what may cross threads.

Or, with the typestate angle made explicit:

> Contextlang concurrency should use generic atomics, typed threads/tasks/pools, affine protocol capabilities, explicit synchronization scopes, and lightweight compiler proofs instead of a heavyweight ownership sub-language.

That keeps it aligned with the rest of the language: explicit, low-level, proof-oriented, concise where it helps, and orthogonal.
