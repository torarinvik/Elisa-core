package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/easm"
	"elisacore/src/lexer"
)

type castHookSignature struct {
	Source string
	Target string
}

type initHookSignature struct {
	Target string
	Params string
}

type regionState struct {
	Destroyed  bool
	Generation int
	Allocated  bool
	// Backing is the normalized backing strategy (chained / fixed / reserve_commit
	// / scratch; docs/68 §3). It governs adopt's backing-family check and the
	// pointer-stability rule.
	Backing string
	// DeclScope is the scope in which the region was declared. A binding whose
	// defining scope strictly encloses DeclScope outlives the region's block, so
	// storing region-allocated data into it dangles (see the region-less-target
	// case of checkNestedRegionStoreEscape).
	DeclScope *Scope
}

type regionMarkState struct {
	Region        *Symbol
	Generation    int
	Valid         bool
	InvalidatedBy string
}

type checkpointKind int

const (
	checkpointKindDArray checkpointKind = iota
)

type checkpointState struct {
	Kind          checkpointKind
	Target        ast.Expr
	TargetType    Type
	Valid         bool
	InvalidatedBy string
}

type storageViewDependencyState struct {
	Sources          []string
	ContainerAliases []string
	Valid            bool
	InvalidatedBy    string
	// Interior: the view is (or carries) an address INTO a relocatable container's buffer
	// (`&xs[i]`), not merely a value derived from it. Only these are invalidated by a callee's
	// mutable borrow; see invalidateStorageViewsForMutableRefArg.
	Interior bool
	// Replaced: the invalidation was a whole-value store (`xs <- [...]`), which points the
	// container at a different buffer. A reserve_commit/fixed backing keeps the OLD base in
	// place across growth, but that does not make the view alias the new contents, so the
	// stable-backing post-pass must not drop it.
	Replaced bool
}

type regionDependencyState struct {
	Generation    int
	Valid         bool
	InvalidatedBy string
}

type packedStoreDependencyState struct {
	Type *PackedEnumStoreType
}

type packedStoreResolution struct {
	Symbol *Symbol
	Type   *PackedEnumStoreType
}

type rewriteDefaultContext struct {
	Allowed    bool
	ExactType  Type
	ResultType Type
	Message    string
}

type sequenceRewriteContext struct {
	ElemType   Type
	OutputElem Type
}

type regionRefState struct {
	Deps                    map[*Symbol]regionDependencyState
	StoreDeps               map[*Symbol]packedStoreDependencyState
	DirectParamDep          int
	HasDirectParamDep       bool
	ParamDeps               IntBitSet
	Fields                  map[string]regionRefState
	PackedStoreSummary      PackedStoreProvenance
	PackedStoreSummaryKnown bool
	ParamOnlySummary        bool
}

type conservativeCallWidening struct {
	Path      []borrowReturnAnnotationStep
	Source    string
	SourcePos lexer.Pos
	Reason    string
	Before    string
	After     string
}

type borrowedOwnerRefState struct {
	HasDirect bool
	Direct    affineValueKey
	Fields    map[string]borrowedOwnerRefState
	// RawInteriorAffineAlias marks an entry that is NOT a borrow of a linear/owner
	// value, but a raw interior pointer copied out of an affine handle's field
	// (e.g. `b: heap T& = h.ptr` where `h` is an affine `Pooled[T]`). `Direct` then
	// names the affine handle whose consumption recycles the slot `b` points at, so
	// using `b` after that handle is consumed is a use-after-free. These entries are
	// merged with conservative UNION semantics (keep the taint if either branch has
	// it), unlike the intersection semantics used for genuine owner borrows.
	RawInteriorAffineAlias bool
}

type affineValueState struct {
	ConsumedBy              string
	LiveProtocolType        Type
	LiveProtocolDescription string
	// Uninitialized marks a local declared with `= zeroed` that has not yet been
	// assigned (definite-assignment tracking). Reading it before any assignment
	// to it (or a field of it) is a use of uninitialized memory. Folded into the
	// affine state so it inherits clone/merge/snapshot across control flow.
	Uninitialized bool
	// ScheduledForDefer marks a linear value whose must-consume obligation will
	// be discharged by a `defer function` body that consumes it. Such a value no
	// longer reports "must be consumed" at scope exit (the deferred body runs at
	// every function exit), may still be borrowed/read, but must NOT be consumed
	// again inline (that would double-consume the resource at runtime).
	ScheduledForDefer bool
}

type affineValueKey struct {
	Root *Symbol
	Path string
}

type poolScopeState struct {
	Name string
	// Node is the pool statement; a submit's pool argument must resolve to it.
	Node *ast.PoolStmt
	// BorrowedPool and BorrowedAlias identify the caller-owned pool and the
	// nursery's private handle. Shutting either down before cleanup waits would
	// free runtime state while submitted task records still refer to it.
	BorrowedPool       *Symbol
	BorrowedPoolKey    affineValueKey
	HasBorrowedPoolKey bool
	BorrowedAlias      *Symbol
	// OuterRegions are the regions live when the pool statement began. The pool
	// joins its workers at scope exit, before any of them is released, so a task
	// may depend on them. Regions declared inside the body die before the join.
	OuterRegions map[*Symbol]int
	// Joined records the outer-region dependencies handed to tasks, so the pool
	// exit can reject a reset/destroy of one inside the body (before the join).
	Joined *[]poolJoinedRegionDependency
	// JoinedViews records the storage views handed to tasks. A task may read its view
	// at any point until the join, so the pool exit re-checks each one: a growth of the
	// backing anywhere in the body (even after the submit) is a use-after-free race.
	JoinedViews *[]poolJoinedView
}

