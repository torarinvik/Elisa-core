//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

void llctxSetBranchWeights(LLVMValueRef branch, LLVMContextRef ctx, unsigned trueWeight, unsigned falseWeight);
*/
import "C"

import (
	"fmt"
	"llcontext/src/ast"
	"llcontext/src/semantic"
	"sort"
)

func (s *functionState) packedEnumVariantPayloadFieldByteOffset(enumType *semantic.EnumType, variant *semantic.EnumVariant, payloadIndex int) (uint64, bool, error) {
	if enumType == nil || !enumType.Packed || variant == nil || payloadIndex < 0 || payloadIndex >= len(variant.Payload) {
		return 0, false, nil
	}
	payloadFieldIndex, err := s.g.packedEnumPayloadFieldIndex(enumType)
	if err != nil {
		return 0, false, err
	}
	rowType, err := s.g.ensurePackedEnumStorageType(enumType)
	if err != nil {
		return 0, false, err
	}
	baseOffsetBytes, err := s.g.abiOffsetOfLLVMElement(rowType, payloadFieldIndex)
	if err != nil {
		return 0, false, err
	}
	if len(variant.Payload) > 1 {
		payloadType, err := s.g.lowerEnumVariantPayloadType(variant)
		if err != nil {
			return 0, false, err
		}
		fieldOffsetBytes, err := s.g.abiOffsetOfLLVMElement(payloadType, payloadIndex)
		if err != nil {
			return 0, false, err
		}
		return baseOffsetBytes + fieldOffsetBytes, true, nil
	}
	return baseOffsetBytes, true, nil
}
func (s *functionState) readPackedEnumTagWithStore(handleValue C.LLVMValueRef, enumType *semantic.EnumType, store *packedStoreBinding) (C.LLVMValueRef, error) {
	ops, ok := s.packedStoreOpsFromBinding(store)
	if !ok {
		return nil, fmt.Errorf("packed enum %s tag read requires store context", enumType.Name)
	}
	return ops.storeTagAt(handleValue, enumType, "packed.tag.store")
}
func packedMatchNeedsEagerDecode(arms []ast.MatchArm) bool {
	for _, arm := range arms {
		if matchPatternNeedsPayloadDecode(arm.Pattern) {
			return true
		}
	}
	return false
}
func packedMatchHasWidePayloadAccess(arms []ast.MatchArm, minArgs int) bool {
	if minArgs <= 0 {
		return false
	}
	for _, arm := range arms {
		pattern, ok := arm.Pattern.(*ast.MatchVariantPattern)
		if !ok {
			continue
		}
		if len(pattern.Args) >= minArgs {
			return true
		}
	}
	return false
}
func (s *functionState) preloadPackedMatchCommonFieldValues(enumType *semantic.EnumType, matchValue ast.Expr, enumValue C.LLVMValueRef, decodedMatchValue C.LLVMValueRef, store *packedStoreBinding, arms []ast.MatchArm) (packedPayloadValueCache, error) {
	var values packedPayloadValueCache
	if s == nil || enumType == nil || !enumType.Packed || len(enumType.Common) == 0 || !packedEnumMatchCanUseTagSwitch(enumType, arms) {
		return values, nil
	}
	ident, ok := matchValue.(*ast.Ident)
	if !ok || ident == nil || ident.Name == "" || !matchArmsReadMatchedValueField(ident.Name, arms) {
		return values, nil
	}
	origin := packedReadOriginKey{}
	if resolvedOrigin, ok, err := s.packedReadOriginKey(matchValue); err != nil {
		return values, err
	} else if ok {
		origin = resolvedOrigin
	}
	var ops *packedStoreOps
	if store != nil {
		if resolvedOps, ok := s.packedStoreOpsFromBinding(store); ok {
			ops = resolvedOps
		}
	}
	fieldNames := make([]string, 0, len(enumType.Common))
	for fieldName := range enumType.Common {
		fieldNames = append(fieldNames, fieldName)
	}
	sort.Strings(fieldNames)
	for _, fieldName := range fieldNames {
		layout, err := s.g.packedEnumCommonFieldLayout(enumType, fieldName)
		if err != nil {
			return values, err
		}
		fieldType := layout.Field.Type
		cacheName := "packed.match.common.preload." + fieldName
		var fieldValue C.LLVMValueRef
		switch {
		case !layout.StoredInline:
			if store == nil {
				continue
			}
			fieldValue, err = s.emitPackedSideTableFieldRead(enumValue, enumType, store, fieldType, layout.SideWordOffset, layout.WordCount, origin, cacheName)
			if err != nil {
				return values, err
			}
		case decodedMatchValue != nil:
			containerType, err := s.loweredEnumStorageType(enumType)
			if err != nil {
				return values, err
			}
			fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerType, decodedMatchValue, C.unsigned(layout.RowFieldIndex), cStringFree(cacheName+".ptr"))
			fieldValue, err = s.loadValue(fieldPtr, fieldType, cacheName)
			if err != nil {
				return values, err
			}
		case ops != nil && ops.canDirectWordRead():
			fieldOffsetBytes, ok, err := s.packedEnumDirectFieldByteOffset(enumType, layout.RowFieldIndex)
			if err != nil {
				return values, err
			}
			if !ok {
				continue
			}
			fieldValue, err = s.emitPackedDirectFieldReadAtOrigin(ops, enumValue, enumType, fieldType, fieldOffsetBytes, origin, cacheName)
			if err != nil {
				return values, err
			}
		default:
			continue
		}
		if fieldValue != nil {
			values.add(fieldName, fieldValue)
		}
	}
	return values, nil
}
func packedMatchShouldEagerDecode(result *semantic.Result, abi packedEnumABIMode, enumType *semantic.EnumType, matchValue ast.Expr, store *packedStoreBinding, arms []ast.MatchArm) bool {
	needsPayloadDecode := packedMatchNeedsEagerDecode(arms)
	ident, ok := matchValue.(*ast.Ident)
	readsMatchedValueField := ok && matchArmsReadMatchedValueField(ident.Name, arms)
	if !needsPayloadDecode && !readsMatchedValueField {
		return false
	}
	if store != nil && store.typ != nil && semantic.IsFrozenPackedEnumStoreType(store.typ) && packedModeUsesDirectWordReads(abi) {
		return false
	}
	hasFrozenPackedStoreDeps := false
	if result != nil && result.ExprHasOnlyFrozenPackedStoreDeps(matchValue) {
		hasFrozenPackedStoreDeps = true
	}
	if !hasFrozenPackedStoreDeps && store != nil && store.typ != nil && semantic.IsFrozenPackedEnumStoreType(store.typ) {
		hasFrozenPackedStoreDeps = true
	}
	if hasFrozenPackedStoreDeps {
		if packedModeUsesDenseIndexHandle(abi) {
			return false
		}
		return true
	}
	if !needsPayloadDecode {
		return false
	}
	if !ok {
		return false
	}
	return readsMatchedValueField
}
func matchArmsReadMatchedValueField(name string, arms []ast.MatchArm) bool {
	for _, arm := range arms {
		if stmtsReadMatchedValueField(name, arm.Body) {
			return true
		}
	}
	return false
}
func stmtsReadMatchedValueField(name string, stmts []ast.Stmt) bool {
	for _, stmt := range stmts {
		if stmtReadsMatchedValueField(name, stmt) {
			return true
		}
	}
	return false
}
func stmtReadsMatchedValueField(name string, stmt ast.Stmt) bool {
	switch n := stmt.(type) {
	case *ast.AssignStmt:
		return exprReadsMatchedValueField(name, n.Target) || exprReadsMatchedValueField(name, n.Value)
	case *ast.AugAssignStmt:
		return exprReadsMatchedValueField(name, n.Target) || exprReadsMatchedValueField(name, n.Value)
	case *ast.AsRefAssignStmt:
		return exprReadsMatchedValueField(name, n.Target) || exprReadsMatchedValueField(name, n.Value)
	case *ast.VarDeclStmt:
		return exprReadsMatchedValueField(name, n.Value)
	case *ast.MoveBindStmt:
		return exprReadsMatchedValueField(name, n.Value) || exprReadsMatchedValueField(name, n.Store)
	case *ast.DeferStmt:
		return stmtsReadMatchedValueField(name, n.Body)
	case *ast.ReturnStmt:
		return exprReadsMatchedValueField(name, n.Value)
	case *ast.IfStmt:
		return exprReadsMatchedValueField(name, n.Cond) || stmtsReadMatchedValueField(name, n.Then) || stmtsReadMatchedValueField(name, n.Else) || elifsReadMatchedValueField(name, n.Elifs)
	case *ast.WhileStmt:
		return exprReadsMatchedValueField(name, n.Cond) || stmtsReadMatchedValueField(name, n.Body)
	case *ast.ForStmt:
		return exprReadsMatchedValueField(name, n.Start) || exprReadsMatchedValueField(name, n.End) || exprReadsMatchedValueField(name, n.Step) || stmtsReadMatchedValueField(name, n.Body)
	case *ast.IterForStmt:
		return exprReadsMatchedValueField(name, n.Source) || stmtsReadMatchedValueField(name, n.Body)
	case *ast.ParallelForStmt:
		return exprReadsMatchedValueField(name, n.Source) || stmtsReadMatchedValueField(name, n.Body)
	case *ast.MatchStmt:
		return exprReadsMatchedValueField(name, n.Value) || exprReadsMatchedValueField(name, n.Store) || matchArmsReadMatchedValueField(name, n.Arms)
	case *ast.InStoreStmt:
		return exprReadsMatchedValueField(name, n.Store) || stmtsReadMatchedValueField(name, n.Body)
	case *ast.PanicStmt:
		return exprReadsMatchedValueField(name, n.Message)
	case *ast.ExprStmt:
		return exprReadsMatchedValueField(name, n.Expr)
	case *ast.StaticIfStmt:
		return exprReadsMatchedValueField(name, n.Cond) || stmtsReadMatchedValueField(name, n.Then) || stmtsReadMatchedValueField(name, n.Else) || staticElifsReadMatchedValueField(name, n.Elifs)
	case *ast.StaticErrorStmt:
		return exprReadsMatchedValueField(name, n.Message)
	case *ast.DiscardStmt:
		return exprReadsMatchedValueField(name, n.Value)
	default:
		return false
	}
}
func elifsReadMatchedValueField(name string, elifs []ast.ElifClause) bool {
	for _, elif := range elifs {
		if exprReadsMatchedValueField(name, elif.Cond) || stmtsReadMatchedValueField(name, elif.Body) {
			return true
		}
	}
	return false
}
func staticElifsReadMatchedValueField(name string, elifs []ast.StaticElifClause) bool {
	for _, elif := range elifs {
		if exprReadsMatchedValueField(name, elif.Cond) || stmtsReadMatchedValueField(name, elif.Body) {
			return true
		}
	}
	return false
}
func exprReadsMatchedValueField(name string, expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	switch n := expr.(type) {
	case *ast.BinaryExpr:
		return exprReadsMatchedValueField(name, n.Left) || exprReadsMatchedValueField(name, n.Right)
	case *ast.UnaryExpr:
		return exprReadsMatchedValueField(name, n.Operand)
	case *ast.CallExpr:
		if exprReadsMatchedValueField(name, n.Func) {
			return true
		}
		if exprReadsMatchedValueField(name, n.SafeReceiver) {
			return true
		}
		for _, arg := range n.Args {
			if exprReadsMatchedValueField(name, arg) {
				return true
			}
		}
		return false
	case *ast.FieldExpr:
		if rootName, ok := fieldRootIdentName(n.Object); ok && rootName == name {
			return true
		}
		return exprReadsMatchedValueField(name, n.Object)
	case *ast.IndexExpr:
		return exprReadsMatchedValueField(name, n.Object) || exprReadsMatchedValueField(name, n.Index) || exprReadsMatchedValueField(name, n.Fallback)
	case *ast.SliceExpr:
		return exprReadsMatchedValueField(name, n.Object) || exprReadsMatchedValueField(name, n.Start) || exprReadsMatchedValueField(name, n.End)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			if exprReadsMatchedValueField(name, elem) {
				return true
			}
		}
		return false
	case *ast.CastExpr:
		return exprReadsMatchedValueField(name, n.Operand)
	case *ast.TernaryExpr:
		return exprReadsMatchedValueField(name, n.Value) || exprReadsMatchedValueField(name, n.Cond) || exprReadsMatchedValueField(name, n.Alt)
	case *ast.AddrOfExpr:
		return exprReadsMatchedValueField(name, n.Operand)
	case *ast.MoveExpr:
		return exprReadsMatchedValueField(name, n.Operand)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			if exprReadsMatchedValueField(name, arg) {
				return true
			}
		}
		return false
	case *ast.ParenExpr:
		return exprReadsMatchedValueField(name, n.Inner)
	case *ast.RaiseExpr:
		return exprReadsMatchedValueField(name, n.Error)
	case *ast.TryExpr:
		return exprReadsMatchedValueField(name, n.Value) || exprReadsMatchedValueField(name, n.Fallback)
	case *ast.CatchExpr:
		if exprReadsMatchedValueField(name, n.Value) {
			return true
		}
		if stmtsReadMatchedValueField(name, n.Success.Body) {
			return true
		}
		for _, arm := range n.Arms {
			if stmtsReadMatchedValueField(name, arm.Body) {
				return true
			}
		}
		return false
	case *ast.UnwrapElseExpr:
		return exprReadsMatchedValueField(name, n.Value) || exprReadsMatchedValueField(name, n.Fallback)
	case *ast.OptionalBindExpr:
		return exprReadsMatchedValueField(name, n.Value)
	case *ast.AllocExpr:
		return exprReadsMatchedValueField(name, n.Owner) || exprReadsMatchedValueField(name, n.Value)
	case *ast.MatchExpr:
		return exprReadsMatchedValueField(name, n.Value) || exprReadsMatchedValueField(name, n.Store) || matchArmsReadMatchedValueField(name, n.Arms)
	case *ast.VisitExpr:
		if exprReadsMatchedValueField(name, n.Value) {
			return true
		}
		for _, arm := range n.Arms {
			if exprReadsMatchedValueField(name, arm.Guard) {
				return true
			}
			if stmtsReadMatchedValueField(name, arm.Body) {
				return true
			}
		}
		return false
	case *ast.FoldExpr:
		if exprReadsMatchedValueField(name, n.Value) {
			return true
		}
		for _, arm := range n.Arms {
			if exprReadsMatchedValueField(name, arm.Guard) {
				return true
			}
			if stmtsReadMatchedValueField(name, arm.Body) {
				return true
			}
		}
		return false
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			if exprReadsMatchedValueField(name, target) {
				return true
			}
		}
		return false
	default:
		return false
	}
}
func fieldRootIdentName(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name, true
	case *ast.FieldExpr:
		return fieldRootIdentName(n.Object)
	case *ast.ParenExpr:
		return fieldRootIdentName(n.Inner)
	case *ast.CastExpr:
		return fieldRootIdentName(n.Operand)
	default:
		return "", false
	}
}
func matchPatternNeedsPayloadDecode(pattern ast.MatchPattern) bool {
	switch p := pattern.(type) {
	case *ast.MatchVariantPattern:
		if len(p.Args) > 0 {
			return true
		}
		for _, arg := range p.Args {
			if matchPatternNeedsPayloadDecode(arg.Pattern) {
				return true
			}
		}
	}
	return false
}
func (s *functionState) enumPayloadPtr(enumPtr C.LLVMValueRef, enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	if enumIsTagOnly(enumType) {
		return nil, fmt.Errorf("enum %s has no lowered payload storage", enumType.Name)
	}
	enumLLVMType, err := s.loweredEnumStorageType(enumType)
	if err != nil {
		return nil, err
	}
	payloadIndex := 1
	if enumType != nil && enumType.Packed {
		payloadIndex, err = s.g.packedEnumPayloadFieldIndex(enumType)
		if err != nil {
			return nil, err
		}
	}
	return C.LLVMBuildStructGEP2(s.builder, enumLLVMType, enumPtr, C.unsigned(payloadIndex), cStringFree("enum.payload.ptr")), nil
}
func (s *functionState) resolvePackedViewStoreBinding(expr ast.Expr, enumType *semantic.EnumType) (*packedStoreBinding, bool, error) {
	if expr == nil || enumType == nil {
		return nil, false, nil
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.resolvePackedViewStoreBinding(n.Inner, enumType)
	case *ast.CastExpr:
		return s.resolvePackedViewStoreBinding(n.Operand, enumType)
	case *ast.MoveExpr:
		return s.resolvePackedViewStoreBinding(n.Operand, enumType)
	case *ast.CanExpr:
		return s.resolvePackedViewStoreBinding(n.Expr, enumType)
	case *ast.Ident:
		if binding, ok := s.lookupPackedVariantView(n.Name); ok && binding.typ != nil && binding.typ.Enum == enumType && binding.store.typ != nil && binding.store.value != nil {
			storeCopy := binding.store
			return &storeCopy, true, nil
		}
		valueBinding, ok := s.lookupBinding(n.Name)
		if !ok {
			return nil, false, nil
		}
		viewType, ok := valueBinding.typ.(*semantic.PackedVariantViewType)
		if !ok || viewType == nil || viewType.Enum != enumType {
			return nil, false, nil
		}
		viewValue, err := s.loadValue(valueBinding.ptr, valueBinding.typ, n.Name)
		if err != nil {
			return nil, false, err
		}
		viewBinding, err := s.unpackPackedVariantViewValue(viewValue, viewType)
		if err != nil {
			return nil, false, err
		}
		if viewBinding.store.typ == nil || viewBinding.store.value == nil {
			return nil, false, nil
		}
		storeCopy := viewBinding.store
		return &storeCopy, true, nil
	default:
		return nil, false, nil
	}
}
func (s *functionState) resolvePackedStoreBindingExpr(expr ast.Expr, enumType *semantic.EnumType) (*packedStoreBinding, bool, error) {
	if expr == nil {
		return nil, false, nil
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.resolvePackedStoreBindingExpr(n.Inner, enumType)
	case *ast.CastExpr:
		return s.resolvePackedStoreBindingExpr(n.Operand, enumType)
	case *ast.MoveExpr:
		return s.resolvePackedStoreBindingExpr(n.Operand, enumType)
	case *ast.FieldExpr:
		actualType := s.exprType(expr)
		storeType, ok := actualType.(*semantic.PackedEnumStoreType)
		if !ok || storeType == nil || storeType.Enum == nil {
			return nil, false, nil
		}
		if enumType != nil && storeType.Enum != enumType {
			return nil, false, nil
		}
		storeValue, actualType, err := s.emitExpr(expr, nil)
		if err != nil {
			return nil, false, err
		}
		resolvedType, ok := actualType.(*semantic.PackedEnumStoreType)
		if !ok || resolvedType == nil || resolvedType.Enum == nil {
			return nil, false, nil
		}
		if enumType != nil && resolvedType.Enum != enumType {
			return nil, false, nil
		}
		resolved := &packedStoreBinding{value: storeValue, typ: resolvedType}
		return resolved, true, nil
	case *ast.Ident:
		binding, ok := s.lookupBinding(n.Name)
		if !ok {
			return nil, false, nil
		}
		storeType, ok := binding.typ.(*semantic.PackedEnumStoreType)
		if !ok || storeType == nil || storeType.Enum == nil {
			return nil, false, nil
		}
		if enumType != nil && storeType.Enum != enumType {
			return nil, false, nil
		}
		storeValue, err := s.loadValue(binding.ptr, binding.typ, n.Name)
		if err != nil {
			return nil, false, err
		}
		resolved := &packedStoreBinding{value: storeValue, typ: storeType}
		return resolved, true, nil
	default:
		return nil, false, nil
	}
}
func (s *functionState) resolvePackedNodeStoreBinding(expr ast.Expr, enumType *semantic.EnumType) (*packedStoreBinding, bool, error) {
	if expr == nil || enumType == nil || !enumType.Packed {
		return nil, false, nil
	}
	if path, ok := s.packedEnumStoragePath(expr); ok {
		if binding, ok := s.lookupPackedEnumStoreOrigin(path, enumType); ok {
			storeCopy := binding
			return &storeCopy, true, nil
		}
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.resolvePackedNodeStoreBinding(n.Inner, enumType)
	case *ast.CastExpr:
		return s.resolvePackedNodeStoreBinding(n.Operand, enumType)
	case *ast.MoveExpr:
		return s.resolvePackedNodeStoreBinding(n.Operand, enumType)
	case *ast.CanExpr:
		return s.resolvePackedNodeStoreBinding(n.Expr, enumType)
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return nil, false, nil
		}
		return s.resolvePackedStoreBindingExpr(n.Object, enumType)
	default:
		return nil, false, nil
	}
}
func (s *functionState) resolvePackedMatchStoreBinding(enumType *semantic.EnumType, valueExpr ast.Expr, storeExpr ast.Expr) (*packedStoreBinding, error) {
	if enumType == nil || !enumType.Packed {
		return nil, nil
	}
	if storeExpr != nil {
		storeValue, actualType, err := s.emitExpr(storeExpr, nil)
		if err != nil {
			return nil, err
		}
		storeType, ok := actualType.(*semantic.PackedEnumStoreType)
		if !ok {
			return nil, fmt.Errorf("packed match over %s requires a packed store, got %s", enumType.Name, actualType.String())
		}
		binding := &packedStoreBinding{value: storeValue, typ: storeType}
		return binding, nil
	}
	if inferred, ok, err := s.resolvePackedViewStoreBinding(valueExpr, enumType); err != nil {
		return nil, err
	} else if ok {
		return inferred, nil
	}
	if inferred, ok, err := s.resolvePackedNodeStoreBinding(valueExpr, enumType); err != nil {
		return nil, err
	} else if ok {
		return inferred, nil
	}
	binding, ok := s.lookupPackedStore(enumType)
	if !ok {
		return nil, fmt.Errorf("missing active packed enum store for %s", enumType.Name)
	}
	return &binding, nil
}
