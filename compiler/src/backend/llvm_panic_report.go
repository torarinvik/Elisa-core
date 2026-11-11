//go:build cgo

package backend

import "unsafe"

// panicReportMemoryEffects is LLVM's MemoryEffects encoding (2 bits per location: ArgMem=0,
// InaccessibleMem=1, ErrnoMem=2, Other=3; Ref=1, Mod=2) for
// `memory(argmem: read, inaccessiblemem: readwrite)`. TestPanicReportHelperIsOutlinedAndCold
// checks the printed attribute so an LLVM encoding change cannot slip through.
const panicReportMemoryEffects = 1 | (3 << 2)

const panicReportFunctionName = "elisa.panic.report"

// ensurePanicReportFunction returns the module's single outlined panic reporter:
//
//	void elisa.panic.report(ptr file, int line, int col, ptr message)  ; prints, backtraces, aborts
//
// Outlining keeps every panic site to one cold call, so the code size of a function with a
// `panic(...)` arm no longer blocks inlining. The helper is `noreturn cold noinline nounwind` and
// `memory(argmem: read, inaccessiblemem: readwrite)`: it reads only the strings it is passed, its
// printf/backtrace state is libc-internal, and it never returns, so no caller memory it might
// touch is ever observed again. That lets FunctionAttrs infer `memory(read)` for a function whose
// only write is its panic arm (e.g. a lookup that panics on a miss), so callers keep loaded
// darray headers in registers across the call.
func (s *functionState) ensurePanicReportFunction() (llvmIRValueRef, llvmIRTypeRef, error) {
	g := s.g
	ptrType := llvmPointerTypeInContext(g.context, 0)
	intLLVMType, err := g.lowerType(g.namedType("int"))
	if err != nil {
		return nil, nil, err
	}
	params := []llvmIRTypeRef{ptrType, intLLVMType, intLLVMType, ptrType}
	fnType := llvmFunctionType(llvmVoidTypeInContext(g.context), (*llvmIRTypeRef)(unsafe.Pointer(&params[0])), llvmIRUnsigned(len(params)), 0)
	if g.panicReportFn != nil {
		return g.panicReportFn, fnType, nil
	}
	nameC := cString(panicReportFunctionName)
	defer llvmFree(unsafe.Pointer(nameC))
	fn := llvmAddFunction(g.module, nameC, fnType)
	llvmSetLinkage(fn, llvmPrivateLinkage)
	for _, attr := range []string{"noreturn", "cold", "noinline", "nounwind"} {
		attrC := cString(attr)
		llvmPanicReportAddEnumAttr(g.context, fn, attrC, 0)
		llvmFree(unsafe.Pointer(attrC))
	}
	memC := cString("memory")
	llvmPanicReportAddEnumAttr(g.context, fn, memC, llvmIRUint64(panicReportMemoryEffects))
	llvmFree(unsafe.Pointer(memC))
	g.panicReportFn = fn

	// Build the body with this function's builder, then restore the insertion point.
	savedBlock := llvmGetInsertBlock(s.builder)
	entryName := cString("entry")
	entry := llvmAppendBasicBlockInContext(g.context, fn, entryName)
	llvmFree(unsafe.Pointer(entryName))
	llvmPositionBuilderAtEnd(s.builder, entry)
	defer llvmPositionBuilderAtEnd(s.builder, savedBlock)

	fileParam := llvmGetParam(fn, 0)
	lineParam := llvmGetParam(fn, 1)
	colParam := llvmGetParam(fn, 2)
	messageParam := llvmGetParam(fn, 3)

	printfFn, printfType, err := s.ensurePrintfFunction()
	if err != nil {
		return nil, nil, err
	}
	printfLLVMType, err := g.lowerFunctionType(printfType)
	if err != nil {
		return nil, nil, err
	}
	formatPtr := s.emitGlobalCStringLiteral("panic at %s:%d:%d: %s\n", "panic.format")
	s.buildCall(printfLLVMType, printfFn, []llvmIRValueRef{formatPtr, fileParam, lineParam, colParam, messageParam}, "")

	backtraceFn, backtraceType, err := s.ensureBacktraceFunction()
	if err != nil {
		return nil, nil, err
	}
	backtraceLLVMType, err := g.lowerFunctionType(backtraceType)
	if err != nil {
		return nil, nil, err
	}
	bufferType := llvmArrayType2(ptrType, 64)
	buffer := llvmBuildAlloca(s.builder, bufferType, cStringFree("panic.backtrace.buffer"))
	frameLimit := llvmConstInt(intLLVMType, 64, 0)
	frameCount := s.buildCall(backtraceLLVMType, backtraceFn, []llvmIRValueRef{buffer, frameLimit}, "panic.backtrace.count")
	backtraceHeader := s.emitGlobalCStringLiteral("backtrace:\n", "panic.backtrace.header")
	s.buildCall(printfLLVMType, printfFn, []llvmIRValueRef{backtraceHeader}, "")
	backtraceSymbolsFn, backtraceSymbolsType, err := s.ensureBacktraceSymbolsFDFunction()
	if err != nil {
		return nil, nil, err
	}
	backtraceSymbolsLLVMType, err := g.lowerFunctionType(backtraceSymbolsType)
	if err != nil {
		return nil, nil, err
	}
	stderrFD := llvmConstInt(intLLVMType, 2, 0)
	s.buildCall(backtraceSymbolsLLVMType, backtraceSymbolsFn, []llvmIRValueRef{buffer, frameCount, stderrFD}, "")
	abortFn, abortType, err := s.ensureAbortFunction()
	if err != nil {
		return nil, nil, err
	}
	abortLLVMType, err := g.lowerFunctionType(abortType)
	if err != nil {
		return nil, nil, err
	}
	s.buildCall(abortLLVMType, abortFn, nil, "")
	llvmBuildUnreachable(s.builder)
	return fn, fnType, nil
}
