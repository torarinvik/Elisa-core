package semantic

import (
	"strconv"
	"strings"

	"elisacore/src/ast"
)

type smtSViewSliceKey struct {
	array, offset, length string
}

func (tr *smtTranslator) envBindsViewRoot(id *ast.Ident, env map[string]string) bool {
	if tr.envBindsRoot(id, env) {
		return true
	}
	// A formal can be bound by projections alone (notably a literal's .len). It must never
	// resolve through a same-spelled caller local just because no whole-array binding exists.
	for name := range env {
		if strings.HasPrefix(name, id.Name+".") {
			return true
		}
	}
	return false
}

func (tr *smtTranslator) sviewSliceLocalArrayTerm(id *ast.Ident, env map[string]string) (string, bool) {
	if tr.calleeBodyDepth != 0 || tr.envBindsViewRoot(id, env) {
		return "", false
	}
	slice, ok := tr.a.immutableSliceLocalInit(id)
	if !ok {
		return "", false
	}
	return tr.sviewSliceArrayTerm(slice, env)
}

// A slice of an authorized immutable byte frame carries its clamped window into a callee.
// The array's bytes outside that window stay unconstrained. This models contents only, never
// pointer identity, and retains the length-only fallback when the base frame is unavailable.
func (tr *smtTranslator) sviewSliceArrayTerm(slice *ast.SliceExpr, env map[string]string) (string, bool) {
	base, offset, length, ok := tr.sviewContentParts(slice, env)
	if !ok {
		return "", false
	}
	return tr.sviewWindowArrayTerm(base, offset, length)
}

// sviewWindowArrayTerm gives a view a zero-based byte array while preserving
// its relationship to the source backing array. It is shared by source slices
// and verified projections such as an sview stored in a stable container.
func (tr *smtTranslator) sviewWindowArrayTerm(base, offset, length string) (string, bool) {
	if base == "" || offset == "" || length == "" || smtTermMentionsQuantifierBinder(base) ||
		smtTermMentionsQuantifierBinder(offset) || smtTermMentionsQuantifierBinder(length) {
		return "", false
	}
	key := smtSViewSliceKey{base, offset, length}
	if arr, found := tr.sviewSliceArrays[key]; found {
		return arr, true
	}
	if tr.sviewSliceArrays == nil {
		tr.sviewSliceArrays = map[smtSViewSliceKey]string{}
	}
	name := "@slice$" + strconv.Itoa(len(tr.sviewSliceArrays))
	arr := smtVar(name)
	tr.sviewSliceArrays[key] = arr
	tr.arrayDecls[name] = true
	lenSym := arr + smtArrayLenSuffix
	tr.lenDecls[lenSym] = true
	tr.enumDecls["~slice_len_"+name] = "(assert (= " + lenSym + " " + length + "))\n"
	// This binder is local to the generated axiom. Unlike a source quantifier's q_* symbols,
	// it must not escape into the window key or be mistaken for an unbound source binder.
	index := "|@slice_index|"
	tr.auxDecls = append(tr.auxDecls, "(assert (forall (("+index+" Int)) (=> (and (<= 0 "+index+") (< "+index+" "+length+")) (= (select "+arr+" "+index+") (select "+base+" (+ "+offset+" "+index+"))))))\n")
	return arr, true
}
