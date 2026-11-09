package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"strconv"
	"strings"
)

// aggregateEquationMaxDeclDepth unfolds a record-returning function once per query. Its nested
// calls stay projection tokens, related to other calls through congruence; a second level
// multiplies the query by the body's call fan-out without adding facts a single step needs.
const aggregateEquationMaxDeclDepth = 1

// emitProjectionCongruence reads each scalar field of a record-result token through an
// uninterpreted function of the call's argument terms, one function per callee, argument
// identity and field. Two calls whose argument terms the solver proves equal then have equal
// fields, e.g. a recursive call exposed by unfolding a definition and the same call written at
// the proof point. A call is deterministic in its arguments (the premise of canonical tokens),
// so the equation is sound; it costs one assertion per field.
func (tr *smtTranslator) emitProjectionCongruence(call *ast.CallExpr, decl *ast.FuncDecl, env map[string]string, token string) {
	sym, ok := tr.a.symbolForFuncDecl(decl)
	if !ok || sym == nil {
		return
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || len(fnType.Params) != len(proofCallArgs(call)) || len(decl.Params) != len(fnType.Params) {
		return
	}
	fields, ok := tr.a.smtEquationRecordFields(fnType.Return)
	if !ok {
		return
	}
	identity, sorts, terms, ok := tr.scalarCallArgTerms(call, decl, fnType, env)
	if !ok || len(terms) == 0 {
		return
	}
	if tr.scalarCallUFNames == nil {
		tr.scalarCallUFNames = map[string]string{}
	}
	args := strings.Join(terms, " ")
	for _, field := range fields {
		name := token + smtFieldSeparator + field.Name
		var result, sort string
		if IsBoolType(stripRefForBounds(field.Type)) {
			result, sort = smtBoolVar(name), "Bool"
			tr.boolDecls[result] = true
		} else {
			result, sort = smtVar(name), "Int"
			tr.decls[name] = true
			tr.markIntVar(name, field.Type)
		}
		key := fmt.Sprintf("%p|proj|%s|", decl, field.Name) + identity
		fn, declared := tr.scalarCallUFNames[key]
		if !declared {
			fn = "|@proj_call_" + strconv.Itoa(len(tr.scalarCallUFNames)) + "_" + sort + "|"
			tr.scalarCallUFNames[key] = fn
			tr.auxDecls = append(tr.auxDecls, "(declare-fun "+fn+" ("+strings.Join(sorts, " ")+") "+sort+")\n")
		}
		tr.auxDecls = append(tr.auxDecls, "(assert (= "+result+" ("+fn+" "+args+")))\n")
	}
}

// emitAggregateCallFieldEquations adds a bounded defining equation for each supported scalar
// projection of a pure, total record-returning function. The aggregate itself remains an opaque
// projection token; only direct bool/integer fields are related to the function's pure return tree.
func (tr *smtTranslator) emitAggregateCallFieldEquations(call *ast.CallExpr, decl *ast.FuncDecl, args []ast.Expr, argEnv map[string]string, token, canonKey string) {
	if tr == nil || tr.a == nil || call == nil || decl == nil || token == "" {
		return
	}
	if !tr.callEquationEligible(decl) && !tr.a.aggregateRecordDefiningEquationEligible(decl) {
		return
	}
	// Recursive calls in the function currently being proved need a termination certificate at this
	// exact call site. The ordinary scalar path has a dedicated certificate/IH route; keep this
	// aggregate projection path conservative there. Calls to an already-verified recursive helper
	// from another function are unfolded below under the existing depth and unroll bounds.
	if tr.a.isRecursiveProofCall(decl) {
		return
	}
	fn, ok := tr.a.symbolForFuncDecl(decl)
	if !ok || fn == nil {
		return
	}
	fnType, ok := fn.Type.(*FuncType)
	if !ok || fnType == nil || len(fnType.Params) != len(args) {
		return
	}
	fields, ok := tr.a.smtEquationRecordFields(fnType.Return)
	if !ok {
		return
	}
	body, ok := tr.a.typedPureReturnExpr(decl)
	if !ok || body == nil {
		return
	}
	// A call without a stable argument key still has a unique token. It may receive one bounded
	// expansion, but recursive re-entry is stopped by the per-declaration depth cap below.
	if canonKey == "" {
		canonKey = token
	}
	if tr.callEqInFlight[canonKey] >= tr.callEqMaxUnroll || tr.callEqDeclDepth[decl] >= aggregateEquationMaxDeclDepth {
		return
	}
	if tr.callEqInFlight == nil {
		tr.callEqInFlight = map[string]int{}
	}
	if tr.callEqDeclDepth == nil {
		tr.callEqDeclDepth = map[*ast.FuncDecl]int{}
	}
	subst := map[string]ast.Expr{}
	for i, param := range decl.Params {
		subst[param.Name] = args[i]
	}
	priorBinding := tr.bindingCallee
	tr.bindingCallee = decl
	env, ok := tr.a.smtEnvForCallSubstEnv(tr, subst, argEnv)
	tr.bindingCallee = priorBinding
	if !ok {
		return
	}
	guard := "true"
	for _, req := range decl.Requires {
		if req == nil {
			continue
		}
		h, ok := tr.calleeBoolTerm(decl, req, env)
		if !ok {
			return
		}
		if guard == "true" {
			guard = h
		} else {
			guard = "(and " + guard + " " + h + ")"
		}
	}
	tr.callEqInFlight[canonKey]++
	tr.callEqDeclDepth[decl]++
	priorDecl, priorDepth := tr.calleeBodyDecl, tr.calleeBodyDepth
	tr.calleeBodyDecl, tr.calleeBodyDepth = decl, priorDepth+1
	defer func() {
		tr.calleeBodyDecl, tr.calleeBodyDepth = priorDecl, priorDepth
		tr.callEqInFlight[canonKey]--
		tr.callEqDeclDepth[decl]--
	}()
	tr.a.exprTypes[body] = fnType.Return
	type projectedField struct {
		field TupleField
		term  string
	}
	projected := make([]projectedField, 0, len(fields))
	for _, field := range fields {
		name := field.Name
		fieldType := stripRefForBounds(field.Type)
		fieldExpr := &ast.FieldExpr{Position: body.Pos(), Object: body, Field: name}
		tr.a.exprTypes[fieldExpr] = field.Type
		var value string
		if IsBoolType(fieldType) {
			value, ok = tr.boolTerm(fieldExpr, env)
		} else {
			value, ok = tr.termEnv(fieldExpr, env)
		}
		if !ok {
			return
		}
		projected = append(projected, projectedField{field: field, term: value})
	}
	for _, projection := range projected {
		field := projection.field
		fieldType := stripRefForBounds(field.Type)
		fieldKey := token + smtFieldSeparator + field.Name
		var result string
		if IsBoolType(fieldType) {
			result = smtBoolVar(fieldKey)
			tr.boolDecls[result] = true
		} else {
			result = smtVar(fieldKey)
			tr.decls[fieldKey] = true
			tr.markIntVar(fieldKey, field.Type)
		}
		equation := "(= " + result + " " + projection.term + ")"
		if guard != "true" {
			equation = "(=> " + guard + " " + equation + ")"
		}
		tr.auxDecls = append(tr.auxDecls, "(assert "+equation+")\n")
	}
}

// assumeAggregateCallEnsures asserts decl's ensures with `result` bound to the projection token.
// Every gate only withholds hypotheses.
func (tr *smtTranslator) assumeAggregateCallEnsures(decl *ast.FuncDecl, subst map[string]ast.Expr, argEnv map[string]string, token string) {
	if len(decl.EnsureValues) == 0 || tr.a.isRecursiveProofCall(decl) || decl == tr.a.currentFuncDecl {
		return
	}
	if tr.callClauseDepth >= smtCallClauseMaxDepth || tr.callClauseExpansions >= smtCallClauseMaxExpansions || tr.callClauseInFlight[decl] {
		return
	}
	tr.callClauseExpansions++
	tr.callClauseDepth++
	tr.callClauseInFlight[decl] = true
	defer func() {
		tr.callClauseDepth--
		delete(tr.callClauseInFlight, decl)
	}()
	env, ok := tr.a.smtEnvForCallSubstEnv(tr, subst, argEnv)
	if !ok {
		return
	}
	guard := "true"
	for _, req := range decl.Requires {
		if req == nil {
			continue
		}
		h, ok := tr.calleeBoolTerm(decl, req, env)
		if !ok {
			return
		}
		if guard == "true" {
			guard = h
		} else {
			guard = "(and " + guard + " " + h + ")"
		}
	}
	ensureEnv := make(map[string]string, len(env)+1)
	for name, term := range env {
		ensureEnv[name] = term
	}
	ensureEnv["result"] = dtToken(token)
	for _, ensure := range decl.EnsureValues {
		if ensure == nil {
			continue
		}
		h, ok := tr.calleeBoolTerm(decl, ensure, ensureEnv)
		if !ok {
			continue
		}
		if guard == "true" {
			tr.auxDecls = append(tr.auxDecls, "(assert "+h+")\n")
		} else {
			tr.auxDecls = append(tr.auxDecls, "(assert (=> "+guard+" "+h+"))\n")
		}
	}
}

// smtAggregateCallLocalEnsures assumes, for the immutable aggregate local `name` initialized by a
// direct call whose definition is still current, the callee's ensures over the local's own
// projections.
func (a *Analyzer) smtAggregateCallLocalEnsures(tr *smtTranslator, declScope *Scope, sym *Symbol, vd *ast.VarDeclStmt) {
	if vd == nil || vd.Value == nil || !aggregateCallResultType(sym.Type) {
		return
	}
	call, ok := stripOptimizationParens(vd.Value).(*ast.CallExpr)
	if !ok || call == nil {
		return
	}
	decl, direct := a.resolveDirectCallFuncDecl(call)
	decl = a.readDecl(decl)
	if !direct || decl == nil || len(decl.EnsureValues) == 0 {
		return
	}
	if !aggregateCallResultType(a.exprType(call)) || !a.immutableLocalDefinitionCurrent(declScope, sym, vd.Value) {
		return
	}
	args := proofCallArgs(call)
	subst := map[string]ast.Expr{}
	for i, param := range decl.Params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		subst[param.Name] = args[i]
	}
	tr.assumeAggregateCallEnsures(decl, subst, nil, localProjectionTokenPrefix+sym.Name)
}

