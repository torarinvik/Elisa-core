# Useful language features

This document is now a landing page for the split design notes in `docs/useful_language_features/`.

## Sections

1. [`01-memory-layout-syntax.md`](docs/useful_language_features/01-memory-layout-syntax.md) — memory layout modifiers, regions, and suggested implementation order.
2. [`02-pointer-typestate-practical.md`](docs/useful_language_features/02-pointer-typestate-practical.md) — practical pointer typestate rules and usage patterns.
3. [`03-pointer-typestate-formal.md`](docs/useful_language_features/03-pointer-typestate-formal.md) — formal typing judgments and refinement rules for pointer typestate.
4. [`04-length-indexed-arrays-and-strings.md`](docs/useful_language_features/04-length-indexed-arrays-and-strings.md) — design discussion for length-indexed arrays and strings.
5. [`05-array-string-shape-mini-spec.md`](docs/useful_language_features/05-array-string-shape-mini-spec.md) — mini-spec for shape-typed arrays and strings.
6. [`06-compiler-implementation-plan.md`](docs/useful_language_features/06-compiler-implementation-plan.md) — staged compiler roadmap for shape-typed arrays and strings.
7. [`07-export-and-c-abi.md`](docs/useful_language_features/07-export-and-c-abi.md) — mini-spec for explicit export declarations, stable C ABI names, and header/object-file interop.

## Notes

- The original monolithic document was split to keep individual files easier to navigate and review.
- Section ordering follows the original progression from syntax ideas to typing rules to implementation planning.
