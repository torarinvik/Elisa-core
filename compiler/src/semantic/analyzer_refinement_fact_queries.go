package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
	"strconv"
	"strings"
)

// isStableWrittenFieldValue reports whether an assigned value can be recorded as a field's last-written
// value: a compile-time constant, or a pure place read (ident or field projection whose key is pure).
// Anything that could observe or cause a side effect, or whose re-evaluation could differ (a call,
// arithmetic, an index), is rejected so a recorded fact never carries an unstable term.
func (a *Analyzer) isStableWrittenFieldValue(value ast.Expr) bool {
	if _, ok := a.evalConstExpr(value); ok {
		return true
	}
	switch n := unwrapParen(value).(type) {
	case *ast.Ident:
		return n != nil
	case *ast.FieldExpr:
		return n != nil && isPureProjectionKey(smtProjectionName(n))
	default:
		return false
	}
}

// invalidateWrittenFieldForWrite drops the field fact(s) a write to `target` invalidates WITHOUT
// recording a new value: a pure field-path target drops only that field (siblings stay); any other
// shape (a bare-ident reassign, an index, a non-pure path) drops the whole root's fields. Used by
// writes whose new value is not a tracked field value (augmented assignment, `as &` stores).
func (a *Analyzer) invalidateWrittenFieldForWrite(target ast.Expr) {
	if name, ok := rootIdentName(unwrapParen(target)); ok {
		for _, r := range a.borrowLaunderedRoots(name, map[string]bool{}) {
			a.invalidateWrittenFieldsForRoot(r)
		}
	}
	if field, ok := unwrapParen(target).(*ast.FieldExpr); ok && field != nil {
		key := smtProjectionName(field)
		if isPureProjectionKey(key) {
			a.invalidateWrittenField(key)
			return
		}
	}
	a.invalidateWrittenFieldsForRoot(rootIdentNameOrEmpty(target))
}

// invalidateWrittenField drops a single field place's last-written fact across the scope chain.
func (a *Analyzer) invalidateWrittenField(key string) {
	if key == "" {
		return
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.writtenField != nil {
			delete(scope.writtenField, key)
		}
	}
}

// lookupWrittenField returns the last-written value expr for a field place (keyed by its projection
// name), if a live fact exists in the active scope chain.
func (a *Analyzer) lookupWrittenField(key string) (ast.Expr, bool) {
	if key == "" {
		return nil, false
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.writtenField != nil {
			if v, ok := scope.writtenField[key]; ok {
				return v, true
			}
		}
	}
	return nil, false
}

// isPureProjectionKey reports whether a projection name was built purely from ident/field components
// (so it round-trips a real field place) rather than smtProjectionName's `expr__…` position fallback.
func isPureProjectionKey(key string) bool {
	return key != "" && !strings.HasPrefix(key, "@expr.")
}

// lookupWrittenStructField returns the construction-time value expr of `name.field` when `name` is a
// local known to hold a struct literal (via writtenStruct) AND the field was given by name in that
// literal. Positional or absent fields decline (sound: no proof, falls to the runtime check).
func (a *Analyzer) lookupWrittenStructField(name, field string) (ast.Expr, bool) {
	if name == "" || field == "" {
		return nil, false
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.writtenStruct == nil {
			continue
		}
		lit, ok := scope.writtenStruct[name]
		if !ok || lit == nil {
			continue
		}
		for i := range lit.Args {
			if lit.ArgName(i) == field {
				return lit.Args[i], true
			}
		}
		return nil, false
	}
	return nil, false
}

// recordWrittenListCount records, for an immutable local initialised from a list literal, the number
// of elements as a compile-time fact.  Only call for immutable bindings; mutable darrays must not
// be recorded because push/pop invalidation is not tracked here.
func (a *Analyzer) recordWrittenListCount(name string, count int64) {
	if name == "" || a.currentScope == nil {
		return
	}
	if a.currentScope.writtenListCount == nil {
		a.currentScope.writtenListCount = map[string]int64{}
	}
	a.currentScope.writtenListCount[name] = count
}

// lookupWrittenListCount returns the known element count of an immutable darray local whose
// initialiser was a list literal, searching the scope chain.  Used by affineOf / substitutedAffine
// to resolve `xs.count` in parametric alias preconditions.
func (a *Analyzer) lookupWrittenListCount(name string) (int64, bool) {
	if name == "" {
		return 0, false
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.writtenListCount != nil {
			if n, ok := scope.writtenListCount[name]; ok {
				return n, true
			}
		}
	}
	return 0, false
}

// lookupWrittenConst returns the known constant-value expr of a variable, if a written-constant fact
// for it is live in the active scope chain. The expr is the original const RHS (any kind), suitable
// for use as a const-eval subject.
func (a *Analyzer) lookupWrittenConst(name string) (ast.Expr, bool) {
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.writtenConst != nil {
			if v, ok := scope.writtenConst[name]; ok {
				return v, true
			}
		}
	}
	return nil, false
}

