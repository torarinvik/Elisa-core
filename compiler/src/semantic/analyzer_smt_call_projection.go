package semantic

import (
	"strconv"
	"strings"

	"elisacore/src/ast"
)

// Field projections of aggregate-valued calls (`parse(text).valid`, `slice(v, 0, n).len`) and of
// immutable aggregate locals initialized by a call (`p: P = parse(text); p.value`).
//
// An aggregate result has no scalar SMT term, so such reads used to be untranslatable and every
// callee `ensure result.f …` was lost at the call site. Each read is now modeled the way a struct
// ARGUMENT already is: the call is named by a projection token (`callproj$N`, canonical per callee +
// argument terms, so equal calls share it), its field reads are symbols under that token
// (`callproj$N.valid`), and the callee's verified ensures are asserted over those symbols
// with `result` bound to the token and the formals bound to the argument terms, guarded by the
// callee's `requires` exactly as for scalar call results (callResultTermEnv).
//
// SOUNDNESS: a call is deterministic in its arguments (the canonicalization callResultTermEnv
// already relies on), so two reads of the same call with equal argument terms denote the same field
// values; a read whose arguments cannot be keyed gets a fresh token (no sharing, still sound). Only
// ensures are assumed (never a defining equation), recursive callees are skipped (their ensures are
// induction hypotheses that need a certificate), and the expansion budget of callResultTermEnv
// applies. For a local, the token `local$name` makes `result.f` read the CALLER's own `name.f`
// symbol; it is only issued for an immutable local whose initializer is still current
// (immutableLocalDefinitionCurrent: same bindings, none written since), so the arguments translated
// at the proof point equal the ones the call received.

const callProjectionTokenPrefix = "@callproj$"
const localProjectionTokenPrefix = "@local$"

// aggregateCallResultType reports whether t is an aggregate whose field reads callers project.
func aggregateCallResultType(t Type) bool {
	switch stripRefForBounds(t).(type) {
	case *StructType, *TupleType, *SViewType:
		return true
	}
	return false
}

// projectionChain splits `root.f.g` into its root expression and field path (".f.g").
func projectionChain(n *ast.FieldExpr) (ast.Expr, string) {
	var fields []string
	var root ast.Expr = n
	for {
		f, ok := stripOptimizationParens(root).(*ast.FieldExpr)
		if !ok || f == nil {
			break
		}
		fields = append(fields, f.Field)
		root = f.Object
	}
	var b strings.Builder
	for i := len(fields) - 1; i >= 0; i-- {
		b.WriteString(smtFieldSeparator)
		b.WriteString(fields[i])
	}
	return stripOptimizationParens(root), b.String()
}

// aggregateProjectionSymbolKey returns the symbol key for a field chain rooted at an aggregate call,
// or at an env binding holding a call/local projection token. ok=false means "not this shape" (the
// caller falls through to its other lowerings); handled=true with ok=false means the shape matched
// but cannot be lowered (decline).
func (tr *smtTranslator) aggregateProjectionKey(n *ast.FieldExpr, env map[string]string) (key string, local *ast.FieldExpr, handled bool) {
	root, path := projectionChain(n)
	switch r := root.(type) {
	case *ast.CallExpr:
		token, ok := tr.callProjectionToken(r, env)
		if !ok {
			return "", nil, false
		}
		return token + path, nil, true
	case *ast.Ident:
		// `result.f.g…` while `result` is substituted by a returned tuple/struct literal: read the
		// chain from the literal's `f` component (re-evaluable at the return, as resultFields
		// already is for `result.f`).
		if r.Name == "result" && tr.resultFields != nil && tr.calleeBodyDepth == 0 && !tr.envBindsRoot(r, env) {
			if rerooted, ok := rerootBelowFirstField(n, tr.resultFields, tr.a); ok {
				return "", rerooted, true
			}
		}
		if literal, ok := tr.immutableLiteralLocal(r, env); ok {
			return "", rerootProjection(n, literal, tr.a), true
		}
		if env == nil {
			return "", nil, false
		}
		bound, ok := env[r.Name]
		if !ok || !strings.HasPrefix(bound, dtTokenPrefix) {
			return "", nil, false
		}
		token := strings.TrimPrefix(bound, dtTokenPrefix)
		if strings.HasPrefix(token, callProjectionTokenPrefix) {
			return token + path, nil, true
		}
		if strings.HasPrefix(token, localProjectionTokenPrefix) {
			name := strings.TrimPrefix(token, localProjectionTokenPrefix)
			return "", rerootProjection(n, &ast.Ident{Position: r.Position, Name: name}, tr.a), true
		}
	}
	return "", nil, false
}

// rerootProjection rebuilds the field chain n over a new root, carrying the analyzed field types.
func rerootProjection(n *ast.FieldExpr, root ast.Expr, a *Analyzer) *ast.FieldExpr {
	object := stripOptimizationParens(n.Object)
	var rebuilt ast.Expr = root
	if inner, ok := object.(*ast.FieldExpr); ok && inner != nil {
		rebuilt = rerootProjection(inner, root, a)
	}
	out := &ast.FieldExpr{Position: n.Position, Object: rebuilt, Field: n.Field}
	if t := a.exprTypes[n]; t != nil {
		a.exprTypes[out] = t
	}
	return out
}

