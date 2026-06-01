# AST emit text surface (`-emit ast`)

This document captures the current textual AST-report contract for `-emit ast`.

## Command

```sh
go run ./src -emit ast path/to/file.elisa
```

## Output header

AST output begins with:

```text
File: <filename> (<declaration-count> declarations)
```

Declarations are then printed in source order with indentation for nested scopes.

## Output style

- output is a structural summary, not a source-preserving pretty print
- rows include declaration kind and compact metadata counts
- nested declarations are indented by two spaces per level
- annotation lines are emitted as `@...` rows before annotated declarations

Representative declaration row patterns:

- `struct Name[...] (<n> fields)`
- `def name(... params) -> ... (<n> stmts)`
- `extern name(... params) -> ...`
- `namespace Name: (<n> decls)`
- `enum Name: (... variants)`
- `static if <cond>: (<then-count> then, <elif-count> elifs)`

## Parse and notice behavior

- parse errors stop output and return nonzero
- semantic notices can be emitted to stderr through warning pass when parse succeeds
- `-o` is not supported for `-emit ast`

## Elisa example

```elisa
@test
def alpha_case() -> void:
    pass

struct Pair:
    left: i64
    right: i64
```

Representative AST output includes:

- file header line
- `@test`
- `def alpha_case(0 params) -> void ...`
- `struct Pair ... (2 fields)`

## Related docs

- `docs/34-cli-emit-mode-catalog.md`
- `docs/35-pipeline-and-introspection-emits.md`
