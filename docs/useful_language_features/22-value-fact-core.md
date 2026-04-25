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

## Rule Catalog

The core transformations are the vocabulary diagnostics, reports, and lowering
should use when explaining semantic behavior:

| Transform | Meaning | Examples |
| --- | --- | --- |
| `produce` | create a value with fresh facts | `new[...]`, `node[...]`, packed constructors, `darray_new`, returns |
| `refine` | gain stronger knowledge | `if p != null`, `assert`, `match`, variant tests |
| `widen` | intentionally lose precision | unknown ref calls, mutation through aliases, loop joins |
| `recompute` | derive exact facts after a write | assignment, field mutation with known derived state |
| `consume` | spend an affine value/capability | `move`, `join`, lock guard release |
| `invalidate` | make dependent values unusable | `restore`, `reset`, `destroy`, region rewind |
| `rebase` | shift identity/provenance while preserving payload meaning | `freeze(move store)` |
| `require` | demand authority or proof before an operation | `can[...]`, non-null deref, sendable/shareable checks |
| `ensure` | promise/prove post-call facts | `ensures job => Ready`, `ensures node => !` |

Existing analyzer code can stay specialized where that is clearer, but every
specialized rule should be explainable with these verbs. A transform should also
carry enough provenance to answer three debugging questions:

- what fact class changed?
- what source operation changed it?
- what path, alias class, region generation, or store dependency was affected?

## Implementation Anchors

The current implementation deliberately exposes these facts in one shared place:

- `compiler/src/semantic/facts.go` defines fact classes, transform kinds,
    transform sources, source positions, details, snapshots, path facts, exit
    summaries, alias sets, and formatting/explanation helpers.
- `compiler/src/semantic/flow_ir.go` and `flow_instrs.go` attach source
    positions to CFG flow instructions so fact traces can point back to the
    producing operation.
- `compiler/src/semantic/fact_transform_projection.go` converts CFG, signature,
    permission, widening, alias, region, freeze, and return information into
    fact transforms.
- `compiler/src/semantic/function_analysis.go` stores the deduped per-function
    stream plus `FactSnapshot`, `FactExitSummary`, `AliasSets`, and block-local
    transforms.
- `compiler/src/semantic_report.go` prints these fields in `-emit semantic` and
    provides a fact-only trace with `-emit facts` or `-emit fact-trace`.

The executable fixture `Code/test_programs/fact_core_rules.llcontext` is the
compile-checked smoke sample for the catalog.

## Reporting Surface

`-emit semantic` is still the broad report. Fact-specific work can use:

```sh
go run ./src -emit facts ../Code/test_programs/fact_core_rules.llcontext
go run ./src -emit fact-trace -filter fact_core_rules ../Code/test_programs/fact_core_rules.llcontext
```

The report surface is intentionally close to the catalog:

- `fact_snapshot` / `snapshot` summarize final per-function facts.
- `fact_exits` / `exits` split normal-return facts from error-path facts.
- `fact_aliases` / `aliases` list alias classes and mutated classes.
- `fact_groups` / `groups` group transforms by verb and fact class.
- `fact_blocks` shows the block-local projection.
- `fact_explanations` / `explanations` renders human-readable provenance such as
    `widen player from FactPlayer[Alive] to FactPlayer after unknown_update(player)`.

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
Fact traces should show the consume, rebase, and produce steps, including the
store dependency detail (`store_deps=store`) for the produced frozen handle.

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
Widen transforms should carry `before` and `after` details and a source
position.

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

### Alias class mutation

```llcontext
alias: mutable Node& = first
alias.value <- alias.value + 1
```

Fact view:

```text
refine alias AliasClass <- first
recompute alias-class#0 AliasClass <- mutation
```

Alias classes are path facts. Reports should expose both the individual alias
refinement and the equivalence class that must be recomputed after mutation.

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

## Cleanup Rules

1. Keep existing precise implementations where they encode real semantics.
2. When adding a language feature, decide which fact classes it touches before
     adding surface syntax.
3. When adding a diagnostic, prefer the shared vocabulary: consumed usage facts,
     nullable refstate requirements, missing effect authority facts, invalidated
     region dependency facts, widened typestate facts.
4. When adding a report field, make it path-aware when the fact belongs to a
     value path rather than the whole value.
5. When a mutable reference may alias another location, update the alias class,
     not only the local path.
6. When region state changes, record generation-before and generation-after
     details for invalidation transforms.
7. When a packed/tree store is frozen, model it as consume + rebase + produce.
8. When a normal-return postcondition is checked, separate success facts from
     error-path facts.
9. Keep fact formatting deterministic so golden tests and LLM review are stable.
10. Add compile-checked fixtures for new fact rules instead of documentation-only
     examples.

Current implementation foothold:

- region invalidation diagnostics now describe invalidated region dependency facts
- local-region escape and thread-transfer diagnostics use the same provenance vocabulary
- consumed affine diagnostics use the shared usage-fact wording
- nullable recovery and optional-chain diagnostics mention refstate fact
    requirements
- effect permission warnings end with `missing required effect facts`
- `ensures` proof failures describe missing `ensure` proofs against current
    tracked facts, and conservative call precision loss uses the `widen`
    vocabulary with source call text
- function analysis records allocation/tree/store `produce`, control-flow guard
    and alias-class `refine`, conservative call-site `widen`, flow-instruction
    `recompute`/`consume`, region lifecycle `invalidate`, store-publication
    `rebase`, effect authority `require`, declaration postcondition `ensure`,
    return exits, and error-path transforms
- CFG blocks carry their own projected fact transforms, while function analysis
    keeps the deduped per-function stream
- semantic reports include `fact_snapshot`, `fact_exits`, `fact_aliases`, flat
    `fact_transforms`, grouped `fact_groups`, explanatory `fact_explanations`, and
    per-block `fact_blocks`
- fact-only traces are available with `-emit facts` / `-emit fact-trace`
- conservative call widening stores source position, call-site source, and
    before/after type details
- region invalidation transforms carry detail tags such as
    `operation=restore region checkpoint`, `checkpoint=cp`,
    `generation_before=...`, and `generation_after=...`
- return provenance snapshots surface explicit packed/tree store dependency
    labels such as `Expr.Store[Frozen]`
