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
	"strings"
)

func (s *functionState) emitCallExpr(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, error) {
	if storeType, ok := s.packedStoreConstructorCall(expr); ok {
		return s.emitPackedStoreConstructorValue(expr, storeType)
	}
	if storeType, ok := s.treeStoreConstructorCall(expr); ok {
		return s.emitTreeStoreConstructorValue(expr, storeType)
	}
	if callIdentName(expr) == "freeze" {
		if len(expr.Args) != 1 {
			return nil, nil, fmt.Errorf("freeze expects 1 argument, got %d", len(expr.Args))
		}
		frozenType := s.exprType(expr)
		if frozenStore, ok := frozenType.(*semantic.TreeStoreType); ok && semantic.IsFrozenTreeStoreType(frozenStore) {
			return s.emitTreeStoreFreezeCall(expr.Args[0], frozenStore)
		}
		return s.emitExpr(expr.Args[0], frozenType)
	}
	if value, actualType, handled, err := s.emitDenseKeyHelperCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitNodeTableFillHelperCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitHashBuiltinCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinCloneCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinCopyCall(expr); handled {
		return value, actualType, err
	}
	if errorType, qualifiedTag, ok := s.errorConstructorInfo(expr); ok {
		return s.emitErrorConstructorValue(expr, errorType, qualifiedTag)
	}
	if enumType, variant, ok := s.enumConstructorInfo(expr); ok {
		if variant == nil {
			return nil, nil, fmt.Errorf("unknown enum constructor")
		}
		if enumType != nil && enumType.Packed {
			store, ok := s.lookupPackedStore(enumType)
			if !ok {
				return nil, nil, fmt.Errorf("packed enum constructor %s.%s requires an active in %s: scope or explicit new[%s]", enumType.Name, variant.Name, enumType.StoreType.Name, enumType.StoreType.Name)
			}
			return s.emitPackedEnumConstructorAlloc(expr, store.value, enumType, variant, expr.Args, expr.ArgNames)
		}
		return s.emitEnumConstructorValue(expr, enumType, variant, expr.Args, expr.ArgNames)
	}
	if treeType, variant, ok := s.treeConstructorInfo(expr); ok {
		if variant == nil {
			return nil, nil, fmt.Errorf("unknown tree constructor")
		}
		return s.emitTreeConstructorValue(expr, treeType, variant, expr.Args, expr.ArgNames, nil)
	}
	if memberType, ok := s.treeExactMemberConstructorCall(expr); ok {
		return s.emitTreeExactMemberConstructorValue(expr, memberType, nil)
	}
	if value, actualType, handled, err := s.emitProofCarryingViewHelperCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitTreeTraversalHelperCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitAtomicRuntimeCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedRuntimeCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedStringSliceCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedStringViewCopyCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedArenaViewCopyCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedArenaViewEqCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedArenaFromViewCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedArenaViewFillCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArrayPushCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArrayExtendCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArrayReserveCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArrayResizeCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArraySviewCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArrayCstrCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArrayClearCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArrayTruncateCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinStorePushCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinStoreReserveCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinStoreClearCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinStoreTruncateCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinStoreRowsCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinSOAValidCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDictEntryCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDictEntryInsertCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDictEntryGetOrInsertCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDictRegionMutationCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedMemcpyCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitTypeConstructorCastCall(expr); handled {
		return value, actualType, err
	}
	callee, funcType, err := s.resolveCallTarget(expr)
	if err != nil {
		return nil, nil, err
	}
	if funcType == nil {
		return nil, nil, fmt.Errorf("call target does not have a function type")
	}
	if len(funcType.ImplicitParamNames) != 0 && (!expr.ResolvedImplicitArgsValid || len(expr.ResolvedImplicitArgs) != len(funcType.ImplicitParamNames)) {
		if recovered, ok := s.recoverImplicitCallArgs(expr, funcType); ok {
			expr.ResolvedImplicitArgs = recovered
			expr.ResolvedImplicitArgsValid = true
		} else {
			return nil, nil, fmt.Errorf("call to %s is missing resolved implicit arguments", funcType.Name)
		}
	}
	loweredArgs := backendLoweredCallArgsForFunc(expr, funcType)
	args := make([]C.LLVMValueRef, 0, len(loweredArgs))
	for i, arg := range loweredArgs {
		var expected semantic.Type
		if i < len(funcType.Params) {
			expected = funcType.Params[i]
		}
		if expected == nil && backendIsUnexpectedImplicitTreeStoreArg(arg, funcType) {
			continue
		}
		if value, ok, err := s.emitImplicitTreeStoreCallArg(arg, expected, funcType); ok || err != nil {
			if err != nil {
				return nil, nil, err
			}
			args = append(args, value)
			continue
		}
		value, actual, err := s.emitCallArg(arg, expected, funcType, i)
		if err != nil {
			return nil, nil, err
		}
		if backendIsCVariadicTailArg(funcType, i) {
			value, _, err = s.promoteCVariadicArg(value, actual)
			if err != nil {
				return nil, nil, err
			}
		}
		args = append(args, value)
	}
	regionArenaArgs := s.resolveRegionArenaArgs(expr, funcType)
	return s.emitResolvedCall(callee, funcType, s.directCallTarget(expr.Func), args, regionArenaArgs)
}

