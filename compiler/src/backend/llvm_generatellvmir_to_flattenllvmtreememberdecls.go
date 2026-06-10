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
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"os"
	"unsafe"
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
	return GenerateLLVMIRWithOptAndPackedLoweringProfileForTarget(result, optLevel, profile, "")
}

func GenerateLLVMIRWithOptAndPackedLoweringProfileForTarget(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile, targetTriple string) (string, error) {
	return GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebug(result, optLevel, profile, targetTriple, false)
}

// GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebug is like the non-Debug
// variant but emits DWARF debug-info metadata into the IR when debugInfo is true, so
// the textual-IR -> llc native build path produces a symbolized (attachable) binary.
func GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebug(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile, targetTriple string, debugInfo bool) (string, error) {
	return GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebugTrace(result, optLevel, profile, targetTriple, debugInfo, false)
}

// GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebugTrace additionally emits
// -ftrace instrumentation when traceInfo is true, so the native build records the
// execution trace.
func GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebugTrace(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile, targetTriple string, debugInfo bool, traceInfo bool) (string, error) {
	output, _, err := GenerateLLVMIRWithWarnings(result, optLevel, profile, targetTriple, debugInfo, traceInfo)
	return output, err
}

// GenerateLLVMIRWithWarnings is the full-arg lowering entry point that, in addition to the IR text,
// returns any performance-friction warnings collected during optimized codegen (the
// auto-vectorization verifier — docs/79 Part IV). The slice is empty at -O0 or when every eligible
// loop vectorized. Build/emit drivers surface these to the user; the thinner overloads that don't
// thread warnings out delegate here and discard them.
func GenerateLLVMIRWithWarnings(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile, targetTriple string, debugInfo bool, traceInfo bool) (string, []string, error) {
	g, err := compileLLVMModuleWithTargetDebugTrace(result, optLevel, profile, targetTriple, debugInfo, traceInfo)
	if err != nil {
		return "", nil, err
	}
	defer g.dispose()
	if err := g.optimizeModule(optLevel); err != nil {
		return "", nil, err
	}
	output := g.printModule()
	if err := validateSegmentAgnosticLLVMIR(output); err != nil {
		return "", nil, err
	}
	return output, g.PerfWarnings(), nil
}
func compileLLVMModule(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile) (*llvmGenerator, error) {
	return compileLLVMModuleWithTarget(result, optLevel, profile, "")
}

func compileLLVMModuleWithTarget(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile, targetTriple string) (*llvmGenerator, error) {
	return compileLLVMModuleWithTargetAndDebug(result, optLevel, profile, targetTriple, false)
}

func compileLLVMModuleWithTargetAndDebug(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile, targetTriple string, debugInfo bool) (*llvmGenerator, error) {
	return compileLLVMModuleWithTargetDebugTrace(result, optLevel, profile, targetTriple, debugInfo, false)
}

func compileLLVMModuleWithTargetDebugTrace(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile, targetTriple string, debugInfo bool, traceInfo bool) (*llvmGenerator, error) {
	g, err := newLLVMGenerator(result)
	if err != nil {
		return nil, err
	}
	g.emitDebugInfo = debugInfo
	g.emitTrace = traceInfo
	// ELISACORE_FORCE_BOUNDS_CHECK forces the debug index-bounds watchdog on even in
	// optimized builds (it is otherwise -O0 only). Set by the `-fbounds-check` CLI flag.
	// Lets release/cross builds (e.g. the CUSA07399 emulator) trap at the offending
	// indexing site instead of crashing later on a derived bad pointer.
	g.forceBoundsCheck = os.Getenv("ELISACORE_FORCE_BOUNDS_CHECK") != ""
	// ELISACORE_FAST_MATH opts the whole program into full fast-math FP (reassociation,
	// reciprocals, no-nan/no-inf) — clang's -ffast-math. Set by the `-ffast-math` CLI flag.
	// This is what unlocks the *tree* (reassociated) horizontal reduction for FP folds, which
	// changes numerical results, so it stays an explicit program-wide opt-in (the per-function
	// `@fast_math` annotation and the per-fold `by simd` marker are the narrower opt-ins).
	g.globalFastMath = os.Getenv("ELISACORE_FAST_MATH") != ""
	g.optLevel = optLevel
	g.packedProfile = profile
	g.packedEnumABI = profile.packedModeForStore(nil)
	g.requestedTargetTriple = targetTriple
	if g.result != nil {
		g.result.PackedLowering = profile.metadata()
	}
	g.preferPrivateLinkage = optLevel != OptimizationLevel0
	if err := g.emitModule(); err != nil {
		g.dispose()
		return nil, err
	}
	g.appendAppleX64SegmentReservations(targetTriple)
	if err := g.verify(); err != nil {
		g.dispose()
		return nil, err
	}
	return g, nil
}

