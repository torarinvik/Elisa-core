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
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
)

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
func (s *functionState) emitRegionInit(arenaPtr C.LLVMValueRef, arenaType semantic.Type, capacityExpr ast.Expr, allocator string) error {
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
	arenaLLVMType, err := s.g.lowerType(arenaType)
	if err != nil {
		return err
	}
	var regionValue C.LLVMValueRef
	if allocator == "malloc" {
		// `region NAME(cap) using malloc:` — eagerly create the first block via the
		// backend-dispatching allocator and record the selector in Arena.backend (field 3)
		// so later growth (arena_alloc) and teardown (arena_free) keep using malloc.
		// ARENA_REGION_BACKEND_MALLOC == 1 (see arena.elisa).
		intType := s.g.result.NamedTypes["int"]
		intLLVMType, lerr := s.g.lowerType(intType)
		if lerr != nil {
			return lerr
		}
		backendTag := C.LLVMConstInt(intLLVMType, 1, 0)
		helperType := &semantic.FuncType{Name: "new_region_backend", Params: []semantic.Type{capacityType, intType}, Return: regionRefType}
		callee, cerr := s.g.ensureFunctionDeclared("new_region_backend", helperType)
		if cerr != nil {
			return cerr
		}
		llvmFnType, ferr := s.g.lowerFunctionType(helperType)
		if ferr != nil {
			return ferr
		}
		regionValue = s.buildCall(llvmFnType, callee, []C.LLVMValueRef{capacityValue, backendTag}, "region.init")
		backendPtr := C.LLVMBuildStructGEP2(s.builder, arenaLLVMType, arenaPtr, 3, cStringFree("region.backend"))
		C.LLVMBuildStore(s.builder, backendTag, backendPtr)
	} else {
		helperType := &semantic.FuncType{Name: "new_region", Params: []semantic.Type{capacityType}, Return: regionRefType}
		callee, cerr := s.g.ensureFunctionDeclared("new_region", helperType)
		if cerr != nil {
			return cerr
		}
		llvmFnType, ferr := s.g.lowerFunctionType(helperType)
		if ferr != nil {
			return ferr
		}
		regionValue = s.buildCall(llvmFnType, callee, []C.LLVMValueRef{capacityValue}, "region.init")
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
	C.elisa_coreSetBranchWeights(branch, s.g.context, C.uint(trueWeight), C.uint(falseWeight))
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
