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
	"elisacore/src/semantic"
	"fmt"
)

func unwrapPackedStoreOriginExpr(expr ast.Expr) ast.Expr {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return unwrapPackedStoreOriginExpr(n.Inner)
	case *ast.CastExpr:
		return unwrapPackedStoreOriginExpr(n.Operand)
	case *ast.MoveExpr:
		return unwrapPackedStoreOriginExpr(n.Operand)
	case *ast.CanExpr:
		return unwrapPackedStoreOriginExpr(n.Expr)
	default:
		return expr
	}
}
func (s *functionState) bindPackedStoreOriginsForExprPath(path string, expr ast.Expr, typ semantic.Type) error {
	if path == "" || expr == nil || typ == nil {
		return nil
	}
	stripped := semantic.StripAggregateStateType(typ)
	if enumType, ok := stripped.(*semantic.EnumType); ok {
		if !enumType.Packed {
			return nil
		}
		origin, ok, err := s.resolvePackedNodeStoreBinding(expr, enumType)
		if err != nil {
			return err
		}
		if ok {
			s.bindPackedEnumStoreOrigin(path, enumType, origin)
		}
		return nil
	}
	switch stripped.(type) {
	case *semantic.StructType, *semantic.GenericInstanceType:
	default:
		return nil
	}
	fields, err := s.g.structLiteralFields(stripped)
	if err != nil {
		return err
	}
	sourceExpr := unwrapPackedStoreOriginExpr(expr)
	if lit, ok := sourceExpr.(*ast.StructLitExpr); ok {
		args := lit.LoweredArgs()
		limit := len(fields)
		if len(args) < limit {
			limit = len(args)
		}
		for i := 0; i < limit; i++ {
			if args[i] == nil {
				continue
			}
			childPath := path + "." + fields[i].Decl.Name
			if err := s.bindPackedStoreOriginsForExprPath(childPath, args[i], fields[i].Type); err != nil {
				return err
			}
		}
		return nil
	}
	for _, field := range fields {
		childPath := path + "." + field.Decl.Name
		childExpr := &ast.FieldExpr{Position: expr.Pos(), Object: expr, Field: field.Decl.Name}
		if err := s.bindPackedStoreOriginsForExprPath(childPath, childExpr, field.Type); err != nil {
			return err
		}
	}
	return nil
}
func (s *functionState) emitInStore(stmt *ast.InStoreStmt) error {
	savedTreeOwner := s.treeAllocOwner
	if owner, ok, err := s.classifyTreeAllocOwnerExpr(stmt.Store); err != nil {
		return err
	} else if ok {
		s.treeAllocOwner = owner
		if owner.storeAnchorBlock == nil {
			s.treeAllocOwner.storeAnchorBlock, s.treeAllocOwner.storeAnchorInstr = s.captureStoreAnchor()
		}
		defer func() {
			s.treeAllocOwner = savedTreeOwner
		}()
		return s.emitBlock(stmt.Body, true)
	}
	storeValue, actualType, err := s.emitExpr(stmt.Store, nil)
	if err != nil {
		return err
	}
	savedStores := s.packedStores
	storeType, ok := actualType.(*semantic.PackedEnumStoreType)
	if !ok {
		return fmt.Errorf("in-block requires a packed enum store, perm, an Arena value, or an Arena reference, got %s", actualType.String())
	}
	s.packedStores = s.clonePackedStores()
	if s.packedStores == nil {
		s.packedStores = map[string]packedStoreBinding{}
	}
	s.packedStores[storeType.Enum.Name] = packedStoreBinding{value: storeValue, typ: storeType}
	defer func() {
		s.treeAllocOwner = savedTreeOwner
		s.packedStores = savedStores
	}()
	return s.emitBlock(stmt.Body, true)
}
func (s *functionState) emitPoolStmt(stmt *ast.PoolStmt) error {
	poolType := s.g.namedType("ThreadPool")
	if stmt.BorrowedPool != "" {
		binding, local := s.lookupBinding(stmt.BorrowedPool)
		poolPtr := C.LLVMValueRef(nil)
		boundType := semantic.Type(nil)
		if local {
			poolPtr, boundType = binding.ptr, binding.typ
		} else {
			var err error
			poolPtr, boundType, err = s.emitAddress(&ast.Ident{Position: stmt.Position, Name: stmt.BorrowedPool})
			if err != nil {
				return fmt.Errorf("missing borrowed ThreadPool binding %q: %w", stmt.BorrowedPool, err)
			}
		}
		if ref, isRef := boundType.(*semantic.RefType); isRef {
			if ref == nil || !ref.Mutable || !semantic.SameType(ref.Elem, poolType) {
				return fmt.Errorf("borrowed nursery binding %q is not a mutable ThreadPool reference", stmt.BorrowedPool)
			}
			var err error
			poolPtr, err = s.loadValue(poolPtr, boundType, stmt.BorrowedPool+".ref")
			if err != nil {
				return err
			}
		} else if (local && !binding.mutable) || !semantic.SameType(boundType, poolType) {
			return fmt.Errorf("borrowed nursery binding %q is not a mutable ThreadPool value", stmt.BorrowedPool)
		}
		s.pushScope()
		defer s.popScope()
		s.defineBinding(stmt.Name, valueBinding{ptr: poolPtr, typ: poolType, mutable: true})
		s.poolScopes = append(s.poolScopes, activePoolBinding{name: stmt.Name, ptr: poolPtr, typ: poolType})
		defer func() { s.poolScopes = s.poolScopes[:len(s.poolScopes)-1] }()
		return s.emitPoolBodyInCurrentScope(stmt)
	}
	usizeType := s.g.namedType("usize")
	workersValue, _, err := s.emitExpr(stmt.Workers, usizeType)
	if err != nil {
		return err
	}
	poolNewType := &semantic.FuncType{Name: "pool_new", Params: []semantic.Type{usizeType}, Return: poolType}
	poolNew, err := s.g.ensureFunctionDeclared("pool_new", poolNewType)
	if err != nil {
		return err
	}
	poolNewLLVMType, err := s.g.lowerFunctionType(poolNewType)
	if err != nil {
		return err
	}
	poolValue := s.buildCall(poolNewLLVMType, poolNew, []C.LLVMValueRef{workersValue}, "pool.new")
	poolAlloca, err := s.createEntryAlloca(stmt.Name, poolType)
	if err != nil {
		return err
	}
	s.pushScope()
	defer s.popScope()
	s.defineBinding(stmt.Name, valueBinding{ptr: poolAlloca, typ: poolType, mutable: true})
	C.LLVMBuildStore(s.builder, poolValue, poolAlloca)
	pool := scopedCleanupBinding{kind: scopedCleanupThreadPool, name: stmt.Name, ptr: poolAlloca, typ: poolType}
	s.registerScopedCleanup(pool)
	s.poolScopes = append(s.poolScopes, activePoolBinding{name: stmt.Name, ptr: poolAlloca, typ: poolType, workers: workersValue})
	defer func() {
		s.poolScopes = s.poolScopes[:len(s.poolScopes)-1]
	}()
	return s.emitPoolBodyInCurrentScope(stmt)
}

