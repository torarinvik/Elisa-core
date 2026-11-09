package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"maps"
	"slices"
	"strings"
)

// pureEquationSViewIndex admits only ordinary single-byte reads from an immutable sview formal.
// This is the indexed-read fragment supported by the SMT array model; rewritten dict/overlay and
// fallback indexing can perform different operations and are deliberately excluded.
func (a *Analyzer) pureEquationSViewIndex(index *ast.IndexExpr, owner *ast.FuncDecl) bool {
	if a == nil || index == nil || owner == nil || index.Index2 != nil || index.Fallback != nil ||
		index.LegacyElseFallback || index.AsSpecialize != nil || index.AsOverlayCall != nil || index.AsBuiltinDictGet != nil {
		return false
	}
	object, ok := stripOptimizationParens(index.Object).(*ast.Ident)
	if !ok || object == nil || !a.equationIdentifierIsStable(object, owner) {
		return false
	}
	paramIndex := -1
	for i, param := range owner.Params {
		if param.Name == object.Name {
			if param.Mutable || a.paramIsMutable(param) {
				return false
			}
			paramIndex = i
			break
		}
	}
	if paramIndex < 0 {
		return false
	}
	sym, ok := a.symbolForFuncDecl(owner)
	if !ok || sym == nil {
		return false
	}
	fn, ok := sym.Type.(*FuncType)
	if !ok || fn == nil || paramIndex >= len(fn.Params) {
		return false
	}
	_, ok = stripRefForBounds(fn.Params[paramIndex]).(*SViewType)
	return ok
}

// exprIsPureForEquation reports whether an expression is a PURE integer expression we may put on the
// RHS of a defining equation: literals, parameter/immutable identifiers, arithmetic, comparisons,
// parens, value-preserving casts, and calls to OTHER functions that are THEMSELVES equation-eligible
// (so purity is transitive and a mutating/effectful helper poisons the whole body). Anything else —
// an address-of, a field/index into mutable state, an unknown call — makes the body impure ⇒ decline.
func (a *Analyzer) exprIsPureForEquation(expr ast.Expr, owner *ast.FuncDecl) bool {
	switch n := expr.(type) {
	case *ast.MatchExpr:
		return pureScalarMatchEquation(n)
	case *ast.IntLit, *ast.BoolLit, *ast.StringLit:
		return true
	case *ast.StructLitExpr:
		if n == nil || n.CopyBase != nil || len(n.Spreads) != 0 {
			return false
		}
		for _, arg := range n.Args {
			if !a.exprIsPureForEquation(arg, owner) {
				return false
			}
		}
		return true
	case *ast.Ident:
		return a.equationIdentifierIsStable(n, owner)
	case *ast.FieldExpr:
		if n == nil {
			return false
		}
		// A constant enum member (`Kind.Struct`) is a fixed value.
		if _, constant := a.constIntValue(n); constant {
			return true
		}
		// A payload-free variant (`Mod::Kind.Struct`) is a fixed value too.
		if _, variant, ok := a.enumConstructorInfoFromFieldExpr(n); ok && variant != nil && len(variant.Payload) == 0 {
			return true
		}
		return a.exprIsPureForEquation(n.Object, owner)
	case *ast.IndexExpr:
		return (a.pureEquationSViewIndex(n, owner) || a.pureEquationContainerIndex(n, owner)) && a.exprIsPureForEquation(n.Index, owner)
	case *ast.ParenExpr:
		return n != nil && a.exprIsPureForEquation(n.Inner, owner)
	case *ast.UnaryExpr:
		return n != nil && n.LoweredCall == nil && n.Op != lexer.TOKEN_AMPERSAND && a.exprIsPureForEquation(n.Operand, owner)
	case *ast.BinaryExpr:
		return n != nil && n.LoweredCall == nil && a.exprIsPureForEquation(n.Left, owner) && a.exprIsPureForEquation(n.Right, owner)
	case *ast.CastExpr:
		return n != nil && a.exprIsPureForEquation(n.Operand, owner)
	case *ast.TernaryExpr:
		return n != nil && a.exprIsPureForEquation(n.Cond, owner) && a.exprIsPureForEquation(n.Value, owner) && a.exprIsPureForEquation(n.Alt, owner)
	case *ast.CallExpr:
		if n == nil {
			return false
		}
		decl, ok := a.resolveDirectCallFuncDecl(n)
		if !ok || decl == nil {
			if ident, idOK := n.Func.(*ast.Ident); idOK && ident != nil && a.definingEquationInProgress != nil {
				for inProgress := range a.definingEquationInProgress {
					if inProgress != nil && inProgress.Name == ident.Name {
						for _, arg := range n.Args {
							if !a.exprIsPureForEquation(arg, owner) {
								return false
							}
						}
						return true
					}
				}
			}
			return false
		}
		for _, arg := range n.Args {
			if !a.exprIsPureForEquation(arg, owner) {
				return false
			}
		}
		// Transitive purity: a callee must itself be a pure equation-eligible function. The in-progress
		// guard in functionDefiningEquationEligible breaks self/mutual recursion (returns true for the
		// node currently being decided), so this terminates.
		return a.functionDefiningEquationEligible(decl) || a.viewEquationEligible(decl)
	default:
		return false
	}
}

