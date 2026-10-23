# Semantic-state disk persistence qualification

The semantic analyzer now exposes `RetainedAnalysis.MarshalState` and
`RestoreAnalysisState`. This is a qualification API; the ordinary CLI does not
admit these snapshots automatically yet.

The state codec writes schema-checked typed values and object IDs. Shared maps,
slices, pointers and cycles survive restoration. Cache bytes cannot supply Go
types or executable callbacks. Checksums, collection/allocation bounds and
reference/type checks reject corrupt records. Unsupported interior alias shapes
currently reject the entire snapshot rather than restoring independent copies.

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
- A real readonly-reference compiler atom analyzes successfully in about 2.2 s.
  Its snapshot is currently declined because a `*ast.MatchArm` points into a
  `[]ast.MatchArm`; supporting that alias is the next codec step.

`TestDiskStateCompilerInput` is an opt-in real-source qualification using
`ELISACORE_STATE_QUALIFY_INPUT`. Always run it with an external 30-second timeout.
This evidence does not establish a sub-10-second fresh-process rebuild of the
full stage1 compiler. CLI disk admission, larger-input qualification, snapshot
load/write cost, and full edit-to-object/executable measurements remain open.
