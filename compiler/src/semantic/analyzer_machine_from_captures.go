package semantic

import (
	"elisacore/src/ast"
)

// machineFromCaptureRoots collects roots whose arm-body writes or known mutating calls need
// the desugared loop's capture manifest (docs/120). Arm declarations and payload binders are
// lexical locals of the generated match arm, not outer state, so they must never be captured.
// Calls are included only when their already-known signature marks an argument as a mutable
// reference, or when the analyzer recognizes a mutating builtin collection receiver. This
// keeps the capture set precise instead of licensing every value merely mentioned by a call.
func (a *Analyzer) machineFromCaptureRoots(expr *ast.MachineFromExpr) []string {
	seen := map[string]bool{}
	var roots []string
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			roots = append(roots, name)
		}
	}
	var walkAssignments func([]ast.Stmt, map[string]bool)
	walkAssignments = func(stmts []ast.Stmt, locals map[string]bool) {
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *ast.AssignStmt:
				name := machineFromRootIdent(s.Target)
				if !locals[name] {
					add(name)
				}
			case *ast.AugAssignStmt:
				name := machineFromRootIdent(s.Target)
				if !locals[name] {
					add(name)
				}
			case *ast.AsRefAssignStmt:
				name := machineFromRootIdent(s.Target)
				if !locals[name] {
					add(name)
				}
			case *ast.IfStmt:
				walkAssignments(s.Then, locals)
				for _, e := range s.Elifs {
					walkAssignments(e.Body, locals)
				}
				walkAssignments(s.Else, locals)
			case *ast.WhileStmt:
				walkAssignments(s.Body, locals)
			case *ast.ForStmt:
				walkAssignments(s.Body, locals)
			case *ast.IterForStmt:
				walkAssignments(s.Body, locals)
			case *ast.MatchStmt:
				for _, arm := range s.Arms {
					walkAssignments(arm.Body, locals)
				}
			case *ast.CanStmt:
				walkAssignments(s.Body, locals)
			}
		}
	}
	collectLocals := func(stmts []ast.Stmt, locals map[string]bool) {
		var collect func([]ast.Stmt)
		collect = func(body []ast.Stmt) {
			for _, stmt := range body {
				switch s := stmt.(type) {
				case *ast.VarDeclStmt:
					if s.Name != "" {
						locals[s.Name] = true
					}
				case *ast.IfStmt:
					collect(s.Then)
					for _, e := range s.Elifs {
						collect(e.Body)
					}
					collect(s.Else)
				case *ast.WhileStmt:
					collect(s.Body)
				case *ast.ForStmt:
					collect(s.Body)
				case *ast.IterForStmt:
					collect(s.Body)
				case *ast.MatchStmt:
					for _, arm := range s.Arms {
						collect(arm.Body)
					}
				case *ast.CanStmt:
					collect(s.Body)
				}
			}
		}
		collect(stmts)
	}
	addCallRoot := func(expr ast.Expr, locals map[string]bool) {
		name := machineFromRootIdent(expr)
		if name == "" || locals[name] || !a.isMutableBinding(name) {
			return
		}
		add(name)
	}
	visitCall := func(call *ast.CallExpr, locals map[string]bool) {
		if call == nil {
			return
		}
		args := call.Args
		if ft, ok, prependReceiver := a.machineFromCallType(call); ok {
			args = machineFromCallArguments(a, call, ft, prependReceiver)
			limit := funcTypeExplicitParamCount(ft)
			if len(args) < limit {
				limit = len(args)
			}
			if len(ft.Params) < limit {
				limit = len(ft.Params)
			}
			for index := 0; index < limit; index++ {
				ref, isRef := ft.Params[index].(*RefType)
				if isRef && ref != nil && ref.Mutable {
					addCallRoot(args[index], locals)
				}
			}
		}
		if receiver, ok := a.machineFromMutatingBuiltinReceiver(call); ok {
			addCallRoot(receiver, locals)
		}
		if call.LmutThreadEffect {
			for _, arg := range args {
				addCallRoot(arg, locals)
			}
			if field, ok := call.Func.(*ast.FieldExpr); ok {
				addCallRoot(field.Object, locals)
			}
		}
		for _, claim := range call.LmutRebindClaims {
			addCallRoot(&ast.Ident{Name: claim.Name}, locals)
		}
	}
	collectCalls := func(value ast.Expr, locals map[string]bool) {
		a.walkStaticExpr(value, func(value ast.Expr) bool {
			call, ok := value.(*ast.CallExpr)
			if ok {
				visitCall(call, locals)
			}
			return false
		})
	}
	for i := range expr.Arms {
		arm := &expr.Arms[i]
		locals := map[string]bool{}
		for _, name := range arm.Bindings {
			if name != "" {
				locals[name] = true
			}
		}
		collectLocals(arm.Body, locals)
		walkAssignments(arm.Body, locals)
		a.walkStaticStmts(arm.Body, func(value ast.Expr) bool {
			if call, ok := value.(*ast.CallExpr); ok {
				visitCall(call, locals)
			}
			return false
		})
		for _, term := range arm.Terminators {
			collectCalls(term.Guard, locals)
			collectCalls(term.Value, locals)
			for _, arg := range term.Args {
				collectCalls(arg, locals)
			}
		}
	}
	return roots
}

