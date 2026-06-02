# Error Unions (Phase 1) Mini-Spec

## Implementation status (2026-06-02 — discovered during impl)

Most of Phase 1 **already exists** in the compiler (front-end + semantics + backend
+ tests). Verified by probing the live compiler:

- ✅ `error` decls (named payloads), `error[E]` and dotted `error[E.Bad]` sets in
  signatures, bare `try x` propagation (enforces "try without else requires an
  error-union return"), `try x else <value|return|raise|err:…|void>`, unified `else`
  with optionals, value-based union return type (`i64 | E`). See
  `semantic/recovery_syntax_test.go`.
- ✅ **NEW (this work):** error-union values are now must-consume in the bare-drop
  case — a dropped `f()` statement is rejected ("error union result must be
  handled…"). `try`/`return`/propagation discharge it. Zero blast radius on the
  emulator codebase. See `semantic/error_union_affine_test.go` and the `ExprStmt`
  check in `analyzer_flow.go` + `isUnhandledErrorUnionType` in
  `analyzer_flow_affine_consumption.go`.
- ✅ **Affine holes closed:** `_ = f()` (a `DiscardStmt`) is now rejected too; and
  `x: <okType> = f()` already errored (union ⊄ ok type — no implicit unwrap; the earlier
  "hole" was a grep miss). So error unions are must-consume across drop and discard. Zero
  blast radius.
- ✅ **Per-variant inspection exists via `catch`** (ok-binding via the first "success"
  arm, `E.Variant:` error arms, exhaustiveness). The real missing piece — **payload
  binding `E.Bad2(x):`** — is now implemented end-to-end (parser/AST/semantic/backend),
  runtime-verified, with an arity check. Zero blast radius. (`match … as` surface syntax
  remains an optional cosmetic alias of `catch`.)
- 〰️ **Brace-subset sugar `error[E{Bad1,Bad2}]`** doesn't parse, but dotted/repeated
  `error[E.Bad1, E.Bad2]` already works — so this is sugar, low priority.

The spec below is the target design; treat the bullets above as the delta to it.

---



This document specifies **Phase 1** of Elisa's error-union feature: composable,
typed error sets carried in the return type, with `raise`, a `try`
propagation/recovery operator, `match` handling, and an affine "must-consume"
rule.

It is deliberately the self-contained slice that does **not** touch the
permission/effect system. The signature surface is designed so the later phases
(effect-set rework, `as` casts / `trusted` drops, set-polymorphism, inference
defaults) slot in without changing Phase-1 semantics. See "Phase boundaries".

## 1. Motivation

A traditional `Result[T, E]` forces a single error type `E`. Combining error
sources means either one monolithic enum, dynamic erasure (`Box<dyn Error>`), or
per-source conversions. Elisa instead lets a function declare a **set** of error
variants drawn from any number of `error` declarations, anonymously unioned:

```elisa
def read_key(path: cstr, key: cstr) -> cstr
        error[FileError{NotFound, PermissionDenied}, ParseError{MissingKey}]:
    ...
```

`error[...]` is a member-granular set, mirroring how `can[...]` will read after
the effect rework. The two are duals: `can[...]` is *required* (contravariant),
`error[...]` is *produced* (covariant). Phase 1 only builds `error[...]`.

## 2. Scope

In Phase 1:

- `error` type declarations (variants, optional named payloads).
- `error[...]` sets in function signatures, with `ErrType{Member, ...}` and
  `ErrType.Member` granularity. **Explicit sets are required in Phase 1** (the
  checking rule is written as `inferred ⊆ declared` so Phase 2 only flips the
  default to inference).
- `raise Variant` / `raise Variant(payload)`.
- `try` operator: bare propagation + `else <fallback>` recovery (+ optional
  error binding).
- `match <expr> as <ok>:` handling with ok-binding, exhaustive-or-rethrow.
- The **affine / must-consume** rule for error-union values.
- Value-based lowering to a tagged union. No stack unwinding.

Out of scope (later phases): effect-set rework, `as`/`trusted` on capabilities,
set-polymorphism, error-set inference defaults, separately-compiled polymorphic
combinators.

## 3. Grammar

### 3.1 Declarations

```
error_decl   := "error" IDENT ":" NEWLINE INDENT variant+ DEDENT
variant      := IDENT ("(" field ("," field)* ")")? NEWLINE
field        := IDENT ":" type            # named payload field
              | type                       # positional (sugar; name = _0, _1, ...)
```

```elisa
error FileError:
    NotFound(path: cstr)
    PermissionDenied(path: cstr)

error ParseError:
    BadSyntax(line: u32)
    MissingKey(key: cstr)
    Empty                                  # no payload
```

An `error` type is a closed, named set of variants. Variants may carry payloads,
including **owned/affine** payloads (see §6.4).

### 3.2 Error sets in signatures

```
error_set    := "error" "[" set_member ("," set_member)* "]"
set_member   := IDENT "{" IDENT ("," IDENT)* "}"   # subset of a type's variants
              | IDENT "." IDENT                     # single variant
              | IDENT                               # all variants of the type
```

A function's full return type is the **error union** `T error[S]`, read as
`T | S` — either the ok value of type `T` or one of the error variants in set
`S`. `void error[S]` is permitted (the ok payload is unit).

The canonical form of `S` is a set of `(ErrType, Variant)` pairs; `FileError`
(bare) expands to all of `FileError`'s variants. Duplicate/overlapping members
collapse by set union.

### 3.3 `raise`

```
raise_stmt   := "raise" qualified_variant ("(" arg ("," arg)* ")")?
qualified_variant := IDENT "." IDENT
```

`raise FileError.NotFound(path)` constructs an error value and returns it from
the enclosing function. Legal only where `(FileError, NotFound)` is in the
enclosing function's declared set (§4.3).

### 3.4 `try`

```
try_expr     := "try" expr                         # propagate on error, yield ok value
              | "try" expr "else" fallback         # recover (blanket, error discarded)
              | "try" expr "else" IDENT ":" fallback  # recover, bind the error value
fallback     := expr                               # value of type T
              | "return" expr | "raise" ... | "break" | "continue" | ...  # control flow
```

This is the **same `else <fallback>` grammar as the existing `get x else …`**
construct. Bare `try e` propagates; `try e else f` recovers.

### 3.5 `match` over an error union

```
match_expr   := "match" expr "as" IDENT ":" NEWLINE INDENT match_arm+ DEDENT
match_arm    := "ok" ":" block
              | qualified_variant ("(" bind ("," bind)* ")")? ":" block
              | ("else" | "_") ":" block
              | ("else" | "_") ":" "rethrow"        # propagate the unhandled remainder
```

Every arm is uniform `pattern: block`. The `ok` arm sees the bound ok value
(`IDENT` from `as`). Error arms bind payloads positionally/by name. `rethrow`
re-raises the unhandled remainder (legal only if that remainder ⊆ the enclosing
function's declared set).

`catch` from earlier sketches is **dropped**; `match … as` subsumes it.

## 4. Static semantics

Let `ErrSet` be a set of `(ErrType, Variant)` pairs. `ErrUnion(T, S)` is the
type of a value that is either `T` or an error in `S`.

### 4.1 Subtyping (widening)

`ErrUnion(T, S1) <: ErrUnion(T, S2)` iff `S1 ⊆ S2`. Producing fewer errors than
declared is always sound (covariant in the error set). There is no narrowing.

### 4.2 Declared-vs-actual (forward-compatible with inference)

A function with declared set `D` must satisfy `actual ⊆ D`, where `actual` is the
union of: every `raise`d variant, and the propagated set of every bare `try`
(§4.4). In Phase 1 `D` is written explicitly; in Phase 2 an omitted `D` is
inferred as exactly `actual` and an explicit `D` remains a checked upper bound.

### 4.3 `raise`

`raise E.V(args)` typechecks iff `(E, V) ∈ D` (enclosing declared set), `V`'s
payload arity/types match `args`, and the enclosing function returns an error
union. It contributes `(E, V)` to `actual`.

### 4.4 `try`

For `e : ErrUnion(T, S)`:

- `try e` (bare) has type `T`; it requires `S ⊆ D` (propagation) and contributes
  `S` to `actual`. Illegal if the enclosing function is not an error union (use
  `else`).
- `try e else f` has type `T`; `f` must be of type `T` *or* diverge
  (return/raise/break/...). No constraint on `D` for the recovered errors. The
  error value is consumed (discarded) — affine rule satisfied.
- `try e else err: f` is as above but binds `err` (the error value, an
  `ErrUnion`-error) in `f`, e.g. to wrap it: `raise ConfigError.Load(err)`.

### 4.5 `match`

`match e as n:` over `e : ErrUnion(T, S)` requires the arms to **cover** `{ok} ∪
S`: either an arm per variant in `S` plus an `ok` arm, or an `else`/`_` arm. An
`else: rethrow` discharges the remainder `S' ⊆ S` by propagation, requiring `S'
⊆ D`. The construct's type is the join of its arm block types (or `void` if used
as a statement). `n` is bound only in the `ok` arm.

### 4.6 Affine / must-consume

An expression of type `ErrUnion(T, S)` is **affine and must be consumed exactly
once**. It is consumed by: `try` (bare or `else`), `match`, or being the operand
of a `raise`/`return` that propagates it. A bound error union (`r = foo()`) must
be consumed before `r` leaves scope. Dropping an error union (letting it die
unconsumed) is a **compile error**, in the same class as dropping an owned region
value. Explicit discard is written `_ = try foo() else <handle>` — i.e. you must
say how you handled it.

Consequences: no silently swallowed errors; and if an error variant carries an
owned payload, the handler is forced to consume it (§6.4), so resources are not
leaked on the error path.

## 5. Dynamic semantics / lowering

Error unions are **values**, not exceptions. No stack unwinding.

`ErrUnion(T, S)` lowers to a tagged union:

```
{ tag: uN, payload: union { ok: T, <one field per variant in S> } }
```

`tag = 0` is ok; `tag = k` selects error variant `k` (a stable ordering over the
canonical `(ErrType, Variant)` set). Single-error-set unions may use a niche/
pointer-tag optimization later; Phase 1 may use the straightforward tagged repr.

- `raise E.V(a)` → build `{tag: k, payload.V: a}` and return it.
- `try e` → evaluate `e`; if `tag == 0`, extract `payload.ok`; else **re-tag**
  the error into the enclosing function's union and `return` it. (Re-tag is a
  small remap since `S ⊆ D`.)
- `try e else f` → if `tag == 0` extract ok; else evaluate `f` (binding `err` to
  the error value when present).
- `match` → switch on `tag`; bind payloads from the active union field;
  `rethrow` re-tags-and-returns the remainder.

All checks (subset, exhaustiveness, affine) are static; the only runtime cost is
the tag test/branch — equivalent to hand-rolled `Result` plumbing.

## 6. Interactions with existing features

### 6.1 Optionals (`T?`)
Orthogonal. `T?` is presence/absence with no cause; `ErrUnion` carries which
error and a payload. `get x else …` and `try x else …` share the `else
<fallback>` grammar intentionally.

### 6.2 `Abort.Panic`
Distinct. `raise` is a **recoverable value** that the type system tracks; panic
is unrecoverable. `try`/`match` never catch panics. A function that only panics
has no `error[...]`.

### 6.3 Regions / affine values
Error unions are affine like owned values; the must-consume rule reuses the same
"unconsumed owned value" machinery. An error union may itself be returned/stored
(then consumed later), as long as the consume-exactly-once invariant holds.

### 6.4 Owned payloads
A variant may carry an owned/affine payload (`Locked(handle: FileHandle)`).
Matching binds it (`Locked(h): h.close()`), which consumes it. Because the union
is affine, you cannot drop the error and leak `h`.

## 7. Worked examples

```elisa
error FileError:
    NotFound(path: cstr)
    PermissionDenied(path: cstr)
error ParseError:
    BadSyntax(line: u32)
    MissingKey(key: cstr)

def read_key(path: cstr, key: cstr) -> cstr
        error[FileError{NotFound, PermissionDenied}, ParseError{MissingKey}]:
    if not file_exists(path):
        raise FileError.NotFound(path)
    # ...
    return "value"

# Propagation: each `try` unions read_key's set into load_config's declared set.
def load_config(path: cstr) -> Config
        error[FileError{NotFound, PermissionDenied}, ParseError{MissingKey}]:
    name = try read_key(path, "name")
    host = try read_key(path, "host")
    return Config(name, host)

# Recovery + control-flow fallbacks (same grammar as `get … else`).
def load_or_default(path: cstr) -> Config:
    return try load_config(path) else Config.default()
def load_or_bail(path: cstr) -> int:
    cfg = try load_config(path) else return -1
    return 0

# Cause-preserving wrap.
def load_wrapped(path: cstr) -> Config error[ConfigError{Load}]:
    return try load_config(path) else err: raise ConfigError.Load(err)

# Inspect per-variant; exhaustive-or-rethrow.
def report(path: cstr) -> void error[ParseError{MissingKey}]:
    match load_config(path) as cfg:
        ok: use(cfg)
        FileError.NotFound(p): log(f"missing file {p}")
        FileError.PermissionDenied(p): log(f"denied {p}")
        ParseError.MissingKey(k): rethrow      # remainder ⊆ report's declared set

# Affine: this is a COMPILE ERROR (error union dropped unconsumed):
def oops(path: cstr) -> void:
    load_config(path)        # ❌ must try/match/discard
```

## 8. Phase boundaries

- **Phase 2 — inference.** Omitted `error[...]` ⇒ inferred = `actual`; explicit ⇒
  checked upper bound. §4.2 already states the rule.
- **Phase 3 — effect-set rework.** `can[ErrType{...}]` member granularity +
  subsumption lattice; shares the set machinery but on the *required* side.
- **Phase 4 — casts/drops.** `as Family` (checked subsumption only) and bare
  `trusted X:` (drop only). No `as _`, no trusted-casts. Effect side only;
  errors keep using `try`/`match`.
- **Phase 5 — set-polymorphism.** Error/effect sets become inferred generic
  params (`E`, `R`); precision via monomorphization, `any`/⊤ as the erasure
  escape. No row polymorphism (union-with-literal covers addition; subtraction is
  not needed).

## 9. Test plan (mirrors existing semantic tests)

Analyzer (`analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics`):

- `raise` of a variant not in the declared set → error.
- bare `try` whose callee set ⊄ declared set → error; `⊆` → ok.
- `try … else <value>` / `else return` / `else raise` / `else err:` typecheck.
- `match` non-exhaustive without `else`/`rethrow` → error; exhaustive → ok;
  `else: rethrow` with remainder ⊄ declared → error.
- affine: dropped error union → error; consumed via try/match/discard → ok;
  owned payload not consumed in arm → error.
- widening: `ErrUnion(T,S1)` assignable where `S2 ⊇ S1` expected; `S1 ⊄ S2` →
  error.

Backend (IR grep, like `llvm_index_watchdog_test.go`): tag construction on
`raise`, branch on `try`/`match`, re-tag on propagation, no unwinding tables.

Runtime (native test): end-to-end ok path, each error path, recovery, owned-
payload release on the error path.