func (a *Analyzer) smtEquationResultType(t Type) bool {
	t = stripRefForBounds(t)
	return t != nil && ((IsNumericType(t) && !IsFloatType(t)) || IsBoolType(t) || a.smtEquationStructResultType(t))
}

// smtEquationStructResultType admits only plain records whose direct fields each have a scalar SMT
// sort. Aggregate call results are still opaque as a whole; the projection translator emits one
// guarded defining equation per field. Keeping fields scalar avoids inventing nested aggregate
// equations or equating unsupported field representations.
func (a *Analyzer) smtEquationStructResultType(t Type) bool {
	st, ok := StripAggregateStateType(stripRefForBounds(t)).(*StructType)
	if !ok || st == nil || !plainFrameRecordType(st, map[*StructType]bool{}) {
		return false
	}
	for _, field := range st.Fields {
		if field.Ghost || field.Phantom || field.IsTail {
			return false
		}
		ft := stripRefForBounds(field.Type)
		if IsBoolType(ft) {
			continue
		}
		_, bits, integer := smtIntWidthSign(ft)
		if !integer || bits <= 0 || bits > 64 {
			return false
		}
	}
	return len(st.Fields) != 0
}

// smtEquationRecordFields returns the scalar fields of a plain record result, or of a named tuple
// whose fields are uniquely named and scalar, sorted by name. A named tuple is projected by field
// name exactly like a record (tupleLiteralFieldValue), so it shares the record equation path.
func (a *Analyzer) smtEquationRecordFields(t Type) ([]TupleField, bool) {
	if a.smtEquationStructResultType(t) {
		st := StripAggregateStateType(stripRefForBounds(t)).(*StructType)
		out := make([]TupleField, 0, len(st.Fields))
		for _, name := range slices.Sorted(maps.Keys(st.Fields)) {
			out = append(out, TupleField{Name: name, Type: st.Fields[name].Type})
		}
		return out, true
	}
	tt, ok := StripAggregateStateType(stripRefForBounds(t)).(*TupleType)
	if !ok || tt == nil || len(tt.Fields) == 0 {
		return nil, false
	}
	seen := map[string]bool{}
	out := make([]TupleField, 0, len(tt.Fields))
	for _, field := range tt.Fields {
		if field.Name == "" || seen[field.Name] || field.Type == nil {
			return nil, false
		}
		seen[field.Name] = true
		ft := stripRefForBounds(field.Type)
		if !IsBoolType(ft) {
			_, bits, integer := smtIntWidthSign(ft)
			if !integer || bits <= 0 || bits > 64 {
				return nil, false
			}
		}
		out = append(out, field)
	}
	slices.SortFunc(out, func(x, y TupleField) int { return strings.Compare(x.Name, y.Name) })
	return out, true
}

