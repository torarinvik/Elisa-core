package semantic

import "strings"

// A stable container can also have an explicitly declared flat Int-array model.
// Ghost call keys retain its identity, while element reads use that same array.
// Opaque container tokens without a flat-array declaration must still decline.
func (tr *smtTranslator) declaredFlatArrayTerm(term string) bool {
	name, ok := strings.CutPrefix(term, smtVar(""))
	return ok && tr.arrayDecls[name] && smtVar(name) == term
}
