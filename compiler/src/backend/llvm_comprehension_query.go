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
)

func (s *functionState) emitDictComprehensionExpr(expr *ast.ListComprehensionExpr) (C.LLVMValueRef, semantic.Type, error) {
	resultType, ok := s.exprType(expr).(*semantic.DictType)
	if !ok || resultType == nil {
		return nil, nil, fmt.Errorf("dict comprehension requires a resolved dict result type")
	}
	resultName := s.g.nextSyntheticName("dict.comp.result.")
	resultInit := &ast.ListLitExpr{Position: expr.Position, Brace: true}
	if s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnExprTypesMap() {
		{
			key, value := resultInit, resultType
			s.g.setResultExprType(key, value)
		}
	}
	resultIdent := &ast.Ident{Position: expr.Position, Name: resultName}
	// `d.put(k, v)` — the frictionless dict-set (parity with darray push: no error
	// union, no `can` grant, panics on OOM). See collections.elisa arena_dict_put_or_panic.
	putCall := &ast.CallExpr{
		Position: expr.Position,
		Func: &ast.FieldExpr{
			Position: expr.Position,
			Object:   resultIdent,
			Field:    "put",
		},
		Args: []ast.Expr{expr.Key, expr.Value},
	}
	block := comprehensionDesugarBlock(expr, resultName, resultInit, resultIdent, putCall)
	return s.emitExprBlock(block, resultType)
}

