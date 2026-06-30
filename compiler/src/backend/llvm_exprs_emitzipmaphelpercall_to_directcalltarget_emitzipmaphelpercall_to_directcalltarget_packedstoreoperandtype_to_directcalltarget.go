//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>

static int elisacoreLLVMIsZeroValue(LLVMValueRef value) {
	return LLVMIsAConstant(value) != NULL && LLVMIsNull(value);
}

static LLVMMetadataRef elisa_coreAliasMDString(LLVMContextRef ctx, const char* value) {
	if (value == NULL) {
		return LLVMMDStringInContext2(ctx, "", 0);
	}
	return LLVMMDStringInContext2(ctx, value, strlen(value));
}

static LLVMMetadataRef elisa_coreAliasMDNode(LLVMContextRef ctx, LLVMMetadataRef* operands, size_t count) {
	return LLVMMDNodeInContext2(ctx, operands, count);
}

static unsigned elisa_coreMetadataKindID(LLVMContextRef ctx, const char* kindName) {
	return LLVMGetMDKindIDInContext(ctx, kindName, strlen(kindName));
}

static void elisa_coreSetMetadataList(LLVMValueRef inst, LLVMContextRef ctx, const char* kindName, LLVMMetadataRef* scopes, size_t count) {
	if (inst == NULL || ctx == NULL || kindName == NULL || count == 0) {
		return;
	}
	LLVMMetadataRef list = elisa_coreAliasMDNode(ctx, scopes, count);
	LLVMValueRef listValue = LLVMMetadataAsValue(ctx, list);
	LLVMSetMetadata(inst, elisa_coreMetadataKindID(ctx, kindName), listValue);
}

static LLVMMetadataRef elisa_coreCreateAliasScopeDomain(LLVMContextRef ctx, const char* domainName) {
	LLVMMetadataRef operands[1];
	operands[0] = elisa_coreAliasMDString(ctx, domainName);
	return elisa_coreAliasMDNode(ctx, operands, 1);
}

static LLVMMetadataRef elisa_coreCreateAliasScope(LLVMContextRef ctx, LLVMMetadataRef domain, const char* scopeName) {
	LLVMMetadataRef operands[2];
	operands[0] = elisa_coreAliasMDString(ctx, scopeName);
	operands[1] = domain;
	return elisa_coreAliasMDNode(ctx, operands, 2);
}

