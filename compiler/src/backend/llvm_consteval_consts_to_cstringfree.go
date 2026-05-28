//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const smallExactArenaFillUnrollLimit = 4
const smallExactArenaCopyUnrollLimit = 4
const smallExactArenaEqUnrollByteLimit = 16

func (s *functionState) sizeOfType(t semantic.Type) (uint64, error) {
	return s.g.abiSizeOfType(t)
}
func shapeFromTypeExpr(expr ast.TypeExpr) semantic.Shape {
	if named, ok := expr.(*ast.NamedType); ok {
		return &semantic.NamedShape{Name: named.Name}
	}
	return &semantic.NamedShape{Name: "?"}
}
func shapeFromValueExpr(expr ast.Expr) semantic.Shape {
	switch n := expr.(type) {
	case *ast.Ident:
		return &semantic.NamedShape{Name: n.Name}
	case *ast.IntLit:
		return &semantic.NamedShape{Name: n.Value}
	default:
		return &semantic.NamedShape{Name: "?"}
	}
}
func normalizeIf(stmt *ast.IfStmt) *ast.IfStmt {
	if stmt == nil || len(stmt.Elifs) == 0 {
		return stmt
	}
	elseBody := stmt.Else
	for i := len(stmt.Elifs) - 1; i >= 0; i-- {
		elif := stmt.Elifs[i]
		elseBody = []ast.Stmt{&ast.IfStmt{Position: elif.Position, Hint: elif.Hint, Cond: elif.Cond, Then: elif.Body, Else: elseBody}}
	}
	return &ast.IfStmt{Position: stmt.Position, Hint: stmt.Hint, Cond: stmt.Cond, Then: stmt.Then, Else: elseBody}
}
func (s *functionState) evalConstIntExpr(expr ast.Expr) (int64, error) {
	value, ok := s.evalConstExpr(expr)
	if !ok || value.Kind != semantic.ConstInt {
		return 0, fmt.Errorf("expression is not a compile-time integer constant")
	}
	return value.Int, nil
}
func (s *functionState) evalConstBoolExpr(expr ast.Expr) (bool, bool) {
	value, ok := s.evalConstExpr(expr)
	if !ok || value.Kind != semantic.ConstBool {
		return false, false
	}
	return value.Bool, true
}
func (g *llvmGenerator) evalConstBoolExpr(expr ast.Expr) (bool, bool) {
	value, ok := g.evalConstExpr(expr)
	if !ok || value.Kind != semantic.ConstBool {
		return false, false
	}
	return value.Bool, true
}
func (s *functionState) evalConstExpr(expr ast.Expr) (semantic.ConstValue, bool) {
	if ident, ok := expr.(*ast.Ident); ok && s.typeMap != nil {
		if bound, ok := s.typeMap[ident.Name]; ok {
			if value, valueOK := bound.(*semantic.ConstValueType); valueOK {
				return value.Value, true
			}
		}
	}
	switch n := expr.(type) {
	case *ast.SizeofExpr:
		t, err := s.resolveTypeExpr(n.Type)
		if err != nil {
			return semantic.ConstValue{}, false
		}
		size, err := s.sizeOfType(t)
		if err != nil || size > math.MaxInt64 {
			return semantic.ConstValue{}, false
		}
		return semantic.ConstValue{Kind: semantic.ConstInt, Int: int64(size)}, true
	case *ast.AlignofExpr:
		t, err := s.resolveTypeExpr(n.Type)
		if err != nil {
			return semantic.ConstValue{}, false
		}
		alignment, err := s.g.abiAlignmentOfType(t)
		if err != nil || alignment > math.MaxInt64 {
			return semantic.ConstValue{}, false
		}
		return semantic.ConstValue{Kind: semantic.ConstInt, Int: int64(alignment)}, true
	case *ast.OffsetofExpr:
		t, err := s.resolveTypeExpr(n.Type)
		if err != nil {
			return semantic.ConstValue{}, false
		}
		_, fieldIndex, containerType, _, err := s.g.fieldInfo(t, n.Field)
		if err != nil {
			return semantic.ConstValue{}, false
		}
		containerLLVMType, err := s.g.lowerType(containerType)
		if err != nil {
			return semantic.ConstValue{}, false
		}
		offset, err := s.g.abiOffsetOfLLVMElement(containerLLVMType, fieldIndex)
		if err != nil || offset > math.MaxInt64 {
			return semantic.ConstValue{}, false
		}
		return semantic.ConstValue{Kind: semantic.ConstInt, Int: int64(offset)}, true
	case *ast.ParenExpr:
		return s.evalConstExpr(n.Inner)
	case *ast.MoveExpr:
		return s.evalConstExpr(n.Operand)
	case *ast.UnaryExpr:
		operand, ok := s.evalConstExpr(n.Operand)
		if !ok {
			return semantic.ConstValue{}, false
		}
		switch n.Op {
		case lexer.TOKEN_NOT:
			if operand.Kind != semantic.ConstBool {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: !operand.Bool}, true
		case lexer.TOKEN_MINUS:
			switch operand.Kind {
			case semantic.ConstInt:
				return semantic.ConstValue{Kind: semantic.ConstInt, Int: -operand.Int}, true
			case semantic.ConstFloat:
				return semantic.ConstValue{Kind: semantic.ConstFloat, Float: -operand.Float}, true
			default:
				return semantic.ConstValue{}, false
			}
		case lexer.TOKEN_TILDE:
			if operand.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: ^operand.Int}, true
		default:
			return semantic.ConstValue{}, false
		}
	case *ast.BinaryExpr:
		left, ok := s.evalConstExpr(n.Left)
		if !ok {
			return semantic.ConstValue{}, false
		}
		right, ok := s.evalConstExpr(n.Right)
		if !ok {
			return semantic.ConstValue{}, false
		}
		switch n.Op {
		case lexer.TOKEN_AND:
			if left.Kind != semantic.ConstBool || right.Kind != semantic.ConstBool {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: left.Bool && right.Bool}, true
		case lexer.TOKEN_OR:
			if left.Kind != semantic.ConstBool || right.Kind != semantic.ConstBool {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: left.Bool || right.Bool}, true
		case lexer.TOKEN_EQEQ:
			return evalConstEquality(left, right, true)
		case lexer.TOKEN_BANGEQ:
			return evalConstEquality(left, right, false)
		case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH,
			lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
			return evalBackendConstNumericBinary(n.Op, left, right)
		default:
			return semantic.ConstValue{}, false
		}
	case *ast.TernaryExpr:
		cond, ok := s.evalConstExpr(n.Cond)
		if !ok || cond.Kind != semantic.ConstBool {
			return semantic.ConstValue{}, false
		}
		if cond.Bool {
			return s.evalConstExpr(n.Value)
		}
		return s.evalConstExpr(n.Alt)
	case *ast.UnwrapElseExpr:
		return s.evalConstUnwrapElseExpr(n)
	case *ast.GetExpr:
		return s.evalConstGetExpr(n)
	case *ast.QueryExpr:
		return s.evalConstQueryExpr(n)
	}
	if castExpr, ok := expr.(*ast.CastExpr); ok {
		operand, ok := s.evalConstExpr(castExpr.Operand)
		if !ok {
			return semantic.ConstValue{}, false
		}
		targetType := s.exprType(castExpr)
		if targetType == nil {
			return semantic.ConstValue{}, false
		}
		return semantic.CastConstValue(operand, targetType)
	}
	return evalConstExprWithLookup(expr, s.g.constValue, s.g.evalStaticFunctionCall)
}
func (g *llvmGenerator) evalConstExpr(expr ast.Expr) (semantic.ConstValue, bool) {
	if unwrapExpr, ok := expr.(*ast.UnwrapElseExpr); ok {
		return g.evalConstUnwrapElseExpr(unwrapExpr)
	}
	if getExpr, ok := expr.(*ast.GetExpr); ok {
		return g.evalConstGetExpr(getExpr)
	}
	if queryExpr, ok := expr.(*ast.QueryExpr); ok {
		return g.evalConstQueryExpr(queryExpr)
	}
	if castExpr, ok := expr.(*ast.CastExpr); ok {
		operand, ok := g.evalConstExpr(castExpr.Operand)
		if !ok {
			return semantic.ConstValue{}, false
		}
		targetType := g.exprType(castExpr)
		if targetType == nil {
			return semantic.ConstValue{}, false
		}
		return semantic.CastConstValue(operand, targetType)
	}
	return evalConstExprWithLookup(expr, g.constValue, g.evalStaticFunctionCall)
}

