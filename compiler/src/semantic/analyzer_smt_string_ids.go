package semantic

import (
	"encoding/hex"
	"fmt"
	"strings"

	"elisacore/src/ast"
)

// String identity terms (docs: SMT strings, family #4).
//
// The classifier fragment models a string by a query-local Int "ID": every known literal has its
// own fixed ID, and a symbolic string is a free Int that may equal at most the ID of the literal it
// spells. Only symbolic-vs-literal equalities are ever formed (symbolic pairs decline), so any model
// assigning a symbolic string the ID of its content (or an unused value) satisfies every assertion:
// the encoding is an over-approximation and cannot prove a false literal claim.
//
// Besides the current function's immutable view formals, two more symbolic strings get IDs:
//   - the result of a DIRECT call to a write-free, deterministic callee returning sview
//     (classifierCalleeWriteFree), canonicalized by callee and argument terms; the callee's
//     verified ensure clauses are assumed for it with `result` bound to that ID;
//   - an immutable sview local initialized by such a call, read while its definition is current.
// Callee formals are reached only through explicit env bindings (smtStringIDEnvPrefix), never by
// name in the caller's scope, so a callee formal cannot alias a same-named caller binding.

const smtStringIDEnvPrefix = "\x00strid:"

// smtStringIDBindingPrefix records env[name] as it was when the identity was bound.
const smtStringIDBindingPrefix = "\x00stridat:"

// bindSMTStringID binds name's string identity; call it AFTER env[name] holds its final binding.
func bindSMTStringID(env map[string]string, name, id string) {
	env[smtStringIDEnvPrefix+name] = id
	env[smtStringIDBindingPrefix+name] = env[name]
}

// stringIDTerm returns the ID term of a symbolic string expression, or false.
func (tr *smtTranslator) stringIDTerm(expr ast.Expr, env map[string]string) (string, bool) {
	if tr == nil {
		return "", false
	}
	switch n := stripOptimizationParens(expr).(type) {
	case *ast.Ident:
		if n == nil {
			return "", false
		}
		if term, ok := env[smtStringIDEnvPrefix+n.Name]; ok {
			// A later binder of the same name (match arm, quantifier) replaces env[name]; the
			// identity then belongs to the shadowed formal and must not be used.
			if env[n.Name] != env[smtStringIDBindingPrefix+n.Name] {
				return "", false
			}
			return term, true
		}
		if _, key, ok := tr.classifierStringParameter(n, env); ok {
			return tr.classifierStringParameterInt(key), true
		}
		return tr.stringLocalCallID(n, env)
	case *ast.CallExpr:
		return tr.stringCallID(n, env)
	case *ast.FieldExpr:
		if n == nil {
			return "", false
		}
		if env != nil {
			if term, ok := env[smtStringIDEnvPrefix+smtProjectionName(n)]; ok {
				return term, true
			}
		}
		// A view read from a stable darray[struct] field is identified by the same
		// (container, field path, index) triple as its header. Keep an indexed
		// quantifier binder inside an uninterpreted function application so two
		// reads at the same element remain congruent without assuming bounds or
		// byte contents.
		if base, index, ok := tr.containerViewIndex(n, env); ok {
			return tr.containerViewStringID(base, index), true
		}
		if term, ok := tr.stableStringFieldID(n, env); ok {
			return term, true
		}
		// `(lit, scalar).wasm_type`: a projection of a tuple/struct literal whose component may be
		// re-evaluated in place of the constructed value (literal re-evaluation rules).
		if n == nil || tr.a == nil || depthOfStringIDField(n) > 32 {
			return "", false
		}
		// `result.f` while `result` is substituted by a returned tuple/struct literal: the field's
		// construction expression, in the return site's (caller) names — as constantStringProjection.
		if tr.resultFields != nil && tr.calleeBodyDepth == 0 {
			if id, ok := stripOptimizationParens(n.Object).(*ast.Ident); ok && id.Name == "result" {
				if value, found := tr.resultFields[n.Field]; found && value != nil {
					return tr.stringIDTerm(value, env)
				}
				return "", false
			}
		}
		if value, ok := tr.a.reevaluableLiteralComponent(n, 0); ok && value != nil {
			return tr.stringIDTerm(value, env)
		}
	}
	return "", false
}

