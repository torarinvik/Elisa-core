# Progress report output surface (`-emit progress`)

This document describes the current textual shape emitted by `-emit progress`.

## Header and counters

Output starts with:

```text
=== progress ===
warnings: <count>
functions: <count>
```

## Function summaries section

When at least one summary exists, output includes:

```text
function summaries:
  <name>: obligations=<csv> evidence=<value> unsafe_nonprogress=<bool> blocking=<bool> unsafe_block_main=<bool> main_thread=<bool> blocking_path=<value>
```

## Field semantics

- `obligations`:
  - aggregated as `kind:count`
  - sorted lexicographically
  - `none` when no obligations are recorded
- `evidence`:
  - `none`
  - `progress`
  - `unsafe-nonprogress`
  - `progress+unsafe-nonprogress`
- `blocking_path`:
  - `none` when empty
  - otherwise joined with ` -> `

## Diagnostics section

Only warnings containing `progress warning:` are included.

When present:

```text
diagnostics:
  <warning line>
```

Warning lines are sorted lexicographically.

## Elisa example

```elisa
def main() -> int:
    return 0
```

```bash
go run ./src -emit progress sample.elisa
```

## Related docs

- `docs/25-progress-safety.md`
- `docs/35-pipeline-and-introspection-emits.md`
