# Tree Lowering Backend Contract

This note documents the current LLVM backend contract for Elisa `tree` values. It is intentionally descriptive, not a new language proposal.

## Supported Layouts

- `@layout(per_variant_rows)` is the default tree layout.
- `@layout(category_union)` is an explicit dense category layout.
- Unsupported layouts must fail with deterministic backend diagnostics.
- Layout selection is centralized in backend `treeLayoutPlan` helpers; new tree lowering paths should use those helpers instead of direct layout checks.

## Handle ABI

- The public lowered handle remains `%Tree__TreeHandle = { ptr, i64 }`.
- The pointer lane carries the tree store state.
- The `i64` key lane packs the exact tag and row index.
- Row index packing traps with `llvm.trap` if the row cannot fit in the key lane.
- A future compact-handle phase may move to store-relative `u32` or `u64` handles, but this is out of scope for the current backend contract.

## Store And Field Semantics

- Tree allocation requires an active tree owner: `perm`, a tree store, an `Arena` value, or an `Arena` reference.
- Tree fields are value reads over handle-lowered storage.
- Tree fields are not stable lvalues in v1.
- Assignment, address-taking, mutable reference binding, and by-reference mutation of tree fields must be rejected.
- Tree field reads must not expose physical row storage pointers as user-visible values.

## Runtime Coverage Expectations

- Both supported layouts should be covered by IR-shape regression tests.
- Native `-emit test` smoke coverage should exercise constructors, field reads, `kind`, `is`, children traversal, fold, rewrite default, record update, clone, and attributes.
- Benchmarks should compare `per_variant_rows` and `category_union` on similar AST workloads before changing defaults or capacity policy.
