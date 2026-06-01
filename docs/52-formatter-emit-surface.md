# Formatter emit surface (`-emit fmt`)

This document captures the current formatter emit behavior.

## Command

```sh
go run ./src -emit fmt path/to/file.elisa
go run ./src -emit fmt -o formatted.elisa path/to/file.elisa
```

## Current behavior

- parses source into AST and formats from AST structure
- emits semantic notices to stderr when available (subject to deprecation suppression settings)
- writes formatted output to stdout by default
- when `-o` is provided, writes to file and does not require stdout output

## Formatting characteristics

Current formatter output normalizes spacing and expression punctuation for canonical source style. Representative effects include:

- assignment spacing normalization
- list literal spacing normalization
- expression parenthesization in precedence-sensitive contexts

## Reparse expectation

Formatted output is expected to be parsable by the compiler and suitable for round-trip CLI workflows.

## Elisa example

Input:

```elisa
@test
def sample_case(value: i64) -> i64:
    values=[1,2,3]
    if likely value > 0:
        return (value)
    return 0
```

Representative formatted output:

```elisa
@test
def sample_case(value: i64) -> i64:
    values = [1, 2, 3]
    if likely (value > 0):
        return value
    return 0
```

## Related docs

- `docs/34-cli-emit-mode-catalog.md`
- `docs/44-cli-argument-normalization-and-defaults.md`
