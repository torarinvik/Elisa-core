//go:build cgo

package backend

/*
#cgo darwin,arm64 CFLAGS: -I/opt/homebrew/opt/llvm/include
#cgo darwin,arm64 LDFLAGS: -L/opt/homebrew/opt/llvm/lib -lLLVM-C -lLLVM
#cgo darwin,amd64 CFLAGS: -I/usr/local/opt/llvm/include
#cgo darwin,amd64 LDFLAGS: -L/usr/local/opt/llvm/lib -lLLVM-C -lLLVM
#cgo linux CFLAGS: -I/usr/include -I/usr/lib/llvm-21/include -I/usr/lib/llvm-20/include -I/usr/lib/llvm-19/include -I/usr/lib/llvm-18/include -I/usr/lib/llvm-17/include -I/usr/lib/llvm-16/include -I/usr/lib/llvm-15/include
#cgo linux LDFLAGS: -L/usr/lib/llvm-21/lib -L/usr/lib/llvm-20/lib -L/usr/lib/llvm-19/lib -L/usr/lib/llvm-18/lib -L/usr/lib/llvm-17/lib -L/usr/lib/llvm-16/lib -L/usr/lib/llvm-15/lib -lLLVM-C -lLLVM
#include <stdlib.h>
#include <llvm-c/Analysis.h>
#include <llvm-c/Core.h>
#include <llvm-c/Target.h>
#include <llvm-c/TargetMachine.h>
*/
import "C"

import (
	"fmt"
	"strings"
	"unsafe"

	"llcontext/src/ast"
	"llcontext/src/semantic"
)

// GenerateLLVMIR lowers the analyzed program into an LLVM module using the LLVM C API
// and returns the textual IR produced by LLVM itself.
func GenerateLLVMIR(result *semantic.Result) (string, error) {
	return GenerateLLVMIRWithOpt(result, OptimizationLevel0)
}

// GenerateLLVMIRWithOpt lowers the analyzed program and optionally optimizes the
// module before returning textual LLVM IR.
func GenerateLLVMIRWithOpt(result *semantic.Result, optLevel OptimizationLevel) (string, error) {
	return GenerateLLVMIRWithOptAndPackedLoweringProfile(result, optLevel, DefaultPackedLoweringProfile())
}

func GenerateLLVMIRWithOptAndPackedLoweringProfile(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile) (string, error) {
	g, err := compileLLVMModule(result, optLevel, profile)
	if err != nil {
		return "", err
	}
	defer g.dispose()
	if err := g.optimizeModule(optLevel); err != nil {
		return "", err
	}
	return g.printModule(), nil
}

func GenerateLLVMIRWithOptAndPackedABI(result *semantic.Result, optLevel OptimizationLevel, packedABI PackedEnumABI) (string, error) {
	profile, err := LegacyPackedLoweringProfile(packedABI)
	if err != nil {
		return "", err
	}
	return GenerateLLVMIRWithOptAndPackedLoweringProfile(result, optLevel, profile)
}

func compileLLVMModule(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile) (*llvmGenerator, error) {
	g, err := newLLVMGenerator(result)
	if err != nil {
		return nil, err
	}
	g.packedProfile = profile
	g.packedEnumABI = profile.packedModeForStore(nil)
	if g.result != nil {
		g.result.PackedLowering = profile.metadata()
	}
	g.preferPrivateLinkage = optLevel != OptimizationLevel0
	if err := g.emitModule(); err != nil {
		g.dispose()
		return nil, err
	}
	if err := g.verify(); err != nil {
		g.dispose()
		return nil, err
	}
	return g, nil
}

type llvmGenerator struct {
	result                    *semantic.Result
	context                   C.LLVMContextRef
	module                    C.LLVMModuleRef
	targetMachine             C.LLVMTargetMachineRef
	targetData                C.LLVMTargetDataRef
	targetTriple              *C.char
	optimizedForCodegen       bool
	preferPrivateLinkage      bool
	packedProfile             PackedLoweringProfile
	packedEnumABI             packedEnumABIMode
	symbolsByNode             map[ast.Node]*semantic.Symbol
	structTypes               map[string]C.LLVMTypeRef
	structBodies              map[string]bool
	functionTypes             map[*semantic.FuncType]C.LLVMTypeRef
	runtimeHelperTypes        map[string]*semantic.FuncType
	packedViewTypes           map[*semantic.EnumVariant]*semantic.PackedVariantViewType
	packedVariantPayloadTypes map[*semantic.EnumVariant]C.LLVMTypeRef
	commonFieldLayouts        map[packedEnumCommonFieldLayoutCacheKey]*packedEnumCommonFieldLayout
	specializedFuncTypes      map[string]*semantic.FuncType
	functions                 map[string]C.LLVMValueRef
	globals                   map[string]C.LLVMValueRef
	noteTypeInProgress        map[semantic.Type]bool
	noteTypeDone              map[semantic.Type]bool
	cachedVoidRefType         *semantic.RefType
	cachedArenaRefType        *semantic.RefType
	syntheticCounter          int
	wordBits                  int
}

