# Region checkpoints, scope statements, and rollback blocks

The current compiler supports two related statement families on top of the
existing `region`, `new[...]`, and `destroy` operations:

- region-local `mark` / `restore ... from ...` / `reset`
- statement-form `scope`, named checkpoints, grouped checkpoints, and `restore name`

## Syntax

```elisa
region scratch(1024u)

mark scratch as cp
temp: scratch i32& = new[scratch] 1
restore scratch from cp

reset scratch
destroy scratch
```

```elisa
scope pool_new(2):
    pass

checkpoint mark = items:
    items.push(4)

checkpoint xs, ys:
    xs.push(5)
    ys.push(6)

restore mark
```

Supported forms:

- `scope <expr>:`
- `checkpoint <name> = <target>:`
- `checkpoint <target1>, <target2>, ...:`
- `restore <checkpoint-name>`
- `mark <region> as <checkpoint>`
- `restore <region> from <checkpoint>`
- `reset <region>`

All three are statements, not expressions.

## Scope statements

`scope expr:` evaluates a scoped resource expression and runs the nested body
with the compiler's ordinary scoped-cleanup machinery.

```elisa
scope pool_new(2):
    pass
```

This is the current explicit block form for surfaces such as thread-pool
acquisition. The body is still an ordinary statement block; `scope` is about
resource lifetime and cleanup, not a new expression form.

## Named and grouped checkpoints

Named checkpoints snapshot rollback state for a checkpointable target and bind
that snapshot to a name.

```elisa
checkpoint mark = items:
    items.push(4)

restore mark
```

Grouped checkpoints create an anonymous rollback block over more than one
target.

```elisa
checkpoint xs, ys:
    xs.push(5)
    ys.push(6)
```

Current rules:

- named checkpoints use `checkpoint name = target:`
- anonymous grouped checkpoints require at least two targets
- current checkpoint targets are regions and mutable `darray` values
- `restore name` rewinds the named checkpoint bound earlier in the same scope
- the formatter normalizes related loop syntax such as `for rev value in items:` to the canonical `for value in rev(items):`

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

```elisa
def bad() -> i32:
    region scratch
    mark scratch as cp
    value: scratch i32& = new[scratch] 1
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

```elisa
def nested(seed: i32) -> i32:
    region scratch(1024u)
    base: scratch i32& = new[scratch] seed
    baseline: i32 = base[0u]

    mark scratch as outer
    stable: scratch i32& = new[scratch] seed + 1

    mark scratch as inner
    temp: scratch i32& = new[scratch] seed + 2
    restore scratch from inner

    kept: i32 = stable[0u]
    restore scratch from outer

    reset scratch
    final: scratch i32& = new[scratch] seed + 3
    return baseline + kept + final[0u]
```

Restoring an older checkpoint invalidates newer checkpoints from the same region.

The same conservative invalidation idea also applies to the statement-form
checkpoint surface: rewinding a checkpoint invalidates state derived from the
rewound portion of the target.

## Lowering model

The implementation lowers directly to runtime helpers that already exist in `arena.elisa`:

- `mark` → `arena_snapshot(...)`
- `restore` → `arena_rewind(...)`
- `reset` → `arena_reset(...)`

Internally, checkpoints use the runtime `ArenaMark` struct, but the source-language surface remains statement-oriented.

The broader `scope` and general checkpoint statements reuse the same existing
cleanup and rollback planning machinery that the compiler already uses for
scoped resources and count-based rewinds.