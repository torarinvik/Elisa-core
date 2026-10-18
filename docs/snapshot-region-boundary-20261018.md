# Snapshot ownership boundary controls

A small extraction uses the current transient snapshot's complete record/enum types and nine owned pools. On the pinned gate Stage0, inserting old empty, region-aware factory, or by-value copy results into a generic caller's `darray[Snapshot] @r` reports inferred-to-r copy errors. A borrowed-source copy variant alone does not clear that boundary. Direct lexical-region examples do not reproduce it, so the generic caller is essential to the experiment. These observations identify a representation/call-shape limitation; they do not prove all such factories unsafe.

Constructing the nine pools directly in that generic caller's region and inserting the literal passes. Moving the byte pool into a shorter scratch region and adding a byte is rejected as a dangling value. The permanent test preserves the accepted inline shape and the actual unsafe pool rejection; it deliberately does not require the conservative factory rejections to remain forever.

A separate Global-axis-shaped StoreFlow control decodes snapshot-owned names into local edge arrays, computes flags, and publishes rows using independent AST/input names. It has no snapshot-to-table retention edge. A deliberate direct push of a snapshot view into table storage does retain that edge. Both programs are well-typed; the negative control tests retention summaries rather than relying on a syntax/type failure. This does not prove the complete production checker has no escape; it narrows one candidate explanation.

Both permanent regression tests pass on the combined Core tree (package 0.384 seconds). Gate CLI inputs, diagnostic logs and identity are retained under `build/verification/snapshot-factory-boundary-20261018/`. The gate binary SHA is recorded in `gate-identity.json`; it was the same old Stage0 used by the source owner. No shared source or installed compiler was changed, and no full build was started. The source owner's inline workaround still requires its complete source-qualified Stage1 run.

## Minimal explicit-region factory reproduction

`docs/repros/explicit-region-factory-packet.elisa` reduces the factory boundary
to a single `darray[u8]` field. Running the pinned gate compiler with
`-emit semantic docs/repros/explicit-region-factory-packet.elisa` exits 1
and reports inferred-region by-value escape at line 12. Gate binary SHA256:
`563cae3a6d621ebffe4a82e00a62246c33ffa44996c46da4823b107991d0f058`.
Rechecked locally on 2026-10-18; this is diagnostic evidence, not a fix.

The factory declares both `Packet[r]` and return `@r`. The caller explicitly
instantiates it with its destination region. A likely precision boundary is
`currentParamRegionFromRefState`: it rejects every named region dependency,
while `regionRefStateForExpr` represents declared call-return regions using
exactly such dependencies. This is a hypothesis pending instrumentation and
negative controls for mixed, shorter-lived, and stale dependencies. Do not
suppress the escape diagnostic solely on the nominal return annotation.

Instrumentation refinement: a focused semantic-package test on this integrated
Core revision reproduces the same two errors (0.01 seconds of test execution).
At the failing call, `valueInteriorRegion` returns `__auto_305`,
`regionRefStateForExpr` reports no state, and `declaredCallReturnRegions` has
no entry. Consequently the hypothesized rejection of an existing named-region
dependency is not the immediate cause in this example. Investigate call-return
metadata creation/specialization first. Temporary tracing and the intentionally
failing probe were removed after recording this observation.

Parser correction: `takeReturnTypeRegion` previously promoted only named and
tuple return suffixes. Generic aggregate suffixes now receive the same function
contract treatment; parameter suffixes remain on their type node. The full Go
parser suite passes (1.044 seconds). This correction alone, and together with an
experimental already-substituted return-region lookup, did not eliminate the
Packet insertion errors. The experimental semantic edit was removed. Further
semantic tracing is required; this commit does not claim to fix Stage1.

## Safety control blocks trusting the return contract

An experimental semantic candidate retained a live already-substituted call
return owner and consulted that owner before ambient-region fallbacks. It made
the Packet positive pass and rejected scratch factory/field controls. Existing
`Test.*(Region|Factory)` tests passed (0.451 seconds). However, the new
`docs/repros/explicit-region-factory-independent-input.elisa` control was accepted:
a factory declares return `@r` while returning its independent `@s` argument as
the owned field of `Packet[r]`. A focused semantic-package test confirmed this
acceptance also occurs with all experimental semantic edits removed (0.368
seconds). This is an existing contract-enforcement gap in the integrated tree.

