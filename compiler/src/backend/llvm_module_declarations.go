//go:build cgo

package backend

/*
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
)

func (g *llvmGenerator) emitModule() error {
	if g.emitDebugInfo {
		g.initDebugInfo()
	}
	if g.emitTrace {
		g.initTrace()
	}
	decls := g.result.ActiveFile().Decls
	if g.hasPartitionModuleDecls {
		decls = g.partitionModuleDecls
	}
	for _, decl := range decls {
		if err := g.predeclareDeclTypes(decl); err != nil {
			return err
		}
	}
	if !g.partitionSpecializationOnly {
		for _, module := range g.result.EASMModules {
			if err := g.emitEASMModule(module); err != nil {
				return err
			}
		}
	}
	for _, decl := range decls {
		if err := g.emitDecl(decl); err != nil {
			return err
		}
	}
	for _, exported := range g.result.ExportedGlobals {
		if err := g.emitExportedGlobal(exported); err != nil {
			return err
		}
	}
	if !g.partitionSpecializationOnly {
		for _, exported := range g.result.ExportedFuncs {
			if err := g.emitExportedFunction(exported); err != nil {
				return err
			}
		}
	}
	if g.di != nil && !g.partitionSpecializationOnly {
		g.di.finalize()
	}
	return nil
}

func (g *llvmGenerator) activeDeclBranch(decl *ast.StaticIfDecl, namespace string) ([]ast.Decl, error) {
	lookup := func(name string) (semantic.ConstValue, bool) {
		for _, candidate := range llvmVisibleGlobalNamesWithAliases(name, namespace, g.result.ModuleAliases) {
			if value, ok := g.constValue(candidate); ok {
				return value, true
			}
		}
		return semantic.ConstValue{}, false
	}
	eval := func(expr ast.Expr) (bool, bool) {
		value, ok := evalConstExprWithLookup(expr, lookup, g.evalStaticFunctionCall, g.constExprType)
		if !ok {
			return g.evalConstBoolExpr(expr)
		}
		return value.Bool, value.Kind == semantic.ConstBool
	}
	selected, ok := eval(decl.Cond)
	if !ok {
		return nil, fmt.Errorf("static if condition must be a compile-time bool")
	}
	if selected {
		return decl.Then, nil
	}
	for _, elif := range decl.Elifs {
		selected, ok := eval(elif.Cond)
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
		t, ok := g.lookupNamedType(qualifiedName)
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
	case *ast.ExternTypeDecl:
		_, err := g.ensureNamedStructType(llvmQualifiedDeclName(namespace, n.Name))
		return err
	case *ast.FuncDecl, *ast.ExternFuncDecl:
		if fnDecl, ok := decl.(*ast.FuncDecl); ok && fnDecl.Static {
			return nil
		}
		if fnDecl, ok := decl.(*ast.FuncDecl); ok && fnDecl.IsLemma {
			return nil // verification-only; never declared or defined in LLVM
		}
		if fnDecl, ok := decl.(*ast.FuncDecl); ok && fnDecl.IsGhost {
			return nil // verification-only spec function; never declared or defined in LLVM
		}
		if fnDecl, ok := decl.(*ast.FuncDecl); ok && fnDecl.IsContract {
			return nil // docs/97: a named-contract bundle is verification-only, never emitted
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
		if _, body := decl.(*ast.FuncDecl); body && !g.ownsPartitionFunction(sym) {
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
	case *ast.GlobalRegionDecl:
		return nil // docs/128: a type-level region; no storage of its own
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
				if _, body := member.(*ast.FuncDecl); body && !g.ownsPartitionFunction(sym) {
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
		branch, err := g.activeDeclBranch(n, namespace)
		if err != nil {
			return err
		}
		for _, inner := range branch {
			if err := g.predeclareDeclTypesInNamespace(inner, namespace); err != nil {
				return err
			}
		}
		return nil
	case *ast.ConstDecl, *ast.TokenSetDecl, *ast.CharsetDecl, *ast.KeywordMapDecl, *ast.ConstEnumDecl:
		return nil
	case *ast.LayoutDecl:
		// docs/107 guest-memory layout: a verifier-only declaration; emits no code (accessors over it
		// were already desugared to MemoryManager_Read/WriteU<N> calls by the analyzer).
		return nil
	case *ast.StaticAssertDecl:
		return g.checkStaticAssertDecl(n)
	case *ast.StaticAssertBlockDecl:
		return g.checkStaticAssertBlockDecl(n)
	case *ast.PermissionDecl:
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
	case *ast.RefineDecl:
		// Named refinement aliases are representation-erased desugaring sugar; consumed during analysis.
		return nil
	case *ast.AliasDecl:
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
			_, isArray := sym.Type.(*semantic.ArrayType)
			_, isDict := sym.Type.(*semantic.DictType)
			if isArray || isDict {
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
	case *ast.LayoutDecl:
		// docs/107 guest-memory layout: verifier-only, emits no code.
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
		t, ok := g.lookupNamedType(qualifiedName)
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
	case *ast.FuncDecl:
		if n.Static {
			return nil
		}
		if n.IsLemma {
			// A lemma is verification-only (ghost code) and its call sites are erased, so nothing
			// references its body — never emit it. (Laws DO get emitted: `x is Law` desugars to a real
			// `Law(x)` call, so the law body must exist.)
			return nil
		}
		if n.IsGhost {
			return nil
		}
		if n.IsContract {
			return nil // docs/97: named-contract bundle is verification-only, never emitted
		}
		if len(n.GenericParams) > 0 {
			return nil
		}
		sym, ok := g.symbolsByNode[decl]
		if !ok {
			return fmt.Errorf("missing semantic symbol for function declaration")
		}
		if !g.ownsPartitionFunction(sym) {
			return nil
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
	case *ast.GlobalRegionDecl:
		return nil // docs/128: a type-level region; no storage of its own
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
				if !g.ownsPartitionFunction(sym) {
					continue
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
		branch, err := g.activeDeclBranch(n, namespace)
		if err != nil {
			return err
		}
		for _, inner := range branch {
			if err := g.emitDeclInNamespace(inner, namespace); err != nil {
				return err
			}
		}
		return nil
	case *ast.ExternTypeDecl, *ast.StaticAssertDecl, *ast.StaticAssertBlockDecl, *ast.StaticGenerateDecl:
		return nil
	case *ast.PermissionDecl:
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
	case *ast.RefineDecl:
		return nil
	case *ast.AliasDecl:
		return nil
	case *ast.UsingDecl, *ast.ImportDecl:
		return nil
	case *ast.ExportTypeDecl, *ast.ExportFuncDecl, *ast.ExportGlobalDecl:
		return nil
	default:
		return fmt.Errorf("unsupported declaration %T", decl)
	}
}

// Non-owner functions are declared on demand by call/value lowering. Avoid
// creating and lowering every program-wide prototype in each optimized IR
// partition, only to prune it later. Intrinsics keep their existing LLVM-name
// ownership check after declaration, because they may resolve to builtins.
func (g *llvmGenerator) ownsPartitionFunction(sym *semantic.Symbol) bool {
	if g.partBody == nil {
		return true
	}
	if fn, ok := sym.Type.(*semantic.FuncType); ok && fn.IntrinsicName != "" {
		return true
	}
	return g.partBody(g.llvmSymbolName(sym.Name))
}
