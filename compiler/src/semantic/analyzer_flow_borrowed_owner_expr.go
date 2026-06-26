package semantic

import (
	"elisacore/src/ast"
)

func projectBorrowedOwnerRefFieldState(state borrowedOwnerRefState, field string) (borrowedOwnerRefState, bool) {
	if len(state.Fields) == 0 {
		return borrowedOwnerRefState{}, false
	}
	child, ok := state.Fields[field]
	if !ok || !hasBorrowedOwnerRefState(child) {
		return borrowedOwnerRefState{}, false
	}
	return cloneBorrowedOwnerRefState(child), true
}

func projectBorrowedOwnerRefIndexKeyState(state borrowedOwnerRefState, key string) (borrowedOwnerRefState, bool) {
	if len(state.Fields) == 0 {
		return borrowedOwnerRefState{}, false
	}
	child, ok := state.Fields[key]
	if !ok || !hasBorrowedOwnerRefState(child) {
		return borrowedOwnerRefState{}, false
	}
	return cloneBorrowedOwnerRefState(child), true
}

func projectBorrowedOwnerRefIndexState(state borrowedOwnerRefState, index ast.Expr, evalConst func(ast.Expr) (ConstValue, bool)) (borrowedOwnerRefState, bool) {
	if len(state.Fields) == 0 {
		return borrowedOwnerRefState{}, false
	}
	if evalConst != nil {
		if value, ok := evalConst(index); ok && value.Kind == ConstInt {
			if child, ok := projectBorrowedOwnerRefIndexKeyState(state, regionIndexFieldKey(value.Int)); ok {
				return child, true
			}
		}
	}
	return projectBorrowedOwnerRefIndexKeyState(state, regionAnyIndexFieldKey())
}

