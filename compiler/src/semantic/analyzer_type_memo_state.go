package semantic

// analyzerTypeMemoState holds type-shape and region-reachability memoization for retained analysis.
type analyzerTypeMemoState struct {
	// typeShapesFrozen is set once every declaration's type has its final fields and
	// affinity (just before body analysis); type-predicate memos are only valid after it.
	typeShapesFrozen bool
	// resolvedStructFieldsMemo stores immutable resolved field lists for finalized types.
	resolvedStructFieldsMemo map[Type][]moveBindResolvedField
	// affineHandleMemo caches containsAffineHandleValues per type once typeShapesFrozen.
	affineHandleMemo map[Type]bool
	// typeParamMemo caches complete type-parameter graph queries after shapes freeze.
	typeParamMemo map[Type]bool
	// retained is non-nil when this analysis is kept for in-place updates (retained_analysis.go).
	retained            *RetainedAnalysis
	explicitBackingMemo *regionBackingMemo // live only during inferRegionParamsForGrownContainerParams
	// preBody stops a scratch analysis after the declaration phase (retained_prebody.go).
	preBody *preBodyState
	// Current declaration-phase classification, including local maps that are
	// otherwise absent from the retained pre-body dependency certificate.
	preBodyClassify *retainedClassifyState
	// typeSeenScratch is a reused visited set for containsAffineHandleValues traversals.
	typeSeenScratch     map[Type]bool
	typeSeenScratchBusy bool
	// borrowedOwnerRefMemo caches containsBorrowedOwnerRefValues likewise.
	borrowedOwnerRefMemo map[Type]bool
	// regionRefsMemo caches typeCanContainRegionRefs likewise.
	regionRefsMemo map[Type]bool
	// abstractParamRegionRefMemo caches complete parameter-provenance layouts for
	// frozen types. Recursive walk results depend on the current cycle path, so only
	// roots entered with an empty seen set are memoized.
	abstractParamRegionRefMemo map[abstractParamRegionRefKey]regionRefState
	// Frozen layout classifications for safe reference-element summary reuse.
	abstractRefRestartMemo map[abstractRefRestartKey]bool
	// regionRefAnyIndexParamFields shares the immutable one-entry wildcard field map for
	// repeated array-like parameter states during abstract region-reference construction.
	regionRefAnyIndexParamFields map[int]map[string]regionRefState
	// mutableBorrowCapabilityMemo caches hasMutableBorrowCapability likewise when no
	// incremental session supplies its own capability cache.
	mutableBorrowCapabilityMemo map[Type]bool
	// containerRegionMemo caches containerRegion per type once typeShapesFrozen.
	containerRegionMemo map[Type]string
	// regionPointeesMemo holds completed immutable reachability sets for frozen types.
	regionPointeesMemo map[regionPointeesMemoKey]regionPointees
	// storeScanAddressMemo caches root store-scan address reachability once type shapes freeze.
	// Recursive child results are never memoized under a partial cycle traversal.
	storeScanAddressMemo map[Type]bool
	storeScanAddressSeen map[Type]bool
	// storeScanPlainValueMemo and storeScanProgramLivedMemo cache the two per-type early-outs of
	// the struct-target store scan's value judgement once type shapes freeze.
	storeScanPlainValueMemo   map[Type]bool
	storeScanProgramLivedMemo map[Type]bool
	storeScanAddressSeenBusy  bool
	// symbolTypeEpoch changes whenever an existing Symbol's Type is reassigned.
	symbolTypeEpoch uint64
	// globalDerivedLoopCandidates caches the global scope's derived-state bindings for
	// captureDerivedLoopEntry (valid for one globalScope symbolsVersion and symbolTypeEpoch).
	globalDerivedLoopCandidates derivedLoopCandidates
	// Parallel body workers read a frozen global symbol table. Cache its local
	// names per scope/version rather than filtering the entire table per proof.
	smtGlobalLocalNames smtGlobalLocalNameCache
}
