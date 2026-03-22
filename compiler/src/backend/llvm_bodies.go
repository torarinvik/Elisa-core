//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"
	"sort"
	"strings"
	"unsafe"

	"llcontext/src/ast"
	"llcontext/src/semantic"
)

type valueBinding struct {
	ptr C.LLVMValueRef
	typ semantic.Type
}

type codegenScope struct {
	parent         *codegenScope
	bindings       map[string]valueBinding
	packedEnumPtrs map[string]packedEnumStorageBinding
	packedViewPtrs  map[string]packedVariantViewBinding
}

type functionState struct {
	g                 *llvmGenerator
	decl              *ast.FuncDecl
	fnValue           C.LLVMValueRef
	fnType            *semantic.FuncType
	builder           C.LLVMBuilderRef
	scope             *codegenScope
	typeMap           map[string]semantic.Type
	resultSlot        C.LLVMValueRef
	regions           []regionBinding
	packedStores      map[string]packedStoreBinding
	packedStoreValues map[packedStoreExtractCacheKey]C.LLVMValueRef
	scopedCleanups    []scopedCleanupBinding
}

type scopedCleanupKind int

const (
	scopedCleanupLockGuard scopedCleanupKind = iota
	scopedCleanupThreadPool
)

type scopedCleanupBinding struct {
	kind scopedCleanupKind
	name string
	ptr  C.LLVMValueRef
	typ  semantic.Type
}

type regionBinding struct {
	name string
	ptr  C.LLVMValueRef
	typ  semantic.Type
}

type packedStoreBinding struct {
	value C.LLVMValueRef
	typ   *semantic.PackedEnumStoreType
}

type packedStoreExtractCacheKey struct {
	block C.LLVMBasicBlockRef
	store C.LLVMValueRef
	name  string
}

type packedEnumStorageBinding struct {
	ptr C.LLVMValueRef
	typ *semantic.EnumType
}

type packedVariantViewBinding struct {
	ptr C.LLVMValueRef
	typ *semantic.PackedVariantViewType
}

func (g *llvmGenerator) defineFunctionBody(decl *ast.FuncDecl, fnType *semantic.FuncType, fnValue C.LLVMValueRef) error {
	return g.defineFunctionBodyWithBindings(decl, fnType, fnValue, nil)
}

func (g *llvmGenerator) defineFunctionBodyWithBindings(decl *ast.FuncDecl, fnType *semantic.FuncType, fnValue C.LLVMValueRef, typeBindings map[string]semantic.Type) error {
	if decl == nil || fnType == nil || fnValue == nil {
		return fmt.Errorf("cannot define function body without declaration, type, and value")
	}
	if C.LLVMCountBasicBlocks(fnValue) != 0 {
		return nil
	}

	builder := C.LLVMCreateBuilderInContext(g.context)
	defer C.LLVMDisposeBuilder(builder)

	entryName := cString("entry")
	defer C.free(unsafe.Pointer(entryName))
	entry := C.LLVMAppendBasicBlockInContext(g.context, fnValue, entryName)
	C.LLVMPositionBuilderAtEnd(builder, entry)

	state := &functionState{
		g:                 g,
		decl:              decl,
		fnValue:           fnValue,
		fnType:            fnType,
		builder:           builder,
		scope:             &codegenScope{bindings: map[string]valueBinding{}, packedEnumPtrs: map[string]packedEnumStorageBinding{}, packedViewPtrs: map[string]packedVariantViewBinding{}},
		typeMap:           typeBindings,
		packedStores:      map[string]packedStoreBinding{},
		packedStoreValues: map[packedStoreExtractCacheKey]C.LLVMValueRef{},
	}

	paramOffset := 0
	if _, ok := nonVoidErrorUnion(fnType.Return); ok {
		state.resultSlot = C.LLVMGetParam(fnValue, 0)
		paramOffset = 1
	}

	for i, param := range decl.Params {
		if i >= len(fnType.Params) {
			break
		}
		alloca, err := state.createEntryAlloca(param.Name, fnType.Params[i])
		if err != nil {
			return err
		}
		paramValue := C.LLVMGetParam(fnValue, C.unsigned(i+paramOffset))
		C.LLVMBuildStore(builder, paramValue, alloca)
		state.defineBinding(param.Name, valueBinding{ptr: alloca, typ: fnType.Params[i]})
		state.bindPackedStoreValue(fnType.Params[i], paramValue)
	}

	if err := state.emitBlock(decl.Body, false); err != nil {
		return err
	}

	if !state.currentBlockTerminated() {
		if err := state.emitActiveScopedCleanup(); err != nil {
			return err
		}
		if err := state.emitRegionCleanup(); err != nil {
			return err
		}
		if isVoidType(fnType.Return) {
			C.LLVMBuildRetVoid(builder)
		} else if retUnion, ok := fnType.Return.(*semantic.ErrorUnionType); ok && isVoidType(retUnion.Value) {
			zeroCode, err := state.errorCodeConstant(0)
			if err != nil {
				return err
			}
			C.LLVMBuildRet(builder, zeroCode)
		} else {
			return fmt.Errorf("function %s may fall through without returning a value", decl.Name)
		}
	}

	return nil
}

func (s *functionState) emitFunctionReturn(value C.LLVMValueRef, actual semantic.Type) error {
	if err := s.emitActiveScopedCleanup(); err != nil {
		return err
	}
	if err := s.emitRegionCleanup(); err != nil {
		return err
	}
	if retUnion, ok := s.fnType.Return.(*semantic.ErrorUnionType); ok {
		coerced, err := s.coerceValue(value, actual, retUnion)
		if err != nil {
			return err
		}
		if isVoidType(retUnion.Value) {
			C.LLVMBuildRet(s.builder, coerced)
			return nil
		}
		if s.resultSlot == nil {
			return fmt.Errorf("function %s is missing a hidden return slot for %s", s.decl.Name, retUnion.String())
		}
		errorCode, err := s.extractErrorUnionCode(coerced, retUnion)
		if err != nil {
			return err
		}
		payload, err := s.extractErrorUnionPayload(coerced, retUnion)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, payload, s.resultSlot)
		C.LLVMBuildRet(s.builder, errorCode)
		return nil
	}
	coerced, err := s.coerceValue(value, actual, s.fnType.Return)
	if err != nil {
		return err
	}
	C.LLVMBuildRet(s.builder, coerced)
	return nil
}

func (s *functionState) emitRegionCleanup() error {
	for i := len(s.regions) - 1; i >= 0; i-- {
		if err := s.emitArenaFree(s.regions[i].ptr, s.regions[i].typ); err != nil {
			return err
		}
	}
	return nil
}

