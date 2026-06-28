//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

void elisa_coreSetBranchWeights(LLVMValueRef branch, LLVMContextRef ctx, unsigned trueWeight, unsigned falseWeight);
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
)

const backendStaticEvalLoopIterationLimit = 100000

func (s *functionState) enumTagConstant(enumType *semantic.EnumType, tag uint32) (C.LLVMValueRef, error) {
	tagType, err := s.g.lowerEnumTagType(enumType)
	if err != nil {
		return nil, err
	}
	return C.LLVMConstInt(tagType, C.ulonglong(tag), 0), nil
}
func (s *functionState) emitStaticIf(stmt *ast.StaticIfStmt) error {
	branch, err := s.activeStmtBranch(stmt)
	if err != nil {
		return err
	}
	for _, inner := range branch {
		if s.currentBlockTerminated() {
			break
		}
		if err := s.emitStmt(inner); err != nil {
			return err
		}
	}
	return nil
}
func (s *functionState) activeStmtBranch(stmt *ast.StaticIfStmt) ([]ast.Stmt, error) {
	selected, ok := s.evalConstBoolExpr(stmt.Cond)
	if !ok {
		return nil, fmt.Errorf("static if condition must be a compile-time bool")
	}
	if selected {
		return stmt.Then, nil
	}
	for _, elif := range stmt.Elifs {
		selected, ok := s.evalConstBoolExpr(elif.Cond)
		if !ok {
			return nil, fmt.Errorf("static elif condition must be a compile-time bool")
		}
		if selected {
			return elif.Body, nil
		}
	}
	return stmt.Else, nil
}

func (s *functionState) emitStaticAssert(stmt *ast.StaticAssertStmt) error {
	selected, ok := s.evalConstBoolExpr(stmt.Cond)
	if !ok {
		return fmt.Errorf("static assert condition must be a compile-time bool")
	}
	if selected {
		return nil
	}
	if stmt.Message != nil {
		if value, ok := s.evalConstExpr(stmt.Message); ok && value.Kind == semantic.ConstString {
			return fmt.Errorf("static assert failed: %s", value.String)
		}
	}
	return fmt.Errorf("static assert failed")
}

func (s *functionState) emitStaticBlock(stmts []ast.Stmt) error {
	s.g.constEvalScopes = append(s.g.constEvalScopes, map[string]semantic.ConstValue{})
	_, returned, ok := s.evalStaticStmtBlock(stmts, false)
	s.g.constEvalScopes = s.g.constEvalScopes[:len(s.g.constEvalScopes)-1]
	if !ok {
		return fmt.Errorf("static block statement must evaluate at compile time")
	}
	if returned {
		return fmt.Errorf("return is not allowed in a static block")
	}
	return nil
}

