# Segment-safe backend lowering constraints

This note documents backend constraints that apply to segment-owner-safe
functions in Elisa-core.

These checks are enforced after semantic analysis to prevent hidden backend
dependencies from violating segment-owner contracts.

## Why backend checks exist

Segment-safe annotations express source-level contracts:

- `@segment_agnostic` means no host or guest segment dependency
- `@async_entry` plus `@segment_establishing` means owner is established
  explicitly at entry

Backend code generation can still introduce hidden segment assumptions through
stack-protector or TLS lowering. The backend rejects such lowering patterns.

## Segment-agnostic functions must stay segment-agnostic in IR

```elisa
@segment_agnostic
def hash_only(value: i64) -> i64:
    return value * 31
```

Current rule:

- lowering must not attach stack-protector attributes or emit literal `%fs` or
  `%gs` segment accesses for `@segment_agnostic` functions

If those appear, lowering is rejected because they create hidden `Segment.Host`
dependency not present in source contract.

## Segment-establishing entry functions must be canary-free before establish

```elisa
@async_entry
@segment_establishing
@reentrant_safe
def alarm_handler() -> void:
    return
```

Current rules:

- entry prologue lowering for segment-establishing async handlers must not rely
  on stack-protector segment assumptions before owner establishment
- stack-protector attributes on these functions are rejected in backend checks

## Practical expectation

When annotating functions as segment-agnostic or segment-establishing:

- avoid backend modes that force hidden `%fs` or `%gs` dependencies
- treat backend rejection as contract protection, not just optimization detail

For source-level segment-owner semantics and annotations, see
[27-segment-owner-safety-surface.md](/Users/torarinvikbjarko/Documents/Coding%20Projects/Go%20projects/Elisa-core/docs/27-segment-owner-safety-surface.md).
