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
	"sort"
	"strings"
	"unsafe"

	"llcontext/src/ast"
	"llcontext/src/lexer"
	"llcontext/src/semantic"
)

type valueBinding struct {
	ptr C.LLVMValueRef
	typ semantic.Type
}

type codegenScope struct {
	parent                 *codegenScope
	bindingName            string
	binding                valueBinding
	bindings               map[string]valueBinding
	packedEnumPtrs         map[string]packedEnumStorageBinding
	packedEnumStoreName    string
	packedEnumStoreBinding packedStoreBinding
	packedEnumStores       map[string]packedStoreBinding
	packedViewName         string
	packedViewBinding      packedVariantViewBinding
	packedViewPtrs         map[string]packedVariantViewBinding
}

type functionState struct {
	g                            *llvmGenerator
	decl                         *ast.FuncDecl
	fnValue                      C.LLVMValueRef
	fnType                       *semantic.FuncType
	builder                      C.LLVMBuilderRef
	scope                        *codegenScope
	typeMap                      map[string]semantic.Type
	specializedFuncTypes         map[*semantic.FuncType]*semantic.FuncType
	resultSlot                   C.LLVMValueRef
	regions                      []regionBinding
	packedStores                 map[string]packedStoreBinding
	packedStoreValueKey1         packedStoreExtractCacheKey
	packedStoreValue1            C.LLVMValueRef
	packedStoreValueKey2         packedStoreExtractCacheKey
	packedStoreValue2            C.LLVMValueRef
	packedStoreValueKey3         packedStoreExtractCacheKey
	packedStoreValue3            C.LLVMValueRef
	packedStoreValues            map[packedStoreExtractCacheKey]C.LLVMValueRef
	packedVariantSparseTagReads  map[packedVariantSparseTagReadCacheKey]C.LLVMValueRef
	packedVariantSparseWordReads map[packedVariantSparseWordReadCacheKey]C.LLVMValueRef
	packedDenseTagReads          map[packedDenseTagReadCacheKey]C.LLVMValueRef
	packedDenseWordReads         map[packedDenseWordReadCacheKey]C.LLVMValueRef
	packedDenseSideWordReads     map[packedDenseSideWordReadCacheKey]C.LLVMValueRef
	scopedCleanups               []scopedCleanupBinding
	poolScopes                   []activePoolBinding
	scopePool                    []*codegenScope
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
	index C.unsigned
}

type packedVariantSparseTagReadCacheKey struct {
	block     C.LLVMBasicBlockRef
	storeType *semantic.PackedEnumStoreType
	state     C.LLVMValueRef
	handle    C.LLVMValueRef
}

type packedVariantSparseWordReadCacheKey struct {
	block     C.LLVMBasicBlockRef
	storeType *semantic.PackedEnumStoreType
	state     C.LLVMValueRef
	handle    C.LLVMValueRef
	offset    C.LLVMValueRef
}

type packedReadOriginKey struct {
	root C.LLVMValueRef
	path string
}

type packedDenseTagReadCacheKey struct {
	block     C.LLVMBasicBlockRef
	storeType *semantic.PackedEnumStoreType
	state     C.LLVMValueRef
	origin    packedReadOriginKey
	handle    C.LLVMValueRef
}

type packedDenseWordReadCacheKey struct {
	block     C.LLVMBasicBlockRef
	storeType *semantic.PackedEnumStoreType
	state     C.LLVMValueRef
	origin    packedReadOriginKey
	handle    C.LLVMValueRef
	offset    C.LLVMValueRef
}

type packedDenseSideWordReadCacheKey struct {
	block     C.LLVMBasicBlockRef
	storeType *semantic.PackedEnumStoreType
	state     C.LLVMValueRef
	origin    packedReadOriginKey
	index     C.LLVMValueRef
	offset    C.LLVMValueRef
}

type packedEnumStorageBinding struct {
	ptr C.LLVMValueRef
	typ *semantic.EnumType
}

type packedVariantViewBinding struct {
	ptr           C.LLVMValueRef
	handle        C.LLVMValueRef
	store         packedStoreBinding
	typ           *semantic.PackedVariantViewType
	payloadValues packedPayloadValueCache
}

type packedPayloadValueCache struct {
	name1  string
	value1 C.LLVMValueRef
	name2  string
	value2 C.LLVMValueRef
	values map[string]C.LLVMValueRef
}

type activePoolBinding struct {
	name    string
	ptr     C.LLVMValueRef
	typ     semantic.Type
	workers C.LLVMValueRef
}

