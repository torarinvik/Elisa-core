# Builtin permission catalog

This note documents the builtin permission families registered by the current
Elisa-core compiler.

Source of truth is semantic builtin registration, not proposal docs.

## Families and members

`Memory`

- `Memory.Allocate`
- `Memory.Release`

`Console`

- `Console.Format`
- `Console.Write`

`Abort`

- `Abort.Exit`
- `Abort.Panic`

`Thread`

- `Thread.Spawn`
- `Thread.Join`
- `Thread.Detach`
- `Thread.Yield`
- `Thread.Sleep`

`Pool`

- `Pool.Create`
- `Pool.Submit`
- `Pool.Await`
- `Pool.WaitAll`
- `Pool.Shutdown`

`Sync`

- `Sync.Lock`
- `Sync.Unlock`
- `Sync.Wait`
- `Sync.Notify`

`Atomics`

- `Atomics.Load`
- `Atomics.Store`
- `Atomics.Exchange`
- `Atomics.CompareExchange`
- `Atomics.Rmw`
- `Atomics.Fence`

`Progress`

- `Progress.Tick`
- `Progress.Yield`
- `Progress.CheckCancel`
- `Progress.EnterRecursion`
- `Progress.LeaveRecursion`
- `Progress.Deadline`
- `Progress.Budget`

`Blocking`

- `Blocking.Wait`
- `Blocking.Join`
- `Blocking.Lock`
- `Blocking.Sleep`
- `Blocking.IO`
- `Blocking.RawExtern`
- `Blocking.UnknownCall`

`Perf`

- `Perf.HotLoop`

`Segment`

- `Segment.Host`
- `Segment.Guest`

`Global`

- `Global.Read`
- `Global.Write`

`Unsafe`

- `Unsafe.PointerCast`
- `Unsafe.PointerArithmetic`
- `Unsafe.GuestHostPointerCast`
- `Unsafe.IndirectCall`
- `Unsafe.UncheckedIndex`
- `Unsafe.RawExtern`
- `Unsafe.MutableGlobal`
- `Unsafe.ThreadShare`
- `Unsafe.StaleRef`
- `Unsafe.Alias`
- `Unsafe.BufferReinterpret`
- `Unsafe.Assembly`
- `Unsafe.ExecutableCodePublish`
- `Unsafe.MachineCodeBuilder`
- `Unsafe.SegmentMutation`
- `Unsafe.GuestSegmentInstall`
- `Unsafe.NonProgress`
- `Unsafe.BlockMain`
- `Unsafe.AssumeProgress`
- `Unsafe.Leak`

## Example declaration surface

```elisa
extern wait_for_worker() -> void can[Blocking.Wait]
extern fence(order: MemoryOrder) -> void can[Atomics.Fence]
extern install_guest_fs() -> void can[Unsafe.GuestSegmentInstall]

def on_click() -> void:
    can Blocking.Wait:
        wait_for_worker()
```

## Notes

- permission rows are surfaced in signatures with `can[...]` and granted locally with `can ...:`
- the old `effects[...]` signature spelling has been removed; capability sets are declared with `alias` and required via `can[...]`, over the same semantic authority model as the families above