func (s *functionState) emitActiveScopedCleanup() error {
	for i := len(s.scopedCleanups) - 1; i >= 0; i-- {
		if err := s.emitScopedCleanup(s.scopedCleanups[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *functionState) emitScopedCleanup(binding scopedCleanupBinding) error {
	switch binding.kind {
	case scopedCleanupLockGuard:
		return s.emitConditionalMutexUnlock(binding)
	case scopedCleanupThreadPool:
		return s.emitConditionalPoolShutdown(binding)
	default:
		return fmt.Errorf("unsupported scoped cleanup kind %d", binding.kind)
	}
}

func (s *functionState) emitBlock(stmts []ast.Stmt, scoped bool) error {
	if scoped {
		savedPackedStores := s.packedStores
		s.packedStores = s.clonePackedStores()
		s.pushScope()
		defer func() {
			s.popScope()
			s.packedStores = savedPackedStores
		}()
	}
	for _, stmt := range stmts {
		if s.currentBlockTerminated() {
			break
		}
		if err := s.emitStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *functionState) emitStmt(stmt ast.Stmt) error {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		var declType semantic.Type
		var err error
		if n.Type != nil {
			declType, err = s.resolveTypeExpr(n.Type)
			if err != nil {
				return err
			}
		} else if n.Value != nil {
			declType = s.exprType(n.Value)
			if declType == nil {
				return fmt.Errorf("cannot infer type for variable %s", n.Name)
			}
		} else {
			return fmt.Errorf("variable %s requires a type or initializer", n.Name)
		}
		alloca, err := s.createEntryAlloca(n.Name, declType)
		if err != nil {
			return err
		}
		s.defineBinding(n.Name, valueBinding{ptr: alloca, typ: declType})
		if n.Value != nil {
			value, _, err := s.emitExpr(n.Value, declType)
			if err != nil {
				return err
			}
			C.LLVMBuildStore(s.builder, value, alloca)
			s.bindPackedStoreValue(declType, value)
		}
		return nil
	case *ast.MoveBindStmt:
		return s.emitMoveBindStmt(n)
	case *ast.OpenStmt:
		return s.emitOpenStmt(n)
	case *ast.ViewStmt:
		return s.emitViewStmt(n)
	case *ast.RegionStmt:
		arenaType := s.g.result.NamedTypes["Arena"]
		if arenaType == nil {
			return fmt.Errorf("missing builtin Arena type for region %s", n.Name)
		}
		alloca, err := s.createEntryAlloca(n.Name, arenaType)
		if err != nil {
			return err
		}
		zero, err := s.zeroValue(arenaType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, zero, alloca)
		s.defineBinding(n.Name, valueBinding{ptr: alloca, typ: arenaType})
		s.regions = append(s.regions, regionBinding{name: n.Name, ptr: alloca, typ: arenaType})
		return s.emitRegionInit(alloca, arenaType, n.Capacity)
	case *ast.MarkStmt:
		regionBinding, ok := s.lookupBinding(n.RegionName)
		if !ok {
			return fmt.Errorf("unknown region %q during LLVM lowering", n.RegionName)
		}
		markType := s.g.result.NamedTypes["ArenaMark"]
		if markType == nil {
			return fmt.Errorf("missing builtin ArenaMark type for region checkpoints")
		}
		alloca, err := s.createEntryAlloca(n.Name, markType)
		if err != nil {
			return err
		}
		markValue, err := s.emitArenaSnapshot(regionBinding.ptr, regionBinding.typ)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, markValue, alloca)
		s.defineBinding(n.Name, valueBinding{ptr: alloca, typ: markType})
		return nil
	case *ast.RestoreStmt:
		regionBinding, ok := s.lookupBinding(n.RegionName)
		if !ok {
			return fmt.Errorf("unknown region %q during LLVM lowering", n.RegionName)
		}
		markBinding, ok := s.lookupBinding(n.MarkName)
		if !ok {
			return fmt.Errorf("unknown checkpoint %q during LLVM lowering", n.MarkName)
		}
		markValue, err := s.loadValue(markBinding.ptr, markBinding.typ, n.MarkName)
		if err != nil {
			return err
		}
		return s.emitArenaRewind(regionBinding.ptr, regionBinding.typ, markValue, markBinding.typ)
	case *ast.ResetStmt:
		regionBinding, ok := s.lookupBinding(n.Name)
		if !ok {
			return fmt.Errorf("unknown region %q during LLVM lowering", n.Name)
		}
		return s.emitArenaReset(regionBinding.ptr, regionBinding.typ)
	case *ast.DestroyStmt:
		binding, ok := s.lookupBinding(n.Name)
		if !ok {
			return fmt.Errorf("unknown region %q during LLVM lowering", n.Name)
		}
		return s.emitArenaFree(binding.ptr, binding.typ)
	case *ast.AssignStmt:
		ptr, targetType, err := s.emitAddress(n.Target)
		if err != nil {
			return err
		}
		s.invalidatePackedEnumStorageExpr(n.Target)
		value, _, err := s.emitExpr(n.Value, targetType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, value, ptr)
		s.bindPackedStoreValue(targetType, value)
		return nil
	case *ast.AsRefAssignStmt:
		ptr, targetType, err := s.emitAddress(n.Target)
		if err != nil {
			return err
		}
		s.invalidatePackedEnumStorageExpr(n.Target)
		value, _, err := s.emitExpr(n.Value, targetType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, value, ptr)
		s.bindPackedStoreValue(targetType, value)
		return nil
	case *ast.AugAssignStmt:
		ptr, targetType, err := s.emitAddress(n.Target)
		if err != nil {
			return err
		}
		current, err := s.loadValue(ptr, targetType, "aug.cur")
		if err != nil {
			return err
		}
		value, _, err := s.emitExpr(n.Value, targetType)
		if err != nil {
			return err
		}
		result, err := s.emitAugmentedValue(n.Op, current, value, targetType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, result, ptr)
		return nil
	case *ast.ReturnStmt:
		if n.Value == nil {
			if err := s.emitActiveScopedCleanup(); err != nil {
				return err
			}
			if err := s.emitRegionCleanup(); err != nil {
				return err
			}
			if retUnion, ok := s.fnType.Return.(*semantic.ErrorUnionType); ok && isVoidType(retUnion.Value) {
				zeroCode, err := s.errorCodeConstant(0)
				if err != nil {
					return err
				}
				C.LLVMBuildRet(s.builder, zeroCode)
				return nil
			}
			C.LLVMBuildRetVoid(s.builder)
			return nil
		}
		value, valueType, err := s.emitExpr(n.Value, nil)
		if err != nil {
			return err
		}
		return s.emitFunctionReturn(value, valueType)
	case *ast.IfStmt:
		return s.emitIf(n)
	case *ast.MatchStmt:
		return s.emitMatch(n)
	case *ast.InStoreStmt:
		return s.emitInStore(n)
	case *ast.CanStmt:
		return s.emitBlock(n.Body, true)
	case *ast.PoolStmt:
		return s.emitPoolStmt(n)
	case *ast.LockStmt:
		return s.emitLockStmt(n)
	case *ast.WhileStmt:
		return s.emitWhile(n)
	case *ast.PassStmt:
		return nil
	case *ast.PanicStmt:
		if n.Message != nil {
			if _, _, err := s.emitExpr(n.Message, nil); err != nil {
				return err
			}
		}
		trapFn, err := s.ensureTrapFunction()
		if err != nil {
			return err
		}
		trapType, err := s.g.lowerFunctionType(&semantic.FuncType{Name: "llvm.trap", Return: s.g.result.NamedTypes["void"]})
		if err != nil {
			return err
		}
		s.buildCall(trapType, trapFn, nil, "")
		C.LLVMBuildUnreachable(s.builder)
		return nil
	case *ast.ExprStmt:
		_, _, err := s.emitExpr(n.Expr, nil)
		return err
	case *ast.DiscardStmt:
		_, _, err := s.emitExpr(n.Value, nil)
		return err
	case *ast.StaticIfStmt:
		return s.emitStaticIf(n)
	case *ast.StaticErrorStmt:
		return fmt.Errorf("static error should not reach LLVM lowering")
	default:
		return fmt.Errorf("unsupported statement %T", stmt)
	}
}

func (s *functionState) emitMoveBindStmt(stmt *ast.MoveBindStmt) error {
	if stmt == nil {
		return nil
	}
	value, valueType, err := s.emitExpr(stmt.Value, nil)
	if err != nil {
		return err
	}
	switch p := stmt.Pattern.(type) {
	case *ast.MoveBindNamePattern:
		return s.emitMoveBindLocal(p.Name, valueType, value)
	case *ast.MoveBindStructPattern:
		fields, err := s.g.structLiteralFields(valueType)
		if err != nil {
			return err
		}
		limit := len(p.Args)
		if len(fields) < limit {
			limit = len(fields)
		}
		for i := 0; i < limit; i++ {
			fieldValue := C.LLVMBuildExtractValue(s.builder, value, C.unsigned(i), cStringFree("move.as.field"))
			if err := s.emitMoveBindLocal(p.Args[i].Name, fields[i].Type, fieldValue); err != nil {
				return err
			}
		}
		return nil
	case *ast.MoveBindVariantPattern:
		enumType, ok := valueType.(*semantic.EnumType)
		if !ok {
			return fmt.Errorf("move-as variant pattern requires an enum value, got %s", valueType.String())
		}
		storeBinding, err := s.resolvePackedMatchStoreBinding(enumType, stmt.Store)
		if err != nil {
			return err
		}
		successBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("move.as.variant.ok"))
		failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("move.as.variant.fail"))
		contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("move.as.variant.cont"))
		matchPattern := &ast.MatchVariantPattern{Position: p.Position, EnumName: p.EnumName, Variant: p.Variant, Args: append([]ast.MatchPatternArg(nil), p.Args...)}
		if _, err := s.emitMatchPatternTest(matchPattern, value, nil, enumType, storeBinding, successBB, failBB); err != nil {
			return err
		}
		C.LLVMPositionBuilderAtEnd(s.builder, successBB)
		if !s.currentBlockTerminated() {
			C.LLVMBuildBr(s.builder, contBB)
		}
		C.LLVMPositionBuilderAtEnd(s.builder, failBB)
		trapFn, err := s.ensureTrapFunction()
		if err != nil {
			return err
		}
		trapType, err := s.g.lowerFunctionType(&semantic.FuncType{Name: "llvm.trap", Return: s.g.result.NamedTypes["void"]})
		if err != nil {
			return err
		}
		s.buildCall(trapType, trapFn, nil, "")
		C.LLVMBuildUnreachable(s.builder)
		C.LLVMPositionBuilderAtEnd(s.builder, contBB)
		return nil
	default:
		return fmt.Errorf("unsupported move-as pattern %T", stmt.Pattern)
	}
}

