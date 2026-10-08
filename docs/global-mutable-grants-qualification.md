# Default grants for mutable global storage

Stage0 qualification on 2026-10-08, based on core `778c8281f97c81adbb3bc764b634f263c9f36f52`, branch `codex/global-mutable-grants`.

Reads of actual `global mutable` storage require an explicit `Global.Read` grant; writes require `Global.Write`. These are semantic errors with default analyzer and CLI options. Simple assignment charges Write; compound assignment charges both. Reads used to compute assignment indices remain reads. Qualified globals and lexical local shadows use the symbols resolved during body analysis.

Passing or returning a writable reference to mutable global storage, constructing a writable alias, and handing a mutable global arena to an `in` block require Write authority before the capability escapes. Readonly references require Read. Mutating collection methods use the resolved collection type and canonical builtin mutator set. Direct calls, generic/function-value calls and concrete callback arguments propagate required mutable-global effects. Lambda bodies receive separate effect summaries.

`can` grants authorize the current lexical scope; trusted grants discharge only their named members. Immutable globals and constants retain their existing advisory behavior. Eight runtime files received explicit, member-selective trusted helper scopes, without expanding exported signature effect rows.

## Validation

All compilation and tests ran remotely in the isolated `/root/work/global-mutable-grants-core` checkout; no local builds were run.

- `GOMAXPROCS=2 go test -p 1 -parallel 2 ./src/semantic -count=1 -timeout=30m`: passed, 59.367s (`semantic-final.log`). Covers the full semantic suite, including new positive/negative grant, scope, reference, callback, generic, container, arena and shadow regressions. Existing lifetime test fixtures received the precise grants their operations now require.
- `GOMAXPROCS=2 go build -p 1 -o elisacore-grants ./src`: passed, empty build log (`build-qualified.log`).
- `GOMAXPROCS=2 ./elisacore-grants -emit semantic runtime/elisacore_std/elisacore_runtime.elisa`: exit 0 (`runtime-qualified.log`).
- Default CLI fixtures containing a direct ungranted read and direct ungranted write each exited 1, respectively reporting `mutable global read` with required `Global.Read` and `mutable global write` with required `Global.Write` (`cli-read.log`, `cli-write.log`). No warning-enforcement flags were supplied.

This qualifies stage0 enforcement and its runtime migration. Stage1 enforcement and proof admission integration are separate lanes and are not certified by this result.
