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
