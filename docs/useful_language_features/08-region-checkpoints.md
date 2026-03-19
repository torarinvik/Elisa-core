# Region checkpoints

The language now supports statement-form region checkpoints on top of the existing `region`, `new[...]`, and `destroy` operations.

## Syntax

```context
region scratch(1024u)

mark scratch as cp
temp: any i32& = new[scratch] 1
restore scratch from cp

reset scratch
destroy scratch
```

Supported forms:

- `mark <region> as <checkpoint>`
- `restore <region> from <checkpoint>`
- `reset <region>`

All three are statements, not expressions.

## Operational meaning

### `mark`

`mark scratch as cp` captures the current allocation position of `scratch` into a named checkpoint.

### `restore`

`restore scratch from cp` rewinds `scratch` back to the point captured by `cp`.

This is a region-local operation:

- the checkpoint must belong to the same region
- the checkpoint must still be valid
- rewinding invalidates allocations made after that checkpoint

### `reset`

`reset scratch` clears the region back to its initial empty state, but keeps the region itself alive and reusable.

This is different from `destroy scratch`, which ends the region lifetime entirely.

## Conservative safety rule

The current compiler tracks locals that directly come from `new[region]` and simple copies/casts of those references.

After `restore`, `reset`, or `destroy`, those tracked locals become invalid if their allocation may have been rewound.

That means code like this is rejected:

```context
def bad() -> i32:
    region scratch
    mark scratch as cp
    value: any i32& = new[scratch] 1
    restore scratch from cp
    return value[0u]
```

Because `value` points into storage that no longer logically exists.

The analysis is intentionally conservative:

- direct region-backed locals are tracked
- straightforward copies/casts of those refs are tracked
- this is **not** yet a full alias/lifetime system

So the feature is safe for common parser/compiler scratch patterns, while leaving room for stronger alias analysis later.

## Nested checkpoints

Nested checkpoints are allowed.

```context
def nested(seed: i32) -> i32:
    region scratch(1024u)
    base: any i32& = new[scratch] seed
    baseline: i32 = base[0u]

    mark scratch as outer
    stable: any i32& = new[scratch] seed + 1

    mark scratch as inner
    temp: any i32& = new[scratch] seed + 2
    restore scratch from inner

    kept: i32 = stable[0u]
    restore scratch from outer

    reset scratch
    final: any i32& = new[scratch] seed + 3
    return baseline + kept + final[0u]
```

Restoring an older checkpoint invalidates newer checkpoints from the same region.

## Lowering model

The implementation lowers directly to runtime helpers that already exist in `arena.llcontext`:

- `mark` → `arena_snapshot(...)`
- `restore` → `arena_rewind(...)`
- `reset` → `arena_reset(...)`

Internally, checkpoints use the runtime `ArenaMark` struct, but the source-language surface remains statement-oriented.