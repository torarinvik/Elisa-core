# Tree Lowering Backend Contract

This note documents the current LLVM backend contract for Elisa `tree` values. It is intentionally descriptive, not a new language proposal.

## Supported Layouts

- `@layout(category_union)` is the default dense tree layout.
- `@layout(per_variant_rows)` is an explicit legacy row layout.
- Unsupported layouts must fail with deterministic backend diagnostics.
- Layout selection is centralized in backend `treeLayoutPlan` helpers; new tree lowering paths should use those helpers instead of direct layout checks.

## Handle ABI

- `category_union` lowers tree handles to store-relative `u32` row ids.
- `category_union` tags live in root/category tag arrays, not inside the handle.
- `category_union` handles require an explicit matching store context for reads; the backend must not synthesize hidden active-store globals.
- `per_variant_rows` keeps the legacy `%Tree__TreeHandle = { ptr, i64 }` carrier, where the pointer lane carries store state and the `i64` key lane packs exact tag plus row index.
- Legacy row-index packing traps with `llvm.trap` if the row cannot fit in the key lane.

## Store And Field Semantics

- Tree allocation requires an active tree owner: `perm`, a tree store, an `Arena` value, or an `Arena` reference.
- Tree fields are value reads over handle-lowered storage.
- Tree fields are not stable lvalues in v1.
- Assignment, address-taking, mutable reference binding, and by-reference mutation of tree fields must be rejected.
- Tree field reads must not expose physical row storage pointers as user-visible values.

## Runtime Coverage Expectations

- Both supported layouts should be covered by IR-shape regression tests.
- Native `-emit test` smoke coverage should exercise constructors, field reads, `kind`, `is`, children traversal, fold, rewrite default, record update, clone, and attributes.
- Benchmarks should continue comparing `per_variant_rows` and `category_union` on similar AST workloads before changing capacity policy.
