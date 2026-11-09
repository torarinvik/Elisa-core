package semantic

import (
	"elisacore/src/ast"
	"strconv"
	"strings"
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
		if !tr.opaqueCallQuantifierIndependent(r, env) {
			return "", nil, true
		}
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
	if t := a.exprType(n); t != nil {
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

// aggregateProjectionFieldType types a field chain read through a projection token. The token's
// recorded result type comes first: a callee's contract clauses carry expression types only once the
// callee's own body was analyzed, so reading them (or resolving `result` in the current scope, which
// may name the caller's own result) would make the lowering depend on body-analysis order.
func (tr *smtTranslator) aggregateProjectionFieldType(n *ast.FieldExpr, env map[string]string) Type {
	if t, ok := tr.projectionTokenChainType(n, env); ok {
		return t
	}
	if t := tr.a.exprType(n); t != nil {
		return t
	}
	if resolved, ok := tr.a.fieldReadResolvedType(n); ok {
		return resolved
	}
	return nil
}

// projectionTokenChainType resolves the type of expr when it is a field chain whose root is an
// aggregate call or an identifier bound to a call projection token.
func (tr *smtTranslator) projectionTokenChainType(expr ast.Expr, env map[string]string) (Type, bool) {
	switch n := stripOptimizationParens(expr).(type) {
	case *ast.FieldExpr:
		inner, ok := tr.projectionTokenChainType(n.Object, env)
		if !ok || inner == nil {
			return nil, false
		}
		return tr.a.lookupResolvedFieldType(peelNamedStateRefs(inner), n.Field)
	case *ast.Ident:
		bound, ok := env[n.Name]
		if !ok || !strings.HasPrefix(bound, dtTokenPrefix) {
			return nil, false
		}
		t, ok := tr.callProjectionTypes[strings.TrimPrefix(bound, dtTokenPrefix)]
		return t, ok && t != nil
	case *ast.CallExpr:
		decl, direct := tr.a.resolveDirectCallFuncDecl(n)
		decl = tr.a.readDecl(decl)
		if !direct || decl == nil {
			return nil, false
		}
		if sym, ok := tr.a.symbolForFuncDecl(decl); ok {
			if fnType, ok := sym.Type.(*FuncType); ok && fnType != nil && fnType.Return != nil && len(decl.GenericParams) == 0 {
				return fnType.Return, true
			}
		}
	}
	return nil, false
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
	if key == "" {
		return "", false, true
	}
	fieldType := tr.aggregateProjectionFieldType(n, env)
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
	if key == "" {
		return "", false, true
	}
	fieldType := tr.aggregateProjectionFieldType(n, env)
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
	if !tr.opaqueCallQuantifierIndependent(call, env) {
		return "", false
	}
	decl, direct := tr.a.resolveDirectCallFuncDecl(call)
	decl = tr.a.readDecl(decl)
	if !direct || decl == nil {
		return "", false
	}
	resultType := tr.a.exprType(call)
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
	if tr.callProjectionTypes == nil {
		tr.callProjectionTypes = map[string]Type{}
	}
	tr.callProjectionTypes[token] = resultType
	subst := map[string]ast.Expr{}
	for i, param := range decl.Params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		subst[param.Name] = args[i]
	}
	tr.assumeAggregateCallEnsures(decl, subst, env, token)
	tr.assumeGuardPrefixReturns(decl, subst, env, token)
	tr.emitAggregateCallFieldEquations(call, decl, args, env, token, canonKey)
	tr.emitProjectionCongruence(call, decl, env, token)
	return token, true
}