func backendIsCVariadicTailArg(fnType *semantic.FuncType, index int) bool {
	return fnType != nil && fnType.Variadic && index >= len(fnType.Params)
}

func (s *functionState) promoteCVariadicArg(value C.LLVMValueRef, actual semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if value == nil || actual == nil || s == nil || s.g == nil || s.g.result == nil {
		return value, actual, nil
	}
	if semantic.IsBoolType(actual) {
		i32Type := s.g.result.NamedTypes["i32"]
		i32LLVM, err := s.g.lowerType(i32Type)
		if err != nil {
			return nil, nil, err
		}
		return C.LLVMBuildZExt(s.builder, value, i32LLVM, cStringFree("vararg.bool")), i32Type, nil
	}
	if builtin, ok := numericCastType(actual).(*semantic.BuiltinType); ok && builtin != nil && builtin.Name == "f32" {
		f64Type := s.g.result.NamedTypes["f64"]
		promoted, err := s.coerceValue(value, actual, f64Type)
		return promoted, f64Type, err
	}
	if _, width, ok := semantic.BitIntInfo(numericCastType(actual)); ok && width < 32 {
		i32Type := s.g.result.NamedTypes["i32"]
		promoted, err := s.coerceValue(value, actual, i32Type)
		return promoted, i32Type, err
	}
	return value, actual, nil
}

func backendLoweredCallArgsForFunc(expr *ast.CallExpr, funcType *semantic.FuncType) []ast.Expr {
	if expr == nil {
		return nil
	}
	explicitArgs := expr.Args
	if expr.ResolvedArgsValid && expr.ResolvedCommonArgs == nil {
		explicitArgs = expr.ResolvedArgs
	}
	if funcType == nil || len(funcType.ImplicitParamNames) == 0 || !expr.ResolvedImplicitArgsValid || len(expr.ResolvedImplicitArgs) == 0 {
		return explicitArgs
	}
	implicitCount := len(funcType.ImplicitParamNames)
	if implicitCount > len(expr.ResolvedImplicitArgs) {
		implicitCount = len(expr.ResolvedImplicitArgs)
	}
	out := make([]ast.Expr, 0, len(explicitArgs)+implicitCount)
	out = append(out, explicitArgs...)
	out = append(out, expr.ResolvedImplicitArgs[:implicitCount]...)
	return out
}

func backendIsUnexpectedImplicitTreeStoreArg(arg ast.Expr, calleeType *semantic.FuncType) bool {
	ident, ok := arg.(*ast.Ident)
	if !ok || ident == nil {
		return false
	}
	if calleeType != nil {
		for _, name := range calleeType.ImplicitParamNames {
			if ident.Name == name {
				return false
			}
		}
	}
	return strings.HasPrefix(ident.Name, "__tree_store")
}