// emitPoolBodyInCurrentScope gives parser-generated nursery joins the same
// all-exit cleanup semantics as other scope-owned resources. The final wait in
// the desugared body is consumed here: the registered cleanup performs the one
// join on normal exit, return, break, or continue.
func (s *functionState) emitPoolBodyInCurrentScope(stmt *ast.PoolStmt) error {
	if stmt == nil || !stmt.Nursery {
		if stmt == nil {
			return nil
		}
		return s.emitBlockInCurrentScope(stmt.Body)
	}
	if len(stmt.Body) < 2 {
		return fmt.Errorf("malformed nursery body: missing generated task group or wait")
	}
	groupDecl, ok := stmt.Body[0].(*ast.VarDeclStmt)
	if !ok || groupDecl == nil || groupDecl.Name == "" {
		return fmt.Errorf("malformed nursery body: missing generated task group")
	}
	newCall, ok := groupDecl.Value.(*ast.CallExpr)
	if !ok || newCall == nil || callIdentName(newCall) != "task_group_new" || len(newCall.Args) != 0 {
		return fmt.Errorf("malformed nursery body: invalid generated task group initializer")
	}
	waitStmt, ok := stmt.Body[len(stmt.Body)-1].(*ast.ExprStmt)
	if !ok || waitStmt == nil {
		return fmt.Errorf("malformed nursery body: missing generated task group wait")
	}
	waitCall, ok := waitStmt.Expr.(*ast.CallExpr)
	if !ok || waitCall == nil || callIdentName(waitCall) != "task_group_wait_all" || len(waitCall.Args) != 1 {
		return fmt.Errorf("malformed nursery body: invalid generated task group wait")
	}
	if err := s.emitStmt(groupDecl); err != nil {
		return err
	}
	groupBinding, found := s.lookupBinding(groupDecl.Name)
	if !found || !semantic.SameType(groupBinding.typ, s.g.namedType("TaskGroup")) {
		return fmt.Errorf("generated nursery task group %q is unavailable after initialization", groupDecl.Name)
	}
	s.registerScopedCleanup(scopedCleanupBinding{
		kind: scopedCleanupTaskGroupWait,
		name: groupDecl.Name,
		ptr:  groupBinding.ptr,
		typ:  groupBinding.typ,
	})
	return s.emitBlockInCurrentScope(stmt.Body[1 : len(stmt.Body)-1])
}
