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
	"strconv"
	"strings"
	"unsafe"

	"llcontext/src/ast"
	"llcontext/src/lexer"
	"llcontext/src/semantic"
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

func cloneCapturedCodegenScope(scope *codegenScope) *codegenScope {
	if scope == nil {
		return nil
	}
	return &codegenScope{
		bindingName:              scope.bindingName,
		binding:                  scope.binding,
		bindings:                 cloneValueBindingMap(scope.bindings),
		packedCommonValueName:    scope.packedCommonValueName,
		packedCommonValueBinding: scope.packedCommonValueBinding,
		packedCommonValues:       clonePackedCommonBindingMap(scope.packedCommonValues),
		packedEnumPtrs:           clonePackedEnumStorageBindingMap(scope.packedEnumPtrs),
		packedEnumStoreName:      scope.packedEnumStoreName,
		packedEnumStoreBinding:   scope.packedEnumStoreBinding,
		packedEnumStores:         clonePackedStoreBindingMap(scope.packedEnumStores),
		packedViewName:           scope.packedViewName,
		packedViewBinding:        scope.packedViewBinding,
		packedViewPtrs:           clonePackedViewBindingMap(scope.packedViewPtrs),
	}
}

func capturePrefixMatches(root string, key string) bool {
	if root == "" || key == "" {
		return false
	}
	return key == root || strings.HasPrefix(key, root+".") || strings.HasPrefix(key, root+"[")
}

func (s *functionState) captureDeferFunctionScope(stmt *ast.DeferStmt) (*codegenScope, error) {
	if s == nil || stmt == nil || s.g == nil || s.g.result == nil {
		return nil, nil
	}
	info := s.g.result.Defer[stmt]
	if info == nil || len(info.Captures) == 0 {
		return nil, nil
	}
	captured := &codegenScope{}
	for _, name := range info.Captures {
		binding, ok := s.lookupBinding(name)
		if !ok {
			return nil, fmt.Errorf("missing defer capture binding %q during LLVM lowering", name)
		}
		defineBindingInCodegenScope(captured, name, binding)
	}
	for _, root := range info.Captures {
		for scope := s.scope; scope != nil; scope = scope.parent {
			if key := scope.packedCommonValueName; capturePrefixMatches(root, key) {
				bindPackedCommonFieldValueInCodegenScope(captured, key, scope.packedCommonValueBinding)
			}
			for key, binding := range scope.packedCommonValues {
				if capturePrefixMatches(root, key) {
					bindPackedCommonFieldValueInCodegenScope(captured, key, binding)
				}
			}
			for key, binding := range scope.packedEnumPtrs {
				if capturePrefixMatches(root, key) {
					bindPackedEnumStorageInCodegenScope(captured, key, binding)
				}
			}
			if key := scope.packedEnumStoreName; capturePrefixMatches(root, key) {
				bindPackedEnumStoreInCodegenScope(captured, key, scope.packedEnumStoreBinding)
			}
			for key, binding := range scope.packedEnumStores {
				if capturePrefixMatches(root, key) {
					bindPackedEnumStoreInCodegenScope(captured, key, binding)
				}
			}
			if key := scope.packedViewName; capturePrefixMatches(root, key) {
				bindPackedViewInCodegenScope(captured, key, scope.packedViewBinding)
			}
			for key, binding := range scope.packedViewPtrs {
				if capturePrefixMatches(root, key) {
					bindPackedViewInCodegenScope(captured, key, binding)
				}
			}
		}
	}
	return cloneCapturedCodegenScope(captured), nil
}

func (s *functionState) injectCapturedScope(captured *codegenScope) {
	if s == nil || s.scope == nil || captured == nil {
		return
	}
	if captured.bindingName != "" {
		defineBindingInCodegenScope(s.scope, captured.bindingName, captured.binding)
	}
	for name, binding := range captured.bindings {
		defineBindingInCodegenScope(s.scope, name, binding)
	}
	if captured.packedCommonValueName != "" {
		bindPackedCommonFieldValueInCodegenScope(s.scope, captured.packedCommonValueName, captured.packedCommonValueBinding)
	}
	for name, binding := range captured.packedCommonValues {
		bindPackedCommonFieldValueInCodegenScope(s.scope, name, binding)
	}
	for name, binding := range captured.packedEnumPtrs {
		bindPackedEnumStorageInCodegenScope(s.scope, name, binding)
	}
	if captured.packedEnumStoreName != "" {
		bindPackedEnumStoreInCodegenScope(s.scope, captured.packedEnumStoreName, captured.packedEnumStoreBinding)
	}
	for name, binding := range captured.packedEnumStores {
		bindPackedEnumStoreInCodegenScope(s.scope, name, binding)
	}
	if captured.packedViewName != "" {
		bindPackedViewInCodegenScope(s.scope, captured.packedViewName, captured.packedViewBinding)
	}
	for name, binding := range captured.packedViewPtrs {
		bindPackedViewInCodegenScope(s.scope, name, binding)
	}
}

func (s *functionState) registerScopedCleanup(binding scopedCleanupBinding) {
	binding.owner = s.scope
	s.scopedCleanups = append(s.scopedCleanups, binding)
}

func (s *functionState) registerFunctionCleanup(binding scopedCleanupBinding) {
	binding.owner = nil
	s.scopedCleanups = append(s.scopedCleanups, binding)
}

func (s *functionState) discardScopeCleanups(scope *codegenScope) {
	if scope == nil || len(s.scopedCleanups) == 0 {
		return
	}
	out := s.scopedCleanups[:0]
	for _, binding := range s.scopedCleanups {
		if binding.owner == scope {
			continue
		}
		out = append(out, binding)
	}
	s.scopedCleanups = out
}

func (s *functionState) emitScopeCleanups(scope *codegenScope) error {
	if scope == nil {
		return nil
	}
	for i := len(s.scopedCleanups) - 1; i >= 0; i-- {
		if s.currentBlockTerminated() {
			break
		}
		binding := s.scopedCleanups[i]
		if binding.owner != scope {
			continue
		}
		if err := s.emitScopedCleanup(binding); err != nil {
			return err
		}
	}
	s.discardScopeCleanups(scope)
	return nil
}

func (s *functionState) emitBlockInCurrentScope(stmts []ast.Stmt) error {
	scope := s.scope
	if err := s.emitBlock(stmts, false); err != nil {
		s.discardScopeCleanups(scope)
		return err
	}
	if s.currentBlockTerminated() {
		s.discardScopeCleanups(scope)
		return nil
	}
	return s.emitScopeCleanups(scope)
}

func (s *functionState) emitDeferredBody(binding *deferredBodyBinding) error {
	if binding == nil || binding.stmt == nil {
		return nil
	}
	s.pushScope()
	defer s.popScope()
	s.injectCapturedScope(binding.captureScope)
	return s.emitBlockInCurrentScope(binding.stmt.Body)
}