type llvmGenerator struct {
	result                    *semantic.Result
	optLevel                  OptimizationLevel
	context                   C.LLVMContextRef
	module                    C.LLVMModuleRef
	targetMachine             C.LLVMTargetMachineRef
	targetData                C.LLVMTargetDataRef
	targetTriple              *C.char
	targetTripleOwnedByLLVM   bool
	requestedTargetTriple     string
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
	treeAttributeHelpers      map[*semantic.TreeAttribute]*treeAttributeHelperInfo
	functions                 map[string]C.LLVMValueRef
	globals                   map[string]C.LLVMValueRef
	constEvalScopes           []map[string]semantic.ConstValue
	staticCallDepth           int
	noteTypeInProgress        map[typeMemoKey]bool
	noteTypeDone              map[typeMemoKey]bool
	cachedVoidRefType         *semantic.RefType
	cachedArenaRefType        *semantic.RefType
	syntheticCounter          int
	wordBits                  int
	emitDebugInfo             bool
	di                        *debugInfo
	emitTrace                 bool
	trace                     *traceState
	forceBoundsCheck          bool
	globalFastMath            bool
	perfWarnings              []string
}
type typeMemoKey struct {
	id  semantic.TypeID
	typ semantic.Type
}

func noteTypeKeyFor(t semantic.Type) typeMemoKey {
	if id, ok := semantic.TryCanonicalTypeID(t); ok {
		return typeMemoKey{id: id}
	}
	return typeMemoKey{typ: t}
}
func newLLVMGenerator(result *semantic.Result) (*llvmGenerator, error) {
	activeFile := result.ActiveFile()
	if result == nil || activeFile == nil || result.GlobalScope == nil {
		return nil, fmt.Errorf("backend requires a semantic result with file and global scope")
	}
	ctx := C.LLVMContextCreate()
	moduleName := cString(activeFile.Filename)
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
		treeAttributeHelpers:      map[*semantic.TreeAttribute]*treeAttributeHelperInfo{},
		functions:                 map[string]C.LLVMValueRef{},
		globals:                   map[string]C.LLVMValueRef{},
		noteTypeInProgress:        map[typeMemoKey]bool{},
		noteTypeDone:              map[typeMemoKey]bool{},
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
		return packedEnumABIVariantSparse
	}
	return g.packedProfile.packedModeForPackedEnum(enumType)
}
func (g *llvmGenerator) packedLoweringForStore(storeType *semantic.PackedEnumStoreType) packedEnumABIMode {
	if g == nil {
		return packedEnumABIVariantSparse
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
	return !g.packedProfile.hasExplicitMode
}
func (g *llvmGenerator) nextSyntheticName(prefix string) string {
	name := fmt.Sprintf("%s%d", prefix, g.syntheticCounter)
	g.syntheticCounter++
	return name
}
func (g *llvmGenerator) dispose() {
	if g.di != nil {
		g.di.dispose()
		g.di = nil
	}
	if g.targetData != nil {
		C.LLVMDisposeTargetData(g.targetData)
	}
	if g.targetMachine != nil {
		C.LLVMDisposeTargetMachine(g.targetMachine)
	}
	if g.targetTriple != nil {
		if g.targetTripleOwnedByLLVM {
			C.LLVMDisposeMessage(g.targetTriple)
		} else {
			C.free(unsafe.Pointer(g.targetTriple))
		}
	}
	if g.module != nil {
		C.LLVMDisposeModule(g.module)
	}
	if g.context != nil {
		C.LLVMContextDispose(g.context)
	}
}
func (g *llvmGenerator) emitModule() error {
	if g.emitDebugInfo {
		g.initDebugInfo()
	}
	if g.emitTrace {
		g.initTrace()
	}
	for _, decl := range g.result.ActiveFile().Decls {
		if err := g.predeclareDeclTypes(decl); err != nil {
			return err
		}
	}
	for _, module := range g.result.EASMModules {
		if err := g.emitEASMModule(module); err != nil {
			return err
		}
	}
	for _, decl := range g.result.ActiveFile().Decls {
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
	if g.di != nil {
		g.di.finalize()
	}
	return nil
}

func (g *llvmGenerator) activeDeclBranch(decl *ast.StaticIfDecl) ([]ast.Decl, error) {
	selected, ok := g.evalConstBoolExpr(decl.Cond)
	if !ok {
		return nil, fmt.Errorf("static if condition must be a compile-time bool")
	}
	if selected {
		return decl.Then, nil
	}
	for _, elif := range decl.Elifs {
		selected, ok := g.evalConstBoolExpr(elif.Cond)
		if !ok {
			return nil, fmt.Errorf("static elif condition must be a compile-time bool")
		}
		if selected {
			return elif.Body, nil
		}
	}
	return decl.Else, nil
}

func llvmQualifiedDeclName(namespace string, name string) string {
	if namespace == "" || name == "" {
		return name
	}
	return namespace + "." + name
}

func (g *llvmGenerator) predeclareDeclTypes(decl ast.Decl) error {
	return g.predeclareDeclTypesInNamespace(decl, "")
}

func (g *llvmGenerator) predeclareDeclTypesInNamespace(decl ast.Decl, namespace string) error {
	switch n := decl.(type) {
	case *ast.StructDecl:
		qualifiedName := llvmQualifiedDeclName(namespace, n.Name)
		if _, err := g.ensureNamedStructType(qualifiedName); err != nil {
			return err
		}
		if len(n.GenericParams) == 0 {
			if st, ok := g.lookupStructType(qualifiedName); ok {
				if _, err := g.ensureStructBody(qualifiedName, st); err != nil {
					return err
				}
				return g.checkAbiLayoutAnnotations(n, st)
			}
		}
	case *ast.EnumDecl:
		qualifiedName := llvmQualifiedDeclName(namespace, n.Name)
		t, ok := g.result.NamedTypes[qualifiedName]
		if !ok {
			return fmt.Errorf("missing semantic enum type %s", qualifiedName)
		}
		enumType, ok := t.(*semantic.EnumType)
		if !ok {
			return fmt.Errorf("declaration %s does not resolve to enum type", qualifiedName)
		}
		if enumType.Packed {
			_, err := g.ensurePackedEnumStorageType(enumType)
			return err
		}
		_, err := g.ensureEnumBody(qualifiedName, enumType)
		return err
	case *ast.TreeDecl:
		return g.predeclareTreeDeclTypes(n, llvmQualifiedDeclName(namespace, n.Name))
	case *ast.ExternTypeDecl:
		_, err := g.ensureNamedStructType(llvmQualifiedDeclName(namespace, n.Name))
		return err
	case *ast.FuncDecl, *ast.ExternFuncDecl:
		if fnDecl, ok := decl.(*ast.FuncDecl); ok && fnDecl.Static {
			return nil
		}
		if fnDecl, ok := decl.(*ast.FuncDecl); ok && len(fnDecl.GenericParams) > 0 {
			return nil
		}
		if externDecl, ok := decl.(*ast.ExternFuncDecl); ok && len(externDecl.GenericParams) > 0 {
			return nil
		}
		sym, ok := g.symbolsByNode[decl]
		if !ok || sym.Type == nil {
			return nil
		}
		if err := g.noteType(sym.Type); err != nil {
			return err
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
	case *ast.InterfaceDecl:
		return nil
	case *ast.ImplDecl:
		if len(n.GenericParams) > 0 {
			// Parametric impl: its methods reference the impl's type params and are
			// monomorphized per concrete receiver at the call site, not emitted standalone.
			return nil
		}
		for _, member := range n.Members {
			switch fnDecl := member.(type) {
			case *ast.FuncDecl, *ast.ExternFuncDecl:
				if methodDecl, ok := member.(*ast.FuncDecl); ok && len(methodDecl.GenericParams) > 0 {
					continue
				}
				if externDecl, ok := member.(*ast.ExternFuncDecl); ok && len(externDecl.GenericParams) > 0 {
					continue
				}
				sym, ok := g.symbolsByNode[fnDecl]
				if !ok || sym.Type == nil {
					continue
				}
				if err := g.noteType(sym.Type); err != nil {
					return err
				}
				fn, ok := sym.Type.(*semantic.FuncType)
				if !ok {
					return fmt.Errorf("impl method %s does not resolve to semantic function type", sym.Name)
				}
				if _, err := g.ensureFunctionDeclared(sym.Name, fn); err != nil {
					return err
				}
			}
		}
		return nil
	case *ast.StaticIfDecl:
		branch, err := g.activeDeclBranch(n)
		if err != nil {
			return err
		}
		for _, inner := range branch {
			if err := g.predeclareDeclTypes(inner); err != nil {
				return err
			}
		}
		return nil
	case *ast.ConstDecl, *ast.TokenSetDecl, *ast.CharsetDecl, *ast.KeywordMapDecl, *ast.ConstEnumDecl, *ast.AttributeDecl:
		return nil
	case *ast.StaticAssertDecl:
		return g.checkStaticAssertDecl(n)
	case *ast.StaticAssertBlockDecl:
		return g.checkStaticAssertBlockDecl(n)
	case *ast.PermissionDecl:
		return nil
	case *ast.ParamsDecl:
		return nil
	case *ast.ContextDecl:
		return nil
	case *ast.NamespaceDecl:
		childNamespace := llvmQualifiedDeclName(namespace, n.Name)
		for _, child := range n.Decls {
			if err := g.predeclareDeclTypesInNamespace(child, childNamespace); err != nil {
				return err
			}
		}
		return nil
	case *ast.StoreDecl:
		qualifiedName := llvmQualifiedDeclName(namespace, n.Name)
		if st, ok := g.lookupStructType(qualifiedName); ok {
			_, err := g.ensureStructBody(qualifiedName, st)
			return err
		}
		return nil
	case *ast.ErrorDecl:
		return nil
	case *ast.GrammarDecl:
		return nil
	case *ast.GrammarEnvDecl:
		return nil
	case *ast.TypeAliasDecl:
		return nil
	case *ast.GrantAliasDecl:
		return nil
	case *ast.UsingDecl, *ast.ImportDecl:
		return nil
	case *ast.ExportTypeDecl, *ast.ExportFuncDecl, *ast.ExportGlobalDecl:
		return nil
	default:
		return fmt.Errorf("unsupported declaration %T", decl)
	}
	return nil
}
func (g *llvmGenerator) emitDecl(decl ast.Decl) error {
	return g.emitDeclInNamespace(decl, "")
}

func (g *llvmGenerator) emitDeclInNamespace(decl ast.Decl, namespace string) error {
	switch n := decl.(type) {
	case *ast.ConstDecl:
		// Scalar consts are folded inline at use sites, but aggregate (array)
		// consts cannot be inlined at a dynamic-index use site. Emit those as
		// private read-only LLVM globals so `CONST_ARRAY[i]` lowers to a GEP.
		if sym, ok := g.symbolsByNode[decl]; ok && n.Value != nil {
			if _, isArray := sym.Type.(*semantic.ArrayType); isArray {
				globalValue, err := g.ensureGlobalDeclared(sym.Name, sym.Type, false)
				if err != nil {
					return err
				}
				initializer, err := g.constExprValueInNamespace(n.Value, sym.Type, llvmNamespaceFromName(sym.Name))
				if err != nil {
					return err
				}
				C.LLVMSetInitializer(globalValue, initializer)
				C.LLVMSetGlobalConstant(globalValue, 1)
				C.LLVMSetLinkage(globalValue, C.LLVMInternalLinkage)
			}
		}
		return nil
	case *ast.TokenSetDecl:
		return nil
	case *ast.CharsetDecl:
		return nil
	case *ast.KeywordMapDecl:
		return nil
	case *ast.ConstEnumDecl:
		return nil
	case *ast.StructDecl:
		if len(n.GenericParams) == 0 {
			qualifiedName := llvmQualifiedDeclName(namespace, n.Name)
			if st, ok := g.lookupStructType(qualifiedName); ok {
				if _, err := g.ensureStructBody(qualifiedName, st); err != nil {
					return err
				}
				return g.checkAbiLayoutAnnotations(n, st)
			}
		}
		return nil
	case *ast.EnumDecl:
		qualifiedName := llvmQualifiedDeclName(namespace, n.Name)
		t, ok := g.result.NamedTypes[qualifiedName]
		if !ok {
			return fmt.Errorf("missing semantic enum type %s", qualifiedName)
		}
		enumType, ok := t.(*semantic.EnumType)
		if !ok {
			return fmt.Errorf("declaration %s does not resolve to enum type", qualifiedName)
		}
		if enumType.Packed {
			_, err := g.ensurePackedEnumStorageType(enumType)
			return err
		}
		_, err := g.ensureEnumBody(qualifiedName, enumType)
		return err
	case *ast.TreeDecl:
		return g.emitTreeDecl(n, llvmQualifiedDeclName(namespace, n.Name))
	case *ast.FuncDecl:
		if n.Static {
			return nil
		}
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
			return g.defineGlobal(sym.Name, globalDecl, sym.Type, globalValue)
		}
		return nil
	case *ast.InterfaceDecl:
		return nil
	case *ast.ImplDecl:
		if len(n.GenericParams) > 0 {
			// Parametric impl: methods are monomorphized at the call site (see
			// resolveStaticInterfaceMethod), so skip standalone body emission with unbound T.
			return nil
		}
		for _, member := range n.Members {
			switch fnDecl := member.(type) {
			case *ast.FuncDecl:
				if len(fnDecl.GenericParams) > 0 {
					continue
				}
				sym, ok := g.symbolsByNode[fnDecl]
				if !ok {
					return fmt.Errorf("missing semantic symbol for impl method declaration")
				}
				fn, ok := sym.Type.(*semantic.FuncType)
				if !ok {
					return fmt.Errorf("impl method %s does not resolve to semantic function type", sym.Name)
				}
				fnValue, err := g.ensureFunctionDeclared(sym.Name, fn)
				if err != nil {
					return err
				}
				g.setDefinedFunctionLinkage(sym.Name, fnValue, fn)
				if err := g.defineFunctionBody(fnDecl, fn, fnValue); err != nil {
					return err
				}
			case *ast.ExternFuncDecl:
				continue
			}
		}
		return nil
	case *ast.StaticIfDecl:
		branch, err := g.activeDeclBranch(n)
		if err != nil {
			return err
		}
		for _, inner := range branch {
			if err := g.emitDecl(inner); err != nil {
				return err
			}
		}
		return nil
	case *ast.ExternTypeDecl, *ast.AttributeDecl, *ast.StaticAssertDecl, *ast.StaticAssertBlockDecl, *ast.StaticGenerateDecl:
		return nil
	case *ast.PermissionDecl:
		return nil
	case *ast.ParamsDecl:
		return nil
	case *ast.ContextDecl:
		return nil
	case *ast.NamespaceDecl:
		childNamespace := llvmQualifiedDeclName(namespace, n.Name)
		for _, child := range n.Decls {
			if err := g.emitDeclInNamespace(child, childNamespace); err != nil {
				return err
			}
		}
		return nil
	case *ast.StoreDecl:
		qualifiedName := llvmQualifiedDeclName(namespace, n.Name)
		if st, ok := g.lookupStructType(qualifiedName); ok {
			_, err := g.ensureStructBody(qualifiedName, st)
			return err
		}
		return nil
	case *ast.ErrorDecl:
		return nil
	case *ast.GrammarDecl:
		return nil
	case *ast.GrammarEnvDecl:
		return nil
	case *ast.TypeAliasDecl:
		return nil
	case *ast.GrantAliasDecl:
		return nil
	case *ast.UsingDecl, *ast.ImportDecl:
		return nil
	case *ast.ExportTypeDecl, *ast.ExportFuncDecl, *ast.ExportGlobalDecl:
		return nil
	default:
		return fmt.Errorf("unsupported declaration %T", decl)
	}
}
func (g *llvmGenerator) predeclareTreeDeclTypes(decl *ast.TreeDecl, qualifiedName string) error {
	if decl == nil {
		return nil
	}
	familyBase, ok := g.result.NamedTypes[qualifiedName]
	if !ok {
		return fmt.Errorf("missing semantic tree type %s", qualifiedName)
	}
	familyType, ok := familyBase.(*semantic.TreeType)
	if !ok || familyType == nil {
		return fmt.Errorf("declaration %s does not resolve to tree type", qualifiedName)
	}
	for _, memberDecl := range flattenLLVMTreeMemberDecls(decl.Members) {
		memberType, err := g.treeMemberTypeForDecl(familyType, memberDecl)
		if err != nil {
			return err
		}
		switch tt := memberType.(type) {
		case *semantic.TreeCategoryType:
			plan := treeCategoryLayoutPlan(tt)
			switch {
			case plan.isPerVariantRows():
				if _, err := g.ensureTreeCategoryStorageNamedType(tt); err != nil {
					return err
				}
			case plan.isCategoryUnion():
				if _, err := g.ensureTreeCategoryUnionPayloadType(tt); err != nil {
					return err
				}
				if _, err := g.ensureTreeCategoryUnionTableType(tt); err != nil {
					return err
				}
				if err := g.ensureTreeCategoryFrozenLayoutTypes(tt); err != nil {
					return err
				}
			default:
				return unsupportedTreeLayoutError(plan.name, plan.layout)
			}
		case *semantic.TreeBlockType:
			if _, err := g.ensureNamedStructType(tt.Name); err != nil {
				return err
			}
		case *semantic.TreeStructType:
			if _, err := g.ensureNamedStructType(tt.Name); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported tree member type %T", memberType)
		}
	}
	for _, memberDecl := range flattenLLVMTreeMemberDecls(decl.Members) {
		memberType, err := g.treeMemberTypeForDecl(familyType, memberDecl)
		if err != nil {
			return err
		}
		if err := g.noteType(memberType); err != nil {
			return err
		}
	}
	return nil
}
func (g *llvmGenerator) emitTreeDecl(decl *ast.TreeDecl, qualifiedName string) error {
	if decl == nil {
		return nil
	}
	familyBase, ok := g.result.NamedTypes[qualifiedName]
	if !ok {
		return fmt.Errorf("missing semantic tree type %s", qualifiedName)
	}
	familyType, ok := familyBase.(*semantic.TreeType)
	if !ok || familyType == nil {
		return fmt.Errorf("declaration %s does not resolve to tree type", qualifiedName)
	}
	for _, memberDecl := range decl.Members {
		memberType, err := g.treeMemberTypeForDecl(familyType, memberDecl)
		if err != nil {
			return err
		}
		switch tt := memberType.(type) {
		case *semantic.TreeCategoryType:
			plan := treeCategoryLayoutPlan(tt)
			switch {
			case plan.isPerVariantRows():
				if _, err := g.ensureTreeCategoryBody(tt); err != nil {
					return err
				}
			case plan.isCategoryUnion():
				if _, err := g.ensureTreeCategoryUnionPayloadType(tt); err != nil {
					return err
				}
				if _, err := g.ensureTreeCategoryUnionTableType(tt); err != nil {
					return err
				}
				if err := g.ensureTreeCategoryFrozenLayoutTypes(tt); err != nil {
					return err
				}
			default:
				return unsupportedTreeLayoutError(plan.name, plan.layout)
			}
		case *semantic.TreeBlockType:
			if _, err := g.ensureTreeBlockBody(tt); err != nil {
				return err
			}
		case *semantic.TreeStructType:
			if _, err := g.ensureTreeStructBody(tt); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported tree member type %T", memberType)
		}
	}
	return nil
}
func (g *llvmGenerator) treeMemberTypeForDecl(familyType *semantic.TreeType, memberDecl ast.TreeMemberDecl) (semantic.Type, error) {
	if familyType == nil || memberDecl == nil {
		return nil, fmt.Errorf("missing tree member metadata")
	}
	memberName := treeMemberDeclName(memberDecl)
	memberType, ok := familyType.Member(memberName)
	if !ok || memberType == nil {
		return nil, fmt.Errorf("missing semantic tree member type %s.%s", familyType.Name, memberName)
	}
	return memberType, nil
}
func treeMemberDeclName(memberDecl ast.TreeMemberDecl) string {
	switch decl := memberDecl.(type) {
	case *ast.TreeCategoryDecl:
		return decl.Name
	case *ast.TreeBlockDecl:
		return decl.Name
	case *ast.TreeStructDecl:
		return decl.Name
	default:
		return ""
	}
}
func flattenLLVMTreeMemberDecls(members []ast.TreeMemberDecl) []ast.TreeMemberDecl {
	out := make([]ast.TreeMemberDecl, 0, len(members))
	for _, member := range members {
		out = append(out, member)
		if category, ok := member.(*ast.TreeCategoryDecl); ok && category != nil {
			nested := make([]ast.TreeMemberDecl, 0, len(category.Nested))
			for i := range category.Nested {
				nested = append(nested, &category.Nested[i])
			}
			out = append(out, flattenLLVMTreeMemberDecls(nested)...)
		}
	}
	return out
}
