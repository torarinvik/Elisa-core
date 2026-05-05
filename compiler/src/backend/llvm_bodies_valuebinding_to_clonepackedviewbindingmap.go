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
	"unsafe"
)

type valueBinding struct {
	ptr     C.LLVMValueRef
	typ     semantic.Type
	mutable bool
}
type conditionBindingInfo struct {
	name string
	typ  semantic.Type
}
type codegenScope struct {
	parent                   *codegenScope
	bindingName              string
	binding                  valueBinding
	bindings                 map[string]valueBinding
	packedCommonValueName    string
	packedCommonValueBinding packedCommonFieldValueBinding
	packedCommonValues       map[string]packedCommonFieldValueBinding
	packedEnumPtrs           map[string]packedEnumStorageBinding
	packedEnumStoreName      string
	packedEnumStoreBinding   packedStoreBinding
	packedEnumStores         map[string]packedStoreBinding
	packedViewName           string
	packedViewBinding        packedVariantViewBinding
	packedViewPtrs           map[string]packedVariantViewBinding
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
	treeAllocOwner               treeAllocOwnerBinding
	treeRewriteDefault           *treeRewriteDefaultContext
	currentSequenceRewrite       *sequenceRewriteCodegenContext
	treeImplicitStores           map[treeImplicitStoreCacheKey]treeImplicitStoreSlot
	packedStoreValueKey1         packedStoreExtractCacheKey
	packedStoreValue1            C.LLVMValueRef
	packedStoreValueKey2         packedStoreExtractCacheKey
	packedStoreValue2            C.LLVMValueRef
	packedStoreValueKey3         packedStoreExtractCacheKey
	packedStoreValue3            C.LLVMValueRef
	packedStoreValues            map[packedStoreExtractCacheKey]C.LLVMValueRef
	packedVariantSparseTagReads  map[packedVariantSparseTagReadCacheKey]C.LLVMValueRef
	packedVariantSparseWordReads map[packedVariantSparseWordReadCacheKey]C.LLVMValueRef
	packedDenseDArrayItemsReads  map[packedDenseDArrayItemsReadCacheKey]C.LLVMValueRef
	packedDenseTagReads          map[packedDenseTagReadCacheKey]C.LLVMValueRef
	packedDenseWordReads         map[packedDenseWordReadCacheKey]C.LLVMValueRef
	packedDenseSideWordReads     map[packedDenseSideWordReadCacheKey]C.LLVMValueRef
	packedDirectFieldReads       map[packedDirectFieldReadCacheKey]C.LLVMValueRef
	packedVariantPayloadReads    map[packedVariantPayloadReadCacheKey][]C.LLVMValueRef
	scopedCleanups               []scopedCleanupBinding
	checkpoints                  map[string]checkpointBinding
	poolScopes                   []activePoolBinding
	cleanupDepth                 int
	scopePool                    []*codegenScope
}
type scopedCleanupKind int

const (
	scopedCleanupLockGuard scopedCleanupKind = iota
	scopedCleanupThreadPool
	scopedCleanupDeferBody
	scopedCleanupValue
)

type scopedCleanupBinding struct {
	kind      scopedCleanupKind
	name      string
	ptr       C.LLVMValueRef
	typ       semantic.Type
	owner     *codegenScope
	deferBody *deferredBodyBinding
}
type deferredBodyBinding struct {
	stmt         *ast.DeferStmt
	captureScope *codegenScope
}
type regionBinding struct {
	name string
	ptr  C.LLVMValueRef
	typ  semantic.Type
}
type checkpointBindingKind int

const (
	checkpointBindingRegion checkpointBindingKind = iota
	checkpointBindingDArray
)