func (s *functionState) evalConstUnwrapElseExpr(expr *ast.UnwrapElseExpr) (semantic.ConstValue, bool) {
	if expr == nil {
		return semantic.ConstValue{}, false
	}
	value, ok := s.evalConstExpr(expr.Value)
	if !ok || value.Kind != semantic.ConstOptional {
		return semantic.ConstValue{}, false
	}
	if value.Some {
		if value.Value == nil {
			return semantic.ConstValue{}, false
		}
		return cloneBackendConstValue(*value.Value), true
	}
	recovery := backendConstRecoveryClauseForExpr(expr.Recovery, expr.Fallback, expr.Position)
	if recovery == nil || recovery.Kind != ast.RecoveryValue || recovery.Value == nil {
		return semantic.ConstValue{}, false
	}
	return s.evalConstExpr(recovery.Value)
}

func (g *llvmGenerator) evalConstUnwrapElseExpr(expr *ast.UnwrapElseExpr) (semantic.ConstValue, bool) {
	if expr == nil {
		return semantic.ConstValue{}, false
	}
	value, ok := g.evalConstExpr(expr.Value)
	if !ok || value.Kind != semantic.ConstOptional {
		return semantic.ConstValue{}, false
	}
	if value.Some {
		if value.Value == nil {
			return semantic.ConstValue{}, false
		}
		return cloneBackendConstValue(*value.Value), true
	}
	recovery := backendConstRecoveryClauseForExpr(expr.Recovery, expr.Fallback, expr.Position)
	if recovery == nil || recovery.Kind != ast.RecoveryValue || recovery.Value == nil {
		return semantic.ConstValue{}, false
	}
	return g.evalConstExpr(recovery.Value)
}

func (s *functionState) evalConstGetExpr(expr *ast.GetExpr) (semantic.ConstValue, bool) {
	if expr == nil {
		return semantic.ConstValue{}, false
	}
	if idx, ok := expr.Value.(*ast.IndexExpr); ok && idx.Fallback != nil {
		return s.evalConstExpr(idx)
	}
	value, ok := s.evalConstExpr(expr.Value)
	if !ok || value.Kind != semantic.ConstOptional {
		return semantic.ConstValue{}, false
	}
	if value.Some {
		if value.Value == nil {
			return semantic.ConstValue{}, false
		}
		return cloneBackendConstValue(*value.Value), true
	}
	recovery := backendConstRecoveryClauseForExpr(expr.Recovery, expr.Fallback, expr.Position)
	if recovery == nil || recovery.Kind != ast.RecoveryValue || recovery.Value == nil {
		return semantic.ConstValue{}, false
	}
	return s.evalConstExpr(recovery.Value)
}

func (g *llvmGenerator) evalConstGetExpr(expr *ast.GetExpr) (semantic.ConstValue, bool) {
	if expr == nil {
		return semantic.ConstValue{}, false
	}
	if idx, ok := expr.Value.(*ast.IndexExpr); ok && idx.Fallback != nil {
		return g.evalConstExpr(idx)
	}
	value, ok := g.evalConstExpr(expr.Value)
	if !ok || value.Kind != semantic.ConstOptional {
		return semantic.ConstValue{}, false
	}
	if value.Some {
		if value.Value == nil {
			return semantic.ConstValue{}, false
		}
		return cloneBackendConstValue(*value.Value), true
	}
	recovery := backendConstRecoveryClauseForExpr(expr.Recovery, expr.Fallback, expr.Position)
	if recovery == nil || recovery.Kind != ast.RecoveryValue || recovery.Value == nil {
		return semantic.ConstValue{}, false
	}
	return g.evalConstExpr(recovery.Value)
}

func backendConstRecoveryClauseForExpr(recovery *ast.RecoveryClause, fallback ast.Expr, pos lexer.Pos) *ast.RecoveryClause {
	if recovery != nil {
		return recovery
	}
	if fallback == nil {
		return nil
	}
	return &ast.RecoveryClause{Position: pos, Kind: ast.RecoveryValue, Value: fallback}
}

