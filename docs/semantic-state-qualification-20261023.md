# Semantic-state disk persistence qualification

The semantic analyzer now exposes `RetainedAnalysis.MarshalState` and
`RestoreAnalysisState`. The CLI path remains opt-in while larger programs and edit kinds are qualified.
Its current publication policy keeps the admitted baseline immutable across
supported source updates and replays those edits in each fresh process. Fresh
analysis (including fallback and bootstrap completion) publishes a new baseline.
Historical publication timings below describe the earlier rewrite-on-edit policy.

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

The graph inventory pass now traverses only fields/elements capable of containing
object identities. Scalar values still undergo complete validation and encoding
in the write pass. A reference-walk test requires byte-identical output and keeps
pointers into scalar-only slice elements/struct fields aliased correctly; hidden
nonzero callbacks and uintptr values still reject publication. Codec race tests
pass. The repeated-string encode benchmark changed from about 0.96 ms to 0.82 ms.
An opt-in `TestAnalysisBuildCompilerCheckpoint` restores an existing real-source
`.building` graph without re-running bodies and never publishes into a CLI cache.
The complete 14,422-declaration body checkpoint restored in 6.105 s, re-encoded in
11.884 s, and restored again in 6.095 s; the bounded qualification passed in
24.661 s. Whole-program completion/publication remains separate and unproven.
The opt-in completion-boundary qualification also passed under the 30 s cap:
5.954 s restoring the full body checkpoint, 9.355 s final checks (zero errors,
26,230 notices), and 12.357 s encoding the finished 619,157,503-byte state; total
27.860 s. This test omits CLI source loading/parsing, publication and native
codegen, so it is not evidence of a completed full CLI build within 30 s.

Resumed CLI bootstrap invocations now reuse the checkpoint's pristine AST and
skip the second source parse. A fresh-process regression checks two parser passes
for the initial attempt and exactly one for every resume, while retaining the
existing IR/corruption/revision checks. Ordinary completed-state edit tests pass.
This removes unused token/AST allocation and parse time from the tight completion
publication path; it does not remove the initial current-source parse.

## Resumable final checks

Initial-analysis checkpoints now record progress through the same ordered final
check phases used by ordinary analysis and retained updates. Time/count slices
can stop after each whole phase; no Result is exposed until all phases and final
result construction complete. Body diagnostic windows stay fixed while final
checks resume, so later retained updates can remove/replay post-body diagnostics
correctly. Debug and incomplete-build messages report both declaration and
final-check progress. The declaration count limit also bounds final-check units.
Tests force a round trip after each phase and compare metadata, IR, negative
contracts, protocol violations and retained diagnostic replay with fresh analysis.

This testing found that protocol effect obligations were consumed after initial
analysis: an unchanged retained update could silently lose its capability-subset
violation. Obligations now remain available for subsequent row solves, and their
positions follow the live method declaration when source lines move. A separate-
process CLI regression requires the violation to persist through an exact repeat,
a real unrelated body edit and a line shift, matching a cache-disabled compiler.
The body-edited and line-shifted cases must use retained updates without fallback.
The full semantic suite and disk-cache bootstrap/edit/native/IR test group pass
after this change. Full-input phase/publication timing is the next qualification.

Full-input qualification of resumable final checks has now published a finished
semantic cache with the server disabled. At 14,422/14,422 declarations, the late
return-borrow phase ran alone in 6.410 s and saved its checkpoint in a 27.576 s
invocation. The next process restored it, ran the remaining ten phases in 2.653 s,
and published the complete 619,453,329-byte semantic state in 13.039 s before
reaching the 30 s native-generation cap. Further processes logged exact-source
restoration/reuse but also reached the cap before emitting an object. Full native
rebuilds and the sub-10-second goal remain unproven.

`TestDiskStateNativeCompilerProfile` is an opt-in native-stage profiler using a
finished snapshot, exact current expanded source, and the CLI's source-authority
and semantic-option checks. Its default object mode is O3, matching the real CLI;
`ELISACORE_STATE_QUALIFY_NATIVE_OPT` is checked against the snapshot options. An
explicit O0 request against the O3 snapshot is rejected. The matching O3 process
still hit 30 s. An 18.19 s bounded CPU profile sampled 82.57% in cgocall, and the
captured stack was in `elisacoreRunOptimizationPipeline` through
`llvmGenerator.optimizeModule(..., 3)`, before native partitions could be saved.
An O0 backend run on an O3 semantic snapshot is not a matching CLI comparison.

The opt-in `ELISACORE_LLVM_PASS_DEBUG=1` diagnostic now enables LLVM's own
PassBuilder progress logging for the ordinary whole-module pipeline. It leaves
optimization level, alias analysis, inliner/unroll thresholds and enabled passes
unchanged. Each pass is named before it runs, allowing a 30-second bounded process
to retain progress even when optimization never returns. A full-input O3 run
restored state in 6.308 s and reached the cap with over 627,000 pass/analysis log
lines; its last recorded work was GVN/MemoryDependenceAnalysis in
`Parser.append_machine_transition_for_values`. Logging perturbs timing, so these
results identify work only and do not replace the uninstrumented CPU profile.
Native optimization-change/revert and language/optimization parity tests, plus
AA/inliner/unroll consistency checks, pass with logging enabled. Run the backend
test binary from `compiler/src/backend` because source-consistency tests read C
sources relative to that directory. This is diagnostic progress, not a measured
rebuild speedup; default O3 and the sub-10-second target remain unresolved.

An additional disk work product now retains the **whole optimized LLVM module**,
keyed by exact pre-optimization bitcode, resolved target triple/CPU/features,
optimization level, AA pipeline and inliner/unroll settings within the existing
executable-content namespace. It preserves the ordinary O3 pipeline and its
whole-module inlining. Restores use a new LLVM context, verify the parsed module,
restore the original module identifier and rerun the autovec warning verifier
against current warning enforcement. The existing checked, atomic, size-bounded
disk format and shared eviction budget cover these entries. Cache-disabled and
O0 paths retain their previous behavior; pass-debug mode bypasses optimized-module
reuse so it can observe actual pass execution.

Separate-process tests compare exact optimized IR and linked native results for
repeats, source-line shifts, body edits, reverts, O2/O3 changes, compiler identity,
resolved CPU changes and debug-info line changes. Debug line shifts correctly miss
while line shifts without position metadata can reuse identical lowering. A
scalar fallback fixture confirms restored metadata regenerates warnings and
honors a later change to hard-error enforcement.

A bounded standalone CLI exercise of 512 reachable loop functions (server unset,
analysis-cache debug bypassing the whole-object source cache) measured whole O3
optimization/cache publication at 55 ms, exact lowered-input restoration at 3 ms,
a changed-body miss at 51 ms and reverted-input restoration at 3 ms. Total CLI
times were 0.258/0.217/0.260/0.198 s respectively. This fixture verifies the reuse
path; it is not the full self-host compiler and does not prove the overall target.
The full compiler still lacks its first completed native O3 work product under
the 30-second invocation cap, and new lowered code still requires optimization.

Opt-in `ELISACORE_PARALLEL_OPT=1` now connects incremental object emission to
isolated optimization workers **before** whole-module optimization. Each stable
partition's exact unoptimized bitcode and resolved native/pipeline settings key
its finished object. A hit skips both O3 optimization and native emission. The
workers receive already restricted, namespaced, pruned and verified bitcode; a
one-part worker retains precisely those definitions. A declined split still runs
the ordinary O-level pipeline before serial emission. This initial path accepts
only import weight zero; a positive `ELISACORE_PARALLEL_OPT_IMPORT` keeps the
whole-module fallback rather than silently discarding the import request.
The default remains whole-module O3: per-partition optimization loses foreign
inlining, and runtime-quality qualification/import support remain outstanding.

`TestPreoptimizedPartitionsPersistAcrossProcesses` starts separate test processes
and actual optimization worker processes. It requires four cold O3 misses, no
misses on exact repeats, precisely one newly optimized partition after a body
edit, reuse after source-line shifts/reverts, and correct O2/O3 invalidation.
Each revision links and executes both partitioned and serial cold objects.
Test executables dispatch native workers only under the explicit
`ELISACORE_TEST_NATIVE_WORKERS=1` qualification flag; production main already
installs the same worker entry point.

The full 14,422-declaration compiler input has now produced its first object
within the 30-second process cap using this opt-in mode. The matched saved-state
qualification restored in 6.461 s, generated IR in 3.369 s, optimized/emitted ten
partitions and merged a 15,366,528-byte object. Native work took 20.154 s; the
external process took 28.550 s. This is a qualification load of the previously
verified source/options snapshot, not a manually admitted production cache entry.
A new process reused all ten partitions, emitted none, restored in 6.258 s and
spent 4.962 s in native work, taking **12.620 s overall**. Both invocations finished
normally, with no surviving worker processes. The full compiler object linked
successfully with the repository's ordinary seed-link flags and profiler weak
fallbacks. That compiler compiled a fresh `return 42` program; its linked native
executable returned 42. Linking and every compiler invocation used a 30-second
external timeout. The product's unsupported `--help` flag returned its ordinary
`unknown flag` diagnostic; qualification proceeded with an actual compilation.

The sub-10-second objective remains unmet, including this no-edit full-input case.
Full-source edited builds, production cache admission, broader rebuilt-compiler
runtime tests and optimization-quality checks are still required. Restoring the
semantic graph plus parsing already accounts for over seven seconds; even exact
native partition reuse still spends about five seconds lowering/serializing and
preparing those partitions.

Semantic restore profiling now has a source/options-validated restore-only mode
(`ELISACORE_STATE_QUALIFY_RESTORE_ONLY=1` in the native qualification test), so
`-test.cpuprofile` captures restoration without overlapping the native profiler.
The complete snapshot contains 8,977,314 object-table entries. Its baseline
restore took 6.506 s; 46.72% of sampled CPU was in Darwin runtime `madvise` calls
under heap-span allocation. Disk checksum time was only about 0.17 s.

Decoder object allocation now retains the pointer/map/slice values directly,
without allocating an extra writable header cell per owned object. Named pointer
types are converted back to their registered type. Slice owners are constructed
with their final length/capacity in one operation; hidden capacity remains filled
by the existing second pass. Graph identity, alias resolution and the wire format
are unchanged. Named pointer aliases, named self-referencing maps/slices and
mutation independence from the original graph have explicit regression coverage.

A 10,000-pointer decode benchmark improved from 1.304–1.327 ms and 20,014
allocations (1,059,550 bytes) to 1.156–1.166 ms and 10,013 allocations (979,530
bytes). Baseline benchmarks used an isolated copy of the previous committed
codec, with the same benchmark, rather than changing the working compiler source.
The full saved state restored in 5.884 s in the matched profiled run, versus
6.506 s before removing the extra headers. Codec tests, race tests, named-reference
regressions and the semantic suite pass. A five-second unguided malformed-input
mutation smoke run completed 2.83 million inputs without failure. An initial
semantic-suite launch from the compiler root failed on relative fixture paths;
it was stopped and rerun successfully from `compiler/src/semantic`.
This is a restore improvement, not proof of sub-10-second edited CLI builds.

The decoder's temporary object descriptions now store their validated schema type
ID instead of a reflected-type interface. On this 64-bit host the entry shrinks
from 40 to 32 bytes, saving about 72 MB for the 8,977,314-entry full snapshot.
Alias resolution obtains the same registered type after the original type-ID
validation; no disk bytes or allocation-budget checks change. The pointer fixture
uses about 905,774 bytes per decode instead of 979,530. Codec and race tests pass,
and the complete snapshot restores and passes exact-source/options admission.
Its measured restore was 5.935 s, within noise of the preceding 5.884 s, so this
change is a memory reduction rather than a claimed latency improvement.

A normal installed-CLI cache baseline (not the snapshot qualification loader) now
exists at the owned directory recorded by `/tmp/elisa-current-disk-cli-root`.
It bootstrapped the unchanged full compiler through nine bounded processes and
published a 622,880,225-byte semantic snapshot under the actual executable's
normal identity. The final bootstrap process reached the 30-second cap after
publication. The next process completed the object in 26.745 s; another process
reused all native work and completed in **11.718 s**. Server forwarding was
disabled; analysis-cache debug mode bypassed the whole-object source cache. A
byte-identical copy of that compiler is kept in the owned output directory so
later compiler-development commits cannot alter the measured cache namespace.
No cache entries were rekeyed. `baseline.state` is an unchanged backup for explicit
source/options-validated component qualification, not an admission override.

A controlled full-source body edit changed only `"unknown flag: "` to
`"unknown option: "` in `src/driver/elisac_cli_capture.elisa`. Through the frozen
compiler and normal cache admission, restore took 5.710 s, retained update
reported one changed body in 1.899 s, and serializing/publishing the new state
took **15.213 s** (690,967,733 bytes). The process hit the 30-second cap before
finishing native generation. The original source bytes were restored in a
`finally` block; the seven pre-existing Claude changes remain untouched. The
updated semantic cache remains a real edited revision; subsequent original-source
builds must update/revert it normally rather than hand-restoring the backup.
This identifies snapshot publication as a primary edited-build bottleneck.

An offline inventory of the earlier full snapshot found 4,949,904 pointer,
3,288,515 slice and 738,895 map entries. Pointer owners now use bounded typed
allocation batches in large graphs, with at most 64 KiB per backing array.
Zero-size/canonical pointers keep their individual rules, rare/large pointees
keep ordinary allocations, and every logical pointer identity remains distinct.
All storage bytes are still budgeted before allocation; type-indexed batch
headers fit inside the existing per-object metadata reservation. A live element
keeps its typed block alive. This allocation detail is not written to disk.

The pointer benchmark drops from 10,012 allocations to 21 per decode (about
912,398 bytes versus 905,774); its 1.187–1.195 ms time does not establish a
microbenchmark speedup. Full-state restore measured 5.603 s. Large-graph tests
cover 10,000 distinct nodes, named pointers, cycles, interior pointers/slices,
mutation independence and re-encoding; codec/race and semantic snapshot/checkpoint
tests pass. Native full-input qualification completed in 26.367 s, then 11.342 s
with disk reuse. Both emitted a byte-identical object to the installed-CLI
baseline, SHA256 `c7e187742d8e37175ef1687804cc2f2a585af98812800d6118451cb57a110af9`.
These component qualification times do not replace the normal edited-CLI result
above, and the sub-10-second goal remains unmet.

The retained pre-body state has four preparation tables (`keyOf`, `prepared`,
`targets`, `regionIndex`) that root prepared ASTs. Code inspection shows old
pre-body comparisons read only fingerprints/global data/diagnostics, while
preparation tables are read from the new scratch state during an update. Their
idle lifetime should be audited next against the observed post-edit snapshot
growth; no preparation-table elision has been implemented or claimed here.

Idle retained state now keeps only pre-body fingerprints, global results and
phase diagnostics. Its old `keyOf`, `prepared`, `targets` and `regionIndex` tables
are consumed only from the new scratch phase while an update runs. Successful
updates detach a comparison-only baseline; snapshot publication and restoration
also compact older baselines. This replaces the retained baseline pointer without
mutating a caller-owned `RetainedScratch`. New regression coverage loads a legacy
full baseline, applies prepared body edits, adds/removes a function, round-trips
each result and compares diagnostics with cold analysis. Full semantic and CLI
cache/bootstrap regression groups pass.

Using the normally admitted edited 690,967,733-byte snapshot from the preceding
CLI experiment, a source/options-validated save-only qualification now writes
620,521,009 bytes: about 70 MB of unused preparation data is gone. It took 13.780 s
to encode in that profiled run. This is encoding-only timing, so it is not directly
comparable to the previous 15.213 s CLI encode-plus-publication measurement. The
controlled source edit was restored afterwards. `ELISACORE_STATE_QUALIFY_SAVE_ONLY`
selects this phase profiler without publishing into a production cache namespace.

The resulting CPU profile attributes 82.5% cumulative CPU to the codec and 66.4%
to recursive `encoder.value`. Flat costs include `memmove` (24.7%), heap clearing
(10.4%) and reflection/field traversal. The existing writer already uses
`bytes.Buffer`; treating it as repeated small-growth `[]byte` appends would be an
incorrect diagnosis. Recording live struct fields once, reducing reflected map
copies and profiling inventory storage are possible next steps. End-to-end
edited CLI builds and the sub-10-second objective remain unproven.

Allocation-range inventory now counts eligible owners during graph traversal and
builds the range table at its final capacity afterwards. This avoids repeatedly
copying a growing multi-million-entry slice. Object discovery order, object IDs,
range values, sorting rules and wire format stay unchanged. Codec, interior-alias,
large-graph, race and CLI cache/bootstrap tests pass. Matched full edited-state
save qualifications measured 12.719 s before and 11.798 s after this change;
these remain encoding-only measurements, not edited-build completion claims.

The save qualifier can additionally restore its new output under
`ELISACORE_STATE_QUALIFY_SAVE_RESTORE=1`. A complete edited-state encode/decode
round trip finished within the external 30-second cap (25.972 s), retaining exact
expanded source, invocation options and all 26,230 diagnostics in order. The
options check retains the include loader's validated augmented options, including
trusted-runtime provenance; an initial test harness comparison against raw CLI
options omitted that provenance and was corrected without relaxing the guard.
Original source bytes were restored after every controlled edit. Normal full
edited CLI builds remain above the requested ten-second target.

Encoder node inventory now uses fixed logical blocks of 2,048 records instead of
growing one multi-million-entry slice. The first block starts small; subsequent
blocks allocate once. Consecutive discovery IDs and alias lookup/sorting rules
are unchanged, and no block metadata enters the wire format. Existing large-graph
round trips cross multiple block boundaries and preserve cyclic/interior aliases.
Codec/race and CLI cache/bootstrap tests pass. A matched full-state pair measured
12.780 s before and 11.739 s after; a separate checked round trip encoded in
12.563 s, so the latency benefit is not a stable sub-ten-second result. That full
round trip finished in 25.967 s and preserved exact source/options plus all 26,230
diagnostics. The mechanism eliminates repeated full inventory copies; more work
is needed on edited-build publication. Controlled source bytes were restored.

The CLI now avoids whole-graph publication after a supported retained update.
It preserves the admitted baseline on disk, checks/replays the current source on
restoration and generates from the updated result. Unsupported updates still
fall back to fresh analysis, which publishes a new baseline. No process or
background worker is retained. This exchanges a repeated cheap update for the
12–15-second full snapshot rewrite; an exact repeat of an edited revision must
therefore replay again when the whole-object source cache is bypassed.

Separate-process regressions prove baseline bytes and mtime remain unchanged
through body edits, repeats, cumulative edits, function addition/removal and a
revert. Current IR and diagnostics match cache-disabled cold compilation. The
existing native execution, contract/protocol rejection, corruption repair,
bootstrap and cache-key regression groups pass with this policy.

Source/options-checked full-input replay/native qualification seeded ten native
partitions in 25.473 s. Replaying the controlled diagnostic-string edit then took
19.839 s: restore 5.538 s, checked update 1.981 s, and native work 10.277 s,
optimizing one partition and reusing nine. Repeating the edited source took
13.865 s (ten native hits, but replay again); reverting took 11.037 s with ten
native hits and no source update. Every process had a 30-second external cap.
These are explicit baseline qualification loads, not manually rekeyed production
semantic cache entries. Production policy is covered by the separate CLI tests.

An additional edited-source qualification emitted/linked an executable compiler.
Its `--baseline-replay-probe` request returned exit 1 and exactly
`unknown option: --baseline-replay-probe`, proving the changed string reached
native output. Controlled source bytes were restored after all probes. The
full-source edited-build goal remains unmet: even this first edited qualification
is about twenty seconds, and bypassed-source-cache repeats are about fourteen.
Next work must address native granularity, frontend restore/lowering and repeated
baseline replay rather than treating omission of publication as completion.


### Independently selectable native unit count

`ELISACORE_OBJECT_PARTITIONS` selects 1–64 stable native units independently of
worker concurrency. Invalid/unset values retain the CPU-count default. This
keeps the existing quality/performance default while allowing measured tuning
of the opt-in pre-optimization partition path.

Source/options-checked full-input qualification with 64 units: initial native
population 23.595 s; controlled one-string edit 14.713 s (one unit emitted,
63 disk hits); repeated edit 14.199 s (64 hits); revert 11.413 s (64 hits).
The edit's native phase fell from 10.277 s with ten units to 5.141 s with 64;
the changed unit itself took 1.169 s. Restore still costs about 5.55 s and
checked replay about 1.97 s. More units increase partition overhead slightly.
All four processes completed under their external 30-second caps. These remain
explicit qualification loads, not production cache rekeying. The default
whole-module O3 policy and experimental partition-path quality caveat remain.


### Project unused pristine function bodies out of snapshots

Snapshots now preserve pristine declaration headers, positions and cached gensym
comparisons while omitting their unused body ASTs. Analyzed/live bodies remain
complete, including declarations shared with a comparison root. The projection
is invocation-local: serialization leaves the caller-owned pristine parse intact
and uses the unchanged checked graph schema. Restored updates compare headers
and source spans through the existing comparison-text cache.

