package semantic

import (
	"elisacore/src/lexer"
	"strings"
)

// Privacy is semantic metadata only: fields retain their normal layout and ABI.
func (a *Analyzer) checkPrivateStructField(st *StructType, name string, pos lexer.Pos) {
	if st == nil || st.Decl == nil || pos.Line == 0 {
		return
	}
	owner := st.Namespace
	if a.currentNamespace == owner || (owner != "" && strings.HasPrefix(a.currentNamespace, owner+".")) {
		return
	}
	for _, field := range st.Decl.Fields {
		if field.Name == name && field.Private {
			a.errorf(pos, "field %q of %q is private to module %q", name, st.Name, owner)
			return
		}
	}
}

func (a *Analyzer) checkPrivateFieldOfType(actual Type, name string, pos lexer.Pos) {
	actual = StripAggregateStateType(actual)
	if ref, ok := actual.(*RefType); ok {
		actual = ref.Elem
	}
	if generic, ok := actual.(*GenericInstanceType); ok {
		actual = generic.Base
	}
	if st, ok := actual.(*StructType); ok {
		a.checkPrivateStructField(st, name, pos)
	}
}