func (s *functionState) emitOpenStmt(stmt *ast.OpenStmt) error {
	if stmt == nil || stmt.Pattern == nil {
		return nil
	}
	enumType, ok := s.exprType(stmt.Value).(*semantic.EnumType)
	if !ok {
		return fmt.Errorf("open requires a packed enum value")
	}
	storeBinding, err := s.resolvePackedMatchStoreBinding(enumType, stmt.Store)
	if err != nil {
		return err
	}
	enumValue, _, err := s.emitExpr(stmt.Value, enumType)
	if err != nil {
		return err
	}
	successBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("open.ok"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("open.fail"))
	contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("open.cont"))
	matchPattern := &ast.MatchVariantPattern{Position: stmt.Pattern.Position, EnumName: stmt.Pattern.EnumName, Variant: stmt.Pattern.Variant, Args: append([]ast.MatchPatternArg(nil), stmt.Pattern.Args...)}
	s.pushScope()
	matchedDecodedValue, err := s.emitMatchPatternTest(matchPattern, enumValue, nil, enumType, storeBinding, successBB, failBB)
	if err != nil {
		s.popScope()
		return err
	}
	C.LLVMPositionBuilderAtEnd(s.builder, successBB)
	if ident, ok := stmt.Value.(*ast.Ident); ok && matchedDecodedValue != nil {
		s.bindPackedEnumStorage(ident.Name, enumType, matchedDecodedValue)
	}
	if err := s.emitBlock(stmt.Body, false); err != nil {
		s.popScope()
		return err
	}
	if !s.currentBlockTerminated() {
		C.LLVMBuildBr(s.builder, contBB)
	}
	s.popScope()

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	trFn, err := s.ensureTrapFunction()
	if err != nil {
		return err
	}
	trType, err := s.g.lowerFunctionType(&semantic.FuncType{Name: "llvm.trap", Return: s.g.result.NamedTypes["void"]})
	if err != nil {
		return err
	}
	s.buildCall(trType, trFn, nil, "")
	C.LLVMBuildUnreachable(s.builder)

	C.LLVMPositionBuilderAtEnd(s.builder, contBB)
	return nil
}

func (s *functionState) emitViewStmt(stmt *ast.ViewStmt) error {
	if stmt == nil || stmt.Pattern == nil {
		return nil
	}
	enumType, ok := s.exprType(stmt.Value).(*semantic.EnumType)
	if !ok {
		return fmt.Errorf("view requires a packed enum value")
	}
	variant, ok := enumType.Variant(stmt.Pattern.Variant)
	if !ok {
		return fmt.Errorf("enum %s has no variant %s", enumType.Name, stmt.Pattern.Variant)
	}
	resolvedViewType := &semantic.PackedVariantViewType{Enum: enumType, Variant: variant}
	storeBinding, err := s.resolvePackedMatchStoreBinding(enumType, stmt.Store)
	if err != nil {
		return err
	}
	enumValue, _, err := s.emitExpr(stmt.Value, enumType)
	if err != nil {
		return err
	}
	successBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("view.ok"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("view.fail"))
	contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("view.cont"))
	matchPattern := &ast.MatchVariantPattern{Position: stmt.Pattern.Position, EnumName: stmt.Pattern.EnumName, Variant: stmt.Pattern.Variant}
	s.pushScope()
	matchedDecodedValue, err := s.emitMatchPatternTest(matchPattern, enumValue, nil, enumType, storeBinding, successBB, failBB)
	if err != nil {
		s.popScope()
		return err
	}
	C.LLVMPositionBuilderAtEnd(s.builder, successBB)
	viewDecodedValue := matchedDecodedValue
	if viewDecodedValue == nil {
		viewDecodedValue, err = s.decodePackedEnumHandleWithStore(enumValue, enumType, storeBinding)
		if err != nil {
			s.popScope()
			return err
		}
	}
	if ident, ok := stmt.Value.(*ast.Ident); ok && viewDecodedValue != nil {
		s.bindPackedEnumStorage(ident.Name, enumType, viewDecodedValue)
	}
	if stmt.Pattern.Name != "_" && viewDecodedValue != nil {
		s.bindPackedVariantView(stmt.Pattern.Name, resolvedViewType, viewDecodedValue)
	}
	if err := s.emitBlock(stmt.Body, false); err != nil {
		s.popScope()
		return err
	}
	if !s.currentBlockTerminated() {
		C.LLVMBuildBr(s.builder, contBB)
	}
	s.popScope()

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	trFn, err := s.ensureTrapFunction()
	if err != nil {
		return err
	}
	trType, err := s.g.lowerFunctionType(&semantic.FuncType{Name: "llvm.trap", Return: s.g.result.NamedTypes["void"]})
	if err != nil {
		return err
	}
	s.buildCall(trType, trFn, nil, "")
	C.LLVMBuildUnreachable(s.builder)

	C.LLVMPositionBuilderAtEnd(s.builder, contBB)
	return nil
}