func evalConstExprWithLookup(expr ast.Expr, lookup func(string) (semantic.ConstValue, bool), call func(*ast.CallExpr) (semantic.ConstValue, bool)) (semantic.ConstValue, bool) {
	switch n := expr.(type) {
	case *ast.IntLit:
		// Use the semantic parser so unsigned-typed literals with the top bit set
		// (e.g. 0xFFFFFFFFFFFFFFFFu64) parse via ParseUint instead of overflowing
		// a signed parse.
		value, ok := semantic.ParseIntLiteral(n)
		if !ok {
			return semantic.ConstValue{}, false
		}
		return semantic.ConstValue{Kind: semantic.ConstInt, Int: value}, true
	case *ast.FloatLit:
		value, err := strconv.ParseFloat(n.Value, 64)
		if err != nil {
			return semantic.ConstValue{}, false
		}
		return semantic.ConstValue{Kind: semantic.ConstFloat, Float: value}, true
	case *ast.BoolLit:
		return semantic.ConstValue{Kind: semantic.ConstBool, Bool: n.Value}, true
	case *ast.StringLit:
		return semantic.ConstValue{Kind: semantic.ConstString, String: n.Value}, true
	case *ast.CharLit:
		value, ok := semantic.ParseCharLiteral(n)
		if !ok {
			return semantic.ConstValue{}, false
		}
		return semantic.ConstValue{Kind: semantic.ConstInt, Int: value}, true
	case *ast.Ident:
		if lookup != nil {
			if value, ok := lookupConstEvalValue(lookup, n.Name); ok {
				return value, true
			}
		}
		if lookup != nil {
			if value, ok := lookup(n.Name); ok {
				return value, true
			}
		}
		return semantic.ConstValue{}, false
	case *ast.FieldExpr:
		if value, ok := evalBackendConstAggregateFieldExpr(n, lookup, call); ok {
			return value, true
		}
		ident, ok := n.Object.(*ast.Ident)
		if !ok || lookup == nil {
			return semantic.ConstValue{}, false
		}
		return lookup(ident.Name + "." + n.Field)
	case *ast.ParenExpr:
		return evalConstExprWithLookup(n.Inner, lookup, call)
	case *ast.CastExpr:
		return evalConstExprWithLookup(n.Operand, lookup, call)
	case *ast.MoveExpr:
		return evalConstExprWithLookup(n.Operand, lookup, call)
	case *ast.UnaryExpr:
		operand, ok := evalConstExprWithLookup(n.Operand, lookup, call)
		if !ok {
			return semantic.ConstValue{}, false
		}
		switch n.Op {
		case lexer.TOKEN_NOT:
			if operand.Kind != semantic.ConstBool {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: !operand.Bool}, true
		case lexer.TOKEN_MINUS:
			switch operand.Kind {
			case semantic.ConstInt:
				return semantic.ConstValue{Kind: semantic.ConstInt, Int: -operand.Int}, true
			case semantic.ConstFloat:
				return semantic.ConstValue{Kind: semantic.ConstFloat, Float: -operand.Float}, true
			default:
				return semantic.ConstValue{}, false
			}
		case lexer.TOKEN_TILDE:
			if operand.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: ^operand.Int}, true
		default:
			return semantic.ConstValue{}, false
		}
	case *ast.BinaryExpr:
		left, ok := evalConstExprWithLookup(n.Left, lookup, call)
		if !ok {
			return semantic.ConstValue{}, false
		}
		right, ok := evalConstExprWithLookup(n.Right, lookup, call)
		if !ok {
			return semantic.ConstValue{}, false
		}
		switch n.Op {
		case lexer.TOKEN_AND:
			if left.Kind != semantic.ConstBool || right.Kind != semantic.ConstBool {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: left.Bool && right.Bool}, true
		case lexer.TOKEN_OR:
			if left.Kind != semantic.ConstBool || right.Kind != semantic.ConstBool {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: left.Bool || right.Bool}, true
		case lexer.TOKEN_EQEQ:
			return evalConstEquality(left, right, true)
		case lexer.TOKEN_BANGEQ:
			return evalConstEquality(left, right, false)
		case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH,
			lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
			if result, ok := evalBackendConstNumericBinary(n.Op, left, right); ok {
				return result, true
			}
			return semantic.ConstValue{}, false
		case lexer.TOKEN_PERCENT:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt || right.Int == 0 {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int % right.Int}, true
		case lexer.TOKEN_LSHIFT:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int << right.Int}, true
		case lexer.TOKEN_RSHIFT:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int >> right.Int}, true
		case lexer.TOKEN_AMPERSAND:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int & right.Int}, true
		case lexer.TOKEN_PIPE:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int | right.Int}, true
		case lexer.TOKEN_CARET:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int ^ right.Int}, true
		default:
			return semantic.ConstValue{}, false
		}
	case *ast.TernaryExpr:
		condValue, ok := evalConstExprWithLookup(n.Cond, lookup, call)
		if !ok || condValue.Kind != semantic.ConstBool {
			return semantic.ConstValue{}, false
		}
		if condValue.Bool {
			return evalConstExprWithLookup(n.Value, lookup, call)
		}
		return evalConstExprWithLookup(n.Alt, lookup, call)
	case *ast.UnwrapElseExpr:
		value, ok := evalConstExprWithLookup(n.Value, lookup, call)
		if !ok || value.Kind != semantic.ConstOptional {
			return semantic.ConstValue{}, false
		}
		if value.Some {
			if value.Value == nil {
				return semantic.ConstValue{}, false
			}
			return cloneBackendConstValue(*value.Value), true
		}
		recovery := backendConstRecoveryClauseForExpr(n.Recovery, n.Fallback, n.Position)
		if recovery == nil || recovery.Kind != ast.RecoveryValue || recovery.Value == nil {
			return semantic.ConstValue{}, false
		}
		return evalConstExprWithLookup(recovery.Value, lookup, call)
	case *ast.GetExpr:
		if idx, ok := n.Value.(*ast.IndexExpr); ok && idx.Fallback != nil {
			return evalConstExprWithLookup(idx, lookup, call)
		}
		value, ok := evalConstExprWithLookup(n.Value, lookup, call)
		if !ok || value.Kind != semantic.ConstOptional {
			return semantic.ConstValue{}, false
		}
		if value.Some {
			if value.Value == nil {
				return semantic.ConstValue{}, false
			}
			return cloneBackendConstValue(*value.Value), true
		}
		recovery := backendConstRecoveryClauseForExpr(n.Recovery, n.Fallback, n.Position)
		if recovery == nil || recovery.Kind != ast.RecoveryValue || recovery.Value == nil {
			return semantic.ConstValue{}, false
		}
		return evalConstExprWithLookup(recovery.Value, lookup, call)
	case *ast.TupleExpr:
		elems := make([]semantic.ConstValue, 0, len(n.Elems))
		for _, elem := range n.Elems {
			value, ok := evalConstExprWithLookup(elem, lookup, call)
			if !ok {
				return semantic.ConstValue{}, false
			}
			elems = append(elems, value)
		}
		return semantic.ConstValue{Kind: semantic.ConstTuple, Elems: elems}, true
	case *ast.ListLitExpr:
		if n.Owner != nil {
			return semantic.ConstValue{}, false
		}
		elems := make([]semantic.ConstValue, 0, len(n.Elems))
		for i, elem := range n.Elems {
			if i < len(n.Spreads) && n.Spreads[i] {
				return semantic.ConstValue{}, false
			}
			value, ok := evalConstExprWithLookup(elem, lookup, call)
			if !ok {
				return semantic.ConstValue{}, false
			}
			elems = append(elems, value)
		}
		return semantic.ConstValue{Kind: semantic.ConstList, Elems: elems}, true
	case *ast.StructLitExpr:
		if len(n.Spreads) != 0 {
			for _, spread := range n.Spreads {
				if spread != nil {
					return semantic.ConstValue{}, false
				}
			}
		}
		args := n.Args
		if n.ResolvedArgsValid {
			args = n.ResolvedArgs
		}
		fields := map[string]semantic.ConstValue{}
		for i, arg := range args {
			if i >= len(n.ArgNames) {
				return semantic.ConstValue{}, false
			}
			fieldName := n.ArgNames[i]
			if fieldName == "" {
				return semantic.ConstValue{}, false
			}
			value, ok := evalConstExprWithLookup(arg, lookup, call)
			if !ok {
				return semantic.ConstValue{}, false
			}
			fields[fieldName] = value
		}
		return semantic.ConstValue{Kind: semantic.ConstRecord, Fields: fields}, true
	case *ast.IndexExpr:
		object, ok := evalConstExprWithLookup(n.Object, lookup, call)
		if !ok {
			return semantic.ConstValue{}, false
		}
		index, ok := evalConstExprWithLookup(n.Index, lookup, call)
		if !ok || index.Kind != semantic.ConstInt {
			return semantic.ConstValue{}, false
		}
		if index.Int >= 0 {
			slot := int(index.Int)
			if slot < len(object.Elems) && (object.Kind == semantic.ConstTuple || object.Kind == semantic.ConstList) {
				return object.Elems[slot], true
			}
		}
		if n.Fallback != nil {
			return evalConstExprWithLookup(n.Fallback, lookup, call)
		}
		return semantic.ConstValue{}, false
	case *ast.CallExpr:
		if call == nil {
			return semantic.ConstValue{}, false
		}
		return call(n)
	default:
		return semantic.ConstValue{}, false
	}
}