const (
	branchWeightLikely   = 2000
	branchWeightUnlikely = 1
)

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
		g:       g,
		decl:    decl,
		fnValue: fnValue,
		fnType:  fnType,
		builder: builder,
		typeMap: typeBindings,
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
			if enumType, ok := declType.(*semantic.EnumType); ok && enumType.Packed {
				origin, ok, err := s.resolvePackedNodeStoreBinding(n.Value, enumType)
				if err != nil {
					return err
				}
				if ok {
					s.bindPackedEnumStoreOrigin(n.Name, enumType, origin)
				}
			}
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
		s.invalidatePackedEnumStoreOriginExpr(n.Target)
		s.invalidatePackedVariantViewExpr(n.Target)
		value, _, err := s.emitExpr(n.Value, targetType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, value, ptr)
		s.bindPackedStoreValue(targetType, value)
		s.invalidatePackedReadCaches()
		return nil
	case *ast.AsRefAssignStmt:
		ptr, targetType, err := s.emitAddress(n.Target)
		if err != nil {
			return err
		}
		s.invalidatePackedEnumStorageExpr(n.Target)
		s.invalidatePackedEnumStoreOriginExpr(n.Target)
		s.invalidatePackedVariantViewExpr(n.Target)
		value, _, err := s.emitExpr(n.Value, targetType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, value, ptr)
		s.bindPackedStoreValue(targetType, value)
		s.invalidatePackedReadCaches()
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
		s.invalidatePackedReadCaches()
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
	case *ast.ForStmt:
		return s.emitForStmt(n)
	case *ast.ParallelForStmt:
		return s.emitParallelForStmt(n)
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
		if err := s.emitMoveBindLocal(p.Name, valueType, value); err != nil {
			return err
		}
		if enumType, ok := valueType.(*semantic.EnumType); ok && enumType.Packed {
			origin, ok, err := s.resolvePackedNodeStoreBinding(stmt.Value, enumType)
			if err != nil {
				return err
			}
			if ok {
				s.bindPackedEnumStoreOrigin(p.Name, enumType, origin)
			}
		}
		return nil
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
		enumType, ok := resolveMatchableEnumType(valueType)
		if !ok {
			return fmt.Errorf("move-as variant pattern requires an enum value, got %s", valueType.String())
		}
		storeBinding, err := s.resolvePackedMatchStoreBinding(enumType, stmt.Value, stmt.Store)
		if err != nil {
			return err
		}
		successBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("move.as.variant.ok"))
		failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("move.as.variant.fail"))
		contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("move.as.variant.cont"))
		matchPattern := &ast.MatchVariantPattern{Position: p.Position, EnumName: p.EnumName, Variant: p.Variant, Args: append([]ast.MatchPatternArg(nil), p.Args...)}
		if _, _, err := s.emitMatchPatternTest(matchPattern, value, nil, enumType, storeBinding, stmt.Value, nil, successBB, failBB); err != nil {
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
	enumType, ok := resolveMatchableEnumType(s.exprType(stmt.Value))
	if !ok {
		return fmt.Errorf("open requires a packed enum value")
	}
	storeBinding, err := s.resolvePackedMatchStoreBinding(enumType, stmt.Value, stmt.Store)
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
	matchedDecodedValue, _, err := s.emitMatchPatternTest(matchPattern, enumValue, nil, enumType, storeBinding, stmt.Value, nil, successBB, failBB)
	if err != nil {
		s.popScope()
		return err
	}
	C.LLVMPositionBuilderAtEnd(s.builder, successBB)
	if ident, ok := stmt.Value.(*ast.Ident); ok && matchedDecodedValue != nil {
		s.bindPackedEnumStorage(ident.Name, enumType, matchedDecodedValue)
	}
	if storeBinding != nil {
		if key, ok := s.packedEnumStoragePath(stmt.Value); ok {
			s.bindPackedEnumStoreOrigin(key, enumType, storeBinding)
		}
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
	enumType, ok := resolveMatchableEnumType(s.exprType(stmt.Value))
	if !ok {
		return fmt.Errorf("view requires a packed enum value")
	}
	variant, ok := enumType.Variant(stmt.Pattern.Variant)
	if !ok {
		return fmt.Errorf("enum %s has no variant %s", enumType.Name, stmt.Pattern.Variant)
	}
	resolvedViewType := s.g.cachedPackedVariantViewType(enumType, variant)
	storeBinding, err := s.resolvePackedMatchStoreBinding(enumType, stmt.Value, stmt.Store)
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
	matchPattern := &ast.MatchVariantPattern{Position: stmt.Pattern.Position, EnumName: stmt.Pattern.EnumName, Variant: stmt.Pattern.Variant, Args: append([]ast.MatchPatternArg(nil), stmt.Pattern.Args...)}
	s.pushScope()
	matchedDecodedValue, matchedPayloadValues, err := s.emitMatchPatternTest(matchPattern, enumValue, nil, enumType, storeBinding, stmt.Value, nil, successBB, failBB)
	if err != nil {
		s.popScope()
		return err
	}
	C.LLVMPositionBuilderAtEnd(s.builder, successBB)
	viewDecodedValue := matchedDecodedValue
	needsDecodedView := true
	if packedModeUsesDenseIndexHandle(s.g.packedModeForEnum(enumType)) && storeBinding != nil {
		needsDecodedView = false
	} else if s.canInlinePackedEnumVariant(enumType, variant) {
		needsDecodedView = false
	}
	if viewDecodedValue == nil && needsDecodedView {
		viewDecodedValue, err = s.decodePackedEnumHandleWithStore(enumValue, enumType, storeBinding)
		if err != nil {
			s.popScope()
			return err
		}
	}
	if ident, ok := stmt.Value.(*ast.Ident); ok {
		storeValue := packedStoreBinding{}
		if storeBinding != nil {
			storeValue = *storeBinding
		}
		if viewDecodedValue != nil {
			s.bindPackedEnumStorage(ident.Name, enumType, viewDecodedValue)
		}
		if viewDecodedValue != nil {
			s.bindPackedVariantView(ident.Name, resolvedViewType, viewDecodedValue, enumValue, storeValue, matchedPayloadValues)
		} else if packedModeUsesDenseIndexHandle(s.g.packedModeForEnum(enumType)) || s.canInlinePackedEnumVariant(enumType, variant) {
			s.bindPackedVariantView(ident.Name, resolvedViewType, nil, enumValue, storeValue, matchedPayloadValues)
		}
	}
	if storeBinding != nil {
		if key, ok := s.packedEnumStoragePath(stmt.Value); ok {
			s.bindPackedEnumStoreOrigin(key, enumType, storeBinding)
		}
	}
	if stmt.Pattern.Name != "" && stmt.Pattern.Name != "_" {
		storeValue := packedStoreBinding{}
		if storeBinding != nil {
			storeValue = *storeBinding
		}
		if viewDecodedValue != nil {
			s.bindPackedVariantView(stmt.Pattern.Name, resolvedViewType, viewDecodedValue, enumValue, storeValue, matchedPayloadValues)
		} else if packedModeUsesDenseIndexHandle(s.g.packedModeForEnum(enumType)) || s.canInlinePackedEnumVariant(enumType, variant) {
			s.bindPackedVariantView(stmt.Pattern.Name, resolvedViewType, nil, enumValue, storeValue, matchedPayloadValues)
		}
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
	poolType := s.g.result.NamedTypes["ThreadPool"]
	usizeType := s.g.result.NamedTypes["usize"]
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
	s.defineBinding(stmt.Name, valueBinding{ptr: poolAlloca, typ: poolType})
	C.LLVMBuildStore(s.builder, poolValue, poolAlloca)
	pool := scopedCleanupBinding{kind: scopedCleanupThreadPool, name: stmt.Name, ptr: poolAlloca, typ: poolType}
	s.scopedCleanups = append(s.scopedCleanups, pool)
	s.poolScopes = append(s.poolScopes, activePoolBinding{name: stmt.Name, ptr: poolAlloca, typ: poolType, workers: workersValue})
	defer func() {
		s.scopedCleanups = s.scopedCleanups[:len(s.scopedCleanups)-1]
		s.poolScopes = s.poolScopes[:len(s.poolScopes)-1]
	}()
	if err := s.emitBlock(stmt.Body, false); err != nil {
		return err
	}
	if s.currentBlockTerminated() {
		return nil
	}
	return s.emitConditionalPoolShutdown(pool)
}

func (s *functionState) emitParallelForStmt(stmt *ast.ParallelForStmt) error {
	info, ok := s.g.result.ParallelFor[stmt]
	if !ok || info == nil {
		return fmt.Errorf("missing semantic parallel-for info")
	}
	pool, ok := s.currentActivePool()
	if !ok {
		return fmt.Errorf("parallel for requires an active pool scope during LLVM lowering")
	}
	sourceValue, _, err := s.emitExpr(stmt.Source, info.SourceType)
	if err != nil {
		return err
	}

	prefix := s.g.nextSyntheticName("__parallel_for_")
	sourceName := prefix + "_source"
	groupName := prefix + "_group"

	s.pushScope()
	defer s.popScope()

	sourceAlloca, err := s.createEntryAlloca(sourceName, info.SourceType)
	if err != nil {
		return err
	}
	C.LLVMBuildStore(s.builder, sourceValue, sourceAlloca)
	s.defineBinding(sourceName, valueBinding{ptr: sourceAlloca, typ: info.SourceType})

	sourceIdent := &ast.Ident{Position: stmt.Position, Name: sourceName}
	lengthField := "len"
	if _, ok := info.SourceType.(*semantic.PackedEnumStoreType); ok {
		lengthField = "count"
	}
	totalExpr := &ast.FieldExpr{Position: stmt.Position, Object: sourceIdent, Field: lengthField}
	usizeType := s.g.result.NamedTypes["usize"]
	totalValue, _, err := s.emitExpr(totalExpr, usizeType)
	if err != nil {
		return err
	}

	groupType := s.g.result.NamedTypes["TaskGroup"]
	groupAlloca, err := s.createEntryAlloca(groupName, groupType)
	if err != nil {
		return err
	}
	taskGroupNew, taskGroupNewType, err := s.ensureRuntimeFunction("task_group_new", nil)
	if err != nil {
		return err
	}
	taskGroupNewLLVMType, err := s.g.lowerFunctionType(taskGroupNewType)
	if err != nil {
		return err
	}
	groupValue := s.buildCall(taskGroupNewLLVMType, taskGroupNew, nil, "task.group.new")
	C.LLVMBuildStore(s.builder, groupValue, groupAlloca)
	s.defineBinding(groupName, valueBinding{ptr: groupAlloca, typ: groupType})

	workerFn, workerFnType, chunkType, err := s.emitParallelForWorkerFunction(stmt, info, prefix)
	if err != nil {
		return err
	}
	poolSubmit, poolSubmitType, err := s.ensureRuntimeFunction("pool_submit1", map[string]semantic.Type{"A": chunkType, "R": s.g.result.NamedTypes["void"]})
	if err != nil {
		return err
	}
	poolSubmitLLVMType, err := s.g.lowerFunctionType(poolSubmitType)
	if err != nil {
		return err
	}
	taskGroupAdd, taskGroupAddType, err := s.ensureRuntimeFunction("task_group_add", map[string]semantic.Type{"R": s.g.result.NamedTypes["void"]})
	if err != nil {
		return err
	}
	taskGroupAddLLVMType, err := s.g.lowerFunctionType(taskGroupAddType)
	if err != nil {
		return err
	}
	taskGroupWait, taskGroupWaitType, err := s.ensureRuntimeFunction("task_group_wait_all", nil)
	if err != nil {
		return err
	}
	taskGroupWaitLLVMType, err := s.g.lowerFunctionType(taskGroupWaitType)
	if err != nil {
		return err
	}

	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
	one := C.LLVMConstInt(usizeLLVMType, 1, 0)

	startAlloca, err := s.createEntryAlloca(prefix+"_start", usizeType)
	if err != nil {
		return err
	}
	C.LLVMBuildStore(s.builder, zero, startAlloca)

	hasWorkers := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), pool.workers, zero, cStringFree("parallel.workers.nonzero"))
	hasItems := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), totalValue, zero, cStringFree("parallel.total.nonzero"))
	shouldRun := C.LLVMBuildAnd(s.builder, hasWorkers, hasItems, cStringFree("parallel.should.run"))

	runBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("parallel.run"))
	loopCondBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("parallel.cond"))
	loopBodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("parallel.body"))
	waitBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("parallel.wait"))
	exitBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("parallel.end"))

	C.LLVMBuildCondBr(s.builder, shouldRun, runBB, exitBB)

	C.LLVMPositionBuilderAtEnd(s.builder, runBB)
	adjustedTotal := C.LLVMBuildAdd(s.builder, totalValue, C.LLVMBuildSub(s.builder, pool.workers, one, cStringFree("parallel.workers.minus.one")), cStringFree("parallel.total.adjusted"))
	chunkSize := C.LLVMBuildUDiv(s.builder, adjustedTotal, pool.workers, cStringFree("parallel.chunk.size"))
	C.LLVMBuildBr(s.builder, loopCondBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopCondBB)
	startValue, err := s.loadValue(startAlloca, usizeType, prefix+".start")
	if err != nil {
		return err
	}
	hasMore := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), startValue, totalValue, cStringFree("parallel.has.more"))
	C.LLVMBuildCondBr(s.builder, hasMore, loopBodyBB, waitBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopBodyBB)
	endCandidate := C.LLVMBuildAdd(s.builder, startValue, chunkSize, cStringFree("parallel.end.candidate"))
	endValue := s.emitUnsignedMin(endCandidate, totalValue, usizeLLVMType, "parallel.end")
	chunkValue, err := s.buildParallelForChunkValue(info, chunkType, sourceValue, startValue, endValue)
	if err != nil {
		return err
	}
	taskValue := s.buildCall(poolSubmitLLVMType, poolSubmit, []C.LLVMValueRef{pool.ptr, workerFn, chunkValue}, "parallel.submit")
	s.buildCall(taskGroupAddLLVMType, taskGroupAdd, []C.LLVMValueRef{groupAlloca, taskValue}, "")
	C.LLVMBuildStore(s.builder, endValue, startAlloca)
	C.LLVMBuildBr(s.builder, loopCondBB)

	C.LLVMPositionBuilderAtEnd(s.builder, waitBB)
	s.buildCall(taskGroupWaitLLVMType, taskGroupWait, []C.LLVMValueRef{groupAlloca}, "")
	C.LLVMBuildBr(s.builder, exitBB)

	C.LLVMPositionBuilderAtEnd(s.builder, exitBB)
	_ = workerFnType
	return nil
}

