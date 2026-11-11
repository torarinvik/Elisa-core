//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

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
	llvmBuildStore(s.builder, guardValue, guardAlloca)
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
	handleValue := llvmBuildExtractValue(s.builder, guardValue, 0, cStringFree("lock.guard.handle"))
	nullHandleType, err := s.g.lowerType(&semantic.RefType{Elem: s.g.namedType("void"), State: semantic.RefStateNullable, Storage: semantic.RefStorageAny, ExplicitStorage: true})
	if err != nil {
		return err
	}
	isNull := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), handleValue, llvmConstNull(nullHandleType), cStringFree("lock.guard.null"))
	unlockBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("lock.unlock"))
	contBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("lock.after"))
	llvmBuildCondBr(s.builder, isNull, contBB, unlockBB)

	llvmPositionBuilderAtEnd(s.builder, unlockBB)
	unlockCall := &ast.CallExpr{
		Func: &ast.Ident{Name: "mutex_unlock"},
		Args: []ast.Expr{&ast.MoveExpr{Operand: &ast.Ident{Name: guard.name}}},
	}
	if _, _, err := s.emitExpr(unlockCall, nil); err != nil {
		return err
	}
	if !s.currentBlockTerminated() {
		llvmBuildBr(s.builder, contBB)
	}

	llvmPositionBuilderAtEnd(s.builder, contBB)
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
	handleValue := llvmBuildExtractValue(s.builder, poolValue, 0, cStringFree("pool.handle"))
	nullHandleType, err := s.g.lowerType(&semantic.RefType{Elem: s.g.namedType("void"), State: semantic.RefStateNullable, Storage: semantic.RefStorageAny, ExplicitStorage: true})
	if err != nil {
		return err
	}
	isNull := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), handleValue, llvmConstNull(nullHandleType), cStringFree("pool.handle.null"))
	shutdownBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("pool.shutdown"))
	contBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("pool.after"))
	llvmBuildCondBr(s.builder, isNull, contBB, shutdownBB)

	llvmPositionBuilderAtEnd(s.builder, shutdownBB)
	if err := s.emitPoolShutdown(pool.ptr, pool.typ); err != nil {
		return err
	}
	zero, err := s.zeroValue(pool.typ)
	if err != nil {
		return err
	}
	llvmBuildStore(s.builder, zero, pool.ptr)
	if !s.currentBlockTerminated() {
		llvmBuildBr(s.builder, contBB)
	}

	llvmPositionBuilderAtEnd(s.builder, contBB)
	return nil
}
func (s *functionState) emitPoolShutdown(poolPtr llvmIRValueRef, poolType semantic.Type) error {
	poolRefType := &semantic.RefType{Elem: poolType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{Name: "pool_shutdown", Params: []semantic.Type{poolRefType}, Return: s.g.namedType("void")}
	callee, err := s.g.ensureFunctionDeclared("pool_shutdown", helperType)
	if err != nil {
		return err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return err
	}
	s.buildCall(llvmFnType, callee, []llvmIRValueRef{poolPtr}, "")
	return nil
}

// regionStrategyTag maps the surface `using <strategy>` selector to the runtime
// ARENA_STRATEGY_* integer (see arena.elisa). "" and "chained" share CHAINED (0).
func regionStrategyTag(allocator string) int {
	switch allocator {
	case "malloc":
		return 1 // ARENA_STRATEGY_MALLOC
	case "reserve_commit":
		return 2 // ARENA_STRATEGY_RESERVE_COMMIT
	case "fixed":
		return 3 // ARENA_STRATEGY_FIXED
	case "scratch":
		return 4 // ARENA_STRATEGY_SCRATCH
	default:
		return 0 // ARENA_STRATEGY_CHAINED ("" or "chained")
	}
}
func (s *functionState) emitRegionInit(arenaPtr llvmIRValueRef, arenaType semantic.Type, capacityExpr ast.Expr, allocator string) error {
	capacityType := s.g.namedType("usize")
	var capacityValue llvmIRValueRef
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
		capacityValue = llvmConstInt(usizeLLVMType, 8*1024, 0)
	}
	return s.emitRegionInitValue(arenaPtr, arenaType, capacityValue, regionStrategyTag(allocator))
}