func evalBackendConstAggregateFieldExpr(expr *ast.FieldExpr, lookup func(string) (semantic.ConstValue, bool), call func(*ast.CallExpr) (semantic.ConstValue, bool)) (semantic.ConstValue, bool) {
	if expr == nil || expr.Object == nil {
		return semantic.ConstValue{}, false
	}
	object, ok := evalConstExprWithLookup(expr.Object, lookup, call)
	if !ok {
		return semantic.ConstValue{}, false
	}
	switch expr.Field {
	case "count":
		if object.Kind == semantic.ConstList || object.Kind == semantic.ConstTuple {
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: int64(len(object.Elems))}, true
		}
	default:
		if value, ok := semantic.ConstReflectionRecordField(object, expr.Field); ok {
			return value, true
		}
	}
	return semantic.ConstValue{}, false
}

func (s *functionState) evalConstQueryExpr(expr *ast.QueryExpr) (semantic.ConstValue, bool) {
	if expr == nil {
		return semantic.ConstValue{}, false
	}
	source, ok := s.evalConstExpr(expr.Source)
	if !ok || (source.Kind != semantic.ConstList && source.Kind != semantic.ConstTuple) {
		return semantic.ConstValue{}, false
	}
	switch expr.Kind {
	case ast.QueryExprAny:
		for _, elem := range source.Elems {
			match, ok := s.evalConstQueryFilter(expr, elem)
			if !ok {
				return semantic.ConstValue{}, false
			}
			if match {
				return semantic.ConstValue{Kind: semantic.ConstBool, Bool: true}, true
			}
		}
		return semantic.ConstValue{Kind: semantic.ConstBool, Bool: false}, true
	case ast.QueryExprAll:
		for _, elem := range source.Elems {
			match, ok := s.evalConstQueryFilter(expr, elem)
			if !ok {
				return semantic.ConstValue{}, false
			}
			if !match {
				return semantic.ConstValue{Kind: semantic.ConstBool, Bool: false}, true
			}
		}
		return semantic.ConstValue{Kind: semantic.ConstBool, Bool: true}, true
	case ast.QueryExprCount:
		total := int64(0)
		for _, elem := range source.Elems {
			match, ok := s.evalConstQueryFilter(expr, elem)
			if !ok {
				return semantic.ConstValue{}, false
			}
			if match {
				total++
			}
		}
		return semantic.ConstValue{Kind: semantic.ConstInt, Int: total}, true
	case ast.QueryExprFirst:
		for _, elem := range source.Elems {
			include, value, ok := s.evalConstQueryProjectedValue(expr, elem)
			if !ok {
				return semantic.ConstValue{}, false
			}
			if include {
				return backendConstOptionalFromFirstProjection(value), true
			}
		}
		return semantic.ConstValue{Kind: semantic.ConstOptional}, true
	case ast.QueryExprEach:
		elems := make([]semantic.ConstValue, 0, len(source.Elems))
		for _, elem := range source.Elems {
			include, value, ok := s.evalConstQueryProjectedValue(expr, elem)
			if !ok {
				return semantic.ConstValue{}, false
			}
			if include {
				elems = append(elems, cloneBackendConstValue(value))
			}
		}
		return semantic.ConstValue{Kind: semantic.ConstList, Elems: elems}, true
	default:
		return semantic.ConstValue{}, false
	}
}

func (s *functionState) evalConstQueryFilter(expr *ast.QueryExpr, item semantic.ConstValue) (bool, bool) {
	result, ok := s.evalConstQueryInItemScope(expr, item, func() (semantic.ConstValue, bool) {
		if expr.Filter == nil {
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: true}, true
		}
		return s.evalConstExpr(expr.Filter)
	})
	if !ok || result.Kind != semantic.ConstBool {
		return false, false
	}
	return result.Bool, true
}

func (s *functionState) evalConstQueryProjectedValue(expr *ast.QueryExpr, item semantic.ConstValue) (bool, semantic.ConstValue, bool) {
	if expr.Projection == nil {
		include, ok := s.evalConstQueryFilter(expr, item)
		return include, item, ok
	}
	return s.evalConstQueryProjection(expr, item)
}

func (s *functionState) evalConstQueryProjection(expr *ast.QueryExpr, item semantic.ConstValue) (bool, semantic.ConstValue, bool) {
	include, ok := s.evalConstQueryFilter(expr, item)
	if !ok || !include {
		return include, semantic.ConstValue{}, ok
	}
	value, ok := s.evalConstQueryInItemScope(expr, item, func() (semantic.ConstValue, bool) {
		return s.evalConstExpr(expr.Projection)
	})
	return true, value, ok
}

func (s *functionState) evalConstQueryInItemScope(expr *ast.QueryExpr, item semantic.ConstValue, eval func() (semantic.ConstValue, bool)) (semantic.ConstValue, bool) {
	scope := map[string]semantic.ConstValue{}
	if expr != nil {
		pattern := ast.MoveBindPattern(&ast.MoveBindNamePattern{Position: expr.Pos(), Name: expr.Name})
		if expr.Pattern != nil {
			pattern = expr.Pattern
		}
		var ok bool
		scope, ok = bindBackendConstMovePattern(pattern, item)
		if !ok {
			return semantic.ConstValue{}, false
		}
	}
	s.g.constEvalScopes = append(s.g.constEvalScopes, scope)
	if expr != nil && expr.PatternFilter != nil {
		matched, bindings, ok := s.evalConstQueryPatternFilter(expr)
		if !ok {
			s.g.constEvalScopes = s.g.constEvalScopes[:len(s.g.constEvalScopes)-1]
			return semantic.ConstValue{}, ok
		}
		if !matched {
			s.g.constEvalScopes = s.g.constEvalScopes[:len(s.g.constEvalScopes)-1]
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: false}, true
		}
		for name, value := range bindings {
			scope[name] = value
		}
		s.g.constEvalScopes[len(s.g.constEvalScopes)-1] = scope
	}
	value, ok := eval()
	s.g.constEvalScopes = s.g.constEvalScopes[:len(s.g.constEvalScopes)-1]
	return value, ok
}