// callPreservesArgRefinements reports whether passing an argument as ref-param `paramIndex` of
// `call` (callee type `appliedType`) provably leaves the argument's refinements intact, so the
// caller can KEEP its predicate/written-const facts across the call (docs/85 brick 3, preserve
// credit). Two sound signals: (1) the callee declares `ensures <param> preserve` (a
// FuncPoststateKindPreserve over the whole target — its ParamIndex aligns with the call loop index,
// same as funcPoststatesForParam); (2) the parameter is an IMMUTABLE borrow (the callee decl's
// ParamDecl.Mutable is false), so the callee cannot write through it and Elisa's borrow rules
// forbid a concurrent mutable alias. Anything unprovable returns false → the brick-1 drop stands.
func (a *Analyzer) callPreservesArgRefinements(call *ast.CallExpr, appliedType *FuncType, paramIndex int) bool {
	if appliedType != nil {
		for _, ps := range appliedType.Poststates {
			if ps.ParamIndex == paramIndex && ps.Kind == FuncPoststateKindPreserve && len(ps.Path) == 0 {
				return true
			}
		}
		// An IMMUTABLE borrow `T&` (non-mutable pointee) cannot be written through, so the argument's
		// refinements survive. Reading this from the resolved FuncType — rather than the call's decl —
		// makes the signal work for EVERY call shape (method `obj.m(arg)`, indirect/closure calls),
		// not just direct by-name calls. Index aligns with the call's param loop, same as Poststates.
		if paramIndex >= 0 && paramIndex < len(appliedType.Params) {
			if rt, ok := appliedType.Params[paramIndex].(*RefType); ok && rt != nil && !rt.Mutable {
				return true
			}
		}
	}
	if decl, ok := a.resolveDirectCallFuncDecl(call); ok && decl != nil {
		if paramIndex >= 0 && paramIndex < len(decl.Params) && paramIsImmutableBorrow(decl.Params[paramIndex]) {
			return true
		}
	}
	return false
}

// paramIsImmutableBorrow reports whether a parameter is an immutable borrow `T&` — a ref through
// which the callee cannot write, so an argument's refinements survive the call. The canonical
// mutable borrow is `p: mutable i64&` (a MutableType wrapping the ref); the legacy form is a leading
// `mutable` keyword (ParamDecl.Mutable). Both are rejected here; only a plain `*ast.RefType` whose
// pointee is not itself mutable counts as immutable. Conservative: any non-ref or mutable shape is
// not a preserving borrow.
func paramIsImmutableBorrow(p ast.ParamDecl) bool {
	if p.Mutable {
		return false
	}
	rt, ok := p.Type.(*ast.RefType)
	if !ok || rt == nil {
		return false
	}
	if _, mut := rt.Elem.(*ast.MutableType); mut {
		return false
	}
	return true
}

// factKey builds the canonical predicate-fact key for a law applied to constant arguments. A bare
// law is just its name; a parametric law `Bounded[0, 500]` whose args are all compile-time integer
// constants is `Bounded[0,500]`. Returns false when any argument is not a constant integer (such a
// fact can't be matched against an obligation soundly, so it is not tracked). Keying by exact arg
// values means a fact only discharges an obligation with the SAME bounds — `Bounded[0,500]` never
// proves `Bounded[10,20]` (no cross-bounds entailment here; that is the range prover's job).
func (a *Analyzer) factKey(lawName string, argExprs []ast.Expr) (string, bool) {
	if lawName == "" {
		return "", false
	}
	if len(argExprs) == 0 {
		return lawName, true
	}
	key := lawName + "["
	for i, arg := range argExprs {
		c, ok := a.constIntValue(arg)
		if !ok {
			return "", false
		}
		if i > 0 {
			key += ","
		}
		key += strconv.FormatInt(c, 10)
	}
	return key + "]", true
}

// factKeyWithDeps builds a predicate-fact key for a law whose args may be CONSTANT or DEPENDENT
// (docs/85 §5.3 dependence-freeze). A constant integer arg is keyed by value (exact-match, as
// factKey). A dependent arg — a pure, side-effect-free path/arithmetic over variables (`xs.count`,
// `xs.count - 1`, `r.vw`) — is keyed by its canonical text, and the root variables it reads are
// returned as `deps`; such a fact is sound only while those roots are frozen (a mutation of any of
// them invalidates it). Returns ok=false for any non-freezable arg (a call, an impure or
// unrecognized expr) so the fact is simply not tracked. deps is nil for an all-constant fact, making
// this a superset of factKey with identical keys in the constant case.
func (a *Analyzer) factKeyWithDeps(lawName string, argExprs []ast.Expr) (string, []string, bool) {
	if lawName == "" {
		return "", nil, false
	}
	if len(argExprs) == 0 {
		return lawName, nil, true
	}
	var deps []string
	seen := map[string]bool{}
	key := lawName + "["
	for i, arg := range argExprs {
		if i > 0 {
			key += ","
		}
		if c, ok := a.constIntValue(arg); ok {
			key += strconv.FormatInt(c, 10)
			continue
		}
		if !isFreezableDependentArg(arg) {
			return "", nil, false
		}
		key += unparse.FormatExpr(arg)
		collectFreezableArgRoots(arg, seen, &deps)
	}
	return key + "]", deps, true
}