func (s *functionState) emitParallelForWorkerFunction(stmt *ast.ParallelForStmt, info *semantic.ParallelForInfo, prefix string) (C.LLVMValueRef, *semantic.FuncType, *semantic.StructType, error) {
	chunkType, err := s.buildParallelForChunkType(info, prefix)
	if err != nil {
		return nil, nil, nil, err
	}
	workerName := prefix + "_worker"
	voidType := s.g.result.NamedTypes["void"]
	workerType := &semantic.FuncType{Name: workerName, Params: []semantic.Type{chunkType}, Return: voidType}
	workerFn, err := s.g.addFunction(workerName, workerType)
	if err != nil {
		return nil, nil, nil, err
	}
	s.g.functions[workerName] = workerFn
	s.g.setDefinedFunctionLinkage(workerName, workerFn, workerType)

	chunkParamName := prefix + "_chunk"
	sourceLocalName := prefix + "_source"
	indexLocalName := prefix + "_index"
	limitLocalName := prefix + "_limit"

	chunkIdent := &ast.Ident{Position: stmt.Position, Name: chunkParamName}
	sourceDecl := &ast.VarDeclStmt{
		Position: stmt.Position,
		Name:     sourceLocalName,
		Value:    &ast.FieldExpr{Position: stmt.Position, Object: chunkIdent, Field: "source"},
	}
	limitDecl := &ast.VarDeclStmt{
		Position: stmt.Position,
		Name:     limitLocalName,
		Value:    &ast.FieldExpr{Position: stmt.Position, Object: chunkIdent, Field: "end"},
	}
	indexDecl := &ast.VarDeclStmt{
		Position: stmt.Position,
		Name:     indexLocalName,
		Mutable:  true,
		Type:     &ast.NamedType{Position: stmt.Position, Name: "usize"},
		Value:    &ast.FieldExpr{Position: stmt.Position, Object: chunkIdent, Field: "start"},
	}

	var body []ast.Stmt
	body = append(body, sourceDecl)
	for i, name := range info.Captures {
		body = append(body, &ast.VarDeclStmt{
			Position: stmt.Position,
			Name:     name,
			Value:    &ast.FieldExpr{Position: stmt.Position, Object: chunkIdent, Field: fmt.Sprintf("capture_%d", i)},
		})
	}
	body = append(body, limitDecl, indexDecl)

	condExpr := &ast.BinaryExpr{
		Position: stmt.Position,
		Op:       lexer.TOKEN_LT,
		Left:     &ast.Ident{Position: stmt.Position, Name: indexLocalName},
		Right:    &ast.Ident{Position: stmt.Position, Name: limitLocalName},
	}
	nodeDecl := &ast.VarDeclStmt{
		Position: stmt.Position,
		Name:     stmt.Name,
		Type:     &ast.NamedType{Position: stmt.Position, Name: info.ItemType.String()},
		Value: &ast.IndexExpr{
			Position: stmt.Position,
			Object:   &ast.Ident{Position: stmt.Position, Name: sourceLocalName},
			Index:    &ast.Ident{Position: stmt.Position, Name: indexLocalName},
		},
	}
	loopBody := make([]ast.Stmt, 0, 2+len(stmt.Body))
	if stmt.IndexName != "" {
		loopBody = append(loopBody, &ast.VarDeclStmt{
			Position: stmt.Position,
			Name:     stmt.IndexName,
			Type:     &ast.NamedType{Position: stmt.Position, Name: "usize"},
			Value:    &ast.Ident{Position: stmt.Position, Name: indexLocalName},
		})
	}
	loopBody = append(loopBody, nodeDecl)
	loopBody = append(loopBody, stmt.Body...)
	loopBody = append(loopBody, &ast.AugAssignStmt{
		Position: stmt.Position,
		Op:       lexer.TOKEN_PLUSEQ,
		Target:   &ast.Ident{Position: stmt.Position, Name: indexLocalName},
		Value:    &ast.IntLit{Position: stmt.Position, Value: "1", Suffix: "u"},
	})
	body = append(body, &ast.WhileStmt{Position: stmt.Position, Hint: ast.BranchHintNone, Cond: condExpr, Body: loopBody})

	s.g.result.ExprTypes[condExpr] = s.g.result.NamedTypes["bool"]

	workerDecl := &ast.FuncDecl{
		Position:   stmt.Position,
		Name:       workerName,
		Params:     []ast.ParamDecl{{Position: stmt.Position, Name: chunkParamName}},
		ReturnType: &ast.NamedType{Position: stmt.Position, Name: "void"},
		Body:       body,
	}
	if err := s.g.defineFunctionBody(workerDecl, workerType, workerFn); err != nil {
		return nil, nil, nil, err
	}
	return workerFn, workerType, chunkType, nil
}