func (s *functionState) evalConstQueryPatternFilter(expr *ast.QueryExpr) (bool, map[string]semantic.ConstValue, bool) {
	if expr == nil || expr.PatternFilter == nil {
		return true, nil, true
	}
	subject, ok := s.evalConstQueryPatternFilterSubject(expr)
	if !ok {
		return false, nil, false
	}
	matched, bindings, ok := s.evalStaticMatchPattern(expr.PatternFilter, subject)
	if !ok {
		return false, nil, false
	}
	if !matched {
		return false, nil, true
	}
	return true, bindings, true
}

func (s *functionState) evalConstQueryPatternFilterSubject(expr *ast.QueryExpr) (semantic.ConstValue, bool) {
	if expr == nil || expr.PatternFilter == nil {
		return semantic.ConstValue{}, false
	}
	if expr.PatternFilterSubject != "" {
		if value, _, ok := s.constEvalValueScope(expr.PatternFilterSubject); ok {
			return value, true
		}
		return semantic.ConstValue{}, false
	}
	if expr.Name != "" && expr.Name != "_" {
		if value, _, ok := s.constEvalValueScope(expr.Name); ok {
			return value, true
		}
	}
	return semantic.ConstValue{}, false
}

func (g *llvmGenerator) evalConstQueryExpr(expr *ast.QueryExpr) (semantic.ConstValue, bool) {
	if expr == nil {
		return semantic.ConstValue{}, false
	}
	source, ok := g.evalConstExpr(expr.Source)
	if !ok || (source.Kind != semantic.ConstList && source.Kind != semantic.ConstTuple) {
		return semantic.ConstValue{}, false
	}
	switch expr.Kind {
	case ast.QueryExprAny:
		for _, elem := range source.Elems {
			match, ok := g.evalConstQueryFilter(expr, elem)
			if !ok {
				return semantic.ConstValue{}, false
			}
			if match {
				return semantic.ConstValue{Kind: semantic.ConstBool, Bool: true}, true
			}
		}
		return semantic.ConstValue{Kind: semantic.ConstBool, Bool: false}, true
	case ast.QueryExprAll:
		for _, elem := range source.Elems {
			match, ok := g.evalConstQueryFilter(expr, elem)
			if !ok {
				return semantic.ConstValue{}, false
			}
			if !match {
				return semantic.ConstValue{Kind: semantic.ConstBool, Bool: false}, true
			}
		}
		return semantic.ConstValue{Kind: semantic.ConstBool, Bool: true}, true
	case ast.QueryExprCount:
		total := int64(0)
		for _, elem := range source.Elems {
			match, ok := g.evalConstQueryFilter(expr, elem)
			if !ok {
				return semantic.ConstValue{}, false
			}
			if match {
				total++
			}
		}
		return semantic.ConstValue{Kind: semantic.ConstInt, Int: total}, true
	case ast.QueryExprFirst:
		for _, elem := range source.Elems {
			include, value, ok := g.evalConstQueryProjectedValue(expr, elem)
			if !ok {
				return semantic.ConstValue{}, false
			}
			if include {
				return backendConstOptionalFromFirstProjection(value), true
			}
		}
		return semantic.ConstValue{Kind: semantic.ConstOptional}, true
	case ast.QueryExprEach:
		elems := make([]semantic.ConstValue, 0, len(source.Elems))
		for _, elem := range source.Elems {
			include, value, ok := g.evalConstQueryProjectedValue(expr, elem)
			if !ok {
				return semantic.ConstValue{}, false
			}
			if include {
				elems = append(elems, cloneBackendConstValue(value))
			}
		}
		return semantic.ConstValue{Kind: semantic.ConstList, Elems: elems}, true
	default:
		return semantic.ConstValue{}, false
	}
}

func (g *llvmGenerator) evalConstQueryFilter(expr *ast.QueryExpr, item semantic.ConstValue) (bool, bool) {
	result, ok := g.evalConstQueryInItemScope(expr, item, func() (semantic.ConstValue, bool) {
		if expr.Filter == nil {
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: true}, true
		}
		return g.evalConstExpr(expr.Filter)
	})
	if !ok || result.Kind != semantic.ConstBool {
		return false, false
	}
	return result.Bool, true
}

func (g *llvmGenerator) evalConstQueryProjectedValue(expr *ast.QueryExpr, item semantic.ConstValue) (bool, semantic.ConstValue, bool) {
	if expr.Projection == nil {
		include, ok := g.evalConstQueryFilter(expr, item)
		return include, item, ok
	}
	return g.evalConstQueryProjection(expr, item)
}

func (g *llvmGenerator) evalConstQueryProjection(expr *ast.QueryExpr, item semantic.ConstValue) (bool, semantic.ConstValue, bool) {
	include, ok := g.evalConstQueryFilter(expr, item)
	if !ok || !include {
		return include, semantic.ConstValue{}, ok
	}
	value, ok := g.evalConstQueryInItemScope(expr, item, func() (semantic.ConstValue, bool) {
		return g.evalConstExpr(expr.Projection)
	})
	return true, value, ok
}

func (g *llvmGenerator) evalConstQueryInItemScope(expr *ast.QueryExpr, item semantic.ConstValue, eval func() (semantic.ConstValue, bool)) (semantic.ConstValue, bool) {
	scope := map[string]semantic.ConstValue{}
	if expr != nil {
		pattern := ast.MoveBindPattern(&ast.MoveBindNamePattern{Position: expr.Pos(), Name: expr.Name})
		if expr.Pattern != nil {
			pattern = expr.Pattern
		}
		var ok bool
		scope, ok = bindBackendConstMovePattern(pattern, item)
		if !ok {
			return semantic.ConstValue{}, false
		}
	}
	g.constEvalScopes = append(g.constEvalScopes, scope)
	if expr != nil && expr.PatternFilter != nil {
		matched, bindings, ok := g.evalConstQueryPatternFilter(expr)
		if !ok {
			g.constEvalScopes = g.constEvalScopes[:len(g.constEvalScopes)-1]
			return semantic.ConstValue{}, ok
		}
		if !matched {
			g.constEvalScopes = g.constEvalScopes[:len(g.constEvalScopes)-1]
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: false}, true
		}
		for name, value := range bindings {
			scope[name] = value
		}
		g.constEvalScopes[len(g.constEvalScopes)-1] = scope
	}
	value, ok := eval()
	g.constEvalScopes = g.constEvalScopes[:len(g.constEvalScopes)-1]
	return value, ok
}