Full-input source/options-checked encode/restore preserved all 26,230 diagnostics.
A fresh-process restore of the projected raw qualification payload took 4.785 s,
versus 5.55–5.72 s for the old full graph. The payload shrank from 622,880,148
bytes to 563,592,982 bytes (map iteration changes exact encoded size slightly).
Full roundtrip completed in 23.93 s under the 30-second external cap. Raw
qualification payloads are never production cache admission or executable
namespace rekeying. New tests cover untouched input bodies, live-body protection,
repeated encode/restore across edits, add/remove/shift/revert and header/default
changes. Retained analysis and separate-process disk-cache regression groups
(including native execution, contract/protocol diagnostics and bootstrap) pass.


### Allocate the snapshot file read once

Production semantic-state reads now allocate their stat-validated final file size
and use `io.ReadFull`, rather than grow an `io.ReadAll` buffer. Truncation or an
extra trailing byte fails closed; identity and codec checks remain. A 16 MiB
artifact benchmark improved from 2.184 ms / 36,233,461 B / 37 allocations to
1.433 ms / 16,787,696 B / 7 allocations (three iterations each). This is an
allocation/read microbenchmark, not an edited-build latency claim. Invalid-envelope
and the full separate-process analysis disk regression group pass.


### Production CLI qualification after partition/snapshot/transcript gains

A fresh owned cache was populated normally using a frozen installed compiler
from `2e3b21c6` (before the later file-read allocation change), without copying
or rekeying any semantic snapshot. Environment: analysis cache and resumable
bootstrap on (six-second body slices), 64 native units, experimental parallel
O3 optimization on, retained server empty, timing/debug observers absent.
Every invocation used an external `timeout 30s`. The nine bootstrap invocations
made resumable progress; invocation nine published the completed baseline and
hit the cap during native work. Invocation ten completed in 22.795 s.

| Normal CLI request | Seconds | Result |
| --- | ---: | --- |
| Unchanged full compiler | 0.528 | Whole-object disk hit |
| First novel diagnostic-string body edit | 15.382 | Successful fresh-process replay/native build |
| Repeat edited revision | 0.536 | Identical object; exact diagnostic transcript |
| Add ordinary unused function on edited revision | 17.756 | Successful fresh-process build |
| Repeat addition | 0.526 | Identical object |
| Remove addition, returning to compiled edited revision | 0.418 | Prior object restored |
| Revert to compiled original revision | 0.417 | Original object restored |

Each successful build emitted 9,381,562 diagnostic bytes. Repeated object hashes
matched the corresponding original build. The edited object linked with LLVM
and the repository's host/profiler fallback hooks; executing the resulting
compiler with `--production-disk-probe` returned exit 1 and exactly
`unknown option: --production-disk-probe`. All controlled source bytes were
restored; the seven original user/Claude modifications remain untouched.

An earlier addition probe used a reserved `__` function name and intentionally
fell back to cold analysis. The retained path explicitly excludes synthesized
names; this guard was preserved and the ordinary-name probe above corrected
the experiment. The resulting partial bootstrap remains isolated to that probe.

The goal is still incomplete: cached revisions are comfortably below ten
seconds, but new body edits take 15.4 s and new function additions 17.8 s.
Next work must reduce frontend hydration/replay and whole-module LLVM lowering,
then requalify novel signatures, structs and transitive edits. More repeated
cache-hit measurements do not establish that new edits meet the target.

Owned evidence root: `/tmp/elisa-server-free-cli-root` points to the run folder
containing the frozen executable, exact environment, checkpoint logs, timing
JSON, native objects and the linked probe compiler. The existing qualification
root still supplies the explicit schema-compatible projected payload for phase
profiling; it is separate from production admission.


### Reuse map-entry decode cells

The graph decoder now keeps one zeroed key/value scratch pair per map type.
`SetMapIndex` copies entries into owned map storage; scratch cells are not graph
objects, and nested map references do not recursively fill map bodies. Clearing
both cells before every entry preserves absent fields, nils and shared pointers.
Scratch storage is charged to the decode allocation budget.

A 10,000-entry map benchmark improved from 2.176 ms / 3,275,632 B / 40,046
allocations to 1.265 ms / 2,316,419 B / 20,050 allocations. Paired fresh-process
restores of the normally admitted full compiler baseline took 5.054/4.954 s
before and 4.685/4.745 s after (the latter after run included CPU profiling).
These are source/options-checked qualification reads, not production rekeying.
Codec, race, sparse-entry/shared-pointer regressions and all separate-process
analysis disk tests pass. Native CLI edit timings still require requalification.


### Restore interface-held references directly

Interface-held pointer/map/slice values now use the decoder's already checked,
owned object table directly. The previous path allocated a mutable intermediate
header per occurrence, decoded its object ID, then copied it into the interface.
Dynamic type assignability and exact reference-type checks remain; zero references
still produce typed nil interfaces. Concrete scalar/struct interface values keep
their ordinary owned value cells.

A 10,000-reference benchmark improved from 0.859 ms / 244,369 B / 10,012
allocations to 0.279 ms / 164,369 B / 12 allocations. A paired checked full-input
restore improved from 4.773 s to 4.279 s. Codec and race tests plus all separate-
process analysis disk regressions pass, including native edits, diagnostic
rejection and bootstrap. Typed nils, shared pointer/map/slice identities and
cycles have explicit regression coverage.


### Batch small slice backing storage

Large graph restores now allocate small slice backing storage in typed blocks
of at most 1 MiB, then use three-index slices to preserve each original length
and capacity. Named slices retain their type. Empty, zero-sized and larger
slices keep the ordinary path. Every request's storage and any unused block
tail are charged to the decode budget; graph object IDs and interior aliases
are unchanged. Pointer and slice batches share the existing checked descriptor
pass; aliases resolve only after all owners have storage.

The 10,000-small-slice benchmark reduced allocations from 30,011 to 20,014
(time 1.294 to 1.240 ms, bytes 1,630,324 to 1,638,905). Full-input restore was
4.383 s before versus 4.255/4.312 s after: a modest latency gain. Codec, race,
20,000-variable-capacity slice/GC/append/cycle/interior-alias tests and the
separate-process disk analysis suite pass. A checked full compiler native build
completed in 22.809 s under its 30-second cap; its object SHA-256 was exactly
the prior production baseline's
`04537dc89a3ee5c3742c08dd156dd1c414f3d76158ab1b4c0854cd41d41c405e`.


### Optional typed slice-header pools and reusable fill views

Common semantic slice types now use compiler-registered, ordinary generic Go
allocators for typed header/data arrays. Registration cannot add schema types;
unlisted types retain the general allocator. Each original length/capacity and
object ID is preserved. A separate per-type scratch header supplies full backing
views during filling, so self-references still observe the original slice length.
No unsafe pointer/header construction is used. The wire schema is unchanged.

The small-slice benchmark fell from 20,014 allocations / 1,638,905 B to 18
allocations / 1,405,624 B; time remained about 1.24 ms. Paired checked full-input
restore times (4.363 s before, 4.262/4.311 s after) do not establish a substantial
latency gain. A further memory comparison measured 4,023,304,192 versus
3,943,301,120 bytes peak RSS; restore stayed 4.272/4.268 s. This is an allocation
and memory gain, not a claim that novel builds meet ten seconds. Codec/race and
50,000-variable-capacity slice GC/alias/cycle/chunk-boundary checks pass, as do
all separate-process disk-analysis regressions. Full native qualification took
22.335 s and produced the exact prior production baseline object SHA-256.

An independent 1 MiB pointer-batch experiment restored in 4.225/4.217 s versus
4.221 s with 64 KiB batches, showing no useful gain; that experiment was reverted.


### Generate optimized native units independently (explicit experiment)

`ELISACORE_PARALLEL_IR=1` together with `ELISACORE_PARALLEL_OPT=1` now generates
optimized units in separate LLVM contexts rather than first lowering one whole
module and serializing it for restriction. Each miss still runs the requested
O-level pipeline in an isolated worker. Explicit cross-unit imports retain the
module-split path. Default whole-module O3 behavior is unchanged. The existing
cross-unit inlining/quality limitation remains a gate before adopting this mode
as the default. Missing worker entry points cannot select optimized unit generation.

Non-owner ordinary functions are declared on demand by call/value lowering,
which avoids creating and subsequently pruning every unused prototype in every
unit. Intrinsics retain the existing final-LLVM-name ownership check. The unit
cache configuration includes the optimization pipeline, so O2/O3 outputs cannot
cross-hit. The existing O0 generator behavior is preserved.

Source/options-checked full-input qualification with 64 units: cold native
population 23.348 s; unchanged baseline 7.725 s; novel string body edit 11.445 s
(one unit emitted, 63 disk hits); edited repeat with whole-object caching bypassed
10.357 s; revert 7.618 s. All processes used external 30-second caps. The edit's
native phase was 3.301 s (changed-unit optimization/emission 1.534 s); unchanged
native work was 2.175 s. Hydration remained about 4.24 s, while source replay
still performs two serial parses and declaration preparation. This remains
explicit qualification, not production namespace rekeying or final goal success.

An earlier version generated all prototypes in each unit and took 3.014 s of
native work on a cached baseline; lazy declarations reduced that to 2.175 s.
The native work products are larger: about 509 MB of retained bitcode/objects,
versus 110 MB for the whole-module restriction path. The initial experiment's
first population hit its 30-second cap with 56/64 units published, then resumed;
the final lazy version's cold population completed within one capped invocation.

Separate-process native tests and race tests pass across body/line/revert edits
and O2/O3 changes, with expected one-unit invalidation and no serial fallback.
Admission guards and existing constant-address checks pass. The edited full
compiler linked and returned the changed `unknown option` diagnostic; it then
compiled an object for a fresh program, whose linked executable returned 42.
All controlled source edits were restored.


### Overlap scratch preparation with disk hydration

The disk-cache path starts a separate scratch parse/declaration preparation
while restoring the graph, then uses `UpdatePrepared`. Hydration itself only
allocates/reconnects checked data. Preparation is joined even when restoration
fails, before any fallback analysis or return, so no worker or state persists
beyond the invocation. Current invocation options/provenance still govern the
scratch phase and restored-state admission.

Source/options-checked full-input qualification now took 9.470 s for the novel
string-body edit (one emitted unit / 63 disk hits), versus 11.445 s before.
Scratch preparation took 2.804 s inside the 4.544 s hydration interval; the
remaining prepared update took 0.325 s. Native work took 3.350 s. The edited
object SHA-256 exactly matched the serial-preparation version's
`3ad2e9a96c9cdb052500c6b5c1934ac442071def08987b41d2b8a6883f5b6fd3`.
Edited repeat with whole-object cache bypassed took 8.306 s; revert 7.984 s.
Cold native population took 23.527 s. Each process had its external 30-second cap.
This is explicit full-input phase qualification; production admission and a wider
edit matrix still need requalification before the complete goal can be claimed.

All separate-process disk cache regressions pass. A new regression verifies
joined preparation on both successful and corrupt restore, then checks prepared
body reuse against cold diagnostics. Race checks for that path, corruption
repair and protocol-effect rejection pass. Speculative scratch work increases
uncached exact-revision qualification slightly (7.725 to 8.315 s); normal
whole-object hits still avoid frontend work entirely.


### Queue likely changed native units first

The retained result exposes recently re-analyzed function names as scheduling
hints only. Unit generation queues their owners first, so optimization misses
can overlap generation of cache-hit units. Every unit is still generated and
checked using its full current bitcode; hints never authorize reuse and are not
claimed to be an exhaustive invalidation set. Returned hints are copied and
sorted. Cold analyses retain ordinary unit order.

Full-input qualification: novel body edit 9.124 s (native 2.961 s), exact same
edited object SHA-256 as before prioritization. Adding an ordinary function on
that edited revision took 10.830 s (two native misses, 62 disk hits); its prepared
semantic update took 2.341 s and native work 2.564 s. Repeating the addition while
bypassing whole-object caching still took 10.703 s because replay repeats. The
remaining body-only revision took 8.487 s and revert 8.051 s. Cold population
completed in 22.793 s. All processes had their external 30-second caps.

The changed-unit-order regression verifies copied hints, correct first owner,
and a complete unique work list; native multi-process admission/edit/optimization
level checks and all disk-analysis regressions pass. The function-addition case
still misses the goal, so prepared semantic replay is the next profiling target.


### Preserve the permission graph for verified append-only function growth

Replay profiling isolated the addition regression: prepared update 2.376 s,
of which post checks took 2.228 s. Permission inference took 1.307 s and
validation 0.665 s; name/declaration diffing was only about 0.13 s. No new
name-index cache was added because the profile did not justify one.

The retained updater now keeps the old permission declaration sequence, types
and known callable types before registration. It permits incremental solving
only when the old sequence/scopes/types remain an exact prefix, old rows are
known, and every tail function is edited with a genuinely fresh, unshared type
that no old equation read. The verified function/type map is passed through
invocation-local post callbacks and rechecked after body analysis. No persistent
field or schema layout was added; old checked graph payloads still restore.

New functions and rebound callers always validate as edited bodies. Fresh rows
alone do not invalidate unrelated old validation; any pre-existing row change
still revalidates all old functions. Replacements, removals, index shifts,
reused callable types and failed guards retain the full solve. A later edit of
a previously added function treats its row as pre-existing normally.

Full-input qualification: novel body edit 9.154 s; function addition on the
edited revision 8.587 s (two misses / 62 native disk hits), versus 10.830 s
before. Prepared addition replay fell to 0.377 s and post checks to 0.234 s.
The final stricter guards reproduced 0.385 s replay, with permission inference
2 ms and validation 5 ms. Addition object SHA-256 exactly matched the full-solve
version: `7f1072993dbfa1be66f19bb4211346a984ca2b6b8dec171423c445ad8dbb8379`.
All processes used external 30-second caps. These remain explicit qualification
loads; final production admission and broader major edits remain to be tested.

The complete semantic package suite passes from its package directory, along
with all separate-process disk cache regressions and targeted race checks.
New cases cover unused additions, new unsafe effects, repair of an undefined
callee, changed existing rows, index shifts, callable-type reuse, retargeted
verified types and removal fallback. Snapshot roundtrip tests preserve the
invocation-local callback reconstruction. Replay-only CPU profiling and detailed
update stats are available in the opt-in qualification harness.


The final append guard also captures known callable types (including externs
and aliases), requires unchanged namespaces/usings and old type identities,
and passes the exact verified declaration/type map into post processing. Type
retargeting during body analysis rejects incremental solving. The old callback
field remains excluded from serialization; its signature change adds no graph
layout or schema change. The complete semantic suite and disk-cache matrix
pass with these stricter guards. A checked replay of the actual full compiler
addition measured 0.385 s; phase logs show effect solving 2 ms and permission
validation 5 ms. Native before/after addition objects matched byte for byte.


### Normal production CLI matrix from the Stage1 project directory

A fresh cache was populated normally with the installed compiler containing
all gains through `68eef070`. The frozen executable SHA-256 was
`7e7e1abb25757ab1b901a1f0264b146b97f573fcea318379817c76a875cfc553`,
byte-identical to the installed executable. Working directory was the Stage1
project root, not the Core compiler directory. No semantic/native namespace was
rekeyed. Environment selected disk analysis/bootstrap, six-second bootstrap
slices, 64 units and explicit parallel IR/O3 optimization; retained server was
empty, with timing/debug observers absent from the normal matrix.

Nine bootstrap invocations preserved progress; the ninth hit its 30-second cap
after publishing the complete semantic baseline. The tenth normal invocation
completed in 19.785 s. Every compiler and linker command used `timeout 30s`.

| New normal CLI request | Seconds | Result |
| --- | ---: | --- |
| Cached original revision | 0.529 | Whole-object hit |
| Novel diagnostic-string body edit | 10.448 | New build; succeeds |
| Repeat body edit | 0.535 | Exact same object |
| Add ordinary function to edited source | 9.898 | New build; succeeds |
| Repeat addition | 0.429 | Exact same object |
| Add unused plain struct as well | 10.484 | New build; same final object as function addition |
| Repeat struct revision | 0.424 | Whole-object hit |
| Add defaulted parameter to existing capture function | 13.203 | New signature/callers; succeeds |
| Repeat signature revision | 0.536 | Exact same object |
| Add leading comment/line | 11.165 | Same final object as original |
| Revert original | 0.539 | Original object restored |
| Different same-length leading comment (novel source key) | 9.387 | Same original object |

Each successful request emitted 9,381,562 diagnostic bytes. Body and signature
objects linked into runnable compilers; both returned the edited `unknown option`
diagnostic. The signature-edited compiler compiled a fresh program, whose linked
executable returned 42. All temporary source edits were restored; only the seven
pre-existing user/Claude Stage1 modifications remain.

Observers that bypass whole-object caching while preserving the admitted
semantic namespace further isolated warm native paths: body replay 8.488 s
(restore/preparation 4.441 s, prepared update 0.327 s, zero native misses),
signature replay 11.128 s (4.508 s restore/preparation, 2.461 s update, zero
native misses), and line-only replay 8.597 s (4.492 s restore/preparation,
0.294 s update, zero native misses). The signature change still takes the full
permission path; ordinary native-unit regeneration also remains significant.
A full CLI signature CPU profile is preserved alongside these logs.

The full goal is still unmet: ordinary novel body/struct/line changes can exceed
ten seconds, and signatures remain about thirteen. Phase qualification alone
was not sufficient proof. Next work must address signature/removal dependency
remapping and remaining current-IR production cost, then expand the major-edit
matrix (layout, contracts, transitive dependencies and generic cases). The
experimental no-cross-unit-import quality caveat still gates default adoption.

`/tmp/elisa-production-parallel-ir-root` points to the owned evidence directory
with the exact executable/environment/CWD, bootstrap logs, timing JSON, object
hashes, native profile and functional verification. Its cache is admitted by
normal CLI rules and can be reused by the installed byte-identical compiler
from the recorded directory/environment.


### Reuse the full permission solve's observed dependency graph

The converged full solver now supplies its observed read graph directly to the
retained permission state. Previously a second traversal of every function body
recomputed the same equations solely to record that graph. The new graph keeps
a conservative union of all reads during the solve, including both initial and
later worklist rounds. Final published rows are snapshotted without reevaluating
bodies. Nonconverged solves retain the previous final observation pass. Cold
non-retained analysis retains its existing scheduling and diagnostics.

Graph tests independently recompute the final equations and require every
fixpoint dependency to be present in the captured graph and every published row
to match. These include cycles, aliases, callbacks, lambdas, drop and cast hooks,
and lowered calls. The complete semantic suite, permission/retained race tests,
and separate-process analysis disk-cache tests passed.

Explicit source/options-checked qualification against the existing production
baseline (not normal CLI cache admission for the new executable) measured the
signature update at 2.688 s before and 2.123 s after, with final passes falling
from 2.410 s to 1.854 s. Subsequent native qualification measured update 1.939
and 1.945 s; the first build completed in 25.728 s with 64 native misses, and
the warm run in 10.620 s with 64 disk hits. Every build used `timeout 30s`.
The emitted signature object is byte-identical to the previous verified object:
`9fefc498f0269171232fd3fd4ec017c7f16c0f807e8ee602b59bb059adeaa8e7`.
Temporary Stage1 edits were restored. Logs and native output are under the
production evidence root as `capture-permissions-*.json` and
`captured-permissions-native/`.

This gain does not establish a sub-ten-second normal CLI signature rebuild.
The new executable still needs normally admitted bootstrap/matrix evidence;
full signature permission validation and the existing native/quality caveats
remain. No semantic snapshots or native products were rekeyed into another
executable's production namespace.


### Hydrate before the primary parse

Normal source analysis now starts checked artifact reading and graph hydration
once included-runtime authority and semantic options are finalized, before the
primary parse. The invocation still joins its restore/preparation work before
semantic replay, fallback analysis, and on primary parse failure. There is no
server or work retained across invocations. The analysis callback recomputes the
artifact authority and declines a mismatched prefetched key. Absent or rejected
artifacts retain the existing cold/bootstrap path and diagnostic handling.

The complete `TestAnalysisDisk` group and runtime-source authority checks passed.
Race checks passed for hydration/preparation, failed-parse joining, explicit
runtime-source inclusion, and the fresh-process edit oracle (cold IR/diagnostic
comparisons). A test hook in the explicit qualification harness permits matched
before/after phase measurements without admitting an old snapshot into the new
executable's normal cache namespace.

Isolated full-source signature qualification with the same executable and native
cache measured late hydration at 10.062 s and early hydration at 9.000 s. Both
reused all 64 native units; native time was 2.315/2.305 s and prepared update
1.939/1.890 s. The final signature object remained byte-identical to the previous
verified `9fefc498...` object. These are explicit qualified-snapshot runs, not a
claim about a normally bootstrapped new production executable. Logs are
`early-restore-{late,early}-isolated.json` under the production evidence root.
An earlier cold qualification overlapped a race-test build and reached the
30-second cap; it is excluded from performance comparisons. The retry reused
61/64 units and finished in 14.173 s, completing the remaining native products.
All compiler builds retained `timeout 30s`; temporary source edits were restored.

Normal new-executable bootstrap and novel-edit matrix evidence remain necessary,
as do the broader major-edit and default optimization-quality gates.


### Normally admitted production matrix after early hydration

