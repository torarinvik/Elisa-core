package semantic

// FactClass names the orthogonal kinds of static knowledge the analyzer tracks
// about values. The current compiler still stores these facts in specialized
// structures such as GuardFactSet, OptimizationFacts, RefType, Shape, effects,
// and packed store state; this enum is the shared vocabulary for gradually
// unifying those systems.
type FactClass string

const (
	FactRepresentation FactClass = "representation"
	FactRefState       FactClass = "refstate"
	FactShape          FactClass = "shape"
	FactTypestate      FactClass = "typestate"
	FactStorage        FactClass = "storage"
	FactRegionDeps     FactClass = "region-deps"
	FactStoreDeps      FactClass = "store-deps"
	FactAliasClass     FactClass = "alias-class"
	FactUsage          FactClass = "usage"
	FactEffects        FactClass = "effects"
	FactOptimization   FactClass = "optimization"
)

// FactTransformKind is the small algebra that operations should lower to when
// they change static knowledge. Keeping these names explicit makes it easier to
// explain why surface conveniences such as node[...] or freeze(move store) are
// still honest: they may hide syntax, but they must not hide fact transitions.
type FactTransformKind string

const (
	FactTransformProduce    FactTransformKind = "produce"
	FactTransformRefine     FactTransformKind = "refine"
	FactTransformWiden      FactTransformKind = "widen"
	FactTransformRecompute  FactTransformKind = "recompute"
	FactTransformConsume    FactTransformKind = "consume"
	FactTransformInvalidate FactTransformKind = "invalidate"
	FactTransformRebase     FactTransformKind = "rebase"
	FactTransformRequire    FactTransformKind = "require"
	FactTransformEnsure     FactTransformKind = "ensure"
)

// FactTransform is a lightweight descriptive record used by diagnostics,
// reports, and future analyzer cleanup work. It intentionally does not own the
// fact payload yet; existing precise structures remain the source of truth until
// each subsystem migrates onto the shared model.
type FactTransform struct {
	Kind    FactTransformKind
	Classes []FactClass
	Target  string
	Source  string
	Reason  string
}

func invalidatedRegionFactUseMessage(label string, name string, reason string) string {
	return label + " " + quoteFactTarget(name) + " cannot be used: region dependency facts were " + FactTransformInvalidate.String() + "d by " + reason
}

func localRegionEscapeMessage(label string, regionName string) string {
	return "cannot return " + label + ": region dependency facts include local region " + quoteFactTarget(regionName)
}

func threadLocalRegionDependencyMessage(callName string, regionName string) string {
	return "argument to " + quoteFactTarget(callName) + " cannot cross thread boundary: region dependency facts include local region " + quoteFactTarget(regionName)
}

func threadUnpublishedStoreDependencyMessage(callName string, storeName string) string {
	return "argument to " + quoteFactTarget(callName) + " cannot cross thread boundary: store dependency facts require " + FactTransformRebase.String() + " to frozen/public store, got " + quoteFactTarget(storeName)
}

func ensureTargetUnresolvedMessage(targetName string, funcName string) string {
	return "cannot prove ensures " + targetName + " on function " + quoteFactTarget(funcName) + ": " + FactTransformEnsure.String() + " fact target cannot be resolved from current tracked facts"
}

func ensurePreserveWidenedMessage(targetName string, funcName string) string {
	return "cannot prove ensures " + targetName + " => preserve on function " + quoteFactTarget(funcName) + ": target facts may have been " + FactTransformWiden.String() + "ed conservatively by a call"
}

func ensureIncomingTargetUnresolvedMessage(targetName string, funcName string) string {
	return "cannot prove ensures " + targetName + " => preserve on function " + quoteFactTarget(funcName) + ": incoming tracked facts cannot be resolved"
}

func ensurePreserveMismatchMessage(targetName string, funcName string, current string) string {
	return "cannot prove ensures " + targetName + " => preserve on function " + quoteFactTarget(funcName) + ": current tracked facts are " + current
}

func ensureNamedStateTargetMessage(targetName string, funcName string) string {
	return "cannot prove ensures " + targetName + " on function " + quoteFactTarget(funcName) + ": target is not currently a named-state fact-bearing value"
}

func ensureNamedStateMismatchMessage(targetName string, desired string, funcName string, current string) string {
	return "cannot prove ensures " + targetName + " => " + desired + " on function " + quoteFactTarget(funcName) + ": current tracked facts are " + current
}

func ensureRefStateTargetMessage(targetName string, funcName string) string {
	return "cannot prove ensures " + targetName + " on function " + quoteFactTarget(funcName) + ": target is not currently a refstate fact-bearing value"
}

func ensureRefStateMismatchMessage(targetName string, desired string, funcName string, current string) string {
	return "cannot prove ensures " + targetName + " => " + desired + " on function " + quoteFactTarget(funcName) + ": current tracked facts are " + current
}

func quoteFactTarget(value string) string {
	return `"` + value + `"`
}

func (k FactTransformKind) String() string {
	return string(k)
}

func (c FactClass) String() string {
	return string(c)
}
