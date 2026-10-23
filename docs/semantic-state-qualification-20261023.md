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

Pending protocol effect obligations are now explicit records (protocol/method,
expected and actual function types, source position), instead of closures. This
removes one process-local value that would prevent a future checkpoint before
post-analysis effect inference. A graph round-trip test verifies the obligation
still aliases the restored implementation's function type and rejects an extra
effect added by subsequent inference. Existing effect/error/frame subset tests
and disk-cache edit/IR comparisons pass; the full semantic suite also passes.
This does not yet implement resumable
initial analysis; whole-program bootstrap still exceeds the process cap.

## Initial-analysis checkpoints

`ELISACORE_ANALYSIS_CACHE=1 ELISACORE_ANALYSIS_BOOTSTRAP=1` opts into a
qualification path for bootstrapping inputs whose initial analysis cannot finish
inside the process cap. It runs the declaration phase once, then advances serial
body analysis between portable disk checkpoints. Repeating the identical build
in a fresh process restores the previous checkpoint. There is no resident worker.
`ELISACORE_ANALYSIS_BOOTSTRAP_SECONDS` controls the body time slice (default 3 s,
range 1–15); `ELISACORE_ANALYSIS_BOOTSTRAP_DECLS` can impose a declaration count
limit for testing. These scheduling controls do not change the semantic key.

An unfinished invocation deliberately fails with a checkpoint message and emits
no object/IR. `.building` files are keyed by the complete source bytes as well as
the normal compiler/options/environment/authority identity. A changed revision
cannot resume them. Completed analyses enter the ordinary `.state` cache and
subsequent supported edits use retained updates normally. Corrupt checkpoints
are rejected and reconstructed. Active journals and unavailable solvers reject
publication. Whole-program checks still run before any successful result.

Tests round-trip checkpoints between declarations and compare published metadata
against fresh analysis, preserve protocol/contract failures, compare LLVM IR
(including static-generated declarations), and edit the completed retained state.
Separate-process CLI tests require progress restoration, no premature artifacts,
normal completed-cache reuse, corrupt-cache repair and old-revision rejection.
Existing disk edit/IR and native execution tests also pass.

The real full stage1 input saved its first 666,967,732-byte checkpoint at 1/14,422
declarations in a 9.938 s invocation. A fresh process restored it in 2.701 s,
advanced to 3,282 declarations and saved 749,567,373 bytes; total 20.676 s. Later
attempts reached 3,302 and 3,339 declarations (959,683,922 bytes). The 3,302 attempt
hit its 30 s cap after publishing progress; the 3,339 attempt returned incomplete
in 25.623 s. Those trials used an 8 s body slice; the default was reduced to 3 s
to reserve more time for snapshots. A declaration remains indivisible, so the
external 30 s timeout is still mandatory. Full bootstrap, post-pass timing,
full-compiler output and sub-10-second edit builds remain unproven.

## Compact snapshot strings (state format v3)

The graph codec now writes each distinct nonempty string once and uses checked
backreferences thereafter. Empty strings have their own zero code. Decoding
copies literal bytes once, preserves arbitrary byte strings/named string types,
and counts literal storage plus dictionary entries against its allocation budget.
Invalid/forward string references, empty literals, truncation and budget exhaustion
are rejected. The `ELISTATE3` format explicitly rejects older wire versions.

The repeated-filename benchmark changed encode allocations from 4,194,681 B to
131,704 B and decode allocations from 2,247,956 B / 20,009 allocations to 328,283 B /
15 allocations. The v3 fixture is 50,268 bytes. Codec race tests, about 1.38 million
five-second malformed-body fuzz executions, semantic-state round trips, the CLI
bootstrap/corruption/revision tests and disk-cache edit/IR/native tests pass.

On the full stage1 input, the initial 1/14,422-declaration checkpoint fell from
666,967,732 bytes to 201,615,227 bytes. The v3 invocation returned incomplete in
8.529 s (4.385 s encoding/publication). A new process restored it in 2.021 s,
advanced to 404 declarations with the new default 3 s slice, and saved 203,285,143
bytes in an 11.689 s invocation. This qualifies full-input checkpoint size and
cross-process progress, not full bootstrap or sub-10-second completed builds.

A broader full-input bootstrap then found a closed-loop stack slot still holding
`storageViewLoopMutation.spare`, a callback needed only while checking that loop's
back edge. The stack pop now clears the closed slot while returning the complete
frame to the back-edge checker. Both finished and unfinished snapshot admission
explicitly reject open storage-view loop frames. A regression requires the live
returned callback to remain callable, all hidden closed slots to be zero, and a
finished state to round-trip afterward. The previous snapshot writer correctly
rejected the callback rather than dropping it. This cleanup also stops the idle
analyzer retaining the callback's captured state.