func (g *llvmGenerator) evalConstQueryPatternFilter(expr *ast.QueryExpr) (bool, map[string]semantic.ConstValue, bool) {
	if expr == nil || expr.PatternFilter == nil {
		return true, nil, true
	}
	subject, ok := g.evalConstQueryPatternFilterSubject(expr)
	if !ok {
		return false, nil, false
	}
	matched, bindings, ok := g.evalStaticMatchPattern(expr.PatternFilter, subject)
	if !ok {
		return false, nil, false
	}
	if !matched {
		return false, nil, true
	}
	return true, bindings, true
}

func (g *llvmGenerator) evalConstQueryPatternFilterSubject(expr *ast.QueryExpr) (semantic.ConstValue, bool) {
	if expr == nil || expr.PatternFilter == nil {
		return semantic.ConstValue{}, false
	}
	if expr.PatternFilterSubject != "" {
		if value, _, ok := g.constEvalValueScope(expr.PatternFilterSubject); ok {
			return value, true
		}
		return semantic.ConstValue{}, false
	}
	if expr.Name != "" && expr.Name != "_" {
		if value, _, ok := g.constEvalValueScope(expr.Name); ok {
			return value, true
		}
	}
	return semantic.ConstValue{}, false
}

func backendConstMoveBindFieldName(arg ast.MoveBindArg) string {
	if arg.Field != "" {
		return arg.Field
	}
	return arg.Name
}

func bindBackendConstMovePattern(pattern ast.MoveBindPattern, item semantic.ConstValue) (map[string]semantic.ConstValue, bool) {
	scope := map[string]semantic.ConstValue{}
	if pattern == nil {
		return scope, true
	}
	switch p := pattern.(type) {
	case *ast.MoveBindNamePattern:
		if p == nil || p.Name == "" || p.Name == "_" {
			return scope, true
		}
		scope[p.Name] = cloneBackendConstValue(item)
		return scope, true
	case *ast.MoveBindTuplePattern:
		if p == nil || (item.Kind != semantic.ConstTuple && item.Kind != semantic.ConstList) || len(item.Elems) < len(p.Args) {
			return nil, false
		}
		for index, arg := range p.Args {
			if arg.Name == "" || arg.Name == "_" {
				continue
			}
			scope[arg.Name] = cloneBackendConstValue(item.Elems[index])
		}
		return scope, true
	case *ast.MoveBindStructPattern:
		if p == nil || item.Kind != semantic.ConstRecord {
			return nil, false
		}
		for _, arg := range p.Args {
			if arg.Name == "" || arg.Name == "_" {
				continue
			}
			fieldValue, ok := semantic.ConstReflectionRecordField(item, backendConstMoveBindFieldName(arg))
			if !ok {
				return nil, false
			}
			scope[arg.Name] = cloneBackendConstValue(fieldValue)
		}
		return scope, true
	default:
		return nil, false
	}
}

func lookupConstEvalValue(lookup func(string) (semantic.ConstValue, bool), name string) (semantic.ConstValue, bool) {
	if lookup == nil {
		return semantic.ConstValue{}, false
	}
	return lookup("$consteval." + name)
}

func (g *llvmGenerator) evalStaticFunctionCall(expr *ast.CallExpr) (semantic.ConstValue, bool) {
	if expr == nil || g == nil || g.staticCallDepth >= semantic.StaticEvalCallDepthLimit {
		return semantic.ConstValue{}, false
	}
	if value, ok := g.evalConstReflectionCall(expr); ok {
		return value, true
	}
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return semantic.ConstValue{}, false
	}
	sym, ok := g.lookupStaticFunctionSymbol(ident.Name)
	if !ok || sym == nil {
		return semantic.ConstValue{}, false
	}
	fnType, ok := sym.Type.(*semantic.FuncType)
	if !ok || fnType == nil || !fnType.Static {
		return semantic.ConstValue{}, false
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok || decl == nil {
		return semantic.ConstValue{}, false
	}
	args := expr.Args
	if expr.ResolvedArgsValid {
		args = expr.ResolvedArgs
	}
	if len(decl.Params) != len(args) {
		return semantic.ConstValue{}, false
	}
	scope := make(map[string]semantic.ConstValue, len(decl.Params))
	for i, arg := range args {
		value, ok := g.evalConstExpr(arg)
		if !ok {
			return semantic.ConstValue{}, false
		}
		scope[decl.Params[i].Name] = value
	}
	g.staticCallDepth++
	g.constEvalScopes = append(g.constEvalScopes, scope)
	state := &functionState{g: g}
	value, returned, ok := state.evalStaticStmtBlock(decl.Body, true)
	g.constEvalScopes = g.constEvalScopes[:len(g.constEvalScopes)-1]
	g.staticCallDepth--
	if !returned {
		if isVoidType(fnType.Return) {
			return semantic.ConstValue{Kind: semantic.ConstUnknown}, true
		}
		return semantic.ConstValue{}, false
	}
	return value, ok
}

func (g *llvmGenerator) evalConstReflectionCall(expr *ast.CallExpr) (semantic.ConstValue, bool) {
	if expr == nil || g == nil || g.result == nil {
		return semantic.ConstValue{}, false
	}
	if fieldExpr, ok := expr.Func.(*ast.FieldExpr); ok && fieldExpr != nil && fieldExpr.Field == "has_field" && len(expr.Args) == 1 && expr.NamedArgCount() == 0 {
		object, objectOK := g.evalConstExpr(fieldExpr.Object)
		fieldName, fieldOK := g.evalConstExpr(expr.Args[0])
		if !objectOK || !fieldOK || fieldName.Kind != semantic.ConstString {
			return semantic.ConstValue{}, false
		}
		return semantic.ConstReflectionRecordHasField(object, fieldName.String)
	}
	name, _ := semantic.QualifiedNameExpr(expr.Func)
	if name != "variants" && name != "fields" {
		return semantic.ConstValue{}, false
	}
	return semantic.ConstReflectionCallValue(name, expr.Args, func(typeName string) (semantic.Type, bool) {
		return g.lookupConstReflectionType(typeName)
	})
}

func (g *llvmGenerator) lookupConstReflectionType(typeName string) (semantic.Type, bool) {
	if g == nil || g.result == nil || typeName == "" {
		return nil, false
	}
	if t, ok := g.result.NamedTypes[typeName]; ok {
		return t, true
	}
	var matched semantic.Type
	suffix := "." + typeName
	for name, t := range g.result.NamedTypes {
		if strings.HasSuffix(name, suffix) {
			if matched != nil {
				return nil, false
			}
			matched = t
		}
	}
	return matched, matched != nil
}

func (g *llvmGenerator) lookupStaticFunctionSymbol(name string) (*semantic.Symbol, bool) {
	if g == nil || g.result == nil || g.result.GlobalScope == nil {
		return nil, false
	}
	if sym, ok := g.result.GlobalScope.Lookup(name); ok {
		return sym, true
	}
	var matched *semantic.Symbol
	suffix := "." + name
	for symbolName, sym := range g.result.GlobalScope.Symbols {
		if strings.HasSuffix(symbolName, suffix) {
			if matched != nil {
				return nil, false
			}
			matched = sym
		}
	}
	return matched, matched != nil
}