func (s *functionState) buildParallelForChunkType(info *semantic.ParallelForInfo, prefix string) (*semantic.StructType, error) {
	fields := map[string]semantic.Field{
		"source": {Name: "source", Type: info.SourceType},
		"start":  {Name: "start", Type: s.g.result.NamedTypes["usize"]},
		"end":    {Name: "end", Type: s.g.result.NamedTypes["usize"]},
	}
	declFields := []ast.FieldDecl{
		{Position: lexer.Pos{}, Name: "source"},
		{Position: lexer.Pos{}, Name: "start"},
		{Position: lexer.Pos{}, Name: "end"},
	}
	for i, name := range info.Captures {
		binding, ok := s.lookupBinding(name)
		if !ok {
			return nil, fmt.Errorf("missing parallel-for capture binding %q during chunk type lowering", name)
		}
		fieldName := fmt.Sprintf("capture_%d", i)
		fields[fieldName] = semantic.Field{Name: fieldName, Type: binding.typ}
		declFields = append(declFields, ast.FieldDecl{Position: lexer.Pos{}, Name: fieldName})
	}
	decl := &ast.StructDecl{Position: lexer.Pos{}, Name: prefix + "_chunk", Fields: declFields, ReprC: true}
	return &semantic.StructType{
		Name:   decl.Name,
		Fields: fields,
		ReprC:  true,
		Decl:   decl,
	}, nil
}

func (s *functionState) buildParallelForChunkValue(info *semantic.ParallelForInfo, chunkType *semantic.StructType, sourceValue, startValue, endValue C.LLVMValueRef) (C.LLVMValueRef, error) {
	chunkLLVMType, err := s.g.lowerType(chunkType)
	if err != nil {
		return nil, err
	}
	chunkValue := C.LLVMGetUndef(chunkLLVMType)
	chunkValue = C.LLVMBuildInsertValue(s.builder, chunkValue, sourceValue, 0, cStringFree("parallel.chunk.source"))
	chunkValue = C.LLVMBuildInsertValue(s.builder, chunkValue, startValue, 1, cStringFree("parallel.chunk.start"))
	chunkValue = C.LLVMBuildInsertValue(s.builder, chunkValue, endValue, 2, cStringFree("parallel.chunk.end"))
	for i, name := range info.Captures {
		binding, ok := s.lookupBinding(name)
		if !ok {
			return nil, fmt.Errorf("missing capture binding %q during parallel-for lowering", name)
		}
		value, err := s.loadValue(binding.ptr, binding.typ, name)
		if err != nil {
			return nil, err
		}
		chunkValue = C.LLVMBuildInsertValue(s.builder, chunkValue, value, C.unsigned(3+i), cStringFree("parallel.chunk.capture"))
	}
	return chunkValue, nil
}

func (s *functionState) ensureRuntimeFunction(name string, bindings map[string]semantic.Type) (C.LLVMValueRef, *semantic.FuncType, error) {
	sym, ok := s.g.result.GlobalScope.Lookup(name)
	if !ok {
		return nil, nil, fmt.Errorf("missing runtime function %q", name)
	}
	fnType, ok := sym.Type.(*semantic.FuncType)
	if !ok {
		return nil, nil, fmt.Errorf("runtime symbol %q is not a function", name)
	}
	if decl, ok := sym.Node.(*ast.FuncDecl); ok && len(funcGenericParams(fnType)) != 0 {
		value, lowered, err := s.g.ensureSpecializedFunction(decl, fnType, bindings)
		return value, lowered, err
	}
	lowered := specializeFuncType(fnType, bindings)
	value, err := s.g.ensureFunctionDeclared(name, lowered)
	return value, lowered, err
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

func (s *functionState) attachBranchHintMetadata(branch C.LLVMValueRef, hint ast.BranchHint) {
	if s == nil || s.g == nil || branch == nil || hint == ast.BranchHintNone {
		return
	}
	trueWeight := branchWeightLikely
	falseWeight := branchWeightUnlikely
	if hint == ast.BranchHintUnlikely {
		trueWeight, falseWeight = falseWeight, trueWeight
	}
	C.llctxSetBranchWeights(branch, s.g.context, C.uint(trueWeight), C.uint(falseWeight))
}

func (s *functionState) buildCondBrWithHint(condValue C.LLVMValueRef, trueBB C.LLVMBasicBlockRef, falseBB C.LLVMBasicBlockRef, hint ast.BranchHint) {
	branch := C.LLVMBuildCondBr(s.builder, condValue, trueBB, falseBB)
	s.attachBranchHintMetadata(branch, hint)
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
		s.buildCondBrWithHint(condValue, thenBB, elseBB, stmt.Hint)
	} else {
		s.buildCondBrWithHint(condValue, thenBB, mergeBB, stmt.Hint)
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
	s.buildCondBrWithHint(condValue, bodyBB, exitBB, stmt.Hint)

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

func (s *functionState) emitForStmt(stmt *ast.ForStmt) error {
	loopType := s.forLoopValueType(stmt)
	if loopType == nil {
		return fmt.Errorf("missing semantic type for for-loop")
	}
	startValue, _, err := s.emitExpr(stmt.Start, loopType)
	if err != nil {
		return err
	}
	endValue, _, err := s.emitExpr(stmt.End, loopType)
	if err != nil {
		return err
	}
	stepValue, err := s.emitForLoopStepMagnitude(stmt, loopType)
	if err != nil {
		return err
	}
	loopLLVMType, err := s.g.lowerType(loopType)
	if err != nil {
		return err
	}
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	zeroValue := C.LLVMConstInt(loopLLVMType, 0, 0)

	var ascendingValue C.LLVMValueRef
	switch stmt.Op {
	case lexer.TOKEN_RANGE:
		pred := C.LLVMIntPredicate(C.LLVMIntULE)
		if isSignedIntegerType(loopType) {
			pred = C.LLVMIntPredicate(C.LLVMIntSLE)
		}
		ascendingValue = C.LLVMBuildICmp(s.builder, pred, startValue, endValue, cStringFree("for.asc"))
	case lexer.TOKEN_RANGE_LT:
		ascendingValue = C.LLVMConstInt(boolType, 1, 0)
	case lexer.TOKEN_RANGE_GT:
		ascendingValue = C.LLVMConstInt(boolType, 0, 0)
	default:
		return fmt.Errorf("unsupported for-loop range operator %s", lexer.TokenName(stmt.Op))
	}
	hasStep := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), stepValue, zeroValue, cStringFree("for.step.nonzero"))

	currentAlloca, err := s.createEntryAlloca(stmt.Name+".for.cur", loopType)
	if err != nil {
		return err
	}
	C.LLVMBuildStore(s.builder, startValue, currentAlloca)
	loopVarAlloca, err := s.createEntryAlloca(stmt.Name, loopType)
	if err != nil {
		return err
	}

	condBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("for.cond"))
	bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("for.body"))
	exitBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("for.end"))

	C.LLVMBuildCondBr(s.builder, hasStep, condBB, exitBB)

	C.LLVMPositionBuilderAtEnd(s.builder, condBB)
	currentValue, err := s.loadValue(currentAlloca, loopType, stmt.Name+".for.cur")
	if err != nil {
		return err
	}
	ascendingCond, err := s.emitForLoopContinueCmp(stmt.Op, loopType, currentValue, endValue, true)
	if err != nil {
		return err
	}
	descendingCond, err := s.emitForLoopContinueCmp(stmt.Op, loopType, currentValue, endValue, false)
	if err != nil {
		return err
	}
	condValue := C.LLVMBuildSelect(s.builder, ascendingValue, ascendingCond, descendingCond, cStringFree("for.cond.select"))
	C.LLVMBuildCondBr(s.builder, condValue, bodyBB, exitBB)

	C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
	s.pushScope()
	s.defineBinding(stmt.Name, valueBinding{ptr: loopVarAlloca, typ: loopType})
	C.LLVMBuildStore(s.builder, currentValue, loopVarAlloca)
	if err := s.emitBlock(stmt.Body, true); err != nil {
		s.popScope()
		return err
	}
	s.popScope()
	if !s.currentBlockTerminated() {
		nextAscending := C.LLVMBuildAdd(s.builder, currentValue, stepValue, cStringFree("for.next.asc"))
		nextDescending := C.LLVMBuildSub(s.builder, currentValue, stepValue, cStringFree("for.next.desc"))
		nextValue := C.LLVMBuildSelect(s.builder, ascendingValue, nextAscending, nextDescending, cStringFree("for.next"))
		C.LLVMBuildStore(s.builder, nextValue, currentAlloca)
		C.LLVMBuildBr(s.builder, condBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, exitBB)
	return nil
}