// aggregateRecordDefiningEquationEligible admits a ghost record-returning function whose body is a
// pure, total return tree, even when that tree projects results from ghost tuple/sview helpers that
// the scalar SMT equation fragment cannot inline. Such helpers remain opaque at the call site; this
// gate independently checks their purity and the termination of every reachable recursive SCC.
func (a *Analyzer) aggregateRecordDefiningEquationEligible(decl *ast.FuncDecl) bool {
	if a == nil || decl == nil || !decl.IsGhost || decl.IsLemma || decl.DecreasesWild != "" {
		return false
	}
	// The verdict reads the callee's typed body and resolves its calls in the current resolution
	// context; memoize per context so each call site does not re-walk the callee's call graph.
	if a.lookupTrace != nil {
		return a.aggregateRecordDefiningEquationEligibleUncached(decl)
	}
	key := a.resolutionContextKey(decl)
	if eligible, ok := a.aggregateEquationEligibleMemo[key]; ok {
		return eligible
	}
	eligible := a.aggregateRecordDefiningEquationEligibleUncached(decl)
	if a.aggregateEquationEligibleMemo == nil {
		a.aggregateEquationEligibleMemo = map[recursiveProofKey]bool{}
	}
	a.aggregateEquationEligibleMemo[key] = eligible
	return eligible
}

func (a *Analyzer) aggregateRecordDefiningEquationEligibleUncached(decl *ast.FuncDecl) bool {
	sym, ok := a.symbolForFuncDecl(decl)
	if !ok || sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil {
		return false
	}
	if _, ok := a.smtEquationRecordFields(fnType.Return); !ok {
		return false
	}
	for _, param := range fnType.Params {
		if !a.smtEquationParamType(param) {
			return false
		}
	}
	body, ok := a.typedPureReturnExpr(decl)
	if !ok || !a.exprIsPureForAggregateEquation(body, decl) {
		return false
	}
	return a.ghostCallGraphPureTotalAndTerminating(decl)
}

func (a *Analyzer) exprIsPureForAggregateEquation(expr ast.Expr, owner *ast.FuncDecl) bool {
	switch n := expr.(type) {
	case *ast.MatchExpr:
		return pureScalarMatchEquation(n)
	case *ast.IntLit, *ast.BoolLit:
		return true
	case *ast.TupleExpr:
		if n == nil {
			return false
		}
		for _, elem := range n.Elems {
			if !a.exprIsPureForAggregateEquation(elem, owner) {
				return false
			}
		}
		return true
	case *ast.StructLitExpr:
		if n == nil || n.CopyBase != nil || len(n.Spreads) != 0 {
			return false
		}
		for _, arg := range n.Args {
			if !a.exprIsPureForAggregateEquation(arg, owner) {
				return false
			}
		}
		return true
	case *ast.Ident:
		return a.equationIdentifierIsStable(n, owner)
	case *ast.FieldExpr:
		return n != nil && a.exprIsPureForAggregateEquation(n.Object, owner)
	case *ast.IndexExpr:
		return (a.pureEquationSViewIndex(n, owner) || a.pureEquationContainerIndex(n, owner)) && a.exprIsPureForAggregateEquation(n.Index, owner)
	case *ast.SliceExpr:
		return a.pureEquationSViewSlice(n, owner) && a.exprIsPureScalarForAggregateEquation(n.Start, owner) && a.exprIsPureScalarForAggregateEquation(n.End, owner)
	case *ast.ParenExpr:
		return n != nil && a.exprIsPureForAggregateEquation(n.Inner, owner)
	case *ast.UnaryExpr:
		return n != nil && n.LoweredCall == nil && n.Op != lexer.TOKEN_AMPERSAND && a.exprIsPureForAggregateEquation(n.Operand, owner)
	case *ast.BinaryExpr:
		return n != nil && n.LoweredCall == nil && a.exprIsPureForAggregateEquation(n.Left, owner) && a.exprIsPureForAggregateEquation(n.Right, owner)
	case *ast.CastExpr:
		return n != nil && a.exprIsPureForAggregateEquation(n.Operand, owner)
	case *ast.TernaryExpr:
		return n != nil && a.exprIsPureForAggregateEquation(n.Cond, owner) && a.exprIsPureForAggregateEquation(n.Value, owner) && a.exprIsPureForAggregateEquation(n.Alt, owner)
	case *ast.CallExpr:
		if n == nil {
			return false
		}
		for _, arg := range proofCallArgs(n) {
			if !a.exprIsPureForAggregateEquation(arg, owner) {
				return false
			}
		}
		callee, ok := a.resolveDirectCallFuncDecl(n)
		if !ok || callee == nil {
			return false
		}
		if a.functionDefiningEquationEligible(callee) {
			return true
		}
		return a.opaqueGhostCallEligible(callee)
	default:
		return false
	}
}

