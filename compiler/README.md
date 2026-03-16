# llcontext compiler

This directory contains the Go-based frontend for `.llcontext` source files.

## Error handling syntax

The frontend now supports lightweight typed error handling with compile-time error sets and explicit propagation/recovery syntax:

```text
error MemoryError:
  OutOfMemory

extern malloc(size: usize) -> heap void&?

def alloc_or_fail(size: usize) -> heap void& | MemoryError:
  ptr: heap void& = malloc(size) else raise MemoryError.OutOfMemory
  return ptr

def alloc_or_null(size: usize) -> any void&:
  ptr: any void& = try alloc_or_fail(size) else null.cast[any void&]()
  return ptr
```

Current lowering in the backend uses integer error codes at runtime; `void | ErrorSet` lowers directly to an error code, while value-carrying fallible functions now return that code plus a hidden payload out-parameter. Inside expressions and locals, the backend still materializes compact `{err, value}` LLVM structs when it needs a first-class error-union value.

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
