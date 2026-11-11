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
	if g.bodyDepth == 0 && g.partBody != nil {
		if C.LLVMGetLinkage(fnValue) == C.LLVMExternalLinkage && !g.partBody(llvmValueName(fnValue)) {
			return nil
		}
	}
	g.bodyDepth++
	defer func() { g.bodyDepth-- }()

	if g.result != nil {
		defer g.installCodegenResult(g.codegenDeclarationResult(decl))()
	}

	// Parametric impl/region lowering may have enclosing bindings while this
	// declaration has no semantic generic arguments. Its existing template
	// facts plus backend type substitution need no reanalysis or private maps.
	if semanticArgs := orderedGenericTypeArgs(fnType, decl, typeBindings); len(semanticArgs) != 0 && g.result != nil {
		body, err := g.specializeFunctionForInputRead(decl, semanticArgs)
		if err != nil {
			return err
		}
		defer g.installCodegenResult(body.Result)()
		decl = body.Decl
	}
	bodyStatements := g.functionBodyForInputRead(decl, "function-emission")

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
		typeMap:           typeBindings,
		adoptedEscapeBody: bodyStatements,
		mainReturnsStatus: decl.Name == "main" && isVoidType(fnType.Return) && C.GoString(C.LLVMGetValueName(fnValue)) == "main",
	}
	// A signature-level `can[Scalar]` grants the whole body; `can Scalar:` blocks nest on top
	// (see the CanStmt case in emitStmt).
	if decl != nil && permissionRefsGrantScalar(decl.Permissions) {
		state.scalarGrantDepth = 1
	}

	if g.di != nil {
		g.di.attachFunction(state, decl, fnValue)
	}
	if g.trace != nil {
		state.traceNameGlobal = g.trace.nameGlobalFor(decl.Name)
	}
	// docs/84 Increment 3b: compute the per-parameter disjoint alias scopes (no-op unless
	// -fnoalias and a proven self-noalias group). Done before param binding so the explicit
	// loop below can record each scope's shadowing-proof alloca identity.
	state.initDisjointParamScopes()

	abiLayout, layoutErr := g.computeFuncAbiLayout(fnType)
	if layoutErr != nil {
		return layoutErr
	}
	abiCABI := funcTypeIsCABI(fnType)
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
		// docs/126 D1: a drop-typed parameter was MOVED in, so this frame owns it and
		// must run its `__drop__` on the way out. Give it a private alloca even in the
		// memory class, where the binding would otherwise alias the caller's copy —
		// the drop zeroes the slot it drops, and that must never reach back into the
		// caller's frame.
		if semantic.DropHookSymbol(paramType) != "" && !state.isOwnDropReceiver(paramType) {
			alloca, err := state.createEntryAlloca(name, paramType)
			if err != nil {
				return err
			}
			if g.aggregateIsMemoryClassABI(paramType, abiCABI) {
				loaded, err := state.loadValue(paramValue, paramType, name)
				if err != nil {
					return err
				}
				if err := state.storeValue(alloca, loaded, paramType, name); err != nil {
					return err
				}
			} else {
				C.LLVMBuildStore(builder, paramValue, alloca)
			}
			state.defineBinding(name, valueBinding{ptr: alloca, typ: paramType, mutable: mutable})
			if _, err := state.registerDropCleanup(name, alloca, paramType); err != nil {
				return err
			}
			return nil
		}
		if g.aggregateIsMemoryClassABI(paramType, abiCABI) {
			// memory-class: the parameter is already a pointer to the aggregate
			// (a byval private copy, or on arm64 C-ABI a plain indirect pointer to
			// the caller's copy), so use it directly as the binding's address.
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
		state.bindImplicitTreeOwnerParam(name, paramType, alloca, paramValue)
		return nil
	}

	for i, param := range decl.Params {
		if err := bindParam(param.Name, param.Mutable, i, i); err != nil {
			return err
		}
		// docs/84 Increment 3b: record this param's binding pointer as its scope identity.
		// The pointer (not the name) is the shadowing-proof key checked at each index site.
		if state.disjointScopes != nil {
			if binding, ok := state.lookupBinding(param.Name); ok {
				state.recordDisjointParamAlloca(i, binding.ptr)
			}
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
		arenaType := g.namedType("Arena")
		base := paramOffset + len(fnType.Params)
		for j, regionName := range fnType.RegionParams {
			arenaParam := C.LLVMGetParam(fnValue, C.unsigned(base+j))
			state.regions = append(state.regions, regionBinding{name: regionName, ptr: arenaParam, typ: arenaType, owned: false})
		}
	}
	state.callerStorageArenas = state.callerStorageArenasForBody(decl, fnType, bodyStatements)

	// Void-grower ambient region (docs/75 cross-fn container growth): when this function grows a
	// caller-owned container by INSERTING region-allocated values (`out.push(make_node())`), bind its
	// ambient allocation region to the container's (caller-provided) arena. A region-poly callee
	// producing the inserted value then allocates into the caller's container region — adopted with it
	// — instead of a per-call synthesized `__auto_*` region freed on return (the void-grower UAF).
	if decl.AmbientGrownContainerRegion != "" {
		state.ambientGrownContainerRegion = decl.AmbientGrownContainerRegion
		if owner, ok := state.regionArenaOwner(decl.AmbientGrownContainerRegion); ok && state.regionPolyOwner.arenaRef == nil {
			state.regionPolyOwner = owner
			if state.treeAllocOwner.arenaRef == nil && state.treeAllocOwner.arenaRefPtr == nil {
				state.treeAllocOwner = owner
			}
		}
	}

	if err := state.emitOldCaptures(decl); err != nil {
		return err
	}
	if err := state.emitPreconditionChecks(decl); err != nil {
		return err
	}

	if err := state.emitBlock(bodyStatements, false); err != nil {
		return err
	}

	if !state.currentBlockTerminated() {
		// Fall-through exit (no explicit `return`): check value-contract `ensure <bool>` postconditions
		// (docs/90) and `ensures … is Law` postconditions (docs/85 brick 2) here too, so a void function
		// that omits a return still enforces its contract / backs the caller's gained fact.
		if err := state.emitPostconditionChecks(nil, nil); err != nil {
			return err
		}
		if err := state.emitRefinementPostconditionChecks(); err != nil {
			return err
		}
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
			if err := state.emitHeapTempCleanup(); err != nil {
				return err
			}
			if state.mainReturnsStatus {
				zero := C.LLVMConstInt(C.LLVMInt32TypeInContext(g.context), 0, 0)
				C.LLVMBuildRet(builder, zero)
			} else {
				C.LLVMBuildRetVoid(builder)
			}
		} else if retUnion, ok := fnType.Return.(*semantic.ErrorUnionType); ok && isVoidType(retUnion.Value) {
			zeroCode, err := state.errorCodeConstant(0)
			if err != nil {
				return err
			}
			successValue, err := state.wrapVoidErrorUnionCode(retUnion.Errors, zeroCode)
			if err != nil {
				return err
			}
			if err := state.emitHeapTempCleanup(); err != nil {
				return err
			}
			C.LLVMBuildRet(builder, successValue)
		} else if block := C.LLVMGetInsertBlock(builder); block != nil && block != C.LLVMGetEntryBasicBlock(fnValue) && !blockReachableFromEntry(fnValue, block) {
			// Nothing reaches here (the body ends in a `while true` that only leaves by `return`):
			// the fall-through point is dead code, not a missing return.
			C.LLVMBuildUnreachable(builder)
		} else {
			return fmt.Errorf("function %s may fall through without returning a value", decl.Name)
		}
	}

	state.elideUnusedAutoArenas()
	return nil
}
func (s *functionState) emitFunctionReturn(value C.LLVMValueRef, actual semantic.Type) error {
	if err := s.emitPostconditionChecks(value, actual); err != nil {
		return err
	}
	if err := s.emitRefinementPostconditionChecks(); err != nil {
		return err
	}
	// Materialize the returned value BEFORE any scoped cleanup or region free runs. The
	// value may still live in function-owned storage: a returned `T&` is auto-dereferenced
	// by coerceValue, and a large aggregate `load` is lowered to a memcpy from its source
	// pointer by storeValue. Either one emitted after arena_free reads freed memory.
	if s.currentBlockTerminated() {
		// The value expression itself left the function (e.g. a `raise` inside it).
		return nil
	}
	finish, err := s.materializeFunctionReturn(value, actual)
	if err != nil {
		return err
	}
	if err := s.emitActiveScopedCleanup(); err != nil {
		return err
	}
	if s.currentBlockTerminated() {
		return nil
	}
	if err := s.emitRegionCleanup(); err != nil {
		return err
	}
	if err := s.emitHeapTempCleanup(); err != nil {
		return err
	}
	finish()
	return nil
}