func newLLVMGenerator(result *semantic.Result) (*llvmGenerator, error) {
	if result == nil || result.File == nil || result.GlobalScope == nil {
		return nil, fmt.Errorf("backend requires a semantic result with file and global scope")
	}
	ctx := C.LLVMContextCreate()
	moduleName := cString(result.File.Filename)
	defer C.free(unsafe.Pointer(moduleName))
	mod := C.LLVMModuleCreateWithNameInContext(moduleName, ctx)
	g := &llvmGenerator{
		result:                    result,
		context:                   ctx,
		module:                    mod,
		packedProfile:             DefaultPackedLoweringProfile(),
		packedEnumABI:             packedEnumABIVariantSparse,
		symbolsByNode:             map[ast.Node]*semantic.Symbol{},
		structTypes:               map[string]C.LLVMTypeRef{},
		structBodies:              map[string]bool{},
		functionTypes:             map[*semantic.FuncType]C.LLVMTypeRef{},
		runtimeHelperTypes:        map[string]*semantic.FuncType{},
		packedViewTypes:           map[*semantic.EnumVariant]*semantic.PackedVariantViewType{},
		packedVariantPayloadTypes: map[*semantic.EnumVariant]C.LLVMTypeRef{},
		commonFieldLayouts:        map[packedEnumCommonFieldLayoutCacheKey]*packedEnumCommonFieldLayout{},
		specializedFuncTypes:      map[string]*semantic.FuncType{},
		functions:                 map[string]C.LLVMValueRef{},
		globals:                   map[string]C.LLVMValueRef{},
		noteTypeInProgress:        map[semantic.Type]bool{},
		noteTypeDone:              map[semantic.Type]bool{},
		wordBits:                  int(unsafe.Sizeof(uintptr(0)) * 8),
	}
	for _, sym := range result.GlobalScope.Symbols {
		if sym == nil || sym.Node == nil {
			continue
		}
		g.symbolsByNode[sym.Node] = sym
	}
	return g, nil
}

func (g *llvmGenerator) packedModeForEnum(enumType *semantic.EnumType) packedEnumABIMode {
	if g == nil {
		return packedEnumABIRowHandle
	}
	return g.packedProfile.packedModeForPackedEnum(enumType)
}

func (g *llvmGenerator) packedLoweringForStore(storeType *semantic.PackedEnumStoreType) packedEnumABIMode {
	if g == nil {
		return packedEnumABIRowHandle
	}
	return g.packedProfile.packedModeForStore(storeType)
}

func (g *llvmGenerator) cachedRuntimeHelperType(name string, build func() *semantic.FuncType) *semantic.FuncType {
	if g == nil {
		return build()
	}
	if cached := g.runtimeHelperTypes[name]; cached != nil {
		return cached
	}
	helperType := build()
	g.runtimeHelperTypes[name] = helperType
	return helperType
}

func (g *llvmGenerator) usesCanonicalPackedLowering() bool {
	if g == nil {
		return false
	}
	return g.packedProfile.Contract() == PackedLoweringContractCanonicalCompilerGraph && !g.packedProfile.HasLegacyOverride()
}

func (g *llvmGenerator) nextSyntheticName(prefix string) string {
	name := fmt.Sprintf("%s%d", prefix, g.syntheticCounter)
	g.syntheticCounter++
	return name
}

func (g *llvmGenerator) dispose() {
	if g.targetData != nil {
		C.LLVMDisposeTargetData(g.targetData)
	}
	if g.targetMachine != nil {
		C.LLVMDisposeTargetMachine(g.targetMachine)
	}
	if g.targetTriple != nil {
		C.LLVMDisposeMessage(g.targetTriple)
	}
	if g.module != nil {
		C.LLVMDisposeModule(g.module)
	}
	if g.context != nil {
		C.LLVMContextDispose(g.context)
	}
}

func (g *llvmGenerator) emitModule() error {
	for _, decl := range g.result.File.Decls {
		if err := g.predeclareDeclTypes(decl); err != nil {
			return err
		}
	}
	for _, decl := range g.result.File.Decls {
		if err := g.emitDecl(decl); err != nil {
			return err
		}
	}
	for _, exported := range g.result.ExportedGlobals {
		if err := g.emitExportedGlobal(exported); err != nil {
			return err
		}
	}
	for _, exported := range g.result.ExportedFuncs {
		if err := g.emitExportedFunction(exported); err != nil {
			return err
		}
	}
	return nil
}

