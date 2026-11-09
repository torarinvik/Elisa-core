package semantic

import (
	"elisacore/src/ast"
	"reflect"
)

func isGlobalPermissionRef(ref ast.PermissionRef) bool {
	return ref.Name == "Global" && (ref.Member == "" || ref.Member == "Read" || ref.Member == "Write")
}

func (a *Analyzer) permissionRefsRequiringLocalGrant(fnType *FuncType) []ast.PermissionRef {
	if fnType == nil {
		return nil
	}
	refs := a.effectiveFunctionPermissionRefs(fnType)
	if len(refs) == 0 {
		return nil
	}
	declared := a.grantedPermissionRefs(fnType.DeclaredPermissionRefs)
	filtered := make([]ast.PermissionRef, 0, len(refs))
	for _, ref := range refs {
		// Mutable-global effects and explicitly declared Global rows are mandatory.
		// Keep legacy inferred immutable-global/extern effects behind -Wglobals.
		if isGlobalPermissionRef(ref) && !permissionRefGranted(ref, declared) && !a.enforceGlobalPermissions && !permissionRefGranted(ref, a.grantedPermissionRefs(a.mandatoryGlobalPermissionRefs(fnType))) {
			continue
		}
		filtered = append(filtered, ref)
	}
	return canonicalizePermissionRefs(filtered)
}

func isGlobalStorageSymbol(sym *Symbol) bool {
	if sym == nil {
		return false
	}
	return sym.Kind == SymbolGlobal || sym.Kind == SymbolExternVar
}

func (a *Analyzer) globalStorageSymbolForIdent(name string) (*Symbol, bool) {
	if name == "" {
		return nil, false
	}
	if a.currentScope != nil {
		if sym, ok := a.currentScope.Lookup(name); ok {
			if isGlobalStorageSymbol(sym) && a.globalNameIsVisible(sym, name) {
				return sym, true
			}
			return nil, false
		}
	}
	sym, _, ok := a.lookupVisibleGlobal(name)
	if !ok || !isGlobalStorageSymbol(sym) {
		return nil, false
	}
	return sym, true
}

func (a *Analyzer) globalStorageRoot(expr ast.Expr) (*Symbol, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if sym, known := a.resolvedGlobalStorage[n]; known {
			return sym, isGlobalStorageSymbol(sym)
		}
		return a.globalStorageSymbolForIdent(n.Name)
	case *ast.FieldExpr:
		return a.globalStorageRoot(n.Object)
	case *ast.IndexExpr:
		return a.globalStorageRoot(n.Object)
	case *ast.SliceExpr:
		return a.globalStorageRoot(n.Object)
	case *ast.ParenExpr:
		return a.globalStorageRoot(n.Inner)
	case *ast.AddrOfExpr:
		return a.globalStorageRoot(n.Operand)
	case *ast.CastExpr:
		if operand, _, ok := a.globalStorageCastOperand(n); ok {
			return a.globalStorageRoot(operand)
		}
		return nil, false
	default:
		return nil, false
	}
}

// globalStorageCastOperand returns the place whose storage identity is retained
// by an explicit reference reinterpret. References loaded from a place and
// references formed with & preserve provenance; numeric-to-pointer casts and
// postfix cast hooks do not establish that their result aliases the source.
func (a *Analyzer) globalStorageCastOperand(expr *ast.CastExpr) (ast.Expr, bool, bool) {
	if expr == nil || expr.Origin != ast.CastExprOriginExplicitCast {
		return nil, false, false
	}
	if _, ok := a.exprType(expr).(*RefType); !ok {
		return nil, false, false
	}
	operand := expr.Operand
	for {
		switch inner := operand.(type) {
		case *ast.ParenExpr:
			operand = inner.Inner
		case *ast.MoveExpr:
			operand = inner.Operand
		case *ast.AddrOfExpr:
			return inner.Operand, true, true
		default:
			if _, ok := a.exprType(operand).(*RefType); ok {
				return operand, false, true
			}
			return nil, false, false
		}
	}
}

// growthReceiverIsProgramLifetime reports whether an in-place container growth
// op (push/extend/reserve/resize on a darray/store/dict) targets storage rooted
// at a GLOBAL. Such a container is program-lifetime, so its backing allocation
// belongs in the permanent region — the in-place analogue of the global-store
// inference (a global `g <- Build()` binds to perm). When true the growth needs
// no ambient `in <arena>:` scope; the backend routes it to the perm arena.
func (a *Analyzer) growthReceiverIsProgramLifetime(opExpr, receiver ast.Expr) bool {
	if a == nil || receiver == nil {
		return false
	}
	if _, ok := a.globalStorageRoot(receiver); ok {
		// Record so the backend routes this growth's allocation to the perm arena
		// (the semantic side already suppressed the missing-region error).
		if opExpr != nil && a.permGrowthOps != nil {
			a.permGrowthOps[opExpr] = true
		}
		return true
	}
	return false
}