func (s *functionState) evalStaticStmtBlock(stmts []ast.Stmt, allowReturn bool) (semantic.ConstValue, bool, bool) {
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.ReturnStmt:
			if !allowReturn {
				return semantic.ConstValue{}, false, false
			}
			value, ok := s.evalConstExpr(n.Value)
			return value, true, ok
		case *ast.VarDeclStmt:
			if n.Value == nil {
				return semantic.ConstValue{}, false, false
			}
			value, ok := s.evalConstExpr(n.Value)
			if !ok {
				return semantic.ConstValue{}, false, false
			}
			s.g.setConstEvalValue(n.Name, value)
		case *ast.AssignStmt:
			ident, ok := n.Target.(*ast.Ident)
			if !ok {
				return semantic.ConstValue{}, false, false
			}
			value, ok := s.evalConstExpr(n.Value)
			if !ok || !s.g.updateConstEvalValue(ident.Name, value) {
				return semantic.ConstValue{}, false, false
			}
		case *ast.StaticAssertStmt:
			if err := s.emitStaticAssert(n); err != nil {
				return semantic.ConstValue{}, false, false
			}
		case *ast.StaticAssertBlockStmt:
			for _, item := range n.Assertions {
				if err := s.emitStaticAssert(&ast.StaticAssertStmt{Position: item.Position, Cond: item.Cond, Message: item.Message}); err != nil {
					return semantic.ConstValue{}, false, false
				}
			}
		case *ast.StaticErrorStmt:
			return semantic.ConstValue{}, false, false
		case *ast.StaticIfStmt:
			branch, err := s.activeStmtBranch(n)
			if err != nil {
				return semantic.ConstValue{}, false, false
			}
			value, returned, ok := s.evalStaticStmtBlock(branch, allowReturn)
			if !ok || returned {
				return value, returned, ok
			}
		case *ast.StaticBlockStmt:
			s.g.constEvalScopes = append(s.g.constEvalScopes, map[string]semantic.ConstValue{})
			value, returned, ok := s.evalStaticStmtBlock(n.Body, allowReturn)
			s.g.constEvalScopes = s.g.constEvalScopes[:len(s.g.constEvalScopes)-1]
			if !ok || returned {
				return value, returned, ok
			}
		case *ast.IfStmt:
			cond, ok := s.evalConstExpr(n.Cond)
			if !ok || cond.Kind != semantic.ConstBool {
				return semantic.ConstValue{}, false, false
			}
			branch := n.Else
			if cond.Bool {
				branch = n.Then
			} else {
				for _, elif := range n.Elifs {
					elifCond, ok := s.evalConstExpr(elif.Cond)
					if !ok || elifCond.Kind != semantic.ConstBool {
						return semantic.ConstValue{}, false, false
					}
					if elifCond.Bool {
						branch = elif.Body
						break
					}
				}
			}
			value, returned, ok := s.evalStaticStmtBlock(branch, allowReturn)
			if !ok || returned {
				return value, returned, ok
			}
		case *ast.WhileStmt:
			for i := 0; i < backendStaticEvalLoopIterationLimit; i++ {
				cond, ok := s.evalConstExpr(n.Cond)
				if !ok || cond.Kind != semantic.ConstBool {
					return semantic.ConstValue{}, false, false
				}
				if !cond.Bool {
					break
				}
				value, returned, ok := s.evalStaticStmtBlock(n.Body, allowReturn)
				if !ok || returned {
					return value, returned, ok
				}
				if i == backendStaticEvalLoopIterationLimit-1 {
					return semantic.ConstValue{}, false, false
				}
			}
		case *ast.ForStmt:
			value, returned, ok := s.evalStaticForStmt(n, allowReturn)
			if !ok || returned {
				return value, returned, ok
			}
		case *ast.IterForStmt:
			value, returned, ok := s.evalStaticIterForStmt(n, allowReturn)
			if !ok || returned {
				return value, returned, ok
			}
		case *ast.MatchStmt:
			value, returned, ok := s.evalStaticMatchStmt(n, allowReturn)
			if !ok || returned {
				return value, returned, ok
			}
		case *ast.PassStmt:
		case *ast.ExprStmt:
			if ok := s.evalStaticExprStmt(n.Expr); !ok {
				return semantic.ConstValue{}, false, false
			}
		default:
			return semantic.ConstValue{}, false, false
		}
	}
	return semantic.ConstValue{}, false, true
}

func (s *functionState) evalStaticExprStmt(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	if s.evalStaticDArrayPushExpr(expr) {
		return true
	}
	if s.evalStaticDArrayExtendExpr(expr) {
		return true
	}
	_, ok := s.evalConstExpr(expr)
	return ok
}

func (s *functionState) evalStaticDArrayPushExpr(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || call == nil {
		return false
	}
	fieldExpr, ok := call.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "push" {
		return false
	}
	ident, ok := fieldExpr.Object.(*ast.Ident)
	if !ok || ident == nil || len(call.Args) != 1 || call.NamedArgCount() != 0 {
		return false
	}
	current, scopeIndex, ok := s.g.constEvalValueScope(ident.Name)
	if !ok || current.Kind != semantic.ConstList {
		return false
	}
	value, ok := s.evalConstExpr(call.Args[0])
	if !ok {
		return false
	}
	updated := cloneBackendConstValue(current)
	updated.Elems = append(updated.Elems, cloneBackendConstValue(value))
	s.g.constEvalScopes[scopeIndex][ident.Name] = updated
	return true
}

func (s *functionState) evalStaticDArrayExtendExpr(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || call == nil {
		return false
	}
	fieldExpr, ok := call.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "extend" {
		return false
	}
	ident, ok := fieldExpr.Object.(*ast.Ident)
	if !ok || ident == nil || len(call.Args) != 1 || call.NamedArgCount() != 0 {
		return false
	}
	current, scopeIndex, ok := s.g.constEvalValueScope(ident.Name)
	if !ok || current.Kind != semantic.ConstList {
		return false
	}
	source, ok := s.evalConstExpr(call.Args[0])
	if !ok || source.Kind != semantic.ConstList {
		return false
	}
	updated := cloneBackendConstValue(current)
	for _, elem := range source.Elems {
		updated.Elems = append(updated.Elems, cloneBackendConstValue(elem))
	}
	s.g.constEvalScopes[scopeIndex][ident.Name] = updated
	return true
}