// poolJoinedView is one view handed to a task: a named view local (Sym nil, looked up by
// name at the join) or an unnamed view argument (`submit f(pv(buf))`, `f(buf.as_sview())`),
// tracked under a synthetic symbol that lives in currentStorageViewDeps until the join.
type poolJoinedView struct {
	Expr ast.Expr
	Name string
	Sym  *Symbol
}

type poolJoinedRegionDependency struct {
	Region     *Symbol
	Generation int
	Pos        lexer.Pos
	CallName   string
}

type AnalyzeOptions struct {
	// TrustedRuntimeSourceFiles is populated by the source loader with canonical
	// paths of runtime files it actually expanded. Do not derive this from a
	// declaration's Position.File alone; source-written #line markers can spoof it.
	TrustedRuntimeSourceFiles []string
	EnforceUnsafePermissions  bool
	// EnforceGlobalPermissions additionally checks inferred immutable-global/extern
	// effects. Mutable globals always require explicit Global.Read/Write grants.
	EnforceGlobalPermissions bool
	EnforceProgressSafety    bool
	// EnforceStrictConcurrency promotes legacy raw-concurrency migration diagnostics
	// from deprecations to hard errors while the proof-carrying surface matures.
	EnforceStrictConcurrency bool
	// EnforcePerfLints promotes the performance-friction lints (pointer-graph, allocation
	// churn) from warnings to hard errors — the `-Wperf` graduated-strictness level for
	// shipped code (docs/70). Off by default so prototyping stays fluid.
	EnforcePerfLints bool
	// FlowLintMode drives the flow-checked-loop lints (docs/121): messy scanner/parser loops
	// that hide an untyped state machine, scatter cursor advancement, or pyramid their branches.
	// FlowLintOff (default in Phase A) disables them; FlowLintWarn (`-Wflow`) nudges; FlowLintStrict
	// (the eventual default, downgraded to warn by `-permissive`) rejects. The `can ComplexFlow:`
	// grant silences a covered loop at any level.
	FlowLintMode FlowLintMode
	// WarnDiscardedValues turns on docs/119 W1: a statement-position expression whose non-void value
	// is silently discarded warns (`_ = expr` to discard on purpose, or use it). OFF by default — the
	// corpus has many intentional side-effecting calls that also return a value, so this is an opt-in
	// tidiness channel, not a default lint.
	WarnDiscardedValues bool
	// EnforceStrictProofs promotes the refinement-proof diagnostic from a warning to a hard error
	// (docs/85: the `-strict` safety channel). When a refinement obligation cannot be discharged
	// statically it falls back to a runtime check; by default that emits a WARNING so the user
	// always KNOWS a static guarantee was not achieved, and under -strict it is an error
	// (prove-it-or-fail, the Dafny-like mode). Off by default.
	EnforceStrictProofs bool
	// EnableSMT turns on the optional SMT discharge tier (docs/90): an obligation the bounded-linear
	// prover declines (a non-linear product, a richer boolean body) is handed to an SMT solver. Off
	// by default — it spawns a solver subprocess and is only worth it for code the linear tier can't
	// reach. SMTSolverBinary overrides the solver (default "z3"). The tier is sound regardless: an
	// `unsat` of the negated obligation proves it; sat/unknown/missing-solver decline to runtime.
	EnableSMT       bool
	SMTSolverBinary string
	TargetTriple    string
	TargetDebug     bool
	// RequireExternContracts enforces extern contract DISCIPLINE: every extern function must carry a
	// `requires`/`ensure` boundary contract OR an explicit `@trusted("reason")` annotation. An extern
	// that is neither is an error. Opt-in (the `-strict-externs` channel) and deliberately NOT implied
	// by -strict: -strict is on by default, and most real programs link uncontracted libc/host externs,
	// so blanket enforcement would break them. This makes "is this native boundary specified?" auditable.
	RequireExternContracts bool
	// RecordDeathTimeCohorts records the docs/91 G0 inferred death cohorts on Result.DeathTimeCohorts
	// (programmatic access, e.g. the tightness-validation harness) without needing the
	// ELISA_DUMP_DEATHTIME env dump. Read-only analysis; no codegen effect.
	RecordDeathTimeCohorts bool
	// EmitProofHoleHints turns on the docs/98 constructive proof-hole assistant for plain asserts: a
	// strict integer-comparison assert the static tiers cannot discharge emits a non-fatal hint listing
	// the goal, in-scope known facts, and a suggested missing precondition. OFF by default — the hint is
	// an assistant you INVOKE (the future `assert ?` / `--explain-hole` surface), not an always-on
	// diagnostic, so normal builds stay quiet and never flag legitimate runtime-checked asserts.
	EmitProofHoleHints bool
	// RequiresReport turns on the -requires-report blast-radius aggregation (docs c3): for every
	// requires-bearing function, count the direct call sites whose precondition is statically
	// dischargeable vs the ones that fall back to a runtime check (with the unprovable sites'
	// locations). Read-only observability surfaced on Result.RequiresReport; OFF by default and with
	// ZERO effect on existing errors/lints — it only accumulates alongside the discharge pass.
	RequiresReport bool
	// OverlayLayouts registers docs/107 typed guest-memory overlay layouts (keyed by name) so a
	// `GuestVAddr[L]` carrier can have `base.field[mem]` field accesses checked against L and
	// desugared to MemoryManager_ReadU<N>/WriteU<N>. Nil/empty disables the overlay accessor.
	OverlayLayouts []*easm.Layout
}