func cloneBackendConstValue(value semantic.ConstValue) semantic.ConstValue {
	return semantic.CloneConstValue(value)
}

func backendConstOptionalSome(value semantic.ConstValue) semantic.ConstValue {
	cloned := cloneBackendConstValue(value)
	return semantic.ConstValue{Kind: semantic.ConstOptional, Some: true, Value: &cloned}
}

func backendConstOptionalFromFirstProjection(value semantic.ConstValue) semantic.ConstValue {
	if value.Kind == semantic.ConstOptional {
		return cloneBackendConstValue(value)
	}
	return backendConstOptionalSome(value)
}

func (g *llvmGenerator) setConstEvalValue(name string, value semantic.ConstValue) {
	if len(g.constEvalScopes) == 0 {
		g.constEvalScopes = append(g.constEvalScopes, map[string]semantic.ConstValue{})
	}
	g.constEvalScopes[len(g.constEvalScopes)-1][name] = cloneBackendConstValue(value)
}

func (g *llvmGenerator) updateConstEvalValue(name string, value semantic.ConstValue) bool {
	for i := len(g.constEvalScopes) - 1; i >= 0; i-- {
		if _, ok := g.constEvalScopes[i][name]; ok {
			g.constEvalScopes[i][name] = cloneBackendConstValue(value)
			return true
		}
	}
	return false
}

func (g *llvmGenerator) constEvalValueScope(name string) (semantic.ConstValue, int, bool) {
	for i := len(g.constEvalScopes) - 1; i >= 0; i-- {
		if value, ok := g.constEvalScopes[i][name]; ok {
			return value, i, true
		}
	}
	return semantic.ConstValue{}, -1, false
}

func (s *functionState) constEvalValueScope(name string) (semantic.ConstValue, int, bool) {
	if s == nil || s.g == nil {
		return semantic.ConstValue{}, -1, false
	}
	return s.g.constEvalValueScope(name)
}