func (s *functionState) emitBlock(stmts []ast.Stmt, scoped bool) error {
	if scoped {
		savedPackedStores := s.packedStores
		s.packedStores = s.clonePackedStores()
		s.pushScope()
		scope := s.scope
		defer func() {
			s.popScope()
			s.packedStores = savedPackedStores
		}()
		for _, stmt := range stmts {
			if s.currentBlockTerminated() {
				break
			}
			if err := s.emitStmt(stmt); err != nil {
				return err
			}
		}
		if s.currentBlockTerminated() {
			s.discardScopeCleanups(scope)
			return nil
		}
		return s.emitScopeCleanups(scope)
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
		s.defineBinding(n.Name, valueBinding{ptr: alloca, typ: declType, mutable: n.Mutable})
		if n.Value != nil {
			value, _, err := s.emitExpr(n.Value, declType)
			if err != nil {
				return err
			}
			C.LLVMBuildStore(s.builder, value, alloca)
			s.bindPackedStoreValue(declType, value)
			if err := s.bindPackedStoreOriginsForExprPath(n.Name, n.Value, declType); err != nil {
				return err
			}
		}
		return nil
	case *ast.LetDestructureStmt:
		return s.emitLetDestructureStmt(n)
	case *ast.TupleBindStmt:
		return s.emitTupleBindStmt(n)
	case *ast.MoveBindStmt:
		return s.emitMoveBindStmt(n)
	case *ast.DeferStmt:
		return s.emitDeferStmt(n)
	case *ast.ScopeStmt:
		return s.emitScopeStmt(n)
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
	case *ast.CheckpointStmt:
		return s.emitCheckpointStmt(n)
	case *ast.GroupedCheckpointStmt:
		return s.emitGroupedCheckpointStmt(n)
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
	case *ast.RestoreCheckpointStmt:
		return s.emitRestoreCheckpointStmt(n)
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
		if n.Optional {
			return s.emitOptionalAssignStmt(n)
		}
		ptr, targetType, err := s.emitAddress(n.Target)
		if err != nil {
			return err
		}
		s.invalidatePackedEnumStorageExpr(n.Target)
		s.invalidatePackedEnumStoreOriginExpr(n.Target)
		s.invalidatePackedCommonFieldValuesExpr(n.Target)
		s.invalidatePackedVariantViewExpr(n.Target)
		value, _, err := s.emitExpr(n.Value, targetType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, value, ptr)
		s.bindPackedStoreValue(targetType, value)
		if path, ok := s.packedEnumStoragePath(n.Target); ok {
			if err := s.bindPackedStoreOriginsForExprPath(path, n.Value, targetType); err != nil {
				return err
			}
		}
		s.invalidatePackedReadCaches()
		return nil
	case *ast.AsRefAssignStmt:
		ptr, targetType, err := s.emitAddress(n.Target)
		if err != nil {
			return err
		}
		s.invalidatePackedEnumStorageExpr(n.Target)
		s.invalidatePackedEnumStoreOriginExpr(n.Target)
		s.invalidatePackedCommonFieldValuesExpr(n.Target)
		s.invalidatePackedVariantViewExpr(n.Target)
		value, _, err := s.emitExpr(n.Value, targetType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, value, ptr)
		s.bindPackedStoreValue(targetType, value)
		if path, ok := s.packedEnumStoragePath(n.Target); ok {
			if err := s.bindPackedStoreOriginsForExprPath(path, n.Value, targetType); err != nil {
				return err
			}
		}
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
		s.invalidatePackedCommonFieldValuesExpr(n.Target)
		s.invalidatePackedReadCaches()
		return nil
	case *ast.LocalParamsStmt:
		return nil
	case *ast.ReturnStmt:
		if n.Value == nil {
			if err := s.emitActiveScopedCleanup(); err != nil {
				return err
			}
			if s.currentBlockTerminated() {
				return nil
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
	case *ast.ExpectPatternStmt:
		return s.emitExpectPatternStmt(n)
	case *ast.InStoreStmt:
		return s.emitInStore(n)
	case *ast.CanStmt:
		return s.emitBlock(n.Body, true)
	case *ast.WithStmt:
		return s.emitBlock(n.Body, true)
	case *ast.ArgsScopeStmt:
		return s.emitBlock(n.Body, true)
	case *ast.PoolStmt:
		return s.emitPoolStmt(n)
	case *ast.LockStmt:
		return s.emitLockStmt(n)
	case *ast.WhileStmt:
		return s.emitWhile(n)
	case *ast.ForStmt:
		return s.emitForStmt(n)
	case *ast.IterForStmt:
		return s.emitIterForStmt(n)
	case *ast.ParallelForStmt:
		return s.emitParallelForStmt(n)
	case *ast.PassStmt:
		return nil
	case *ast.SignalStmt:
		return nil
	case *ast.PanicStmt:
		return s.emitPanicWithBacktrace(n.Pos(), n.Message)
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

func (s *functionState) emitTupleBindStmt(stmt *ast.TupleBindStmt) error {
	if stmt == nil {
		return nil
	}
	value, valueType, err := s.emitExpr(stmt.Value, nil)
	if err != nil {
		return err
	}
	if _, ok := semantic.StripAggregateStateType(valueType).(*semantic.TupleType); !ok {
		return fmt.Errorf("tuple destructuring requires a tuple value, got %s", valueType.String())
	}
	fields, err := s.g.structLiteralFields(valueType)
	if err != nil {
		return err
	}
	if len(stmt.Names) != len(fields) {
		return fmt.Errorf("tuple destructuring expects %d bindings, got %d", len(fields), len(stmt.Names))
	}
	limit := len(stmt.Names)
	if len(fields) < limit {
		limit = len(fields)
	}
	for i := 0; i < limit; i++ {
		name := stmt.Names[i].Name
		fieldValue := C.LLVMBuildExtractValue(s.builder, value, C.unsigned(i), cStringFree("tuple.field"))
		if name == "_" {
			continue
		}
		if stmt.Declare {
			alloca, err := s.createEntryAlloca(name, fields[i].Type)
			if err != nil {
				return err
			}
			s.defineBinding(name, valueBinding{ptr: alloca, typ: fields[i].Type, mutable: false})
			C.LLVMBuildStore(s.builder, fieldValue, alloca)
			continue
		}
		target := &ast.Ident{Position: stmt.Names[i].Position, Name: name}
		s.invalidatePackedEnumStorageExpr(target)
		s.invalidatePackedEnumStoreOriginExpr(target)
		s.invalidatePackedCommonFieldValuesExpr(target)
		s.invalidatePackedVariantViewExpr(target)
		binding, ok := s.lookupBinding(name)
		if !ok {
			return fmt.Errorf("unknown tuple destructuring target %q", name)
		}
		if !binding.mutable {
			return fmt.Errorf("cannot assign to immutable binding %q", name)
		}
		coerced, err := s.coerceValue(fieldValue, fields[i].Type, binding.typ)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, coerced, binding.ptr)
		s.bindPackedStoreValue(binding.typ, coerced)
		s.invalidatePackedReadCaches()
	}
	return nil
}

func moveBindFieldName(arg ast.MoveBindArg) string {
	if arg.Field != "" {
		return arg.Field
	}
	return arg.Name
}

func (s *functionState) emitLetDestructureStmt(stmt *ast.LetDestructureStmt) error {
	if stmt == nil || stmt.Pattern == nil || stmt.Value == nil {
		return nil
	}
	value, valueType, err := s.emitExpr(stmt.Value, nil)
	if err != nil {
		return err
	}
	if _, ok := semantic.StripAggregateStateType(valueType).(*semantic.StoreRowViewType); ok {
		tempName := s.g.nextSyntheticName("let.destructure.")
		tempAlloca, err := s.createEntryAlloca(tempName, valueType)
		if err != nil {
			return err
		}
		s.defineBinding(tempName, valueBinding{ptr: tempAlloca, typ: valueType, mutable: false})
		C.LLVMBuildStore(s.builder, value, tempAlloca)
		tempIdent := &ast.Ident{Position: stmt.Position, Name: tempName}
		for _, arg := range stmt.Pattern.Args {
			fieldExpr := &ast.FieldExpr{Position: arg.Position, Object: tempIdent, Field: moveBindFieldName(arg)}
			fieldValue, fieldType, err := s.emitExpr(fieldExpr, nil)
			if err != nil {
				return err
			}
			if err := s.emitMoveBindLocal(arg.Name, fieldType, fieldValue); err != nil {
				return err
			}
		}
		return nil
	}
	fields, err := s.g.structLiteralFields(valueType)
	if err != nil {
		return err
	}
	for _, arg := range stmt.Pattern.Args {
		fieldName := moveBindFieldName(arg)
		field, ok := lookupStructLiteralField(fields, fieldName)
		if !ok {
			return fmt.Errorf("unknown field %q in let destructuring", fieldName)
		}
		fieldValue := C.LLVMBuildExtractValue(s.builder, value, C.unsigned(field.Index), cStringFree("let.field"))
		if err := s.emitMoveBindLocal(arg.Name, field.Type, fieldValue); err != nil {
			return err
		}
	}
	return nil
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
		if p.Brace {
			for _, arg := range p.Args {
				fieldName := moveBindFieldName(arg)
				field, ok := lookupStructLiteralField(fields, fieldName)
				if !ok {
					return fmt.Errorf("unknown field %q in move-as destructuring", fieldName)
				}
				fieldValue := C.LLVMBuildExtractValue(s.builder, value, C.unsigned(field.Index), cStringFree("move.as.field"))
				if err := s.emitMoveBindLocal(arg.Name, field.Type, fieldValue); err != nil {
					return err
				}
			}
			return nil
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

func (s *functionState) emitDeferStmt(stmt *ast.DeferStmt) error {
	if stmt == nil {
		return nil
	}
	binding := scopedCleanupBinding{
		kind:      scopedCleanupDeferBody,
		deferBody: &deferredBodyBinding{stmt: stmt},
	}
	if stmt.Mode == ast.DeferModeFunction {
		captured, err := s.captureDeferFunctionScope(stmt)
		if err != nil {
			return err
		}
		binding.deferBody.captureScope = captured
		s.registerFunctionCleanup(binding)
		return nil
	}
	s.registerScopedCleanup(binding)
	return nil
}

func (s *functionState) emitScopeStmt(stmt *ast.ScopeStmt) error {
	if stmt == nil {
		return nil
	}
	guardType := s.exprType(stmt.Guard)
	if guardType == nil {
		return fmt.Errorf("missing semantic type for scope guard")
	}
	guardValue, _, err := s.emitExpr(stmt.Guard, guardType)
	if err != nil {
		return err
	}
	tempName := s.g.nextSyntheticName("scope.guard.")
	guardAlloca, err := s.createEntryAlloca(tempName, guardType)
	if err != nil {
		return err
	}
	C.LLVMBuildStore(s.builder, guardValue, guardAlloca)
	s.pushScope()
	scope := s.scope
	defer s.popScope()
	s.registerScopedCleanup(scopedCleanupBinding{kind: scopedCleanupValue, name: tempName, ptr: guardAlloca, typ: guardType})
	if err := s.emitBlock(stmt.Body, false); err != nil {
		s.discardScopeCleanups(scope)
		return err
	}
	if s.currentBlockTerminated() {
		s.discardScopeCleanups(scope)
		return nil
	}
	return s.emitScopeCleanups(scope)
}

func (s *functionState) emitBuiltinCheckpointDArray(target ast.Expr, targetType semantic.Type, name string) (checkpointBinding, error) {
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(targetType)
	if !ok || darrayType == nil {
		return checkpointBinding{}, fmt.Errorf("checkpoint %q requires region or darray target", name)
	}
	darrayPtr, _, err := s.emitBuiltinDArrayReceiverPtr(target, receiverRefType)
	if err != nil {
		return checkpointBinding{}, err
	}
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(darrayPtr, darrayType)
	if err != nil {
		return checkpointBinding{}, err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return checkpointBinding{}, err
	}
	countValue := C.LLVMBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree(name+".checkpoint.count"))
	markAlloca, err := s.createEntryAlloca(name+".checkpoint.mark", usizeType)
	if err != nil {
		return checkpointBinding{}, err
	}
	C.LLVMBuildStore(s.builder, countValue, markAlloca)
	return checkpointBinding{kind: checkpointBindingDArray, name: name, targetPtr: darrayPtr, targetType: darrayType, markPtr: markAlloca, markType: usizeType}, nil
}

func (s *functionState) emitRestoreCheckpointBinding(binding checkpointBinding) error {
	switch binding.kind {
	case checkpointBindingRegion:
		markValue, err := s.loadValue(binding.markPtr, binding.markType, binding.name)
		if err != nil {
			return err
		}
		return s.emitArenaRewind(binding.targetPtr, binding.targetType, markValue, binding.markType)
	case checkpointBindingDArray:
		darrayType, ok := binding.targetType.(*semantic.DArrayType)
		if !ok || darrayType == nil {
			return fmt.Errorf("checkpoint %q is not bound to a darray", binding.name)
		}
		countPtr, _, err := s.emitBuiltinDArrayCountPtr(binding.targetPtr, darrayType)
		if err != nil {
			return err
		}
		markValue, err := s.loadValue(binding.markPtr, binding.markType, binding.name)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, markValue, countPtr)
		return nil
	default:
		return fmt.Errorf("unsupported checkpoint kind %d", binding.kind)
	}
}

func (s *functionState) emitCheckpointBindingFromTarget(name string, target ast.Expr, targetType semantic.Type) (checkpointBinding, error) {
	var binding checkpointBinding
	if ident, ok := target.(*ast.Ident); ok {
		if regionBinding, ok := s.lookupBinding(ident.Name); ok && regionBinding.typ != nil && regionBinding.typ.String() == "Arena" {
			markType := s.g.result.NamedTypes["ArenaMark"]
			if markType == nil {
				return checkpointBinding{}, fmt.Errorf("missing builtin ArenaMark type for region checkpoints")
			}
			markAlloca, err := s.createEntryAlloca(name+".checkpoint.mark", markType)
			if err != nil {
				return checkpointBinding{}, err
			}
			markValue, err := s.emitArenaSnapshot(regionBinding.ptr, regionBinding.typ)
			if err != nil {
				return checkpointBinding{}, err
			}
			C.LLVMBuildStore(s.builder, markValue, markAlloca)
			binding = checkpointBinding{kind: checkpointBindingRegion, name: name, targetPtr: regionBinding.ptr, targetType: regionBinding.typ, markPtr: markAlloca, markType: markType}
		}
	}
	if binding.markPtr == nil {
		var err error
		binding, err = s.emitBuiltinCheckpointDArray(target, targetType, name)
		if err != nil {
			return checkpointBinding{}, err
		}
	}
	return binding, nil
}

func (s *functionState) emitCheckpointStmt(stmt *ast.CheckpointStmt) error {
	if stmt == nil {
		return nil
	}
	if s.checkpoints == nil {
		s.checkpoints = map[string]checkpointBinding{}
	}
	name := stmt.Name
	targetType := s.exprType(stmt.Target)
	binding, err := s.emitCheckpointBindingFromTarget(name, stmt.Target, targetType)
	if err != nil {
		return err
	}
	saved, hadSaved := s.checkpoints[name]
	s.checkpoints[name] = binding
	defer func() {
		if hadSaved {
			s.checkpoints[name] = saved
		} else {
			delete(s.checkpoints, name)
		}
	}()
	if len(stmt.Body) == 0 {
		return nil
	}
	if err := s.emitBlock(stmt.Body, true); err != nil {
		return err
	}
	if s.currentBlockTerminated() {
		return nil
	}
	return s.emitRestoreCheckpointBinding(binding)
}

func (s *functionState) emitGroupedCheckpointStmt(stmt *ast.GroupedCheckpointStmt) error {
	if stmt == nil {
		return nil
	}
	bindings := make([]checkpointBinding, 0, len(stmt.Targets))
	for i, target := range stmt.Targets {
		name := fmt.Sprintf("__grouped_checkpoint_%d_%d_%d", stmt.Position.Line, stmt.Position.Col, i)
		binding, err := s.emitCheckpointBindingFromTarget(name, target, s.exprType(target))
		if err != nil {
			return err
		}
		bindings = append(bindings, binding)
	}
	if err := s.emitBlock(stmt.Body, true); err != nil {
		return err
	}
	if s.currentBlockTerminated() {
		return nil
	}
	for i := len(bindings) - 1; i >= 0; i-- {
		if err := s.emitRestoreCheckpointBinding(bindings[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *functionState) emitRestoreCheckpointStmt(stmt *ast.RestoreCheckpointStmt) error {
	if stmt == nil {
		return nil
	}
	binding, ok := s.checkpoints[stmt.Name]
	if !ok {
		return fmt.Errorf("unknown checkpoint %q during LLVM lowering", stmt.Name)
	}
	return s.emitRestoreCheckpointBinding(binding)
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

func treeChildrenSourceInfo(sourceType semantic.Type) (*semantic.TreeCategoryType, *semantic.EnumVariant, bool) {
	switch tt := sourceType.(type) {
	case *semantic.TreeCategoryType:
		return tt, nil, tt != nil
	case *semantic.TreeVariantViewType:
		if tt == nil || tt.Category == nil || tt.Variant == nil {
			return nil, nil, false
		}
		return tt.Category, tt.Variant, true
	default:
		return nil, nil, false
	}
}

func treeChildrenExactSourceInfo(sourceType semantic.Type) (semantic.Type, *semantic.TreeType, bool) {
	switch tt := semantic.StripAggregateStateType(sourceType).(type) {
	case *semantic.TreeBlockType:
		return tt, tt.Family, tt != nil && tt.Family != nil
	case *semantic.TreeStructType:
		return tt, tt.Family, tt != nil && tt.Family != nil
	case *semantic.TreeNodeType:
		return tt, tt.Family, tt != nil && tt.Family != nil
	default:
		return nil, nil, false
	}
}

func (s *functionState) emitTreeChildrenTrapBlock(block C.LLVMBasicBlockRef) error {
	C.LLVMPositionBuilderAtEnd(s.builder, block)
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
}

func (s *functionState) emitTreeStructuralSequenceCount(payloadValue C.LLVMValueRef, payloadType semantic.Type, name string) (C.LLVMValueRef, error) {
	if optionalType, ok := payloadType.(*semantic.OptionalType); ok {
		presentValue, err := s.extractOptionalPresent(payloadValue, optionalType)
		if err != nil {
			return nil, err
		}
		innerValue, err := s.extractOptionalPayload(payloadValue, optionalType)
		if err != nil {
			return nil, err
		}
		seqCount, err := s.emitTreeStructuralSequenceCount(innerValue, optionalType.Value, name+".optional")
		if err != nil {
			return nil, err
		}
		usizeLLVMType, err := s.g.lowerType(s.g.result.NamedTypes["usize"])
		if err != nil {
			return nil, err
		}
		zeroValue := C.LLVMConstInt(usizeLLVMType, 0, 0)
		return C.LLVMBuildSelect(s.builder, presentValue, seqCount, zeroValue, cStringFree(name+".tree.children.seq.count")), nil
	}
	tempAlloca, err := s.emitStackTempValue(payloadValue, payloadType, name+".tree.children.seq")
	if err != nil {
		return nil, err
	}
	return s.emitIterLoopCount(nil, tempAlloca, payloadType, name+".tree.children.seq")
}

func (s *functionState) emitTreeStructuralSequenceItemValue(payloadValue C.LLVMValueRef, payloadType semantic.Type, indexValue C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	if optionalType, ok := payloadType.(*semantic.OptionalType); ok {
		innerValue, err := s.extractOptionalPayload(payloadValue, optionalType)
		if err != nil {
			return nil, nil, err
		}
		return s.emitTreeStructuralSequenceItemValue(innerValue, optionalType.Value, indexValue, name+".optional")
	}
	tempAlloca, err := s.emitStackTempValue(payloadValue, payloadType, name+".tree.children.seq")
	if err != nil {
		return nil, nil, err
	}
	return s.emitIterLoopElementValue(nil, tempAlloca, payloadType, indexValue, name+".tree.children.seq")
}

func (s *functionState) coerceTreeChildrenItemValue(value C.LLVMValueRef, actualType semantic.Type, itemType semantic.Type) (C.LLVMValueRef, error) {
	if actualType == nil || itemType == nil {
		return value, nil
	}
	if !semantic.AssignableTo(itemType, actualType) {
		return nil, fmt.Errorf("tree structural child item type mismatch: expected %s, got %s", itemType.String(), actualType.String())
	}
	return s.coerceValue(value, actualType, itemType)
}

func (s *functionState) emitTreeVariantStructuralChildCount(nodeValue C.LLVMValueRef, categoryType *semantic.TreeCategoryType, variant *semantic.EnumVariant, name string) (C.LLVMValueRef, error) {
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	total := C.LLVMConstInt(usizeLLVMType, 0, 0)
	if variant == nil || !variant.HasStructuralPayloads() {
		return total, nil
	}
	payloadValues, err := s.extractTreeVariantPayloadValues(nodeValue, categoryType, variant)
	if err != nil {
		return nil, err
	}
	for payloadIndex, payloadType := range variant.Payload {
		switch variant.PayloadRelation(payloadIndex) {
		case ast.EnumPayloadRelationChild:
			childCount := C.LLVMConstInt(usizeLLVMType, 1, 0)
			if optionalType, ok := payloadType.(*semantic.OptionalType); ok {
				presentValue, err := s.extractOptionalPresent(payloadValues[payloadIndex], optionalType)
				if err != nil {
					return nil, err
				}
				zeroValue := C.LLVMConstInt(usizeLLVMType, 0, 0)
				childCount = C.LLVMBuildSelect(s.builder, presentValue, childCount, zeroValue, cStringFree(name+".tree.children.count"))
			}
			total = C.LLVMBuildAdd(s.builder, total, childCount, cStringFree(name+".tree.children.count"))
		case ast.EnumPayloadRelationChildren:
			seqCount, err := s.emitTreeStructuralSequenceCount(payloadValues[payloadIndex], payloadType, name)
			if err != nil {
				return nil, err
			}
			total = C.LLVMBuildAdd(s.builder, total, seqCount, cStringFree(name+".tree.children.count"))
		}
	}
	return total, nil
}

func (s *functionState) emitTreeVariantStructuralChildValue(nodeValue C.LLVMValueRef, categoryType *semantic.TreeCategoryType, variant *semantic.EnumVariant, indexValue C.LLVMValueRef, itemType semantic.Type, name string) (C.LLVMValueRef, error) {
	if variant == nil {
		return nil, fmt.Errorf("tree category %s is missing variant metadata", categoryType.Name)
	}
	if !variant.HasStructuralPayloads() {
		return nil, fmt.Errorf("tree category %s variant %s has no structural children", categoryType.Name, variant.Name)
	}
	payloadValues, err := s.extractTreeVariantPayloadValues(nodeValue, categoryType, variant)
	if err != nil {
		return nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	itemLLVMType, err := s.g.lowerType(itemType)
	if err != nil {
		return nil, err
	}
	resultBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.children.result"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.children.fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(variant.Payload))
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(variant.Payload))
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
	one := C.LLVMConstInt(usizeLLVMType, 1, 0)
	remaining := indexValue
	for payloadIndex, payloadType := range variant.Payload {
		relation := variant.PayloadRelation(payloadIndex)
		if relation != ast.EnumPayloadRelationChild && relation != ast.EnumPayloadRelationChildren {
			continue
		}
		payloadValue := payloadValues[payloadIndex]
		matchBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.children.match"))
		continueBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.children.next"))
		var condValue C.LLVMValueRef
		edgeCount := one
		resolvedType := payloadType
		matchValue := payloadValue
		if relation == ast.EnumPayloadRelationChild {
			if optionalType, ok := payloadType.(*semantic.OptionalType); ok {
				presentValue, err := s.extractOptionalPresent(payloadValue, optionalType)
				if err != nil {
					return nil, err
				}
				matchValue, err = s.extractOptionalPayload(payloadValue, optionalType)
				if err != nil {
					return nil, err
				}
				resolvedType = optionalType.Value
				edgeCount = C.LLVMBuildSelect(s.builder, presentValue, one, zero, cStringFree(name+".tree.children.edge.count"))
			}
			condValue = C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), remaining, edgeCount, cStringFree(name+".tree.children.lt"))
		} else {
			var err error
			edgeCount, err = s.emitTreeStructuralSequenceCount(payloadValue, payloadType, name)
			if err != nil {
				return nil, err
			}
			condValue = C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), remaining, edgeCount, cStringFree(name+".tree.children.lt"))
		}
		C.LLVMBuildCondBr(s.builder, condValue, matchBB, continueBB)

		C.LLVMPositionBuilderAtEnd(s.builder, matchBB)
		if relation == ast.EnumPayloadRelationChild {
			// matchValue and resolvedType are already set above.
		} else {
			var value C.LLVMValueRef
			value, resolvedType, err = s.emitTreeStructuralSequenceItemValue(payloadValue, payloadType, remaining, name)
			if err != nil {
				return nil, err
			}
			matchValue = value
		}
		matchValue, err = s.coerceTreeChildrenItemValue(matchValue, resolvedType, itemType)
		if err != nil {
			return nil, err
		}
		incomingValues = append(incomingValues, matchValue)
		incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
		C.LLVMBuildBr(s.builder, resultBB)

		C.LLVMPositionBuilderAtEnd(s.builder, continueBB)
		remaining = C.LLVMBuildSub(s.builder, remaining, edgeCount, cStringFree(name+".tree.children.rem"))
	}
	C.LLVMBuildBr(s.builder, failBB)
	if err := s.emitTreeChildrenTrapBlock(failBB); err != nil {
		return nil, err
	}
	C.LLVMPositionBuilderAtEnd(s.builder, resultBB)
	phi := C.LLVMBuildPhi(s.builder, itemLLVMType, cStringFree(name+".tree.children.phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, nil
}

func (s *functionState) emitTreeExactStructuralChildCount(nodeValue C.LLVMValueRef, memberType semantic.Type, name string) (C.LLVMValueRef, error) {
	family := treeExactMemberFamily(memberType)
	if family == nil {
		return nil, fmt.Errorf("children(...) exact member is missing tree family metadata")
	}
	stateValue := s.emitTreeHandleStateValue(nodeValue, name+".state")
	rowIndex, err := s.emitTreeHandleIndexValue(nodeValue, name+".index")
	if err != nil {
		return nil, err
	}
	tablePtr, err := s.emitTreeStateTablePtr(stateValue, family, memberType, name)
	if err != nil {
		return nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	total := C.LLVMConstInt(usizeLLVMType, 0, 0)
	for _, fieldDecl := range treeExactFieldDecls(memberType) {
		field, ok := treeExactFieldInfo(memberType, fieldDecl.Name)
		if !ok {
			return nil, fmt.Errorf("missing exact tree field %s.%s", treeExactMemberSurfaceName(memberType), fieldDecl.Name)
		}
		relation := semantic.TreeFieldStructuralRelation(family, field.Type)
		switch relation {
		case ast.EnumPayloadRelationChild:
			fieldValue, _, err := s.emitTreeExactFieldValueAtIndex(tablePtr, memberType, fieldDecl.Name, rowIndex, name)
			if err != nil {
				return nil, err
			}
			childCount := C.LLVMConstInt(usizeLLVMType, 1, 0)
			if optionalType, ok := field.Type.(*semantic.OptionalType); ok {
				presentValue, err := s.extractOptionalPresent(fieldValue, optionalType)
				if err != nil {
					return nil, err
				}
				zeroValue := C.LLVMConstInt(usizeLLVMType, 0, 0)
				childCount = C.LLVMBuildSelect(s.builder, presentValue, childCount, zeroValue, cStringFree(name+".count"))
			}
			total = C.LLVMBuildAdd(s.builder, total, childCount, cStringFree(name+".count"))
		case ast.EnumPayloadRelationChildren:
			fieldValue, _, err := s.emitTreeExactFieldValueAtIndex(tablePtr, memberType, fieldDecl.Name, rowIndex, name)
			if err != nil {
				return nil, err
			}
			seqCount, err := s.emitTreeStructuralSequenceCount(fieldValue, field.Type, name)
			if err != nil {
				return nil, err
			}
			total = C.LLVMBuildAdd(s.builder, total, seqCount, cStringFree(name+".count"))
		}
	}
	return total, nil
}

func (s *functionState) emitTreeExactStructuralChildValue(nodeValue C.LLVMValueRef, memberType semantic.Type, indexValue C.LLVMValueRef, itemType semantic.Type, name string) (C.LLVMValueRef, error) {
	family := treeExactMemberFamily(memberType)
	if family == nil {
		return nil, fmt.Errorf("children(...) exact member is missing tree family metadata")
	}
	stateValue := s.emitTreeHandleStateValue(nodeValue, name+".state")
	rowIndex, err := s.emitTreeHandleIndexValue(nodeValue, name+".index")
	if err != nil {
		return nil, err
	}
	tablePtr, err := s.emitTreeStateTablePtr(stateValue, family, memberType, name)
	if err != nil {
		return nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	itemLLVMType, err := s.g.lowerType(itemType)
	if err != nil {
		return nil, err
	}
	resultBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".result"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".fail"))
	var incomingValues []C.LLVMValueRef
	var incomingBlocks []C.LLVMBasicBlockRef
	remaining := indexValue
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
	one := C.LLVMConstInt(usizeLLVMType, 1, 0)
	for _, fieldDecl := range treeExactFieldDecls(memberType) {
		field, ok := treeExactFieldInfo(memberType, fieldDecl.Name)
		if !ok {
			return nil, fmt.Errorf("missing exact tree field %s.%s", treeExactMemberSurfaceName(memberType), fieldDecl.Name)
		}
		relation := semantic.TreeFieldStructuralRelation(family, field.Type)
		if relation != ast.EnumPayloadRelationChild && relation != ast.EnumPayloadRelationChildren {
			continue
		}
		fieldValue, _, err := s.emitTreeExactFieldValueAtIndex(tablePtr, memberType, fieldDecl.Name, rowIndex, name)
		if err != nil {
			return nil, err
		}
		matchBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".match"))
		continueBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".next"))
		var condValue C.LLVMValueRef
		edgeCount := one
		resolvedType := field.Type
		matchValue := fieldValue
		if relation == ast.EnumPayloadRelationChild {
			if optionalType, ok := field.Type.(*semantic.OptionalType); ok {
				presentValue, err := s.extractOptionalPresent(fieldValue, optionalType)
				if err != nil {
					return nil, err
				}
				matchValue, err = s.extractOptionalPayload(fieldValue, optionalType)
				if err != nil {
					return nil, err
				}
				resolvedType = optionalType.Value
				edgeCount = C.LLVMBuildSelect(s.builder, presentValue, one, zero, cStringFree(name+".edge.count"))
			}
			condValue = C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), remaining, edgeCount, cStringFree(name+".lt"))
		} else {
			edgeCount, err = s.emitTreeStructuralSequenceCount(fieldValue, field.Type, name)
			if err != nil {
				return nil, err
			}
			condValue = C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), remaining, edgeCount, cStringFree(name+".lt"))
		}
		C.LLVMBuildCondBr(s.builder, condValue, matchBB, continueBB)

		C.LLVMPositionBuilderAtEnd(s.builder, matchBB)
		if relation == ast.EnumPayloadRelationChild {
			// matchValue and resolvedType are already set above.
		} else {
			var value C.LLVMValueRef
			value, resolvedType, err = s.emitTreeStructuralSequenceItemValue(fieldValue, field.Type, remaining, name)
			if err != nil {
				return nil, err
			}
			matchValue = value
		}
		matchValue, err = s.coerceTreeChildrenItemValue(matchValue, resolvedType, itemType)
		if err != nil {
			return nil, err
		}
		incomingValues = append(incomingValues, matchValue)
		incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
		C.LLVMBuildBr(s.builder, resultBB)

		C.LLVMPositionBuilderAtEnd(s.builder, continueBB)
		remaining = C.LLVMBuildSub(s.builder, remaining, edgeCount, cStringFree(name+".rem"))
	}
	C.LLVMBuildBr(s.builder, failBB)
	if err := s.emitTreeChildrenTrapBlock(failBB); err != nil {
		return nil, err
	}
	C.LLVMPositionBuilderAtEnd(s.builder, resultBB)
	phi := C.LLVMBuildPhi(s.builder, itemLLVMType, cStringFree(name+".phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, nil
}

func (s *functionState) emitTreeChildrenCount(sourceType semantic.Type, nodeValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	if exactType, family, ok := treeChildrenExactSourceInfo(sourceType); ok {
		switch exact := exactType.(type) {
		case *semantic.TreeBlockType, *semantic.TreeStructType:
			return s.emitTreeExactStructuralChildCount(nodeValue, exact, name)
		case *semantic.TreeNodeType:
			tagValue, err := s.emitTreeHandleTagValue(nodeValue, name+".node")
			if err != nil {
				return nil, err
			}
			usizeType := s.g.result.NamedTypes["usize"]
			usizeLLVMType, err := s.g.lowerType(usizeType)
			if err != nil {
				return nil, err
			}
			resultBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".node.result"))
			failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".node.fail"))
			switchInst := C.LLVMBuildSwitch(s.builder, tagValue, failBB, C.unsigned(len(semantic.TreeFamilyExactMembersInTagOrder(family))))
			var incomingValues []C.LLVMValueRef
			var incomingBlocks []C.LLVMBasicBlockRef
			for _, member := range semantic.TreeFamilyExactMembersInTagOrder(family) {
				tag, ok := treeExactMemberTag(member)
				if !ok {
					continue
				}
				tagConst, err := s.enumTagConstant(tag)
				if err != nil {
					return nil, err
				}
				caseBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".node.case"))
				C.LLVMAddCase(switchInst, tagConst, caseBB)
				C.LLVMPositionBuilderAtEnd(s.builder, caseBB)
				countValue, err := s.emitTreeExactStructuralChildCount(nodeValue, member, name)
				if err != nil {
					return nil, err
				}
				incomingValues = append(incomingValues, countValue)
				incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
				C.LLVMBuildBr(s.builder, resultBB)
			}
			if err := s.emitTreeChildrenTrapBlock(failBB); err != nil {
				return nil, err
			}
			C.LLVMPositionBuilderAtEnd(s.builder, resultBB)
			phi := C.LLVMBuildPhi(s.builder, usizeLLVMType, cStringFree(name+".node.count.phi"))
			C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
			return phi, nil
		}
	}
	categoryType, fixedVariant, ok := treeChildrenSourceInfo(sourceType)
	if !ok || categoryType == nil {
		return nil, fmt.Errorf("children(...) expects a tree node source, got %s", sourceType.String())
	}
	if fixedVariant != nil {
		return s.emitTreeVariantStructuralChildCount(nodeValue, categoryType, fixedVariant, name)
	}
	matchTagValue, err := s.extractTreeCategoryTagValue(nodeValue, categoryType)
	if err != nil {
		return nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	resultBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.children.count.result"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.children.count.fail"))
	switchInst := C.LLVMBuildSwitch(s.builder, matchTagValue, failBB, C.unsigned(len(categoryType.Variants)))
	incomingValues := make([]C.LLVMValueRef, 0, len(categoryType.Variants))
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(categoryType.Variants))
	for _, variant := range categoryType.Variants {
		tagConst, err := s.enumTagConstant(variant.Tag)
		if err != nil {
			return nil, err
		}
		caseBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.children.count.case"))
		C.LLVMAddCase(switchInst, tagConst, caseBB)
		C.LLVMPositionBuilderAtEnd(s.builder, caseBB)
		countValue, err := s.emitTreeVariantStructuralChildCount(nodeValue, categoryType, variant, name)
		if err != nil {
			return nil, err
		}
		incomingValues = append(incomingValues, countValue)
		incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
		C.LLVMBuildBr(s.builder, resultBB)
	}
	if err := s.emitTreeChildrenTrapBlock(failBB); err != nil {
		return nil, err
	}
	C.LLVMPositionBuilderAtEnd(s.builder, resultBB)
	phi := C.LLVMBuildPhi(s.builder, usizeLLVMType, cStringFree(name+".tree.children.count.phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, nil
}

func (s *functionState) emitTreeChildrenValue(sourceType semantic.Type, nodeValue C.LLVMValueRef, itemType semantic.Type, indexValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	if exactType, family, ok := treeChildrenExactSourceInfo(sourceType); ok {
		switch exact := exactType.(type) {
		case *semantic.TreeBlockType, *semantic.TreeStructType:
			return s.emitTreeExactStructuralChildValue(nodeValue, exact, indexValue, itemType, name)
		case *semantic.TreeNodeType:
			tagValue, err := s.emitTreeHandleTagValue(nodeValue, name+".node")
			if err != nil {
				return nil, err
			}
			itemLLVMType, err := s.g.lowerType(itemType)
			if err != nil {
				return nil, err
			}
			resultBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".node.value.result"))
			failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".node.value.fail"))
			switchInst := C.LLVMBuildSwitch(s.builder, tagValue, failBB, C.unsigned(len(semantic.TreeFamilyExactMembersInTagOrder(family))))
			var incomingValues []C.LLVMValueRef
			var incomingBlocks []C.LLVMBasicBlockRef
			for _, member := range semantic.TreeFamilyExactMembersInTagOrder(family) {
				tag, ok := treeExactMemberTag(member)
				if !ok {
					continue
				}
				tagConst, err := s.enumTagConstant(tag)
				if err != nil {
					return nil, err
				}
				caseBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".node.value.case"))
				C.LLVMAddCase(switchInst, tagConst, caseBB)
				C.LLVMPositionBuilderAtEnd(s.builder, caseBB)
				value, err := s.emitTreeExactStructuralChildValue(nodeValue, member, indexValue, itemType, name)
				if err != nil {
					return nil, err
				}
				incomingValues = append(incomingValues, value)
				incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
				C.LLVMBuildBr(s.builder, resultBB)
			}
			if err := s.emitTreeChildrenTrapBlock(failBB); err != nil {
				return nil, err
			}
			C.LLVMPositionBuilderAtEnd(s.builder, resultBB)
			phi := C.LLVMBuildPhi(s.builder, itemLLVMType, cStringFree(name+".node.value.phi"))
			C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
			return phi, nil
		}
	}
	categoryType, fixedVariant, ok := treeChildrenSourceInfo(sourceType)
	if !ok || categoryType == nil {
		return nil, fmt.Errorf("children(...) expects a tree node source, got %s", sourceType.String())
	}
	if fixedVariant != nil {
		return s.emitTreeVariantStructuralChildValue(nodeValue, categoryType, fixedVariant, indexValue, itemType, name)
	}
	matchTagValue, err := s.extractTreeCategoryTagValue(nodeValue, categoryType)
	if err != nil {
		return nil, err
	}
	itemLLVMType, err := s.g.lowerType(itemType)
	if err != nil {
		return nil, err
	}
	resultBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.children.value.result"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.children.value.fail"))
	switchInst := C.LLVMBuildSwitch(s.builder, matchTagValue, failBB, C.unsigned(len(categoryType.Variants)))
	incomingValues := make([]C.LLVMValueRef, 0, len(categoryType.Variants))
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(categoryType.Variants))
	for _, variant := range categoryType.Variants {
		tagConst, err := s.enumTagConstant(variant.Tag)
		if err != nil {
			return nil, err
		}
		caseBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.children.value.case"))
		C.LLVMAddCase(switchInst, tagConst, caseBB)
		C.LLVMPositionBuilderAtEnd(s.builder, caseBB)
		if !variant.HasStructuralPayloads() {
			C.LLVMBuildBr(s.builder, failBB)
			continue
		}
		value, err := s.emitTreeVariantStructuralChildValue(nodeValue, categoryType, variant, indexValue, itemType, name)
		if err != nil {
			return nil, err
		}
		incomingValues = append(incomingValues, value)
		incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
		C.LLVMBuildBr(s.builder, resultBB)
	}
	if err := s.emitTreeChildrenTrapBlock(failBB); err != nil {
		return nil, err
	}
	C.LLVMPositionBuilderAtEnd(s.builder, resultBB)
	phi := C.LLVMBuildPhi(s.builder, itemLLVMType, cStringFree(name+".tree.children.value.phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, nil
}

func iterLoopItemTypeBackend(t semantic.Type) (semantic.Type, bool) {
	switch tt := t.(type) {
	case *semantic.ArrayType:
		if tt.SurfaceName == "str" || tt.SurfaceName == "string" {
			return nil, false
		}
		return tt.Elem, true
	case *semantic.DArrayType:
		return tt.Elem, true
	case *semantic.ViewType:
		return tt.Elem, true
	case *semantic.DArrayViewType:
		if tt.SurfaceName != "" && tt.SurfaceName != "dview" {
			return nil, false
		}
		return tt.Elem, true
	case *semantic.StoreRowsViewType:
		return &semantic.StoreRowViewType{Store: tt.Store}, true
	case *semantic.GenericInstanceType:
		if itemType, ok := semantic.TreeChildrenItemType(tt); ok {
			return itemType, true
		}
		if itemType, ok := semantic.TreeAttributeSequenceItemType(tt); ok {
			return itemType, true
		}
		if itemType, ok := semantic.ChunksExactViewItemType(tt); ok {
			return itemType, true
		}
		return nil, false
	case *semantic.RefType:
		if tt.State != semantic.RefStateNonNull {
			return nil, false
		}
		return iterLoopItemTypeBackend(tt.Elem)
	default:
		return nil, false
	}
}

func isIterLoopRuntimeStringViewType(t semantic.Type) bool {
	st, ok := t.(*semantic.StructType)
	return ok && st != nil && st.Name == "StringView"
}

func (s *functionState) emitStoreRowsCarrierStorePtr(sourceAlloca C.LLVMValueRef, sourceType semantic.Type, sourceName string) (*semantic.StoreRowsViewType, C.LLVMValueRef, error) {
	switch tt := sourceType.(type) {
	case *semantic.StoreRowsViewType:
		carrierValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".rows")
		if err != nil {
			return nil, nil, err
		}
		storePtr := C.LLVMBuildExtractValue(s.builder, carrierValue, 0, cStringFree(sourceName+".rows.store"))
		return tt, storePtr, nil
	case *semantic.RefType:
		rowsType, ok := tt.Elem.(*semantic.StoreRowsViewType)
		if !ok || rowsType == nil {
			return nil, nil, fmt.Errorf("unsupported store rows carrier %s", sourceType.String())
		}
		if tt.State != semantic.RefStateNonNull {
			return nil, nil, fmt.Errorf("iterable loop requires a non-null reference source, got %s", sourceType.String())
		}
		carrierPtr, err := s.loadValue(sourceAlloca, sourceType, sourceName+".rows.ref")
		if err != nil {
			return nil, nil, err
		}
		rowsLLVMType, err := s.g.lowerType(rowsType)
		if err != nil {
			return nil, nil, err
		}
		storeFieldPtr := C.LLVMBuildStructGEP2(s.builder, rowsLLVMType, carrierPtr, 0, cStringFree(sourceName+".rows.store.ptr"))
		storePtr, err := s.loadValue(storeFieldPtr, &semantic.RefType{Elem: rowsType.Store, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}, sourceName+".rows.store")
		if err != nil {
			return nil, nil, err
		}
		return rowsType, storePtr, nil
	default:
		return nil, nil, fmt.Errorf("unsupported store rows carrier %s", sourceType.String())
	}
}

