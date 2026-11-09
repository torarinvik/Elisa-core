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

const profileAllocationRegionCreate uint64 = 5

func (s *functionState) emitParallelForStmt(stmt *ast.ParallelForStmt) error {
	info, ok := s.g.semanticReadView().LookupParallelFor(stmt)
	if !ok || info == nil {
		return fmt.Errorf("missing semantic parallel-for info")
	}
	pool, ok := s.currentActivePool()
	if !ok {
		// No explicit `pool workers(w):` scope -> the implicit default pool: a zeroed ThreadPool
		// (pool_submit_raw ignores the handle, spawning a worker per task) with perf_cores() workers.
		pool, err := s.defaultParallelForPool(stmt)
		if err != nil {
			return err
		}
		return s.emitParallelForStmtWithPool(stmt, info, pool)
	}
	return s.emitParallelForStmtWithPool(stmt, info, pool)
}

// defaultParallelForPool builds the implicit pool binding used when `parallel for` has no explicit
// `pool workers(w):` scope: default_pool() (a cost-free zeroed dispatcher) plus a perf_cores()
// worker count. Both are runtime calls; the ThreadPool is stored in a fresh alloca for pool_submit1.
func (s *functionState) defaultParallelForPool(stmt *ast.ParallelForStmt) (activePoolBinding, error) {
	poolType := s.g.namedType("ThreadPool")
	usizeType := s.g.namedType("usize")
	defCallee, defType, err := s.ensureRuntimeFunction("default_pool", nil)
	if err != nil {
		return activePoolBinding{}, err
	}
	defLLVM, err := s.g.lowerFunctionType(defType)
	if err != nil {
		return activePoolBinding{}, err
	}
	poolValue := s.buildCall(defLLVM, defCallee, nil, "parallel.default.pool")
	poolAlloca, err := s.createEntryAlloca("__parallel_default_pool", poolType)
	if err != nil {
		return activePoolBinding{}, err
	}
	C.LLVMBuildStore(s.builder, poolValue, poolAlloca)

	coresCallee, coresType, err := s.ensureRuntimeFunction("perf_cores", nil)
	if err != nil {
		return activePoolBinding{}, err
	}
	coresLLVM, err := s.g.lowerFunctionType(coresType)
	if err != nil {
		return activePoolBinding{}, err
	}
	coresI64 := s.buildCall(coresLLVM, coresCallee, nil, "parallel.default.cores")
	usizeLLVM, err := s.g.lowerType(usizeType)
	if err != nil {
		return activePoolBinding{}, err
	}
	// perf_cores() returns i64; usize is the same width on supported targets, so a bitcast/trunc is
	// unnecessary -- the value is already a non-negative core count in an i64.
	workers := coresI64
	if C.LLVMGetIntTypeWidth(C.LLVMTypeOf(coresI64)) != C.LLVMGetIntTypeWidth(usizeLLVM) {
		workers = C.LLVMBuildTrunc(s.builder, coresI64, usizeLLVM, cStringFree("parallel.default.cores.usize"))
	}
	return activePoolBinding{name: "__parallel_default_pool", ptr: poolAlloca, typ: poolType, workers: workers}, nil
}