The frozen `7b237070` production compiler created its own new cache from normal
CLI requests; no snapshot or native namespace was copied. Bootstrap requests
1–8 checkpointed in 16.150, 25.563, 24.398, 18.176, 22.772, 23.882, 25.181 and
29.020 s. Request 9 published a complete 563,861,428-byte state envelope before
its 30.228 s timeout; request 10 completed object emission in 19.537 s. Every
compiler request had an external `timeout 30s`.

| Normal CLI revision | Seconds |
| --- | ---: |
| Cached original | 0.546 |
| Novel body edit | 9.749 |
| Repeat body | 0.435 |
| Add ordinary function | 9.256 |
| Repeat function addition | 0.436 |
| Add unused plain struct | 10.040 |
| Repeat struct addition | 0.547 |
| Change existing signature with a defaulted parameter | 11.330 |
| Repeat signature | 0.544 |
| Add leading comment/line | 10.445 |
| Revert original | 0.452 |

All requests succeeded and emitted the same 9,381,562 diagnostic bytes. The
objects matched the previous respective body/addition/signature/original hashes.
`/tmp/elisa-early-production-root` points to the owned evidence directory with
frozen executable hash, exact environment/CWD, bootstrap and matrix logs. All
temporary Stage1 edits were restored. This matrix still fails the complete goal:
novel signatures, struct additions and line edits remain above ten seconds.

### Reindex permission equations across verified function replacements

Function-set edits may now reuse the surviving equation graph after checking
node/type/scope identity, distinct genuinely new callable types, and that every
surviving reader of a retired row has a refreshed body. Reader indexes are
remapped explicitly, and invalid references decline reuse before mutating the
cache. The retired row's complete transitive reader cone restarts from declared
rows and is enqueued in the incremental solver. This is necessary even when a
first evaluation seems unchanged: recursive peers can otherwise keep a removed
effect alive. General reindexing clears validation diagnostics so every function
is validated again. Existing verified append-only behavior is retained.

An initial uncommitted implementation failed the call-cycle retraction oracle;
it was corrected before retention. The complete semantic suite then passed,
including a new originally-present-effect withdrawal test, signature/removal
and snapshot round trips, explicit permission-row comparisons and negative
reader guards. Targeted permission/retained race tests and all separate-process
analysis disk-cache tests (including cold IR/diagnostic edit oracles) passed.
There are no new persistent graph fields or weakened invocation checks.

Explicit checked qualification against the preceding normally admitted baseline
measured signature update 2.034 -> 1.182 s, with final passes 1.647 -> 0.864 s.
Full native qualification completed in 23.772 s with 64 misses and 8.718 s with
64 disk hits; updates were 1.147/1.148 s. The emitted signature object remained
byte-identical:
`9fefc498f0269171232fd3fd4ec017c7f16c0f807e8ee602b59bb059adeaa8e7`.
Evidence is `reindexed-permissions-*.json` and `reindexed-permissions-native/`
under the new production evidence root. Every build used `timeout 30s`.

These qualification runs do not authorize the new executable to consume another
executable's production namespace. Fresh normal CLI evidence for this change,
remaining novel-edit misses, broader major edits and the existing optimization
quality/default-adoption gates remain open. The overall goal is not complete.


### Validate canonical struct fields without per-value maps

The ELISTATE3 encoder already writes nonzero struct fields in increasing schema
order. Its decoder now walks that ordered allowed-field list once, rejecting
duplicate, descending, excluded and unknown IDs without allocating a seen-field
map or repeatedly scanning the list. Sparse fields and structs larger than a
machine-word bitset remain supported. Encoded output and schema fingerprints
are unchanged; snapshots written by previous local encoders remain readable.

Isolated 10,000-record, sixteen-field decoding benchmarks improved from
7.634/7.666 ms, 10,646,553–557 bytes and 50,009 allocations to 2.745/2.797 ms,
1,286,498–501 bytes and 9 allocations. New tests exercise canonical sparse
input, duplicate/descending/excluded/unknown/overflow IDs, truncated values and
130-field round trips. The complete codec race suite, semantic snapshot/build
and retained/permission tests, and all separate-process analysis disk-cache
oracles passed.

Source/options-checked full compiler qualification measured overlapped restore
4.504 -> 4.039 s. Full native qualification completed in 23.531 s with 64 misses
and 8.318 s with 64 disk hits; the warm overlapped restore was 4.329 s and native
stage 2.224 s. The emitted signature object is byte-identical to the previously
verified `9fefc498...` object. All builds used `timeout 30s`; temporary source
edits were restored. Evidence is `ordered-fields-*.json` and
`ordered-fields-native/` under the early-production evidence root.

These are explicit qualified-snapshot measurements. The next normal production
matrix must bootstrap the latest executable under its own key; previous normal
signature/struct/line misses and the broader major-edit/quality gates remain
open. The full goal is still unproven.


### Latest normal CLI matrix and a used-layout miss

The frozen `ae945119` production executable bootstrapped a fresh normal cache
without importing snapshots or native products. Requests 1–8 checkpointed in
16.301, 25.579, 23.339, 17.660, 22.497, 23.628, 25.184 and 28.385 s. Request 9
published the 563,401,183-byte state envelope before its 30.219 s timeout;
request 10 completed object emission in 18.372 s. All builds used `timeout 30s`.

| Normal CLI revision | Seconds |
| --- | ---: |
| Cached original | 0.570 |
| Novel body edit / repeat | 8.985 / 0.427 |
| Add ordinary function / repeat | 8.872 / 0.449 |
| Add unused struct / repeat | 9.498 / 0.433 |
| Existing signature with defaulted parameter / repeat | 10.194 / 0.439 |
| Leading comment/line | 9.986 |
| Revert | 0.549 |
| Add defaulted i64 field before `IncrementalCacheRead.bytes` / repeat | 14.298 / 0.547 |
| Remove `stage1_semantic_options_from_observations` flow-strict postcondition / repeat | 9.309 / 0.430 |
| Revert major cases | 0.428 |

Every request succeeded with 9,381,562 diagnostic bytes. Earlier body/addition/
signature/original hashes were reproduced. Removing the postcondition produced
the original object; the used-layout change produced
`89c7492da1be922960102f69478006ad2dbe5c013b01487588cb3e02a9fe0272`.
The layout-edited object linked into a runnable compiler. With its Stage1
sidecar cache enabled, it compiled a fixture twice (`prior=miss`, then
`prior=valid`), exercising the changed `IncrementalCacheRead` return layout and
its bytes field. The fixture linked and returned 42. All temporary source edits
were restored; the seven pre-existing user/Claude changes remain untouched.

A normal CLI observer reading the same admitted baseline (only cache-debug,
build-timing and CPU-profile observers enabled) replayed the layout edit in
7.805 s: restore 4.114 s, prepared scratch 2.607 s overlapped, semantic update
0.923 s, IR/native stage 2.005 s, merge 0.050 s, no native misses. Seven unique
native cache entries have write times in the original layout-build window.
The large first-edit gap therefore points to invalidated O3 units rather than
semantic fallback. Each cache entry still compares exact current bitcode and
all target/compiler authority before reuse. Finer native granularity or reduced
duplicate specialization work is the next investigation, not weaker admission.

`/tmp/elisa-ordered-production-root` points to the owned directory with exact
executable hash, environment/CWD, bootstrap/matrix JSON, observer CPU profile,
objects and functional logs. The full goal remains incomplete: signatures are
still just over ten seconds, line-only edits have almost no margin, used-layout
edits take fourteen seconds, and broader major-edit and optimization-quality
qualification remains open.


### 2026-10-24: finer granularity did not remove the critical path

A temporary, subsequently reverted extension allowed 128/256 units. Matched
qualification used one frozen test executable, the same checked original
semantic baseline, exact O3 inputs and separate native-output directories. No
semantic snapshots were rekeyed into a production namespace.

| Units | Initial build | Warm baseline | Novel used layout | Warm layout | Novel signature | Warm signature | Native products |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 64 | 21.649 | 7.125 | 12.645 | 7.586 | 8.874 | 7.965 | 509 MB |
| 128 | 23.382 | 7.257 | 12.853 | 8.033 | 8.892 | 8.350 | 917 MB |
| 256 | 26.664 | 8.259 | 12.620 | 9.011 | 9.187 | 9.067 | 1,730 MB |

The earlier normal CLI 14.298 s layout result was insufficient as a comparator
for the 128-unit qualification. The matched comparison shows no meaningful
end-to-end gain from finer units. The experimental cap and fixture changes were
reverted; production limits/defaults stay as before. Functional checks on the
experimental layout/signature compilers at both larger counts linked and ran
fixtures returning 42, including successful second-read sidecar admission. The
experiment's data remain as `fine-*.json` and `fine-partitions-*/` under the
ordered-production evidence root.

### Define source globals and exported aliases once per partitioned program

Native object inspection exposed a different cost: 64 current unit objects
contained 417,858,704 file bytes, including 397,814,778 initialized data bytes.
A single mutable `annotation_pair_next` array occupied four MiB in many units;
other mutable parser/backend arrays were duplicated too. Partition zero already
provides their actual definitions, but the non-owners were emitting redundant
weak initialized copies. Non-owner generators now retain source-global
declarations instead of building their initializers. Aggregate constants and
on-demand generated storage keep their existing initialization/linkage paths.

Export aliases also belong to the data owner. Non-owner generators bind either
source name to the same target value without emitting an LLVM alias to an
undefined declaration. An initial alias-preserving workaround passed runtime
address checks but rebuilt all four units when its initializer changed; the
final owner-only alias path rebuilds one. New O0/O3 native C-client tests check
initial values, volatile alias/target address equality, writes through the
public alias observed by LLVM-generated getters, disk initializer edits and
reverts. IR checks cover ordinary/owner/non-owner globals, aliases and retained
aggregate-constant initializers. Source-global writers/readers are forced into
non-owner partitions. Native fixture links/runs now use 30-second contexts.

Targeted backend checks, including pre-O3 fresh-process disk reuse, global and
target changes, constant address identity and same-name exports, passed both
normally and under the race detector. All main-package `TestAnalysisDisk`
oracles passed. The rebuilt baseline/layout/signature compilers linked, reported
the expected unknown-flag/option diagnostics, compiled an included fixture
twice (`prior=miss` then `prior=valid`), and produced programs returning 42.

Checked full-source qualification at 64 units measured:

| Case | Previous | Single source-global owner |
| --- | ---: | ---: |
| Initial native build | 21.649 | 21.777 |
| Warm baseline | 7.125 | 6.620 |
| Novel used layout | 12.645 | 12.122 |
| Warm layout | 7.586 | 7.342 |
| Novel signature | 8.874 | 8.545 |
| Warm signature | 7.965 | 7.631 |
| Retained native bytes | 509,435,200 | 110,643,808 |

All compiler/linker builds retained the 30-second cap. Source edits were
restored. The final linked objects differ by sixteen bytes of initialized data
and relocation encodings, so byte identity is not claimed; text-section sizes
match the earlier versions and runtime tests pass. Evidence is
`source-global-owner-*.json`, `source-global-owner-64/`, native inventories and
`source-global-owner-alias-race.log` under the ordered-production root.

The full goal remains open. The latest executable needs fresh normal CLI
admission/matrix evidence, and the used-layout cold native miss remains above
ten seconds. The same inventory found thousands of repeated weak specialization
bodies (4,917 ordinary weak-external text definitions for 919 unique names),
which motivates investigating duplicate optimization/emission while preserving
O3 and inlining inputs. No such specialization ownership change is implemented
by this commit; default optimization-quality and broader edit gates remain open.


### Fresh normal CLI admission after source-global ownership

The installed `e0a224b6` executable (SHA-256
`9adf671b075524eb0656ca25fc83450ec592963ce80b2efb1d083728870f0369`)
was frozen into a new owned directory and admitted its semantic baseline through
normal CLI bootstrap requests. No earlier snapshot was rekeyed. Every request
had an external 30-second timeout. Eight requests saved intermediate checkpoints;
the ninth timed out after publishing the complete baseline, and the tenth
successfully emitted the initial object in 17.558 seconds.

Fresh compiler processes then measured the following normal CLI builds with disk
analysis/native caches, the server disabled, O3, 64 independent IR units and
parallel native optimization enabled:

| Source change | First edited build | Repeated source |
| --- | ---: | ---: |
| Function body string | 8.614 s | 0.441 s |
| Add function | 8.032 s | 0.420 s |
| Add unused struct | 8.647 s | 0.420 s |
| Add defaulted signature parameter | 9.654 s | 0.420 s |
| Leading comment / line shift | 9.279 s | — |
| Used struct layout | 13.283 s | 0.536 s |
| Remove real postcondition | 8.919 s | 0.412 s |

The original revision repeated in 0.517 seconds; reverting after the minor edit
matrix took 0.425 seconds. Every completed build returned zero and replayed
9,381,562 diagnostic bytes. Source variants were restored, preserving all seven
pre-existing Claude/user changes. Evidence, exact environment/CWD, executable,
objects and per-case JSON are under `/tmp/elisa-global-owner-production-root`.

These numbers qualify the explicitly enabled independent-unit mode. Its existing
loss of cross-unit inlining remains an optimization-quality limitation; default
whole-module O3 equivalence is not claimed. The used-layout edit still misses
the ten-second goal, and broader major-edit/runtime-quality gates remain open.


A subsequently reverted weak-specialization ownership experiment kept duplicate
bodies available to the ordinary O3 inliner while suppressing their out-of-line
emission. An all-IR barrier measured 12.156 seconds for the novel layout and
7.422 seconds for the warm baseline, both worse than the earlier 12.122/6.620.
Pipelining deterministic prefix ownership reduced layout to 11.744 seconds but
measured 6.689 seconds warm baseline, 7.595 seconds warm layout, and 9.147 seconds
novel signature (earlier 8.545). The small isolated layout improvement did not
justify the memory/scheduling complexity and regressions. Neither implementation
is retained. Explicit qualification evidence remains in `weak-owner-64-*.json`
and `weak-prefix-64-*.json` under the same production evidence root.


### Reuse checked bitcode for native worker input

Both pre-O3 partition paths serialized current LLVM IR to check the native cache,
then serialized the same unchanged module again to write a worker input file on
a miss. Workers now receive the first serialization directly. Its LLVM buffer
remains owned until the synchronous worker call returns, including when a unit
exceeds the cache budget and no owned cache-key copy is retained. Cache inputs,
target settings, O3 pipeline, partition ownership and compiler identity checks
are unchanged. Per-unit native timing is available through the existing
`ELISACORE_BUILD_TIMING=1` observer.

Fresh-process O2/O3 native edit/revert/line reuse, global/target changes and
exported-global alias address/value tests passed. A new regression uses a
one-byte session budget, checks both split-module and independent IR paths,
executes linked output twice, and confirms every native unit is rebuilt without
retaining any cache entries. These checks also passed under the race detector;
main-package `TestAnalysisDisk` oracles passed.

Explicit full-source qualification (checked old semantic payload, no production
rekeying) compared immediately preceding frozen executables at 64 units:

| Case | Duplicate serialization | Reuse checked bytes |
| --- | ---: | ---: |
| Initial native build | 22.564 s | 21.700 s |
| Warm baseline | 7.079 s | 6.709 s |
| Novel used layout | 12.680 s | 12.260 s |
| Warm layout | 7.674 s | 7.679 s |

The first native build's aggregate worker time fell from 138.822 to 131.736
seconds; layout aggregate worker time was 28.031 versus 27.443 seconds. These
single samples include machine variance; the concrete gain is removing one full
bitcode serialization per native miss. The baseline and layout final objects
were byte-identical to their previous counterparts. Both linked compilers
compiled an included fixture twice (sidecar miss then valid), and both fixture
executables returned 42. All builds and links retained 30-second external caps.
Evidence is `unit-timing-64-*.json`, `once-bitcode-64-*.json` and
`once-bitcode-64/functional.json` under the global-owner production evidence root.

Restoration investigation found 7,250,967 graph objects in the 563,710,010-byte
payload, 28,274,310 descriptor bytes, and about 1.44 GB estimated logical backing
storage before object-table/descriptor metadata. A four-worker pointer-arena
allocation experiment did not improve three alternating restore-only samples:
serial 3.956/3.837/3.917 seconds, parallel 3.895/3.882/3.907 seconds. It was
reverted. GOGC 100/400/off also showed no material restoration gain.
`state-inventory.json`, `parallel-hydration-restore.json` and `restore-only.pprof`
preserve those observations. The ten-second used-layout goal and full runtime
optimization-quality/major-edit qualification remain open; this change does not
claim fresh normal CLI admission for the new compiler executable.


### Normal CLI broader-edit probes and caller-arena function removal

`288cb6b7` was freshly admitted through normal CLI bootstrap in an independent
cache directory. Eight partial requests took 16.089–28.420 seconds; request nine
hit its 30-second cap after publishing the complete baseline, and request ten
emitted the initial object in 18.174 seconds. The frozen executable SHA-256 is
`8ee7695769d904fafff2d37efbc0fa5a8b6b1748a7624c7c58d95ae152708bd5`.
`/tmp/elisa-once-bitcode-production-root` points to its environment/CWD,
bootstrap logs, admitted snapshot and normal CLI edit evidence.

| Normal CLI edit | First request | Outcome / repeat |
| --- | ---: | --- |
| Original source | 0.422 s | Whole-object hit |
| Used struct layout | 13.578 s | Success; repeat 0.532 s |
| Used constant 64 → 65 | 20.770 s | Cold checkpoint; no object |
| Swap two const-enum ordinals | 20.270 s | Cold checkpoint; no object |
| Add declared Console permission | 11.217 s | Success; repeat 0.540 s |
| Remove existing caller-arena wrapper | 20.579 s | Cold checkpoint; no object |

The baseline/layout/permission compilers linked, compiled the included fixture
twice (miss then valid), and produced executables returning 42. All temporary
edits were restored. The new constant/enum/removal first requests are failures
of the requested fast-rebuild behavior, not successful timings. Explicit checked
replay isolated the reasons: the constant occurs in a function contract header;
const-enum declarations are not supported by the existing plain-enum editor;
and removal changed `regionPolyReturnsCallerArena` from 606 to 605 entries.

The latter table has at most one declaration-phase entry per changed function.
Its size can now move by the accepted function add/remove count, while the
existing individual declaration fingerprints still reject changed summaries of
surviving functions. Removing a live function also deletes its caller-arena
entry. A new regression failed on the old guard, then passed removal, re-add,
removal and a later caller edit across disk round trips, comparing diagnostics
and all surviving function region summaries to cold analysis. Another real
fixture verifies that a simultaneous change to a surviving function's inferred
region signature still rejects incremental reuse.

The full semantic suite, targeted snapshot/region race checks and main-package
`TestAnalysisDisk` oracles pass. An accidentally broad race selection also
exposed the existing parallel flow/typestate race in
`TestTSRealConstructorInInitialState`; the older frozen
`/tmp/elisa-reindexed-permissions-race-v2` independently reproduced it for the
same test. This is recorded in `baseline-typestate-race.log`; the broader
semantic race suite is not claimed clean.

Full-source explicit qualification of the corrected removal measured 10.544
seconds with three native misses, versus the old immediate cold fallback, and
8.398 seconds with those native products warm. Replay itself took 1.212 seconds.
The removed-wrapper compiler linked and compiled the included fixture twice,
producing an executable returning 42. All compiler/linker builds remained capped
at 30 seconds. `region-remove-64-*.json`, `region-remove-64/functional.json`, and
`replay-diagnose-*.json` preserve the evidence. This qualifies checked replay,
not fresh normal CLI admission of the subsequent semantic fix.

The full goal remains open. Besides layout and permission misses and unsupported
contract-constant/const-enum edits, removal is still just above ten seconds.
Its update reports two removed functions and one added function for deleting
one wrapper; overload tracking currently uses bare names across namespaces.
Qualified overload bookkeeping is the next investigation to avoid re-declaring
a separate same-named module function. Optimization-quality gates remain open.


### Scope overload registration and preserve unrelated function metadata

Overload edit bookkeeping now uses qualified declaration names for supported
namespace members. Bare names remain conservative caller-invalidation hints.
Deleting one global wrapper no longer forces a separate same-named module
function through removal/re-registration. Function-type cleanup checks the
owning type before removing an alias, and shared region-parameter metadata is
preserved when another same-named function survives. A surviving same-named
function is still re-analyzed if its body references the removed callee.

New disk-round-trip tests remove/re-add global and nested-module functions,
add/remove real overloads, assert surviving type identities and metadata, and
compare cold diagnostics. A second test checks a same-named caller in another
namespace losing and regaining its callee. Existing overload and caller-arena
regressions, the complete semantic suite, targeted semantic/snapshot race checks
and main-package disk-cache oracles pass.

Full-source checked qualification now reports Added=0/Removed=1 for the wrapper
edit, versus Added=1/Removed=2 previously. First removal was 10.357 seconds
(previous 10.544), warm removal 8.041 (previous 8.398), with replay 1.133 seconds.
These small latency differences include machine variance; the verified change
is avoiding unrelated declaration replacement while preserving cold behavior.
Baseline/removal objects match the preceding objects byte-for-byte. Both linked
compilers compiled the included fixture twice (miss then valid) and the produced
executables returned 42. Every build/link retained the 30-second cap. Evidence is
`qualified-overload-64-*.json` and `qualified-overload-64/functional.json` under
the once-bitcode production evidence root. Fresh normal CLI admission of this
semantic change remains unqualified.

