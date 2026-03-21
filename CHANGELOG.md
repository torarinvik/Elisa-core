# Changelog

All notable changes to this repository should be documented in this file.

## Unreleased

### Highlights

- First-class `refstorage` / `refstate` generics now work end to end across parsing, semantic analysis, specialization, exports, LLVM lowering, and C header generation.
- Concrete export wrappers such as `keep_handle[heap, &]` now parse and lower correctly, including stable public header emission for concrete qualifier-specialized exports.
- A compile-checked showcase for the feature now lives at `Code/test_programs/ref_qualifier_generics.llcontext`.

### Added

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
  - named qualifier parsing
  - nearest-`&` state attachment
  - qualifier-generic call inference
  - qualifier-specialized LLVM lowering
  - concrete export/header behavior

### Changed

- Mixed generic argument order is now declaration order for:
  - ordinary type parameters
  - `refstorage`
  - `refstate`
- Export specialization parsing now accepts generic literal args like `keep_handle[heap, &]`.
- Call inference now binds `refstorage` and `refstate` parameters from concrete argument types.
- Export type validation now rejects unresolved qualifier-parameter types at the C ABI boundary.

### Compatibility

- Existing anonymous aggregate-state syntax remains supported.
- `region` and `permission` parameters are not part of explicit generic specialization order.
- Legacy nullable-array syntax like `&?[COUNT]` still parses as before; named refstate syntax only attaches on direct `&[name]`.

### Documentation

- Expanded pointer typestate documentation in:
  - `docs/useful_language_features/02-pointer-typestate-practical.md`
  - `docs/useful_language_features/03-pointer-typestate-formal.md`
  - `docs/useful_language_features/07-export-and-c-abi.md`
- Added a compile-checked end-to-end feature example at:
  - `Code/test_programs/ref_qualifier_generics.llcontext`

### Verification

- Verified with a full compiler test sweep:
  - `cd /Users/torarinbjarko/Documents/FSharpProjects/LowLevelContextlang/compiler && go test ./...`
