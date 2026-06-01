//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
#include <llvm-c/DebugInfo.h>

static LLVMDIBuilderRef elisacoreCreateDIBuilder(LLVMModuleRef m) {
	return LLVMCreateDIBuilder(m);
}

static LLVMMetadataRef elisacoreDIFile(LLVMDIBuilderRef dib, const char *name, size_t nameLen, const char *dir, size_t dirLen) {
	return LLVMDIBuilderCreateFile(dib, name, nameLen, dir, dirLen);
}

// Args: isOptimized=0, flags="", runtimeVer=0, splitName="", emissionFull,
// dwoId=0, splitDebugInlining=0, debugInfoForProfiling=0, sysRoot="", sdk="".
static LLVMMetadataRef elisacoreDICompileUnit(LLVMDIBuilderRef dib, LLVMMetadataRef file, const char *producer, size_t producerLen) {
	return LLVMDIBuilderCreateCompileUnit(
		dib, LLVMDWARFSourceLanguageC99, file, producer, producerLen,
		0, "", 0,
		0, "", 0,
		LLVMDWARFEmissionFull, 0, 0,
		0, "", 0, "", 0);
}

// CreateFunction args: isLocalToUnit=0, isDefinition=1, scopeLine=line, flags=0, isOptimized=0.
static LLVMMetadataRef elisacoreDIFunction(LLVMDIBuilderRef dib, LLVMMetadataRef scope, LLVMMetadataRef file,
		const char *name, size_t nameLen, const char *linkage, size_t linkageLen, unsigned line) {
	LLVMMetadataRef subType = LLVMDIBuilderCreateSubroutineType(dib, file, NULL, 0, 0);
	return LLVMDIBuilderCreateFunction(dib, scope, name, nameLen, linkage, linkageLen, file, line, subType,
		0, 1, line, 0, 0);
}

static void elisacoreSetSubprogram(LLVMValueRef fn, LLVMMetadataRef sp) {
	LLVMSetSubprogram(fn, sp);
}

static void elisacoreSetDebugLoc(LLVMContextRef ctx, LLVMBuilderRef b, unsigned line, unsigned col, LLVMMetadataRef scope) {
	if (scope == NULL) {
		return;
	}
	LLVMMetadataRef loc = LLVMDIBuilderCreateDebugLocation(ctx, line, col, scope, NULL);
	LLVMSetCurrentDebugLocation2(b, loc);
}

static void elisacoreClearDebugLoc(LLVMBuilderRef b) {
	LLVMSetCurrentDebugLocation2(b, NULL);
}

static void elisacoreFinalizeDIBuilder(LLVMDIBuilderRef dib) {
	LLVMDIBuilderFinalize(dib);
}

static void elisacoreDisposeDIBuilder(LLVMDIBuilderRef dib) {
	LLVMDisposeDIBuilder(dib);
}

static void elisacoreAddModuleFlagU32(LLVMModuleRef m, const char *key, size_t keyLen, unsigned val) {
	LLVMMetadataRef mv = LLVMValueAsMetadata(LLVMConstInt(LLVMInt32TypeInContext(LLVMGetModuleContext(m)), val, 0));
	LLVMAddModuleFlag(m, LLVMModuleFlagBehaviorWarning, key, keyLen, mv);
}

static LLVMMetadataRef elisacoreDIBasicType(LLVMDIBuilderRef dib, const char *name, size_t nameLen, uint64_t bits, unsigned encoding) {
	return LLVMDIBuilderCreateBasicType(dib, name, nameLen, bits, (LLVMDWARFTypeEncoding)encoding, 0);
}

// A pointer-sized (64-bit) DWARF pointer to the given pointee (NULL pointee => void*).
static LLVMMetadataRef elisacoreDIPointerType(LLVMDIBuilderRef dib, LLVMMetadataRef pointee) {
	return LLVMDIBuilderCreatePointerType(dib, pointee, 64, 64, 0, "", 0);
}

// AlwaysPreserve=1 so the variable survives even when unused.
static LLVMMetadataRef elisacoreDIParamVar(LLVMDIBuilderRef dib, LLVMMetadataRef scope, const char *name, size_t nameLen,
		unsigned argNo, LLVMMetadataRef file, unsigned line, LLVMMetadataRef ty) {
	return LLVMDIBuilderCreateParameterVariable(dib, scope, name, nameLen, argNo, file, line, ty, 1, 0);
}

static LLVMMetadataRef elisacoreDIAutoVar(LLVMDIBuilderRef dib, LLVMMetadataRef scope, const char *name, size_t nameLen,
		LLVMMetadataRef file, unsigned line, LLVMMetadataRef ty) {
	return LLVMDIBuilderCreateAutoVariable(dib, scope, name, nameLen, file, line, ty, 1, 0, 0);
}