// Save lexical resolution while parameters and block locals are still in scope.
// A nil entry records a local, preventing later file-scope validation from mistaking
// shadowed names for globals.
func (a *Analyzer) recordResolvedGlobalStorage(expr *ast.Ident, sym *Symbol) {
	if a.suppressDiagnostics {
		return
	}
	if !isGlobalStorageSymbol(sym) {
		// Ordinary local names need no cached entry; only shadowing a visible
		// global can be confused by the later file-scope traversal.
		global, _, ok := a.lookupVisibleGlobal(expr.Name)
		if !ok || !isGlobalStorageSymbol(global) {
			return
		}
		sym = nil
	}
	if a.resolvedGlobalStorage == nil {
		a.resolvedGlobalStorage = make(map[*ast.Ident]*Symbol)
	}
	a.resolvedGlobalStorage[expr] = sym
}

func (a *Analyzer) mutableGlobalStorageRoot(expr ast.Expr) (*Symbol, bool) {
	sym, ok := a.globalStorageRoot(expr)
	return sym, ok && sym.Kind == SymbolGlobal && (sym.Mutable || a.globalPathLoadsMutableBorrow(expr) || a.globalPathHasMutableField(expr))
}

// mutableGlobalBorrowStorageRoot also admits a mutable borrow selected from a
// global aggregate. The selected expression's type matters when the expression
// transfers the borrow; it must not make a direct assignment to the stored slot
// look like a read through that slot.
func (a *Analyzer) mutableGlobalBorrowStorageRoot(expr ast.Expr) (*Symbol, bool) {
	if sym, mutable := a.mutableGlobalStorageRoot(expr); mutable {
		return sym, true
	}
	if !a.hasMutableBorrowCapability(a.exprType(expr)) {
		return nil, false
	}
	if sym, ok := a.globalStorageRoot(expr); ok && sym.Kind == SymbolGlobal {
		return sym, true
	}
	root := a.globalStoreRowsViewRoot(expr, 0)
	sym, ok := a.globalStorageRoot(root)
	return sym, ok && sym.Kind == SymbolGlobal
}

