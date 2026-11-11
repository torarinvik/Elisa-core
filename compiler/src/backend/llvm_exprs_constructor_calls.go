//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitTypeConstructorCastCall(expr *ast.CallExpr) (llvmValueRef, semantic.Type, bool, error) {
	if s == nil || expr == nil {
		return nil, nil, false, nil
	}
	if s.g != nil && s.g.result != nil {
		if _, ok := s.g.resultExprType(expr.Func).(*semantic.FuncType); ok {
			return nil, nil, false, nil
		}
	}
	targetExpr, ok := backendTypeExprFromConstructorCallee(expr.Func)
	if !ok {
		return nil, nil, false, nil
	}
	if s.g == nil || s.g.result == nil || !backendTypeConstructorTargetKnown(targetExpr, s.g.namedTypesMap("type-constructor-target-check")) {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("type constructor cast expects exactly 1 positional argument")
	}
	targetType := s.exprType(expr)
	if targetType == nil {
		resolved, err := s.resolveTypeExpr(targetExpr)
		if err != nil {
			return nil, nil, true, err
		}
		targetType = resolved
	}
	if s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnCastHooksMap() {
		if sym, ok := s.g.semanticReadView().LookupCastHooks(expr); ok && sym != nil {
			fnType, ok := sym.Type.(*semantic.FuncType)
			if !ok || fnType == nil || len(fnType.Params) == 0 {
				return nil, nil, true, fmt.Errorf("invalid semantic cast hook for %T", expr)
			}
			args, err := s.emitCastHookArgs(fmt.Sprintf("%T", expr), expr.Args[0], fnType)
			if err != nil {
				return nil, nil, true, err
			}
			callee, err := s.g.ensureFunctionDeclared(sym.Name, fnType)
			if err != nil {
				return nil, nil, true, err
			}
			llvmFnType, err := s.g.lowerFunctionType(fnType)
			if err != nil {
				return nil, nil, true, err
			}
			call := s.buildCall(llvmFnType, callee, args, "casthook")
			return call, fnType.Return, true, nil
		}
	}
	operandExpected := semantic.Type(nil)
	if _, ok := expr.Args[0].(*ast.ZeroedLit); ok {
		operandExpected = targetType
	}
	value, actualType, err := s.emitExpr(expr.Args[0], operandExpected)
	if err != nil {
		return nil, nil, true, err
	}
	coerced, err := s.coerceValue(value, actualType, targetType)
	if err != nil {
		return nil, nil, true, err
	}
	return coerced, targetType, true, nil
}

func backendTypeConstructorTargetKnown(expr ast.TypeExpr, namedTypes map[string]semantic.Type) bool {
	if expr == nil || namedTypes == nil {
		return false
	}
	switch n := expr.(type) {
	case *ast.NamedType:
		_, ok := namedTypes[n.Name]
		return ok
	case *ast.GenericType:
		_, ok := namedTypes[n.Name]
		return ok
	case *ast.BuiltinTypeExpr:
		_, ok := namedTypes[n.Name]
		return ok
	default:
		return false
	}
}

func backendTypeExprFromConstructorCallee(expr ast.Expr) (ast.TypeExpr, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil || n.Name == "" {
			return nil, false
		}
		return &ast.NamedType{Position: n.Position, Name: n.Name}, true
	case *ast.FieldExpr:
		name, ok := backendQualifiedTypePathFromExpr(n)
		if !ok || name == "" {
			return nil, false
		}
		return &ast.NamedType{Position: n.Position, Name: name}, true
	case *ast.TypeExprExpr:
		if n == nil || n.Type == nil {
			return nil, false
		}
		return n.Type, true
	case *ast.SpecializeExpr:
		name, ok := backendQualifiedTypePathFromExpr(n.Operand)
		if !ok || name == "" {
			return nil, false
		}
		return &ast.GenericType{Position: n.Position, Name: name, Args: n.TypeArgs}, true
	case *ast.ParenExpr:
		return backendTypeExprFromConstructorCallee(n.Inner)
	default:
		return nil, false
	}
}

func backendQualifiedTypePathFromExpr(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil || n.Name == "" {
			return "", false
		}
		return n.Name, true
	case *ast.FieldExpr:
		prefix, ok := backendQualifiedTypePathFromExpr(n.Object)
		if !ok || prefix == "" || n.Field == "" {
			return "", false
		}
		return prefix + "." + n.Field, true
	case *ast.ParenExpr:
		return backendQualifiedTypePathFromExpr(n.Inner)
	case *ast.TypeExprExpr:
		named, ok := n.Type.(*ast.NamedType)
		if !ok || named == nil || named.Name == "" {
			return "", false
		}
		return named.Name, true
	default:
		return "", false
	}
}