// emitRegionInitValue eagerly initializes a region's first block from a precomputed slot capacity
// and strategy tag (used for inferred reserve_commit stacks, where the capacity is a derived value
// rather than a surface expression).
func (s *functionState) emitRegionInitValue(arenaPtr llvmIRValueRef, arenaType semantic.Type, capacityValue llvmIRValueRef, strategyTag int) error {
	capacityType := s.g.namedType("usize")
	regionType := s.g.namedType("Region")
	if regionType == nil {
		return fmt.Errorf("missing builtin Region type for region initialization")
	}
	regionRefType := &semantic.RefType{Elem: regionType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	arenaLLVMType, err := s.g.lowerType(arenaType)
	if err != nil {
		return err
	}
	// The ARENA_STRATEGY_* tag selects the block source (MALLOC -> libc, everything else -> mmap)
	// and is recorded in Arena.strategy (field 3) so growth (arena_alloc/arena_realloc) and
	// teardown (arena_free) honor it. CHAINED (0) leaves the zero-initialized field untouched.
	intType := s.g.namedType("int")
	intLLVMType, lerr := s.g.lowerType(intType)
	if lerr != nil {
		return lerr
	}
	strategyConst := llvmConstInt(intLLVMType, llvmIRUnsignedLongLong(strategyTag), 0)
	helperType := &semantic.FuncType{Name: "new_region_backend", Params: []semantic.Type{capacityType, intType}, Return: regionRefType}
	callee, cerr := s.g.ensureFunctionDeclared("new_region_backend", helperType)
	if cerr != nil {
		return cerr
	}
	llvmFnType, ferr := s.g.lowerFunctionType(helperType)
	if ferr != nil {
		return ferr
	}
	regionValue := s.buildCall(llvmFnType, callee, []llvmIRValueRef{capacityValue, strategyConst}, "region.init")
	if strategyTag != 0 {
		strategyPtr := llvmBuildStructGEP2(s.builder, arenaLLVMType, arenaPtr, 3, cStringFree("region.strategy"))
		llvmBuildStore(s.builder, strategyConst, strategyPtr)
	}
	beginPtr := llvmBuildStructGEP2(s.builder, arenaLLVMType, arenaPtr, 0, cStringFree("region.begin"))
	endPtr := llvmBuildStructGEP2(s.builder, arenaLLVMType, arenaPtr, 1, cStringFree("region.end"))
	llvmBuildStore(s.builder, regionValue, beginPtr)
	llvmBuildStore(s.builder, regionValue, endPtr)
	if err := s.emitRegionProfileCreate(arenaPtr, regionValue, capacityValue); err != nil {
		return err
	}
	return nil
}

// emitRegionProfileCreate reports the backing block that an explicit region
// obtains during initialization. Lazy arenas already report this from arena_alloc;
// explicit regions bypass that path, so omitting this call loses their lifecycle
// entirely from the profiler stream.
func (s *functionState) emitRegionProfileCreate(arenaPtr llvmIRValueRef, regionValue llvmIRValueRef, capacityValue llvmIRValueRef) error {
	u32Type := s.g.namedType("u32")
	uintptrType := s.g.namedType("uintptr")
	usizeType := s.g.namedType("usize")
	voidType := s.g.namedType("void")
	if u32Type == nil || uintptrType == nil || usizeType == nil || voidType == nil {
		return fmt.Errorf("missing builtin type for profiler region-create event")
	}
	profileType := &semantic.FuncType{
		Name: "arena_profile_allocation_event",
		Params: []semantic.Type{
			u32Type, uintptrType, usizeType, uintptrType,
			usizeType, uintptrType, usizeType,
		},
		Return: voidType,
	}
	callee, err := s.g.ensureFunctionDeclared(profileType.Name, profileType)
	if err != nil {
		return err
	}
	llvmFnType, err := s.g.lowerFunctionType(profileType)
	if err != nil {
		return err
	}
	u32LLVMType, err := s.g.lowerType(u32Type)
	if err != nil {
		return err
	}
	uintptrLLVMType, err := s.g.lowerType(uintptrType)
	if err != nil {
		return err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	capacityBytes := llvmBuildMul(
		s.builder,
		capacityValue,
		llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(s.g.wordBits/8), 0),
		cStringFree("profile.region.bytes"),
	)
	args := []llvmIRValueRef{
		llvmConstInt(u32LLVMType, llvmIRUnsignedLongLong(profileAllocationRegionCreate), 0),
		llvmBuildPtrToInt(s.builder, regionValue, uintptrLLVMType, cStringFree("profile.region.address")),
		capacityBytes,
		llvmConstInt(uintptrLLVMType, 0, 0),
		llvmConstInt(usizeLLVMType, 0, 0),
		llvmBuildPtrToInt(s.builder, arenaPtr, uintptrLLVMType, cStringFree("profile.arena.address")),
		llvmConstInt(usizeLLVMType, 0, 0),
	}
	s.buildCall(llvmFnType, callee, args, "")
	return nil
}
func (s *functionState) emitArenaFree(arenaPtr llvmIRValueRef, arenaType semantic.Type) error {
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{Name: "arena_free", Params: []semantic.Type{arenaRefType}, Return: s.g.namedType("void")}
	callee, err := s.g.ensureFunctionDeclared("arena_free", helperType)
	if err != nil {
		return err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return err
	}
	s.buildCall(llvmFnType, callee, []llvmIRValueRef{arenaPtr}, "")
	return nil
}
func (s *functionState) emitArenaSnapshot(arenaPtr llvmIRValueRef, arenaType semantic.Type) (llvmIRValueRef, error) {
	markType := s.g.namedType("ArenaMark")
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
	return s.buildCall(llvmFnType, callee, []llvmIRValueRef{arenaPtr}, "region.mark"), nil
}
func (s *functionState) emitArenaRewind(arenaPtr llvmIRValueRef, arenaType semantic.Type, markValue llvmIRValueRef, markType semantic.Type) error {
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{Name: "arena_rewind", Params: []semantic.Type{arenaRefType, markType}, Return: s.g.namedType("void")}
	callee, err := s.g.ensureFunctionDeclared("arena_rewind", helperType)
	if err != nil {
		return err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return err
	}
	s.buildCall(llvmFnType, callee, []llvmIRValueRef{arenaPtr, markValue}, "")
	return nil
}
func (s *functionState) emitArenaReset(arenaPtr llvmIRValueRef, arenaType semantic.Type) error {
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{Name: "arena_reset", Params: []semantic.Type{arenaRefType}, Return: s.g.namedType("void")}
	callee, err := s.g.ensureFunctionDeclared("arena_reset", helperType)
	if err != nil {
		return err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return err
	}
	s.buildCall(llvmFnType, callee, []llvmIRValueRef{arenaPtr}, "")
	return nil
}
func (s *functionState) emitArenaAdopt(parentPtr llvmIRValueRef, childPtr llvmIRValueRef, arenaType semantic.Type) error {
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{Name: "arena_adopt", Params: []semantic.Type{arenaRefType, arenaRefType}, Return: s.g.namedType("void")}
	callee, err := s.g.ensureFunctionDeclared("arena_adopt", helperType)
	if err != nil {
		return err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return err
	}
	s.buildCall(llvmFnType, callee, []llvmIRValueRef{parentPtr, childPtr}, "")
	return nil
}
func (s *functionState) attachBranchHintMetadata(branch llvmIRValueRef, hint ast.BranchHint) {
	if s == nil || s.g == nil || branch == nil || hint == ast.BranchHintNone {
		return
	}
	trueWeight := branchWeightLikely
	falseWeight := branchWeightUnlikely
	if hint == ast.BranchHintUnlikely {
		trueWeight, falseWeight = falseWeight, trueWeight
	}
	llvmSetBranchWeights(branch, s.g.context, llvmIRUnsigned(trueWeight), llvmIRUnsigned(falseWeight))
}
func (s *functionState) buildCondBrWithHint(condValue llvmIRValueRef, trueBB llvmIRBlockRef, falseBB llvmIRBlockRef, hint ast.BranchHint) {
	branch := llvmBuildCondBr(s.builder, condValue, trueBB, falseBB)
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