Three native units still miss on removal. The bitcode inspection in
`qualified-overload-64/bitcode-diffs/` establishes that two of them differ solely
in generated local `__auto_<source-offset>` value names: after substituting those
names in disassembled IR, both complete module texts match. This is an evidence
comparison, not relaxed cache admission. Stabilizing non-debug local LLVM names
in the actual emitted IR is the next optimization; exact bitcode validation must
continue to compare the same bytes consumed by the native worker. The full
sub-ten-second and optimization-quality goals remain incomplete.


### Discard non-debug local LLVM names before lowering

Native object sessions now set LLVM's `LLVMContextSetDiscardValueNames` before
lowering when debug info is disabled. LLVM retains GlobalValue names (functions,
globals, aliases) while omitting local argument/instruction/block names. The
actual emitted/cache-checked IR uses those unnamed values; cache comparison is
still complete exact bitcode plus target/compiler authority. Textual IR and
debug object generation keep their existing named contexts. O3, inlining,
alias-analysis and unroll settings are unchanged.

Fresh-process split-module and independent-IR tests now rename source locals:
non-debug units all hit the disk cache, body changes still rebuild one unit,
and debug renames rebuild current debug artifacts before repeating as hits.
Debug fixtures exposed invalid transient linkage during lazy module loading.
Omitted functions now keep valid temporary declaration linkage through metadata
materialization, then restore original linkage for the existing namespace and
externalization step. This preserves lazy loading rather than copying foreign
bodies. The prior helper independently reproduces the failure in
`debug-linkage-before.log`.

Fresh-process O2/O3 edit/revert/line/local/debug matrices, uncached oversized
inputs, exported-global alias addresses/values, global/target changes and C++
partition guards pass. Relevant backend matrices also pass under the race
detector, as do main-package disk-cache oracles. The C++ fixture compiler/run
now have a 30-second context.

Full-source checked qualification with the same admitted baseline measured:

| Case | First build | Native products warm |
| --- | ---: | ---: |
| Original source | 21.001 s | 6.332 s |
| Used struct layout | 11.757 s | 7.381 s |
| Existing wrapper removal | 9.688 s | 7.638 s |
| Defaulted signature parameter | 8.366 s | 7.466 s |
| Function body string | 7.507 s | 6.858 s |

Removal now misses one native unit instead of three; its preceding qualified
first-build sample was 10.357 seconds. Retained native inputs/objects for the
original revision fell from 110,643,808 to 56,769,856 bytes. Warm native time was
1.765 seconds, including current IR generation and exact-key checks. Layout still
misses seven native units and exceeds ten seconds. All builds/links kept their
30-second cap and source edits were restored.

Every baseline/layout/removal/signature/body final object is byte-identical to
its preceding counterpart (`object-parity.json`). All five linked compilers
compiled the included fixture twice (sidecar miss then valid), and each fixture
returned 42. Evidence is `discard-locals-64-*.json`,
`discard-locals-64/functional.json` and `object-parity.json` under the once-bitcode
production evidence root. These are explicit checked-payload qualifications;
fresh normal CLI admission of this executable remains to run. The existing
independent-unit cross-unit-inlining limitation, layout/permission targets,
contract-constant/const-enum fallbacks and broader major-edit/quality gates keep
the full goal open.


### Fresh normal CLI admission and edit matrix for 8fc62b88

The installed executable was frozen with SHA-256
`23977e0b2222ae51dd9f9a2ed08fc6f5ed50389f1453e873f00e753a476537fb`
and admitted a new semantic baseline through normal CLI requests in an empty
cache directory. No earlier state was rekeyed. Eight requests saved partial
checkpoints (16.075–28.053 seconds); request nine hit its 30-second cap after
publishing the complete snapshot, and request ten emitted the initial object in
18.137 seconds. The complete checked envelope is 563,023,605 bytes.

`/tmp/elisa-discard-locals-production-root` points to this directory, its exact
environment/CWD, executable, bootstrap logs and normal CLI records. Disk analysis
and native caches were enabled, server forwarding disabled, and the same explicit
O3 independent-IR/parallel-opt mode and 64 units retained. Every compiler/linker
invocation had an external 30-second timeout.

| Normal CLI source edit | First request | Repeat |
| --- | ---: | ---: |
| Original revision | 0.531 s | — |
| Used struct layout | 12.881 s | 0.575 s |
| Declared Console permission | 8.673 s | 0.420 s |
| Existing caller-arena wrapper removal | 10.423 s | 0.421 s |
| Function body string | 8.632 s | 0.423 s |
| Defaulted signature parameter | 9.605 s | 0.422 s |
| Remove real postcondition | 8.723 s | 0.426 s |
| Leading comment / line shift | 9.142 s | 0.418 s |

The signature probe here changes only the signature; earlier signature
qualification also changed the body string. Permission edits improved from the
preceding normal CLI sample of 11.217 seconds. Removal and layout remain above
ten seconds despite their faster checked-payload results. Native cache file
birth times show seven new products for layout, one for removal/body, three for
signature, none for permission/contract, and one for the line probe.

All eight rebuilt compilers linked, produced the expected unknown-flag/option
runtime diagnostic, compiled the included fixture twice (sidecar miss then
valid), and emitted executable fixtures returning 42. Their functional records
are `functional.json` and `functional-diagnostics.json` in the evidence root.
All source variants were restored; the seven pre-existing Claude/user changes
remain untouched.

The full goal is still incomplete: normal removal takes 10.423 seconds, used
layout 12.881, and contract-used constant and const-enum edits retain their
previously measured cold fallbacks. Runtime/default optimization-quality gates
also remain open. Native critical-path timing shows the removed unit starting
late and large layout units starting behind cache-hit work; bounded advisory
native cost scheduling is a candidate next investigation, with exact current
bitcode/target authority unchanged.


### Rejected native cost-order prototype

A bounded, checksummed history of measured native unit durations was prototyped
as advisory scheduling metadata. Exact current bitcode and target validation
remained the object reuse authority. Matched checked-payload runs with separate
native namespaces gave:

| Request | Existing order | Cost history order |
| --- | ---: | ---: |
| First native population | 20.817 s | 20.833 s |
| Baseline replay, native hits | 6.323 s | 6.515 s |
| Used layout edit | 11.692 s | 11.853 s |
| Layout repeat | 7.143 s | 7.285 s |
| Wrapper removal | 9.215 s | 9.015 s |
| Removal repeat | 7.271 s | 7.549 s |

This did not establish a reliable overall improvement, so the entire prototype
was removed without changing the committed scheduling implementation. Logs are
`native-cost-{before,after}-64-*.json` in the directory identified by
`/tmp/elisa-discard-locals-production-root`. The rejected patch and its two new
files were saved under `/tmp/elisa-native-cost-rejected.patch` and
`/tmp/rejected-incremental_object_cost*.go` for investigation only.


### Persistent constant-enum member replay

Retained updates now recognize implicit-valued constant-enum member edits with
an unchanged explicit storage declaration. They preserve the live type identity,
replace both its member table and folded `constValues` entries, clear type memos,
and reanalyze functions whose types or bodies can observe the enum. The shared
type-dependency walker now recognizes `ConstEnumType` targets. Member count deltas
are included in the existing declaration-phase table-size validation.

Declaration dependencies, static/law function users, contract/default references, explicit member values,
inferred or changed storage, and open/foreign enum edits still fall back. The
new guard distinguishes enum type references and shorthand member access from
unrelated bare declaration names and comments. It does not remove declaration
phase fingerprint or diagnostic checks.

Disk round trips cover reorder/add/remove/restore, current member values and
cold diagnostic parity. A strict-SMT fixture retracts and restores a numeric
proof when a reordered member's integer value changes. A fresh-process CLI
oracle compares the current LLVM IR and diagnostics with cold compilation for
reorder/rename/revert and checks the admitted baseline remains byte-identical.


The full semantic suite passed during development, as did the fresh-process
`TestAnalysisDisk` oracles. A checked-payload self-build qualification used the
previously admitted, fully validated envelope (not a production rekey), O3 with
independent IR/parallel native optimization, 64 units, no server, and 30-second
external request caps. Its evidence is under `const-enum-qualified-v5-64` and
`native-cost-const-enum-v5-64-*.json` in the directory selected by
`/tmp/elisa-discard-locals-production-root`.

The first accepted reorder rebuilt in 11.608 seconds, rechecking 30 functions.
Narrowing body invalidation from arbitrary matching field names to enum type
references and actual shorthand members reduced this to two functions and
10.683 seconds (repeat 8.313 seconds). Its restore/scratch overlap took 4.427
seconds, replay 1.888, and native generation/merge 4.005. The two native misses
were units 8 and 43. Baseline native population took 22.238 seconds and its
all-hit replay 6.651. This is a verified escape from the previous cold fallback,
not yet a sub-ten-second normal CLI result. Fresh production admission for this
compiler namespace is still required.

Both baseline and reordered objects linked and ran the included fixture twice,
with the second request reporting a valid disk sidecar and both executables
returning 42 (`const-enum-qualified-v5-64/functional.json`). The reordered native
object SHA-256 is
`b7ca554f948253d1a6395021fe8a35f3e5573738a7ea5c577cc48b92cb85f661`.
Source probes were restored; the seven pre-existing Stage1 changes remain.


### Persist body dependency facts and qualify finer native units

The idle semantic snapshot now records, for each retained function, the exact
identifier/member names and deduplicated expression types that the existing
`retainedMentionsAny` / `retainedUsesTypes` walks consult. This includes contracts
and default arguments. Replay reads those facts instead of repeating the full
body walk. Signature types are still read from the live function symbol. Missing
indexes retain the original walk, and an update invalidates the index before
applying edits (including all fallback exits). A later snapshot publication
rebuilds it from the finished AST rather than trusting earlier scan memos.

This changes the checked logical schema. A new baseline was admitted through
normal CLI checkpoint requests in an empty cache; no prior snapshot was rekeyed.
`/tmp/elisa-body-use-production-root` selects its executable/environment/CWD,
bootstrap records, sources/probe logs and products. The 64-unit prototype producer
is SHA-256
`97ad7faefea6df5e23115083993e01d5ec8c6f07158fa394471768166474c476`.
Its complete envelope is 567,418,066 bytes, up 4,394,461 bytes (about 0.8%).
Requests 1–8 saved incomplete checkpoints in 16.291–27.961 seconds; request 9
reached its 30-second cap after publishing the complete state; request 10 emitted
the native object in 17.236 seconds. These costs include index construction and
serialization. They are not a claim that cold admission completes in one request.

The index is built at snapshot publication, not on each supported edit. Normal
CLI replay keeps the admitted baseline immutable (`!replayed` guards publication),
and the fresh-process oracle verifies the baseline bytes remain unchanged across
edits. An explicit re-serialization of the admitted snapshot took 11.381 seconds,
including rebuilding the index, or 16.249 seconds including restore/scratch and
writing the payload. It emitted 566,359,095 payload bytes; comparison bodies were
already projected in that input, so this is not a replacement for the cold
admission measurement. Sampling attributed 210 ms CPU to `captureBodyUses`.
Logs/profile are `snapshot-publication-cost.json`, `snapshot-index-cpu.txt` and
`snapshot-publication-cost/`; the payload is qualification evidence, not a new
production admission.

Small disk-round-trip tests cover name-only unresolved calls, dependencies
through callees, contracts/defaults, and a body edit followed by a field rename
that must retract the new user's old field access. The opt-in large-snapshot
oracle compared indexed answers with the original AST walk for all 12,048
functions, for both `IncrementalCacheRead` and `CliArgumentKind`; both passed
(`body-use-oracle.log`). The full semantic suite, retained/state/snapshot race
selection, `TestAnalysisDisk` fresh-process oracles, and partition/cache/native
backend fixtures passed. This does not claim the unrelated broader typestate
race suite is clean.

The first normal CLI matrix used the 64-unit prototype before the subsequent
header-format cleanup: enum reorder 11.480 seconds, enum member + classifier +
handler addition 11.994, layout 13.032, permission 8.910, wrapper removal 10.838,
body 9.047, signature-only 9.983, contract 9.086, lines 9.389. Repeats were
0.427–0.545 seconds. These are retained as evidence of remaining normal CLI
misses, not relabeled as final results.

Profiling also found that the constant-enum header guard asked `retainedText`
for a header, needlessly formatting each complete function body to compute the
unused gensym comparison. It now requests exactly the same normalized header
without that body formatting. The explicit native unit limit is raised to 128;
the ordinary CPU-based default remains unchanged. After the earlier global-owner
and local-name changes, increasing unit count no longer duplicates the old
hundreds of megabytes of source globals.

Matched checked-payload qualifications with the same final test executable,
O3 independent IR, isolated native caches, and 30-second external request caps:

| Request | 64 units | 96 units | 128 units |
| --- | ---: | ---: | ---: |
| Native population | 21.537 s | 21.090 s | 22.132 s |
| Baseline replay, native hits | 6.840 s | 6.483 s | 6.693 s |
| Enum reorder | 10.016 s | 8.174 s | 9.912 s |
| Enum repeat | 7.773 s | 7.851 s | 7.981 s |
| Used layout | 11.687 s | 11.872 s | 12.058 s |
| Layout repeat | 7.067 s | 7.114 s | 7.487 s |
| Wrapper removal | 9.463 s | 7.902 s | 8.197 s |
| Removal repeat | 7.581 s | 7.954 s | 7.701 s |

Initial native retained input/product bytes were 56,769,856 / 57,661,216 /
58,614,816 respectively. Actual first requests emitted 64 / 96 / 128 units;
there was no hidden clamp to 64. Records are `native-cost-body-wide-*-*.json`,
`body-wide-{64,96,128}/`; `wide-test.sha256` identifies the frozen test executable.
These explicit, invocation/source-validated payload qualifications do not rekey
or admit the earlier producer's state into the final compiler namespace.

All ten normal 64-unit products linked and compiled the included fixture twice
(sidecar miss then valid), with executables returning 42. C constructor probes
also called the emitted `cli_argument_kind` symbol: reorder changed Output/Filter
from 0/1 to 1/0, and the added CLI flag returned tag 37 and successfully passed
through its new handler. All four 96-unit products (baseline, enum, layout,
removal) passed the same link/fixture/value checks (`body-wide-96/functional.json`).
Twelve corresponding 64-unit products were compared byte-for-byte with earlier
products and were identical (`object-parity.json`); this is actual native-object
comparison, not an inference from green semantic tests.

The full goal remains open. Layout still exceeds ten seconds. Fresh normal CLI
admission/matrix for the final executable with 96 units remains to be done, and
contract-used constant / additional enum forms still have conservative fallbacks.
The independent-unit cross-inlining caveat and default/runtime optimization
quality gates also remain open; widening the opt-in limit does not establish
those gates or change the default unit count.


### Final 96-unit standalone admission and matrix (32f5233f)

Commit `32f5233f` was installed by the post-commit hook and frozen as SHA-256
`5eddbf85a9eabfe4b8b7b24c3638a27cf2308fe62667d03a19ec4a9aadad6dde`.
A new empty cache admitted its own baseline through normal CLI requests with
96 units, O3 independent IR/parallel optimization, and server forwarding disabled.
`/tmp/elisa-body-96-production-root` selects this evidence directory, exact
settings, CWD, executable and per-request logs. No prototype snapshot was copied
or rekeyed into this namespace.

Requests 1–8 saved checkpoints (16.450–27.897 seconds); request 9 reached the
30-second cap after publishing the complete state; request 10 emitted the object
in 16.804 seconds. The complete envelope is 567,604,910 bytes. Every matrix compiler invocation,
LLVM transform, native link and runtime fixture probe had a 30-second external cap.

| Normal standalone CLI source edit | First request | Repeat |
| --- | ---: | ---: |
| Original revision | 0.515 s | — |
| Const-enum member reorder | 9.451 s | 0.413 s |
| New enum member + CLI classifier + handler | 9.128 s | 0.427 s |
| Used struct layout | 12.793 s | 0.419 s |
| Declared Console permission | 8.762 s | 0.423 s |
| Existing caller-arena wrapper removal | 8.941 s | 0.413 s |
| Function body string | 8.046 s | 0.419 s |
| Defaulted signature parameter (signature only) | 9.018 s | 0.415 s |
| Remove real postcondition | 8.928 s | 0.426 s |
| Leading comment / line shift | 8.069 s | 0.432 s |

All ten products linked and compiled the included fixture twice, reporting a
sidecar miss then valid and producing executables returning 42. Native C probes
called `cli_argument_kind` and checked the reordered tags and new flag's tag 37;
the enum-add fixture compiled successfully with that new flag. Unknown-flag
runtime diagnostics were also checked: the body edit printed `unknown option:`,
all other variants `unknown flag:`. Records are `functional.json`. Controlled
sources were restored and the seven pre-existing Stage1 changes preserved. The large-snapshot oracle
also passed again against this final namespace: 12,048 indexed-vs-walk function
checks for each of the two target types (`body-use-oracle.log`).

Layout remains above ten seconds. Inspecting its slowest 96-unit input found an
unchanged `pymodule_dict_type_name_float_lists_records` function (19,916 IR body
lines) sharing native unit 80 with two generic instances of
`run_semantic_gate_incremental_with_session`. Comparing the corresponding
baseline/layout inputs showed only the changed `IncrementalCacheRead` type and
two field GEP indices, aside from llvm-dis's input-path comment. Exact bitcode
validation correctly misses, but O3 then reoptimizes the entire large unit.
The investigation (`layout-unit-investigation/` under the earlier
`/tmp/elisa-body-use-production-root`) preserves both bitcodes/IR, their diff and
function sizes. The large unchanged function contains 89 existing memcmp calls;
short literals use the current per-byte load/compare/and lowering. Compact,
bounded integer-chunk comparisons are a candidate to reduce that IR without
lowering O3; this has not been implemented or qualified. Actual generic-provider
membership would also give more accurate scheduling hints than bare source
function hashes. These observations do not establish either proposed gain.

The goal remains active: layout, conservative unsupported declaration/contract
edits, and default/runtime optimization-quality gates are still unresolved.


### Bounded short-string chunk lowering

Short string-view literals now compare complete 8/4/2/1-byte integer chunks
with alignment 1 and target byte order. Existing length/null guards still precede
loads; no chunk extends beyond the literal, and longer literals retain memcmp.
This keeps O3 and its tuning unchanged. Native guard-page tests cover lengths
0–9, every unaligned offset, embedded NUL/high bytes/UTF-8, mismatches, wrong
lengths and null views at O0/O3. LLVM target checks additionally constant-fold
known comparisons on x86-64, big-endian PowerPC64 and wasm32.

The 96-unit checked-payload profiler used the previous admitted snapshot with
full invocation/source validation and a separate native cache for the changed
executable. This is not normal CLI admission of the new executable. Records
`native-cost-string-chunk-96-*.json` live under
`/tmp/elisa-body-96-production-root`; outputs and runtime evidence are in
`string-chunk-96/`. Enum reorder was 8.125 s, layout 11.722 s and wrapper removal
8.027 s. These small timing differences do not establish a repeatable wall-time
gain over the earlier checked-payload measurements. The deterministic gain is
smaller IR: the large `pymodule_dict_type_name_float_lists_records` body falls
from 19,916 to 15,403 lines (23%); its unit input falls from 1,274,192 to
1,248,384 bytes. Exact extracted input and counts are in `ir-investigation/`.
All four products linked, compiled the included fixture with miss then valid
cache reports, ran with exit 42, and passed direct enum-tag and diagnostic
checks. Backend partition/cache regressions and main analysis-disk tests pass.

The layout target still fails, and new normal CLI admission plus its edit matrix
remain required. No server-free/default optimization-quality completion claim
is made by this reduction.


Normal CLI follow-up for `3a65a343`: the newly installed executable admitted
its own empty-cache baseline (`/tmp/elisa-string-chunk-production-root`). Eight
checkpoint requests took 16.399–27.751 s; request nine published the complete
snapshot before its 30 s cap, and request ten emitted the object in 16.292 s.
The full edit matrix succeeded: baseline 0.526 s; enum reorder 9.188 s; new
flag/handler 9.355 s; layout 13.120 s; permission 9.069 s; removal 9.142 s;
body 8.201 s; signature 9.152 s; contract 9.056 s; lines 8.314 s. Repeats
were 0.418–0.519 s. Thus there is no demonstrated end-to-end latency gain;
the committed gain is the independently verified IR reduction. Layout remains
above target. This run retains the same opt-in independent-unit O3 settings
and server-disabled environment as the earlier normal matrix.
All ten normal-CLI products also passed the included fixture's miss/valid
cache sequence and exit-42 execution, enum tag probes and expected unknown-flag
diagnostics (`functional.json`). Controlled sources were restored.


### Persisted generic-provider scheduling

Independent native units now persist their actual defined function stems,
including generic bodies emitted in a provider other than the bare source
function's hashed owner. On a later build, recently rechecked function hints
prioritize these provider units too. Every unit still generates its complete
current bitcode and validates the exact native cache key. The records authorize
no reuse or skipped work. Records are compiler-namespaced and keyed by logical
source unit, target configuration and partition count; publication is atomic,
checksummed and bounded to 4 MiB. They share the native cache's eviction budget.
Corrupt/unavailable records fall back to the existing complete work queue; stale
records can only change its order.
`ELISACORE_PARTITION_SCHEDULE=0` disables their scheduling use for comparison.

