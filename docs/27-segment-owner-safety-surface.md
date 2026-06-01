# Segment owner safety surface

This note documents the implemented Elisa-core surface for segment owner
contracts, async entry points, and reentrant safety.

This is a current-surface reference, not a proposal.

## Scope

The compiler tracks an ambient segment owner and checks host or guest
requirements at call sites. Segment-changing operations must be explicit in
both permissions and annotations.

## Annotations

### `@async_entry`

Marks a function that can be entered asynchronously.

```elisa
@async_entry
@segment_establishing
@reentrant_safe
def alarm_handler() -> void:
    return
```

Current rules:

- `@async_entry` takes no arguments
- an `@async_entry` function must also be either `@segment_agnostic` or `@segment_establishing`
- an `@async_entry` function must also be `@reentrant_safe`

### `@segment_agnostic`

Marks code that must not depend on host or guest segment owner assumptions.

```elisa
@segment_agnostic
def hash_only(value: i64) -> i64:
    return value * 31
```

Current rules:

- `@segment_agnostic` takes no arguments
- segment-agnostic code cannot grant or signal segment-owner capabilities
- segment-agnostic code cannot call functions that require `Segment.Host`, `Segment.Guest`, or `Unsafe.SegmentMutation`

### `@segment_establishing`

Marks an entry function that establishes segment ownership before performing
segment-owner-specific work.

```elisa
@async_entry
@segment_establishing
@reentrant_safe
def interrupt_entry() -> void:
    return
```

Current rules:

- `@segment_establishing` takes no arguments
- used with `@async_entry` to avoid entering with unknown owner assumptions

### `@segment_transition(host|guest)`

Declares the owner after an extern segment mutation call.

```elisa
@segment_transition(guest)
extern load_guest() -> void can[Unsafe.SegmentMutation, Segment.Guest]

@segment_transition(host)
extern restore_host() -> void can[Unsafe.SegmentMutation, Segment.Host]
```

Current rules:

- requires exactly one argument
- allowed values are `host` or `guest`
- externs that mutate active segment state must carry an explicit transition annotation

### `@reentrant_safe`

Marks functions that are safe for asynchronous and reentrant paths.

```elisa
@reentrant_safe
def interrupt_log(counter: atomic[i64]&) -> void:
    counter.store(1, .release)
```

Current rules:

- `@reentrant_safe` takes no arguments
- a `@reentrant_safe` function cannot call non-`@reentrant_safe` functions
- `@reentrant_safe` code cannot enter `lock` blocks
- `@reentrant_safe` code cannot spawn pool work
- `@reentrant_safe` code cannot use `parallel for`

## Permission families used by segment safety

The following permission rows participate in segment-owner checks:

- `Segment.Host`
- `Segment.Guest`
- `Unsafe.SegmentMutation`
- `Unsafe.GuestSegmentInstall`

Example:

```elisa
@segment_transition(guest)
extern load_guest_fs() -> void can[Unsafe.SegmentMutation, Segment.Guest]

extern guest_only() -> void can[Segment.Guest]
extern host_only() -> void can[Segment.Host]

def run_guest_then_host() -> void:
    can Unsafe.SegmentMutation, Segment.Guest:
        load_guest_fs()
        can Segment.Guest:
            guest_only()

    can Segment.Host:
        host_only()
```

## Ambient owner checks

Calls are checked against current ambient owner facts.

If ambient owner is guest:

- calls requiring `Segment.Guest` are allowed with local grant
- calls requiring `Segment.Host` are rejected as owner mismatch

If ambient owner is unknown:

- owner-specific calls are rejected until owner is established

## Practical migration notes

- if an extern mutates segment state, add `@segment_transition(host)` or `@segment_transition(guest)` instead of relying on permissions alone
- for async entry points, treat `@reentrant_safe` plus one of `@segment_agnostic` or `@segment_establishing` as required baseline annotations