func (s *functionState) emitImplicitTreeStoreCallArg(arg ast.Expr, expected semantic.Type, calleeType *semantic.FuncType) (C.LLVMValueRef, bool, error) {
	storeType, ok := expected.(*semantic.TreeStoreType)
	ident, ok := arg.(*ast.Ident)
	if !ok || ident == nil {
		if storeType == nil || storeType.Family == nil || !backendExprCanCreateTreeStore(s, arg) {
			return nil, false, nil
		}
		value, err := s.emitImplicitTreeStoreValueFromOwnerArg(arg, storeType)
		return value, true, err
	}
	if !ok || storeType == nil || storeType.Family == nil {
		if calleeType == nil {
			return nil, false, nil
		}
		matchesImplicitParam := false
		for _, name := range calleeType.ImplicitParamNames {
			if ident.Name == name {
				matchesImplicitParam = true
				break
			}
		}
		if !matchesImplicitParam {
			return nil, false, nil
		}
		for _, typ := range s.g.result.NamedTypes {
			family, isTree := typ.(*semantic.TreeType)
			if !isTree || family == nil || family.StoreType == nil {
				continue
			}
			if ident.Name == semantic.TreeStoreImplicitParamName(family) {
				storeType = family.StoreType
				ok = true
				break
			}
		}
		if !ok || storeType == nil || storeType.Family == nil {
			return nil, false, nil
		}
	}
	if ident.Name != semantic.TreeStoreImplicitParamName(storeType.Family) {
		if !backendExprCanCreateTreeStore(s, arg) {
			return nil, false, nil
		}
		value, err := s.emitImplicitTreeStoreValueFromOwnerArg(arg, storeType)
		return value, true, err
	}
	owner, ok := s.lookupTreeAllocOwnerForFamily(storeType.Family)
	if !ok {
		callee := "<unknown>"
		if calleeType != nil && calleeType.Name != "" {
			callee = calleeType.Name
		}
		caller := "<helper>"
		if s != nil && s.decl != nil {
			caller = s.decl.Name
		}
		return nil, true, fmt.Errorf("call from %s to %s requires tree store context for %s; use in owner/in perm around the call", caller, callee, storeType.Family.Name)
	}
	value, _, err := s.ensureTreeOwnerStoreValue(owner, storeType.Family)
	if err != nil {
		return nil, true, err
	}
	return value, true, nil
}

func (s *functionState) emitImplicitTreeStoreValueFromOwnerArg(arg ast.Expr, storeType *semantic.TreeStoreType) (C.LLVMValueRef, error) {
	if storeType == nil || storeType.Family == nil {
		return nil, fmt.Errorf("missing tree store metadata")
	}
	owner, ok, err := s.classifyTreeAllocOwnerExpr(arg)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("tree store context for %s requires an Arena or tree store owner", storeType.Family.Name)
	}
	if s != nil && s.implicitTreeStoreOwners != nil {
		if existing, ok := s.implicitTreeStoreOwners[storeType.Family.Name]; ok && existing.storeType != nil && (existing.storeValue != nil || existing.storePtr != nil) {
			if existing.arenaRef == nil && existing.arenaRefPtr == nil && owner.arenaRef != nil {
				existing.arenaRef = owner.arenaRef
			}
			if existing.arenaRef == nil && existing.arenaRefPtr == nil && owner.arenaRefPtr != nil {
				existing.arenaRefPtr = owner.arenaRefPtr
			}
			value, _, err := s.ensureTreeOwnerStoreValue(existing, storeType.Family)
			return value, err
		}
	}
	value, _, err := s.ensureTreeOwnerStoreValue(owner, storeType.Family)
	if err != nil {
		return nil, err
	}
	return value, nil
}

func backendExprCanCreateTreeStore(s *functionState, expr ast.Expr) bool {
	if s == nil || expr == nil || s.g == nil || s.g.result == nil {
		return false
	}
	return backendTypeCanCreateTreeStore(s.exprType(expr), s.g.result.NamedTypes)
}