func (s *functionState) evalStaticMatchStmt(stmt *ast.MatchStmt, allowReturn bool) (semantic.ConstValue, bool, bool) {
	if stmt.Store != nil {
		return semantic.ConstValue{}, false, false
	}
	value, ok := s.evalConstExpr(stmt.Value)
	if !ok {
		return semantic.ConstValue{}, false, false
	}
	for _, arm := range stmt.Arms {
		matched, bindings, ok := s.evalStaticMatchPattern(arm.Pattern, value)
		if !ok {
			return semantic.ConstValue{}, false, false
		}
		if !matched {
			continue
		}
		scope := map[string]semantic.ConstValue{}
		for name, bindingValue := range bindings {
			scope[name] = bindingValue
		}
		s.g.constEvalScopes = append(s.g.constEvalScopes, scope)
		result, returned, ok := s.evalStaticStmtBlock(arm.Body, allowReturn)
		s.g.constEvalScopes = s.g.constEvalScopes[:len(s.g.constEvalScopes)-1]
		return result, returned, ok
	}
	return semantic.ConstValue{}, false, true
}

func (s *functionState) evalStaticMatchPattern(pattern ast.MatchPattern, value semantic.ConstValue) (bool, map[string]semantic.ConstValue, bool) {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return true, nil, true
	case *ast.MatchBindPattern:
		if p.Name == "" || p.Name == "_" {
			return true, nil, true
		}
		return true, map[string]semantic.ConstValue{p.Name: value}, true
	case *ast.MatchLiteralPattern:
		patternValue, ok := s.evalConstExpr(p.Value)
		if !ok {
			return false, nil, false
		}
		equal, ok := evalConstEquality(value, patternValue, true)
		if !ok || equal.Kind != semantic.ConstBool {
			return false, nil, false
		}
		return equal.Bool, nil, true
	case *ast.MatchStringLiteralPattern:
		if value.Kind != semantic.ConstString {
			return false, nil, true
		}
		return value.String == p.Value, nil, true
	case *ast.MatchTuplePattern:
		if value.Kind != semantic.ConstTuple && value.Kind != semantic.ConstList {
			return false, nil, true
		}
		return s.evalStaticSequenceMatchPatterns(p.Elems, value.Elems)
	case *ast.MatchListPattern:
		if value.Kind != semantic.ConstList {
			return false, nil, true
		}
		return s.evalStaticSequenceMatchPatterns(p.Elems, value.Elems)
	case *ast.MatchStructPattern:
		if value.Kind != semantic.ConstRecord {
			return false, nil, true
		}
		return s.evalStaticStructMatchPatterns(matchStructPatternArgs(p), value)
	case *ast.MatchVariantPattern:
		if len(p.Args) != 0 {
			return false, nil, false
		}
		patternValue, ok := s.g.constValue(p.EnumName + "." + p.Variant)
		if !ok {
			return false, nil, false
		}
		equal, ok := evalConstEquality(value, patternValue, true)
		if !ok || equal.Kind != semantic.ConstBool {
			return false, nil, false
		}
		return equal.Bool, nil, true
	case *ast.MatchOrPattern:
		for _, option := range p.Options {
			matched, bindings, ok := s.evalStaticMatchPattern(option, value)
			if !ok {
				return false, nil, false
			}
			if matched {
				return true, bindings, true
			}
		}
		return false, nil, true
	default:
		return false, nil, false
	}
}

func matchStructPatternArgs(pattern *ast.MatchStructPattern) []*ast.MatchPatternArg {
	if pattern == nil {
		return nil
	}
	if len(pattern.ResolvedArgs) != 0 {
		return pattern.ResolvedArgs
	}
	args := make([]*ast.MatchPatternArg, 0, len(pattern.Args))
	for i := range pattern.Args {
		arg := pattern.Args[i]
		args = append(args, &ast.MatchPatternArg{Position: arg.Position, Name: arg.Name, Pattern: arg.Pattern})
	}
	return args
}