// isFreezableDependentArg reports whether a dependent-bound argument is a pure, re-evaluation-stable
// path/arithmetic expression: identifiers, field paths (incl. `.count`), constant-or-path indexing,
// integer literals, parens, unary +/-/~, and arithmetic binary ops over such operands. Anything that
// could observe or cause a side effect (a call) or that this checker doesn't recognize is rejected,
// so a fact is tracked only when its dependence on frozen variables is explicit.
func isFreezableDependentArg(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.Ident:
		return n != nil
	case *ast.IntLit:
		return true
	case *ast.ParenExpr:
		return n != nil && isFreezableDependentArg(n.Inner)
	case *ast.FieldExpr:
		return n != nil && isFreezableDependentArg(n.Object)
	case *ast.IndexExpr:
		return n != nil && isFreezableDependentArg(n.Object) && isFreezableDependentArg(n.Index)
	case *ast.UnaryExpr:
		return n != nil && isFreezableDependentArg(n.Operand)
	case *ast.BinaryExpr:
		if n == nil {
			return false
		}
		switch n.Op {
		case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT:
			return isFreezableDependentArg(n.Left) && isFreezableDependentArg(n.Right)
		}
		return false
	default:
		return false
	}
}

// collectFreezableArgRoots adds the root variable names a freezable dependent arg reads (the bases of
// its paths and the operands of its arithmetic) into deps, deduped via seen.
func collectFreezableArgRoots(expr ast.Expr, seen map[string]bool, deps *[]string) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n != nil && !seen[n.Name] {
			seen[n.Name] = true
			*deps = append(*deps, n.Name)
		}
	case *ast.ParenExpr:
		if n != nil {
			collectFreezableArgRoots(n.Inner, seen, deps)
		}
	case *ast.FieldExpr:
		if n != nil {
			collectFreezableArgRoots(n.Object, seen, deps)
		}
	case *ast.IndexExpr:
		if n != nil {
			collectFreezableArgRoots(n.Object, seen, deps)
			collectFreezableArgRoots(n.Index, seen, deps)
		}
	case *ast.UnaryExpr:
		if n != nil {
			collectFreezableArgRoots(n.Operand, seen, deps)
		}
	case *ast.BinaryExpr:
		if n != nil {
			collectFreezableArgRoots(n.Left, seen, deps)
			collectFreezableArgRoots(n.Right, seen, deps)
		}
	}
}

// gatherLawIsPredFact records a predicate fact for `if x is Law:` / `if x is Law[consts]:` where x
// is a bare identifier — regardless of whether x is mutable. Mutable is safe here because any later
// mutation of x invalidates the fact via invalidatePredFacts. The integer range path
// (gatherLawIsRangeRefinement) handles the decidable `self OP const` fragment over immutable ints;
// this complements it for laws outside that fragment (e.g. `darray is NonEmpty`, body `self.count >
// 0`), for mutable subjects, and for parametric laws applied to constant bounds.
func (a *Analyzer) gatherLawIsPredFact(scope *Scope, n *ast.BinaryExpr, truthy bool) {
	if !truthy || scope == nil || n == nil {
		return
	}
	ident, ok := n.Left.(*ast.Ident)
	if !ok || ident == nil {
		return
	}
	targets := flattenIsTargetExprs(n.Right)
	if len(targets) != 1 {
		return
	}
	lawName, lawArgs, ok := a.resolveLawIsTarget(targets[0])
	if !ok {
		return
	}
	key, deps, ok := a.factKeyWithDeps(lawName, lawArgs)
	if !ok {
		return // non-constant, non-freezable parametric args can't be tracked soundly
	}
	// A dependent fact (deps non-empty, docs/85 §5.3) also drops when any frozen dependency mutates.
	recordPredFactWithDeps(scope, ident.Name, key, deps)
}

// tryProveRefinementByFactSet attempts to discharge `value is law[args]` from a tracked predicate
// fact: when value is a bare identifier with a live fact for the SAME (law, constant args) — gained
// from a flow narrowing and not since invalidated by a mutation — the obligation is proven with no
// runtime check.
func (a *Analyzer) tryProveRefinementByFactSet(value ast.Expr, lawName string, predArgs []ast.Expr, allowDependentFacts bool) bool {
	ident, ok := value.(*ast.Ident)
	if !ok || ident == nil {
		return false
	}
	// Build the key the same way the narrowing site recorded it (constant OR dependent §5.3). A live
	// dependent fact has already survived the freeze invariant — any mutation of its deps would have
	// dropped it — so a hit discharges the obligation with no runtime check.
	key, deps, ok := a.factKeyWithDeps(lawName, predArgs)
	if !ok {
		return false
	}
	// A dependent key names variables; matching one across a function boundary (a call argument, whose
	// callee param refinement spells names in the CALLEE's scope) would conflate distinct variables
	// that happen to share a spelling. Dependent facts therefore discharge only same-function
	// obligations (var decl, return, ensures); constant facts (no deps) are absolute and always match.
	if len(deps) != 0 && !allowDependentFacts {
		return false
	}
	return a.lookupPredFact(ident.Name, key)
}
