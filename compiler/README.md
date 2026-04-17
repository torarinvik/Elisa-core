# llcontext compiler

This directory contains the Go-based frontend for `.llcontext` source files.

## Changelog and recent highlight

Repository-level release notes now live in `../CHANGELOG.md`.

The current unreleased highlights include:

- postfix shorthand cast hooks, so exact `__cast__` helpers can back method-like conversions such as `op.i64()`
- side-table storage for packed-enum `common:` fields via `@storage(side_table)`
- first-class `effect` declarations plus explicit `signal` statements for effect tracking
- the earlier `refstorage` / `refstate` generic work across parsing, semantics, specialization, lowering, and generated C headers

For a compile-checked end-to-end example, see `../Code/test_programs/ref_qualifier_generics.llcontext`.

## Current syntax reference

For the newer source-language surface that is easy to miss in older design notes, see `../docs/useful_language_features/18-current-surface-ergonomics.md`.

For compile-time interfaces and receiver-style dispatch, see `../docs/useful_language_features/19-static-interfaces-extension-methods-and-ufcs.md`.

For scope/checkpoint rollback blocks, see `../docs/useful_language_features/08-region-checkpoints.md`.

For current annotations and compile-time hints, see `../docs/useful_language_features/20-annotations-and-compile-time-hints.md`.

That reference covers the currently implemented syntax for:

- default and named arguments, including `..` forwarding
- effect declarations, `signal`, local `can` grants, effects aliases, and implicit contexts
- explicit argument packs via `params` and ambient `with args(...)` scopes
- brace destructuring, field punning, record updates, and filtered iterable loops
- `do:` blocks, `defer`, index fallback, store/dict sugar, char literals, and explicit `parallel for`
- cascade blocks and expressions, lambda literals, and postfix cast hooks
- static interfaces, associated types, extension methods, UFCS rewriting, safe call chaining, and the preferred generic specialization surface

## Syntax cheat sheet

The current commonly used surface fits into a few recurring patterns:

```text
return consume(value:, ..)
effect FooEffect: pass
return puts(text) can Console.Write
can ConsoleEffect.Write:
  signal ConsoleEffect.Write
effects FrontendEffects = error[ParseErr] can[Abort.Panic]
context ParseCtx:
with ParseCtx(.., alloc = scratch_alloc):
params Pair:
with args(use Pair(left:), width:):
```

```text
let {left: first, right} = row
built: Row = Row{left: first, right, flag}
next: Row = built{flag, right = first}
for {left, right: value} in items if left != 0:
```

```text
cascade report:
  .inner.value <- value
return lambda (value: i64) -> i64:
  return value + 1
```

```text
value = do:
  base = 40
  base + 2
defer block:
  cleanup()
pool workers(2u):
  parallel for node in frozen:
    pass
```

```text
impl Tok:
  def score(self: Tok) -> i64:
    return 7

interface Builder:
  type Node
  def make(value: int) -> Node
```

Use the two reference docs above for the exact rules and edge cases.

## Test annotations and runner emits

The source surface includes lightweight test and benchmark annotations:

```text
@test
def alpha_case() -> void:
  pass

@fixture
def shared_seed() -> int:
  return 7

@bench
def bench_hot_loop() -> void:
  pass

@skip(todo)
@test
def beta_case() -> void:
  pass
```

Current rules:

- `@test` marks a test case
- `@fixture` marks a helper fixture surface
- `@bench` marks a benchmark case
- `@skip(reason)` and `@ignore(reason)` skip an annotated test case while preserving it in listing and runner output
- `@test` functions may declare and infer ordinary `can[...]` effects, and the generated test runner carries the selected tests' required permissions into its wrapper surface

Current test-oriented emit modes:

- `-emit tests`, `-emit benches`, and `-emit fixtures` list matching annotated functions
- `-emit test-runner` emits generated `.llcontext` runner source rather than executing it directly
- `-emit test` compiles and runs the selected tests through the native harness path
- `-filter` is only supported for `tests`, `benches`, `fixtures`, `test-runner`, and `test`
- filters are case-insensitive and accept substring matches, glob patterns such as `*beta*`, and comma-separated OR combinations

## Emit mode quick reference

In addition to the core `llvm`, `header`, `iface`, and project commands, the CLI also exposes several smaller helper emit modes:

- `-emit ast` prints the parsed AST
- `-emit fmt` prints canonical formatted source and normalizes local grants into surface `can Name` / `can Name.Member` form, conservatively inlining simple one-statement `can ...:` blocks when safe
- `-emit doc` emits reference docs for the current file
- `-emit deps` prints the expanded source dependency list
- `-emit deps-json` emits the same dependency information as JSON with `root` and `files` fields
- `-emit ir` emits the frontend IR bundle form
- `-emit packed` emits packed-lowering inspection output
- `-emit serve` starts the compile server on `-addr`, which defaults to `127.0.0.1:8080`

Optimization defaults:

- `-emit bc` and `-emit obj` default to `-O3` unless an explicit `-O0`, `-O2`, or `-O3` flag is supplied
- the other emit modes default to `-O0`

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

## Regular enums

Regular `enum` values are inline tagged values. Constructors like `Expr.Int(1)` produce an `Expr` directly, so locals, returns, arrays, and optionals can store them without `new[...]`.

```text
enum Small:
  Int(value: i64)
  Pair(left: i64, right: i64)
  Done

def make_node(value: i64) -> Small:
  return Small.Int(value)

def score(node: Small) -> i64:
  return match node:
    Small.Int(value):
      value
    Small.Pair(left, right):
      left + right
    Small.Done:
      0

def total(seed: i64) -> i64:
  items: array[Small, 3] = [Small.Int(1), make_node(seed), Small.Done]
  maybe: Small? = Small.Pair(2, 3)
  total: i64 = score(items[0]) + score(items[1]) + score(items[2])
  if let node = maybe:
    total = total + score(node)
  return total
```

Use `new[region]` only when you need a reference to a regular enum value, such as a recursive structure whose payloads store `any Expr&` handles. For compile-checked examples, see `../Code/test_programs/regular_enum_values.llcontext` for by-value usage and `../Code/test_programs/recursive_enum.llcontext` for recursive references.

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
- `scripts/ensure_json_parser_bench_input.sh` creates the default large synthetic corpus on demand and prints the path, which keeps ignored local task runners usable without hand-creating `/tmp/zimdjson-dom-large.json`

One way to compare both self-hosted paths against the Go baseline is:

```text
bash ./scripts/ensure_json_parser_bench_input.sh /tmp/llcontext-large.json
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

There is also now a Lua frontend storage-layout benchmark lane for comparing the checked-in side-table packed-span layout against a temporary inline-control variant:

- `../Code/llcontext_lua/src/lua_ast.llcontext` uses `@storage(side_table)` on `LuaNode.common.span`
- `../Code/benchmarks/lua_frontend_bench.c` benchmarks exported parse/checksum/sample entry points plus the semantic analysis modes
- `./scripts/run_lua_frontend_storage_benchmark.py` builds both layouts, generates valid Lua input, runs repeated parse/sample measurements, and summarizes throughput deltas

One way to run it is:

```text
python3 ./scripts/run_lua_frontend_storage_benchmark.py --stmt-count 4000 --parse-iterations 20 --sample-iterations 5000 --repeats 3
```

There is also now an ML-style packed AST benchmark ladder under `../Code/benchmarks/` for exercising packed/frozen tree matching and the produced native runtime:

- `packed_lowering_ml_ast_medium_{core,bench,parallel_bench}.llcontext` is the default everyday perf tier
- `packed_lowering_ml_ast_mega_{core,bench,parallel_bench}.llcontext` is the explicit slow validation tier
- generic ML AST benchmark names now target the medium fixture, while explicit `Mega` benchmark names keep the long-running workload available
- `LLCONTEXT_SLOW_NATIVE=1` gates the full mega native smoke in `src/main_test.go`
- `scripts/run_ml_ast_perf_loop.sh` runs the normal repro -> medium loop, and `--mega` adds the explicit slow-path validation lane

Recommended workflow:

- `repro`: use `TestRunCLIPackedMLExprReproSmoke` for bug fixing and tiny correctness checks
- `medium`: use the generic `PackedMLAST...` benchmarks for normal compiler/runtime perf iteration
- `mega`: use the explicit `PackedMLASTMega...` benches/tests before landing changes that touch packed lowering, semantic provenance, or native benchmark wiring

One way to run the default loop is:

```text
./scripts/run_ml_ast_perf_loop.sh --benchtime 1x
```

And one way to run the milestone validation pass is:

```text
./scripts/run_ml_ast_perf_loop.sh --benchtime 1x --mega
```

## Module interfaces and project manifests

The compiler now has a small project and library workflow around `.llcontext`, `.llcontexti`, `project.json`, and `manifest.json`.

Module interface emission:

```text
llcontext -emit iface -o mathcore.llcontexti mathcore.llcontext
```

Current interface-emission behavior:

- emitted `.llcontexti` files stay valid llcontext source
- type declarations are preserved
- globals become `extern` declarations rather than exported initialized definitions
- function bodies are stripped and replaced by `extern` signatures
- namespaces are preserved, so nested exported surface stays structured

Current project scaffold commands:

```text
llcontext init demo --path /tmp
llcontext init-lib mathcore --path /tmp/demo/lib
llcontext build app --project /tmp/demo
llcontext run app --project /tmp/demo
llcontext test tests --project /tmp/demo
llcontext bench benches --project /tmp/demo
llcontext project view app --project /tmp/demo
llcontext project deps app --project /tmp/demo --json
```

Project file shape:

```json
{
  "version": "0.1.0",
  "dependency-search-paths": ["lib"],
  "dependencies": ["mathcore"],
  "include-dirs": ["shared"],
  "foreign": ["native/app_runtime.c"],
  "exec": [],
  "targets": {
    "app": {
      "entry": "src/main.llcontext",
      "emit": "llvm",
      "run-emit": "interpret",
      "output": "build/app.ll",
      "dependencies": [],
      "include-dirs": [],
      "foreign": [],
      "exec": [],
      "opt": "O0",
      "packed-abi": ""
    }
  }
}
```

Library manifest shape:

```json
{
  "provides": "mathcore",
  "entry": "src/mathcore.llcontext",
  "interface": "src/mathcore.llcontexti",
  "dependencies": [],
  "include-dirs": ["shared"],
  "foreign": ["native/mathcore_runtime.c"],
  "exec": []
}
```

Current rules:

- `project.json` is the top-level project file
- `.llctxlib/manifest.json` is the dependency manifest format
- dependency search paths default to `lib` when the project does not override them
- project targets currently require an `entry` that is a `.llcontext` or `.llcontexti` source file
- manifests may provide an `entry`, an `interface`, or both; dependency loading prefers `entry` when present and falls back to `interface` otherwise
- project-wide and target-specific `include-dirs`, `foreign`, and `dependencies` merge with dependency-provided include dirs and foreign files
- `project view` reports resolved targets and the selected target configuration
- `project deps` reports the combined source set, dependency interfaces or entries, include dirs, and foreign sources
- `exec` hooks exist at the project, target, and dependency-manifest level, but current execution requires `--trust=full`

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

The active runtime implementation lives in llcontext source files under `../Code/llcontext_std/`.

- `../Code/llcontext_std/contextlang_runtime.llcontext` — canonical runtime entrypoint
- `../Code/llcontext_std/` — staged runtime helpers, wrappers, allocator, collections, stores, and test support

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