// immutableLiteralLocal resolves `missing` in `missing.owner.value` to its constructor literal when
// it is an immutable local of the current function initialized by a tuple/struct literal whose
// definition is still current (same bindings, none written since): the projection then reads the
// literal's component, as for `(false, missing).known`. Only at caller level (no callee frame, name
// not substituted by the env).
func (tr *smtTranslator) immutableLiteralLocal(id *ast.Ident, env map[string]string) (ast.Expr, bool) {
	if tr == nil || tr.a == nil || id == nil || tr.calleeBodyDepth != 0 || tr.a.currentScope == nil {
		return nil, false
	}
	if _, bound := env[id.Name]; bound {
		return nil, false
	}
	var declScope *Scope
	var sym *Symbol
	for sc := tr.a.currentScope; sc != nil; sc = sc.Parent {
		if found, ok := sc.Symbols[id.Name]; ok {
			declScope, sym = sc, found
			break
		}
	}
	if sym == nil || sym.Mutable || sym.Kind != SymbolLocal {
		return nil, false
	}
	vd, ok := sym.Node.(*ast.VarDeclStmt)
	if !ok || vd == nil || vd.Value == nil {
		return nil, false
	}
	switch literal := stripOptimizationParens(vd.Value).(type) {
	case *ast.StructLitExpr:
		if literal.CopyBase != nil || len(literal.Spreads) != 0 {
			return nil, false
		}
	case *ast.TupleExpr:
	default:
		return nil, false
	}
	if !tr.a.immutableLocalDefinitionCurrent(declScope, sym, vd.Value) {
		return nil, false
	}
	return stripOptimizationParens(vd.Value), true
}

// rerootBelowFirstField rewrites `root.f.g…` (at least two fields deep) to `V.g…`, where V is
// fields[f]. ok=false for a one-field chain (handled by the caller's direct lookup) or a missing f.
func rerootBelowFirstField(n *ast.FieldExpr, fields map[string]ast.Expr, a *Analyzer) (*ast.FieldExpr, bool) {
	inner, ok := stripOptimizationParens(n.Object).(*ast.FieldExpr)
	if !ok || inner == nil {
		return nil, false
	}
	if _, rootIdent := stripOptimizationParens(inner.Object).(*ast.Ident); rootIdent {
		value, found := fields[inner.Field]
		if !found || value == nil {
			return nil, false
		}
		out := &ast.FieldExpr{Position: n.Position, Object: value, Field: n.Field}
		if t := a.exprType(n); t != nil {
			a.exprTypes[out] = t
		}
		return out, true
	}
	rebuilt, ok := rerootBelowFirstField(inner, fields, a)
	if !ok {
		return nil, false
	}
	out := &ast.FieldExpr{Position: n.Position, Object: rebuilt, Field: n.Field}
	if t := a.exprType(n); t != nil {
		a.exprTypes[out] = t
	}
	return out, true
}
