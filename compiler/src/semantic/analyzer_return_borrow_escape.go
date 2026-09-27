package semantic

import "elisacore/src/ast"

// returnBorrowFlow is a may-flow summary for references contained in a returned
// value. Params names the formal parameters whose storage may be retained by the
// result; Local means the value may retain the current function's stack storage.
// It is intentionally separate from borrowedOwnerRefState: that state tracks
// affine-owner aliases, while this one protects ordinary references in aggregates.
type returnBorrowFlow struct {
	Params map[int]bool
	Local  bool
}

func (f returnBorrowFlow) empty() bool {
	return !f.Local && len(f.Params) == 0
}

func mergeReturnBorrowFlow(left, right returnBorrowFlow) returnBorrowFlow {
	merged := returnBorrowFlow{Local: left.Local || right.Local}
	if len(left.Params)+len(right.Params) == 0 {
		return merged
	}
	merged.Params = make(map[int]bool, len(left.Params)+len(right.Params))
	for index := range left.Params {
		merged.Params[index] = true
	}
	for index := range right.Params {
		merged.Params[index] = true
	}
	return merged
}

func (a *Analyzer) typeCarriesBorrowedStorage(t Type, seen map[Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	switch tt := t.(type) {
	case *RefType, *ViewType, *SViewType, *CStrType:
		return true
	case *ArrayType:
		return a.typeCarriesBorrowedStorage(tt.Elem, seen)
	case *DArrayType:
		return a.typeCarriesBorrowedStorage(tt.Elem, seen)
	case *OptionalType:
		return a.typeCarriesBorrowedStorage(tt.Value, seen)
	case *TupleType:
		for _, field := range tt.Fields {
			if a.typeCarriesBorrowedStorage(field.Type, seen) {
				return true
			}
		}
	case *DictType:
		return a.typeCarriesBorrowedStorage(tt.Key, seen) || a.typeCarriesBorrowedStorage(tt.Value, seen)
	case *SetType:
		return a.typeCarriesBorrowedStorage(tt.Elem, seen)
	case *StructType:
		for _, field := range tt.Fields {
			if a.typeCarriesBorrowedStorage(field.Type, seen) {
				return true
			}
		}
	case *EnumType:
		for _, field := range tt.Common {
			if a.typeCarriesBorrowedStorage(field.Type, seen) {
				return true
			}
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				if a.typeCarriesBorrowedStorage(payload, seen) {
					return true
				}
			}
		}
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			bindings := make(map[string]Type, len(base.TypeParams))
			for i, name := range base.TypeParams {
				if i < len(tt.Args) {
					bindings[name] = tt.Args[i]
				}
			}
			for _, field := range base.Fields {
				fieldType := a.substituteType(field.Type, bindings, nil, nil, nil)
				if a.typeCarriesBorrowedStorage(fieldType, seen) {
					return true
				}
			}
		}
	}
	return false
}

