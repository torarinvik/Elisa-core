# Extern reconciliation and link-name contracts

This note documents implemented Elisa-core behavior for how `extern`
declarations reconcile with Elisa implementations and how `@link_name` aliases
are validated.

This is current-surface behavior, not a proposal.

## Extern declaration satisfied by Elisa `def`

An `extern` declaration can be satisfied by a same-name Elisa function
definition when signatures match.

```elisa
@c_abi(c)
extern bridge(value: i32) -> i32

def bridge(value: i32) -> i32:
    return value + 1
```

The same reconciliation works when the `def` appears before the `extern`:

```elisa
def bridge(value: i32) -> i32:
    return value + 1

@link_name(native_bridge)
@c_abi(c)
extern bridge(value: i32) -> i32
```

Current rules:

- matching extern and `def` signatures reconcile to one function surface
- ABI metadata like call convention carries onto the implementation-facing type
- `@link_name(...)` metadata from extern declaration is preserved on the symbol

## Signature mismatches are rejected

If an extern declaration and same-name Elisa implementation do not match, the
compiler reports a declaration mismatch.

```elisa
extern bridge(value: i32) -> i32

def bridge(value: u32) -> i32:
    return 1
```

## Duplicate extern declarations

Identical duplicate extern declarations coalesce without error:

```elisa
extern puts(text: u8&) -> int
extern puts(text: u8&) -> int
```

Conflicting duplicate extern declarations are rejected:

```elisa
extern puts(text: u8&) -> int
extern puts(text: u8&) -> u64
```

## `@link_name` alias families

Multiple extern declarations may share one native symbol only when their
declaration signatures agree.

Allowed:

```elisa
@link_name(native_bridge)
extern bridge_one(value: uintptr) -> int

@link_name(native_bridge)
extern bridge_two(value: uintptr) -> int
```

Rejected:

```elisa
@link_name(native_bridge)
extern bridge_one(value: void&) -> int

@link_name(native_bridge)
extern bridge_two(value: uintptr) -> int
```

Function and variable alias collisions are also rejected:

```elisa
@link_name(native_bridge)
extern bridge_value: uintptr

@link_name(native_bridge)
extern bridge_call(value: uintptr) -> int
```

Current rules:

- `@link_name` aliases are validated as one native symbol contract
- aliasing externs that disagree on signature are rejected
- function and variable declarations cannot share one native link symbol