func (s *functionState) emitMoveBindLocal(name string, typ semantic.Type, value C.LLVMValueRef) error {
	if name == "_" {
		return nil
	}
	alloca, err := s.createEntryAlloca(name, typ)
	if err != nil {
		return err
	}
	s.defineBinding(name, valueBinding{ptr: alloca, typ: typ})
	C.LLVMBuildStore(s.builder, value, alloca)
	s.bindPackedStoreValue(typ, value)
	return nil
}

func (s *functionState) emitInStore(stmt *ast.InStoreStmt) error {
	storeValue, actualType, err := s.emitExpr(stmt.Store, nil)
	if err != nil {
		return err
	}
	storeType, ok := actualType.(*semantic.PackedEnumStoreType)
	if !ok {
		return fmt.Errorf("in-store block requires a packed enum store, got %s", actualType.String())
	}
	savedStores := s.packedStores
	s.packedStores = s.clonePackedStores()
	if s.packedStores == nil {
		s.packedStores = map[string]packedStoreBinding{}
	}
	s.packedStores[storeType.Enum.Name] = packedStoreBinding{value: storeValue, typ: storeType}
	defer func() {
		s.packedStores = savedStores
	}()
	return s.emitBlock(stmt.Body, true)
}

func (s *functionState) emitPoolStmt(stmt *ast.PoolStmt) error {
	poolCall := &ast.CallExpr{
		Position: stmt.Position,
		Func:     &ast.Ident{Position: stmt.Position, Name: "pool_new"},
		Args:     []ast.Expr{stmt.Workers},
	}
	poolValue, poolType, err := s.emitExpr(poolCall, nil)
	if err != nil {
		return err
	}
	poolAlloca, err := s.createEntryAlloca(stmt.Name, poolType)
	if err != nil {
		return err
	}
	s.pushScope()
	defer s.popScope()
	s.defineBinding(stmt.Name, valueBinding{ptr: poolAlloca, typ: poolType})
	C.LLVMBuildStore(s.builder, poolValue, poolAlloca)
	pool := scopedCleanupBinding{kind: scopedCleanupThreadPool, name: stmt.Name, ptr: poolAlloca, typ: poolType}
	s.scopedCleanups = append(s.scopedCleanups, pool)
	defer func() {
		s.scopedCleanups = s.scopedCleanups[:len(s.scopedCleanups)-1]
	}()
	if err := s.emitBlock(stmt.Body, false); err != nil {
		return err
	}
	if s.currentBlockTerminated() {
		return nil
	}
	return s.emitConditionalPoolShutdown(pool)
}

func (s *functionState) emitLockStmt(stmt *ast.LockStmt) error {
	lockCall := &ast.CallExpr{
		Position: stmt.Position,
		Func:     &ast.Ident{Position: stmt.Position, Name: "mutex_lock"},
		Args: []ast.Expr{&ast.CastExpr{
			Position: stmt.Mutex.Pos(),
			Operand: &ast.AddrOfExpr{
				Position: stmt.Mutex.Pos(),
				Operand:  stmt.Mutex,
			},
			Target: &ast.RefType{
				Position: stmt.Mutex.Pos(),
				Elem:     &ast.NamedType{Position: stmt.Mutex.Pos(), Name: "Mutex"},
				State:    ast.RefStateNonNull,
				Storage:  ast.RefStorageAny,
				Explicit: true,
			},
		}},
	}
	guardValue, guardType, err := s.emitExpr(lockCall, nil)
	if err != nil {
		return err
	}
	guardAlloca, err := s.createEntryAlloca(stmt.GuardName, guardType)
	if err != nil {
		return err
	}
	s.pushScope()
	defer s.popScope()
	s.defineBinding(stmt.GuardName, valueBinding{ptr: guardAlloca, typ: guardType})
	C.LLVMBuildStore(s.builder, guardValue, guardAlloca)
	guard := scopedCleanupBinding{kind: scopedCleanupLockGuard, name: stmt.GuardName, ptr: guardAlloca, typ: guardType}
	s.scopedCleanups = append(s.scopedCleanups, guard)
	defer func() {
		s.scopedCleanups = s.scopedCleanups[:len(s.scopedCleanups)-1]
	}()
	if err := s.emitBlock(stmt.Body, false); err != nil {
		return err
	}
	if s.currentBlockTerminated() {
		return nil
	}
	return s.emitConditionalMutexUnlock(guard)
}

func (s *functionState) emitConditionalMutexUnlock(guard scopedCleanupBinding) error {
	if s.currentBlockTerminated() {
		return nil
	}
	guardValue, err := s.loadValue(guard.ptr, guard.typ, guard.name)
	if err != nil {
		return err
	}
	handleValue := C.LLVMBuildExtractValue(s.builder, guardValue, 0, cStringFree("lock.guard.handle"))
	nullHandleType, err := s.g.lowerType(&semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNullable, Storage: semantic.RefStorageAny, ExplicitStorage: true})
	if err != nil {
		return err
	}
	isNull := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), handleValue, C.LLVMConstNull(nullHandleType), cStringFree("lock.guard.null"))
	unlockBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("lock.unlock"))
	contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("lock.after"))
	C.LLVMBuildCondBr(s.builder, isNull, contBB, unlockBB)

	C.LLVMPositionBuilderAtEnd(s.builder, unlockBB)
	unlockCall := &ast.CallExpr{
		Func: &ast.Ident{Name: "mutex_unlock"},
		Args: []ast.Expr{&ast.MoveExpr{Operand: &ast.Ident{Name: guard.name}}},
	}
	if _, _, err := s.emitExpr(unlockCall, nil); err != nil {
		return err
	}
	if !s.currentBlockTerminated() {
		C.LLVMBuildBr(s.builder, contBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, contBB)
	return nil
}

func (s *functionState) emitConditionalPoolShutdown(pool scopedCleanupBinding) error {
	if s.currentBlockTerminated() {
		return nil
	}
	poolValue, err := s.loadValue(pool.ptr, pool.typ, pool.name)
	if err != nil {
		return err
	}
	handleValue := C.LLVMBuildExtractValue(s.builder, poolValue, 0, cStringFree("pool.handle"))
	nullHandleType, err := s.g.lowerType(&semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNullable, Storage: semantic.RefStorageAny, ExplicitStorage: true})
	if err != nil {
		return err
	}
	isNull := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), handleValue, C.LLVMConstNull(nullHandleType), cStringFree("pool.handle.null"))
	shutdownBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("pool.shutdown"))
	contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("pool.after"))
	C.LLVMBuildCondBr(s.builder, isNull, contBB, shutdownBB)

	C.LLVMPositionBuilderAtEnd(s.builder, shutdownBB)
	if err := s.emitPoolShutdown(pool.ptr, pool.typ); err != nil {
		return err
	}
	zero, err := s.zeroValue(pool.typ)
	if err != nil {
		return err
	}
	C.LLVMBuildStore(s.builder, zero, pool.ptr)
	if !s.currentBlockTerminated() {
		C.LLVMBuildBr(s.builder, contBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, contBB)
	return nil
}