func (a *Analyzer) returnBorrowFlowForExpr(expr ast.Expr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	if expr == nil {
		return returnBorrowFlow{}
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.returnBorrowFlowForExpr(n.Inner, aliases, active, localBindings)
	case *ast.MoveExpr:
		return a.returnBorrowFlowForExpr(n.Operand, aliases, active, localBindings)
	case *ast.CastExpr:
		return a.returnBorrowFlowForExpr(n.Operand, aliases, active, localBindings)
	case *ast.SpecializeExpr:
		return a.returnBorrowFlowForExpr(n.Operand, aliases, active, localBindings)
	case *ast.OptionalBindExpr:
		return a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings)
	case *ast.Ident:
		if flow, ok := aliases[n.Name]; ok {
			return flow
		}
		if a.currentScope == nil {
			return returnBorrowFlow{}
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok || sym == nil || sym.Kind != SymbolLocal || localBindings[sym] {
			return returnBorrowFlow{}
		}
		if value, ok := a.currentValueBindings[sym]; ok && value != nil {
			localBindings[sym] = true
			flow := a.returnBorrowFlowForExpr(value, aliases, active, localBindings)
			delete(localBindings, sym)
			return flow
		}
		return returnBorrowFlow{}
	case *ast.AddrOfExpr:
		if param := a.borrowedContainerParamRoot(n.Operand); param != nil {
			return returnBorrowFlow{Params: map[int]bool{param.ParamIndex: true}}
		}
		if storage, known := a.borrowProvenanceStorage(n); known {
			return returnBorrowFlow{Local: storage == RefStorageStack}
		}
		return returnBorrowFlow{}
	case *ast.StructLitExpr:
		flow := returnBorrowFlow{}
		for _, arg := range n.LoweredArgs() {
			flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(arg, aliases, active, localBindings))
		}
		for _, spread := range n.Spreads {
			flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(spread, aliases, active, localBindings))
		}
		return flow
	case *ast.RecordUpdateExpr:
		flow := a.returnBorrowFlowForExpr(n.Base, aliases, active, localBindings)
		for _, arg := range n.LoweredArgs() {
			flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(arg, aliases, active, localBindings))
		}
		return flow
	case *ast.TupleExpr:
		flow := returnBorrowFlow{}
		for _, elem := range n.Elems {
			flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(elem, aliases, active, localBindings))
		}
		return flow
	case *ast.ListLitExpr:
		flow := returnBorrowFlow{}
		for _, elem := range n.Elems {
			flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(elem, aliases, active, localBindings))
		}
		for _, key := range n.Keys {
			flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(key, aliases, active, localBindings))
		}
		return flow
	case *ast.FieldExpr:
		fieldType := a.exprTypes[n]
		if fieldType == nil || a.typeCarriesBorrowedStorage(fieldType, map[Type]bool{}) {
			return a.returnBorrowFlowForExpr(n.Object, aliases, active, localBindings)
		}
	case *ast.IndexExpr:
		indexType := a.exprTypes[n]
		if indexType == nil || a.typeCarriesBorrowedStorage(indexType, map[Type]bool{}) {
			return a.returnBorrowFlowForExpr(n.Object, aliases, active, localBindings)
		}
	case *ast.TernaryExpr:
		return mergeReturnBorrowFlow(
			a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings),
			a.returnBorrowFlowForExpr(n.Alt, aliases, active, localBindings),
		)
	case *ast.ExprBlock:
		savedScope := a.currentScope
		a.currentScope = NewScope(savedScope)
		blockAliases := cloneReturnBorrowAliases(aliases)
		flow := a.returnBorrowFlowForStatements(n.Stmts, blockAliases, active, localBindings, false)
		flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(n.Value, blockAliases, active, localBindings))
		a.currentScope = savedScope
		return flow
	case *ast.CallExpr:
		return a.returnBorrowFlowForCall(n, aliases, active, localBindings)
	}
	return returnBorrowFlow{}
}

func (a *Analyzer) returnBorrowFlowForCall(call *ast.CallExpr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	decl, ok := a.resolveDirectCallFuncDecl(call)
	if !ok {
		if ident, isIdent := call.Func.(*ast.Ident); isIdent && ident != nil {
			shadowed := false
			if a.currentScope != nil {
				_, shadowed = a.currentScope.Lookup(ident.Name)
			}
			if !shadowed {
				if sym, _, visible := a.lookupVisibleGlobal(ident.Name); visible && sym != nil {
					decl, ok = sym.Node.(*ast.FuncDecl)
				}
			}
		}
	}
	args := call.Args
	if call.ResolvedArgsValid {
		args = call.ResolvedArgs
	}
	if ok && decl != nil {
		summary := a.returnBorrowFlowForFunc(decl, active)
		flow := returnBorrowFlow{Local: summary.Local}
		for index := range summary.Params {
			if index < len(args) {
				argFlow := a.returnBorrowFlowForExpr(args[index], aliases, active, localBindings)
				if argFlow.empty() {
					argFlow = a.returnBorrowFlowForBorrowedArgument(args[index], aliases, active, localBindings)
				}
				flow = mergeReturnBorrowFlow(flow, argFlow)
			}
		}
		return flow
	}
	// Indirect and external calls without a source summary are unknown. Preserve
	// every argument's local-borrow taint rather than silently treating the result
	// as fresh; this can conservatively reject a safe call but cannot bless a leak.
	flow := returnBorrowFlow{}
	for _, arg := range args {
		flow = mergeReturnBorrowFlow(flow, a.returnBorrowFlowForExpr(arg, aliases, active, localBindings))
	}
	return flow
}