func (s *functionState) evalStaticSequenceMatchPatterns(patterns []ast.MatchPattern, elems []semantic.ConstValue) (bool, map[string]semantic.ConstValue, bool) {
	if len(patterns) == 0 {
		return len(elems) == 0, nil, true
	}
	if _, ok := patterns[len(patterns)-1].(*ast.MatchRestPattern); ok {
		if len(elems) < len(patterns)-1 {
			return false, nil, true
		}
	} else if len(elems) != len(patterns) {
		return false, nil, true
	}
	bindings := map[string]semantic.ConstValue{}
	limit := len(patterns)
	if _, ok := patterns[len(patterns)-1].(*ast.MatchRestPattern); ok {
		limit--
	}
	for i := 0; i < limit; i++ {
		matched, itemBindings, ok := s.evalStaticMatchPattern(patterns[i], elems[i])
		if !ok {
			return false, nil, false
		}
		if !matched {
			return false, nil, true
		}
		for name, value := range itemBindings {
			bindings[name] = value
		}
	}
	return true, bindings, true
}

func (s *functionState) evalStaticStructMatchPatterns(args []*ast.MatchPatternArg, value semantic.ConstValue) (bool, map[string]semantic.ConstValue, bool) {
	bindings := map[string]semantic.ConstValue{}
	for _, arg := range args {
		if arg == nil || arg.Name == "" {
			continue
		}
		fieldValue, ok := semantic.ConstReflectionRecordField(value, arg.Name)
		if !ok {
			return false, nil, true
		}
		if arg.Pattern == nil {
			continue
		}
		matched, itemBindings, ok := s.evalStaticMatchPattern(arg.Pattern, fieldValue)
		if !ok {
			return false, nil, false
		}
		if !matched {
			return false, nil, true
		}
		for name, value := range itemBindings {
			bindings[name] = value
		}
	}
	return true, bindings, true
}

func (s *functionState) evalStaticForStmt(stmt *ast.ForStmt, allowReturn bool) (semantic.ConstValue, bool, bool) {
	start, ok := s.evalConstExpr(stmt.Start)
	if !ok || start.Kind != semantic.ConstInt {
		return semantic.ConstValue{}, false, false
	}
	end, ok := s.evalConstExpr(stmt.End)
	if !ok || end.Kind != semantic.ConstInt {
		return semantic.ConstValue{}, false, false
	}
	step := int64(1)
	if stmt.Step != nil {
		stepValue, ok := s.evalConstExpr(stmt.Step)
		if !ok || stepValue.Kind != semantic.ConstInt {
			return semantic.ConstValue{}, false, false
		}
		step = stepValue.Int
		if step < 0 {
			step = -step
		}
	}
	if step == 0 {
		return semantic.ConstValue{}, false, false
	}
	ascending := start.Int <= end.Int
	if stmt.Op == lexer.TOKEN_RANGE_LT {
		ascending = true
	} else if stmt.Op == lexer.TOKEN_RANGE_GT {
		ascending = false
	}
	current := start.Int
	for i := 0; i < backendStaticEvalLoopIterationLimit; i++ {
		if !backendStaticForLoopContinue(stmt.Op, current, end.Int, ascending) {
			return semantic.ConstValue{}, false, true
		}
		s.g.constEvalScopes = append(s.g.constEvalScopes, map[string]semantic.ConstValue{stmt.Name: semantic.ConstValue{Kind: semantic.ConstInt, Int: current}})
		value, returned, ok := s.evalStaticStmtBlock(stmt.Body, allowReturn)
		s.g.constEvalScopes = s.g.constEvalScopes[:len(s.g.constEvalScopes)-1]
		if !ok || returned {
			return value, returned, ok
		}
		if ascending {
			current += step
		} else {
			current -= step
		}
	}
	return semantic.ConstValue{}, false, false
}

