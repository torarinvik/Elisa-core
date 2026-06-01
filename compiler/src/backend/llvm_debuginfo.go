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
*/
import "C"

import (
	"path/filepath"
	"unsafe"

	"elisacore/src/ast"
)

// debugInfo carries the DWARF emission state for a module. Phase 1 emits a compile
// unit, one DISubprogram per defined function, and per-statement line locations --
// enough for a native (attach-model) debugger to symbolize backtraces, set
// source-line breakpoints, and show where execution is. Local-variable DWARF is a
// later phase.
type debugInfo struct {
	g    *llvmGenerator
	dib  C.LLVMDIBuilderRef
	file C.LLVMMetadataRef
	cu   C.LLVMMetadataRef
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

	g.di = &debugInfo{g: g, dib: dib, file: file, cu: cu}
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
