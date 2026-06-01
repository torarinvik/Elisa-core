# Semantic report emit contract (`-emit semantic`)

This document captures the current text contract for semantic report output.

## Command

```sh
go run ./src -emit semantic path/to/file.elisa
```

## Top-level sections

Current report is structured as:

- optional `=== lowered ===` section (when an active file is available)
- mandatory `=== semantic ===` section
- optional trailing `exports` section (when exports are present)

If no global symbols exist, semantic section prints:

```text
<no global symbols>
```

## Function listing order and inclusion

- function entries are collected from global symbols
- only function/external-function symbols are listed
- names are sorted lexicographically before rendering

Each function starts with:

```text
func <name>
  signature: <func-type>
```

If signature metadata is invalid, report prints:

```text
  signature: <invalid>
```

## Per-function semantic fields

When function-analysis metadata is present, current fields may include:

- `annotations`
- `sink_params`
- `return_isolation`
- `fact_snapshot`
- `fact_exits`
- `fact_aliases` block
- `fact_effects`
- `fact_transforms`
- `fact_groups` block
- `fact_explanations` block
- `fact_blocks` block

## Return-isolation rendering

Current summaries include forms such as:

- `unknown`
- `isolated`
- `alias_params=[...]`
- `alias_mutable_params=[...]`
- `alias_locations=[...]`

## Exports section

When export metadata exists, report appends:

```text
exports
  type <public>: <type>
  func <public>: <signature>
  global <public>: <type>
```

## Elisa example

```elisa
struct Pair:
    left: i64
    right: i64

def add_pair(p: Pair) -> i64:
    return p.left + p.right
```

```bash
go run ./src -emit semantic sample.elisa
```

## Related docs

- `docs/35-pipeline-and-introspection-emits.md`
- `docs/22-value-fact-core.md`
- `docs/53-ast-emit-text-surface.md`