func (s *functionState) evalStaticIterForStmt(stmt *ast.IterForStmt, allowReturn bool) (semantic.ConstValue, bool, bool) {
	if stmt == nil || stmt.Mode != ast.IterBindValue {
		return semantic.ConstValue{}, false, false
	}
	source, ok := s.evalConstExpr(stmt.Source)
	if !ok || (source.Kind != semantic.ConstList && source.Kind != semantic.ConstTuple) {
		return semantic.ConstValue{}, false, false
	}
	pattern := stmt.Pattern
	if pattern == nil {
		pattern = &ast.MoveBindNamePattern{Position: stmt.Pos(), Name: "_"}
	}
	elems := source.Elems
	for i := 0; i < len(elems); i++ {
		item := elems[i]
		if stmt.Reverse {
			item = elems[len(elems)-1-i]
		}
		scope, ok := bindBackendConstMovePattern(pattern, item)
		if !ok {
			return semantic.ConstValue{}, false, false
		}
		s.g.constEvalScopes = append(s.g.constEvalScopes, scope)
		if stmt.PatternFilter != nil {
			matched, bindings, ok := s.evalStaticIterPatternFilter(stmt)
			if !ok {
				s.g.constEvalScopes = s.g.constEvalScopes[:len(s.g.constEvalScopes)-1]
				return semantic.ConstValue{}, false, false
			}
			if !matched {
				s.g.constEvalScopes = s.g.constEvalScopes[:len(s.g.constEvalScopes)-1]
				continue
			}
			for name, value := range bindings {
				scope[name] = value
			}
			s.g.constEvalScopes[len(s.g.constEvalScopes)-1] = scope
		}
		include := true
		if stmt.WhereFilter != nil {
			filter, ok := s.evalConstExpr(stmt.WhereFilter)
			if !ok || filter.Kind != semantic.ConstBool {
				s.g.constEvalScopes = s.g.constEvalScopes[:len(s.g.constEvalScopes)-1]
				return semantic.ConstValue{}, false, false
			}
			include = include && filter.Bool
		}
		if stmt.Filter != nil {
			filter, ok := s.evalConstExpr(stmt.Filter)
			if !ok || filter.Kind != semantic.ConstBool {
				s.g.constEvalScopes = s.g.constEvalScopes[:len(s.g.constEvalScopes)-1]
				return semantic.ConstValue{}, false, false
			}
			include = include && filter.Bool
		}
		if include {
			value, returned, ok := s.evalStaticStmtBlock(stmt.Body, allowReturn)
			if !ok || returned {
				s.g.constEvalScopes = s.g.constEvalScopes[:len(s.g.constEvalScopes)-1]
				return value, returned, ok
			}
		}
		s.g.constEvalScopes = s.g.constEvalScopes[:len(s.g.constEvalScopes)-1]
	}
	return semantic.ConstValue{}, false, true
}

func (s *functionState) evalStaticIterPatternFilter(stmt *ast.IterForStmt) (bool, map[string]semantic.ConstValue, bool) {
	if stmt == nil || stmt.PatternFilter == nil {
		return true, nil, true
	}
	subjectName := stmt.PatternFilterSubject
	if subjectName == "" {
		if bind, ok := stmt.Pattern.(*ast.MoveBindNamePattern); ok && bind != nil && bind.Name != "" && bind.Name != "_" {
			subjectName = bind.Name
		}
	}
	subject, _, ok := s.constEvalValueScope(subjectName)
	if !ok {
		if subjectName == "" {
			return false, nil, true
		}
		return false, nil, false
	}
	matched, bindings, ok := s.evalStaticMatchPattern(stmt.PatternFilter, subject)
	if !ok {
		return false, nil, false
	}
	if !matched {
		return false, nil, true
	}
	return true, bindings, true
}

func backendStaticForLoopContinue(op lexer.TokenKind, current int64, end int64, ascending bool) bool {
	switch op {
	case lexer.TOKEN_RANGE:
		if ascending {
			return current <= end
		}
		return current >= end
	case lexer.TOKEN_RANGE_LT:
		return current < end
	case lexer.TOKEN_RANGE_GT:
		return current > end
	default:
		return false
	}
}

func (g *llvmGenerator) checkStaticAssertDecl(decl *ast.StaticAssertDecl) error {
	state := &functionState{g: g}
	return state.emitStaticAssert(&ast.StaticAssertStmt{
		Position: decl.Position,
		Cond:     decl.Cond,
		Message:  decl.Message,
	})
}

func (g *llvmGenerator) checkStaticAssertBlockDecl(decl *ast.StaticAssertBlockDecl) error {
	state := &functionState{g: g}
	for _, item := range decl.Assertions {
		if err := state.emitStaticAssert(&ast.StaticAssertStmt{Position: item.Position, Cond: item.Cond, Message: item.Message}); err != nil {
			return err
		}
	}
	return nil
}
