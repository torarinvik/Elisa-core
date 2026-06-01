# Unsafe report and budget surface

This note documents the implemented CLI unsafe-audit surfaces in Elisa-core.

It covers `-emit unsafe`, trusted-unsafe accounting, and `-unsafe-budget`
enforcement.

## Emit unsafe summary

Use:

```sh
go run ./src -emit unsafe path/to/file.elisa
```

Example Elisa input:

```elisa
extern raw_cast(value: uintptr) -> heap u8& can[Unsafe.PointerCast]
extern c_probe() -> i64

def safe_wrapper(value: uintptr) -> heap u8&:
    trusted Unsafe.PointerCast:
        return value.cast[heap u8&]

def safe_ffi_wrapper() -> i64:
    trusted Unsafe.RawExtern:
        return c_probe()
```

Report shape includes:

- total unsafe capability count
- per-capability counts
- per-function unsafe permissions
- `trusted-total` and trusted-use breakdown
- `boundary-invariants` activated by observed triggers

Current text section order is:

1. `=== unsafe ===`
2. `total`
3. `capabilities`
4. `boundary-invariants`
5. `functions`
6. `trusted-total`
7. `trusted`

Current capability rows in the built-in ordered set are emitted as:

- `Unsafe.PointerCast`
- `Unsafe.PointerArithmetic`
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
- `Unsafe.GuestHostPointerCast`
- `Unsafe.PoisonPointerSentinel`
- `Unsafe.TinyPointerSentinel`
- `Unsafe.SegmentMutation`
- `Unsafe.GuestSegmentInstall`
- `Unsafe.CrossThreadSignalJump`
- `Unsafe.MachineCodeBuilder`
- `Unsafe.NonProgress`
- `Unsafe.BlockMain`
- `Unsafe.AssumeProgress`

Additional non-built-in keys are emitted in sorted order after built-in rows.

## Unsafe budget enforcement

Use:

```sh
go run ./src -emit unsafe -unsafe-budget "trusted-total=1,Unsafe.PointerCast=2" path/to/file.elisa
```

Current budget key forms:

- `total`
- `trusted-total`
- `Unsafe.Member`
- bare member keys that map to unsafe capability counters
- non-unsafe capability keys surfaced by report entries

Budget entries support `name=N` and `name<=N` forms.

Budget parser behavior:

- entries are comma-separated
- whitespace around entries and operators is ignored
- invalid or unknown keys fail the command with diagnostics

If a budget is exceeded, CLI exits non-zero with a diagnostic like:

```text
unsafe budget exceeded for trusted-total: 1 > 0
```

## EASM-triggered unsafe report entries

Unsafe reporting also includes EASM-derived requirements, including
`EASM.Requires.*` keys and derived unsafe escape-hatch counters such as:

- `Unsafe.Assembly`
- `Unsafe.TinyPointerSentinel`
- `Unsafe.PoisonPointerSentinel`

Example report function entry shape:

```text
easm:easm_sentinel_jump: EASM.Requires.control.indirect, EASM.Requires.control.tiny_target.unchecked, Unsafe.Assembly, Unsafe.TinyPointerSentinel
```

## Boundary invariant sections

When matching triggers appear, report includes invariant sections such as:

- `GuestHostPointer`
- `ExecutableCodePublish`
- `MachineSegmentState`
- `TinyCallable`
- `ThreadAffineSignalJump`

Each section carries static, trace, and runtime audit rules for that boundary
class.

## Trusted vs surface-function accounting

Current function list under `functions:` reflects callable symbol signatures and
EASM export surfaces. Trusted blocks inside function bodies are tracked
separately under `trusted:` and do not create extra surface function entries.