func (a *Analyzer) borrowedOwnerRefStateForExpr(expr ast.Expr) (borrowedOwnerRefState, bool) {
	if expr == nil {
		return borrowedOwnerRefState{}, false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.borrowedOwnerRefStateForExpr(n.Inner)
	case *ast.CastExpr:
		state, ok := a.borrowedOwnerRefStateForExpr(n.Operand)
		if !ok {
			return borrowedOwnerRefState{}, false
		}
		// A cast that yields a non-reference value (e.g. `.uintptr()`) severs a raw
		// interior-pointer alias: the result is a copied value, not a pointer into the
		// pooled slot, so it is safe to use after release. Genuine owner-borrow content
		// is left intact.
		castType := a.exprTypes[n]
		if castType == nil {
			castType = a.analyzeExpr(n)
		}
		if _, isRef := castType.(*RefType); !isRef {
			state = stripRawInteriorAffineAlias(state)
			if !hasBorrowedOwnerRefState(state) {
				return borrowedOwnerRefState{}, false
			}
		}
		return state, true
	case *ast.MoveExpr:
		return a.borrowedOwnerRefStateForExpr(n.Operand)
	case *ast.AddrOfExpr:
		operandType := a.exprTypes[n.Operand]
		if operandType == nil {
			operandType = a.analyzeExpr(n.Operand)
		}
		if !isBorrowableAffineOwnerType(operandType) {
			return borrowedOwnerRefState{}, false
		}
		key, ok := a.lookupAffineValueKey(n.Operand)
		if !ok {
			return borrowedOwnerRefState{}, false
		}
		return borrowedOwnerRefState{HasDirect: true, Direct: key}, true
	case *ast.Ident:
		if a.currentScope == nil {
			return borrowedOwnerRefState{}, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok {
			return borrowedOwnerRefState{}, false
		}
		if state, ok := a.currentBorrowedOwnerRefs[sym]; ok && hasBorrowedOwnerRefState(state) {
			return cloneBorrowedOwnerRefState(state), true
		}
		if root := symbolAliasRoot(sym); root != nil && root != sym {
			if state, ok := a.currentBorrowedOwnerRefs[root]; ok && hasBorrowedOwnerRefState(state) {
				return cloneBorrowedOwnerRefState(state), true
			}
			if _, ok := borrowableOwnerRefElemType(root.Type); ok {
				return borrowedOwnerRefState{HasDirect: true, Direct: affineValueKey{Root: root}}, true
			}
		}
		if _, ok := borrowableOwnerRefElemType(sym.Type); ok {
			return borrowedOwnerRefState{HasDirect: true, Direct: affineValueKey{Root: sym}}, true
		}
		// Only while capturing a function's return-borrow summary, treat a raw reference
		// parameter as a pass-through borrow source, so a function that returns one of its
		// reference parameters records a "returns param i" summary. At call sites that
		// summary is instantiated against the actual argument, so the interior-alias fact
		// only materializes when a caller passes a raw interior borrow of a Pooled handle;
		// ordinary reference arguments instantiate to nothing. Confining this to the
		// return-capture site keeps normal-path analysis, diagnostics, and use-site
		// behavior completely unchanged.
		if a.inReturnBorrowCapture && sym.Kind == SymbolParam {
			if _, ok := sym.Type.(*RefType); ok {
				return borrowedOwnerRefState{HasDirect: true, Direct: affineValueKey{Root: sym}}, true
			}
		}
		return borrowedOwnerRefState{}, false
	case *ast.StructLitExpr:
		actual := a.exprTypes[n]
		fields, ok := a.resolvedStructFields(actual)
		if !ok {
			return borrowedOwnerRefState{}, false
		}
		args := n.LoweredArgs()
		state := borrowedOwnerRefState{}
		for i, field := range fields {
			if i >= len(args) {
				break
			}
			if args[i] == nil {
				continue
			}
			fieldState, ok := a.borrowedOwnerRefStateForExpr(args[i])
			if !ok || !hasBorrowedOwnerRefState(fieldState) {
				continue
			}
			if state.Fields == nil {
				state.Fields = map[string]borrowedOwnerRefState{}
			}
			state.Fields[field.Name] = fieldState
		}
		if !hasBorrowedOwnerRefState(state) {
			return borrowedOwnerRefState{}, false
		}
		return state, true
	case *ast.RecordUpdateExpr:
		actual := a.exprTypes[n]
		fields, ok := a.resolvedStructFields(actual)
		if !ok {
			return borrowedOwnerRefState{}, false
		}
		baseState, hasBaseState := a.borrowedOwnerRefStateForExpr(n.Base)
		args := n.LoweredArgs()
		state := borrowedOwnerRefState{}
		for i, field := range fields {
			var (
				fieldState borrowedOwnerRefState
				hasState   bool
			)
			if i < len(args) && args[i] != nil {
				fieldState, hasState = a.borrowedOwnerRefStateForExpr(args[i])
			} else if hasBaseState {
				fieldState, hasState = projectBorrowedOwnerRefFieldState(baseState, field.Name)
			}
			if !hasState || !hasBorrowedOwnerRefState(fieldState) {
				continue
			}
			if state.Fields == nil {
				state.Fields = map[string]borrowedOwnerRefState{}
			}
			state.Fields[field.Name] = fieldState
		}
		if !hasBorrowedOwnerRefState(state) {
			return borrowedOwnerRefState{}, false
		}
		return state, true
	case *ast.ListLitExpr:
		state := borrowedOwnerRefState{}
		for i, elem := range n.Elems {
			elemState, ok := a.borrowedOwnerRefStateForExpr(elem)
			if !ok || !hasBorrowedOwnerRefState(elemState) {
				continue
			}
			if state.Fields == nil {
				state.Fields = map[string]borrowedOwnerRefState{}
			}
			key := regionIndexFieldKey(int64(i))
			state.Fields[key] = elemState
			if anyState, ok := state.Fields[regionAnyIndexFieldKey()]; ok {
				if merged, ok := mergeBorrowedOwnerRefState(anyState, elemState); ok {
					state.Fields[regionAnyIndexFieldKey()] = merged
				} else {
					delete(state.Fields, regionAnyIndexFieldKey())
				}
			} else {
				state.Fields[regionAnyIndexFieldKey()] = cloneBorrowedOwnerRefState(elemState)
			}
		}
		// Dict-literal keys (`{k: v}`) are borrowed-owner carriers too; they are not
		// positionally index-addressable like values, so fold their state into the
		// wildcard element bucket (deep audit #14).
		for _, keyExpr := range n.Keys {
			keyState, ok := a.borrowedOwnerRefStateForExpr(keyExpr)
			if !ok || !hasBorrowedOwnerRefState(keyState) {
				continue
			}
			if state.Fields == nil {
				state.Fields = map[string]borrowedOwnerRefState{}
			}
			if anyState, ok := state.Fields[regionAnyIndexFieldKey()]; ok {
				if merged, ok := mergeBorrowedOwnerRefState(anyState, keyState); ok {
					state.Fields[regionAnyIndexFieldKey()] = merged
				} else {
					delete(state.Fields, regionAnyIndexFieldKey())
				}
			} else {
				state.Fields[regionAnyIndexFieldKey()] = cloneBorrowedOwnerRefState(keyState)
			}
		}
		if !hasBorrowedOwnerRefState(state) {
			return borrowedOwnerRefState{}, false
		}
		return state, true
	case *ast.FieldExpr:
		if key, ok := a.poolInteriorBorrowKey(n, n.Object); ok {
			return borrowedOwnerRefState{HasDirect: true, Direct: key, RawInteriorAffineAlias: true}, true
		}
		state, ok := a.borrowedOwnerRefStateForExpr(n.Object)
		if !ok {
			return borrowedOwnerRefState{}, false
		}
		return projectBorrowedOwnerRefFieldState(state, n.Field)
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return a.borrowedOwnerRefStateForRecoveredExpr(&ast.IndexExpr{Position: n.Position, Object: n.Object, Index: n.Index}, n.Fallback)
		}
		if key, ok := a.poolInteriorBorrowKey(n, n.Object); ok {
			return borrowedOwnerRefState{HasDirect: true, Direct: key, RawInteriorAffineAlias: true}, true
		}
		state, ok := a.borrowedOwnerRefStateForExpr(n.Object)
		if !ok {
			return borrowedOwnerRefState{}, false
		}
		return projectBorrowedOwnerRefIndexState(state, n.Index, a.evalConstExpr)
	case *ast.CallExpr:
		if state, ok := a.borrowedOwnerRefStateForProofCarryingViewCall(n); ok {
			return state, true
		}
		if _, ok := a.freezeStoreArg(n); ok {
			return borrowedOwnerRefState{}, false
		}
		if len(n.Args) == 1 && a.isTypeConstructorCall(n) {
			return a.borrowedOwnerRefStateForExpr(n.Args[0])
		}
		fnType, _ := a.exprTypes[n.Func].(*FuncType)
		if fnType == nil {
			if analyzed := a.analyzeExpr(n.Func); analyzed != nil {
				fnType, _ = analyzed.(*FuncType)
			}
		}
		if fnType != nil {
			if !fnType.ReturnBorrowedOwnerRefsKnown {
				a.inferFuncReturnBorrowedOwnerRefsForExpr(n.Func, fnType)
			}
			if fnType.ReturnBorrowedOwnerRefsKnown {
				return a.instantiateBorrowedOwnerRefSummary(fnType.ReturnBorrowedOwnerRefs, n.Args)
			}
		}
		return borrowedOwnerRefState{}, false
	case *ast.TryExpr:
		return a.borrowedOwnerRefStateForRecoveredExpr(n.Value, n.Fallback)
	case *ast.CatchExpr:
		return borrowedOwnerRefState{}, false
	case *ast.UnwrapElseExpr:
		return a.borrowedOwnerRefStateForRecoveredExpr(n.Value, n.Fallback)
	case *ast.GetExpr:
		return a.borrowedOwnerRefStateForRecoveredExpr(n.Value, n.Fallback)
	case *ast.OptionalBindExpr:
		return a.borrowedOwnerRefStateForExpr(n.Value)
	case *ast.TernaryExpr:
		left, leftOK := a.borrowedOwnerRefStateForExpr(n.Value)
		right, rightOK := a.borrowedOwnerRefStateForExpr(n.Alt)
		if !leftOK || !rightOK {
			return borrowedOwnerRefState{}, false
		}
		return mergeBorrowedOwnerRefState(left, right)
	default:
		return borrowedOwnerRefState{}, false
	}
}

