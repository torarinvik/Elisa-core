//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"strings"
)

func (s *functionState) emitTupleExpr(expr *ast.TupleExpr, expected semantic.Type) (llvmIRValueRef, semantic.Type, error) {
	tupleType, ok := semantic.StripAggregateStateType(s.exprType(expr)).(*semantic.TupleType)
	if !ok || tupleType == nil {
		return nil, nil, fmt.Errorf("tuple expression requires a tuple type, got %s", s.exprType(expr))
	}
	// Prefer a compatible EXPECTED tuple type: a match-expression arm's tuple is
	// emitted with the merged arm type, whose fields may adapt an element (a string
	// literal recorded as static u8& lowering into an sview field). Emitting each
	// element with the expected FIELD type triggers the same literal→view conversion
	// as a typed declaration, and the result struct matches the phi's type.
	if expectedTuple, ok := semantic.StripAggregateStateType(expected).(*semantic.TupleType); ok && expectedTuple != nil && len(expectedTuple.Fields) == len(tupleType.Fields) {
		tupleType = expectedTuple
	}
	llvmType, err := s.g.lowerType(tupleType)
	if err != nil {
		return nil, nil, err
	}
	value := llvmGetUndef(llvmType)
	for i, elem := range expr.Elems {
		if i >= len(tupleType.Fields) {
			break
		}
		elemValue, _, err := s.emitExpr(elem, tupleType.Fields[i].Type)
		if err != nil {
			return nil, nil, err
		}
		value = llvmBuildInsertValue(s.builder, value, elemValue, llvmIRUnsigned(i), cStringFree("tuple.ins"))
	}
	return value, tupleType, nil
}
func (s *functionState) enumConstructorInfo(expr *ast.CallExpr) (*semantic.EnumType, *semantic.EnumVariant, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, nil, false
	}
	return s.enumConstructorInfoFromField(fieldExpr)
}
func (s *functionState) enumConstructorInfoFromField(expr *ast.FieldExpr) (*semantic.EnumType, *semantic.EnumVariant, bool) {
	ownerName, variantName, ok := qualifiedFieldOwnerAndLeaf(expr)
	if !ok {
		return nil, nil, false
	}
	base, _, ok := s.lookupVisibleNamedType(ownerName)
	if !ok {
		return nil, nil, false
	}
	enumType, ok := base.(*semantic.EnumType)
	if !ok {
		return nil, nil, false
	}
	variant, ok := enumType.Variant(variantName)
	if !ok {
		return enumType, nil, true
	}
	return enumType, variant, true
}
func qualifiedFieldOwnerAndLeaf(expr *ast.FieldExpr) (string, string, bool) {
	parts, ok := qualifiedFieldParts(expr)
	if !ok || len(parts) < 2 {
		return "", "", false
	}
	return strings.Join(parts[:len(parts)-1], "."), parts[len(parts)-1], true
}
func qualifiedFieldParts(expr ast.Expr) ([]string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil || n.Name == "" {
			return nil, false
		}
		return []string{n.Name}, true
	case *ast.FieldExpr:
		parts, ok := qualifiedFieldParts(n.Object)
		if !ok || n.Field == "" {
			return nil, false
		}
		return append(parts, n.Field), true
	case *ast.ParenExpr:
		return qualifiedFieldParts(n.Inner)
	default:
		return nil, false
	}
}
