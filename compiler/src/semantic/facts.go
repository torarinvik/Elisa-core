package semantic

import "strings"

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
	FactErrorPath      FactClass = "error-path"
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

type FactTransformSourceKind string

const (
	FactSourceUnknown    FactTransformSourceKind = "unknown"
	FactSourceGuard      FactTransformSourceKind = "guard"
	FactSourceFlowInstr  FactTransformSourceKind = "flow-instr"
	FactSourceSignature  FactTransformSourceKind = "signature"
	FactSourceCallWiden  FactTransformSourceKind = "call-widen"
	FactSourcePermission FactTransformSourceKind = "permission"
	FactSourceRegion     FactTransformSourceKind = "region"
	FactSourceStore      FactTransformSourceKind = "store"
	FactSourceErrorPath  FactTransformSourceKind = "error-path"
	FactSourceReturn     FactTransformSourceKind = "return"
)

type FactTransformDetail struct {
	Name  string
	Value string
}

// FactTransform is a lightweight descriptive record used by diagnostics,
// reports, and future analyzer cleanup work. It intentionally does not own the
// fact payload yet; existing precise structures remain the source of truth until
// each subsystem migrates onto the shared model.
type FactTransform struct {
	Kind       FactTransformKind
	Classes    []FactClass
	Target     string
	Source     string
	SourceKind FactTransformSourceKind
	Details    []FactTransformDetail
	Reason     string
}

type FactSnapshot struct {
	Parameters         []string
	Returns            []string
	Consumed           []string
	Produced           []string
	InvalidatedRegions []string
	RebasedStores      []string
	RequiredEffects    []string
	Ensured            []string
	Refined            []string
	Widened            []string
	ErrorExits         []string
	StoreDeps          []string
}

type RefinementFacts = GuardFactSet

func NewRefinementFacts() RefinementFacts {
	return NewGuardFactSet()
}

func FormatFactTransforms(transforms []FactTransform) string {
	if len(transforms) == 0 {
		return ""
	}
	parts := make([]string, 0, len(transforms))
	for _, transform := range transforms {
		if text := FormatFactTransform(transform); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

func FormatFactTransform(transform FactTransform) string {
	if transform.Kind == "" {
		return ""
	}
	var out strings.Builder
	out.WriteString(transform.Kind.String())
	if transform.Target != "" {
		out.WriteByte(' ')
		out.WriteString(transform.Target)
	}
	if len(transform.Classes) != 0 {
		classes := make([]string, 0, len(transform.Classes))
		for _, class := range transform.Classes {
			classes = append(classes, class.String())
		}
		out.WriteString(" [")
		out.WriteString(strings.Join(classes, ","))
		out.WriteByte(']')
	}
	if transform.Source != "" {
		out.WriteString(" <- ")
		out.WriteString(transform.Source)
	}
	if len(transform.Details) != 0 {
		details := make([]string, 0, len(transform.Details))
		for _, detail := range transform.Details {
			if detail.Name == "" || detail.Value == "" {
				continue
			}
			details = append(details, detail.Name+"="+detail.Value)
		}
		if len(details) != 0 {
			out.WriteString(" {")
			out.WriteString(strings.Join(details, ","))
			out.WriteByte('}')
		}
	}
	if transform.Reason != "" {
		out.WriteString(" (")
		out.WriteString(transform.Reason)
		out.WriteByte(')')
	}
	return out.String()
}

func FormatFactTransformGroups(transforms []FactTransform) string {
	groups := GroupFactTransforms(transforms)
	if len(groups) == 0 {
		return ""
	}
	labels := []struct {
		Kind  FactTransformKind
		Label string
	}{
		{FactTransformRequire, "requires"},
		{FactTransformEnsure, "ensures"},
		{FactTransformRefine, "refines"},
		{FactTransformWiden, "widens"},
		{FactTransformProduce, "produces"},
		{FactTransformConsume, "consumes"},
		{FactTransformInvalidate, "invalidates"},
		{FactTransformRebase, "rebases"},
		{FactTransformRecompute, "recomputes"},
	}
	lines := make([]string, 0, len(labels))
	for _, label := range labels {
		items := groups[label.Kind]
		if len(items) == 0 {
			continue
		}
		parts := make([]string, 0, len(items))
		for _, item := range items {
			if text := FormatFactTransform(item); text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) != 0 {
			lines = append(lines, label.Label+": "+strings.Join(parts, "; "))
		}
	}
	return strings.Join(lines, "\n")
}

func GroupFactTransforms(transforms []FactTransform) map[FactTransformKind][]FactTransform {
	if len(transforms) == 0 {
		return nil
	}
	groups := map[FactTransformKind][]FactTransform{}
	for _, transform := range transforms {
		if transform.Kind == "" {
			continue
		}
		groups[transform.Kind] = append(groups[transform.Kind], transform)
	}
	if len(groups) == 0 {
		return nil
	}
	return groups
}

func FormatFactSnapshot(snapshot FactSnapshot) string {
	parts := make([]string, 0, 11)
	appendPart := func(label string, values []string) {
		values = canonicalStringList(values)
		if len(values) != 0 {
			parts = append(parts, label+"=["+strings.Join(values, ", ")+"]")
		}
	}
	appendPart("params", snapshot.Parameters)
	appendPart("returns", snapshot.Returns)
	appendPart("consumed", snapshot.Consumed)
	appendPart("produced", snapshot.Produced)
	appendPart("invalidated_regions", snapshot.InvalidatedRegions)
	appendPart("rebased_stores", snapshot.RebasedStores)
	appendPart("required_effects", snapshot.RequiredEffects)
	appendPart("ensured", snapshot.Ensured)
	appendPart("refined", snapshot.Refined)
	appendPart("widened", snapshot.Widened)
	appendPart("error_exits", snapshot.ErrorExits)
	appendPart("store_deps", snapshot.StoreDeps)
	return strings.Join(parts, " ")
}

func canonicalStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
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

func (k FactTransformSourceKind) String() string {
	return string(k)
}