The complete new checked-payload profile (same options/snapshot as the normal
CLI matrix above, separate cache) measured enum reorder 8.680 s, layout 11.284 s
and removal 8.117 s. Its 540,211-byte membership record correctly identifies
provider units 8, 12, 64, 79, 80 and 94 for
`run_semantic_gate_incremental_with_session`. These six native misses now start
together instead of waiting for later hash-ordered unit generation.

A same-executable OFF/ON/ON/OFF comparison used separate copies of the same
pre-layout exact native entries and membership record. Only entries born before
the completed enum-warm request were copied (98 entries; the seven layout
products were absent). Layout times were OFF 11.509/11.918 s and ON
10.972/11.090 s. Records are `generic-schedule-ab.json` and each
`generic-schedule-ab-*/result.json` under `/tmp/elisa-string-chunk-production-root`;
`/tmp/elisa-generic-schedule-ab.py` preserves the setup and 30-second caps.
This is a modest reproducible scheduling gain, not the requested sub-ten result.
The four profile products and all four A/B layout products are byte-identical
to the functionally checked normal CLI products (`generic-schedule-96/object-parity.json`).
Backend regressions cover persisted generic membership across fresh sessions,
corrupt/oversized/configuration-incompatible records, complete unit scheduling,
and linked execution after edits/reverts at O0/O3. New normal CLI admission of
the scheduling executable is still required. Independent-unit/default/runtime
quality and unsupported declaration/contract gates remain unresolved.
Targeted scheduling persistence/order/fresh-session tests also pass under the Go
race detector. All controlled Stage1 sources were restored after profiling.


Normal admission for `d0c9a710` succeeded in its own empty namespace
(`/tmp/elisa-generic-schedule-production-root`): eight checkpoints
16.315–28.778 s, request nine capped at 30 s after publishing the complete
snapshot, request ten emitted an object in 16.622 s. The standalone CLI matrix
was baseline 0.531 s; enum reorder 9.552 s; new enum flag/handler 9.566 s; layout
12.572 s; permission 9.130 s; removal 9.601 s; body 8.892 s; signature 9.690 s;
contract 9.216 s; lines 8.469 s. Repeats were 0.435–0.559 s. Server forwarding
was disabled, and all compiler/link steps remained capped at 30 s. All ten
objects are byte-identical to the previously linked/executed `3a65a343` products
(`object-parity.json`); controlled sources were restored. The modest scheduling
gain survives normal CLI qualification, but layout is still over ten seconds.


A subsequent uncommitted large-function grouping experiment was rejected. It
reserved native units for ordinary functions with at least 128 top-level body
statements; linked O0/O3 tests proved layout-only reuse, body-edit invalidation,
revert reuse and same-name aggregate C exports. On the real compiler, however,
96 units gave enum reorder 10.687 s and layout 11.079 s, while 128 units gave
enum reorder 8.523 s and layout 11.756 s. Removing the large ordinary function
from the layout-sensitive unit did not remove the many duplicated generic
walker/fact-collector bodies that dominate its remaining O3 cost. The 96-unit
variant also made ordinary units denser. No production grouping change was
kept. Source/patch/tests are preserved in `/tmp/elisa-large-function-prototype`;
complete profiles are `native-cost-large-function-{96,128}-*.json` under
`/tmp/elisa-generic-schedule-production-root`. These are checked-payload
experiments, not admitted new normal CLI executables.


### ThinLTO native-stage investigation (not a production mode)

`/tmp/elisa-thinlto-investigation` preserves a standalone LLVM 23.1.3 trial
on the 96 exact baseline native inputs and seven layout replacements from
the checked-payload scheduler profile. No production caches were rekeyed.
The preparation utility assigns a distinct stable source filename per logical
unit/partition before LLVM's required `AssignGUIDPass`, builds a module summary
with `ProfileSummaryInfo`, and writes hashed ThinLTO-ready bitcode. Symbol
resolution chooses prevailing definitions and preserves every externally visible
definition, so dead-stripping the compiler cannot masquerade as a speed gain.
CLI flags explicitly retain O3, default native codegen level 2, apple-m5 CPU,
AA `scoped-noalias-aa,tbaa,basic-aa,globals-aa,scev-aa`, inliner 400 and aggressive
unroll 600; import instruction limit is 400. This is native-only qualification,
not a measured complete rebuild or proof of runtime optimization equivalence.

Direct ThinLTO crashed in `SCEVAAResult::alias` / ScalarEvolution during
jump-threading (`wasm_report_error`, `base.json`). Standard
`thinlto-pre-link<O3>` preparation succeeded for all 103 modules in 6.182 s
(`prelink/prepare.json`), but post-link O3 still crashed during jump-threading
(`run_selected_tests`, `prelink/base.json`). Forcing iterative range evaluation
did not help (`prelink-iterative-ranges/base.json`). A one-backend-thread trial
also crashed after 16.444 s, leaving eight partial native cache entries
(`prelink-serial/base.json`); this rules out backend threading as the explanation
for this reproducer. Every compiler/LLVM command had an external 30 s cap.
There is no successful complete ThinLTO product, executable check or qualifying
cache timing here, and no optimizer analysis was removed from production.

The current native CPU profile instead identifies Go allocation work worth
investigating: 3.65 CPU seconds in malloc out of 11.14 sampled CPU seconds,
including 2.64 s clearing allocator spans, alongside 3.75 s in C calls. Building
the generator's symbol map accounts for only 0.23 s; sharing that map alone
would not explain the remaining rebuild miss. A focused allocation profile completed successfully in 6.913 s using
`/tmp/elisa-native-alloc-profile.py` and the existing frozen checked-payload
profiler, without modifying Stage1 sources. This is a profiling invocation,
not a new normal CLI qualification. The complete-process allocation profile is
`generic-schedule-96/native-allocation.pprof` under
`/tmp/elisa-string-chunk-production-root`; focused flat/cumulative reports are
`native-allocation-{flat,space,objects}.txt`. Backend partition generation accounts
for about 1,483 MiB of allocations. Notable direct allocations are about 240 MiB
for `provenIndexPositions`, 130 MiB for global suffix indexes, 170 MiB cumulatively
constructing generators and their symbol maps, and 70 MiB for `externCParts`.
These read-only indexes are currently rebuilt per generator. The ABI helper
also builds then discards a parameter-plan slice when no view needs splitting.
Reducing those allocations is the next concrete candidate; no gain has yet
been implemented or claimed from this profile.


### Shared read-only codegen indexes and unsplit ABI fast paths

Independent IR generators now share immutable symbol-by-node, unique-suffix and
semantic proof-span indexes for the duration of one `BeginSharedCodegen` build.
Indexes contain no LLVM handles, are never retained across builds, and cannot
authorize IR/native reuse. Synthesized expression/proof overlays remain private
to each generator; span fallback still requires an exact complete source span.
The original sorted symbol-name tie-break is preserved. Ordinary ABI queries
also return before allocating a parameter-plan slice when no C view needs
splitting; explicit bounds plans retain precedence and split C views keep
the same pointer/length order.

Backend regressions (O0/O3, debug/nondebug, globals/aliases, edit/revert, source
shifts and native disk reuse) pass. New tests check simultaneous shared-index
reads and proof-overlay isolation, exact-span lookup, symbol aliases, suffix
ambiguity, zero-allocation unsplit ABI queries and explicit/split C plans.
Targeted tests including fresh-session native builds pass with the Go race
detector. LLVM optimization flags and unit placement are unchanged.

The frozen checked-payload profiler used the admitted `d0c9a710` snapshot with
full invocation/source checks and a new native cache for the changed executable.
Records `native-cost-shared-lookups-96-*.json` and output `shared-lookups-96/` are
under `/tmp/elisa-generic-schedule-production-root`. Enum reorder was 8.285 s,
layout 11.141 s and removal 7.915 s. All 106 full cache entries (exact input
bitcode, target, checksum and native object) match the previous scheduler's
entries byte-for-byte. All four complete objects match the previously linked
and executed normal CLI products (`parity.json`).

A separate warm-cache allocation profile (`/tmp/elisa-shared-lookups-alloc-profile.py`)
completed in 6.711 s with all 96 native entries reused. Compared with the earlier
profile, sampled backend partition allocations fall from 1,483.03 MiB to
967.11 MiB (about 35%). Whole-process sampled allocations fall from
7,065.23 MiB to 6,541.09 MiB; the profile includes frontend restoration too.
Files are `native-allocation.pprof`, `allocation-profile.json`,
`allocation-{flat,space}.txt` in that output directory. Native warm-stage wall
time remains about 1.9 s, so these samples establish an allocation gain, not
a repeatable end-to-end timing gain. The layout case still exceeds ten seconds.
New normal CLI admission/matrix for this executable remain required; the
unsupported declaration/contract and default/runtime-quality gates remain open.


Normal CLI follow-up for `aa4639cc` used a new empty native/semantic namespace
(`/tmp/elisa-shared-lookups-production-root`). Requests 1–8 saved checkpoints
(16.311–27.891 s); request nine hit its 30 s cap after publishing the complete
state; request ten emitted the object in 16.354 s. The standalone matrix was
baseline 0.531 s; enum reorder 9.336 s; new flag/handler 9.431 s; layout
12.614 s; permission 9.181 s; removal 9.326 s; body 8.197 s; signature 9.163 s;
contract 9.008 s; lines 8.195 s. Repeats were 0.421–0.533 s. All ten products
are byte-identical to the previously executed normal products (`object-parity.json`).
The environment still disables server forwarding, and every compiler invocation
had a 30 s external cap. Controlled sources were restored. The allocation gain
is real, but no full rebuild timing gain is established by this normal matrix.


### Read-only generic-parameter query fast paths

All backend callers of `structGenericParams` use the returned list read-only.
Non-generic structs now return nil without allocating an empty one-element
backing array. Complete structured parameter lists are borrowed when no inferred
region needs adding. Small region membership checks use a bounded scan; larger
lists retain the original linear map/synthesis path. Missing-region and legacy
type/region/named-state synthesis retain their original order and independent
storage. No type binding, lowering, unit assignment or optimization flag changes.

Tests cover zero-allocation common queries, missing-region copy isolation and
legacy state cases, plus existing generic/region forwarding, named-argument,
view, nested reference and fresh-session native regressions. Targeted tests
also pass under the race detector. Checked-payload source replay uses the
independently admitted `aa4639cc` state in
`/tmp/elisa-shared-lookups-production-root`, with separate new-executable native
entries and full invocation/source checks. Records
`native-cost-generic-params-96-*.json` measure enum reorder 8.259 s, layout
11.357 s and removal 7.927 s. All 106 complete native entries and all four
merged compiler products are byte-identical to the previous verified products
(`generic-params-96/parity.json`).

The separate warm allocation sample completed in 6.409 s with all 96 native
entries reused (`/tmp/elisa-generic-params-alloc-profile.py`). Direct helper
allocations fell from approximately 83.52 MiB to 27.01 MiB; sampled backend
partition allocations fell from 967.11 MiB to 884.39 MiB. Profiles and focused
reports are under `generic-params-96/`. This demonstrates an allocation gain;
it does not establish a repeatable request-latency gain or meet the layout
target. New normal CLI admission of this executable remains required.

An instrumented, observer-mode normal CLI baseline request for the installed
`aa4639cc` executable took 6.818 s, reused all native entries, and emitted the
exact previously checked object. `normal-base.cpu.pprof` and focused reports in
`/tmp/elisa-shared-lookups-production-root` show 2.88 sampled CPU seconds in
state decoding (2.16 in value filling), alongside substantial allocator system
work. This profile bypassed whole-object replay through the existing observer
flags; server forwarding remained disabled and the request had a 30 s cap.
The candidate parallel-framing design in `/tmp/elisa-parallel-state-plan.txt`
is not implemented or qualified and must be justified by further measurements.


A normal CLI layout observer profile for `aa4639cc` also succeeded in 11.244 s
(`normal-layout-profile.{json,stderr}`, `normal-layout.cpu.pprof` in the same
root). Only the seven exact layout native entries were temporarily moved aside;
the admitted semantic state and all baseline native entries stayed in place.
The request regenerated those seven entries, emitted the previously executed
layout object's exact bytes, and the script restored the original source and
all original native entries/inodes. Native IR+partitions was 5.542 s; the
slowest native unit was 4.440 s. No semantic state was copied or rekeyed.
Observer mode bypasses whole-object replay/publication, so this is not a
replacement for the plain 12.614 s matrix result.

Inspection finds duplicated source loading on an ordinary whole-object miss:
`tryBuildObjectCacheHit` calls `buildCacheObjectArtifactFor`, which loads and
expands source before the pre-server lookup, then drops that loaded program
on a miss. `runCLI` calls `tryBuildObjectCacheWith`, which loads it again.
Observer mode skips both cache wrappers and loads once. Reusing the first
loaded snapshot within the same invocation is a concrete candidate to remove
this repeated work while keeping cache keys/compiler inputs identical. The
measured observer/plain difference does not prove how much of it is caused
by that duplication. `/tmp/elisa-source-handoff-plan.txt` records test and
source-authority requirements; the implementation and focused validation follow below.


## One source snapshot across a whole-object cache miss

The prefix cache lookup now hands its loaded program to the local CLI on a
miss. The loader accepts it only for the exact matching filename. Cache keys
and compilation consume the same request-local source bytes; no global state
or retained server is needed. Cache hits discard the snapshot, observer
requests still bypass the prefix lookup, and invalid input follows the existing
diagnostic path. Compiler, option, source, toolchain and artifact checks remain
unchanged.

Focused cache/semantic-disk tests pass, including a real compiled and linked
fixture whose source changes between lookup and build: it executes the captured
revision, while the changed revision misses that artifact. Other tests cover
one source load, different-filename fallback, observer bypass, and edit/revert
cache authority.

An alternating source/key-only benchmark on the full compiler tree measured
legacy 676.9/683.4 ms versus handoff 629.8/642.8 ms (three operations per sample).
Source loads fell from two to one; allocations fell from about 291.9 MB to
171.6 MB per operation. The benchmark declines compilation and publishes no
native artifacts. This verifies a small source-loading/allocation gain, not a
sub-10-second end-to-end result. The broad normal CLI matrix and new executable
namespace admission are still required. Raw output:
`/tmp/elisa-source-handoff-benchmark.log`.

The four focused handoff tests also pass under the race detector. The broader
race suite reached its externally enforced 30-second cap without reporting a
race; it is not recorded as a pass. The non-race cache/analysis-disk suite passed.


Normal CLI admission of `27b85baa` completed in its own executable-keyed cache,
with the server empty, the unchanged O3 flags and 96 opt-in object partitions.
`/tmp/elisa-source-handoff-production-root` points to all inputs, environment,
logs and hashes. Eight partial analysis checkpoints took 16.167–28.046 s.
Invocation nine reached its external 30-second cap after publishing the complete
state; invocation ten finished the object in 15.666 s. No semantic payload was
copied into this namespace. This does not meet a one-command cold-start target.

The ordinary fresh-process edit matrix succeeded: baseline replay 0.534 s,
enum reorder 9.335 s, enum addition/handler 9.351 s, layout 12.393 s, permission
8.768 s, removal 9.161 s, body 8.136 s, signature 8.997 s, contract 8.743 s and
line insertion 8.126 s. Exact-repeat whole-object hits took 0.421–0.539 s. All ten
objects are byte-identical to the previously checked normal CLI products
(`object-parity.json`). Controlled edits were restored; the seven preexisting
user source changes remain. These results include the generic-parameter
allocation improvement and cannot isolate the snapshot handoff's latency effect.
Layout remains over ten seconds. Unsupported declaration/contract edits and
default/runtime optimization-quality gates remain open.

A restoration-only profile on that admitted payload completed in 4.462 s
(`restore-profile/` under the same root). It used the explicit checked-payload
qualification test, validated the exact source/options, and returned before
native compilation. Restore/scratch overlap was 4.122 s, scratch 2.667 s. CPU
samples include 2.74 s under semantic restore, 2.60 s in object reading and
2.18 s in value filling; reflected string/int writes account for 0.79/0.27 s.
Runtime page reclamation (`madvise`) also accounts for 3.46 sampled CPU seconds
across threads. These measurements justify investigating restoration but do
not establish that parallel decoding alone will close the layout gap.


## Framed restoration qualification

A typed source-position decoder prototype passed identity/malformed-input tests
and improved a dense 10,000-position microbenchmark by about 26%, but alternating
full-source requests did not show a reliable request-time gain. It was removed
from production; its code and evidence are preserved at
`/tmp/elisa-typed-position-prototype` and `typed-position-ab/` under the source
handoff qualification root.

The replacement candidate writes `ELISTATE4`: independent object
frames within a checksummed payload, validated bounded lengths, and a copied immutable string dictionary at
the tail. All canonical allocations and storage aliases are resolved before any
object is filled. Up to six workers fill disjoint non-alias objects with private
scratch and a shared CAS allocation budget. Any error cancels work and returns
no graph. An explicit registry assertion is required for a concurrent field
accessor; legacy `ELISTATE3` remains readable sequentially. Semantic snapshots
use the same field projection and complete schema as before.

The full statecache race suite passes, including framed cyclic/interface/map
identities, named references, hidden-capacity overlapping slices, interior
pointers, alias-body rejection, malformed framing/dictionaries, truncation and
budget failures. Semantic snapshot round-trip/edit/projection tests and the
main cache/analysis-disk suite pass. Every build and compiler invocation used an
external 30-second cap.

An explicit qualification helper restored the admitted source-handoff payload,
validated exact source/options through the native qualification pipeline, and
re-encoded it only to `framed-qualification/framed.raw` outside cache admission.
The raw snapshot grew from approximately 566.9 MB to 624.1 MB. Re-encoding plus
validation took 16.754 s. Alternating fresh-process tests in one frozen executable
measured restore/scratch overlap of 4.148/4.169/4.178 s for legacy versus
2.931/2.873/2.905 s for framed. Full restoration-only requests were legacy
4.482/4.367/4.380 s versus framed 3.342/3.065/3.119 s (the first framed request
includes CPU profiling). The gain is about 1.3 s of overlap; the scratch parser
now finishes last. Raw records and a profile are under `framed-qualification/`
in `/tmp/elisa-source-handoff-production-root`. Native edit equivalence and
normal CLI admission of the new executable remain required. This does not yet
meet the layout or broader major-edit/optimization-quality gates.

Native qualification of the framed payload succeeded with unchanged O3/96-unit
settings: base (new native namespace) 20.047 s, unchanged/native disk reuse
5.290 s, enum edit 6.906 s, layout 9.986 s and removal 7.039 s. Warm regeneration
requests took 6.263–6.601 s. All four merged objects are byte-identical to the
previously verified normal CLI products (`framed-96/parity.json` in the same
root). Controlled sources were restored. This is explicit payload qualification,
not new compiler namespace admission; the normal CLI layout result remains
unproven for this change.

A bounded four-worker alternative did not establish another meaningful gain:
six-worker overlap 2.861/2.990/2.888 s versus four 2.831/2.872/2.962 s. The
implementation retains six. Both use the same payload and options;
`framed-qualification/workers-*.json` records complete process timings, including
a slower first launch of the newly built four-worker executable. Additional
tests cover concurrent allocation-budget underflow/overflow, dictionary byte
ownership/checksums, and the explicit custom-field-accessor parallel assertion.


Normal CLI admission of `c8c14c8b` completed in its own fresh namespace
(`/tmp/elisa-framed-production-root`). Eight partial checkpoints took
15.860–27.976 s; invocation nine reached its external 30-second cap after
publishing the complete state. Invocation ten finished in 15.634 s. The server
was disabled and all O3/96-unit settings were unchanged.

The ordinary matrix succeeded: baseline whole-object replay 0.531 s, enum reorder
8.230 s, enum addition/handler 8.347 s, layout 11.368 s, permission 7.442 s,
removal 7.791 s, body 6.840 s, signature 7.805 s, contract 7.545 s and line
insertion 6.909 s. Exact-repeat hits took 0.418–0.456 s. All ten products match
the previously verified objects byte for byte (`object-parity.json`); the
installed executable matched the frozen admitted binary. Controlled sources
were restored and the seven preexisting user changes remain. Layout improves
from 12.393 s to 11.368 s but still misses the requested limit.

A concrete next candidate is diagnostic-path key preparation. The whole-object
miss computes its key twice, and both `startAnalysisDiskRestoration` and
`analyzeSemanticFromDisk` compute an analysis key containing
`diagnosticPathInputs`. That helper tokenizes the entire expanded input whenever
any `#line` appears, just to discover bare diagnostic filenames. A conservative
negative prefilter could skip these lexer passes only when no physical
column-zero candidate can name a bare path; otherwise it would retain the
existing full lexer. Its grammar/physical-position invariant and file-existence
invalidation need differential validation before changing authority. The plan
is `/tmp/elisa-diagnostic-key-prefilter-plan.txt`; no prefilter is implemented
or qualified yet. Broader unsupported declaration/contract and default/runtime
optimization-quality gates remain open.


## Diagnostic path-key negative prefilter