func (s *functionState) emitParallelForStmtWithPool(stmt *ast.ParallelForStmt, info *semantic.ParallelForInfo, pool activePoolBinding) error {
	sourceValue, _, err := s.emitExpr(stmt.Source, info.SourceType)
	if err != nil {
		return err
	}

	partitionLocal := s.g.partSynthNS != "" && s.g.optLevel == OptimizationLevel0 && !s.g.emitDebugInfo && !s.g.emitTrace
	prefix := ""
	if partitionLocal {
		prefix = s.g.nextSyntheticHelperName("__parallel_for_")
	} else {
		// Without a partition-qualified nominal type and local specialization,
		// the generated worker graph can collide or be split across modules.
		s.g.partUnsafeHelpers = true
		prefix = s.g.nextSyntheticName("__parallel_for_")
	}
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
	if info.BandMode {
		lengthField = "count" // Slice[T] exposes its length as `count`
	}
	totalExpr := &ast.FieldExpr{Position: stmt.Position, Object: sourceIdent, Field: lengthField}
	usizeType := s.g.namedType("usize")
	totalValue, _, err := s.emitExpr(totalExpr, usizeType)
	if err != nil {
		return err
	}

	groupType := s.g.namedType("TaskGroup")
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

	workerFn, workerFnType, chunkType, err := s.emitParallelForWorkerFunction(stmt, info, prefix, partitionLocal)
	if err != nil {
		return err
	}
	partitionLocalSubmit := partitionLocal && s.g.partSubmitTypes.register(chunkType)
	if partitionLocal && !partitionLocalSubmit {
		s.g.partUnsafeHelpers = true
	}
	var poolSubmit C.LLVMValueRef
	var poolSubmitType *semantic.FuncType
	if partitionLocalSubmit {
		s.g.partSyntheticDepth++
	}
	poolSubmit, poolSubmitType, err = s.ensureRuntimeFunction("pool_submit1", map[string]semantic.Type{"A": chunkType, "R": s.g.namedType("i64")})
	if partitionLocalSubmit {
		s.g.partSyntheticDepth--
	}
	if err != nil {
		return err
	}
	poolSubmitLLVMType, err := s.g.lowerFunctionType(poolSubmitType)
	if err != nil {
		return err
	}
	taskGroupAdd, taskGroupAddType, err := s.ensureRuntimeFunction("task_group_add", map[string]semantic.Type{"R": s.g.namedType("i64")})
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
func (s *functionState) emitParallelForWorkerFunction(stmt *ast.ParallelForStmt, info *semantic.ParallelForInfo, prefix string, partitionLocal bool) (C.LLVMValueRef, *semantic.FuncType, *semantic.StructType, error) {
	chunkType, err := s.buildParallelForChunkType(info, prefix)
	if err != nil {
		return nil, nil, nil, err
	}
	workerName := prefix + "_worker"
	// The worker returns i64 (not void): pool_submit1 boxes the result type, and boxing a void
	// value crashes lowering. Workers return a dummy 0 -- matching the each()/nursery convention.
	i64Type := s.g.namedType("i64")
	workerType := &semantic.FuncType{Name: workerName, Params: []semantic.Type{chunkType}, Return: i64Type}
	workerFn, err := s.g.addFunction(workerName, workerType)
	if err != nil {
		return nil, nil, nil, err
	}
	s.g.functions[workerName] = workerFn
	s.g.setDefinedFunctionLinkage(workerName, workerFn, workerType)
	if partitionLocal {
		C.LLVMSetLinkage(workerFn, C.LLVMPrivateLinkage)
		if s.g.partLocalHelpers == nil {
			s.g.partLocalHelpers = make(map[string]bool)
		}
		s.g.partLocalHelpers[llvmValueName(workerFn)] = true
	}

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

	// Band mode: the chunk carries this worker's precomputed disjoint Slice[T] band (field
	// "band") plus its global start offset. Bind the loop variable to the band and run the body
	// ONCE -- no per-element inner loop. The band points into the shared backing so writes go
	// through; disjointness across workers (the [start, end) chunks tile the slice) is race-free.
	if info.BandMode {
		var bandBody []ast.Stmt
		for i, name := range info.Captures {
			bandBody = append(bandBody, &ast.VarDeclStmt{
				Position: stmt.Position,
				Name:     name,
				Value:    &ast.FieldExpr{Position: stmt.Position, Object: chunkIdent, Field: fmt.Sprintf("capture_%d", i)},
			})
		}
		if stmt.IndexName != "" {
			bandBody = append(bandBody, &ast.VarDeclStmt{
				Position: stmt.Position,
				Name:     stmt.IndexName,
				Type:     &ast.NamedType{Position: stmt.Position, Name: "usize"},
				Value:    &ast.FieldExpr{Position: stmt.Position, Object: chunkIdent, Field: "start"},
			})
		}
		// No explicit type: the band's Slice[T] type is inferred from the chunk.band field read
		// (Slice[T] has no resolvable bare-name spelling for a synthesized NamedType).
		bandBody = append(bandBody, &ast.VarDeclStmt{
			Position: stmt.Position,
			Name:     stmt.Name,
			Value:    &ast.FieldExpr{Position: stmt.Position, Object: chunkIdent, Field: "band"},
		})
		bandBody = append(bandBody, stmt.Body...)
		bandBody = append(bandBody, &ast.ReturnStmt{Position: stmt.Position, Value: &ast.IntLit{Position: stmt.Position, Value: "0"}})
		workerDecl := &ast.FuncDecl{
			Position:   stmt.Position,
			Name:       workerName,
			Params:     []ast.ParamDecl{{Position: stmt.Position, Name: chunkParamName}},
			ReturnType: &ast.NamedType{Position: stmt.Position, Name: "i64"},
			Body:       bandBody,
		}
		if err := s.g.defineFunctionBody(workerDecl, workerType, workerFn); err != nil {
			return nil, nil, nil, err
		}
		return workerFn, workerType, chunkType, nil
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
	body = append(body, &ast.ReturnStmt{Position: stmt.Position, Value: &ast.IntLit{Position: stmt.Position, Value: "0"}})

	{
		key, value := condExpr, s.g.namedType("bool")
		s.g.setResultExprType(key, value)
	}

	workerDecl := &ast.FuncDecl{
		Position:   stmt.Position,
		Name:       workerName,
		Params:     []ast.ParamDecl{{Position: stmt.Position, Name: chunkParamName}},
		ReturnType: &ast.NamedType{Position: stmt.Position, Name: "i64"},
		Body:       body,
	}
	if err := s.g.defineFunctionBody(workerDecl, workerType, workerFn); err != nil {
		return nil, nil, nil, err
	}
	return workerFn, workerType, chunkType, nil
}
func (s *functionState) buildParallelForChunkType(info *semantic.ParallelForInfo, prefix string) (*semantic.StructType, error) {
	usize := s.g.namedType("usize")
	var fields map[string]semantic.Field
	var declFields []ast.FieldDecl
	if info.BandMode {
		// Band mode: the chunk carries the precomputed band + its global start offset.
		fields = map[string]semantic.Field{
			"band":  {Name: "band", Type: info.SourceType},
			"start": {Name: "start", Type: usize},
		}
		declFields = []ast.FieldDecl{
			{Position: lexer.Pos{}, Name: "band"},
			{Position: lexer.Pos{}, Name: "start"},
		}
	} else {
		fields = map[string]semantic.Field{
			"source": {Name: "source", Type: info.SourceType},
			"start":  {Name: "start", Type: usize},
			"end":    {Name: "end", Type: usize},
		}
		declFields = []ast.FieldDecl{
			{Position: lexer.Pos{}, Name: "source"},
			{Position: lexer.Pos{}, Name: "start"},
			{Position: lexer.Pos{}, Name: "end"},
		}
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
	captureBase := 3
	if info.BandMode {
		// Build this worker's band inline: { base: source.base + start*sizeof(T), count: end-start }.
		// No call (no ABI coercion); mirrors slice_band. The chunk range is already within
		// [0, source.count], so no clamping is needed. Slice[T] field order is {base, count}.
		elemSize, err := s.sizeOfType(info.BandElement)
		if err != nil {
			return nil, err
		}
		sliceLLVM, err := s.g.lowerType(info.SourceType)
		if err != nil {
			return nil, err
		}
		usizeLLVM, err := s.g.lowerType(s.g.namedType("usize"))
		if err != nil {
			return nil, err
		}
		srcBase := C.LLVMBuildExtractValue(s.builder, sourceValue, 0, cStringFree("band.src.base"))
		elemSizeConst := C.LLVMConstInt(usizeLLVM, C.ulonglong(elemSize), 0)
		offset := C.LLVMBuildMul(s.builder, startValue, elemSizeConst, cStringFree("band.offset"))
		bandBase := C.LLVMBuildAdd(s.builder, srcBase, offset, cStringFree("band.base"))
		bandCount := C.LLVMBuildSub(s.builder, endValue, startValue, cStringFree("band.count"))
		bandValue := C.LLVMGetUndef(sliceLLVM)
		bandValue = C.LLVMBuildInsertValue(s.builder, bandValue, bandBase, 0, cStringFree("band.base.ins"))
		bandValue = C.LLVMBuildInsertValue(s.builder, bandValue, bandCount, 1, cStringFree("band.count.ins"))
		chunkValue = C.LLVMBuildInsertValue(s.builder, chunkValue, bandValue, 0, cStringFree("parallel.chunk.band"))
		chunkValue = C.LLVMBuildInsertValue(s.builder, chunkValue, startValue, 1, cStringFree("parallel.chunk.start"))
		captureBase = 2
	} else {
		chunkValue = C.LLVMBuildInsertValue(s.builder, chunkValue, sourceValue, 0, cStringFree("parallel.chunk.source"))
		chunkValue = C.LLVMBuildInsertValue(s.builder, chunkValue, startValue, 1, cStringFree("parallel.chunk.start"))
		chunkValue = C.LLVMBuildInsertValue(s.builder, chunkValue, endValue, 2, cStringFree("parallel.chunk.end"))
	}
	for i, name := range info.Captures {
		binding, ok := s.lookupBinding(name)
		if !ok {
			return nil, fmt.Errorf("missing capture binding %q during parallel-for lowering", name)
		}
		value, err := s.loadValue(binding.ptr, binding.typ, name)
		if err != nil {
			return nil, err
		}
		chunkValue = C.LLVMBuildInsertValue(s.builder, chunkValue, value, C.unsigned(captureBase+i), cStringFree("parallel.chunk.capture"))
	}
	return chunkValue, nil
}
func (s *functionState) ensureRuntimeFunction(name string, bindings map[string]semantic.Type) (C.LLVMValueRef, *semantic.FuncType, error) {
	sym, ok := s.g.lookupGlobal(name)
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
