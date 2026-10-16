# Default grants for mutable global storage

Stage0 qualification on 2026-10-08, based on core `778c8281f97c81adbb3bc764b634f263c9f36f52`, branch `codex/global-mutable-grants`.

Reads of actual `global mutable` storage require an explicit `Global.Read` grant; writes require `Global.Write`. These are semantic errors with default analyzer and CLI options. Simple assignment charges Write; compound assignment charges both. Reads used to compute assignment indices remain reads. Qualified globals and lexical local shadows use the symbols resolved during body analysis.

Passing or returning a writable reference or mutable view to mutable global storage, copying one into a local, and handing a mutable global arena to an `in` block require Write authority before the capability escapes. This includes mutable views selected from immutable global aggregate fields; read-only views do not transfer Write authority. Readonly references require Read. Mutating collection methods use the resolved collection type and canonical builtin mutator set. Direct calls, generic/function-value calls and concrete callback arguments propagate required mutable-global effects. Lambda bodies receive separate effect summaries.

`can` grants authorize the current lexical scope; trusted grants discharge only their named members. Immutable globals and constants retain their existing advisory behavior. Eight runtime files received explicit, member-selective trusted helper scopes, without expanding exported signature effect rows.

## Validation

All compilation and tests ran remotely in the isolated `/root/work/global-mutable-grants-core` checkout; no local builds were run.

- `GOMAXPROCS=2 go test -p 1 -parallel 2 ./src/semantic -count=1 -timeout=30m`: passed, 59.367s (`semantic-final.log`). Covers the full semantic suite, including new positive/negative grant, scope, reference, callback, generic, container, arena and shadow regressions. Existing lifetime test fixtures received the precise grants their operations now require.
- `GOMAXPROCS=2 go build -p 1 -o elisacore-grants ./src`: passed, empty build log (`build-qualified.log`).
- `GOMAXPROCS=2 ./elisacore-grants -emit semantic runtime/elisacore_std/elisacore_runtime.elisa`: exit 0 (`runtime-qualified.log`).
- Default CLI fixtures containing a direct ungranted read and direct ungranted write each exited 1, respectively reporting `mutable global read` with required `Global.Read` and `mutable global write` with required `Global.Write` (`cli-read.log`, `cli-write.log`). No warning-enforcement flags were supplied.

This qualifies stage0 enforcement and its runtime migration. Stage1 enforcement and proof admission integration are separate lanes and are not certified by this result.

## Follow-up: defaults, methods and function-value joins

Actual default CLI qualification found three admission gaps after the initial commit: omitted default expressions were skipped by the permission traversal, implementation method bodies were absent from the effect fixpoint, and branch-joined callback types lost concrete declaration identities before that fixpoint. Defaults are executable expressions: both a direct mutable-global read and a nested `reader()` call were valid source programs and incorrectly admitted.

The follow-up traverses resolved lowered arguments at the caller's grant scope, includes implementation members in inference and validation, and preserves function-value declaration sources through branch joins and type substitution. A default callback transfers its actual authority even when the receiving function does not invoke it. Caller grants discharge default evaluation; merely declaring a default does not execute it.

Factory callback qualification used an explicit `fn() -> i32 can[Global.Read]` return type. Creating the callback without a grant is allowed; invoking it without Read fails, and invoking it inside a Read grant passes. Explicit declared Global rows are mandatory; inferred immutable-global advisory rows remain distinct. Parenthesized and explicitly generic calls already rejected missing grants.

Remote focused regressions passed (0.021s); the complete semantic suite passed again (58.518s). CLI fixtures and logs are retained under `grants-cli-qualification` in the isolated remote checkout. The frontend rebuild passed with an empty build log and complete default runtime admission exited 0. Final CLI branch-callback and factory-invocation negatives exited 1; granted factory invocation exited 0.

## Bounded callback container and loop audit

Valid CLI programs confirmed that a mutable global function slot initialized with a writer, struct-field callbacks, a callback returned through a local/type alias, optional callbacks returned by a writer-or-null factory, and branch callback joins all reject missing Write even when Read is granted. Optional qualification used `type Action = fn() -> i32 can[Global.Write]`, `Action?`, and an `is callback` bind after a factory call; redundant optional binding after a known nonnull initializer is rejected by existing type analysis.

A concrete loop admission gap remained: assigning the writer inside a while body and leaving with `break` discarded the callable state, because loop jump frames retained ownership and specialized types but omitted function values. The fix records and joins function values on break/continue edges for while, range-for, and collection-for loops. CLI while-break, while-continue, and range-for-break negatives now exit 1 with required Write; the corresponding granted call exits 0. No grant is required merely to manufacture the callback.

The new focused callback-container/loop tests passed (0.021s). The complete remote semantic suite passed again (60.508s); the Linux frontend rebuilt cleanly and complete runtime admission exited 0. Raw CLI fixtures and logs persist under `grants-callback-audit` in the private remote checkout.

## Follow-up: whole-value reads of aggregates with mutable storage

A whole-value read from an immutable global now requires mandatory `Global.Read` when the selected value contains mutable fields, mutable references or views, or nested values with writable storage. This covers returning or copying the aggregate and returning a nested aggregate field. Reading an immutable sibling scalar projection remains advisory. Indexing through a stored mutable view also requires `Global.Read`; a mutable view retains its write capability through an immutable global binding, and writing through it requires both Read and Write. Passing, copying, or returning a mutable view selected from an aggregate field also requires Write. Direct writes to mutable fields remain Write-only; access through a mutable reference or view requires Read and Write.

Focused semantic regressions cover whole and nested aggregate reads, an explicit Read grant, immutable sibling projections, mutable-view indexing and write-through, view transfer through calls/local copies/returns, read-only view behavior, the direct mutable-field write case, and reference projections. The focused tests passed on 2026-10-16; the complete semantic suite was not rerun for this follow-up.

## Follow-up: capability transfers on rebinding and aggregate construction

Mutable-borrow transfer checks now also cover assignment into an existing local binding and
typed aggregate expressions. The source walk follows only value-producing constructors whose
types carry a mutable-borrow capability, including tuples, lists, structs, record updates,
conditional values, optionals and list comprehensions. Calls and returns use the same transfer
check. This preserves `Global.Write` when a mutable view is rebound from an immutable global
alias, placed inside a tuple, or passed in a struct literal to a callee that can mutate it.

SoA row proxies retain the same capability classification. Direct `rows[index].field <- value`
requires Write; iterating `rows.rows()` or its enumerated form requires Read and Write because
the loop exposes writable row proxies. Rebinding a SoA loop row was rejected as a regression
shape because loop variables are immutable and the synthetic row-view type cannot be named in a
local declaration. The mutable-view rebind tests exercise the same reachable capability-transfer
boundary.

Focused transfer regressions and the complete semantic package passed on 2026-10-16 with
`go test ./src/semantic -count=1` (41.575s). The full-package run catches the Windows arena
backend type-check as well. This validates semantic analysis in the current checkout; it does not
replace a fresh frontend build or CLI qualification for this follow-up.