// mutableGlobalBorrowTransferRefs preserves the Global.Write effect when a
// value carrying a mutable borrow is transferred through a binding, argument,
// or return. A capability may be nested inside a tuple or other expression, so
// inspect the complete typed expression tree and charge each global-backed
// capability source.
func (a *Analyzer) mutableGlobalBorrowTransferRefs(value ast.Expr, destinationType Type) []ast.PermissionRef {
	if value == nil || !a.hasMutableBorrowCapability(destinationType) {
		return nil
	}
	var refs []ast.PermissionRef
	var visit func(ast.Expr, int)
	visit = func(expr ast.Expr, depth int) {
		if expr == nil || depth > semanticTraversalDepthLimit || !a.hasMutableBorrowCapability(a.exprType(expr)) {
			return
		}
		if call, ok := expr.(*ast.CallExpr); ok && !a.isEnumVariantConstructorCall(call) {
			// Call argument transfer and returned-global effects are collected at
			// the call site. Descending here would report the same capability
			// transfer again at an enclosing aggregate or sequence emission.
			return
		}
		if _, global := a.mutableGlobalBorrowStorageRoot(expr); global {
			refs = append(refs, globalWriteRefs(expr.Pos())...)
			return
		}
		root := globalReferenceStorageExpr(expr)
		if _, ok := root.(*ast.CallExpr); ok {
			root = nil
		}
		if root != nil {
			if _, global := a.mutableGlobalBorrowStorageRoot(root); global {
				refs = append(refs, globalWriteRefs(root.Pos())...)
				return
			}
		}
		switch n := expr.(type) {
		case *ast.MoveExpr:
			visit(n.Operand, depth+1)
		case *ast.CallExpr:
			// Ordinary calls return above; enum variant construction has no call
			// effect row, so inspect its payloads to preserve their source roots.
			for _, arg := range n.LoweredArgs() {
				visit(arg, depth+1)
			}
		case *ast.CastExpr:
			visit(n.Operand, depth+1)
		case *ast.ParenExpr:
			visit(n.Inner, depth+1)
		case *ast.AddrOfExpr:
			visit(n.Operand, depth+1)
		case *ast.TupleExpr:
			for _, elem := range n.Elems {
				visit(elem, depth+1)
			}
		case *ast.ListLitExpr:
			visit(n.Owner, depth+1)
			for _, key := range n.Keys {
				visit(key, depth+1)
			}
			for _, elem := range n.Elems {
				visit(elem, depth+1)
			}
		case *ast.StructLitExpr:
			visit(n.CopyBase, depth+1)
			for _, spread := range n.Spreads {
				visit(spread, depth+1)
			}
			args := n.Args
			if n.ResolvedArgsValid {
				args = n.ResolvedArgs
			}
			for _, arg := range args {
				visit(arg, depth+1)
			}
		case *ast.RecordUpdateExpr:
			visit(n.Base, depth+1)
			args := n.Args
			if n.ResolvedArgsValid {
				args = n.ResolvedArgs
			}
			for _, arg := range args {
				visit(arg, depth+1)
			}
		case *ast.TernaryExpr:
			visit(n.Value, depth+1)
			visit(n.Alt, depth+1)
		case *ast.TryExpr:
			visit(n.Value, depth+1)
			visit(n.Fallback, depth+1)
			if n.Recovery != nil {
				visit(n.Recovery.Value, depth+1)
			}
		case *ast.GetExpr:
			visit(n.Value, depth+1)
			visit(n.Fallback, depth+1)
			if n.Recovery != nil {
				visit(n.Recovery.Value, depth+1)
			}
		case *ast.UnwrapElseExpr:
			visit(n.Value, depth+1)
			visit(n.Fallback, depth+1)
			if n.Recovery != nil {
				visit(n.Recovery.Value, depth+1)
			}
		case *ast.OptionalBindExpr:
			visit(n.Value, depth+1)
		case *ast.CanExpr:
			visit(n.Expr, depth+1)
		case *ast.AllocExpr:
			visit(n.Owner, depth+1)
			visit(n.Value, depth+1)
		case *ast.ExprBlock:
			// Leading declarations/assignments are checked by their own transfer
			// records during statement collection; the tail is the block's value.
			visit(n.Value, depth+1)
		case *ast.MatchExpr:
			visit(n.Value, depth+1)
			visit(n.Store, depth+1)
			for _, arm := range n.Arms {
				visit(arm.Guard, depth+1)
				visit(matchArmTailExpr(arm.Body), depth+1)
			}
		case *ast.CatchExpr:
			visit(n.Value, depth+1)
			visit(matchArmTailExpr(n.Success.Body), depth+1)
			for _, arm := range n.Arms {
				visit(matchArmTailExpr(arm.Body), depth+1)
			}
		case *ast.ListComprehensionExpr:
			visit(n.Source, depth+1)
			visit(n.RangeEnd, depth+1)
			visit(n.RangeStep, depth+1)
			visit(n.Filter, depth+1)
			visit(n.Owner, depth+1)
			for _, binding := range n.Bindings {
				if declaration, ok := binding.(*ast.VarDeclStmt); ok {
					visit(declaration.Owner, depth+1)
					visit(declaration.Value, depth+1)
				}
			}
			visit(n.Value, depth+1)
			visit(n.Key, depth+1)
		case *ast.QueryExpr:
			visit(n.Source, depth+1)
			visit(n.Filter, depth+1)
			visit(n.Projection, depth+1)
			visit(n.Owner, depth+1)
		}
	}
	visit(value, 0)
	return canonicalizePermissionRefs(refs)
}

// mutableBorrowEmitValues returns the values explicitly appended by sequence
// rewrite arms. The generated AST walker keeps this in sync with nested control
// flow in arm bodies (if, match, loops, and blocks) without treating the Fold's
// ordinary expressions, nested Fold outputs, or deferred lambda bodies as this
// Fold's results.
func mutableBorrowEmitValues(body []ast.Stmt) []ast.Expr {
	var nestedFolds []*ast.FoldExpr
	var nestedLambdas []*ast.LambdaExpr
	var emits []*ast.EmitExpr
	fillMayAdoptWalk(reflect.ValueOf(body), func(node any) {
		if fold, ok := node.(*ast.FoldExpr); ok {
			nestedFolds = append(nestedFolds, fold)
		}
		if lambda, ok := node.(*ast.LambdaExpr); ok {
			nestedLambdas = append(nestedLambdas, lambda)
		}
		if emitted, ok := node.(*ast.EmitExpr); ok && emitted.Value != nil {
			emits = append(emits, emitted)
		}
	})

	nestedEmits := map[*ast.EmitExpr]bool{}
	markNestedEmits := func(value any) {
		fillMayAdoptWalk(reflect.ValueOf(value), func(node any) {
			if emitted, ok := node.(*ast.EmitExpr); ok {
				nestedEmits[emitted] = true
			}
		})
	}
	for _, fold := range nestedFolds {
		for _, arm := range fold.Arms {
			markNestedEmits(arm.Body)
		}
	}
	for _, lambda := range nestedLambdas {
		markNestedEmits(lambda.Body)
		markNestedEmits(lambda.BodyExpr)
	}

	var values []ast.Expr
	for _, emitted := range emits {
		if !nestedEmits[emitted] {
			values = append(values, emitted.Value)
		}
	}
	return values
}

