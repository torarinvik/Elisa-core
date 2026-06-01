# Interpreter emit surface (`-emit interpret`)

This document captures current CLI behavior for interpreter execution mode.

## Command

```sh
go run ./src -emit interpret path/to/file.elisa
```

## Current behavior

- input is parsed/analyzed, then executed through interpreter runtime
- interpreter stdout is streamed to CLI stdout
- if return value is non-void, CLI appends:
  - `[ result   ] <value>`
- if return value is void, no result line is appended

## Newline behavior before result line

If interpreter stdout is non-empty and does not end with newline, CLI inserts one newline before printing `[ result   ] ...`.

## Option constraints

- `-o` is not supported for `-emit interpret`
- `-filter` is not supported for `-emit interpret`

## Failure behavior

- runtime/interpreter failures return nonzero
- stderr includes `error: ...` diagnostics

## Elisa example

```elisa
def main() -> i64:
    return 42
```

Representative output:

```text
[ result   ] 42
```

## Related docs

- `docs/34-cli-emit-mode-catalog.md`
- `docs/35-pipeline-and-introspection-emits.md`
- `docs/37-compile-server-api-surface.md`