func (s *functionState) emitStoreRowsCount(sourceAlloca C.LLVMValueRef, sourceType semantic.Type, sourceName string) (C.LLVMValueRef, error) {
	rowsType, storePtr, err := s.emitStoreRowsCarrierStorePtr(sourceAlloca, sourceType, sourceName)
	if err != nil {
		return nil, err
	}
	if rowsType.Store == nil || len(rowsType.Store.StoreFieldOrder) == 0 {
		usizeType, err := s.g.lowerType(s.g.result.NamedTypes["usize"])
		if err != nil {
			return nil, err
		}
		return C.LLVMConstInt(usizeType, 0, 0), nil
	}
	firstField := rowsType.Store.StoreFieldOrder[0]
	columnPtr, darrayType, err := s.emitBuiltinStoreFieldDArrayPtr(storePtr, rowsType.Store, firstField)
	if err != nil {
		return nil, err
	}
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(columnPtr, darrayType)
	if err != nil {
		return nil, err
	}
	return s.loadValue(countPtr, usizeType, sourceName+".rows.count")
}

func (s *functionState) emitStoreRowItemValue(sourceAlloca C.LLVMValueRef, sourceType semantic.Type, indexValue C.LLVMValueRef, sourceName string) (C.LLVMValueRef, semantic.Type, error) {
	rowsType, storePtr, err := s.emitStoreRowsCarrierStorePtr(sourceAlloca, sourceType, sourceName)
	if err != nil {
		return nil, nil, err
	}
	rowType := &semantic.StoreRowViewType{Store: rowsType.Store}
	rowLLVMType, err := s.g.lowerType(rowType)
	if err != nil {
		return nil, nil, err
	}
	rowValue := C.LLVMGetUndef(rowLLVMType)
	rowValue = C.LLVMBuildInsertValue(s.builder, rowValue, storePtr, 0, cStringFree(sourceName+".row.store"))
	rowValue = C.LLVMBuildInsertValue(s.builder, rowValue, indexValue, 1, cStringFree(sourceName+".row.index"))
	return rowValue, rowType, nil
}

func (s *functionState) emitIterLoopChunksExactItemValue(carrierValue C.LLVMValueRef, carrierType *semantic.GenericInstanceType, indexValue C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	chunkType, ok := semantic.ChunksExactViewItemType(carrierType)
	if !ok || chunkType == nil {
		return nil, nil, fmt.Errorf("iterable loop chunks_exact item type is not supported for %s", carrierType.String())
	}
	sourceValue := C.LLVMBuildExtractValue(s.builder, carrierValue, 0, cStringFree(name+".chunks.source"))
	chunkSizeValue := C.LLVMBuildExtractValue(s.builder, carrierValue, 1, cStringFree(name+".chunks.chunk_size"))
	startValue := C.LLVMBuildMul(s.builder, indexValue, chunkSizeValue, cStringFree(name+".chunks.start"))
	endValue := C.LLVMBuildAdd(s.builder, startValue, chunkSizeValue, cStringFree(name+".chunks.end"))
	value, err := s.emitArenaViewSliceValue(sourceValue, chunkType, startValue, endValue, name+".chunks.item")
	if err != nil {
		return nil, nil, err
	}
	return value, chunkType, nil
}

func (s *functionState) emitIterLoopCount(sourceExpr ast.Expr, sourceAlloca C.LLVMValueRef, sourceType semantic.Type, sourceName string) (C.LLVMValueRef, error) {
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	switch tt := sourceType.(type) {
	case *semantic.ArrayType:
		if !tt.HasConstSize {
			return nil, fmt.Errorf("iterable loop over %s requires constant array extent metadata", sourceType.String())
		}
		return C.LLVMConstInt(usizeLLVMType, C.ulonglong(tt.ConstSize), 0), nil
	case *semantic.DArrayType, *semantic.ViewType, *semantic.DArrayViewType:
		containerLLVMType, err := s.g.lowerType(sourceType)
		if err != nil {
			return nil, err
		}
		lenPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, sourceAlloca, 1, cStringFree(sourceName+".iter.len.ptr"))
		lenValue, err := s.loadValue(lenPtr, usizeType, sourceName+".iter.len")
		if err != nil {
			return nil, err
		}
		return lenValue, nil
	case *semantic.StoreRowsViewType:
		return s.emitStoreRowsCount(sourceAlloca, sourceType, sourceName)
	case *semantic.DStrType:
		sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.source")
		if err != nil {
			return nil, err
		}
		lenValue, err := s.emitRuntimeStringLengthValue(sourceValue, sourceType, s.g.result.NamedTypes["i64"], sourceName+".iter.len")
		if err != nil {
			return nil, err
		}
		return s.coerceValue(lenValue, s.g.result.NamedTypes["i64"], usizeType)
	case *semantic.SViewType:
		containerLLVMType, err := s.g.lowerType(sourceType)
		if err != nil {
			return nil, err
		}
		lenPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, sourceAlloca, 1, cStringFree(sourceName+".iter.len.ptr"))
		lenValue, err := s.loadValue(lenPtr, s.g.result.NamedTypes["i64"], sourceName+".iter.len")
		if err != nil {
			return nil, err
		}
		return s.coerceValue(lenValue, s.g.result.NamedTypes["i64"], usizeType)
	case *semantic.GenericInstanceType:
		if sourceNodeType, ok := semantic.TreeChildrenSourceType(tt); ok {
			sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.source")
			if err != nil {
				return nil, err
			}
			nodeValue := C.LLVMBuildExtractValue(s.builder, sourceValue, 0, cStringFree(sourceName+".iter.tree.children.node"))
			return s.emitTreeChildrenCount(sourceNodeType, nodeValue, sourceName)
		}
		if projectedSourceType, ok := semantic.TreeAttributeSequenceSourceType(tt); ok {
			sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.projected.source")
			if err != nil {
				return nil, err
			}
			projectedLLVMType, err := s.g.lowerType(sourceType)
			if err != nil {
				return nil, err
			}
			innerSourceAlloca, err := s.createEntryAlloca(sourceName+".iter.projected.inner", projectedSourceType)
			if err != nil {
				return nil, err
			}
			innerSourceValue := C.LLVMBuildExtractValue(s.builder, sourceValue, 0, cStringFree(sourceName+".iter.projected.inner.extract"))
			_ = projectedLLVMType
			C.LLVMBuildStore(s.builder, innerSourceValue, innerSourceAlloca)
			return s.emitIterLoopCount(nil, innerSourceAlloca, projectedSourceType, sourceName+".iter.projected")
		}
		if _, ok := semantic.ChunksExactViewItemType(tt); !ok {
			return nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
		}
		containerLLVMType, err := s.g.lowerType(sourceType)
		if err != nil {
			return nil, err
		}
		lenPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, sourceAlloca, 2, cStringFree(sourceName+".iter.len.ptr"))
		lenValue, err := s.loadValue(lenPtr, usizeType, sourceName+".iter.len")
		if err != nil {
			return nil, err
		}
		return lenValue, nil
	case *semantic.StructType:
		if !isIterLoopRuntimeStringViewType(tt) {
			return nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
		}
		containerLLVMType, err := s.g.lowerType(sourceType)
		if err != nil {
			return nil, err
		}
		lenPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, sourceAlloca, 1, cStringFree(sourceName+".iter.len.ptr"))
		lenValue, err := s.loadValue(lenPtr, s.g.result.NamedTypes["i64"], sourceName+".iter.len")
		if err != nil {
			return nil, err
		}
		return s.coerceValue(lenValue, s.g.result.NamedTypes["i64"], usizeType)
	case *semantic.RefType:
		if tt.State != semantic.RefStateNonNull {
			return nil, fmt.Errorf("iterable loop requires a non-null reference source, got %s", sourceType.String())
		}
		sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.ref")
		if err != nil {
			return nil, err
		}
		switch elem := tt.Elem.(type) {
		case *semantic.ArrayType:
			if !elem.HasConstSize {
				return nil, fmt.Errorf("iterable loop over %s requires constant array extent metadata", sourceType.String())
			}
			return C.LLVMConstInt(usizeLLVMType, C.ulonglong(elem.ConstSize), 0), nil
		case *semantic.DArrayType, *semantic.ViewType, *semantic.DArrayViewType:
			containerLLVMType, err := s.g.lowerType(tt.Elem)
			if err != nil {
				return nil, err
			}
			lenPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, sourceValue, 1, cStringFree(sourceName+".iter.len.ptr"))
			lenValue, err := s.loadValue(lenPtr, usizeType, sourceName+".iter.len")
			if err != nil {
				return nil, err
			}
			return lenValue, nil
		case *semantic.StoreRowsViewType:
			return s.emitStoreRowsCount(sourceAlloca, sourceType, sourceName)
		case *semantic.DStrType:
			lenValue, err := s.emitRuntimeStringLengthValue(sourceValue, tt.Elem, s.g.result.NamedTypes["i64"], sourceName+".iter.len")
			if err != nil {
				return nil, err
			}
			return s.coerceValue(lenValue, s.g.result.NamedTypes["i64"], usizeType)
		case *semantic.SViewType:
			containerLLVMType, err := s.g.lowerType(tt.Elem)
			if err != nil {
				return nil, err
			}
			lenPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, sourceValue, 1, cStringFree(sourceName+".iter.len.ptr"))
			lenValue, err := s.loadValue(lenPtr, s.g.result.NamedTypes["i64"], sourceName+".iter.len")
			if err != nil {
				return nil, err
			}
			return s.coerceValue(lenValue, s.g.result.NamedTypes["i64"], usizeType)
		case *semantic.StructType:
			if !isIterLoopRuntimeStringViewType(elem) {
				return nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
			}
			containerLLVMType, err := s.g.lowerType(tt.Elem)
			if err != nil {
				return nil, err
			}
			lenPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, sourceValue, 1, cStringFree(sourceName+".iter.len.ptr"))
			lenValue, err := s.loadValue(lenPtr, s.g.result.NamedTypes["i64"], sourceName+".iter.len")
			if err != nil {
				return nil, err
			}
			return s.coerceValue(lenValue, s.g.result.NamedTypes["i64"], usizeType)
		case *semantic.GenericInstanceType:
			if _, ok := semantic.ChunksExactViewItemType(elem); !ok {
				return nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
			}
			containerLLVMType, err := s.g.lowerType(tt.Elem)
			if err != nil {
				return nil, err
			}
			lenPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, sourceValue, 2, cStringFree(sourceName+".iter.len.ptr"))
			lenValue, err := s.loadValue(lenPtr, usizeType, sourceName+".iter.len")
			if err != nil {
				return nil, err
			}
			return lenValue, nil
		default:
			return nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
		}
	default:
		return nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
	}
}

func (s *functionState) emitIterLoopElementAddress(sourceAlloca C.LLVMValueRef, sourceType semantic.Type, indexValue C.LLVMValueRef, sourceName string) (C.LLVMValueRef, semantic.Type, error) {
	zero := C.LLVMConstInt(C.LLVMInt32TypeInContext(s.g.context), 0, 0)
	switch tt := sourceType.(type) {
	case *semantic.ArrayType:
		arrayLLVMType, err := s.g.lowerType(tt)
		if err != nil {
			return nil, nil, err
		}
		indices := []C.LLVMValueRef{zero, indexValue}
		ptr := C.LLVMBuildGEP2(s.builder, arrayLLVMType, sourceAlloca, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree(sourceName+".iter.ptr"))
		return ptr, tt.Elem, nil
	case *semantic.DArrayType:
		return s.emitRuntimePointerIndexedAddressWithType(sourceAlloca, mustLowerType(s, tt), tt.Elem, indexValue)
	case *semantic.ViewType:
		return s.emitRuntimePointerIndexedAddressWithType(sourceAlloca, mustLowerType(s, tt), tt.Elem, indexValue)
	case *semantic.DArrayViewType:
		return s.emitRuntimePointerIndexedAddressWithType(sourceAlloca, mustLowerType(s, tt), tt.Elem, indexValue)
	case *semantic.StoreRowsViewType:
		return nil, nil, fmt.Errorf("iterable loop does not support ref binding for %s", sourceType.String())
	case *semantic.GenericInstanceType:
		if _, ok := semantic.TreeChildrenItemType(tt); ok {
			return nil, nil, fmt.Errorf("iterable loop does not support ref binding for %s", sourceType.String())
		}
		return nil, nil, fmt.Errorf("iterable loop does not support ref binding for %s", sourceType.String())
	case *semantic.RefType:
		if tt.State != semantic.RefStateNonNull {
			return nil, nil, fmt.Errorf("iterable loop requires a non-null reference source, got %s", sourceType.String())
		}
		sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.ref")
		if err != nil {
			return nil, nil, err
		}
		switch elem := tt.Elem.(type) {
		case *semantic.ArrayType:
			arrayLLVMType, err := s.g.lowerType(elem)
			if err != nil {
				return nil, nil, err
			}
			indices := []C.LLVMValueRef{zero, indexValue}
			ptr := C.LLVMBuildGEP2(s.builder, arrayLLVMType, sourceValue, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree(sourceName+".iter.ptr"))
			return ptr, elem.Elem, nil
		case *semantic.DArrayType:
			containerLLVMType, err := s.g.lowerType(tt.Elem)
			if err != nil {
				return nil, nil, err
			}
			return s.emitRuntimePointerIndexedAddressWithType(sourceValue, containerLLVMType, elem.Elem, indexValue)
		case *semantic.ViewType:
			containerLLVMType, err := s.g.lowerType(tt.Elem)
			if err != nil {
				return nil, nil, err
			}
			return s.emitRuntimePointerIndexedAddressWithType(sourceValue, containerLLVMType, elem.Elem, indexValue)
		case *semantic.DArrayViewType:
			containerLLVMType, err := s.g.lowerType(tt.Elem)
			if err != nil {
				return nil, nil, err
			}
			return s.emitRuntimePointerIndexedAddressWithType(sourceValue, containerLLVMType, elem.Elem, indexValue)
		case *semantic.StoreRowsViewType:
			return nil, nil, fmt.Errorf("iterable loop does not support ref binding for %s", sourceType.String())
		default:
			return nil, nil, fmt.Errorf("iterable loop does not support ref binding for %s", sourceType.String())
		}
	default:
		return nil, nil, fmt.Errorf("iterable loop does not support ref binding for %s", sourceType.String())
	}
}

func mustLowerType(s *functionState, t semantic.Type) C.LLVMTypeRef {
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil
	}
	return llvmType
}

func (s *functionState) emitIterLoopStringIndexValue(stringValue C.LLVMValueRef, operandType semantic.Type, indexValue C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	helperName, _, ok := runtimeStringIndexedOperand(operandType)
	if !ok {
		return nil, nil, fmt.Errorf("iterable loop string index is not supported for %s", operandType.String())
	}
	indexType := s.g.result.NamedTypes["i64"]
	coercedIndex, err := s.coerceValue(indexValue, s.g.result.NamedTypes["usize"], indexType)
	if err != nil {
		return nil, nil, err
	}
	resultType := s.g.result.NamedTypes["char"]
	helperType := &semantic.FuncType{
		Name:   helperName,
		Params: []semantic.Type{operandType, indexType},
		Return: resultType,
	}
	callee, err := s.g.ensureFunctionDeclared(helperName, helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	call := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stringValue, coercedIndex}, name)
	return call, resultType, nil
}

