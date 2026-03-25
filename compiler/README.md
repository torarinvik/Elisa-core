# llcontext compiler

This directory contains the Go-based frontend for `.llcontext` source files.

## Changelog and recent highlight

Repository-level release notes now live in `../CHANGELOG.md`.

The current unreleased highlight is first-class `refstorage` / `refstate` generics for pointer storage and proof-state abstraction across structs, functions, exports, specialization, LLVM lowering, and generated C headers.

For a compile-checked end-to-end example, see `../Code/test_programs/ref_qualifier_generics.llcontext`.

## Error handling syntax

The frontend now supports lightweight typed error handling with compile-time error sets and explicit propagation/recovery syntax:

```text
error MemoryError:
  OutOfMemory

extern malloc(size: usize) -> heap void&?

def alloc_or_fail(size: usize) -> heap void& error[MemoryError]:
  ptr: heap void& = malloc(size) else raise MemoryError.OutOfMemory
  return ptr

def alloc_or_null(size: usize) -> heap void&?
  ptr: heap void&? = try alloc_or_fail(size) else null
  return ptr
```

When propagating fallible results, a function may return a broader error set than its callees as long as it can map every source tag into a destination tag. Exact family/tag matches win; otherwise the compiler falls back to matching by short tag name when that is unique in the destination. The surface syntax is now `-> T error[SomeSet]` for a full declared set, `-> T error[SomeSet, ...]` for the row-flavored family alias, `-> T error[SomeSet.SomeTag]` for an exact inline subset, and `-> T error[FileError, NetworkError]` for a composed multi-family set. Mixed row-style forms with ellipsis now intentionally widen every mentioned family, so `error[FileError.NotFound, NetworkError.Timeout, ...]` expands to the full `FileError` + `NetworkError` composition. Equivalent spellings are normalized for diagnostics and LLVM naming, so full-family subsets collapse back to the declared family name and multi-family compositions use a stable canonical family ordering. The old `-> T | ErrorSet` spelling is rejected with a parser migration diagnostic, and the temporary `error[SomeSet.*]` shorthand has been retired in favor of `error[SomeSet]`.

Current lowering in the backend uses integer error codes at runtime; `void | ErrorSet` lowers directly to an error code, while value-carrying fallible functions now return that code plus a hidden payload out-parameter. Inside expressions and locals, the backend still materializes compact `{err, value}` LLVM structs when it needs a first-class error-union value.

## Packed enums

The frontend also supports explicit-store packed enums for arena-backed ADTs:

```text
packed enum Expr:
  common:
    span: int
  Int(value: int)
  Add(left: Expr, right: Expr)

def build(owner: Arena) -> Expr:
  store: Expr.Store = Expr.Store(owner)
  left: Expr = new[store] Expr.Int(span: 1, value: 3)
  right: Expr = new[store] Expr.Int(span: 2, value: 4)
  return new[store] Expr.Add(span: 3, left: left, right: right)

def eval(node: Expr, store: Expr.Store) -> int:
  return match node in store:
    Expr.Int(value: value):
      value + node.span
    Expr.Add(left: left, right: right):
      node.span + eval(left, store) + eval(right, store)
```

Current packed-enum rules:

- packed values are handle-backed and must be created with `new[store] Enum.Variant(...)`
- `Enum.Store` is the explicit per-enum store constructor surface
- packed control-flow and projection forms (`match`, `open`, `view`, `move-as`, and ownerless `new`) can infer the active store when a matching `Enum.Store[...]` is already in scope
- first-class `packedview[Enum.Variant]` carriers also provide the omitted-store context for packed `open` / `view` / `move-as`, so those forms do not need a separate `in store` clause when operating on the view itself
- packed values loaded from an exact `Enum.Store[...]` index root (including immutable whole-value aliases of those loads, and store-bearing field projections such as `box.store[index]`) also carry enough provenance for omitted-store `match` / `open` / `view` / `move-as`
- packed `common:` fields are readable on the packed handle (`node.span`)
- packed `common:` fields can be initialized by name during allocation; omitted common fields remain zero-initialized
- packed variants may include one tail payload, which lowers as a `dview[...]` regardless of where that payload appears in the variant field list
- packed enums may carry affine common fields and affine payloads; when they do, the packed handle becomes affine and packed destructuring forms consume that handle after a successful `match`, `open`, or `view`
- `view value as Enum.Variant(alias):` still binds a first-class `packedview[Enum.Variant]` alias, while `view value as Enum.Variant(field: x, ...)` and multi-payload positional forms now destructure payloads directly
- inside a successful `view` body, identifier scrutinees are also refined to `packedview[Enum.Variant]`, so variant fields can be read from the viewed value directly

