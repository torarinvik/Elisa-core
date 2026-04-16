# Changelog

All notable changes to this repository should be documented in this file.

## Unreleased

### Highlights

- Postfix shorthand casts like `value.i64()` now dispatch to visible `__cast__` hooks when an exact source-to-target hook exists, with permission-aware semantic analysis and LLVM lowering.
- Packed-enum `common:` fields can now opt into side-table storage via `@storage(side_table)`, including end-to-end runtime/backend support and ABI validation.
- `@test` functions may now declare `Abort.*` permissions, which keeps panic/assert-heavy compiler fixtures valid without opening the door to unrelated declared permissions.
- The Lua frontend experiment now ships with a storage-layout benchmark harness that compares the checked-in side-table layout against a temporary inline-control variant.
- First-class `refstorage` / `refstate` generics now work end to end across parsing, semantic analysis, specialization, exports, LLVM lowering, and C header generation.
- Concrete export wrappers such as `keep_handle[heap, &]` now parse and lower correctly, including stable public header emission for concrete qualifier-specialized exports.
- A compile-checked showcase for the feature now lives at `Code/test_programs/ref_qualifier_generics.llcontext`.
- Frozen-store projection APIs are now effectively complete across the current semantic and packed-ABI backend matrix, including wrapper continuity, decode-once reuse for projected frozen common-field reads, and correct decode-cache invalidation after projected mutation.
- Frozen packed enums now support dense node-key side tables through `NodeKey[Enum]`, `NodeTable[Enum, T]`, `dense_key(...)`, `node_table_fill.specialize[...]()`, `frozen[key]`, `table[key]`, and `table.values`.

### Added

- Postfix shorthand cast-hook resolution for `expr.TargetType()` syntax when a visible `def __cast__(value: Source) -> Target` hook matches exactly.
- Side-table packed common-field storage via `@storage(side_table)` on `packed enum` `common:` fields.
- Lua frontend storage benchmark tooling:
  - `Code/benchmarks/lua_frontend_bench.c`
  - `compiler/scripts/run_lua_frontend_storage_benchmark.py`
- First-class generic parameter kinds for pointer storage and pointer proof state:
  - `refstorage name`
  - `refstate name`
- Named symbolic pointer qualifiers such as:
  - `store T&[state]`
  - nested forms like `store T&&[state]`
- Concrete qualifier literals for generic specialization:
  - refstorage: `any`, `heap`, `stack`, `static`
  - refstate: `&`, `?`, `!`
- End-to-end compiler support for qualifier generics across:
  - AST
  - parser
  - semantic type resolution and substitution
  - function specialization and call-site inference
  - LLVM lowering and generic mangling
  - export analysis and C header generation
  - CLI/type formatting
- Regression coverage for:
  - postfix cast-hook AST printing, LLVM emission, duplicate/misuse rejection, and arrow-cast non-dispatch
  - packed side-table common-field lowering and ABI/error cases
  - `@test` functions that declare `Abort.Panic` while rejecting non-`Abort` declared permissions
  - named qualifier parsing
  - nearest-`&` state attachment
  - qualifier-generic call inference
  - qualifier-specialized LLVM lowering
  - concrete export/header behavior
- Frozen packed dense-node-table helpers for:
  - exact frozen-store-root key provenance
  - `NodeKey` / `NodeTable` builtin carrier typing
  - direct backend lowering of `node_table_fill`
  - canonical + legacy packed-ABI dense-key indexing
  - `table.values` optimization facts

### Changed

- `@test` annotations now permit declared `Abort` permissions while continuing to reject other declared permission families.
- Explicit casts (`expr.cast[T]`, legacy `.cast[T]()`, and `expr -> T`) continue to use ordinary cast rules even when a postfix `__cast__` hook exists; only postfix shorthand dispatches to hooks.
- Packed-enum layout computation, runtime helpers, and LLVM lowering now support `common:` fields stored in side tables as well as inline words.
- Mixed generic argument order is now declaration order for:
  - ordinary type parameters
  - `refstorage`
  - `refstate`
- Export specialization parsing now accepts generic literal args like `keep_handle[heap, &]`.
- Call inference now binds `refstorage` and `refstate` parameters from concrete argument types.
- Export type validation now rejects unresolved qualifier-parameter types at the C ABI boundary.
- Frozen packed projections now have explicit regression coverage across direct, helper-wrapped, helper-indexed, nested rebased helper-indexed, and nested wildcard rebased helper-indexed carriers. The validated matrix now covers repeated direct common-field reads, projected reassignment invalidation, and repeated matched-child common-field reuse under `in frozen:` lowering.
- Frozen packed helper analysis now tracks exact-store provenance for dense node keys and fixed-size side tables, and the backend lowers `node_table_fill` directly through `arena_alloc` + `arena_da_fill` instead of a user-visible wrapper.

### Compatibility

- Existing anonymous aggregate-state syntax remains supported.
- `region` and `permission` parameters are not part of explicit generic specialization order.
- Legacy nullable-array syntax like `&?[COUNT]` still parses as before; named refstate syntax only attaches on direct `&[name]`.

### Documentation

- `compiler/README.md` now documents the latest postfix cast-hook surface and the Lua frontend storage benchmark harness.
- Expanded `docs/useful_language_features/18-current-surface-ergonomics.md` with the current implemented surfaces for `do:` blocks, `defer`, index fallback, store/dict sugar, explicit `parallel for`, char literals, and the newer loop/control-flow ergonomics.
- Added `docs/useful_language_features/19-static-interfaces-extension-methods-and-ufcs.md` as the implemented reference for static interfaces, extension methods, UFCS rewriting, safe call chaining, and the preferred generic specialization surface.
- Added `docs/useful_language_features/20-annotations-and-compile-time-hints.md` as the implemented reference for current layout annotations, packed-layout annotations, function codegen hints, guard annotations, and branch hints.
- Expanded `docs/useful_language_features/08-region-checkpoints.md` to cover the current `scope`, named checkpoint, grouped checkpoint, and rollback-block statement surface in addition to region-local checkpoints.
- Updated `docs/useful_language_features/17-iterators-and-for-in-mini-spec.md` so it no longer reads as if the current explicit `parallel for` feature is still purely deferred.
- Expanded `compiler/README.md` with a compact syntax cheat sheet plus current `.llcontexti`, `project.json`, and `.llctxlib/manifest.json` workflow documentation.
- Expanded `compiler/README.md` with current test annotation, test-runner, filter, and helper emit-mode documentation, including `deps-json` and the distinction between listing, runner-generation, and direct test execution modes.
- `Code/llcontext_lua/README.md` now reflects the current parser/export surface, side-table packed-span layout, and benchmark entry points.
- Expanded pointer typestate documentation in:
  - `docs/useful_language_features/02-pointer-typestate-practical.md`
  - `docs/useful_language_features/03-pointer-typestate-formal.md`
  - `docs/useful_language_features/07-export-and-c-abi.md`
- Added a compile-checked end-to-end feature example at:
  - `Code/test_programs/ref_qualifier_generics.llcontext`
- Added a human-readable frozen packed-projection completeness note to `compiler/README.md`.
- Documented the dense frozen packed node-table helper surface in `compiler/README.md`.

### Verification

- Verified with a full compiler test sweep:
  - `cd /Users/torarinbjarko/Documents/FSharpProjects/LowLevelContextlang/compiler && go test ./...`