func builtinDArrayPushReceiverType(t semantic.Type) (*semantic.DArrayType, *semantic.RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if darrayType, ok := t.(*semantic.DArrayType); ok && darrayType != nil {
		return darrayType, nil, true
	}
	refType, ok := t.(*semantic.RefType)
	if !ok || refType == nil {
		return nil, nil, false
	}
	darrayType, ok := refType.Elem.(*semantic.DArrayType)
	if !ok || darrayType == nil {
		return nil, nil, false
	}
	return darrayType, refType, true
}
func builtinDArrayExtendSourceType(t semantic.Type) (semantic.Type, bool) {
	if t == nil {
		return nil, false
	}
	// Contiguous element sequences are extend sources: darray, fixed array, or a view
	// (see builtinDArrayExtendSourceCompatible in the analyzer). A view source lowers
	// through the ViewType case in emitBuiltinDArrayExtendSource to the same memcpy a
	// darray source uses — reading a view's bytes into `dst` is a plain value copy.
	switch tt := t.(type) {
	case *semantic.DArrayType, *semantic.ArrayType, *semantic.ViewType, *semantic.SViewType:
		return t, true
	case *semantic.RefType:
		switch tt.Elem.(type) {
		case *semantic.DArrayType, *semantic.ArrayType, *semantic.ViewType, *semantic.SViewType:
			return tt.Elem, true
		}
	}
	return nil, false
}
func builtinStoreReceiverType(t semantic.Type) (*semantic.StructType, *semantic.RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if st, ok := semantic.StripAggregateStateType(t).(*semantic.StructType); ok && st != nil && st.Store {
		return st, nil, true
	}
	refType, ok := t.(*semantic.RefType)
	if !ok || refType == nil || refType.Elem == nil {
		return nil, nil, false
	}
	st, ok := semantic.StripAggregateStateType(refType.Elem).(*semantic.StructType)
	if !ok || st == nil || !st.Store {
		return nil, nil, false
	}
	return st, refType, true
}
func builtinDictEntryReceiverType(t semantic.Type) (*semantic.DictEntryType, *semantic.RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if entryType, ok := semantic.StripAggregateStateType(t).(*semantic.DictEntryType); ok && entryType != nil {
		return entryType, nil, true
	}
	refType, ok := t.(*semantic.RefType)
	if !ok || refType == nil || refType.Elem == nil {
		return nil, nil, false
	}
	entryType, ok := semantic.StripAggregateStateType(refType.Elem).(*semantic.DictEntryType)
	if !ok || entryType == nil {
		return nil, nil, false
	}
	return entryType, refType, true
}
func builtinDictReceiverType(t semantic.Type) (*semantic.DictType, *semantic.RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if dictType, ok := t.(*semantic.DictType); ok && dictType != nil {
		return dictType, nil, true
	}
	refType, ok := t.(*semantic.RefType)
	if !ok || refType == nil || refType.Elem == nil {
		return nil, nil, false
	}
	dictType, ok := refType.Elem.(*semantic.DictType)
	if !ok || dictType == nil {
		return nil, nil, false
	}
	return dictType, refType, true
}

func builtinSetReceiverType(t semantic.Type) (*semantic.SetType, *semantic.RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if setType, ok := t.(*semantic.SetType); ok && setType != nil {
		return setType, nil, true
	}
	refType, ok := t.(*semantic.RefType)
	if !ok || refType == nil || refType.Elem == nil {
		return nil, nil, false
	}
	setType, ok := refType.Elem.(*semantic.SetType)
	if !ok || setType == nil {
		return nil, nil, false
	}
	return setType, refType, true
}

func builtinDictEntryValueRefType(dictType *semantic.DictType, mutable bool) *semantic.RefType {
	if dictType == nil {
		return &semantic.RefType{Elem: sInvalidType(), Mutable: mutable, State: semantic.RefStateNullable, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	}
	return &semantic.RefType{Elem: dictType.Value, Mutable: mutable, State: semantic.RefStateNullable, Storage: semantic.RefStorageAny, ExplicitStorage: true}
}
func sInvalidType() semantic.Type {
	return &semantic.InvalidType{}
}
