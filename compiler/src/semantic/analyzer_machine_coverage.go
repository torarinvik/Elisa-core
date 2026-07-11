package semantic

import (
	"strings"

	"elisacore/src/ast"
)

// checkMachineCoverage enforces the CLOSED-ENUM input-totality rule for `machine over`
// (docs/125 §9 Tier 2). The parser already diagnoses an open-domain state (char/int/range
// arms) missing its `_` — it can prove that without types. This handles the case only the
// type reveals: a machine whose input is a closed const enum (classified dispatch). There,
// every state must spell all the enum's variants as explicit tags — a missing variant is a
// hard error (the add-a-variant safety net) — and an unguarded `_` is REJECTED, because on a
// closed domain a wildcard is exactly what erases that net: "spell the tags."
func (a *Analyzer) checkMachineCoverage(stmt *ast.MachineCoverageStmt) {
	if stmt == nil {
		return
	}
	constEnum, isConstEnum := underlyingConstEnum(a.exprTypes[stmt.Input])
	if !isConstEnum {
		return // open-domain totality is the parser's early check
	}
	for _, st := range stmt.States {
		a.checkMachineCoverageClosedEnum(stmt, st, constEnum)
	}
}

func (a *Analyzer) checkMachineCoverageClosedEnum(stmt *ast.MachineCoverageStmt, st ast.MachineCoverageState, enum *ConstEnumType) {
	if st.HasWildcard {
		a.errorf(st.Position, "machine state %q dispatches on the closed enum %q with a `_` wildcard — spell the variants as explicit tags instead, so adding a variant to %q forces this machine to handle it (docs/125 §9)", st.Name, enum.Name, enum.Name)
		return
	}
	covered := map[string]bool{}
	for _, tag := range st.Tags {
		covered[tag] = true
	}
	var missing []string
	for _, m := range enum.Members {
		if !covered[m.Name] {
			missing = append(missing, enum.Name+"."+m.Name)
		}
	}
	if len(missing) > 0 {
		a.errorf(st.Position, "machine state %q does not cover all variants of the closed enum %q; missing: %s (docs/125 §9)", st.Name, enum.Name, strings.Join(missing, ", "))
	}
}

// underlyingConstEnum unwraps a type to a *ConstEnumType if that is what it is.
func underlyingConstEnum(t Type) (*ConstEnumType, bool) {
	ce, ok := t.(*ConstEnumType)
	return ce, ok
}