func (s *functionState) emitIterLoopElementValue(sourceExpr ast.Expr, sourceAlloca C.LLVMValueRef, sourceType semantic.Type, indexValue C.LLVMValueRef, sourceName string) (C.LLVMValueRef, semantic.Type, error) {
	switch tt := sourceType.(type) {
	case *semantic.ArrayType:
		ptr, elemType, err := s.emitIterLoopElementAddress(sourceAlloca, sourceType, indexValue, sourceName)
		if err != nil {
			return nil, nil, err
		}
		if tt.SurfaceName == "str" || tt.SurfaceName == "string" {
			loaded, err := s.loadValue(ptr, elemType, sourceName+".iter.byte")
			if err != nil {
				return nil, nil, err
			}
			llvmResultType, err := s.g.lowerType(s.g.result.NamedTypes["char"])
			if err != nil {
				return nil, nil, err
			}
			return C.LLVMBuildZExt(s.builder, loaded, llvmResultType, cStringFree(sourceName+".iter.char")), s.g.result.NamedTypes["char"], nil
		}
		value, err := s.loadValue(ptr, elemType, sourceName+".iter.value")
		return value, elemType, err
	case *semantic.DArrayType, *semantic.ViewType, *semantic.DArrayViewType:
		ptr, elemType, err := s.emitIterLoopElementAddress(sourceAlloca, sourceType, indexValue, sourceName)
		if err != nil {
			return nil, nil, err
		}
		value, err := s.loadValue(ptr, elemType, sourceName+".iter.value")
		return value, elemType, err
	case *semantic.StoreRowsViewType:
		return s.emitStoreRowItemValue(sourceAlloca, sourceType, indexValue, sourceName)
	case *semantic.DStrType, *semantic.SViewType:
		sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.source")
		if err != nil {
			return nil, nil, err
		}
		return s.emitIterLoopStringIndexValue(sourceValue, sourceType, indexValue, sourceName+".iter.char")
	case *semantic.GenericInstanceType:
		if sourceNodeType, ok := semantic.TreeChildrenSourceType(tt); ok {
			itemType, ok := semantic.TreeChildrenItemType(tt)
			if !ok {
				return nil, nil, fmt.Errorf("unsupported tree children iterable %s", sourceType.String())
			}
			sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.source")
			if err != nil {
				return nil, nil, err
			}
			nodeValue := C.LLVMBuildExtractValue(s.builder, sourceValue, 0, cStringFree(sourceName+".iter.tree.children.node"))
			value, err := s.emitTreeChildrenValue(sourceNodeType, nodeValue, itemType, indexValue, sourceName)
			if err != nil {
				return nil, nil, err
			}
			return value, itemType, nil
		}
		if projectedSourceType, ok := semantic.TreeAttributeSequenceSourceType(tt); ok {
			attrRef := treeAttributeFieldRefForExpr(s.g.result, sourceExpr)
			if attrRef == nil || attrRef.Attribute == nil {
				return nil, nil, fmt.Errorf("missing projected tree attribute metadata for iterable source")
			}
			sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.projected.source")
			if err != nil {
				return nil, nil, err
			}
			innerSourceAlloca, err := s.createEntryAlloca(sourceName+".iter.projected.inner", projectedSourceType)
			if err != nil {
				return nil, nil, err
			}
			innerSourceValue := C.LLVMBuildExtractValue(s.builder, sourceValue, 0, cStringFree(sourceName+".iter.projected.inner.extract"))
			C.LLVMBuildStore(s.builder, innerSourceValue, innerSourceAlloca)
			itemValue, _, err := s.emitIterLoopElementValue(nil, innerSourceAlloca, projectedSourceType, indexValue, sourceName+".iter.projected")
			if err != nil {
				return nil, nil, err
			}
			helper, err := s.ensureTreeAttributeHelper(attrRef.Attribute)
			if err != nil {
				return nil, nil, err
			}
			projectedValue, err := s.emitTreeAttributeHelperCall(helper, itemValue, sourceName+".iter.projected.attr")
			if err != nil {
				return nil, nil, err
			}
			return projectedValue, attrRef.Attribute.ReturnType, nil
		}
		if _, ok := semantic.ChunksExactViewItemType(tt); !ok {
			return nil, nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
		}
		sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.source")
		if err != nil {
			return nil, nil, err
		}
		return s.emitIterLoopChunksExactItemValue(sourceValue, tt, indexValue, sourceName)
	case *semantic.StructType:
		if !isIterLoopRuntimeStringViewType(tt) {
			return nil, nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
		}
		sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.source")
		if err != nil {
			return nil, nil, err
		}
		return s.emitIterLoopStringIndexValue(sourceValue, sourceType, indexValue, sourceName+".iter.char")
	case *semantic.RefType:
		switch elem := tt.Elem.(type) {
		case *semantic.ArrayType, *semantic.DArrayType, *semantic.ViewType, *semantic.DArrayViewType:
			ptr, elemType, err := s.emitIterLoopElementAddress(sourceAlloca, sourceType, indexValue, sourceName)
			if err != nil {
				return nil, nil, err
			}
			if arrayElem, ok := elem.(*semantic.ArrayType); ok && (arrayElem.SurfaceName == "str" || arrayElem.SurfaceName == "string") {
				loaded, err := s.loadValue(ptr, elemType, sourceName+".iter.byte")
				if err != nil {
					return nil, nil, err
				}
				llvmResultType, err := s.g.lowerType(s.g.result.NamedTypes["char"])
				if err != nil {
					return nil, nil, err
				}
				return C.LLVMBuildZExt(s.builder, loaded, llvmResultType, cStringFree(sourceName+".iter.char")), s.g.result.NamedTypes["char"], nil
			}
			value, err := s.loadValue(ptr, elemType, sourceName+".iter.value")
			return value, elemType, err
		case *semantic.StoreRowsViewType:
			return s.emitStoreRowItemValue(sourceAlloca, sourceType, indexValue, sourceName)
		case *semantic.DStrType, *semantic.SViewType:
			sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.ref")
			if err != nil {
				return nil, nil, err
			}
			if _, ok := elem.(*semantic.SViewType); ok {
				loadedView, err := s.loadValue(sourceValue, tt.Elem, sourceName+".iter.view")
				if err != nil {
					return nil, nil, err
				}
				return s.emitIterLoopStringIndexValue(loadedView, tt.Elem, indexValue, sourceName+".iter.char")
			}
			return s.emitIterLoopStringIndexValue(sourceValue, tt.Elem, indexValue, sourceName+".iter.char")
		case *semantic.StructType:
			if !isIterLoopRuntimeStringViewType(elem) {
				return nil, nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
			}
			sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.ref")
			if err != nil {
				return nil, nil, err
			}
			loadedView, err := s.loadValue(sourceValue, tt.Elem, sourceName+".iter.view")
			if err != nil {
				return nil, nil, err
			}
			return s.emitIterLoopStringIndexValue(loadedView, tt.Elem, indexValue, sourceName+".iter.char")
		case *semantic.GenericInstanceType:
			if _, ok := semantic.ChunksExactViewItemType(elem); !ok {
				return nil, nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
			}
			sourceValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".iter.ref")
			if err != nil {
				return nil, nil, err
			}
			loadedCarrier, err := s.loadValue(sourceValue, tt.Elem, sourceName+".iter.chunks")
			if err != nil {
				return nil, nil, err
			}
			return s.emitIterLoopChunksExactItemValue(loadedCarrier, elem, indexValue, sourceName)
		default:
			return nil, nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
		}
	default:
		return nil, nil, fmt.Errorf("unsupported iterable loop source %s", sourceType.String())
	}
}

func (s *functionState) emitIterLoopRefLocal(name string, refType *semantic.RefType, ptrValue C.LLVMValueRef) error {
	if name == "_" {
		return nil
	}
	alloca, err := s.createEntryAlloca(name, refType)
	if err != nil {
		return err
	}
	s.defineBinding(name, valueBinding{ptr: alloca, typ: refType})
	C.LLVMBuildStore(s.builder, ptrValue, alloca)
	return nil
}

func (s *functionState) emitIterLoopPatternBindings(pattern ast.MoveBindPattern, mode ast.IterBindMode, itemType semantic.Type, itemValue C.LLVMValueRef, itemPtr C.LLVMValueRef) error {
	switch p := pattern.(type) {
	case *ast.MoveBindNamePattern:
		if mode == ast.IterBindValue {
			return s.emitMoveBindLocal(p.Name, itemType, itemValue)
		}
		return s.emitIterLoopRefLocal(p.Name, &semantic.RefType{Elem: itemType, Mutable: mode == ast.IterBindMutableRef, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny}, itemPtr)
	case *ast.MoveBindStructPattern:
		if p.Brace {
			if _, ok := semantic.StripAggregateStateType(itemType).(*semantic.StoreRowViewType); ok {
				if mode != ast.IterBindValue {
					return fmt.Errorf("iterable loop does not support ref binding for %s", itemType.String())
				}
				tempName := s.g.nextSyntheticName("iter.destructure.row.")
				tempAlloca, err := s.createEntryAlloca(tempName, itemType)
				if err != nil {
					return err
				}
				s.defineBinding(tempName, valueBinding{ptr: tempAlloca, typ: itemType, mutable: false})
				C.LLVMBuildStore(s.builder, itemValue, tempAlloca)
				tempIdent := &ast.Ident{Position: p.Position, Name: tempName}
				for _, arg := range p.Args {
					if arg.Name == "_" {
						continue
					}
					fieldExpr := &ast.FieldExpr{Position: arg.Position, Object: tempIdent, Field: moveBindFieldName(arg)}
					fieldValue, fieldType, err := s.emitExpr(fieldExpr, nil)
					if err != nil {
						return err
					}
					if err := s.emitMoveBindLocal(arg.Name, fieldType, fieldValue); err != nil {
						return err
					}
				}
				return nil
			}
		}
		fields, err := s.g.structLiteralFields(itemType)
		if err != nil {
			return err
		}
		if p.Brace {
			if mode == ast.IterBindValue {
				for _, arg := range p.Args {
					if arg.Name == "_" {
						continue
					}
					fieldName := moveBindFieldName(arg)
					field, ok := lookupStructLiteralField(fields, fieldName)
					if !ok {
						return fmt.Errorf("unknown field %q in iterable destructuring", fieldName)
					}
					fieldValue := C.LLVMBuildExtractValue(s.builder, itemValue, C.unsigned(field.Index), cStringFree(arg.Name+".iter.field"))
					if err := s.emitMoveBindLocal(arg.Name, field.Type, fieldValue); err != nil {
						return err
					}
				}
				return nil
			}
			containerLLVMType, err := s.g.lowerType(semantic.StripAggregateStateType(itemType))
			if err != nil {
				return err
			}
			for _, arg := range p.Args {
				if arg.Name == "_" {
					continue
				}
				fieldName := moveBindFieldName(arg)
				field, ok := lookupStructLiteralField(fields, fieldName)
				if !ok {
					return fmt.Errorf("unknown field %q in iterable destructuring", fieldName)
				}
				fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, itemPtr, C.unsigned(field.Index), cStringFree(arg.Name+".iter.field.ptr"))
				refType := &semantic.RefType{Elem: field.Type, Mutable: mode == ast.IterBindMutableRef, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny}
				if err := s.emitIterLoopRefLocal(arg.Name, refType, fieldPtr); err != nil {
					return err
				}
			}
			return nil
		}
		limit := len(p.Args)
		if len(fields) < limit {
			limit = len(fields)
		}
		if mode == ast.IterBindValue {
			for i := 0; i < limit; i++ {
				if p.Args[i].Name == "_" {
					continue
				}
				fieldValue := C.LLVMBuildExtractValue(s.builder, itemValue, C.unsigned(fields[i].Index), cStringFree(p.Args[i].Name+".iter.field"))
				if err := s.emitMoveBindLocal(p.Args[i].Name, fields[i].Type, fieldValue); err != nil {
					return err
				}
			}
			return nil
		}
		containerLLVMType, err := s.g.lowerType(semantic.StripAggregateStateType(itemType))
		if err != nil {
			return err
		}
		for i := 0; i < limit; i++ {
			if p.Args[i].Name == "_" {
				continue
			}
			fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, itemPtr, C.unsigned(fields[i].Index), cStringFree(p.Args[i].Name+".iter.field.ptr"))
			refType := &semantic.RefType{Elem: fields[i].Type, Mutable: mode == ast.IterBindMutableRef, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny}
			if err := s.emitIterLoopRefLocal(p.Args[i].Name, refType, fieldPtr); err != nil {
				return err
			}
		}
		return nil
	case *ast.MoveBindTuplePattern:
		fields, err := s.g.structLiteralFields(itemType)
		if err != nil {
			return err
		}
		limit := len(p.Args)
		if len(fields) < limit {
			limit = len(fields)
		}
		if mode == ast.IterBindValue {
			for i := 0; i < limit; i++ {
				if p.Args[i].Name == "_" {
					continue
				}
				fieldValue := C.LLVMBuildExtractValue(s.builder, itemValue, C.unsigned(fields[i].Index), cStringFree(p.Args[i].Name+".iter.tuple.field"))
				if err := s.emitMoveBindLocal(p.Args[i].Name, fields[i].Type, fieldValue); err != nil {
					return err
				}
			}
			return nil
		}
		containerLLVMType, err := s.g.lowerType(semantic.StripAggregateStateType(itemType))
		if err != nil {
			return err
		}
		for i := 0; i < limit; i++ {
			if p.Args[i].Name == "_" {
				continue
			}
			fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, itemPtr, C.unsigned(fields[i].Index), cStringFree(p.Args[i].Name+".iter.tuple.field.ptr"))
			refType := &semantic.RefType{Elem: fields[i].Type, Mutable: mode == ast.IterBindMutableRef, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny}
			if err := s.emitIterLoopRefLocal(p.Args[i].Name, refType, fieldPtr); err != nil {
				return err
			}
		}
		return nil
	case *ast.MoveBindVariantPattern:
		return fmt.Errorf("iterable loop lowering requires an irrefutable pattern, got variant pattern %s.%s", p.EnumName, p.Variant)
	default:
		return fmt.Errorf("unsupported iterable loop pattern %T", pattern)
	}
}

func (s *functionState) buildEnumerateItemValue(tupleType semantic.Type, indexValue C.LLVMValueRef, itemValue C.LLVMValueRef, itemActualType semantic.Type, name string) (C.LLVMValueRef, error) {
	tuple, ok := semantic.StripAggregateStateType(tupleType).(*semantic.TupleType)
	if !ok || tuple == nil || len(tuple.Fields) != 2 {
		return nil, fmt.Errorf("enumerate loop item requires a 2-field tuple type")
	}
	tupleLLVMType, err := s.g.lowerType(tuple)
	if err != nil {
		return nil, err
	}
	indexCoerced, err := s.coerceValue(indexValue, s.g.result.NamedTypes["usize"], tuple.Fields[0].Type)
	if err != nil {
		return nil, err
	}
	if itemActualType == nil {
		itemActualType = tuple.Fields[1].Type
	}
	itemCoerced, err := s.coerceValue(itemValue, itemActualType, tuple.Fields[1].Type)
	if err != nil {
		return nil, err
	}
	value := C.LLVMGetUndef(tupleLLVMType)
	value = C.LLVMBuildInsertValue(s.builder, value, indexCoerced, 0, cStringFree(name+".enumerate.item.index.insert"))
	value = C.LLVMBuildInsertValue(s.builder, value, itemCoerced, 1, cStringFree(name+".enumerate.item.value.insert"))
	return value, nil
}

func (s *functionState) emitIterForStmt(stmt *ast.IterForStmt) error {
	sourceType := s.exprType(stmt.Source)
	if sourceType == nil {
		return fmt.Errorf("missing semantic type for iterable loop source")
	}
	sourceName := s.g.nextSyntheticName("iter.src.")
	sourceAlloca, err := s.createEntryAlloca(sourceName, sourceType)
	if err != nil {
		return err
	}
	sourceValue, _, err := s.emitExpr(stmt.Source, sourceType)
	if err != nil {
		return err
	}
	C.LLVMBuildStore(s.builder, sourceValue, sourceAlloca)

	iterSourceAlloca := sourceAlloca
	iterSourceType := sourceType
	var enumerateItemType semantic.Type
	if carrierType, ok := semantic.EnumerateViewInstance(sourceType); ok {
		innerSourceType, ok := semantic.EnumerateViewSourceType(carrierType)
		if !ok || innerSourceType == nil {
			return fmt.Errorf("enumerate carrier is missing its source type")
		}
		enumerateItemType, ok = semantic.EnumerateViewItemType(carrierType)
		if !ok || enumerateItemType == nil {
			return fmt.Errorf("enumerate carrier is missing its tuple item type")
		}
		if stmt.Mode != ast.IterBindValue {
			return fmt.Errorf("iterable loop does not support ref binding for %s", sourceType.String())
		}
		iterSourceAlloca, err = s.createEntryAlloca(sourceName+".enumerate.source", innerSourceType)
		if err != nil {
			return err
		}
		innerSourceValue := C.LLVMBuildExtractValue(s.builder, sourceValue, 0, cStringFree(sourceName+".enumerate.source.extract"))
		C.LLVMBuildStore(s.builder, innerSourceValue, iterSourceAlloca)
		iterSourceType = innerSourceType
	}

	iterSourceExpr := stmt.Source
	if enumerateCall, ok := stmt.Source.(*ast.CallExpr); ok && callIdentName(enumerateCall) == "enumerate" && len(enumerateCall.Args) == 1 {
		iterSourceExpr = enumerateCall.Args[0]
	}
	countValue, err := s.emitIterLoopCount(iterSourceExpr, iterSourceAlloca, iterSourceType, sourceName)
	if err != nil {
		return err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	indexAlloca, err := s.createEntryAlloca(sourceName+".index", usizeType)
	if err != nil {
		return err
	}
	zeroValue := C.LLVMConstInt(usizeLLVMType, 0, 0)
	C.LLVMBuildStore(s.builder, zeroValue, indexAlloca)

	condBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.cond"))
	bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.body"))
	stepBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.step"))
	exitBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.end"))
	C.LLVMBuildBr(s.builder, condBB)

	C.LLVMPositionBuilderAtEnd(s.builder, condBB)
	indexValue, err := s.loadValue(indexAlloca, usizeType, sourceName+".index")
	if err != nil {
		return err
	}
	condValue := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), indexValue, countValue, cStringFree("iter.cmp"))
	C.LLVMBuildCondBr(s.builder, condValue, bodyBB, exitBB)

	itemType, ok := iterLoopItemTypeBackend(iterSourceType)
	if !ok && stmt.Mode != ast.IterBindValue {
		return fmt.Errorf("iterable loop ref binding requires an addressable array-like source, got %s", iterSourceType.String())
	}
	C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
	s.pushScope()
	iterIndexValue := indexValue
	if stmt.Reverse {
		lastIndex := C.LLVMBuildSub(s.builder, countValue, C.LLVMConstInt(usizeLLVMType, 1, 0), cStringFree("iter.rev.last"))
		iterIndexValue = C.LLVMBuildSub(s.builder, lastIndex, indexValue, cStringFree("iter.rev.index"))
	}
	if stmt.Mode == ast.IterBindValue {
		itemValue, resolvedItemType, err := s.emitIterLoopElementValue(iterSourceExpr, iterSourceAlloca, iterSourceType, iterIndexValue, sourceName)
		if err != nil {
			s.popScope()
			return err
		}
		if enumerateItemType != nil {
			itemValue, err = s.buildEnumerateItemValue(enumerateItemType, iterIndexValue, itemValue, resolvedItemType, sourceName)
			if err != nil {
				s.popScope()
				return err
			}
			resolvedItemType = enumerateItemType
			itemType = resolvedItemType
		}
		if itemType == nil {
			itemType = resolvedItemType
		}
		if err := s.emitIterLoopPatternBindings(stmt.Pattern, stmt.Mode, itemType, itemValue, nil); err != nil {
			s.popScope()
			return err
		}
	} else {
		itemPtr, resolvedItemType, err := s.emitIterLoopElementAddress(iterSourceAlloca, iterSourceType, iterIndexValue, sourceName)
		if err != nil {
			s.popScope()
			return err
		}
		if itemType == nil {
			itemType = resolvedItemType
		}
		if err := s.emitIterLoopPatternBindings(stmt.Pattern, stmt.Mode, itemType, nil, itemPtr); err != nil {
			s.popScope()
			return err
		}
	}
	if stmt.Filter != nil {
		filterValue, filterType, err := s.emitExpr(stmt.Filter, nil)
		if err != nil {
			s.popScope()
			return err
		}
		filterBool, err := s.coerceValue(filterValue, filterType, s.g.result.NamedTypes["bool"])
		if err != nil {
			s.popScope()
			return err
		}
		filterBodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.filter.body"))
		C.LLVMBuildCondBr(s.builder, filterBool, filterBodyBB, stepBB)
		C.LLVMPositionBuilderAtEnd(s.builder, filterBodyBB)
	}
	if err := s.emitBlock(stmt.Body, true); err != nil {
		s.popScope()
		return err
	}
	s.popScope()
	if !s.currentBlockTerminated() {
		C.LLVMBuildBr(s.builder, stepBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, stepBB)
	if !s.currentBlockTerminated() {
		nextValue := C.LLVMBuildAdd(s.builder, indexValue, C.LLVMConstInt(usizeLLVMType, 1, 0), cStringFree("iter.next"))
		C.LLVMBuildStore(s.builder, nextValue, indexAlloca)
		C.LLVMBuildBr(s.builder, condBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, exitBB)
	return nil
}

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
	if treeStore, ok := actualType.(*semantic.TreeStoreType); ok {
		s.treeAllocOwner = treeAllocOwnerBinding{storeValue: storeValue, storeType: treeStore}
		defer func() {
			s.treeAllocOwner = savedTreeOwner
			s.packedStores = savedStores
		}()
		return s.emitBlock(stmt.Body, true)
	}
	storeType, ok := actualType.(*semantic.PackedEnumStoreType)
	if !ok {
		return fmt.Errorf("in-block requires a tree store, packed enum store, perm, an Arena value, or an Arena reference, got %s", actualType.String())
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
	s.registerScopedCleanup(pool)
	s.poolScopes = append(s.poolScopes, activePoolBinding{name: stmt.Name, ptr: poolAlloca, typ: poolType, workers: workersValue})
	defer func() {
		s.poolScopes = s.poolScopes[:len(s.poolScopes)-1]
	}()
	return s.emitBlockInCurrentScope(stmt.Body)
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
		Value:    &ast.IntLit{Position: stmt.Position, Value: "1"},
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
	lowered := specializeFuncType(fnType, bindings, s.g.result.StaticImpls)
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
	s.registerScopedCleanup(guard)
	return s.emitBlockInCurrentScope(stmt.Body)
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

func backendConditionOptionalBindType(valueType semantic.Type) (semantic.Type, bool) {
	switch t := valueType.(type) {
	case *semantic.OptionalType:
		if t == nil || t.Value == nil {
			return nil, false
		}
		return t.Value, true
	case *semantic.RefType:
		if t == nil || t.State != semantic.RefStateNullable {
			return nil, false
		}
		cloned := *t
		cloned.State = semantic.RefStateNonNull
		return &cloned, true
	default:
		return nil, false
	}
}

func (s *functionState) conditionTargetPattern(expr ast.Expr) (ast.MatchPattern, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.conditionTargetPattern(n.Inner)
	case *ast.StructTestExpr:
		if n != nil && n.Pattern != nil {
			return n.Pattern, true
		}
	case *ast.VariantTestExpr:
		if n != nil && n.Pattern != nil {
			return n.Pattern, true
		}
	}
	return nil, false
}

func (s *functionState) directConditionPattern(expr ast.Expr) (ast.Expr, ast.MatchPattern, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.directConditionPattern(n.Inner)
	case *ast.BinaryExpr:
		if n.Op != lexer.TOKEN_IS {
			return nil, nil, false
		}
		pattern, ok := s.conditionTargetPattern(n.Right)
		if !ok || pattern == nil {
			return nil, nil, false
		}
		return n.Left, pattern, true
	default:
		return nil, nil, false
	}
}

func (s *functionState) optionalBindSourceType(expr *ast.OptionalBindExpr) semantic.Type {
	if expr == nil {
		return nil
	}
	if s.g != nil && s.g.result != nil && s.g.result.OptionalBindSourceTypes != nil {
		if valueType, ok := s.g.result.OptionalBindSourceTypes[expr]; ok && valueType != nil {
			return valueType
		}
	}
	return s.exprType(expr.Value)
}

func (s *functionState) emitOptionalBindSourceValue(expr ast.Expr, sourceType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || sourceType == nil {
		return nil, nil, fmt.Errorf("invalid let condition source")
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.emitOptionalBindSourceValue(n.Inner, sourceType)
	}
	if ptr, storedType, err := s.emitAddress(expr); err == nil && storedType != nil && semantic.SameType(storedType, sourceType) {
		value, loadErr := s.loadValue(ptr, sourceType, "cond.let.source")
		return value, sourceType, loadErr
	}
	return s.emitExpr(expr, sourceType)
}

func (s *functionState) emitOptionalBindTest(expr *ast.OptionalBindExpr) (C.LLVMValueRef, C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || expr.Value == nil {
		return nil, nil, nil, fmt.Errorf("invalid let condition")
	}
	valueType := s.optionalBindSourceType(expr)
	if optionalType, ok := valueType.(*semantic.OptionalType); ok {
		fallibleValue, _, err := s.emitOptionalBindSourceValue(expr.Value, valueType)
		if err != nil {
			return nil, nil, nil, err
		}
		presentValue, err := s.extractOptionalPresent(fallibleValue, optionalType)
		if err != nil {
			return nil, nil, nil, err
		}
		payloadValue, err := s.extractOptionalPayload(fallibleValue, optionalType)
		if err != nil {
			return nil, nil, nil, err
		}
		return presentValue, payloadValue, optionalType.Value, nil
	}
	if refType, ok := valueType.(*semantic.RefType); ok && refType.State == semantic.RefStateNullable {
		refValue, _, err := s.emitExpr(expr.Value, valueType)
		if err != nil {
			return nil, nil, nil, err
		}
		nullValue := C.LLVMConstNull(C.LLVMTypeOf(refValue))
		presentValue := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), refValue, nullValue, cStringFree("cond.let.present"))
		boundType, _ := backendConditionOptionalBindType(valueType)
		return presentValue, refValue, boundType, nil
	}
	return nil, nil, nil, fmt.Errorf("let condition requires an optional or nullable reference")
}