// machineFromMutatingBuiltinReceiver includes the ordinary value-receiver builtins plus
// darray.pop. pop is analyzed through a synthesized mutable-ref signature, so it is not in
// mutatingBuiltinCollectionMethods; before arm analysis, however, that signature does not exist
// yet and the receiver still needs to be captured.
func (a *Analyzer) machineFromMutatingBuiltinReceiver(call *ast.CallExpr) (ast.Expr, bool) {
	if receiver, ok := a.mutatingBuiltinMethodReceiver(call); ok {
		return receiver, true
	}
	field := machineFromFieldCallCallee(call.Func)
	if field == nil || field.Field != "pop" || field.Object == nil {
		return nil, false
	}
	receiverType := a.machineFromStaticExprType(field.Object)
	for {
		switch current := receiverType.(type) {
		case *RefType:
			receiverType = current.Elem
		case *AggregateStateType:
			receiverType = current.Base
		default:
			if _, ok := receiverType.(*DArrayType); ok {
				return field.Object, true
			}
			return nil, false
		}
	}
}

// machineFromCallType resolves the call signature without analyzing the call. The machine's
// capture manifest is built before its generated value block is analyzed, so a method call can
// still be in its source UFCS form (`value.method(...)`) here. The bool reports whether the
// receiver must be prepended to the source arguments for parameter-position matching.
func (a *Analyzer) machineFromCallType(call *ast.CallExpr) (*FuncType, bool, bool) {
	if a == nil || call == nil {
		return nil, false, false
	}
	if ft, ok := a.callFuncType(call); ok {
		return ft, true, false
	}
	field := machineFromFieldCallCallee(call.Func)
	if field == nil || field.Object == nil || field.Field == "" {
		return nil, false, false
	}
	receiverType := a.machineFromStaticExprType(field.Object)
	if receiverType == nil || IsInvalidType(receiverType) {
		return nil, false, false
	}
	// A real value member has a signature whose parameters do not include the receiver.
	if member, ok := a.lookupFieldNoError(receiverType, field.Field); ok {
		if ft, ok := member.Type.(*FuncType); ok && ft != nil {
			return ft, true, false
		}
	}
	// Extension methods and UFCS functions are lowered to receiver-first calls by the normal
	// analyzer. Do the same positional accounting here, but do not report ambiguity or other
	// diagnostics from this speculative pre-lowering lookup; the real call analysis owns those.
	if method, ok, err := a.lookupVisibleExtensionMethod(field.Field, receiverType); err == nil && ok && method != nil && method.Symbol != nil {
		if ft, ok := method.Symbol.Type.(*FuncType); ok && ft != nil {
			return ft, true, true
		}
	}
	if symbol, ok, err := a.lookupVisibleUFCSFunctionWithArity(field.Field, receiverType, 1+len(call.Args)); err == nil && ok && symbol != nil {
		if ft, ok := symbol.Type.(*FuncType); ok && ft != nil {
			return ft, true, true
		}
	}
	return nil, false, false
}

func machineFromFieldCallCallee(callee ast.Expr) *ast.FieldExpr {
	callee = stripOptimizationParens(callee)
	if field, ok := callee.(*ast.FieldExpr); ok && field != nil {
		return field
	}
	if specialized, ok := callee.(*ast.SpecializeExpr); ok && specialized != nil {
		field, _ := stripOptimizationParens(specialized.Operand).(*ast.FieldExpr)
		return field
	}
	return nil
}

