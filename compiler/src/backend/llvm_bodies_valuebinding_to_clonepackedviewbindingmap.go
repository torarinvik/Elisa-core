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
	"strings"
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
	diScope                      C.LLVMMetadataRef
	traceNameGlobal              C.LLVMValueRef
	typeMap                      map[string]semantic.Type
	specializedFuncTypes         map[*semantic.FuncType]*semantic.FuncType
	resultSlot                   C.LLVMValueRef
	sretReturn                   bool
	regions                      []regionBinding
	// darrayStackTag routes a fresh inferred-region darray (by name) to its assigned parallel
	// arena (multi-stack regions, Phase B1b): name -> region arena tag "__auto_N#k". Populated at
	// region entry from Result.RegionStacks, cleared at region exit. Empty for ordinary code.
	darrayStackTag map[string]string
	// earlyFreeByOffset frees an own-stack arena early (Phase B2): the byte offset of a top-level
	// statement -> the stack arena to free right after it (the object died and is not aliased).
	// Populated at region entry, fired once and removed when the statement is emitted.
	earlyFreeByOffset map[int]C.LLVMValueRef
	packedStores                 map[string]packedStoreBinding
	treeAllocOwner               treeAllocOwnerBinding
	// regionPolyOwner is the region threaded into a region-polymorphic function via the hidden
	// `__region_auto` Arena& param (docs/75). The function's synthesized `__auto_*` region adopts it
	// rather than creating a fresh, locally-freed arena, so `new[auto]` allocates into the caller's
	// region and the returned handle outlives the call. Zero value (nil arenaRef) for ordinary fns.
	regionPolyOwner              treeAllocOwnerBinding
	implicitTreeStoreOwners      map[string]treeAllocOwnerBinding
	treeRewriteDefault           *treeRewriteDefaultContext
	currentSequenceRewrite       *sequenceRewriteCodegenContext
	treeImplicitStores           map[treeImplicitStoreCacheKey]treeImplicitStoreSlot
	treeResolvedStores           map[treeResolvedStoreCacheKey]treeResolvedStoreSlot
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
	treeExactRowPointers         map[treeExactRowCacheKey]C.LLVMValueRef
	treeExactRowValues           map[treeExactRowCacheKey]C.LLVMValueRef
	treeDenseKindValues          map[treeDenseValueCacheKey]C.LLVMValueRef
	treeDensePayloadValues       map[treeDenseValueCacheKey]C.LLVMValueRef
	scopedCleanups               []scopedCleanupBinding
	checkpoints                  map[string]checkpointBinding
	poolScopes                   []activePoolBinding
	breakTargets                 []C.LLVMBasicBlockRef
	continueTargets              []C.LLVMBasicBlockRef
	loopCleanupFloors            []int
	cleanupDepth                 int
	scopePool                    []*codegenScope
}

func (s *functionState) currentNamespace() string {
	if s == nil {
		return ""
	}
	name := ""
	if s.fnType != nil {
		name = strings.TrimSpace(s.fnType.Name)
	}
	if name == "" && s.g != nil && s.decl != nil && s.g.symbolsByNode != nil {
		if sym, ok := s.g.symbolsByNode[s.decl]; ok && sym != nil {
			name = strings.TrimSpace(sym.Name)
		}
	}
	if name == "" {
		return ""
	}
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[:idx]
	}
	return ""
}

func (s *functionState) visibleGlobalNames(name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if strings.Contains(name, ".") || strings.Contains(name, "::") {
		return []string{strings.ReplaceAll(name, "::", ".")}
	}
	namespace := s.currentNamespace()
	if namespace == "" {
		return []string{name}
	}
	return []string{namespace + "." + name, name}
}

func (s *functionState) lookupVisibleGlobalSymbol(name string) (*semantic.Symbol, string, bool) {
	if s == nil || s.g == nil || s.g.result == nil || s.g.result.GlobalScope == nil {
		return nil, "", false
	}
	for _, candidate := range s.visibleGlobalNames(name) {
		if sym, ok := s.g.result.GlobalScope.Lookup(candidate); ok && sym != nil {
			return sym, candidate, true
		}
	}
	if !strings.Contains(name, ".") && !strings.Contains(name, "::") {
		suffix := "." + strings.TrimSpace(name)
		var resolvedName string
		var resolvedSym *semantic.Symbol
		for candidate, sym := range s.g.result.GlobalScope.Symbols {
			if !strings.HasSuffix(candidate, suffix) || sym == nil {
				continue
			}
			if resolvedSym != nil {
				return nil, "", false
			}
			resolvedName = candidate
			resolvedSym = sym
		}
		if resolvedSym != nil {
			return resolvedSym, resolvedName, true
		}
	}
	return nil, "", false
}

