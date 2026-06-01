//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

// Declare (idempotently) void elisa_trace_record(i8* name, i32 line) and return it
// plus its function type for building calls. The runtime that defines it lives in
// debug_referee.elisa (linked standalone alongside any program).
static LLVMValueRef elisacoreDeclareTraceRecord(LLVMModuleRef m, LLVMContextRef ctx, LLVMTypeRef *outFnTy) {
	LLVMTypeRef params[2];
	params[0] = LLVMPointerType(LLVMInt8TypeInContext(ctx), 0);
	params[1] = LLVMInt32TypeInContext(ctx);
	LLVMTypeRef fnTy = LLVMFunctionType(LLVMVoidTypeInContext(ctx), params, 2, 0);
	*outFnTy = fnTy;
	LLVMValueRef fn = LLVMGetNamedFunction(m, "elisa_trace_record");
	if (fn == NULL) {
		fn = LLVMAddFunction(m, "elisa_trace_record", fnTy);
	}
	return fn;
}

// A private, constant, null-terminated string global; returns its (opaque) pointer.
static LLVMValueRef elisacoreTraceStringGlobal(LLVMModuleRef m, LLVMContextRef ctx, const char *s, size_t len) {
	LLVMValueRef str = LLVMConstStringInContext(ctx, s, (unsigned)len, 0);
	LLVMTypeRef arrTy = LLVMArrayType(LLVMInt8TypeInContext(ctx), (unsigned)(len + 1));
	LLVMValueRef g = LLVMAddGlobal(m, arrTy, ".elisa.trace.fname");
	LLVMSetInitializer(g, str);
	LLVMSetGlobalConstant(g, 1);
	LLVMSetLinkage(g, LLVMPrivateLinkage);
	return g;
}

static void elisacoreBuildTraceCall(LLVMBuilderRef b, LLVMContextRef ctx, LLVMTypeRef fnTy, LLVMValueRef fn,
		LLVMValueRef nameGlobal, unsigned line) {
	LLVMValueRef args[2];
	args[0] = nameGlobal;
	args[1] = LLVMConstInt(LLVMInt32TypeInContext(ctx), line, 0);
	LLVMBuildCall2(b, fnTy, fn, args, 2, "");
}
*/
import "C"

import (
	"unsafe"

	"elisacore/src/ast"
)

// traceState drives -ftrace instrumentation: it emits a call to
// elisa_trace_record(func_name, line) at every statement. The runtime (in
// debug_referee.elisa) keeps only a fixed-size ring of recent steps, giving bounded
// "rewind a few steps" observability with no reversible execution. The function name is
// passed as a pointer to a per-function private string constant, so there is no separate
// id/name table to maintain or relocate.
type traceState struct {
	g          *llvmGenerator
	recordFn   C.LLVMValueRef
	recordFnTy C.LLVMTypeRef
}

func (g *llvmGenerator) initTrace() {
	if g == nil || g.module == nil || g.trace != nil {
		return
	}
	t := &traceState{g: g}
	var fnTy C.LLVMTypeRef
	t.recordFn = C.elisacoreDeclareTraceRecord(g.module, g.context, &fnTy)
	t.recordFnTy = fnTy
	g.trace = t
}

// nameGlobalFor returns a private string constant holding the function's name.
func (t *traceState) nameGlobalFor(name string) C.LLVMValueRef {
	nameC := C.CString(name)
	defer C.free(unsafe.Pointer(nameC))
	return C.elisacoreTraceStringGlobal(t.g.module, t.g.context, nameC, C.size_t(len(name)))
}

// recordStmt emits the per-statement record call. It runs after setLoc so the call
// inherits the current debug location when -g is also enabled.
func (t *traceState) recordStmt(state *functionState, stmt ast.Stmt) {
	if t == nil || state == nil || stmt == nil || state.builder == nil || state.traceNameGlobal == nil {
		return
	}
	line := stmt.Pos().Line
	if line < 0 {
		line = 0
	}
	C.elisacoreBuildTraceCall(state.builder, t.g.context, t.recordFnTy, t.recordFn,
		state.traceNameGlobal, C.unsigned(line))
}
