# Value + Fact Core

This document is the cleanup spine for llcontext's safety, optimization, tree,
effect, and DSL features.

The short model is:

```text
Value = Representation + Facts
Operation = Fact transformations + runtime code
```

Surface syntax may be compact, but it should never be semantically magical. If a
feature changes what the compiler knows about a value, that change should fit one
of the fact transformations below.

## Fact Classes

Every value can carry facts from several orthogonal classes:

| Fact class | Examples | Current surface |
| --- | --- | --- |
| Representation | scalar, struct, packed handle, view, array | ordinary types, `packed enum`, `dview`, `darray` |
| Ref state | non-null, maybe-null, null | `T&`, `T&?`, `T!` |
| Shape | exact length identity, const extent, view bounds | `darray[T, shape]`, `dstr[key_shape]`, dense views |
| Typestate | domain/protocol state | `Player[Alive]`, `Thread[T, Joinable]` |
| Storage | stack, heap, static, named region | `heap T&`, `scratch T&`, `new[scratch]` |
| Region deps | arena generation/checkpoint dependencies | `region`, `mark`, `restore`, `reset` |
| Store deps | packed tree/IR store provenance | `Expr.Store[Local]`, `Expr.Store[Frozen]`, `in store:` |
| Alias class | which paths may refer to the same mutable cell | ref params, mutable references, call boundaries |
| Usage | copyable, affine, consumed, moved | `move`, thread/guard protocols |
| Effects | ambient authority required | `effects[...]`, `can ...:` |
| Error path | alternate failure/control exits | `raise`, `try`, nullable `else` recovery |
| Optimization | readonly, exclusive, contiguous, exact extent | dense loops, frozen scans, parallel legality |

These facts do not all need to be written by the programmer. The important rule
is that they are all part of one model, not separate mini-languages.

## Fact Transformations

The core transformations are:

| Transform | Meaning | Examples |
| --- | --- | --- |
| `produce` | create a value with fresh facts | `new[...]`, `node[...]`, packed constructors, `darray_new` |
| `refine` | gain stronger knowledge | `if p != null`, `assert`, `match`, variant tests |
| `widen` | intentionally lose precision | unknown ref calls, mutation through aliases, loop joins |
| `recompute` | derive exact facts after a write | assignment, field mutation with known derived state |
| `consume` | spend an affine value/capability | `move`, `join`, lock guard release |
| `invalidate` | make dependent values unusable | `restore`, `reset`, `destroy`, region rewind |
| `rebase` | shift identity/provenance while preserving payload meaning | `freeze(move store)` |
| `require` | demand authority or proof before an operation | `can[...]`, non-null deref, sendable/shareable checks |
| `ensure` | promise/prove post-call facts | `ensures job => Ready`, `ensures node => !` |

This is the proposed internal calculus. Existing analyzer code can stay
specialized where that is clearer, but every specialized rule should be
explainable with these verbs.

## Canonical Examples

### Null check

```llcontext
if p != null:
    use(p)
```

Fact view:

```text
refine p: RefState maybe-null -> non-null
require use(p): RefState non-null
```

### Region checkpoint restore

```llcontext
mark scratch as cp
tmp: scratch Node& = new[scratch] Node(...)
restore scratch from cp
```

Fact view:

```text
produce tmp:
    Storage = scratch
    RegionDeps = scratch after cp
invalidate values with RegionDeps = scratch after cp
```

### Packed tree construction

```llcontext
return node[span = left.span + right.span] Pascal.Expr.Binary(left: left, right: right)
```

Fact view:

```text
require active tree store/allocator
require Memory.Allocate when the selected store backend allocates
produce Pascal.Expr handle:
    Representation = packed/tree handle
    StoreDeps = active Pascal.Expr.Store[Local]
    common.span = left.span + right.span
```

The syntax is allowed to hide constructor plumbing. It must not hide the fact
that this is a producing operation tied to a store/allocator.

### Freeze

```llcontext
frozen: Expr.Store[Frozen] = freeze(move store)
```

Fact view:

```text
consume store: Expr.Store[Local]
rebase handle provenance: Expr.Store[Local] -> Expr.Store[Frozen]
produce frozen: Expr.Store[Frozen]
```

This is not "just a cast". It is a publication boundary.

