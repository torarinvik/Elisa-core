package semantic

import "strings"
import "sort"

// Absence is the dependency value: adding/changing irrelevant impls preserves
// it, while any potentially matching arithmetic candidate forces cold analysis.
const primitiveOperatorAbsence = "canonical-primitives-no-operator-candidates-v2"

type primitiveOperatorSafety struct {
	collecting      int
	valid, absent   bool
	integer, wide   *BuiltinType
	predicateAbsent map[string]bool
}

func (proof *primitiveOperatorSafety) beginCollection() {
	proof.collecting++
	proof.valid = false
	proof.predicateAbsent = nil
}
func (proof *primitiveOperatorSafety) endCollection() {
	proof.collecting--
	proof.valid = false
	proof.predicateAbsent = nil
}

func (proof *primitiveOperatorSafety) operatorsAbsent(a *Analyzer, integer, wide *BuiltinType) bool {
	if proof.valid && proof.integer == integer && proof.wide == wide {
		return proof.absent
	}
	absent := primitiveOperatorCandidatesAbsent(a, integer, wide)
	if proof.collecting == 0 {
		proof.valid = true
		proof.absent = absent
		proof.integer = integer
		proof.wide = wide
	}
	return absent
}

// predicateComparisonsAbsent checks the exact receiver types on which the
// admitted closed predicate can attempt overloaded comparison dispatch. The
// caller has already checked that every name resolves to its current canonical
// builtin. A sorted name key lets many functions share the scan while collection
// boundaries discard the result whenever the impl registry may have changed.
func (proof *primitiveOperatorSafety) predicateComparisonsAbsent(a *Analyzer, names []string) bool {
	if a == nil {
		return false
	}
	ordered := append([]string(nil), names...)
	ordered = append(ordered, "int") // unsuffixed integer literals and unary -
	ordered = sortStringsUnique(ordered)
	key := strings.Join(ordered, "\x00")
	if proof.collecting == 0 {
		if absent, ok := proof.predicateAbsent[key]; ok {
			return absent
		}
	}
	absent := predicateComparisonCandidatesAbsent(a, ordered)
	if proof.collecting == 0 {
		if proof.predicateAbsent == nil {
			proof.predicateAbsent = make(map[string]bool)
		}
		proof.predicateAbsent[key] = absent
	}
	return absent
}

func predicateComparisonCandidatesAbsent(a *Analyzer, names []string) bool {
	receivers := make([]Type, 0, len(names))
	for _, name := range names {
		builtin, ok := a.namedTypes[name].(*BuiltinType)
		if !ok || builtin == nil || builtin.Name != name {
			return false
		}
		receivers = append(receivers, builtin)
	}
	for _, impl := range a.staticImpls {
		if impl == nil || impl.Receiver == nil || (impl.Methods["__eq__"] == nil && impl.Methods["__cmp__"] == nil) {
			continue
		}
		for _, receiver := range receivers {
			if SameType(impl.Receiver, receiver) {
				return false
			}
		}
	}
	return true
}

func sortStringsUnique(values []string) []string {
	sort.Strings(values)
	write := 0
	for _, value := range values {
		if write != 0 && values[write-1] == value {
			continue
		}
		values[write] = value
		write++
	}
	return values[:write]
}

// Match the exact receiver path used by buildOverloadCall, enumerating every
// candidate instead of interpreting failed/ambiguous resolution as absence.
// Numeric unary minus and span algebra bypass protocol dispatch. Generic
// unification is a different resolver and is not used by these binary operators.
// No method bodies or symbols are persisted in an analysis session.
func primitiveOperatorCandidatesAbsent(a *Analyzer, integer, wide *BuiltinType) bool {
	for _, impl := range a.staticImpls {
		if impl == nil || impl.Receiver == nil {
			continue
		}
		relevant := false
		for _, method := range []string{"__add__", "__sub__", "__mul__"} {
			if impl.Methods[method] != nil {
				relevant = true
				break
			}
		}
		if !relevant {
			continue
		}
		for _, receiver := range []Type{integer, wide} {
			if SameType(impl.Receiver, receiver) {
				return false
			}
		}
	}
	return true
}
