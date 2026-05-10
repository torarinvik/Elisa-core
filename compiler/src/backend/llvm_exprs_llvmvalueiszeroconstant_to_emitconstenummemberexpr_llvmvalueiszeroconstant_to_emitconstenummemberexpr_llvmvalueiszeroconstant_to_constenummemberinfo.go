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
	"unsafe"
)

func llvmValueIsZeroConstant(value C.LLVMValueRef) bool {
	return value != nil && C.elisacoreLLVMIsZeroValue(value) != 0
}
func (s *functionState) addCallSiteEnumAttribute(call C.LLVMValueRef, index C.uint, name string) {
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	kind := C.LLVMGetEnumAttributeKindForName(nameC, C.size_t(len(name)))
	if kind == 0 {
		return
	}
	attr := C.LLVMCreateEnumAttribute(s.g.context, kind, 0)
	C.LLVMAddCallSiteAttribute(call, index, attr)
}
func (s *functionState) attachAliasScopeMetadataWithNames(inst C.LLVMValueRef, domainName string, aliasScopeName string, noAliasScopeNames []string) {
	if s == nil || s.g == nil || inst == nil || domainName == "" || aliasScopeName == "" {
		return
	}
	domainNameC := cString(domainName)
	defer C.free(unsafe.Pointer(domainNameC))
	aliasScopeNameC := cString(aliasScopeName)
	defer C.free(unsafe.Pointer(aliasScopeNameC))
	var noAliasScope1C *C.char
	var noAliasScope2C *C.char
	hasNoAliasScope1 := C.int(0)
	hasNoAliasScope2 := C.int(0)
	if len(noAliasScopeNames) != 0 && noAliasScopeNames[0] != "" {
		noAliasScope1C = cString(noAliasScopeNames[0])
		defer C.free(unsafe.Pointer(noAliasScope1C))
		hasNoAliasScope1 = 1
	}
	if len(noAliasScopeNames) > 1 && noAliasScopeNames[1] != "" {
		noAliasScope2C = cString(noAliasScopeNames[1])
		defer C.free(unsafe.Pointer(noAliasScope2C))
		hasNoAliasScope2 = 1
	}
	C.elisa_coreAttachAliasScopeMetadata(inst, s.g.context, domainNameC, aliasScopeNameC, noAliasScope1C, hasNoAliasScope1, noAliasScope2C, hasNoAliasScope2)
}
func callIdentName(expr *ast.CallExpr) string {
	if expr == nil {
		return ""
	}
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return ""
	}
	return ident.Name
}
func callSpecializedIdent(expr ast.Expr) (*ast.Ident, *ast.SpecializeExpr, bool) {
	if expr == nil {
		return nil, nil, false
	}
	specialize, ok := expr.(*ast.SpecializeExpr)
	if !ok || specialize == nil {
		return nil, nil, false
	}
	ident, ok := specialize.Operand.(*ast.Ident)
	if !ok || ident == nil {
		return nil, nil, false
	}
	return ident, specialize, true
}
func callSpecializedIdentName(expr *ast.CallExpr) string {
	if expr == nil {
		return ""
	}
	if ident, _, ok := callSpecializedIdent(expr.Func); ok {
		return ident.Name
	}
	return ""
}
func denseKeySourceEnumType(t semantic.Type) (*semantic.EnumType, bool) {
	if t == nil {
		return nil, false
	}
	t = semantic.StripAggregateStateType(t)
	if enumType, ok := t.(*semantic.EnumType); ok && enumType != nil && enumType.Packed {
		return enumType, true
	}
	if viewType, ok := t.(*semantic.PackedVariantViewType); ok && viewType != nil && viewType.Enum != nil {
		return viewType.Enum, true
	}
	return nil, false
}
func (s *functionState) emitExpr(expr ast.Expr, expected semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil {
		return nil, nil, fmt.Errorf("cannot emit nil expression")
	}

	actualType := s.exprType(expr)
	if expected != nil && isZeroedExpr(expr) {
		value, err := s.zeroValue(expected)
		return value, expected, err
	}

	var (
		value C.LLVMValueRef
		err   error
	)

	switch n := expr.(type) {
	case *ast.Ident:
		if s.g != nil && s.g.result != nil && s.g.result.RewriteDefaults[n] {
			value, actualType, err = s.emitTreeRewriteDefaultExpr(n)
		} else {
			value, actualType, err = s.emitIdent(n)
		}
	case *ast.IntLit:
		value, actualType, err = s.emitIntLiteral(n)
	case *ast.FloatLit:
		value, actualType, err = s.emitFloatLiteral(n)
	case *ast.ShorthandMemberExpr:
		if constEnumType, member, ok := s.shorthandConstEnumMemberInfo(n); ok {
			value, actualType, err = s.emitConstEnumMemberExpr(constEnumType, member)
		} else {
			err = fmt.Errorf("unsupported shorthand member %q during LLVM lowering", shorthandMemberDisplayBackend(n))
		}
	case *ast.StringLit:
		value, actualType, err = s.emitStringLiteral(n, expected)
	case *ast.CharLit:
		value, actualType, err = s.emitCharLiteral(n)
	case *ast.BoolLit:
		value, actualType, err = s.emitBoolLiteral(n)
	case *ast.NullLit:
		value, actualType, err = s.emitNullLiteral()
	case *ast.ZeroedLit:
		return nil, nil, fmt.Errorf("zeroed requires an expected destination type")
	case *ast.ExprBlock:
		value, actualType, err = s.emitExprBlock(n, expected)
	case *ast.ListLitExpr:
		value, actualType, err = s.emitListLitExpr(n, expected)
	case *ast.ListComprehensionExpr:
		value, actualType, err = s.emitListComprehensionExpr(n)
	case *ast.QueryExpr:
		value, actualType, err = s.emitQueryExpr(n)
	case *ast.BinaryExpr:
		value, actualType, err = s.emitBinaryExpr(n)
	case *ast.UnaryExpr:
		value, actualType, err = s.emitUnaryExpr(n)
	case *ast.CallExpr:
		if n.Safe {
			value, actualType, err = s.emitSafeCallExpr(n)
		} else {
			value, actualType, err = s.emitCallExpr(n)
		}
	case *ast.FieldExpr:
		if n.Safe {
			value, actualType, err = s.emitSafeFieldExpr(n)
		} else if errorType, _, ok := s.errorTagInfo(n); ok {
			value, actualType, err = s.emitErrorTagExpr(n, errorType)
		} else if constEnumType, member, ok := s.constEnumMemberInfo(n); ok {
			value, actualType, err = s.emitConstEnumMemberExpr(constEnumType, member)
		} else if treeType, variant, ok := s.treeConstructorInfoFromField(n); ok && variant != nil && len(variant.Payload) == 0 {
			value, actualType, err = s.emitTreeConstructorValue(nil, treeType, variant, nil, nil, nil)
		} else if enumType, variant, ok := s.enumConstructorInfoFromField(n); ok && variant != nil && len(variant.Payload) == 0 {
			if enumType != nil && enumType.Packed {
				store, ok := s.lookupPackedStore(enumType)
				if !ok {
					err = fmt.Errorf("packed enum constructor %s.%s requires an active in %s: scope or explicit new[%s]", enumType.Name, variant.Name, enumType.StoreType.Name, enumType.StoreType.Name)
				} else {
					value, actualType, err = s.emitPackedEnumConstructorAlloc(nil, store.value, enumType, variant, nil, nil)
				}
			} else {
				value, actualType, err = s.emitEnumConstructorValue(nil, enumType, variant, nil, nil)
			}
		} else {
			value, actualType, err = s.emitFieldExpr(n)
		}
	case *ast.RaiseExpr:
		value, actualType, err = s.emitRaiseExpr(n)
	case *ast.TryExpr:
		value, actualType, err = s.emitTryExpr(n)
	case *ast.CatchExpr:
		value, actualType, err = s.emitCatchExpr(n)
	case *ast.UnwrapElseExpr:
		value, actualType, err = s.emitUnwrapElseExpr(n)
	case *ast.OptionalBindExpr:
		value, actualType, err = s.emitOptionalBindExpr(n)
	case *ast.AllocExpr:
		value, actualType, err = s.emitAllocExpr(n)
	case *ast.MatchExpr:
		value, actualType, err = s.emitMatchExpr(n)
	case *ast.VisitExpr:
		value, actualType, err = s.emitVisitExpr(n)
	case *ast.FoldExpr:
		value, actualType, err = s.emitFoldExpr(n)
	case *ast.EmitExpr:
		value, actualType, err = s.emitSequenceRewriteEmitExpr(n)
	case *ast.IndexExpr:
		value, actualType, err = s.emitIndexExpr(n)
	case *ast.SliceExpr:
		value, actualType, err = s.emitSliceExpr(n)
	case *ast.CastExpr:
		value, actualType, err = s.emitCastExpr(n)
	case *ast.SizeofExpr:
		value, actualType, err = s.emitSizeofExpr(n)
	case *ast.AlignofExpr:
		value, actualType, err = s.emitAlignofExpr(n)
	case *ast.OffsetofExpr:
		value, actualType, err = s.emitOffsetofExpr(n)
	case *ast.TernaryExpr:
		value, actualType, err = s.emitTernaryExpr(n)
	case *ast.AddrOfExpr:
		value, actualType, err = s.emitAddrOfExpr(n)
	case *ast.MoveExpr:
		value, actualType, err = s.emitMoveExpr(n, expected)
	case *ast.SpecializeExpr:
		value, actualType, err = s.emitSpecializeExpr(n)
	case *ast.StructLitExpr:
		value, actualType, err = s.emitStructLitExpr(n)
	case *ast.RecordUpdateExpr:
		value, actualType, err = s.emitRecordUpdateExpr(n)
	case *ast.LambdaExpr:
		value, actualType, err = s.emitLambdaExpr(n)
	case *ast.TupleExpr:
		value, actualType, err = s.emitTupleExpr(n)
	case *ast.ParenExpr:
		value, actualType, err = s.emitExpr(n.Inner, expected)
	case *ast.CanExpr:
		value, actualType, err = s.emitExpr(n.Expr, expected)
	default:
		return nil, nil, fmt.Errorf("unsupported expression %T", expr)
	}
	if err != nil {
		return nil, nil, err
	}
	if expected != nil {
		coerced, err := s.coerceValue(value, actualType, expected)
		if err != nil {
			return nil, nil, err
		}
		return coerced, expected, nil
	}
	return value, actualType, nil
}
func (s *functionState) emitExprBlock(expr *ast.ExprBlock, expected semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || expr.Value == nil {
		return nil, nil, fmt.Errorf("invalid expression block")
	}
	savedPackedStores := s.packedStores
	s.packedStores = s.clonePackedStores()
	s.pushScope()
	scope := s.scope
	defer func() {
		s.popScope()
		s.packedStores = savedPackedStores
	}()
	for _, stmt := range expr.Stmts {
		if s.currentBlockTerminated() {
			s.discardScopeCleanups(scope)
			return nil, nil, fmt.Errorf("expression block setup statements terminated control flow")
		}
		if err := s.emitStmt(stmt); err != nil {
			s.discardScopeCleanups(scope)
			return nil, nil, err
		}
	}
	if s.currentBlockTerminated() {
		s.discardScopeCleanups(scope)
		return nil, nil, fmt.Errorf("expression block setup statements terminated control flow")
	}
	value, actualType, err := s.emitExpr(expr.Value, expected)
	if err != nil {
		s.discardScopeCleanups(scope)
		return nil, nil, err
	}
	if s.currentBlockTerminated() {
		s.discardScopeCleanups(scope)
		return value, actualType, nil
	}
	if err := s.emitScopeCleanups(scope); err != nil {
		return nil, nil, err
	}
	return value, actualType, nil
}
func (s *functionState) emitMoveExpr(expr *ast.MoveExpr, expected semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	return s.emitMovedValue(expr.Operand, expected)
}
func (s *functionState) emitMovedValue(operand ast.Expr, expected semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if binding, ok := s.lookupScopedMoveBinding(operand); ok {
		value, err := s.loadValue(binding.ptr, binding.typ, binding.name)
		if err != nil {
			return nil, nil, err
		}
		zero, err := s.zeroValue(binding.typ)
		if err != nil {
			return nil, nil, err
		}
		C.LLVMBuildStore(s.builder, zero, binding.ptr)
		return value, binding.typ, nil
	}
	return s.emitExpr(operand, expected)
}
func (s *functionState) lookupScopedMoveBinding(expr ast.Expr) (scopedCleanupBinding, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.lookupScopedMoveBinding(n.Inner)
	case *ast.Ident:
		binding, ok := s.lookupBinding(n.Name)
		if !ok {
			return scopedCleanupBinding{}, false
		}
		for i := len(s.scopedCleanups) - 1; i >= 0; i-- {
			if s.scopedCleanups[i].ptr == binding.ptr {
				return s.scopedCleanups[i], true
			}
		}
	}
	return scopedCleanupBinding{}, false
}
func (s *functionState) buildCall(llvmFnType C.LLVMTypeRef, callee C.LLVMValueRef, args []C.LLVMValueRef, name string) C.LLVMValueRef {
	argCount := len(args)
	argPtr := llvmValueSlicePtr(args)
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	return C.LLVMBuildCall2(s.builder, llvmFnType, callee, argPtr, C.unsigned(argCount), nameC)
}
func (s *functionState) emitIdent(expr *ast.Ident) (C.LLVMValueRef, semantic.Type, error) {
	if actualType := s.exprType(expr); semantic.IsNullType(actualType) {
		return s.emitNullLiteral()
	} else if viewType, ok := actualType.(*semantic.PackedVariantViewType); ok {
		if binding, ok := s.lookupPackedVariantView(expr.Name); ok {
			if viewType.Enum != nil && !viewType.Enum.Packed && binding.ptr != nil {
				value, err := s.loadValue(binding.ptr, viewType.Enum, expr.Name)
				return value, viewType.Enum, err
			}
			return s.materializePackedVariantViewValue(binding)
		}
		if ptr, valueType, err := s.emitIdentValueAddress(expr); err == nil {
			if enumType, ok := valueType.(*semantic.EnumType); ok && enumType == viewType.Enum && enumType.Packed {
				handle, err := s.loadValue(ptr, valueType, expr.Name)
				if err != nil {
					return nil, nil, err
				}
				var store *packedStoreBinding
				if enumType.StoreType != nil {
					resolvedStore, ok := s.lookupPackedStore(enumType)
					if ok {
						store = &resolvedStore
					}
				}
				value, err := s.buildPackedVariantViewValue(viewType, handle, store)
				if err != nil {
					return nil, nil, err
				}
				if store != nil {
					s.bindPackedVariantView(expr.Name, viewType, nil, handle, *store, packedPayloadValueCache{})
				} else {
					s.bindPackedVariantView(expr.Name, viewType, nil, handle, packedStoreBinding{}, packedPayloadValueCache{})
				}
				return value, viewType, nil
			}
		}
	}
	if ptr, valueType, err := s.emitIdentValueAddress(expr); err == nil {
		value, loadErr := s.loadValue(ptr, valueType, expr.Name)
		return value, valueType, loadErr
	}
	if binding, ok := s.lookupBinding(expr.Name); ok {
		value, err := s.loadValue(binding.ptr, binding.typ, expr.Name)
		return value, binding.typ, err
	}
	if sym, ok := s.g.result.GlobalScope.Lookup(expr.Name); ok {
		switch sym.Kind {
		case semantic.SymbolFunc, semantic.SymbolExternFunc:
			fnType, ok := sym.Type.(*semantic.FuncType)
			if !ok {
				return nil, nil, fmt.Errorf("global function %s is missing function type", expr.Name)
			}
			value, err := s.g.ensureFunctionDeclared(expr.Name, fnType)
			return value, fnType, err
		case semantic.SymbolGlobal, semantic.SymbolExternVar:
			global, err := s.g.ensureGlobalDeclared(expr.Name, sym.Type, sym.Kind == semantic.SymbolExternVar)
			if err != nil {
				return nil, nil, err
			}
			value, err := s.loadValue(global, sym.Type, expr.Name)
			return value, sym.Type, err
		case semantic.SymbolConst:
			if value, ok := s.g.constValue(expr.Name); ok {
				llvmValue, llvmType, err := s.emitConstValueWithType(value, sym.Type)
				return llvmValue, llvmType, err
			}
		}
	}
	if value, ok := s.g.constValue(expr.Name); ok {
		llvmValue, llvmType, err := s.emitConstValue(value)
		return llvmValue, llvmType, err
	}
	if s.typeMap != nil {
		if bound, ok := s.typeMap[expr.Name]; ok {
			if value, valueOK := bound.(*semantic.ConstValueType); valueOK {
				llvmValue, llvmType, err := s.emitConstValue(value.Value)
				return llvmValue, llvmType, err
			}
		}
	}
	fnName := "<unknown>"
	if s.decl != nil {
		fnName = s.decl.Name
	}
	return nil, nil, fmt.Errorf("unknown identifier %q during LLVM lowering in %s", expr.Name, fnName)
}
func (s *functionState) errorTagInfo(expr *ast.FieldExpr) (*semantic.ErrorSetType, string, bool) {
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, "", false
	}
	base, ok := s.g.result.NamedTypes[ident.Name]
	if !ok {
		return nil, "", false
	}
	errorType, ok := base.(*semantic.ErrorSetType)
	if !ok || !errorType.HasQualifiedTag(ident.Name, expr.Field) {
		return nil, "", false
	}
	return errorType, semantic.QualifyErrorTag(ident.Name, expr.Field), true
}
func (s *functionState) constEnumMemberInfo(expr *ast.FieldExpr) (*semantic.ConstEnumType, *semantic.ConstEnumMember, bool) {
	constEnumType, _, member, ok := s.constEnumMemberInfoForExpr(expr)
	if !ok {
		return nil, nil, false
	}
	return constEnumType, member, member != nil
}
