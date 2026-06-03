package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) recordFunctionValueBinding(sym *Symbol, value ast.Expr) {
	if a.currentFunctionValues == nil || sym == nil {
		return
	}
	if sym.Kind != SymbolLocal && sym.Kind != SymbolParam {
		return
	}
	fnType, ok := a.functionValueTypeForExpr(value)
	if !ok {
		delete(a.currentFunctionValues, sym)
		return
	}
	a.currentFunctionValues[sym] = fnType
}

func (a *Analyzer) updateSpecializedValueTypeAtPath(declared Type, current Type, steps []borrowReturnAnnotationStep, actual Type) (Type, bool) {
	if declared == nil {
		return nil, false
	}
	if current == nil {
		current = declared
	}
	if len(steps) == 0 {
		if refined := assignedRefinementType(declared, actual); refined != nil {
			return refined, true
		}
		if tracked, ok := applyNamedStateFromActualType(declared, actual); ok {
			return tracked, true
		}
		if specialized, ok := a.specializeCallbackCarryingType(declared, actual); ok {
			return specialized, true
		}
		return a.cloneTrackedValueType(declared), true
	}
	step := steps[0]
	if step.Field == "" {
		return nil, false
	}
	switch declaredType := declared.(type) {
	case *RefType:
		currentType, _ := current.(*RefType)
		if currentType == nil {
			currentType = declaredType
		}
		nextElem, ok := a.updateSpecializedValueTypeAtPath(declaredType.Elem, currentType.Elem, steps, actual)
		if !ok {
			return nil, false
		}
		cloned := *currentType
		cloned.Elem = nextElem
		return &cloned, true
	case *StructType:
		currentType, _ := current.(*StructType)
		if currentType == nil {
			currentType = declaredType
		}
		declaredFieldType, ok := a.lookupResolvedFieldType(declaredType, step.Field)
		if !ok {
			return nil, false
		}
		currentFieldType, ok := a.lookupResolvedFieldType(currentType, step.Field)
		if !ok {
			currentFieldType = declaredFieldType
		}
		nextFieldType, ok := a.updateSpecializedValueTypeAtPath(declaredFieldType, currentFieldType, steps[1:], actual)
		if !ok {
			return nil, false
		}
		fields := cloneStructFields(currentType.Fields)
		field, ok := fields[step.Field]
		if !ok {
			return nil, false
		}
		field.Type = nextFieldType
		fields[step.Field] = field
		return cloneStructTypeWithFields(currentType, fields), true
	case *GenericInstanceType:
		currentType, _ := current.(*GenericInstanceType)
		if currentType == nil {
			currentType = declaredType
		}
		declaredFieldType, ok := a.lookupResolvedFieldType(declaredType, step.Field)
		if !ok {
			return nil, false
		}
		currentFieldType, ok := a.lookupResolvedFieldType(currentType, step.Field)
		if !ok {
			currentFieldType = declaredFieldType
		}
		nextFieldType, ok := a.updateSpecializedValueTypeAtPath(declaredFieldType, currentFieldType, steps[1:], actual)
		if !ok {
			return nil, false
		}
		baseStruct, ok := currentType.Base.(*StructType)
		if !ok || baseStruct == nil {
			return nil, false
		}
		fields := cloneStructFields(baseStruct.Fields)
		field, ok := fields[step.Field]
		if !ok {
			return nil, false
		}
		field.Type = nextFieldType
		fields[step.Field] = field
		clonedBase := cloneStructTypeWithFields(baseStruct, fields)
		cloned := *currentType
		cloned.Base = clonedBase
		return &cloned, true
	default:
		return nil, false
	}
}

func (a *Analyzer) recordSpecializedValueTypeTarget(target ast.Expr, valueType Type) {
	if a.currentSpecializedValueTypes == nil {
		return
	}
	root, steps, ok := a.lookupBorrowedOwnerRefTargetPath(target)
	if !ok || root == nil {
		return
	}
	if len(steps) == 0 {
		a.recordSpecializedValueTypeBinding(root, valueType)
		return
	}
	current := root.Type
	if currentType, ok := a.lookupCurrentSpecializedValueType(root); ok {
		current = currentType
	}
	updatedType, ok := a.updateSpecializedValueTypeAtPath(root.Type, current, steps, valueType)
	if !ok {
		delete(a.currentSpecializedValueTypes, root)
		return
	}
	if specializedType, ok := a.specializeCallbackCarryingType(root.Type, updatedType); ok {
		a.currentSpecializedValueTypes[root] = a.cloneTrackedValueType(specializedType)
		return
	}
	a.currentSpecializedValueTypes[root] = a.cloneTrackedValueType(updatedType)
}