func (s *functionState) emitPoolShutdown(poolPtr C.LLVMValueRef, poolType semantic.Type) error {
	poolRefType := &semantic.RefType{Elem: poolType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{Name: "pool_shutdown", Params: []semantic.Type{poolRefType}, Return: s.g.result.NamedTypes["void"]}
	callee, err := s.g.ensureFunctionDeclared("pool_shutdown", helperType)
	if err != nil {
		return err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return err
	}
	s.buildCall(llvmFnType, callee, []C.LLVMValueRef{poolPtr}, "")
	return nil
}

func (s *functionState) emitRegionInit(arenaPtr C.LLVMValueRef, arenaType semantic.Type, capacityExpr ast.Expr) error {
	capacityType := s.g.result.NamedTypes["usize"]
	var capacityValue C.LLVMValueRef
	if capacityExpr != nil {
		value, _, err := s.emitExpr(capacityExpr, capacityType)
		if err != nil {
			return err
		}
		capacityValue = value
	} else {
		usizeLLVMType, err := s.g.lowerType(capacityType)
		if err != nil {
			return err
		}
		capacityValue = C.LLVMConstInt(usizeLLVMType, 8*1024, 0)
	}
	regionType := s.g.result.NamedTypes["Region"]
	if regionType == nil {
		return fmt.Errorf("missing builtin Region type for region initialization")
	}
	regionRefType := &semantic.RefType{Elem: regionType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{Name: "new_region", Params: []semantic.Type{capacityType}, Return: regionRefType}
	callee, err := s.g.ensureFunctionDeclared("new_region", helperType)
	if err != nil {
		return err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return err
	}
	regionValue := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{capacityValue}, "region.init")
	arenaLLVMType, err := s.g.lowerType(arenaType)
	if err != nil {
		return err
	}
	beginPtr := C.LLVMBuildStructGEP2(s.builder, arenaLLVMType, arenaPtr, 0, cStringFree("region.begin"))
	endPtr := C.LLVMBuildStructGEP2(s.builder, arenaLLVMType, arenaPtr, 1, cStringFree("region.end"))
	C.LLVMBuildStore(s.builder, regionValue, beginPtr)
	C.LLVMBuildStore(s.builder, regionValue, endPtr)
	return nil
}

func (s *functionState) emitArenaFree(arenaPtr C.LLVMValueRef, arenaType semantic.Type) error {
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{Name: "arena_free", Params: []semantic.Type{arenaRefType}, Return: s.g.result.NamedTypes["void"]}
	callee, err := s.g.ensureFunctionDeclared("arena_free", helperType)
	if err != nil {
		return err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return err
	}
	s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaPtr}, "")
	return nil
}

func (s *functionState) emitArenaSnapshot(arenaPtr C.LLVMValueRef, arenaType semantic.Type) (C.LLVMValueRef, error) {
	markType := s.g.result.NamedTypes["ArenaMark"]
	if markType == nil {
		return nil, fmt.Errorf("missing builtin ArenaMark type for region checkpoints")
	}
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{Name: "arena_snapshot", Params: []semantic.Type{arenaRefType}, Return: markType}
	callee, err := s.g.ensureFunctionDeclared("arena_snapshot", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	return s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaPtr}, "region.mark"), nil
}

func (s *functionState) emitArenaRewind(arenaPtr C.LLVMValueRef, arenaType semantic.Type, markValue C.LLVMValueRef, markType semantic.Type) error {
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{Name: "arena_rewind", Params: []semantic.Type{arenaRefType, markType}, Return: s.g.result.NamedTypes["void"]}
	callee, err := s.g.ensureFunctionDeclared("arena_rewind", helperType)
	if err != nil {
		return err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return err
	}
	s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaPtr, markValue}, "")
	return nil
}

func (s *functionState) emitArenaReset(arenaPtr C.LLVMValueRef, arenaType semantic.Type) error {
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{Name: "arena_reset", Params: []semantic.Type{arenaRefType}, Return: s.g.result.NamedTypes["void"]}
	callee, err := s.g.ensureFunctionDeclared("arena_reset", helperType)
	if err != nil {
		return err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return err
	}
	s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaPtr}, "")
	return nil
}

