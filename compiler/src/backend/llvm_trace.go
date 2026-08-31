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

// Declare (idempotently) void elisa_trace_record_value(i8* name, i32 line, i8* var, i64 value, i32 signed).
static LLVMValueRef elisacoreDeclareTraceRecordValue(LLVMModuleRef m, LLVMContextRef ctx, LLVMTypeRef *outFnTy) {
	LLVMTypeRef i8ptr = LLVMPointerType(LLVMInt8TypeInContext(ctx), 0);
	LLVMTypeRef params[5];
	params[0] = i8ptr;
	params[1] = LLVMInt32TypeInContext(ctx);
	params[2] = i8ptr;
	params[3] = LLVMInt64TypeInContext(ctx);
	params[4] = LLVMInt32TypeInContext(ctx);
	LLVMTypeRef fnTy = LLVMFunctionType(LLVMVoidTypeInContext(ctx), params, 5, 0);
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
		LLVMValueRef nameGlobal, unsigned line, LLVMValueRef varGlobal, LLVMValueRef value64, unsigned signedValue) {
	LLVMValueRef args[5];
	args[0] = nameGlobal;
	args[1] = LLVMConstInt(LLVMInt32TypeInContext(ctx), line, 0);
	args[2] = varGlobal;
	args[3] = value64;
	args[4] = LLVMConstInt(LLVMInt32TypeInContext(ctx), signedValue, 0);
	LLVMBuildCall2(b, fnTy, fn, args, 5, "");
}
*/
import "C"

import (
	"unsafe"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// traceCapturableScalar reports whether a value of this semantic type fits a single
// integer register and is safe to widen to i64 for recording. Aggregates, error unions,
// optionals, dstr etc. are skipped (recorded as a plain step) -- both because they do not
// fit i64 and because their LLVM value representation is not always a plain scalar the
// widening helper can consume.
func traceCapturableScalar(t semantic.Type) bool {
	if t == nil {
		return false
	}
	if _, ok := t.(*semantic.RefType); ok {
		return true
	}
	return isNumericType(t) || semantic.IsBoolType(t)
}

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
func (t *traceState) recordValue(state *functionState, line int, varName string, value C.LLVMValueRef, valueType semantic.Type) {
	if t == nil || state == nil || state.builder == nil || state.traceNameGlobal == nil {
		return
	}
	if line < 0 {
		line = 0
	}
	var v64 C.LLVMValueRef
	if value != nil && traceCapturableScalar(valueType) {
		v64 = C.elisacoreTraceValueToI64(state.builder, t.g.context, value)
	}
	if v64 == nil || varName == "" || varName == "_" {
		t.recordStep(state, line)
		return
	}
	varGlobal := t.nameGlobalFor(varName)
	C.elisacoreBuildTraceValueCall(state.builder, t.g.context, t.valueFnTy, t.valueFn,
		state.traceNameGlobal, C.unsigned(line), varGlobal, v64, C.unsigned(traceValueSigned(valueType)))
}

func traceValueSigned(t semantic.Type) uint {
	if t == nil {
		return 0
	}
	if bitInt, ok := t.(*semantic.BitIntType); ok && bitInt.Signed {
		return 1
	}
	builtin, ok := t.(*semantic.BuiltinType)
	if !ok {
		return 0
	}
	switch builtin.Name {
	case "int", "i8", "i16", "i32", "i64", "isize":
		return 1
	default:
		return 0
	}
}

// recordStmt emits the per-statement record call. It runs after setLoc so the call
// inherits the current debug location when -g is also enabled.
func (t *traceState) recordStmt(state *functionState, stmt ast.Stmt) {
	if t == nil || state == nil || stmt == nil || state.builder == nil || state.traceNameGlobal == nil {
		return
	}
	// Declarations and assignments self-record (with their value) in their own handlers;
	// skip the generic step here so each such line produces exactly one entry.
	switch stmt.(type) {
	case *ast.VarDeclStmt, *ast.AssignStmt:
		return
	}
	t.recordStep(state, stmt.Pos().Line)
}

// recordStep emits a plain step record (function + line) -- the execution path with no
// captured value.
func (t *traceState) recordStep(state *functionState, line int) {
	if t == nil || state == nil || state.builder == nil || state.traceNameGlobal == nil {
		return
	}
	if line < 0 {
		line = 0
	}
	C.elisacoreBuildTraceCall(state.builder, t.g.context, t.recordFnTy, t.recordFn,
		state.traceNameGlobal, C.unsigned(line))
}
