package semantic

// CodegenFactRead records one diagnostic observation of a semantic Result view.
// Key and Value are opaque live-process payloads, including borrowed slices,
// maps and nominal pointers. Neither their identity nor their spelling or
// source position is a portable dependency key. These events do not authorize
// reuse: canonical encoding, full query coverage and dependency validation are
// deliberately unresolved.
//
// Operation is lookup, value, len, range-item, range-end, own-map-presence
// or own-map. The Own APIs access only the physical current-Result map; they
// never inherit from a specialization root. Their Present reports map != nil.
// An own-map event stores a shallow maps.Clone membership snapshot, preserving
// nil versus allocated empty; its keys and pointer/slice/map values remain opaque.
// The caller receives the original map, so later membership edits do not change
// the event snapshot, but mutation inside borrowed values remains visible. Present preserves
// comma-ok membership (including stored nil/false) for lookup/value/range-item.
// Complete applies only to range-end and is false for early stop, nil yield or
// panic. A full range is still an opaque observation, not a cache certificate.
type CodegenFactRead struct {
	Result    *Result
	Table     string
	Operation string
	Key       any
	Value     any
	Present   bool
	Complete  bool
}

// CodegenReadView is a receiver-local read path. It never installs an observer
// on the shared Result. Recreate it from the current Result after any private
// specialization install/restore; a previously created value keeps its context.
// Logical methods delegate to the existing sparse accessors, preserving owned misses,
// canonical/private remapping and inherited rows without introducing new policy.
// HasOwn/Own map methods separately preserve physical direct-field semantics.
type CodegenReadView struct {
	Result  *Result
	Observe func(CodegenFactRead)
}

func NewCodegenReadView(result *Result, observe func(CodegenFactRead)) CodegenReadView {
	return CodegenReadView{Result: result, Observe: observe}
}
