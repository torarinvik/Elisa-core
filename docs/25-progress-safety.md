# Progress Safety

Progress safety is Elisa Core's lightweight responsiveness contract. It is not a termination proof system. It catches the practical footguns that freeze UI programs and long-running tools:

- loops with no local progress evidence
- recursive cycles with no progress evidence
- blocking calls that may reach main-thread code
- intentional escape hatches that should be easy to audit

The core rule is:

```text
Progress obligation + local evidence = accepted.
Progress obligation without evidence = diagnostic.
```

## Loop Evidence

An unbounded `while` loop produces a progress obligation.

```elisa
def spin(flag: bool) -> void:
    while flag:
        pass
```

Use `Progress.Tick` or `Progress.Yield` inside the loop body to show that the loop cooperates with a budget or scheduler.

```elisa
def spin(flag: bool, budget: mutable ProgressBudget&) -> void:
    while flag:
        progress_tick(budget) can Progress.Tick, Progress.CheckCancel, Abort.Panic
```

Intentional non-progress code must say so explicitly:

```elisa
def spin_forever(flag: bool) -> void:
    trusted Unsafe.NonProgress:
        while flag:
            pass
```

## Recursion Evidence

Recursive cycles are reported unless at least one function in the cycle provides progress evidence, for example by entering a recursion budget.

```elisa
def visit(node: Node&, budget: mutable ProgressBudget&) -> void:
    progress_enter_recursion(budget) can Progress.EnterRecursion, Progress.CheckCancel, Abort.Panic
    # recurse...
    progress_leave_recursion(budget) can Progress.LeaveRecursion
```

## Blocking Calls

Blocking operations use the `Blocking.*` permission family. Ordinary functions that may block are reported by `-emit progress`.

```elisa
extern wait_for_worker() -> void can[Blocking.Wait]

def wait_for_compile() -> void:
    wait_for_worker()
```

Main-thread functions are stricter. A blocking call reachable from `@main_thread` is a progress error and includes a call path.

```elisa
@main_thread
def on_click() -> void:
    wait_for_compile()
```

Example diagnostic shape:

```text
progress error: @main_thread function may block via Blocking.* permission
path: on_click -> wait_for_compile -> wait_for_worker
```

If blocking on the main thread is truly intentional, make the risk auditable:

```elisa
@main_thread
def on_click() -> void:
    trusted Unsafe.BlockMain:
        wait_for_worker()
```

## Extern Classification

Extern calls can declare progress behavior directly:

```elisa
@blocking
extern waitpid(pid: i32) -> i32

@nonblocking
extern mach_absolute_time() -> u64
```

`@blocking` adds `Blocking.RawExtern` to the extern function. `@nonblocking` is a contract/documentation marker and does not add blocking permissions.

Unknown externs are not currently treated as blocking by default. That keeps today's code low-friction; a stricter mode can make unknown extern progress conservative later.

## Audit Command

Use:

```sh
go run ./src -emit progress path/to/file.elisa
```

Pressure fixtures live in:

```text
compiler/test/progress/
```

They cover unbudgeted loops, budgeted loops, recursive cycles, main-thread blocking, and trusted escape hatches.