// translateLocalProjection lowers a reread caller-local projection in the caller's own frame.
func (tr *smtTranslator) translateLocalProjection(local *ast.FieldExpr, wantBool bool) (string, bool) {
	priorDecl, priorDepth := tr.calleeBodyDecl, tr.calleeBodyDepth
	tr.calleeBodyDecl, tr.calleeBodyDepth = nil, 0
	defer func() { tr.calleeBodyDecl, tr.calleeBodyDepth = priorDecl, priorDepth }()
	if wantBool {
		return tr.boolTerm(local, nil)
	}
	return tr.termEnv(local, nil)
}

// aggregateProjectionTerm lowers an integer-valued aggregate projection (handled=false: not this shape).
func (tr *smtTranslator) aggregateProjectionTerm(n *ast.FieldExpr, env map[string]string) (term string, ok bool, handled bool) {
	key, local, handled := tr.aggregateProjectionKey(n, env)
	if !handled {
		return "", false, false
	}
	if local != nil {
		t, ok := tr.translateLocalProjection(local, false)
		return t, ok, true
	}
	fieldType := tr.a.exprTypes[n]
	if fieldType == nil {
		if resolved, ok := tr.a.fieldReadResolvedType(n); ok {
			fieldType = resolved
		}
	}
	t, numeric := smtNumericValueType(fieldType)
	if !numeric || IsFloatType(t) {
		// An sview projection's `.len` is an i64 even when the node carries no type.
		if !(n.Field == "len" && fieldType == nil) {
			return "", false, true
		}
		t = &BuiltinType{Name: "i64"}
	}
	tr.decls[key] = true
	tr.markIntVar(key, t)
	return smtVar(key), true, true
}

// aggregateProjectionBool lowers a bool-valued aggregate projection.
func (tr *smtTranslator) aggregateProjectionBool(n *ast.FieldExpr, env map[string]string) (term string, ok bool, handled bool) {
	key, local, handled := tr.aggregateProjectionKey(n, env)
	if !handled {
		return "", false, false
	}
	if local != nil {
		t, ok := tr.translateLocalProjection(local, true)
		return t, ok, true
	}
	fieldType := tr.a.exprTypes[n]
	if fieldType == nil {
		if resolved, ok := tr.a.fieldReadResolvedType(n); ok {
			fieldType = resolved
		}
	}
	if !IsBoolType(fieldType) {
		return "", false, true
	}
	pred := smtBoolVar(key)
	tr.boolDecls[pred] = true
	return pred, true, true
}

// callProjectionToken names an aggregate-valued direct call and, the first time, assumes the callee's
// ensures over the token's projections.
func (tr *smtTranslator) callProjectionToken(call *ast.CallExpr, env map[string]string) (string, bool) {
	if tr == nil || tr.a == nil || call == nil {
		return "", false
	}
	decl, direct := tr.a.resolveDirectCallFuncDecl(call)
	if !direct || decl == nil {
		return "", false
	}
	resultType := tr.a.exprTypes[call]
	if resultType == nil {
		if sym, ok := tr.a.symbolForFuncDecl(decl); ok {
			if fnType, ok := sym.Type.(*FuncType); ok && fnType != nil {
				resultType = fnType.Return
			}
		}
	}
	if !aggregateCallResultType(resultType) {
		return "", false
	}
	args := proofCallArgs(call)
	canonKey := ""
	if key, ok := tr.callCanonKeyEnv(decl, args, tr.canonKeyEnv(decl, env)); ok {
		canonKey = "proj|" + key
		if token, seen := tr.callCanon[canonKey]; seen {
			return token, true
		}
	}
	token := callProjectionTokenPrefix + strconv.Itoa(tr.callProjectionCount)
	tr.callProjectionCount++
	if canonKey != "" {
		tr.callCanon[canonKey] = token
	}
	subst := map[string]ast.Expr{}
	for i, param := range decl.Params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		subst[param.Name] = args[i]
	}
	tr.assumeAggregateCallEnsures(decl, subst, env, token)
	tr.assumeGuardPrefixReturns(decl, subst, env, token)
	return token, true
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
	if !direct || decl == nil || len(decl.EnsureValues) == 0 {
		return
	}
	if !aggregateCallResultType(a.exprTypes[call]) || !a.immutableLocalDefinitionCurrent(declScope, sym, vd.Value) {
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
		if t := a.exprTypes[n]; t != nil {
			a.exprTypes[out] = t
		}
		return out, true
	}
	rebuilt, ok := rerootBelowFirstField(inner, fields, a)
	if !ok {
		return nil, false
	}
	out := &ast.FieldExpr{Position: n.Position, Object: rebuilt, Field: n.Field}
	if t := a.exprTypes[n]; t != nil {
		a.exprTypes[out] = t
	}
	return out, true
}