func (a *Analyzer) recordFunctionValueTarget(target ast.Expr, value ast.Expr) {
	ident, ok := target.(*ast.Ident)
	if !ok || a.currentScope == nil {
		return
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return
	}
	a.recordFunctionValueBinding(sym, value)
}

func (a *Analyzer) ensureFunctionValueTypeSummaries(expr ast.Expr, fnType *FuncType) {
	if a == nil || fnType == nil {
		return
	}
	if !fnType.ReturnProvenanceKnown {
		a.inferFuncReturnProvenanceForExpr(expr, fnType)
	}
	if !fnType.ReturnBorrowedOwnerRefsKnown {
		a.inferFuncReturnBorrowedOwnerRefsForExpr(expr, fnType)
	}
}

func (a *Analyzer) functionValueTypeForExpr(expr ast.Expr) (*FuncType, bool) {
	if expr == nil {
		return nil, false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.functionValueTypeForExpr(n.Inner)
		// Casts intentionally do not recurse here. A cast to a non-function carrier
		// such as uintptr or void& is an explicit erasure boundary and must stop
		// function-value identity from propagating through later analysis.
	case *ast.MoveExpr:
		return a.functionValueTypeForExpr(n.Operand)
	case *ast.CanExpr:
		return a.functionValueTypeForExpr(n.Expr)
	case *ast.FieldExpr:
		if valueExpr, ok := a.resolveProjectedFieldValueExpr(n.Object, n.Field); ok && valueExpr != nil {
			return a.functionValueTypeForExpr(valueExpr)
		}
	case *ast.Ident:
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(n.Name); ok {
				if fnType, ok := a.lookupCurrentFunctionValueType(sym); ok {
					return fnType, true
				}
				if valueExpr, ok := a.immutableValueExprForSymbol(sym); ok && valueExpr != nil {
					return a.functionValueTypeForExpr(valueExpr)
				}
				if fnType, ok := sym.Type.(*FuncType); ok {
					a.ensureFunctionValueTypeSummaries(expr, fnType)
					cloned := a.cloneFunctionValueType(fnType)
					if cloned == nil {
						return nil, false
					}
					return cloned, true
				}
				// The name resolved to a local/param that is not a function value
				// (e.g. a parameter `builder: StringBuilder&` that shadows a global
				// `def builder[T](...)`). It must shadow the global — do NOT fall
				// through to the global lookup below, which would mis-bind the local
				// to the same-named global function's type.
				return nil, false
			}
		}
		if sym, _, ok := a.lookupVisibleGlobal(n.Name); ok {
			if valueExpr, ok := a.immutableValueExprForSymbol(sym); ok && valueExpr != nil {
				return a.functionValueTypeForExpr(valueExpr)
			}
			if fnType, ok := sym.Type.(*FuncType); ok {
				a.ensureFunctionValueTypeSummaries(expr, fnType)
				cloned := a.cloneFunctionValueType(fnType)
				if cloned == nil {
					return nil, false
				}
				return cloned, true
			}
		}
	}
	valueType := a.exprTypes[expr]
	if valueType == nil {
		valueType = a.analyzeExpr(expr)
	}
	fnType, ok := valueType.(*FuncType)
	if !ok {
		return nil, false
	}
	a.ensureFunctionValueTypeSummaries(expr, fnType)
	cloned := a.cloneFunctionValueType(fnType)
	if cloned == nil {
		return nil, false
	}
	return cloned, true
}

func (a *Analyzer) lookupCurrentFunctionValueType(sym *Symbol) (*FuncType, bool) {
	if a.currentFunctionValues == nil || sym == nil {
		return nil, false
	}
	fnType, ok := a.currentFunctionValues[sym]
	if !ok || fnType == nil {
		return nil, false
	}
	if valueExpr, ok := a.immutableValueExprForSymbol(sym); ok && valueExpr != nil {
		a.ensureFunctionValueTypeSummaries(valueExpr, fnType)
	}
	return a.cloneFunctionValueType(fnType), true
}

func (a *Analyzer) currentFunctionValueTypeRef(sym *Symbol) (*FuncType, bool) {
	if a == nil || a.currentFunctionValues == nil || sym == nil {
		return nil, false
	}
	fnType, ok := a.currentFunctionValues[sym]
	if !ok || fnType == nil {
		return nil, false
	}
	return fnType, true
}

func (a *Analyzer) lookupCurrentSpecializedValueType(sym *Symbol) (Type, bool) {
	if a.currentSpecializedValueTypes == nil || sym == nil {
		return nil, false
	}
	valueType, ok := a.currentSpecializedValueTypes[sym]
	if !ok || valueType == nil {
		return nil, false
	}
	return a.cloneTrackedValueType(valueType), true
}