func (s *functionState) emitOptionalBindExpr(expr *ast.OptionalBindExpr) (C.LLVMValueRef, semantic.Type, error) {
	presentValue, _, _, err := s.emitOptionalBindTest(expr)
	if err != nil {
		return nil, nil, err
	}
	return presentValue, s.g.result.NamedTypes["bool"], nil
}

func (s *functionState) emitOptionalBindCondition(expr *ast.OptionalBindExpr, trueBB C.LLVMBasicBlockRef, falseBB C.LLVMBasicBlockRef, hint ast.BranchHint) error {
	presentValue, boundValue, _, err := s.emitOptionalBindTest(expr)
	if err != nil {
		return err
	}
	if expr.Name == "" || expr.Name == "_" {
		s.buildCondBrWithHint(presentValue, trueBB, falseBB, hint)
		return nil
	}
	bindBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("cond.let.bind"))
	s.buildCondBrWithHint(presentValue, bindBB, falseBB, hint)
	C.LLVMPositionBuilderAtEnd(s.builder, bindBB)
	if binding, ok := s.lookupBinding(expr.Name); ok && binding.ptr != nil {
		C.LLVMBuildStore(s.builder, boundValue, binding.ptr)
	}
	C.LLVMBuildBr(s.builder, trueBB)
	return nil
}

func stripOptionalAssignTargetExpr(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok || paren == nil {
			return expr
		}
		expr = paren.Inner
	}
}

func (s *functionState) emitOptionalAssignThroughNullableRef(refValue C.LLVMValueRef, elemType semantic.Type, valueExpr ast.Expr, name string) error {
	if refValue == nil || elemType == nil {
		return fmt.Errorf("invalid ?= target")
	}
	nullValue := C.LLVMConstNull(C.LLVMTypeOf(refValue))
	presentValue := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), refValue, nullValue, cStringFree(name+".present"))
	assignBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".assign"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".merge"))
	C.LLVMBuildCondBr(s.builder, presentValue, assignBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, assignBB)
	value, _, err := s.emitExpr(valueExpr, elemType)
	if err != nil {
		return err
	}
	C.LLVMBuildStore(s.builder, value, refValue)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	return nil
}

func (s *functionState) emitOptionalAssignStmt(stmt *ast.AssignStmt) error {
	if stmt == nil || stmt.Target == nil || stmt.Value == nil {
		return fmt.Errorf("invalid ?= statement")
	}
	s.invalidatePackedEnumStorageExpr(stmt.Target)
	s.invalidatePackedEnumStoreOriginExpr(stmt.Target)
	s.invalidatePackedCommonFieldValuesExpr(stmt.Target)
	s.invalidatePackedVariantViewExpr(stmt.Target)
	s.invalidatePackedReadCaches()
	switch target := stripOptionalAssignTargetExpr(stmt.Target).(type) {
	case *ast.Ident:
		targetType := s.exprType(target)
		refType, ok := targetType.(*semantic.RefType)
		if !ok || refType == nil || refType.State != semantic.RefStateNullable {
			return fmt.Errorf("?= requires a nullable reference target")
		}
		refValue, _, err := s.emitExpr(target, targetType)
		if err != nil {
			return err
		}
		return s.emitOptionalAssignThroughNullableRef(refValue, refType.Elem, stmt.Value, "opt.assign.ident")
	case *ast.FieldExpr:
		if !target.Safe {
			return fmt.Errorf("?= requires optional chaining on field targets")
		}
		presentValue, receiverValue, receiverType, err := s.emitSafeChainReceiverValue(target.Object)
		if err != nil {
			return err
		}
		assignBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("opt.assign.field.assign"))
		mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("opt.assign.field.merge"))
		C.LLVMBuildCondBr(s.builder, presentValue, assignBB, mergeBB)

		C.LLVMPositionBuilderAtEnd(s.builder, assignBB)
		fieldPtr, fieldType, err := s.emitFieldAddressFromObjectValue(receiverValue, receiverType, target.Field, "opt.assign.field")
		if err != nil {
			return err
		}
		value, _, err := s.emitExpr(stmt.Value, fieldType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, value, fieldPtr)
		C.LLVMBuildBr(s.builder, mergeBB)

		C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
		return nil
	default:
		return fmt.Errorf("invalid ?= target")
	}
}

func (s *functionState) collectTruthyConditionBindings(expr ast.Expr) ([]conditionBindingInfo, error) {
	var collectPattern func(pattern ast.MatchPattern, expected semantic.Type, out map[string]semantic.Type) error
	collectPattern = func(pattern ast.MatchPattern, expected semantic.Type, out map[string]semantic.Type) error {
		if pattern == nil || expected == nil || out == nil {
			return nil
		}
		switch p := pattern.(type) {
		case *ast.MatchBindPattern:
			if p.Name == "" || p.Name == "_" {
				return nil
			}
			if s.predicateMatchPatternFuncType(p.Name) != nil {
				return nil
			}
			if prev, ok := out[p.Name]; ok && !semantic.SameType(prev, expected) {
				return fmt.Errorf("condition binding %q has inconsistent types %s and %s", p.Name, prev.String(), expected.String())
			}
			out[p.Name] = expected
			return nil
		case *ast.MatchListPattern:
			elemType, ok := semantic.SequenceMatchElementType(expected)
			if !ok {
				return nil
			}
			for _, elem := range p.Elems {
				if err := collectPattern(elem, elemType, out); err != nil {
					return err
				}
			}
			return nil
		case *ast.MatchStructPattern:
			if _, ok, err := countMatchPatternExpectedLen(p); ok || err != nil {
				return err
			}
			fields, orderedArgs, err := s.resolveStructMatchPatternArgs(p, expected)
			if err != nil {
				return err
			}
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				if err := collectPattern(arg.Pattern, fields[i].Type, out); err != nil {
					return err
				}
			}
			return nil
		case *ast.MatchVariantPattern:
			switch base := expected.(type) {
			case *semantic.EnumType:
				variant, ok := base.Variant(p.Variant)
				if !ok {
					return nil
				}
				orderedArgs, err := s.resolveMatchPatternArgs(p, variant)
				if err != nil {
					return err
				}
				for i, arg := range orderedArgs {
					if arg == nil {
						continue
					}
					if err := collectPattern(arg.Pattern, variant.Payload[i], out); err != nil {
						return err
					}
				}
			case *semantic.TreeCategoryType:
				variant, ok := base.Variant(p.Variant)
				if !ok {
					return nil
				}
				orderedArgs, err := s.resolveMatchPatternArgs(p, variant)
				if err != nil {
					return err
				}
				for i, arg := range orderedArgs {
					if arg == nil {
						continue
					}
					if err := collectPattern(arg.Pattern, variant.Payload[i], out); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	var collectExpr func(expr ast.Expr, truthy bool) (map[string]semantic.Type, error)
	collectExpr = func(expr ast.Expr, truthy bool) (map[string]semantic.Type, error) {
		if expr == nil || !truthy {
			return nil, nil
		}
		switch n := expr.(type) {
		case *ast.ParenExpr:
			return collectExpr(n.Inner, truthy)
		case *ast.OptionalBindExpr:
			if n.Name == "" || n.Name == "_" {
				return nil, nil
			}
			boundType, ok := backendConditionOptionalBindType(s.optionalBindSourceType(n))
			if !ok {
				return nil, nil
			}
			return map[string]semantic.Type{n.Name: boundType}, nil
		case *ast.UnaryExpr:
			return nil, nil
		case *ast.BinaryExpr:
			switch n.Op {
			case lexer.TOKEN_AND:
				left, err := collectExpr(n.Left, true)
				if err != nil {
					return nil, err
				}
				right, err := collectExpr(n.Right, true)
				if err != nil {
					return nil, err
				}
				if len(left) == 0 {
					return right, nil
				}
				if len(right) == 0 {
					return left, nil
				}
				out := make(map[string]semantic.Type, len(left)+len(right))
				for name, typ := range left {
					out[name] = typ
				}
				for name, typ := range right {
					if prev, ok := out[name]; ok && !semantic.SameType(prev, typ) {
						return nil, fmt.Errorf("condition binding %q has inconsistent types %s and %s", name, prev.String(), typ.String())
					}
					out[name] = typ
				}
				return out, nil
			case lexer.TOKEN_OR:
				left, err := collectExpr(n.Left, true)
				if err != nil {
					return nil, err
				}
				right, err := collectExpr(n.Right, true)
				if err != nil {
					return nil, err
				}
				if len(left) == 0 || len(right) == 0 {
					return nil, nil
				}
				out := map[string]semantic.Type{}
				for name, leftType := range left {
					rightType, ok := right[name]
					if !ok || !semantic.SameType(leftType, rightType) {
						continue
					}
					out[name] = leftType
				}
				if len(out) == 0 {
					return nil, nil
				}
				return out, nil
			}
		}
		leftExpr, pattern, ok := s.directConditionPattern(expr)
		if !ok || leftExpr == nil || pattern == nil {
			return nil, nil
		}
		out := map[string]semantic.Type{}
		if err := collectPattern(pattern, s.exprType(leftExpr), out); err != nil {
			return nil, err
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil
	}
	collected, err := collectExpr(expr, true)
	if err != nil {
		return nil, err
	}
	if len(collected) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(collected))
	for name := range collected {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]conditionBindingInfo, 0, len(names))
	for _, name := range names {
		out = append(out, conditionBindingInfo{name: name, typ: collected[name]})
	}
	return out, nil
}

func (s *functionState) createConditionBindingScope(expr ast.Expr) (*codegenScope, bool, error) {
	infos, err := s.collectTruthyConditionBindings(expr)
	if err != nil {
		return nil, false, err
	}
	if len(infos) == 0 {
		return nil, false, nil
	}
	scope := &codegenScope{parent: s.scope}
	for _, info := range infos {
		alloca, err := s.createEntryAlloca(info.name+".cond", info.typ)
		if err != nil {
			return nil, false, err
		}
		defineBindingInCodegenScope(scope, info.name, valueBinding{ptr: alloca, typ: info.typ, mutable: false})
	}
	return scope, true, nil
}

func (s *functionState) collectMatchPatternBindings(pattern ast.MatchPattern, expected semantic.Type, out map[string]semantic.Type) error {
	if pattern == nil || expected == nil || out == nil {
		return nil
	}
	switch p := pattern.(type) {
	case *ast.MatchBindPattern:
		if p.Name == "" || p.Name == "_" || s.predicateMatchPatternFuncType(p.Name) != nil {
			return nil
		}
		if prev, ok := out[p.Name]; ok && !semantic.SameType(prev, expected) {
			return fmt.Errorf("condition binding %q has inconsistent types %s and %s", p.Name, prev.String(), expected.String())
		}
		out[p.Name] = expected
	case *ast.MatchStructPattern:
		if _, ok, err := countMatchPatternExpectedLen(p); ok || err != nil {
			return err
		}
		fields, orderedArgs, err := s.resolveStructMatchPatternArgs(p, expected)
		if err != nil {
			return err
		}
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			if err := s.collectMatchPatternBindings(arg.Pattern, fields[i].Type, out); err != nil {
				return err
			}
		}
	case *ast.MatchListPattern:
		elemType, ok := semantic.SequenceMatchElementType(expected)
		if !ok {
			return nil
		}
		for _, elem := range p.Elems {
			if err := s.collectMatchPatternBindings(elem, elemType, out); err != nil {
				return err
			}
		}
	case *ast.MatchVariantPattern:
		switch base := expected.(type) {
		case *semantic.EnumType:
			variant, ok := base.Variant(p.Variant)
			if !ok {
				return nil
			}
			orderedArgs, err := s.resolveMatchPatternArgs(p, variant)
			if err != nil {
				return err
			}
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				if err := s.collectMatchPatternBindings(arg.Pattern, variant.Payload[i], out); err != nil {
					return err
				}
			}
		case *semantic.TreeCategoryType:
			variant, ok := base.Variant(p.Variant)
			if !ok {
				return nil
			}
			orderedArgs, err := s.resolveMatchPatternArgs(p, variant)
			if err != nil {
				return err
			}
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				if err := s.collectMatchPatternBindings(arg.Pattern, variant.Payload[i], out); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *functionState) emitConditionPatternTestAndBind(pattern ast.MatchPattern, actualValue C.LLVMValueRef, actualType semantic.Type, actualExpr ast.Expr, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) error {
	if pattern == nil || actualType == nil {
		C.LLVMBuildBr(s.builder, successBB)
		return nil
	}
	if actualExpr == nil {
		actualExpr = &ast.Ident{Name: "<cond>"}
	}
	if bindPattern, ok := pattern.(*ast.MatchBindPattern); ok {
		if handled, err := s.emitPredicateMatchPatternTest(bindPattern.Name, actualValue, actualType, successBB, failureBB); handled || err != nil {
			return err
		}
		if bindPattern.Name != "" && bindPattern.Name != "_" {
			if binding, ok := s.lookupBinding(bindPattern.Name); ok && binding.ptr != nil {
				C.LLVMBuildStore(s.builder, actualValue, binding.ptr)
			}
		}
		C.LLVMBuildBr(s.builder, successBB)
		return nil
	}
	if handled, err := s.emitCountMatchPatternTest(pattern, actualValue, actualType, actualExpr, successBB, failureBB); handled || err != nil {
		return err
	}
	if structPattern, ok := pattern.(*ast.MatchStructPattern); ok {
		fields, orderedArgs, err := s.resolveStructMatchPatternArgs(structPattern, actualType)
		if err != nil {
			return err
		}
		matchedIndexes := make([]int, 0, len(orderedArgs))
		for i, arg := range orderedArgs {
			if arg != nil {
				matchedIndexes = append(matchedIndexes, i)
			}
		}
		if len(matchedIndexes) == 0 {
			C.LLVMBuildBr(s.builder, successBB)
			return nil
		}
		for i, fieldIndex := range matchedIndexes {
			arg := orderedArgs[fieldIndex]
			fieldValue := C.LLVMBuildExtractValue(s.builder, actualValue, C.unsigned(fields[fieldIndex].Index), cStringFree("cond.struct.field"))
			nextSuccess := successBB
			if i != len(matchedIndexes)-1 {
				nextSuccess = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("cond.struct.next"))
			}
			fieldExpr := ast.Expr(nil)
			if actualExpr != nil {
				fieldExpr = &ast.FieldExpr{Position: arg.Position, Object: actualExpr, Field: fields[fieldIndex].Decl.Name}
			}
			if err := s.emitConditionPatternTestAndBind(arg.Pattern, fieldValue, fields[fieldIndex].Type, fieldExpr, nextSuccess, failureBB); err != nil {
				return err
			}
			if i != len(matchedIndexes)-1 {
				C.LLVMPositionBuilderAtEnd(s.builder, nextSuccess)
			}
		}
		return nil
	}
	if _, _, err := s.emitMatchPatternTest(pattern, actualValue, nil, actualType, nil, actualExpr, nil, successBB, failureBB); err != nil {
		return err
	}
	return nil
}

func invertBranchHint(hint ast.BranchHint) ast.BranchHint {
	switch hint {
	case ast.BranchHintLikely:
		return ast.BranchHintUnlikely
	case ast.BranchHintUnlikely:
		return ast.BranchHintLikely
	default:
		return hint
	}
}

func (s *functionState) emitConditionBranchWithBindings(expr ast.Expr, trueBB C.LLVMBasicBlockRef, falseBB C.LLVMBasicBlockRef, hint ast.BranchHint) error {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.emitConditionBranchWithBindings(n.Inner, trueBB, falseBB, hint)
	case *ast.OptionalBindExpr:
		return s.emitOptionalBindCondition(n, trueBB, falseBB, hint)
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			return s.emitConditionBranchWithBindings(n.Operand, falseBB, trueBB, invertBranchHint(hint))
		}
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			rhsBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("cond.and.rhs"))
			if err := s.emitConditionBranchWithBindings(n.Left, rhsBB, falseBB, hint); err != nil {
				return err
			}
			C.LLVMPositionBuilderAtEnd(s.builder, rhsBB)
			return s.emitConditionBranchWithBindings(n.Right, trueBB, falseBB, ast.BranchHintNone)
		case lexer.TOKEN_OR:
			rhsBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("cond.or.rhs"))
			if err := s.emitConditionBranchWithBindings(n.Left, trueBB, rhsBB, hint); err != nil {
				return err
			}
			C.LLVMPositionBuilderAtEnd(s.builder, rhsBB)
			return s.emitConditionBranchWithBindings(n.Right, trueBB, falseBB, ast.BranchHintNone)
		}
	}
	if leftExpr, pattern, ok := s.directConditionPattern(expr); ok {
		leftType := s.exprType(leftExpr)
		leftValue, _, err := s.emitExpr(leftExpr, leftType)
		if err != nil {
			return err
		}
		return s.emitConditionPatternTestAndBind(pattern, leftValue, leftType, leftExpr, trueBB, falseBB)
	}
	condValue, _, err := s.emitExpr(expr, s.g.result.NamedTypes["bool"])
	if err != nil {
		return err
	}
	s.buildCondBrWithHint(condValue, trueBB, falseBB, hint)
	return nil
}

func (s *functionState) emitIf(stmt *ast.IfStmt) error {
	stmt = normalizeIf(stmt)
	if condScope, ok, err := s.createConditionBindingScope(stmt.Cond); err != nil {
		return err
	} else if ok {
		parentScope := s.scope
		s.scope = condScope
		thenBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("if.then"))
		mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("if.end"))
		var elseBB C.LLVMBasicBlockRef
		if len(stmt.Else) > 0 {
			elseBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("if.else"))
			if err := s.emitConditionBranchWithBindings(stmt.Cond, thenBB, elseBB, stmt.Hint); err != nil {
				s.scope = parentScope
				return err
			}
		} else {
			if err := s.emitConditionBranchWithBindings(stmt.Cond, thenBB, mergeBB, stmt.Hint); err != nil {
				s.scope = parentScope
				return err
			}
		}

		C.LLVMPositionBuilderAtEnd(s.builder, thenBB)
		s.scope = condScope
		if err := s.emitBlock(stmt.Then, true); err != nil {
			s.scope = parentScope
			return err
		}
		thenTerminated := s.currentBlockTerminated()
		if !thenTerminated {
			C.LLVMBuildBr(s.builder, mergeBB)
		}

		elseTerminated := false
		if len(stmt.Else) > 0 {
			C.LLVMPositionBuilderAtEnd(s.builder, elseBB)
			s.scope = parentScope
			if err := s.emitBlock(stmt.Else, true); err != nil {
				s.scope = parentScope
				return err
			}
			elseTerminated = s.currentBlockTerminated()
			if !elseTerminated {
				C.LLVMBuildBr(s.builder, mergeBB)
			}
		}

		C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
		s.scope = parentScope
		if len(stmt.Else) > 0 && thenTerminated && elseTerminated {
			C.LLVMBuildUnreachable(s.builder)
		}
		return nil
	}
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
	if condScope, ok, err := s.createConditionBindingScope(stmt.Cond); err != nil {
		return err
	} else if ok {
		parentScope := s.scope
		condBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("while.cond"))
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("while.body"))
		exitBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("while.end"))

		C.LLVMBuildBr(s.builder, condBB)
		C.LLVMPositionBuilderAtEnd(s.builder, condBB)
		s.scope = condScope
		if err := s.emitConditionBranchWithBindings(stmt.Cond, bodyBB, exitBB, stmt.Hint); err != nil {
			s.scope = parentScope
			return err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.scope = condScope
		if err := s.emitBlock(stmt.Body, true); err != nil {
			s.scope = parentScope
			return err
		}
		if !s.currentBlockTerminated() {
			C.LLVMBuildBr(s.builder, condBB)
		}

		C.LLVMPositionBuilderAtEnd(s.builder, exitBB)
		s.scope = parentScope
		return nil
	}
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

func resolveMatchableConstEnumType(actual semantic.Type) (*semantic.ConstEnumType, bool) {
	constEnumType, ok := semantic.StripAggregateStateType(actual).(*semantic.ConstEnumType)
	return constEnumType, ok && constEnumType != nil
}

func resolveMatchableStructTypeBackend(actual semantic.Type) bool {
	switch tt := semantic.StripAggregateStateType(actual).(type) {
	case *semantic.StructType:
		return tt != nil && tt.Decl != nil
	case *semantic.GenericInstanceType:
		base, _ := tt.Base.(*semantic.StructType)
		return base != nil && base.Decl != nil
	case *semantic.TreeBlockType:
		return tt != nil && tt.Decl != nil
	case *semantic.TreeStructType:
		return tt != nil && tt.Decl != nil
	default:
		return false
	}
}

func resolveMatchableTupleTypeBackend(actual semantic.Type) bool {
	tupleType, ok := semantic.StripAggregateStateType(actual).(*semantic.TupleType)
	return ok && tupleType != nil
}

func resolveMatchableSequenceTypeBackend(actual semantic.Type) bool {
	_, ok := semantic.SequenceMatchElementType(actual)
	return ok
}

func runtimeStringLiteralType() semantic.Type {
	return &semantic.RefType{Elem: &semantic.BuiltinType{Name: "u8"}, State: semantic.RefStateNonNull, Storage: semantic.RefStorageStatic, ExplicitStorage: true}
}

func isStringMatchableType(actual semantic.Type) bool {
	_, _, _, _, ok := runtimeStringCompareInfo(actual, runtimeStringLiteralType())
	return ok
}

func resolveMatchableErrorSetTypeBackend(actual semantic.Type) (*semantic.ErrorSetType, bool) {
	errorSetType, ok := semantic.StripAggregateStateType(actual).(*semantic.ErrorSetType)
	return errorSetType, ok && errorSetType != nil
}

func (s *functionState) emitMatch(stmt *ast.MatchStmt) error {
	enumType, ok := resolveMatchableEnumType(s.exprType(stmt.Value))
	if ok {
		return s.emitEnumMatch(stmt, enumType)
	}
	constEnumType, ok := resolveMatchableConstEnumType(s.exprType(stmt.Value))
	if ok {
		return s.emitConstEnumMatch(stmt, constEnumType)
	}
	errorSetType, ok := resolveMatchableErrorSetTypeBackend(s.exprType(stmt.Value))
	if ok {
		return s.emitErrorSetMatch(stmt, errorSetType)
	}
	treeType, _, ok := resolveMatchableTreeCategoryTypeBackend(s.exprType(stmt.Value))
	if ok {
		return s.emitTreeMatch(stmt, treeType)
	}
	if isStringMatchableType(s.exprType(stmt.Value)) {
		return s.emitStringMatch(stmt)
	}
	if resolveMatchableTupleTypeBackend(s.exprType(stmt.Value)) {
		return s.emitTupleMatch(stmt)
	}
	if resolveMatchableSequenceTypeBackend(s.exprType(stmt.Value)) {
		return s.emitSequenceMatch(stmt)
	}
	if resolveMatchableStructTypeBackend(s.exprType(stmt.Value)) {
		return s.emitStructMatch(stmt)
	}
	return fmt.Errorf("match requires an enum, const enum, error set, tree-category, string, tuple, sequence, or struct value")
}