type checkpointBinding struct {
	kind       checkpointBindingKind
	name       string
	targetPtr  C.LLVMValueRef
	targetType semantic.Type
	markPtr    C.LLVMValueRef
	markType   semantic.Type
}
type packedStoreBinding struct {
	value C.LLVMValueRef
	typ   *semantic.PackedEnumStoreType
}
type treeAllocOwnerBinding struct {
	isPerm     bool
	arenaRef   C.LLVMValueRef
	storeValue C.LLVMValueRef
	storeType  *semantic.TreeStoreType
}
type treeRewriteDefaultContext struct {
	memberType     semantic.Type
	nodeValue      C.LLVMValueRef
	childViewValue C.LLVMValueRef
}
type treeImplicitStoreCacheKey struct {
	family *semantic.TreeType
	isPerm bool
	arena  C.LLVMValueRef
}
type treeImplicitStoreSlot struct {
	ptr       C.LLVMValueRef
	storeType *semantic.TreeStoreType
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
type packedDenseDArrayItemsReadCacheKey struct {
	block            C.LLVMBasicBlockRef
	storeType        *semantic.PackedEnumStoreType
	state            C.LLVMValueRef
	fieldOffsetBytes uint64
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
type packedDirectFieldReadCacheKey struct {
	block    C.LLVMBasicBlockRef
	store    C.LLVMValueRef
	enumType *semantic.EnumType
	origin   packedReadOriginKey
	handle   C.LLVMValueRef
	offset   uint64
	size     uint64
	typeKey  string
}
type packedVariantPayloadReadCacheKey struct {
	block    C.LLVMBasicBlockRef
	store    C.LLVMValueRef
	enumType *semantic.EnumType
	variant  *semantic.EnumVariant
	origin   packedReadOriginKey
	handle   C.LLVMValueRef
}
type packedEnumStorageBinding struct {
	ptr C.LLVMValueRef
	typ *semantic.EnumType
}
type packedCommonFieldValueBinding struct {
	typ    *semantic.EnumType
	values packedPayloadValueCache
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
func backendExplicitParamCount(fnType *semantic.FuncType, decl *ast.FuncDecl) int {
	if fnType == nil {
		if decl == nil {
			return 0
		}
		return len(decl.Params)
	}
	if fnType.ExplicitParamCount != 0 || len(fnType.ImplicitParamNames) != 0 {
		return fnType.ExplicitParamCount
	}
	if decl != nil {
		return len(decl.Params)
	}
	return len(fnType.Params)
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

	explicitCount := backendExplicitParamCount(fnType, decl)
	bindParam := func(name string, mutable bool, typeIndex int, llvmIndex int) error {
		if typeIndex < 0 || typeIndex >= len(fnType.Params) {
			return nil
		}
		alloca, err := state.createEntryAlloca(name, fnType.Params[typeIndex])
		if err != nil {
			return err
		}
		paramValue := C.LLVMGetParam(fnValue, C.unsigned(llvmIndex+paramOffset))
		C.LLVMBuildStore(builder, paramValue, alloca)
		state.defineBinding(name, valueBinding{ptr: alloca, typ: fnType.Params[typeIndex], mutable: mutable})
		state.bindPackedStoreValue(fnType.Params[typeIndex], paramValue)
		return nil
	}

	for i, param := range decl.Params {
		if err := bindParam(param.Name, param.Mutable, i, i); err != nil {
			return err
		}
	}
	for i, name := range fnType.ImplicitParamNames {
		if err := bindParam(name, false, explicitCount+i, explicitCount+i); err != nil {
			return err
		}
	}

	if err := state.emitBlock(decl.Body, false); err != nil {
		return err
	}

	if !state.currentBlockTerminated() {
		if err := state.emitActiveScopedCleanup(); err != nil {
			return err
		}
		if state.currentBlockTerminated() {
			return nil
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
	if s.currentBlockTerminated() {
		return nil
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
	if s.cleanupDepth != 0 {
		return nil
	}
	s.cleanupDepth++
	defer func() {
		s.cleanupDepth--
	}()
	for i := len(s.scopedCleanups) - 1; i >= 0; i-- {
		if s.currentBlockTerminated() {
			break
		}
		if err := s.emitScopedCleanup(s.scopedCleanups[i]); err != nil {
			return err
		}
	}
	return nil
}
func (s *functionState) emitScopedCleanup(binding scopedCleanupBinding) error {
	if binding.kind == scopedCleanupDeferBody {
		return s.emitDeferredBody(binding.deferBody)
	}
	ops := semantic.CreateTypeBoundOps(binding.typ)
	if len(ops) == 0 {
		switch binding.kind {
		case scopedCleanupLockGuard:
			return s.emitConditionalMutexUnlock(binding)
		case scopedCleanupThreadPool:
			return s.emitConditionalPoolShutdown(binding)
		default:
			return fmt.Errorf("unsupported scoped cleanup kind %d", binding.kind)
		}
	}
	for _, op := range ops {
		if len(op.Path) != 0 || op.IsFillSeq() {
			return fmt.Errorf("unsupported synthesized scoped cleanup path for %q", binding.name)
		}
		switch op.Kind {
		case semantic.TypeBoundCleanupMutexUnlock:
			if err := s.emitConditionalMutexUnlock(binding); err != nil {
				return err
			}
		case semantic.TypeBoundCleanupThreadPoolShutdown:
			if err := s.emitConditionalPoolShutdown(binding); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported synthesized scoped cleanup kind %q", op.Kind)
		}
	}
	return nil
}
func defineBindingInCodegenScope(scope *codegenScope, name string, binding valueBinding) {
	if scope == nil || name == "" {
		return
	}
	if scope.bindingName == "" || scope.bindingName == name {
		scope.bindingName = name
		scope.binding = binding
		return
	}
	if scope.bindings == nil {
		scope.bindings = map[string]valueBinding{}
	}
	scope.bindings[name] = binding
}
func bindPackedCommonFieldValueInCodegenScope(scope *codegenScope, name string, binding packedCommonFieldValueBinding) {
	if scope == nil || name == "" {
		return
	}
	if scope.packedCommonValueName == "" || scope.packedCommonValueName == name {
		scope.packedCommonValueName = name
		scope.packedCommonValueBinding = binding
		return
	}
	if scope.packedCommonValues == nil {
		scope.packedCommonValues = map[string]packedCommonFieldValueBinding{}
	}
	scope.packedCommonValues[name] = binding
}
func bindPackedEnumStorageInCodegenScope(scope *codegenScope, name string, binding packedEnumStorageBinding) {
	if scope == nil || name == "" {
		return
	}
	if scope.packedEnumPtrs == nil {
		scope.packedEnumPtrs = map[string]packedEnumStorageBinding{}
	}
	scope.packedEnumPtrs[name] = binding
}
func bindPackedEnumStoreInCodegenScope(scope *codegenScope, name string, binding packedStoreBinding) {
	if scope == nil || name == "" {
		return
	}
	if scope.packedEnumStoreName == "" || scope.packedEnumStoreName == name {
		scope.packedEnumStoreName = name
		scope.packedEnumStoreBinding = binding
		return
	}
	if scope.packedEnumStores == nil {
		scope.packedEnumStores = map[string]packedStoreBinding{}
	}
	scope.packedEnumStores[name] = binding
}
func bindPackedViewInCodegenScope(scope *codegenScope, name string, binding packedVariantViewBinding) {
	if scope == nil || name == "" {
		return
	}
	if scope.packedViewName == "" || scope.packedViewName == name {
		scope.packedViewName = name
		scope.packedViewBinding = binding
		return
	}
	if scope.packedViewPtrs == nil {
		scope.packedViewPtrs = map[string]packedVariantViewBinding{}
	}
	scope.packedViewPtrs[name] = binding
}
func cloneValueBindingMap(src map[string]valueBinding) map[string]valueBinding {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]valueBinding, len(src))
	for name, binding := range src {
		cloned[name] = binding
	}
	return cloned
}
func clonePackedCommonBindingMap(src map[string]packedCommonFieldValueBinding) map[string]packedCommonFieldValueBinding {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]packedCommonFieldValueBinding, len(src))
	for name, binding := range src {
		cloned[name] = binding
	}
	return cloned
}
func clonePackedEnumStorageBindingMap(src map[string]packedEnumStorageBinding) map[string]packedEnumStorageBinding {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]packedEnumStorageBinding, len(src))
	for name, binding := range src {
		cloned[name] = binding
	}
	return cloned
}
func clonePackedStoreBindingMap(src map[string]packedStoreBinding) map[string]packedStoreBinding {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]packedStoreBinding, len(src))
	for name, binding := range src {
		cloned[name] = binding
	}
	return cloned
}
func clonePackedViewBindingMap(src map[string]packedVariantViewBinding) map[string]packedVariantViewBinding {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]packedVariantViewBinding, len(src))
	for name, binding := range src {
		cloned[name] = binding
	}
	return cloned
}