func (s *functionState) emitCastHookArgs(exprLabel string, operand ast.Expr, fnType *semantic.FuncType) ([]C.LLVMValueRef, error) {
	if fnType == nil || len(fnType.Params) == 0 {
		return nil, fmt.Errorf("invalid semantic cast hook for %s", exprLabel)
	}
	arg, _, err := s.emitExpr(operand, fnType.Params[0])
	if err != nil {
		return nil, err
	}
	args := []C.LLVMValueRef{arg}
	for i := 1; i < len(fnType.Params); i++ {
		storeType, ok := fnType.Params[i].(*semantic.TreeStoreType)
		if !ok || storeType == nil || storeType.Family == nil {
			return nil, fmt.Errorf("unsupported semantic cast hook parameter %d for %s", i, exprLabel)
		}
		implicitArg := &ast.Ident{Name: semantic.TreeStoreImplicitParamName(storeType.Family)}
		value, ok, err := s.emitImplicitTreeStoreCallArg(implicitArg, storeType, fnType)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("missing implicit semantic cast hook parameter %d for %s", i, exprLabel)
		}
		args = append(args, value)
	}
	return args, nil
}

func (s *functionState) emitTypeConstructorCastCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if s == nil || expr == nil {
		return nil, nil, false, nil
	}
	if s.g != nil && s.g.result != nil {
		if _, ok := s.g.result.ExprTypes[expr.Func].(*semantic.FuncType); ok {
			return nil, nil, false, nil
		}
	}
	targetExpr, ok := backendTypeExprFromConstructorCallee(expr.Func)
	if !ok {
		return nil, nil, false, nil
	}
	if s.g == nil || s.g.result == nil || !backendTypeConstructorTargetKnown(targetExpr, s.g.result.NamedTypes) {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("type constructor cast expects exactly 1 positional argument")
	}
	targetType := s.exprType(expr)
	if targetType == nil {
		resolved, err := s.resolveTypeExpr(targetExpr)
		if err != nil {
			return nil, nil, true, err
		}
		targetType = resolved
	}
	if s.g != nil && s.g.result != nil && s.g.result.CastHooks != nil {
		if sym, ok := s.g.result.CastHooks[expr]; ok && sym != nil {
			fnType, ok := sym.Type.(*semantic.FuncType)
			if !ok || fnType == nil || len(fnType.Params) == 0 {
				return nil, nil, true, fmt.Errorf("invalid semantic cast hook for %T", expr)
			}
			args, err := s.emitCastHookArgs(fmt.Sprintf("%T", expr), expr.Args[0], fnType)
			if err != nil {
				return nil, nil, true, err
			}
			callee, err := s.g.ensureFunctionDeclared(sym.Name, fnType)
			if err != nil {
				return nil, nil, true, err
			}
			llvmFnType, err := s.g.lowerFunctionType(fnType)
			if err != nil {
				return nil, nil, true, err
			}
			call := s.buildCall(llvmFnType, callee, args, "casthook")
			return call, fnType.Return, true, nil
		}
	}
	operandExpected := semantic.Type(nil)
	if _, ok := expr.Args[0].(*ast.ZeroedLit); ok {
		operandExpected = targetType
	}
	value, actualType, err := s.emitExpr(expr.Args[0], operandExpected)
	if err != nil {
		return nil, nil, true, err
	}
	coerced, err := s.coerceValue(value, actualType, targetType)
	if err != nil {
		return nil, nil, true, err
	}
	return coerced, targetType, true, nil
}

func backendTypeConstructorTargetKnown(expr ast.TypeExpr, namedTypes map[string]semantic.Type) bool {
	if expr == nil || namedTypes == nil {
		return false
	}
	switch n := expr.(type) {
	case *ast.NamedType:
		_, ok := namedTypes[n.Name]
		return ok
	case *ast.GenericType:
		_, ok := namedTypes[n.Name]
		return ok
	case *ast.BuiltinTypeExpr:
		_, ok := namedTypes[n.Name]
		return ok
	default:
		return false
	}
}

func backendTypeExprFromConstructorCallee(expr ast.Expr) (ast.TypeExpr, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil || n.Name == "" {
			return nil, false
		}
		return &ast.NamedType{Position: n.Position, Name: n.Name}, true
	case *ast.FieldExpr:
		name, ok := backendQualifiedTypePathFromExpr(n)
		if !ok || name == "" {
			return nil, false
		}
		return &ast.NamedType{Position: n.Position, Name: name}, true
	case *ast.TypeExprExpr:
		if n == nil || n.Type == nil {
			return nil, false
		}
		return n.Type, true
	case *ast.SpecializeExpr:
		name, ok := backendQualifiedTypePathFromExpr(n.Operand)
		if !ok || name == "" {
			return nil, false
		}
		return &ast.GenericType{Position: n.Position, Name: name, Args: n.TypeArgs}, true
	case *ast.ParenExpr:
		return backendTypeExprFromConstructorCallee(n.Inner)
	default:
		return nil, false
	}
}