For a compile-checked end-to-end example, see `../Code/test_programs/packed_enum_common.llcontext`.

The frontend also now supports `in store:` block sugar over that explicit core:

```text
def build(owner: Arena) -> Expr:
  store: Expr.Store = Expr.Store(owner)
  in store:
    left: Expr = new Expr.Int(span: 1, value: 3)
    right: Expr = new Expr.Int(span: 2, value: 4)
    return new Expr.Add(span: 3, left: left, right: right)
```

Inside an active store context — whether introduced by `in store:` or by an in-scope `Enum.Store[...]` local/parameter — packed allocations can omit `[store]`, packed matches can omit `in store`, and `open`/`view`/`move-as` can omit their store clause; the same omitted-store forms also work when the packed value itself is proven to come from an exact store-index root. All forms still lower to the same explicit-store representation.

## Frozen packed projections

The frozen packed-projection lane is now covered end to end across the current semantic and backend matrix.

- Semantic optimization facts preserve frozen packed-store provenance through the wrapper forms exercised so far, including `try`, `unwrap else`, ternary expressions, and direct constant indexing.
- Projected frozen packed reads in the word-handle ABI now reuse decoded rows for repeated common-field access instead of re-decoding on every read.
- That decode-row reuse is validated not just for direct values, but also for helper-wrapped, helper-indexed, nested rebased helper-indexed, and nested wildcard rebased helper-indexed carriers.
- Projected field reassignment correctly invalidates cached decoded rows so post-mutation reads re-decode instead of reusing stale data.
- Frozen `match` lowering over projected packed values keeps repeated matched-child common-field reads on the eager-decode path, avoiding tag/word helper fallback in the validated cases.

In short: the current frozen-store projection surface is no longer just “works in the happy path”; it has explicit regression coverage for the carrier shapes and decode-cache coherence rules the compiler relies on. Tiny compiler dragon, now with a seatbelt.

The same frozen-store lane now also exposes dense node-key helpers for fixed-size side tables over packed enums:

```text
def annotate(owner: Arena) -> i32:
  store: Expr.Store[Local] = Expr.Store(owner)
  in store:
    left: Expr = new Expr.Lit(span: 1, value: 3)
    right: Expr = new Expr.Lit(span: 2, value: 4)
    _ = new Expr.Add(span: 5, left: left, right: right)

  frozen: Expr.Store[Frozen] = freeze(move store)
  node: Expr = frozen[2u]
  key: NodeKey[Expr] = dense_key(node, frozen)
  depths: NodeTable[Expr, i32] = node_table_fill.specialize[Expr, i32]()(owner, frozen, -1i32)
  depths[key] <- 0i32
  again: Expr = frozen[key]
  all_depths: dview[i32] = depths.values
  return again.span
```

Current dense frozen-node-table rules:

- `dense_key(node, frozen)` is only valid for packed-enum values or packed views proven to come from the exact same frozen store root.
- That frozen root can come from an exact hidden store-field projection such as `box.store`, not just a bare frozen local or parameter.
- `NodeKey[Enum]` is a compact carrier for the dense frozen index; `frozen[key]` rehydrates the packed handle from that key.
- `node_table_fill.specialize[Enum, T]()(arena, frozen, init)` allocates a fixed-size `NodeTable[Enum, T]` with one slot per frozen packed-store row and initializes it eagerly.
- `table[key]` is writable, while `table.values` is a dense `dview[T]` with frozen packed-store provenance and exact extent tracking.
- Canonical packed lowering keeps the key path zero-cost under `index-soa`, while legacy row/word-handle overrides lower through the existing dense-index encode/decode helpers.