func (a *Analyzer) exprIsPureScalarForAggregateEquation(expr ast.Expr, owner *ast.FuncDecl) bool {
	if a == nil || expr == nil {
		return false
	}
	t := stripRefForBounds(a.exprType(expr))
	if !IsNumericType(t) || IsFloatType(t) {
		return false
	}
	_, bits, integer := smtIntWidthSign(t)
	return integer && bits > 0 && bits <= 64 && a.exprIsPureForAggregateEquation(expr, owner)
}

// pureEquationSViewSlice admits only an explicitly bounded slice of a direct immutable sview
// parameter. Computed bases, local containers, and arrays remain outside this aggregate equation
// fragment because their slice identity is not represented by the call projection model.
func (a *Analyzer) pureEquationSViewSlice(slice *ast.SliceExpr, owner *ast.FuncDecl) bool {
	if a == nil || slice == nil || owner == nil || slice.Start == nil || slice.End == nil {
		return false
	}
	object, ok := stripOptimizationParens(slice.Object).(*ast.Ident)
	if !ok || object == nil || !a.equationIdentifierIsStable(object, owner) {
		return false
	}
	paramIndex := -1
	for i, param := range owner.Params {
		if param.Name == object.Name {
			if param.Mutable || a.paramIsMutable(param) {
				return false
			}
			paramIndex = i
			break
		}
	}
	if paramIndex < 0 {
		return false
	}
	sym, ok := a.symbolForFuncDecl(owner)
	if !ok || sym == nil {
		return false
	}
	fn, ok := sym.Type.(*FuncType)
	if !ok || fn == nil || paramIndex >= len(fn.Params) {
		return false
	}
	_, ok = stripRefForBounds(fn.Params[paramIndex]).(*SViewType)
	return ok
}

func (a *Analyzer) opaqueGhostCallEligible(decl *ast.FuncDecl) bool {
	decl = a.readDecl(decl)
	if a == nil || decl == nil || !decl.IsGhost || decl.IsLemma || decl.DecreasesWild != "" {
		return false
	}
	sym, ok := a.symbolForFuncDecl(decl)
	if !ok || sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil {
		return false
	}
	resultType := stripRefForBounds(fnType.Return)
	if IsBoolType(resultType) {
		// Bool results use the canonical predicate token and guarded ensure expansion in
		// callResultBoolTermEnv.
	} else if IsNumericType(resultType) && !IsFloatType(resultType) {
		_, bits, integer := smtIntWidthSign(resultType)
		if !integer || bits <= 0 || bits > 64 || !isSMTExactAssignmentType(resultType) {
			return false
		}
	} else if !a.smtEquationStructResultType(resultType) {
		if _, ok := resultType.(*TupleType); !ok {
			if _, ok := resultType.(*SViewType); !ok {
				return false
			}
		}
	}
	return a.ghostBodyPureTotal(decl, map[*ast.FuncDecl]bool{}) && a.ghostCallGraphPureTotalAndTerminating(decl)
}

