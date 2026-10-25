package semantic

import "elisacore/src/ast"

// Resolve only literal components that the constructor evaluation-order rules
// allow us to read again. In particular, a later constructor argument may have
// changed a mutable pointer slot after the field captured its old value.
func (tr *smtTranslator) pointerLiteralOperand(expr ast.Expr, env map[string]string, depth int) ast.Expr {
	expr = stripOptimizationParens(expr)
	if expr == nil || depth > 32 || tr.a == nil || tr.a.shadowingActive() {
		return expr
	}
	field, ok := expr.(*ast.FieldExpr)
	if !ok {
		return expr
	}
	if tr.resultFields != nil && tr.calleeBodyDepth == 0 {
		root, _ := projectionChain(field)
		if id, ok := root.(*ast.Ident); ok && id.Name == "result" && !tr.envBindsRoot(id, env) {
			if _, direct := stripOptimizationParens(field.Object).(*ast.Ident); direct {
				if value := tr.resultFields[field.Field]; value != nil {
					return tr.pointerLiteralOperand(value, env, depth+1)
				}
			} else if rerooted, found := rerootBelowFirstField(field, tr.resultFields, tr.a); found {
				return tr.pointerLiteralOperand(rerooted, env, depth+1)
			}
		}
	}
	if value, found := tr.a.reevaluableLiteralComponent(field, 0); found && value != nil {
		return tr.pointerLiteralOperand(value, env, depth+1)
	}
	return expr
}

// pointerIdentityReadStable permits reflexivity of stable pointer reads and
// builtin element addresses. Computing an address reads the container header
// and index but not the element. Keep call support local to pointer equality:
// it does not make an indexed value safe to re-evaluate after later constructor
// arguments have mutated its storage or index.
func (tr *smtTranslator) pointerIdentityReadStable(expr ast.Expr) bool {
	if tr.pointerIdentityStableExpr(expr, 0) {
		return true
	}
	address, ok := stripOptimizationParens(expr).(*ast.AddrOfExpr)
	if !ok || address == nil || tr.a == nil {
		return false
	}
	index, ok := stripOptimizationParens(address.Operand).(*ast.IndexExpr)
	if !ok || index == nil || index.Index2 != nil || index.Fallback != nil || index.AsOverlayCall != nil ||
		!tr.pointerIdentityStableExpr(index.Object, 0) || !tr.pointerIdentityStableExpr(index.Index, 0) {
		return false
	}
	typ := tr.a.exprType(index.Object)
	switch stripRefForBounds(typ).(type) {
	case *ArrayType, *DArrayType:
		return true
	}
	// Raw pointers to primitive integers use builtin indexing. A reference to a
	// user-defined indexable value must not acquire this rule by its syntax.
	if ref, ok := typ.(*RefType); ok && ref != nil {
		_, _, integer := smtIntWidthSign(ref.Elem)
		return integer
	}
	return false
}

// pointerIdentityStableExpr extends the ordinary side-effect-free expression
// whitelist only for calls whose completed bodies are proven write-free. This
// is deliberately local to pointer identity: a safe call may be repeated while
// comparing the same pointer expression, but it is not globally re-evaluable
// for constructor projection or other proof translations.
func (tr *smtTranslator) pointerIdentityStableExpr(expr ast.Expr, depth int) bool {
	if expr == nil || depth > 64 || tr.a == nil {
		return false
	}
	if sideEffectFreeExpr(expr, 0) {
		return true
	}
	switch n := stripOptimizationParens(expr).(type) {
	case *ast.ParenExpr:
		return tr.pointerIdentityStableExpr(n.Inner, depth+1)
	case *ast.UnaryExpr:
		return n.LoweredCall == nil && tr.pointerIdentityStableExpr(n.Operand, depth+1)
	case *ast.BinaryExpr:
		return n.LoweredCall == nil && tr.pointerIdentityStableExpr(n.Left, depth+1) && tr.pointerIdentityStableExpr(n.Right, depth+1)
	case *ast.TernaryExpr:
		return tr.pointerIdentityStableExpr(n.Cond, depth+1) &&
			tr.pointerIdentityStableExpr(n.Value, depth+1) && tr.pointerIdentityStableExpr(n.Alt, depth+1)
	case *ast.CastExpr:
		return tr.pointerIdentityStableExpr(n.Operand, depth+1)
	case *ast.FieldExpr:
		return tr.pointerIdentityStableExpr(n.Object, depth+1)
	case *ast.CallExpr:
		return tr.pointerIdentityCallStable(n, depth+1)
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			if !tr.pointerIdentityStableExpr(elem, depth+1) {
				return false
			}
		}
		return true
	case *ast.StructLitExpr:
		if n.CopyBase != nil || len(n.Spreads) != 0 {
			return false
		}
		for _, arg := range n.Args {
			if !tr.pointerIdentityStableExpr(arg, depth+1) {
				return false
			}
		}
		return true
	}
	return false
}