## Benchmark scaffolding

There is now a synthetic JSON benchmark scaffold under `test/benchmarks/json_bench_test.go`.

- it generates deterministic nested JSON corpora of several sizes
- it validates the generated corpora with a normal `go test` unit test
- it benchmarks Go's `encoding/json` as a baseline for future parser comparisons

Run it with:

```text
go test ./test/benchmarks -run '^TestSyntheticJSONCorpusIsValid$'
go test ./test/benchmarks -run '^$' -bench '^BenchmarkEncodingJSONParseSyntheticCorpus$' -benchmem
```

There is also now a self-hosted parser fixture at `../Code/test_programs/json_parser.llcontext` plus benchmark helpers:

- `../Code/test_programs/json_parser.llcontext` exports `json_parser_checksum(...)`, `json_parser_ast_checksum(...)`, `json_parser_ast_cached_checksum(...)`, `json_parser_parallel_checksum(...)`, `json_parser_parallel_ast_checksum(...)`, `json_parser_parallel_ast_cached_checksum(...)`, and `json_parser_parity_suite()`
- `../Code/benchmarks/json_parser_bench.c` is a standalone checksum-parser benchmark executable for file-backed corpora
- `../Code/benchmarks/json_parser_ast_bench.c` is the AST-building benchmark executable for the same corpora
- `../Code/benchmarks/json_parser_parallel_bench.c` is a pool-driven parallel batch benchmark over the same parser exports
- `../Code/benchmarks/json_parser_concurrency_runtime.c` provides a small pthread-backed implementation of the raw pool/task-group runtime seam used by the parallel benchmark path
- `test/benchmarks/cmd/gen_synthetic_json` writes the same deterministic corpus family to disk for external benchmarking

One way to compare both self-hosted paths against the Go baseline is:

```text
go run ./test/benchmarks/cmd/gen_synthetic_json -case large -o /tmp/llcontext-large.json
go run ./src -O3 -emit header -o /tmp/json_parser.h ../Code/test_programs/json_parser.llcontext
go run ./src -O3 -emit obj -o /tmp/json_parser.o ../Code/test_programs/json_parser.llcontext
clang -O3 -Wl,-undefined,dynamic_lookup -I /tmp ../Code/benchmarks/json_parser_bench.c ../Code/benchmarks/json_parser_runtime_shims.c /tmp/json_parser.o -o /tmp/json_parser_bench
clang -O3 -Wl,-undefined,dynamic_lookup -I /tmp ../Code/benchmarks/json_parser_ast_bench.c ../Code/benchmarks/json_parser_runtime_shims.c /tmp/json_parser.o -o /tmp/json_parser_ast_bench
clang -O3 -pthread -Wl,-undefined,dynamic_lookup -I /tmp ../Code/benchmarks/json_parser_parallel_bench.c ../Code/benchmarks/json_parser_runtime_shims.c ../Code/benchmarks/json_parser_concurrency_runtime.c /tmp/json_parser.o -o /tmp/json_parser_parallel_bench
/tmp/json_parser_bench /tmp/llcontext-large.json 20
/tmp/json_parser_ast_bench /tmp/llcontext-large.json 20 full
/tmp/json_parser_parallel_bench /tmp/llcontext-large.json 20 4 checksum
/tmp/json_parser_parallel_bench /tmp/llcontext-large.json 20 4 ast-cached
go test ./test/benchmarks -run '^$' -bench '^BenchmarkEncodingJSONParseSyntheticCorpus/large$' -benchmem -benchtime=20x
```

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

## Runtime source of truth

The active runtime implementation lives in llcontext source files under `../Code/`.

- `../Code/contextlang_runtime.llcontext` — canonical runtime entrypoint
- `../Code/runtime_llcontext/` — staged runtime helpers and wrappers
- `../Code/arena.llcontext` — arena, dynamic-array, and dictionary support

Retained C sources under `../Code/benchmarks/` are benchmark scaffolding only and are not part of the active runtime implementation.

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