The candidate was withdrawn rather than allowing callers to trust an unchecked
contract. Its patch and test are saved under ignored
`build/verification/factory-contract-safety-20261018/`. No semantic production
change remains. Next fix must enforce independent region constraints on aggregate
construction/return before enabling precise call-owner reuse.

## Enforce independent input regions on aggregate returns

`checkDeclaredAggregateReturnParamRegions` now visits interior reference
provenance for an explicit return-region contract. Value-parameter dependencies,
named region dependencies, and nested field states must not retain an independent
source-declared region that cannot outlive the contract region. The nominal
aggregate type alone is insufficient because contextual checking may already
name the target region. Existing local-region checks remain in place.

Regression cases reject direct, aliased, and nested independent-input aggregates;
the matching input/return region passes. The semantic `Test.*(Region|Factory)`
suite passes (0.677 seconds). Precise call-owner candidate remains withdrawn
pending additional review; no full Stage1 build was run.

## Preserve resolved factory call owners after contract enforcement

The call-owner candidate is now restored on top of the independent-input
contract check. A specialized function's return region may already be the caller
owner; if no formal binding remains, record that exact live owner. Both value
interior storage checks and auto-scope escape checks consult the recorded owner
before using the ambient allocation fallback. Dead/unresolved owners do not
receive this precision.

Tests cover the original generic Packet factory insertion, a generic forwarding
helper with a differently named region parameter, scratch-owner insertion,
scratch backing returned from the factory, and a region-less aggregate forwarded
from independent `s` into an `r` return contract. Safe cases pass and all unsafe
cases reject. The combined semantic region/factory suite passes (0.660 seconds).
This is semantic-package evidence; no current Stage1 binary or rebuild timing
is claimed.

Production-shape confirmation: `testdata/snapshot_region_generic_factory.elisa`
retains the actual snapshot records, nine owned buffers, and region-generic
factory insertion used in the earlier failing gate experiment. It now passes
`TestSnapshotExplicitRegionFactory` against the integrated semantic analyzer.
The combined region/factory/snapshot suite passes (0.545 seconds). This pins the
full aggregate boundary independently of ongoing shared Stage1 source edits.

Canonicalizer boundary confirmation: the original complete nine-buffer snapshot
fixture now passes when a factory result is passed by value to a helper that
allocates and copies fresh destination-owned buffers, then inserted into the
generic destination array. A borrowed-source variant is analyzed as well.
Replacing only that variant's returned byte pool with `snapshot.bytes` rejects
the independent input region. This guards against treating a shallow descriptor
copy as a rehome. Fixture: `testdata/snapshot_region_canonicalizer.elisa`; test:
`TestSnapshotFactoryByValueCanonicalizer`. Combined region/factory/snapshot suite
passes (1.011 seconds); verbose evidence is saved in the ignored verification
directory. No full compiler build or rebuild-time claim is attached.

## Integration with committed Core 25f2d4e9

Merge cbc15c3a incorporates current committed Core without conflicts or edits to
the shared checkout. A broader test selection spans root runtime/build-cache,
parser, and semantic region/factory/snapshot tests. It records 468 passed test
cases, 12 failed runtime cases, and four skipped server-response subcases. All
12 failures report missing Global grants on print; one representative
`TestS4Stage2ByValueSameRegionCohortRuns` was rerun at unmodified 25f2d4e9 and
reproduced that failure. This establishes baseline behavior for that representative
only, not all twelve tests. Parser and semantic package selections pass.

The four server-response subcases declined because inherited
`ELISACORE_PGO_HOT_FUNCTIONS` disables whole-object memoization. Clearing only
that variable and rerunning `TestCompileServerResponseTracksBareFilenameExistence`
passes all four subcases (five cases including parent), with no skips (0.594 s).
JSON evidence for the combined run, baseline comparison, and clean-environment
rerun is saved under `build/verification/factory-contract-safety-20261018/`.
The broader integration selection is not a wholly green gate; full Stage1
qualification remains outstanding.

Runtime fixture correction: the twelve previously failing region runtime cases
now explicitly grant Global.Read/Global.Write in their main functions and print
calls, matching print's checked effects. Their allocation/ownership bodies and
expected runtime outputs are unchanged. All twelve compile-and-run tests pass,
zero skips (5.281 seconds). This restores end-to-end coverage that previously
stopped at the effect checker; compiler effect enforcement was not relaxed.
The earlier combined run's twelve failures and four skips are now individually
resolved by targeted reruns, rather than repeating unaffected cases.
