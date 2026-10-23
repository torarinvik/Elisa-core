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

Unchanged revisions now skip snapshot encoding and publication after successful
restore. The regression test requires the saved bytes and modification time to
remain identical across processes. An isolated-cache rerun of the same atom
measured 2.906 s cold, then 0.741 s and 0.731 s unchanged (213–216 ms restore).
The complete analysis-disk CLI test group passed after this change.

The full stage1 compiler still reaches the 30 s process cap during body analysis,
before it can publish its initial snapshot. A bounded 25 s CPU profile attributed
about half its samples to allocation zeroing and virtual-memory page operations;
a 24 s allocation profile recorded about 12 GiB allocated. Lower-GOGC trials at
50 and 100 also reached the cap, so no GC default was changed. These observations
do not establish that any particular allocation optimization will meet the goal.

Direct-call refinement adapters now build one owned result from the declaration
header and canonical predicate channels. They no longer build, clone and discard
canonical parameter/requirement slices. A four-parameter contract benchmark
changed from 1,984 B / 23 allocations per call to 1,168 B / 12 allocations.
Regression cases cover direct and extern calls, empty declaration overrides, all
predicate channels and mutation isolation across calls. The full semantic suite
and disk-state edit/IR comparisons passed. This is an allocation improvement;
its full-input wall-time effect is not yet established.

Sink-parameter fixed-point analysis now stores its transfer sets in one backing
allocation and updates intersections/unions without allocating temporary sets on
every iteration. It preserves predecessor order, self-edge reads and the prior
initialization/convergence rules. Results match a frozen reference algorithm on
1,200 generated branching/looping graphs. The 128-block, eight-parameter benchmark
changed from 797 to 156 allocations and 19,344 to 17,168 bytes per solve; sampled
time changed from 10.7–11.6 us to 8.8–8.9 us. The full semantic suite passed.
