# llcontext compiler

This directory contains the Go-based frontend for `.llcontext` source files.

## Layout

- `src/` — active compiler source code
  - `src/main.go` — CLI entry point
  - `src/ast/` — AST definitions
  - `src/lexer/` — lexer and token definitions
  - `src/parser/` — parser implementation
- `test/` — tests
  - `test/lexer/` — lexer tests
- `scripts/` — helper sync scripts for canonical source files in `src/`

## Build

Build the compiler binary from the `src` package tree:

```text
mkdir -p bin
go build -o bin/llcontext ./src
```

## Test

Run all tests:

```text
go test ./...
```

## Script sync

The scripts in `scripts/` are safe sync wrappers for the canonical files in `src/`:

- `gen_ast.py`
- `gen_parser.py`
- `gen_main.py`

Running them will preserve the current `src/` layout instead of regenerating the old top-level package structure.

## Verified commands

These commands were verified in this workspace:

```text
go test ./...
mkdir -p bin
go build -o bin/llcontext ./src
```