func (a *Analyzer) returnBorrowFlowForBorrowedArgument(arg ast.Expr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) returnBorrowFlow {
	root := arg
	for {
		switch n := root.(type) {
		case *ast.ParenExpr:
			root = n.Inner
		case *ast.CastExpr:
			root = n.Operand
		case *ast.MoveExpr:
			root = n.Operand
		case *ast.FieldExpr:
			root = n.Object
		case *ast.IndexExpr:
			root = n.Object
		default:
			goto rooted
		}
	}
rooted:
	ident, ok := root.(*ast.Ident)
	if !ok || ident == nil || a.currentScope == nil {
		return returnBorrowFlow{}
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return returnBorrowFlow{}
	}
	switch sym.Type.(type) {
	case *DArrayType:
		if sym.Kind == SymbolParam {
			return returnBorrowFlow{Params: map[int]bool{sym.ParamIndex: true}}
		}
		if sym.Kind == SymbolLocal {
			return returnBorrowFlow{Local: true}
		}
	}
	if sym.Kind == SymbolParam && a.typeCarriesBorrowedStorage(sym.Type, map[Type]bool{}) {
		return returnBorrowFlow{Params: map[int]bool{sym.ParamIndex: true}}
	}
	if sym.Kind == SymbolLocal {
		if flow, exists := aliases[ident.Name]; exists {
			return flow
		}
		if _, knownBinding := a.currentValueBindings[sym]; !knownBinding && a.typeCarriesBorrowedStorage(sym.Type, map[Type]bool{}) {
			return returnBorrowFlow{Local: true}
		}
	}
	return returnBorrowFlow{}
}