### Effects and local grants

```llcontext
def log(text: u8&) -> void effects[Console.Write]:
    return puts(text) can Console.Write
```

Fact view:

```text
signature requires effect authority Console.Write
local grant satisfies effect use at puts(text)
```

Effects are authority facts. They should not replace typestate facts such as
`MutexGuard[Held]`; they describe permission to perform an operation, not the
protocol state of a value.

### Ensures

```llcontext
def finish_ok(mutable job: ParseJob[Pending]&) -> void ensures job => Ready:
    job.status <- ParseJobStatus.Ready
```

Fact view:

```text
recompute job typestate from mutation
ensure normal return: job Typestate Ready
```

`ensures` applies to normal return paths unless the clause explicitly says
otherwise. Error paths need their own fact story; they should not silently get
success postconditions.

### Ref-call widening

```llcontext
extern unknown_update(mutable player: Player[Alive]&) -> void

def use(mutable player: Player[Alive]&) -> void:
    unknown_update(player)
```

Fact view:

```text
widen player Typestate <- unknown_update(player)
```

The call source matters. If an `ensures ... => preserve` proof later fails, the
compiler should be able to point at the call that caused the loss of precision,
not merely say that some unknown widening happened.

### Error path exits

```llcontext
def read() -> int error[FileError]:
    raise FileError.NotFound
```

Fact view:

```text
produce <error> ErrorPath <- FileError.NotFound
```

Similarly, `try checked()` without a fallback produces a propagated error path;
`try checked() else fallback` and nullable `value else fallback` produce handled
alternate paths. These are not success-path `ensures` facts.

## Surface Design Rule

The pyramid remains the language direction:

- low-level code can spell allocation, regions, stores, effects, and mutation directly
- higher-level sugar can compress common patterns
- DSL-like grammar/tree syntax can sit on top

But every sugar layer must obey this rule:

> Syntax may hide boilerplate, but it must not hide fact transitions from the
> compiler, diagnostics, docs, or formatter.

That means future diagnostics should prefer messages like:

```text
cannot use expr after restore scratch: value depends on invalidated region facts
cannot publish handle: Expr depends on Store[Local], expected Store[Frozen]
cannot call parse: missing required effect fact Memory.Allocate
```

rather than treating each subsystem as unrelated.

## Migration Plan

1. Keep existing precise implementations in place.
2. Use the shared fact vocabulary in docs, diagnostics, and tests.
3. Gradually map current structures onto the model:
    - `GuardFactSet` / `RefinementFacts` = refinement facts
   - `OptimizationFacts` = optimization/provenance facts
   - `RefType.State` = refstate facts
   - `Shape` = shape facts
   - `EnsuresClause` = normal-return ensure transforms
   - `effects[...]` / `can` = required authority facts
   - packed store state = store dependency and rebase facts
4. Add diagnostics that explain `widen`, `invalidate`, and `rebase` explicitly.
5. Only after the vocabulary is stable, consider merging internal fact stores.

Current implementation foothold:

- `compiler/src/semantic/facts.go` defines the shared fact class, transform names, typed transform sources, metadata details, formatter, grouped formatter, and per-function snapshot formatter
- region invalidation diagnostics now describe invalidated region dependency facts
- local-region escape and thread-transfer diagnostics use the same provenance vocabulary
- `ensures` proof failures now describe missing `ensure` proofs against current tracked facts, and conservative call precision loss uses the `widen` vocabulary
- function analysis records allocation/tree/store `produce`, control-flow guard and alias-class `refine`, conservative call-site `widen`, flow-instruction `recompute`/`consume`, region lifecycle `invalidate`, store-publication `rebase`, effect authority `require`, declaration postcondition `ensure`, return exits, and error-path transforms
- CFG blocks now carry their own projected fact transforms, while the function analysis keeps the deduped per-function stream
- semantic reports now include `fact_snapshot`, flat `fact_transforms`, grouped `fact_groups`, and per-block `fact_blocks`
- conservative call widening stores the call-site source, such as `unknown_update(player)`, alongside the widened target
- region invalidation transforms carry detail tags such as `operation=restore region checkpoint` and `checkpoint=cp`
- return provenance snapshots surface explicit packed/tree store dependency labels such as `Expr.Store[Frozen]`
