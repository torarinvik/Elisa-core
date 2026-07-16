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
8. [`08-region-checkpoints.md`](docs/useful_language_features/08-region-checkpoints.md) — implemented region checkpoints plus current `scope`, named checkpoint, grouped checkpoint, and rollback-block syntax.
9. [`09-concurrency-mini-spec.md`](docs/09-concurrency-mini-spec.md) — proposed strict-concurrency model built from share rights, domains, typestate protocol states, predicate waits, progress evidence, and legacy raw-surface migration.
10. [`10-orthogonality-packed-enums-regions-and-affine-concurrency.md`](docs/10-orthogonality-packed-enums-regions-and-affine-concurrency.md) — design note on keeping layout, provenance, regions, and affine concurrency orthogonal.
11. [`11-proof-carrying-views-and-optimization-legality.md`](docs/useful_language_features/11-proof-carrying-views-and-optimization-legality.md) — mini-spec for compiler-internal optimization legality facts derived from views, provenance, shapes, and effects.
12. [`12-nim-orthogonal-language-and-compiler-ideas.md`](docs/useful_language_features/12-nim-orthogonal-language-and-compiler-ideas.md) — notes on orthogonal low-level language design ideas inspired by Nim and how they map onto Elisa core.
13. [`13-v-orthogonal-language-and-compiler-ideas.md`](docs/useful_language_features/13-v-orthogonal-language-and-compiler-ideas.md) — similar design notes inspired by V, with an emphasis on orthogonality and compiler ergonomics.
14. [`14-typestate-system.md`](docs/14-typestate-system.md) — the full typestate guide covering named states, aggregate state placeholders, refinement, mutation, aliasing, concurrency protocol states, and current soundness rules.
15. [`15-typestate-practical-cheat-sheet.md`](docs/useful_language_features/15-typestate-practical-cheat-sheet.md) — short practical companion with motivating examples, widening expectations, and guidance on when to re-prove with `is`.
16. [`16-ref-parameter-poststate-ensures.md`](docs/useful_language_features/16-ref-parameter-poststate-ensures.md) — concrete proposal for unified `ensures`-based call poststate summaries covering both named typestates and pointer/refstates such as `ensures node => !`.
17. [`17-iterators-and-for-in-mini-spec.md`](docs/useful_language_features/17-iterators-and-for-in-mini-spec.md) — design note for the broader sequential iterable model, with companion references now documenting the currently implemented filtered, reverse, and explicit parallel loop surfaces.
18. [`18-current-surface-ergonomics.md`](docs/useful_language_features/18-current-surface-ergonomics.md) — implemented reference for current surface features such as default and named arguments, `..` forwarding, effect declarations, `signal`, local `can` grants, `effectalias` bundles, implicit and explicit `bundle` declarations, brace destructuring and updates, filtered loops, `do:` blocks, `defer`, index fallback, store/dict sugar, explicit `parallel for`, lambdas, tree `rewrite`, char literals, and shorthand cast hooks.
19. [`19-static-interfaces-extension-methods-and-ufcs.md`](docs/useful_language_features/19-static-interfaces-extension-methods-and-ufcs.md) — implemented reference for protocols, associated types, receiver-scoped extension impls, UFCS rewriting, safe call chaining, and the preferred generic specialization surface.
20. [`20-annotations-and-compile-time-hints.md`](docs/useful_language_features/20-annotations-and-compile-time-hints.md) — implemented reference for current layout, packed-lowering, function-codegen, guard, and branch-hint metadata surfaces.
21. [`20-tree-capabilities-and-interface-cleanup.md`](docs/useful_language_features/20-tree-capabilities-and-interface-cleanup.md) — canonical cleanup direction for tree construction, implicit bundles, parser helper style, and capability-oriented frontend structure.
23. [`22-value-fact-core.md`](docs/useful_language_features/22-value-fact-core.md) — design note on the fact-core rule for keeping sugar from obscuring semantic state transitions.

## Notes

- The original monolithic document was split to keep individual files easier to navigate and review.
- Section ordering follows the original progression from syntax ideas to typing rules to implementation planning.
- The newer typestate notes in sections 14 and 15 are a good current entry point if you care about proof-carrying state, mutation, and protocol-style APIs.
- If you want the likely next typestate feature rather than the current model, section 16 is the design note to read next.
- If you care about ergonomic traversal, tree-friendly iteration, and the broader sequential iterable design, section 17 is still the design note to read next.
- If you want the implemented surface syntax that landed more recently than many of the older proposal docs, section 18 is the practical reference to read next.
- If you want the companion reference for static dispatch and receiver-style calls, section 19 covers protocols, extension methods, and UFCS.
- If you want the current annotation, layout-hint, and codegen-hint surface, section 20 is the practical reference to read next.
- If you want the canonical cleanup direction for bundles, tree construction, and parser-helper style, read [`20-tree-capabilities-and-interface-cleanup.md`](docs/useful_language_features/20-tree-capabilities-and-interface-cleanup.md) alongside sections 18 and 19.

## Current reference mutability rule

- `T&` is a readonly reference.
- `mutable T&` is a writable reference.
- This is orthogonal to storage and nullability qualifiers, so the same rule applies to forms like `heap T&`, `any T&?`, and `stack T&`.
- Common places that should now be `mutable T&` are out-parameters, arena/deque/darray mutation helpers, and explicit ref casts passed to callees that write through the reference.
- If the semantic analyzer says a mutation goes through a readonly ref, the usual fix is to change the declaration, parameter, or cast from `T&` to `mutable T&`.