// materializeFunctionReturn coerces the returned value and writes any out-slot (sret or
// error-union payload) at the current insertion point, returning the terminator to emit
// once cleanups have run.
func (s *functionState) materializeFunctionReturn(value C.LLVMValueRef, actual semantic.Type) (func(), error) {
	if retUnion, ok := s.fnType.Return.(*semantic.ErrorUnionType); ok {
		coerced, err := s.coerceValue(value, actual, retUnion)
		if err != nil {
			return nil, err
		}
		if isVoidType(retUnion.Value) {
			return func() { C.LLVMBuildRet(s.builder, coerced) }, nil
		}
		if s.resultSlot == nil {
			return nil, fmt.Errorf("function %s is missing a hidden return slot for %s", s.decl.Name, retUnion.String())
		}
		errorCode, err := s.extractErrorUnionCode(coerced, retUnion)
		if err != nil {
			return nil, err
		}
		payload, err := s.extractErrorUnionPayload(coerced, retUnion)
		if err != nil {
			return nil, err
		}
		// A non-void error union uses a split ABI (scalar error return plus payload
		// out-pointer).  Large payloads must use the same memcpy-aware store path as
		// ordinary sret returns; a raw LLVM aggregate store makes llc -O0 expand a
		// multi-hundred-KB value into megabytes of elementwise machine code.
		if err := s.storeValue(s.resultSlot, payload, retUnion.Value, "errunion.ret"); err != nil {
			return nil, err
		}
		return func() { C.LLVMBuildRet(s.builder, errorCode) }, nil
	}
	if isVoidType(s.fnType.Return) {
		// `return <void-expr>` (e.g. `return void_fn()`): the value expression was
		// already emitted for its side effects by the caller. A void function must
		// terminate with RetVoid — building a value `ret` here yields an invalid
		// `ret void <badref>` that the module verifier rejects.
		if s.mainReturnsStatus {
			zero := C.LLVMConstInt(C.LLVMInt32TypeInContext(s.g.context), 0, 0)
			return func() { C.LLVMBuildRet(s.builder, zero) }, nil
		}
		return func() { C.LLVMBuildRetVoid(s.builder) }, nil
	}
	coerced, err := s.coerceValue(value, actual, s.fnType.Return)
	if err != nil {
		return nil, err
	}
	if s.sretReturn {
		// Large aggregate return: write the value through the sret out-pointer
		// (memcpy-lowered when `coerced` is a load) and return void.
		if s.resultSlot == nil {
			return nil, fmt.Errorf("function is missing the sret return slot")
		}
		if err := s.storeValue(s.resultSlot, coerced, s.fnType.Return, "sret.ret"); err != nil {
			return nil, err
		}
		return func() { C.LLVMBuildRetVoid(s.builder) }, nil
	}
	return func() { C.LLVMBuildRet(s.builder, coerced) }, nil
}
func (s *functionState) emitRegionCleanup() error {
	for i := len(s.regions) - 1; i >= 0; i-- {
		if !s.regions[i].owned {
			continue
		}
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
	// A nursery joins before locals, defers, regions, and pools are cleaned up.
	// Keep reverse registration order within both passes.
	for pass := 0; pass < 2; pass++ {
		for i := len(s.scopedCleanups) - 1; i >= floor; i-- {
			if s.currentBlockTerminated() {
				break
			}
			isJoin := s.scopedCleanups[i].kind == scopedCleanupTaskGroupWait
			if isJoin != (pass == 0) {
				continue
			}
			if err := s.emitScopedCleanup(s.scopedCleanups[i]); err != nil {
				return err
			}
		}
	}
	return nil
}
