package semantic

import (
	"fmt"
	"sort"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// interfaceMethodBoolSym models a bool-returning PROTOCOL-method call over a type-parameter receiver
// (`x.le(y)` inside `def sort[T: Ord]`) as an uninterpreted predicate: a stable Bool symbol keyed by the
// protocol+method and the canonical names of its receiver/argument idents. It applies ONLY when every
// operand is a plain identifier whose type is a protocol-bounded type parameter — exactly the shape the
// law-fact injection and the generic goal share — so it never perturbs concrete-type proofs. Returns
// ok=false otherwise (the caller then takes the normal path).
func (tr *smtTranslator) interfaceMethodBoolSym(call *ast.CallExpr, env map[string]string) (string, bool) {
	if tr == nil || tr.a == nil || call == nil {
		return "", false
	}
	sel, ok := call.Func.(*ast.FieldExpr)
	if !ok || sel == nil {
		return "", false
	}
	// Two shapes reach here: the UFCS form `x.le(y)` (receiver = sel.Object, args = call.Args) and the
	// qualified static form `T.le(x, y)` — the analyzer rewrites a bound type-param method call to this,
	// so sel.Object is the type-param name `T` and ALL value operands are call.Args. Detect the latter by
	// sel.Object naming the protocol-bounded type parameter itself.
	var ifaceName string
	var operands []ast.Expr
	if ifn, ok := tr.a.qualifiedTypeParamMethodInterface(sel.Object, sel.Field); ok {
		ifaceName = ifn
		operands = call.Args
	} else if ifn, ok := tr.a.protocolMethodReceiverInterface(sel.Object, sel.Field); ok {
		ifaceName = ifn
		operands = append([]ast.Expr{sel.Object}, call.Args...)
	} else {
		return "", false
	}
	parts := make([]string, 0, len(operands)+2)
	parts = append(parts, ifaceName, sel.Field)
	for _, op := range operands {
		name, ok := lawOperandIdentName(op, env)
		if !ok {
			return "", false
		}
		parts = append(parts, name)
	}
	// Hex components are injective even for qualified names and substituted SMT
	// terms. Build the final symbol once, avoiding per-component formatted copies.
	size := len("b_@iface") + len(parts)
	for _, part := range parts {
		size += 2 * len(part)
	}
	var symbol strings.Builder
	symbol.Grow(size)
	symbol.WriteString("b_@iface")
	const hex = "0123456789abcdef"
	for _, part := range parts {
		symbol.WriteByte('.')
		for i := 0; i < len(part); i++ {
			symbol.WriteByte(hex[part[i]>>4])
			symbol.WriteByte(hex[part[i]&15])
		}
	}
	sym := symbol.String()
	tr.boolDecls[sym] = true
	return sym, true
}

// lawOperandIdentName returns a stable canonical name for an operand that is a plain identifier,
// resolving through the quantifier/substitution env so a bound `a` and the param it maps to share a key.
func lawOperandIdentName(expr ast.Expr, env map[string]string) (string, bool) {
	id, ok := stripOptimizationParens(expr).(*ast.Ident)
	if !ok || id == nil {
		return "", false
	}
	if env != nil {
		if bound, ok := env[id.Name]; ok {
			// A `T`-typed operand resolves through the env to a DATATYPE-projection token (`__dt__a`), not
			// an SMT term. The law-hypothesis side instantiates the law over the bare parameter idents
			// (env-free ⇒ raw name `a`). Both denote the SAME place, so normalize the dt-token to its bare
			// projection — otherwise the goal symbol (`le___dt__a_a`) and the law symbol (`le_a_a`) diverge
			// and the injected law fact never transports to the goal. This is the chokepoint that connects
			// a `return a` ensure (`result` ↦ `a`) to the law instantiated at `a`.
			return strings.TrimPrefix(bound, dtTokenPrefix), true
		}
	}
	return id.Name, true
}

// protocolMethodReceiverInterface reports the protocol name whose method `method` applies to `recv` when
// recv is an identifier typed as a protocol-bounded type parameter. This is the gate that restricts the
// uninterpreted-predicate model to the generic protocol-law setting.
func (a *Analyzer) protocolMethodReceiverInterface(recv ast.Expr, method string) (string, bool) {
	id, ok := stripOptimizationParens(recv).(*ast.Ident)
	if !ok || id == nil || a.currentScope == nil {
		return "", false
	}
	sym, ok := a.currentScope.Lookup(id.Name)
	if !ok || sym == nil {
		return "", false
	}
	tp, ok := sym.Type.(*TypeParamType)
	if !ok {
		return "", false
	}
	iface, ok := a.lookupTypeParamInterface(tp.Name)
	if !ok || iface == nil {
		return "", false
	}
	if _, ok := iface.Methods[method]; !ok {
		return "", false
	}
	return iface.Name, true
}

// qualifiedTypeParamMethodInterface reports the protocol name for a qualified static method call
// `T.method(...)` where `T` is a protocol-bounded type parameter in scope and the protocol declares
// `method`. This is the canonical lowered shape of a generic `value.method(...)` UFCS call.
func (a *Analyzer) qualifiedTypeParamMethodInterface(owner ast.Expr, method string) (string, bool) {
	id, ok := stripOptimizationParens(owner).(*ast.Ident)
	if !ok || id == nil {
		return "", false
	}
	if _, ok := a.lookupTypeParam(id.Name); !ok {
		return "", false
	}
	iface, ok := a.lookupTypeParamInterface(id.Name)
	if !ok || iface == nil {
		return "", false
	}
	if _, ok := iface.Methods[method]; !ok {
		return "", false
	}
	return iface.Name, true
}

// sanitizeSMTFragment maps a (possibly qualified) name to an SMT-symbol-safe fragment.
func sanitizeSMTFragment(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func (tr *smtTranslator) callCanonKeyEnv(decl *ast.FuncDecl, args []ast.Expr, env map[string]string) (string, bool) {
	if !tr.a.callResultStateIndependent(decl) {
		return "", false
	}
	// Verified self-recursion canonicalizes source arguments in its own lexical
	// frame, rather than the unfolded substitution frame (the IH uses that key).
	if env == nil && decl == tr.a.currentFuncDecl && tr.calleeBodyDecl == decl && tr.a.isRecursiveProofCall(decl) {
		env = make(map[string]string, len(decl.Params))
		for index, param := range decl.Params {
			sym, ok := tr.a.currentScope.Lookup(param.Name)
			if !ok || sym == nil || sym.Kind != SymbolParam || sym.Node != decl || sym.ParamIndex != index {
				return "", false
			}
			if IsBoolType(sym.Type) {
				tr.boolDecls[param.Name] = true
				env[param.Name] = smtBoolVar(param.Name)
			} else if _, numeric := smtNumericValueType(sym.Type); numeric {
				tr.decls[param.Name] = true
				tr.markIntVar(param.Name, sym.Type)
				env[param.Name] = smtVar(param.Name)
			} else {
				env[param.Name] = dtToken(param.Name)
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%p|", decl)
	for _, arg := range args {
		if arg == nil {
			return "", false
		}
		t, ok := tr.callCanonArgKeyEnv(arg, env)
		if !ok {
			return "", false
		}
		b.WriteString(t)
		b.WriteByte('|')
	}
	return b.String(), true
}

func (tr *smtTranslator) callCanonArgKey(arg ast.Expr) (string, bool) {
	return tr.callCanonArgKeyEnv(arg, nil)
}

type smtCallKeyOwnerIndex struct {
	identifiers map[*ast.Ident]struct{}
	locals      map[string][]*ast.VarDeclStmt
}

func (tr *smtTranslator) callKeyOwnerIndex(owner *ast.FuncDecl) *smtCallKeyOwnerIndex {
	if tr == nil || tr.a == nil || owner == nil {
		return nil
	}
	if index := tr.callKeyOwnerIndexes[owner]; index != nil {
		return index
	}
	index := &smtCallKeyOwnerIndex{
		identifiers: map[*ast.Ident]struct{}{},
		locals:      map[string][]*ast.VarDeclStmt{},
	}
	tr.a.walkStaticStmts(owner.Body, func(expr ast.Expr) bool {
		if id, ok := expr.(*ast.Ident); ok && id != nil {
			index.identifiers[id] = struct{}{}
		}
		return false
	})
	for _, stmt := range owner.Body {
		local, ok := stmt.(*ast.VarDeclStmt)
		if ok && local != nil && local.Position.Offset > 0 {
			index.locals[local.Name] = append(index.locals[local.Name], local)
		}
	}
	for _, declarations := range index.locals {
		sort.Slice(declarations, func(i, j int) bool {
			return declarations[i].Position.Offset < declarations[j].Position.Offset
		})
	}
	if tr.callKeyOwnerIndexes == nil {
		tr.callKeyOwnerIndexes = map[*ast.FuncDecl]*smtCallKeyOwnerIndex{}
	}
	tr.callKeyOwnerIndexes[owner] = index
	return index
}

func (tr *smtTranslator) callKeyTopLevelLocal(id *ast.Ident, owner *ast.FuncDecl) *ast.VarDeclStmt {
	if tr == nil || tr.a == nil || id == nil || owner == nil || id.Pos().Offset == 0 || tr.a.currentScope == nil {
		return nil
	}
	index := tr.callKeyOwnerIndex(owner)
	if index == nil {
		return nil
	}
	if _, belongsToOwner := index.identifiers[id]; !belongsToOwner {
		return nil
	}
	declarations := index.locals[id.Name]
	declarationIndex := sort.Search(len(declarations), func(i int) bool {
		return declarations[i].Position.Offset >= id.Pos().Offset
	})
	if declarationIndex == 0 {
		return nil
	}
	declaration := declarations[declarationIndex-1]
	if declaration.Mutable || declaration.Value == nil {
		return nil
	}
	sym, ok := tr.a.currentScope.Lookup(id.Name)
	if !ok || sym == nil || sym.Mutable || sym.Kind != SymbolLocal || sym.Node != declaration {
		return nil
	}
	return declaration
}

func (tr *smtTranslator) callKeyExprIsPure(expr ast.Expr, owner *ast.FuncDecl, active map[*ast.VarDeclStmt]bool) bool {
	if expr == nil || tr == nil || tr.a == nil || owner == nil {
		return false
	}
	switch n := expr.(type) {
	case *ast.IntLit, *ast.BoolLit:
		return true
	case *ast.Ident:
		if tr.a.equationIdentifierIsStable(n, owner) {
			return true
		}
		local := tr.callKeyTopLevelLocal(n, owner)
		if local == nil || active[local] {
			return false
		}
		active[local] = true
		pure := tr.callKeyExprIsPure(local.Value, owner, active)
		delete(active, local)
		return pure
	case *ast.ParenExpr:
		return tr.callKeyExprIsPure(n.Inner, owner, active)
	case *ast.CastExpr:
		return tr.callKeyExprIsPure(n.Operand, owner, active)
	case *ast.IndexExpr:
		return tr.a.pureEquationSViewIndex(n, owner) && tr.callKeyExprIsPure(n.Index, owner, active)
	case *ast.UnaryExpr:
		return n.LoweredCall == nil && n.Op != lexer.TOKEN_AMPERSAND && tr.callKeyExprIsPure(n.Operand, owner, active)
	case *ast.BinaryExpr:
		return n.LoweredCall == nil && tr.callKeyExprIsPure(n.Left, owner, active) && tr.callKeyExprIsPure(n.Right, owner, active)
	case *ast.TernaryExpr:
		return tr.callKeyExprIsPure(n.Cond, owner, active) && tr.callKeyExprIsPure(n.Value, owner, active) && tr.callKeyExprIsPure(n.Alt, owner, active)
	default:
		return false
	}
}

func cloneSMTTermEnv(env map[string]string) map[string]string {
	copy := make(map[string]string, len(env)+4)
	for name, term := range env {
		copy[name] = term
	}
	return copy
}

func (tr *smtTranslator) callKeyBindLocal(local *ast.VarDeclStmt, owner *ast.FuncDecl, env map[string]string, active map[*ast.VarDeclStmt]bool) bool {
	if local == nil || local.Value == nil || active[local] {
		return false
	}
	if _, bound := env[local.Name]; bound {
		return true
	}
	active[local] = true
	defer delete(active, local)
	failed := false
	tr.a.walkStaticExpr(local.Value, func(expr ast.Expr) bool {
		id, ok := stripOptimizationParens(expr).(*ast.Ident)
		if !ok || id == nil {
			return false
		}
		nested := tr.callKeyTopLevelLocal(id, owner)
		if nested != nil && !tr.callKeyBindLocal(nested, owner, env, active) {
			failed = true
			return true
		}
		return false
	})
	if failed || !tr.callKeyExprIsPure(local.Value, owner, map[*ast.VarDeclStmt]bool{local: true}) {
		return false
	}
	var term string
	var ok bool
	if IsBoolType(tr.a.exprType(local.Value)) {
		term, ok = tr.boolTerm(local.Value, env)
	} else if _, isView := stripRefForBounds(tr.a.exprType(local.Value)).(*SViewType); isView {
		term, ok = tr.arrayTermEnv(local.Value, env)
	} else {
		term, ok = tr.termEnv(local.Value, env)
	}
	if !ok {
		return false
	}
	env[local.Name] = term
	return true
}

func (tr *smtTranslator) callCanonArgKeyEnv(arg ast.Expr, env map[string]string) (string, bool) {
	if arg == nil {
		return "", false
	}
	// Immutable views have no scalar SMT term, but call congruence can key them by
	// their already-authorized byte-array binding. This preserves sharing for
	// recursive contract calls without asserting any property of the view contents.
	if _, ok := stripRefForBounds(tr.a.exprType(arg)).(*SViewType); ok {
		return tr.arrayTermEnv(arg, env)
	}
	if t := tr.a.containerArgType(arg); t != nil && !tr.isArrayLike(t) {
		inner := stripOptimizationParens(arg)
		if addr, isAddr := inner.(*ast.AddrOfExpr); isAddr && addr != nil {
			inner = addr.Operand
		}
		if base, ok := tr.containerBase(inner, env); ok {
			return "container:" + base, true
		}
		return "", false
	}
	if id, ok := stripOptimizationParens(arg).(*ast.Ident); ok && id != nil && tr.a.currentFuncDecl != nil {
		if _, bound := env[id.Name]; !bound {
			local := tr.callKeyTopLevelLocal(id, tr.a.currentFuncDecl)
			if local != nil {
				localEnv := cloneSMTTermEnv(env)
				if tr.callKeyBindLocal(local, tr.a.currentFuncDecl, localEnv, map[*ast.VarDeclStmt]bool{}) {
					return localEnv[id.Name], true
				}
			}
		}
	}
	if IsBoolType(tr.a.exprType(arg)) {
		return tr.boolTerm(arg, env)
	}
	// A bound type-param-typed receiver (`c: C`) has no scalar term; key it by its env binding (a
	// dtToken, when substituted) or its projection token. This is what makes an abstract method call
	// keyed by its receiver canonicalize identically on the obligation and caller-fact sides (docs P1).
	if _, ok := stripRefForBounds(tr.a.exprType(arg)).(*TypeParamType); ok {
		if id, ok := arg.(*ast.Ident); ok && env != nil {
			if bound, ok := env[id.Name]; ok {
				return bound, true
			}
		}
		return dtToken(smtProjectionName(arg)), true
	}
	// A stable record root is the same fixed snapshot wherever it is passed. Ghost calls write
	// nothing, so they do not unsettle the snapshot.
	if token, ok := tr.stableRecordRootTokenGate(arg, env, tr.a.sviewFrameWriteFreeGhostReads); ok {
		return "record:" + token, true
	}
	if tr.a.smtEquationStructType(tr.a.exprType(arg)) {
		st, _ := stripRefForBounds(tr.a.exprType(arg)).(*StructType)
		leaves, ok := tr.scalarStructArgLeaves(arg, st, env)
		if !ok {
			return "", false
		}
		var parts []string
		for _, leaf := range leaves {
			parts = append(parts, leaf.path+"="+leaf.term)
		}
		return "struct{" + strings.Join(parts, ",") + "}", true
	}
	// An array-modeled container is the array term the call's env binds it to, so that term
	// identifies it (contents and length) as a call argument.
	if tr.isArrayLike(tr.a.exprType(arg)) {
		if arr, ok := tr.arrayTermEnv(arg, env); ok {
			return "array:" + arr, true
		}
		return "", false
	}
	return tr.termEnv(arg, env)
}

// emitDefiningEquation asserts `ret == body[params:=args]` for a pure, total function, with the body's
// own recursive calls bounded-unrolled (callEqMaxUnroll). It is a NO-OP (sound decline) whenever the
// purity/termination gate fails, the body is not a single pure return, or the body cannot be lowered
// to an integer term.
func (tr *smtTranslator) emitDefiningEquation(call *ast.CallExpr, decl *ast.FuncDecl, ret string, subst map[string]ast.Expr, canonKey, guard string, recursiveCallDomainProven bool) {
	tr.emitDefiningEquationWithEnv(call, decl, ret, subst, canonKey, guard, recursiveCallDomainProven, nil)
}

func (tr *smtTranslator) emitDefiningEquationWithEnv(call *ast.CallExpr, decl *ast.FuncDecl, ret string, subst map[string]ast.Expr, canonKey, guard string, recursiveCallDomainProven bool, envOverride map[string]string) {
	tr.a.addProofReportOrigin(decl)
	if tr == nil || decl == nil {
		return
	}
	if tr.a.isRecursiveProofCall(decl) && !recursiveCallDomainProven {
		return
	}
	if canonKey == "" {
		var ok bool
		canonKey, ok = tr.callCanonKey(decl, proofCallArgs(call))
		if !ok {
			return
		}
	}
	if tr.callEqInFlight[canonKey] >= tr.callEqMaxUnroll {
		return // bounded unroll reached: leave the symbol opaque (still sound)
	}
	// Per-callee depth cap. canonKey embeds the arguments, so genuine recursion f(n)->f(n-1)->... mints a
	// fresh key at every level and callEqInFlight never trips. This per-decl counter bounds how many
	// nested instantiations of the SAME function may be unfolded, so the unfold terminates. Leaving the
	// deeper self-call opaque is sound (a missing defining equation only removes a hypothesis).
	if tr.callEqDeclDepth[decl] >= tr.callEqMaxDeclDepth {
		return
	}
	if !tr.callEquationEligible(decl) {
		return
	}
	if sym, found := tr.a.symbolForFuncDecl(decl); !found || sym == nil {
		return
	} else if fn, isFn := sym.Type.(*FuncType); !isFn || fn == nil || tr.proofParamCount(fn) != len(decl.Params) {
		return
	}
	body, ok := pureReturnBody(decl)
	if !ok {
		return
	}
	env := envOverride
	if env == nil {
		var ok bool
		env, ok = tr.a.smtEnvForSubst(tr, subst)
		if !ok {
			return
		}
	}
	priorDecl := tr.calleeBodyDecl
	tr.calleeBodyDecl = decl
	tr.calleeBodyDepth++
	defer func() { tr.calleeBodyDepth-- }()
	defer func() { tr.calleeBodyDecl = priorDecl }()
	tr.callEqInFlight[canonKey]++
	defer func() { tr.callEqInFlight[canonKey]-- }()
	tr.callEqDeclDepth[decl]++
	defer func() { tr.callEqDeclDepth[decl]-- }()
	bodyTerm, ok := tr.termEnv(body, env)
	if !ok {
		bodyTerm, ok = tr.boolTerm(body, env)
		if !ok {
			return
		}
	}
	eq := "(= " + ret + " " + bodyTerm + ")"
	if guard != "" && guard != "true" {
		eq = "(=> " + guard + " " + eq + ")"
	}
	tr.auxDecls = append(tr.auxDecls, "(assert "+eq+")\n")
	if tr.a.isRecursiveProofCall(decl) {
		if cert, ok := tr.a.recursiveEquationCertificate(decl); ok {
			tr.a.recordProof(call.Pos(), "recursive equation of "+decl.Name+" ("+cert.label()+")", "definition", ProofProvenContract)
		} else {
			tr.a.recordProof(call.Pos(), "recursive equation of "+decl.Name+" (verified termination)", "definition", ProofProvenContract)
		}
	} else {
		tr.a.recordProof(call.Pos(), "defining equation of "+decl.Name, "definition", ProofProvenContract)
	}
}

func (a *Analyzer) isRecursiveProofCall(decl *ast.FuncDecl) bool {
	if a == nil || decl == nil || a.currentFuncDecl == nil {
		return false
	}
	if decl == a.currentFuncDecl {
		return true
	}
	return a.recursiveProofMembers()[decl]
}

// recursiveProofKey is everything direct-call resolution reads (resolveDirectCallFuncDecl): the
// lexical scope chain (by identity and symbol versions) and the name-visibility context, plus the
// declaration the memoized fact is about.
type recursiveProofKey struct {
	root          *ast.FuncDecl
	scope         *Scope
	scopeVersions uint64
	globalVersion uint64
	namespace     string
	usings        string
	funcType      *FuncType
}

func (a *Analyzer) resolutionContextKey(root *ast.FuncDecl) recursiveProofKey {
	key := recursiveProofKey{root: root, scope: a.currentScope, namespace: a.currentNamespace, usings: strings.Join(a.currentUsings, "\x00"), funcType: a.currentFuncType}
	for cur := a.currentScope; cur != nil; cur = cur.Parent {
		key.scopeVersions += cur.symbolsVersion + 1
	}
	if a.globalScope != nil {
		key.globalVersion = a.globalScope.symbolsVersion
	}
	return key
}
