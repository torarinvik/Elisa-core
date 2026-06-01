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

// Declare (idempotently) void elisa_trace_record_value(i8* name, i32 line, i8* var, i64 value).
static LLVMValueRef elisacoreDeclareTraceRecordValue(LLVMModuleRef m, LLVMContextRef ctx, LLVMTypeRef *outFnTy) {
	LLVMTypeRef i8ptr = LLVMPointerType(LLVMInt8TypeInContext(ctx), 0);
	LLVMTypeRef params[4];
	params[0] = i8ptr;
	params[1] = LLVMInt32TypeInContext(ctx);
	params[2] = i8ptr;
	params[3] = LLVMInt64TypeInContext(ctx);
	LLVMTypeRef fnTy = LLVMFunctionType(LLVMVoidTypeInContext(ctx), params, 4, 0);
	*outFnTy = fnTy;
	LLVMValueRef fn = LLVMGetNamedFunction(m, "elisa_trace_record_value");
	if (fn == NULL) {
		fn = LLVMAddFunction(m, "elisa_trace_record_value", fnTy);
	}
	return fn;
}

// Widen a scalar/pointer value to i64 for recording. Returns NULL for values that do not
// fit a single integer register (floats, aggregates) -- those are recorded as plain steps.
static LLVMValueRef elisacoreTraceValueToI64(LLVMBuilderRef b, LLVMContextRef ctx, LLVMValueRef v) {
	if (v == NULL) {
		return NULL;
	}
	LLVMTypeRef ty = LLVMTypeOf(v);
	LLVMTypeKind kind = LLVMGetTypeKind(ty);
	LLVMTypeRef i64 = LLVMInt64TypeInContext(ctx);
	if (kind == LLVMPointerTypeKind) {
		return LLVMBuildPtrToInt(b, v, i64, "trace.ptr");
	}
	if (kind == LLVMIntegerTypeKind) {
		unsigned w = LLVMGetIntTypeWidth(ty);
		if (w == 64) {
			return v;
		}
		if (w < 64) {
			return LLVMBuildZExt(b, v, i64, "trace.zext");
		}
		return LLVMBuildTrunc(b, v, i64, "trace.trunc");
	}
	return NULL;
}

static void elisacoreBuildTraceValueCall(LLVMBuilderRef b, LLVMContextRef ctx, LLVMTypeRef fnTy, LLVMValueRef fn,
		LLVMValueRef nameGlobal, unsigned line, LLVMValueRef varGlobal, LLVMValueRef value64) {
	LLVMValueRef args[4];
	args[0] = nameGlobal;
	args[1] = LLVMConstInt(LLVMInt32TypeInContext(ctx), line, 0);
	args[2] = varGlobal;
	args[3] = value64;
	LLVMBuildCall2(b, fnTy, fn, args, 4, "");
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
	valueFn    C.LLVMValueRef
	valueFnTy  C.LLVMTypeRef
	nameCache  map[string]C.LLVMValueRef
}

func (g *llvmGenerator) initTrace() {
	if g == nil || g.module == nil || g.trace != nil {
		return
	}
	t := &traceState{g: g, nameCache: map[string]C.LLVMValueRef{}}
	var fnTy C.LLVMTypeRef
	t.recordFn = C.elisacoreDeclareTraceRecord(g.module, g.context, &fnTy)
	t.recordFnTy = fnTy
	var valFnTy C.LLVMTypeRef
	t.valueFn = C.elisacoreDeclareTraceRecordValue(g.module, g.context, &valFnTy)
	t.valueFnTy = valFnTy
	g.trace = t
}

// nameGlobalFor returns a private, deduplicated string constant for a name (function or
// variable). Caching keeps one global per distinct name.
func (t *traceState) nameGlobalFor(name string) C.LLVMValueRef {
	if g, ok := t.nameCache[name]; ok {
		return g
	}
	nameC := C.CString(name)
	g := C.elisacoreTraceStringGlobal(t.g.module, t.g.context, nameC, C.size_t(len(name)))
	C.free(unsafe.Pointer(nameC))
	t.nameCache[name] = g
	return g
}

// recordValue emits a value record if the value widens to i64 (scalar/pointer); otherwise
// it falls back to a plain step record so the line still appears in the path.
func (t *traceState) recordValue(state *functionState, line int, varName string, value C.LLVMValueRef) {
	if t == nil || state == nil || state.builder == nil || state.traceNameGlobal == nil {
		return
	}
	if line < 0 {
		line = 0
	}
	var v64 C.LLVMValueRef
	if value != nil {
		v64 = C.elisacoreTraceValueToI64(state.builder, t.g.context, value)
	}
	if v64 == nil || varName == "" || varName == "_" {
		C.elisacoreBuildTraceCall(state.builder, t.g.context, t.recordFnTy, t.recordFn,
			state.traceNameGlobal, C.unsigned(line))
		return
	}
	varGlobal := t.nameGlobalFor(varName)
	C.elisacoreBuildTraceValueCall(state.builder, t.g.context, t.valueFnTy, t.valueFn,
		state.traceNameGlobal, C.unsigned(line), varGlobal, v64)
}

// recordStmt emits the per-statement record call. It runs after setLoc so the call
// inherits the current debug location when -g is also enabled.
func (t *traceState) recordStmt(state *functionState, stmt ast.Stmt) {
	if t == nil || state == nil || stmt == nil || state.builder == nil || state.traceNameGlobal == nil {
		return
	}
	// A variable declaration self-records (with its value) in the VarDeclStmt handler;
	// skip the generic step here to avoid a duplicate entry for that line.
	if _, ok := stmt.(*ast.VarDeclStmt); ok {
		return
	}
	line := stmt.Pos().Line
	if line < 0 {
		line = 0
	}
	C.elisacoreBuildTraceCall(state.builder, t.g.context, t.recordFnTy, t.recordFn,
		state.traceNameGlobal, C.unsigned(line))
}
