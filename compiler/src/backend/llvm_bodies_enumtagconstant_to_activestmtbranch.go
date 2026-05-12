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

func (s *functionState) enumTagConstant(tag uint32) (C.LLVMValueRef, error) {
	tagType, err := s.g.lowerBuiltin("u32")
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
		case *ast.PassStmt:
		case *ast.ExprStmt:
			if _, ok := s.evalConstExpr(n.Expr); !ok {
				return semantic.ConstValue{}, false, false
			}
		default:
			return semantic.ConstValue{}, false, false
		}
	}
	return semantic.ConstValue{}, false, true
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
