package semantic

// Absence is the dependency value: adding/changing irrelevant impls preserves
// it, while any potentially matching arithmetic candidate forces cold analysis.
const primitiveOperatorAbsence = "canonical-primitives-no-operator-candidates-v2"

type primitiveOperatorSafety struct {
	collecting    int
	valid, absent bool
	integer, wide *BuiltinType
}

func (proof *primitiveOperatorSafety) beginCollection() { proof.collecting++; proof.valid = false }
func (proof *primitiveOperatorSafety) endCollection()   { proof.collecting--; proof.valid = false }

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