func (g *llvmGenerator) predeclareDeclTypes(decl ast.Decl) error {
	switch n := decl.(type) {
	case *ast.StructDecl:
		if _, err := g.ensureNamedStructType(n.Name); err != nil {
			return err
		}
		if len(n.GenericParams) == 0 {
			if st, ok := g.lookupStructType(n.Name); ok {
				_, err := g.ensureStructBody(n.Name, st)
				return err
			}
		}
	case *ast.EnumDecl:
		t, ok := g.result.NamedTypes[n.Name]
		if !ok {
			return fmt.Errorf("missing semantic enum type %s", n.Name)
		}
		enumType, ok := t.(*semantic.EnumType)
		if !ok {
			return fmt.Errorf("declaration %s does not resolve to enum type", n.Name)
		}
		if enumType.Packed {
			_, err := g.ensurePackedEnumStorageType(enumType)
			return err
		}
		_, err := g.ensureEnumBody(n.Name, enumType)
		return err
	case *ast.ExternTypeDecl:
		_, err := g.ensureNamedStructType(n.Name)
		return err
	case *ast.FuncDecl, *ast.ExternFuncDecl:
		sym, ok := g.symbolsByNode[decl]
		if !ok || sym.Type == nil {
			return nil
		}
		if err := g.noteType(sym.Type); err != nil {
			return err
		}
		if fnDecl, ok := decl.(*ast.FuncDecl); ok && len(fnDecl.GenericParams) > 0 {
			return nil
		}
		fn, ok := sym.Type.(*semantic.FuncType)
		if !ok {
			return fmt.Errorf("function %s does not resolve to semantic function type", sym.Name)
		}
		_, err := g.ensureFunctionDeclared(sym.Name, fn)
		return err
	case *ast.GlobalDecl, *ast.ExternVarDecl:
		sym, ok := g.symbolsByNode[decl]
		if !ok || sym.Type == nil {
			return nil
		}
		if err := g.noteType(sym.Type); err != nil {
			return err
		}
		_, err := g.ensureGlobalDeclared(sym.Name, sym.Type, declIsExternVar(decl))
		return err
	case *ast.ConstDecl, *ast.ConstEnumDecl, *ast.StaticIfDecl:
		return nil
	case *ast.PermissionDecl:
		return nil
	case *ast.ErrorDecl:
		return nil
	case *ast.ExportTypeDecl, *ast.ExportFuncDecl, *ast.ExportGlobalDecl:
		return nil
	default:
		return fmt.Errorf("unsupported declaration %T", decl)
	}
	return nil
}

func (g *llvmGenerator) emitDecl(decl ast.Decl) error {
	switch n := decl.(type) {
	case *ast.ConstDecl:
		return nil
	case *ast.ConstEnumDecl:
		return nil
	case *ast.StructDecl:
		if len(n.GenericParams) == 0 {
			if st, ok := g.lookupStructType(n.Name); ok {
				_, err := g.ensureStructBody(n.Name, st)
				return err
			}
		}
		return nil
	case *ast.EnumDecl:
		t, ok := g.result.NamedTypes[n.Name]
		if !ok {
			return fmt.Errorf("missing semantic enum type %s", n.Name)
		}
		enumType, ok := t.(*semantic.EnumType)
		if !ok {
			return fmt.Errorf("declaration %s does not resolve to enum type", n.Name)
		}
		if enumType.Packed {
			_, err := g.ensurePackedEnumStorageType(enumType)
			return err
		}
		_, err := g.ensureEnumBody(n.Name, enumType)
		return err
	case *ast.FuncDecl:
		if len(n.GenericParams) > 0 {
			return nil
		}
		sym, ok := g.symbolsByNode[decl]
		if !ok {
			return fmt.Errorf("missing semantic symbol for function declaration")
		}
		fn, ok := sym.Type.(*semantic.FuncType)
		if !ok {
			return fmt.Errorf("function %s does not resolve to semantic function type", sym.Name)
		}
		fnValue, err := g.ensureFunctionDeclared(sym.Name, fn)
		if err != nil {
			return err
		}
		g.setDefinedFunctionLinkage(sym.Name, fnValue, fn)
		return g.defineFunctionBody(n, fn, fnValue)
	case *ast.ExternFuncDecl:
		return nil
	case *ast.GlobalDecl, *ast.ExternVarDecl:
		sym, ok := g.symbolsByNode[decl]
		if !ok {
			return fmt.Errorf("missing semantic symbol for global declaration")
		}
		globalValue, err := g.ensureGlobalDeclared(sym.Name, sym.Type, declIsExternVar(decl))
		if err != nil {
			return err
		}
		if globalDecl, ok := decl.(*ast.GlobalDecl); ok {
			return g.defineGlobal(globalDecl, sym.Type, globalValue)
		}
		return nil
	case *ast.ExternTypeDecl, *ast.StaticIfDecl:
		return nil
	case *ast.PermissionDecl:
		return nil
	case *ast.ErrorDecl:
		return nil
	case *ast.ExportTypeDecl, *ast.ExportFuncDecl, *ast.ExportGlobalDecl:
		return nil
	default:
		return fmt.Errorf("unsupported declaration %T", decl)
	}
}

func (g *llvmGenerator) verify() error {
	var message *C.char
	status := C.LLVMVerifyModule(g.module, C.LLVMReturnStatusAction, &message)
	if status == 0 {
		if message != nil {
			C.LLVMDisposeMessage(message)
		}
		return nil
	}
	errText := "LLVM module verification failed"
	if message != nil {
		errText = strings.TrimSpace(C.GoString(message))
		C.LLVMDisposeMessage(message)
	}
	return fmt.Errorf("%s", errText)
}

func (g *llvmGenerator) printModule() string {
	text := C.LLVMPrintModuleToString(g.module)
	defer C.LLVMDisposeMessage(text)
	return C.GoString(text)
}