The candidate now scans only physical column-zero `#line` candidates. If every
candidate contains `/` after `#line`, no accepted directive can introduce a bare
filename: the prefix accepted by the lexer consists only of the keyword,
whitespace and decimal digits. Those slashed filenames are already excluded by
the diagnostic path policy. Any slash-free candidate retains the original full
lexer, including malformed candidates and those inside block comments; initial
request filenames are always handled. No filename is extracted by the prefilter.

A differential oracle retains the previous full-lexer implementation. All pairs
of syntax/comment/string/directive fragments and 5,000 deterministic random
combinations return identical path states for both absolute and bare request
names. Bare-attribution file creation/removal remains tracked. The main cache
and semantic-disk suites pass. Native response path-existence tests pass in an
isolated process; in the broader suite they are skipped because earlier tests
change the default compiler configuration.

On the full expanded compiler source, three-operation samples measured diagnostic
path collection at 278.891 ms / 25.443 MB / 220,729 allocations with the previous
lexer versus 4.123 ms / zero allocations with the prefilter. Path states were
compared before timing. The source/key-only handoff benchmark took
75.790/75.531 ms versus the earlier approximately 630/643 ms. No native artifact
is produced by that benchmark. Raw output: `/tmp/elisa-path-prefilter-bench.log`.
This is cache-key preparation evidence, not an end-to-end rebuild result. Fresh
normal CLI namespace admission and the edit matrix remain required.

Focused race checks pass, including all four isolated native response
file-existence cases. An instrumented fuzz build initially hit its 30-second
external cap before executing tests; the warm-build retry completed and fuzzed
42,238 mutations in five seconds with two workers, with no differential failure
(`/tmp/elisa-path-prefilter-fuzz-retry.log`). The capped first attempt is not
counted as a fuzz pass.


Normal CLI admission of `a2228b09` completed in its own fresh namespace
(`/tmp/elisa-path-prefilter-production-root`). Eight partial checkpoints took
14.685–26.422 s. Invocation nine reached its external 30-second cap after
publishing the complete state; invocation ten finished in 12.337 s. The server
was empty and the O3/96-unit settings were unchanged.

Ordinary fresh-process results: baseline whole-object replay 0.160 s; enum
reorder 6.722 s; enum addition/handler 7.248 s; layout 10.112 s; permission
6.367 s; removal 6.616 s; body 5.645 s; signature 6.585 s; contract 6.515 s;
line insertion 5.625 s. Exact repeats took 0.146–0.161 s. All ten objects match
the previous verified products byte for byte (`object-parity.json`). Controlled
sources were restored, preserving the seven user changes. Layout improves
from 11.368 s but remains a miss; a 0.112 s miss is not relabeled as success.
The cold start and broader unsupported declaration/contract/default/runtime
optimization-quality gates also remain open.

The seven exact layout bitcodes, target configurations and native products were
then copied only to `layout-native-experiment/` under that root for isolated
LLVM measurements; cache entries were neither altered nor rekeyed. Standalone
workers at concurrency seven took 4.002/4.279 s versus 4.721/4.920 s at four
(largest inputs first). All 28 objects matched the stored native products
exactly. Lower native concurrency is rejected by this evidence. The helper is
`/tmp/elisa-layout-native-concurrency.py`.

For the critical 1,248,384-byte input, separate `opt` and `llc` invocations with
the exact O3/AA/inliner-400/unroll-600 and CPU/native-codegen settings produced
an object byte-identical to the stored native product. O3 optimization took
1.290 s; machine code generation took 2.754 s. The machine pass report attributes
1.233 s to AArch64 instruction selection, 0.439 s to greedy register allocation
and 0.316 s to scheduling. Reports and `phase-profile.json` are in that experiment
directory; this is an isolated phase profile, not an end-to-end request timing.

An additional LLVM `mergefunc` pass is only an unadmitted experiment. The critical
unit's object shrank from 714,144 to 651,208 bytes; separate merge plus native
emission took 0.113 + 2.538 s. Alternating seven-worker native batches took
3.986/4.079 s without it versus 3.887/3.949 s with it. Total native bytes fell
from 3,528,800 to 3,363,656 (about 4.7%). The external defined symbol names are
preserved in all 14 candidate checks, but all seven object byte streams change.
This does not prove runtime, function-address, ABI or full-request equivalence.
No production pipeline was changed. `merge-trial-*.json`,
`merged-symbol-parity.json`, the pass profiles and
`/tmp/elisa-layout-native-merge.py` record the experiment. Any promotion needs
those correctness/quality checks plus ordinary CLI admission and novel edits;
the isolated approximately 0.1 s difference cannot establish the goal.


## O3 equivalent-function merging qualification

The candidate appends LLVM `mergefunc` after the unchanged default O3 pipeline.
Worker emission and native cache configuration use the same pipeline helper.
O0/O1/O2, native codegen level, AA stack, inliner 400 and unroll 600 are unchanged.
Serial optimization first runs the base pipeline and collects vectorization
performance diagnostics. Modules with such warnings are left unmerged, because
merging equivalent loops could drop a second source-position marker and lose a
warning on optimized-module cache replay. A regression checks two distinct loop
warnings before and after replay, including warning-to-error enforcement.

New linked C tests check distinct addresses of identical exported functions,
scalar and aggregate ABI values, indirect calls, split view arguments to a C
callback and arena cleanup. They pass for serial, split, independent-IR and
debug variants, including a focused race run. Existing C ABI/view tests, inline
attributes, debug information, exported globals, AA/inliner/unroll parity and
cross-process O2/O3/debug/line/body/local-name cache edit/revert tests pass. The
main cache and semantic-disk suite passes. An initial source-file-relative test
was run from the wrong cwd; the recorded passing suite uses the backend source
directory. Every build/native/LLVM/link invocation has an external 30-second cap.

Checked-payload qualification uses the exact admitted source/options with no
production state rekeying (`/tmp/elisa-merge-functions-native-probe.py`). Results
under `merge-functions-96/` in `/tmp/elisa-path-prefilter-production-root`:
native population 19.468 s; unchanged/native reuse 5.083 s; enum edit 7.016 s;
layout 9.822 s; removal 6.541 s. Warm edit requests took 5.756–7.017 s. These
include profiling and are not ordinary CLI timings. The native-only matched
experiment in the preceding section establishes the small latency/codesize gain;
no larger end-to-end gain is inferred from unmatched historical timings.

All four resulting objects (baseline, enum, layout, removal) were linked into
real compiler executables. Each compiled the included fixture on a sidecar miss
and valid second read; linked fixture executables returned 42. C constructor
probes verified enum tags (including reordered Output/Filter), and unknown-flag
diagnostics remained correct. Results are `merge-functions-96/functional.json`.
Object bytes differ as expected from deduplication; this functional validation
does not relabel them as byte-identical to the previous pipeline. New ordinary
CLI admission, its novel-edit matrix and broader major/default/runtime-quality
gates remain required.


Normal CLI admission of `add8285c` completed in a fresh executable-keyed cache
(`/tmp/elisa-merge-functions-production-root`), with the server empty and the
same O3/96-unit settings. Eight partial checkpoints took 14.819–26.736 s.
Invocation nine reached its 30-second external cap after publishing the complete
state; invocation ten finished in 12.934 s. No state was copied or rekeyed.

The ordinary matrix succeeded: baseline replay 0.250 s; enum reorder 7.073 s;
enum addition/handler 6.902 s; layout 9.834 s; permission 6.202 s; removal
6.620 s; body 5.572 s; signature 6.576 s; contract 6.387 s; line insertion
5.592 s. Exact repeats took 0.140–0.167 s. Layout improves from 10.112 s, but
the 0.166 s margin is thin. The four overlapping normal products exactly match
the functionally validated qualified products (`qualification-parity.json`).
All ten normal products then linked and compiled the included fixture twice,
with sidecar miss/valid behavior and executable exit 42; enum constructor probes
and unknown-flag diagnostics pass (`functional.json`). This proves this matrix,
not all major edits or the default/runtime optimization-quality gates.

The seven-unit native size reduction is not a claim about whole-object size.
The baseline relocatable compiler object grows from 13,809,632 to 13,919,192
bytes (about 0.8%). Linking both with the same hooks/options and dead stripping
reduces the executable from 10,981,368 to 10,929,632 bytes (about 0.47%); text
falls from 9,251,556 to 9,238,884 bytes. Raw sections and `size-comparison.json`
are in the same root.

Additional runtime/race cases exercise non-inlined signed/unsigned generic
instances and post-O3 native splitting, including debug mode; no serial
fallback is accepted. These pass. A temporary negative-control binary built
with the warning guard removed fails the warning-replay regression: only one
of the two source warnings survives on cache replay. Its source modification
was restored before the ordinary matrix; no such binary was installed. Logs
are `/tmp/elisa-merge-postopt.log`, `/tmp/elisa-merge-postopt-race.log` and
`/tmp/elisa-merge-warning-negative.log`.

### Newly measured major-edit gap: enum storage width

Changing only `const enum CliArgumentKind of u8` to `of u16` in the admitted
real compiler took 17.810 s and returned status 1 with no object: it saved an
initial-analysis checkpoint at 2,271/14,422 declarations instead of replaying
the edit. This is a failure of the requested rebuild behavior, not a successful
sub-ten-second compile. `enum-storage-width.json` and its stdout/stderr record
the normal request. The source was restored, and the full admitted baseline
state's SHA-256 remained unchanged.

A separate observer request (one-declaration bootstrap budget, not a performance
measurement) confirms the retained fallback at `elisac_cli.elisa:105:1`, restores
the width-specific partial checkpoint and advances it by one declaration. It
returns status 1 in 11.712 s, preserves the baseline state and restores source.
The conservative first-line equality guard in `retainedConstEnumEditAllowed`
rejects changed storage. Supporting this requires complete width-dependent ABI,
layout, bounds/proof, member and transitive caller invalidation, plus cold IR and
diagnostic checks; simply dropping the guard is unqualified. Scripts:
`/tmp/elisa-merge-functions-enum-width.py` and the corresponding `-debug.py`.
The plan is `/tmp/elisa-const-enum-storage-next-plan.txt`. This gap, other
unsupported declaration/contract forms and default/runtime-quality gates keep
the full goal active.


## Primitive enum storage replay and renewed major-edit matrix

`d9ad97eb` permits explicit primitive integer storage changes for implicit-member
constant enums, preserving live type identity while rebuilding storage/members.
Plain aliases and passive containing records/enum payloads remain eligible;
layout targets and their names include containing types and aliases, so layout
queries are invalidated even when their operand has no recorded expression type.
Foreign/open/inferred storage and declaration computations retain their guards.

The retained semantic suite passes, including disk round trips, signedness,
widening/reversion, containing/nested layout queries and cold diagnostic parity.
The backend disk test compares complete LLVM IR with fresh analysis and links
both products to native layout/aggregate-call checks at u16/u64/u8 widths. Enum
fields are not accepted by the existing C export rules; the test uses internal
aggregate calls and exported scalar probes, not an unsupported C aggregate ABI.

Fresh ordinary CLI admission used the installed binary in its own namespace:
`/tmp/elisa-enum-storage-production-root`. No payload was copied or rekeyed.
Eight partial checkpoints completed in 14.984–26.273 s, request nine reached
its external 30 s cap after publishing complete state, and request ten emitted
an object in 13.282 s. Server is empty; O3/96 independent IR units are unchanged.
Scripts are `/tmp/elisa-enum-storage-production-{bootstrap,matrix,functional}.py`.

The new width edits succeeded: u16 7.311 s, u64 7.122 s, i16 6.944 s, replacing
the previous 17.810 s unsuccessful u16 fallback. Exact repeats were 0.143–0.162 s.
The remaining matrix: baseline 5.469 s, reorder 7.213 s, add/handler 7.131 s,
layout 10.272 s, permission 6.406 s, remove 6.895 s, body 5.732 s, signature
6.762 s, contract 6.612 s, lines 6.271 s. The layout request exceeds ten seconds;
the earlier thin margin was not robust. All thirteen products linked and compiled
the include fixture twice (miss then valid), with linked fixture exit 42, enum
tag constructor checks using each storage ABI, and correct unknown diagnostics.
The thirteen records are in `functional.json` in this root.

Additional major cases (`/tmp/elisa-enum-storage-major-matrix.py`) found generic
body 9.696 s and private constant bound 9.799 s successful, but contract-used
constant 18.318 s and region-parameterized record field addition 18.026 s both
returned status 1 with no object, saving initial-analysis checkpoints. These
are failures, not sub-ten-second compilations. Controlled source files were
restored and the seven preexisting user edits remain intact. The full goal is
still open: layout margin/native work, declaration/contract fallbacks, broader
major edits and ordinary/default/runtime-quality qualification remain.


### Contract-bound constant replay candidate

Typed constant value edits now admit references from value `requires`/`ensure`
clauses when the remaining header has no such dependency. Defaults, array
extents, other declarations and other contract forms retain their guards. The
update rechecks every affected contract function and the transitive caller
closure. Dependency name facts are reused only for byte-identical nonempty
function spans; changed/new syntax is scanned. Header-only checks no longer
format entire function bodies merely to inspect the header. The same guarded
name facts accelerate ordinary const-user selection.

The retained semantic suite passes: strict pre/postcondition proof retraction,
reversion and repeated disk round trips; edited caller syntax with/without the
saved name index; and rejection of array/default/global declaration dependencies.
Backend tests compare cold and replay LLVM IR, including debug assertions.
The enum storage/native tests still pass.

Checked-payload qualification (not ordinary production admission) used the
snapshot under `/tmp/elisa-enum-storage-production-root`, validated through the
normal source/options path. `/tmp/elisa-const-contract-native-qualification.py`
keeps all outputs in `const-contract-qualification/`; no payload was rekeyed or
published into production. Baseline native population took 19.927 s and its
object is byte-identical to the admitted normal baseline. The contract-bound
constant edit then succeeded in 6.536 s: eleven functions invalidated, one native
unit emitted and 95 reused. A repeat took 5.906 s with all 96 native units reused.
The baseline state hash stayed unchanged and source was restored. The prototype
objects both linked into compiler executables, compiled the include fixture on
miss/valid cache reads and produced fixture exit 42; enum and diagnostics probes
passed (`const-contract-qualification/functional.json`). All toolchain processes
were externally capped at 30 s. Ordinary CLI admission/rebuild timing remains
required before counting this candidate as a normal sub-ten-second result.


### Ordinary CLI confirmation of contract-bound replay

`d5231e9b` was admitted normally in a new executable-keyed namespace at
`/tmp/elisa-const-contract-production-root`; no snapshot was copied or rekeyed.
Eight checkpoints took 14.642–26.451 s, invocation nine hit its external 30 s cap
with complete state saved, and ten emitted the object in 12.397 s. The server
setting remains empty and O3/96 independent IR units remain explicit.

The contract-bound constant edit succeeds normally in 5.928 s (previously
18.318 s with no object), and its object is byte-identical to the checked-payload
qualification product. Generic-body and private-bound edits take 9.903 and
9.927 s, leaving almost no margin. The renewed matrix: baseline 5.108 s; storage
u16 6.996 s, u64 6.971 s, i16 6.997 s; enum reorder 7.118 s; addition/handler
7.334 s; layout 10.155 s; permission 6.393 s; removal 6.808 s; body 5.592 s;
signature 6.651 s; contract 6.606 s; lines 5.738 s. Layout remains over ten seconds.
All sixteen normal products pass linked compiler/include-fixture tests, miss and
valid cache reads, executable exit 42, enum tag and unknown-diagnostic probes
(`functional.json`). Thirty overlapping normal/repeat objects match earlier
products exactly (`previous-object-parity.json`). The region-record fallback,
layout/native margin and ordinary/default/runtime quality gates remain open.

### Isolated post-O3 native fragment experiment

This is an experiment outside production, not an implemented cache path. Root:
`/tmp/elisa-native-fragment-probe-root`; source/tool scripts are
`/tmp/elisa-native-fragment-probe.cpp`, `-benchmark.py`, `-reconstruct.py` and
`/tmp/elisa-native-fragment-functional.py`. It reads checked native cache entries,
validates entry sizes/checksums and exact target settings, and optimizes their
complete unit with unchanged O3 + mergefunc, AA, inliner 400 and unroll 600.
The standalone opt/llc path reproduces the admitted unit-80 object byte for byte
on all three repetitions, confirming these settings for this input.

The critical layout unit has 303 weak ODR definitions. Existing selective clone
ownership intentionally duplicates such definitions, so the prototype assigns
weak/linkonce ODR functions to one fragment too. Its private copy of
`parallel_emit.c` turns omitted weak definitions into ordinary external
DECLARATIONS; retained definitions preserve weak linkage. Without this correction
LLVM verification rejects the fragments. No production clone/worker code changed.
The module has no aliases/ifuncs/assembly and passes the existing clone guard.

Four fragments of the fully optimized baseline and layout unit share three
byte-identical fragment inputs. The changed fragment's native emission takes
0.783–0.832 s versus 1.970–2.073 s for the whole optimized unit. This excludes O3,
cache lookup, cloning/serialization, merging and concurrency effects, so it is
not a claimed end-to-end saving. Records are `inputs.json`, `fragments.json` and
`native-timings.json`. All LLVM/compiler/linker commands have external 30 s caps.

Before substitution, reconstructing all 96 baseline and layout native objects
reproduces their normal full compiler objects byte for byte. Substituting only
the critical layout unit's four fragments then links into a working compiler:
include fixture compiles on cache miss and valid repeat, linked fixture exits 42,
and enum/diagnostic probes pass (`functional.json`). With the same source and
link flags the normal layout executable is 10,929,632 bytes with 9,239,268 text
bytes; the fragment candidate is 10,929,336 bytes with 9,235,864 text bytes.
This narrow check does not establish broader ABI/debug/runtime quality.
A real worker/cache implementation and ordinary CLI qualification are still
needed before counting the fragment experiment as a rebuild gain.


### Integrated post-O3 native fragment worker (explicit qualification)

The independent-IR optimizer worker now has an opt-in native fragment cache
(`ELISACORE_NATIVE_FRAGMENTS=1`). It runs the unchanged complete-unit O3 pipeline
in its isolated process, then clones four native fragments using the existing
verified selective-clone machinery. Strong/local and weak/linkonce ODR function
definitions have stable owners. Omitted definitions become external declarations;
retained weak definitions retain their linkage. The ordinary owner policy remains
unchanged. Aliases, ifuncs, module/inline assembly, block addresses, COMDAT groups
and appending registrations conservatively use whole-unit native emission.

Fragment reuse compares complete current optimized fragment bitcode and target
configuration in the existing checked disk-entry format. Namespaces derive from
the logical source unit and parent IR partition, not input content. Compiler,
pipeline, native codegen and resolved target settings remain key inputs. Since
unit output now includes a partial link, its enclosing key also includes merger
argv, canonical executable path and executable content digest. The existing
whole-unit cache still handles exact input repeats. Cache-disabled builds retain
the original worker. Cold fragment emission is serial inside each worker, avoiding
a fourfold increase in worker concurrency. No server or retained worker is used.

Cross-process tests pass for body edit, unchanged repeat, revert and corrupt
fragment recovery. Merger argument/content invalidation tests pass. Linked tests
check function addresses, scalar/aggregate ABI, generic identities, C callbacks,
arenas, alias/global identity and debug variants. C++ boundary fixtures check
aliases, ctor/dtor arrays, assembly, block addresses and COMDAT rejection. Existing
preoptimized cross-process O2/O3/debug/edit tests and uncached-input tests pass.
Logs: `/tmp/elisa-native-fragments-{cross-process,abi,final-tests}.log`.

Final checked-payload qualification uses the admitted source/options under
`/tmp/elisa-const-contract-production-root`, with all candidate outputs restricted
to `native-fragments-final-qualification/`. No state was copied/rekeyed into a
production namespace. Script `/tmp/elisa-native-fragments-final-qualification.py`
uses a frozen candidate test binary. Initial native population takes 21.361 s;
layout succeeds in 8.337 s, generic body 7.585 s, private bound 7.738 s. Each edit
emits seven native fragments and reuses twenty-one inside seven changed units;
89 whole units also reuse their native objects. No fragment fallback occurs.
The baseline state hash stays unchanged and controlled source is restored.

All four candidate products link into compiler executables and compile the include
fixture on cache miss and valid repeat; linked fixture exit is 42, enum-tag and
unknown-diagnostic probes pass (`functional.json`). Every compiler/build/LLVM/link
invocation is externally capped at 30 s. These explicit qualification timings are
not relabeled as ordinary CLI measurements. Fresh ordinary admission, renewed
novel edit matrix, region-record fallback and default/runtime-quality gates remain
required. The cache path remains explicitly gated pending that qualification.


### Ordinary CLI admission of native fragment reuse

`034fb474` was admitted in a fresh executable-keyed namespace selected by
`/tmp/elisa-native-fragments-production-root`, with the server empty and fragment
reuse explicitly enabled. No payload was copied or rekeyed. Eight checkpoints
took 14.922–26.352 s, invocation nine reached its external 30 s cap after complete
state publication, and ten emitted an object in 14.011 s. Setup/population times
are not relabeled as incremental rebuild results.