func (a *Analyzer) borrowedOwnerRefStateForProofCarryingViewCall(call *ast.CallExpr) (borrowedOwnerRefState, bool) {
	if a == nil || call == nil || len(call.Args) == 0 {
		return borrowedOwnerRefState{}, false
	}
	sourceState, ok := a.borrowedOwnerRefStateForExpr(call.Args[0])
	if !ok || !hasBorrowedOwnerRefState(sourceState) {
		return borrowedOwnerRefState{}, false
	}
	switch callIdentName(call) {
	case "readonly":
		return cloneBorrowedOwnerRefState(sourceState), true
	case "split_at":
		summarized, summaryOK := summarizeBorrowedOwnerRefIndexStates(sourceState)
		if !summaryOK {
			summarized = cloneBorrowedOwnerRefState(sourceState)
		}
		state := cloneBorrowedOwnerRefState(summarized)
		state.Fields = map[string]borrowedOwnerRefState{
			"left":  cloneBorrowedOwnerRefState(summarized),
			"right": cloneBorrowedOwnerRefState(summarized),
		}
		return state, true
	case "chunks_exact":
		summarized, summaryOK := summarizeBorrowedOwnerRefIndexStates(sourceState)
		if !summaryOK {
			summarized = cloneBorrowedOwnerRefState(sourceState)
		}
		state := cloneBorrowedOwnerRefState(summarized)
		state.Fields = map[string]borrowedOwnerRefState{
			"source": cloneBorrowedOwnerRefState(summarized),
		}
		return state, true
	case "reduce_sum":
		return borrowedOwnerRefState{}, true
	default:
		return borrowedOwnerRefState{}, false
	}
}

