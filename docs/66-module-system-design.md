# 66 — Module system design (library composition without name collisions)

## Motivation

Elisa has one flat global declaration namespace. Two files that both declare a
top-level name collide, even when the declarations are identical. Concretely, the
emulator cannot pull in the stdlib `dict` because `collections.elisa → deque.elisa →
arena.elisa` declares `const PROT_READ = 0x1` while the emulator already declares
`const PROT_READ: int = 1` (address_space.elisa) — same value, but a "duplicate
declaration" error. This blocks composing any library that incidentally shares a
common name with the consumer.

The fix is real per-module namespacing so a library's declarations are isolated and
referenced explicitly (qualified or via `using`).

## What already exists (build on this)

- `namespace Name:` / `module Name:` — an **indented block** whose decls are prefixed
  with `Name.` (`ast.NamespaceDecl`, parser `parseNamespaceDecl`).
- `using Name` — brings a namespace's names into scope (`ast.UsingDecl`).
- Resolution: `flattenScopedDeclsWithVisibility` recursively qualifies decls
  (`joinQualifiedName`), threads `using` inheritance and `private` visibility, and
  `visibleNameCandidates` / `withResolutionContext` resolve qualified+`using` names.
  `currentNamespace` / `currentUsings` track context during analysis.

So name *resolution* across namespaces works today. Three things block using it for
library composition:

## The three gaps

### 1. The include model concatenates before parsing (no module boundaries)

`buildProjectLoadedProgram` / `readSourceWithIncludes` textually concatenate every
included file into ONE source buffer, parsed once. After concatenation there are no
file boundaries, so there is nowhere to attach a per-file namespace. A file-level
`module std` "namespace the rest of this file" directive can't work — post-concat it
would namespace everything after it across file seams.

**Design:** make `include` able to wrap an included file's decls in a namespace at the
**AST** level, not textually. Two sub-options:
- (a) **Namespaced include**: `include "path/x.elisa" as std` — the include expander/
  loader parses `x.elisa` (and its transitive includes) as its own unit and wraps the
  resulting decls in a synthetic `NamespaceDecl{Name: "std"}`. Reuses all existing
  namespace resolution. Minimal new surface; the wrapping is the only new mechanic.
- (b) **File-declared module**: a file may start with `module std` (no block) and the
  loader treats the whole file's decls as that module. Requires the loader to track
  per-file decl groups (parse-per-file) rather than concatenate. Cleaner for authors,
  bigger loader change.

Recommended: **(a) first** (smaller, reuses the block-namespace path), **(b)** later as
sugar once parse-per-file exists.

### 2. The runtime contract is 77 hardcoded bare names

The backend/semantic reference ~77 runtime functions by **unqualified** name
(`arena_alloc`, `arena_dict_get`, `arena_da_append`, `ctx_strlen`, …) — codegen emits
calls to these literal symbols, and generic collection ops monomorphize into them. If
the stdlib is namespaced (`std.arena_dict_get`), every one of these breaks.

**Design — reserved runtime namespace / global carve-out:** the runtime-support layer
(arena, ctx_*, the generic collection ops the compiler lowers to) stays in a reserved
global namespace that is *always* in scope and is what codegen targets. Options:
- Keep the runtime files in the global namespace (do NOT namespace them); only
  namespace *other* libraries. The collision class we hit (PROT_READ) is between a
  library and the consumer, not the runtime per se — but `arena.elisa` mixes the
  runtime allocator with OS constants, so it would need splitting: runtime symbols stay
  global, the OS-constant decls move under a namespaced `os` module (or are guarded).
- Or: tag codegen-referenced runtime functions as "extern-C-like" reserved names exempt
  from namespacing, and let everything else namespace normally.

Either way the 77 names must be enumerated and pinned as the stable codegen↔runtime ABI.
This is the bulk of the work and the main risk.

### 3. `static if` and generic monomorphization interaction

`arena.elisa`'s consts are inside `static if ARENA_BACKEND == …:`. Namespacing must
compose with `static if` (the flattener already recurses `StaticIfDecl`). Generic
collection ops instantiated from a namespaced `dict` must still emit the reserved
runtime symbol names (per gap 2), independent of the call site's namespace.

## Migration / staging (opt-in, non-breaking)

1. **Reserved runtime namespace**: enumerate the 77 names; split `arena.elisa` so the
   allocator/runtime stays global and OS constants move to a namespaced/guarded module.
   No consumer changes yet. (Largest, riskiest step — do first, in isolation.)
2. **Namespaced include `… as std`**: implement AST-level wrapping; add a test that two
   files declaring the same name compose when one is included `as`.
3. **Namespace the stdlib** behind `std` (collections/heap/strings/…), runtime symbols
   excepted. Existing global consumers keep working (they don't use the namespaced
   form); new consumers `include … as std` + `using std`.
4. **Emulator**: `include elisacore_std … as std` → `dict` composes (PROT_READ etc. now
   `std.os.PROT_READ`, no clash with the emulator's global `PROT_READ`).
5. (later) File-level `module` sugar via parse-per-file.

## Scope / risk

This is a multi-session, compiler-architecture effort. The high-risk core is gap 2 (the
77-name runtime ABI carve-out) and gap 1 (include-model change from concatenate-then-
parse to parse-and-namespace). Do step 1 in isolation with the full `go test ./src/...`
suite + the emulator boot as the regression gate before touching consumers.

## Not required for the boot-speed work

The symbol-index that motivated this (O(1) export lookup in the loader) does **not**
need the module system: it can use a contained darray-based open-addressing hash inside
module.elisa (no stdlib `dict`, no compiler change). The module system is the strategic
fix for library composition generally; the boot-speed win should not wait on it.