func (g *llvmGenerator) evalStaticMatchPattern(pattern ast.MatchPattern, value semantic.ConstValue) (bool, map[string]semantic.ConstValue, bool) {
	if g == nil {
		return false, nil, false
	}
	return (&functionState{g: g}).evalStaticMatchPattern(pattern, value)
}
func evalConstEquality(left, right semantic.ConstValue, equal bool) (semantic.ConstValue, bool) {
	matched := false
	switch {
	case left.Kind == semantic.ConstInt && right.Kind == semantic.ConstInt:
		matched = left.Int == right.Int
	case (left.Kind == semantic.ConstInt || left.Kind == semantic.ConstFloat) && (right.Kind == semantic.ConstInt || right.Kind == semantic.ConstFloat):
		matched = math.Float64bits(backendConstNumericAsFloat64(left)) == math.Float64bits(backendConstNumericAsFloat64(right))
	case left.Kind == semantic.ConstBool && right.Kind == semantic.ConstBool:
		matched = left.Bool == right.Bool
	case left.Kind == semantic.ConstString && right.Kind == semantic.ConstString:
		matched = left.String == right.String
	default:
		return semantic.ConstValue{}, false
	}
	if !equal {
		matched = !matched
	}
	return semantic.ConstValue{Kind: semantic.ConstBool, Bool: matched}, true
}
func evalBackendConstNumericBinary(op lexer.TokenKind, left, right semantic.ConstValue) (semantic.ConstValue, bool) {
	if left.Kind == semantic.ConstInt && right.Kind == semantic.ConstInt {
		switch op {
		case lexer.TOKEN_LT:
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: left.Int < right.Int}, true
		case lexer.TOKEN_GT:
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: left.Int > right.Int}, true
		case lexer.TOKEN_LTEQ:
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: left.Int <= right.Int}, true
		case lexer.TOKEN_GTEQ:
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: left.Int >= right.Int}, true
		case lexer.TOKEN_PLUS:
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int + right.Int}, true
		case lexer.TOKEN_MINUS:
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int - right.Int}, true
		case lexer.TOKEN_STAR:
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int * right.Int}, true
		case lexer.TOKEN_SLASH:
			if right.Int == 0 {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int / right.Int}, true
		default:
			return semantic.ConstValue{}, false
		}
	}
	if !backendIsConstNumeric(left) || !backendIsConstNumeric(right) {
		return semantic.ConstValue{}, false
	}
	leftFloat := backendConstNumericAsFloat64(left)
	rightFloat := backendConstNumericAsFloat64(right)
	switch op {
	case lexer.TOKEN_LT:
		return semantic.ConstValue{Kind: semantic.ConstBool, Bool: leftFloat < rightFloat}, true
	case lexer.TOKEN_GT:
		return semantic.ConstValue{Kind: semantic.ConstBool, Bool: leftFloat > rightFloat}, true
	case lexer.TOKEN_LTEQ:
		return semantic.ConstValue{Kind: semantic.ConstBool, Bool: leftFloat <= rightFloat}, true
	case lexer.TOKEN_GTEQ:
		return semantic.ConstValue{Kind: semantic.ConstBool, Bool: leftFloat >= rightFloat}, true
	case lexer.TOKEN_PLUS:
		return semantic.ConstValue{Kind: semantic.ConstFloat, Float: leftFloat + rightFloat}, true
	case lexer.TOKEN_MINUS:
		return semantic.ConstValue{Kind: semantic.ConstFloat, Float: leftFloat - rightFloat}, true
	case lexer.TOKEN_STAR:
		return semantic.ConstValue{Kind: semantic.ConstFloat, Float: leftFloat * rightFloat}, true
	case lexer.TOKEN_SLASH:
		if rightFloat == 0 {
			return semantic.ConstValue{}, false
		}
		return semantic.ConstValue{Kind: semantic.ConstFloat, Float: leftFloat / rightFloat}, true
	default:
		return semantic.ConstValue{}, false
	}
}
func backendIsConstNumeric(value semantic.ConstValue) bool {
	return value.Kind == semantic.ConstInt || value.Kind == semantic.ConstFloat
}
func backendConstNumericAsFloat64(value semantic.ConstValue) float64 {
	if value.Kind == semantic.ConstFloat {
		return value.Float
	}
	return float64(value.Int)
}
func isZeroedExpr(expr ast.Expr) bool {
	_, ok := expr.(*ast.ZeroedLit)
	return ok
}
func isStaticallyZeroFillExpr(s *functionState, expr ast.Expr) bool {
	if isZeroedExpr(expr) {
		return true
	}
	if s == nil {
		return false
	}
	value, ok := s.evalConstExpr(expr)
	if !ok {
		return false
	}
	switch value.Kind {
	case semantic.ConstInt:
		return value.Int == 0
	case semantic.ConstBool:
		return !value.Bool
	default:
		return false
	}
}
func staticRepeatedByteFillValueForType(s *functionState, expr ast.Expr, fillType semantic.Type) (uint8, bool) {
	if isZeroedExpr(expr) {
		return 0, true
	}
	if s == nil || fillType == nil {
		return 0, false
	}
	value, ok := s.evalConstExpr(expr)
	if !ok {
		return 0, false
	}
	sizeBytes, err := s.sizeOfType(fillType)
	if err != nil || sizeBytes == 0 || sizeBytes > 8 {
		return 0, false
	}
	switch value.Kind {
	case semantic.ConstBool:
		if sizeBytes != 1 {
			return 0, false
		}
		if value.Bool {
			return 1, true
		}
		return 0, true
	case semantic.ConstInt:
		if !semantic.IsNumericType(fillType) {
			return 0, false
		}
		raw := uint64(value.Int)
		bitWidth := sizeBytes * 8
		if bitWidth < 64 {
			raw &= (uint64(1) << bitWidth) - 1
		}
		fillByte := uint8(raw & 0xff)
		for i := uint64(1); i < sizeBytes; i++ {
			if uint8((raw>>(8*i))&0xff) != fillByte {
				return 0, false
			}
		}
		return fillByte, true
	default:
		return 0, false
	}
}
func isSingleByteScalarFillType(s *functionState, t semantic.Type) bool {
	if s == nil || t == nil {
		return false
	}
	if !semantic.IsNumericType(t) && !semantic.IsBoolType(t) {
		return false
	}
	sizeBytes, err := s.sizeOfType(t)
	if err != nil {
		return false
	}
	return sizeBytes == 1
}
func constOptimizationExtentSize(extent *semantic.OptimizationExtent) (uint64, bool) {
	if extent == nil {
		return 0, false
	}
	switch extent.Kind {
	case semantic.OptimizationExtentArraySize:
		if extent.HasConstSize && extent.ConstSize >= 0 {
			return uint64(extent.ConstSize), true
		}
		return 0, false
	case semantic.OptimizationExtentViewBounds:
		begin, ok := parseOptimizationExtentConstInt(extent.Begin)
		if !ok {
			return 0, false
		}
		end, ok := parseOptimizationExtentConstInt(extent.End)
		if !ok || end < begin {
			return 0, false
		}
		return uint64(end - begin), true
	default:
		return 0, false
	}
}
func parseOptimizationExtentConstInt(value string) (int64, bool) {
	trimmed := strings.TrimSpace(value)
	for len(trimmed) >= 2 && trimmed[0] == '(' && trimmed[len(trimmed)-1] == ')' {
		trimmed = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	}
	if trimmed == "" {
		return 0, false
	}
	if last := trimmed[len(trimmed)-1]; last == 'u' || last == 'i' {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if trimmed == "" {
		return 0, false
	}
	parsed, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}
func isVoidType(t semantic.Type) bool {
	b, ok := t.(*semantic.BuiltinType)
	return ok && b.Name == "void"
}
func isNumericType(t semantic.Type) bool {
	return semantic.IsNumericType(t)
}
func numericCastType(t semantic.Type) semantic.Type {
	if storage, ok := semantic.ConstEnumStorageType(t); ok {
		return storage
	}
	if idType, ok := t.(*semantic.IDType); ok && idType != nil && idType.Storage != nil {
		return idType.Storage
	}
	return t
}
func isNumericCastType(t semantic.Type) bool {
	return semantic.IsNumericType(numericCastType(t))
}
func isPointerLikeType(t semantic.Type) bool {
	switch t.(type) {
	case *semantic.RefType, *semantic.NullType, *semantic.DStrType, *semantic.FuncType:
		return true
	default:
		return false
	}
}
func isSignedIntegerType(t semantic.Type) bool {
	t = numericCastType(t)
	if signed, _, ok := semantic.BitIntInfo(t); ok {
		return signed
	}
	b, ok := t.(*semantic.BuiltinType)
	if !ok {
		if _, ok := t.(*semantic.NullType); ok {
			return false
		}
		return false
	}
	switch b.Name {
	case "char", "int", "isize", "i8", "i16", "i32", "i64":
		return true
	default:
		return false
	}
}
func isFloatType(t semantic.Type) bool {
	t = numericCastType(t)
	return semantic.IsFloatType(t)
}
func integerBitWidth(t semantic.Type, wordBits int) int {
	t = numericCastType(t)
	if _, width, ok := semantic.BitIntInfo(t); ok {
		return width
	}
	b, ok := t.(*semantic.BuiltinType)
	if !ok {
		if isPointerLikeType(t) {
			return wordBits
		}
		return wordBits
	}
	switch b.Name {
	case "bool":
		return 1
	case "i8", "u8":
		return 8
	case "i16", "u16":
		return 16
	case "i32", "u32":
		return 32
	case "f32":
		return 32
	case "char", "i64", "u64":
		return 64
	case "f64":
		return 64
	case "int", "isize", "usize", "uintptr":
		return wordBits
	default:
		return wordBits
	}
}
func llvmIntPredicate(op lexer.TokenKind, operandType semantic.Type) (C.LLVMIntPredicate, error) {
	signed := isSignedIntegerType(operandType)
	switch op {
	case lexer.TOKEN_EQEQ:
		return C.LLVMIntEQ, nil
	case lexer.TOKEN_BANGEQ:
		return C.LLVMIntNE, nil
	case lexer.TOKEN_LT:
		if signed {
			return C.LLVMIntSLT, nil
		}
		return C.LLVMIntULT, nil
	case lexer.TOKEN_GT:
		if signed {
			return C.LLVMIntSGT, nil
		}
		return C.LLVMIntUGT, nil
	case lexer.TOKEN_LTEQ:
		if signed {
			return C.LLVMIntSLE, nil
		}
		return C.LLVMIntULE, nil
	case lexer.TOKEN_GTEQ:
		if signed {
			return C.LLVMIntSGE, nil
		}
		return C.LLVMIntUGE, nil
	default:
		return 0, fmt.Errorf("unsupported comparison operator %s", lexer.TokenName(op))
	}
}
func llvmFloatPredicate(op lexer.TokenKind) (C.LLVMRealPredicate, error) {
	switch op {
	case lexer.TOKEN_EQEQ:
		return C.LLVMRealOEQ, nil
	case lexer.TOKEN_BANGEQ:
		return C.LLVMRealONE, nil
	case lexer.TOKEN_LT:
		return C.LLVMRealOLT, nil
	case lexer.TOKEN_GT:
		return C.LLVMRealOGT, nil
	case lexer.TOKEN_LTEQ:
		return C.LLVMRealOLE, nil
	case lexer.TOKEN_GTEQ:
		return C.LLVMRealOGE, nil
	default:
		return 0, fmt.Errorf("unsupported comparison operator %s", lexer.TokenName(op))
	}
}
func cStringFree(s string) *C.char {
	if s == "" {
		return nil
	}
	ptr := C.CString(s)
	return ptr
}
