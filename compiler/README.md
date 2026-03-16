# llcontext compiler

This directory contains the Go-based frontend for `.llcontext` source files.

## Error handling syntax

The frontend now supports lightweight typed error handling with compile-time error sets and explicit propagation/recovery syntax:

```text
error MemoryError:
  OutOfMemory

extern malloc(size: usize) -> heap void&?

def alloc_or_fail(size: usize) -> heap void& error[MemoryError]:
  ptr: heap void& = malloc(size) else raise MemoryError.OutOfMemory
  return ptr

def alloc_or_null(size: usize) -> any void&:
  ptr: any void& = try alloc_or_fail(size) else null.cast[any void&]()
  return ptr
```

When propagating fallible results, a function may return a broader error set than its callees as long as it can map every source tag into a destination tag. Exact family/tag matches win; otherwise the compiler falls back to matching by short tag name when that is unique in the destination. The surface syntax is now `-> T error[SomeSet]` for a full declared set, `-> T error[SomeSet, ...]` for the row-flavored family alias, `-> T error[SomeSet.SomeTag]` for an exact inline subset, and `-> T error[FileError, NetworkError]` for a composed multi-family set. Mixed row-style forms with ellipsis now intentionally widen every mentioned family, so `error[FileError.NotFound, NetworkError.Timeout, ...]` expands to the full `FileError` + `NetworkError` composition. Equivalent spellings are normalized for diagnostics and LLVM naming, so full-family subsets collapse back to the declared family name and multi-family compositions use a stable canonical family ordering. The old `-> T | ErrorSet` spelling is rejected with a parser migration diagnostic, and the temporary `error[SomeSet.*]` shorthand has been retired in favor of `error[SomeSet]`.

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
