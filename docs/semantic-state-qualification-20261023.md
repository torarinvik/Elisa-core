# Semantic-state disk persistence qualification

The semantic analyzer now exposes `RetainedAnalysis.MarshalState` and
`RestoreAnalysisState`. The CLI path remains opt-in while larger programs and edit kinds are qualified.

The state codec writes schema-checked typed values and object IDs. Shared maps,
slices, pointers and cycles survive restoration. Cache bytes cannot supply Go
types or executable callbacks. Checksums, collection/allocation bounds and
reference/type checks reject corrupt records. Typed interior pointers, overlapping slice views with a covering owner, and
slices into array fields now preserve their owner relationships. Uncovered
partial backing-store overlaps still reject the entire snapshot.

Analyzer post passes are callable after restoration, without relying on a closure
from the creating process. Scope binder observation uses explicit function-frame
data with an active collection flag; it no longer retains a method closure and
its analyzer/worker. Optimization symbol/expression identities use a shared atomic
counter whose value survives restoration, rather than process addresses.
Stateless invalid/never/null markers bind to the receiving process's canonical
singletons.

Validation so far:

- Full semantic test suite passes after the pipeline/observer changes.
- Restored semantic state matches fresh LLVM IR for body, signature, contract,
  effect-row, overload, struct-field/order and function-add/remove edit sequences.
- The graph codec passes race tests and a five-second malformed-body fuzz run
  (about 965,000 executions).
- The real readonly-reference compiler atom (153 retained compiler functions,
  11,762 source lines before standard-library expansion) analyzes successfully.
  With the server disabled and a 30-second process cap, its 42,311,961-byte state
  encoded in 345 ms, restored in 201 ms, and completed a restored no-op update
  in 52 ms. Initial analysis was 1.97 s. This is a focused slice, not the full
  self-host compiler.
- The v2 codec allocates all owners before resolving field/element alias paths
  and filling values. Slice capacity and hidden backing values are preserved;
  named byte slices and distinct zero-size pointer map keys are covered.
- Closed return-borrow journal frames clear their stale map-address slots.
  Snapshot admission rejects active branch/defer journals, solvers and shared
  code-generation state.

`TestDiskStateCompilerInput` is an opt-in real-source qualification using
`ELISACORE_STATE_QUALIFY_INPUT`. Always run it with an external 30-second timeout.
This evidence does not establish a sub-10-second fresh-process rebuild of the
full stage1 compiler. Default-on admission, larger-input qualification, snapshot load/write cost, and
full-compiler edit-to-object/executable measurements remain open.

## Standalone CLI path

`ELISACORE_ANALYSIS_CACHE=1` opts into disk-backed semantic state and suppresses
retained-server forwarding/discovery. Each invocation is an ordinary process;
there is no resident analysis worker. `ELISACORE_ANALYSIS_CACHE_DEBUG=1` logs
restoration, update, fallback and publication and bypasses the whole-object cache
so measurements exercise the frontend cache. It does not disable the native
partition cache.

Entries live under `<build-cache-root>/analysis/<identity>.state`; publication is
atomic. Identity covers the running compiler artifact, source root, working
directory, source authority, semantic options, relevant environment, target and
SMT executable contents. Current source bytes are intentionally not part of this
identity: they are checked against the stored revision and passed through the
incremental update. Corrupt, incompatible or unavailable entries analyze fresh.
A solver-unavailable result is not persisted across invocations.

Fresh-process CLI tests compare LLVM IR and diagnostics against cache-disabled
builds after body, struct-field, contract, permission-row, overload, signature and
function-add/remove edits and reversions. A weakened-contract negative control
must reject the same caller as the cold compiler, then accept it after reverting.
Native objects from separate processes are linked and executed for unchanged,
body-edited and reverted revisions. Tests also cover semantic/environment/source
identity changes, same-path solver replacement and corrupt-cache repair.

A real compiler atom object-build run (the same focused 153-function slice above)
measured 3.694 s cold, 1.083 s unchanged, 1.308 s after a function-body edit and
1.267 s after reverting. Every compiler invocation had a 30 s timeout and the
server was disabled. Snapshot restoration took 217–254 ms; the changed-body
analysis took 57–58 ms. This does not establish the target for the full compiler,
major edits or final executable linking. The mode remains opt-in while those
larger cases are qualified and optimized.