static void elisa_coreAttachAliasScopeMetadata(LLVMValueRef inst, LLVMContextRef ctx, const char* domainName, const char* aliasScopeName,
	const char* noAliasScope1Name, int hasNoAliasScope1, const char* noAliasScope2Name, int hasNoAliasScope2) {
	if (inst == NULL || ctx == NULL || domainName == NULL || aliasScopeName == NULL) {
		return;
	}
	LLVMMetadataRef domain = elisa_coreCreateAliasScopeDomain(ctx, domainName);
	LLVMMetadataRef aliasScope = elisa_coreCreateAliasScope(ctx, domain, aliasScopeName);
	LLVMMetadataRef aliasScopes[1];
	aliasScopes[0] = aliasScope;
	elisa_coreSetMetadataList(inst, ctx, "alias.scope", aliasScopes, 1);

	LLVMMetadataRef noAliasScopes[2];
	size_t noAliasCount = 0;
	if (hasNoAliasScope1 && noAliasScope1Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope1Name);
	}
	if (hasNoAliasScope2 && noAliasScope2Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope2Name);
	}
	if (noAliasCount != 0) {
		elisa_coreSetMetadataList(inst, ctx, "noalias", noAliasScopes, noAliasCount);
	}
}
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func packedStoreOperandType(t semantic.Type) (*semantic.PackedEnumStoreType, bool) {
	if storeType, ok := t.(*semantic.PackedEnumStoreType); ok {
		return storeType, true
	}
	refType, ok := t.(*semantic.RefType)
	if !ok || refType.State != semantic.RefStateNonNull {
		return nil, false
	}
	storeType, ok := refType.Elem.(*semantic.PackedEnumStoreType)
	return storeType, ok
}
func (s *functionState) emitPackedStoreValueFromExpr(expr ast.Expr) (C.LLVMValueRef, *semantic.PackedEnumStoreType, error) {
	if expr == nil {
		return nil, nil, fmt.Errorf("missing packed store expression")
	}
	objectType := s.exprType(expr)
	if objectType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for packed store expression")
	}
	if storeType, ok := objectType.(*semantic.PackedEnumStoreType); ok {
		value, _, err := s.emitExpr(expr, storeType)
		if err != nil {
			return nil, nil, err
		}
		return value, storeType, nil
	}
	refType, ok := objectType.(*semantic.RefType)
	if !ok || refType.State != semantic.RefStateNonNull {
		return nil, nil, fmt.Errorf("packed store access requires a store value or proven non-null store reference")
	}
	storeType, ok := refType.Elem.(*semantic.PackedEnumStoreType)
	if !ok {
		return nil, nil, fmt.Errorf("packed store access requires a packed store, got %s", objectType.String())
	}
	ptrValue, _, err := s.emitExpr(expr, objectType)
	if err != nil {
		return nil, nil, err
	}
	storeValue, err := s.loadValue(ptrValue, storeType, "packed.store.load")
	if err != nil {
		return nil, nil, err
	}
	return storeValue, storeType, nil
}
func (s *functionState) resolveCallTarget(expr *ast.CallExpr) (C.LLVMValueRef, *semantic.FuncType, error) {
	if fieldExpr, ok := expr.Func.(*ast.FieldExpr); ok {
		if sym, fnType, handled, err := s.resolveStaticInterfaceMethod(fieldExpr); handled {
			if err != nil {
				return nil, nil, err
			}
			// A protocol method with its own generic params (e.g. `[errorset R]`)
			// must be monomorphized per call site, exactly like the plain-Ident
			// generic path below — fnType.Name is the unique __impl__ symbol, so
			// the mangled specialization name cannot collide across impls.
			if decl, ok := sym.Node.(*ast.FuncDecl); ok && len(funcGenericParams(fnType)) > 0 {
				argTypes := make([]semantic.Type, 0, len(expr.Args))
				for _, arg := range expr.Args {
					argTypes = append(argTypes, s.exprType(arg))
				}
				bindings := inferTypeBindingsFromCall(fnType, expr.Args, argTypes, s.exprType(expr))
				value, specialized, err := s.g.ensureSpecializedFunction(decl, fnType, bindings)
				return value, specialized, err
			}
			value, err := s.g.ensureFunctionDeclared(sym.Name, fnType)
			return value, fnType, err
		}
	}
	if ident, ok := expr.Func.(*ast.Ident); ok && !s.identIsLocalBinding(ident.Name) {
		// A local binding (a function-value variable) shadows a same-named global
		// function — fall through to the function-value path below so the call
		// dispatches through the local value, not the global (which, if generic,
		// would also fail to bind its type params). Resolve through the analyzer's
		// recorded canonical (namespace/using/import-qualified) name so a
		// namespaced/imported call target — including a generic function that must be
		// monomorphized — is found instead of the bare name.
		lookupName := ident.Name
		if s.g != nil && s.g.result != nil && s.g.result.ResolvedValueNames != nil {
			if canon, ok := s.g.result.ResolvedValueNames[ident]; ok {
				lookupName = canon
			}
		}
		if sym, ok := s.g.result.GlobalScope.Lookup(lookupName); ok {
			fnType, ok := sym.Type.(*semantic.FuncType)
			if !ok {
				return nil, nil, fmt.Errorf("call target %s does not resolve to a function type", lookupName)
			}
			if decl, ok := sym.Node.(*ast.FuncDecl); ok && len(decl.GenericParams) > 0 {
				argTypes := make([]semantic.Type, 0, len(expr.Args))
				for _, arg := range expr.Args {
					argTypes = append(argTypes, s.exprType(arg))
				}
				bindings := inferTypeBindingsFromCall(fnType, expr.Args, argTypes, s.exprType(expr))
				value, specialized, err := s.g.ensureSpecializedFunction(decl, fnType, bindings)
				return value, specialized, err
			}
			if decl, ok := sym.Node.(*ast.ExternFuncDecl); ok && len(decl.GenericParams) > 0 {
				argTypes := make([]semantic.Type, 0, len(expr.Args))
				for _, arg := range expr.Args {
					argTypes = append(argTypes, s.exprType(arg))
				}
				bindings := inferTypeBindingsFromCall(fnType, expr.Args, argTypes, s.exprType(expr))
				value, specialized, err := s.g.ensureSpecializedExternFunction(decl, fnType, bindings)
				return value, specialized, err
			}
			specialized := s.specializeFunctionType(fnType)
			value, err := s.g.ensureFunctionDeclared(lookupName, specialized)
			return value, specialized, err
		}
	}
	callee, calleeType, err := s.emitExpr(expr.Func, nil)
	if err != nil {
		return nil, nil, err
	}
	fnType, ok := calleeType.(*semantic.FuncType)
	if !ok {
		return nil, nil, fmt.Errorf("call target does not have a function type")
	}
	return callee, fnType, nil
}
func (s *functionState) directCallTarget(expr ast.Expr) bool {
	// `value.call_as[func(...)->T](args)` lowers its receiver to a raw function
	// pointer (an opaque `ptr`). Treat it as a "direct" callee so the call uses
	// buildTypedCall (a plain LLVMBuildCall2 on the pointer) instead of the
	// lambda function-value path, which would do an unnecessary — and for an
	// unaligned address incorrect — closure-tag dispatch.
	if cast, ok := expr.(*ast.CastExpr); ok && cast != nil && cast.Origin == ast.CastExprOriginIndirectCall {
		return true
	}
	if fieldExpr, ok := expr.(*ast.FieldExpr); ok {
		_, _, handled, err := s.resolveStaticInterfaceMethod(fieldExpr)
		return handled && err == nil
	}
	ident, ok := expr.(*ast.Ident)
	if !ok || s == nil || s.g == nil || s.g.result == nil {
		return false
	}
	// A local binding (function-value variable) shadows a same-named global
	// function: the call lowers through the function-value path, not a direct call.
	if s.identIsLocalBinding(ident.Name) {
		return false
	}
	// Resolve through the analyzer's recorded canonical (namespace/using/import-
	// qualified) name, exactly as resolveCallTarget does — otherwise a module-scoped
	// callee (`M.grow`) misses the bare-name lookup and is mis-dispatched through the
	// function-value path, which doesn't thread the hidden region-param args and
	// produces a call/definition arity mismatch.
	lookupName := ident.Name
	if s.g.result.ResolvedValueNames != nil {
		if canon, ok := s.g.result.ResolvedValueNames[ident]; ok {
			lookupName = canon
		}
	}
	sym, ok := s.g.result.GlobalScope.Lookup(lookupName)
	if !ok {
		return false
	}
	return sym.Kind == semantic.SymbolFunc || sym.Kind == semantic.SymbolExternFunc
}

// identIsLocalBinding reports whether name resolves to a local FUNCTION-VALUE
// binding in the current codegen scope chain. Such a local shadows any same-named
// global function, so a call through it must dispatch on the local value rather
// than re-resolving (and specializing) the global by bare name. Only function-
// typed locals divert a call: a non-function local of the same name (e.g. an
// `item: Box[i32]` shadowing a global `def item[T](...)`) is not a call
// target, so calls to the global must still resolve normally — matching how the
// analyzer resolved them.
func (s *functionState) identIsLocalBinding(name string) bool {
	if s == nil {
		return false
	}
	binding, ok := s.lookupBinding(name)
	if !ok {
		return false
	}
	_, isFunc := binding.typ.(*semantic.FuncType)
	return isFunc
}