// emitSetComprehensionExpr lowers `{ value for name in source [if filter] }` into
//
//	{ result: mutable set[V] = {};  for name in source: (if filter:) result.add(value);  result }
//
// — a single fused loop with no intermediate collection (docs/79). `add` is the frictionless
// set insert (parity with darray push). Mirrors the darray/dict comprehension paths.
func (s *functionState) emitSetComprehensionExpr(expr *ast.ListComprehensionExpr) (C.LLVMValueRef, semantic.Type, error) {
	resultType, ok := s.exprType(expr).(*semantic.SetType)
	if !ok || resultType == nil {
		return nil, nil, fmt.Errorf("set comprehension requires a resolved set result type")
	}
	resultName := s.g.nextSyntheticName("set.comp.result.")
	resultInit := &ast.ListLitExpr{Position: expr.Position, Brace: true}
	if s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnExprTypesMap() {
		{
			key, value := resultInit, resultType
			s.g.setResultExprType(key, value)
		}
	}
	resultIdent := &ast.Ident{Position: expr.Position, Name: resultName}
	addCall := &ast.CallExpr{
		Position: expr.Position,
		Func: &ast.FieldExpr{
			Position: expr.Position,
			Object:   resultIdent,
			Field:    "add",
		},
		Args: []ast.Expr{expr.Value},
	}
	block := comprehensionDesugarBlock(expr, resultName, resultInit, resultIdent, addCall)
	return s.emitExprBlock(block, resultType)
}
func (s *functionState) emitQueryExpr(expr *ast.QueryExpr) (C.LLVMValueRef, semantic.Type, error) {
	resultType := s.exprType(expr)
	if resultType == nil {
		return nil, nil, fmt.Errorf("query expression requires a resolved result type")
	}
	resultName := s.g.nextSyntheticName("query.result.")
	resultIdent := &ast.Ident{Position: expr.Position, Name: resultName}
	var init ast.Expr
	var body []ast.Stmt
	filter := expr.Filter
	patternFilter := expr.PatternFilter
	switch expr.Kind {
	case ast.QueryExprAny:
		init = &ast.BoolLit{Position: expr.Position, Value: false}
		body = []ast.Stmt{&ast.AssignStmt{Position: expr.Position, Target: resultIdent, Value: &ast.BoolLit{Position: expr.Position, Value: true}}}
	case ast.QueryExprAll:
		init = &ast.BoolLit{Position: expr.Position, Value: true}
		if expr.PatternFilter != nil {
			itemName := expr.Name
			if expr.PatternFilterSubject != "" {
				itemName = expr.PatternFilterSubject
			}
			item := &ast.Ident{Position: expr.Position, Name: itemName}
			failValue := ast.Expr(&ast.BoolLit{Position: expr.Position, Value: false})
			if expr.Filter != nil {
				failValue = &ast.UnaryExpr{Position: expr.Filter.Pos(), Op: lexer.TOKEN_NOT, Operand: expr.Filter}
			}
			wildcardValue := &ast.BoolLit{Position: expr.PatternFilter.Pos(), Value: true}
			filter = &ast.MatchExpr{
				Position: expr.PatternFilter.Pos(),
				Value:    item,
				Arms: []ast.MatchArm{
					{Position: expr.PatternFilter.Pos(), Pattern: expr.PatternFilter, Body: []ast.Stmt{&ast.ExprStmt{Position: failValue.Pos(), Expr: failValue}}},
					{Position: expr.PatternFilter.Pos(), Pattern: &ast.MatchWildcardPattern{Position: expr.PatternFilter.Pos()}, Body: []ast.Stmt{&ast.ExprStmt{Position: expr.PatternFilter.Pos(), Expr: wildcardValue}}},
				},
			}
			patternFilter = nil
			if s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnExprTypesMap() {
				boolType := s.g.namedType("bool")
				{
					key, value := failValue, boolType
					s.g.setResultExprType(key, value)
				}
				{
					key, value := wildcardValue, boolType
					s.g.setResultExprType(key, value)
				}
				{
					key, value := filter, boolType
					s.g.setResultExprType(key, value)
				}
			}
		} else if expr.Filter != nil {
			filter = &ast.UnaryExpr{Position: expr.Position, Op: lexer.TOKEN_NOT, Operand: expr.Filter}
		}
		body = []ast.Stmt{&ast.AssignStmt{Position: expr.Position, Target: resultIdent, Value: &ast.BoolLit{Position: expr.Position, Value: false}}}
	case ast.QueryExprCount:
		init = &ast.IntLit{Position: expr.Position, Value: "0", Suffix: "usize"}
		body = []ast.Stmt{&ast.AugAssignStmt{Position: expr.Position, Op: lexer.TOKEN_PLUSEQ, Target: resultIdent, Value: &ast.IntLit{Position: expr.Position, Value: "1", Suffix: "usize"}}}
	case ast.QueryExprSum, ast.QueryExprProduct:
		// Monoid reducer: result = unit; for each (filtered) item: result <op>= value.
		op := lexer.TOKEN_PLUSEQ
		init = &ast.IntLit{Position: expr.Position, Value: "0"}
		if expr.Kind == ast.QueryExprProduct {
			op = lexer.TOKEN_STAREQ
			init = &ast.IntLit{Position: expr.Position, Value: "1"}
		}
		value := expr.Projection
		if value == nil {
			value = &ast.Ident{Position: expr.Position, Name: expr.Name}
		}
		body = []ast.Stmt{&ast.AugAssignStmt{Position: expr.Position, Op: op, Target: resultIdent, Value: value}}
	case ast.QueryExprMin, ast.QueryExprMax:
		optionalType, ok := resultType.(*semantic.OptionalType)
		if !ok || optionalType == nil {
			return nil, nil, fmt.Errorf("min/max query expression requires an optional result type")
		}
		init = &ast.NullLit{Position: expr.Position}
		cmp := lexer.TOKEN_LT
		if expr.Kind == ast.QueryExprMax {
			cmp = lexer.TOKEN_GT
		}
		value := expr.Projection
		if value == nil {
			value = &ast.Ident{Position: expr.Position, Name: expr.Name}
		}
		// Bind the (filtered) element once, then update the optional accumulator: take it if the
		// accumulator is still null, else keep the smaller/larger by matching the some-payload.
		vName := s.g.nextSyntheticName("query.minmax.v.")
		curName := s.g.nextSyntheticName("query.minmax.cur.")
		assignV := func() ast.Stmt {
			return &ast.AssignStmt{Position: expr.Position, Target: resultIdent, Value: &ast.Ident{Position: expr.Position, Name: vName}}
		}
		matchStmt := &ast.MatchStmt{
			Position: expr.Position,
			Value:    resultIdent,
			Arms: []ast.MatchArm{
				{Position: expr.Position, Pattern: &ast.MatchLiteralPattern{Position: expr.Position, Value: &ast.NullLit{Position: expr.Position}}, Body: []ast.Stmt{assignV()}},
				{Position: expr.Position, Pattern: &ast.MatchBindPattern{Position: expr.Position, Name: curName}, Body: []ast.Stmt{
					&ast.IfStmt{Position: expr.Position,
						Cond: &ast.BinaryExpr{Position: expr.Position, Op: cmp, Left: &ast.Ident{Position: expr.Position, Name: vName}, Right: &ast.Ident{Position: expr.Position, Name: curName}},
						Then: []ast.Stmt{assignV()}},
				}},
			},
		}
		body = []ast.Stmt{
			&ast.VarDeclStmt{Position: expr.Position, Name: vName, Value: value},
			matchStmt,
		}
	case ast.QueryExprEach:
		darrayType, ok := resultType.(*semantic.DArrayType)
		if !ok || darrayType == nil {
			return nil, nil, fmt.Errorf("each query expression requires a darray result type")
		}
		init = &ast.ListLitExpr{Position: expr.Position, Owner: expr.Owner}
		if s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnExprTypesMap() {
			{
				key, value := init, darrayType
				s.g.setResultExprType(key, value)
			}
		}
		value := expr.Projection
		if value == nil {
			value = &ast.Ident{Position: expr.Position, Name: expr.Name}
		}
		pushCall := &ast.CallExpr{
			Position: expr.Position,
			Func: &ast.FieldExpr{
				Position: expr.Position,
				Object:   resultIdent,
				Field:    "push",
			},
			Args: []ast.Expr{value},
		}
		body = []ast.Stmt{&ast.ExprStmt{Position: expr.Position, Expr: pushCall}}
	case ast.QueryExprFirst:
		optionalType, ok := resultType.(*semantic.OptionalType)
		if !ok || optionalType == nil {
			return nil, nil, fmt.Errorf("first query expression requires an optional result type")
		}
		init = &ast.NullLit{Position: expr.Position}
		if s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnExprTypesMap() {
			{
				key, value := init, optionalType
				s.g.setResultExprType(key, value)
			}
		}
		nullCheck := &ast.BinaryExpr{Position: expr.Position, Op: lexer.TOKEN_EQEQ, Left: resultIdent, Right: &ast.NullLit{Position: expr.Position}}
		if expr.Filter != nil {
			filter = &ast.BinaryExpr{Position: expr.Position, Op: lexer.TOKEN_AND, Left: nullCheck, Right: expr.Filter}
		} else {
			filter = nullCheck
		}
		value := expr.Projection
		if value == nil {
			value = &ast.Ident{Position: expr.Position, Name: expr.Name}
		}
		body = []ast.Stmt{&ast.AssignStmt{Position: expr.Position, Target: resultIdent, Value: value}}
	default:
		return nil, nil, fmt.Errorf("unknown query expression kind %d", expr.Kind)
	}
	if s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnExprTypesMap() {
		{
			key, value := init, resultType
			s.g.setResultExprType(key, value)
		}
	}
	pattern := ast.MoveBindPattern(&ast.MoveBindNamePattern{Position: expr.Position, Name: expr.Name})
	if expr.Pattern != nil {
		pattern = expr.Pattern
	}
	loopStmt := ast.Stmt(&ast.IterForStmt{
		Position:             expr.Position,
		Pattern:              pattern,
		Mode:                 ast.IterBindValue,
		Source:               expr.Source,
		PatternFilter:        patternFilter,
		PatternFilterSubject: expr.PatternFilterSubject,
		Filter:               filter,
		Body:                 body,
	})
	if expr.Owner != nil {
		loopStmt = &ast.InStoreStmt{Position: expr.Position, Store: expr.Owner, Body: []ast.Stmt{loopStmt}}
	}
	block := &ast.ExprBlock{
		Position: expr.Position,
		Stmts: []ast.Stmt{
			&ast.VarDeclStmt{Position: expr.Position, Name: resultName, Mutable: true, Value: init},
			loopStmt,
		},
		Value: resultIdent,
	}
	return s.emitExprBlock(block, resultType)
}