func (tr *smtTranslator) stableStringFieldID(field *ast.FieldExpr, env map[string]string) (string, bool) {
	if tr == nil || tr.a == nil || field == nil || tr.a.currentFuncDecl == nil || tr.calleeBodyDepth != 0 ||
		tr.envBindsRoot(field, env) || (!classifierStringFrameStable(tr.a, tr.a.currentFuncDecl) && !tr.a.sviewFrameWriteFree(tr.a.currentFuncDecl)) {
		return "", false
	}
	t := tr.a.exprTypes[field]
	if t == nil {
		if resolved, ok := tr.a.fieldReadResolvedType(field); ok {
			t = resolved
		}
	}
	if !isStringViewType(t) {
		return "", false
	}
	root, _ := projectionChain(field)
	id, ok := root.(*ast.Ident)
	if !ok || id == nil || tr.a.currentScope == nil {
		return "", false
	}
	if smtEnvHasQuantifierBinder(env) {
		return "", false
	}
	sym, found := tr.a.currentScope.Lookup(id.Name)
	if !found || sym == nil || (sym.Kind != SymbolParam && sym.Kind != SymbolLocal) || sym.Mutable || symbolRootMutable(sym) {
		return "", false
	}
	if ref, ok := StripAggregateStateType(sym.Type).(*RefType); ok && (ref.Mutable || ref.Linear) {
		return "", false
	}
	key, ok := tr.fieldProjectionKey(field, env)
	if !ok {
		return "", false
	}
	if tr.stringFieldIDs == nil {
		tr.stringFieldIDs = map[string]string{}
	}
	if term, found := tr.stringFieldIDs[key]; found {
		return term, true
	}
	term := "|@string_field_" + hex.EncodeToString([]byte(key)) + "|"
	tr.stringFieldIDs[key] = term
	tr.auxDecls = append(tr.auxDecls, "(declare-const "+term+" Int)\n")
	return term, true
}

func depthOfStringIDField(n *ast.FieldExpr) int {
	depth := 0
	for cur := ast.Expr(n); cur != nil; depth++ {
		field, ok := stripOptimizationParens(cur).(*ast.FieldExpr)
		if !ok || field == nil {
			break
		}
		cur = field.Object
	}
	return depth
}

// stringLocalCallID: `name: sview = f(args)` (immutable binding, still current at the proof point).
func (tr *smtTranslator) stringLocalCallID(id *ast.Ident, env map[string]string) (string, bool) {
	a := tr.a
	if a == nil || a.currentScope == nil || a.currentFuncDecl == nil || tr.calleeBodyDepth != 0 {
		return "", false
	}
	if _, bound := env[id.Name]; bound {
		return "", false
	}
	if _, bound := env["\x00string:"+id.Name]; bound {
		return "", false
	}
	if !classifierStringFrameStable(a, a.currentFuncDecl) {
		return "", false
	}
	var declScope *Scope
	var sym *Symbol
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		if s, ok := sc.Symbols[id.Name]; ok {
			declScope, sym = sc, s
			break
		}
	}
	if sym == nil || sym.Mutable || sym.Ghost || sym.Kind != SymbolLocal || !isStringViewType(sym.Type) {
		return "", false
	}
	vd, ok := sym.Node.(*ast.VarDeclStmt)
	if !ok || vd == nil || vd.Mutable || vd.Ghost || vd.Owner != nil || vd.Value == nil {
		return "", false
	}
	call, ok := stripOptimizationParens(vd.Value).(*ast.CallExpr)
	if !ok || call == nil || !a.immutableLocalDefinitionCurrent(declScope, sym, vd.Value) {
		return "", false
	}
	return tr.stringCallID(call, nil)
}