The ordinary CLI matrix succeeds below ten seconds: baseline 5.139 s; generic
body 7.735 s; private bound 7.913 s; contract-bound constant 5.963 s; storage u16
7.102 s, u64 7.273 s, i16 6.913 s; enum reorder 6.998 s; add/handler 7.122 s;
layout 8.419 s; permission 6.605 s; removal 6.826 s; body 5.697 s; signature
6.788 s; contract 6.694 s; lines 5.699 s. Exact repeats take 0.149–0.169 s.
The three former native-cost cases now have more than two seconds of headroom.
All sixteen normal products link and compile the include fixture on miss/valid
cache reads; linked fixture exits 42, enum tag and diagnostic probes pass
(`functional.json`). Scripts are `/tmp/elisa-native-fragments-{major-matrix,
production-matrix,production-functional}.py`. All toolchain commands have
external 30 s caps. This proves the matrix, not all major edits or default quality.

### Region-parameterized record field replay candidate

A paired record edit can retain its explicit region binders when their names/order
and generic-region protocol remain unchanged. Type/value/state/permission generic
binders and the existing complex declaration features remain guarded. Existing
scratch declaration comparison remains mandatory. The live field table is rebuilt,
type memos cleared and dependent bodies rechecked. Layout invalidation now follows
by-value GenericInstance bases and adds containing/alias names, covering layout
queries whose operand has no expression-type record.

The retained semantic suite passes. Disk round trips check adding/narrowing/removing
fields, reversion, nested region records and borrow-bearing element retyping that
retracts a scalar getter's validity. Region binder rename/removal and added type
binder changes still fall back. Concrete backend tests instantiate region arguments,
compare full replay/cold LLVM IR in normal and debug mode, then link and execute
record construction/forwarding at three shapes (exit 17). Logs are
`/tmp/elisa-region-struct-{semantic-all,semantic-final,backend}.log`.

Checked-payload qualification (`/tmp/elisa-region-record-qualification.py`) uses the
admitted native-fragment baseline with normal source/options validation, outputs
only into `region-record-qualification/` under that root, and leaves the baseline
state hash unchanged. Initial native population takes 21.716 s. Adding a u64 field
to `IncrementalSessionIdentityKey[@r]` succeeds in 9.195 s, replacing the earlier
18.026 s unsuccessful fallback. It rechecks two functions, emits thirteen fragments,
reuses fifteen fragments within seven changed units, and reuses 89 whole units.
Both candidate compiler products pass the native include fixture, miss/valid cache
reads, exit 42, enum and diagnostic probes (`functional.json`). Controlled source
was restored. This remains explicit qualification; fresh ordinary admission and
novel edit timing for the region-record implementation are still needed. Other
major forms and default/runtime-quality gates keep the full goal open.


### Ordinary region-record replay and two further declaration gaps

`6848302c` was admitted normally in its own fresh namespace selected by
`/tmp/elisa-region-record-production-root`; no payload was copied or rekeyed.
Eight checkpoints took 15.247–26.366 s. Invocation nine hit its external 30 s cap
with complete state saved; ten emitted the initial object in 14.179 s. Server is
empty, O3/96 independent IR units and native fragments remain explicit.
The region-record field addition succeeds through the ordinary CLI in 9.211 s,
with object bytes equal to the qualified candidate. Generic body takes 7.542 s,
private bound 8.001 s, contract-bound constant 5.883 s. The remaining matrix:
baseline 5.227 s; u16 storage 6.656 s, u64 6.832 s, i16 6.837 s; enum reorder
6.886 s, add/handler 7.579 s; layout 8.901 s; permission 6.455 s; removal
6.731 s; body 5.639 s; signature 6.720 s; contract 6.471 s; lines 5.778 s.
All seventeen normal products pass linked compiler/include-fixture exit 42,
cache miss/valid, enum tag and diagnostic probes (`functional.json`).

The novel matrix (`/tmp/elisa-region-record-novel-matrix.py`) changes the first
CLI enum tag to explicit 64, or changes IncrementalCacheRead to layout(c).
Both return status 1 with no object, saving initial-analysis checkpoints:
17.606 s for explicit tags and 17.738 s for C layout. Sources are restored.
These are rebuild failures, not passing timings; the C-layout gap remains open.

### Closed explicit enum value replay candidate

Explicit enum values derived entirely from literals, unary/binary operations,
parentheses and conditional expressions can be replayed without consulting a
changed declaration environment. Eligibility checks their AST dependencies
iteratively; actual evaluation, integer-kind and storage/bounds checks still use
the ordinary scratch declaration phase. Names, calls, reflected layouts and cast
type bindings remain guarded. Existing static/global/header-member dependency
guards remain in force. The live member/constant tables are rebuilt and enum users
rechecked using the existing path.

The retained suite passes, including disk round trips, computed explicit values,
implicit following ordinals, strict proof retraction/reversion and external
name/call/layout dependency rejection. Backend tests compare complete normal/debug
cold and replay LLVM IR and execute native probes for tags above 255, negative
tags, arithmetic tags, dispatch and reversion. Logs are
`/tmp/elisa-explicit-enum-{semantic,semantic-all,backend}.log`. The existing
storage/header diagnostic test now explicitly requires successful replay and cold
diagnostic parity for both requires-size and default-size cases; it cannot pass
by silently ignoring an Update error (`/tmp/elisa-enum-header-path.log`).

Checked-payload qualification uses the admitted source/options from the region
root and writes only into `explicit-enum-qualification/`, outside production
admission (`/tmp/elisa-explicit-enum-qualification.py`). Initial native population
takes 21.794 s; the real explicit-tag edit then succeeds in 7.071 s, replacing
the 17.606 s unsuccessful fallback. Baseline state hash stays unchanged and
source is restored. Both resulting compiler executables compile the include
fixture on miss/valid cache reads and produce fixture exit 42. C constructor
probes verify shifted Output=64, Filter=65, Unknown=100 tags; diagnostics pass
(`functional.json`). All toolchain invocations have external 30 s caps. Fresh
ordinary admission and timing for this candidate are still needed. C-layout,
other major forms and default/runtime-quality gates keep the full goal open.


### Ordinary explicit-tag confirmation and C-layout replay candidate

`2232cbbd` was admitted in its own fresh namespace selected by
`/tmp/elisa-explicit-enum-production-root`; no state was copied or rekeyed.
Eight checkpoints took 14.682–26.529 s, invocation nine reached its external
30 s cap with complete state saved, and ten emitted an initial object in 13.806 s.
The ordinary explicit-tag edit succeeds in 7.114 s and matches the qualified
object bytes. Region record takes 9.381 s, generic body 7.947 s, private bound
7.752 s and contract-bound constant 6.087 s. Remaining cases: baseline 5.048 s;
u16/u64/i16 storage 6.928/6.802/6.784 s; reorder 7.222 s; addition/handler
6.944 s; layout 8.386 s; permission 6.499 s; removal 6.783 s; body 5.685 s;
signature 6.754 s; contract 6.783 s; lines 6.248 s. All eighteen normal products
pass linked compiler/include-fixture exit 42, cache miss/valid, enum tags and
diagnostic probes (`functional.json`). The server remains empty; all toolchain
invocations use external 30 s caps. These timings prove this matrix, not all edits.

Paired simple records can now replay ordinary/C layout changes and field edits
while preserving their generic-region protocol. Existing complex-layout,
annotation, affinity/state and other-generic guards remain. The live record's
ReprC/Layout/PackedLayout flags are updated along with its declaration/fields and
memos. Semantic CParamPlan is split-view argument ordering; aggregate memory-class
classification belongs to each fresh backend generator and reads current types.
Tests exercise C exports and calls across the 16-to-24-byte threshold and revert,
including signed large field values, aliases, containing-record layout queries
and full cold/replay normal/debug LLVM IR parity. Both cold and replay native
products pass. The retained semantic suite also passes mode transition/reversion,
field edits and alias/containing-query invalidation.

The query test found that a parameterless layout builtin can lack an expression
type linking it to the record. Struct-user selection now also checks its paired
source spans, as enum selection already did. Literal-name rejection avoids the
regexp's whole-span boundary scan on misses, and unchanged spans are scanned once.
The enum path uses the same prefilter. A differential test checks original regexp
parity across identifier, punctuation, quoted and Unicode fragment combinations.
Logs: `/tmp/elisa-c-layout-{semantic-final,backend-final}.log`.

An exploratory top-level sizeof const initializer was rejected by cold analysis;
it is not used as evidence for supported constant replay. No implementation claim
rests on comparing absent constant-table entries.

Checked-payload qualification reads the admitted source/options and writes only
into `c-layout-final-qualification/` under that root, outside production admission
(`/tmp/elisa-c-layout-final-qualification.py`). Initial native population takes
22.563 s; changing IncrementalCacheRead to layout(c) succeeds in 6.279 s, replacing
the prior 17.738 s unsuccessful fallback. The update rechecks two functions and
reuses all 96 native units; this program's metadata change leaves object bytes
identical. The separate C ABI field tests cover changes that alter actual native
calling layout. An earlier candidate with unfiltered source regexp scans took
7.245 s (diff phase 1.393 s); the final diff phase is 0.522 s with the same output.
Baseline state hash is unchanged and controlled source restored. Both candidate
compilers pass include fixture exit 42, miss/valid cache behavior, enum tags and
diagnostics (`functional.json`). Fresh ordinary admission/rebuild timing for
this candidate and broader major/default/runtime-quality gates remain open.


### C-layout ordinary CLI confirmation and leading annotation correction (2026-10-24)

Fresh installed `98e3eb25` binary, disk cache namespace, no retained server:
root `/tmp/elisa-c-layout-production-root` points to
`elisa-c-layout-production-8uptgfnn`. Eight bounded admission checkpoints,
step 9 timed out after publishing a full state, step 10 emitted the object in
15.673 seconds. This is setup, not a sub-10-second cold compile claim.
All 19 ordinary CLI variants emitted objects below ten seconds: major cases
(generic/private/contract/region/explicit enum/C-layout) were
8.291 / 8.756 / 6.289 / 9.524 / 6.784 / 8.773 seconds. Remaining matrix ranged
5.361–9.554 seconds. Every exact repeat succeeded in 0.150–0.206 seconds.
Functional native compiler checks are recorded in the root's `functional.json`:
include fixture exits 42, sidecar miss then valid, enum tags, unknown diagnostics.
These measurements remain for explicit independent-IR/native-fragment mode;
default whole-module/runtime-quality admission is still open.

An additional leading `@align(16)` edit appeared to succeed in 5.773 seconds
with identical object bytes. **This is not alignment replay qualification.**
A minimized disk round-trip regression proved that the first declaration's
leading annotation lay outside the compared source span: Update silently
returned the old unaligned StructType after adding `@align(64)`.
`retainedDeclSourceStart` now includes leading annotations for all currently
annotated declaration kinds. This also assigns a later annotation to its own
declaration rather than its predecessor. Unsupported annotation edits now
fall back instead of silently reusing stale metadata. Alignment replay remains
a separate optimization task. The regression covers the first declaration,
a following declaration, and an include-style line directive.
Before: `/tmp/elisa-alignment-span-before.log` fails on stale alignment.
After: `/tmp/elisa-annotation-span-semantic.log` passes targeted retained
annotation, line/shift, region, C-layout and constant-enum tests.


### Alignment replay after annotation-span correction (2026-10-24)

Paired simple-record edits now admit exactly one valid `@align` annotation,
including its addition/removal. Invalid powers, duplicate annotations,
`@fixed_layout`, `@append_only`, C binding metadata, and mixed annotations
remain guarded. The live canonical StructType resets Alignment/HasAlignment
before collecting new annotation metadata, so removal cannot retain old flags.
Existing field, region binder, affinity and layout eligibility checks remain.

`TestRetainedAlignmentDiskRoundTrip` exercises 64→128→default→16→default
through serialized states, first and following declarations, aliases and inline
containers. Metadata and diagnostics match cold analysis with identity preserved.
`TestRetainedAlignmentDiskStorage` compares complete normal/debug IR against
cold compilation and links/runs both cold and replay O3 products with a C observer
checking stack/global pointer alignment and stored values. This qualifies the
existing requested-storage-alignment behavior; it does not establish new nested
record/array stride or C by-value ABI semantics for over-aligned records.
Logs `/tmp/elisa-alignment-semantic.log`, `/tmp/elisa-alignment-backend.log`,
and `/tmp/elisa-alignment-retained-suite.log` pass.

Frozen test binary `/tmp/elisa-alignment-main-test` was qualified using the
untouched `98e3eb25` ordinary cache payload (no namespace rewriting). Results in
that root's `alignment-qualification`: baseline 22.521 seconds, actual
`@align(16)` compiler edit **8.841 seconds**. Replay selected two functions,
0.760 seconds total (diff 0.485, bodies 0.011, post 0.261); 90 native units reused,
six rebuilt, six fragments emitted and 18 reused, no fallback. The edited object
SHA is `7fc0789e56d2fd0fbf5c5fd4c91927a7143aaf1a109e70d1f0451f1f26bdd133`,
different from the baseline (unlike the erroneous pre-correction result).
Both products pass native compiler include/cache fixture checks (exit 42,
miss→valid), enum dispatch tags and unknown-option diagnostics. Functional
records are in `alignment-qualification/functional.json`.
Fresh ordinary binary/cache namespace confirmation remains the next gate.


### Alignment ordinary installed-binary confirmation (2026-10-24)

Installed `feed0a00`, fresh namespace and frozen binary at
`/tmp/elisa-alignment-production-root` → `elisa-alignment-production-5wxtkamp`.
No retained server; every compiler/link/native invocation externally bounded at
30 seconds. Eight setup checkpoints were 15.138–27.822 seconds, ninth timed out
at 30.225 after publishing state, tenth produced the object in 15.488 seconds.
This setup cost is separate from incremental rebuild timing.

The ordinary CLI alignment edit succeeded in **9.297 seconds**, exact repeat
0.155, with the exact qualified changed object SHA `7fc0789e…bdd133`.
The major matrix generic/private-bound/contract-bound/region-record/explicit-tags/
C-layout took 8.562 / 8.368 / 6.503 / 9.113 / 6.821 / 6.181 seconds.
Remaining twelve changed variants took 6.035–8.561 seconds; baseline was an exact
cache hit at 0.141 seconds. All exact repeats succeeded at 0.147–0.174 seconds.
All **20 native compiler products** pass include fixture compile/link/run
(exit 42), cache miss→valid, enum dispatch values and unknown diagnostics.
Details: root case JSON files and `functional.json`, runner log
`/tmp/elisa-alignment-production-functional.log`. Stage1 controlled source edits
restored; its seven preexisting user modifications preserved.
These results qualify the explicitly enabled independent-IR/native-fragment mode.
Broader edit coverage and default whole-module/runtime-quality admission remain open.


### Additional gaps and packed direct-access correctness (2026-10-24)

Ordinary `feed0a00` packed-layout edit on IncrementalCacheRead falls back:
17.904 seconds, exit 1, no object, new analysis checkpoint. Replay admission for
packed records remains guarded at this point.

Separately, whole-module mode (PARALLEL_IR=0, PARALLEL_OPT=0,
NATIVE_FRAGMENTS=0) was admitted under its distinct environment identity.
Initial probes merely entered admission (14.428/14.577 seconds), not qualified
rebuilds. Bounded default-bootstrap continued to a second complete analysis
state, then steps 9–13 repeatedly timed out at about 30.13 seconds without an
object. Analysis was about 2.1 seconds on these retries. A separate 30-second
profile invocation sampled at 8 seconds; stacks were inside
elisacoreRunOptimizationPipeline / LLVM Module PassManager, including
CalledValuePropagation and InstCombine. Root files `default-bootstrap-*`,
`default-native-sample.txt`, `default-profile.stderr` record this unresolved
whole-module optimization bottleneck. No optimizer settings were weakened.

The packed-layout investigation reproduced a **cold compiler** error using a
valid mutable-field fixture: `Record layout(packed)` with u8 then mutable i64
emitted field loads/stores at offset 1 with `align 8`.
`alignPackedRecordAccesses` now corrects direct load/store addresses derived
through packed-record GEPs to align 1 once before optimization. Nested GEP paths,
arrays, casts, selects and phi paths are handled; cycles are bounded. Unpacked
field paths keep their original alignment. The scan is skipped entirely when no
packed struct body was emitted. LLVM may strengthen proven alignment later.
This is a direct-access correction, not a new guarantee about escaped scalar
reference contracts or atomic packed fields; neither is qualified by these tests.

Tests assert direct/nested/array IR on arm64 and x86_64, preserve ordinary-field
align 8 in the same module, and run native O0/O3 exported reads/writes against
four deliberately unaligned packed records allocated by a C client. Relevant
C-layout ABI, alignment replay, and explicit-layout regressions pass.
Before: `/tmp/elisa-packed-access-before.log` shows invalid align 8.
After: `/tmp/elisa-packed-record-alignment.log` and
`/tmp/elisa-packed-access-regressions.log` pass. Replay speed work remains next.


### Simple packed-record replay qualification (2026-10-24)

After the direct packed-access correction in `7c649e1d`, the paired simple-record
edit helper admits ordinary/C/packed layouts with the same existing restrictions
on fields, binders, annotations, affinity, typestate and ownership protocols.
`applyStructEdits` now derives PackedLayout from the edited declaration instead
of forcing false. Canonical type identity, field-order reset, containing-type
selection and memo invalidation remain unchanged.

`TestRetainedPackedLayoutModesAndFieldsDiskRoundTrip` covers serialized edits,
field addition/removal and transitions through packed/C/default with metadata
and cold diagnostics parity. The complete retained semantic suite passes
(`/tmp/elisa-packed-record-semantic.log`).
`TestRetainedPackedRecordDiskLayouts` compares complete normal/debug IR and runs
both cold and replay O3 objects through packed→C→default→packed→default.
A C client checks sizeof/align/field offsets and an inline alias/container,
Elisa internal by-value copying, high signed values, and four pointer-accessed
records (deliberately unaligned for packed mode). Physical Pair size changes
13↔24 bytes. This pointer-boundary test does not qualify C by-value packed ABI.
The scalar-only internal copy fixture has no arena allocations and uses the same
empty arena-free hook convention as existing backend tests.
Backend log `/tmp/elisa-packed-record-backend.log` passes.

Frozen `/tmp/elisa-packed-record-main-test`, untouched ordinary `feed0a00` state,
qualification-only output in its root's `packed-record-qualification`:
baseline 22.031 seconds; actual packed compiler-record edit **8.685 seconds**
(previous ordinary guarded fallback 17.904 seconds/exit 1/no object).
Two functions selected, diff 0.440, bodies 0.009, post 0.253, replay 0.705 seconds;
89 native units reused, seven rebuilt, seven fragments emitted and 21 reused,
no fallbacks. The packed object SHA is
`14cd35e3af0765174c9ab0d3c30eb9f336723cc2caaf696a191b47287881140d`.
Both baseline and packed native compiler products pass the include fixture
(exit 42), sidecar miss→valid, enum dispatch and unknown diagnostics
(`packed-record-qualification/functional.json`). No baseline payload changed;
controlled Stage1 edits restored. Fresh ordinary installed-binary confirmation
is next. Default whole-module optimization remains above the 30-second cap.


### Twenty-one ordinary server-free variants confirmed (2026-10-24)

Installed `f880510a`, fresh namespace/frozen binary:
`/tmp/elisa-packed-record-production-root` → `elisa-packed-record-production-2_dwk1k1`.
Eight bounded checkpoints (15.025–27.837 seconds), ninth timeout 30.247 after
state publication, tenth object 14.924. This is admission setup, not a cold
sub-10-second build claim. Every compiler/link/native invocation capped at 30 seconds.
No retained server; exact disk artifact authority preserved.

Ordinary packed edit **8.410 seconds**, repeat 0.149; alignment 8.017, repeat
0.145. Major generic/private-bound/contract-bound/region-record/explicit-values/
C-layout: 7.459 / 7.437 / 5.778 / 8.951 / 6.531 / 5.843 seconds.
Remaining twelve changed cases: 5.934–8.748; baseline exact hit 0.145 seconds.
All 41 timed invocations succeeded (21 variants including baseline, 20 repeats),
with exact repeats 0.141–0.176 seconds. Maximum changed rebuild **8.951 seconds**.
Packed/alignment object SHA values exactly match prior qualification products.
All **21 native compiler products** pass the include fixture (exit 42), sidecar
miss→valid, enum tag checks and unknown-flag diagnostics. Records: root JSON
files and `functional.json`; log `/tmp/elisa-packed-record-production-functional.log`.
Controlled Stage1 source restored, seven preexisting user edits preserved.

This is the explicitly enabled independent-IR/native-fragment configuration,
with unchanged O3, AA, inliner 400, unroll 600 and default native codegen level.
Ordinary whole-module optimization still exceeds the 30-second cap after
analysis; its runtime-quality/default-mode gate remains open. The full user goal
is active despite this passing representative matrix.


### Bounded cross-partition imports: quality and first rebuild gate (2026-10-24)

A new optional diagnostic `TestPartitionInliningQualityProbe` compares a hot
loop calling a leaf in a different native owner with whole-module O3. It checks
the result against a C reference, executes five 50-million-iteration samples,
and rejects a declined independent-IR path. This is runtime quality evidence,
not merely identical exit codes. Final current-code results in
`/tmp/elisa-partition-quality-final`: checksum 3751756410333120 in all modes;
whole-module median 0.015360 seconds, ordinary independent IR 0.022572,
bounded imports 0.015348. Log `/tmp/elisa-partition-quality-final.log` passes.
These short local timings establish this probe, not broad workload parity.

