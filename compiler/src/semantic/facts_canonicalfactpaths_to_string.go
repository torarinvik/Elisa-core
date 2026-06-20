package semantic

import (
	"fmt"
	"sort"
)

func canonicalFactPaths(values []FactPath) []FactPath {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]FactPath, 0, len(values))
	for _, value := range values {
		if value.Target == "" || value.Root == "" {
			continue
		}
		key := value.Target + "\x00" + value.Root + "\x00" + value.Path + "\x00" + formatFactPathSteps(value.Steps)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		if out[i].Root != out[j].Root {
			return out[i].Root < out[j].Root
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return formatFactPathSteps(out[i].Steps) < formatFactPathSteps(out[j].Steps)
	})
	return out
}
func regionDependencyKey(region string, before string, after string) string {
	if region == "" || (before == "" && after == "") {
		return ""
	}
	return fmt.Sprintf("%s[%s->%s]", region, emptyFactDetailFallback(before, "?"), emptyFactDetailFallback(after, "?"))
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
	sort.Strings(out)
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
func consumedFactUseMessage(label string, name string, consumedBy string) string {
	return label + " " + quoteFactTarget(name) + " cannot be used: usage facts were " + FactTransformConsume.String() + "d by " + consumedBy
}
func nullableRefRequirementMessage(operation string, actual string) string {
	return operation + " requires an optional or nullable reference (refstate fact nullable), got " + actual
}
func effectAuthorityGrantMessage(label string, missing []string, hint string) string {
	return label + " requires" + permissionFamiliesString(missing) + " and has no explicit local effect grant; add " + hint + " or a surrounding can ...: block; missing required effect facts"
}
func interfaceConformanceFactMessage(typ string, iface string, context string) string {
	message := "type " + quoteFactTarget(typ) + " does not satisfy required interface fact " + quoteFactTarget(iface)
	if context != "" {
		message += " for " + context
	}
	return message
}
func ensureTargetUnresolvedMessage(targetName string, funcName string) string {
	return "cannot prove ensures " + targetName + " on function " + quoteFactTarget(funcName) + ": " + FactTransformEnsure.String() + " fact target cannot be resolved from current tracked facts"
}
func ensurePreserveWidenedMessage(targetName string, funcName string, source string) string {
	message := "cannot prove ensures " + targetName + " => preserve on function " + quoteFactTarget(funcName) + ": target facts may have been " + FactTransformWiden.String() + "ed conservatively by a call"
	if source != "" {
		message += " at " + quoteFactTarget(source)
	}
	return message
}
func ensureIncomingTargetUnresolvedMessage(targetName string, funcName string) string {
	return "cannot prove ensures " + targetName + " => preserve on function " + quoteFactTarget(funcName) + ": incoming tracked facts cannot be resolved"
}
func ensurePreserveMismatchMessage(targetName string, funcName string, current string) string {
	return "cannot prove ensures " + targetName + " => preserve on function " + quoteFactTarget(funcName) + ": current tracked facts are " + current
}

// implicitPreserveMismatchMessage explains a STRICT-PROTOCOL-BALANCE failure: a borrowed `mutable
// T[S]&` parameter was left in a different state than it was declared with, and the function did not
// declare the transition. Either restore the state or add an explicit `ensures` for the new state.
func implicitPreserveMismatchMessage(targetName string, funcName string, declared string, current string) string {
	return "parameter " + targetName + " must be left in its declared state " + declared + " before " + quoteFactTarget(funcName) + " returns, but it is " + current + "; a borrowed `mutable T[S]&` resource must be handed back in the state it was lent in — restore it, or declare the transition with `ensures " + targetName + " => <state>`"
}

func implicitPreserveWidenedMessage(targetName string, funcName string, source string) string {
	message := "parameter " + targetName + " must be left in its declared state before " + quoteFactTarget(funcName) + " returns, but its state was " + FactTransformWiden.String() + "ed by a call"
	if source != "" {
		message += " at " + quoteFactTarget(source)
	}
	message += "; restore it or declare the transition with `ensures " + targetName + " => <state>`"
	return message
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
func CanonicalFactTransforms(transforms []FactTransform) []FactTransform {
	return dedupeAndSortFactTransforms(transforms)
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