func (a *Analyzer) borrowedOwnerRefStateForRecoveredExpr(value ast.Expr, fallback ast.Expr) (borrowedOwnerRefState, bool) {
	valueState, valueOK := a.borrowedOwnerRefStateForExpr(value)
	if fallback == nil || a.exprDefinitelyNever(fallback) {
		if !valueOK {
			return borrowedOwnerRefState{}, false
		}
		return cloneBorrowedOwnerRefState(valueState), true
	}
	fallbackState, fallbackOK := a.borrowedOwnerRefStateForExpr(fallback)
	if !valueOK || !fallbackOK {
		return borrowedOwnerRefState{}, false
	}
	return mergeBorrowedOwnerRefState(valueState, fallbackState)
}

func (a *Analyzer) lookupBorrowedOwnerRefTargetPath(expr ast.Expr) (*Symbol, []borrowReturnAnnotationStep, bool) {
	if expr == nil {
		return nil, nil, false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.lookupBorrowedOwnerRefTargetPath(n.Inner)
	case *ast.Ident:
		if a.currentScope == nil {
			return nil, nil, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		return sym, nil, ok
	case *ast.FieldExpr:
		root, steps, ok := a.lookupBorrowedOwnerRefTargetPath(n.Object)
		if !ok {
			return nil, nil, false
		}
		return root, append(steps, borrowReturnAnnotationStep{Field: n.Field}), true
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return nil, nil, false
		}
		root, steps, ok := a.lookupBorrowedOwnerRefTargetPath(n.Object)
		if !ok {
			return nil, nil, false
		}
		step := borrowReturnAnnotationStep{Wildcard: true}
		if value, ok := a.evalConstExpr(n.Index); ok && value.Kind == ConstInt {
			valueCopy := value.Int
			step = borrowReturnAnnotationStep{Index: &valueCopy}
		}
		return root, append(steps, step), true
	default:
		return nil, nil, false
	}
}

func (a *Analyzer) lookupBorrowedOwnerRefKey(expr ast.Expr) (affineValueKey, bool) {
	state, ok := a.borrowedOwnerRefStateForExpr(expr)
	if !ok || !state.HasDirect {
		return affineValueKey{}, false
	}
	return state.Direct, true
}