func (a *Analyzer) borrowedContainerParamRoot(expr ast.Expr) *Symbol {
	root := expr
	for {
		switch n := root.(type) {
		case *ast.ParenExpr:
			root = n.Inner
		case *ast.CastExpr:
			root = n.Operand
		case *ast.FieldExpr:
			root = n.Object
		case *ast.IndexExpr:
			root = n.Object
		default:
			goto rooted
		}
	}
rooted:
	ident, ok := root.(*ast.Ident)
	if !ok || ident == nil || a.currentScope == nil {
		return nil
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil || sym.Kind != SymbolParam {
		return nil
	}
	if _, ok := sym.Type.(*DArrayType); !ok {
		return nil
	}
	return sym
}

func (a *Analyzer) returnBorrowFlowForFunc(fn *ast.FuncDecl, active map[*ast.FuncDecl]bool) returnBorrowFlow {
	if fn == nil {
		return returnBorrowFlow{}
	}
	if active[fn] {
		return a.allBorrowedParamFlow(fn)
	}
	if len(active) >= semanticTraversalDepthLimit {
		return a.allBorrowedParamFlow(fn)
	}
	active[fn] = true
	defer delete(active, fn)

	savedNamespace, savedUsings, savedScope := a.currentNamespace, a.currentUsings, a.currentScope
	if sym := a.funcDeclSymbols[fn]; sym != nil {
		if index := lastDotIndex(sym.Name); index >= 0 {
			a.currentNamespace = sym.Name[:index]
		} else {
			a.currentNamespace = ""
		}
	}
	a.currentUsings = append([]string(nil), a.funcDeclUsings[fn]...)
	defer func() {
		a.currentNamespace, a.currentUsings, a.currentScope = savedNamespace, savedUsings, savedScope
	}()

	aliases := map[string]returnBorrowFlow{}
	params := a.expandedFuncDeclParams(fn)
	var fnType *FuncType
	if sym := a.funcDeclSymbols[fn]; sym != nil {
		fnType, _ = sym.Type.(*FuncType)
	}
	a.currentScope = NewScope(a.globalScope)
	for index, param := range params {
		var paramType Type
		if fnType != nil && index < len(fnType.Params) {
			paramType = fnType.Params[index]
		}
		if paramType == nil {
			paramType = a.resolveType(param.Type)
		}
		a.currentScope.Define(&Symbol{Name: param.Name, Kind: SymbolParam, Type: paramType, ParamIndex: index, Node: fn})
		if a.typeCarriesBorrowedStorage(paramType, map[Type]bool{}) {
			aliases[param.Name] = returnBorrowFlow{Params: map[int]bool{index: true}}
		}
	}
	return a.returnBorrowFlowForStatements(fn.Body, aliases, active, map[*Symbol]bool{}, true)
}

func (a *Analyzer) allBorrowedParamFlow(fn *ast.FuncDecl) returnBorrowFlow {
	flow := returnBorrowFlow{}
	if fn == nil {
		return flow
	}
	if sym := a.funcDeclSymbols[fn]; sym != nil {
		if fnType, ok := sym.Type.(*FuncType); ok {
			for index, paramType := range fnType.Params {
				if a.typeCarriesBorrowedStorage(paramType, map[Type]bool{}) {
					if flow.Params == nil {
						flow.Params = map[int]bool{}
					}
					flow.Params[index] = true
				}
			}
		}
	}
	return flow
}

func (a *Analyzer) returnBorrowFlowForStatements(stmts []ast.Stmt, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool, tailValue bool) returnBorrowFlow {
	var returned returnBorrowFlow
	for index, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.VarDeclStmt:
			aliases[n.Name] = a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings)
			if a.currentScope != nil {
				typ := a.exprTypes[n.Value]
				if typ == nil && n.Type != nil {
					typ = a.resolveType(n.Type)
				}
				a.currentScope.Define(&Symbol{Name: n.Name, Kind: SymbolLocal, Type: typ, Node: n})
			}
		case *ast.AssignStmt:
			if ident, ok := n.Target.(*ast.Ident); ok {
				aliases[ident.Name] = mergeReturnBorrowFlow(aliases[ident.Name], a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings))
			}
		case *ast.ReturnStmt:
			returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings))
		case *ast.ExprStmt:
			if tailValue && index+1 == len(stmts) {
				returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForExpr(n.Expr, aliases, active, localBindings))
			}
		case *ast.IfStmt:
			branchEnvs := make([]map[string]returnBorrowFlow, 0, len(n.Elifs)+2)
			outerScope := a.currentScope
			thenEnv := cloneReturnBorrowAliases(aliases)
			a.currentScope = NewScope(outerScope)
			returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(n.Then, thenEnv, active, localBindings, false))
			branchEnvs = append(branchEnvs, thenEnv)
			for _, clause := range n.Elifs {
				elifEnv := cloneReturnBorrowAliases(aliases)
				a.currentScope = NewScope(outerScope)
				returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(clause.Body, elifEnv, active, localBindings, false))
				branchEnvs = append(branchEnvs, elifEnv)
			}
			elseEnv := cloneReturnBorrowAliases(aliases)
			a.currentScope = NewScope(outerScope)
			returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(n.Else, elseEnv, active, localBindings, false))
			a.currentScope = outerScope
			branchEnvs = append(branchEnvs, elseEnv)
			for _, env := range branchEnvs {
				mergeReturnBorrowAliasMaps(aliases, env)
			}
		case *ast.WhileStmt:
			loopEnv := cloneReturnBorrowAliases(aliases)
			outerScope := a.currentScope
			a.currentScope = NewScope(outerScope)
			returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(n.Body, loopEnv, active, localBindings, false))
			a.currentScope = outerScope
			mergeReturnBorrowAliasMaps(aliases, loopEnv)
		case *ast.ForStmt:
			loopEnv := cloneReturnBorrowAliases(aliases)
			outerScope := a.currentScope
			a.currentScope = NewScope(outerScope)
			returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(n.Body, loopEnv, active, localBindings, false))
			a.currentScope = outerScope
			mergeReturnBorrowAliasMaps(aliases, loopEnv)
		case *ast.MatchStmt:
			for _, arm := range n.Arms {
				armEnv := cloneReturnBorrowAliases(aliases)
				outerScope := a.currentScope
				a.currentScope = NewScope(outerScope)
				returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(arm.Body, armEnv, active, localBindings, false))
				a.currentScope = outerScope
				mergeReturnBorrowAliasMaps(aliases, armEnv)
			}
		}
	}
	return returned
}

func cloneReturnBorrowAliases(src map[string]returnBorrowFlow) map[string]returnBorrowFlow {
	cloned := make(map[string]returnBorrowFlow, len(src))
	for name, flow := range src {
		cloned[name] = flow
	}
	return cloned
}

func mergeReturnBorrowAliasMaps(dst, src map[string]returnBorrowFlow) {
	for name, flow := range src {
		dst[name] = mergeReturnBorrowFlow(dst[name], flow)
	}
}

func lastDotIndex(value string) int {
	for index := len(value) - 1; index >= 0; index-- {
		if value[index] == '.' {
			return index
		}
	}
	return -1
}