// ghostCallGraphPureTotalAndTerminating closes the gap in ghostBodyPureTotal: ghost callees are
// treated as already validated there, and the helper does not require their recorded termination
// certificate. Walk the complete direct-call graph, check each ghost body, require ordinary callees
// to pass the existing pure-total equation gate, then require a stored certificate for every member
// of each recursive SCC.
func (a *Analyzer) ghostCallGraphPureTotalAndTerminating(root *ast.FuncDecl) bool {
	root = a.readDecl(root)
	if a == nil || root == nil {
		return false
	}
	seen := map[*ast.FuncDecl]bool{}
	var visit func(*ast.FuncDecl) bool
	visit = func(fn *ast.FuncDecl) bool {
		if fn == nil || seen[fn] {
			return fn != nil
		}
		seen[fn] = true
		if fn.IsGhost {
			if !a.ghostBodyPureTotal(fn, map[*ast.FuncDecl]bool{}) || !a.ghostReturnBindingsStable(fn) {
				return false
			}
		} else if !a.functionDefiningEquationEligible(fn) {
			return false
		}
		for _, call := range a.collectDirectFunctionCalls(fn) {
			if a.ghostCallIsValueConversion(call) {
				continue
			}
			callee, ok := a.resolveDirectCallFuncDecl(call)
			if !ok || callee == nil || !visit(callee) {
				return false
			}
		}
		return true
	}
	if !visit(root) {
		return false
	}
	for fn := range seen {
		edges := a.collectRecursiveSCCEdges(fn)
		if len(edges) == 0 {
			continue
		}
		for _, edge := range edges {
			if !a.terminationVerified[edge.Caller] || !a.terminationVerified[edge.Callee] {
				return false
			}
		}
	}
	return true
}

// ghostReturnBindingsStable makes opaque ghost-call canonicalization depend only on immutable
// parameters and compile-time constants. ghostBodyPureTotal proves a pure return-tree shape, but
// intentionally does not distinguish those bindings from mutable module state.
func (a *Analyzer) ghostReturnBindingsStable(fn *ast.FuncDecl) bool {
	if a == nil || fn == nil {
		return false
	}
	body, ok := a.typedPureReturnExpr(fn)
	if !ok {
		return false
	}
	var stable func(ast.Expr) bool
	stable = func(expr ast.Expr) bool {
		switch n := expr.(type) {
		case nil, *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.BoolLit, *ast.CharLit, *ast.NullLit,
			*ast.SizeofExpr, *ast.AlignofExpr, *ast.OffsetofExpr:
			return true
		case *ast.Ident:
			return a.equationIdentifierIsStable(n, fn)
		case *ast.StructLitExpr:
			if n == nil || n.CopyBase != nil || len(n.Spreads) != 0 {
				return false
			}
			for _, arg := range n.Args {
				if !stable(arg) {
					return false
				}
			}
			return true
		case *ast.FieldExpr:
			return n != nil && stable(n.Object)
		case *ast.IndexExpr:
			return n != nil && stable(n.Object) && stable(n.Index)
		case *ast.SliceExpr:
			return n != nil && stable(n.Object) && stable(n.Start) && stable(n.End)
		case *ast.TupleExpr:
			if n == nil {
				return false
			}
			for _, elem := range n.Elems {
				if !stable(elem) {
					return false
				}
			}
			return true
		case *ast.ParenExpr:
			return n != nil && stable(n.Inner)
		case *ast.UnaryExpr:
			return n != nil && stable(n.Operand)
		case *ast.BinaryExpr:
			return n != nil && stable(n.Left) && stable(n.Right)
		case *ast.CastExpr:
			return n != nil && stable(n.Operand)
		case *ast.TernaryExpr:
			return n != nil && stable(n.Cond) && stable(n.Value) && stable(n.Alt)
		case *ast.CallExpr:
			if n == nil {
				return false
			}
			for _, arg := range proofCallArgs(n) {
				if !stable(arg) {
					return false
				}
			}
			// A field-shaped callee can be either a method receiver or a qualified function name.
			// Check it as a value only when semantic analysis resolved the object to a value type.
			if field, ok := n.Func.(*ast.FieldExpr); ok && field != nil && field.Object != nil && a.exprType(field.Object) != nil {
				return stable(field.Object)
			}
			return true
		default:
			return false
		}
	}
	return stable(body)
}