func (s *functionState) emitExpectPatternStmt(stmt *ast.ExpectPatternStmt) error {
	if stmt == nil {
		return nil
	}
	actualType := s.exprType(stmt.Value)
	actualValue, _, err := s.emitExpr(stmt.Value, actualType)
	if err != nil {
		return err
	}
	if len(stmt.Patterns) == 1 {
		bindings := map[string]semantic.Type{}
		if err := s.collectMatchPatternBindings(stmt.Patterns[0], actualType, bindings); err != nil {
			return err
		}
		names := make([]string, 0, len(bindings))
		for name := range bindings {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			alloca, err := s.createEntryAlloca(name+".expect", bindings[name])
			if err != nil {
				return err
			}
			s.defineBinding(name, valueBinding{ptr: alloca, typ: bindings[name], mutable: false})
		}
	}
	successBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("expect.ok"))
	failureBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("expect.fail"))
	if len(stmt.Patterns) == 0 {
		C.LLVMBuildBr(s.builder, successBB)
	} else {
		for i, pattern := range stmt.Patterns {
			nextFailure := failureBB
			if i != len(stmt.Patterns)-1 {
				nextFailure = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("expect.next"))
			}
			if err := s.emitConditionPatternTestAndBind(pattern, actualValue, actualType, stmt.Value, successBB, nextFailure); err != nil {
				return err
			}
			if i != len(stmt.Patterns)-1 {
				C.LLVMPositionBuilderAtEnd(s.builder, nextFailure)
			}
		}
	}
	C.LLVMPositionBuilderAtEnd(s.builder, failureBB)
	if err := s.emitPanicWithBacktrace(stmt.Position, &ast.StringLit{Position: stmt.Position, Value: "expect pattern failed"}); err != nil {
		return err
	}
	C.LLVMPositionBuilderAtEnd(s.builder, successBB)
	return nil
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
	preloadedCommonValues, err := s.preloadPackedMatchCommonFieldValues(enumType, stmt.Value, enumValue, decodedMatchValue, storeBinding, stmt.Arms)
	if err != nil {
		return err
	}
	matchTagValue, err := s.extractEnumTagValue(enumValue, decodedMatchValue, enumType, storeBinding)
	if err != nil {
		return err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.fail"))
	allTerminated := true
	valuePath, hasValuePath := s.packedEnumStoragePath(stmt.Value)
	if packedEnumMatchCanUseTagSwitch(enumType, stmt.Arms) {
		wildcardIndex := -1
		variantArmCount := 0
		for i, arm := range stmt.Arms {
			switch arm.Pattern.(type) {
			case *ast.MatchVariantPattern:
				variantArmCount++
			case *ast.MatchWildcardPattern:
				wildcardIndex = i
			}
		}
		var wildcardBB C.LLVMBasicBlockRef
		defaultBB := failBB
		if wildcardIndex >= 0 {
			wildcardBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.wildcard"))
			defaultBB = wildcardBB
		}
		switchInst := C.LLVMBuildSwitch(s.builder, matchTagValue, defaultBB, C.unsigned(variantArmCount))
		for i, arm := range stmt.Arms {
			pattern, ok := arm.Pattern.(*ast.MatchVariantPattern)
			if !ok {
				continue
			}
			variant, _ := enumType.Variant(pattern.Variant)
			tagConst, err := s.enumTagConstant(variant.Tag)
			if err != nil {
				return err
			}
			dispatchBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.dispatch"))
			bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.arm"))
			C.LLVMAddCase(switchInst, tagConst, dispatchBB)

			C.LLVMPositionBuilderAtEnd(s.builder, dispatchBB)
			patternFailureBB := failBB
			if wildcardBB != nil {
				patternFailureBB = wildcardBB
			}
			armDecodedValue, armPayloadValues, err := s.emitMatchedVariantPayloadPatternTest(pattern, enumValue, decodedMatchValue, enumType, variant, storeBinding, stmt.Value, bodyBB, patternFailureBB)
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
			if hasValuePath && !preloadedCommonValues.empty() {
				s.bindPackedCommonFieldValues(valuePath, enumType, preloadedCommonValues)
			}
			if err := s.emitBlockInCurrentScope(arm.Body); err != nil {
				s.popScope()
				return err
			}
			s.popScope()
			if !s.currentBlockTerminated() {
				allTerminated = false
				C.LLVMBuildBr(s.builder, mergeBB)
			}
			_ = i
		}
		if wildcardIndex >= 0 {
			arm := stmt.Arms[wildcardIndex]
			C.LLVMPositionBuilderAtEnd(s.builder, wildcardBB)
			s.pushScope()
			if hasValuePath && enumType.Packed && decodedMatchValue != nil {
				s.bindPackedEnumStorage(valuePath, enumType, decodedMatchValue)
			}
			if hasValuePath && enumType.Packed {
				s.bindPackedEnumStoreOrigin(valuePath, enumType, storeBinding)
			}
			s.bindMatchedPackedVariantView(valuePath, arm.Pattern, enumValue, decodedMatchValue, enumType, storeBinding, packedPayloadValueCache{})
			if hasValuePath && !preloadedCommonValues.empty() {
				s.bindPackedCommonFieldValues(valuePath, enumType, preloadedCommonValues)
			}
			if err := s.emitBlockInCurrentScope(arm.Body); err != nil {
				s.popScope()
				return err
			}
			s.popScope()
			if !s.currentBlockTerminated() {
				allTerminated = false
				C.LLVMBuildBr(s.builder, mergeBB)
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
		if hasValuePath && !preloadedCommonValues.empty() {
			s.bindPackedCommonFieldValues(valuePath, enumType, preloadedCommonValues)
		}
		if err := s.emitBlockInCurrentScope(arm.Body); err != nil {
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

func (s *functionState) emitTreeMatch(stmt *ast.MatchStmt, treeType *semantic.TreeCategoryType) error {
	if treeType == nil {
		return fmt.Errorf("match requires a tree-category value")
	}
	if stmt.Store != nil {
		return fmt.Errorf("tree match over %q does not take an in-store clause", treeType.Name)
	}
	actualType := s.exprType(stmt.Value)
	treeValue, _, err := s.emitExpr(stmt.Value, nil)
	if err != nil {
		return err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.fail"))
	allTerminated := true
	exhaustive := treeMatchIsExhaustive(treeType, stmt.Arms)

	for i, arm := range stmt.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(stmt.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.next"))
		}
		if _, _, err := s.emitMatchPatternTest(arm.Pattern, treeValue, nil, actualType, nil, stmt.Value, nil, bodyBB, nextBB); err != nil {
			return err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if err := s.emitBlockInCurrentScope(arm.Body); err != nil {
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
	if exhaustive {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if allTerminated && exhaustive {
		C.LLVMBuildUnreachable(s.builder)
	}
	return nil
}

func (s *functionState) emitConstEnumMatch(stmt *ast.MatchStmt, constEnumType *semantic.ConstEnumType) error {
	if constEnumType == nil {
		return fmt.Errorf("match requires a const enum value")
	}
	if stmt.Store != nil {
		return fmt.Errorf("const enum match over %q does not take an in-store clause", constEnumType.Name)
	}
	actualType := s.exprType(stmt.Value)
	value, _, err := s.emitExpr(stmt.Value, actualType)
	if err != nil {
		return err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.fail"))
	allTerminated := true
	exhaustive := constEnumMatchIsExhaustive(constEnumType, stmt.Arms)
	for i, arm := range stmt.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(stmt.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.next"))
		}
		if _, _, err := s.emitMatchPatternTest(arm.Pattern, value, nil, actualType, nil, stmt.Value, nil, bodyBB, nextBB); err != nil {
			return err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if err := s.emitBlockInCurrentScope(arm.Body); err != nil {
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
	if exhaustive {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if allTerminated && exhaustive {
		C.LLVMBuildUnreachable(s.builder)
	}
	return nil
}

func errorSetMatchIsExhaustive(errorSetType *semantic.ErrorSetType, arms []ast.MatchArm) bool {
	if errorSetType == nil {
		return false
	}
	covered := make(map[string]bool, len(errorSetType.Tags))
	hasWildcard := false
	for _, arm := range arms {
		switch pattern := arm.Pattern.(type) {
		case *ast.MatchWildcardPattern:
			hasWildcard = true
		case *ast.MatchVariantPattern:
			if errorSetType.HasQualifiedTag(errorSetType.Name, pattern.Variant) {
				covered[semantic.QualifyErrorTag(errorSetType.Name, pattern.Variant)] = true
			}
		}
	}
	if hasWildcard {
		return true
	}
	for _, tag := range errorSetType.Tags {
		if !covered[tag] {
			return false
		}
	}
	return true
}

func (s *functionState) emitErrorSetMatch(stmt *ast.MatchStmt, errorSetType *semantic.ErrorSetType) error {
	if errorSetType == nil {
		return fmt.Errorf("match requires an error set value")
	}
	if stmt.Store != nil {
		return fmt.Errorf("error-set match over %q does not take an in-store clause", errorSetType.Name)
	}
	actualType := s.exprType(stmt.Value)
	value, _, err := s.emitExpr(stmt.Value, actualType)
	if err != nil {
		return err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.fail"))
	allTerminated := true
	exhaustive := errorSetMatchIsExhaustive(errorSetType, stmt.Arms)
	for i, arm := range stmt.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(stmt.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.next"))
		}
		if _, _, err := s.emitMatchPatternTest(arm.Pattern, value, nil, actualType, nil, stmt.Value, nil, bodyBB, nextBB); err != nil {
			return err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if err := s.emitBlockInCurrentScope(arm.Body); err != nil {
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
	if exhaustive {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if allTerminated && exhaustive {
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
	constEnumType, ok := resolveMatchableConstEnumType(s.exprType(expr.Value))
	if ok {
		return s.emitConstEnumMatchExpr(expr, resultType, constEnumType)
	}
	errorSetType, ok := resolveMatchableErrorSetTypeBackend(s.exprType(expr.Value))
	if ok {
		return s.emitErrorSetMatchExpr(expr, resultType, errorSetType)
	}
	treeType, _, ok := resolveMatchableTreeCategoryTypeBackend(s.exprType(expr.Value))
	if ok {
		return s.emitTreeMatchExpr(expr, resultType, treeType)
	}
	if isStringMatchableType(s.exprType(expr.Value)) {
		return s.emitStringMatchExpr(expr, resultType)
	}
	if resolveMatchableTupleTypeBackend(s.exprType(expr.Value)) {
		return s.emitTupleMatchExpr(expr, resultType)
	}
	if resolveMatchableStructTypeBackend(s.exprType(expr.Value)) {
		return s.emitStructMatchExpr(expr, resultType)
	}
	return nil, nil, fmt.Errorf("match requires an enum, const enum, error set, tree-category, string, tuple, or struct value")
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
	preloadedCommonValues, err := s.preloadPackedMatchCommonFieldValues(enumType, expr.Value, enumValue, decodedMatchValue, storeBinding, expr.Arms)
	if err != nil {
		return nil, nil, err
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
	exhaustive := matchIsExhaustive(enumType, expr.Arms)
	if packedEnumMatchCanUseTagSwitch(enumType, expr.Arms) {
		wildcardIndex := -1
		variantArmCount := 0
		for i, arm := range expr.Arms {
			switch arm.Pattern.(type) {
			case *ast.MatchVariantPattern:
				variantArmCount++
			case *ast.MatchWildcardPattern:
				wildcardIndex = i
			}
		}
		var wildcardBB C.LLVMBasicBlockRef
		defaultBB := failBB
		if wildcardIndex >= 0 {
			wildcardBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.wildcard"))
			defaultBB = wildcardBB
		}
		switchInst := C.LLVMBuildSwitch(s.builder, matchTagValue, defaultBB, C.unsigned(variantArmCount))
		for _, arm := range expr.Arms {
			pattern, ok := arm.Pattern.(*ast.MatchVariantPattern)
			if !ok {
				continue
			}
			variant, _ := enumType.Variant(pattern.Variant)
			tagConst, err := s.enumTagConstant(variant.Tag)
			if err != nil {
				return nil, nil, err
			}
			dispatchBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.dispatch"))
			bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.arm"))
			C.LLVMAddCase(switchInst, tagConst, dispatchBB)

			C.LLVMPositionBuilderAtEnd(s.builder, dispatchBB)
			patternFailureBB := failBB
			if wildcardBB != nil {
				patternFailureBB = wildcardBB
			}
			armDecodedValue, armPayloadValues, err := s.emitMatchedVariantPayloadPatternTest(pattern, enumValue, decodedMatchValue, enumType, variant, storeBinding, expr.Value, bodyBB, patternFailureBB)
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
			if hasValuePath && !preloadedCommonValues.empty() {
				s.bindPackedCommonFieldValues(valuePath, enumType, preloadedCommonValues)
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
		}
		if wildcardIndex >= 0 {
			arm := expr.Arms[wildcardIndex]
			C.LLVMPositionBuilderAtEnd(s.builder, wildcardBB)
			s.pushScope()
			if hasValueIdent && enumType.Packed && decodedMatchValue != nil {
				s.bindPackedEnumStorage(valueIdent.Name, enumType, decodedMatchValue)
			}
			if hasValuePath && enumType.Packed {
				s.bindPackedEnumStoreOrigin(valuePath, enumType, storeBinding)
			}
			s.bindMatchedPackedVariantView(valuePath, arm.Pattern, enumValue, decodedMatchValue, enumType, storeBinding, packedPayloadValueCache{})
			if hasValuePath && !preloadedCommonValues.empty() {
				s.bindPackedCommonFieldValues(valuePath, enumType, preloadedCommonValues)
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
		}

		C.LLVMPositionBuilderAtEnd(s.builder, failBB)
		if semantic.IsNeverType(resultType) || exhaustive {
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
		if hasValuePath && !preloadedCommonValues.empty() {
			s.bindPackedCommonFieldValues(valuePath, enumType, preloadedCommonValues)
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
	if semantic.IsNeverType(resultType) || exhaustive {
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

func (s *functionState) emitConstEnumMatchExpr(expr *ast.MatchExpr, resultType semantic.Type, constEnumType *semantic.ConstEnumType) (C.LLVMValueRef, semantic.Type, error) {
	if constEnumType == nil {
		return nil, nil, fmt.Errorf("match requires a const enum value")
	}
	if expr.Store != nil {
		return nil, nil, fmt.Errorf("const enum match over %q does not take an in-store clause", constEnumType.Name)
	}
	actualType := s.exprType(expr.Value)
	value, _, err := s.emitExpr(expr.Value, actualType)
	if err != nil {
		return nil, nil, err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(expr.Arms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(expr.Arms)+1)
	exhaustive := constEnumMatchIsExhaustive(constEnumType, expr.Arms)
	for i, arm := range expr.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(expr.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.next"))
		}
		if _, _, err := s.emitMatchPatternTest(arm.Pattern, value, nil, actualType, nil, expr.Value, nil, bodyBB, nextBB); err != nil {
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

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if exhaustive || semantic.IsNeverType(resultType) {
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

func (s *functionState) emitErrorSetMatchExpr(expr *ast.MatchExpr, resultType semantic.Type, errorSetType *semantic.ErrorSetType) (C.LLVMValueRef, semantic.Type, error) {
	if errorSetType == nil {
		return nil, nil, fmt.Errorf("match requires an error set value")
	}
	if expr.Store != nil {
		return nil, nil, fmt.Errorf("error-set match over %q does not take an in-store clause", errorSetType.Name)
	}
	actualType := s.exprType(expr.Value)
	value, _, err := s.emitExpr(expr.Value, actualType)
	if err != nil {
		return nil, nil, err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(expr.Arms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(expr.Arms)+1)
	exhaustive := errorSetMatchIsExhaustive(errorSetType, expr.Arms)
	for i, arm := range expr.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(expr.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.next"))
		}
		if _, _, err := s.emitMatchPatternTest(arm.Pattern, value, nil, actualType, nil, expr.Value, nil, bodyBB, nextBB); err != nil {
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

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if exhaustive || semantic.IsNeverType(resultType) {
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

func (s *functionState) emitTreeMatchExpr(expr *ast.MatchExpr, resultType semantic.Type, treeType *semantic.TreeCategoryType) (C.LLVMValueRef, semantic.Type, error) {
	if treeType == nil {
		return nil, nil, fmt.Errorf("match requires a tree-category value")
	}
	if expr.Store != nil {
		return nil, nil, fmt.Errorf("tree match over %q does not take an in-store clause", treeType.Name)
	}
	actualType := s.exprType(expr.Value)
	treeValue, _, err := s.emitExpr(expr.Value, nil)
	if err != nil {
		return nil, nil, err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(expr.Arms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(expr.Arms)+1)
	exhaustive := treeMatchIsExhaustive(treeType, expr.Arms)

	for i, arm := range expr.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(expr.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.next"))
		}
		if _, _, err := s.emitMatchPatternTest(arm.Pattern, treeValue, nil, actualType, nil, expr.Value, nil, bodyBB, nextBB); err != nil {
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

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if semantic.IsNeverType(resultType) || exhaustive {
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

type treeVisitRelevantArm struct {
	arm      ast.VisitArm
	variant  *semantic.EnumVariant
	wildcard bool
}

type treeVisitExactArm struct {
	arm      ast.VisitArm
	member   semantic.Type
	wildcard bool
}

func exactTreeVisitArm(memberName string, arms []ast.VisitArm) (ast.VisitArm, bool, bool) {
	for _, arm := range arms {
		if arm.Wildcard {
			return arm, true, true
		}
		if arm.TargetName == memberName {
			return arm, true, false
		}
	}
	return ast.VisitArm{}, false, false
}

func exactTreeVisitArms(memberName string, arms []ast.VisitArm) []ast.VisitArm {
	matched := make([]ast.VisitArm, 0)
	for _, arm := range arms {
		if arm.Wildcard || arm.TargetName == memberName {
			matched = append(matched, arm)
		}
	}
	return matched
}

func visitArmsHaveGuard(arms []ast.VisitArm) bool {
	for _, arm := range arms {
		if arm.Guard != nil {
			return true
		}
	}
	return false
}

func visitArmsCoverExactMember(memberName string, arms []ast.VisitArm) bool {
	for _, arm := range arms {
		if arm.Guard != nil {
			continue
		}
		if arm.Wildcard || arm.TargetName == memberName {
			return true
		}
	}
	return false
}

func (s *functionState) treeVisitRelevantArms(categoryType *semantic.TreeCategoryType, arms []ast.VisitArm) ([]treeVisitRelevantArm, bool, error) {
	relevant := make([]treeVisitRelevantArm, 0, len(arms))
	exhaustive := false
	covered := map[string]bool{}
	for _, arm := range arms {
		if arm.Wildcard {
			relevant = append(relevant, treeVisitRelevantArm{arm: arm, wildcard: true})
			if arm.Guard == nil {
				exhaustive = true
			}
			continue
		}
		idx := strings.LastIndex(arm.TargetName, ".")
		if idx <= 0 || idx+1 >= len(arm.TargetName) {
			continue
		}
		categoryName := arm.TargetName[:idx]
		variantName := arm.TargetName[idx+1:]
		if categoryName != categoryType.Name {
			continue
		}
		variant, ok := categoryType.Variant(variantName)
		if !ok {
			return nil, false, fmt.Errorf("tree category %s has no variant %s", categoryType.Name, variantName)
		}
		relevant = append(relevant, treeVisitRelevantArm{arm: arm, variant: variant})
		if arm.Guard == nil {
			covered[variant.Name] = true
		}
	}
	if !exhaustive && categoryType != nil {
		exhaustive = len(covered) == len(categoryType.Variants)
	}
	return relevant, exhaustive, nil
}

func (s *functionState) treeVisitRelevantExactArms(treeType *semantic.TreeType, arms []ast.VisitArm) ([]treeVisitExactArm, bool, error) {
	if treeType == nil {
		return nil, false, fmt.Errorf("missing tree family for visit lowering")
	}
	relevant := make([]treeVisitExactArm, 0, len(semantic.TreeFamilyExactMembersInTagOrder(treeType)))
	exhaustive := false
	for _, member := range semantic.TreeFamilyExactMembersInTagOrder(treeType) {
		memberName := treeExactMemberSurfaceName(member)
		arm, ok, wildcard := exactTreeVisitArm(memberName, arms)
		if !ok {
			continue
		}
		relevant = append(relevant, treeVisitExactArm{arm: arm, member: member, wildcard: wildcard})
		if wildcard && arm.Guard == nil {
			exhaustive = true
		}
	}
	if !exhaustive {
		exhaustive = true
		for _, member := range semantic.TreeFamilyExactMembersInTagOrder(treeType) {
			if !visitArmsCoverExactMember(treeExactMemberSurfaceName(member), arms) {
				exhaustive = false
				break
			}
		}
	}
	return relevant, exhaustive, nil
}

func (s *functionState) emitExactVisitArmSequence(value C.LLVMValueRef, bindType semantic.Type, arms []ast.VisitArm, resultType semantic.Type, name string) (C.LLVMValueRef, bool, error) {
	if len(arms) == 0 {
		if semantic.IsNeverType(resultType) {
			C.LLVMBuildUnreachable(s.builder)
			return nil, true, nil
		}
		llvmType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, false, err
		}
		return C.LLVMGetUndef(llvmType), false, nil
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(arms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(arms)+1)
	for i, arm := range arms {
		bodyEntryBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".arm.entry"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".next"))
		}
		C.LLVMBuildBr(s.builder, bodyEntryBB)
		C.LLVMPositionBuilderAtEnd(s.builder, bodyEntryBB)
		s.pushScope()
		if arm.BindName != "" && arm.BindName != "_" {
			if err := s.emitMoveBindLocal(arm.BindName, bindType, value); err != nil {
				s.popScope()
				return nil, false, err
			}
		}
		if arm.Guard != nil {
			guardBodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".guard.body"))
			guardValue, _, err := s.emitExpr(arm.Guard, s.g.result.NamedTypes["bool"])
			if err != nil {
				s.popScope()
				return nil, false, err
			}
			C.LLVMBuildCondBr(s.builder, guardValue, guardBodyBB, nextBB)
			C.LLVMPositionBuilderAtEnd(s.builder, guardBodyBB)
		}
		armValue, reachable, err := s.emitMatchExprArmBody(arm.Body, resultType)
		if err != nil {
			s.popScope()
			return nil, false, err
		}
		if reachable && !s.currentBlockTerminated() {
			inBlock := C.LLVMGetInsertBlock(s.builder)
			incomingValues = append(incomingValues, armValue)
			incomingBlocks = append(incomingBlocks, inBlock)
			C.LLVMBuildBr(s.builder, mergeBB)
		}
		s.popScope()
		C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
	}
	if semantic.IsNeverType(resultType) {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		llvmType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, false, err
		}
		inBlock := C.LLVMGetInsertBlock(s.builder)
		incomingValues = append(incomingValues, C.LLVMGetUndef(llvmType))
		incomingBlocks = append(incomingBlocks, inBlock)
		C.LLVMBuildBr(s.builder, mergeBB)
	}
	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingValues) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil, true, nil
	}
	if len(incomingValues) == 1 || semantic.IsNeverType(resultType) {
		return incomingValues[0], false, nil
	}
	llvmType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, false, err
	}
	phi := C.LLVMBuildPhi(s.builder, llvmType, cStringFree(name+".phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, false, nil
}

func (s *functionState) emitVisitExpr(expr *ast.VisitExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil {
		return nil, nil, fmt.Errorf("missing visit expression")
	}
	actualType := s.exprType(expr.Value)
	resultType := s.exprType(expr)
	switch tt := semantic.StripAggregateStateType(actualType).(type) {
	case *semantic.TreeNodeType:
		return s.emitFamilyTreeVisitExpr(expr, tt, resultType)
	case *semantic.TreeBlockType:
		return s.emitExactTreeVisitExpr(expr, tt.Name, tt, resultType)
	case *semantic.TreeStructType:
		return s.emitExactTreeVisitExpr(expr, tt.Name, tt, resultType)
	}
	categoryType, _, ok := resolveMatchableTreeCategoryTypeBackend(actualType)
	if !ok || categoryType == nil {
		return nil, nil, fmt.Errorf("visit expression lowering currently requires a tree category, tree block, or tree struct source, got %s", actualType.String())
	}
	treeValue, _, err := s.emitExpr(expr.Value, nil)
	if err != nil {
		return nil, nil, err
	}
	relevantArms, exhaustive, err := s.treeVisitRelevantArms(categoryType, expr.Arms)
	if err != nil {
		return nil, nil, err
	}
	if len(relevantArms) == 0 {
		return nil, nil, fmt.Errorf("visit expression over %s has no relevant arms", categoryType.Name)
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.expr.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.expr.fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(relevantArms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(relevantArms)+1)

	for i, armInfo := range relevantArms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.expr.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(relevantArms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.expr.next"))
		}
		if armInfo.wildcard {
			C.LLVMBuildBr(s.builder, bodyBB)
		} else {
			matchPattern := &ast.MatchVariantPattern{Position: armInfo.arm.Position, EnumName: categoryType.Name, Variant: armInfo.variant.Name}
			if _, _, err := s.emitMatchPatternTest(matchPattern, treeValue, nil, actualType, nil, expr.Value, nil, bodyBB, nextBB); err != nil {
				return nil, nil, err
			}
		}
		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if armInfo.arm.BindName != "" && armInfo.arm.BindName != "_" && armInfo.variant != nil {
			viewType := categoryType.VariantViewType(armInfo.variant)
			if err := s.emitMoveBindLocal(armInfo.arm.BindName, viewType, treeValue); err != nil {
				s.popScope()
				return nil, nil, err
			}
		}
		if armInfo.arm.Guard != nil {
			guardBodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.expr.guard.body"))
			guardValue, _, err := s.emitExpr(armInfo.arm.Guard, s.g.result.NamedTypes["bool"])
			if err != nil {
				s.popScope()
				return nil, nil, err
			}
			C.LLVMBuildCondBr(s.builder, guardValue, guardBodyBB, nextBB)
			C.LLVMPositionBuilderAtEnd(s.builder, guardBodyBB)
		}
		armValue, reachable, err := s.emitMatchExprArmBody(armInfo.arm.Body, resultType)
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
		if nextBB != mergeBB && !armInfo.wildcard {
			C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if semantic.IsNeverType(resultType) || exhaustive {
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
	phi := C.LLVMBuildPhi(s.builder, llvmType, cStringFree("visit.expr.phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}

func (s *functionState) emitFamilyTreeVisitExpr(expr *ast.VisitExpr, rootType *semantic.TreeNodeType, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if rootType == nil || rootType.Family == nil {
		return nil, nil, fmt.Errorf("missing family-root visit metadata")
	}
	treeValue, _, err := s.emitExpr(expr.Value, nil)
	if err != nil {
		return nil, nil, err
	}
	if visitArmsHaveGuard(expr.Arms) {
		mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.node.end"))
		failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.node.fail"))
		tagValue, err := s.emitTreeHandleTagValue(treeValue, "visit.node")
		if err != nil {
			return nil, nil, err
		}
		members := semantic.TreeFamilyExactMembersInTagOrder(rootType.Family)
		switchInst := C.LLVMBuildSwitch(s.builder, tagValue, failBB, C.unsigned(len(members)))
		incomingValues := make([]C.LLVMValueRef, 0, len(members)+1)
		incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(members)+1)
		for _, member := range members {
			memberName := treeExactMemberSurfaceName(member)
			memberArms := exactTreeVisitArms(memberName, expr.Arms)
			if len(memberArms) == 0 {
				continue
			}
			bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.node.arm"))
			tag, ok := treeExactMemberTag(member)
			if !ok {
				return nil, nil, fmt.Errorf("missing exact tag for %s", memberName)
			}
			tagConst, err := s.errorCodeConstant(tag)
			if err != nil {
				return nil, nil, err
			}
			C.LLVMAddCase(switchInst, tagConst, bodyBB)
			C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
			armValue, terminated, err := s.emitExactVisitArmSequence(treeValue, member, memberArms, resultType, "visit.node.exact")
			if err != nil {
				return nil, nil, err
			}
			if !terminated && !s.currentBlockTerminated() {
				inBlock := C.LLVMGetInsertBlock(s.builder)
				incomingValues = append(incomingValues, armValue)
				incomingBlocks = append(incomingBlocks, inBlock)
				C.LLVMBuildBr(s.builder, mergeBB)
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
			inBlock := C.LLVMGetInsertBlock(s.builder)
			incomingValues = append(incomingValues, C.LLVMGetUndef(llvmType))
			incomingBlocks = append(incomingBlocks, inBlock)
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
		phi := C.LLVMBuildPhi(s.builder, llvmType, cStringFree("visit.node.phi"))
		C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
		return phi, resultType, nil
	}
	relevantArms, exhaustive, err := s.treeVisitRelevantExactArms(rootType.Family, expr.Arms)
	if err != nil {
		return nil, nil, err
	}
	if len(relevantArms) == 0 {
		return nil, nil, fmt.Errorf("visit expression over %s has no relevant arms", rootType.String())
	}
	tagValue, err := s.emitTreeHandleTagValue(treeValue, "visit.node")
	if err != nil {
		return nil, nil, err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.node.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.node.fail"))
	switchInst := C.LLVMBuildSwitch(s.builder, tagValue, failBB, C.unsigned(len(relevantArms)))
	incomingValues := make([]C.LLVMValueRef, 0, len(relevantArms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(relevantArms)+1)
	for _, armInfo := range relevantArms {
		if armInfo.member == nil {
			continue
		}
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.node.arm"))
		tag, ok := treeExactMemberTag(armInfo.member)
		if !ok {
			return nil, nil, fmt.Errorf("missing exact tag for %s", treeExactMemberSurfaceName(armInfo.member))
		}
		tagConst, err := s.errorCodeConstant(tag)
		if err != nil {
			return nil, nil, err
		}
		C.LLVMAddCase(switchInst, tagConst, bodyBB)
		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if armInfo.arm.BindName != "" && armInfo.arm.BindName != "_" {
			if err := s.emitMoveBindLocal(armInfo.arm.BindName, armInfo.member, treeValue); err != nil {
				s.popScope()
				return nil, nil, err
			}
		}
		armValue, reachable, err := s.emitMatchExprArmBody(armInfo.arm.Body, resultType)
		if err != nil {
			s.popScope()
			return nil, nil, err
		}
		if reachable && !s.currentBlockTerminated() {
			incomingValues = append(incomingValues, armValue)
			incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
			C.LLVMBuildBr(s.builder, mergeBB)
		}
		s.popScope()
	}
	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if semantic.IsNeverType(resultType) || exhaustive {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		llvmType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, nil, err
		}
		undefValue := C.LLVMGetUndef(llvmType)
		incomingValues = append(incomingValues, undefValue)
		incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
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
	phi := C.LLVMBuildPhi(s.builder, llvmType, cStringFree("visit.node.phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}

func (s *functionState) emitExactTreeVisitExpr(expr *ast.VisitExpr, memberName string, bindType semantic.Type, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	value, _, err := s.emitExpr(expr.Value, bindType)
	if err != nil {
		return nil, nil, err
	}
	if visitArmsHaveGuard(expr.Arms) {
		armValue, _, err := s.emitExactVisitArmSequence(value, bindType, exactTreeVisitArms(memberName, expr.Arms), resultType, "visit.exact")
		return armValue, resultType, err
	}
	arm, ok, _ := exactTreeVisitArm(memberName, expr.Arms)
	if !ok {
		if semantic.IsNeverType(resultType) {
			C.LLVMBuildUnreachable(s.builder)
			return nil, resultType, nil
		}
		llvmType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, nil, err
		}
		return C.LLVMGetUndef(llvmType), resultType, nil
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.exact.end"))
	bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.exact.arm"))
	C.LLVMBuildBr(s.builder, bodyBB)
	C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
	s.pushScope()
	if arm.BindName != "" && arm.BindName != "_" {
		if err := s.emitMoveBindLocal(arm.BindName, bindType, value); err != nil {
			s.popScope()
			return nil, nil, err
		}
	}
	armValue, reachable, err := s.emitMatchExprArmBody(arm.Body, resultType)
	if err != nil {
		s.popScope()
		return nil, nil, err
	}
	var incomingValue C.LLVMValueRef
	hasIncoming := false
	if reachable && !s.currentBlockTerminated() {
		incomingValue = armValue
		hasIncoming = true
		C.LLVMBuildBr(s.builder, mergeBB)
	}
	s.popScope()
	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if !hasIncoming {
		C.LLVMBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	return incomingValue, resultType, nil
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
		if err := s.emitBlockInCurrentScope(arm.Body); err != nil {
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

func (s *functionState) emitStructMatch(stmt *ast.MatchStmt) error {
	if stmt.Store != nil {
		return fmt.Errorf("struct match does not take an in-store clause")
	}
	actualType := s.exprType(stmt.Value)
	value, _, err := s.emitExpr(stmt.Value, nil)
	if err != nil {
		return err
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
		if _, _, err := s.emitMatchPatternTest(arm.Pattern, value, nil, actualType, nil, stmt.Value, nil, bodyBB, nextBB); err != nil {
			return err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if err := s.emitBlockInCurrentScope(arm.Body); err != nil {
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

func (s *functionState) emitTupleMatch(stmt *ast.MatchStmt) error {
	if stmt.Store != nil {
		return fmt.Errorf("tuple match does not take an in-store clause")
	}
	actualType := s.exprType(stmt.Value)
	value, _, err := s.emitExpr(stmt.Value, nil)
	if err != nil {
		return err
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
		if _, _, err := s.emitMatchPatternTest(arm.Pattern, value, nil, actualType, nil, stmt.Value, nil, bodyBB, nextBB); err != nil {
			return err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if err := s.emitBlockInCurrentScope(arm.Body); err != nil {
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

func (s *functionState) emitSequenceMatch(stmt *ast.MatchStmt) error {
	if stmt.Store != nil {
		return fmt.Errorf("sequence match does not take an in-store clause")
	}
	actualType := s.exprType(stmt.Value)
	value, _, err := s.emitExpr(stmt.Value, nil)
	if err != nil {
		return err
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
		if _, _, err := s.emitMatchPatternTest(arm.Pattern, value, nil, actualType, nil, stmt.Value, nil, bodyBB, nextBB); err != nil {
			return err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if err := s.emitBlockInCurrentScope(arm.Body); err != nil {
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

func (s *functionState) emitStructMatchExpr(expr *ast.MatchExpr, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if expr.Store != nil {
		return nil, nil, fmt.Errorf("struct match does not take an in-store clause")
	}
	actualType := s.exprType(expr.Value)
	value, _, err := s.emitExpr(expr.Value, nil)
	if err != nil {
		return nil, nil, err
	}
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
		if _, _, err := s.emitMatchPatternTest(arm.Pattern, value, nil, actualType, nil, expr.Value, nil, bodyBB, nextBB); err != nil {
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

func (s *functionState) emitTupleMatchExpr(expr *ast.MatchExpr, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if expr.Store != nil {
		return nil, nil, fmt.Errorf("tuple match does not take an in-store clause")
	}
	actualType := s.exprType(expr.Value)
	value, _, err := s.emitExpr(expr.Value, nil)
	if err != nil {
		return nil, nil, err
	}
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
		if _, _, err := s.emitMatchPatternTest(arm.Pattern, value, nil, actualType, nil, expr.Value, nil, bodyBB, nextBB); err != nil {
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
	scope := s.scope
	for i, stmt := range body {
		isLast := i == len(body)-1
		if !isLast {
			if err := s.emitStmt(stmt); err != nil {
				s.discardScopeCleanups(scope)
				return nil, false, err
			}
			if s.currentBlockTerminated() {
				s.discardScopeCleanups(scope)
				return nil, false, nil
			}
			continue
		}
		if exprStmt, ok := stmt.(*ast.ExprStmt); ok {
			value, _, err := s.emitExpr(exprStmt.Expr, resultType)
			if err != nil {
				s.discardScopeCleanups(scope)
				return nil, false, err
			}
			if s.currentBlockTerminated() {
				s.discardScopeCleanups(scope)
				return nil, false, nil
			}
			if err := s.emitScopeCleanups(scope); err != nil {
				return nil, false, err
			}
			if s.currentBlockTerminated() {
				return nil, false, nil
			}
			return value, true, nil
		}
		if err := s.emitStmt(stmt); err != nil {
			s.discardScopeCleanups(scope)
			return nil, false, err
		}
		if s.currentBlockTerminated() {
			s.discardScopeCleanups(scope)
			return nil, false, nil
		}
		if err := s.emitScopeCleanups(scope); err != nil {
			return nil, false, err
		}
		if s.currentBlockTerminated() {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("match expression arm must end with an expression")
	}
	return nil, false, fmt.Errorf("match expression arm must end with an expression")
}

func (s *functionState) predicateMatchPatternFuncType(name string) *semantic.FuncType {
	if s == nil || s.g == nil || s.g.result == nil || s.g.result.GlobalScope == nil || name == "" {
		return nil
	}
	sym, ok := s.g.result.GlobalScope.Lookup(name)
	if !ok || sym == nil {
		return nil
	}
	fnType, ok := sym.Type.(*semantic.FuncType)
	if !ok || fnType == nil || backendExplicitParamCount(fnType, nil) != 1 || len(fnType.Params) == 0 || !semantic.IsBoolType(fnType.Return) {
		return nil
	}
	return s.specializeFunctionType(fnType)
}

func (s *functionState) emitPredicateMatchPatternTest(name string, actualValue C.LLVMValueRef, actualType semantic.Type, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) (bool, error) {
	fnType := s.predicateMatchPatternFuncType(name)
	if fnType == nil {
		return false, nil
	}
	arg := actualValue
	if !semantic.SameType(actualType, fnType.Params[0]) {
		var err error
		arg, err = s.coerceValue(actualValue, actualType, fnType.Params[0])
		if err != nil {
			return true, err
		}
	}
	callee, err := s.g.ensureFunctionDeclared(name, fnType)
	if err != nil {
		return true, err
	}
	llvmFnType, err := s.g.lowerFunctionType(fnType)
	if err != nil {
		return true, err
	}
	call := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arg}, "match.predicate")
	C.LLVMBuildCondBr(s.builder, call, successBB, failureBB)
	return true, nil
}

func (s *functionState) emitSequenceCountValue(expr ast.Expr, actualType semantic.Type, name string) (C.LLVMValueRef, error) {
	actualType = semantic.StripAggregateStateType(actualType)
	switch t := actualType.(type) {
	case *semantic.ArrayType:
		return s.safeIndexArrayCountValue(t)
	case *semantic.DArrayType:
		ptr, _, err := s.emitAddressOrTemp(expr)
		if err != nil {
			return nil, err
		}
		return s.emitContainerCountValue(ptr, t, name)
	case *semantic.ViewType:
		ptr, _, err := s.emitAddressOrTemp(expr)
		if err != nil {
			return nil, err
		}
		return s.emitContainerCountValue(ptr, t, name)
	case *semantic.DArrayViewType:
		ptr, _, err := s.emitAddressOrTemp(expr)
		if err != nil {
			return nil, err
		}
		return s.emitContainerCountValue(ptr, t, name)
	case *semantic.RefType:
		if t.State != semantic.RefStateNonNull {
			return nil, fmt.Errorf("list pattern requires a non-null sequence reference")
		}
		switch elem := semantic.StripAggregateStateType(t.Elem).(type) {
		case *semantic.ArrayType:
			return s.safeIndexArrayCountValue(elem)
		case *semantic.DArrayType, *semantic.ViewType, *semantic.DArrayViewType:
			ptr, _, err := s.emitExpr(expr, actualType)
			if err != nil {
				return nil, err
			}
			return s.emitContainerCountValue(ptr, elem, name)
		default:
			return nil, fmt.Errorf("list pattern length is not implemented for %s", actualType.String())
		}
	default:
		return nil, fmt.Errorf("list pattern length is not implemented for %s", actualType.String())
	}
}

func countMatchPatternExpectedLen(pattern ast.MatchPattern) (uint64, bool, error) {
	structPattern, ok := pattern.(*ast.MatchStructPattern)
	if !ok || structPattern == nil || structPattern.TypeName != "count" || structPattern.Brace {
		return 0, false, nil
	}
	if len(structPattern.Args) != 1 || structPattern.Args[0].Name != "" {
		return 0, true, fmt.Errorf("count pattern expects one positional integer literal")
	}
	literal, ok := structPattern.Args[0].Pattern.(*ast.MatchLiteralPattern)
	if !ok {
		return 0, true, fmt.Errorf("count pattern expects an integer literal")
	}
	intLit, ok := literal.Value.(*ast.IntLit)
	if !ok {
		return 0, true, fmt.Errorf("count pattern expects an integer literal")
	}
	value, err := strconv.ParseUint(intLit.Value, 10, 64)
	if err != nil {
		return 0, true, fmt.Errorf("count pattern expects a non-negative integer literal")
	}
	return value, true, nil
}

func (s *functionState) emitSequenceCountValueFromPatternValue(actualValue C.LLVMValueRef, actualType semantic.Type, originExpr ast.Expr, name string) (C.LLVMValueRef, error) {
	actualType = semantic.StripAggregateStateType(actualType)
	switch t := actualType.(type) {
	case *semantic.ArrayType:
		return s.safeIndexArrayCountValue(t)
	case *semantic.DArrayType, *semantic.ViewType, *semantic.DArrayViewType:
		if actualValue == nil {
			if originExpr == nil {
				return nil, fmt.Errorf("count pattern requires a sequence value")
			}
			return s.emitSequenceCountValue(originExpr, actualType, name)
		}
		return C.LLVMBuildExtractValue(s.builder, actualValue, 1, cStringFree(name)), nil
	case *semantic.RefType:
		if t.State != semantic.RefStateNonNull {
			return nil, fmt.Errorf("count pattern requires a non-null sequence reference")
		}
		switch elem := semantic.StripAggregateStateType(t.Elem).(type) {
		case *semantic.ArrayType:
			return s.safeIndexArrayCountValue(elem)
		case *semantic.DArrayType, *semantic.ViewType, *semantic.DArrayViewType:
			if actualValue == nil {
				return nil, fmt.Errorf("count pattern requires a sequence reference value")
			}
			return s.emitContainerCountValue(actualValue, elem, name)
		default:
			return nil, fmt.Errorf("count pattern length is not implemented for %s", actualType.String())
		}
	case *semantic.DStrType, *semantic.SViewType:
		if originExpr == nil {
			return nil, fmt.Errorf("count pattern length requires an origin expression for %s", actualType.String())
		}
		return s.emitSequenceCountValue(originExpr, actualType, name)
	default:
		return nil, fmt.Errorf("count pattern length is not implemented for %s", actualType.String())
	}
}

func (s *functionState) emitCountMatchPatternTest(pattern ast.MatchPattern, actualValue C.LLVMValueRef, actualType semantic.Type, originExpr ast.Expr, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) (bool, error) {
	expected, ok, err := countMatchPatternExpectedLen(pattern)
	if !ok || err != nil {
		return ok, err
	}
	if _, sequence := semantic.SequenceMatchElementType(actualType); !sequence {
		return true, fmt.Errorf("count pattern requires an array, darray, view, or string-like value, got %s", actualType.String())
	}
	countValue, err := s.emitSequenceCountValueFromPatternValue(actualValue, actualType, originExpr, "match.count")
	if err != nil {
		return true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return true, err
	}
	expectedValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(expected), 0)
	countOK := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), countValue, expectedValue, cStringFree("match.count.ok"))
	C.LLVMBuildCondBr(s.builder, countOK, successBB, failureBB)
	return true, nil
}

func (s *functionState) emitListMatchPatternTest(pattern *ast.MatchListPattern, actualValue C.LLVMValueRef, actualType semantic.Type, originExpr ast.Expr, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) error {
	if pattern == nil {
		C.LLVMBuildBr(s.builder, successBB)
		return nil
	}
	if originExpr == nil {
		return fmt.Errorf("list pattern lowering requires an origin expression")
	}
	elemType, ok := semantic.SequenceMatchElementType(actualType)
	if !ok {
		return fmt.Errorf("list pattern requires an array, darray, view, or string-like value, got %s", actualType.String())
	}
	countValue, err := s.emitSequenceCountValue(originExpr, actualType, "match.list.len")
	if err != nil {
		return err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	expectedLen := C.LLVMConstInt(usizeLLVMType, C.ulonglong(len(pattern.Elems)), 0)
	lenOK := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), countValue, expectedLen, cStringFree("match.list.len.ok"))
	itemsBB := successBB
	if len(pattern.Elems) != 0 {
		itemsBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.list.items"))
	}
	C.LLVMBuildCondBr(s.builder, lenOK, itemsBB, failureBB)
	if len(pattern.Elems) == 0 {
		return nil
	}
	C.LLVMPositionBuilderAtEnd(s.builder, itemsBB)
	for i, elem := range pattern.Elems {
		indexExpr := &ast.IndexExpr{
			Position: elem.Pos(),
			Object:   originExpr,
			Index:    &ast.IntLit{Position: elem.Pos(), Value: strconv.Itoa(i), Suffix: "u"},
		}
		elemValue, _, err := s.emitExpr(indexExpr, elemType)
		if err != nil {
			return err
		}
		nextSuccess := successBB
		if i != len(pattern.Elems)-1 {
			nextSuccess = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.list.pattern.next"))
		}
		if _, _, err := s.emitMatchPatternTest(elem, elemValue, nil, elemType, nil, indexExpr, nil, nextSuccess, failureBB); err != nil {
			return err
		}
		if i != len(pattern.Elems)-1 {
			C.LLVMPositionBuilderAtEnd(s.builder, nextSuccess)
		}
	}
	return nil
}

func (s *functionState) emitMatchPatternTest(pattern ast.MatchPattern, actualValue C.LLVMValueRef, decodedActualValue C.LLVMValueRef, actualType semantic.Type, store *packedStoreBinding, originExpr ast.Expr, precomputedTagValue C.LLVMValueRef, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) (C.LLVMValueRef, packedPayloadValueCache, error) {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		C.LLVMBuildBr(s.builder, successBB)
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchBindPattern:
		if handled, err := s.emitPredicateMatchPatternTest(p.Name, actualValue, actualType, successBB, failureBB); handled || err != nil {
			return decodedActualValue, packedPayloadValueCache{}, err
		}
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
	case *ast.MatchLiteralPattern:
		if err := s.emitLiteralMatchPatternTest(p.Value, actualValue, actualType, successBB, failureBB); err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchStringLiteralPattern:
		if err := s.emitLiteralMatchPatternTest(&ast.StringLit{Position: p.Position, Value: p.Value}, actualValue, actualType, successBB, failureBB); err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchTuplePattern:
		fields, err := s.resolveTupleMatchPatternElems(p, actualType)
		if err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		if len(p.Elems) == 0 {
			C.LLVMBuildBr(s.builder, successBB)
			return decodedActualValue, packedPayloadValueCache{}, nil
		}
		for i, elem := range p.Elems {
			fieldValue := C.LLVMBuildExtractValue(s.builder, actualValue, C.unsigned(fields[i].Index), cStringFree("match.tuple.field"))
			nextSuccess := successBB
			if i != len(p.Elems)-1 {
				nextSuccess = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.tuple.pattern.next"))
			}
			if _, _, err := s.emitMatchPatternTest(elem, fieldValue, nil, fields[i].Type, nil, nil, nil, nextSuccess, failureBB); err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			if i != len(p.Elems)-1 {
				C.LLVMPositionBuilderAtEnd(s.builder, nextSuccess)
			}
		}
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchListPattern:
		if err := s.emitListMatchPatternTest(p, actualValue, actualType, originExpr, successBB, failureBB); err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchStructPattern:
		if handled, err := s.emitCountMatchPatternTest(p, actualValue, actualType, originExpr, successBB, failureBB); handled || err != nil {
			return decodedActualValue, packedPayloadValueCache{}, err
		}
		fields, orderedArgs, err := s.resolveStructMatchPatternArgs(p, actualType)
		if err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		matchedIndexes := make([]int, 0, len(orderedArgs))
		for i, arg := range orderedArgs {
			if arg != nil {
				matchedIndexes = append(matchedIndexes, i)
			}
		}
		if len(matchedIndexes) == 0 {
			C.LLVMBuildBr(s.builder, successBB)
			return decodedActualValue, packedPayloadValueCache{}, nil
		}
		for i, fieldIndex := range matchedIndexes {
			arg := orderedArgs[fieldIndex]
			fieldValue := C.LLVMBuildExtractValue(s.builder, actualValue, C.unsigned(fields[fieldIndex].Index), cStringFree("match.struct.field"))
			nextSuccess := successBB
			if i != len(matchedIndexes)-1 {
				nextSuccess = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.struct.pattern.next"))
			}
			if _, _, err := s.emitMatchPatternTest(arg.Pattern, fieldValue, nil, fields[fieldIndex].Type, nil, nil, nil, nextSuccess, failureBB); err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			if i != len(matchedIndexes)-1 {
				C.LLVMPositionBuilderAtEnd(s.builder, nextSuccess)
			}
		}
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchVariantPattern:
		if errorSetType, ok := resolveMatchableErrorSetTypeBackend(actualType); ok {
			if p.EnumName != errorSetType.Name {
				return nil, packedPayloadValueCache{}, fmt.Errorf("match arm expects error set %s, got %s", errorSetType.Name, p.EnumName)
			}
			if !errorSetType.HasQualifiedTag(errorSetType.Name, p.Variant) {
				return nil, packedPayloadValueCache{}, fmt.Errorf("error set %s has no tag %s", errorSetType.Name, p.Variant)
			}
			if len(p.Args) != 0 {
				return nil, packedPayloadValueCache{}, fmt.Errorf("match arm %q expects 0 payload patterns, got %d", errorSetType.Name+"."+p.Variant, len(p.Args))
			}
			fieldExpr := &ast.FieldExpr{Position: p.Position, Object: &ast.Ident{Position: p.Position, Name: errorSetType.Name}, Field: p.Variant}
			memberValue, _, err := s.emitErrorTagExpr(fieldExpr, errorSetType)
			if err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			pred := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), actualValue, memberValue, cStringFree("match.tag"))
			C.LLVMBuildCondBr(s.builder, pred, successBB, failureBB)
			return decodedActualValue, packedPayloadValueCache{}, nil
		}
		if constEnumType, ok := resolveMatchableConstEnumType(actualType); ok {
			if p.EnumName != constEnumType.Name {
				return nil, packedPayloadValueCache{}, fmt.Errorf("match arm expects const enum %s, got %s", constEnumType.Name, p.EnumName)
			}
			member, ok := constEnumType.Member(p.Variant)
			if !ok {
				return nil, packedPayloadValueCache{}, fmt.Errorf("const enum %s has no member %s", constEnumType.Name, p.Variant)
			}
			if len(p.Args) != 0 {
				return nil, packedPayloadValueCache{}, fmt.Errorf("match arm %q expects 0 payload patterns, got %d", constEnumType.Name+"."+p.Variant, len(p.Args))
			}
			memberValue, _, err := s.emitConstEnumMemberExpr(constEnumType, member)
			if err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			pred := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), actualValue, memberValue, cStringFree("match.tag"))
			C.LLVMBuildCondBr(s.builder, pred, successBB, failureBB)
			return decodedActualValue, packedPayloadValueCache{}, nil
		}
		enumType, ok := actualType.(*semantic.EnumType)
		if !ok {
			treeType, _, treeOK := resolveMatchableTreeCategoryTypeBackend(actualType)
			if !treeOK || treeType == nil {
				return nil, packedPayloadValueCache{}, fmt.Errorf("variant pattern %s.%s requires enum, const enum, error set, or tree-category type, got %s", p.EnumName, p.Variant, actualType.String())
			}
			patternTreeType, variant, ok := s.resolveTreeMatchPatternCategory(treeType, p)
			if !ok {
				return nil, packedPayloadValueCache{}, fmt.Errorf("tree category %s has no variant %s", p.EnumName, p.Variant)
			}
			tagValue, err := s.extractTreeCategoryTagValue(actualValue, treeType)
			if err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			tagConst, err := s.enumTagConstant(variant.Tag)
			if err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			pred := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), tagValue, tagConst, cStringFree("match.tree.tag"))
			matchedBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.tree.pattern.ok"))
			C.LLVMBuildCondBr(s.builder, pred, matchedBB, failureBB)

			C.LLVMPositionBuilderAtEnd(s.builder, matchedBB)
			return s.emitMatchedTreeVariantPayloadPatternTest(p, actualValue, patternTreeType, variant, successBB, failureBB)
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
		pred := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), tagValue, tagConst, cStringFree("match.tag"))
		matchedBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.pattern.ok"))
		C.LLVMBuildCondBr(s.builder, pred, matchedBB, failureBB)

		C.LLVMPositionBuilderAtEnd(s.builder, matchedBB)
		return s.emitMatchedVariantPayloadPatternTest(p, actualValue, decodedActualValue, enumType, variant, store, originExpr, successBB, failureBB)
	default:
		return nil, packedPayloadValueCache{}, fmt.Errorf("unsupported match pattern %T", pattern)
	}
}

func (s *functionState) emitLiteralMatchPatternTest(literalExpr ast.Expr, actualValue C.LLVMValueRef, actualType semantic.Type, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) error {
	if literalExpr == nil {
		return fmt.Errorf("missing literal pattern expression")
	}
	actualType = semantic.StripAggregateStateType(actualType)
	literalType := s.exprType(literalExpr)
	if helperName, firstType, secondType, swap, ok := runtimeStringCompareInfo(actualType, literalType); ok {
		cmp, err := s.emitRuntimeStringCompareLiteralValue(actualValue, literalExpr, literalType, helperName, firstType, secondType, swap)
		if err != nil {
			return err
		}
		C.LLVMBuildCondBr(s.builder, cmp, successBB, failureBB)
		return nil
	}
	comparisonType := actualType
	if semantic.IsNumericType(actualType) && semantic.IsNumericType(literalType) {
		comparisonType = semantic.CommonNumericType(actualType, literalType)
	}
	coercedActual := actualValue
	if comparisonType != actualType {
		var err error
		coercedActual, err = s.coerceValue(actualValue, actualType, comparisonType)
		if err != nil {
			return err
		}
	}
	literalValue, _, err := s.emitExpr(literalExpr, comparisonType)
	if err != nil {
		return err
	}
	if isFloatType(comparisonType) {
		cmp := C.LLVMBuildFCmp(s.builder, C.LLVMRealOEQ, coercedActual, literalValue, cStringFree("match.literal.eq"))
		C.LLVMBuildCondBr(s.builder, cmp, successBB, failureBB)
		return nil
	}
	cmp := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), coercedActual, literalValue, cStringFree("match.literal.eq"))
	C.LLVMBuildCondBr(s.builder, cmp, successBB, failureBB)
	return nil
}

func (s *functionState) emitRuntimeStringCompareLiteralValue(actualValue C.LLVMValueRef, literalExpr ast.Expr, literalType semantic.Type, helperName string, firstType semantic.Type, secondType semantic.Type, swap bool) (C.LLVMValueRef, error) {
	firstValue := actualValue
	secondValue, _, err := s.emitExpr(literalExpr, literalType)
	if err != nil {
		return nil, err
	}
	if swap {
		firstValue, secondValue = secondValue, firstValue
	}
	helperReturn := s.g.result.NamedTypes["int"]
	helperType := &semantic.FuncType{Name: helperName, Params: []semantic.Type{firstType, secondType}, Return: helperReturn}
	callee, err := s.g.ensureFunctionDeclared(helperName, helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	call := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{firstValue, secondValue}, "match.literal.str")
	helperLLVMType, err := s.g.lowerType(helperReturn)
	if err != nil {
		return nil, err
	}
	zero := C.LLVMConstInt(helperLLVMType, 0, 0)
	return C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), call, zero, cStringFree("match.literal.str.eq")), nil
}

func (s *functionState) emitMatchedVariantPayloadPatternTest(pattern *ast.MatchVariantPattern, actualValue C.LLVMValueRef, decodedActualValue C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant, store *packedStoreBinding, originExpr ast.Expr, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) (C.LLVMValueRef, packedPayloadValueCache, error) {
	matchedDecodedValue := decodedActualValue
	if pattern == nil || variant == nil {
		C.LLVMBuildBr(s.builder, successBB)
		return matchedDecodedValue, packedPayloadValueCache{}, nil
	}
	if len(pattern.Args) == 0 {
		C.LLVMBuildBr(s.builder, successBB)
		return matchedDecodedValue, packedPayloadValueCache{}, nil
	}
	namedCount := 0
	for i := range pattern.Args {
		if pattern.Args[i].Name != "" {
			namedCount++
		}
	}
	var orderedArgs []*ast.MatchPatternArg
	if namedCount == 0 {
		if len(pattern.Args) != len(variant.Payload) {
			return nil, packedPayloadValueCache{}, fmt.Errorf("match arm %s.%s expects %d payload patterns, got %d", pattern.EnumName, pattern.Variant, len(variant.Payload), len(pattern.Args))
		}
		orderedArgs = make([]*ast.MatchPatternArg, len(pattern.Args))
		for i := range pattern.Args {
			orderedArgs[i] = &pattern.Args[i]
		}
	} else {
		if namedCount != len(pattern.Args) {
			return nil, packedPayloadValueCache{}, fmt.Errorf("match arm %s.%s cannot mix positional and named payload patterns", pattern.EnumName, pattern.Variant)
		}
		var err error
		orderedArgs, err = s.resolveMatchPatternArgs(pattern, variant)
		if err != nil {
			return nil, packedPayloadValueCache{}, err
		}
	}
	lastNestedPattern := -1
	for i := range orderedArgs {
		if orderedArgs[i] != nil && orderedArgs[i].Pattern != nil {
			lastNestedPattern = i
		}
	}
	if lastNestedPattern < 0 {
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
		arg := orderedArgs[i]
		if arg == nil || arg.Pattern == nil {
			continue
		}
		nextSuccess := successBB
		if i != lastNestedPattern {
			nextSuccess = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.pattern.next"))
		}
		if _, _, err := s.emitMatchPatternTest(arg.Pattern, payloadValues[i], nil, variant.Payload[i], store, nil, nil, nextSuccess, failureBB); err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		if i != lastNestedPattern {
			C.LLVMPositionBuilderAtEnd(s.builder, nextSuccess)
		}
	}
	return matchedDecodedValue, cachedPayloads, nil
}

func (s *functionState) emitMatchedTreeVariantPayloadPatternTest(pattern *ast.MatchVariantPattern, actualValue C.LLVMValueRef, treeType *semantic.TreeCategoryType, variant *semantic.EnumVariant, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) (C.LLVMValueRef, packedPayloadValueCache, error) {
	if pattern == nil || variant == nil {
		C.LLVMBuildBr(s.builder, successBB)
		return actualValue, packedPayloadValueCache{}, nil
	}
	if len(pattern.Args) == 0 {
		C.LLVMBuildBr(s.builder, successBB)
		return actualValue, packedPayloadValueCache{}, nil
	}
	namedCount := 0
	for i := range pattern.Args {
		if pattern.Args[i].Name != "" {
			namedCount++
		}
	}
	var orderedArgs []*ast.MatchPatternArg
	if namedCount == 0 {
		if len(pattern.Args) != len(variant.Payload) {
			return nil, packedPayloadValueCache{}, fmt.Errorf("match arm %s.%s expects %d payload patterns, got %d", pattern.EnumName, pattern.Variant, len(variant.Payload), len(pattern.Args))
		}
		orderedArgs = make([]*ast.MatchPatternArg, len(pattern.Args))
		for i := range pattern.Args {
			orderedArgs[i] = &pattern.Args[i]
		}
	} else {
		if namedCount != len(pattern.Args) {
			return nil, packedPayloadValueCache{}, fmt.Errorf("match arm %s.%s cannot mix positional and named payload patterns", pattern.EnumName, pattern.Variant)
		}
		var err error
		orderedArgs, err = s.resolveMatchPatternArgs(pattern, variant)
		if err != nil {
			return nil, packedPayloadValueCache{}, err
		}
	}
	lastNestedPattern := -1
	for i := range orderedArgs {
		if orderedArgs[i] != nil && orderedArgs[i].Pattern != nil {
			lastNestedPattern = i
		}
	}
	if lastNestedPattern < 0 {
		C.LLVMBuildBr(s.builder, successBB)
		return actualValue, packedPayloadValueCache{}, nil
	}
	payloadValues, err := s.extractTreeVariantPayloadValues(actualValue, treeType, variant)
	if err != nil {
		return nil, packedPayloadValueCache{}, err
	}
	for i := range orderedArgs {
		arg := orderedArgs[i]
		if arg == nil || arg.Pattern == nil {
			continue
		}
		nextSuccess := successBB
		if i != lastNestedPattern {
			nextSuccess = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.tree.pattern.next"))
		}
		if _, _, err := s.emitMatchPatternTest(arg.Pattern, payloadValues[i], nil, variant.Payload[i], nil, nil, nil, nextSuccess, failureBB); err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		if i != lastNestedPattern {
			C.LLVMPositionBuilderAtEnd(s.builder, nextSuccess)
		}
	}
	return actualValue, packedPayloadValueCache{}, nil
}

func (s *functionState) resolveTreeMatchPatternCategory(expected *semantic.TreeCategoryType, pattern *ast.MatchVariantPattern) (*semantic.TreeCategoryType, *semantic.EnumVariant, bool) {
	if expected == nil || pattern == nil {
		return nil, nil, false
	}
	category := expected
	if pattern.EnumName != expected.Name {
		base, ok := s.g.result.NamedTypes[pattern.EnumName]
		if !ok {
			return nil, nil, false
		}
		resolvedCategory, ok := semantic.StripAggregateStateType(base).(*semantic.TreeCategoryType)
		if !ok || resolvedCategory == nil || !treeCategoryDescendsFromBackend(resolvedCategory, expected) {
			return nil, nil, false
		}
		category = resolvedCategory
	}
	variant, ok := category.Variant(pattern.Variant)
	if !ok {
		return nil, nil, false
	}
	return category, variant, true
}

func treeCategoryDescendsFromBackend(src *semantic.TreeCategoryType, dst *semantic.TreeCategoryType) bool {
	for current := src; current != nil; current = current.Parent {
		if semantic.SameType(current, dst) {
			return true
		}
	}
	return false
}

func packedEnumMatchCanUseTagSwitch(enumType *semantic.EnumType, arms []ast.MatchArm) bool {
	if enumType == nil || len(arms) == 0 {
		return false
	}
	seen := map[string]bool{}
	for i, arm := range arms {
		switch pattern := arm.Pattern.(type) {
		case *ast.MatchVariantPattern:
			if seen[pattern.Variant] {
				return false
			}
			if _, ok := enumType.Variant(pattern.Variant); !ok {
				return false
			}
			seen[pattern.Variant] = true
		case *ast.MatchWildcardPattern:
			if i != len(arms)-1 {
				return false
			}
		default:
			return false
		}
	}
	return len(seen) >= 3
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

func (s *functionState) resolveStructMatchPatternArgs(pattern *ast.MatchStructPattern, actualType semantic.Type) ([]structLiteralField, []*ast.MatchPatternArg, error) {
	if pattern == nil {
		return nil, nil, fmt.Errorf("missing struct match pattern")
	}
	fields, err := s.g.structLiteralFields(actualType)
	if err != nil {
		return nil, nil, err
	}
	switch tt := semantic.StripAggregateStateType(actualType).(type) {
	case *semantic.StructType:
		if tt == nil || tt.Name != pattern.TypeName || tt.Decl == nil {
			got := "<invalid>"
			if tt != nil {
				got = tt.Name
			}
			return nil, nil, fmt.Errorf("struct pattern expects struct %s, got %s", pattern.TypeName, got)
		}
	case *semantic.GenericInstanceType:
		base, _ := tt.Base.(*semantic.StructType)
		if base == nil || base.Name != pattern.TypeName || base.Decl == nil {
			got := semantic.StripAggregateStateType(actualType).String()
			if base != nil {
				got = base.Name
			}
			return nil, nil, fmt.Errorf("struct pattern expects struct %s, got %s", pattern.TypeName, got)
		}
	case *semantic.TreeBlockType:
		if tt == nil || tt.Name != pattern.TypeName || tt.Decl == nil {
			got := "<invalid>"
			if tt != nil {
				got = tt.Name
			}
			return nil, nil, fmt.Errorf("struct pattern expects struct %s, got %s", pattern.TypeName, got)
		}
	case *semantic.TreeStructType:
		if tt == nil || tt.Name != pattern.TypeName || tt.Decl == nil {
			got := "<invalid>"
			if tt != nil {
				got = tt.Name
			}
			return nil, nil, fmt.Errorf("struct pattern expects struct %s, got %s", pattern.TypeName, got)
		}
	default:
		return nil, nil, fmt.Errorf("struct pattern %s requires a concrete struct value, got %s", pattern.TypeName, semantic.StripAggregateStateType(actualType).String())
	}
	if len(pattern.ResolvedArgs) == len(fields) {
		return fields, pattern.ResolvedArgs, nil
	}
	ordered := make([]*ast.MatchPatternArg, len(fields))
	fieldIndexes := make(map[string]int, len(fields))
	for i := range fields {
		fieldIndexes[fields[i].Decl.Name] = i
	}
	seen := make([]bool, len(fields))
	for i := range pattern.Args {
		arg := &pattern.Args[i]
		index, ok := fieldIndexes[arg.Name]
		if !ok {
			return nil, nil, fmt.Errorf("struct %s has no field %s", pattern.TypeName, arg.Name)
		}
		if seen[index] {
			return nil, nil, fmt.Errorf("struct %s matches field %s more than once", pattern.TypeName, arg.Name)
		}
		seen[index] = true
		ordered[index] = arg
	}
	pattern.ResolvedArgs = ordered
	return fields, ordered, nil
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

func (s *functionState) resolveTupleMatchPatternElems(pattern *ast.MatchTuplePattern, actualType semantic.Type) ([]structLiteralField, error) {
	if pattern == nil {
		return nil, fmt.Errorf("missing tuple match pattern")
	}
	tupleType, ok := semantic.StripAggregateStateType(actualType).(*semantic.TupleType)
	if !ok || tupleType == nil {
		return nil, fmt.Errorf("tuple pattern requires a tuple value, got %s", semantic.StripAggregateStateType(actualType).String())
	}
	fields, err := s.g.structLiteralFields(actualType)
	if err != nil {
		return nil, err
	}
	if len(pattern.Elems) != len(fields) {
		return nil, fmt.Errorf("tuple pattern expects %d elements, got %d", len(fields), len(pattern.Elems))
	}
	return fields, nil
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

func constEnumMatchIsExhaustive(constEnumType *semantic.ConstEnumType, arms []ast.MatchArm) bool {
	if constEnumType == nil {
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
	return len(covered) == len(constEnumType.Members)
}

func treeMatchIsExhaustive(treeType *semantic.TreeCategoryType, arms []ast.MatchArm) bool {
	if treeType == nil {
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
	return len(covered) == len(treeType.Variants)
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
	var payloadCacheKey packedVariantPayloadReadCacheKey
	cachePayloadValues := false
	if ops.canCacheDirectReadValues(enumType) {
		if s.packedVariantPayloadReads == nil {
			s.packedVariantPayloadReads = map[packedVariantPayloadReadCacheKey][]C.LLVMValueRef{}
		}
		originKey, cacheHandle := ops.directReadCacheIdentity(enumType, origin, handleValue)
		payloadCacheKey = packedVariantPayloadReadCacheKey{
			block:    ops.currentBlock(),
			store:    ops.storeValue,
			enumType: enumType,
			variant:  variant,
			origin:   originKey,
			handle:   cacheHandle,
		}
		if cached, ok := s.packedVariantPayloadReads[payloadCacheKey]; ok && len(cached) == len(variant.Payload) {
			return cached, true, nil
		}
		cachePayloadValues = true
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
	for payloadIndex, payloadType := range variant.Payload {
		if hasTail && payloadIndex == tailIndex {
			values = append(values, tailValue)
			continue
		}
		fieldOffsetBytes, ok, err := s.packedEnumVariantPayloadFieldByteOffset(enumType, variant, payloadIndex)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		coerced, err := s.emitPackedDirectFieldReadAtOrigin(ops, handleValue, enumType, payloadType, fieldOffsetBytes, origin, "packed.payload")
		if err != nil {
			return nil, false, err
		}
		values = append(values, coerced)
	}
	if cachePayloadValues && len(values) == len(variant.Payload) {
		s.packedVariantPayloadReads[payloadCacheKey] = values
	}
	return values, true, nil
}

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