// stringCallID models a direct call returning sview from a write-free callee.
func (tr *smtTranslator) stringCallID(call *ast.CallExpr, env map[string]string) (string, bool) {
	a := tr.a
	if a == nil || call == nil || a.currentFuncDecl == nil || tr.calleeBodyDepth != 0 {
		return "", false
	}
	// SView calls currently receive query-local result constants keyed by their
	// argument terms. If an argument term contains a quantifier binder, that
	// result must vary with the binder; a free constant would erase that
	// dependency. Until this path has a binder-parameterized result UF, decline
	// it rather than hoisting a call result out of the quantifier.
	if smtEnvHasQuantifierBinder(env) {
		return "", false
	}
	if !classifierStringFrameStable(a, a.currentFuncDecl) {
		return "", false
	}
	decl, direct := a.classifierFrameCallDecl(call)
	if !direct || decl == nil || decl == a.currentFuncDecl || !a.classifierCalleeWriteFree(decl) || !a.callResultStateIndependent(decl) {
		return "", false
	}
	sym, ok := a.symbolForFuncDecl(decl)
	if !ok || sym == nil {
		return "", false
	}
	fn, ok := sym.Type.(*FuncType)
	if !ok || fn == nil || !isStringViewType(fn.Return) {
		return "", false
	}
	args := proofCallArgs(call)
	if len(args) != len(decl.Params) {
		return "", false
	}
	ensureEnv := map[string]string{}
	keyParts := []string{fmt.Sprintf("strcall|%p", decl)}
	for i, param := range decl.Params {
		arg := args[i]
		if arg == nil {
			return "", false
		}
		pt := fn.Params[i]
		if ref, isRef := pt.(*RefType); isRef && ref != nil {
			pt = ref.Elem
		}
		var term string
		switch {
		case isStringViewType(pt):
			if lit, isLit := stripOptimizationParens(arg).(*ast.StringLit); isLit {
				if strings.ContainsRune(lit.Value, 0) {
					return "", false
				}
				term = smtInt(tr.classifierStringLiteralInt(lit.Value))
			} else {
				term, ok = tr.stringIDTerm(arg, env)
				if !ok {
					return "", false
				}
			}
			bindSMTStringID(ensureEnv, param.Name, term)
		case IsBoolType(pt):
			term, ok = tr.boolTerm(arg, env)
			if !ok {
				return "", false
			}
			ensureEnv[param.Name] = term
		default:
			term, ok = tr.termEnv(arg, env)
			if !ok {
				return "", false
			}
			ensureEnv[param.Name] = term
		}
		keyParts = append(keyParts, term)
	}
	key := strings.Join(keyParts, "|")
	if tr.callCanon == nil {
		tr.callCanon = map[string]string{}
	}
	if term, seen := tr.callCanon[key]; seen {
		return term, true
	}
	ret := tr.freshAux(false)
	tr.callCanon[key] = ret
	guard := "true"
	for _, req := range decl.Requires {
		if req == nil {
			continue
		}
		h, ok := tr.calleeBoolTerm(decl, req, ensureEnv)
		if !ok {
			return ret, true // unknown precondition: assume nothing about the result
		}
		if guard == "true" {
			guard = h
		} else {
			guard = "(and " + guard + " " + h + ")"
		}
	}
	bindSMTStringID(ensureEnv, "result", ret)
	for _, ensure := range decl.EnsureValues {
		if ensure == nil {
			continue
		}
		h, ok := tr.calleeBoolTerm(decl, ensure, ensureEnv)
		if !ok {
			continue // assuming fewer verified postconditions is always sound
		}
		if guard == "true" {
			tr.auxDecls = append(tr.auxDecls, "(assert "+h+")\n")
		} else {
			tr.auxDecls = append(tr.auxDecls, "(assert (=> "+guard+" "+h+"))\n")
		}
	}
	return ret, true
}