// Associate a variable's storage (alloca / byval pointer) with its debug-info entry,
// inserting the dbg record at the end of the given block.
static void elisacoreDIInsertDeclare(LLVMDIBuilderRef dib, LLVMContextRef ctx, LLVMValueRef storage,
		LLVMMetadataRef varInfo, unsigned line, unsigned col, LLVMMetadataRef scope, LLVMBasicBlockRef block) {
	if (storage == NULL || varInfo == NULL || block == NULL) {
		return;
	}
	LLVMMetadataRef expr = LLVMDIBuilderCreateExpression(dib, NULL, 0);
	LLVMMetadataRef loc = LLVMDIBuilderCreateDebugLocation(ctx, line, col, scope, NULL);
	LLVMDIBuilderInsertDeclareRecordAtEnd(dib, storage, varInfo, expr, loc, block);
}
*/
import "C"

import (
	"path/filepath"
	"unsafe"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// DWARF DW_ATE_* base-type encodings.
const (
	dwarfATEboolean  = 2
	dwarfATEfloat    = 4
	dwarfATEsigned   = 5
	dwarfATEunsigned = 7
)

// debugInfo carries the DWARF emission state for a module. Phase 1 emits a compile
// unit, one DISubprogram per defined function, and per-statement line locations --
// enough for a native (attach-model) debugger to symbolize backtraces, set
// source-line breakpoints, and show where execution is. Local-variable DWARF is a
// later phase.
type debugInfo struct {
	g         *llvmGenerator
	dib       C.LLVMDIBuilderRef
	file      C.LLVMMetadataRef
	cu        C.LLVMMetadataRef
	typeCache map[typeMemoKey]C.LLVMMetadataRef
	voidPtr   C.LLVMMetadataRef
}

func (g *llvmGenerator) initDebugInfo() {
	if g == nil || g.module == nil || g.di != nil {
		return
	}
	activeFile := g.result.ActiveFile()
	if activeFile == nil {
		return
	}
	// The DWARF "Debug Info Version" module flag must match LLVM's metadata version
	// or the verifier strips all debug info. Pin the Dwarf version too.
	keyDIV := C.CString("Debug Info Version")
	C.elisacoreAddModuleFlagU32(g.module, keyDIV, C.size_t(len("Debug Info Version")), 3)
	C.free(unsafe.Pointer(keyDIV))
	keyDW := C.CString("Dwarf Version")
	C.elisacoreAddModuleFlagU32(g.module, keyDW, C.size_t(len("Dwarf Version")), 4)
	C.free(unsafe.Pointer(keyDW))

	dib := C.elisacoreCreateDIBuilder(g.module)

	fileName := filepath.Base(activeFile.Filename)
	dirName := filepath.Dir(activeFile.Filename)
	if dirName == "" {
		dirName = "."
	}
	nameC := C.CString(fileName)
	dirC := C.CString(dirName)
	file := C.elisacoreDIFile(dib, nameC, C.size_t(len(fileName)), dirC, C.size_t(len(dirName)))
	C.free(unsafe.Pointer(nameC))
	C.free(unsafe.Pointer(dirC))

	producer := "elisacore"
	prodC := C.CString(producer)
	cu := C.elisacoreDICompileUnit(dib, file, prodC, C.size_t(len(producer)))
	C.free(unsafe.Pointer(prodC))

	g.di = &debugInfo{g: g, dib: dib, file: file, cu: cu, typeCache: map[typeMemoKey]C.LLVMMetadataRef{}}
}

// diType returns (and caches) the DWARF type for an Elisa type. Phase 2 emits precise
// base types for numerics/bools and pointer types for references/optionals/pointer-like
// values; structs and other aggregates are rendered as an opaque blob of the right size
// for now (full DICompositeType is a later refinement). Feeds both lldb inspection and
// the upcoming state-trace recorder.
func (d *debugInfo) diType(t semantic.Type) C.LLVMMetadataRef {
	if d == nil || d.dib == nil {
		return nil
	}
	if t == nil {
		return d.voidPointer()
	}
	key := noteTypeKeyFor(t)
	if v, ok := d.typeCache[key]; ok {
		return v
	}
	result := d.buildDIType(t)
	d.typeCache[key] = result
	return result
}

func (d *debugInfo) voidPointer() C.LLVMMetadataRef {
	if d.voidPtr == nil {
		d.voidPtr = C.elisacoreDIPointerType(d.dib, nil)
	}
	return d.voidPtr
}

func (d *debugInfo) basicType(name string, bits uint64, encoding C.unsigned) C.LLVMMetadataRef {
	nameC := C.CString(name)
	defer C.free(unsafe.Pointer(nameC))
	return C.elisacoreDIBasicType(d.dib, nameC, C.size_t(len(name)), C.uint64_t(bits), encoding)
}

func (d *debugInfo) buildDIType(t semantic.Type) C.LLVMMetadataRef {
	g := d.g
	name := debugTypeName(t)

	if semantic.IsBoolType(t) {
		return d.basicType("bool", 8, dwarfATEboolean)
	}
	if isNumericType(t) {
		sizeBytes, err := g.abiSizeOfType(t)
		if err != nil || sizeBytes == 0 {
			sizeBytes = uint64(g.wordBits / 8)
		}
		enc := C.unsigned(dwarfATEunsigned)
		switch {
		case isFloatType(t):
			enc = dwarfATEfloat
		case isSignedIntegerType(t):
			enc = dwarfATEsigned
		}
		return d.basicType(name, sizeBytes*8, enc)
	}
	if refT, ok := t.(*semantic.RefType); ok && refT != nil {
		return C.elisacoreDIPointerType(d.dib, d.diType(refT.Elem))
	}
	if optT, ok := t.(*semantic.OptionalType); ok && optT != nil {
		// Optionals are pointer-or-null at the ABI level; point at the payload type.
		return C.elisacoreDIPointerType(d.dib, d.diType(optT.Value))
	}
	if isPointerLikeType(t) {
		return d.voidPointer()
	}
	// Structs, arrays and other aggregates: an opaque blob sized to the type. Scalars
	// (the values that usually matter for "what produced this") are already precise.
	sizeBytes, err := g.abiSizeOfType(t)
	if err != nil || sizeBytes == 0 {
		return d.voidPointer()
	}
	return d.basicType(name, sizeBytes*8, dwarfATEunsigned)
}

func debugTypeName(t semantic.Type) string {
	if b, ok := t.(*semantic.BuiltinType); ok && b != nil {
		return b.Name
	}
	if t == nil {
		return "void"
	}
	s := t.String()
	if s == "" {
		return "anon"
	}
	return s
}

// declareVariable associates a source variable's storage with a DWARF local/parameter
// variable so the debugger can read it by name. argNo > 0 marks a parameter (1-based).
func (d *debugInfo) declareVariable(state *functionState, name string, storage C.LLVMValueRef, typ semantic.Type, line, argNo int) {
	if d == nil || d.dib == nil || state == nil || state.diScope == nil || storage == nil {
		return
	}
	if name == "" || name == "_" {
		return
	}
	if line <= 0 {
		line = 1
	}
	dty := d.diType(typ)
	if dty == nil {
		return
	}
	nameC := C.CString(name)
	defer C.free(unsafe.Pointer(nameC))
	var diVar C.LLVMMetadataRef
	if argNo > 0 {
		diVar = C.elisacoreDIParamVar(d.dib, state.diScope, nameC, C.size_t(len(name)), C.unsigned(argNo), d.file, C.unsigned(line), dty)
	} else {
		diVar = C.elisacoreDIAutoVar(d.dib, state.diScope, nameC, C.size_t(len(name)), d.file, C.unsigned(line), dty)
	}
	block := C.LLVMGetInsertBlock(state.builder)
	C.elisacoreDIInsertDeclare(d.dib, d.g.context, storage, diVar, C.unsigned(line), 1, state.diScope, block)
}

// attachFunction creates a DISubprogram for a defined function, attaches it to the
// LLVM function, records it as the active scope on the functionState, and seeds the
// builder with an entry debug location (so every instruction has a !dbg, which the
// verifier requires once a function carries debug info).
func (d *debugInfo) attachFunction(state *functionState, decl *ast.FuncDecl, fnValue C.LLVMValueRef) {
	if d == nil || state == nil || decl == nil || fnValue == nil {
		return
	}
	line := decl.Pos().Line
	if line <= 0 {
		line = 1
	}
	name := decl.Name
	nameC := C.CString(name)
	// Use the LLVM symbol name as the linkage name so the debugger ties source to symbol.
	linkage := C.GoString(C.LLVMGetValueName(fnValue))
	linkageC := C.CString(linkage)
	sp := C.elisacoreDIFunction(d.dib, d.cu, d.file, nameC, C.size_t(len(name)), linkageC, C.size_t(len(linkage)), C.unsigned(line))
	C.free(unsafe.Pointer(nameC))
	C.free(unsafe.Pointer(linkageC))

	C.elisacoreSetSubprogram(fnValue, sp)
	state.diScope = sp
	C.elisacoreSetDebugLoc(d.g.context, state.builder, C.unsigned(line), 1, sp)
}

// setLoc points the builder's current debug location at the given AST node's line
// within the active function scope. Subsequent instructions inherit it.
func (d *debugInfo) setLoc(state *functionState, node ast.Node) {
	if d == nil || state == nil || state.diScope == nil || node == nil {
		return
	}
	pos := node.Pos()
	line := pos.Line
	if line <= 0 {
		line = 1
	}
	col := pos.Col
	if col <= 0 {
		col = 1
	}
	C.elisacoreSetDebugLoc(d.g.context, state.builder, C.unsigned(line), C.unsigned(col), state.diScope)
}

func (d *debugInfo) finalize() {
	if d == nil || d.dib == nil {
		return
	}
	C.elisacoreFinalizeDIBuilder(d.dib)
}

func (d *debugInfo) dispose() {
	if d == nil || d.dib == nil {
		return
	}
	C.elisacoreDisposeDIBuilder(d.dib)
	d.dib = nil
}