func (s *functionState) forLoopValueType(stmt *ast.ForStmt) semantic.Type {
	if stmt == nil {
		return nil
	}
	loopType := semantic.CommonNumericType(s.exprType(stmt.Start), s.exprType(stmt.End))
	if stmt.Step != nil {
		loopType = semantic.CommonNumericType(loopType, s.exprType(stmt.Step))
	}
	return loopType
}

func (s *functionState) emitForLoopStepMagnitude(stmt *ast.ForStmt, loopType semantic.Type) (C.LLVMValueRef, error) {
	loopLLVMType, err := s.g.lowerType(loopType)
	if err != nil {
		return nil, err
	}
	if stmt.Step == nil {
		return C.LLVMConstInt(loopLLVMType, 1, 0), nil
	}
	rawStep, _, err := s.emitExpr(stmt.Step, loopType)
	if err != nil {
		return nil, err
	}
	if !isSignedIntegerType(loopType) {
		return rawStep, nil
	}
	zeroValue := C.LLVMConstInt(loopLLVMType, 0, 0)
	isNegative := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntSLT), rawStep, zeroValue, cStringFree("for.step.neg"))
	negated := C.LLVMBuildNeg(s.builder, rawStep, cStringFree("for.step.abs.neg"))
	return C.LLVMBuildSelect(s.builder, isNegative, negated, rawStep, cStringFree("for.step.abs")), nil
}

func (s *functionState) emitForLoopContinueCmp(op lexer.TokenKind, loopType semantic.Type, currentValue, endValue C.LLVMValueRef, ascending bool) (C.LLVMValueRef, error) {
	var pred C.LLVMIntPredicate
	signed := isSignedIntegerType(loopType)
	switch op {
	case lexer.TOKEN_RANGE:
		if ascending {
			if signed {
				pred = C.LLVMIntPredicate(C.LLVMIntSLE)
			} else {
				pred = C.LLVMIntPredicate(C.LLVMIntULE)
			}
		} else {
			if signed {
				pred = C.LLVMIntPredicate(C.LLVMIntSGE)
			} else {
				pred = C.LLVMIntPredicate(C.LLVMIntUGE)
			}
		}
	case lexer.TOKEN_RANGE_LT:
		if signed {
			pred = C.LLVMIntPredicate(C.LLVMIntSLT)
		} else {
			pred = C.LLVMIntPredicate(C.LLVMIntULT)
		}
	case lexer.TOKEN_RANGE_GT:
		if signed {
			pred = C.LLVMIntPredicate(C.LLVMIntSGT)
		} else {
			pred = C.LLVMIntPredicate(C.LLVMIntUGT)
		}
	default:
		return nil, fmt.Errorf("unsupported for-loop range operator %s", lexer.TokenName(op))
	}
	return C.LLVMBuildICmp(s.builder, pred, currentValue, endValue, cStringFree("for.cmp")), nil
}

func (s *functionState) bindMatchedPackedVariantView(name string, pattern ast.MatchPattern, enumValue C.LLVMValueRef, decodedValue C.LLVMValueRef, enumType *semantic.EnumType, store *packedStoreBinding, payloadValues packedPayloadValueCache) {
	if enumType == nil || !enumType.Packed {
		return
	}
	if name == "" {
		return
	}
	variantPattern, ok := pattern.(*ast.MatchVariantPattern)
	if !ok {
		return
	}
	variant, ok := enumType.Variant(variantPattern.Variant)
	if !ok || variant == nil {
		return
	}
	viewType := s.g.cachedPackedVariantViewType(enumType, variant)
	if decodedValue != nil {
		storeValue := packedStoreBinding{}
		if store != nil {
			storeValue = *store
		}
		s.bindPackedVariantViewOwned(name, viewType, decodedValue, enumValue, storeValue, payloadValues)
		return
	}
	if store != nil {
		s.bindPackedVariantViewOwned(name, viewType, nil, enumValue, *store, payloadValues)
		return
	}
	if s.canInlinePackedEnumVariant(enumType, variant) {
		s.bindPackedVariantViewOwned(name, viewType, nil, enumValue, packedStoreBinding{}, payloadValues)
	}
}

func resolveMatchableEnumType(actual semantic.Type) (*semantic.EnumType, bool) {
	switch tt := actual.(type) {
	case *semantic.EnumType:
		return tt, tt != nil
	case *semantic.PackedVariantViewType:
		if tt == nil || tt.Enum == nil {
			return nil, false
		}
		return tt.Enum, true
	default:
		return nil, false
	}
}

func runtimeStringLiteralType() semantic.Type {
	return &semantic.RefType{Elem: &semantic.BuiltinType{Name: "u8"}, State: semantic.RefStateNonNull, Storage: semantic.RefStorageStatic, ExplicitStorage: true}
}

