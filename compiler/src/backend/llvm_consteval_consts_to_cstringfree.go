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
	"strings"
	"sync"
	"unsafe"
)

var moduleCStringState struct {
	sync.Mutex
	owned  []*C.char
	byName map[string]*C.char
	users  int
}

// LLVM retains some C API names until the containing value or module is
// disposed, so freeing a name immediately after a builder call corrupts the
// generated module. Keep one deduplicated owner pool for the active generator
// lifetime and release it only after LLVMContextDispose. The reference count
// also makes concurrent generators safe: one generator cannot release another
// generator's names while its module is still alive.
func beginModuleCStringLifetime() {
	moduleCStringState.Lock()
	defer moduleCStringState.Unlock()
	if moduleCStringState.users == 0 {
		moduleCStringState.owned = nil
		moduleCStringState.byName = make(map[string]*C.char)
	}
	moduleCStringState.users++
}

func endModuleCStringLifetime() {
	moduleCStringState.Lock()
	defer moduleCStringState.Unlock()
	if moduleCStringState.users == 0 {
		return
	}
	moduleCStringState.users--
	if moduleCStringState.users != 0 {
		return
	}
	for _, ptr := range moduleCStringState.owned {
		C.free(unsafe.Pointer(ptr))
	}
	moduleCStringState.owned = nil
	moduleCStringState.byName = nil
}

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
	return evalConstExprWithLookup(expr, s.scopedConstValue, s.evalStaticFunctionCall, s.constExprType)
}

// scopedConstValue is the const lookup for an expression inside this function: a name (or the
// root of a dotted name) bound in the function's scope chain shadows every const of that name,
// and the generator's const table knows nothing of those bindings.
func (s *functionState) scopedConstValue(name string) (semantic.ConstValue, bool) {
	if !strings.HasPrefix(name, "$consteval.") {
		root := name
		if idx := strings.Index(root, "."); idx > 0 {
			root = root[:idx]
		}
		if s.nameIsRuntimeBinding(root) {
			return semantic.ConstValue{}, false
		}
	}
	return s.g.constValue(name)
}

// evalStaticFunctionCall evaluates a static call made from this function; its ARGUMENTS are
// expressions of this function, so they are evaluated in its scope.
func (s *functionState) evalStaticFunctionCall(expr *ast.CallExpr) (semantic.ConstValue, bool) {
	if expr == nil {
		return semantic.ConstValue{}, false
	}
	if ident, ok := expr.Func.(*ast.Ident); ok && s.nameIsRuntimeBinding(ident.Name) {
		return semantic.ConstValue{}, false
	}
	return s.g.evalStaticFunctionCallWith(expr, s.evalConstExpr)
}

// constExprType is a subexpression's static type for a type-dependent fold: the analyzed type, or
// for a bare const name its declared type.
func (s *functionState) constExprType(expr ast.Expr) semantic.Type {
	if t := s.exprType(expr); t != nil {
		return t
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		if n != nil {
			return s.constExprType(n.Inner)
		}
	case *ast.StringLit:
		return &semantic.RefType{Elem: &semantic.BuiltinType{Name: "u8"}}
	case *ast.Ident:
		if n == nil || s.nameIsRuntimeBinding(n.Name) {
			return nil
		}
		if sym, _, ok := s.lookupVisibleGlobalSymbol(n.Name); ok && sym != nil && sym.Kind == semantic.SymbolConst {
			return sym.Type
		}
	}
	return nil
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
	return evalConstExprWithLookup(expr, g.constValue, g.evalStaticFunctionCall, g.constExprType)
}

// constExprType is a subexpression's static type for a type-dependent fold: the analyzed type, or
// for a bare global const its declared type.
func (g *llvmGenerator) constExprType(expr ast.Expr) semantic.Type {
	if t := g.exprType(expr); t != nil {
		return t
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		if n != nil {
			return g.constExprType(n.Inner)
		}
	case *ast.StringLit:
		return &semantic.RefType{Elem: &semantic.BuiltinType{Name: "u8"}}
	case *ast.Ident:
		if n != nil && g.result != nil && g.hasGlobalScope() {
			if sym, ok := g.lookupGlobal(n.Name); ok && sym != nil && sym.Kind == semantic.SymbolConst {
				return sym.Type
			}
		}
	}
	return nil
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