// machineFromStaticExprType performs the small, side-effect-free type walk needed by the
// pre-lowering capture pass. It intentionally uses only already-collected symbols and field
// descriptors; calling analyzeExpr here would mutate flow state and duplicate diagnostics.
func (a *Analyzer) machineFromStaticExprType(expr ast.Expr) Type {
	if a == nil || expr == nil {
		return nil
	}
	if typ := a.exprType(expr); typ != nil {
		return typ
	}
	switch n := stripOptimizationParens(expr).(type) {
	case *ast.Ident:
		if n == nil || a.currentScope == nil {
			return nil
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok || sym == nil || !a.globalNameIsVisible(sym, n.Name) {
			return nil
		}
		return promoteWritableRefType(sym.Type, symbolPromotesWritableRef(sym))
	case *ast.FieldExpr:
		if n == nil || n.Object == nil || n.Field == "" {
			return nil
		}
		objectType := a.machineFromStaticExprType(n.Object)
		if objectType == nil {
			return nil
		}
		field, ok := a.lookupFieldNoError(objectType, n.Field)
		if !ok {
			return nil
		}
		return field.Type
	case *ast.IndexExpr:
		if n == nil || n.Object == nil {
			return nil
		}
		objectType := a.machineFromStaticExprType(n.Object)
		for {
			switch current := objectType.(type) {
			case *RefType:
				objectType = current.Elem
			case *AggregateStateType:
				objectType = current.Base
			default:
				goto indexedBase
			}
		}
	indexedBase:
		switch current := objectType.(type) {
		case *ArrayType:
			return current.Elem
		case *DArrayType:
			return current.Elem
		case *ViewType:
			return current.Elem
		}
	}
	return nil
}

// machineFromCallArguments maps the arguments that are already present in a call to the
// callee's explicit parameter positions without invoking normal call resolution. The capture
// pass runs before call analysis, so calling resolveFunctionCallArgs here would duplicate
// diagnostics and mutate semantic state. Defaults do not matter: they cannot carry a caller
// binding, while named and forwarded arguments can.
func machineFromCallArguments(a *Analyzer, call *ast.CallExpr, ft *FuncType, prependReceiver bool) []ast.Expr {
	if call == nil {
		return nil
	}
	if !prependReceiver && call.ResolvedArgsValid && call.ResolvedCommonArgs == nil {
		return call.ResolvedArgs
	}
	args := call.Args
	if prependReceiver {
		if field := machineFromFieldCallCallee(call.Func); field != nil && field.Object != nil {
			args = make([]ast.Expr, 0, len(call.Args)+1)
			args = append(args, field.Object)
			args = append(args, call.Args...)
		} else {
			prependReceiver = false
		}
	}
	if ft == nil || (!call.HasArgForward && call.NamedArgCount() == 0) {
		return args
	}
	explicitCount := funcTypeExplicitParamCount(ft)
	if len(ft.ExplicitParamNames) != explicitCount {
		return args
	}
	ordered := make([]ast.Expr, explicitCount)
	nameToIndex := make(map[string]int, explicitCount)
	for index, name := range ft.ExplicitParamNames {
		if name == "" {
			return args
		}
		nameToIndex[name] = index
	}
	if call.HasArgForward && a != nil {
		for index, name := range ft.ExplicitParamNames {
			if forwarded, ok := a.lookupCallForwardValueExpr(name); ok {
				ordered[index] = forwarded
			}
		}
	}
	nextPositional := 0
	for index, arg := range args {
		name := ""
		if !prependReceiver || index > 0 {
			argIndex := index
			if prependReceiver {
				argIndex--
			}
			name = call.ArgName(argIndex)
		}
		if name != "" {
			if parameterIndex, ok := nameToIndex[name]; ok {
				ordered[parameterIndex] = arg
			}
			continue
		}
		for nextPositional < explicitCount && ordered[nextPositional] != nil {
			nextPositional++
		}
		if nextPositional < explicitCount {
			ordered[nextPositional] = arg
			nextPositional++
		}
	}
	return ordered
}

func machineFromRootIdent(target ast.Expr) string {
	switch t := target.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.FieldExpr:
		return machineFromRootIdent(t.Object)
	case *ast.IndexExpr:
		return machineFromRootIdent(t.Object)
	case *ast.ParenExpr:
		return machineFromRootIdent(t.Inner)
	}
	return ""
}