func pointerIdentityStableValueType(typ Type) bool {
	if typ == nil {
		return false
	}
	if _, isRef := typ.(*RefType); isRef {
		return false
	}
	if _, isView := typ.(*SViewType); isView {
		return true
	}
	if IsBoolType(typ) {
		return true
	}
	_, _, integer := smtIntWidthSign(typ)
	return integer && !IsFloatType(typ)
}

func pointerIdentityIntegralValueType(typ Type) bool {
	if typ == nil {
		return false
	}
	if _, isRef := typ.(*RefType); isRef {
		return false
	}
	_, _, integer := smtIntWidthSign(typ)
	return integer && !IsFloatType(typ)
}

func (tr *smtTranslator) pointerIdentityCallStable(call *ast.CallExpr, depth int) bool {
	if call == nil || depth > 64 || tr.a == nil || tr.a.shadowingActive() {
		return false
	}
	if tr.a.ghostCallIsValueConversion(call) {
		field, ok := call.Func.(*ast.FieldExpr)
		return ok && field != nil && tr.pointerIdentityStableExpr(field.Object, depth+1)
	}
	decl, direct := tr.a.resolveDirectCallFuncDecl(call)
	decl = tr.a.readDecl(decl)
	if !direct || decl == nil || decl == tr.a.currentFuncDecl || decl.IsLemma || decl.IsGhost ||
		len(call.Args) != len(decl.Params) || !tr.a.sviewFrameCallBodyWriteFree(decl, len(call.Args)) {
		return false
	}
	sym, found := tr.a.symbolForFuncDecl(decl)
	if !found || sym == nil {
		return false
	}
	fn, ok := sym.Type.(*FuncType)
	if !ok || fn == nil || !pointerIdentityIntegralValueType(fn.Return) || len(fn.Params) != len(call.Args) {
		return false
	}
	for i, arg := range call.Args {
		if decl.Params[i].Mutable || tr.a.paramIsMutable(decl.Params[i]) ||
			!pointerIdentityStableValueType(fn.Params[i]) || !tr.pointerIdentityStableExpr(arg, depth+1) {
			return false
		}
	}
	return true
}

func (tr *smtTranslator) samePointerIdentityExpr(left, right ast.Expr) bool {
	if tr.a.sameExprText(left, right) {
		return true
	}
	l, lok := stripOptimizationParens(left).(*ast.AddrOfExpr)
	r, rok := stripOptimizationParens(right).(*ast.AddrOfExpr)
	return lok && rok && l != nil && r != nil && tr.a.sameExprText(l.Operand, r.Operand)
}

// Substituting a return value creates field nodes without exprTypes entries.
// Scope lookup is valid only for caller expressions; a callee formal must never
// acquire the type (or literal value) of a same-spelled caller local.
func (tr *smtTranslator) pointerIdentityOperandType(expr ast.Expr) Type {
	typ := tr.a.exprType(expr)
	if typ == nil && tr.calleeBodyDepth == 0 {
		typ = tr.a.smtOperandType(expr)
		if typ == nil {
			if field, ok := stripOptimizationParens(expr).(*ast.FieldExpr); ok {
				object := tr.a.smtOperandType(field.Object)
				if runtime := tr.a.runtimeBackedStructType(peelNamedStateRefs(object)); runtime != nil {
					typ, _ = tr.a.lookupResolvedFieldType(runtime, field.Field)
				}
			}
		}
	}
	return typ
}

// Callee envs carry scalar values, view bytes/length and aggregate tokens, not
// address terms. Formal-name predicates would merge different calls, including
// composite addresses whose offsets depend on scalar formals. Keep them opaque
// until addresses can be bound; reflexivity is handled independently.
func (tr *smtTranslator) pointerHasUnboundCalleeAddress(expr ast.Expr, env map[string]string) bool {
	if tr.calleeBodyDepth == 0 {
		return false
	}
	_, literalNull := stripOptimizationParens(expr).(*ast.NullLit)
	return !literalNull
}
