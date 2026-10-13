//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>

static void elisacorePanicReportAddEnumAttr(LLVMContextRef ctx, LLVMValueRef fn, const char *name, uint64_t value) {
	unsigned kind = LLVMGetEnumAttributeKindForName(name, strlen(name));
	if (kind == 0) {
		return;
	}
	LLVMAddAttributeAtIndex(fn, LLVMAttributeFunctionIndex, LLVMCreateEnumAttribute(ctx, kind, value));
}
*/
import "C"

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
func (s *functionState) ensurePanicReportFunction() (C.LLVMValueRef, C.LLVMTypeRef, error) {
	g := s.g
	ptrType := C.LLVMPointerTypeInContext(g.context, 0)
	intLLVMType, err := g.lowerType(g.result.NamedTypes["int"])
	if err != nil {
		return nil, nil, err
	}
	params := []C.LLVMTypeRef{ptrType, intLLVMType, intLLVMType, ptrType}
	fnType := C.LLVMFunctionType(C.LLVMVoidTypeInContext(g.context), (*C.LLVMTypeRef)(unsafe.Pointer(&params[0])), C.unsigned(len(params)), 0)
	if g.panicReportFn != nil {
		return g.panicReportFn, fnType, nil
	}
	nameC := cString(panicReportFunctionName)
	defer C.free(unsafe.Pointer(nameC))
	fn := C.LLVMAddFunction(g.module, nameC, fnType)
	C.LLVMSetLinkage(fn, C.LLVMPrivateLinkage)
	for _, attr := range []string{"noreturn", "cold", "noinline", "nounwind"} {
		attrC := cString(attr)
		C.elisacorePanicReportAddEnumAttr(g.context, fn, attrC, 0)
		C.free(unsafe.Pointer(attrC))
	}
	memC := cString("memory")
	C.elisacorePanicReportAddEnumAttr(g.context, fn, memC, C.uint64_t(panicReportMemoryEffects))
	C.free(unsafe.Pointer(memC))
	g.panicReportFn = fn

	// Build the body with this function's builder, then restore the insertion point.
	savedBlock := C.LLVMGetInsertBlock(s.builder)
	entryName := cString("entry")
	entry := C.LLVMAppendBasicBlockInContext(g.context, fn, entryName)
	C.free(unsafe.Pointer(entryName))
	C.LLVMPositionBuilderAtEnd(s.builder, entry)
	defer C.LLVMPositionBuilderAtEnd(s.builder, savedBlock)

	fileParam := C.LLVMGetParam(fn, 0)
	lineParam := C.LLVMGetParam(fn, 1)
	colParam := C.LLVMGetParam(fn, 2)
	messageParam := C.LLVMGetParam(fn, 3)

	printfFn, printfType, err := s.ensurePrintfFunction()
	if err != nil {
		return nil, nil, err
	}
	printfLLVMType, err := g.lowerFunctionType(printfType)
	if err != nil {
		return nil, nil, err
	}
	formatPtr := s.emitGlobalCStringLiteral("panic at %s:%d:%d: %s\n", "panic.format")
	s.buildCall(printfLLVMType, printfFn, []C.LLVMValueRef{formatPtr, fileParam, lineParam, colParam, messageParam}, "")

	backtraceFn, backtraceType, err := s.ensureBacktraceFunction()
	if err != nil {
		return nil, nil, err
	}
	backtraceLLVMType, err := g.lowerFunctionType(backtraceType)
	if err != nil {
		return nil, nil, err
	}
	bufferType := C.LLVMArrayType2(ptrType, 64)
	buffer := C.LLVMBuildAlloca(s.builder, bufferType, cStringFree("panic.backtrace.buffer"))
	frameLimit := C.LLVMConstInt(intLLVMType, 64, 0)
	frameCount := s.buildCall(backtraceLLVMType, backtraceFn, []C.LLVMValueRef{buffer, frameLimit}, "panic.backtrace.count")
	backtraceHeader := s.emitGlobalCStringLiteral("backtrace:\n", "panic.backtrace.header")
	s.buildCall(printfLLVMType, printfFn, []C.LLVMValueRef{backtraceHeader}, "")
	backtraceSymbolsFn, backtraceSymbolsType, err := s.ensureBacktraceSymbolsFDFunction()
	if err != nil {
		return nil, nil, err
	}
	backtraceSymbolsLLVMType, err := g.lowerFunctionType(backtraceSymbolsType)
	if err != nil {
		return nil, nil, err
	}
	stderrFD := C.LLVMConstInt(intLLVMType, 2, 0)
	s.buildCall(backtraceSymbolsLLVMType, backtraceSymbolsFn, []C.LLVMValueRef{buffer, frameCount, stderrFD}, "")
	abortFn, abortType, err := s.ensureAbortFunction()
	if err != nil {
		return nil, nil, err
	}
	abortLLVMType, err := g.lowerFunctionType(abortType)
	if err != nil {
		return nil, nil, err
	}
	s.buildCall(abortLLVMType, abortFn, nil, "")
	C.LLVMBuildUnreachable(s.builder)
	return fn, fnType, nil
}