func backendQualifiedTypePathFromExpr(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil || n.Name == "" {
			return "", false
		}
		return n.Name, true
	case *ast.FieldExpr:
		prefix, ok := backendQualifiedTypePathFromExpr(n.Object)
		if !ok || prefix == "" || n.Field == "" {
			return "", false
		}
		return prefix + "." + n.Field, true
	case *ast.ParenExpr:
		return backendQualifiedTypePathFromExpr(n.Inner)
	case *ast.TypeExprExpr:
		named, ok := n.Type.(*ast.NamedType)
		if !ok || named == nil || named.Name == "" {
			return "", false
		}
		return named.Name, true
	default:
		return "", false
	}
}

func builtinDArrayPushReceiverType(t semantic.Type) (*semantic.DArrayType, *semantic.RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if darrayType, ok := t.(*semantic.DArrayType); ok && darrayType != nil {
		return darrayType, nil, true
	}
	refType, ok := t.(*semantic.RefType)
	if !ok || refType == nil {
		return nil, nil, false
	}
	darrayType, ok := refType.Elem.(*semantic.DArrayType)
	if !ok || darrayType == nil {
		return nil, nil, false
	}
	return darrayType, refType, true
}
func builtinDArrayExtendSourceType(t semantic.Type) (semantic.Type, bool) {
	if t == nil {
		return nil, false
	}
	switch tt := t.(type) {
	case *semantic.DArrayType, *semantic.DArrayViewType, *semantic.ArrayType:
		return t, true
	case *semantic.RefType:
		switch tt.Elem.(type) {
		case *semantic.DArrayType, *semantic.DArrayViewType, *semantic.ArrayType:
			return tt.Elem, true
		}
	}
	return nil, false
}
func builtinStoreReceiverType(t semantic.Type) (*semantic.StructType, *semantic.RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if st, ok := semantic.StripAggregateStateType(t).(*semantic.StructType); ok && st != nil && st.Store {
		return st, nil, true
	}
	refType, ok := t.(*semantic.RefType)
	if !ok || refType == nil || refType.Elem == nil {
		return nil, nil, false
	}
	st, ok := semantic.StripAggregateStateType(refType.Elem).(*semantic.StructType)
	if !ok || st == nil || !st.Store {
		return nil, nil, false
	}
	return st, refType, true
}
func builtinDictEntryReceiverType(t semantic.Type) (*semantic.DictEntryType, *semantic.RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if entryType, ok := semantic.StripAggregateStateType(t).(*semantic.DictEntryType); ok && entryType != nil {
		return entryType, nil, true
	}
	refType, ok := t.(*semantic.RefType)
	if !ok || refType == nil || refType.Elem == nil {
		return nil, nil, false
	}
	entryType, ok := semantic.StripAggregateStateType(refType.Elem).(*semantic.DictEntryType)
	if !ok || entryType == nil {
		return nil, nil, false
	}
	return entryType, refType, true
}
func builtinDictReceiverType(t semantic.Type) (*semantic.DictType, *semantic.RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if dictType, ok := t.(*semantic.DictType); ok && dictType != nil {
		return dictType, nil, true
	}
	refType, ok := t.(*semantic.RefType)
	if !ok || refType == nil || refType.Elem == nil {
		return nil, nil, false
	}
	dictType, ok := refType.Elem.(*semantic.DictType)
	if !ok || dictType == nil {
		return nil, nil, false
	}
	return dictType, refType, true
}
func builtinDictEntryValueRefType(dictType *semantic.DictType) *semantic.RefType {
	if dictType == nil {
		return &semantic.RefType{Elem: sInvalidType(), Mutable: true, State: semantic.RefStateNullable, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	}
	return &semantic.RefType{Elem: dictType.Value, Mutable: true, State: semantic.RefStateNullable, Storage: semantic.RefStorageAny, ExplicitStorage: true}
}
func sInvalidType() semantic.Type {
	return &semantic.InvalidType{}
}