func (s *functionState) emitIf(stmt *ast.IfStmt) error {
	stmt = normalizeIf(stmt)
	condValue, _, err := s.emitExpr(stmt.Cond, s.g.result.NamedTypes["bool"])
	if err != nil {
		return err
	}

	thenName := cString("if.then")
	defer C.free(unsafe.Pointer(thenName))
	thenBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, thenName)

	mergeName := cString("if.end")
	defer C.free(unsafe.Pointer(mergeName))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, mergeName)

	var elseBB C.LLVMBasicBlockRef
	if len(stmt.Else) > 0 {
		elseName := cString("if.else")
		defer C.free(unsafe.Pointer(elseName))
		elseBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, elseName)
		C.LLVMBuildCondBr(s.builder, condValue, thenBB, elseBB)
	} else {
		C.LLVMBuildCondBr(s.builder, condValue, thenBB, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, thenBB)
	if err := s.emitBlock(stmt.Then, true); err != nil {
		return err
	}
	thenTerminated := s.currentBlockTerminated()
	if !thenTerminated {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	elseTerminated := false
	if len(stmt.Else) > 0 {
		C.LLVMPositionBuilderAtEnd(s.builder, elseBB)
		if err := s.emitBlock(stmt.Else, true); err != nil {
			return err
		}
		elseTerminated = s.currentBlockTerminated()
		if !elseTerminated {
			C.LLVMBuildBr(s.builder, mergeBB)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(stmt.Else) > 0 && thenTerminated && elseTerminated {
		C.LLVMBuildUnreachable(s.builder)
	}
	return nil
}

func (s *functionState) emitWhile(stmt *ast.WhileStmt) error {
	condName := cString("while.cond")
	defer C.free(unsafe.Pointer(condName))
	condBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, condName)

	bodyName := cString("while.body")
	defer C.free(unsafe.Pointer(bodyName))
	bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, bodyName)

	exitName := cString("while.end")
	defer C.free(unsafe.Pointer(exitName))
	exitBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, exitName)

	C.LLVMBuildBr(s.builder, condBB)
	C.LLVMPositionBuilderAtEnd(s.builder, condBB)
	condValue, _, err := s.emitExpr(stmt.Cond, s.g.result.NamedTypes["bool"])
	if err != nil {
		return err
	}
	C.LLVMBuildCondBr(s.builder, condValue, bodyBB, exitBB)

	C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
	if err := s.emitBlock(stmt.Body, true); err != nil {
		return err
	}
	if !s.currentBlockTerminated() {
		C.LLVMBuildBr(s.builder, condBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, exitBB)
	return nil
}

func (s *functionState) emitMatch(stmt *ast.MatchStmt) error {
	enumType, ok := s.exprType(stmt.Value).(*semantic.EnumType)
	if !ok {
		return fmt.Errorf("match requires an enum value")
	}
	storeBinding, err := s.resolvePackedMatchStoreBinding(enumType, stmt.Store)
	if err != nil {
		return err
	}
	enumValue, _, err := s.emitExpr(stmt.Value, enumType)
	if err != nil {
		return err
	}
	var decodedMatchValue C.LLVMValueRef
	if enumType.Packed && packedMatchShouldEagerDecode(s.g.result, stmt.Value, stmt.Arms) {
		decodedMatchValue, err = s.decodePackedEnumHandleWithStore(enumValue, enumType, storeBinding)
		if err != nil {
			return err
		}
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.fail"))
	allTerminated := true

	for i, arm := range stmt.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(stmt.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.next"))
		}
		armDecodedValue, err := s.emitMatchPatternTest(arm.Pattern, enumValue, decodedMatchValue, enumType, storeBinding, bodyBB, nextBB)
		if err != nil {
			return err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if ident, ok := stmt.Value.(*ast.Ident); ok && enumType.Packed && armDecodedValue != nil {
			s.bindPackedEnumStorage(ident.Name, enumType, armDecodedValue)
		}
		if err := s.emitBlock(arm.Body, false); err != nil {
			s.popScope()
			return err
		}
		s.popScope()
		if !s.currentBlockTerminated() {
			allTerminated = false
			C.LLVMBuildBr(s.builder, mergeBB)
		}

		if nextBB != mergeBB {
			C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if matchIsExhaustive(enumType, stmt.Arms) {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if allTerminated && matchIsExhaustive(enumType, stmt.Arms) {
		C.LLVMBuildUnreachable(s.builder)
	}
	return nil
}

func (s *functionState) emitMatchExpr(expr *ast.MatchExpr) (C.LLVMValueRef, semantic.Type, error) {
	resultType := s.exprType(expr)
	enumType, ok := s.exprType(expr.Value).(*semantic.EnumType)
	if !ok {
		return nil, nil, fmt.Errorf("match requires an enum value")
	}
	storeBinding, err := s.resolvePackedMatchStoreBinding(enumType, expr.Store)
	if err != nil {
		return nil, nil, err
	}
	enumValue, _, err := s.emitExpr(expr.Value, enumType)
	if err != nil {
		return nil, nil, err
	}
	var decodedMatchValue C.LLVMValueRef
	if enumType.Packed && packedMatchShouldEagerDecode(s.g.result, expr.Value, expr.Arms) {
		decodedMatchValue, err = s.decodePackedEnumHandleWithStore(enumValue, enumType, storeBinding)
		if err != nil {
			return nil, nil, err
		}
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(expr.Arms))
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(expr.Arms))
	for i, arm := range expr.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(expr.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.next"))
		}
		armDecodedValue, err := s.emitMatchPatternTest(arm.Pattern, enumValue, decodedMatchValue, enumType, storeBinding, bodyBB, nextBB)
		if err != nil {
			return nil, nil, err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if ident, ok := expr.Value.(*ast.Ident); ok && enumType.Packed && armDecodedValue != nil {
			s.bindPackedEnumStorage(ident.Name, enumType, armDecodedValue)
		}
		armValue, reachable, err := s.emitMatchExprArmBody(arm.Body, resultType)
		if err != nil {
			s.popScope()
			return nil, nil, err
		}
		if reachable && !s.currentBlockTerminated() {
			armEnd := C.LLVMGetInsertBlock(s.builder)
			incomingValues = append(incomingValues, armValue)
			incomingBlocks = append(incomingBlocks, armEnd)
			C.LLVMBuildBr(s.builder, mergeBB)
		}
		s.popScope()

		if nextBB != mergeBB {
			C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if semantic.IsNeverType(resultType) {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		llvmType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, nil, err
		}
		undefValue := C.LLVMGetUndef(llvmType)
		failEnd := C.LLVMGetInsertBlock(s.builder)
		incomingValues = append(incomingValues, undefValue)
		incomingBlocks = append(incomingBlocks, failEnd)
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingValues) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	if len(incomingValues) == 1 || semantic.IsNeverType(resultType) {
		return incomingValues[0], resultType, nil
	}
	llvmType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, llvmType, cStringFree("match.expr.phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}

func (s *functionState) emitMatchExprArmBody(body []ast.Stmt, resultType semantic.Type) (C.LLVMValueRef, bool, error) {
	if len(body) == 0 {
		return nil, false, fmt.Errorf("match expression arm must end with an expression")
	}
	for i, stmt := range body {
		isLast := i == len(body)-1
		if !isLast {
			if err := s.emitStmt(stmt); err != nil {
				return nil, false, err
			}
			if s.currentBlockTerminated() {
				return nil, false, nil
			}
			continue
		}
		if exprStmt, ok := stmt.(*ast.ExprStmt); ok {
			value, _, err := s.emitExpr(exprStmt.Expr, resultType)
			if err != nil {
				return nil, false, err
			}
			return value, true, nil
		}
		if err := s.emitStmt(stmt); err != nil {
			return nil, false, err
		}
		if s.currentBlockTerminated() {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("match expression arm must end with an expression")
	}
	return nil, false, fmt.Errorf("match expression arm must end with an expression")
}

func (s *functionState) emitMatchPatternTest(pattern ast.MatchPattern, actualValue C.LLVMValueRef, decodedActualValue C.LLVMValueRef, actualType semantic.Type, store *packedStoreBinding, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) (C.LLVMValueRef, error) {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		C.LLVMBuildBr(s.builder, successBB)
		return decodedActualValue, nil
	case *ast.MatchBindPattern:
		alloca, err := s.createEntryAlloca(p.Name, actualType)
		if err != nil {
			return nil, err
		}
		C.LLVMBuildStore(s.builder, actualValue, alloca)
		s.defineBinding(p.Name, valueBinding{ptr: alloca, typ: actualType})
		if enumType, ok := actualType.(*semantic.EnumType); ok && enumType.Packed && decodedActualValue != nil {
			s.bindPackedEnumStorage(p.Name, enumType, decodedActualValue)
		}
		C.LLVMBuildBr(s.builder, successBB)
		return decodedActualValue, nil
	case *ast.MatchVariantPattern:
		enumType, ok := actualType.(*semantic.EnumType)
		if !ok {
			return nil, fmt.Errorf("variant pattern %s.%s requires enum type, got %s", p.EnumName, p.Variant, actualType.String())
		}
		variant, ok := enumType.Variant(p.Variant)
		if !ok {
			return nil, fmt.Errorf("enum %s has no variant %s", enumType.Name, p.Variant)
		}
		tagValue, err := s.extractEnumTagValue(actualValue, decodedActualValue, enumType, store)
		if err != nil {
			return nil, err
		}
		tagConst, err := s.enumTagConstant(variant.Tag)
		if err != nil {
			return nil, err
		}
		matchedBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.pattern.ok"))
		pred := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), tagValue, tagConst, cStringFree("match.tag"))
		C.LLVMBuildCondBr(s.builder, pred, matchedBB, failureBB)

		C.LLVMPositionBuilderAtEnd(s.builder, matchedBB)
		orderedArgs, err := s.resolveMatchPatternArgs(p, variant)
		if err != nil {
			return nil, err
		}
		matchedDecodedValue := decodedActualValue
		if len(orderedArgs) == 0 {
			C.LLVMBuildBr(s.builder, successBB)
			return matchedDecodedValue, nil
		}
		hasNestedPattern := false
		for i := range orderedArgs {
			if orderedArgs[i] != nil {
				hasNestedPattern = true
				break
			}
		}
		if !hasNestedPattern {
			C.LLVMBuildBr(s.builder, successBB)
			return matchedDecodedValue, nil
		}
		payloadValues, err := s.extractEnumVariantPayloadValues(actualValue, matchedDecodedValue, enumType, variant, store)
		if err != nil {
			return nil, err
		}
		for i := range orderedArgs {
			if orderedArgs[i] == nil {
				continue
			}
			nextSuccess := successBB
			if i != len(orderedArgs)-1 {
				nextSuccess = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.pattern.next"))
			}
			if _, err := s.emitMatchPatternTest(orderedArgs[i].Pattern, payloadValues[i], nil, variant.Payload[i], store, nextSuccess, failureBB); err != nil {
				return nil, err
			}
			if i != len(orderedArgs)-1 {
				C.LLVMPositionBuilderAtEnd(s.builder, nextSuccess)
			}
		}
		return matchedDecodedValue, nil
	default:
		return nil, fmt.Errorf("unsupported match pattern %T", pattern)
	}
}

func (s *functionState) resolveMatchPatternArgs(pattern *ast.MatchVariantPattern, variant *semantic.EnumVariant) ([]*ast.MatchPatternArg, error) {
	ordered := make([]*ast.MatchPatternArg, len(variant.Payload))
	if len(pattern.Args) == 0 {
		return ordered, nil
	}
	namedCount := 0
	for i := range pattern.Args {
		if pattern.Args[i].Name != "" {
			namedCount++
		}
	}
	if namedCount == 0 {
		if len(pattern.Args) != len(variant.Payload) {
			return nil, fmt.Errorf("match arm %s.%s expects %d payload patterns, got %d", pattern.EnumName, pattern.Variant, len(variant.Payload), len(pattern.Args))
		}
		for i := range pattern.Args {
			ordered[i] = &pattern.Args[i]
		}
		return ordered, nil
	}
	if namedCount != len(pattern.Args) {
		return nil, fmt.Errorf("match arm %s.%s cannot mix positional and named payload patterns", pattern.EnumName, pattern.Variant)
	}
	if !variant.HasNamedPayloads() {
		return nil, fmt.Errorf("match arm %s.%s uses named payload patterns but the variant payloads are unnamed", pattern.EnumName, pattern.Variant)
	}
	seen := map[int]bool{}
	for i := range pattern.Args {
		arg := &pattern.Args[i]
		index, ok := variant.PayloadIndex(arg.Name)
		if !ok {
			return nil, fmt.Errorf("match arm %s.%s has no payload field %q", pattern.EnumName, pattern.Variant, arg.Name)
		}
		if seen[index] {
			return nil, fmt.Errorf("match arm %s.%s matches payload field %q more than once", pattern.EnumName, pattern.Variant, arg.Name)
		}
		seen[index] = true
		ordered[index] = arg
	}
	missing := make([]string, 0)
	for i := range ordered {
		if ordered[i] == nil {
			missing = append(missing, variant.PayloadLabel(i))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("match arm %s.%s is missing named payload patterns for: %s", pattern.EnumName, pattern.Variant, strings.Join(missing, ", "))
	}
	return ordered, nil
}

func (s *functionState) extractEnumTagValue(enumValue C.LLVMValueRef, decodedEnumValue C.LLVMValueRef, enumType *semantic.EnumType, store *packedStoreBinding) (C.LLVMValueRef, error) {
	if enumType != nil && enumType.Packed {
		return s.loadEnumTag(decodedEnumValue, enumValue, enumType, store)
	}
	if enumIsTagOnly(enumType) {
		return enumValue, nil
	}
	return C.LLVMBuildExtractValue(s.builder, enumValue, 0, cStringFree("match.tag.value")), nil
}

func (s *functionState) extractEnumVariantPayloadValues(enumValue C.LLVMValueRef, decodedEnumValue C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant, store *packedStoreBinding) ([]C.LLVMValueRef, error) {
	if variant == nil || len(variant.Payload) == 0 {
		return nil, nil
	}
	if enumType != nil && enumType.Packed {
		return s.loadEnumVariantPayload(decodedEnumValue, enumValue, enumType, variant, store)
	}
	enumPtr, err := s.emitStackTempValue(enumValue, enumType, "match.payload.tmp")
	if err != nil {
		return nil, err
	}
	return s.loadEnumVariantPayload(nil, enumPtr, enumType, variant, store)
}

func matchIsExhaustive(enumType *semantic.EnumType, arms []ast.MatchArm) bool {
	if enumType == nil {
		return false
	}
	covered := map[string]bool{}
	for _, arm := range arms {
		switch pattern := arm.Pattern.(type) {
		case *ast.MatchWildcardPattern:
			return true
		case *ast.MatchVariantPattern:
			covered[pattern.Variant] = true
		}
	}
	return len(covered) == len(enumType.Variants)
}

func (s *functionState) loadEnumTag(decodedEnumPtr C.LLVMValueRef, enumPtr C.LLVMValueRef, enumType *semantic.EnumType, store *packedStoreBinding) (C.LLVMValueRef, error) {
	if enumType != nil && !enumType.Packed && enumIsTagOnly(enumType) {
		tagType, err := s.g.lowerBuiltin("u32")
		if err != nil {
			return nil, err
		}
		return C.LLVMBuildLoad2(s.builder, tagType, enumPtr, cStringFree("match.tag.value")), nil
	}
	if enumType != nil && enumType.Packed {
		if decodedEnumPtr != nil {
			enumPtr = decodedEnumPtr
		} else if s.g.packedEnumABI == packedEnumABIWordHandle {
			return s.readPackedEnumTagWithStore(enumPtr, enumType, store)
		} else {
			var err error
			enumPtr, err = s.decodePackedEnumHandleWithStore(enumPtr, enumType, store)
			if err != nil {
				return nil, err
			}
		}
	}
	enumLLVMType, err := s.loweredEnumStorageType(enumType)
	if err != nil {
		return nil, err
	}
	tagPtr := C.LLVMBuildStructGEP2(s.builder, enumLLVMType, enumPtr, 0, cStringFree("match.tag.ptr"))
	tagType, err := s.g.lowerBuiltin("u32")
	if err != nil {
		return nil, err
	}
	return C.LLVMBuildLoad2(s.builder, tagType, tagPtr, cStringFree("match.tag.value")), nil
}

func (s *functionState) loadEnumVariantPayload(decodedEnumPtr C.LLVMValueRef, enumPtr C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant, store *packedStoreBinding) ([]C.LLVMValueRef, error) {
	if variant == nil || len(variant.Payload) == 0 {
		return nil, nil
	}
	if enumType != nil && enumType.Packed {
		if decodedEnumPtr != nil {
			enumPtr = decodedEnumPtr
		} else if s.g.packedEnumABI == packedEnumABIWordHandle {
			values, ok, readErr := s.readPackedEnumVariantPayloadWithStore(enumPtr, enumType, variant, store)
			if readErr != nil {
				return nil, readErr
			}
			if ok {
				return values, nil
			}
			var decodeErr error
			enumPtr, decodeErr = s.decodePackedEnumHandleWithStore(enumPtr, enumType, store)
			if decodeErr != nil {
				return nil, decodeErr
			}
		} else {
			var decodeErr error
			enumPtr, decodeErr = s.decodePackedEnumHandleWithStore(enumPtr, enumType, store)
			if decodeErr != nil {
				return nil, decodeErr
			}
		}
	}
	payloadPtr, err := s.enumPayloadPtr(enumPtr, enumType)
	if err != nil {
		return nil, err
	}
	payloadType, err := s.g.lowerEnumVariantPayloadType(variant)
	if err != nil {
		return nil, err
	}
	if len(variant.Payload) == 1 {
		value := C.LLVMBuildLoad2(s.builder, payloadType, payloadPtr, cStringFree("match.payload"))
		return []C.LLVMValueRef{value}, nil
	}
	aggregate := C.LLVMBuildLoad2(s.builder, payloadType, payloadPtr, cStringFree("match.payload"))
	values := make([]C.LLVMValueRef, 0, len(variant.Payload))
	for i := range variant.Payload {
		values = append(values, C.LLVMBuildExtractValue(s.builder, aggregate, C.unsigned(i), cStringFree("match.payload.field")))
	}
	return values, nil
}

func (s *functionState) readPackedEnumVariantPayloadWithStore(handleValue C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant, store *packedStoreBinding) ([]C.LLVMValueRef, bool, error) {
	if enumType == nil || !enumType.Packed || variant == nil || len(variant.Payload) == 0 {
		return nil, false, nil
	}
	if s.g.packedEnumABI != packedEnumABIWordHandle {
		return nil, false, nil
	}
	if store == nil || store.typ == nil {
		return nil, false, nil
	}
	wordOffsets, ok, err := s.packedEnumVariantDirectPayloadWordOffsets(enumType, variant)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	values := make([]C.LLVMValueRef, 0, len(variant.Payload))
	uintptrType := s.g.result.NamedTypes["uintptr"]
	for i, payloadType := range variant.Payload {
		wordValue, err := s.readPackedEnumWordWithStore(handleValue, enumType, store, wordOffsets[i])
		if err != nil {
			return nil, false, err
		}
		coerced, err := s.coerceValue(wordValue, uintptrType, payloadType)
		if err != nil {
			return nil, false, err
		}
		values = append(values, coerced)
	}
	return values, true, nil
}

func (s *functionState) packedEnumVariantDirectPayloadWordOffsets(enumType *semantic.EnumType, variant *semantic.EnumVariant) ([]C.LLVMValueRef, bool, error) {
	if enumType == nil || !enumType.Packed || variant == nil || len(variant.Payload) == 0 {
		return nil, false, nil
	}
	wordBytes := uint64(s.g.wordBits / 8)
	if wordBytes == 0 {
		wordBytes = 8
	}
	for _, payloadType := range variant.Payload {
		sizeBytes, err := s.g.abiSizeOfType(payloadType)
		if err != nil {
			return nil, false, err
		}
		if sizeBytes != wordBytes {
			return nil, false, nil
		}
	}
	payloadFieldIndex, err := s.g.packedEnumPayloadFieldIndex(enumType)
	if err != nil {
		return nil, false, err
	}
	rowType, err := s.g.ensurePackedEnumStorageType(enumType)
	if err != nil {
		return nil, false, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, false, err
	}
	i32Type := C.LLVMInt32TypeInContext(s.g.context)
	zeroIndex := C.LLVMConstInt(i32Type, 0, 0)
	payloadFieldIndexValue := C.LLVMConstInt(i32Type, C.ulonglong(payloadFieldIndex), 0)
	nullPtr := C.LLVMConstNull(C.LLVMPointerTypeInContext(s.g.context, 0))
	payloadIndices := []C.LLVMValueRef{zeroIndex, payloadFieldIndexValue}
	payloadPtr := C.LLVMBuildGEP2(s.builder, rowType, nullPtr, llvmValueSlicePtr(payloadIndices), C.unsigned(len(payloadIndices)), cStringFree("packed.payload.word.ptr"))
	payloadOffsetBytes := C.LLVMBuildPtrToInt(s.builder, payloadPtr, usizeType, cStringFree("packed.payload.word.bytes"))
	wordBytesValue := C.LLVMConstInt(usizeType, C.ulonglong(wordBytes), 0)
	baseWordOffset := C.LLVMBuildUDiv(s.builder, payloadOffsetBytes, wordBytesValue, cStringFree("packed.payload.word.offset"))
	if len(variant.Payload) == 1 {
		return []C.LLVMValueRef{baseWordOffset}, true, nil
	}
	payloadType, err := s.g.lowerEnumVariantPayloadType(variant)
	if err != nil {
		return nil, false, err
	}
	offsets := make([]C.LLVMValueRef, 0, len(variant.Payload))
	for i := range variant.Payload {
		fieldIndexValue := C.LLVMConstInt(i32Type, C.ulonglong(i), 0)
		fieldIndices := []C.LLVMValueRef{zeroIndex, fieldIndexValue}
		fieldPtr := C.LLVMBuildGEP2(s.builder, payloadType, nullPtr, llvmValueSlicePtr(fieldIndices), C.unsigned(len(fieldIndices)), cStringFree("packed.payload.field.word.ptr"))
		fieldOffsetBytes := C.LLVMBuildPtrToInt(s.builder, fieldPtr, usizeType, cStringFree("packed.payload.field.word.bytes"))
		fieldWordOffset := C.LLVMBuildUDiv(s.builder, fieldOffsetBytes, wordBytesValue, cStringFree("packed.payload.field.word.offset"))
		offsets = append(offsets, C.LLVMBuildAdd(s.builder, baseWordOffset, fieldWordOffset, cStringFree("packed.payload.word.total")))
	}
	return offsets, true, nil
}

func (s *functionState) readPackedEnumTagWithStore(handleValue C.LLVMValueRef, enumType *semantic.EnumType, store *packedStoreBinding) (C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum tag metadata")
	}
	if store == nil || store.typ == nil {
		return nil, fmt.Errorf("packed enum %s word-handle tag read requires store context", enumType.Name)
	}
	arenaValue, err := s.emitPackedStoreArenaValueNamed(store.value, store.typ, "packed.tag.store.arena")
	if err != nil {
		return nil, err
	}
	stateValue, err := s.emitPackedStoreStateValueNamed(store.value, store.typ, "packed.tag.store.state")
	if err != nil {
		return nil, err
	}
	arenaType := s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	tagType := s.g.result.NamedTypes["u32"]
	helperType := &semantic.FuncType{Name: "ctx_packed_store_read_tag", Params: []semantic.Type{arenaRefType, s.g.result.NamedTypes["uintptr"], voidRefType}, Return: tagType}
	callee, err := s.g.ensureFunctionDeclared("ctx_packed_store_read_tag", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	coercedHandle, err := s.coerceValue(handleValue, enumType, s.g.result.NamedTypes["uintptr"])
	if err != nil {
		return nil, err
	}
	return s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaValue, coercedHandle, stateValue}, "packed.handle.tag"), nil
}

func packedMatchNeedsEagerDecode(arms []ast.MatchArm) bool {
	for _, arm := range arms {
		if matchPatternNeedsPayloadDecode(arm.Pattern) {
			return true
		}
	}
	return false
}

func packedMatchShouldEagerDecode(result *semantic.Result, matchValue ast.Expr, arms []ast.MatchArm) bool {
	needsPayloadDecode := packedMatchNeedsEagerDecode(arms)
	ident, ok := matchValue.(*ast.Ident)
	readsMatchedValueField := ok && matchArmsReadMatchedValueField(ident.Name, arms)
	if !needsPayloadDecode && !readsMatchedValueField {
		return false
	}
	if result != nil && result.ExprHasOnlyFrozenPackedStoreDeps(matchValue) {
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
	case *ast.ReturnStmt:
		return exprReadsMatchedValueField(name, n.Value)
	case *ast.IfStmt:
		return exprReadsMatchedValueField(name, n.Cond) || stmtsReadMatchedValueField(name, n.Then) || stmtsReadMatchedValueField(name, n.Else) || elifsReadMatchedValueField(name, n.Elifs)
	case *ast.WhileStmt:
		return exprReadsMatchedValueField(name, n.Cond) || stmtsReadMatchedValueField(name, n.Body)
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
		return exprReadsMatchedValueField(name, n.Object) || exprReadsMatchedValueField(name, n.Index)
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
	case *ast.UnwrapElseExpr:
		return exprReadsMatchedValueField(name, n.Value) || exprReadsMatchedValueField(name, n.Fallback)
	case *ast.AllocExpr:
		return exprReadsMatchedValueField(name, n.Owner) || exprReadsMatchedValueField(name, n.Value)
	case *ast.MatchExpr:
		return exprReadsMatchedValueField(name, n.Value) || exprReadsMatchedValueField(name, n.Store) || matchArmsReadMatchedValueField(name, n.Arms)
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

func (s *functionState) resolvePackedMatchStoreBinding(enumType *semantic.EnumType, storeExpr ast.Expr) (*packedStoreBinding, error) {
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
	binding, ok := s.lookupPackedStore(enumType)
	if !ok {
		return nil, fmt.Errorf("missing active packed enum store for %s", enumType.Name)
	}
	return &binding, nil
}

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