`ELISACORE_IR_IMPORTS=1` now experimentally permits on-demand imports during
owned function-body lowering. Only canonical ordinary FuncDecl/FuncType pairs
are eligible: no static/extern/intrinsic/generic/ghost/lemma/contract functions,
no explicit noinline, at most 64 syntax pointers per function, 1024 per unit,
and three transitive import hops. Weight hints are invocation-local and shared
read-only through a sync.Map. Imported bodies have available_externally linkage;
the original owner remains the only emitted provider. Current complete bitcode,
including imported bodies, remains native cache authority. No optimization,
inliner, AA, unroll or codegen budget is weakened. This remains a gated experiment;
it does not enable default whole-module fast builds or establish all workload quality.

New tests assert foreign import linkage, folding and noinline guards. Across
fresh processes, callee body edits invalidate owner and caller units; repeats
and reverts reuse all four units, in normal and debug modes, with native exit
values checked. An exported function-pointer test compares imported references
from two foreign owners with the C-visible original address and calls them,
with native fragment caching enabled. Aggregate ABI, packed replay, function
addresses and performance-warning cache regressions pass under the new flag.
Log `/tmp/elisa-partition-imports-abi-final.log` passes.

Frozen `/tmp/elisa-ir-imports-main-test` qualifies the flag against the untouched
ordinary `f880510a` snapshot. Output only in its root's `ir-imports-qualification`:
baseline 21.569 seconds; packed edit **8.650 seconds**. Two users selected,
replay 0.804 seconds; 89 units reused, seven rebuilt, seven fragments emitted,
21 reused, no fallback. Baseline object
`64e173d0d20dc28ef96d0cd2e61789f98058605d04e09be36c035b1c74236297`,
packed object `8376731d32c24dd721571e9c8a5f8bcdd7eba455bfeb692149730ae70402b0c8`.
Both native compiler products pass include fixture exit 42, sidecar miss→valid,
enum dispatch and unknown diagnostics (`ir-imports-qualification/functional.json`).
Snapshot hash unchanged; controlled Stage1 source restored. Full ordinary
installed-binary namespace/matrix and broader runtime-quality gates remain next.


### Bounded-import installed-binary confirmation: twenty-two variants (2026-10-24)

Installed `227b8e30`, fresh executable/cache namespace with IR_IMPORTS=1,
PARALLEL_IR=1, PARALLEL_OPT=1 and NATIVE_FRAGMENTS=1, no retained server.
Pointer `/tmp/elisa-ir-imports-production-root` →
`elisa-ir-imports-production-1slyyq7t`. Eight bounded admission checkpoints
(15.210–26.928 seconds), ninth timeout 30.241 after full state publication,
tenth object 14.962. Initial setup is separate from incremental timing.
Every compiler/LLVM/link/native invocation externally capped at 30 seconds.

Packed edit **8.831 seconds**, alignment 8.225. Major generic/private-bound/
contract-bound/region-record/explicit-values/C-layout:
7.267 / 7.413 / 5.788 / 8.936 / 6.542 / 5.815 seconds.
Twelve remaining changed variants took 5.833–8.822; baseline exact hit 0.149.
New `@inline(never)` edit on the capture file's first function completed in
6.689 seconds, repeat 0.170. All **43 timed invocations** (22 variants including
baseline and 21 repeats) succeeded; changed rebuild range **5.788–8.936 seconds**,
exact repeats 0.139–0.175 seconds.

The inline-policy object legitimately matches baseline: the large function was
already not imported/inlined. A separate serialized-state regression verifies
that this is not a lost annotation: `TestRetainedInlinePolicyDiskRoundTrip`
checks never→always→default→never metadata and complete cold/replay O0/O3 IR,
normal and debug. `/tmp/elisa-inline-policy-retained.log` passes.

All **22 native compiler products** pass include fixture compile/link/run
(exit 42), sidecar miss→valid, enum dispatch tags, and unknown-option diagnostics.
Details: case JSONs and `functional.json`; runner log
`/tmp/elisa-ir-imports-production-functional22.log`. Controlled Stage1 changes
restored and seven user modifications preserved.
This confirms the quality-improving bounded import mode stays within the current
rebuild matrix. It remains explicitly gated; the single helper-loop quality
probe does not qualify every workload or justify claiming the full goal complete.
Broader runtime-quality/default-mode admission and further major edits remain open.


### Broader import-mode quality probes (2026-10-24)

`TestPartitionBroaderQualityProbes` now checks three additional workloads against
C references, whole-module O3, independent IR without imports, and bounded
imports. Every independent case asserts that its path was not declined.
The saved source and C driver accompany each result. All three check every
sample's result; the struct case additionally checks the final mutated value
and unchanged bias, and strict-FP checks exact double bits with contraction
disabled on both sides. Mutual recursion uses two different native owners,
both foreign to the exported caller, with a known depth of 20.

The shared diagnostic runner now performs five checked warm-up calls before
five timed calls. On macOS `/usr/bin/time -l` also records instruction/cycle
counts; other platforms run the same correctness/timing probe without that
macOS-specific option. Counters cover the whole process, including warm-ups,
C reference calculation and driver overhead; they are not kernel-only counts.
Initial two-million-element recursion timings were too short for stable CPU
placement despite warm-up, so the final fixture uses fifty million elements.
Do not mix those exploratory short timings with the final data.

Current-code run `/tmp/elisa-broader-quality-final.log` passes. Final medians
in milliseconds (whole-module / independent / imports):

| workload | whole O3 | no imports | bounded imports |
|---|---:|---:|---:|
| struct alias read/write, 30M | 3.986 | 13.912 | 3.963 |
| strict floating-point, 20M | 10.661 | 13.889 | 10.728 |
| mutual recursion, 50M | 9.431 | 344.108 | 9.390 |

Whole-process retired instructions (millions, same column order):
struct 753.417 / 3378.490 / 753.177;
strict-FP 1880.802 / 3180.870 / 1880.690;
recursion 2486.047 / 65747.172 / 2485.745.
All checksums/reference bits match. JSON samples, source and counter outputs
are in `/tmp/elisa-broader-quality-final/{struct-alias,strict-fp,mutual-recursion}`.
The longer preliminary repeat `/tmp/elisa-broader-quality-long` has the same
instruction-level conclusions. These probes materially extend quality
qualification, but do not establish every workload or enable default modes.
Production code has not changed since `227b8e30`; the previously qualified
22-case disk-rebuild matrix remains applicable. Full goal remains active.


### Fixed-type-parameter record replay and packed instances (2026-10-24)

A new real edit adds `disk_cache_generic_probe: u64 = 0` after the value field of
runtime `ConcurrencyBox[T]`. Ordinary `227b8e30` falls back after **19.200 seconds**,
exit 1/no object, checkpoint 2152/14422 declarations. This is a valid runtime
source edit; its controlled file is restored after every invocation.

Paired simple-record edits now permit identical type and region binders,
including unchanged type interface bounds. Binder names/kinds/bounds remain
fixed; value/state/permission/error-set binders and existing complex ownership,
annotation, invariant and field protocols retain their guards. Generic base
identity is preserved and its fields are repopulated using the original binder
context. Concrete generic aliases now participate in layout-target widening;
a new test initially selected five instead of six users because a parameterless
Small alias query was missed. `/tmp/elisa-generic-record-semantic-before-alias.log`
records that failure. Widening now includes GenericInstanceType aliases.

`TestRetainedGenericStructFieldsDiskRoundTrip` checks scalar addition, reordering,
removal/revert and invalid field retyping, cold diagnostics, identity and all six
direct/generic/alias/container users. The invalid retype is required to produce
cold errors, avoiding vacuous diagnostic parity. Fixed-bound and binder-change
checks also pass. Full retained semantic suite:
`/tmp/elisa-generic-record-semantic-all.log`.

Native serialized-state tests use multiple concrete instances, including nested
Pair, u8 and u8-reference payloads. Normal/debug IR matches cold compilation.
Both cold and replay O3 C clients check size/alignment/offsets, 16↔24-byte C ABI
transitions, reordered fields, high signed payloads, a nonzero high probe value
through generic copying, small payload extraction and reference identity.
The public by-value fixture declares `layout(c)`, as required by the language.

A separate valid cold test found that `ensureGenericInstanceStruct` ignored
PackedLayout. It emitted an unpacked i8/i64 record and align-8 field accesses.
`/tmp/elisa-generic-packed-before.log` records the failure. Concrete generic
instances now pass the base's PackedLayout to LLVM and enable the existing
packed-address alignment normalization. Cold O0/O3 native tests operate on four
unaligned packed generic records. Additional serialized packed→C→default
transitions check 13↔24-byte layouts, generic aliases, internal copies, C pointer
access and complete normal/debug IR parity. Logs
`/tmp/elisa-generic-record-backend-final.log` and
`/tmp/elisa-generic-layout-backend.log` pass. This does not newly qualify atomic
packed fields or escaped unaligned scalar-reference contracts.

Frozen `/tmp/elisa-generic-record-main-test` qualifies against the untouched
ordinary `227b8e30` state, output only in its root's `generic-record-qualification`:
baseline 22.793 seconds, actual runtime generic-field edit **9.571 seconds**.
Replay selects three users, 0.862 seconds total (diff 0.565, bodies 0.003,
post 0.290); 23 native units rebuilt/73 reused, 28 fragments emitted/64 reused,
zero fallback. Edited object SHA
`6556fe227aaa5c0d708c78ba094cc4b9167ed569382187ff3bd80e1d01789173`.
Both native compiler products pass include fixture exit 42, sidecar miss→valid,
enum dispatch and unknown diagnostics (`generic-record-qualification/functional.json`).
Snapshot unchanged, controlled runtime source restored. This is a thin margin;
fresh installed-binary namespace and the full ordinary matrix remain the next gate.


## Generic argument layout dependencies, 24 October 2026

Review exposed a missed dependency when an edited record is the argument to
`Wrapper[T] { value: T }`, rather than the edited generic base itself. The old
by-value walk inspected only the generic base and stopped at its type parameter.
The disk-round-trip regression selected zero layout-only users before the fix
(`/tmp/elisa-wrapper-argument-before.log`).

The walk now substitutes each instantiated struct field before following inline
storage. References still stop traversal; depth and visit bounds conservatively
invalidate pathological recursive generic graphs. The semantic regression checks
add/remove/add through nested wrappers and an outer record, selects both inline
layout-only users, excludes the reference-only wrapper, and matches cold diagnostics.
The retained semantic suite passes (`/tmp/elisa-wrapper-argument-semantic.log`).
Backend tests compare complete normal/debug IR to cold compilation after each
serialized edit; cold and replay O3 native C clients independently check physical
sizes and offsets. The new regression plus generic layout/ABI tests pass in
5.262 seconds (`/tmp/elisa-wrapper-argument-backend.log`). Full compiler performance
and fresh installed-binary matrix qualification remain pending for this change.


Installed `51b79192` was admitted into a fresh standalone cache namespace:
`/var/folders/k7/71kp36qd5_q9_tfp77s2vqzc0000gn/T/elisa-wrapper-argument-production-4ppsfqnb`. Steps 1–8 advanced bounded checkpoints,
step 9 reached its 30.226-second external timeout after publishing complete
state, and step 10 emitted the baseline object in 13.882 seconds. All invocations
had external 30-second caps; no retained server was configured.

All 23 baseline/edit products compile, link and pass include fixture exit 42,
sidecar miss→valid, enum dispatch and diagnostic checks. The 45 timed invocations
all exited zero. However, the generic-field edit took **10.246 seconds**, failing
the requested performance gate; alignment was 9.651 and packed layout 9.307.
Exact repeats were 0.141–0.327 seconds. Earlier explicit snapshot qualification
of the same production source took 8.519 seconds for the generic-field edit
(baseline 22.123); that does not supersede the ordinary CLI miss. Full timing and
functional records live in the fresh namespace; aggregate log is
`/tmp/elisa-wrapper-argument-production-matrix-all.log`. Further profiling is
required before claiming this latest binary meets the target across the matrix.


## Shared analyzer map-copy allocations, 24 October 2026

The generic-field miss was profiled with allocation samples: the preceding
binary allocated 6.64 GiB across restoration, preparation and code generation.
One candidate memoized syntax-only adopted escape facts within the build. It
saved only about 11 MiB of sampled escape-scan allocations and still measured
10.322 seconds; that candidate was discarded, with no production changes retained.
Its base/generic objects nevertheless matched the established SHA-256 values.

`privatizeSharedMaps` previously called reflect iterator Key/Value per entry.
It now uses reusable SetIterKey/SetIterValue scratch values; SetMapIndex still
copies every entry, and analyzer/result isolation and restoration are unchanged.
A 10,000-expression shared-map benchmark (three 200 ms samples) changes from
543566–556160 ns/op, 976222–976226 B/op, 20038 allocations/op to
419378–425552 ns/op, 656248 B/op, 40 allocations/op. Logs are
`/tmp/elisa-map-copy-before.log` and `/tmp/elisa-map-copy-after.log`.
All retained semantic tests pass three times; map isolation/restored identity
has a direct regression. Generic disk layout/ABI, nested generic argument,
adopted backing ownership and shared lookup backend regressions also pass
(`/tmp/elisa-map-copy-backend.log`). This is a verified map-copy improvement;
full compiler timing for this implementation remains to be qualified.


The committed map-copy version's explicit snapshot qualification measured baseline
22.675 seconds and generic-field 9.604 seconds. Both native compiler products
match the established base/generic object hashes and pass include execution 42,
sidecar miss→valid, enum dispatch and unknown-diagnostic checks. Evidence:
`map-copy-qualification` under the original imports production root, plus
`/tmp/elisa-map-copy-functional.log`. This does not replace the failed ordinary
CLI timing gate or claim a stable full-compiler speedup from the microbenchmark.

## Added include-file baseline lookup, 24 October 2026

A novel helper include on ordinary `51b79192` took 13.976 seconds, exited with an
admission checkpoint, and emitted no object. The included-file authority set
changed the exact semantic-state key. Result is `new-include.json` in the fresh
wrapper-argument production namespace. Controlled source/helper changes restored.

Completed states now publish an atomic, bounded family hint beside the exact
artifact. The family identity retains compiler, working directory/root, semantic
options, environment, diagnostic path state, target and solver authority; only the
included-file set is omitted from that hint's identity. On a missing exact state,
a recorded baseline is considered only if its sorted, unique file set is a
strict subset of the current set. Its exact key is recomputed from current
inputs plus that old file set, and its original envelope is read without rekeying.
No exact-state corruption bypass, trust/policy change, authority removal or
unvalidated key is admitted. Every family candidate goes through checked scratch
replay even if expanded bytes happen to match. The complete baseline stays immutable.

The fresh-process cold oracle found an existing emission-order error: retained
physical byte offsets remained old when an include was inserted before a function.
Added declarations were sorted against those stale offsets. Append sorting now
uses current expanded-source offsets matched by file/line/column, retaining the
synthesized-declaration fallback. The oracle now matches complete cold LLVM IR
and diagnostics for valid 42, invalid bool return, and valid 43 helper revisions.
Native qualification honestly admits a native baseline separately from textual
IR (their semantic options differ), then replays the added include, links and
executes with status 43. Baseline snapshots remain unchanged. Authority/policy,
trust, bad-key, duplicate-origin and format-version guards pass.

`/tmp/elisa-analysis-family-final.log`: all analysis-disk tests pass, 4.592 s.
`/tmp/elisa-analysis-family-semantic.log`: retained semantic suite passes, 0.878 s.
`/tmp/elisa-analysis-family-backend.log`: all retained backend tests pass, 11.036 s.
All commands externally capped at 30 s. Full-project installed-binary performance
for the new include path remains the next gate; the broader performance goal is open.


Fresh installed `39ed5c2c` namespace: `/var/folders/k7/71kp36qd5_q9_tfp77s2vqzc0000gn/T/elisa-include-family-production-2ayyu5ok`.
Eight admission checkpoints, step 9 externally timed out at 30.220 seconds after
publishing one complete state and its family hint (1,274 origins); step 10 emitted
the baseline in 16.113 seconds. The ordinary novel helper-include edit now emits
an object in **6.171 seconds**, exit zero, compared with 13.976 seconds/exit 1/no
object before family lookup. Root source and temporary include were restored.
The edited object SHA-256 is
`c63f01a561ee725e8694460f2cc89eaedb20dce247b4752eb2d5b3250b2dd974`.
It links and runs the direct new-helper oracle (42), and the rebuilt compiler
passes include fixture execution 42, sidecar miss→valid, enum dispatch, and unknown
flag diagnostics. Records: `new-include.json`, `new-include-functional.json` in
that namespace and `/tmp/elisa-include-family-production-include-functional.log`.
This verifies the added-file gain; the fresh 23-case matrix and remaining broader
minor/major edit families still need qualification on the latest implementation.


The fresh installed-39 matrix is complete: **24 variants including baseline and
new include**, 46 timed invocations, all exit zero and under ten seconds.
Largest edited times: packed 9.197 s, region record 8.759, ordinary record field
8.720, alignment 8.631, generic field 8.322, private bound 8.234. The new include
was 6.171 s; exact repeats were 0.140–0.173 s. Generic field output matches its
established `6556fe...` object hash. All 24 native compiler products pass the
include fixture/sidecar/enum/diagnostic checks; the new-include product also
passes the direct emitted helper oracle. Controlled sources are restored.
`functional.json` contains 23 existing variants, `new-include-functional.json`
the additional case, and `/tmp/elisa-include-family-production-matrix-all.log`
the main matrix run. This closes the current matrix's ordinary timing miss on
this installed binary; further edit forms and default-mode policy remain open.


## Generic type-binder name edits, 24 October 2026

Renaming runtime `ConcurrencyBox[T]`/`value: mutable T` to
`ConcurrencyBox[Payload]`/`value: mutable Payload` on ordinary installed 39 hit
a guarded fallback: 18.382 seconds, exit 1, checkpoint 2434/14422 declarations,
no object. `generic-record-binder.json` in the include-family production namespace
records this new miss; the runtime source is restored. A serialized two-parameter
regression also failed before the change (`/tmp/elisa-generic-binder-before.log`).

Simple generic-record field replay now permits type-parameter names to change
while preserving arity, binder kinds, region binders and exact bounds. The live
canonical StructType receives the edited TypeParams and GenericParams before
its fields are repopulated; its identity remains unchanged. Existing struct-user
invalidation and memo clearing still apply. Bound changes, arity changes, value
and state binders retain their guards.

The retained semantic suite passes in 0.956 seconds, including disk round trips
through renamed parameters, a two-parameter name swap, restoration, generic
function consumers, alias layout queries, and a renamed binder with the same
interface bound. The fixed-bound rejection controls now test bound removal and
arity change, rather than treating a name change as a protocol change.
`/tmp/elisa-generic-binder-semantic.log` contains the run.

All retained backend tests pass in 11.357 seconds. The generic-instance ABI test
now interleaves binder renames with field addition/reordering/restoration; full
normal/debug IR matches cold compilation and both cold/replay O3 native C clients
verify high signed copies, nonzero added payloads, small values, references and
physical layouts. `/tmp/elisa-generic-binder-backend.log` contains the run.
Full compiler performance of this newly supported edit remains to be qualified.


Explicit qualification `/tmp/elisa-generic-binder-main-test` against the unchanged
original imports snapshot measured baseline 22.985 s and the actual runtime binder
rename **6.836 s**. Both native products have baseline object SHA
`64e173d0d20dc28ef96d0cd2e61789f98058605d04e09be36c035b1c74236297`
and pass include execution 42, sidecar miss→valid, enum dispatch and diagnostics
(`/tmp/elisa-generic-binder-functional.log`; `generic-binder-qualification` under
the original imports production root). No checkpoint rekeying or publication.

Fresh installed `ce99026c` namespace: `/var/folders/k7/71kp36qd5_q9_tfp77s2vqzc0000gn/T/elisa-generic-binder-production-u_ju2ui3`.
Eight bounded admission checkpoints; step 9 timed out externally at 30.248 s after
publishing complete state, step 10 emitted the baseline in 15.165 s. Ordinary
binder-name edit now takes **5.987 s**, exit zero, versus the previous 18.382 s
checkpoint/no-object miss. Generic field 8.832 s; new include 6.152 s.

The full matrix now has **25 variants including baseline**, **48 timed
invocations**, all exit zero and below ten seconds. Largest edited times:
packed 9.321 s, region record 9.209, ordinary record field 9.004, generic field
8.832, alignment 8.707, private bound 7.919. All previously successful product
hashes match the preceding matrix; the newly supported binder rename emits the
unchanged baseline bytes. All 25 native products pass include fixture execution,
sidecar and enum/diagnostic checks; the new include also passes its direct helper
oracle. `functional.json` records 24 variants, `new-include-functional.json` one.
Aggregate run: `/tmp/elisa-generic-binder-production-matrix-all.log`.
Controlled files restored; the seven preexisting Stage1 user modifications remain.

This is representative qualification of the opt-in standalone disk-cache mode,
with O3, bounded imports, independent IR/native units and no retained server.
It does not claim every possible major edit, default-mode policy, or one-pass cold
admission is complete. The performance goal remains active.