func isStringMatchableType(actual semantic.Type) bool {
	_, _, _, _, ok := runtimeStringCompareInfo(actual, runtimeStringLiteralType())
	return ok
}

func (s *functionState) emitMatch(stmt *ast.MatchStmt) error {
	enumType, ok := resolveMatchableEnumType(s.exprType(stmt.Value))
	if ok {
		return s.emitEnumMatch(stmt, enumType)
	}
	if isStringMatchableType(s.exprType(stmt.Value)) {
		return s.emitStringMatch(stmt)
	}
	return fmt.Errorf("match requires an enum or string value")
}

func (s *functionState) emitEnumMatch(stmt *ast.MatchStmt, enumType *semantic.EnumType) error {
	storeBinding, err := s.resolvePackedMatchStoreBinding(enumType, stmt.Value, stmt.Store)
	if err != nil {
		return err
	}
	enumValue, _, err := s.emitExpr(stmt.Value, enumType)
	if err != nil {
		return err
	}
	var decodedMatchValue C.LLVMValueRef
	if enumType.Packed && packedMatchShouldEagerDecode(s.g.result, s.g.packedModeForEnum(enumType), enumType, stmt.Value, storeBinding, stmt.Arms) {
		decodedMatchValue, err = s.decodePackedEnumHandleWithStore(enumValue, enumType, storeBinding)
		if err != nil {
			return err
		}
	}
	matchTagValue, err := s.extractEnumTagValue(enumValue, decodedMatchValue, enumType, storeBinding)
	if err != nil {
		return err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.fail"))
	allTerminated := true
	valuePath, hasValuePath := s.packedEnumStoragePath(stmt.Value)

	for i, arm := range stmt.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(stmt.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.next"))
		}
		armDecodedValue, armPayloadValues, err := s.emitMatchPatternTest(arm.Pattern, enumValue, decodedMatchValue, enumType, storeBinding, stmt.Value, matchTagValue, bodyBB, nextBB)
		if err != nil {
			return err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if hasValuePath && enumType.Packed && armDecodedValue != nil {
			s.bindPackedEnumStorage(valuePath, enumType, armDecodedValue)
		}
		if hasValuePath && enumType.Packed {
			s.bindPackedEnumStoreOrigin(valuePath, enumType, storeBinding)
		}
		s.bindMatchedPackedVariantView(valuePath, arm.Pattern, enumValue, armDecodedValue, enumType, storeBinding, armPayloadValues)
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
	enumType, ok := resolveMatchableEnumType(s.exprType(expr.Value))
	if ok {
		return s.emitEnumMatchExpr(expr, resultType, enumType)
	}
	if isStringMatchableType(s.exprType(expr.Value)) {
		return s.emitStringMatchExpr(expr, resultType)
	}
	return nil, nil, fmt.Errorf("match requires an enum or string value")
}

func (s *functionState) emitEnumMatchExpr(expr *ast.MatchExpr, resultType semantic.Type, enumType *semantic.EnumType) (C.LLVMValueRef, semantic.Type, error) {
	storeBinding, err := s.resolvePackedMatchStoreBinding(enumType, expr.Value, expr.Store)
	if err != nil {
		return nil, nil, err
	}
	enumValue, _, err := s.emitExpr(expr.Value, enumType)
	if err != nil {
		return nil, nil, err
	}
	var decodedMatchValue C.LLVMValueRef
	if enumType.Packed && packedMatchShouldEagerDecode(s.g.result, s.g.packedModeForEnum(enumType), enumType, expr.Value, storeBinding, expr.Arms) {
		decodedMatchValue, err = s.decodePackedEnumHandleWithStore(enumValue, enumType, storeBinding)
		if err != nil {
			return nil, nil, err
		}
	}
	matchTagValue, err := s.extractEnumTagValue(enumValue, decodedMatchValue, enumType, storeBinding)
	if err != nil {
		return nil, nil, err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(expr.Arms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(expr.Arms)+1)
	valuePath, hasValuePath := s.packedEnumStoragePath(expr.Value)
	valueIdent, hasValueIdent := expr.Value.(*ast.Ident)
	for i, arm := range expr.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(expr.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.next"))
		}
		armDecodedValue, armPayloadValues, err := s.emitMatchPatternTest(arm.Pattern, enumValue, decodedMatchValue, enumType, storeBinding, expr.Value, matchTagValue, bodyBB, nextBB)
		if err != nil {
			return nil, nil, err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if hasValueIdent && enumType.Packed && armDecodedValue != nil {
			s.bindPackedEnumStorage(valueIdent.Name, enumType, armDecodedValue)
		}
		if hasValuePath && enumType.Packed {
			s.bindPackedEnumStoreOrigin(valuePath, enumType, storeBinding)
		}
		s.bindMatchedPackedVariantView(valuePath, arm.Pattern, enumValue, armDecodedValue, enumType, storeBinding, armPayloadValues)
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

func matchHasWildcard(arms []ast.MatchArm) bool {
	for _, arm := range arms {
		if _, ok := arm.Pattern.(*ast.MatchWildcardPattern); ok {
			return true
		}
	}
	return false
}

func (s *functionState) emitStringMatch(stmt *ast.MatchStmt) error {
	actualType := s.exprType(stmt.Value)
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
		if err := s.emitStringMatchPatternTest(arm.Pattern, stmt.Value, actualType, bodyBB, nextBB); err != nil {
			return err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
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

	hasWildcard := matchHasWildcard(stmt.Arms)
	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if hasWildcard {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if allTerminated && hasWildcard {
		C.LLVMBuildUnreachable(s.builder)
	}
	return nil
}

func (s *functionState) emitStringMatchExpr(expr *ast.MatchExpr, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	actualType := s.exprType(expr.Value)
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(expr.Arms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(expr.Arms)+1)
	for i, arm := range expr.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(expr.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.next"))
		}
		if err := s.emitStringMatchPatternTest(arm.Pattern, expr.Value, actualType, bodyBB, nextBB); err != nil {
			return nil, nil, err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
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

	hasWildcard := matchHasWildcard(expr.Arms)
	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if hasWildcard || semantic.IsNeverType(resultType) {
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

func (s *functionState) emitMatchPatternTest(pattern ast.MatchPattern, actualValue C.LLVMValueRef, decodedActualValue C.LLVMValueRef, actualType semantic.Type, store *packedStoreBinding, originExpr ast.Expr, precomputedTagValue C.LLVMValueRef, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) (C.LLVMValueRef, packedPayloadValueCache, error) {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		C.LLVMBuildBr(s.builder, successBB)
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchBindPattern:
		alloca, err := s.createEntryAlloca(p.Name, actualType)
		if err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		C.LLVMBuildStore(s.builder, actualValue, alloca)
		s.defineBinding(p.Name, valueBinding{ptr: alloca, typ: actualType})
		if enumType, ok := actualType.(*semantic.EnumType); ok && enumType.Packed && decodedActualValue != nil {
			s.bindPackedEnumStorage(p.Name, enumType, decodedActualValue)
		}
		if enumType, ok := actualType.(*semantic.EnumType); ok && enumType.Packed {
			s.bindPackedEnumStoreOrigin(p.Name, enumType, store)
		}
		C.LLVMBuildBr(s.builder, successBB)
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchVariantPattern:
		enumType, ok := actualType.(*semantic.EnumType)
		if !ok {
			return nil, packedPayloadValueCache{}, fmt.Errorf("variant pattern %s.%s requires enum type, got %s", p.EnumName, p.Variant, actualType.String())
		}
		variant, ok := enumType.Variant(p.Variant)
		if !ok {
			return nil, packedPayloadValueCache{}, fmt.Errorf("enum %s has no variant %s", enumType.Name, p.Variant)
		}
		tagValue := precomputedTagValue
		if tagValue == nil {
			var err error
			tagValue, err = s.extractEnumTagValue(actualValue, decodedActualValue, enumType, store)
			if err != nil {
				return nil, packedPayloadValueCache{}, err
			}
		}
		tagConst, err := s.enumTagConstant(variant.Tag)
		if err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		matchedBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.pattern.ok"))
		pred := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), tagValue, tagConst, cStringFree("match.tag"))
		C.LLVMBuildCondBr(s.builder, pred, matchedBB, failureBB)

		C.LLVMPositionBuilderAtEnd(s.builder, matchedBB)
		matchedDecodedValue := decodedActualValue
		if len(p.Args) == 0 {
			C.LLVMBuildBr(s.builder, successBB)
			return matchedDecodedValue, packedPayloadValueCache{}, nil
		}
		namedCount := 0
		for i := range p.Args {
			if p.Args[i].Name != "" {
				namedCount++
			}
		}
		if namedCount == 0 {
			if len(p.Args) != len(variant.Payload) {
				return nil, packedPayloadValueCache{}, fmt.Errorf("match arm %s.%s expects %d payload patterns, got %d", p.EnumName, p.Variant, len(variant.Payload), len(p.Args))
			}
			hasNestedPattern := false
			for i := range p.Args {
				if p.Args[i].Pattern != nil {
					hasNestedPattern = true
					break
				}
			}
			if !hasNestedPattern {
				C.LLVMBuildBr(s.builder, successBB)
				return matchedDecodedValue, packedPayloadValueCache{}, nil
			}
			payloadValues, err := s.extractEnumVariantPayloadValues(actualValue, matchedDecodedValue, enumType, variant, store, originExpr)
			if err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			var cachedPayloads packedPayloadValueCache
			for i, value := range payloadValues {
				if value == nil {
					continue
				}
				cachedPayloads.add(variant.PayloadLabel(i), value)
			}
			for i := range p.Args {
				if p.Args[i].Pattern == nil {
					continue
				}
				nextSuccess := successBB
				if i != len(p.Args)-1 {
					nextSuccess = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.pattern.next"))
				}
				if _, _, err := s.emitMatchPatternTest(p.Args[i].Pattern, payloadValues[i], nil, variant.Payload[i], store, nil, nil, nextSuccess, failureBB); err != nil {
					return nil, packedPayloadValueCache{}, err
				}
				if i != len(p.Args)-1 {
					C.LLVMPositionBuilderAtEnd(s.builder, nextSuccess)
				}
			}
			return matchedDecodedValue, cachedPayloads, nil
		}
		if namedCount != len(p.Args) {
			return nil, packedPayloadValueCache{}, fmt.Errorf("match arm %s.%s cannot mix positional and named payload patterns", p.EnumName, p.Variant)
		}
		orderedArgs, err := s.resolveMatchPatternArgs(p, variant)
		if err != nil {
			return nil, packedPayloadValueCache{}, err
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
			return matchedDecodedValue, packedPayloadValueCache{}, nil
		}
		payloadValues, err := s.extractEnumVariantPayloadValues(actualValue, matchedDecodedValue, enumType, variant, store, originExpr)
		if err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		var cachedPayloads packedPayloadValueCache
		for i, value := range payloadValues {
			if value == nil {
				continue
			}
			cachedPayloads.add(variant.PayloadLabel(i), value)
		}
		for i := range orderedArgs {
			if orderedArgs[i] == nil {
				continue
			}
			nextSuccess := successBB
			if i != len(orderedArgs)-1 {
				nextSuccess = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.pattern.next"))
			}
			if _, _, err := s.emitMatchPatternTest(orderedArgs[i].Pattern, payloadValues[i], nil, variant.Payload[i], store, nil, nil, nextSuccess, failureBB); err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			if i != len(orderedArgs)-1 {
				C.LLVMPositionBuilderAtEnd(s.builder, nextSuccess)
			}
		}
		return matchedDecodedValue, cachedPayloads, nil
	default:
		return nil, packedPayloadValueCache{}, fmt.Errorf("unsupported match pattern %T", pattern)
	}
}

func (s *functionState) emitStringMatchPatternTest(pattern ast.MatchPattern, actualExpr ast.Expr, actualType semantic.Type, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) error {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		C.LLVMBuildBr(s.builder, successBB)
		return nil
	case *ast.MatchStringLiteralPattern:
		literalExpr := &ast.StringLit{Position: p.Pos(), Value: p.Value}
		literalType := runtimeStringLiteralType()
		helperName, firstType, secondType, swap, ok := runtimeStringCompareInfo(actualType, literalType)
		if !ok {
			return fmt.Errorf("string match pattern requires a string value, got %s", actualType.String())
		}
		synthetic := &ast.BinaryExpr{Position: p.Pos(), Op: lexer.TOKEN_EQEQ, Left: actualExpr, Right: literalExpr}
		cmp, _, err := s.emitRuntimeStringCompareExpr(synthetic, helperName, firstType, secondType, swap)
		if err != nil {
			return err
		}
		C.LLVMBuildCondBr(s.builder, cmp, successBB, failureBB)
		return nil
	default:
		return fmt.Errorf("unsupported string match pattern %T", pattern)
	}
}

func (s *functionState) resolveMatchPatternArgs(pattern *ast.MatchVariantPattern, variant *semantic.EnumVariant) ([]*ast.MatchPatternArg, error) {
	if len(pattern.ResolvedArgs) == len(variant.Payload) {
		return pattern.ResolvedArgs, nil
	}
	ordered := make([]*ast.MatchPatternArg, len(variant.Payload))
	if len(pattern.Args) == 0 {
		pattern.ResolvedArgs = ordered
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
		pattern.ResolvedArgs = ordered
		return ordered, nil
	}
	if namedCount != len(pattern.Args) {
		return nil, fmt.Errorf("match arm %s.%s cannot mix positional and named payload patterns", pattern.EnumName, pattern.Variant)
	}
	if !variant.HasNamedPayloads() {
		return nil, fmt.Errorf("match arm %s.%s uses named payload patterns but the variant payloads are unnamed", pattern.EnumName, pattern.Variant)
	}
	seen := make([]bool, len(variant.Payload))
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
	pattern.ResolvedArgs = ordered
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

func (s *functionState) extractEnumVariantPayloadValues(enumValue C.LLVMValueRef, decodedEnumValue C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant, store *packedStoreBinding, originExpr ast.Expr) ([]C.LLVMValueRef, error) {
	if variant == nil || len(variant.Payload) == 0 {
		return nil, nil
	}
	origin := packedReadOriginKey{}
	if originExpr != nil {
		resolvedOrigin, ok, err := s.packedReadOriginKey(originExpr)
		if err != nil {
			return nil, err
		}
		if ok {
			origin = resolvedOrigin
		}
	}
	if enumType != nil && enumType.Packed {
		return s.loadEnumVariantPayload(decodedEnumValue, enumValue, enumType, variant, store, origin)
	}
	enumPtr, err := s.emitStackTempValue(enumValue, enumType, "match.payload.tmp")
	if err != nil {
		return nil, err
	}
	return s.loadEnumVariantPayload(nil, enumPtr, enumType, variant, store, packedReadOriginKey{})
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
		} else {
			if ops, ok := s.packedStoreOpsFromBinding(store); ok && ops.canDirectTagRead() {
				return ops.storeTagAt(enumPtr, enumType, "packed.tag.store")
			}
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

func (s *functionState) readInlineWordHandlePayload(handleValue C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant) ([]C.LLVMValueRef, bool, error) {
	if !s.canInlinePackedEnumVariant(enumType, variant) || len(variant.Payload) != 1 {
		return nil, false, nil
	}
	uintptrLLVMType, err := s.g.lowerBuiltin("uintptr")
	if err != nil {
		return nil, false, err
	}
	payloadLLVMType, err := s.g.lowerType(variant.Payload[0])
	if err != nil {
		return nil, false, err
	}
	shifted := C.LLVMBuildLShr(s.builder, handleValue, C.LLVMConstInt(uintptrLLVMType, 1, 0), cStringFree("packed.inline.payload.bits"))
	masked := C.LLVMBuildAnd(s.builder, shifted, C.LLVMConstInt(uintptrLLVMType, C.ulonglong(0x0000ffffffffffff), 0), cStringFree("packed.inline.payload.mask"))
	value := C.LLVMBuildTrunc(s.builder, masked, payloadLLVMType, cStringFree("packed.inline.payload.value"))
	return []C.LLVMValueRef{value}, true, nil
}

func (s *functionState) loadEnumVariantPayload(decodedEnumPtr C.LLVMValueRef, enumPtr C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant, store *packedStoreBinding, origin packedReadOriginKey) ([]C.LLVMValueRef, error) {
	if variant == nil || len(variant.Payload) == 0 {
		return nil, nil
	}
	if enumType != nil && enumType.Packed {
		if decodedEnumPtr != nil {
			enumPtr = decodedEnumPtr
		} else {
			values, ok, inlineErr := s.readInlineWordHandlePayload(enumPtr, enumType, variant)
			if inlineErr != nil {
				return nil, inlineErr
			}
			if ok {
				return values, nil
			}
			values, ok, readErr := s.readPackedEnumVariantPayloadWithStore(enumPtr, enumType, variant, store, origin)
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

func (s *functionState) readPackedEnumVariantPayloadWithStore(handleValue C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant, store *packedStoreBinding, origin packedReadOriginKey) ([]C.LLVMValueRef, bool, error) {
	if enumType == nil || !enumType.Packed || variant == nil || len(variant.Payload) == 0 {
		return nil, false, nil
	}
	if values, ok, err := s.readInlineWordHandlePayload(handleValue, enumType, variant); err != nil {
		return nil, false, err
	} else if ok {
		return values, true, nil
	}
	ops, ok := s.packedStoreOpsFromBinding(store)
	if !ok {
		return nil, false, nil
	}
	tailIndex, hasTail := variant.TailPayloadIndex()
	var tailValue C.LLVMValueRef
	if hasTail {
		var err error
		tailValue, ok, err = ops.loadTailView(handleValue, enumType, variant, tailIndex, "packed.payload.tail")
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
	} else if !ops.canDirectWordRead() {
		return nil, false, nil
	}
	values := make([]C.LLVMValueRef, 0, len(variant.Payload))
	uintptrType := s.g.result.NamedTypes["uintptr"]
	for payloadIndex, payloadType := range variant.Payload {
		if hasTail && payloadIndex == tailIndex {
			values = append(values, tailValue)
			continue
		}
		wordOffset, ok, err := s.packedEnumVariantPayloadWordOffset(enumType, variant, payloadIndex)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		wordValue, err := ops.loadPayloadWordAtOrigin(handleValue, enumType, wordOffset, origin, "packed.payload.word")
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

func (s *functionState) packedEnumVariantPayloadWordOffset(enumType *semantic.EnumType, variant *semantic.EnumVariant, payloadIndex int) (C.LLVMValueRef, bool, error) {
	if enumType == nil || !enumType.Packed || variant == nil || payloadIndex < 0 || payloadIndex >= len(variant.Payload) {
		return nil, false, nil
	}
	wordBytes := uint64(s.g.wordBits / 8)
	if wordBytes == 0 {
		wordBytes = 8
	}
	payloadElemType := variant.Payload[payloadIndex]
	sizeBytes, err := s.g.abiSizeOfType(payloadElemType)
	if err != nil {
		return nil, false, err
	}
	if sizeBytes == 0 || sizeBytes > wordBytes {
		return nil, false, nil
	}
	payloadFieldIndex, err := s.g.packedEnumPayloadFieldIndex(enumType)
	if err != nil {
		return nil, false, err
	}
	rowType, err := s.g.ensurePackedEnumStorageType(enumType)
	if err != nil {
		return nil, false, err
	}
	baseOffsetBytes, err := s.g.abiOffsetOfLLVMElement(rowType, payloadFieldIndex)
	if err != nil {
		return nil, false, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, false, err
	}
	baseWordOffset := C.LLVMConstInt(usizeType, C.ulonglong(baseOffsetBytes/wordBytes), 0)
	if len(variant.Payload) == 1 {
		return baseWordOffset, true, nil
	}
	payloadType, err := s.g.lowerEnumVariantPayloadType(variant)
	if err != nil {
		return nil, false, err
	}
	fieldOffsetBytes, err := s.g.abiOffsetOfLLVMElement(payloadType, payloadIndex)
	if err != nil {
		return nil, false, err
	}
	return C.LLVMConstInt(usizeType, C.ulonglong((baseOffsetBytes+fieldOffsetBytes)/wordBytes), 0), true, nil
}

func (s *functionState) readPackedEnumTagWithStore(handleValue C.LLVMValueRef, enumType *semantic.EnumType, store *packedStoreBinding) (C.LLVMValueRef, error) {
	ops, ok := s.packedStoreOpsFromBinding(store)
	if !ok {
		return nil, fmt.Errorf("packed enum %s word-handle tag read requires store context", enumType.Name)
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

func packedMatchShouldEagerDecode(result *semantic.Result, abi packedEnumABIMode, enumType *semantic.EnumType, matchValue ast.Expr, store *packedStoreBinding, arms []ast.MatchArm) bool {
	if abi == packedEnumABIWordHandle && enumType != nil && enumType.HasInlineWordHandleVariant() {
		return false
	}
	needsPayloadDecode := packedMatchNeedsEagerDecode(arms)
	ident, ok := matchValue.(*ast.Ident)
	readsMatchedValueField := ok && matchArmsReadMatchedValueField(ident.Name, arms)
	if !needsPayloadDecode && !readsMatchedValueField {
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
	case *ast.ReturnStmt:
		return exprReadsMatchedValueField(name, n.Value)
	case *ast.IfStmt:
		return exprReadsMatchedValueField(name, n.Cond) || stmtsReadMatchedValueField(name, n.Then) || stmtsReadMatchedValueField(name, n.Else) || elifsReadMatchedValueField(name, n.Elifs)
	case *ast.WhileStmt:
		return exprReadsMatchedValueField(name, n.Cond) || stmtsReadMatchedValueField(name, n.Body)
	case *ast.ForStmt:
		return exprReadsMatchedValueField(name, n.Start) || exprReadsMatchedValueField(name, n.End) || exprReadsMatchedValueField(name, n.Step) || stmtsReadMatchedValueField(name, n.Body)
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
	binding, ok := s.lookupPackedStore(enumType)
	if !ok {
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
