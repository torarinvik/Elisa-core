# Tree Lowering Backend Contract

This note documents the current LLVM backend contract for Elisa `tree` values. It is intentionally descriptive, not a new language proposal.

## Supported Layouts

- `@layout(category_union)` is the default dense tree layout.
- `@layout(aos)` is an explicit dense row-payload layout for recursive traversal locality.
- `@layout(soa)` is an explicit dense column-oriented request for scan-heavy/frozen-store work.
- `@layout(per_variant_rows)` is an explicit legacy row layout.
- Unsupported layouts must fail with deterministic backend diagnostics.
- Layout selection is centralized in backend `treeLayoutPlan` helpers; new tree lowering paths should use those helpers instead of direct layout checks.
- `@index(kind)` and `@index(field_name)` are accepted on trees and categories and are preserved in the layout plan for freeze/index lowering.
- `@hot` and `@cold` are accepted on tree common/block/struct fields and preserved as field-temperature metadata.

## Handle ABI

- Dense layouts (`category_union`, `aos`, and `soa`) lower tree handles to store-relative `u32` row ids.
- Dense tags live in root/category tag arrays, not inside the handle.
- Dense handles require an explicit matching store context for reads; the backend must not synthesize hidden active-store globals.
- `per_variant_rows` keeps the legacy `%Tree__TreeHandle = { ptr, i64 }` carrier, where the pointer lane carries store state and the `i64` key lane packs exact tag plus row index.
- Legacy row-index packing traps with `llvm.trap` if the row cannot fit in the key lane.

## Store And Field Semantics

- Tree allocation requires an active tree owner: `perm`, a tree store, an `Arena` value, or an `Arena` reference.
- `freeze(move store)` accepts local tree stores and produces the matching frozen tree store type. Frozen store values preserve handle identity through the existing rebase/fact model.
- Dense `category_union` code should prefer explicit stores:
  `store = Tree.Store(owner)` followed by `in store:` around constructors, reads, visits, folds, rewrites, clones, and attributes.
- `in owner:` remains valid for short-lived local construction/read scopes, but values that escape the scope should also carry an explicit store somewhere in the surrounding data model.
- Functions that accept dense tree handles and read them receive hidden tree-store context parameters when the semantic pass can infer the matching store from the call site.
- Implicit store parameters are threaded from explicit `in store:` / `in owner:` scopes, `perm` store scopes, and payload types that carry tree handles through structs or enum variants.
- Functions that already have an active tree store in scope, such as an explicit `Tree.Store[...]` parameter or a local `in store:` block, use that explicit context and do not receive a duplicate hidden store parameter for the same family.
- Plain arena/region scopes can allocate destination nodes, but they do not identify the source table for an incoming dense handle. Reads of incoming handles inside `in owner:` / `in region:` still receive hidden tree-store context.
- If multiple stores could satisfy the same tree family, the call site must keep enough explicit context for deterministic inference; the backend must not fall back to a global active store.
- Tree fields are value reads over handle-lowered storage.
- Tree fields are not stable lvalues in v1.
- Assignment, address-taking, mutable reference binding, and by-reference mutation of tree fields must be rejected.
- Tree field reads must not expose physical row storage pointers as user-visible values.

## Migration Rules

- Default to dense `category_union` for new trees.
- Use `@layout(aos)` when the hot path repeatedly visits a small recursive neighborhood and wants payload fields together.
- Use `@layout(soa)` when the hot path scans one or two scalar fields across many rows, especially after `freeze(move store)`.
- Use explicit `@layout(per_variant_rows)` only for compatibility code that passes tree handles through APIs without carrying a store value yet.
- Avoid accidental root materialization: keep category handles such as `Lua.Expr` category-local in locals, parameters, fields, and helper returns. Convert to the root family type only when the source type or expected type is the root tree value itself.
- A dense root row is useful for mixed root dispatch and `children(root)`, but category-local algorithms should stay on category-local handles so they only touch the category `{tags, payloads}` table pair.
- Migrating a legacy tree means updating the public container type that owns root handles to also own the matching generated tree store. For example, a parser AST result should store both the root handle and the tree store that owns that row.
- The Pascal AST is still explicitly pinned to `per_variant_rows` because `Ast` currently contains only `{root, names}`. Removing that annotation requires changing `Ast`, parser entrypoints, semantic entrypoints, and backends to thread `Pascal.Store[...]` and `PascalType.Store[...]` explicitly.

## Runtime Coverage Expectations

- Both supported layouts should be covered by IR-shape regression tests.
- Native `-emit test` smoke coverage should exercise constructors, field reads, `kind`, `is`, children traversal, fold, rewrite default, record update, clone, and attributes.
- Benchmarks should continue comparing `per_variant_rows` and `category_union` on similar AST workloads before changing capacity policy.
- New benchmarks should compare dense `aos` and explicit `soa` on both recursive traversal and column-scan workloads before enabling heuristic layout selection.
- Compile-time IR generation is benchmarked with `BenchmarkGenerateLLVMIRTree`.
- Native runtime traversal is benchmarked with `BenchmarkNativeTreeRuntime`; the generated executable accepts an iteration count and loops internally so measurements are not dominated by process startup.
- On Apple M5 during this pass, IR generation measured about `673 us/op` for `per_variant_rows` and `881 us/op` for dense `category_union`; native traversal measured about `5.7 ns/op` for `per_variant_rows` and `8.4 ns/op` for dense `category_union`. Treat these as directional until the dense path has more root-materialization and caching work.