func (s *functionState) visibleConstValue(name string) (semantic.ConstValue, string, bool) {
	if s == nil || s.g == nil {
		return semantic.ConstValue{}, "", false
	}
	for _, candidate := range s.visibleGlobalNames(name) {
		if value, ok := s.g.constValue(candidate); ok {
			return value, candidate, true
		}
	}
	return semantic.ConstValue{}, "", false
}

type scopedCleanupKind int

const (
	scopedCleanupLockGuard scopedCleanupKind = iota
	scopedCleanupThreadPool
	scopedCleanupDeferBody
	scopedCleanupValue
	// scopedCleanupRegion frees a scoped region's arena at block exit (every exit path), so a
	// `region`/`in auto:` scope reclaims at scope exit instead of leaking until function return.
	scopedCleanupRegion
	// scopedCleanupRegionReset resets (not frees) a loop-entered lazy region at block exit, so its
	// blocks are reused next iteration (no mmap/munmap churn); function-return arena_free releases.
	scopedCleanupRegionReset
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
	isPerm      bool
	arenaRef    C.LLVMValueRef
	arenaRefPtr C.LLVMValueRef
	storePtr    C.LLVMValueRef
	storeValue  C.LLVMValueRef
	storeType   *semantic.TreeStoreType
}
type treeRewriteDefaultContext struct {
	memberType      semantic.Type
	nodeValue       C.LLVMValueRef
	childViewValue  C.LLVMValueRef
	childResultType semantic.Type
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
type treeResolvedStoreCacheKey struct {
	block  C.LLVMBasicBlockRef
	family *semantic.TreeType
	isPerm bool
	arena  C.LLVMValueRef
}
type treeResolvedStoreSlot struct {
	value     C.LLVMValueRef
	storeType *semantic.TreeStoreType
}
type treeDenseValueCacheKey struct {
	block C.LLVMBasicBlockRef
	kind  string
	table C.LLVMValueRef
	row   C.LLVMValueRef
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
type treeExactRowCacheKey struct {
	block      C.LLVMBasicBlockRef
	memberName string
	table      C.LLVMValueRef
	row        C.LLVMValueRef
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

	if g.di != nil {
		g.di.attachFunction(state, decl, fnValue)
	}
	if g.trace != nil {
		state.traceNameGlobal = g.trace.nameGlobalFor(decl.Name)
	}

	abiLayout, layoutErr := g.computeFuncAbiLayout(fnType)
	if layoutErr != nil {
		return layoutErr
	}
	paramOffset := abiLayout.paramBase()
	if abiLayout.errorUnionOut {
		state.resultSlot = C.LLVMGetParam(fnValue, 0)
	}
	if abiLayout.sret {
		state.resultSlot = C.LLVMGetParam(fnValue, C.unsigned(abiLayout.sretParamPos()))
		state.sretReturn = true
	}

	explicitCount := backendExplicitParamCount(fnType, decl)
	bindParam := func(name string, mutable bool, typeIndex int, llvmIndex int) error {
		if typeIndex < 0 || typeIndex >= len(fnType.Params) {
			return nil
		}
		paramType := fnType.Params[typeIndex]
		paramValue := C.LLVMGetParam(fnValue, C.unsigned(llvmIndex+paramOffset))
		if g.aggregateIsMemoryClass(paramType) {
			// byval: the parameter is already a pointer to the callee's private
			// copy of the aggregate, so use it directly as the binding's address
			// (no entry alloca + giant element-wise store).
			state.defineBinding(name, valueBinding{ptr: paramValue, typ: paramType, mutable: mutable})
			return nil
		}
		alloca, err := state.createEntryAlloca(name, paramType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(builder, paramValue, alloca)
		state.defineBinding(name, valueBinding{ptr: alloca, typ: paramType, mutable: mutable})
		state.bindPackedStoreValue(paramType, paramValue)
		state.bindImplicitTreeStoreValue(paramType, paramValue)
		state.bindImplicitTreeOwnerParam(name, paramType, alloca, paramValue)
		return nil
	}

	for i, param := range decl.Params {
		if err := bindParam(param.Name, param.Mutable, i, i); err != nil {
			return err
		}
		if g.di != nil {
			if binding, ok := state.lookupBinding(param.Name); ok {
				g.di.declareVariable(state, param.Name, binding.ptr, binding.typ, decl.Pos().Line, i+1)
			}
		}
	}
	for i, name := range fnType.ImplicitParamNames {
		if err := bindParam(name, false, explicitCount+i, explicitCount+i); err != nil {
			return err
		}
	}

	// Region-parameterized containers: the hidden Arena& params (appended after
	// all explicit/implicit params by lowerFunctionType) become this function's
	// region environment, so container ops on `@r` resolve their arena via
	// regionArenaOwner(r). Inert for functions with no region params.
	if len(fnType.RegionParams) != 0 {
		arenaType := g.result.NamedTypes["Arena"]
		base := paramOffset + len(fnType.Params)
		for j, regionName := range fnType.RegionParams {
			arenaParam := C.LLVMGetParam(fnValue, C.unsigned(base+j))
			state.regions = append(state.regions, regionBinding{name: regionName, ptr: arenaParam, typ: arenaType})
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
			successValue, err := state.wrapVoidErrorUnionCode(retUnion.Errors, zeroCode)
			if err != nil {
				return err
			}
			C.LLVMBuildRet(builder, successValue)
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
	if s.sretReturn {
		// Large aggregate return: write the value through the sret out-pointer
		// (memcpy-lowered when `coerced` is a load) and return void.
		if s.resultSlot == nil {
			return fmt.Errorf("function is missing the sret return slot")
		}
		if err := s.storeValue(s.resultSlot, coerced, s.fnType.Return, "sret.ret"); err != nil {
			return err
		}
		C.LLVMBuildRetVoid(s.builder)
		return nil
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
// pushLoopTargets records the break/continue targets for a loop along with the current scoped
// cleanup floor — the number of cleanups registered by ENCLOSING scopes. A break/continue must
// only run cleanups registered INSIDE the loop body (indices >= floor); firing an enclosing
// scope's cleanup (e.g. a function-body region that wraps the loop) on a continue that stays
// within the loop would free a still-live region — a use-after-free.
func (s *functionState) pushLoopTargets(breakBB, continueBB C.LLVMBasicBlockRef) {
	s.breakTargets = append(s.breakTargets, breakBB)
	s.continueTargets = append(s.continueTargets, continueBB)
	s.loopCleanupFloors = append(s.loopCleanupFloors, len(s.scopedCleanups))
}

func (s *functionState) popLoopTargets() {
	s.breakTargets = s.breakTargets[:len(s.breakTargets)-1]
	s.continueTargets = s.continueTargets[:len(s.continueTargets)-1]
	s.loopCleanupFloors = s.loopCleanupFloors[:len(s.loopCleanupFloors)-1]
}

// emitActiveScopedCleanup runs ALL active scoped cleanups. This is the function-EXIT semantics:
// a return / function return / lambda return leaves every enclosing scope, so every registered
// cleanup (defers, mutex unlocks, pool shutdowns, region frees) must fire — including those of
// scopes that enclose a loop the return sits inside. Break/continue must NOT use this (they stay
// within the loop's enclosing scopes); they use emitLoopExitCleanup, which stops at the loop floor.
func (s *functionState) emitActiveScopedCleanup() error {
	return s.emitScopedCleanupsFrom(0)
}

// emitLoopExitCleanup runs only the cleanups registered INSIDE the innermost loop (indices >= its
// floor). break/continue transfer control to a point still inside every enclosing scope, so firing
// an enclosing scope's cleanup (e.g. a region that wraps the loop) would free a still-live owner —
// a use-after-free. When not inside a loop the floor is 0 (identical to emitActiveScopedCleanup).
func (s *functionState) emitLoopExitCleanup() error {
	floor := 0
	if len(s.loopCleanupFloors) > 0 {
		floor = s.loopCleanupFloors[len(s.loopCleanupFloors)-1]
	}
	return s.emitScopedCleanupsFrom(floor)
}

func (s *functionState) emitScopedCleanupsFrom(floor int) error {
	if s.cleanupDepth != 0 {
		return nil
	}
	s.cleanupDepth++
	defer func() {
		s.cleanupDepth--
	}()
	for i := len(s.scopedCleanups) - 1; i >= floor; i-- {
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
	if binding.kind == scopedCleanupRegion {
		// arena_free is idempotent (nulls begin/end) and an adopted/destroyed region is already
		// zeroed, so freeing here is safe even though the function-return cleanup also frees it.
		return s.emitArenaFree(binding.ptr, binding.typ)
	}
	if binding.kind == scopedCleanupRegionReset {
		// Keep the blocks for next iteration; the function-return arena_free releases them.
		return s.emitArenaReset(binding.ptr, binding.typ)
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
