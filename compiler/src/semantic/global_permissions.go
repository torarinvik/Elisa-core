package semantic

import "elisacore/src/ast"

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
	if _, ok := a.exprTypes[expr].(*RefType); !ok {
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
			if _, ok := a.exprTypes[operand].(*RefType); ok {
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
	if !a.hasMutableBorrowCapability(a.exprTypes[expr]) {
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
		if expr == nil || depth > semanticTraversalDepthLimit || !a.hasMutableBorrowCapability(a.exprTypes[expr]) {
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
	fillMayAdoptWalkTyped(body, func(node any) {
		if fold, ok := node.(*ast.FoldExpr); ok {
			nestedFolds = append(nestedFolds, fold)
		}
		if lambda, ok := node.(*ast.LambdaExpr); ok {
			nestedLambdas = append(nestedLambdas, lambda)
		}
	}, map[uintptr]bool{})

	nestedEmits := map[*ast.EmitExpr]bool{}
	markNestedEmits := func(value any) {
		fillMayAdoptWalkTyped(value, func(node any) {
			if emitted, ok := node.(*ast.EmitExpr); ok {
				nestedEmits[emitted] = true
			}
		}, map[uintptr]bool{})
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
	fillMayAdoptWalkTyped(body, func(node any) {
		if emitted, ok := node.(*ast.EmitExpr); ok && emitted.Value != nil && !nestedEmits[emitted] {
			values = append(values, emitted.Value)
		}
	}, map[uintptr]bool{})
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
			refs = append(refs, a.mutableGlobalBorrowTransferRefs(value, a.exprTypes[value])...)
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
			refs = append(refs, a.mutableGlobalBorrowTransferRefs(value, a.exprTypes[value])...)
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
	if !a.hasMutableBorrowCapability(a.exprTypes[source]) {
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
		if _, ok := a.exprTypes[n].(*StoreRowsViewType); ok {
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

// globalWholeValueReadRequiresGrant reports whether reading expr as a value
// observes mutable storage nested inside an otherwise immutable global value.
// Projection receivers are handled separately so an immutable sibling field
// does not inherit the grant requirement from another field in the aggregate.
func (a *Analyzer) globalWholeValueReadRequiresGrant(expr ast.Expr) bool {
	sym, ok := a.globalStorageRoot(expr)
	if !ok || sym.Kind != SymbolGlobal || sym.Mutable {
		return false
	}
	typ, typed := a.exprTypes[expr]
	if !typed {
		if _, ident := expr.(*ast.Ident); ident {
			typ = sym.Type
		} else {
			return false
		}
	}
	return a.typeContainsMutableStorage(typ, map[Type]bool{}, map[*StructType][][]Type{}, 0)
}

// typeContainsMutableStorage checks whether a value can expose writable state
// through one of its fields, container elements, or mutable references.
func (a *Analyzer) typeContainsMutableStorage(typ Type, seen map[Type]bool, genericSeen map[*StructType][][]Type, depth int) bool {
	if typ == nil {
		return false
	}
	typ = StripAggregateStateType(typ)
	if depth > semanticTraversalDepthLimit {
		a.reportSemanticDepthLimit("global mutable-storage traversal", semanticTraversalDepthLimit)
		// Treat an incomplete traversal as mutable so it cannot silently omit
		// a required Global.Read effect.
		return true
	}
	if seen[typ] {
		return false
	}
	seen[typ] = true
	contains := func(child Type) bool {
		return a.typeContainsMutableStorage(child, seen, genericSeen, depth+1)
	}
	fieldContains := func(field Field) bool {
		if field.Ghost || field.Phantom {
			return false
		}
		return field.Mutable || contains(field.Type)
	}
	switch t := typ.(type) {
	case *RefType:
		return t != nil && t.Mutable
	case *BitGroupType:
		return t != nil && len(t.Members) != 0
	case *ArrayType:
		return t != nil && contains(t.Elem)
	case *DArrayType:
		return t != nil && contains(t.Elem)
	case *ViewType:
		return t != nil && (t.Mutable || contains(t.Elem))
	case *OptionalType:
		return t != nil && contains(t.Value)
	case *ErrorUnionType:
		if t == nil {
			return false
		}
		if contains(t.Value) {
			return true
		}
		if t.Errors != nil {
			for _, payloads := range t.Errors.Payloads {
				for _, payload := range payloads {
					if contains(payload) {
						return true
					}
				}
			}
		}
		return false
	case *TupleType:
		if t != nil {
			for _, field := range t.Fields {
				if contains(field.Type) {
					return true
				}
			}
		}
	case *DictType:
		return t != nil && (contains(t.Key) || contains(t.Value))
	case *SetType:
		return t != nil && contains(t.Elem)
	case *DictEntryType:
		return t != nil && (t.Mutable || contains(t.Dict))
	case *StoreRowsViewType:
		return t != nil
	case *StoreRowViewType:
		return t != nil
	case *StructType:
		if t != nil {
			for _, field := range t.Fields {
				if fieldContains(field) {
					return true
				}
			}
		}
	case *GenericInstanceType:
		if t == nil {
			return false
		}
		if base, ok := t.Base.(*StructType); ok && base != nil {
			for _, previousArgs := range genericSeen[base] {
				if len(previousArgs) != len(t.Args) {
					continue
				}
				sameArgs := true
				for i := range previousArgs {
					if !SameType(previousArgs[i], t.Args[i]) {
						sameArgs = false
						break
					}
				}
				if sameArgs {
					return false
				}
			}
			genericSeen[base] = append(genericSeen[base], append([]Type(nil), t.Args...))
			bindings := genericBindingsForStructInstance(base, t.Args)
			for _, field := range base.Fields {
				if field.Ghost || field.Phantom {
					continue
				}
				if field.Mutable {
					return true
				}
				fieldType := a.substituteType(field.Type, bindings, nil, nil, nil)
				if contains(fieldType) {
					return true
				}
			}
			return false
		}
		for _, arg := range t.Args {
			if contains(arg) {
				return true
			}
		}
		return contains(t.Base)
	case *EnumType:
		if t != nil {
			for _, field := range t.Common {
				if fieldContains(field) {
					return true
				}
			}
			for _, variant := range t.Variants {
				if variant == nil {
					continue
				}
				for _, payload := range variant.Payload {
					if contains(payload) {
						return true
					}
				}
			}
		}
	case *PackedVariantViewType:
		if t != nil {
			if contains(t.Enum) {
				return true
			}
			if t.Variant != nil {
				for _, payload := range t.Variant.Payload {
					if contains(payload) {
						return true
					}
				}
			}
		}
	case *TypeParamType:
		// A generic value's mutability is not known until specialization. Keep
		// the mandatory read when this traversal cannot resolve the parameter.
		return true
	}
	return false
}

// globalPathHasMutableField reports whether a global-rooted projection selects a
// mutable field. Such a field is writable even when the global binding itself is
// immutable, and mutating collection methods may use that field as their receiver.
func (a *Analyzer) globalPathHasMutableField(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.FieldExpr:
		field, ok := a.lookupFieldNoError(a.exprTypes[n.Object], n.Field)
		return (ok && field.Mutable) || a.globalPathHasMutableField(n.Object)
	case *ast.IndexExpr:
		return a.globalPathHasMutableField(n.Object)
	case *ast.SliceExpr:
		return a.globalPathHasMutableField(n.Object)
	case *ast.ParenExpr:
		return a.globalPathHasMutableField(n.Inner)
	case *ast.AddrOfExpr:
		return a.globalPathHasMutableField(n.Operand)
	case *ast.CastExpr:
		if operand, _, ok := a.globalStorageCastOperand(n); ok {
			return a.globalPathHasMutableField(operand)
		}
		return false
	default:
		return false
	}
}

// globalProjectionBeginsMutablePath reports whether expr is the first projection
// that reaches mutable global storage. Reads use this to require Global.Read once
// per access path, even when the root global binding itself is immutable.
func (a *Analyzer) globalProjectionBeginsMutablePath(expr ast.Expr) bool {
	var object ast.Expr
	switch n := expr.(type) {
	case *ast.FieldExpr:
		object = n.Object
	case *ast.IndexExpr:
		object = n.Object
	case *ast.SliceExpr:
		object = n.Object
	default:
		return false
	}
	if _, mutable := a.mutableGlobalStorageRoot(expr); !mutable {
		return false
	}
	_, parentMutable := a.mutableGlobalStorageRoot(object)
	return !parentMutable
}

func mutableBorrowStorageType(t Type) bool {
	for {
		switch n := t.(type) {
		case *OptionalType:
			if n == nil {
				return false
			}
			t = n.Value
		case *RefType:
			return n != nil && n.Mutable
		case *ViewType:
			return n != nil && n.Mutable
		default:
			return false
		}
	}
}

// typeContainsMutableBorrowCapability reports whether a value can carry a
// writable reference or view, including through aggregate fields and elements.
// This is deliberately separate from typeContainsMutableStorage: a mutable
// by-value field makes its global storage writable, but copying that aggregate
// does not transfer a borrow capability.
func (a *Analyzer) typeContainsMutableBorrowCapability(typ Type, seen map[Type]bool, genericSeen map[*StructType][][]Type, depth int) bool {
	if typ == nil {
		return false
	}
	typ = StripAggregateStateType(typ)
	if depth > semanticTraversalDepthLimit {
		a.reportSemanticDepthLimit("global mutable-borrow capability traversal", semanticTraversalDepthLimit)
		// An incomplete traversal must not let a nested mutable capability escape
		// without its mandatory Global.Write effect.
		return true
	}
	// These cases are terminal for this query. Avoid writing them to the cycle
	// map: they cannot lead back to an ancestor, and their result does not depend
	// on any child type. Keep the depth check above this fast path so oversized
	// paths still report the same conservative diagnostic.
	switch t := typ.(type) {
	case *BuiltinType, *NeverType, *NullType, *InvalidType, *ErrorSetType, *OpaqueType:
		return false
	case *TypeParamType:
		// Until specialization, an opaque type parameter may carry a mutable
		// borrow. Conservatively require the effect at transfer boundaries.
		return !seen[typ]
	case *StoreRowsViewType:
		// Row views are writable proxies into backing store columns.
		return t != nil && !seen[typ]
	case *StoreRowViewType:
		return t != nil && !seen[typ]
	}
	if seen[typ] {
		return false
	}
	seen[typ] = true
	contains := func(child Type) bool {
		return a.typeContainsMutableBorrowCapability(child, seen, genericSeen, depth+1)
	}
	fieldContains := func(field Field) bool {
		if field.Ghost || field.Phantom {
			return false
		}
		return contains(field.Type)
	}
	switch t := typ.(type) {
	case *RefType:
		return t != nil && (t.Mutable || contains(t.Elem))
	case *ViewType:
		return t != nil && (t.Mutable || contains(t.Elem))
	case *ArrayType:
		return t != nil && contains(t.Elem)
	case *DArrayType:
		return t != nil && contains(t.Elem)
	case *OptionalType:
		return t != nil && contains(t.Value)
	case *ErrorUnionType:
		if t == nil {
			return false
		}
		if contains(t.Value) {
			return true
		}
		if t.Errors != nil {
			for _, payloads := range t.Errors.Payloads {
				for _, payload := range payloads {
					if contains(payload) {
						return true
					}
				}
			}
		}
		return false
	case *TupleType:
		if t != nil {
			for _, field := range t.Fields {
				if contains(field.Type) {
					return true
				}
			}
		}
	case *DictType:
		return t != nil && (contains(t.Key) || contains(t.Value))
	case *SetType:
		return t != nil && contains(t.Elem)
	case *DictEntryType:
		return t != nil && (t.Mutable || contains(t.Dict))
	case *StoreRowsViewType:
		// Iteration yields StoreRowViewType proxies whose fields write through to
		// the store's backing columns. Keep the capability when the rows view is
		// copied, passed, or returned.
		return t != nil
	case *StoreRowViewType:
		// A row view is an addressable proxy into a SoA store, not a detached row
		// value; its synthetic fields are writable even when the binding is local.
		return t != nil
	case *StructType:
		if t != nil {
			for _, field := range t.Fields {
				if fieldContains(field) {
					return true
				}
			}
		}
	case *GenericInstanceType:
		if t == nil {
			return false
		}
		if base, ok := t.Base.(*StructType); ok && base != nil {
			for _, previousArgs := range genericSeen[base] {
				if len(previousArgs) != len(t.Args) {
					continue
				}
				sameArgs := true
				for i := range previousArgs {
					if !SameType(previousArgs[i], t.Args[i]) {
						sameArgs = false
						break
					}
				}
				if sameArgs {
					return false
				}
			}
			genericSeen[base] = append(genericSeen[base], append([]Type(nil), t.Args...))
			bindings := genericBindingsForStructInstance(base, t.Args)
			for _, field := range base.Fields {
				if field.Ghost || field.Phantom {
					continue
				}
				fieldType := a.substituteType(field.Type, bindings, nil, nil, nil)
				if contains(fieldType) {
					return true
				}
			}
			return false
		}
		for _, arg := range t.Args {
			if contains(arg) {
				return true
			}
		}
		return contains(t.Base)
	case *EnumType:
		if t != nil {
			for _, field := range t.Common {
				if fieldContains(field) {
					return true
				}
			}
			for _, variant := range t.Variants {
				if variant == nil {
					continue
				}
				for _, payload := range variant.Payload {
					if contains(payload) {
						return true
					}
				}
			}
		}
	case *PackedVariantViewType:
		if t != nil {
			if contains(t.Enum) {
				return true
			}
			if t.Variant != nil {
				for _, payload := range t.Variant.Payload {
					if contains(payload) {
						return true
					}
				}
			}
		}
	case *PackedEnumStoreType:
		return t != nil && (contains(t.Enum) || contains(t.State))
	}
	return false
}

// globalPathLoadsMutableBorrow reports whether accessing this global-rooted path loads a
// mutable reference or view from storage. A direct field/index projection into a global
// aggregate is address formation, but projecting through a stored borrow reaches mutable state
// even when the global binding itself is immutable.
func (a *Analyzer) globalPathLoadsMutableBorrow(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.Ident:
		sym, ok := a.globalStorageRoot(n)
		return ok && sym.Kind == SymbolGlobal && !sym.Mutable && mutableBorrowStorageType(sym.Type)
	case *ast.FieldExpr:
		return a.globalPathObjectLoadsMutableBorrow(n.Object)
	case *ast.IndexExpr:
		return a.globalPathObjectLoadsMutableBorrow(n.Object)
	case *ast.SliceExpr:
		return a.globalPathObjectLoadsMutableBorrow(n.Object)
	case *ast.ParenExpr:
		return a.globalPathLoadsMutableBorrow(n.Inner)
	case *ast.AddrOfExpr:
		return a.globalPathLoadsMutableBorrow(n.Operand)
	case *ast.CastExpr:
		if operand, addressFormed, ok := a.globalStorageCastOperand(n); ok {
			if addressFormed {
				return a.globalPathLoadsMutableBorrow(operand)
			}
			return mutableBorrowStorageType(a.exprTypes[n.Operand]) || a.globalPathLoadsMutableBorrow(operand)
		}
		return false
	default:
		return false
	}
}

// globalPathObjectLoadsMutableBorrow distinguishes a reference loaded from
// storage from one formed by taking the address of a place. Projection through
// the former reads the stored capability; projection through the latter only
// forms an address to the existing place.
func (a *Analyzer) globalPathObjectLoadsMutableBorrow(expr ast.Expr) bool {
	place := expr
	for {
		switch n := place.(type) {
		case *ast.ParenExpr:
			place = n.Inner
		case *ast.AddrOfExpr:
			return a.globalPathLoadsMutableBorrow(n.Operand)
		case *ast.CastExpr:
			if operand, addressFormed, ok := a.globalStorageCastOperand(n); ok && addressFormed {
				return a.globalPathLoadsMutableBorrow(operand)
			}
			return mutableBorrowStorageType(a.exprTypes[expr]) || a.globalPathLoadsMutableBorrow(expr)
		default:
			return mutableBorrowStorageType(a.exprTypes[expr]) || a.globalPathLoadsMutableBorrow(expr)
		}
	}
}

// Calls through mutable references and mutating collection receivers can write
// global storage even when the syntax contains no assignment statement.
func (a *Analyzer) mutableGlobalCallWriteRefs(call *ast.CallExpr) []ast.PermissionRef {
	var refs []ast.PermissionRef
	add := func(expr ast.Expr) {
		if _, ok := a.mutableGlobalBorrowStorageRoot(expr); ok {
			refs = append(refs, globalWriteRefs(expr.Pos())...)
			return
		}
		for {
			switch n := expr.(type) {
			case *ast.AddrOfExpr:
				expr = n.Operand
			case *ast.CastExpr:
				expr = n.Operand
			case *ast.ParenExpr:
				expr = n.Inner
			default:
				if _, ok := a.mutableGlobalBorrowStorageRoot(expr); ok {
					refs = append(refs, globalWriteRefs(expr.Pos())...)
				}
				return
			}
		}
	}
	if receiver, ok := a.mutatingBuiltinMethodReceiver(call); ok {
		add(receiver)
	}
	if ft, ok := a.exprTypes[call.Func].(*FuncType); ok {
		args := call.Args
		if aligned, ok := a.callAlignedAliasArgs[call]; ok {
			args = aligned
		}
		for i, arg := range args {
			if i >= len(ft.Params) {
				break
			}
			if a.hasMutableBorrowCapability(ft.Params[i]) {
				refs = append(refs, a.mutableGlobalBorrowTransferRefs(arg, ft.Params[i])...)
			}
		}
	}
	return canonicalizePermissionRefs(refs)
}

func globalReferenceStorageExpr(expr ast.Expr) ast.Expr {
	for {
		switch n := expr.(type) {
		case *ast.AddrOfExpr:
			expr = n.Operand
		case *ast.MoveExpr:
			expr = n.Operand
		case *ast.CastExpr:
			expr = n.Operand
		case *ast.ParenExpr:
			expr = n.Inner
		default:
			return expr
		}
	}
}

func (a *Analyzer) mutableGlobalReturnedRefRefs(value ast.Expr, returnType Type) []ast.PermissionRef {
	return a.mutableGlobalBorrowTransferRefs(value, returnType)
}

func (a *Analyzer) mandatoryGlobalPermissionRefs(fn *FuncType) []ast.PermissionRef {
	if fn == nil {
		return nil
	}
	a.observePermissionFunction(fn)
	refs := append([]ast.PermissionRef(nil), fn.MutableGlobalPermissionRefs...)
	for _, name := range fn.FunctionReturnSourceNames {
		if factory := a.functionTypes[name]; factory != nil {
			a.observePermissionFunction(factory)
			refs = append(refs, factory.ReturnedFunctionGlobalPermissionRefs...)
		}
	}
	for _, name := range fn.MutableGlobalSourceNames {
		if source := a.functionTypes[name]; source != nil {
			a.observePermissionFunction(source)
			refs = append(refs, source.MutableGlobalPermissionRefs...)
		}
	}
	// Generic/function-value copies may predate the effect fixpoint. Resolve the
	// named declaration's final mutable row instead of trusting that stale copy.
	if sym, _, ok := a.lookupVisibleGlobal(fn.Name); ok {
		if declared, ok := sym.Type.(*FuncType); ok {
			a.observePermissionFunction(declared)
			refs = append(refs, declared.MutableGlobalPermissionRefs...)
		}
	}
	for _, ref := range fn.DeclaredPermissionRefs {
		if isGlobalPermissionRef(ref) {
			refs = append(refs, ref)
		}
	}
	return canonicalizePermissionRefs(refs)
}

// Handing a mutable-global callback to a higher-order callee transfers the
// ability to perform its accesses, just as handing out a writable reference does.
func (a *Analyzer) mutableGlobalCallbackRefs(call *ast.CallExpr) []ast.PermissionRef {
	var refs []ast.PermissionRef
	for _, arg := range call.LoweredArgs() {
		if callback, ok := a.exprTypes[arg].(*FuncType); ok {
			refs = append(refs, a.mandatoryGlobalPermissionRefs(callback)...)
		}
	}
	return canonicalizePermissionRefs(refs)
}

// Returned callback capabilities are separate from execution effects: producing
// a callback is pure, but invoking it must retain its concrete member row.
func (a *Analyzer) effectiveFunctionPermissionRefs(fn *FuncType) []ast.PermissionRef {
	if fn == nil {
		return nil
	}
	a.observePermissionFunction(fn)
	refs := append([]ast.PermissionRef(nil), functionPermissionRefs(fn)...)
	for _, name := range fn.MutableGlobalSourceNames {
		if source := a.functionTypes[name]; source != nil {
			a.observePermissionFunction(source)
			refs = append(refs, functionPermissionRefs(source)...)
		}
	}
	if source := a.functionTypes[fn.Name]; source != nil {
		a.observePermissionFunction(source)
		refs = append(refs, functionPermissionRefs(source)...)
	}
	for _, name := range fn.FunctionReturnSourceNames {
		if factory := a.functionTypes[name]; factory != nil {
			a.observePermissionFunction(factory)
			refs = append(refs, factory.ReturnedFunctionPermissionRefs...)
		}
	}
	return canonicalizePermissionRefs(refs)
}