// mutableGlobalFoldEmitRefs records the global write authority transferred into
// a sequence rewrite's output, including `emit all` inputs whose element type
// carries the mutable borrow capability.
func (a *Analyzer) mutableGlobalFoldEmitRefs(fold *ast.FoldExpr) []ast.PermissionRef {
	if fold == nil {
		return nil
	}
	var refs []ast.PermissionRef
	for _, arm := range fold.Arms {
		for _, value := range mutableBorrowEmitValues(arm.Body) {
			refs = append(refs, a.mutableGlobalBorrowTransferRefs(value, a.exprType(value))...)
		}
	}
	return canonicalizePermissionRefs(refs)
}

// mutableGlobalMachinePayloadRefs records mutable global borrows placed into
// the machine's state payloads. Their roots become arm-local bindings after
// lowering, so later writes cannot recover the global source path.
func (a *Analyzer) mutableGlobalMachinePayloadRefs(machine *ast.MachineFromExpr) []ast.PermissionRef {
	if machine == nil {
		return nil
	}
	var refs []ast.PermissionRef
	add := func(value ast.Expr) {
		if value != nil {
			refs = append(refs, a.mutableGlobalBorrowTransferRefs(value, a.exprType(value))...)
		}
	}
	for _, arg := range machine.StartArgs {
		add(arg)
	}
	for _, arm := range machine.Arms {
		for _, term := range arm.Terminators {
			for _, arg := range term.Args {
				add(arg)
			}
		}
	}
	return canonicalizePermissionRefs(refs)
}

// mutableGlobalStoreRowsWriteRefs accounts for the writable row proxies yielded
// by iterating a global SoA store directly. The row binder is a loop-local
// synthetic value, so its later field writes no longer have the global AST root.
func (a *Analyzer) mutableGlobalStoreRowsWriteRefs(source ast.Expr) []ast.PermissionRef {
	root := a.globalStoreRowsCapabilityRoot(source)
	if root == nil {
		return nil
	}
	if sym, global := a.globalStorageRoot(root); !global || sym.Kind != SymbolGlobal {
		return nil
	}
	return globalWriteRefs(root.Pos())
}

// Iterating global SoA rows also reads the global store to form row proxies.
// Direct field assignment on `rows[index].field` remains write-only; this
// read applies to the reference-like view exposed to the loop body.
func (a *Analyzer) mutableGlobalStoreRowsReadRefs(source ast.Expr) []ast.PermissionRef {
	root := a.globalStoreRowsCapabilityRoot(source)
	if root == nil {
		return nil
	}
	if sym, global := a.globalStorageRoot(root); !global || sym.Kind != SymbolGlobal {
		return nil
	}
	return globalReadRefs(root.Pos())
}

func (a *Analyzer) globalStoreRowsCapabilityRoot(source ast.Expr) ast.Expr {
	if !a.hasMutableBorrowCapability(a.exprType(source)) {
		return nil
	}
	return a.globalStoreRowsViewRoot(source, 0)
}

// globalStoreRowsViewRoot finds the global SoA receiver beneath view adapters
// such as `.enumerate()` and `rev(...)`. The source type gate in the caller
// ensures the adapter still yields a row capability.
func (a *Analyzer) globalStoreRowsViewRoot(expr ast.Expr, depth int) ast.Expr {
	if expr == nil || depth > semanticTraversalDepthLimit {
		return nil
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.globalStoreRowsViewRoot(n.Inner, depth+1)
	case *ast.CastExpr:
		return a.globalStoreRowsViewRoot(n.Operand, depth+1)
	case *ast.FieldExpr:
		return a.globalStoreRowsViewRoot(n.Object, depth+1)
	case *ast.IndexExpr:
		return a.globalStoreRowsViewRoot(n.Object, depth+1)
	case *ast.SliceExpr:
		return a.globalStoreRowsViewRoot(n.Object, depth+1)
	case *ast.CallExpr:
		if _, ok := a.exprType(n).(*StoreRowsViewType); ok {
			if field, ok := n.Func.(*ast.FieldExpr); ok && field != nil && field.Field == "rows" && field.Object != nil {
				return globalReferenceStorageExpr(field.Object)
			}
		}
		if field, ok := n.Func.(*ast.FieldExpr); ok && field != nil {
			if root := a.globalStoreRowsViewRoot(field.Object, depth+1); root != nil {
				return root
			}
		}
		for _, arg := range n.Args {
			if root := a.globalStoreRowsViewRoot(arg, depth+1); root != nil {
				return root
			}
		}
	}
	return nil
}
