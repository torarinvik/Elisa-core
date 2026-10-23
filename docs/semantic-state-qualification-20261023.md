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
