//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>

static int llcontextLLVMIsZeroValue(LLVMValueRef value) {
	return LLVMIsAConstant(value) != NULL && LLVMIsNull(value);
}

static LLVMMetadataRef llctxAliasMDString(LLVMContextRef ctx, const char* value) {
	if (value == NULL) {
		return LLVMMDStringInContext2(ctx, "", 0);
	}
	return LLVMMDStringInContext2(ctx, value, strlen(value));
}

static LLVMMetadataRef llctxAliasMDNode(LLVMContextRef ctx, LLVMMetadataRef* operands, size_t count) {
	return LLVMMDNodeInContext2(ctx, operands, count);
}

static unsigned llctxMetadataKindID(LLVMContextRef ctx, const char* kindName) {
	return LLVMGetMDKindIDInContext(ctx, kindName, strlen(kindName));
}

static void llctxSetMetadataList(LLVMValueRef inst, LLVMContextRef ctx, const char* kindName, LLVMMetadataRef* scopes, size_t count) {
	if (inst == NULL || ctx == NULL || kindName == NULL || count == 0) {
		return;
	}
	LLVMMetadataRef list = llctxAliasMDNode(ctx, scopes, count);
	LLVMValueRef listValue = LLVMMetadataAsValue(ctx, list);
	LLVMSetMetadata(inst, llctxMetadataKindID(ctx, kindName), listValue);
}

static LLVMMetadataRef llctxCreateAliasScopeDomain(LLVMContextRef ctx, const char* domainName) {
	LLVMMetadataRef operands[1];
	operands[0] = llctxAliasMDString(ctx, domainName);
	return llctxAliasMDNode(ctx, operands, 1);
}

static LLVMMetadataRef llctxCreateAliasScope(LLVMContextRef ctx, LLVMMetadataRef domain, const char* scopeName) {
	LLVMMetadataRef operands[2];
	operands[0] = llctxAliasMDString(ctx, scopeName);
	operands[1] = domain;
	return llctxAliasMDNode(ctx, operands, 2);
}

static void llctxAttachAliasScopeMetadata(LLVMValueRef inst, LLVMContextRef ctx, const char* domainName, const char* aliasScopeName,
	const char* noAliasScope1Name, int hasNoAliasScope1, const char* noAliasScope2Name, int hasNoAliasScope2) {
	if (inst == NULL || ctx == NULL || domainName == NULL || aliasScopeName == NULL) {
		return;
	}
	LLVMMetadataRef domain = llctxCreateAliasScopeDomain(ctx, domainName);
	LLVMMetadataRef aliasScope = llctxCreateAliasScope(ctx, domain, aliasScopeName);
	LLVMMetadataRef aliasScopes[1];
	aliasScopes[0] = aliasScope;
	llctxSetMetadataList(inst, ctx, "alias.scope", aliasScopes, 1);

	LLVMMetadataRef noAliasScopes[2];
	size_t noAliasCount = 0;
	if (hasNoAliasScope1 && noAliasScope1Name != NULL) {
		noAliasScopes[noAliasCount++] = llctxCreateAliasScope(ctx, domain, noAliasScope1Name);
	}
	if (hasNoAliasScope2 && noAliasScope2Name != NULL) {
		noAliasScopes[noAliasCount++] = llctxCreateAliasScope(ctx, domain, noAliasScope2Name);
	}
	if (noAliasCount != 0) {
		llctxSetMetadataList(inst, ctx, "noalias", noAliasScopes, noAliasCount);
	}
}
*/
import "C"

import (
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"llcontext/src/ast"
	"llcontext/src/lexer"
	"llcontext/src/semantic"
)

func llvmValueIsZeroConstant(value C.LLVMValueRef) bool {
	return value != nil && C.llcontextLLVMIsZeroValue(value) != 0
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
	C.llctxAttachAliasScopeMetadata(inst, s.g.context, domainNameC, aliasScopeNameC, noAliasScope1C, hasNoAliasScope1, noAliasScope2C, hasNoAliasScope2)
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
		value, actualType, err = s.emitStringLiteral(n)
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
			return s.materializePackedVariantViewValue(binding)
		}
		if ptr, valueType, err := s.emitIdentValueAddress(expr); err == nil {
			if enumType, ok := valueType.(*semantic.EnumType); ok && enumType == viewType.Enum && enumType.Packed {
				handle, err := s.loadValue(ptr, valueType, expr.Name)
				if err != nil {
					return nil, nil, err
				}
				store, ok := s.lookupPackedStore(enumType)
				if !ok {
					return nil, nil, fmt.Errorf("packedview %s requires store context for %q", viewType.String(), expr.Name)
				}
				value, err := s.buildPackedVariantViewValue(viewType, handle, &store)
				if err != nil {
					return nil, nil, err
				}
				s.bindPackedVariantView(expr.Name, viewType, nil, handle, store, packedPayloadValueCache{})
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
	return nil, nil, fmt.Errorf("unknown identifier %q during LLVM lowering", expr.Name)
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

func (s *functionState) constEnumMemberInfoForExpr(expr ast.Expr) (*semantic.ConstEnumType, string, *semantic.ConstEnumMember, bool) {
	parts, ok := qualifiedFieldParts(expr)
	if !ok || len(parts) < 2 {
		return nil, "", nil, false
	}
	fullName := strings.Join(parts, ".")
	if !ok || fullName == "" {
		return nil, "", nil, false
	}
	var matchedType *semantic.ConstEnumType
	var matchedMemberName string
	for i := len(parts) - 1; i >= 1; i-- {
		baseName := strings.Join(parts[:i], ".")
		base, ok := s.g.result.NamedTypes[baseName]
		if !ok {
			continue
		}
		constEnumType, ok := base.(*semantic.ConstEnumType)
		if !ok || constEnumType == nil {
			continue
		}
		memberName := strings.Join(parts[i:], ".")
		if matchedType == nil {
			matchedType = constEnumType
			matchedMemberName = memberName
		}
		if member, ok := constEnumType.Member(memberName); ok {
			return constEnumType, memberName, member, true
		}
	}
	if matchedType != nil {
		return matchedType, matchedMemberName, nil, true
	}
	return nil, "", nil, false
}

func (s *functionState) constEnumTypeForExpr(expr ast.Expr) (*semantic.ConstEnumType, bool) {
	if expr == nil {
		return nil, false
	}
	if constEnumType, ok := s.exprType(expr).(*semantic.ConstEnumType); ok {
		return constEnumType, true
	}
	switch n := expr.(type) {
	case *ast.Ident:
		base, ok := s.g.result.NamedTypes[n.Name]
		if !ok {
			return nil, false
		}
		constEnumType, ok := base.(*semantic.ConstEnumType)
		return constEnumType, ok
	case *ast.FieldExpr:
		if kindType, ok := s.treeCategoryKindTypeForExpr(n); ok {
			return kindType, true
		}
		ident, ok := n.Object.(*ast.Ident)
		if !ok || n.Field != "Tag" {
			return nil, false
		}
		base, ok := s.g.result.NamedTypes[ident.Name]
		if !ok {
			return nil, false
		}
		enumType, ok := base.(*semantic.EnumType)
		if !ok || !enumType.Packed || enumType.TagType == nil {
			return nil, false
		}
		return enumType.TagType, true
	case *ast.ParenExpr:
		return s.constEnumTypeForExpr(n.Inner)
	default:
		return nil, false
	}
}

func shorthandMemberNameBackend(expr *ast.ShorthandMemberExpr) string {
	if expr == nil {
		return ""
	}
	return strings.Join(expr.Parts, ".")
}

func shorthandMemberDisplayBackend(expr *ast.ShorthandMemberExpr) string {
	if expr == nil {
		return ".<invalid>"
	}
	return "." + shorthandMemberNameBackend(expr)
}

func (s *functionState) shorthandConstEnumMemberInfo(expr *ast.ShorthandMemberExpr) (*semantic.ConstEnumType, *semantic.ConstEnumMember, bool) {
	if expr == nil {
		return nil, nil, false
	}
	constEnumType, ok := s.exprType(expr).(*semantic.ConstEnumType)
	if !ok || constEnumType == nil {
		return nil, nil, false
	}
	member, ok := constEnumType.Member(shorthandMemberNameBackend(expr))
	if !ok || member == nil {
		return nil, nil, false
	}
	return constEnumType, member, true
}

func (s *functionState) treeCategoryKindTypeForExpr(expr *ast.FieldExpr) (*semantic.ConstEnumType, bool) {
	if expr == nil || expr.Field != "Kind" {
		return nil, false
	}
	ownerName, _, ok := qualifiedFieldOwnerAndLeaf(expr)
	if !ok {
		return nil, false
	}
	base, ok := s.g.result.NamedTypes[ownerName]
	if !ok {
		return nil, false
	}
	categoryType, ok := base.(*semantic.TreeCategoryType)
	if !ok || categoryType == nil || categoryType.KindType == nil {
		return nil, false
	}
	return categoryType.KindType, true
}

func (s *functionState) treeTypeForExpr(expr ast.Expr) (*semantic.TreeType, bool) {
	if expr == nil {
		return nil, false
	}
	if treeType, ok := s.exprType(expr).(*semantic.TreeType); ok {
		return treeType, true
	}
	switch n := expr.(type) {
	case *ast.Ident:
		base, ok := s.g.result.NamedTypes[n.Name]
		if !ok {
			return nil, false
		}
		treeType, ok := base.(*semantic.TreeType)
		return treeType, ok
	case *ast.ParenExpr:
		return s.treeTypeForExpr(n.Inner)
	default:
		return nil, false
	}
}

func (s *functionState) emitConstEnumMemberExpr(constEnumType *semantic.ConstEnumType, member *semantic.ConstEnumMember) (C.LLVMValueRef, semantic.Type, error) {
	if constEnumType == nil || member == nil {
		return nil, nil, fmt.Errorf("missing const enum member metadata")
	}
	llvmType, err := s.g.lowerType(constEnumType)
	if err != nil {
		return nil, nil, err
	}
	return C.LLVMConstInt(llvmType, C.ulonglong(member.Value), boolToLLVMBool(member.Value < 0)), constEnumType, nil
}

func (s *functionState) emitErrorTagExpr(expr *ast.FieldExpr, errorType *semantic.ErrorSetType) (C.LLVMValueRef, semantic.Type, error) {
	if errorType == nil {
		return nil, nil, fmt.Errorf("missing error set for tag expression")
	}
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, nil, fmt.Errorf("missing error set qualifier for tag expression")
	}
	code, ok := errorType.TagCodeFor(ident.Name, expr.Field)
	if !ok {
		return nil, nil, fmt.Errorf("unknown error tag %s.%s", errorType.Name, expr.Field)
	}
	value, err := s.errorCodeConstant(code)
	if err != nil {
		return nil, nil, err
	}
	return value, errorType, nil
}

func (s *functionState) emitRaiseExpr(expr *ast.RaiseExpr) (C.LLVMValueRef, semantic.Type, error) {
	currentUnion, ok := s.fnType.Return.(*semantic.ErrorUnionType)
	if !ok {
		return nil, nil, fmt.Errorf("raise requires an error-union return type")
	}
	var (
		errorValue C.LLVMValueRef
		errorType  semantic.Type
		err        error
	)
	if fieldExpr, ok := expr.Error.(*ast.FieldExpr); ok {
		if _, qualifiedTag, ok := s.errorTagInfo(fieldExpr); ok {
			mappedTag, matched := semantic.MatchErrorTag(currentUnion.Errors, qualifiedTag)
			if matched {
				code, ok := currentUnion.Errors.TagCode(mappedTag)
				if !ok {
					return nil, nil, fmt.Errorf("missing destination error tag %s", mappedTag)
				}
				errorValue, err = s.errorCodeConstant(code)
				errorType = currentUnion.Errors
			} else {
				errorValue, errorType, err = s.emitExpr(expr.Error, currentUnion.Errors)
			}
		} else {
			errorValue, errorType, err = s.emitExpr(expr.Error, currentUnion.Errors)
		}
	} else {
		errorValue, errorType, err = s.emitExpr(expr.Error, currentUnion.Errors)
	}
	if err != nil {
		return nil, nil, err
	}
	if err := s.emitFunctionReturn(errorValue, errorType); err != nil {
		return nil, nil, err
	}
	return nil, s.exprType(expr), nil
}

func (s *functionState) emitTryExpr(expr *ast.TryExpr) (C.LLVMValueRef, semantic.Type, error) {
	resultType := s.exprType(expr)
	if unionType, ok := s.exprType(expr.Value).(*semantic.ErrorUnionType); ok {
		fallibleValue, _, err := s.emitExpr(expr.Value, nil)
		if err != nil {
			return nil, nil, err
		}
		errorCode, err := s.extractErrorUnionCode(fallibleValue, unionType)
		if err != nil {
			return nil, nil, err
		}
		zeroCode, err := s.errorCodeConstant(0)
		if err != nil {
			return nil, nil, err
		}
		successCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), errorCode, zeroCode, cStringFree("try.ok"))

		if expr.Fallback == nil {
			okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.ok"))
			errBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.err"))
			C.LLVMBuildCondBr(s.builder, successCond, okBB, errBB)

			C.LLVMPositionBuilderAtEnd(s.builder, errBB)
			if _, ok := s.fnType.Return.(*semantic.ErrorUnionType); !ok {
				return nil, nil, fmt.Errorf("try propagation requires an error-union function return")
			}
			if err := s.emitFunctionReturn(errorCode, unionType.Errors); err != nil {
				return nil, nil, err
			}

			C.LLVMPositionBuilderAtEnd(s.builder, okBB)
			if isVoidType(resultType) {
				return nil, resultType, nil
			}
			payload, err := s.extractErrorUnionPayload(fallibleValue, unionType)
			if err != nil {
				return nil, nil, err
			}
			return payload, resultType, nil
		}

		okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.value"))
		fallbackBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.fallback"))
		mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.merge"))
		C.LLVMBuildCondBr(s.builder, successCond, okBB, fallbackBB)

		incomingValues := make([]C.LLVMValueRef, 0, 2)
		incomingBlocks := make([]C.LLVMBasicBlockRef, 0, 2)

		C.LLVMPositionBuilderAtEnd(s.builder, okBB)
		var okValue C.LLVMValueRef
		if !isVoidType(resultType) {
			okValue, err = s.extractErrorUnionPayload(fallibleValue, unionType)
			if err != nil {
				return nil, nil, err
			}
		}
		if !s.currentBlockTerminated() {
			okEnd := C.LLVMGetInsertBlock(s.builder)
			C.LLVMBuildBr(s.builder, mergeBB)
			if !isVoidType(resultType) {
				incomingValues = append(incomingValues, okValue)
				incomingBlocks = append(incomingBlocks, okEnd)
			}
		}

		C.LLVMPositionBuilderAtEnd(s.builder, fallbackBB)
		fallbackValue, _, err := s.emitExpr(expr.Fallback, resultType)
		if err != nil {
			return nil, nil, err
		}
		if !s.currentBlockTerminated() {
			fallbackEnd := C.LLVMGetInsertBlock(s.builder)
			C.LLVMBuildBr(s.builder, mergeBB)
			if !isVoidType(resultType) {
				incomingValues = append(incomingValues, fallbackValue)
				incomingBlocks = append(incomingBlocks, fallbackEnd)
			}
		}

		C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
		if len(incomingBlocks) == 0 {
			C.LLVMBuildUnreachable(s.builder)
			return nil, resultType, nil
		}
		if isVoidType(resultType) {
			return nil, resultType, nil
		}
		if len(incomingValues) == 1 {
			return incomingValues[0], resultType, nil
		}
		phiType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, nil, err
		}
		phi := C.LLVMBuildPhi(s.builder, phiType, cStringFree("tryphi"))
		C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
		return phi, resultType, nil
	}
	optionalType, ok := s.exprType(expr.Value).(*semantic.OptionalType)
	if !ok {
		return nil, nil, fmt.Errorf("try requires a lowered fallible operand")
	}
	if expr.Fallback == nil {
		return nil, nil, fmt.Errorf("try without else is only supported for error unions")
	}
	fallibleValue, _, err := s.emitExpr(expr.Value, nil)
	if err != nil {
		return nil, nil, err
	}
	presentValue, err := s.extractOptionalPresent(fallibleValue, optionalType)
	if err != nil {
		return nil, nil, err
	}
	okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.value"))
	fallbackBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.fallback"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("try.merge"))
	C.LLVMBuildCondBr(s.builder, presentValue, okBB, fallbackBB)

	incomingValues := make([]C.LLVMValueRef, 0, 2)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, 2)

	C.LLVMPositionBuilderAtEnd(s.builder, okBB)
	var okValue C.LLVMValueRef
	if !isVoidType(resultType) {
		okValue, err = s.extractOptionalPayload(fallibleValue, optionalType)
		if err != nil {
			return nil, nil, err
		}
	}
	if !s.currentBlockTerminated() {
		okEnd := C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
		if !isVoidType(resultType) {
			incomingValues = append(incomingValues, okValue)
			incomingBlocks = append(incomingBlocks, okEnd)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, fallbackBB)
	fallbackValue, _, err := s.emitExpr(expr.Fallback, resultType)
	if err != nil {
		return nil, nil, err
	}
	if !s.currentBlockTerminated() {
		fallbackEnd := C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
		if !isVoidType(resultType) {
			incomingValues = append(incomingValues, fallbackValue)
			incomingBlocks = append(incomingBlocks, fallbackEnd)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingBlocks) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	if isVoidType(resultType) {
		return nil, resultType, nil
	}
	if len(incomingValues) == 1 {
		return incomingValues[0], resultType, nil
	}
	phiType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, phiType, cStringFree("tryphi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}

func (s *functionState) emitCatchExpr(expr *ast.CatchExpr) (C.LLVMValueRef, semantic.Type, error) {
	resultType := s.exprType(expr)
	unionType, ok := s.exprType(expr.Value).(*semantic.ErrorUnionType)
	if !ok {
		return nil, nil, fmt.Errorf("catch requires an error-union operand")
	}
	fallibleValue, _, err := s.emitExpr(expr.Value, nil)
	if err != nil {
		return nil, nil, err
	}
	errorCode, err := s.extractErrorUnionCode(fallibleValue, unionType)
	if err != nil {
		return nil, nil, err
	}
	zeroCode, err := s.errorCodeConstant(0)
	if err != nil {
		return nil, nil, err
	}
	successCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), errorCode, zeroCode, cStringFree("catch.ok"))
	successBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("catch.value"))
	dispatchBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("catch.dispatch"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("catch.merge"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("catch.fail"))
	C.LLVMBuildCondBr(s.builder, successCond, successBB, dispatchBB)

	incomingValues := make([]C.LLVMValueRef, 0, len(expr.Arms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(expr.Arms)+1)
	covered := map[string]bool{}

	C.LLVMPositionBuilderAtEnd(s.builder, successBB)
	successValue, err := s.extractErrorUnionPayload(fallibleValue, unionType)
	if err != nil {
		return nil, nil, err
	}
	s.pushScope()
	if !isVoidType(unionType.Value) {
		successPtr, err := s.emitStackTempValue(successValue, unionType.Value, "catch.value")
		if err != nil {
			s.popScope()
			return nil, nil, err
		}
		s.defineBinding(expr.Success.Name, valueBinding{ptr: successPtr, typ: unionType.Value, mutable: false})
	}
	armValue, reachable, err := s.emitMatchExprArmBody(expr.Success.Body, resultType)
	if err != nil {
		s.popScope()
		return nil, nil, err
	}
	if reachable && !s.currentBlockTerminated() {
		armEnd := C.LLVMGetInsertBlock(s.builder)
		incomingBlocks = append(incomingBlocks, armEnd)
		if !isVoidType(resultType) {
			incomingValues = append(incomingValues, armValue)
		}
		C.LLVMBuildBr(s.builder, mergeBB)
	}
	s.popScope()

	C.LLVMPositionBuilderAtEnd(s.builder, dispatchBB)
	switchInst := C.LLVMBuildSwitch(s.builder, errorCode, failBB, C.unsigned(len(expr.Arms)))
	for _, arm := range expr.Arms {
		matchedTag, ok := semantic.MatchErrorTag(unionType.Errors, arm.Name)
		if !ok {
			return nil, nil, fmt.Errorf("catch arm %q does not match %s", arm.Name, semantic.ErrorSetDiagnosticName(unionType.Errors))
		}
		if covered[matchedTag] {
			continue
		}
		covered[matchedTag] = true
		code, ok := unionType.Errors.TagCode(matchedTag)
		if !ok {
			return nil, nil, fmt.Errorf("missing error tag code for %s", matchedTag)
		}
		tagConst, err := s.errorCodeConstant(code)
		if err != nil {
			return nil, nil, err
		}
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("catch.arm"))
		C.LLVMAddCase(switchInst, tagConst, bodyBB)
		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		armValue, reachable, err := s.emitMatchExprArmBody(arm.Body, resultType)
		if err != nil {
			s.popScope()
			return nil, nil, err
		}
		if reachable && !s.currentBlockTerminated() {
			armEnd := C.LLVMGetInsertBlock(s.builder)
			incomingBlocks = append(incomingBlocks, armEnd)
			if !isVoidType(resultType) {
				incomingValues = append(incomingValues, armValue)
			}
			C.LLVMBuildBr(s.builder, mergeBB)
		}
		s.popScope()
	}

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if len(covered) == len(unionType.Errors.Tags) || semantic.IsNeverType(resultType) {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		failEnd := C.LLVMGetInsertBlock(s.builder)
		incomingBlocks = append(incomingBlocks, failEnd)
		if !isVoidType(resultType) {
			llvmType, err := s.g.lowerType(resultType)
			if err != nil {
				return nil, nil, err
			}
			incomingValues = append(incomingValues, C.LLVMGetUndef(llvmType))
		}
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingBlocks) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	if isVoidType(resultType) {
		return nil, resultType, nil
	}
	if len(incomingValues) == 1 || semantic.IsNeverType(resultType) {
		return incomingValues[0], resultType, nil
	}
	phiType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, phiType, cStringFree("catchphi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}

func (s *functionState) emitUnwrapElseExpr(expr *ast.UnwrapElseExpr) (C.LLVMValueRef, semantic.Type, error) {
	valueType, ok := s.exprType(expr.Value).(*semantic.RefType)
	if !ok {
		return nil, nil, fmt.Errorf("else recovery requires a reference operand")
	}
	resultType := s.exprType(expr)
	value, _, err := s.emitExpr(expr.Value, valueType)
	if err != nil {
		return nil, nil, err
	}
	llvmRefType, err := s.g.lowerType(valueType)
	if err != nil {
		return nil, nil, err
	}
	nullValue := C.LLVMConstNull(llvmRefType)
	nonNullCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), value, nullValue, cStringFree("unwrap.nonnull"))
	okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("unwrap.ok"))
	fallbackBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("unwrap.fallback"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("unwrap.merge"))
	C.LLVMBuildCondBr(s.builder, nonNullCond, okBB, fallbackBB)

	incomingValues := make([]C.LLVMValueRef, 0, 2)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, 2)

	C.LLVMPositionBuilderAtEnd(s.builder, okBB)
	if !s.currentBlockTerminated() {
		okEnd := C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
		incomingValues = append(incomingValues, value)
		incomingBlocks = append(incomingBlocks, okEnd)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, fallbackBB)
	fallbackValue, _, err := s.emitExpr(expr.Fallback, resultType)
	if err != nil {
		return nil, nil, err
	}
	if !s.currentBlockTerminated() {
		fallbackEnd := C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
		incomingValues = append(incomingValues, fallbackValue)
		incomingBlocks = append(incomingBlocks, fallbackEnd)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingValues) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	if len(incomingValues) == 1 {
		return incomingValues[0], resultType, nil
	}
	phiType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, phiType, cStringFree("unwrapphi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}

func (s *functionState) emitIntLiteral(expr *ast.IntLit) (C.LLVMValueRef, semantic.Type, error) {
	t := s.exprType(expr)
	if t == nil {
		t = s.g.result.NamedTypes["int"]
	}
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := strconv.ParseUint(expr.Value, 0, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse integer literal %q: %w", expr.Value, err)
	}
	return C.LLVMConstInt(llvmType, C.ulonglong(parsed), 0), t, nil
}

func (s *functionState) emitFloatLiteral(expr *ast.FloatLit) (C.LLVMValueRef, semantic.Type, error) {
	t := s.exprType(expr)
	if t == nil {
		t = s.g.result.NamedTypes["f64"]
	}
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := strconv.ParseFloat(expr.Value, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse float literal %q: %w", expr.Value, err)
	}
	return C.LLVMConstReal(llvmType, C.double(parsed)), t, nil
}

func (s *functionState) emitStringLiteral(expr *ast.StringLit) (C.LLVMValueRef, semantic.Type, error) {
	name := cString("str")
	defer C.free(unsafe.Pointer(name))
	text := cString(expr.Value)
	defer C.free(unsafe.Pointer(text))
	value := C.LLVMBuildGlobalStringPtr(s.builder, text, name)
	return value, s.exprType(expr), nil
}

func (s *functionState) emitCharLiteral(expr *ast.CharLit) (C.LLVMValueRef, semantic.Type, error) {
	t := s.exprType(expr)
	if t == nil {
		t = s.g.result.NamedTypes["char"]
	}
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, nil, err
	}
	parsed, ok := semantic.ParseCharLiteral(expr)
	if !ok {
		return nil, nil, fmt.Errorf("failed to parse char literal %q", expr.Value)
	}
	return C.LLVMConstInt(llvmType, C.ulonglong(parsed), 0), t, nil
}

func (s *functionState) emitListLitExpr(expr *ast.ListLitExpr, expected semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if arrayType, ok, err := s.listLiteralTargetArrayType(expr, expected); err != nil {
		return nil, nil, err
	} else if ok {
		if arrayType.HasConstSize && arrayType.ConstSize != int64(len(expr.Elems)) {
			return nil, nil, fmt.Errorf("array literal resolved to %s but has %d elements", arrayType.String(), len(expr.Elems))
		}
		llvmType, err := s.g.lowerType(arrayType)
		if err != nil {
			return nil, nil, err
		}
		current := C.LLVMGetUndef(llvmType)
		for i, elem := range expr.Elems {
			elemValue, _, err := s.emitExpr(elem, arrayType.Elem)
			if err != nil {
				return nil, nil, err
			}
			current = C.LLVMBuildInsertValue(s.builder, current, elemValue, C.unsigned(i), cStringFree("arraylit.elem"))
		}
		return current, arrayType, nil
	}
	darrayType, err := s.listLiteralTargetDArrayType(expr, expected)
	if err != nil {
		return nil, nil, err
	}
	if len(expr.Elems) == 0 {
		zero, err := s.zeroValue(darrayType)
		if err != nil {
			return nil, nil, err
		}
		return zero, darrayType, nil
	}
	owner, ok := s.lookupTreeAllocOwner()
	if !ok || owner.arenaRef == nil {
		return nil, nil, fmt.Errorf("darray literal requires an active in <arena>: scope")
	}
	llvmType, err := s.g.lowerType(darrayType)
	if err != nil {
		return nil, nil, err
	}
	elemLLVMType, err := s.g.lowerType(darrayType.Elem)
	if err != nil {
		return nil, nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, err
	}
	elemSize, err := s.sizeOfType(darrayType.Elem)
	if err != nil {
		return nil, nil, err
	}
	byteCount := C.LLVMConstInt(usizeLLVMType, C.ulonglong(uint64(len(expr.Elems))*elemSize), 0)
	arenaType := s.g.result.NamedTypes["Arena"]
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return nil, nil, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, nil, err
	}
	allocPtr := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{owner.arenaRef, byteCount}, "darray.literal.alloc")
	indexLLVMType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, err
	}
	for i, elem := range expr.Elems {
		elemValue, _, err := s.emitExpr(elem, darrayType.Elem)
		if err != nil {
			return nil, nil, err
		}
		indexValue := C.LLVMConstInt(indexLLVMType, C.ulonglong(i), 0)
		indices := []C.LLVMValueRef{indexValue}
		elemPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, allocPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("darray.literal.elem.ptr"))
		C.LLVMBuildStore(s.builder, elemValue, elemPtr)
	}
	countValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(len(expr.Elems)), 0)
	current := C.LLVMGetUndef(llvmType)
	current = C.LLVMBuildInsertValue(s.builder, current, allocPtr, 0, cStringFree("darray.literal.items"))
	current = C.LLVMBuildInsertValue(s.builder, current, countValue, 1, cStringFree("darray.literal.count"))
	current = C.LLVMBuildInsertValue(s.builder, current, countValue, 2, cStringFree("darray.literal.capacity"))
	return current, darrayType, nil
}

func (s *functionState) listLiteralTargetArrayType(expr *ast.ListLitExpr, expected semantic.Type) (*semantic.ArrayType, bool, error) {
	if expectedArray, ok := expected.(*semantic.ArrayType); ok {
		return expectedArray, true, nil
	}
	actualArray, ok := s.exprType(expr).(*semantic.ArrayType)
	if !ok {
		return nil, false, nil
	}
	return actualArray, true, nil
}

func (s *functionState) listLiteralTargetDArrayType(expr *ast.ListLitExpr, expected semantic.Type) (*semantic.DArrayType, error) {
	if expectedDArray, ok := expected.(*semantic.DArrayType); ok {
		return expectedDArray, nil
	}
	actualDArray, ok := s.exprType(expr).(*semantic.DArrayType)
	if !ok {
		return nil, fmt.Errorf("list literal did not resolve to a fixed array or darray type")
	}
	return actualDArray, nil
}

func (s *functionState) emitStackTempZeroed(t semantic.Type, name string) (C.LLVMValueRef, error) {
	zero, err := s.zeroValue(t)
	if err != nil {
		return nil, err
	}
	return s.emitStackTempValue(zero, t, name)
}

func (s *functionState) emitStackTempValue(value C.LLVMValueRef, t semantic.Type, name string) (C.LLVMValueRef, error) {
	alloca, err := s.createEntryAlloca(name, t)
	if err != nil {
		return nil, err
	}
	C.LLVMBuildStore(s.builder, value, alloca)
	return alloca, nil
}

func (s *functionState) emitBoolLiteral(expr *ast.BoolLit) (C.LLVMValueRef, semantic.Type, error) {
	llvmType, err := s.g.lowerBuiltin("bool")
	if err != nil {
		return nil, nil, err
	}
	var raw C.ulonglong
	if expr.Value {
		raw = 1
	}
	return C.LLVMConstInt(llvmType, raw, 0), s.g.result.NamedTypes["bool"], nil
}

func (s *functionState) emitNullLiteral() (C.LLVMValueRef, semantic.Type, error) {
	ptrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	return C.LLVMConstNull(ptrType), &semantic.NullType{}, nil
}

func (s *functionState) emitBinaryExpr(expr *ast.BinaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr.LoweredCall != nil {
		return s.emitExpr(expr.LoweredCall, nil)
	}
	if expr.Op == lexer.TOKEN_IS {
		return s.emitIsExpr(expr)
	}
	if expr.Op == lexer.TOKEN_IN {
		return s.emitMembershipExpr(expr)
	}
	if expr.Op == lexer.TOKEN_AND || expr.Op == lexer.TOKEN_OR {
		return s.emitLogicalExpr(expr)
	}
	if helperName, firstType, secondType, swap, ok := runtimeStringCompareInfo(s.exprType(expr.Left), s.exprType(expr.Right)); ok && (expr.Op == lexer.TOKEN_EQEQ || expr.Op == lexer.TOKEN_BANGEQ) {
		return s.emitRuntimeStringCompareExpr(expr, helperName, firstType, secondType, swap)
	}
	leftType := s.exprType(expr.Left)
	rightType := s.exprType(expr.Right)
	resultType := s.exprType(expr)
	if value, actualType, handled, err := s.emitOptionalCompareExpr(expr, leftType, rightType, resultType); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitPointerCompareExpr(expr, leftType, rightType, resultType); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitPointerArithmeticExpr(expr, leftType, rightType, resultType); handled {
		return value, actualType, err
	}
	operandType := s.binaryOperandType(expr.Op, leftType, rightType)

	left, _, err := s.emitExpr(expr.Left, operandType)
	if err != nil {
		return nil, nil, err
	}
	right, _, err := s.emitExpr(expr.Right, operandType)
	if err != nil {
		return nil, nil, err
	}
	if enumType, ok := operandType.(*semantic.EnumType); ok && (expr.Op == lexer.TOKEN_EQEQ || expr.Op == lexer.TOKEN_BANGEQ) {
		return s.emitEnumCompareExpr(expr.Op, enumType, left, right, resultType)
	}

	switch expr.Op {
	case lexer.TOKEN_PLUS:
		if isFloatType(operandType) {
			return C.LLVMBuildFAdd(s.builder, left, right, cStringFree("addtmp")), resultType, nil
		}
		return C.LLVMBuildAdd(s.builder, left, right, cStringFree("addtmp")), resultType, nil
	case lexer.TOKEN_MINUS:
		if isFloatType(operandType) {
			return C.LLVMBuildFSub(s.builder, left, right, cStringFree("subtmp")), resultType, nil
		}
		return C.LLVMBuildSub(s.builder, left, right, cStringFree("subtmp")), resultType, nil
	case lexer.TOKEN_STAR:
		if isFloatType(operandType) {
			return C.LLVMBuildFMul(s.builder, left, right, cStringFree("multmp")), resultType, nil
		}
		return C.LLVMBuildMul(s.builder, left, right, cStringFree("multmp")), resultType, nil
	case lexer.TOKEN_SLASH:
		if isFloatType(operandType) {
			return C.LLVMBuildFDiv(s.builder, left, right, cStringFree("divtmp")), resultType, nil
		}
		if isSignedIntegerType(operandType) {
			return C.LLVMBuildSDiv(s.builder, left, right, cStringFree("divtmp")), resultType, nil
		}
		return C.LLVMBuildUDiv(s.builder, left, right, cStringFree("divtmp")), resultType, nil
	case lexer.TOKEN_PERCENT:
		if isSignedIntegerType(operandType) {
			return C.LLVMBuildSRem(s.builder, left, right, cStringFree("remtmp")), resultType, nil
		}
		return C.LLVMBuildURem(s.builder, left, right, cStringFree("remtmp")), resultType, nil
	case lexer.TOKEN_PIPE:
		return C.LLVMBuildOr(s.builder, left, right, cStringFree("ortmp")), resultType, nil
	case lexer.TOKEN_CARET:
		return C.LLVMBuildXor(s.builder, left, right, cStringFree("xortmp")), resultType, nil
	case lexer.TOKEN_AMPERSAND:
		return C.LLVMBuildAnd(s.builder, left, right, cStringFree("andtmp")), resultType, nil
	case lexer.TOKEN_LSHIFT:
		return C.LLVMBuildShl(s.builder, left, right, cStringFree("shltmp")), resultType, nil
	case lexer.TOKEN_RSHIFT:
		if isSignedIntegerType(operandType) {
			return C.LLVMBuildAShr(s.builder, left, right, cStringFree("shrtmp")), resultType, nil
		}
		return C.LLVMBuildLShr(s.builder, left, right, cStringFree("shrtmp")), resultType, nil
	case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ, lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
		if isFloatType(operandType) {
			pred, err := llvmFloatPredicate(expr.Op)
			if err != nil {
				return nil, nil, err
			}
			return C.LLVMBuildFCmp(s.builder, pred, left, right, cStringFree("cmptmp")), resultType, nil
		}
		pred, err := llvmIntPredicate(expr.Op, operandType)
		if err != nil {
			return nil, nil, err
		}
		return C.LLVMBuildICmp(s.builder, pred, left, right, cStringFree("cmptmp")), resultType, nil
	default:
		return nil, nil, fmt.Errorf("unsupported binary operator %s", lexer.TokenName(expr.Op))
	}
}

func (s *functionState) emitMembershipExpr(expr *ast.BinaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	list, ok := expr.Right.(*ast.ListLitExpr)
	if !ok || list == nil {
		return nil, nil, fmt.Errorf("membership operator requires a list literal on the right-hand side")
	}
	resultType := s.g.result.NamedTypes["bool"]
	boolLLVMType := C.LLVMInt1TypeInContext(s.g.context)
	if len(list.Elems) == 0 {
		return C.LLVMConstInt(boolLLVMType, 0, 0), resultType, nil
	}
	leftType := s.exprType(expr.Left)
	leftValue, _, err := s.emitExpr(expr.Left, leftType)
	if err != nil {
		return nil, nil, err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("membership.merge"))
	incomingValues := make([]C.LLVMValueRef, 0, len(list.Elems))
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(list.Elems))
	for i, elem := range list.Elems {
		currentBlock := C.LLVMGetInsertBlock(s.builder)
		cmp, err := s.emitMembershipCompareValueAndExpr(leftValue, leftType, elem)
		if err != nil {
			return nil, nil, err
		}
		if i == len(list.Elems)-1 {
			C.LLVMBuildBr(s.builder, mergeBB)
			incomingValues = append(incomingValues, cmp)
			incomingBlocks = append(incomingBlocks, currentBlock)
			break
		}
		nextBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(fmt.Sprintf("membership.next.%d", i)))
		C.LLVMBuildCondBr(s.builder, cmp, mergeBB, nextBB)
		incomingValues = append(incomingValues, C.LLVMConstInt(boolLLVMType, 1, 0))
		incomingBlocks = append(incomingBlocks, currentBlock)
		C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
	}
	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phi := C.LLVMBuildPhi(s.builder, boolLLVMType, cStringFree("membership.result"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}

func (s *functionState) emitMembershipCompareValueAndExpr(leftValue C.LLVMValueRef, leftType semantic.Type, rightExpr ast.Expr) (C.LLVMValueRef, error) {
	rightType := s.exprType(rightExpr)
	resultType := s.g.result.NamedTypes["bool"]
	if helperName, firstType, secondType, swap, ok := runtimeStringCompareInfo(leftType, rightType); ok {
		return s.emitRuntimeStringCompareValues(lexer.TOKEN_EQEQ, leftValue, leftType, rightExpr, helperName, firstType, secondType, swap)
	}
	if value, handled, err := s.emitMembershipOptionalCompareValueAndExpr(leftValue, leftType, rightExpr, rightType, resultType); handled {
		return value, err
	}
	if value, handled, err := s.emitMembershipPointerCompareValueAndExpr(leftValue, leftType, rightExpr, rightType, resultType); handled {
		return value, err
	}
	operandType := s.binaryOperandType(lexer.TOKEN_EQEQ, leftType, rightType)
	coercedLeft, err := s.coerceValue(leftValue, leftType, operandType)
	if err != nil {
		return nil, err
	}
	rightValue, _, err := s.emitExpr(rightExpr, operandType)
	if err != nil {
		return nil, err
	}
	if enumType, ok := operandType.(*semantic.EnumType); ok {
		cmp, _, err := s.emitEnumCompareExpr(lexer.TOKEN_EQEQ, enumType, coercedLeft, rightValue, resultType)
		return cmp, err
	}
	if isFloatType(operandType) {
		return C.LLVMBuildFCmp(s.builder, C.LLVMRealOEQ, coercedLeft, rightValue, cStringFree("membership.eq")), nil
	}
	return C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), coercedLeft, rightValue, cStringFree("membership.eq")), nil
}

func (s *functionState) emitMembershipPointerCompareValueAndExpr(leftValue C.LLVMValueRef, leftType semantic.Type, rightExpr ast.Expr, rightType semantic.Type, resultType semantic.Type) (C.LLVMValueRef, bool, error) {
	leftPointerish := isPointerLikeType(leftType) || semantic.IsNullType(leftType)
	rightPointerish := isPointerLikeType(rightType) || semantic.IsNullType(rightType)
	if !leftPointerish || !rightPointerish {
		return nil, false, nil
	}
	operandType := s.binaryOperandType(lexer.TOKEN_EQEQ, leftType, rightType)
	coercedLeft, err := s.coerceValue(leftValue, leftType, operandType)
	if err != nil {
		return nil, true, err
	}
	rightValue, _, err := s.emitExpr(rightExpr, operandType)
	if err != nil {
		return nil, true, err
	}
	cmp := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), coercedLeft, rightValue, cStringFree("membership.ptr.eq"))
	return cmp, resultType != nil, nil
}

func (s *functionState) emitMembershipOptionalCompareValueAndExpr(leftValue C.LLVMValueRef, leftType semantic.Type, rightExpr ast.Expr, rightType semantic.Type, resultType semantic.Type) (C.LLVMValueRef, bool, error) {
	if leftOptional, ok := leftType.(*semantic.OptionalType); ok && semantic.IsNullType(rightType) {
		presentValue, err := s.extractOptionalPresent(leftValue, leftOptional)
		if err != nil {
			return nil, true, err
		}
		return C.LLVMBuildNot(s.builder, presentValue, cStringFree("membership.optional.isnull")), resultType != nil, nil
	}
	if rightOptional, ok := rightType.(*semantic.OptionalType); ok && semantic.IsNullType(leftType) {
		rightValue, _, err := s.emitExpr(rightExpr, rightOptional)
		if err != nil {
			return nil, true, err
		}
		presentValue, err := s.extractOptionalPresent(rightValue, rightOptional)
		if err != nil {
			return nil, true, err
		}
		return C.LLVMBuildNot(s.builder, presentValue, cStringFree("membership.optional.isnull")), resultType != nil, nil
	}
	return nil, false, nil
}

func (s *functionState) emitRuntimeStringCompareValues(op lexer.TokenKind, leftValue C.LLVMValueRef, leftType semantic.Type, rightExpr ast.Expr, helperName string, firstType semantic.Type, secondType semantic.Type, swap bool) (C.LLVMValueRef, error) {
	var (
		firstValue  C.LLVMValueRef
		secondValue C.LLVMValueRef
		err         error
	)
	if swap {
		firstValue, _, err = s.emitExpr(rightExpr, firstType)
		if err != nil {
			return nil, err
		}
		secondValue, err = s.coerceValue(leftValue, leftType, secondType)
		if err != nil {
			return nil, err
		}
	} else {
		firstValue, err = s.coerceValue(leftValue, leftType, firstType)
		if err != nil {
			return nil, err
		}
		secondValue, _, err = s.emitExpr(rightExpr, secondType)
		if err != nil {
			return nil, err
		}
	}
	helperReturn := s.g.result.NamedTypes["int"]
	helperType := &semantic.FuncType{
		Name:   helperName,
		Params: []semantic.Type{firstType, secondType},
		Return: helperReturn,
	}
	callee, err := s.g.ensureFunctionDeclared(helperName, helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	call := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{firstValue, secondValue}, "membership.strcmp")
	helperLLVMType, err := s.g.lowerType(helperReturn)
	if err != nil {
		return nil, err
	}
	zero := C.LLVMConstInt(helperLLVMType, 0, 0)
	pred := C.LLVMIntPredicate(C.LLVMIntNE)
	if op == lexer.TOKEN_BANGEQ {
		pred = C.LLVMIntPredicate(C.LLVMIntEQ)
	}
	return C.LLVMBuildICmp(s.builder, pred, call, zero, cStringFree("membership.strcmp.eq")), nil
}

func (s *functionState) emitIsExpr(expr *ast.BinaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	targets := flattenIsTargetExprsBackend(expr.Right)
	if len(targets) == 0 {
		return nil, nil, fmt.Errorf("is expression is missing a target")
	}
	var combined C.LLVMValueRef
	for i, target := range targets {
		var (
			value C.LLVMValueRef
			err   error
		)
		if treeType, variant, pattern, ok := s.treeIsTargetPattern(target); ok {
			value, _, err = s.emitTreeIsTest(expr.Left, treeType, variant, pattern)
		} else if pattern, ok := s.structIsTargetPattern(target); ok {
			value, _, err = s.emitStructIsTest(expr.Left, pattern)
		} else if enumType, variant, pattern, ok := s.enumIsTargetPattern(target); ok {
			value, _, err = s.emitEnumIsTest(expr.Left, enumType, variant, pattern)
		} else if base, cases, ok := s.namedStateIsTarget(target); ok {
			value, _, err = s.emitNamedStateIsTest(expr.Left, base, cases)
		} else {
			value, _, err = s.emitComparableIsTargetTest(expr.Left, target)
		}
		if err != nil {
			return nil, nil, err
		}
		if i == 0 {
			combined = value
			continue
		}
		combined = C.LLVMBuildOr(s.builder, combined, value, cStringFree("istest.or"))
	}
	return combined, s.g.result.NamedTypes["bool"], nil
}

func appendIsTargetExprsBackend(out []ast.Expr, expr ast.Expr) []ast.Expr {
	if expr == nil {
		return out
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return appendIsTargetExprsBackend(out, n.Inner)
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			out = appendIsTargetExprsBackend(out, target)
		}
		return out
	default:
		return append(out, expr)
	}
}

func flattenIsTargetExprsBackend(expr ast.Expr) []ast.Expr {
	return appendIsTargetExprsBackend(nil, expr)
}

func (s *functionState) emitComparableIsTargetTest(leftExpr ast.Expr, targetExpr ast.Expr) (C.LLVMValueRef, semantic.Type, error) {
	leftType := s.exprType(leftExpr)
	rightType := s.exprType(targetExpr)
	resultType := s.g.result.NamedTypes["bool"]
	synthetic := &ast.BinaryExpr{Position: leftExpr.Pos(), Op: lexer.TOKEN_EQEQ, Left: leftExpr, Right: targetExpr}
	if helperName, firstType, secondType, swap, ok := runtimeStringCompareInfo(leftType, rightType); ok {
		return s.emitRuntimeStringCompareExpr(synthetic, helperName, firstType, secondType, swap)
	}
	if value, actualType, handled, err := s.emitOptionalCompareExpr(synthetic, leftType, rightType, resultType); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitPointerCompareExpr(synthetic, leftType, rightType, resultType); handled {
		return value, actualType, err
	}
	operandType := s.binaryOperandType(lexer.TOKEN_EQEQ, leftType, rightType)
	left, _, err := s.emitExpr(leftExpr, operandType)
	if err != nil {
		return nil, nil, err
	}
	right, _, err := s.emitExpr(targetExpr, operandType)
	if err != nil {
		return nil, nil, err
	}
	if enumType, ok := operandType.(*semantic.EnumType); ok {
		return s.emitEnumCompareExpr(lexer.TOKEN_EQEQ, enumType, left, right, resultType)
	}
	if isFloatType(operandType) {
		return C.LLVMBuildFCmp(s.builder, C.LLVMRealOEQ, left, right, cStringFree("isvalue.eq")), resultType, nil
	}
	return C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), left, right, cStringFree("isvalue.eq")), resultType, nil
}

func (s *functionState) emitEnumIsTest(leftExpr ast.Expr, enumType *semantic.EnumType, variant *semantic.EnumVariant, pattern *ast.MatchVariantPattern) (C.LLVMValueRef, semantic.Type, error) {
	storeBinding, err := s.resolvePackedMatchStoreBinding(enumType, leftExpr, nil)
	if err != nil {
		return nil, nil, err
	}
	enumValue, _, err := s.emitExpr(leftExpr, enumType)
	if err != nil {
		return nil, nil, err
	}
	if pattern != nil && len(pattern.Args) != 0 {
		successBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.variant.ok"))
		failureBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.variant.fail"))
		contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.variant.cont"))
		if _, _, err := s.emitMatchPatternTest(pattern, enumValue, nil, enumType, storeBinding, leftExpr, nil, successBB, failureBB); err != nil {
			return nil, nil, err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, successBB)
		successValue := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0)
		C.LLVMBuildBr(s.builder, contBB)
		successEnd := C.LLVMGetInsertBlock(s.builder)

		C.LLVMPositionBuilderAtEnd(s.builder, failureBB)
		failureValue := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 0, 0)
		C.LLVMBuildBr(s.builder, contBB)
		failureEnd := C.LLVMGetInsertBlock(s.builder)

		C.LLVMPositionBuilderAtEnd(s.builder, contBB)
		phi := C.LLVMBuildPhi(s.builder, C.LLVMInt1TypeInContext(s.g.context), cStringFree("is.variant.result"))
		values := []C.LLVMValueRef{successValue, failureValue}
		blocks := []C.LLVMBasicBlockRef{successEnd, failureEnd}
		C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
		return phi, s.g.result.NamedTypes["bool"], nil
	}
	tagValue, err := s.extractEnumTagValue(enumValue, nil, enumType, storeBinding)
	if err != nil {
		return nil, nil, err
	}
	tagConst, err := s.enumTagConstant(variant.Tag)
	if err != nil {
		return nil, nil, err
	}
	cmp := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), tagValue, tagConst, cStringFree("istag"))
	return cmp, s.g.result.NamedTypes["bool"], nil
}

func (s *functionState) structIsTargetPattern(expr ast.Expr) (*ast.MatchStructPattern, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return s.structIsTargetPattern(paren.Inner)
	}
	if testExpr, ok := expr.(*ast.StructTestExpr); ok && testExpr != nil && testExpr.Pattern != nil {
		return testExpr.Pattern, true
	}
	return nil, false
}

func (s *functionState) emitStructIsTest(leftExpr ast.Expr, pattern *ast.MatchStructPattern) (C.LLVMValueRef, semantic.Type, error) {
	leftType := s.exprType(leftExpr)
	value, _, err := s.emitExpr(leftExpr, leftType)
	if err != nil {
		return nil, nil, err
	}
	successBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.struct.ok"))
	failureBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.struct.fail"))
	contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.struct.cont"))
	if _, _, err := s.emitMatchPatternTest(pattern, value, nil, leftType, nil, leftExpr, nil, successBB, failureBB); err != nil {
		return nil, nil, err
	}

	C.LLVMPositionBuilderAtEnd(s.builder, successBB)
	successValue := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0)
	C.LLVMBuildBr(s.builder, contBB)
	successEnd := C.LLVMGetInsertBlock(s.builder)

	C.LLVMPositionBuilderAtEnd(s.builder, failureBB)
	failureValue := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 0, 0)
	C.LLVMBuildBr(s.builder, contBB)
	failureEnd := C.LLVMGetInsertBlock(s.builder)

	C.LLVMPositionBuilderAtEnd(s.builder, contBB)
	phi := C.LLVMBuildPhi(s.builder, C.LLVMInt1TypeInContext(s.g.context), cStringFree("is.struct.result"))
	values := []C.LLVMValueRef{successValue, failureValue}
	blocks := []C.LLVMBasicBlockRef{successEnd, failureEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, s.g.result.NamedTypes["bool"], nil
}

func (s *functionState) enumIsTargetPattern(expr ast.Expr) (*semantic.EnumType, *semantic.EnumVariant, *ast.MatchVariantPattern, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return s.enumIsTargetPattern(paren.Inner)
	}
	if testExpr, ok := expr.(*ast.VariantTestExpr); ok {
		if testExpr == nil || testExpr.Pattern == nil {
			return nil, nil, nil, false
		}
		base, ok := s.g.result.NamedTypes[testExpr.Pattern.EnumName]
		if !ok {
			return nil, nil, nil, false
		}
		enumType, ok := base.(*semantic.EnumType)
		if !ok || enumType == nil {
			return nil, nil, nil, false
		}
		variant, ok := enumType.Variant(testExpr.Pattern.Variant)
		if !ok || variant == nil {
			return nil, nil, nil, false
		}
		return enumType, variant, testExpr.Pattern, true
	}
	enumType, variant, ok := s.enumIsTarget(expr)
	if !ok || enumType == nil || variant == nil {
		return nil, nil, nil, false
	}
	pattern := &ast.MatchVariantPattern{Position: expr.Pos(), EnumName: enumType.Name, Variant: variant.Name}
	return enumType, variant, pattern, true
}

func (s *functionState) emitNamedStateIsTest(leftExpr ast.Expr, base *semantic.StructType, cases []string) (C.LLVMValueRef, semantic.Type, error) {
	if base == nil {
		return nil, nil, fmt.Errorf("missing named-state struct metadata")
	}
	if len(cases) == 0 {
		return C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 0, 0), s.g.result.NamedTypes["bool"], nil
	}
	if len(cases) == len(base.NamedStateCases) {
		return C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0), s.g.result.NamedTypes["bool"], nil
	}
	selfType := s.exprType(leftExpr)
	selfValue, _, err := s.emitExpr(leftExpr, selfType)
	if err != nil {
		return nil, nil, err
	}
	var combined C.LLVMValueRef
	for i, stateName := range cases {
		pred, err := s.emitDerivedStatePredicate(base, stateName, selfValue, selfType)
		if err != nil {
			return nil, nil, err
		}
		if i == 0 {
			combined = pred
			continue
		}
		combined = C.LLVMBuildOr(s.builder, combined, pred, cStringFree("isstate.or"))
	}
	return combined, s.g.result.NamedTypes["bool"], nil
}

func (s *functionState) emitDerivedStatePredicate(base *semantic.StructType, stateName string, selfValue C.LLVMValueRef, selfType semantic.Type) (C.LLVMValueRef, error) {
	if base == nil || base.DerivedStateMap == nil {
		return nil, fmt.Errorf("struct %q is missing derived state metadata", base.Name)
	}
	derived := base.DerivedStateMap[stateName]
	if derived == nil || derived.Condition == nil {
		return nil, fmt.Errorf("struct %q has no derived state rule for %q", base.Name, stateName)
	}
	value, resultType, err := s.emitDerivedStateExpr(derived.Condition, selfValue, selfType)
	if err != nil {
		return nil, err
	}
	if !semantic.IsBoolType(resultType) {
		return nil, fmt.Errorf("derived state %s.%s does not evaluate to bool", base.Name, stateName)
	}
	return value, nil
}

func (s *functionState) emitDerivedStateExpr(expr ast.Expr, selfValue C.LLVMValueRef, selfType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if path, ok := derivedStateSelfFieldPath(expr); ok {
		value, fieldType, err := s.emitDerivedStateSelfPath(selfValue, selfType, path)
		return value, fieldType, err
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.emitDerivedStateExpr(n.Inner, selfValue, selfType)
	case *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.CharLit, *ast.BoolLit, *ast.NullLit:
		return s.emitExpr(expr, nil)
	case *ast.UnaryExpr:
		operand, operandType, err := s.emitDerivedStateExpr(n.Operand, selfValue, selfType)
		if err != nil {
			return nil, nil, err
		}
		switch n.Op {
		case lexer.TOKEN_NOT:
			return C.LLVMBuildNot(s.builder, operand, cStringFree("isstate.not")), s.g.result.NamedTypes["bool"], nil
		case lexer.TOKEN_MINUS:
			if isFloatType(operandType) {
				return C.LLVMBuildFNeg(s.builder, operand, cStringFree("isstate.fneg")), operandType, nil
			}
			return C.LLVMBuildNeg(s.builder, operand, cStringFree("isstate.neg")), operandType, nil
		case lexer.TOKEN_TILDE:
			return C.LLVMBuildNot(s.builder, operand, cStringFree("isstate.bitnot")), operandType, nil
		default:
			return nil, nil, fmt.Errorf("unsupported derived-state unary operator %s", lexer.TokenName(n.Op))
		}
	case *ast.BinaryExpr:
		leftValue, leftType, err := s.emitDerivedStateExpr(n.Left, selfValue, selfType)
		if err != nil {
			return nil, nil, err
		}
		rightValue, rightType, err := s.emitDerivedStateExpr(n.Right, selfValue, selfType)
		if err != nil {
			return nil, nil, err
		}
		if n.Op == lexer.TOKEN_AND || n.Op == lexer.TOKEN_OR {
			if n.Op == lexer.TOKEN_AND {
				return C.LLVMBuildAnd(s.builder, leftValue, rightValue, cStringFree("isstate.and")), s.g.result.NamedTypes["bool"], nil
			}
			return C.LLVMBuildOr(s.builder, leftValue, rightValue, cStringFree("isstate.or")), s.g.result.NamedTypes["bool"], nil
		}
		if (n.Op == lexer.TOKEN_EQEQ || n.Op == lexer.TOKEN_BANGEQ) && (isPointerLikeType(leftType) || semantic.IsNullType(leftType)) && (isPointerLikeType(rightType) || semantic.IsNullType(rightType)) {
			pred := C.LLVMIntPredicate(C.LLVMIntEQ)
			if n.Op == lexer.TOKEN_BANGEQ {
				pred = C.LLVMIntPredicate(C.LLVMIntNE)
			}
			return C.LLVMBuildICmp(s.builder, pred, leftValue, rightValue, cStringFree("isstate.ptrcmp")), s.g.result.NamedTypes["bool"], nil
		}
		if semantic.IsBoolType(leftType) && semantic.IsBoolType(rightType) && (n.Op == lexer.TOKEN_EQEQ || n.Op == lexer.TOKEN_BANGEQ) {
			pred := C.LLVMIntPredicate(C.LLVMIntEQ)
			if n.Op == lexer.TOKEN_BANGEQ {
				pred = C.LLVMIntPredicate(C.LLVMIntNE)
			}
			return C.LLVMBuildICmp(s.builder, pred, leftValue, rightValue, cStringFree("isstate.boolcmp")), s.g.result.NamedTypes["bool"], nil
		}
		operandType := s.binaryOperandType(n.Op, leftType, rightType)
		coercedLeft, err := s.coerceValue(leftValue, leftType, operandType)
		if err != nil {
			return nil, nil, err
		}
		coercedRight, err := s.coerceValue(rightValue, rightType, operandType)
		if err != nil {
			return nil, nil, err
		}
		resultType := s.exprType(n)
		if resultType == nil {
			resultType = operandType
		}
		switch n.Op {
		case lexer.TOKEN_PLUS:
			if isFloatType(operandType) {
				return C.LLVMBuildFAdd(s.builder, coercedLeft, coercedRight, cStringFree("isstate.add")), resultType, nil
			}
			return C.LLVMBuildAdd(s.builder, coercedLeft, coercedRight, cStringFree("isstate.add")), resultType, nil
		case lexer.TOKEN_MINUS:
			if isFloatType(operandType) {
				return C.LLVMBuildFSub(s.builder, coercedLeft, coercedRight, cStringFree("isstate.sub")), resultType, nil
			}
			return C.LLVMBuildSub(s.builder, coercedLeft, coercedRight, cStringFree("isstate.sub")), resultType, nil
		case lexer.TOKEN_STAR:
			if isFloatType(operandType) {
				return C.LLVMBuildFMul(s.builder, coercedLeft, coercedRight, cStringFree("isstate.mul")), resultType, nil
			}
			return C.LLVMBuildMul(s.builder, coercedLeft, coercedRight, cStringFree("isstate.mul")), resultType, nil
		case lexer.TOKEN_SLASH:
			if isFloatType(operandType) {
				return C.LLVMBuildFDiv(s.builder, coercedLeft, coercedRight, cStringFree("isstate.div")), resultType, nil
			}
			if isSignedIntegerType(operandType) {
				return C.LLVMBuildSDiv(s.builder, coercedLeft, coercedRight, cStringFree("isstate.div")), resultType, nil
			}
			return C.LLVMBuildUDiv(s.builder, coercedLeft, coercedRight, cStringFree("isstate.div")), resultType, nil
		case lexer.TOKEN_PERCENT:
			if isSignedIntegerType(operandType) {
				return C.LLVMBuildSRem(s.builder, coercedLeft, coercedRight, cStringFree("isstate.rem")), resultType, nil
			}
			return C.LLVMBuildURem(s.builder, coercedLeft, coercedRight, cStringFree("isstate.rem")), resultType, nil
		case lexer.TOKEN_PIPE:
			return C.LLVMBuildOr(s.builder, coercedLeft, coercedRight, cStringFree("isstate.bitor")), resultType, nil
		case lexer.TOKEN_CARET:
			return C.LLVMBuildXor(s.builder, coercedLeft, coercedRight, cStringFree("isstate.bitxor")), resultType, nil
		case lexer.TOKEN_AMPERSAND:
			return C.LLVMBuildAnd(s.builder, coercedLeft, coercedRight, cStringFree("isstate.bitand")), resultType, nil
		case lexer.TOKEN_LSHIFT:
			return C.LLVMBuildShl(s.builder, coercedLeft, coercedRight, cStringFree("isstate.shl")), resultType, nil
		case lexer.TOKEN_RSHIFT:
			if isSignedIntegerType(operandType) {
				return C.LLVMBuildAShr(s.builder, coercedLeft, coercedRight, cStringFree("isstate.shr")), resultType, nil
			}
			return C.LLVMBuildLShr(s.builder, coercedLeft, coercedRight, cStringFree("isstate.shr")), resultType, nil
		case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ, lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
			if isFloatType(operandType) {
				pred, err := llvmFloatPredicate(n.Op)
				if err != nil {
					return nil, nil, err
				}
				return C.LLVMBuildFCmp(s.builder, pred, coercedLeft, coercedRight, cStringFree("isstate.fcmp")), s.g.result.NamedTypes["bool"], nil
			}
			pred, err := llvmIntPredicate(n.Op, operandType)
			if err != nil {
				return nil, nil, err
			}
			return C.LLVMBuildICmp(s.builder, pred, coercedLeft, coercedRight, cStringFree("isstate.icmp")), s.g.result.NamedTypes["bool"], nil
		default:
			return nil, nil, fmt.Errorf("unsupported derived-state binary operator %s", lexer.TokenName(n.Op))
		}
	default:
		return nil, nil, fmt.Errorf("unsupported derived-state expression %T", expr)
	}
}

func (s *functionState) emitDerivedStateSelfPath(selfValue C.LLVMValueRef, selfType semantic.Type, path []string) (C.LLVMValueRef, semantic.Type, error) {
	currentValue := selfValue
	currentType := selfType
	if len(path) == 0 {
		return currentValue, currentType, nil
	}
	for _, field := range path {
		fieldType, index, _, pointerLike, err := s.g.fieldInfo(currentType, field)
		if err != nil {
			return nil, nil, err
		}
		if pointerLike {
			return nil, nil, fmt.Errorf("derived-state field access over pointer-like self path is not supported")
		}
		currentValue = C.LLVMBuildExtractValue(s.builder, currentValue, C.unsigned(index), cStringFree("isstate.field."+field))
		currentType = fieldType
	}
	return currentValue, currentType, nil
}

func (s *functionState) enumIsTarget(expr ast.Expr) (*semantic.EnumType, *semantic.EnumVariant, bool) {
	if fieldExpr, ok := expr.(*ast.FieldExpr); ok {
		return s.enumConstructorInfoFromField(fieldExpr)
	}
	typedExpr, ok := expr.(*ast.TypeExprExpr)
	if !ok || typedExpr == nil || typedExpr.Type == nil {
		return nil, nil, false
	}
	named, ok := typedExpr.Type.(*ast.NamedType)
	if !ok || named == nil {
		return nil, nil, false
	}
	idx := strings.LastIndex(named.Name, ".")
	if idx <= 0 || idx+1 >= len(named.Name) {
		return nil, nil, false
	}
	enumName := named.Name[:idx]
	variantName := named.Name[idx+1:]
	base, ok := s.g.result.NamedTypes[enumName]
	if !ok {
		return nil, nil, false
	}
	enumType, ok := base.(*semantic.EnumType)
	if !ok || enumType == nil {
		return nil, nil, false
	}
	variant, ok := enumType.Variant(variantName)
	if !ok {
		return enumType, nil, false
	}
	return enumType, variant, true
}

func (s *functionState) namedStateIsTarget(expr ast.Expr) (*semantic.StructType, []string, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return s.namedStateIsTarget(paren.Inner)
	}
	typedExpr, ok := expr.(*ast.TypeExprExpr)
	if !ok || typedExpr == nil || typedExpr.Type == nil {
		return nil, nil, false
	}
	switch n := typedExpr.Type.(type) {
	case *ast.NamedType:
		base, ok := s.g.result.NamedTypes[n.Name]
		if !ok {
			return nil, nil, false
		}
		structType, ok := base.(*semantic.StructType)
		if !ok || structType == nil || len(structType.NamedStateCases) == 0 {
			return nil, nil, false
		}
		return structType, append([]string(nil), structType.NamedStateCases...), true
	case *ast.GenericType:
		base, ok := s.g.result.NamedTypes[n.Name]
		if !ok {
			return nil, nil, false
		}
		structType, ok := base.(*semantic.StructType)
		if !ok || structType == nil || len(structType.NamedStateCases) == 0 {
			return nil, nil, false
		}
		params := structGenericParams(structType)
		if len(n.Args) != len(params) {
			return nil, nil, false
		}
		for i, param := range params {
			if param.Kind != ast.GenericParamState {
				continue
			}
			cases, ok := namedStateCasesFromTypeExpr(n.Args[i])
			if !ok {
				return nil, nil, false
			}
			return structType, cases, true
		}
		return nil, nil, false
	default:
		return nil, nil, false
	}
}

func namedStateCasesFromTypeExpr(expr ast.TypeExpr) ([]string, bool) {
	switch n := expr.(type) {
	case *ast.NamedType:
		return []string{n.Name}, true
	case *ast.StateSetTypeExpr:
		return append([]string(nil), n.Cases...), true
	default:
		return nil, false
	}
}

func derivedStateSelfFieldPath(expr ast.Expr) ([]string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n.Name == "self" {
			return nil, true
		}
		return nil, false
	case *ast.FieldExpr:
		path, ok := derivedStateSelfFieldPath(n.Object)
		if !ok {
			return nil, false
		}
		return append(path, n.Field), true
	case *ast.ParenExpr:
		return derivedStateSelfFieldPath(n.Inner)
	default:
		return nil, false
	}
}

func (s *functionState) emitEnumCompareExpr(op lexer.TokenKind, enumType *semantic.EnumType, left C.LLVMValueRef, right C.LLVMValueRef, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if enumType == nil {
		return nil, nil, fmt.Errorf("missing enum type for comparison")
	}
	if enumType.Packed {
		pred := C.LLVMIntPredicate(C.LLVMIntEQ)
		if op == lexer.TOKEN_BANGEQ {
			pred = C.LLVMIntPredicate(C.LLVMIntNE)
		}
		return C.LLVMBuildICmp(s.builder, pred, left, right, cStringFree("enumcmp.packed")), resultType, nil
	}
	if enumIsTagOnly(enumType) {
		pred := C.LLVMIntPredicate(C.LLVMIntEQ)
		if op == lexer.TOKEN_BANGEQ {
			pred = C.LLVMIntPredicate(C.LLVMIntNE)
		}
		return C.LLVMBuildICmp(s.builder, pred, left, right, cStringFree("enumcmp.tagonly")), resultType, nil
	}
	leftTag := C.LLVMBuildExtractValue(s.builder, left, 0, cStringFree("enumcmp.left.tag"))
	rightTag := C.LLVMBuildExtractValue(s.builder, right, 0, cStringFree("enumcmp.right.tag"))
	equal := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftTag, rightTag, cStringFree("enumcmp.tag.eq"))

	payloadSlots, err := s.enumPayloadWordCount(enumType)
	if err != nil {
		return nil, nil, err
	}
	if payloadSlots > 0 {
		leftPayload := C.LLVMBuildExtractValue(s.builder, left, 1, cStringFree("enumcmp.left.payload"))
		rightPayload := C.LLVMBuildExtractValue(s.builder, right, 1, cStringFree("enumcmp.right.payload"))
		for i := uint64(0); i < payloadSlots; i++ {
			nameSuffix := fmt.Sprintf(".%d", i)
			leftWord := C.LLVMBuildExtractValue(s.builder, leftPayload, C.unsigned(i), cStringFree("enumcmp.left.word"+nameSuffix))
			rightWord := C.LLVMBuildExtractValue(s.builder, rightPayload, C.unsigned(i), cStringFree("enumcmp.right.word"+nameSuffix))
			wordEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftWord, rightWord, cStringFree("enumcmp.word.eq"+nameSuffix))
			equal = C.LLVMBuildAnd(s.builder, equal, wordEqual, cStringFree("enumcmp.and"+nameSuffix))
		}
	}
	if op == lexer.TOKEN_BANGEQ {
		return C.LLVMBuildNot(s.builder, equal, cStringFree("enumcmp.ne")), resultType, nil
	}
	return equal, resultType, nil
}

func (s *functionState) encodePackedEnumHandle(rowPtr C.LLVMValueRef, enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	return s.encodePackedEnumHandleWithStore(rowPtr, enumType, nil)
}

func (s *functionState) encodePackedEnumHandleWithStore(rowPtr C.LLVMValueRef, enumType *semantic.EnumType, storeValue C.LLVMValueRef) (C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum handle metadata")
	}
	storeType := enumType.StoreType
	if storeType == nil {
		return nil, fmt.Errorf("packed enum %s is missing store metadata", enumType.Name)
	}
	if storeValue == nil {
		return nil, fmt.Errorf("packed enum %s encode requires store context", enumType.Name)
	}
	return (&packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}).encodeHandle(rowPtr, enumType, "packed.encode.store")
}

func (s *functionState) decodePackedEnumHandle(handleValue C.LLVMValueRef, enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	return s.decodePackedEnumHandleWithStore(handleValue, enumType, nil)
}

func (s *functionState) decodePackedEnumHandleWithStore(handleValue C.LLVMValueRef, enumType *semantic.EnumType, store *packedStoreBinding) (C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum handle metadata")
	}
	ops, ok := s.packedStoreOpsFromBinding(store)
	if !ok {
		return nil, fmt.Errorf("packed enum %s decode requires store context", enumType.Name)
	}
	return ops.decodeHandle(handleValue, enumType, "packed.decode.store")
}

func (s *functionState) enumPayloadWordCount(enumType *semantic.EnumType) (uint64, error) {
	if enumType == nil {
		return 0, nil
	}
	maxSlots := uint64(0)
	for _, variant := range enumType.Variants {
		slots, err := s.g.enumVariantPayloadSlots(variant)
		if err != nil {
			return 0, err
		}
		if slots > maxSlots {
			maxSlots = slots
		}
	}
	return maxSlots, nil
}

func (s *functionState) emitPointerCompareExpr(expr *ast.BinaryExpr, leftType semantic.Type, rightType semantic.Type, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr.Op != lexer.TOKEN_EQEQ && expr.Op != lexer.TOKEN_BANGEQ {
		return nil, nil, false, nil
	}
	leftPointerish := isPointerLikeType(leftType) || semantic.IsNullType(leftType)
	rightPointerish := isPointerLikeType(rightType) || semantic.IsNullType(rightType)
	if !leftPointerish || !rightPointerish {
		return nil, nil, false, nil
	}
	operandType := s.binaryOperandType(expr.Op, leftType, rightType)
	left, _, err := s.emitExpr(expr.Left, operandType)
	if err != nil {
		return nil, nil, true, err
	}
	right, _, err := s.emitExpr(expr.Right, operandType)
	if err != nil {
		return nil, nil, true, err
	}
	pred := C.LLVMIntPredicate(C.LLVMIntEQ)
	if expr.Op == lexer.TOKEN_BANGEQ {
		pred = C.LLVMIntPredicate(C.LLVMIntNE)
	}
	cmp := C.LLVMBuildICmp(s.builder, pred, left, right, cStringFree("ptrcmptmp"))
	return cmp, resultType, true, nil
}

func (s *functionState) emitOptionalCompareExpr(expr *ast.BinaryExpr, leftType semantic.Type, rightType semantic.Type, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr.Op != lexer.TOKEN_EQEQ && expr.Op != lexer.TOKEN_BANGEQ {
		return nil, nil, false, nil
	}
	var (
		optionalExpr ast.Expr
		optionalType *semantic.OptionalType
	)
	switch leftOptional := leftType.(type) {
	case *semantic.OptionalType:
		if semantic.IsNullType(rightType) {
			optionalExpr = expr.Left
			optionalType = leftOptional
		}
	}
	if optionalType == nil {
		if leftOptional, ok := leftType.(*semantic.OptionalType); ok {
			if _, isNull := expr.Right.(*ast.NullLit); isNull {
				optionalExpr = expr.Left
				optionalType = leftOptional
			}
		}
	}
	if optionalType == nil {
		if rightOptional, ok := rightType.(*semantic.OptionalType); ok && semantic.IsNullType(leftType) {
			optionalExpr = expr.Right
			optionalType = rightOptional
		}
	}
	if optionalType == nil {
		if rightOptional, ok := rightType.(*semantic.OptionalType); ok {
			if _, isNull := expr.Left.(*ast.NullLit); isNull {
				optionalExpr = expr.Right
				optionalType = rightOptional
			}
		}
	}
	if optionalType == nil {
		return nil, nil, false, nil
	}
	optionalValue, _, err := s.emitExpr(optionalExpr, optionalType)
	if err != nil {
		return nil, nil, true, err
	}
	presentValue, err := s.extractOptionalPresent(optionalValue, optionalType)
	if err != nil {
		return nil, nil, true, err
	}
	if expr.Op == lexer.TOKEN_EQEQ {
		return C.LLVMBuildNot(s.builder, presentValue, cStringFree("optionalisnull")), resultType, true, nil
	}
	return presentValue, resultType, true, nil
}

func (s *functionState) emitPointerArithmeticExpr(expr *ast.BinaryExpr, leftType semantic.Type, rightType semantic.Type, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, bool, error) {
	leftRef, leftIsRef := leftType.(*semantic.RefType)
	rightRef, rightIsRef := rightType.(*semantic.RefType)
	leftIsNumeric := isNumericType(leftType)
	rightIsNumeric := isNumericType(rightType)

	var (
		baseExpr  ast.Expr
		baseType  *semantic.RefType
		indexExpr ast.Expr
	)

	switch {
	case leftIsRef && rightIsNumeric && (expr.Op == lexer.TOKEN_PLUS || expr.Op == lexer.TOKEN_MINUS):
		baseExpr, baseType, indexExpr = expr.Left, leftRef, expr.Right
	case expr.Op == lexer.TOKEN_PLUS && leftIsNumeric && rightIsRef:
		baseExpr, baseType, indexExpr = expr.Right, rightRef, expr.Left
	default:
		return nil, nil, false, nil
	}

	baseValue, _, err := s.emitExpr(baseExpr, baseType)
	if err != nil {
		return nil, nil, true, err
	}
	indexValue, _, err := s.emitExpr(indexExpr, nil)
	if err != nil {
		return nil, nil, true, err
	}
	if expr.Op == lexer.TOKEN_MINUS {
		indexValue = C.LLVMBuildNeg(s.builder, indexValue, cStringFree("ptridx.neg"))
	}
	elemLLVMType, err := s.g.lowerType(baseType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	indices := []C.LLVMValueRef{indexValue}
	ptr := C.LLVMBuildGEP2(s.builder, elemLLVMType, baseValue, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("ptrarith"))
	return ptr, resultType, true, nil
}

func (s *functionState) emitRuntimeStringCompareExpr(expr *ast.BinaryExpr, helperName string, firstType semantic.Type, secondType semantic.Type, swap bool) (C.LLVMValueRef, semantic.Type, error) {
	firstExpr := expr.Left
	secondExpr := expr.Right
	if swap {
		firstExpr, secondExpr = secondExpr, firstExpr
	}
	if helperName == "ctx_streq" {
		if literalText, ok := s.staticCStringLiteral(secondExpr); ok {
			cmp, err := s.emitDStrStaticLiteralEqual(firstExpr, firstType, secondExpr, literalText)
			if err != nil {
				return nil, nil, err
			}
			if expr.Op == lexer.TOKEN_BANGEQ {
				cmp = C.LLVMBuildNot(s.builder, cmp, cStringFree("dstrlit.eq.not"))
			}
			return cmp, s.g.result.NamedTypes["bool"], nil
		}
		if literalText, ok := s.staticCStringLiteral(firstExpr); ok {
			cmp, err := s.emitDStrStaticLiteralEqual(secondExpr, secondType, firstExpr, literalText)
			if err != nil {
				return nil, nil, err
			}
			if expr.Op == lexer.TOKEN_BANGEQ {
				cmp = C.LLVMBuildNot(s.builder, cmp, cStringFree("dstrlit.eq.not"))
			}
			return cmp, s.g.result.NamedTypes["bool"], nil
		}
	}
	if helperName == "ctx_string_view_eq" {
		if literalText, ok := s.staticCStringLiteral(secondExpr); ok {
			cmp, err := s.emitStringViewStaticLiteralEqual(firstExpr, firstType, secondExpr, literalText)
			if err != nil {
				return nil, nil, err
			}
			if expr.Op == lexer.TOKEN_BANGEQ {
				cmp = C.LLVMBuildNot(s.builder, cmp, cStringFree("strcmp.lit.not"))
			}
			return cmp, s.g.result.NamedTypes["bool"], nil
		}
	}
	if cmp, ok, err := s.emitSameExtentRuntimeStringCompareExpr(expr.Op, firstExpr, firstType, secondExpr, secondType); ok {
		if err != nil {
			return nil, nil, err
		}
		return cmp, s.g.result.NamedTypes["bool"], nil
	}
	if cmp, ok, err := s.emitDisjointRuntimeStringCompareExpr(expr.Op, firstExpr, firstType, secondExpr, secondType); ok {
		if err != nil {
			return nil, nil, err
		}
		return cmp, s.g.result.NamedTypes["bool"], nil
	}
	firstValue, _, err := s.emitExpr(firstExpr, firstType)
	if err != nil {
		return nil, nil, err
	}
	secondValue, _, err := s.emitExpr(secondExpr, secondType)
	if err != nil {
		return nil, nil, err
	}
	helperReturn := s.g.result.NamedTypes["int"]
	helperType := &semantic.FuncType{
		Name:   helperName,
		Params: []semantic.Type{firstType, secondType},
		Return: helperReturn,
	}
	callee, err := s.g.ensureFunctionDeclared(helperName, helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	args := []C.LLVMValueRef{firstValue, secondValue}
	call := s.buildCall(llvmFnType, callee, args, "streqtmp")
	helperLLVMType, err := s.g.lowerType(helperReturn)
	if err != nil {
		return nil, nil, err
	}
	zero := C.LLVMConstInt(helperLLVMType, 0, 0)
	pred := C.LLVMIntPredicate(C.LLVMIntNE)
	if expr.Op == lexer.TOKEN_BANGEQ {
		pred = C.LLVMIntPredicate(C.LLVMIntEQ)
	}
	cmp := C.LLVMBuildICmp(s.builder, pred, call, zero, cStringFree("strcmp"))
	return cmp, s.g.result.NamedTypes["bool"], nil
}

func (s *functionState) emitSameExtentRuntimeStringCompareExpr(op lexer.TokenKind, firstExpr ast.Expr, firstType semantic.Type, secondExpr ast.Expr, secondType semantic.Type) (C.LLVMValueRef, bool, error) {
	if s == nil || s.g == nil || s.g.result == nil || !s.g.result.ExprsHaveSameExtent(firstExpr, secondExpr) {
		return nil, false, nil
	}
	firstData, firstLen, firstLenType, firstKind, err := s.emitRuntimeStringCompareOperand(firstExpr, firstType)
	if err != nil {
		return nil, true, err
	}
	secondData, secondLen, secondLenType, secondKind, err := s.emitRuntimeStringCompareOperand(secondExpr, secondType)
	if err != nil {
		return nil, true, err
	}
	if firstKind == runtimeStringCompareNone || secondKind == runtimeStringCompareNone {
		return nil, false, nil
	}
	lenValue := firstLen
	lenType := firstLenType
	if lenValue == nil {
		lenValue = secondLen
		lenType = secondLenType
	}
	if lenValue == nil || lenType == nil {
		return nil, false, nil
	}
	usizeType := s.g.result.NamedTypes["usize"]
	coercedLen, err := s.coerceValue(lenValue, lenType, usizeType)
	if err != nil {
		return nil, true, err
	}
	disjoint := s.g.result.ExprsAreDisjoint(firstExpr, secondExpr)
	cmp, err := s.emitMemcmpEqualValue(firstData, secondData, coercedLen, "streq.memcmp", disjoint)
	if err != nil {
		return nil, true, err
	}
	if op == lexer.TOKEN_BANGEQ {
		cmp = C.LLVMBuildNot(s.builder, cmp, cStringFree("streq.memcmp.not"))
	}
	return cmp, true, nil
}

func (s *functionState) emitDisjointRuntimeStringCompareExpr(op lexer.TokenKind, firstExpr ast.Expr, firstType semantic.Type, secondExpr ast.Expr, secondType semantic.Type) (C.LLVMValueRef, bool, error) {
	if s == nil || s.g == nil || s.g.result == nil || !s.g.result.ExprsAreDisjoint(firstExpr, secondExpr) {
		return nil, false, nil
	}
	firstData, firstLen, firstLenType, firstKind, err := s.emitRuntimeStringCompareOperand(firstExpr, firstType)
	if err != nil {
		return nil, true, err
	}
	secondData, secondLen, secondLenType, secondKind, err := s.emitRuntimeStringCompareOperand(secondExpr, secondType)
	if err != nil {
		return nil, true, err
	}
	if firstKind == runtimeStringCompareNone || secondKind == runtimeStringCompareNone {
		return nil, false, nil
	}
	if firstLen == nil || firstLenType == nil || secondLen == nil || secondLenType == nil {
		return nil, false, nil
	}
	usizeType := s.g.result.NamedTypes["usize"]
	firstCoercedLen, err := s.coerceValue(firstLen, firstLenType, usizeType)
	if err != nil {
		return nil, true, err
	}
	secondCoercedLen, err := s.coerceValue(secondLen, secondLenType, usizeType)
	if err != nil {
		return nil, true, err
	}
	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), firstCoercedLen, secondCoercedLen, cStringFree("streq.disjoint.len.eq"))
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	memcmpBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("streq.disjoint.memcmp"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("streq.disjoint.merge"))
	C.LLVMBuildCondBr(s.builder, lenEqual, memcmpBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, memcmpBB)
	cmp, err := s.emitMemcmpEqualValue(firstData, secondData, firstCoercedLen, "streq.disjoint.memcmp", true)
	if err != nil {
		return nil, true, err
	}
	if op == lexer.TOKEN_BANGEQ {
		cmp = C.LLVMBuildNot(s.builder, cmp, cStringFree("streq.disjoint.memcmp.not"))
	}
	memcmpEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("streq.disjoint.result"))
	fallbackRaw := C.ulonglong(0)
	if op == lexer.TOKEN_BANGEQ {
		fallbackRaw = 1
	}
	fallback := C.LLVMConstInt(boolType, fallbackRaw, 0)
	values := []C.LLVMValueRef{fallback, cmp}
	blocks := []C.LLVMBasicBlockRef{entryBlock, memcmpEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, true, nil
}

func (s *functionState) emitRuntimeStringCompareOperand(expr ast.Expr, exprType semantic.Type) (C.LLVMValueRef, C.LLVMValueRef, semantic.Type, runtimeStringCompareKind, error) {
	kind := classifyRuntimeStringCompareKind(exprType)
	if kind == runtimeStringCompareNone {
		return nil, nil, nil, kind, nil
	}
	value, _, err := s.emitExpr(expr, exprType)
	if err != nil {
		return nil, nil, nil, kind, err
	}
	switch kind {
	case runtimeStringCompareView:
		lenType := s.g.result.NamedTypes["i64"]
		data := C.LLVMBuildExtractValue(s.builder, value, 0, cStringFree("streq.view.data"))
		length := C.LLVMBuildExtractValue(s.builder, value, 1, cStringFree("streq.view.len"))
		return data, length, lenType, kind, nil
	case runtimeStringCompareDStr:
		lenType := s.g.result.NamedTypes["i64"]
		length, err := s.emitRuntimeStringLengthValue(value, exprType, lenType, "streq.len")
		if err != nil {
			return nil, nil, nil, kind, err
		}
		return value, length, lenType, kind, nil
	case runtimeStringCompareRaw:
		return value, nil, nil, kind, nil
	default:
		return nil, nil, nil, kind, nil
	}
}

func (s *functionState) emitRuntimeStringLengthValue(stringValue C.LLVMValueRef, stringType semantic.Type, resultType semantic.Type, name string) (C.LLVMValueRef, error) {
	helperType := &semantic.FuncType{
		Name:   "ctx_strlen",
		Params: []semantic.Type{stringType},
		Return: resultType,
	}
	callee, err := s.g.ensureFunctionDeclared("ctx_strlen", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	return s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stringValue}, name), nil
}

func (s *functionState) emitSpecializedRuntimeCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if value, actualType, handled, err := s.emitSpecializedStringViewLiteralCall(expr); handled {
		return value, actualType, true, err
	}
	if value, actualType, handled, err := s.emitSpecializedStringSliceEqCall(expr); handled {
		return value, actualType, true, err
	}
	if value, actualType, handled, err := s.emitSpecializedStringSlicesEqCall(expr); handled {
		return value, actualType, true, err
	}
	if value, actualType, handled, err := s.emitSpecializedRuntimeStringCompareCall(expr); handled {
		return value, actualType, true, err
	}
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return nil, nil, false, nil
	}
	if ident.Name != "string_view_eq" && ident.Name != "ctx_string_view_eq" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 {
		return nil, nil, false, nil
	}
	literalText, ok := s.staticCStringLiteral(expr.Args[1])
	if !ok {
		return nil, nil, false, nil
	}
	firstType := s.exprType(expr.Args[0])
	if classifyRuntimeStringCompareKind(firstType) != runtimeStringCompareView {
		return nil, nil, false, nil
	}
	cmp, err := s.emitStringViewStaticLiteralEqual(expr.Args[0], firstType, expr.Args[1], literalText)
	if err != nil {
		return nil, nil, true, err
	}
	intType := s.g.result.NamedTypes["int"]
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	return C.LLVMBuildZExt(s.builder, cmp, intLLVMType, cStringFree("svlit.eq.int")), intType, true, nil
}

func (s *functionState) staticIntLiteral(expr ast.Expr) (int64, bool) {
	switch n := expr.(type) {
	case *ast.IntLit:
		value := n.Value
		if n.Suffix != "" {
			value += n.Suffix
		}
		return parseOptimizationExtentConstInt(value)
	case *ast.ParenExpr:
		return s.staticIntLiteral(n.Inner)
	case *ast.CastExpr:
		return s.staticIntLiteral(n.Operand)
	case *ast.CanExpr:
		return s.staticIntLiteral(n.Expr)
	default:
		return 0, false
	}
}

func (s *functionState) emitMinInt64Value(left C.LLVMValueRef, right C.LLVMValueRef, namePrefix string) C.LLVMValueRef {
	chooseLeft := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntSLE), left, right, cStringFree(namePrefix+".chooseleft"))
	leftBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(namePrefix+".left"))
	rightBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(namePrefix+".right"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(namePrefix+".merge"))
	C.LLVMBuildCondBr(s.builder, chooseLeft, leftBB, rightBB)

	C.LLVMPositionBuilderAtEnd(s.builder, leftBB)
	leftEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, rightBB)
	rightEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phi := C.LLVMBuildPhi(s.builder, C.LLVMInt64TypeInContext(s.g.context), cStringFree(namePrefix))
	values := []C.LLVMValueRef{left, right}
	blocks := []C.LLVMBasicBlockRef{leftEnd, rightEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi
}

func (s *functionState) emitConstantClampedStringSliceOperand(expr ast.Expr, exprType semantic.Type, start int64, end int64, namePrefix string) (C.LLVMValueRef, C.LLVMValueRef, error) {
	if classifyRuntimeStringCompareKind(exprType) != runtimeStringCompareDStr {
		return nil, nil, fmt.Errorf("constant string slice specialization requires dstr operand")
	}
	stringValue, _, err := s.emitExpr(expr, exprType)
	if err != nil {
		return nil, nil, err
	}
	i64Type := s.g.result.NamedTypes["i64"]
	stringLen, err := s.emitRuntimeStringLengthValue(stringValue, exprType, i64Type, namePrefix+".len")
	if err != nil {
		return nil, nil, err
	}
	i64LLVMType := C.LLVMInt64TypeInContext(s.g.context)
	zeroI64 := C.LLVMConstInt(i64LLVMType, 0, 0)
	clampedStart := zeroI64
	if start > 0 {
		startValue := C.LLVMConstInt(i64LLVMType, C.ulonglong(start), 0)
		clampedStart = s.emitMinInt64Value(startValue, stringLen, namePrefix+".start")
	}
	clampedEnd := stringLen
	if end >= 0 {
		endValue := C.LLVMConstInt(i64LLVMType, C.ulonglong(end), 0)
		clampedEnd = s.emitMinInt64Value(endValue, stringLen, namePrefix+".end")
	}
	sliceLen := C.LLVMBuildSub(s.builder, clampedEnd, clampedStart, cStringFree(namePrefix+".slice.len"))
	sliceData := stringValue
	if start > 0 {
		usizeType := s.g.result.NamedTypes["usize"]
		clampedStartUsize, err := s.coerceValue(clampedStart, i64Type, usizeType)
		if err != nil {
			return nil, nil, err
		}
		i8LLVMType, err := s.g.lowerBuiltin("u8")
		if err != nil {
			return nil, nil, err
		}
		indices := []C.LLVMValueRef{clampedStartUsize}
		sliceData = C.LLVMBuildGEP2(s.builder, i8LLVMType, stringValue, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree(namePrefix+".data"))
	}
	return sliceData, sliceLen, nil
}

func (s *functionState) constantDStrSliceCall(expr ast.Expr) (ast.Expr, semantic.Type, int64, int64, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.constantDStrSliceCall(n.Inner)
	case *ast.CastExpr:
		return s.constantDStrSliceCall(n.Operand)
	case *ast.CanExpr:
		return s.constantDStrSliceCall(n.Expr)
	case *ast.CallExpr:
		ident, ok := n.Func.(*ast.Ident)
		if !ok || ident.Name != "ctx_string_slice" || len(n.Args) != 3 {
			return nil, nil, 0, 0, false
		}
		baseExpr := n.Args[0]
		baseType := s.exprType(baseExpr)
		if classifyRuntimeStringCompareKind(baseType) != runtimeStringCompareDStr {
			return nil, nil, 0, 0, false
		}
		start, ok := s.staticIntLiteral(n.Args[1])
		if !ok || start < 0 {
			return nil, nil, 0, 0, false
		}
		end, ok := s.staticIntLiteral(n.Args[2])
		if !ok || end < start {
			return nil, nil, 0, 0, false
		}
		return baseExpr, baseType, start, end, true
	default:
		return nil, nil, 0, 0, false
	}
}

func (s *functionState) emitSpecializedStringSliceEqCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "ctx_string_slice_eq" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 4 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	leftExpr := expr.Args[0]
	leftType := s.exprType(leftExpr)
	if classifyRuntimeStringCompareKind(leftType) != runtimeStringCompareDStr {
		return nil, nil, false, nil
	}
	start, ok := s.staticIntLiteral(expr.Args[1])
	if !ok || start < 0 {
		return nil, nil, false, nil
	}
	end, ok := s.staticIntLiteral(expr.Args[2])
	if !ok || end < start {
		return nil, nil, false, nil
	}
	rightExpr := expr.Args[3]
	rightType := s.exprType(rightExpr)
	if classifyRuntimeStringCompareKind(rightType) != runtimeStringCompareDStr {
		return nil, nil, false, nil
	}
	intType := s.g.result.NamedTypes["int"]
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	leftData, leftSliceLen, err := s.emitConstantClampedStringSliceOperand(leftExpr, leftType, start, end, "strsliceeq.left")
	if err != nil {
		return nil, nil, true, err
	}
	rightData, rightLen, rightLenType, rightKind, err := s.emitRuntimeStringCompareOperand(rightExpr, rightType)
	if err != nil {
		return nil, nil, true, err
	}
	if rightKind != runtimeStringCompareDStr || rightLen == nil || rightLenType == nil {
		return nil, nil, false, nil
	}
	rightLenI64, err := s.coerceValue(rightLen, rightLenType, s.g.result.NamedTypes["i64"])
	if err != nil {
		return nil, nil, true, err
	}
	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftSliceLen, rightLenI64, cStringFree("strsliceeq.len.eq"))
	sliceZero := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftSliceLen, C.LLVMConstInt(C.LLVMInt64TypeInContext(s.g.context), 0, 0), cStringFree("strsliceeq.len.zero"))
	dataEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftData, rightData, cStringFree("strsliceeq.data.eq"))
	usizeType := s.g.result.NamedTypes["usize"]
	lenValue, err := s.coerceValue(leftSliceLen, s.g.result.NamedTypes["i64"], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	zeroBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceeq.zero"))
	nonZeroBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceeq.nonzero"))
	sameBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceeq.same"))
	memcmpBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceeq.memcmp"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceeq.merge"))
	C.LLVMBuildCondBr(s.builder, lenEqual, zeroBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, zeroBB)
	zeroEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildCondBr(s.builder, sliceZero, mergeBB, nonZeroBB)

	C.LLVMPositionBuilderAtEnd(s.builder, nonZeroBB)
	C.LLVMBuildCondBr(s.builder, dataEqual, sameBB, memcmpBB)

	C.LLVMPositionBuilderAtEnd(s.builder, sameBB)
	sameEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, memcmpBB)
	disjoint := s.g.result.ExprsAreDisjoint(leftExpr, rightExpr)
	cmp, err := s.emitMemcmpEqualValue(leftData, rightData, lenValue, "strsliceeq.memcmp", disjoint)
	if err != nil {
		return nil, nil, true, err
	}
	memcmpEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("strsliceeq.result"))
	falseValue := C.LLVMConstInt(boolType, 0, 0)
	trueValue := C.LLVMConstInt(boolType, 1, 0)
	values := []C.LLVMValueRef{falseValue, trueValue, trueValue, cmp}
	blocks := []C.LLVMBasicBlockRef{entryBlock, zeroEnd, sameEnd, memcmpEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return C.LLVMBuildZExt(s.builder, phi, intLLVMType, cStringFree("strsliceeq.int")), intType, true, nil
}

func (s *functionState) emitSpecializedStringSlicesEqCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "ctx_string_slices_eq" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 6 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	leftExpr := expr.Args[0]
	leftStartExpr := expr.Args[1]
	leftEndExpr := expr.Args[2]
	rightExpr := expr.Args[3]
	rightStartExpr := expr.Args[4]
	rightEndExpr := expr.Args[5]
	leftType := s.exprType(leftExpr)
	rightType := s.exprType(rightExpr)
	if classifyRuntimeStringCompareKind(leftType) != runtimeStringCompareDStr || classifyRuntimeStringCompareKind(rightType) != runtimeStringCompareDStr {
		return nil, nil, false, nil
	}
	leftStart, ok := s.staticIntLiteral(leftStartExpr)
	if !ok || leftStart < 0 {
		return nil, nil, false, nil
	}
	leftEnd, ok := s.staticIntLiteral(leftEndExpr)
	if !ok || leftEnd < leftStart {
		return nil, nil, false, nil
	}
	rightStart, ok := s.staticIntLiteral(rightStartExpr)
	if !ok || rightStart < 0 {
		return nil, nil, false, nil
	}
	rightEnd, ok := s.staticIntLiteral(rightEndExpr)
	if !ok || rightEnd < rightStart {
		return nil, nil, false, nil
	}
	intType := s.g.result.NamedTypes["int"]
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	leftData, leftSliceLen, err := s.emitConstantClampedStringSliceOperand(leftExpr, leftType, leftStart, leftEnd, "strsliceseq.left")
	if err != nil {
		return nil, nil, true, err
	}
	rightData, rightSliceLen, err := s.emitConstantClampedStringSliceOperand(rightExpr, rightType, rightStart, rightEnd, "strsliceseq.right")
	if err != nil {
		return nil, nil, true, err
	}
	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftSliceLen, rightSliceLen, cStringFree("strsliceseq.len.eq"))
	sliceZero := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftSliceLen, C.LLVMConstInt(C.LLVMInt64TypeInContext(s.g.context), 0, 0), cStringFree("strsliceseq.len.zero"))
	dataEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftData, rightData, cStringFree("strsliceseq.data.eq"))
	usizeType := s.g.result.NamedTypes["usize"]
	lenValue, err := s.coerceValue(leftSliceLen, s.g.result.NamedTypes["i64"], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	zeroBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceseq.zero"))
	nonZeroBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceseq.nonzero"))
	sameBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceseq.same"))
	memcmpBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceseq.memcmp"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceseq.merge"))
	C.LLVMBuildCondBr(s.builder, lenEqual, zeroBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, zeroBB)
	zeroEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildCondBr(s.builder, sliceZero, mergeBB, nonZeroBB)

	C.LLVMPositionBuilderAtEnd(s.builder, nonZeroBB)
	C.LLVMBuildCondBr(s.builder, dataEqual, sameBB, memcmpBB)

	C.LLVMPositionBuilderAtEnd(s.builder, sameBB)
	sameEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, memcmpBB)
	disjoint := s.g.result.ExprsAreDisjoint(leftExpr, rightExpr)
	cmp, err := s.emitMemcmpEqualValue(leftData, rightData, lenValue, "strsliceseq.memcmp", disjoint)
	if err != nil {
		return nil, nil, true, err
	}
	memcmpEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("strsliceseq.result"))
	falseValue := C.LLVMConstInt(boolType, 0, 0)
	trueValue := C.LLVMConstInt(boolType, 1, 0)
	values := []C.LLVMValueRef{falseValue, trueValue, trueValue, cmp}
	blocks := []C.LLVMBasicBlockRef{entryBlock, zeroEnd, sameEnd, memcmpEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return C.LLVMBuildZExt(s.builder, phi, intLLVMType, cStringFree("strsliceseq.int")), intType, true, nil
}

func (s *functionState) emitSpecializedRuntimeStringCompareCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	switch ident.Name {
	case "ctx_streq", "ctx_string_view_eq", "string_view_eq", "ctx_string_views_eq", "string_views_eq":
	default:
		return nil, nil, false, nil
	}
	leftExpr := expr.Args[0]
	rightExpr := expr.Args[1]
	leftType := s.exprType(leftExpr)
	rightType := s.exprType(rightExpr)
	if ident.Name == "ctx_streq" {
		if literalText, ok := s.staticCStringLiteral(rightExpr); ok {
			cmp, err := s.emitDStrStaticLiteralEqual(leftExpr, leftType, rightExpr, literalText)
			if err != nil {
				return nil, nil, true, err
			}
			intType := s.g.result.NamedTypes["int"]
			intLLVMType, err := s.g.lowerType(intType)
			if err != nil {
				return nil, nil, true, err
			}
			return C.LLVMBuildZExt(s.builder, cmp, intLLVMType, cStringFree("dstrlit.direct.int")), intType, true, nil
		}
		if literalText, ok := s.staticCStringLiteral(leftExpr); ok {
			cmp, err := s.emitDStrStaticLiteralEqual(rightExpr, rightType, leftExpr, literalText)
			if err != nil {
				return nil, nil, true, err
			}
			intType := s.g.result.NamedTypes["int"]
			intLLVMType, err := s.g.lowerType(intType)
			if err != nil {
				return nil, nil, true, err
			}
			return C.LLVMBuildZExt(s.builder, cmp, intLLVMType, cStringFree("dstrlit.direct.int")), intType, true, nil
		}
	}
	cmp, ok, err := s.emitSameExtentRuntimeStringCompareExpr(lexer.TOKEN_EQEQ, leftExpr, leftType, rightExpr, rightType)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok {
		cmp, ok, err = s.emitDisjointRuntimeStringCompareExpr(lexer.TOKEN_EQEQ, leftExpr, leftType, rightExpr, rightType)
		if err != nil {
			return nil, nil, true, err
		}
		if !ok {
			return nil, nil, false, nil
		}
	}
	intType := s.g.result.NamedTypes["int"]
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	return C.LLVMBuildZExt(s.builder, cmp, intLLVMType, cStringFree("streq.direct.int")), intType, true, nil
}

func isStringViewCarrierType(t semantic.Type) bool {
	return classifyRuntimeStringCompareKind(t) == runtimeStringCompareView
}

func (s *functionState) emitGlobalCStringLiteral(text string, name string) C.LLVMValueRef {
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	textC := cString(text)
	defer C.free(unsafe.Pointer(textC))
	return C.LLVMBuildGlobalStringPtr(s.builder, textC, nameC)
}

func (s *functionState) emitInternSmallStringCall(data C.LLVMValueRef, lenValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	u8Type := s.g.result.NamedTypes["u8"]
	usizeType := s.g.result.NamedTypes["usize"]
	srcType := &semantic.RefType{Elem: u8Type, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	retType := &semantic.RefType{Elem: u8Type, State: semantic.RefStateNonNull, Storage: semantic.RefStorageHeap, ExplicitStorage: true}
	helperType := &semantic.FuncType{Name: "intern_small_string", Params: []semantic.Type{srcType, usizeType}, Return: retType}
	callee, err := s.g.ensureFunctionDeclared("intern_small_string", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	return s.buildCall(llvmFnType, callee, []C.LLVMValueRef{data, lenValue}, name), nil
}

func (s *functionState) emitDirectStringViewCopyLarge(viewData C.LLVMValueRef, viewLen C.LLVMValueRef) (C.LLVMValueRef, error) {
	i64Type := s.g.result.NamedTypes["i64"]
	usizeType := s.g.result.NamedTypes["usize"]
	voidType := s.g.result.NamedTypes["void"]
	u8Type := s.g.result.NamedTypes["u8"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	nullableU8RefType := &semantic.RefType{Elem: u8Type, State: semantic.RefStateNullable, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	heapVoidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageHeap, ExplicitStorage: true}
	allocType := &semantic.FuncType{Name: "alloc_perm", Params: []semantic.Type{i64Type}, Return: heapVoidRefType}
	allocCallee, err := s.g.ensureFunctionDeclared("alloc_perm", allocType)
	if err != nil {
		return nil, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, err
	}
	oneValue := C.LLVMConstInt(C.LLVMInt64TypeInContext(s.g.context), 1, 0)
	allocSize := C.LLVMBuildAdd(s.builder, viewLen, oneValue, cStringFree("svcopy.alloc.size"))
	allocPtr := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{allocSize}, "svcopy.alloc")

	lenUsize, err := s.coerceValue(viewLen, i64Type, usizeType)
	if err != nil {
		return nil, err
	}
	memcpyType := &semantic.FuncType{Name: "memcpy", Params: []semantic.Type{voidRefType, voidRefType, usizeType}, Return: voidRefType}
	memcpyCallee, err := s.g.ensureFunctionDeclared("memcpy", memcpyType)
	if err != nil {
		return nil, err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return nil, err
	}
	_ = s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{allocPtr, viewData, lenUsize}, "svcopy.memcpy")

	i8LLVMType, err := s.g.lowerBuiltin("u8")
	if err != nil {
		return nil, err
	}
	bytePtr := C.LLVMBuildGEP2(s.builder, i8LLVMType, allocPtr, llvmValueSlicePtr([]C.LLVMValueRef{lenUsize}), 1, cStringFree("svcopy.term.ptr"))
	zeroByte := C.LLVMConstInt(i8LLVMType, 0, 0)
	C.LLVMBuildStore(s.builder, zeroByte, bytePtr)

	registerType := &semantic.FuncType{Name: "register_perm_string_len", Params: []semantic.Type{nullableU8RefType, usizeType}, Return: voidType}
	registerCallee, err := s.g.ensureFunctionDeclared("register_perm_string_len", registerType)
	if err != nil {
		return nil, err
	}
	registerLLVMType, err := s.g.lowerFunctionType(registerType)
	if err != nil {
		return nil, err
	}
	_ = s.buildCall(registerLLVMType, registerCallee, []C.LLVMValueRef{allocPtr, lenUsize}, "")
	return allocPtr, nil
}

func (s *functionState) emitSpecializedStringSliceCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "ctx_string_slice" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 3 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	resultType := s.exprType(expr)
	inputExpr := expr.Args[0]
	inputType := s.exprType(inputExpr)
	if _, ok := inputType.(*semantic.DStrType); !ok {
		return nil, nil, false, nil
	}
	if s.g.result.ExprsHaveSameExtent(expr, inputExpr) {
		value, _, err := s.emitExpr(inputExpr, inputType)
		return value, resultType, true, err
	}
	sliceFacts, ok := s.g.result.ExprOptimizationFacts(expr)
	if !ok || !sliceFacts.HasExactExtent() {
		return nil, nil, false, nil
	}
	exactLen, ok := constOptimizationExtentSize(sliceFacts.Extent)
	if !ok {
		return nil, nil, false, nil
	}
	begin, ok := parseOptimizationExtentConstInt(sliceFacts.Extent.Begin)
	if !ok || begin < 0 {
		return nil, nil, false, nil
	}
	inputValue, _, err := s.emitExpr(inputExpr, inputType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	beginValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(begin), 0)
	sliceData := inputValue
	if begin != 0 {
		i8LLVMType, err := s.g.lowerBuiltin("u8")
		if err != nil {
			return nil, nil, true, err
		}
		indices := []C.LLVMValueRef{beginValue}
		sliceData = C.LLVMBuildGEP2(s.builder, i8LLVMType, inputValue, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("strslice.data"))
	}
	lenValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(exactLen), 0)
	if exactLen == 0 {
		emptyPtr := s.emitGlobalCStringLiteral("", "strslice.empty")
		value, err := s.emitInternSmallStringCall(emptyPtr, lenValue, "strslice.zero.small")
		return value, resultType, true, err
	}
	if exactLen <= 8 {
		value, err := s.emitInternSmallStringCall(sliceData, lenValue, "strslice.small")
		return value, resultType, true, err
	}
	largeLen := C.LLVMConstInt(C.LLVMInt64TypeInContext(s.g.context), C.ulonglong(exactLen), 0)
	value, err := s.emitDirectStringViewCopyLarge(sliceData, largeLen)
	return value, resultType, true, err
}

func (s *functionState) emitSpecializedStringViewCopyCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || (ident.Name != "string_view_copy" && ident.Name != "ctx_string_from_view") {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	viewExpr := expr.Args[0]
	viewType := s.exprType(viewExpr)
	resultType := s.exprType(expr)
	if !isStringViewCarrierType(viewType) {
		return nil, nil, false, nil
	}
	viewFacts, ok := s.g.result.ExprOptimizationFacts(viewExpr)
	if !ok || !viewFacts.HasExactExtent() {
		return nil, nil, false, nil
	}
	viewValue, _, err := s.emitExpr(viewExpr, viewType)
	if err != nil {
		return nil, nil, true, err
	}
	viewData := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("svcopy.data"))
	viewLen := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("svcopy.len"))
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	if exactLen, ok := constOptimizationExtentSize(viewFacts.Extent); ok {
		lenValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(exactLen), 0)
		if exactLen <= 8 {
			dataValue := viewData
			if exactLen == 0 {
				dataValue = s.emitGlobalCStringLiteral("", "svcopy.empty")
			}
			value, err := s.emitInternSmallStringCall(dataValue, lenValue, "svcopy.small")
			return value, resultType, true, err
		}
		largeLen := C.LLVMConstInt(C.LLVMInt64TypeInContext(s.g.context), C.ulonglong(exactLen), 0)
		value, err := s.emitDirectStringViewCopyLarge(viewData, largeLen)
		return value, resultType, true, err
	}

	i64LLVMType := C.LLVMInt64TypeInContext(s.g.context)
	zeroLen := C.LLVMConstInt(i64LLVMType, 0, 0)
	eightLen := C.LLVMConstInt(i64LLVMType, 8, 0)
	zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntSLE), viewLen, zeroLen, cStringFree("svcopy.len.zero"))
	positiveBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svcopy.positive"))
	zeroBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svcopy.zero"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svcopy.merge"))
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildCondBr(s.builder, zeroCond, zeroBB, positiveBB)

	C.LLVMPositionBuilderAtEnd(s.builder, zeroBB)
	emptyPtr := s.emitGlobalCStringLiteral("", "svcopy.empty")
	zeroSmall, err := s.emitInternSmallStringCall(emptyPtr, C.LLVMConstInt(usizeLLVMType, 0, 0), "svcopy.zero.small")
	if err != nil {
		return nil, nil, true, err
	}
	zeroEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, positiveBB)
	smallCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntSLE), viewLen, eightLen, cStringFree("svcopy.len.small"))
	smallBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svcopy.small"))
	largeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svcopy.large"))
	C.LLVMBuildCondBr(s.builder, smallCond, smallBB, largeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, smallBB)
	viewLenUsize, err := s.coerceValue(viewLen, s.g.result.NamedTypes["i64"], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	smallValue, err := s.emitInternSmallStringCall(viewData, viewLenUsize, "svcopy.small")
	if err != nil {
		return nil, nil, true, err
	}
	smallEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, largeBB)
	largeValue, err := s.emitDirectStringViewCopyLarge(viewData, viewLen)
	if err != nil {
		return nil, nil, true, err
	}
	largeEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	llvmResultType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	phi := C.LLVMBuildPhi(s.builder, llvmResultType, cStringFree("svcopy.result"))
	values := []C.LLVMValueRef{zeroSmall, smallValue, largeValue}
	blocks := []C.LLVMBasicBlockRef{zeroEnd, smallEnd, largeEnd}
	_ = entryBlock
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, true, nil
}

func (s *functionState) emitSpecializedStringViewLiteralCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 {
		return nil, nil, false, nil
	}
	viewArgIndex, literalArgIndex, returnsInt, ok := s.specializedStringViewLiteralCallShape(ident.Name)
	if !ok {
		return nil, nil, false, nil
	}
	literalText, ok := s.staticCStringLiteral(expr.Args[literalArgIndex])
	if !ok {
		return nil, nil, false, nil
	}
	viewType := s.exprType(expr.Args[viewArgIndex])
	if classifyRuntimeStringCompareKind(viewType) != runtimeStringCompareView {
		return nil, nil, false, nil
	}
	cmp, err := s.emitStringViewStaticLiteralEqual(expr.Args[viewArgIndex], viewType, expr.Args[literalArgIndex], literalText)
	if err != nil {
		return nil, nil, true, err
	}
	if !returnsInt {
		return cmp, s.exprType(expr), true, nil
	}
	intType := s.g.result.NamedTypes["int"]
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	return C.LLVMBuildZExt(s.builder, cmp, intLLVMType, cStringFree("svlit.eq.int")), intType, true, nil
}

func (s *functionState) specializedStringViewLiteralCallShape(funcName string) (int, int, bool, bool) {
	if funcName == "string_view_eq" || funcName == "ctx_string_view_eq" {
		return 0, 1, true, true
	}
	sym, ok := s.g.result.GlobalScope.Lookup(funcName)
	if !ok {
		return 0, 0, false, false
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok || len(decl.Params) != 2 || len(decl.Body) != 1 {
		return 0, 0, false, false
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok || ret.Value == nil {
		return 0, 0, false, false
	}
	callExpr, ok := s.boolStringViewLiteralWrapperCall(ret.Value, decl.Params[0].Name, decl.Params[1].Name)
	if !ok || callExpr == nil {
		return 0, 0, false, false
	}
	callee, ok := callExpr.Func.(*ast.Ident)
	if !ok {
		return 0, 0, false, false
	}
	if callee.Name != "string_view_eq" && callee.Name != "ctx_string_view_eq" {
		return 0, 0, false, false
	}
	return 0, 1, false, true
}

func (s *functionState) boolStringViewLiteralWrapperCall(expr ast.Expr, viewParam string, literalParam string) (*ast.CallExpr, bool) {
	binary, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return nil, false
	}
	if binary.Op != lexer.TOKEN_EQEQ && binary.Op != lexer.TOKEN_BANGEQ {
		return nil, false
	}
	callExpr, intLit, ok := unwrapCallComparedToZero(binary.Left, binary.Right)
	if !ok {
		callExpr, intLit, ok = unwrapCallComparedToZero(binary.Right, binary.Left)
	}
	if !ok || intLit == nil || intLit.Value != "0" {
		return nil, false
	}
	if binary.Op != lexer.TOKEN_BANGEQ {
		return nil, false
	}
	if !matchesStringViewLiteralWrapperArgs(callExpr, viewParam, literalParam) {
		return nil, false
	}
	return callExpr, true
}

func unwrapCallComparedToZero(left ast.Expr, right ast.Expr) (*ast.CallExpr, *ast.IntLit, bool) {
	callExpr, ok := left.(*ast.CallExpr)
	if !ok {
		return nil, nil, false
	}
	intLit, ok := right.(*ast.IntLit)
	if !ok {
		return nil, nil, false
	}
	return callExpr, intLit, true
}

func matchesStringViewLiteralWrapperArgs(callExpr *ast.CallExpr, viewParam string, literalParam string) bool {
	if callExpr == nil || len(callExpr.Args) != 2 {
		return false
	}
	viewIdent, ok := callExpr.Args[0].(*ast.Ident)
	if !ok || viewIdent.Name != viewParam {
		return false
	}
	return exprIsParamOrCastOfParam(callExpr.Args[1], literalParam)
}

func exprIsParamOrCastOfParam(expr ast.Expr, paramName string) bool {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name == paramName
	case *ast.ParenExpr:
		return exprIsParamOrCastOfParam(n.Inner, paramName)
	case *ast.CastExpr:
		return exprIsParamOrCastOfParam(n.Operand, paramName)
	default:
		return false
	}
}

func stripMemcpyOperandExpr(expr ast.Expr) ast.Expr {
	for expr != nil {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.CastExpr:
			expr = n.Operand
		case *ast.CanExpr:
			expr = n.Expr
		default:
			return expr
		}
	}
	return nil
}

func isMemcpyViewCarrierType(t semantic.Type) bool {
	switch tt := t.(type) {
	case *semantic.ViewType, *semantic.DArrayViewType, *semantic.SViewType:
		return true
	case *semantic.StructType:
		return tt != nil && (tt.Name == "DynArrayView" || tt.Name == "StringView")
	default:
		return false
	}
}

func isDynArrayViewCarrierType(t semantic.Type) bool {
	switch tt := t.(type) {
	case *semantic.ViewType, *semantic.DArrayViewType:
		return true
	case *semantic.StructType:
		return tt != nil && tt.Name == "DynArrayView"
	default:
		return false
	}
}

func isDynArrayCarrierType(t semantic.Type) bool {
	switch t.(type) {
	case *semantic.DArrayType:
		return true
	default:
		return false
	}
}

func (s *functionState) memcpyDisjointCarrierExpr(expr ast.Expr) ast.Expr {
	stripped := stripMemcpyOperandExpr(expr)
	fieldExpr, ok := stripped.(*ast.FieldExpr)
	if !ok || fieldExpr.Field != "data" {
		return nil
	}
	if !isMemcpyViewCarrierType(s.exprType(fieldExpr.Object)) {
		return nil
	}
	return fieldExpr.Object
}

func (s *functionState) memcpyOperandsAreDisjoint(destExpr ast.Expr, srcExpr ast.Expr) bool {
	if s == nil || s.g == nil || s.g.result == nil {
		return false
	}
	if s.g.result.ExprsAreDisjoint(destExpr, srcExpr) {
		return true
	}
	destCarrier := s.memcpyDisjointCarrierExpr(destExpr)
	srcCarrier := s.memcpyDisjointCarrierExpr(srcExpr)
	if destCarrier == nil || srcCarrier == nil {
		return false
	}
	return s.g.result.ExprsAreDisjoint(destCarrier, srcCarrier)
}

func (s *functionState) emitSpecializedMemcpyCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return nil, nil, false, nil
	}
	if ident.Name != "memcpy" && ident.Name != "arena_memcpy" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 3 {
		return nil, nil, false, nil
	}
	callee, funcType, err := s.resolveCallTarget(expr)
	if err != nil {
		return nil, nil, true, err
	}
	if funcType == nil {
		return nil, nil, true, fmt.Errorf("copy helper target does not have a function type")
	}
	llvmFnType, err := s.g.lowerFunctionType(funcType)
	if err != nil {
		return nil, nil, true, err
	}
	args := make([]C.LLVMValueRef, 0, len(expr.Args))
	for i, arg := range expr.Args {
		var expected semantic.Type
		if i < len(funcType.Params) {
			expected = funcType.Params[i]
		}
		value, _, err := s.emitExpr(arg, expected)
		if err != nil {
			return nil, nil, true, err
		}
		args = append(args, value)
	}
	callName := "calltmp"
	if isVoidType(funcType.Return) {
		callName = ""
	}
	call := s.buildCall(llvmFnType, callee, args, callName)
	if s.memcpyOperandsAreDisjoint(expr.Args[0], expr.Args[1]) {
		s.addCallSiteEnumAttribute(call, C.uint(1), "noalias")
		s.addCallSiteEnumAttribute(call, C.uint(2), "noalias")
	}
	return call, funcType.Return, true, nil
}

func (s *functionState) emitSpecializedArenaViewCopyCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "arena_da_copy_exact" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	dstExpr := expr.Args[0]
	srcExpr := expr.Args[1]
	dstType := s.exprType(dstExpr)
	srcType := s.exprType(srcExpr)
	if !isDynArrayViewCarrierType(dstType) || !isDynArrayViewCarrierType(srcType) {
		return nil, nil, false, nil
	}
	callee, funcType, err := s.resolveCallTarget(expr)
	if err != nil {
		return nil, nil, true, err
	}
	if funcType == nil {
		return nil, nil, true, fmt.Errorf("arena_da_copy_exact target does not have a function type")
	}
	exactCopyCount := uint64(0)
	hasSmallExactCopyCount := false
	if dstFacts, ok := s.g.result.ExprOptimizationFacts(dstExpr); ok {
		if dstCount, ok := constOptimizationExtentSize(dstFacts.Extent); ok && dstCount <= smallExactArenaCopyUnrollLimit {
			if srcFacts, ok := s.g.result.ExprOptimizationFacts(srcExpr); ok {
				if srcCount, ok := constOptimizationExtentSize(srcFacts.Extent); ok && srcCount == dstCount {
					exactCopyCount = dstCount
					hasSmallExactCopyCount = true
				}
			}
		}
	}
	disjoint := s.g.result.ExprsAreDisjoint(dstExpr, srcExpr)
	if !hasSmallExactCopyCount && !disjoint {
		return nil, nil, false, nil
	}
	dstValue, _, err := s.emitExpr(dstExpr, dstType)
	if err != nil {
		return nil, nil, true, err
	}
	srcValue, _, err := s.emitExpr(srcExpr, srcType)
	if err != nil {
		return nil, nil, true, err
	}
	if hasSmallExactCopyCount {
		if exactCopyCount == 0 {
			return nil, funcType.Return, true, nil
		}
		var elemType semantic.Type
		switch viewType := funcType.Params[0].(type) {
		case *semantic.ViewType:
			elemType = viewType.Elem
		case *semantic.DArrayViewType:
			elemType = viewType.Elem
		default:
			return nil, nil, true, fmt.Errorf("arena_da_copy_exact specialization expected dview parameter, got %T", funcType.Params[0])
		}
		elemLLVMType, err := s.g.lowerType(elemType)
		if err != nil {
			return nil, nil, true, err
		}
		usizeType, err := s.g.lowerBuiltin("usize")
		if err != nil {
			return nil, nil, true, err
		}
		dstData := C.LLVMBuildExtractValue(s.builder, dstValue, 0, cStringFree("dview.copy.dst.data"))
		srcData := C.LLVMBuildExtractValue(s.builder, srcValue, 0, cStringFree("dview.copy.src.data"))
		domainName := ""
		dstScopeName := ""
		srcScopeName := ""
		if disjoint {
			domainName = fmt.Sprintf("llctx.dview.copy.%p.domain", expr)
			dstScopeName = domainName + ".dst"
			srcScopeName = domainName + ".src"
		}
		for i := uint64(0); i < exactCopyCount; i++ {
			indexValue := C.LLVMConstInt(usizeType, C.ulonglong(i), 0)
			indices := []C.LLVMValueRef{indexValue}
			srcPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, srcData, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("dview.copy.src.elem.ptr"))
			elemValue := C.LLVMBuildLoad2(s.builder, elemLLVMType, srcPtr, cStringFree("dview.copy.elem"))
			if disjoint {
				s.attachAliasScopeMetadataWithNames(elemValue, domainName, srcScopeName, []string{dstScopeName})
			}
			dstPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, dstData, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("dview.copy.dst.elem.ptr"))
			store := C.LLVMBuildStore(s.builder, elemValue, dstPtr)
			if disjoint {
				s.attachAliasScopeMetadataWithNames(store, domainName, dstScopeName, []string{srcScopeName})
			}
		}
		return nil, funcType.Return, true, nil
	}
	llvmFnType, err := s.g.lowerFunctionType(funcType)
	if err != nil {
		return nil, nil, true, err
	}
	dstData := C.LLVMBuildExtractValue(s.builder, dstValue, 0, cStringFree("dview.copy.dst.data"))
	dstLen := C.LLVMBuildExtractValue(s.builder, dstValue, 1, cStringFree("dview.copy.dst.len"))
	dstElemSize := C.LLVMBuildExtractValue(s.builder, dstValue, 2, cStringFree("dview.copy.dst.elem_size"))
	srcData := C.LLVMBuildExtractValue(s.builder, srcValue, 0, cStringFree("dview.copy.src.data"))
	srcLen := C.LLVMBuildExtractValue(s.builder, srcValue, 1, cStringFree("dview.copy.src.len"))
	srcElemSize := C.LLVMBuildExtractValue(s.builder, srcValue, 2, cStringFree("dview.copy.src.elem_size"))
	dstBytes := C.LLVMBuildMul(s.builder, dstLen, dstElemSize, cStringFree("dview.copy.dst.bytes"))
	srcBytes := C.LLVMBuildMul(s.builder, srcLen, srcElemSize, cStringFree("dview.copy.src.bytes"))
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, true, err
	}
	zeroBytes := C.LLVMConstInt(usizeType, 0, 0)
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	memcpyType := s.g.cachedRuntimeHelperType("arena_memcpy", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_memcpy", Params: []semantic.Type{voidRefType, voidRefType, s.g.result.NamedTypes["usize"]}, Return: voidRefType}
	})
	memcpyCallee, err := s.g.ensureFunctionDeclared("arena_memcpy", memcpyType)
	if err != nil {
		return nil, nil, true, err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return nil, nil, true, err
	}
	buildMemcpyNoAlias := func(byteCount C.LLVMValueRef) {
		memcpyCall := s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{dstData, srcData, byteCount}, "dview.copy.memcpy")
		s.addCallSiteEnumAttribute(memcpyCall, C.uint(1), "noalias")
		s.addCallSiteEnumAttribute(memcpyCall, C.uint(2), "noalias")
	}

	if s.g.result.ExprsHaveEqualExtentSize(dstExpr, srcExpr) {
		zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), dstBytes, zeroBytes, cStringFree("dview.copy.bytes.zero"))
		copyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.copy.exact.fast"))
		mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.copy.exact.merge"))
		C.LLVMBuildCondBr(s.builder, zeroCond, mergeBB, copyBB)

		C.LLVMPositionBuilderAtEnd(s.builder, copyBB)
		buildMemcpyNoAlias(dstBytes)
		C.LLVMBuildBr(s.builder, mergeBB)

		C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
		return nil, funcType.Return, true, nil
	}

	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), dstBytes, srcBytes, cStringFree("dview.copy.bytes.eq"))
	copyCheckBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.copy.fast.check"))
	fallbackBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.copy.fallback"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.copy.merge"))
	C.LLVMBuildCondBr(s.builder, lenEqual, copyCheckBB, fallbackBB)

	C.LLVMPositionBuilderAtEnd(s.builder, copyCheckBB)
	zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), dstBytes, zeroBytes, cStringFree("dview.copy.bytes.zero"))
	copyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.copy.fast"))
	C.LLVMBuildCondBr(s.builder, zeroCond, mergeBB, copyBB)

	C.LLVMPositionBuilderAtEnd(s.builder, copyBB)
	buildMemcpyNoAlias(dstBytes)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, fallbackBB)
	fallbackCall := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{dstValue, srcValue}, "")
	_ = fallbackCall
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	return nil, funcType.Return, true, nil
}

func (s *functionState) emitSpecializedArenaViewEqCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "arena_da_eq_exact" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	leftExpr := expr.Args[0]
	rightExpr := expr.Args[1]
	leftType := s.exprType(leftExpr)
	rightType := s.exprType(rightExpr)
	resultType := s.exprType(expr)
	if resultType == nil {
		resultType = s.g.result.NamedTypes["bool"]
	}
	if !isDynArrayViewCarrierType(leftType) || !isDynArrayViewCarrierType(rightType) {
		return nil, nil, false, nil
	}
	if !s.g.result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		return nil, nil, false, nil
	}
	disjoint := s.g.result.ExprsAreDisjoint(leftExpr, rightExpr)
	exactEqByteCount := uint64(0)
	hasSmallExactEqByteCount := false
	if elemType, ok := runtimeIndexedElemType(leftType); ok {
		if elemSize, err := s.sizeOfType(elemType); err == nil && elemSize != 0 {
			if leftFacts, ok := s.g.result.ExprOptimizationFacts(leftExpr); ok {
				if leftCount, ok := constOptimizationExtentSize(leftFacts.Extent); ok {
					if rightFacts, ok := s.g.result.ExprOptimizationFacts(rightExpr); ok {
						if rightCount, ok := constOptimizationExtentSize(rightFacts.Extent); ok && rightCount == leftCount {
							totalBytes := leftCount * elemSize
							if totalBytes <= smallExactArenaEqUnrollByteLimit {
								exactEqByteCount = totalBytes
								hasSmallExactEqByteCount = true
							}
						}
					}
				}
			}
		}
	}

	leftValue, _, err := s.emitExpr(leftExpr, leftType)
	if err != nil {
		return nil, nil, true, err
	}
	rightValue, _, err := s.emitExpr(rightExpr, rightType)
	if err != nil {
		return nil, nil, true, err
	}
	leftData := C.LLVMBuildExtractValue(s.builder, leftValue, 0, cStringFree("dview.eq.left.data"))
	leftLen := C.LLVMBuildExtractValue(s.builder, leftValue, 1, cStringFree("dview.eq.left.len"))
	leftElemSize := C.LLVMBuildExtractValue(s.builder, leftValue, 2, cStringFree("dview.eq.left.elem_size"))
	rightData := C.LLVMBuildExtractValue(s.builder, rightValue, 0, cStringFree("dview.eq.right.data"))
	if hasSmallExactEqByteCount {
		if exactEqByteCount == 0 {
			return C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0), resultType, true, nil
		}
		byteType := C.LLVMInt8TypeInContext(s.g.context)
		usizeType, err := s.g.lowerBuiltin("usize")
		if err != nil {
			return nil, nil, true, err
		}
		cmpResult := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0)
		domainName := ""
		leftScopeName := ""
		rightScopeName := ""
		if disjoint {
			domainName = fmt.Sprintf("llctx.dview.eq.%p.domain", expr)
			leftScopeName = domainName + ".left"
			rightScopeName = domainName + ".right"
		}
		for i := uint64(0); i < exactEqByteCount; i++ {
			indexValue := C.LLVMConstInt(usizeType, C.ulonglong(i), 0)
			indices := []C.LLVMValueRef{indexValue}
			leftBytePtr := C.LLVMBuildGEP2(s.builder, byteType, leftData, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("dview.eq.left.byte.ptr"))
			leftByte := C.LLVMBuildLoad2(s.builder, byteType, leftBytePtr, cStringFree("dview.eq.left.byte"))
			if disjoint {
				s.attachAliasScopeMetadataWithNames(leftByte, domainName, leftScopeName, []string{rightScopeName})
			}
			rightBytePtr := C.LLVMBuildGEP2(s.builder, byteType, rightData, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("dview.eq.right.byte.ptr"))
			rightByte := C.LLVMBuildLoad2(s.builder, byteType, rightBytePtr, cStringFree("dview.eq.right.byte"))
			if disjoint {
				s.attachAliasScopeMetadataWithNames(rightByte, domainName, rightScopeName, []string{leftScopeName})
			}
			bytesEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), leftByte, rightByte, cStringFree("dview.eq.byte.eq"))
			cmpResult = C.LLVMBuildAnd(s.builder, cmpResult, bytesEqual, cStringFree("dview.eq.byte.and"))
		}
		return cmpResult, resultType, true, nil
	}
	_ = C.LLVMBuildExtractValue(s.builder, rightValue, 1, cStringFree("dview.eq.right.len"))
	_ = C.LLVMBuildExtractValue(s.builder, rightValue, 2, cStringFree("dview.eq.right.elem_size"))
	byteCount := C.LLVMBuildMul(s.builder, leftLen, leftElemSize, cStringFree("dview.eq.bytes"))
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, true, err
	}
	zeroBytes := C.LLVMConstInt(usizeType, 0, 0)
	zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), byteCount, zeroBytes, cStringFree("dview.eq.bytes.zero"))
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	memcmpBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.eq.memcmp"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.eq.merge"))
	C.LLVMBuildCondBr(s.builder, zeroCond, mergeBB, memcmpBB)

	C.LLVMPositionBuilderAtEnd(s.builder, memcmpBB)
	cmp, err := s.emitMemcmpEqualValue(leftData, rightData, byteCount, "dview.eq.memcmp", disjoint)
	if err != nil {
		return nil, nil, true, err
	}
	memcmpEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("dview.eq.result"))
	trueValue := C.LLVMConstInt(boolType, 1, 0)
	values := []C.LLVMValueRef{trueValue, cmp}
	blocks := []C.LLVMBasicBlockRef{entryBlock, memcmpEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, true, nil
}

func (s *functionState) emitSpecializedArenaFromViewCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "arena_da_from_view" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	arenaExpr := expr.Args[0]
	viewExpr := expr.Args[1]
	arenaType := s.exprType(arenaExpr)
	viewType := s.exprType(viewExpr)
	resultType := s.exprType(expr)
	if !isDynArrayViewCarrierType(viewType) || !isDynArrayCarrierType(resultType) {
		return nil, nil, false, nil
	}
	viewFacts, ok := s.g.result.ExprOptimizationFacts(viewExpr)
	if !ok || !viewFacts.HasExactExtent() {
		return nil, nil, false, nil
	}
	exactMaterializeCount := uint64(0)
	hasSmallExactMaterializeCount := false
	if elemType, ok := runtimeIndexedElemType(viewType); ok {
		if elemSize, err := s.sizeOfType(elemType); err == nil && elemSize != 0 {
			if count, ok := constOptimizationExtentSize(viewFacts.Extent); ok && count <= smallExactArenaCopyUnrollLimit {
				exactMaterializeCount = count
				hasSmallExactMaterializeCount = true
			}
		}
	}
	arenaValue, _, err := s.emitExpr(arenaExpr, arenaType)
	if err != nil {
		return nil, nil, true, err
	}
	viewValue, _, err := s.emitExpr(viewExpr, viewType)
	if err != nil {
		return nil, nil, true, err
	}
	llvmResultType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroResult, err := s.zeroValue(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	viewData := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("dview.materialize.src.data"))
	viewLen := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("dview.materialize.src.len"))
	viewElemSize := C.LLVMBuildExtractValue(s.builder, viewValue, 2, cStringFree("dview.materialize.src.elem_size"))
	byteCount := C.LLVMBuildMul(s.builder, viewLen, viewElemSize, cStringFree("dview.materialize.bytes"))
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroBytes := C.LLVMConstInt(usizeLLVMType, 0, 0)
	zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), byteCount, zeroBytes, cStringFree("dview.materialize.bytes.zero"))
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	allocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.materialize.alloc"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.materialize.merge"))
	C.LLVMBuildCondBr(s.builder, zeroCond, mergeBB, allocBB)

	C.LLVMPositionBuilderAtEnd(s.builder, allocBB)
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	if _, ok := resultType.(*semantic.DArrayType); !ok {
		return nil, nil, true, fmt.Errorf("arena_da_from_view specialization expected darray result type, got %T", resultType)
	}
	allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaType, usizeType}, Return: voidRefType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return nil, nil, true, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, nil, true, err
	}
	allocPtr := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{arenaValue, byteCount}, "dview.materialize.alloc")
	if hasSmallExactMaterializeCount {
		if exactMaterializeCount != 0 {
			elemType, ok := runtimeIndexedElemType(viewType)
			if !ok {
				return nil, nil, true, fmt.Errorf("arena_da_from_view specialization expected dview element type")
			}
			elemLLVMType, err := s.g.lowerType(elemType)
			if err != nil {
				return nil, nil, true, err
			}
			indexLLVMType, err := s.g.lowerBuiltin("usize")
			if err != nil {
				return nil, nil, true, err
			}
			domainName := fmt.Sprintf("llctx.dview.materialize.%p.domain", expr)
			dstScopeName := domainName + ".dst"
			srcScopeName := domainName + ".src"
			for i := uint64(0); i < exactMaterializeCount; i++ {
				indexValue := C.LLVMConstInt(indexLLVMType, C.ulonglong(i), 0)
				indices := []C.LLVMValueRef{indexValue}
				srcPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, viewData, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("dview.materialize.src.elem.ptr"))
				elemValue := C.LLVMBuildLoad2(s.builder, elemLLVMType, srcPtr, cStringFree("dview.materialize.elem"))
				s.attachAliasScopeMetadataWithNames(elemValue, domainName, srcScopeName, []string{dstScopeName})
				dstPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, allocPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("dview.materialize.dst.elem.ptr"))
				store := C.LLVMBuildStore(s.builder, elemValue, dstPtr)
				s.attachAliasScopeMetadataWithNames(store, domainName, dstScopeName, []string{srcScopeName})
			}
		}
	} else {

		memcpyType := s.g.cachedRuntimeHelperType("arena_memcpy", func() *semantic.FuncType {
			return &semantic.FuncType{Name: "arena_memcpy", Params: []semantic.Type{voidRefType, voidRefType, usizeType}, Return: voidRefType}
		})
		memcpyCallee, err := s.g.ensureFunctionDeclared("arena_memcpy", memcpyType)
		if err != nil {
			return nil, nil, true, err
		}
		memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
		if err != nil {
			return nil, nil, true, err
		}
		memcpyCall := s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{allocPtr, viewData, byteCount}, "dview.materialize.memcpy")
		s.addCallSiteEnumAttribute(memcpyCall, C.uint(1), "noalias")
		s.addCallSiteEnumAttribute(memcpyCall, C.uint(2), "noalias")
	}

	materialized := C.LLVMGetUndef(llvmResultType)
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, allocPtr, 0, cStringFree("dview.materialize.items"))
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, viewLen, 1, cStringFree("dview.materialize.count"))
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, viewLen, 2, cStringFree("dview.materialize.capacity"))
	allocEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phi := C.LLVMBuildPhi(s.builder, llvmResultType, cStringFree("dview.materialize.result"))
	values := []C.LLVMValueRef{zeroResult, materialized}
	blocks := []C.LLVMBasicBlockRef{entryBlock, allocEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, true, nil
}

func (s *functionState) emitSpecializedArenaViewFillCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "arena_da_fill" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	dstExpr := expr.Args[0]
	dstType := s.exprType(dstExpr)
	resultType := s.exprType(expr)
	fillExpr := expr.Args[1]
	_, funcType, err := s.resolveCallTarget(expr)
	if err != nil {
		return nil, nil, true, err
	}
	if funcType == nil || len(funcType.Params) != 2 {
		return nil, nil, true, fmt.Errorf("arena_da_fill target does not have the expected function type")
	}
	fillType := funcType.Params[1]
	fillByte, constByte := staticRepeatedByteFillValueForType(s, fillExpr, fillType)
	dynamicByte := !constByte && isSingleByteScalarFillType(s, fillType)
	if !isDynArrayViewCarrierType(dstType) || !s.g.result.ExprSupportsDenseWrite(dstExpr) {
		return nil, nil, false, nil
	}
	exactFillCount := uint64(0)
	hasSmallExactFillCount := false
	if facts, ok := s.g.result.ExprOptimizationFacts(dstExpr); ok {
		if count, ok := constOptimizationExtentSize(facts.Extent); ok && count <= smallExactArenaFillUnrollLimit {
			exactFillCount = count
			hasSmallExactFillCount = true
		}
	}
	if !hasSmallExactFillCount && !constByte && !dynamicByte {
		return nil, nil, false, nil
	}
	dstValue, _, err := s.emitExpr(dstExpr, dstType)
	if err != nil {
		return nil, nil, true, err
	}
	fillRawValue, actualFillType, err := s.emitExpr(fillExpr, fillType)
	if err != nil {
		return nil, nil, true, err
	}
	typedFillValue, err := s.coerceValue(fillRawValue, actualFillType, fillType)
	if err != nil {
		return nil, nil, true, err
	}
	dstData := C.LLVMBuildExtractValue(s.builder, dstValue, 0, cStringFree("dview.fill.dst.data"))
	if hasSmallExactFillCount {
		if exactFillCount == 0 {
			return nil, resultType, true, nil
		}
		elemLLVMType, err := s.g.lowerType(fillType)
		if err != nil {
			return nil, nil, true, err
		}
		usizeType, err := s.g.lowerBuiltin("usize")
		if err != nil {
			return nil, nil, true, err
		}
		for i := uint64(0); i < exactFillCount; i++ {
			indexValue := C.LLVMConstInt(usizeType, C.ulonglong(i), 0)
			indices := []C.LLVMValueRef{indexValue}
			elemPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, dstData, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("dview.fill.elem.ptr"))
			C.LLVMBuildStore(s.builder, typedFillValue, elemPtr)
		}
		return nil, resultType, true, nil
	}
	var fillValue C.LLVMValueRef
	if constByte {
		fillValue = C.LLVMConstInt(C.LLVMInt32TypeInContext(s.g.context), C.ulonglong(fillByte), 0)
	} else {
		fillValue, err = s.coerceValue(typedFillValue, fillType, s.g.result.NamedTypes["i32"])
		if err != nil {
			return nil, nil, true, err
		}
	}
	dstLen := C.LLVMBuildExtractValue(s.builder, dstValue, 1, cStringFree("dview.fill.dst.len"))
	dstElemSize := C.LLVMBuildExtractValue(s.builder, dstValue, 2, cStringFree("dview.fill.dst.elem_size"))
	dstBytes := C.LLVMBuildMul(s.builder, dstLen, dstElemSize, cStringFree("dview.fill.dst.bytes"))
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, true, err
	}
	zeroBytes := C.LLVMConstInt(usizeType, 0, 0)
	zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), dstBytes, zeroBytes, cStringFree("dview.fill.bytes.zero"))
	fillBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.fill.fast"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dview.fill.merge"))
	C.LLVMBuildCondBr(s.builder, zeroCond, mergeBB, fillBB)

	C.LLVMPositionBuilderAtEnd(s.builder, fillBB)
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	memsetValueType := s.g.result.NamedTypes["int"]
	memsetType := &semantic.FuncType{Name: "memset", Params: []semantic.Type{voidRefType, memsetValueType, s.g.result.NamedTypes["usize"]}, Return: voidRefType}
	memsetCallee, err := s.g.ensureFunctionDeclared("memset", memsetType)
	if err != nil {
		return nil, nil, true, err
	}
	fillValue, err = s.coerceValue(fillValue, s.g.result.NamedTypes["i32"], memsetValueType)
	if err != nil {
		return nil, nil, true, err
	}
	memsetLLVMType, err := s.g.lowerFunctionType(memsetType)
	if err != nil {
		return nil, nil, true, err
	}
	_ = s.buildCall(memsetLLVMType, memsetCallee, []C.LLVMValueRef{dstData, fillValue, dstBytes}, "dview.fill.memset")
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	return nil, resultType, true, nil
}

func (s *functionState) emitStringViewStaticLiteralEqual(viewExpr ast.Expr, viewType semantic.Type, literalExpr ast.Expr, literalText string) (C.LLVMValueRef, error) {
	viewValue, _, err := s.emitExpr(viewExpr, viewType)
	if err != nil {
		return nil, err
	}
	viewData := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("svlit.data"))
	viewLen := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("svlit.len"))
	literalLen := len([]byte(literalText))
	lenLLVMType, err := s.g.lowerBuiltin("i64")
	if err != nil {
		return nil, err
	}
	lenValue := C.LLVMConstInt(lenLLVMType, C.ulonglong(literalLen), 0)
	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), viewLen, lenValue, cStringFree("svlit.len.eq"))
	if literalLen == 0 {
		return lenEqual, nil
	}

	entryBlock := C.LLVMGetInsertBlock(s.builder)
	compareBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svlit.compare"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svlit.merge"))
	C.LLVMBuildCondBr(s.builder, lenEqual, compareBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, compareBB)
	var compareValue C.LLVMValueRef
	if literalLen <= 8 {
		compareValue, err = s.emitStringViewLiteralBytesEqual(viewData, literalText)
	} else {
		literalValue, _, emitErr := s.emitExpr(literalExpr, nil)
		if emitErr != nil {
			return nil, emitErr
		}
		compareValue, err = s.emitMemcmpEqual(viewData, literalValue, literalLen)
	}
	if err != nil {
		return nil, err
	}
	compareEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("svlit.eq"))
	falseValue := C.LLVMConstInt(boolType, 0, 0)
	values := []C.LLVMValueRef{falseValue, compareValue}
	blocks := []C.LLVMBasicBlockRef{entryBlock, compareEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, nil
}

func (s *functionState) emitDStrStaticLiteralEqual(textExpr ast.Expr, textType semantic.Type, literalExpr ast.Expr, literalText string) (C.LLVMValueRef, error) {
	if classifyRuntimeStringCompareKind(textType) != runtimeStringCompareDStr {
		return nil, fmt.Errorf("dstr literal specialization requires dstr operand")
	}
	lenType := s.g.result.NamedTypes["i64"]
	var (
		textData C.LLVMValueRef
		textLen  C.LLVMValueRef
		err      error
	)
	if baseExpr, baseType, start, end, ok := s.constantDStrSliceCall(textExpr); ok {
		textData, textLen, err = s.emitConstantClampedStringSliceOperand(baseExpr, baseType, start, end, "dstrlit.slice")
		if err != nil {
			return nil, err
		}
	} else {
		textValue, _, err := s.emitExpr(textExpr, textType)
		if err != nil {
			return nil, err
		}
		textData = textValue
		textLen, err = s.emitRuntimeStringLengthValue(textValue, textType, lenType, "dstrlit.len")
		if err != nil {
			return nil, err
		}
	}
	literalLen := len([]byte(literalText))
	lenLLVMType, err := s.g.lowerBuiltin("i64")
	if err != nil {
		return nil, err
	}
	lenValue := C.LLVMConstInt(lenLLVMType, C.ulonglong(literalLen), 0)
	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), textLen, lenValue, cStringFree("dstrlit.len.eq"))
	if literalLen == 0 {
		return lenEqual, nil
	}

	entryBlock := C.LLVMGetInsertBlock(s.builder)
	compareBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dstrlit.compare"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dstrlit.merge"))
	C.LLVMBuildCondBr(s.builder, lenEqual, compareBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, compareBB)
	var compareValue C.LLVMValueRef
	if literalLen <= 8 {
		compareValue, err = s.emitStringViewLiteralBytesEqual(textData, literalText)
	} else {
		literalValue, _, emitErr := s.emitExpr(literalExpr, nil)
		if emitErr != nil {
			return nil, emitErr
		}
		compareValue, err = s.emitMemcmpEqual(textData, literalValue, literalLen)
	}
	if err != nil {
		return nil, err
	}
	compareEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("dstrlit.eq"))
	falseValue := C.LLVMConstInt(boolType, 0, 0)
	values := []C.LLVMValueRef{falseValue, compareValue}
	blocks := []C.LLVMBasicBlockRef{entryBlock, compareEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, nil
}

func (s *functionState) emitStringViewLiteralBytesEqual(viewData C.LLVMValueRef, literalText string) (C.LLVMValueRef, error) {
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	byteType := C.LLVMInt8TypeInContext(s.g.context)
	indexType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	result := C.LLVMConstInt(boolType, 1, 0)
	for i, b := range []byte(literalText) {
		indexValue := C.LLVMConstInt(indexType, C.ulonglong(i), 0)
		indices := []C.LLVMValueRef{indexValue}
		bytePtr := C.LLVMBuildGEP2(s.builder, byteType, viewData, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("svlit.byte.ptr"))
		byteValue := C.LLVMBuildLoad2(s.builder, byteType, bytePtr, cStringFree("svlit.byte"))
		literalValue := C.LLVMConstInt(byteType, C.ulonglong(b), 0)
		byteEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), byteValue, literalValue, cStringFree("svlit.byte.eq"))
		result = C.LLVMBuildAnd(s.builder, result, byteEqual, cStringFree("svlit.bytes.and"))
	}
	return result, nil
}

func (s *functionState) emitMemcmpEqual(left C.LLVMValueRef, right C.LLVMValueRef, length int) (C.LLVMValueRef, error) {
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	lengthValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(length), 0)
	return s.emitMemcmpEqualValue(left, right, lengthValue, "svlit.memcmp", false)
}

func (s *functionState) emitMemcmpEqualValue(left C.LLVMValueRef, right C.LLVMValueRef, lengthValue C.LLVMValueRef, callName string, noAliasArgs bool) (C.LLVMValueRef, error) {
	voidType := s.g.result.NamedTypes["void"]
	usizeType := s.g.result.NamedTypes["usize"]
	intType := s.g.result.NamedTypes["int"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{
		Name:   "memcmp",
		Params: []semantic.Type{voidRefType, voidRefType, usizeType},
		Return: intType,
	}
	callee, err := s.g.ensureFunctionDeclared("memcmp", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	call := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{left, right, lengthValue}, callName)
	if noAliasArgs {
		s.addCallSiteEnumAttribute(call, C.uint(1), "noalias")
		s.addCallSiteEnumAttribute(call, C.uint(2), "noalias")
	}
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, err
	}
	zero := C.LLVMConstInt(intLLVMType, 0, 0)
	return C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), call, zero, cStringFree(callName+".eq")), nil
}

func (s *functionState) staticCStringLiteral(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.StringLit:
		return n.Value, true
	case *ast.ParenExpr:
		return s.staticCStringLiteral(n.Inner)
	case *ast.CastExpr:
		return s.staticCStringLiteral(n.Operand)
	case *ast.Ident:
		value, ok := s.g.constValue(n.Name)
		if !ok || value.Kind != semantic.ConstString {
			return "", false
		}
		return value.String, true
	default:
		return "", false
	}
}

func (s *functionState) emitLogicalExpr(expr *ast.BinaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	left, _, err := s.emitExpr(expr.Left, s.g.result.NamedTypes["bool"])
	if err != nil {
		return nil, nil, err
	}
	parentBlock := C.LLVMGetInsertBlock(s.builder)
	rhsName := cString("logic.rhs")
	defer C.free(unsafe.Pointer(rhsName))
	rhsBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, rhsName)
	mergeName := cString("logic.end")
	defer C.free(unsafe.Pointer(mergeName))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, mergeName)

	if expr.Op == lexer.TOKEN_AND {
		C.LLVMBuildCondBr(s.builder, left, rhsBB, mergeBB)
	} else {
		C.LLVMBuildCondBr(s.builder, left, mergeBB, rhsBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, rhsBB)
	right, _, err := s.emitExpr(expr.Right, s.g.result.NamedTypes["bool"])
	if err != nil {
		return nil, nil, err
	}
	rhsEnd := C.LLVMGetInsertBlock(s.builder)
	if C.LLVMGetBasicBlockTerminator(rhsEnd) == nil {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phi := C.LLVMBuildPhi(s.builder, C.LLVMInt1TypeInContext(s.g.context), cStringFree("logicphi"))
	fallback := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 0, 0)
	if expr.Op == lexer.TOKEN_OR {
		fallback = C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0)
	}
	values := []C.LLVMValueRef{fallback, right}
	blocks := []C.LLVMBasicBlockRef{parentBlock, rhsEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, s.g.result.NamedTypes["bool"], nil
}

func (s *functionState) emitUnaryExpr(expr *ast.UnaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	operandType := s.exprType(expr.Operand)
	value, _, err := s.emitExpr(expr.Operand, operandType)
	if err != nil {
		return nil, nil, err
	}
	resultType := s.exprType(expr)
	switch expr.Op {
	case lexer.TOKEN_NOT:
		return C.LLVMBuildNot(s.builder, value, cStringFree("nottmp")), resultType, nil
	case lexer.TOKEN_MINUS:
		if isFloatType(operandType) {
			return C.LLVMBuildFNeg(s.builder, value, cStringFree("negtmp")), resultType, nil
		}
		return C.LLVMBuildNeg(s.builder, value, cStringFree("negtmp")), resultType, nil
	case lexer.TOKEN_TILDE:
		return C.LLVMBuildNot(s.builder, value, cStringFree("invt")), resultType, nil
	default:
		return nil, nil, fmt.Errorf("unsupported unary operator %s", lexer.TokenName(expr.Op))
	}
}

func (s *functionState) emitAllocExpr(expr *ast.AllocExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr.Owner == nil {
		return s.emitScopedPackedAllocExpr(expr)
	}
	if updateExpr, ok := expr.Value.(*ast.RecordUpdateExpr); ok && updateExpr != nil {
		memberType := semantic.StripAggregateStateType(s.exprType(updateExpr.Base))
		if _, exact := semantic.TreeExactTag(memberType); exact {
			owner, ownerOK, err := s.classifyTreeAllocOwnerExpr(expr.Owner)
			if err != nil {
				return nil, nil, err
			}
			if !ownerOK {
				return nil, nil, fmt.Errorf("tree allocation owner must be perm, a tree store, an Arena value, or an Arena reference")
			}
			return s.emitTreeExactMemberUpdateExpr(updateExpr, memberType, &owner)
		}
	}
	if callExpr, ok := expr.Value.(*ast.CallExpr); ok {
		if memberType, ok := s.treeExactMemberConstructorCall(callExpr); ok {
			owner, ownerOK, err := s.classifyTreeAllocOwnerExpr(expr.Owner)
			if err != nil {
				return nil, nil, err
			}
			if !ownerOK {
				return nil, nil, fmt.Errorf("tree allocation owner must be perm, a tree store, an Arena value, or an Arena reference")
			}
			return s.emitTreeExactMemberConstructorValue(callExpr, memberType, &owner)
		}
	}
	if treeType, variant, callExpr, ok := s.treeAllocConstructorInfo(expr.Value); ok {
		if variant == nil {
			return nil, nil, fmt.Errorf("unknown tree constructor")
		}
		owner, ownerOK, err := s.classifyTreeAllocOwnerExpr(expr.Owner)
		if err != nil {
			return nil, nil, err
		}
		if !ownerOK {
			return nil, nil, fmt.Errorf("tree allocation owner must be perm, a tree store, an Arena value, or an Arena reference")
		}
		return s.emitTreeConstructorValue(callExpr, treeType, variant, treeAllocArgs(callExpr), treeAllocArgNames(callExpr), &owner)
	}
	if isTreeAllocPermExpr(expr.Owner) {
		return nil, nil, fmt.Errorf("new[perm] expects a tree constructor")
	}
	if _, ok := s.exprType(expr.Owner).(*semantic.PackedEnumStoreType); ok {
		return s.emitPackedAllocExpr(expr)
	}
	ownerIdent, ok := expr.Owner.(*ast.Ident)
	if !ok {
		return nil, nil, fmt.Errorf("only region-backed new[...] is lowered so far")
	}
	binding, ok := s.lookupBinding(ownerIdent.Name)
	if !ok {
		return nil, nil, fmt.Errorf("unknown region %q during LLVM lowering", ownerIdent.Name)
	}
	valueType := s.exprType(expr.Value)
	if valueType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for region allocation value in %q", ownerIdent.Name)
	}
	value, _, err := s.emitExpr(expr.Value, valueType)
	if err != nil {
		return nil, nil, err
	}
	sizeBytes, err := s.sizeOfType(valueType)
	if err != nil {
		return nil, nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, err
	}
	sizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(sizeBytes), 0)
	arenaRefType := &semantic.RefType{Elem: binding.typ, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return nil, nil, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, nil, err
	}
	allocPtr := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{binding.ptr, sizeValue}, "region.alloc")
	C.LLVMBuildStore(s.builder, value, allocPtr)
	return allocPtr, s.exprType(expr), nil
}

func (s *functionState) emitScopedPackedAllocExpr(expr *ast.AllocExpr) (C.LLVMValueRef, semantic.Type, error) {
	switch n := expr.Value.(type) {
	case *ast.FieldExpr:
		enumType, variant, ok := s.enumConstructorInfoFromField(n)
		if !ok || enumType == nil || variant == nil || !enumType.Packed {
			return nil, nil, fmt.Errorf("new without [...] expects a packed enum constructor inside an in-store block")
		}
		store, ok := s.lookupPackedStore(enumType)
		if !ok {
			return nil, nil, fmt.Errorf("missing active packed enum store for %s", enumType.Name)
		}
		return s.emitPackedEnumConstructorAlloc(nil, store.value, enumType, variant, nil, nil)
	case *ast.CallExpr:
		enumType, variant, ok := s.enumConstructorInfo(n)
		if !ok || enumType == nil || variant == nil || !enumType.Packed {
			return nil, nil, fmt.Errorf("new without [...] expects a packed enum constructor inside an in-store block")
		}
		store, ok := s.lookupPackedStore(enumType)
		if !ok {
			return nil, nil, fmt.Errorf("missing active packed enum store for %s", enumType.Name)
		}
		return s.emitPackedEnumConstructorAlloc(n, store.value, enumType, variant, n.Args, n.ArgNames)
	default:
		return nil, nil, fmt.Errorf("new without [...] expects a packed enum constructor inside an in-store block")
	}
}

func (s *functionState) emitPackedAllocExpr(expr *ast.AllocExpr) (C.LLVMValueRef, semantic.Type, error) {
	storeValue, _, err := s.emitExpr(expr.Owner, nil)
	if err != nil {
		return nil, nil, err
	}
	if fieldExpr, ok := expr.Value.(*ast.FieldExpr); ok {
		enumType, variant, ok := s.enumConstructorInfoFromField(fieldExpr)
		if ok && enumType != nil && variant != nil && enumType.Packed && len(variant.Payload) == 0 {
			return s.emitPackedEnumConstructorAlloc(nil, storeValue, enumType, variant, nil, nil)
		}
	}
	callExpr, ok := expr.Value.(*ast.CallExpr)
	if !ok {
		return nil, nil, fmt.Errorf("packed enum allocation expects a constructor call")
	}
	enumType, variant, ok := s.enumConstructorInfo(callExpr)
	if !ok || enumType == nil || variant == nil || !enumType.Packed {
		return nil, nil, fmt.Errorf("packed enum allocation expects a packed enum constructor call")
	}
	return s.emitPackedEnumConstructorAlloc(callExpr, storeValue, enumType, variant, callExpr.Args, callExpr.ArgNames)
}

func (s *functionState) nodeTableFillTypeArgs(expr *ast.CallExpr) (*semantic.EnumType, semantic.Type, error) {
	if expr == nil || callSpecializedIdentName(expr) != "node_table_fill" {
		return nil, nil, fmt.Errorf("node_table_fill expects explicit specialization")
	}
	_, specialize, ok := callSpecializedIdent(expr.Func)
	if !ok || specialize == nil || len(specialize.TypeArgs) != 2 {
		return nil, nil, fmt.Errorf("node_table_fill expects exactly 2 type arguments")
	}
	enumArg, err := s.resolveTypeExpr(specialize.TypeArgs[0])
	if err != nil {
		return nil, nil, err
	}
	enumType, ok := semantic.StripAggregateStateType(enumArg).(*semantic.EnumType)
	if !ok || enumType == nil || !enumType.Packed {
		return nil, nil, fmt.Errorf("node_table_fill expects a packed enum type argument")
	}
	elemType, err := s.resolveTypeExpr(specialize.TypeArgs[1])
	if err != nil {
		return nil, nil, err
	}
	return enumType, elemType, nil
}

func (s *functionState) emitNodeKeyIndexValue(expr ast.Expr) (C.LLVMValueRef, *semantic.EnumType, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	keyType := s.exprType(expr)
	enumType, ok := semantic.NodeKeyEnumType(keyType)
	if !ok || enumType == nil {
		return nil, nil, false, nil
	}
	value, actualType, err := s.emitExpr(expr, keyType)
	if err != nil {
		return nil, nil, true, err
	}
	if refType, ok := actualType.(*semantic.RefType); ok && refType != nil && refType.State == semantic.RefStateNonNull {
		loaded, loadErr := s.loadValue(value, refType.Elem, "nodekey.load")
		if loadErr != nil {
			return nil, nil, true, loadErr
		}
		value = loaded
	}
	return C.LLVMBuildExtractValue(s.builder, value, 0, cStringFree("nodekey.index")), enumType, true, nil
}

func (s *functionState) emitDenseKeyHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if callIdentName(expr) != "dense_key" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 {
		return nil, nil, true, fmt.Errorf("dense_key expects 2 arguments, got %d", len(expr.Args))
	}
	resultType := s.exprType(expr)
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	_, storeType, err := s.emitPackedStoreValueFromExpr(expr.Args[1])
	if err != nil {
		return nil, nil, true, err
	}
	if storeType == nil || storeType.Enum == nil {
		return nil, nil, true, fmt.Errorf("dense_key requires frozen packed-store metadata")
	}
	var handleValue C.LLVMValueRef
	actualNodeType := s.exprType(expr.Args[0])
	sourceEnum, ok := denseKeySourceEnumType(actualNodeType)
	if !ok || sourceEnum == nil {
		return nil, nil, true, fmt.Errorf("dense_key expects a packed enum value or packedview")
	}
	if viewType, ok := actualNodeType.(*semantic.PackedVariantViewType); ok && viewType != nil {
		viewValue, _, err := s.emitExpr(expr.Args[0], actualNodeType)
		if err != nil {
			return nil, nil, true, err
		}
		handleValue = C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("nodekey.view.handle"))
	} else {
		var actualType semantic.Type
		handleValue, actualType, err = s.emitExpr(expr.Args[0], actualNodeType)
		if err != nil {
			return nil, nil, true, err
		}
		if refType, ok := actualType.(*semantic.RefType); ok && refType != nil && refType.State == semantic.RefStateNonNull {
			handleValue, err = s.loadValue(handleValue, refType.Elem, "nodekey.handle")
			if err != nil {
				return nil, nil, true, err
			}
		}
	}
	var indexValue C.LLVMValueRef
	switch s.g.packedModeForEnum(sourceEnum) {
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse:
		indexValue, err = s.coerceValue(handleValue, sourceEnum, s.g.result.NamedTypes["u32"])
		if err != nil {
			return nil, nil, true, err
		}
	default:
		return nil, nil, true, fmt.Errorf("unsupported packed enum ABI mode %d", s.g.packedModeForEnum(sourceEnum))
	}
	resultValue := C.LLVMGetUndef(resultLLVMType)
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, indexValue, 0, cStringFree("nodekey.index.insert"))
	return resultValue, resultType, true, nil
}

func (s *functionState) emitNodeTableFillHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if callSpecializedIdentName(expr) != "node_table_fill" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 3 {
		return nil, nil, true, fmt.Errorf("node_table_fill expects 3 arguments, got %d", len(expr.Args))
	}
	resultType := s.exprType(expr)
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	_, elemType, err := s.nodeTableFillTypeArgs(expr)
	if err != nil {
		return nil, nil, true, err
	}
	storeValue, storeType, err := s.emitPackedStoreValueFromExpr(expr.Args[1])
	if err != nil {
		return nil, nil, true, err
	}
	if storeType == nil || storeType.Enum == nil {
		return nil, nil, true, fmt.Errorf("node_table_fill requires frozen packed-store metadata")
	}
	countValue, err := s.emitPackedStoreCountValue(storeValue, storeType, "node.table.count")
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroCount := C.LLVMConstInt(usizeLLVMType, 0, 0)
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	allocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("node.table.alloc"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("node.table.merge"))
	isZero := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), countValue, zeroCount, cStringFree("node.table.count.zero"))
	C.LLVMBuildCondBr(s.builder, isZero, mergeBB, allocBB)

	C.LLVMPositionBuilderAtEnd(s.builder, allocBB)
	arenaPtr, _, err := s.emitAddressOrTemp(expr.Args[0])
	if err != nil {
		return nil, nil, true, err
	}
	elemSize, err := s.sizeOfType(elemType)
	if err != nil {
		return nil, nil, true, err
	}
	elemSizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(elemSize), 0)
	byteCount := C.LLVMBuildMul(s.builder, countValue, elemSizeValue, cStringFree("node.table.bytes"))
	arenaType := s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return nil, nil, true, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, nil, true, err
	}
	allocPtr := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{arenaPtr, byteCount}, "node.table.alloc.ptr")
	viewType := &semantic.DArrayViewType{Elem: elemType, SurfaceName: "dview"}
	viewLLVMType, err := s.g.lowerType(viewType)
	if err != nil {
		return nil, nil, true, err
	}
	viewValue := C.LLVMGetUndef(viewLLVMType)
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, allocPtr, 0, cStringFree("node.table.view.data"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, countValue, 1, cStringFree("node.table.view.len"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, elemSizeValue, 2, cStringFree("node.table.view.elem_size"))
	initValue, actualInitType, err := s.emitExpr(expr.Args[2], elemType)
	if err != nil {
		return nil, nil, true, err
	}
	initValue, err = s.coerceValue(initValue, actualInitType, elemType)
	if err != nil {
		return nil, nil, true, err
	}
	fillType := s.g.cachedRuntimeHelperType("arena_da_fill", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_da_fill", Params: []semantic.Type{viewType, elemType}, Return: s.g.result.NamedTypes["void"]}
	})
	fillCallee, err := s.g.ensureFunctionDeclared("arena_da_fill", fillType)
	if err != nil {
		return nil, nil, true, err
	}
	fillLLVMType, err := s.g.lowerFunctionType(fillType)
	if err != nil {
		return nil, nil, true, err
	}
	_ = s.buildCall(fillLLVMType, fillCallee, []C.LLVMValueRef{viewValue, initValue}, "")
	materialized := C.LLVMGetUndef(resultLLVMType)
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, viewValue, 0, cStringFree("node.table.values.insert"))
	allocEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	zeroResult := C.LLVMConstNull(resultLLVMType)
	phi := C.LLVMBuildPhi(s.builder, resultLLVMType, cStringFree("node.table.result"))
	values := []C.LLVMValueRef{zeroResult, materialized}
	blocks := []C.LLVMBasicBlockRef{entryBlock, allocEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, true, nil
}

func (s *functionState) emitResolvedCall(callee C.LLVMValueRef, funcType *semantic.FuncType, direct bool, args []C.LLVMValueRef) (C.LLVMValueRef, semantic.Type, error) {
	if funcType == nil {
		return nil, nil, fmt.Errorf("call target does not have a function type")
	}
	llvmFnType, err := s.g.lowerFunctionType(funcType)
	if err != nil {
		return nil, nil, err
	}
	if retUnion, ok := nonVoidErrorUnion(funcType.Return); ok {
		resultSlot, err := s.emitStackTempZeroed(retUnion.Value, "call.result")
		if err != nil {
			return nil, nil, err
		}
		callArgs := make([]C.LLVMValueRef, 0, len(args)+1)
		callArgs = append(callArgs, resultSlot)
		callArgs = append(callArgs, args...)
		var call C.LLVMValueRef
		if direct {
			call = s.buildCall(llvmFnType, callee, callArgs, "calltmp")
		} else {
			call, err = s.emitFunctionValueCall(callee, funcType, callArgs, "calltmp")
			if err != nil {
				return nil, nil, err
			}
		}
		payload, err := s.loadValue(resultSlot, retUnion.Value, "call.payload")
		if err != nil {
			return nil, nil, err
		}
		unionValue, err := s.buildErrorUnionValue(retUnion, call, payload)
		if err != nil {
			return nil, nil, err
		}
		return unionValue, funcType.Return, nil
	}
	callName := ""
	if !isVoidType(funcType.Return) {
		callName = "calltmp"
	}
	var call C.LLVMValueRef
	if direct {
		call = s.buildCall(llvmFnType, callee, args, callName)
	} else {
		call, err = s.emitFunctionValueCall(callee, funcType, args, "calltmp")
		if err != nil {
			return nil, nil, err
		}
	}
	return call, funcType.Return, nil
}

func (s *functionState) emitSafeFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || expr.Object == nil {
		return nil, nil, fmt.Errorf("optional field access requires a receiver")
	}
	resultType := s.exprType(expr)
	optionalType, ok := resultType.(*semantic.OptionalType)
	if !ok || optionalType == nil || optionalType.Value == nil {
		return nil, nil, fmt.Errorf("optional field access requires an optional result type")
	}
	presentValue, receiverValue, receiverType, err := s.emitSafeChainReceiverValue(expr.Object)
	if err != nil {
		return nil, nil, err
	}
	presentBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.field.present"))
	noneBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.field.none"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.field.merge"))
	C.LLVMBuildCondBr(s.builder, presentValue, presentBB, noneBB)

	var (
		someValue  C.LLVMValueRef
		noneValue  C.LLVMValueRef
		presentEnd C.LLVMBasicBlockRef
		noneEnd    C.LLVMBasicBlockRef
	)

	C.LLVMPositionBuilderAtEnd(s.builder, presentBB)
	payloadValue, payloadType, err := s.emitFieldValueFromObjectValue(receiverValue, receiverType, expr.Field, "safe.field")
	if err != nil {
		return nil, nil, err
	}
	payloadValue, err = s.coerceValue(payloadValue, payloadType, optionalType.Value)
	if err != nil {
		return nil, nil, err
	}
	someValue, err = s.buildOptionalSome(optionalType, payloadValue)
	if err != nil {
		return nil, nil, err
	}
	presentEnd = C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, noneBB)
	noneValue, err = s.buildOptionalNone(optionalType)
	if err != nil {
		return nil, nil, err
	}
	noneEnd = C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, resultLLVMType, cStringFree("valuephi"))
	values := []C.LLVMValueRef{someValue, noneValue}
	blocks := []C.LLVMBasicBlockRef{presentEnd, noneEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, nil
}

func (s *functionState) emitSafeCallExpr(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Object == nil {
		return nil, nil, fmt.Errorf("optional call requires member-call syntax")
	}
	resultType := s.exprType(expr)
	presentValue, receiverValue, receiverType, err := s.emitSafeChainReceiverValue(fieldExpr.Object)
	if err != nil {
		return nil, nil, err
	}
	presentBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.call.present"))
	noneBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.call.none"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.call.merge"))
	C.LLVMBuildCondBr(s.builder, presentValue, presentBB, noneBB)

	var (
		wrappedValue C.LLVMValueRef
		noneValue    C.LLVMValueRef
		presentEnd   C.LLVMBasicBlockRef
		noneEnd      C.LLVMBasicBlockRef
	)

	C.LLVMPositionBuilderAtEnd(s.builder, presentBB)
	info := (*semantic.SafeCallInfo)(nil)
	if s != nil && s.g != nil && s.g.result != nil && s.g.result.SafeCalls != nil {
		info = s.g.result.SafeCalls[expr]
	}
	var (
		callValue C.LLVMValueRef
		callType  semantic.Type
	)
	if info != nil && info.ResolvedFuncName != "" {
		receiverArgType := info.ReceiverArgType
		if receiverArgType == nil {
			receiverArgType = receiverType
		}
		fakeReceiver := &ast.ZeroedLit{Position: fieldExpr.Object.Pos()}
		synthetic := &ast.CallExpr{
			Position:                  expr.Position,
			Func:                      &ast.Ident{Position: fieldExpr.Position, Name: info.ResolvedFuncName},
			Args:                      append([]ast.Expr{fakeReceiver}, info.TailArgs...),
			ResolvedImplicitArgsValid: len(info.ImplicitArgs) != 0,
			ResolvedImplicitArgs:      append([]ast.Expr(nil), info.ImplicitArgs...),
		}
		if s.g.result.ExprTypes != nil {
			s.g.result.ExprTypes[fakeReceiver] = receiverArgType
			defer delete(s.g.result.ExprTypes, fakeReceiver)
		}
		callee, funcType, err := s.resolveCallTarget(synthetic)
		if err != nil {
			return nil, nil, err
		}
		expectedReceiverType := receiverArgType
		if len(funcType.Params) != 0 {
			expectedReceiverType = funcType.Params[0]
		}
		receiverArg, _, err := s.emitPreparedUFCSReceiverValue(receiverValue, receiverType, expectedReceiverType, "safe.call.receiver")
		if err != nil {
			return nil, nil, err
		}
		args := make([]C.LLVMValueRef, 0, 1+len(info.TailArgs)+len(info.ImplicitArgs))
		args = append(args, receiverArg)
		for i, arg := range info.TailArgs {
			paramIndex := i + 1
			var expected semantic.Type
			if paramIndex < len(funcType.Params) {
				expected = funcType.Params[paramIndex]
			}
			value, _, err := s.emitCallArg(arg, expected, funcType, paramIndex)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, value)
		}
		implicitStart := 1 + len(info.TailArgs)
		for i, arg := range info.ImplicitArgs {
			paramIndex := implicitStart + i
			var expected semantic.Type
			if paramIndex < len(funcType.Params) {
				expected = funcType.Params[paramIndex]
			}
			value, _, err := s.emitCallArg(arg, expected, funcType, paramIndex)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, value)
		}
		callValue, callType, err = s.emitResolvedCall(callee, funcType, true, args)
		if err != nil {
			return nil, nil, err
		}
	} else {
		calleeValue, calleeType, err := s.emitFieldValueFromObjectValue(receiverValue, receiverType, fieldExpr.Field, "safe.call.callee")
		if err != nil {
			return nil, nil, err
		}
		funcType, ok := calleeType.(*semantic.FuncType)
		if !ok || funcType == nil {
			return nil, nil, fmt.Errorf("cannot call non-function value of type %s", calleeType.String())
		}
		loweredArgs := expr.LoweredArgs()
		args := make([]C.LLVMValueRef, 0, len(loweredArgs))
		for i, arg := range loweredArgs {
			var expected semantic.Type
			if i < len(funcType.Params) {
				expected = funcType.Params[i]
			}
			value, _, err := s.emitCallArg(arg, expected, funcType, i)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, value)
		}
		callValue, callType, err = s.emitResolvedCall(calleeValue, funcType, false, args)
		if err != nil {
			return nil, nil, err
		}
	}
	if isVoidType(resultType) {
		presentEnd = C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
	} else {
		optionalType, ok := resultType.(*semantic.OptionalType)
		if !ok || optionalType == nil || optionalType.Value == nil {
			return nil, nil, fmt.Errorf("optional call requires an optional result type")
		}
		callValue, err = s.coerceValue(callValue, callType, optionalType.Value)
		if err != nil {
			return nil, nil, err
		}
		wrappedValue, err = s.buildOptionalSome(optionalType, callValue)
		if err != nil {
			return nil, nil, err
		}
		presentEnd = C.LLVMGetInsertBlock(s.builder)
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, noneBB)
	if !isVoidType(resultType) {
		optionalType := resultType.(*semantic.OptionalType)
		noneValue, err = s.buildOptionalNone(optionalType)
		if err != nil {
			return nil, nil, err
		}
	}
	noneEnd = C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if isVoidType(resultType) {
		return nil, resultType, nil
	}
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, resultLLVMType, cStringFree("safe.call.result"))
	values := []C.LLVMValueRef{wrappedValue, noneValue}
	blocks := []C.LLVMBasicBlockRef{presentEnd, noneEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, nil
}

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
		return s.emitExpr(expr.Args[0], frozenType)
	}
	if value, actualType, handled, err := s.emitDenseKeyHelperCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitNodeTableFillHelperCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinCloneCall(expr); handled {
		return value, actualType, err
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
	if value, actualType, handled, err := s.emitBuiltinDictEntryCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDictEntryInsertCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDictEntryGetOrInsertCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedMemcpyCall(expr); handled {
		return value, actualType, err
	}
	callee, funcType, err := s.resolveCallTarget(expr)
	if err != nil {
		return nil, nil, err
	}
	if funcType == nil {
		return nil, nil, fmt.Errorf("call target does not have a function type")
	}
	if len(funcType.ImplicitParamNames) != 0 && !expr.ResolvedImplicitArgsValid {
		if recovered, ok := s.recoverImplicitCallArgs(expr, funcType); ok {
			expr.ResolvedImplicitArgs = recovered
			expr.ResolvedImplicitArgsValid = true
		} else {
			return nil, nil, fmt.Errorf("call to %s is missing resolved implicit arguments", funcType.Name)
		}
	}
	loweredArgs := expr.LoweredArgs()
	args := make([]C.LLVMValueRef, 0, len(loweredArgs))
	for i, arg := range loweredArgs {
		var expected semantic.Type
		if i < len(funcType.Params) {
			expected = funcType.Params[i]
		}
		value, _, err := s.emitCallArg(arg, expected, funcType, i)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, value)
	}
	return s.emitResolvedCall(callee, funcType, s.directCallTarget(expr.Func), args)
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

func (s *functionState) emitBuiltinDArrayPushCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "push" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("darray push expects 1 argument, got %d", len(expr.Args))
	}
	owner, ok := s.lookupTreeAllocOwner()
	if !ok || owner.arenaRef == nil {
		return nil, nil, true, fmt.Errorf("darray push requires an active in <arena>: scope")
	}
	darrayPtr, resultType, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	itemValue, _, err := s.emitExpr(expr.Args[0], darrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	currentCount := C.LLVMBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("darray.push.count"))
	neededValue := C.LLVMBuildAdd(s.builder, currentCount, C.LLVMConstInt(usizeLLVMType, 1, 0), cStringFree("darray.push.needed"))
	if err := s.emitBuiltinDArrayEnsureCapacity(darrayPtr, darrayType, owner.arenaRef, neededValue, "darray.push"); err != nil {
		return nil, nil, true, err
	}
	itemsPtr, err := s.emitBuiltinDArrayItemsPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	voidPtrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	itemsValue := C.LLVMBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree("darray.push.items"))
	elemLLVMType, err := s.g.lowerType(darrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	slotPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, itemsValue, llvmValueSlicePtr([]C.LLVMValueRef{currentCount}), 1, cStringFree("darray.push.slot"))
	C.LLVMBuildStore(s.builder, itemValue, slotPtr)
	C.LLVMBuildStore(s.builder, neededValue, countPtr)
	return darrayPtr, resultType, true, nil
}

func (s *functionState) emitBuiltinDArrayExtendCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "extend" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("darray extend expects 1 argument, got %d", len(expr.Args))
	}
	owner, ok := s.lookupTreeAllocOwner()
	if !ok || owner.arenaRef == nil {
		return nil, nil, true, fmt.Errorf("darray extend requires an active in <arena>: scope")
	}
	darrayPtr, resultType, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	sourceData, sourceCount, err := s.emitBuiltinDArrayExtendSource(expr.Args[0], darrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroCount := C.LLVMConstInt(usizeLLVMType, 0, 0)
	isZero := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), sourceCount, zeroCount, cStringFree("darray.extend.count.zero"))
	callBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("darray.extend.call"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("darray.extend.merge"))
	C.LLVMBuildCondBr(s.builder, isZero, mergeBB, callBB)

	C.LLVMPositionBuilderAtEnd(s.builder, callBB)
	countPtr, _, err := s.emitBuiltinDArrayCountPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	currentCount := C.LLVMBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("darray.extend.count"))
	neededValue := C.LLVMBuildAdd(s.builder, currentCount, sourceCount, cStringFree("darray.extend.needed"))
	if err := s.emitBuiltinDArrayEnsureCapacity(darrayPtr, darrayType, owner.arenaRef, neededValue, "darray.extend"); err != nil {
		return nil, nil, true, err
	}
	itemsPtr, err := s.emitBuiltinDArrayItemsPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	voidPtrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	itemsValue := C.LLVMBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree("darray.extend.items"))
	elemLLVMType, err := s.g.lowerType(darrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	dstPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, itemsValue, llvmValueSlicePtr([]C.LLVMValueRef{currentCount}), 1, cStringFree("darray.extend.dst"))
	elemSizeBytes, err := s.sizeOfType(darrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	byteCount := C.LLVMBuildMul(s.builder, sourceCount, C.LLVMConstInt(usizeLLVMType, C.ulonglong(elemSizeBytes), 0), cStringFree("darray.extend.bytes"))
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	memcpyType := s.g.cachedRuntimeHelperType("arena_memcpy", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_memcpy", Params: []semantic.Type{voidRefType, voidRefType, s.g.result.NamedTypes["usize"]}, Return: voidRefType}
	})
	memcpyCallee, err := s.g.ensureFunctionDeclared("arena_memcpy", memcpyType)
	if err != nil {
		return nil, nil, true, err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return nil, nil, true, err
	}
	_ = s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{dstPtr, sourceData, byteCount}, "darray.extend.memcpy")
	C.LLVMBuildStore(s.builder, neededValue, countPtr)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	return darrayPtr, resultType, true, nil
}

func (s *functionState) emitBuiltinDArrayExtendSource(arg ast.Expr, elemType semantic.Type) (C.LLVMValueRef, C.LLVMValueRef, error) {
	sourceType := s.exprType(arg)
	baseType, ok := builtinDArrayExtendSourceType(sourceType)
	if !ok || baseType == nil {
		return nil, nil, fmt.Errorf("darray extend expects a compatible darray, dview, or array source")
	}
	switch tt := baseType.(type) {
	case *semantic.DArrayType:
		var arrayValue C.LLVMValueRef
		if refType, ok := sourceType.(*semantic.RefType); ok && refType != nil {
			ptr, _, err := s.emitExpr(arg, sourceType)
			if err != nil {
				return nil, nil, err
			}
			arrayValue, err = s.loadValue(ptr, tt, "darray.extend.src.darray")
			if err != nil {
				return nil, nil, err
			}
		} else {
			var err error
			arrayValue, _, err = s.emitExpr(arg, tt)
			if err != nil {
				return nil, nil, err
			}
		}
		data := C.LLVMBuildExtractValue(s.builder, arrayValue, 0, cStringFree("darray.extend.src.data"))
		count := C.LLVMBuildExtractValue(s.builder, arrayValue, 1, cStringFree("darray.extend.src.count"))
		return data, count, nil
	case *semantic.DArrayViewType:
		var viewValue C.LLVMValueRef
		if refType, ok := sourceType.(*semantic.RefType); ok && refType != nil {
			ptr, _, err := s.emitExpr(arg, sourceType)
			if err != nil {
				return nil, nil, err
			}
			viewValue, err = s.loadValue(ptr, tt, "darray.extend.src.view")
			if err != nil {
				return nil, nil, err
			}
		} else {
			var err error
			viewValue, _, err = s.emitExpr(arg, tt)
			if err != nil {
				return nil, nil, err
			}
		}
		data := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("darray.extend.src.data"))
		count := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("darray.extend.src.len"))
		return data, count, nil
	case *semantic.ArrayType:
		arrayType, arrayPtr, ok, err := s.fixedArraySliceBase(arg)
		if err != nil {
			return nil, nil, err
		}
		if !ok || arrayType == nil {
			return nil, nil, fmt.Errorf("darray extend could not materialize fixed array source")
		}
		usizeType := s.g.result.NamedTypes["usize"]
		usizeLLVMType, err := s.g.lowerType(usizeType)
		if err != nil {
			return nil, nil, err
		}
		count := C.LLVMConstInt(usizeLLVMType, C.ulonglong(arrayType.ConstSize), 0)
		elemRefType := &semantic.RefType{Elem: elemType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		elemRefLLVMType, err := s.g.lowerType(elemRefType)
		if err != nil {
			return nil, nil, err
		}
		if arrayType.ConstSize == 0 {
			return C.LLVMConstNull(elemRefLLVMType), count, nil
		}
		arrayLLVMType, err := s.g.lowerType(arrayType)
		if err != nil {
			return nil, nil, err
		}
		zeroIndex := C.LLVMConstInt(usizeLLVMType, 0, 0)
		indices := []C.LLVMValueRef{zeroIndex, zeroIndex}
		data := C.LLVMBuildGEP2(s.builder, arrayLLVMType, arrayPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("darray.extend.src.array.data"))
		return data, count, nil
	default:
		return nil, nil, fmt.Errorf("unsupported darray extend source %T", baseType)
	}
}

func (s *functionState) emitBuiltinDArrayReserveCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "reserve" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("darray reserve expects 1 argument, got %d", len(expr.Args))
	}
	owner, ok := s.lookupTreeAllocOwner()
	if !ok || owner.arenaRef == nil {
		return nil, nil, true, fmt.Errorf("darray reserve requires an active in <arena>: scope")
	}
	darrayPtr, resultType, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	neededValue, _, err := s.emitExpr(expr.Args[0], s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, nil, true, err
	}
	if err := s.emitBuiltinDArrayEnsureCapacity(darrayPtr, darrayType, owner.arenaRef, neededValue, "darray.reserve"); err != nil {
		return nil, nil, true, err
	}
	return darrayPtr, resultType, true, nil
}

func (s *functionState) emitBuiltinDArrayClearCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "clear" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 0 {
		return nil, nil, true, fmt.Errorf("darray clear expects 0 arguments, got %d", len(expr.Args))
	}
	darrayPtr, resultType, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	C.LLVMBuildStore(s.builder, C.LLVMConstInt(usizeLLVMType, 0, 0), countPtr)
	return darrayPtr, resultType, true, nil
}

func (s *functionState) emitBuiltinDArrayTruncateCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "truncate" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("darray truncate expects 1 argument, got %d", len(expr.Args))
	}
	darrayPtr, resultType, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	currentCount := C.LLVMBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("darray.truncate.count"))
	limitValue, _, err := s.emitExpr(expr.Args[0], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	shouldStore := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), limitValue, currentCount, cStringFree("darray.truncate.lt"))
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	storeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("darray.truncate.store"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("darray.truncate.merge"))
	C.LLVMBuildCondBr(s.builder, shouldStore, storeBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, storeBB)
	C.LLVMBuildStore(s.builder, limitValue, countPtr)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	_ = entryBlock
	return darrayPtr, resultType, true, nil
}

func (s *functionState) emitBuiltinDArrayReceiverPtr(receiver ast.Expr, receiverRefType *semantic.RefType) (C.LLVMValueRef, semantic.Type, error) {
	if receiverRefType != nil {
		ptr, _, err := s.emitExpr(receiver, receiverRefType)
		return ptr, receiverRefType, err
	}
	ptr, _, err := s.emitAddress(receiver)
	if err != nil {
		return nil, nil, err
	}
	resultType := &semantic.RefType{Elem: s.exprType(receiver), Mutable: true, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	return ptr, resultType, nil
}

func (s *functionState) emitBuiltinDArrayCountPtr(darrayPtr C.LLVMValueRef, darrayType *semantic.DArrayType) (C.LLVMValueRef, semantic.Type, error) {
	if darrayType == nil {
		return nil, nil, fmt.Errorf("missing darray type")
	}
	containerType, err := s.g.lowerType(darrayType)
	if err != nil {
		return nil, nil, err
	}
	countPtr := C.LLVMBuildStructGEP2(s.builder, containerType, darrayPtr, 1, cStringFree("darray.count.ptr"))
	return countPtr, s.g.result.NamedTypes["usize"], nil
}

func (s *functionState) emitBuiltinDArrayItemsPtr(darrayPtr C.LLVMValueRef, darrayType *semantic.DArrayType) (C.LLVMValueRef, error) {
	if darrayType == nil {
		return nil, fmt.Errorf("missing darray type")
	}
	containerType, err := s.g.lowerType(darrayType)
	if err != nil {
		return nil, err
	}
	return C.LLVMBuildStructGEP2(s.builder, containerType, darrayPtr, 0, cStringFree("darray.items.ptr")), nil
}

func (s *functionState) emitBuiltinDArrayCapacityPtr(darrayPtr C.LLVMValueRef, darrayType *semantic.DArrayType) (C.LLVMValueRef, semantic.Type, error) {
	if darrayType == nil {
		return nil, nil, fmt.Errorf("missing darray type")
	}
	containerType, err := s.g.lowerType(darrayType)
	if err != nil {
		return nil, nil, err
	}
	capacityPtr := C.LLVMBuildStructGEP2(s.builder, containerType, darrayPtr, 2, cStringFree("darray.capacity.ptr"))
	return capacityPtr, s.g.result.NamedTypes["usize"], nil
}

func (s *functionState) emitBuiltinStoreReceiverPtr(receiver ast.Expr, receiverRefType *semantic.RefType) (C.LLVMValueRef, semantic.Type, error) {
	if receiverRefType != nil {
		ptr, _, err := s.emitExpr(receiver, receiverRefType)
		return ptr, receiverRefType.Elem, err
	}
	ptr, _, err := s.emitAddress(receiver)
	if err != nil {
		return nil, nil, err
	}
	return ptr, s.exprType(receiver), nil
}

func (s *functionState) emitBuiltinStoreFieldDArrayPtr(storePtr C.LLVMValueRef, storeType semantic.Type, fieldName string) (C.LLVMValueRef, *semantic.DArrayType, error) {
	fieldType, index, _, _, err := s.g.fieldInfo(storeType, fieldName)
	if err != nil {
		return nil, nil, err
	}
	darrayType, ok := fieldType.(*semantic.DArrayType)
	if !ok || darrayType == nil {
		return nil, nil, fmt.Errorf("store field %q is not a darray column", fieldName)
	}
	containerType, err := s.g.lowerType(storeType)
	if err != nil {
		return nil, nil, err
	}
	fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerType, storePtr, C.unsigned(index), cStringFree("store."+fieldName+".ptr"))
	return fieldPtr, darrayType, nil
}

func (s *functionState) emitBuiltinDArrayEnsureCapacity(darrayPtr C.LLVMValueRef, darrayType *semantic.DArrayType, arenaRef C.LLVMValueRef, neededValue C.LLVMValueRef, name string) error {
	if darrayType == nil {
		return fmt.Errorf("missing darray type")
	}
	capacityPtr, usizeType, err := s.emitBuiltinDArrayCapacityPtr(darrayPtr, darrayType)
	if err != nil {
		return err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	currentCapacity := C.LLVMBuildLoad2(s.builder, usizeLLVMType, capacityPtr, cStringFree(name+".capacity"))
	hasCapacity := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntUGE), currentCapacity, neededValue, cStringFree(name+".capacity.ok"))
	growBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".grow"))
	contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".cont"))
	C.LLVMBuildCondBr(s.builder, hasCapacity, contBB, growBB)

	C.LLVMPositionBuilderAtEnd(s.builder, growBB)
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
	initCap := C.LLVMConstInt(usizeLLVMType, 256, 0)
	doubled := C.LLVMBuildMul(s.builder, currentCapacity, C.LLVMConstInt(usizeLLVMType, 2, 0), cStringFree(name+".capacity.double"))
	baseCapacity := C.LLVMBuildSelect(
		s.builder,
		C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), currentCapacity, zero, cStringFree(name+".capacity.zero")),
		initCap,
		doubled,
		cStringFree(name+".capacity.base"),
	)
	newCapacity := C.LLVMBuildSelect(
		s.builder,
		C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), baseCapacity, neededValue, cStringFree(name+".capacity.lt")),
		neededValue,
		baseCapacity,
		cStringFree(name+".capacity.new"),
	)
	elemSizeBytes, err := s.sizeOfType(darrayType.Elem)
	if err != nil {
		return err
	}
	elemSizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(elemSizeBytes), 0)
	oldSize := C.LLVMBuildMul(s.builder, currentCapacity, elemSizeValue, cStringFree(name+".old.bytes"))
	newSize := C.LLVMBuildMul(s.builder, newCapacity, elemSizeValue, cStringFree(name+".new.bytes"))
	itemsPtr, err := s.emitBuiltinDArrayItemsPtr(darrayPtr, darrayType)
	if err != nil {
		return err
	}
	voidPtrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	currentItems := C.LLVMBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree(name+".items"))
	isNull := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), currentItems, C.LLVMConstPointerNull(voidPtrType), cStringFree(name+".items.null"))

	arenaType := s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
	})
	reallocType := s.g.cachedRuntimeHelperType("arena_realloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_realloc", Params: []semantic.Type{arenaRefType, voidRefType, usizeType, usizeType}, Return: voidRefType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return err
	}
	reallocCallee, err := s.g.ensureFunctionDeclared("arena_realloc", reallocType)
	if err != nil {
		return err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return err
	}
	reallocLLVMType, err := s.g.lowerFunctionType(reallocType)
	if err != nil {
		return err
	}
	allocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".alloc"))
	reallocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".realloc"))
	storeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".store"))
	C.LLVMBuildCondBr(s.builder, isNull, allocBB, reallocBB)

	C.LLVMPositionBuilderAtEnd(s.builder, allocBB)
	allocated := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{arenaRef, newSize}, name+".alloc")
	allocEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, storeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, reallocBB)
	reallocated := s.buildCall(reallocLLVMType, reallocCallee, []C.LLVMValueRef{arenaRef, currentItems, oldSize, newSize}, name+".realloc")
	reallocEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, storeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, storeBB)
	newItems := C.LLVMBuildPhi(s.builder, voidPtrType, cStringFree(name+".items.new"))
	C.LLVMAddIncoming(newItems, llvmValueSlicePtr([]C.LLVMValueRef{allocated, reallocated}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{allocEnd, reallocEnd}), 2)
	C.LLVMBuildStore(s.builder, newItems, itemsPtr)
	C.LLVMBuildStore(s.builder, newCapacity, capacityPtr)
	C.LLVMBuildBr(s.builder, contBB)

	C.LLVMPositionBuilderAtEnd(s.builder, contBB)
	return nil
}

func builtinStoreResultRefType(storeType *semantic.StructType) *semantic.RefType {
	return &semantic.RefType{Elem: storeType, Mutable: true, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
}

func (s *functionState) emitBuiltinStorePushCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "push" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != len(storeType.StoreFieldOrder) {
		return nil, nil, true, fmt.Errorf("store push expects %d arguments, got %d", len(storeType.StoreFieldOrder), len(expr.Args))
	}
	owner, ok := s.lookupTreeAllocOwner()
	if !ok || owner.arenaRef == nil {
		return nil, nil, true, fmt.Errorf("store push requires an active in <arena>: scope")
	}
	storePtr, loweredStoreType, err := s.emitBuiltinStoreReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	for i, name := range storeType.StoreFieldOrder {
		fieldPtr, darrayType, err := s.emitBuiltinStoreFieldDArrayPtr(storePtr, loweredStoreType, name)
		if err != nil {
			return nil, nil, true, err
		}
		itemValue, _, err := s.emitExpr(expr.Args[i], darrayType.Elem)
		if err != nil {
			return nil, nil, true, err
		}
		countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(fieldPtr, darrayType)
		if err != nil {
			return nil, nil, true, err
		}
		usizeLLVMType, err := s.g.lowerType(usizeType)
		if err != nil {
			return nil, nil, true, err
		}
		currentCount := C.LLVMBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("store."+name+".push.count"))
		neededValue := C.LLVMBuildAdd(s.builder, currentCount, C.LLVMConstInt(usizeLLVMType, 1, 0), cStringFree("store."+name+".push.needed"))
		if err := s.emitBuiltinDArrayEnsureCapacity(fieldPtr, darrayType, owner.arenaRef, neededValue, "store."+name+".push"); err != nil {
			return nil, nil, true, err
		}
		itemsPtr, err := s.emitBuiltinDArrayItemsPtr(fieldPtr, darrayType)
		if err != nil {
			return nil, nil, true, err
		}
		voidPtrType := C.LLVMPointerTypeInContext(s.g.context, 0)
		itemsValue := C.LLVMBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree("store."+name+".push.items"))
		elemLLVMType, err := s.g.lowerType(darrayType.Elem)
		if err != nil {
			return nil, nil, true, err
		}
		slotPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, itemsValue, llvmValueSlicePtr([]C.LLVMValueRef{currentCount}), 1, cStringFree("store."+name+".push.slot"))
		C.LLVMBuildStore(s.builder, itemValue, slotPtr)
		C.LLVMBuildStore(s.builder, neededValue, countPtr)
	}
	return storePtr, builtinStoreResultRefType(storeType), true, nil
}

func (s *functionState) emitBuiltinStoreReserveCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "reserve" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("store reserve expects 1 argument, got %d", len(expr.Args))
	}
	owner, ok := s.lookupTreeAllocOwner()
	if !ok || owner.arenaRef == nil {
		return nil, nil, true, fmt.Errorf("store reserve requires an active in <arena>: scope")
	}
	storePtr, loweredStoreType, err := s.emitBuiltinStoreReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	neededValue, _, err := s.emitExpr(expr.Args[0], s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, nil, true, err
	}
	for _, name := range storeType.StoreFieldOrder {
		fieldPtr, darrayType, err := s.emitBuiltinStoreFieldDArrayPtr(storePtr, loweredStoreType, name)
		if err != nil {
			return nil, nil, true, err
		}
		if err := s.emitBuiltinDArrayEnsureCapacity(fieldPtr, darrayType, owner.arenaRef, neededValue, "store."+name+".reserve"); err != nil {
			return nil, nil, true, err
		}
	}
	return storePtr, builtinStoreResultRefType(storeType), true, nil
}

func (s *functionState) emitBuiltinStoreClearCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "clear" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 0 {
		return nil, nil, true, fmt.Errorf("store clear expects 0 arguments, got %d", len(expr.Args))
	}
	storePtr, loweredStoreType, err := s.emitBuiltinStoreReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	for _, name := range storeType.StoreFieldOrder {
		fieldPtr, darrayType, err := s.emitBuiltinStoreFieldDArrayPtr(storePtr, loweredStoreType, name)
		if err != nil {
			return nil, nil, true, err
		}
		countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(fieldPtr, darrayType)
		if err != nil {
			return nil, nil, true, err
		}
		usizeLLVMType, err := s.g.lowerType(usizeType)
		if err != nil {
			return nil, nil, true, err
		}
		C.LLVMBuildStore(s.builder, C.LLVMConstInt(usizeLLVMType, 0, 0), countPtr)
	}
	return storePtr, builtinStoreResultRefType(storeType), true, nil
}

func (s *functionState) emitBuiltinStoreTruncateCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "truncate" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("store truncate expects 1 argument, got %d", len(expr.Args))
	}
	storePtr, loweredStoreType, err := s.emitBuiltinStoreReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	limitValue, _, err := s.emitExpr(expr.Args[0], s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, nil, true, err
	}
	for _, name := range storeType.StoreFieldOrder {
		fieldPtr, darrayType, err := s.emitBuiltinStoreFieldDArrayPtr(storePtr, loweredStoreType, name)
		if err != nil {
			return nil, nil, true, err
		}
		countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(fieldPtr, darrayType)
		if err != nil {
			return nil, nil, true, err
		}
		usizeLLVMType, err := s.g.lowerType(usizeType)
		if err != nil {
			return nil, nil, true, err
		}
		currentCount := C.LLVMBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("store."+name+".truncate.count"))
		shouldStore := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), limitValue, currentCount, cStringFree("store."+name+".truncate.lt"))
		storeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("store."+name+".truncate.store"))
		mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("store."+name+".truncate.merge"))
		C.LLVMBuildCondBr(s.builder, shouldStore, storeBB, mergeBB)
		C.LLVMPositionBuilderAtEnd(s.builder, storeBB)
		C.LLVMBuildStore(s.builder, limitValue, countPtr)
		C.LLVMBuildBr(s.builder, mergeBB)
		C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	}
	return storePtr, builtinStoreResultRefType(storeType), true, nil
}

func (s *functionState) emitBuiltinStoreRowsCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "rows" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 0 {
		return nil, nil, true, fmt.Errorf("store rows expects 0 arguments, got %d", len(expr.Args))
	}
	storePtr, err := s.emitReadableStoreReceiverPtr(fieldExpr.Object, receiverType, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	resultType, ok := s.exprType(expr).(*semantic.StoreRowsViewType)
	if !ok || resultType == nil {
		resultType = &semantic.StoreRowsViewType{Store: storeType}
	}
	rowViewLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	value := C.LLVMGetUndef(rowViewLLVMType)
	value = C.LLVMBuildInsertValue(s.builder, value, storePtr, 0, cStringFree("store.rows.store"))
	return value, resultType, true, nil
}

func (s *functionState) emitReadableStoreReceiverPtr(receiver ast.Expr, receiverType semantic.Type, receiverRefType *semantic.RefType) (C.LLVMValueRef, error) {
	if receiverRefType != nil {
		ptr, _, err := s.emitExpr(receiver, receiverRefType)
		return ptr, err
	}
	if ptr, _, err := s.emitValueAddress(receiver); err == nil {
		return ptr, nil
	}
	value, _, err := s.emitExpr(receiver, receiverType)
	if err != nil {
		return nil, err
	}
	tempName := s.g.nextSyntheticName("store.rows.tmp.")
	tempAlloca, err := s.createEntryAlloca(tempName, receiverType)
	if err != nil {
		return nil, err
	}
	C.LLVMBuildStore(s.builder, value, tempAlloca)
	return tempAlloca, nil
}

func (s *functionState) emitBuiltinDictReceiverValue(receiver ast.Expr, receiverType semantic.Type) (C.LLVMValueRef, *semantic.DictType, error) {
	dictType, receiverRefType, ok := builtinDictReceiverType(receiverType)
	if !ok || dictType == nil {
		return nil, nil, fmt.Errorf("dict receiver is not a dict")
	}
	if receiverRefType != nil {
		value, _, err := s.emitExpr(receiver, receiverRefType)
		return value, dictType, err
	}
	ptr, _, err := s.emitAddress(receiver)
	return ptr, dictType, err
}

func (s *functionState) emitBuiltinDictEntryValue(entryExpr ast.Expr, entryType *semantic.DictEntryType, receiverRefType *semantic.RefType) (C.LLVMValueRef, error) {
	if receiverRefType != nil {
		entryPtr, _, err := s.emitExpr(entryExpr, receiverRefType)
		if err != nil {
			return nil, err
		}
		entryLLVMType, err := s.g.lowerType(entryType)
		if err != nil {
			return nil, err
		}
		return C.LLVMBuildLoad2(s.builder, entryLLVMType, entryPtr, cStringFree("dict.entry.load")), nil
	}
	entryValue, _, err := s.emitExpr(entryExpr, entryType)
	return entryValue, err
}

func (s *functionState) emitBuiltinDictEntryCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "entry" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	resultType, ok := s.exprType(expr).(*semantic.DictEntryType)
	if !ok || resultType == nil || resultType.Dict == nil {
		return nil, nil, true, fmt.Errorf("dict entry call missing semantic entry type")
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("dict entry expects 1 argument, got %d", len(expr.Args))
	}
	dictValue, dictType, err := s.emitBuiltinDictReceiverValue(fieldExpr.Object, s.exprType(fieldExpr.Object))
	if err != nil {
		return nil, nil, true, err
	}
	keyValue, _, err := s.emitExpr(expr.Args[0], dictType.Key)
	if err != nil {
		return nil, nil, true, err
	}
	getCallee, getType, err := s.ensureRuntimeFunction("arena_dict_get", map[string]semantic.Type{"K": dictType.Key, "T": dictType.Value})
	if err != nil {
		return nil, nil, true, err
	}
	getLLVMType, err := s.g.lowerFunctionType(getType)
	if err != nil {
		return nil, nil, true, err
	}
	valuePtr := s.buildCall(getLLVMType, getCallee, []C.LLVMValueRef{dictValue, keyValue}, "dict.entry.get")
	entryLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	entryValue := C.LLVMGetUndef(entryLLVMType)
	entryValue = C.LLVMBuildInsertValue(s.builder, entryValue, dictValue, 0, cStringFree("dict.entry.dict"))
	entryValue = C.LLVMBuildInsertValue(s.builder, entryValue, keyValue, 1, cStringFree("dict.entry.key"))
	entryValue = C.LLVMBuildInsertValue(s.builder, entryValue, valuePtr, 2, cStringFree("dict.entry.value"))
	return entryValue, resultType, true, nil
}

func (s *functionState) emitBuiltinDictEntryInsertCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "insert" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	entryType, receiverRefType, ok := builtinDictEntryReceiverType(s.exprType(fieldExpr.Object))
	if !ok || entryType == nil || entryType.Dict == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("dict entry insert expects 1 argument, got %d", len(expr.Args))
	}
	owner, ok := s.lookupTreeAllocOwner()
	if !ok || owner.arenaRef == nil {
		return nil, nil, true, fmt.Errorf("dict entry insert requires an active in <arena>: scope")
	}
	var entryPtr C.LLVMValueRef
	var err error
	if receiverRefType != nil {
		entryPtr, _, err = s.emitExpr(fieldExpr.Object, receiverRefType)
	} else {
		entryPtr, _, err = s.emitAddress(fieldExpr.Object)
		if err != nil {
			entryPtr = nil
			err = nil
		}
	}
	entryLLVMType, err := s.g.lowerType(entryType)
	if err != nil {
		return nil, nil, true, err
	}
	dictRefType := &semantic.RefType{Elem: entryType.Dict, Mutable: true, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	dictRefLLVMType, err := s.g.lowerType(dictRefType)
	if err != nil {
		return nil, nil, true, err
	}
	keyLLVMType, err := s.g.lowerType(entryType.Dict.Key)
	if err != nil {
		return nil, nil, true, err
	}
	valueRefType := builtinDictEntryValueRefType(entryType.Dict)
	valueRefLLVMType, err := s.g.lowerType(valueRefType)
	if err != nil {
		return nil, nil, true, err
	}
	var dictValue, keyValue, cachedValue C.LLVMValueRef
	var valuePtrPtr C.LLVMValueRef
	if entryPtr != nil {
		dictPtr := C.LLVMBuildStructGEP2(s.builder, entryLLVMType, entryPtr, 0, cStringFree("dict.entry.insert.dict.ptr"))
		keyPtr := C.LLVMBuildStructGEP2(s.builder, entryLLVMType, entryPtr, 1, cStringFree("dict.entry.insert.key.ptr"))
		valuePtrPtr = C.LLVMBuildStructGEP2(s.builder, entryLLVMType, entryPtr, 2, cStringFree("dict.entry.insert.value.ptr"))
		dictValue = C.LLVMBuildLoad2(s.builder, dictRefLLVMType, dictPtr, cStringFree("dict.entry.insert.dict"))
		keyValue = C.LLVMBuildLoad2(s.builder, keyLLVMType, keyPtr, cStringFree("dict.entry.insert.key"))
		cachedValue = C.LLVMBuildLoad2(s.builder, valueRefLLVMType, valuePtrPtr, cStringFree("dict.entry.insert.cached"))
	} else {
		entryValue, err := s.emitBuiltinDictEntryValue(fieldExpr.Object, entryType, receiverRefType)
		if err != nil {
			return nil, nil, true, err
		}
		dictValue = C.LLVMBuildExtractValue(s.builder, entryValue, 0, cStringFree("dict.entry.insert.dict"))
		keyValue = C.LLVMBuildExtractValue(s.builder, entryValue, 1, cStringFree("dict.entry.insert.key"))
		cachedValue = C.LLVMBuildExtractValue(s.builder, entryValue, 2, cStringFree("dict.entry.insert.cached"))
	}
	nullValue := C.LLVMConstNull(valueRefLLVMType)
	hasCached := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), cachedValue, nullValue, cStringFree("dict.entry.insert.has"))
	currentBB := C.LLVMGetInsertBlock(s.builder)
	nonNullBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dict.entry.insert.nonnull"))
	insertBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dict.entry.insert.insert"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dict.entry.insert.merge"))
	C.LLVMBuildCondBr(s.builder, hasCached, nonNullBB, insertBB)
	C.LLVMPositionBuilderAtEnd(s.builder, nonNullBB)
	C.LLVMBuildBr(s.builder, mergeBB)
	nonNullEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMPositionBuilderAtEnd(s.builder, insertBB)
	insertedArg, _, err := s.emitExpr(expr.Args[0], entryType.Dict.Value)
	if err != nil {
		return nil, nil, true, err
	}
	putCallee, putType, err := s.ensureRuntimeFunction("arena_dict_put", map[string]semantic.Type{"K": entryType.Dict.Key, "T": entryType.Dict.Value})
	if err != nil {
		return nil, nil, true, err
	}
	putLLVMType, err := s.g.lowerFunctionType(putType)
	if err != nil {
		return nil, nil, true, err
	}
	insertedValue := s.buildCall(putLLVMType, putCallee, []C.LLVMValueRef{owner.arenaRef, dictValue, keyValue, insertedArg}, "dict.entry.insert.result")
	if valuePtrPtr != nil {
		C.LLVMBuildStore(s.builder, insertedValue, valuePtrPtr)
	}
	C.LLVMBuildBr(s.builder, mergeBB)
	insertEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phi := C.LLVMBuildPhi(s.builder, valueRefLLVMType, cStringFree("dict.entry.insert.phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr([]C.LLVMValueRef{cachedValue, insertedValue}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{nonNullEnd, insertEnd}), 2)
	_ = currentBB
	return phi, valueRefType, true, nil
}

func (s *functionState) emitBuiltinDictEntryGetOrInsertCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "get_or_insert" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	entryType, receiverRefType, ok := builtinDictEntryReceiverType(s.exprType(fieldExpr.Object))
	if !ok || entryType == nil || entryType.Dict == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("dict entry get_or_insert expects 1 argument, got %d", len(expr.Args))
	}
	owner, ok := s.lookupTreeAllocOwner()
	if !ok || owner.arenaRef == nil {
		return nil, nil, true, fmt.Errorf("dict entry get_or_insert requires an active in <arena>: scope")
	}
	var entryPtr C.LLVMValueRef
	var err error
	if receiverRefType != nil {
		entryPtr, _, err = s.emitExpr(fieldExpr.Object, receiverRefType)
	} else {
		entryPtr, _, err = s.emitAddress(fieldExpr.Object)
		if err != nil {
			entryPtr = nil
			err = nil
		}
	}
	entryLLVMType, err := s.g.lowerType(entryType)
	if err != nil {
		return nil, nil, true, err
	}
	dictRefType := &semantic.RefType{Elem: entryType.Dict, Mutable: true, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	dictRefLLVMType, err := s.g.lowerType(dictRefType)
	if err != nil {
		return nil, nil, true, err
	}
	keyLLVMType, err := s.g.lowerType(entryType.Dict.Key)
	if err != nil {
		return nil, nil, true, err
	}
	valueRefType := builtinDictEntryValueRefType(entryType.Dict)
	valueRefLLVMType, err := s.g.lowerType(valueRefType)
	if err != nil {
		return nil, nil, true, err
	}
	var dictValue, keyValue, cachedValue C.LLVMValueRef
	var valuePtrPtr C.LLVMValueRef
	if entryPtr != nil {
		dictPtr := C.LLVMBuildStructGEP2(s.builder, entryLLVMType, entryPtr, 0, cStringFree("dict.entry.get_or_insert.dict.ptr"))
		keyPtr := C.LLVMBuildStructGEP2(s.builder, entryLLVMType, entryPtr, 1, cStringFree("dict.entry.get_or_insert.key.ptr"))
		valuePtrPtr = C.LLVMBuildStructGEP2(s.builder, entryLLVMType, entryPtr, 2, cStringFree("dict.entry.get_or_insert.value.ptr"))
		dictValue = C.LLVMBuildLoad2(s.builder, dictRefLLVMType, dictPtr, cStringFree("dict.entry.get_or_insert.dict"))
		keyValue = C.LLVMBuildLoad2(s.builder, keyLLVMType, keyPtr, cStringFree("dict.entry.get_or_insert.key"))
		cachedValue = C.LLVMBuildLoad2(s.builder, valueRefLLVMType, valuePtrPtr, cStringFree("dict.entry.get_or_insert.cached"))
	} else {
		entryValue, err := s.emitBuiltinDictEntryValue(fieldExpr.Object, entryType, receiverRefType)
		if err != nil {
			return nil, nil, true, err
		}
		dictValue = C.LLVMBuildExtractValue(s.builder, entryValue, 0, cStringFree("dict.entry.get_or_insert.dict"))
		keyValue = C.LLVMBuildExtractValue(s.builder, entryValue, 1, cStringFree("dict.entry.get_or_insert.key"))
		cachedValue = C.LLVMBuildExtractValue(s.builder, entryValue, 2, cStringFree("dict.entry.get_or_insert.cached"))
	}
	nullValue := C.LLVMConstNull(valueRefLLVMType)
	hasCached := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), cachedValue, nullValue, cStringFree("dict.entry.get_or_insert.has"))
	nonNullBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dict.entry.get_or_insert.nonnull"))
	insertBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dict.entry.get_or_insert.insert"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dict.entry.get_or_insert.merge"))
	C.LLVMBuildCondBr(s.builder, hasCached, nonNullBB, insertBB)
	C.LLVMPositionBuilderAtEnd(s.builder, nonNullBB)
	C.LLVMBuildBr(s.builder, mergeBB)
	nonNullEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMPositionBuilderAtEnd(s.builder, insertBB)
	insertedArg, _, err := s.emitExpr(expr.Args[0], entryType.Dict.Value)
	if err != nil {
		return nil, nil, true, err
	}
	getOrInsertCallee, getOrInsertType, err := s.ensureRuntimeFunction("arena_dict_get_or_insert", map[string]semantic.Type{"K": entryType.Dict.Key, "T": entryType.Dict.Value})
	if err != nil {
		return nil, nil, true, err
	}
	getOrInsertLLVMType, err := s.g.lowerFunctionType(getOrInsertType)
	if err != nil {
		return nil, nil, true, err
	}
	insertedValue := s.buildCall(getOrInsertLLVMType, getOrInsertCallee, []C.LLVMValueRef{owner.arenaRef, dictValue, keyValue, insertedArg}, "dict.entry.get_or_insert.result")
	if valuePtrPtr != nil {
		C.LLVMBuildStore(s.builder, insertedValue, valuePtrPtr)
	}
	C.LLVMBuildBr(s.builder, mergeBB)
	insertEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phi := C.LLVMBuildPhi(s.builder, valueRefLLVMType, cStringFree("dict.entry.get_or_insert.phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr([]C.LLVMValueRef{cachedValue, insertedValue}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{nonNullEnd, insertEnd}), 2)
	return phi, valueRefType, true, nil
}

func (s *functionState) emitBuiltinDictEntryFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil || expr.Object == nil {
		return nil, nil, false, nil
	}
	entryType, receiverRefType, ok := builtinDictEntryReceiverType(s.exprType(expr.Object))
	if !ok || entryType == nil || entryType.Dict == nil {
		return nil, nil, false, nil
	}
	entryValue, err := s.emitBuiltinDictEntryValue(expr.Object, entryType, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	valueRefType := builtinDictEntryValueRefType(entryType.Dict)
	valueRefLLVMType, err := s.g.lowerType(valueRefType)
	if err != nil {
		return nil, nil, true, err
	}
	switch expr.Field {
	case "value":
		value := C.LLVMBuildExtractValue(s.builder, entryValue, 2, cStringFree("dict.entry.value"))
		return value, valueRefType, true, nil
	case "found":
		value := C.LLVMBuildExtractValue(s.builder, entryValue, 2, cStringFree("dict.entry.found.value"))
		nullValue := C.LLVMConstNull(valueRefLLVMType)
		found := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), value, nullValue, cStringFree("dict.entry.found"))
		return found, s.g.result.NamedTypes["bool"], true, nil
	default:
		return nil, nil, false, nil
	}
}

func (s *functionState) emitCallArg(arg ast.Expr, expected semantic.Type, fnType *semantic.FuncType, index int) (C.LLVMValueRef, semantic.Type, error) {
	if s != nil && fnType != nil && fnType.SinkParamsKnown && index >= 0 && index < len(fnType.SinkParams) && fnType.SinkParams[index] {
		if operand, moved := backendExplicitMoveOperand(arg); moved {
			return s.emitMovedValue(operand, expected)
		}
		return s.emitMovedValue(arg, expected)
	}
	return s.emitExpr(arg, expected)
}

func backendOrderedWithItems(bundles []ast.WithBundleUse, args []ast.WithArg, order []ast.WithItem) []ast.WithItem {
	if len(order) != 0 {
		return append([]ast.WithItem(nil), order...)
	}
	items := make([]ast.WithItem, 0, len(bundles)+len(args))
	for _, bundle := range bundles {
		items = append(items, ast.WithItem{Position: bundle.Position, Bundle: bundle, IsBundle: true})
	}
	for _, arg := range args {
		items = append(items, ast.WithItem{Position: arg.Position, Arg: arg})
	}
	return items
}

func (s *functionState) lookupBackendContextBundle(name string) (*semantic.ContextBundle, bool) {
	if s == nil || s.g == nil || s.g.result == nil {
		return nil, false
	}
	if bundle, ok := s.g.result.ContextBundles[name]; ok && bundle != nil {
		return bundle, true
	}
	var matched *semantic.ContextBundle
	for qualifiedName, bundle := range s.g.result.ContextBundles {
		if bundle == nil {
			continue
		}
		if qualifiedName == name || strings.HasSuffix(qualifiedName, "."+name) {
			if matched != nil && matched != bundle {
				return nil, false
			}
			matched = bundle
		}
	}
	return matched, matched != nil
}

func (s *functionState) lookupBackendImplicitExpr(name string, working map[string]ast.Expr) (ast.Expr, bool) {
	if working != nil {
		if expr, ok := working[name]; ok && expr != nil {
			return expr, true
		}
	}
	if _, ok := s.lookupBinding(name); ok {
		return &ast.Ident{Name: name}, true
	}
	if s.g != nil && s.g.result != nil && s.g.result.GlobalScope != nil {
		if _, ok := s.g.result.GlobalScope.Lookup(name); ok {
			return &ast.Ident{Name: name}, true
		}
	}
	return nil, false
}

func (s *functionState) recoverImplicitCallArgs(expr *ast.CallExpr, funcType *semantic.FuncType) ([]ast.Expr, bool) {
	if s == nil || expr == nil || funcType == nil || len(funcType.ImplicitParamNames) == 0 {
		return nil, false
	}
	working := map[string]ast.Expr{}
	for _, item := range backendOrderedWithItems(expr.WithBundles, expr.WithArgs, expr.WithItemOrder) {
		if item.IsBundle {
			bundle, ok := s.lookupBackendContextBundle(item.Bundle.Name)
			if !ok || bundle == nil {
				return nil, false
			}
			explicitValues := make(map[string]ast.Expr, len(item.Bundle.Args))
			for _, arg := range item.Bundle.Args {
				explicitValues[arg.Name] = arg.Value
			}
			for _, field := range bundle.Fields {
				if value, ok := explicitValues[field.Name]; ok {
					working[field.Name] = value
					continue
				}
				value, ok := s.lookupBackendImplicitExpr(field.Name, working)
				if !ok {
					return nil, false
				}
				working[field.Name] = value
			}
			continue
		}
		working[item.Arg.Name] = item.Arg.Value
	}
	resolved := make([]ast.Expr, 0, len(funcType.ImplicitParamNames))
	for _, name := range funcType.ImplicitParamNames {
		value, ok := s.lookupBackendImplicitExpr(name, working)
		if !ok {
			return nil, false
		}
		resolved = append(resolved, value)
	}
	return resolved, true
}

func backendExplicitMoveOperand(expr ast.Expr) (ast.Expr, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return backendExplicitMoveOperand(n.Inner)
	case *ast.MoveExpr:
		return n.Operand, true
	default:
		return nil, false
	}
}

func (s *functionState) emitProofCarryingViewHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	switch callIdentName(expr) {
	case "any":
		return s.emitIterableBoolAggregateHelperCall(expr, "any", true)
	case "all":
		return s.emitIterableBoolAggregateHelperCall(expr, "all", false)
	case "enumerate":
		return s.emitEnumerateHelperCall(expr)
	case "readonly":
		return s.emitReadonlyHelperCall(expr)
	case "split_at":
		return s.emitSplitAtHelperCall(expr)
	case "chunks_exact":
		return s.emitChunksExactHelperCall(expr)
	case "reduce_sum":
		return s.emitReduceSumHelperCall(expr)
	case "zip_map":
		return s.emitZipMapHelperCall(expr)
	default:
		return nil, nil, false, nil
	}
}

func (s *functionState) emitIterableBoolAggregateHelperCall(expr *ast.CallExpr, helperName string, stopOnTrue bool) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("%s expects 1 argument, got %d", helperName, len(expr.Args))
	}
	sourceType := s.exprType(expr.Args[0])
	if sourceType == nil {
		return nil, nil, true, fmt.Errorf("%s source is missing a semantic type", helperName)
	}
	resultType := s.exprType(expr)
	if resultType == nil {
		resultType = s.g.result.NamedTypes["bool"]
	}
	sourceValue, _, err := s.emitExpr(expr.Args[0], sourceType)
	if err != nil {
		return nil, nil, true, err
	}
	sourceAlloca, err := s.emitStackTempValue(sourceValue, sourceType, helperName+".source")
	if err != nil {
		return nil, nil, true, err
	}
	countValue, err := s.emitIterLoopCount(expr.Args[0], sourceAlloca, sourceType, helperName)
	if err != nil {
		return nil, nil, true, err
	}
	boolType := s.g.result.NamedTypes["bool"]
	boolLLVMType, err := s.g.lowerType(boolType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroIndex := C.LLVMConstInt(usizeLLVMType, 0, 0)
	one := C.LLVMConstInt(usizeLLVMType, 1, 0)
	defaultBool := C.LLVMConstInt(boolLLVMType, 0, 0)
	terminalBool := C.LLVMConstInt(boolLLVMType, 1, 0)
	if !stopOnTrue {
		defaultBool = C.LLVMConstInt(boolLLVMType, 1, 0)
		terminalBool = C.LLVMConstInt(boolLLVMType, 0, 0)
	}

	entryBlock := C.LLVMGetInsertBlock(s.builder)
	loopCondBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(helperName+".cond"))
	loopBodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(helperName+".body"))
	loopContinueBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(helperName+".continue"))
	loopShortCircuitBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(helperName+".short_circuit"))
	loopEndBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(helperName+".end"))
	C.LLVMBuildBr(s.builder, loopCondBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopCondBB)
	indexValue := C.LLVMBuildPhi(s.builder, usizeLLVMType, cStringFree(helperName+".index"))
	C.LLVMAddIncoming(indexValue, llvmValueSlicePtr([]C.LLVMValueRef{zeroIndex}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{entryBlock}), 1)
	hasMore := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), indexValue, countValue, cStringFree(helperName+".has_more"))
	C.LLVMBuildCondBr(s.builder, hasMore, loopBodyBB, loopEndBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopBodyBB)
	itemValue, itemType, err := s.emitIterLoopElementValue(expr.Args[0], sourceAlloca, sourceType, indexValue, helperName)
	if err != nil {
		return nil, nil, true, err
	}
	itemBool, err := s.coerceValue(itemValue, itemType, boolType)
	if err != nil {
		return nil, nil, true, err
	}
	if stopOnTrue {
		C.LLVMBuildCondBr(s.builder, itemBool, loopShortCircuitBB, loopContinueBB)
	} else {
		C.LLVMBuildCondBr(s.builder, itemBool, loopContinueBB, loopShortCircuitBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, loopContinueBB)
	nextIndex := C.LLVMBuildAdd(s.builder, indexValue, one, cStringFree(helperName+".index.next"))
	continueEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, loopCondBB)
	C.LLVMAddIncoming(indexValue, llvmValueSlicePtr([]C.LLVMValueRef{nextIndex}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{continueEnd}), 1)

	C.LLVMPositionBuilderAtEnd(s.builder, loopShortCircuitBB)
	shortCircuitEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, loopEndBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopEndBB)
	resultValue := C.LLVMBuildPhi(s.builder, boolLLVMType, cStringFree(helperName+".result"))
	C.LLVMAddIncoming(resultValue, llvmValueSlicePtr([]C.LLVMValueRef{defaultBool, terminalBool}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{loopCondBB, shortCircuitEnd}), 2)
	return resultValue, boolType, true, nil
}

func (s *functionState) emitEnumerateHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("enumerate expects 1 argument, got %d", len(expr.Args))
	}
	sourceType := s.exprType(expr.Args[0])
	if sourceType == nil {
		return nil, nil, true, fmt.Errorf("enumerate source is missing a semantic type")
	}
	resultType := s.exprType(expr)
	if resultType == nil {
		return nil, nil, true, fmt.Errorf("enumerate result is missing a semantic type")
	}
	sourceValue, _, err := s.emitExpr(expr.Args[0], sourceType)
	if err != nil {
		return nil, nil, true, err
	}
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	resultValue := C.LLVMGetUndef(resultLLVMType)
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, sourceValue, 0, cStringFree("enumerate.source.insert"))
	return resultValue, resultType, true, nil
}

func (s *functionState) emitTreeTraversalHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	switch callIdentName(expr) {
	case "children":
		return s.emitChildrenHelperCall(expr)
	default:
		return nil, nil, false, nil
	}
}

func (s *functionState) emitChildrenHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("children expects 1 argument, got %d", len(expr.Args))
	}
	sourceType := s.exprType(expr.Args[0])
	if sourceType == nil {
		return nil, nil, true, fmt.Errorf("children source is missing a semantic type")
	}
	resultType := s.exprType(expr)
	if resultType == nil {
		return nil, nil, true, fmt.Errorf("children result is missing a semantic type")
	}
	sourceValue, _, err := s.emitExpr(expr.Args[0], sourceType)
	if err != nil {
		return nil, nil, true, err
	}
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	resultValue := C.LLVMGetUndef(resultLLVMType)
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, sourceValue, 0, cStringFree("tree.children.node.insert"))
	return resultValue, resultType, true, nil
}

func (s *functionState) emitReadonlyHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("readonly expects 1 argument, got %d", len(expr.Args))
	}
	value, actualType, err := s.emitExpr(expr.Args[0], s.exprType(expr.Args[0]))
	if err != nil {
		return nil, nil, true, err
	}
	return value, actualType, true, nil
}

func (s *functionState) emitSplitAtHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 2 {
		return nil, nil, true, fmt.Errorf("split_at expects 2 arguments, got %d", len(expr.Args))
	}
	viewType, ok := s.exprType(expr.Args[0]).(*semantic.DArrayViewType)
	if !ok || viewType == nil {
		return nil, nil, true, fmt.Errorf("split_at expects a dview source")
	}
	resultType := s.exprType(expr)
	viewValue, _, err := s.emitExpr(expr.Args[0], viewType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	indexValue, _, err := s.emitExpr(expr.Args[1], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	viewLen := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("split.view.len"))
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
	leftValue, err := s.emitArenaViewSliceValue(viewValue, viewType, zero, indexValue, "split.left")
	if err != nil {
		return nil, nil, true, err
	}
	rightValue, err := s.emitArenaViewSliceValue(viewValue, viewType, indexValue, viewLen, "split.right")
	if err != nil {
		return nil, nil, true, err
	}
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	resultValue := C.LLVMGetUndef(resultLLVMType)
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, leftValue, 0, cStringFree("split.left.insert"))
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, rightValue, 1, cStringFree("split.right.insert"))
	return resultValue, resultType, true, nil
}

func (s *functionState) emitChunksExactHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 2 {
		return nil, nil, true, fmt.Errorf("chunks_exact expects 2 arguments, got %d", len(expr.Args))
	}
	viewType, ok := s.exprType(expr.Args[0]).(*semantic.DArrayViewType)
	if !ok || viewType == nil {
		return nil, nil, true, fmt.Errorf("chunks_exact expects a dview source")
	}
	resultType := s.exprType(expr)
	viewValue, _, err := s.emitExpr(expr.Args[0], viewType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	chunkSizeValue, _, err := s.emitExpr(expr.Args[1], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	viewLen := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("chunks.view.len"))
	if err := s.emitChunksExactValidation(chunkSizeValue, viewLen, "chunks_exact"); err != nil {
		return nil, nil, true, err
	}
	chunksLen := C.LLVMBuildUDiv(s.builder, viewLen, chunkSizeValue, cStringFree("chunks.len"))
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	resultValue := C.LLVMGetUndef(resultLLVMType)
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, viewValue, 0, cStringFree("chunks.source.insert"))
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, chunkSizeValue, 1, cStringFree("chunks.chunk_size.insert"))
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, chunksLen, 2, cStringFree("chunks.len.insert"))
	return resultValue, resultType, true, nil
}

func (s *functionState) emitReduceSumHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) < 2 {
		return nil, nil, true, fmt.Errorf("reduce_sum expects at least 2 arguments, got %d", len(expr.Args))
	}
	srcType, srcElemType, ok := zipMapViewInfo(s.exprType(expr.Args[0]))
	if !ok {
		return nil, nil, true, fmt.Errorf("reduce_sum source expects a dense view")
	}
	callbackType, ok := s.exprType(expr.Args[1]).(*semantic.FuncType)
	if !ok || callbackType == nil {
		return nil, nil, true, fmt.Errorf("reduce_sum callback expects a function value")
	}
	resultType := s.exprType(expr)
	srcValue, _, err := s.emitExpr(expr.Args[0], srcType)
	if err != nil {
		return nil, nil, true, err
	}
	callbackValue, _, err := s.emitExpr(expr.Args[1], callbackType)
	if err != nil {
		return nil, nil, true, err
	}
	extraArgs := make([]C.LLVMValueRef, 0, len(expr.Args)-2)
	for i, arg := range expr.Args[2:] {
		var expected semantic.Type
		if i+1 < len(callbackType.Params) {
			expected = callbackType.Params[i+1]
		}
		value, _, err := s.emitExpr(arg, expected)
		if err != nil {
			return nil, nil, true, err
		}
		extraArgs = append(extraArgs, value)
	}
	srcDataPtr, err := s.emitDenseViewDataPointer(srcValue, srcElemType, "reduce_sum.src")
	if err != nil {
		return nil, nil, true, err
	}
	totalValue := C.LLVMBuildExtractValue(s.builder, srcValue, 1, cStringFree("reduce_sum.total"))
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroIndex := C.LLVMConstInt(usizeLLVMType, 0, 0)
	one := C.LLVMConstInt(usizeLLVMType, 1, 0)
	accZero, err := s.zeroValue(resultType)
	if err != nil {
		return nil, nil, true, err
	}

	entryBlock := C.LLVMGetInsertBlock(s.builder)
	loopCondBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("reduce_sum.cond"))
	loopBodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("reduce_sum.body"))
	loopEndBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("reduce_sum.end"))
	C.LLVMBuildBr(s.builder, loopCondBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopCondBB)
	indexValue := C.LLVMBuildPhi(s.builder, usizeLLVMType, cStringFree("reduce_sum.index"))
	accValue := C.LLVMBuildPhi(s.builder, resultLLVMType, cStringFree("reduce_sum.acc"))
	initValues := []C.LLVMValueRef{zeroIndex, accZero}
	initBlocks := []C.LLVMBasicBlockRef{entryBlock, entryBlock}
	C.LLVMAddIncoming(indexValue, llvmValueSlicePtr(initValues[:1]), llvmBlockSlicePtr(initBlocks[:1]), 1)
	C.LLVMAddIncoming(accValue, llvmValueSlicePtr(initValues[1:]), llvmBlockSlicePtr(initBlocks[1:]), 1)
	hasMore := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), indexValue, totalValue, cStringFree("reduce_sum.has_more"))
	C.LLVMBuildCondBr(s.builder, hasMore, loopBodyBB, loopEndBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopBodyBB)
	srcElemPtr, err := s.emitDenseViewIndexedAddress(srcDataPtr, srcElemType, indexValue, "reduce_sum.src")
	if err != nil {
		return nil, nil, true, err
	}
	srcElem, err := s.loadValue(srcElemPtr, srcElemType, "reduce_sum.src.elem")
	if err != nil {
		return nil, nil, true, err
	}
	callArgs := make([]C.LLVMValueRef, 0, len(extraArgs)+1)
	callArgs = append(callArgs, srcElem)
	callArgs = append(callArgs, extraArgs...)
	mappedValue, err := s.emitFunctionValueCall(callbackValue, callbackType, callArgs, "reduce_sum.call")
	if err != nil {
		return nil, nil, true, err
	}
	coercedValue, err := s.coerceValue(mappedValue, callbackType.Return, resultType)
	if err != nil {
		return nil, nil, true, err
	}
	nextAcc, err := s.emitAugmentedValue(lexer.TOKEN_PLUSEQ, accValue, coercedValue, resultType)
	if err != nil {
		return nil, nil, true, err
	}
	nextIndex := C.LLVMBuildAdd(s.builder, indexValue, one, cStringFree("reduce_sum.index.next"))
	bodyEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, loopCondBB)
	nextIndexValues := []C.LLVMValueRef{nextIndex}
	nextIndexBlocks := []C.LLVMBasicBlockRef{bodyEnd}
	C.LLVMAddIncoming(indexValue, llvmValueSlicePtr(nextIndexValues), llvmBlockSlicePtr(nextIndexBlocks), 1)
	nextAccValues := []C.LLVMValueRef{nextAcc}
	nextAccBlocks := []C.LLVMBasicBlockRef{bodyEnd}
	C.LLVMAddIncoming(accValue, llvmValueSlicePtr(nextAccValues), llvmBlockSlicePtr(nextAccBlocks), 1)

	C.LLVMPositionBuilderAtEnd(s.builder, loopEndBB)
	return accValue, resultType, true, nil
}

func (s *functionState) emitZipMapHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 4 {
		return nil, nil, true, fmt.Errorf("zip_map expects 4 arguments, got %d", len(expr.Args))
	}
	dstType, dstElemType, ok := zipMapViewInfo(s.exprType(expr.Args[0]))
	if !ok {
		return nil, nil, true, fmt.Errorf("zip_map destination expects a dense view")
	}
	src1Type, src1ElemType, ok := zipMapViewInfo(s.exprType(expr.Args[1]))
	if !ok {
		return nil, nil, true, fmt.Errorf("zip_map source 1 expects a dense view")
	}
	src2Type, src2ElemType, ok := zipMapViewInfo(s.exprType(expr.Args[2]))
	if !ok {
		return nil, nil, true, fmt.Errorf("zip_map source 2 expects a dense view")
	}
	callbackType, ok := s.exprType(expr.Args[3]).(*semantic.FuncType)
	if !ok || callbackType == nil {
		return nil, nil, true, fmt.Errorf("zip_map callback expects a function value")
	}
	dstValue, _, err := s.emitExpr(expr.Args[0], dstType)
	if err != nil {
		return nil, nil, true, err
	}
	src1Value, _, err := s.emitExpr(expr.Args[1], src1Type)
	if err != nil {
		return nil, nil, true, err
	}
	src2Value, _, err := s.emitExpr(expr.Args[2], src2Type)
	if err != nil {
		return nil, nil, true, err
	}
	callbackValue, _, err := s.emitExpr(expr.Args[3], callbackType)
	if err != nil {
		return nil, nil, true, err
	}
	dstDataPtr, err := s.emitDenseViewDataPointer(dstValue, dstElemType, "zip_map.dst")
	if err != nil {
		return nil, nil, true, err
	}
	src1DataPtr, err := s.emitDenseViewDataPointer(src1Value, src1ElemType, "zip_map.src1")
	if err != nil {
		return nil, nil, true, err
	}
	src2DataPtr, err := s.emitDenseViewDataPointer(src2Value, src2ElemType, "zip_map.src2")
	if err != nil {
		return nil, nil, true, err
	}
	dstSrc1Disjoint := s.g.result.ExprsAreDisjoint(expr.Args[0], expr.Args[1])
	dstSrc2Disjoint := s.g.result.ExprsAreDisjoint(expr.Args[0], expr.Args[2])
	src1Src2Disjoint := s.g.result.ExprsAreDisjoint(expr.Args[1], expr.Args[2])
	domainName := ""
	dstScopeName := ""
	src1ScopeName := ""
	src2ScopeName := ""
	hasScopedNoAlias := dstSrc1Disjoint || dstSrc2Disjoint || src1Src2Disjoint
	if hasScopedNoAlias {
		domainName = fmt.Sprintf("llctx.zip_map.%p.domain", expr)
		dstScopeName = domainName + ".dst"
		src1ScopeName = domainName + ".src1"
		src2ScopeName = domainName + ".src2"
	}
	totalValue := C.LLVMBuildExtractValue(s.builder, dstValue, 1, cStringFree("zip_map.total"))
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
	one := C.LLVMConstInt(usizeLLVMType, 1, 0)

	entryBlock := C.LLVMGetInsertBlock(s.builder)
	loopCondBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("zip_map.cond"))
	loopBodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("zip_map.body"))
	loopEndBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("zip_map.end"))
	C.LLVMBuildBr(s.builder, loopCondBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopCondBB)
	indexValue := C.LLVMBuildPhi(s.builder, usizeLLVMType, cStringFree("zip_map.index"))
	initIndexValues := []C.LLVMValueRef{zero}
	initIndexBlocks := []C.LLVMBasicBlockRef{entryBlock}
	C.LLVMAddIncoming(indexValue, llvmValueSlicePtr(initIndexValues), llvmBlockSlicePtr(initIndexBlocks), 1)
	hasMore := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), indexValue, totalValue, cStringFree("zip_map.has_more"))
	C.LLVMBuildCondBr(s.builder, hasMore, loopBodyBB, loopEndBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopBodyBB)
	dstElemPtr, err := s.emitDenseViewIndexedAddress(dstDataPtr, dstElemType, indexValue, "zip_map.dst")
	if err != nil {
		return nil, nil, true, err
	}
	src1ElemPtr, err := s.emitDenseViewIndexedAddress(src1DataPtr, src1ElemType, indexValue, "zip_map.src1")
	if err != nil {
		return nil, nil, true, err
	}
	src2ElemPtr, err := s.emitDenseViewIndexedAddress(src2DataPtr, src2ElemType, indexValue, "zip_map.src2")
	if err != nil {
		return nil, nil, true, err
	}
	src1Elem, err := s.loadValue(src1ElemPtr, src1ElemType, "zip_map.src1.elem")
	if err != nil {
		return nil, nil, true, err
	}
	if hasScopedNoAlias {
		var noAliasScopes []string
		if dstSrc1Disjoint {
			noAliasScopes = append(noAliasScopes, dstScopeName)
		}
		if src1Src2Disjoint {
			noAliasScopes = append(noAliasScopes, src2ScopeName)
		}
		s.attachAliasScopeMetadataWithNames(src1Elem, domainName, src1ScopeName, noAliasScopes)
	}
	src2Elem, err := s.loadValue(src2ElemPtr, src2ElemType, "zip_map.src2.elem")
	if err != nil {
		return nil, nil, true, err
	}
	if hasScopedNoAlias {
		var noAliasScopes []string
		if dstSrc2Disjoint {
			noAliasScopes = append(noAliasScopes, dstScopeName)
		}
		if src1Src2Disjoint {
			noAliasScopes = append(noAliasScopes, src1ScopeName)
		}
		s.attachAliasScopeMetadataWithNames(src2Elem, domainName, src2ScopeName, noAliasScopes)
	}
	resultValue, err := s.emitFunctionValueCall(callbackValue, callbackType, []C.LLVMValueRef{src1Elem, src2Elem}, "zip_map.call")
	if err != nil {
		return nil, nil, true, err
	}
	coerced, err := s.coerceValue(resultValue, callbackType.Return, dstElemType)
	if err != nil {
		return nil, nil, true, err
	}
	store := C.LLVMBuildStore(s.builder, coerced, dstElemPtr)
	if hasScopedNoAlias {
		var noAliasScopes []string
		if dstSrc1Disjoint {
			noAliasScopes = append(noAliasScopes, src1ScopeName)
		}
		if dstSrc2Disjoint {
			noAliasScopes = append(noAliasScopes, src2ScopeName)
		}
		s.attachAliasScopeMetadataWithNames(store, domainName, dstScopeName, noAliasScopes)
	}
	nextIndex := C.LLVMBuildAdd(s.builder, indexValue, one, cStringFree("zip_map.index.next"))
	bodyEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, loopCondBB)
	nextIndexValues := []C.LLVMValueRef{nextIndex}
	nextIndexBlocks := []C.LLVMBasicBlockRef{bodyEnd}
	C.LLVMAddIncoming(indexValue, llvmValueSlicePtr(nextIndexValues), llvmBlockSlicePtr(nextIndexBlocks), 1)

	C.LLVMPositionBuilderAtEnd(s.builder, loopEndBB)
	return nil, s.g.result.NamedTypes["void"], true, nil
}

func zipMapViewInfo(t semantic.Type) (semantic.Type, semantic.Type, bool) {
	switch tt := t.(type) {
	case *semantic.ViewType:
		if tt == nil {
			return nil, nil, false
		}
		return tt, tt.Elem, true
	case *semantic.DArrayViewType:
		if tt == nil || tt.SurfaceName == "packedtags" {
			return nil, nil, false
		}
		return tt, tt.Elem, true
	default:
		return nil, nil, false
	}
}

func (s *functionState) emitDenseViewDataPointer(viewValue C.LLVMValueRef, elemType semantic.Type, name string) (C.LLVMValueRef, error) {
	if s == nil || s.g == nil {
		return nil, fmt.Errorf("missing function state for dense view data extraction")
	}
	if elemType == nil {
		return nil, fmt.Errorf("missing dense view element type")
	}
	return C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree(name+".data")), nil
}

func (s *functionState) emitDenseViewIndexedAddress(dataPtr C.LLVMValueRef, elemType semantic.Type, indexValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	if s == nil || s.g == nil {
		return nil, fmt.Errorf("missing function state for dense view indexing")
	}
	elemLLVMType, err := s.g.lowerType(elemType)
	if err != nil {
		return nil, err
	}
	indices := []C.LLVMValueRef{indexValue}
	return C.LLVMBuildGEP2(s.builder, elemLLVMType, dataPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree(name+".ptr")), nil
}

func (s *functionState) emitArenaViewSliceValue(viewValue C.LLVMValueRef, viewType *semantic.DArrayViewType, startValue C.LLVMValueRef, endValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	if viewType == nil {
		return nil, fmt.Errorf("missing dview type for slice helper lowering")
	}
	usizeType := s.g.result.NamedTypes["usize"]
	helperType := &semantic.FuncType{
		Name:   "arena_da_view_slice",
		Params: []semantic.Type{viewType, usizeType, usizeType},
		Return: viewType,
	}
	callee, err := s.g.ensureFunctionDeclared("arena_da_view_slice", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	return s.buildCall(llvmFnType, callee, []C.LLVMValueRef{viewValue, startValue, endValue}, name), nil
}

func (s *functionState) emitChunksExactValidation(chunkSizeValue C.LLVMValueRef, totalValue C.LLVMValueRef, prefix string) error {
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
	nonZeroBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(prefix+".nonzero"))
	okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(prefix+".ok"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(prefix+".fail"))
	isNonZero := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), chunkSizeValue, zero, cStringFree(prefix+".chunk.nonzero"))
	C.LLVMBuildCondBr(s.builder, isNonZero, nonZeroBB, failBB)

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	trapFn, err := s.ensureTrapFunction()
	if err != nil {
		return err
	}
	trapType, err := s.g.lowerFunctionType(&semantic.FuncType{Name: "llvm.trap", Return: s.g.result.NamedTypes["void"]})
	if err != nil {
		return err
	}
	s.buildCall(trapType, trapFn, nil, "")
	C.LLVMBuildUnreachable(s.builder)

	C.LLVMPositionBuilderAtEnd(s.builder, nonZeroBB)
	remainder := C.LLVMBuildURem(s.builder, totalValue, chunkSizeValue, cStringFree(prefix+".remainder"))
	isExact := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), remainder, zero, cStringFree(prefix+".exact"))
	C.LLVMBuildCondBr(s.builder, isExact, okBB, failBB)

	C.LLVMPositionBuilderAtEnd(s.builder, okBB)
	return nil
}

func (s *functionState) emitPackedStoreConstructorValue(expr *ast.CallExpr, storeType *semantic.PackedEnumStoreType) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || len(expr.Args) != 1 {
		return nil, nil, fmt.Errorf("packed store constructor expects exactly one arena argument")
	}
	value, err := s.emitPackedStoreValue(expr.Args[0], storeType)
	if err != nil {
		return nil, nil, err
	}
	return value, storeType, nil
}

func (s *functionState) emitTreeStoreConstructorValue(expr *ast.CallExpr, storeType *semantic.TreeStoreType) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || len(expr.Args) != 1 {
		return nil, nil, fmt.Errorf("tree store constructor expects exactly one arena argument")
	}
	value, err := s.emitTreeStoreValue(expr.Args[0], storeType)
	if err != nil {
		return nil, nil, err
	}
	return value, storeType, nil
}

func (s *functionState) emitTreeStoreValue(arenaExpr ast.Expr, storeType *semantic.TreeStoreType) (C.LLVMValueRef, error) {
	if storeType == nil {
		return nil, fmt.Errorf("missing tree store type")
	}
	arenaPtr, _, err := s.emitAddressOrTemp(arenaExpr)
	if err != nil {
		return nil, err
	}
	return s.emitTreeStoreValueFromArenaRef(arenaPtr, storeType)
}

func (s *functionState) emitTreeStoreArenaValue(storeValue C.LLVMValueRef, storeType *semantic.TreeStoreType) (C.LLVMValueRef, error) {
	if storeType == nil {
		return nil, fmt.Errorf("missing tree store type")
	}
	return s.emitTreeStoreArenaValueNamed(storeValue, "tree.store.arena.value"), nil
}

func (s *functionState) emitTreeStoreValueFromExpr(expr ast.Expr) (C.LLVMValueRef, *semantic.TreeStoreType, error) {
	if expr == nil {
		return nil, nil, fmt.Errorf("missing tree store expression")
	}
	objectType := s.exprType(expr)
	if objectType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for tree store expression")
	}
	if storeType, ok := objectType.(*semantic.TreeStoreType); ok {
		value, _, err := s.emitExpr(expr, storeType)
		if err != nil {
			return nil, nil, err
		}
		return value, storeType, nil
	}
	refType, ok := objectType.(*semantic.RefType)
	if !ok || refType.State != semantic.RefStateNonNull {
		return nil, nil, fmt.Errorf("tree store access requires a store value or proven non-null store reference")
	}
	storeType, ok := refType.Elem.(*semantic.TreeStoreType)
	if !ok {
		return nil, nil, fmt.Errorf("tree store access requires a tree store, got %s", objectType.String())
	}
	ptrValue, _, err := s.emitExpr(expr, objectType)
	if err != nil {
		return nil, nil, err
	}
	storeValue, err := s.loadValue(ptrValue, storeType, "tree.store.load")
	if err != nil {
		return nil, nil, err
	}
	return storeValue, storeType, nil
}

func (s *functionState) emitPackedStoreValue(arenaExpr ast.Expr, storeType *semantic.PackedEnumStoreType) (C.LLVMValueRef, error) {
	if storeType == nil {
		return nil, fmt.Errorf("missing packed enum store type")
	}
	arenaPtr, _, err := s.emitAddressOrTemp(arenaExpr)
	if err != nil {
		return nil, err
	}
	if storeType.Enum == nil {
		return nil, fmt.Errorf("packed enum store %s is missing enum metadata", storeType.Name)
	}
	rowType, err := s.g.ensurePackedEnumStorageType(storeType.Enum)
	if err != nil {
		return nil, err
	}
	rowSizeBytes, err := s.g.abiSizeOfLLVMType(rowType)
	if err != nil {
		return nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	rowSizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(rowSizeBytes), 0)
	storeLLVMType, err := s.g.lowerPackedEnumStoreType(storeType)
	if err != nil {
		return nil, err
	}
	sideWords, err := s.g.packedEnumCommonSideTableWordCount(storeType.Enum)
	if err != nil {
		return nil, err
	}
	arenaType := s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	stateHelperName := "ctx_packed_store_state_new"
	stateHelperParams := []semantic.Type{arenaRefType, usizeType}
	stateArgs := []C.LLVMValueRef{arenaPtr, rowSizeValue}
	if s.g.packedLoweringForStore(storeType) == packedEnumABIVariantSparse {
		if sideWords > 0 {
			stateHelperName = "ctx_packed_store_state_new_variant_sparse_with_side_words"
			stateHelperParams = []semantic.Type{arenaRefType, usizeType, usizeType}
			stateArgs = append(stateArgs, C.LLVMConstInt(usizeLLVMType, C.ulonglong(sideWords), 0))
		} else {
			stateHelperName = "ctx_packed_store_state_new_variant_sparse"
		}
	} else if storeType.Enum != nil && storeType.Enum.HasPackedPrefixOverride && storeType.Enum.PackedPrefixOverride == "common-only" && s.g.packedLoweringForStore(storeType) == packedEnumABIIndexSOA {
		prefixWords, err := s.g.packedEnumCommonPrefixWordCount(storeType.Enum)
		if err != nil {
			return nil, err
		}
		if sideWords > 0 {
			stateHelperName = "ctx_packed_store_state_new_with_prefix_and_side_words"
			stateHelperParams = []semantic.Type{arenaRefType, usizeType, usizeType, usizeType}
			stateArgs = append(stateArgs,
				C.LLVMConstInt(usizeLLVMType, C.ulonglong(prefixWords), 0),
				C.LLVMConstInt(usizeLLVMType, C.ulonglong(sideWords), 0),
			)
		} else {
			stateHelperName = "ctx_packed_store_state_new_with_prefix_words"
			stateHelperParams = []semantic.Type{arenaRefType, usizeType, usizeType}
			stateArgs = append(stateArgs, C.LLVMConstInt(usizeLLVMType, C.ulonglong(prefixWords), 0))
		}
	} else if sideWords > 0 {
		stateHelperName = "ctx_packed_store_state_new_with_side_words"
		stateHelperParams = []semantic.Type{arenaRefType, usizeType, usizeType}
		stateArgs = append(stateArgs, C.LLVMConstInt(usizeLLVMType, C.ulonglong(sideWords), 0))
	}
	stateHelperType := &semantic.FuncType{Name: stateHelperName, Params: stateHelperParams, Return: voidRefType}
	stateCallee, err := s.g.ensureFunctionDeclared(stateHelperName, stateHelperType)
	if err != nil {
		return nil, err
	}
	stateLLVMFnType, err := s.g.lowerFunctionType(stateHelperType)
	if err != nil {
		return nil, err
	}
	stateValue := s.buildCall(stateLLVMFnType, stateCallee, stateArgs, "packed.store.state")
	storeValue := C.LLVMGetUndef(storeLLVMType)
	storeValue = C.LLVMBuildInsertValue(s.builder, storeValue, arenaPtr, 0, cStringFree("packed.store.arena"))
	storeValue = C.LLVMBuildInsertValue(s.builder, storeValue, rowSizeValue, 1, cStringFree("packed.store.row_bytes"))
	storeValue = C.LLVMBuildInsertValue(s.builder, storeValue, stateValue, 2, cStringFree("packed.store.state"))
	return storeValue, nil
}

func (s *functionState) emitPackedStoreArenaValue(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType) (C.LLVMValueRef, error) {
	return s.emitPackedStoreArenaValueNamed(storeValue, storeType, "packed.store.arena.value")
}

func (s *functionState) emitPackedStoreFieldValueNamed(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType, index C.unsigned, name string) (C.LLVMValueRef, error) {
	if storeType == nil {
		return nil, fmt.Errorf("missing packed enum store type")
	}
	if block := C.LLVMGetInsertBlock(s.builder); block != nil && storeValue != nil {
		key := packedStoreExtractCacheKey{block: block, store: storeValue, index: index}
		if cached, ok := s.lookupPackedStoreFieldValue(key); ok && cached != nil {
			return cached, nil
		}
		value := C.LLVMBuildExtractValue(s.builder, storeValue, index, cStringFree(name))
		s.cachePackedStoreFieldValue(key, value)
		return value, nil
	}
	return C.LLVMBuildExtractValue(s.builder, storeValue, index, cStringFree(name)), nil
}

func (s *functionState) emitPackedStoreArenaValueNamed(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType, name string) (C.LLVMValueRef, error) {
	return s.emitPackedStoreFieldValueNamed(storeValue, storeType, 0, name)
}

func (s *functionState) emitPackedStoreRowBytesValue(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType) (C.LLVMValueRef, error) {
	return s.emitPackedStoreRowBytesValueNamed(storeValue, storeType, "packed.store.row_bytes.value")
}

func (s *functionState) emitPackedStoreRowBytesValueNamed(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType, name string) (C.LLVMValueRef, error) {
	return s.emitPackedStoreFieldValueNamed(storeValue, storeType, 1, name)
}

func (s *functionState) emitPackedStoreStateValueNamed(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType, name string) (C.LLVMValueRef, error) {
	return s.emitPackedStoreFieldValueNamed(storeValue, storeType, 2, name)
}

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
			value, err := s.g.ensureFunctionDeclared(sym.Name, fnType)
			return value, fnType, err
		}
	}
	if ident, ok := expr.Func.(*ast.Ident); ok {
		if sym, ok := s.g.result.GlobalScope.Lookup(ident.Name); ok {
			fnType, ok := sym.Type.(*semantic.FuncType)
			if !ok {
				return nil, nil, fmt.Errorf("call target %s does not resolve to a function type", ident.Name)
			}
			if decl, ok := sym.Node.(*ast.FuncDecl); ok && len(decl.GenericParams) > 0 {
				argTypes := make([]semantic.Type, 0, len(expr.Args))
				for _, arg := range expr.Args {
					argTypes = append(argTypes, s.exprType(arg))
				}
				bindings := inferTypeBindingsFromCall(fnType, expr.Args, argTypes)
				value, specialized, err := s.g.ensureSpecializedFunction(decl, fnType, bindings)
				return value, specialized, err
			}
			specialized := s.specializeFunctionType(fnType)
			value, err := s.g.ensureFunctionDeclared(ident.Name, specialized)
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
	if fieldExpr, ok := expr.(*ast.FieldExpr); ok {
		_, _, handled, err := s.resolveStaticInterfaceMethod(fieldExpr)
		return handled && err == nil
	}
	ident, ok := expr.(*ast.Ident)
	if !ok || s == nil || s.g == nil || s.g.result == nil {
		return false
	}
	sym, ok := s.g.result.GlobalScope.Lookup(ident.Name)
	if !ok {
		return false
	}
	return sym.Kind == semantic.SymbolFunc || sym.Kind == semantic.SymbolExternFunc
}

func (s *functionState) emitFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, error) {
	if value, fieldType, handled, err := s.emitStaticInterfaceMethodExpr(expr); handled {
		return value, fieldType, err
	}
	if treeType, variant, ok := s.treeConstructorInfoFromField(expr); ok {
		if variant == nil {
			return nil, nil, fmt.Errorf("unknown tree constructor %s.%s", treeType.Name, expr.Field)
		}
		if len(variant.Payload) == 0 {
			if treeType.Family != nil && treeType.Family.Decl != nil && len(treeType.Family.Decl.Common) != 0 {
				return nil, nil, fmt.Errorf("tree constructor %s.%s requires explicit common fields; use call syntax with named arguments", treeType.Name, variant.Name)
			}
			return s.emitTreeConstructorValue(nil, treeType, variant, nil, nil, nil)
		}
	}
	if enumType, variant, ok := s.enumConstructorInfoFromField(expr); ok {
		if variant == nil {
			return nil, nil, fmt.Errorf("unknown enum constructor %s.%s", enumType.Name, expr.Field)
		}
		if len(variant.Payload) == 0 {
			return s.emitEnumConstructorValue(nil, enumType, variant, nil, nil)
		}
	}
	if fieldType, ok := dstrSyntheticFieldType(s.exprType(expr.Object), expr.Field); ok {
		return s.emitRuntimeStringLenExpr(expr.Object, fieldType)
	}
	if value, fieldType, handled, err := s.emitPackedStoreCountExpr(expr); handled {
		return value, fieldType, err
	}
	if value, fieldType, handled, err := s.emitPackedStoreTagsExpr(expr); handled {
		return value, fieldType, err
	}
	if value, fieldType, handled, err := s.emitPackedVariantViewFieldExpr(expr); handled {
		return value, fieldType, err
	}
	if value, fieldType, handled, err := s.emitTreeAttributeFieldExpr(expr); handled {
		return value, fieldType, err
	}
	if value, fieldType, handled, err := s.emitTreeFieldExpr(expr); handled {
		return value, fieldType, err
	}
	if value, fieldType, handled, err := s.emitPackedCommonFieldExpr(expr); handled {
		return value, fieldType, err
	}
	if value, fieldType, handled, err := s.emitBuiltinDictEntryFieldExpr(expr); handled {
		return value, fieldType, err
	}
	ptr, fieldType, addressErr := s.emitReadableFieldAddress(expr)
	if addressErr == nil {
		value, loadErr := s.loadValue(ptr, fieldType, expr.Field)
		return value, fieldType, loadErr
	}
	objValue, objType, err := s.emitExpr(expr.Object, nil)
	if err != nil {
		return nil, nil, err
	}
	fieldType, index, _, pointerLike, err := s.g.fieldInfo(objType, expr.Field)
	if err != nil {
		return nil, nil, err
	}
	if pointerLike {
		return nil, nil, fmt.Errorf("field %s requires an addressable object (base %T: %v)", expr.Field, expr.Object, addressErr)
	}
	value := C.LLVMBuildExtractValue(s.builder, objValue, C.unsigned(index), cStringFree(expr.Field))
	return value, fieldType, nil
}

func (s *functionState) emitPackedStoreCountExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil || expr.Field != "count" {
		return nil, nil, false, nil
	}
	ops, ok, err := s.packedStoreOpsFromExpr(expr.Object)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok {
		return nil, nil, false, nil
	}
	value, err := ops.storeCount("packed.store.count")
	if err != nil {
		return nil, nil, true, err
	}
	return value, s.g.result.NamedTypes["usize"], true, nil
}

func (s *functionState) emitPackedStoreCountValue(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType, name string) (C.LLVMValueRef, error) {
	return (&packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}).storeCount(name)
}

func (s *functionState) emitPackedStoreTagsExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil || expr.Field != "tags" {
		return nil, nil, false, nil
	}
	ops, ok, err := s.packedStoreOpsFromExpr(expr.Object)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok || ops.storeType == nil || ops.storeType.Enum == nil || !semantic.IsFrozenPackedEnumStoreType(ops.storeType) {
		return nil, nil, false, nil
	}
	resultType, ok := s.exprType(expr).(*semantic.DArrayViewType)
	if !ok || resultType == nil {
		return nil, nil, true, fmt.Errorf("packed store tags requires dview result type")
	}
	totalValue, err := ops.storeCount("packed.store.tags.count")
	if err != nil {
		return nil, nil, true, err
	}
	zeroType, err := s.g.lowerType(s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, nil, true, err
	}
	zeroValue := C.LLVMConstInt(zeroType, 0, 0)
	value, actualType, err := ops.storeTagsView(zeroValue, totalValue, resultType, "packed.store.tags")
	return value, actualType, true, err
}

func (s *functionState) emitPackedVariantViewFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	name, hasName := packedVariantViewName(expr.Object)
	binding, ok := s.lookupPackedVariantView(name)
	if !ok {
		viewType, ok := s.exprType(expr.Object).(*semantic.PackedVariantViewType)
		if !ok || viewType == nil {
			return nil, nil, false, nil
		}
		objectValue, _, err := s.emitExpr(expr.Object, viewType)
		if err != nil {
			return nil, nil, true, err
		}
		binding, err = s.unpackPackedVariantViewValue(objectValue, viewType)
		if err != nil {
			return nil, nil, true, err
		}
		if hasName {
			s.bindPackedVariantView(name, viewType, binding.ptr, binding.handle, binding.store, binding.payloadValues)
		}
	}
	if binding.typ == nil || (binding.ptr == nil && binding.handle == nil) {
		return nil, nil, false, nil
	}
	origin := packedReadOriginKey{}
	if resolvedOrigin, ok, err := s.packedReadOriginKey(expr.Object); err != nil {
		return nil, nil, true, err
	} else if ok {
		origin = resolvedOrigin
	}
	field, ok := binding.typ.Field(expr.Field)
	if !ok {
		return nil, nil, true, fmt.Errorf("%s has no field %s", binding.typ.String(), expr.Field)
	}
	if _, isCommonField := binding.typ.Enum.Common[expr.Field]; isCommonField {
		if hasName {
			if cachedValue, ok := s.lookupPackedCommonFieldValue(name, binding.typ.Enum, expr.Field); ok {
				return cachedValue, field.Type, true, nil
			}
		}
		layout, err := s.g.packedEnumCommonFieldLayout(binding.typ.Enum, expr.Field)
		if err != nil {
			return nil, nil, true, err
		}
		fieldType := layout.Field.Type
		if !layout.StoredInline {
			if binding.handle == nil || binding.store.typ == nil {
				return nil, nil, true, fmt.Errorf("packed enum common field %s.%s is stored in a side table and requires store context", binding.typ.Enum.Name, expr.Field)
			}
			value, err := s.emitPackedSideTableFieldRead(binding.handle, binding.typ.Enum, &binding.store, fieldType, layout.SideWordOffset, layout.WordCount, origin, "packed.view.common.side")
			return value, fieldType, true, err
		}
		if binding.ptr == nil && binding.handle != nil && binding.store.typ != nil {
			ops, ok := s.packedStoreOpsFromBinding(&binding.store)
			if ok && ops.canDirectWordRead() {
				fieldOffsetBytes, ok, err := s.packedEnumDirectFieldByteOffset(binding.typ.Enum, layout.RowFieldIndex)
				if err != nil {
					return nil, nil, true, err
				}
				if ok {
					coerced, err := s.emitPackedDirectFieldReadAtOrigin(ops, binding.handle, binding.typ.Enum, fieldType, fieldOffsetBytes, origin, "packed.view.common")
					if err != nil {
						return nil, nil, true, err
					}
					return coerced, fieldType, true, nil
				}
			}
			decodedPtr, err := s.decodePackedEnumHandleWithStore(binding.handle, binding.typ.Enum, &binding.store)
			if err != nil {
				return nil, nil, true, err
			}
			binding.ptr = decodedPtr
			if hasName {
				s.updatePackedVariantViewDecodedPtr(name, decodedPtr)
			}
		}
		containerType, err := s.loweredEnumStorageType(binding.typ.Enum)
		if err != nil {
			return nil, nil, true, err
		}
		fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerType, binding.ptr, C.unsigned(layout.RowFieldIndex), cStringFree("view.common.field"))
		value, err := s.loadValue(fieldPtr, fieldType, expr.Field)
		return value, fieldType, true, err
	}
	if cachedValue, ok := binding.payloadValues.lookup(expr.Field); ok && cachedValue != nil {
		return cachedValue, field.Type, true, nil
	}
	if binding.ptr == nil && binding.handle != nil && binding.store.typ != nil {
		ops, ok := s.packedStoreOpsFromBinding(&binding.store)
		if ok && ops.canDirectWordRead() {
			payloadValues, ok, err := s.readPackedEnumVariantPayloadWithStore(binding.handle, binding.typ.Enum, binding.typ.Variant, &binding.store, origin)
			if err != nil {
				return nil, nil, true, err
			}
			if ok {
				index, ok := binding.typ.Variant.PayloadIndex(expr.Field)
				if !ok || index < 0 || index >= len(payloadValues) {
					return nil, nil, true, fmt.Errorf("%s has no field %s", binding.typ.String(), expr.Field)
				}
				return payloadValues[index], field.Type, true, nil
			}
		}
		decodedPtr, err := s.decodePackedEnumHandleWithStore(binding.handle, binding.typ.Enum, &binding.store)
		if err != nil {
			return nil, nil, true, err
		}
		binding.ptr = decodedPtr
		if hasName {
			s.updatePackedVariantViewDecodedPtr(name, decodedPtr)
		}
	}
	payloadValues, err := s.loadEnumVariantPayload(binding.ptr, binding.handle, binding.typ.Enum, binding.typ.Variant, nil, origin)
	if err != nil {
		return nil, nil, true, err
	}
	index, ok := binding.typ.Variant.PayloadIndex(expr.Field)
	if !ok || index < 0 || index >= len(payloadValues) {
		return nil, nil, true, fmt.Errorf("%s has no field %s", binding.typ.String(), expr.Field)
	}
	return payloadValues[index], field.Type, true, nil
}

func packedVariantViewName(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name, true
	case *ast.FieldExpr:
		base, ok := packedVariantViewName(n.Object)
		if !ok || base == "" {
			return "", false
		}
		return base + "." + n.Field, true
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return "", false
		}
		base, ok := packedVariantViewName(n.Object)
		if !ok || base == "" {
			return "", false
		}
		indexKey, ok := packedEnumStorageIndexKey(n.Index)
		if !ok {
			return "", false
		}
		return base + "[" + indexKey + "]", true
	case *ast.CastExpr:
		return packedVariantViewName(n.Operand)
	case *ast.CanExpr:
		return packedVariantViewName(n.Expr)
	case *ast.ParenExpr:
		return packedVariantViewName(n.Inner)
	default:
		return "", false
	}
}

func (s *functionState) emitPackedCommonFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	objectType := s.exprType(expr.Object)
	if objectType == nil {
		return nil, nil, false, nil
	}
	containerType := objectType
	if refType, ok := objectType.(*semantic.RefType); ok {
		containerType = refType.Elem
	}
	containerType = semantic.StripAggregateStateType(containerType)
	enumType, ok := containerType.(*semantic.EnumType)
	if !ok || enumType == nil || !enumType.Packed {
		return nil, nil, false, nil
	}
	if _, ok := enumType.Common[expr.Field]; !ok {
		return nil, nil, false, nil
	}
	layout, err := s.g.packedEnumCommonFieldLayout(enumType, expr.Field)
	if err != nil {
		return nil, nil, true, err
	}
	fieldType := layout.Field.Type
	if key, ok := s.packedEnumStoragePath(expr.Object); ok {
		if cachedValue, ok := s.lookupPackedCommonFieldValue(key, enumType, expr.Field); ok {
			return cachedValue, fieldType, true, nil
		}
	}
	if layout.StoredInline {
		if key, ok := s.packedEnumStoragePath(expr.Object); ok {
			if _, ok := s.lookupPackedEnumStorage(key, enumType); ok {
				return nil, nil, false, nil
			}
		}
	}
	store, ok := s.lookupPackedStore(enumType)
	if !ok {
		if layout.StoredInline {
			return nil, nil, false, nil
		}
		return nil, nil, true, fmt.Errorf("packed enum common field %s.%s is stored in a side table and requires store context", enumType.Name, expr.Field)
	}
	if !layout.StoredInline {
		handleValue, err := s.packedEnumFieldHandleValue(expr.Object, objectType, enumType)
		if err != nil {
			return nil, nil, true, err
		}
		origin, ok, err := s.packedReadOriginKey(expr.Object)
		if err != nil {
			return nil, nil, true, err
		}
		if !ok {
			origin = packedReadOriginKey{}
		}
		value, err := s.emitPackedSideTableFieldRead(handleValue, enumType, &store, fieldType, layout.SideWordOffset, layout.WordCount, origin, "packed.common.side")
		if err != nil {
			return nil, nil, true, err
		}
		return value, fieldType, true, nil
	}
	ops, ok := s.packedStoreOpsFromBinding(&store)
	if !ok || !ops.canDirectWordRead() {
		return nil, nil, false, nil
	}
	if s.g != nil && s.g.result != nil && s.g.result.ExprHasOnlyFrozenPackedStoreDeps(expr.Object) {
		if !packedModeUsesDenseIndexHandle(s.g.packedModeForEnum(enumType)) {
			return nil, nil, false, nil
		}
	}
	fieldOffsetBytes, ok, err := s.packedEnumDirectFieldByteOffset(enumType, layout.RowFieldIndex)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok {
		return nil, nil, false, nil
	}
	handleValue, err := s.packedEnumFieldHandleValue(expr.Object, objectType, enumType)
	if err != nil {
		return nil, nil, true, err
	}
	origin, ok, err := s.packedReadOriginKey(expr.Object)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok {
		origin = packedReadOriginKey{}
	}
	coerced, err := s.emitPackedDirectFieldReadAtOrigin(ops, handleValue, enumType, fieldType, fieldOffsetBytes, origin, "packed.common.store")
	if err != nil {
		return nil, nil, true, err
	}
	return coerced, fieldType, true, nil
}

func (s *functionState) emitPackedStoreValueAtDenseKey(ops *packedStoreOps, keyIndex C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	if ops == nil || ops.storeType == nil || ops.storeType.Enum == nil {
		return nil, nil, fmt.Errorf("dense-key packed store read requires store metadata")
	}
	switch s.g.packedModeForEnum(ops.storeType.Enum) {
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse:
		coerced, err := s.coerceValue(keyIndex, s.g.result.NamedTypes["u32"], ops.storeType.Enum)
		if err != nil {
			return nil, nil, err
		}
		return coerced, ops.storeType.Enum, nil
	default:
		return nil, nil, fmt.Errorf("unsupported packed enum ABI mode %d", s.g.packedModeForEnum(ops.storeType.Enum))
	}
}

func (s *functionState) emitPackedStoreIndexExpr(expr *ast.IndexExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	ops, ok, err := s.packedStoreOpsFromExpr(expr.Object)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok {
		return nil, nil, false, nil
	}
	if keyIndex, _, handled, err := s.emitNodeKeyIndexValue(expr.Index); handled {
		if err != nil {
			return nil, nil, true, err
		}
		value, actualType, err := s.emitPackedStoreValueAtDenseKey(ops, keyIndex, "packed.store.key")
		return value, actualType, true, err
	}
	indexValue, _, err := s.emitExpr(expr.Index, s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, nil, true, err
	}
	value, actualType, err := ops.storeValueAt(indexValue, "packed.store.index")
	return value, actualType, true, err
}

func (s *functionState) emitPackedStoreSliceExpr(expr *ast.SliceExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	ops, ok, err := s.packedStoreOpsFromExpr(expr.Object)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok {
		return nil, nil, false, nil
	}
	startValue, _, err := s.emitExpr(expr.Start, s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, nil, true, err
	}
	endValue, _, err := s.emitExpr(expr.End, s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, nil, true, err
	}
	resultType := s.exprType(expr)
	value, actualType, err := ops.storeSlice(startValue, endValue, resultType, "packed.store.view")
	return value, actualType, true, err
}

func (s *functionState) packedEnumFieldHandleValue(expr ast.Expr, objectType semantic.Type, enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	if refType, ok := objectType.(*semantic.RefType); ok {
		if refEnum, ok := refType.Elem.(*semantic.EnumType); ok && refEnum == enumType {
			refValue, _, err := s.emitExpr(expr, objectType)
			if err != nil {
				return nil, err
			}
			return s.loadValue(refValue, enumType, "packed.common.handle")
		}
	}
	handleValue, _, err := s.emitExpr(expr, objectType)
	if err != nil {
		return nil, err
	}
	return handleValue, nil
}

func (s *functionState) emitRawMemcpy(dstPtr C.LLVMValueRef, srcPtr C.LLVMValueRef, byteCount uint64, name string) error {
	if byteCount == 0 {
		return nil
	}
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	usizeType := s.g.result.NamedTypes["usize"]
	memcpyType := &semantic.FuncType{Name: "memcpy", Params: []semantic.Type{voidRefType, voidRefType, usizeType}, Return: voidRefType}
	memcpyCallee, err := s.g.ensureFunctionDeclared("memcpy", memcpyType)
	if err != nil {
		return err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	byteCountValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(byteCount), 0)
	_ = s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{dstPtr, srcPtr, byteCountValue}, name)
	return nil
}

func (s *functionState) emitByteOffsetPtr(basePtr C.LLVMValueRef, byteOffset uint64, name string) (C.LLVMValueRef, error) {
	i8Type := s.g.result.NamedTypes["u8"]
	i8LLVMType, err := s.g.lowerType(i8Type)
	if err != nil {
		return nil, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	indices := []C.LLVMValueRef{C.LLVMConstInt(usizeType, C.ulonglong(byteOffset), 0)}
	return C.LLVMBuildGEP2(s.builder, i8LLVMType, basePtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree(name)), nil
}

func (s *functionState) emitPackedSideTableFieldRead(handleValue C.LLVMValueRef, enumType *semantic.EnumType, store *packedStoreBinding, fieldType semantic.Type, sideWordOffset uint64, wordCount uint64, origin packedReadOriginKey, name string) (C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("packed side-table field read requires packed enum metadata")
	}
	if !packedModeUsesDenseIndexHandle(s.g.packedModeForEnum(enumType)) {
		return nil, fmt.Errorf("packed enum %s side-tabled common fields require an index-based packed ABI", enumType.Name)
	}
	ops, ok := s.packedStoreOpsFromBinding(store)
	if !ok {
		return nil, fmt.Errorf("packed enum %s side-tabled common-field read requires store context", enumType.Name)
	}
	fieldSizeBytes, err := s.g.abiSizeOfType(fieldType)
	if err != nil {
		return nil, err
	}
	if fieldSizeBytes == 0 || wordCount == 0 {
		return s.zeroValue(fieldType)
	}
	fieldPtr, err := s.createEntryAlloca(name+".tmp", fieldType)
	if err != nil {
		return nil, err
	}
	fieldLLVMType, err := s.g.lowerType(fieldType)
	if err != nil {
		return nil, err
	}
	C.LLVMBuildStore(s.builder, C.LLVMConstNull(fieldLLVMType), fieldPtr)
	wordBytes := uint64(s.g.wordBits / 8)
	if wordBytes == 0 {
		wordBytes = 8
	}
	for i := uint64(0); i < wordCount; i++ {
		wordOffsetValue, err := s.g.lowerBuiltin("usize")
		if err != nil {
			return nil, err
		}
		wordValue, err := ops.loadSideWordAtOrigin(handleValue, C.LLVMConstInt(wordOffsetValue, C.ulonglong(sideWordOffset+i), 0), origin, name+".word")
		if err != nil {
			return nil, err
		}
		wordPtr, err := s.createEntryAlloca(name+".word.tmp", s.g.result.NamedTypes["uintptr"])
		if err != nil {
			return nil, err
		}
		C.LLVMBuildStore(s.builder, wordValue, wordPtr)
		dstPtr, err := s.emitByteOffsetPtr(fieldPtr, i*wordBytes, name+".dst")
		if err != nil {
			return nil, err
		}
		remainingBytes := fieldSizeBytes - i*wordBytes
		copyBytes := wordBytes
		if remainingBytes < copyBytes {
			copyBytes = remainingBytes
		}
		if err := s.emitRawMemcpy(dstPtr, wordPtr, copyBytes, name+".copy"); err != nil {
			return nil, err
		}
	}
	return s.loadValue(fieldPtr, fieldType, name+".value")
}

func (s *functionState) emitPackedDirectFieldReadAtOrigin(ops *packedStoreOps, handleValue C.LLVMValueRef, enumType *semantic.EnumType, fieldType semantic.Type, fieldOffsetBytes uint64, origin packedReadOriginKey, name string) (C.LLVMValueRef, error) {
	if ops == nil || enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("packed direct field read requires packed enum metadata")
	}
	fieldSizeBytes, err := s.g.abiSizeOfType(fieldType)
	if err != nil {
		return nil, err
	}
	if fieldSizeBytes == 0 {
		return s.zeroValue(fieldType)
	}
	var cacheKey packedDirectFieldReadCacheKey
	cacheDirectField := false
	if ops.canCacheDirectReadValues(enumType) {
		if s.packedDirectFieldReads == nil {
			s.packedDirectFieldReads = map[packedDirectFieldReadCacheKey]C.LLVMValueRef{}
		}
		originKey, cacheHandle := ops.directReadCacheIdentity(enumType, origin, handleValue)
		cacheKey = packedDirectFieldReadCacheKey{
			block:    ops.currentBlock(),
			store:    ops.storeValue,
			enumType: enumType,
			origin:   originKey,
			handle:   cacheHandle,
			offset:   fieldOffsetBytes,
			size:     fieldSizeBytes,
			typeKey:  fieldType.String(),
		}
		if cached, ok := s.packedDirectFieldReads[cacheKey]; ok && cached != nil {
			return cached, nil
		}
		cacheDirectField = true
	}
	wordBytes := uint64(s.g.wordBits / 8)
	if wordBytes == 0 {
		wordBytes = 8
	}
	wordOffset := fieldOffsetBytes / wordBytes
	byteOffsetInWord := fieldOffsetBytes % wordBytes
	if byteOffsetInWord+fieldSizeBytes <= wordBytes {
		usizeType, err := s.g.lowerBuiltin("usize")
		if err != nil {
			return nil, err
		}
		wordValue, err := ops.loadPayloadWordAtOrigin(handleValue, enumType, C.LLVMConstInt(usizeType, C.ulonglong(wordOffset), 0), origin, name+".word")
		if err != nil {
			return nil, err
		}
		if byteOffsetInWord != 0 {
			uintptrLLVMType, err := s.g.lowerBuiltin("uintptr")
			if err != nil {
				return nil, err
			}
			shiftBits := C.LLVMConstInt(uintptrLLVMType, C.ulonglong(byteOffsetInWord*8), 0)
			wordValue = C.LLVMBuildLShr(s.builder, wordValue, shiftBits, cStringFree(name+".shift"))
		}
		coerced, err := s.coerceValue(wordValue, s.g.result.NamedTypes["uintptr"], fieldType)
		if err != nil {
			return nil, err
		}
		if cacheDirectField {
			s.packedDirectFieldReads[cacheKey] = coerced
		}
		return coerced, nil
	}

	fieldPtr, err := s.createEntryAlloca(name+".tmp", fieldType)
	if err != nil {
		return nil, err
	}
	fieldLLVMType, err := s.g.lowerType(fieldType)
	if err != nil {
		return nil, err
	}
	C.LLVMBuildStore(s.builder, C.LLVMConstNull(fieldLLVMType), fieldPtr)
	lastByte := byteOffsetInWord + fieldSizeBytes
	wordCount := lastByte / wordBytes
	if lastByte%wordBytes != 0 {
		wordCount++
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	for i := uint64(0); i < wordCount; i++ {
		wordValue, err := ops.loadPayloadWordAtOrigin(handleValue, enumType, C.LLVMConstInt(usizeType, C.ulonglong(wordOffset+i), 0), origin, name+".word")
		if err != nil {
			return nil, err
		}
		wordPtr, err := s.createEntryAlloca(name+".word.tmp", s.g.result.NamedTypes["uintptr"])
		if err != nil {
			return nil, err
		}
		C.LLVMBuildStore(s.builder, wordValue, wordPtr)
		wordStart := i * wordBytes
		copyStart := wordStart
		if copyStart < byteOffsetInWord {
			copyStart = byteOffsetInWord
		}
		copyEnd := wordStart + wordBytes
		if copyEnd > lastByte {
			copyEnd = lastByte
		}
		if copyEnd <= copyStart {
			continue
		}
		srcPtr := wordPtr
		if copyStart > wordStart {
			srcPtr, err = s.emitByteOffsetPtr(wordPtr, copyStart-wordStart, name+".src")
			if err != nil {
				return nil, err
			}
		}
		dstPtr, err := s.emitByteOffsetPtr(fieldPtr, copyStart-byteOffsetInWord, name+".dst")
		if err != nil {
			return nil, err
		}
		if err := s.emitRawMemcpy(dstPtr, srcPtr, copyEnd-copyStart, name+".copy"); err != nil {
			return nil, err
		}
	}
	value, err := s.loadValue(fieldPtr, fieldType, name+".value")
	if err != nil {
		return nil, err
	}
	if cacheDirectField {
		s.packedDirectFieldReads[cacheKey] = value
	}
	return value, nil
}

func (s *functionState) emitPackSideTableFieldValue(bufferPtr C.LLVMValueRef, byteOffset uint64, fieldValue C.LLVMValueRef, fieldType semantic.Type, name string) error {
	fieldSizeBytes, err := s.g.abiSizeOfType(fieldType)
	if err != nil {
		return err
	}
	if fieldSizeBytes == 0 {
		return nil
	}
	fieldPtr, err := s.emitStackTempValue(fieldValue, fieldType, name+".field.tmp")
	if err != nil {
		return err
	}
	dstPtr, err := s.emitByteOffsetPtr(bufferPtr, byteOffset, name+".dst")
	if err != nil {
		return err
	}
	return s.emitRawMemcpy(dstPtr, fieldPtr, fieldSizeBytes, name+".copy")
}

func (s *functionState) packedEnumDirectFieldByteOffset(enumType *semantic.EnumType, fieldIndex int) (uint64, bool, error) {
	if enumType == nil || !enumType.Packed || fieldIndex <= 0 {
		return 0, false, nil
	}
	payloadIndex, err := s.g.packedEnumPayloadFieldIndex(enumType)
	if err != nil {
		return 0, false, err
	}
	if fieldIndex >= payloadIndex {
		return 0, false, nil
	}
	rowType, err := s.g.ensurePackedEnumStorageType(enumType)
	if err != nil {
		return 0, false, err
	}
	offsetBytes, err := s.g.abiOffsetOfLLVMElement(rowType, fieldIndex)
	if err != nil {
		return 0, false, err
	}
	return offsetBytes, true, nil
}

func (s *functionState) readPackedEnumWordWithStore(handleValue C.LLVMValueRef, enumType *semantic.EnumType, store *packedStoreBinding, wordOffset C.LLVMValueRef) (C.LLVMValueRef, error) {
	ops, ok := s.packedStoreOpsFromBinding(store)
	if !ok {
		return nil, fmt.Errorf("packed enum %s common-field read requires store context", enumType.Name)
	}
	return ops.loadPayloadWord(handleValue, enumType, wordOffset, "packed.common.store")
}

func (s *functionState) packedStoreConstructorInfoFromField(expr *ast.FieldExpr) (*semantic.PackedEnumStoreType, bool) {
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, false
	}
	base, ok := s.g.result.NamedTypes[ident.Name]
	if !ok {
		return nil, false
	}
	enumType, ok := base.(*semantic.EnumType)
	if !ok || !enumType.Packed || expr.Field != "Store" || enumType.StoreType == nil {
		return nil, false
	}
	return semantic.PackedEnumStoreWithState(enumType.StoreType, s.g.result.NamedTypes["Local"]), true
}

func (s *functionState) treeStoreConstructorInfoFromField(expr *ast.FieldExpr) (*semantic.TreeStoreType, bool) {
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, false
	}
	base, ok := s.g.result.NamedTypes[ident.Name]
	if !ok {
		return nil, false
	}
	treeType, ok := base.(*semantic.TreeType)
	if !ok || expr.Field != "Store" || treeType.StoreType == nil {
		return nil, false
	}
	return semantic.TreeStoreWithState(treeType.StoreType, s.g.result.NamedTypes["Local"]), true
}

func (s *functionState) packedStoreConstructorCall(expr *ast.CallExpr) (*semantic.PackedEnumStoreType, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, false
	}
	return s.packedStoreConstructorInfoFromField(fieldExpr)
}

func (s *functionState) treeStoreConstructorCall(expr *ast.CallExpr) (*semantic.TreeStoreType, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, false
	}
	return s.treeStoreConstructorInfoFromField(fieldExpr)
}

func (s *functionState) emitSliceExpr(expr *ast.SliceExpr) (C.LLVMValueRef, semantic.Type, error) {
	if value, resultType, handled, err := s.emitPackedStoreSliceExpr(expr); handled {
		return value, resultType, err
	}
	if value, resultType, handled, err := s.emitFixedArraySliceExpr(expr); handled {
		return value, resultType, err
	}
	info, ok := runtimeSliceOperandInfo(s.exprType(expr.Object), s.exprType(expr))
	if !ok {
		return nil, nil, fmt.Errorf("slice is not implemented for %s", s.exprType(expr.Object).String())
	}
	var (
		objectValue C.LLVMValueRef
		err         error
	)
	if info.useAddress {
		objectValue, _, err = s.emitAddressOrTemp(expr.Object)
	} else {
		objectValue, _, err = s.emitExpr(expr.Object, info.operandType)
	}
	if err != nil {
		return nil, nil, err
	}
	startValue, _, err := s.emitExpr(expr.Start, info.indexType)
	if err != nil {
		return nil, nil, err
	}
	endValue, _, err := s.emitExpr(expr.End, info.indexType)
	if err != nil {
		return nil, nil, err
	}
	helperType := &semantic.FuncType{
		Name:   info.helperName,
		Params: []semantic.Type{info.operandType, info.indexType, info.indexType},
		Return: info.resultType,
	}
	callee, err := s.g.ensureFunctionDeclared(info.helperName, helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	args := []C.LLVMValueRef{objectValue, startValue, endValue}
	callName := "slicetmp"
	if isVoidType(info.resultType) {
		callName = ""
	}
	call := s.buildCall(llvmFnType, callee, args, callName)
	return call, info.resultType, nil
}

func (s *functionState) emitFixedArraySliceExpr(expr *ast.SliceExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	arrayType, arrayPtr, handled, err := s.fixedArraySliceBase(expr.Object)
	if err != nil || !handled {
		return nil, nil, handled, err
	}
	resultType := s.exprType(expr)
	usizeSemanticType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeSemanticType)
	if err != nil {
		return nil, nil, true, err
	}
	startValue, _, err := s.emitExpr(expr.Start, usizeSemanticType)
	if err != nil {
		return nil, nil, true, err
	}
	endValue, _, err := s.emitExpr(expr.End, usizeSemanticType)
	if err != nil {
		return nil, nil, true, err
	}
	arrayLen := C.LLVMConstInt(usizeLLVMType, C.ulonglong(arrayType.ConstSize), 0)
	startClamped := s.emitUnsignedMin(startValue, arrayLen, usizeLLVMType, "arrayslice.start.clamped")
	endClamped := s.emitUnsignedMin(endValue, arrayLen, usizeLLVMType, "arrayslice.end.clamped")
	boundedStart := s.emitUnsignedMin(startClamped, endClamped, usizeLLVMType, "arrayslice.start.bounded")
	sliceLen := C.LLVMBuildSub(s.builder, endClamped, boundedStart, cStringFree("arrayslice.len"))

	llvmArrayType, err := s.g.lowerType(arrayType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroIndex := C.LLVMConstInt(usizeLLVMType, 0, 0)
	indices := []C.LLVMValueRef{zeroIndex, boundedStart}
	dataPtr := C.LLVMBuildGEP2(s.builder, llvmArrayType, arrayPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("arrayslice.data"))

	viewLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	if _, ok := resultType.(*semantic.SViewType); ok {
		viewValue := C.LLVMGetUndef(viewLLVMType)
		viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, dataPtr, 0, cStringFree("strslice.view.data"))
		viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, sliceLen, 1, cStringFree("strslice.view.len"))
		return viewValue, resultType, true, nil
	}
	elemSize, err := s.sizeOfType(arrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	elemSizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(elemSize), 0)
	viewValue := C.LLVMGetUndef(viewLLVMType)
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, dataPtr, 0, cStringFree("arrayslice.view.data"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, sliceLen, 1, cStringFree("arrayslice.view.len"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, elemSizeValue, 2, cStringFree("arrayslice.view.elem_size"))
	return viewValue, resultType, true, nil
}

func (s *functionState) fixedArraySliceBase(object ast.Expr) (*semantic.ArrayType, C.LLVMValueRef, bool, error) {
	objectType := s.exprType(object)
	if arrayType, ok := objectType.(*semantic.ArrayType); ok {
		arrayPtr, _, err := s.emitAddressOrTemp(object)
		if err != nil {
			return nil, nil, true, err
		}
		return arrayType, arrayPtr, true, nil
	}
	refType, ok := objectType.(*semantic.RefType)
	if !ok || refType.State != semantic.RefStateNonNull {
		return nil, nil, false, nil
	}
	arrayType, ok := refType.Elem.(*semantic.ArrayType)
	if !ok {
		return nil, nil, false, nil
	}
	arrayPtr, _, err := s.emitExpr(object, objectType)
	if err != nil {
		return nil, nil, true, err
	}
	return arrayType, arrayPtr, true, nil
}

func (s *functionState) emitUnsignedMin(left C.LLVMValueRef, right C.LLVMValueRef, llvmType C.LLVMTypeRef, name string) C.LLVMValueRef {
	cmp := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULE), left, right, cStringFree(name+".cmp"))
	mask := C.LLVMBuildZExt(s.builder, cmp, llvmType, cStringFree(name+".mask"))
	negMask := C.LLVMBuildSub(s.builder, C.LLVMConstNull(llvmType), mask, cStringFree(name+".negmask"))
	diff := C.LLVMBuildXor(s.builder, left, right, cStringFree(name+".diff"))
	maskedDiff := C.LLVMBuildAnd(s.builder, diff, negMask, cStringFree(name+".masked"))
	return C.LLVMBuildXor(s.builder, right, maskedDiff, cStringFree(name))
}

func (s *functionState) emitRuntimeStringLenExpr(object ast.Expr, fieldType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	stringType, ok := dstrFieldOperandType(s.exprType(object))
	if !ok {
		return nil, nil, fmt.Errorf("string len requires dstr operand")
	}
	stringValue, _, err := s.emitExpr(object, stringType)
	if err != nil {
		return nil, nil, err
	}
	helperType := &semantic.FuncType{
		Name:   "ctx_strlen",
		Params: []semantic.Type{stringType},
		Return: fieldType,
	}
	callee, err := s.g.ensureFunctionDeclared("ctx_strlen", helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	args := []C.LLVMValueRef{stringValue}
	call := s.buildCall(llvmFnType, callee, args, "strlen")
	return call, fieldType, nil
}

func (s *functionState) emitIndexExpr(expr *ast.IndexExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr != nil && expr.Fallback != nil {
		return s.emitIndexFallbackExpr(expr)
	}
	if value, actualType, handled, err := s.emitPackedStoreIndexExpr(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitChunksExactIndexExpr(expr); handled {
		return value, actualType, err
	}
	if _, ok := semanticStringArrayType(s.exprType(expr.Object)); ok {
		return s.emitStaticStringIndexExpr(expr)
	}
	if helperName, operandType, ok := runtimeStringIndexedOperand(s.exprType(expr.Object)); ok {
		return s.emitRuntimeStringIndexExpr(expr, helperName, operandType)
	}
	ptr, elemType, err := s.emitIndexAddress(expr)
	if err != nil {
		return nil, nil, err
	}
	value, err := s.loadValue(ptr, elemType, "idx")
	return value, elemType, err
}

func (s *functionState) emitIndexFallbackExpr(expr *ast.IndexExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || expr.Fallback == nil {
		return nil, nil, fmt.Errorf("missing safe index fallback expression")
	}
	resultType := s.exprType(expr)
	if resultType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for safe index fallback result")
	}
	usizeType := s.g.result.NamedTypes["usize"]
	indexValue, _, err := s.emitExpr(expr.Index, usizeType)
	if err != nil {
		return nil, nil, err
	}
	countValue, loadValue, err := s.prepareSafeIndexFallback(expr, indexValue)
	if err != nil {
		return nil, nil, err
	}

	condValue := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), indexValue, countValue, cStringFree("safe.index.in.range"))
	inRangeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.index.in_range"))
	fallbackBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.index.fallback"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.index.end"))
	C.LLVMBuildCondBr(s.builder, condValue, inRangeBB, fallbackBB)

	C.LLVMPositionBuilderAtEnd(s.builder, inRangeBB)
	inRangeValue, actualType, err := loadValue()
	if err != nil {
		return nil, nil, err
	}
	if !semantic.SameType(actualType, resultType) {
		inRangeValue, err = s.coerceValue(inRangeValue, actualType, resultType)
		if err != nil {
			return nil, nil, err
		}
	}
	inRangeEnd := C.LLVMGetInsertBlock(s.builder)
	if C.LLVMGetBasicBlockTerminator(inRangeEnd) == nil {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, fallbackBB)
	fallbackValue, _, err := s.emitExpr(expr.Fallback, resultType)
	if err != nil {
		return nil, nil, err
	}
	fallbackEnd := C.LLVMGetInsertBlock(s.builder)
	if C.LLVMGetBasicBlockTerminator(fallbackEnd) == nil {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phiType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, phiType, cStringFree("safe.index.result"))
	values := []C.LLVMValueRef{inRangeValue, fallbackValue}
	blocks := []C.LLVMBasicBlockRef{inRangeEnd, fallbackEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, nil
}

func (s *functionState) prepareSafeIndexFallback(expr *ast.IndexExpr, indexValue C.LLVMValueRef) (C.LLVMValueRef, func() (C.LLVMValueRef, semantic.Type, error), error) {
	objectType := s.exprType(expr.Object)
	if objectType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for safe index operand")
	}
	if storeType, ok := packedStoreOperandType(objectType); ok {
		storeValue, _, err := s.emitPackedStoreValueFromExpr(expr.Object)
		if err != nil {
			return nil, nil, err
		}
		countValue, err := s.emitPackedStoreCountValue(storeValue, storeType, "safe.index.count")
		if err != nil {
			return nil, nil, err
		}
		ops := &packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}
		return countValue, func() (C.LLVMValueRef, semantic.Type, error) {
			return ops.storeValueAt(indexValue, "safe.index.value")
		}, nil
	}
	switch t := objectType.(type) {
	case *semantic.ArrayType:
		arrayPtr, _, err := s.emitAddressOrTemp(expr.Object)
		if err != nil {
			return nil, nil, err
		}
		countValue, err := s.safeIndexArrayCountValue(t)
		if err != nil {
			return nil, nil, err
		}
		return countValue, func() (C.LLVMValueRef, semantic.Type, error) {
			return s.loadArrayIndexValue(arrayPtr, t, indexValue, "safe.index.value")
		}, nil
	case *semantic.DArrayType:
		containerPtr, _, err := s.emitAddressOrTemp(expr.Object)
		if err != nil {
			return nil, nil, err
		}
		countValue, err := s.emitContainerCountValue(containerPtr, t, "safe.index.count")
		if err != nil {
			return nil, nil, err
		}
		return countValue, func() (C.LLVMValueRef, semantic.Type, error) {
			ptr, elemType, err := s.emitRuntimeIndexedAddress(containerPtr, t, t.Elem, indexValue)
			if err != nil {
				return nil, nil, err
			}
			value, err := s.loadValue(ptr, elemType, "safe.index.value")
			return value, elemType, err
		}, nil
	case *semantic.ViewType:
		containerPtr, _, err := s.emitAddressOrTemp(expr.Object)
		if err != nil {
			return nil, nil, err
		}
		countValue, err := s.emitContainerCountValue(containerPtr, t, "safe.index.count")
		if err != nil {
			return nil, nil, err
		}
		return countValue, func() (C.LLVMValueRef, semantic.Type, error) {
			ptr, elemType, err := s.emitRuntimeIndexedAddress(containerPtr, t, t.Elem, indexValue)
			if err != nil {
				return nil, nil, err
			}
			value, err := s.loadValue(ptr, elemType, "safe.index.value")
			return value, elemType, err
		}, nil
	case *semantic.DArrayViewType:
		containerPtr, _, err := s.emitAddressOrTemp(expr.Object)
		if err != nil {
			return nil, nil, err
		}
		countValue, err := s.emitContainerCountValue(containerPtr, t, "safe.index.count")
		if err != nil {
			return nil, nil, err
		}
		return countValue, func() (C.LLVMValueRef, semantic.Type, error) {
			ptr, elemType, err := s.emitRuntimeIndexedAddress(containerPtr, t, t.Elem, indexValue)
			if err != nil {
				return nil, nil, err
			}
			value, err := s.loadValue(ptr, elemType, "safe.index.value")
			return value, elemType, err
		}, nil
	case *semantic.RefType:
		switch elem := t.Elem.(type) {
		case *semantic.ArrayType:
			arrayPtr, _, err := s.emitExpr(expr.Object, objectType)
			if err != nil {
				return nil, nil, err
			}
			countValue, err := s.safeIndexArrayCountValue(elem)
			if err != nil {
				return nil, nil, err
			}
			return countValue, func() (C.LLVMValueRef, semantic.Type, error) {
				return s.loadArrayIndexValue(arrayPtr, elem, indexValue, "safe.index.value")
			}, nil
		case *semantic.DArrayType:
			containerPtr, _, err := s.emitExpr(expr.Object, objectType)
			if err != nil {
				return nil, nil, err
			}
			countValue, err := s.emitContainerCountValue(containerPtr, elem, "safe.index.count")
			if err != nil {
				return nil, nil, err
			}
			return countValue, func() (C.LLVMValueRef, semantic.Type, error) {
				ptr, elemType, err := s.emitRuntimeIndexedAddress(containerPtr, elem, elem.Elem, indexValue)
				if err != nil {
					return nil, nil, err
				}
				value, err := s.loadValue(ptr, elemType, "safe.index.value")
				return value, elemType, err
			}, nil
		case *semantic.ViewType:
			containerPtr, _, err := s.emitExpr(expr.Object, objectType)
			if err != nil {
				return nil, nil, err
			}
			countValue, err := s.emitContainerCountValue(containerPtr, elem, "safe.index.count")
			if err != nil {
				return nil, nil, err
			}
			return countValue, func() (C.LLVMValueRef, semantic.Type, error) {
				ptr, elemType, err := s.emitRuntimeIndexedAddress(containerPtr, elem, elem.Elem, indexValue)
				if err != nil {
					return nil, nil, err
				}
				value, err := s.loadValue(ptr, elemType, "safe.index.value")
				return value, elemType, err
			}, nil
		case *semantic.DArrayViewType:
			containerPtr, _, err := s.emitExpr(expr.Object, objectType)
			if err != nil {
				return nil, nil, err
			}
			countValue, err := s.emitContainerCountValue(containerPtr, elem, "safe.index.count")
			if err != nil {
				return nil, nil, err
			}
			return countValue, func() (C.LLVMValueRef, semantic.Type, error) {
				ptr, elemType, err := s.emitRuntimeIndexedAddress(containerPtr, elem, elem.Elem, indexValue)
				if err != nil {
					return nil, nil, err
				}
				value, err := s.loadValue(ptr, elemType, "safe.index.value")
				return value, elemType, err
			}, nil
		}
	}
	return nil, nil, fmt.Errorf("safe index fallback is not implemented for %s", objectType.String())
}

func (s *functionState) safeIndexArrayCountValue(arrayType *semantic.ArrayType) (C.LLVMValueRef, error) {
	if arrayType == nil || !arrayType.HasConstSize || arrayType.ConstSize < 0 {
		return nil, fmt.Errorf("safe index fallback requires a concrete array bound")
	}
	usizeLLVMType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	return C.LLVMConstInt(usizeLLVMType, C.ulonglong(arrayType.ConstSize), 0), nil
}

func (s *functionState) loadArrayIndexValue(arrayPtr C.LLVMValueRef, arrayType *semantic.ArrayType, indexValue C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	arrayLLVMType, err := s.g.lowerType(arrayType)
	if err != nil {
		return nil, nil, err
	}
	zero := C.LLVMConstInt(C.LLVMInt32TypeInContext(s.g.context), 0, 0)
	indices := []C.LLVMValueRef{zero, indexValue}
	ptr := C.LLVMBuildGEP2(s.builder, arrayLLVMType, arrayPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree(name+".ptr"))
	value, err := s.loadValue(ptr, arrayType.Elem, name)
	return value, arrayType.Elem, err
}

func (s *functionState) emitContainerCountValue(containerPtr C.LLVMValueRef, containerType semantic.Type, name string) (C.LLVMValueRef, error) {
	containerLLVMType, err := s.g.lowerType(containerType)
	if err != nil {
		return nil, err
	}
	countPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, containerPtr, 1, cStringFree(name+".ptr"))
	usizeType := s.g.result.NamedTypes["usize"]
	return s.loadValue(countPtr, usizeType, name)
}

func (s *functionState) emitChunksExactIndexExpr(expr *ast.IndexExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	chunkType, ok := semantic.ChunksExactViewItemType(s.exprType(expr.Object))
	if !ok || chunkType == nil {
		return nil, nil, false, nil
	}
	objectType, ok := s.exprType(expr.Object).(*semantic.GenericInstanceType)
	if !ok || objectType == nil {
		return nil, nil, true, fmt.Errorf("chunks_exact index expects a carrier value")
	}
	objectValue, _, err := s.emitExpr(expr.Object, objectType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	indexValue, _, err := s.emitExpr(expr.Index, usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	sourceValue := C.LLVMBuildExtractValue(s.builder, objectValue, 0, cStringFree("chunks.source"))
	chunkSizeValue := C.LLVMBuildExtractValue(s.builder, objectValue, 1, cStringFree("chunks.chunk_size"))
	startValue := C.LLVMBuildMul(s.builder, indexValue, chunkSizeValue, cStringFree("chunks.start"))
	endValue := C.LLVMBuildAdd(s.builder, startValue, chunkSizeValue, cStringFree("chunks.end"))
	value, err := s.emitArenaViewSliceValue(sourceValue, chunkType, startValue, endValue, "chunks.item")
	if err != nil {
		return nil, nil, true, err
	}
	return value, chunkType, true, nil
}

func (s *functionState) emitRuntimeStringIndexExpr(expr *ast.IndexExpr, helperName string, operandType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	stringValue, _, err := s.emitExpr(expr.Object, operandType)
	if err != nil {
		return nil, nil, err
	}
	indexType := s.g.result.NamedTypes["i64"]
	resultType := s.g.result.NamedTypes["char"]
	indexValue, _, err := s.emitExpr(expr.Index, indexType)
	if err != nil {
		return nil, nil, err
	}
	helperType := &semantic.FuncType{
		Name:   helperName,
		Params: []semantic.Type{operandType, indexType},
		Return: resultType,
	}
	callee, err := s.g.ensureFunctionDeclared(helperName, helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	args := []C.LLVMValueRef{stringValue, indexValue}
	call := s.buildCall(llvmFnType, callee, args, "stridx")
	return call, resultType, nil
}

func semanticStringArrayType(t semantic.Type) (*semantic.ArrayType, bool) {
	if arrayType, ok := t.(*semantic.ArrayType); ok {
		if arrayType.SurfaceName == "str" || arrayType.SurfaceName == "string" {
			return arrayType, true
		}
		return nil, false
	}
	ref, ok := t.(*semantic.RefType)
	if !ok || ref.State != semantic.RefStateNonNull {
		return nil, false
	}
	arrayType, ok := ref.Elem.(*semantic.ArrayType)
	if !ok || (arrayType.SurfaceName != "str" && arrayType.SurfaceName != "string") {
		return nil, false
	}
	return arrayType, true
}

func (s *functionState) emitStaticStringIndexExpr(expr *ast.IndexExpr) (C.LLVMValueRef, semantic.Type, error) {
	ptr, _, err := s.emitIndexAddress(expr)
	if err != nil {
		return nil, nil, err
	}
	byteType := s.g.result.NamedTypes["u8"]
	loaded, err := s.loadValue(ptr, byteType, "str.byte")
	if err != nil {
		return nil, nil, err
	}
	resultType := s.g.result.NamedTypes["char"]
	llvmResultType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	return C.LLVMBuildZExt(s.builder, loaded, llvmResultType, cStringFree("stridx.zext")), resultType, nil
}

func runtimeStringIndexedOperand(t semantic.Type) (string, semantic.Type, bool) {
	if _, ok := t.(*semantic.DStrType); ok {
		return "ctx_string_index", t, true
	}
	if _, ok := t.(*semantic.SViewType); ok {
		return "ctx_string_view_index", t, true
	}
	if st, ok := t.(*semantic.StructType); ok && st.Name == "StringView" {
		return "ctx_string_view_index", t, true
	}
	ref, ok := t.(*semantic.RefType)
	if !ok {
		return "", nil, false
	}
	if ref.State != semantic.RefStateNonNull {
		return "", nil, false
	}
	if _, ok := ref.Elem.(*semantic.DStrType); ok {
		return "ctx_string_index", ref.Elem, true
	}
	if _, ok := ref.Elem.(*semantic.SViewType); ok {
		return "ctx_string_view_index", ref.Elem, true
	}
	if st, ok := ref.Elem.(*semantic.StructType); ok && st.Name == "StringView" {
		return "ctx_string_view_index", ref.Elem, true
	}
	return "", nil, false
}

func dstrSyntheticFieldType(t semantic.Type, fieldName string) (semantic.Type, bool) {
	if fieldName != "len" {
		return nil, false
	}
	if _, ok := dstrFieldOperandType(t); !ok {
		return nil, false
	}
	return &semantic.BuiltinType{Name: "i64"}, true
}

func dstrFieldOperandType(t semantic.Type) (semantic.Type, bool) {
	if _, ok := t.(*semantic.DStrType); ok {
		return t, true
	}
	ref, ok := t.(*semantic.RefType)
	if !ok {
		return nil, false
	}
	if ref.State != semantic.RefStateNonNull {
		return nil, false
	}
	if _, ok := ref.Elem.(*semantic.DStrType); ok {
		return ref.Elem, true
	}
	return nil, false
}

type runtimeSliceInfo struct {
	helperName  string
	operandType semantic.Type
	resultType  semantic.Type
	indexType   semantic.Type
	useAddress  bool
}

func runtimeSliceOperandInfo(objectType semantic.Type, resultType semantic.Type) (runtimeSliceInfo, bool) {
	i64Type := &semantic.BuiltinType{Name: "i64"}
	usizeType := &semantic.BuiltinType{Name: "usize"}
	if view, ok := objectType.(*semantic.DArrayType); ok {
		return runtimeSliceInfo{
			helperName:  "arena_da_view",
			operandType: &semantic.RefType{Elem: objectType, State: semantic.RefStateNonNull},
			resultType:  &semantic.DArrayViewType{Elem: view.Elem},
			indexType:   usizeType,
			useAddress:  true,
		}, true
	}
	if view, ok := objectType.(*semantic.ViewType); ok {
		return runtimeSliceInfo{
			helperName:  "arena_da_view_slice",
			operandType: objectType,
			resultType:  &semantic.ViewType{Elem: view.Elem},
			indexType:   usizeType,
		}, true
	}
	if view, ok := objectType.(*semantic.DArrayViewType); ok {
		return runtimeSliceInfo{
			helperName:  "arena_da_view_slice",
			operandType: objectType,
			resultType:  &semantic.DArrayViewType{Elem: view.Elem},
			indexType:   usizeType,
		}, true
	}
	if _, ok := objectType.(*semantic.DStrType); ok {
		return runtimeSliceInfo{helperName: "ctx_string_view", operandType: objectType, resultType: resultType, indexType: i64Type}, true
	}
	if _, ok := objectType.(*semantic.SViewType); ok {
		return runtimeSliceInfo{helperName: "ctx_string_view_slice", operandType: objectType, resultType: resultType, indexType: i64Type}, true
	}
	if st, ok := objectType.(*semantic.StructType); ok && st.Name == "StringView" {
		return runtimeSliceInfo{helperName: "ctx_string_view_slice", operandType: objectType, resultType: resultType, indexType: i64Type}, true
	}
	ref, ok := objectType.(*semantic.RefType)
	if !ok || ref.State != semantic.RefStateNonNull {
		return runtimeSliceInfo{}, false
	}
	if view, ok := ref.Elem.(*semantic.DArrayType); ok {
		return runtimeSliceInfo{
			helperName:  "arena_da_view",
			operandType: objectType,
			resultType:  &semantic.DArrayViewType{Elem: view.Elem},
			indexType:   usizeType,
		}, true
	}
	if view, ok := ref.Elem.(*semantic.ViewType); ok {
		return runtimeSliceInfo{helperName: "arena_da_view_slice", operandType: ref.Elem, resultType: &semantic.ViewType{Elem: view.Elem}, indexType: usizeType}, true
	}
	if _, ok := ref.Elem.(*semantic.DStrType); ok {
		return runtimeSliceInfo{helperName: "ctx_string_view", operandType: ref.Elem, resultType: resultType, indexType: i64Type}, true
	}
	if _, ok := ref.Elem.(*semantic.SViewType); ok {
		return runtimeSliceInfo{helperName: "ctx_string_view_slice", operandType: ref.Elem, resultType: resultType, indexType: i64Type}, true
	}
	if st, ok := ref.Elem.(*semantic.StructType); ok && st.Name == "StringView" {
		return runtimeSliceInfo{helperName: "ctx_string_view_slice", operandType: ref.Elem, resultType: resultType, indexType: i64Type}, true
	}
	return runtimeSliceInfo{}, false
}

func runtimeStringCompareInfo(leftType semantic.Type, rightType semantic.Type) (string, semantic.Type, semantic.Type, bool, bool) {
	leftKind := classifyRuntimeStringCompareKind(leftType)
	rightKind := classifyRuntimeStringCompareKind(rightType)
	if leftKind == runtimeStringCompareNone || rightKind == runtimeStringCompareNone {
		return "", nil, nil, false, false
	}
	if leftKind == runtimeStringCompareRaw && rightKind == runtimeStringCompareRaw {
		return "", nil, nil, false, false
	}
	if leftKind == runtimeStringCompareView && rightKind == runtimeStringCompareView {
		return "ctx_string_views_eq", leftType, rightType, false, true
	}
	if leftKind == runtimeStringCompareView {
		return "ctx_string_view_eq", leftType, rightType, false, true
	}
	if rightKind == runtimeStringCompareView {
		return "ctx_string_view_eq", rightType, leftType, true, true
	}
	return "ctx_streq", leftType, rightType, false, true
}

type runtimeStringCompareKind int

const (
	runtimeStringCompareNone runtimeStringCompareKind = iota
	runtimeStringCompareDStr
	runtimeStringCompareView
	runtimeStringCompareRaw
)

func classifyRuntimeStringCompareKind(t semantic.Type) runtimeStringCompareKind {
	if t == nil {
		return runtimeStringCompareNone
	}
	if _, ok := t.(*semantic.DStrType); ok {
		return runtimeStringCompareDStr
	}
	if _, ok := t.(*semantic.SViewType); ok {
		return runtimeStringCompareView
	}
	if st, ok := t.(*semantic.StructType); ok && st.Name == "StringView" {
		return runtimeStringCompareView
	}
	ref, ok := t.(*semantic.RefType)
	if !ok {
		return runtimeStringCompareNone
	}
	if builtin, ok := ref.Elem.(*semantic.BuiltinType); ok && builtin.Name == "u8" {
		return runtimeStringCompareRaw
	}
	if _, ok := ref.Elem.(*semantic.SViewType); ok {
		return runtimeStringCompareView
	}
	return runtimeStringCompareNone
}
func (s *functionState) emitCastExpr(expr *ast.CastExpr) (C.LLVMValueRef, semantic.Type, error) {
	if s != nil && s.g != nil && s.g.result != nil && s.g.result.CastHooks != nil {
		if sym, ok := s.g.result.CastHooks[expr]; ok && sym != nil {
			fnType, ok := sym.Type.(*semantic.FuncType)
			if !ok || fnType == nil || len(fnType.Params) != 1 {
				return nil, nil, fmt.Errorf("invalid semantic cast hook for %T", expr)
			}
			arg, _, err := s.emitExpr(expr.Operand, fnType.Params[0])
			if err != nil {
				return nil, nil, err
			}
			callee, err := s.g.ensureFunctionDeclared(sym.Name, fnType)
			if err != nil {
				return nil, nil, err
			}
			llvmFnType, err := s.g.lowerFunctionType(fnType)
			if err != nil {
				return nil, nil, err
			}
			call := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arg}, "casthook")
			return call, fnType.Return, nil
		}
	}
	targetType := s.exprType(expr)
	if targetType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for cast target")
	}
	operandExpected := semantic.Type(nil)
	if _, ok := expr.Operand.(*ast.ZeroedLit); ok {
		operandExpected = targetType
	}
	value, actualType, err := s.emitExpr(expr.Operand, operandExpected)
	if err != nil {
		return nil, nil, err
	}
	coerced, err := s.coerceValue(value, actualType, targetType)
	if err != nil {
		return nil, nil, err
	}
	return coerced, targetType, nil
}

func (s *functionState) emitSizeofExpr(expr *ast.SizeofExpr) (C.LLVMValueRef, semantic.Type, error) {
	t, err := s.resolveTypeExpr(expr.Type)
	if err != nil {
		return nil, nil, err
	}
	size, err := s.sizeOfType(t)
	if err != nil {
		return nil, nil, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, err
	}
	return C.LLVMConstInt(usizeType, C.ulonglong(size), 0), s.g.result.NamedTypes["usize"], nil
}

func (s *functionState) emitTernaryExpr(expr *ast.TernaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	resultType := s.exprType(expr)
	condValue, _, err := s.emitExpr(expr.Cond, s.g.result.NamedTypes["bool"])
	if err != nil {
		return nil, nil, err
	}
	parentBlock := C.LLVMGetInsertBlock(s.builder)
	thenBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("ternary.then"))
	elseBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("ternary.else"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("ternary.end"))
	C.LLVMBuildCondBr(s.builder, condValue, thenBB, elseBB)

	C.LLVMPositionBuilderAtEnd(s.builder, thenBB)
	leftValue, _, err := s.emitExpr(expr.Value, resultType)
	if err != nil {
		return nil, nil, err
	}
	thenEnd := C.LLVMGetInsertBlock(s.builder)
	if C.LLVMGetBasicBlockTerminator(thenEnd) == nil {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, elseBB)
	rightValue, _, err := s.emitExpr(expr.Alt, resultType)
	if err != nil {
		return nil, nil, err
	}
	elseEnd := C.LLVMGetInsertBlock(s.builder)
	if C.LLVMGetBasicBlockTerminator(elseEnd) == nil {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phiType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, phiType, cStringFree("termp"))
	values := []C.LLVMValueRef{leftValue, rightValue}
	blocks := []C.LLVMBasicBlockRef{thenEnd, elseEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	_ = parentBlock
	return phi, resultType, nil
}

func (s *functionState) emitAddrOfExpr(expr *ast.AddrOfExpr) (C.LLVMValueRef, semantic.Type, error) {
	ptr, operandType, err := s.emitAddress(expr.Operand)
	if err != nil {
		return nil, nil, err
	}
	return ptr, &semantic.RefType{Elem: operandType, State: semantic.RefStateNonNull}, nil
}

func (s *functionState) emitSpecializeExpr(expr *ast.SpecializeExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || expr.Operand == nil {
		return nil, nil, fmt.Errorf("missing specialization operand")
	}
	ident, ok := expr.Operand.(*ast.Ident)
	if !ok {
		return nil, nil, fmt.Errorf("specialize expects a named generic function")
	}
	sym, ok := s.g.result.GlobalScope.Lookup(ident.Name)
	if !ok {
		return nil, nil, fmt.Errorf("unknown generic function %q during LLVM lowering", ident.Name)
	}
	baseType, ok := sym.Type.(*semantic.FuncType)
	if !ok {
		return nil, nil, fmt.Errorf("specialize expects a function, got %s", sym.Type.String())
	}
	params := funcGenericParams(baseType)
	if len(params) == 0 {
		return nil, nil, fmt.Errorf("function %q is not generic", ident.Name)
	}
	if len(expr.TypeArgs) != len(params) {
		return nil, nil, fmt.Errorf("function %q expects %d arguments, got %d", ident.Name, len(params), len(expr.TypeArgs))
	}
	bindings := make(map[string]semantic.Type, len(params))
	for i, arg := range expr.TypeArgs {
		resolved, err := s.resolveTypeExpr(arg)
		if err != nil {
			return nil, nil, err
		}
		bindings[params[i].Name] = resolved
	}
	specialized := specializeFuncType(baseType, bindings, s.g.result.StaticImpls)
	if decl, ok := sym.Node.(*ast.FuncDecl); ok {
		value, lowered, err := s.g.ensureSpecializedFunction(decl, baseType, bindings)
		return value, lowered, err
	}
	value, err := s.g.ensureFunctionDeclared(ident.Name, specialized)
	return value, specialized, err
}

func (s *functionState) emitStructLitExpr(expr *ast.StructLitExpr) (C.LLVMValueRef, semantic.Type, error) {
	if s != nil && s.g != nil && s.g.result != nil && s.g.result.InitCalls != nil {
		if call, ok := s.g.result.InitCalls[expr]; ok && call != nil {
			return s.emitExpr(call, s.exprType(expr))
		}
	}
	structType := s.exprType(expr)
	llvmType, err := s.g.lowerType(structType)
	if err != nil {
		return nil, nil, err
	}
	fields, err := s.g.structLiteralFields(structType)
	if err != nil {
		return nil, nil, err
	}
	value := C.LLVMGetUndef(llvmType)
	args := expr.LoweredArgs()
	for i, arg := range args {
		if i >= len(fields) {
			break
		}
		if arg == nil {
			return nil, nil, fmt.Errorf("struct literal field %d was not resolved", i)
		}
		fieldValue, _, err := s.emitExpr(arg, fields[i].Type)
		if err != nil {
			return nil, nil, err
		}
		value = C.LLVMBuildInsertValue(s.builder, value, fieldValue, C.unsigned(i), cStringFree("ins"))
	}
	return value, structType, nil
}

func (s *functionState) emitRecordUpdateExpr(expr *ast.RecordUpdateExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || expr.Base == nil {
		return nil, nil, fmt.Errorf("invalid record update")
	}
	memberType := semantic.StripAggregateStateType(s.exprType(expr.Base))
	if exactType, ok := s.rewriteDefaultExactMemberType(expr.Base); ok {
		memberType = exactType
	}
	if _, exact := semantic.TreeExactTag(memberType); exact {
		value, _, err := s.emitTreeExactMemberUpdateExpr(expr, memberType, nil)
		return value, s.exprType(expr), err
	}
	baseValue, baseType, err := s.emitExpr(expr.Base, nil)
	if err != nil {
		return nil, nil, err
	}
	fields, err := s.g.structLiteralFields(baseType)
	if err != nil {
		return nil, nil, err
	}
	value := baseValue
	args := expr.LoweredArgs()
	for i, field := range fields {
		if i >= len(args) || args[i] == nil {
			continue
		}
		fieldValue, _, err := s.emitExpr(args[i], field.Type)
		if err != nil {
			return nil, nil, err
		}
		value = C.LLVMBuildInsertValue(s.builder, value, fieldValue, C.unsigned(field.Index), cStringFree("record.update"))
	}
	return value, baseType, nil
}

func (s *functionState) rewriteDefaultExactMemberType(expr ast.Expr) (semantic.Type, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n.Name != "default" || s.treeRewriteDefault == nil || s.treeRewriteDefault.memberType == nil {
			return nil, false
		}
		return semantic.StripAggregateStateType(s.treeRewriteDefault.memberType), true
	case *ast.ParenExpr:
		return s.rewriteDefaultExactMemberType(n.Inner)
	default:
		return nil, false
	}
}

func (s *functionState) emitTreeExactMemberUpdateExpr(expr *ast.RecordUpdateExpr, memberType semantic.Type, owner *treeAllocOwnerBinding) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || expr.Base == nil {
		return nil, nil, fmt.Errorf("invalid tree update")
	}
	memberType = semantic.StripAggregateStateType(memberType)
	family := treeExactMemberFamily(memberType)
	if family == nil {
		return nil, nil, fmt.Errorf("missing tree family metadata for update %s", memberType.String())
	}
	tag, ok := treeExactMemberTag(memberType)
	if !ok {
		return nil, nil, fmt.Errorf("missing exact tree member tag for update %s", memberType.String())
	}
	handleValue, baseType, err := s.emitTreeHandleValue(expr.Base, s.exprType(expr.Base))
	if err != nil {
		return nil, nil, err
	}
	if handleValue == nil || baseType == nil {
		return nil, nil, fmt.Errorf("tree update requires an exact tree member base")
	}
	if resolvedBaseType := semantic.StripAggregateStateType(baseType); resolvedBaseType != nil {
		if _, exact := semantic.TreeExactTag(resolvedBaseType); exact {
			memberType = resolvedBaseType
		}
	}
	resolvedOwner := treeAllocOwnerBinding{}
	if owner != nil {
		resolvedOwner = *owner
	} else {
		activeOwner, ok := s.lookupTreeAllocOwner()
		if !ok {
			return nil, nil, fmt.Errorf("tree update of %s requires an active in <owner>: scope or explicit new[owner]", memberType.String())
		}
		resolvedOwner = activeOwner
	}
	fieldDecls := treeExactFieldDecls(memberType)
	orderedArgs := expr.LoweredArgs()
	if len(orderedArgs) == 0 {
		orderedArgs = make([]ast.Expr, len(fieldDecls))
	}
	storeValue, _, err := s.ensureTreeOwnerStoreValue(resolvedOwner, family)
	if err != nil {
		return nil, nil, err
	}
	sourceStateValue := s.emitTreeHandleStateValue(handleValue, "tree.update.src.state")
	sourceRowIndex, err := s.emitTreeHandleIndexValue(handleValue, "tree.update.src.index")
	if err != nil {
		return nil, nil, err
	}
	sourceTablePtr, err := s.emitTreeStateTablePtr(sourceStateValue, family, memberType, "tree.update.src")
	if err != nil {
		return nil, nil, err
	}
	arenaValue := s.emitTreeStoreArenaValueNamed(storeValue, "tree.update.store.arena")
	stateValue := s.emitTreeStoreStateValueNamed(storeValue, "tree.update.store.state")
	tablePtr, err := s.emitTreeStateTablePtr(stateValue, family, memberType, "tree.update")
	if err != nil {
		return nil, nil, err
	}
	rowIndex, err := s.emitTreeTableCountValue(tablePtr, memberType, "tree.update")
	if err != nil {
		return nil, nil, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, err
	}
	neededCount := C.LLVMBuildAdd(s.builder, rowIndex, C.LLVMConstInt(usizeType, 1, 0), cStringFree("tree.update.needed"))
	if err := s.emitTreeEnsureTableCapacity(arenaValue, tablePtr, memberType, neededCount, "tree.update"); err != nil {
		return nil, nil, err
	}
	for i, fieldDecl := range fieldDecls {
		field, ok := treeExactFieldInfo(memberType, fieldDecl.Name)
		if !ok {
			return nil, nil, fmt.Errorf("missing exact tree field %s.%s", memberType.String(), fieldDecl.Name)
		}
		var fieldValue C.LLVMValueRef
		if i < len(orderedArgs) && orderedArgs[i] != nil {
			fieldValue, _, err = s.emitExpr(orderedArgs[i], field.Type)
		} else {
			fieldValue, _, err = s.emitTreeExactFieldValueAtIndex(sourceTablePtr, memberType, fieldDecl.Name, sourceRowIndex, "tree.update.src")
		}
		if err != nil {
			return nil, nil, err
		}
		if err := s.emitTreeStoreExactFieldValueAtIndex(tablePtr, memberType, fieldDecl.Name, rowIndex, fieldValue, "tree.update"); err != nil {
			return nil, nil, err
		}
	}
	if err := s.emitTreeTableSetCount(tablePtr, memberType, neededCount, "tree.update"); err != nil {
		return nil, nil, err
	}
	keyValue, err := s.buildTreeHandleKey(tag, rowIndex, "tree.update")
	if err != nil {
		return nil, nil, err
	}
	handleValue, err = s.buildTreeHandleValue(family, stateValue, keyValue, "tree.update")
	if err != nil {
		return nil, nil, err
	}
	return handleValue, memberType, nil
}

func (s *functionState) emitTreeRewriteDefaultValue(ctx *treeRewriteDefaultContext, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if ctx == nil || ctx.memberType == nil {
		return nil, nil, fmt.Errorf("default is only available while lowering a rewrite arm")
	}
	if resultType == nil {
		resultType = semantic.TreeRewriteResultTypeForValue(ctx.memberType)
	}
	memberType := semantic.StripAggregateStateType(ctx.memberType)
	family := treeExactMemberFamily(memberType)
	if family == nil {
		return nil, nil, fmt.Errorf("rewrite default requires an exact tree member")
	}
	tag, ok := treeExactMemberTag(memberType)
	if !ok {
		return nil, nil, fmt.Errorf("rewrite default is missing an exact tree tag for %s", memberType.String())
	}
	owner, ok := s.lookupTreeAllocOwner()
	if !ok {
		return nil, nil, fmt.Errorf("default requires an active in <owner>: scope")
	}
	sourceStateValue := s.emitTreeHandleStateValue(ctx.nodeValue, "tree.default.src.state")
	sourceRowIndex, err := s.emitTreeHandleIndexValue(ctx.nodeValue, "tree.default.src.index")
	if err != nil {
		return nil, nil, err
	}
	sourceTablePtr, err := s.emitTreeStateTablePtr(sourceStateValue, family, memberType, "tree.default.src")
	if err != nil {
		return nil, nil, err
	}
	storeValue, _, err := s.ensureTreeOwnerStoreValue(owner, family)
	if err != nil {
		return nil, nil, err
	}
	arenaValue := s.emitTreeStoreArenaValueNamed(storeValue, "tree.default.store.arena")
	stateValue := s.emitTreeStoreStateValueNamed(storeValue, "tree.default.store.state")
	tablePtr, err := s.emitTreeStateTablePtr(stateValue, family, memberType, "tree.default")
	if err != nil {
		return nil, nil, err
	}
	rowIndex, err := s.emitTreeTableCountValue(tablePtr, memberType, "tree.default")
	if err != nil {
		return nil, nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, err
	}
	zeroValue := C.LLVMConstInt(usizeLLVMType, 0, 0)
	oneValue := C.LLVMConstInt(usizeLLVMType, 1, 0)
	neededCount := C.LLVMBuildAdd(s.builder, rowIndex, oneValue, cStringFree("tree.default.needed"))
	if err := s.emitTreeEnsureTableCapacity(arenaValue, tablePtr, memberType, neededCount, "tree.default"); err != nil {
		return nil, nil, err
	}
	offsetValue := zeroValue
	for _, fieldDecl := range treeExactFieldDecls(memberType) {
		field, ok := treeExactFieldInfo(memberType, fieldDecl.Name)
		if !ok {
			return nil, nil, fmt.Errorf("missing exact tree field %s.%s", memberType.String(), fieldDecl.Name)
		}
		sourceFieldValue, _, err := s.emitTreeExactFieldValueAtIndex(sourceTablePtr, memberType, fieldDecl.Name, sourceRowIndex, "tree.default.src")
		if err != nil {
			return nil, nil, err
		}
		fieldValue := sourceFieldValue
		relation := semantic.TreeFieldStructuralRelation(family, field.Type)
		switch relation {
		case ast.EnumPayloadRelationChild:
			bindingType, ok := semantic.TreeRewriteChildBindingType(field.Type, relation)
			if !ok {
				return nil, nil, fmt.Errorf("rewrite default could not determine child result type for %s.%s", memberType.String(), fieldDecl.Name)
			}
			childResultType := bindingType
			if optionalBinding, ok := bindingType.(*semantic.OptionalType); ok {
				childResultType = optionalBinding.Value
			}
			if optionalFieldType, ok := field.Type.(*semantic.OptionalType); ok {
				presentValue, err := s.extractOptionalPresent(sourceFieldValue, optionalFieldType)
				if err != nil {
					return nil, nil, err
				}
				childCount := C.LLVMBuildSelect(s.builder, presentValue, oneValue, zeroValue, cStringFree("tree.default.child.count"))
				payloadLLVMType, err := s.g.lowerType(optionalFieldType.Value)
				if err != nil {
					return nil, nil, err
				}
				presentBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("tree.default.child.some"))
				absentBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("tree.default.child.none"))
				contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("tree.default.child.cont"))
				C.LLVMBuildCondBr(s.builder, presentValue, presentBB, absentBB)

				C.LLVMPositionBuilderAtEnd(s.builder, presentBB)
				presentPayload, err := s.emitTreeFoldChildResultAtIndex(ctx.childViewValue, childResultType, offsetValue, "tree.default.child")
				if err != nil {
					return nil, nil, err
				}
				presentEnd := C.LLVMGetInsertBlock(s.builder)
				C.LLVMBuildBr(s.builder, contBB)

				C.LLVMPositionBuilderAtEnd(s.builder, absentBB)
				absentPayload, err := s.zeroValue(optionalFieldType.Value)
				if err != nil {
					return nil, nil, err
				}
				absentEnd := C.LLVMGetInsertBlock(s.builder)
				C.LLVMBuildBr(s.builder, contBB)

				C.LLVMPositionBuilderAtEnd(s.builder, contBB)
				payloadPhi := C.LLVMBuildPhi(s.builder, payloadLLVMType, cStringFree("tree.default.child.payload"))
				C.LLVMAddIncoming(payloadPhi, llvmValueSlicePtr([]C.LLVMValueRef{presentPayload, absentPayload}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{presentEnd, absentEnd}), 2)
				fieldValue, err = s.buildOptionalValue(optionalFieldType, presentValue, payloadPhi)
				if err != nil {
					return nil, nil, err
				}
				offsetValue = C.LLVMBuildAdd(s.builder, offsetValue, childCount, cStringFree("tree.default.child.offset.next"))
			} else {
				fieldValue, err = s.emitTreeFoldChildResultAtIndex(ctx.childViewValue, childResultType, offsetValue, "tree.default.child")
				if err != nil {
					return nil, nil, err
				}
				offsetValue = C.LLVMBuildAdd(s.builder, offsetValue, oneValue, cStringFree("tree.default.child.offset.next"))
			}
		case ast.EnumPayloadRelationChildren:
			bindingType, ok := semantic.TreeRewriteChildBindingType(field.Type, relation)
			if !ok {
				return nil, nil, fmt.Errorf("rewrite default could not determine children result type for %s.%s", memberType.String(), fieldDecl.Name)
			}
			childElemType := field.Type
			if optionalBinding, ok := bindingType.(*semantic.OptionalType); ok {
				if viewType, ok := optionalBinding.Value.(*semantic.DArrayViewType); ok {
					childElemType = viewType.Elem
				}
			} else if viewType, ok := bindingType.(*semantic.DArrayViewType); ok {
				childElemType = viewType.Elem
			}
			countValue, err := s.emitTreeStructuralSequenceCount(sourceFieldValue, field.Type, "tree.default.children.count")
			if err != nil {
				return nil, nil, err
			}
			subViewValue, subViewType, err := s.emitTreeFoldChildResultsSubview(ctx.childViewValue, childElemType, offsetValue, countValue, "tree.default.children")
			if err != nil {
				return nil, nil, err
			}
			fieldValue, err = s.coerceTreeRewriteSequenceFieldValue(subViewValue, subViewType, field.Type, owner, "tree.default.children")
			if err != nil {
				return nil, nil, err
			}
			offsetValue = C.LLVMBuildAdd(s.builder, offsetValue, countValue, cStringFree("tree.default.children.offset.next"))
		}
		if err := s.emitTreeStoreExactFieldValueAtIndex(tablePtr, memberType, fieldDecl.Name, rowIndex, fieldValue, "tree.default"); err != nil {
			return nil, nil, err
		}
	}
	if err := s.emitTreeTableSetCount(tablePtr, memberType, neededCount, "tree.default"); err != nil {
		return nil, nil, err
	}
	keyValue, err := s.buildTreeHandleKey(tag, rowIndex, "tree.default")
	if err != nil {
		return nil, nil, err
	}
	handleValue, err := s.buildTreeHandleValue(family, stateValue, keyValue, "tree.default")
	if err != nil {
		return nil, nil, err
	}
	return handleValue, resultType, nil
}

func (s *functionState) emitTreeRewriteDefaultExpr(expr *ast.Ident) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil {
		return nil, nil, fmt.Errorf("invalid rewrite default expression")
	}
	return s.emitTreeRewriteDefaultValue(s.treeRewriteDefault, s.exprType(expr))
}

func (s *functionState) coerceTreeRewriteSequenceFieldValue(viewValue C.LLVMValueRef, viewType semantic.Type, targetType semantic.Type, owner treeAllocOwnerBinding, name string) (C.LLVMValueRef, error) {
	if optionalType, ok := targetType.(*semantic.OptionalType); ok {
		payloadValue, err := s.coerceTreeRewriteSequenceFieldValue(viewValue, viewType, optionalType.Value, owner, name+".payload")
		if err != nil {
			return nil, err
		}
		presentValue := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0)
		return s.buildOptionalValue(optionalType, presentValue, payloadValue)
	}
	view, ok := viewType.(*semantic.DArrayViewType)
	if !ok || view == nil {
		return nil, fmt.Errorf("rewrite default expected dview child results, got %s", viewType.String())
	}
	switch tt := targetType.(type) {
	case *semantic.DArrayViewType:
		return viewValue, nil
	case *semantic.DArrayType:
		return s.materializeTreeOwnerDArrayFromView(viewValue, view, tt, owner, name)
	default:
		return nil, fmt.Errorf("rewrite default does not know how to rebuild sequence field %s from %s", targetType.String(), viewType.String())
	}
}

func (s *functionState) emitTreeOwnerAllocBytes(owner treeAllocOwnerBinding, byteCount C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	usizeType := s.g.result.NamedTypes["usize"]
	if !owner.isPerm {
		if owner.arenaRef == nil && owner.storeValue != nil && owner.storeType != nil {
			arenaRef, err := s.emitTreeStoreArenaValue(owner.storeValue, owner.storeType)
			if err != nil {
				return nil, err
			}
			owner.arenaRef = arenaRef
		}
		if owner.arenaRef == nil {
			return nil, fmt.Errorf("missing Arena owner for tree rewrite default materialization")
		}
		arenaType := s.g.result.NamedTypes["Arena"]
		arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		voidType := s.g.result.NamedTypes["void"]
		voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
			return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
		})
		allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
		if err != nil {
			return nil, err
		}
		allocLLVMType, err := s.g.lowerFunctionType(allocType)
		if err != nil {
			return nil, err
		}
		return s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{owner.arenaRef, byteCount}, name+".alloc"), nil
	}
	i64Type := s.g.result.NamedTypes["i64"]
	sizeValue, err := s.coerceValue(byteCount, usizeType, i64Type)
	if err != nil {
		return nil, err
	}
	heapVoidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageHeap, ExplicitStorage: true}
	allocType := s.g.cachedRuntimeHelperType("alloc_perm", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "alloc_perm", Params: []semantic.Type{i64Type}, Return: heapVoidRefType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("alloc_perm", allocType)
	if err != nil {
		return nil, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, err
	}
	return s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{sizeValue}, name+".alloc"), nil
}

func (s *functionState) materializeTreeOwnerDArrayFromView(viewValue C.LLVMValueRef, viewType *semantic.DArrayViewType, resultType *semantic.DArrayType, owner treeAllocOwnerBinding, name string) (C.LLVMValueRef, error) {
	if viewType == nil || resultType == nil {
		return nil, fmt.Errorf("missing dview materialization metadata")
	}
	llvmResultType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, err
	}
	zeroResult, err := s.zeroValue(resultType)
	if err != nil {
		return nil, err
	}
	viewData := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree(name+".src.data"))
	viewLen := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree(name+".src.len"))
	viewElemSize := C.LLVMBuildExtractValue(s.builder, viewValue, 2, cStringFree(name+".src.elem_size"))
	byteCount := C.LLVMBuildMul(s.builder, viewLen, viewElemSize, cStringFree(name+".bytes"))
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	zeroBytes := C.LLVMConstInt(usizeLLVMType, 0, 0)
	zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), byteCount, zeroBytes, cStringFree(name+".bytes.zero"))
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	allocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".alloc"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".merge"))
	C.LLVMBuildCondBr(s.builder, zeroCond, mergeBB, allocBB)

	C.LLVMPositionBuilderAtEnd(s.builder, allocBB)
	allocPtr, err := s.emitTreeOwnerAllocBytes(owner, byteCount, name)
	if err != nil {
		return nil, err
	}
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	memcpyType := s.g.cachedRuntimeHelperType("arena_memcpy", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_memcpy", Params: []semantic.Type{voidRefType, voidRefType, usizeType}, Return: voidRefType}
	})
	memcpyCallee, err := s.g.ensureFunctionDeclared("arena_memcpy", memcpyType)
	if err != nil {
		return nil, err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return nil, err
	}
	memcpyCall := s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{allocPtr, viewData, byteCount}, name+".memcpy")
	s.addCallSiteEnumAttribute(memcpyCall, C.uint(1), "noalias")
	s.addCallSiteEnumAttribute(memcpyCall, C.uint(2), "noalias")
	materialized := C.LLVMGetUndef(llvmResultType)
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, allocPtr, 0, cStringFree(name+".items"))
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, viewLen, 1, cStringFree(name+".count"))
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, viewLen, 2, cStringFree(name+".capacity"))
	allocEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phi := C.LLVMBuildPhi(s.builder, llvmResultType, cStringFree(name+".result"))
	values := []C.LLVMValueRef{zeroResult, materialized}
	blocks := []C.LLVMBasicBlockRef{entryBlock, allocEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, nil
}

func (s *functionState) emitTupleExpr(expr *ast.TupleExpr) (C.LLVMValueRef, semantic.Type, error) {
	tupleType, ok := semantic.StripAggregateStateType(s.exprType(expr)).(*semantic.TupleType)
	if !ok || tupleType == nil {
		return nil, nil, fmt.Errorf("tuple expression requires a tuple type, got %s", s.exprType(expr))
	}
	llvmType, err := s.g.lowerType(tupleType)
	if err != nil {
		return nil, nil, err
	}
	value := C.LLVMGetUndef(llvmType)
	for i, elem := range expr.Elems {
		if i >= len(tupleType.Fields) {
			break
		}
		elemValue, _, err := s.emitExpr(elem, tupleType.Fields[i].Type)
		if err != nil {
			return nil, nil, err
		}
		value = C.LLVMBuildInsertValue(s.builder, value, elemValue, C.unsigned(i), cStringFree("tuple.ins"))
	}
	return value, tupleType, nil
}

func (s *functionState) enumConstructorInfo(expr *ast.CallExpr) (*semantic.EnumType, *semantic.EnumVariant, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, nil, false
	}
	return s.enumConstructorInfoFromField(fieldExpr)
}

func (s *functionState) enumConstructorInfoFromField(expr *ast.FieldExpr) (*semantic.EnumType, *semantic.EnumVariant, bool) {
	ownerName, variantName, ok := qualifiedFieldOwnerAndLeaf(expr)
	if !ok {
		return nil, nil, false
	}
	base, ok := s.g.result.NamedTypes[ownerName]
	if !ok {
		return nil, nil, false
	}
	enumType, ok := base.(*semantic.EnumType)
	if !ok {
		return nil, nil, false
	}
	variant, ok := enumType.Variant(variantName)
	if !ok {
		return enumType, nil, true
	}
	return enumType, variant, true
}

func (s *functionState) treeConstructorInfo(expr *ast.CallExpr) (*semantic.TreeCategoryType, *semantic.EnumVariant, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, nil, false
	}
	return s.treeConstructorInfoFromField(fieldExpr)
}

func (s *functionState) treeExactMemberConstructorInfoFromField(expr *ast.FieldExpr) (semantic.Type, bool) {
	if expr == nil {
		return nil, false
	}
	base, ok := s.treeTypeForExpr(expr.Object)
	if !ok || base == nil {
		return nil, false
	}
	memberType, ok := base.Member(expr.Field)
	if !ok {
		return nil, false
	}
	switch semantic.StripAggregateStateType(memberType).(type) {
	case *semantic.TreeBlockType, *semantic.TreeStructType:
		return memberType, true
	default:
		return nil, false
	}
}

func (s *functionState) treeExactMemberConstructorCall(expr *ast.CallExpr) (semantic.Type, bool) {
	if expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, false
	}
	return s.treeExactMemberConstructorInfoFromField(fieldExpr)
}

func (s *functionState) treeConstructorInfoFromField(expr *ast.FieldExpr) (*semantic.TreeCategoryType, *semantic.EnumVariant, bool) {
	ownerName, variantName, ok := qualifiedFieldOwnerAndLeaf(expr)
	if !ok {
		return nil, nil, false
	}
	base, ok := s.g.result.NamedTypes[ownerName]
	if !ok {
		return nil, nil, false
	}
	treeType, ok := base.(*semantic.TreeCategoryType)
	if !ok {
		return nil, nil, false
	}
	variant, ok := treeType.Variant(variantName)
	if !ok {
		return treeType, nil, true
	}
	return treeType, variant, true
}

func (s *functionState) treeAllocConstructorInfo(expr ast.Expr) (*semantic.TreeCategoryType, *semantic.EnumVariant, *ast.CallExpr, bool) {
	switch n := expr.(type) {
	case *ast.FieldExpr:
		treeType, variant, ok := s.treeConstructorInfoFromField(n)
		if !ok {
			return nil, nil, nil, false
		}
		if variant != nil && len(variant.Payload) != 0 {
			return nil, nil, nil, false
		}
		return treeType, variant, nil, true
	case *ast.CallExpr:
		treeType, variant, ok := s.treeConstructorInfo(n)
		return treeType, variant, n, ok
	default:
		return nil, nil, nil, false
	}
}

func treeAllocArgs(callExpr *ast.CallExpr) []ast.Expr {
	if callExpr != nil {
		return callExpr.Args
	}
	return nil
}

func treeAllocArgNames(callExpr *ast.CallExpr) []string {
	if callExpr != nil {
		return callExpr.ArgNames
	}
	return nil
}

func qualifiedFieldOwnerAndLeaf(expr *ast.FieldExpr) (string, string, bool) {
	parts, ok := qualifiedFieldParts(expr)
	if !ok || len(parts) < 2 {
		return "", "", false
	}
	return strings.Join(parts[:len(parts)-1], "."), parts[len(parts)-1], true
}

func qualifiedFieldParts(expr ast.Expr) ([]string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil || n.Name == "" {
			return nil, false
		}
		return []string{n.Name}, true
	case *ast.FieldExpr:
		parts, ok := qualifiedFieldParts(n.Object)
		if !ok || n.Field == "" {
			return nil, false
		}
		return append(parts, n.Field), true
	case *ast.ParenExpr:
		return qualifiedFieldParts(n.Inner)
	default:
		return nil, false
	}
}

func resolveMatchableTreeCategoryTypeBackend(actual semantic.Type) (*semantic.TreeCategoryType, *semantic.TreeVariantViewType, bool) {
	actual = semantic.StripAggregateStateType(actual)
	switch tt := actual.(type) {
	case *semantic.TreeCategoryType:
		return tt, nil, true
	case *semantic.TreeVariantViewType:
		if tt == nil || tt.Category == nil {
			return nil, nil, false
		}
		return tt.Category, tt, true
	default:
		return nil, nil, false
	}
}

func (s *functionState) treeIsTargetPattern(expr ast.Expr) (*semantic.TreeCategoryType, *semantic.EnumVariant, *ast.MatchVariantPattern, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return s.treeIsTargetPattern(paren.Inner)
	}
	if testExpr, ok := expr.(*ast.VariantTestExpr); ok {
		if testExpr == nil || testExpr.Pattern == nil {
			return nil, nil, nil, false
		}
		base, ok := s.g.result.NamedTypes[testExpr.Pattern.EnumName]
		if !ok {
			return nil, nil, nil, false
		}
		treeType, ok := base.(*semantic.TreeCategoryType)
		if !ok || treeType == nil {
			return nil, nil, nil, false
		}
		variant, ok := treeType.Variant(testExpr.Pattern.Variant)
		if !ok || variant == nil {
			return nil, nil, nil, false
		}
		return treeType, variant, testExpr.Pattern, true
	}
	treeType, variant, ok := s.treeIsTarget(expr)
	if !ok || treeType == nil || variant == nil {
		return nil, nil, nil, false
	}
	pattern := &ast.MatchVariantPattern{Position: expr.Pos(), EnumName: treeType.Name, Variant: variant.Name}
	return treeType, variant, pattern, true
}

func (s *functionState) treeIsTarget(expr ast.Expr) (*semantic.TreeCategoryType, *semantic.EnumVariant, bool) {
	if fieldExpr, ok := expr.(*ast.FieldExpr); ok {
		return s.treeConstructorInfoFromField(fieldExpr)
	}
	typedExpr, ok := expr.(*ast.TypeExprExpr)
	if !ok || typedExpr == nil || typedExpr.Type == nil {
		return nil, nil, false
	}
	named, ok := typedExpr.Type.(*ast.NamedType)
	if !ok || named == nil {
		return nil, nil, false
	}
	idx := strings.LastIndex(named.Name, ".")
	if idx <= 0 || idx+1 >= len(named.Name) {
		return nil, nil, false
	}
	categoryName := named.Name[:idx]
	variantName := named.Name[idx+1:]
	base, ok := s.g.result.NamedTypes[categoryName]
	if !ok {
		return nil, nil, false
	}
	treeType, ok := base.(*semantic.TreeCategoryType)
	if !ok || treeType == nil {
		return nil, nil, false
	}
	variant, ok := treeType.Variant(variantName)
	if !ok {
		return treeType, nil, false
	}
	return treeType, variant, true
}

func (s *functionState) emitTreeIsTest(leftExpr ast.Expr, treeType *semantic.TreeCategoryType, variant *semantic.EnumVariant, pattern *ast.MatchVariantPattern) (C.LLVMValueRef, semantic.Type, error) {
	leftType := s.exprType(leftExpr)
	treeValue, _, err := s.emitExpr(leftExpr, leftType)
	if err != nil {
		return nil, nil, err
	}
	if pattern != nil && len(pattern.Args) != 0 {
		successBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.tree.variant.ok"))
		failureBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.tree.variant.fail"))
		contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.tree.variant.cont"))
		if _, _, err := s.emitMatchPatternTest(pattern, treeValue, nil, leftType, nil, leftExpr, nil, successBB, failureBB); err != nil {
			return nil, nil, err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, successBB)
		successValue := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0)
		C.LLVMBuildBr(s.builder, contBB)
		successEnd := C.LLVMGetInsertBlock(s.builder)

		C.LLVMPositionBuilderAtEnd(s.builder, failureBB)
		failureValue := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 0, 0)
		C.LLVMBuildBr(s.builder, contBB)
		failureEnd := C.LLVMGetInsertBlock(s.builder)

		C.LLVMPositionBuilderAtEnd(s.builder, contBB)
		phi := C.LLVMBuildPhi(s.builder, C.LLVMInt1TypeInContext(s.g.context), cStringFree("is.tree.variant.result"))
		values := []C.LLVMValueRef{successValue, failureValue}
		blocks := []C.LLVMBasicBlockRef{successEnd, failureEnd}
		C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
		return phi, s.g.result.NamedTypes["bool"], nil
	}
	tagValue, err := s.extractTreeCategoryTagValue(treeValue, treeType)
	if err != nil {
		return nil, nil, err
	}
	tagConst, err := s.enumTagConstant(variant.Tag)
	if err != nil {
		return nil, nil, err
	}
	cmp := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), tagValue, tagConst, cStringFree("istree.tag"))
	return cmp, s.g.result.NamedTypes["bool"], nil
}

func (s *functionState) emitTreeCategoryAlloc(treeType *semantic.TreeCategoryType, owner treeAllocOwnerBinding) (C.LLVMValueRef, error) {
	if treeType == nil {
		return nil, fmt.Errorf("missing tree constructor metadata")
	}
	storageType, err := s.g.ensureTreeCategoryBody(treeType)
	if err != nil {
		return nil, err
	}
	storageBytes, err := s.g.abiSizeOfLLVMType(storageType)
	if err != nil {
		return nil, err
	}
	if !owner.isPerm {
		if owner.storeValue != nil && owner.storeType != nil {
			arenaRef, err := s.emitTreeStoreArenaValue(owner.storeValue, owner.storeType)
			if err != nil {
				return nil, err
			}
			owner.arenaRef = arenaRef
		}
		if owner.arenaRef == nil {
			return nil, fmt.Errorf("missing Arena owner for tree constructor")
		}
		usizeType := s.g.result.NamedTypes["usize"]
		usizeLLVMType, err := s.g.lowerType(usizeType)
		if err != nil {
			return nil, err
		}
		sizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(storageBytes), 0)
		arenaType := s.g.result.NamedTypes["Arena"]
		arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		voidType := s.g.result.NamedTypes["void"]
		voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
			return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
		})
		callee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
		if err != nil {
			return nil, err
		}
		llvmFnType, err := s.g.lowerFunctionType(allocType)
		if err != nil {
			return nil, err
		}
		return s.buildCall(llvmFnType, callee, []C.LLVMValueRef{owner.arenaRef, sizeValue}, "tree.region.alloc"), nil
	}
	voidType := s.g.result.NamedTypes["void"]
	heapVoidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageHeap, ExplicitStorage: true}
	allocType := s.g.cachedRuntimeHelperType("alloc_perm", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "alloc_perm", Params: []semantic.Type{s.g.result.NamedTypes["i64"]}, Return: heapVoidRefType}
	})
	callee, err := s.g.ensureFunctionDeclared("alloc_perm", allocType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, err
	}
	sizeValue := C.LLVMConstInt(C.LLVMInt64TypeInContext(s.g.context), C.ulonglong(storageBytes), 0)
	return s.buildCall(llvmFnType, callee, []C.LLVMValueRef{sizeValue}, "tree.alloc"), nil
}

func (s *functionState) emitTreeConstructorValue(callExpr *ast.CallExpr, treeType *semantic.TreeCategoryType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string, owner *treeAllocOwnerBinding) (C.LLVMValueRef, semantic.Type, error) {
	if treeType == nil || variant == nil {
		return nil, nil, fmt.Errorf("missing tree constructor metadata")
	}
	resolvedOwner := treeAllocOwnerBinding{}
	if owner != nil {
		resolvedOwner = *owner
	} else {
		activeOwner, ok := s.lookupTreeAllocOwner()
		if !ok {
			return nil, nil, fmt.Errorf("tree constructor %s.%s requires an active in <owner>: scope or explicit new[owner]", treeType.Name, variant.Name)
		}
		resolvedOwner = activeOwner
	}
	orderedArgs, commonArgs, err := s.resolveTreeConstructorArgs(callExpr, treeType, variant, args, argNames)
	if err != nil {
		return nil, nil, err
	}
	if len(orderedArgs) != len(variant.Payload) {
		return nil, nil, fmt.Errorf("tree constructor %s.%s expects %d arguments, got %d", treeType.Name, variant.Name, len(variant.Payload), len(args))
	}
	storeValue, _, err := s.ensureTreeOwnerStoreValue(resolvedOwner, treeType.Family)
	if err != nil {
		return nil, nil, err
	}
	arenaValue := s.emitTreeStoreArenaValueNamed(storeValue, "tree.store.arena")
	stateValue := s.emitTreeStoreStateValueNamed(storeValue, "tree.store.state")
	memberType := treeType.VariantViewType(variant)
	tablePtr, err := s.emitTreeStateTablePtr(stateValue, treeType.Family, memberType, "tree.ctor")
	if err != nil {
		return nil, nil, err
	}
	rowIndex, err := s.emitTreeTableCountValue(tablePtr, memberType, "tree.ctor")
	if err != nil {
		return nil, nil, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, err
	}
	neededCount := C.LLVMBuildAdd(s.builder, rowIndex, C.LLVMConstInt(usizeType, 1, 0), cStringFree("tree.ctor.needed"))
	if err := s.emitTreeEnsureTableCapacity(arenaValue, tablePtr, memberType, neededCount, "tree.ctor"); err != nil {
		return nil, nil, err
	}
	for _, fieldDecl := range treeCommonFieldDecls(treeType) {
		arg := commonArgs[fieldDecl.Name]
		field, ok := treeExactFieldInfo(memberType, fieldDecl.Name)
		if !ok {
			return nil, nil, fmt.Errorf("missing tree common field %s.%s", treeType.Name, fieldDecl.Name)
		}
		fieldValue, _, err := s.emitExpr(arg, field.Type)
		if err != nil {
			return nil, nil, err
		}
		if err := s.emitTreeStoreExactFieldValueAtIndex(tablePtr, memberType, fieldDecl.Name, rowIndex, fieldValue, "tree.ctor"); err != nil {
			return nil, nil, err
		}
	}
	for i, payloadType := range variant.Payload {
		fieldName := variant.PayloadLabel(i)
		if fieldName == "" {
			fieldName = fmt.Sprintf("payload%d", i)
		}
		fieldValue, _, err := s.emitExpr(orderedArgs[i], payloadType)
		if err != nil {
			return nil, nil, err
		}
		if err := s.emitTreeStoreExactFieldValueAtIndex(tablePtr, memberType, fieldName, rowIndex, fieldValue, "tree.ctor"); err != nil {
			return nil, nil, err
		}
	}
	if err := s.emitTreeTableSetCount(tablePtr, memberType, neededCount, "tree.ctor"); err != nil {
		return nil, nil, err
	}
	keyValue, err := s.buildTreeHandleKey(variant.Tag, rowIndex, "tree.ctor")
	if err != nil {
		return nil, nil, err
	}
	handleValue, err := s.buildTreeHandleValue(treeType.Family, stateValue, keyValue, "tree.ctor")
	if err != nil {
		return nil, nil, err
	}
	return handleValue, treeType, nil
}

func (s *functionState) emitTreeExactMemberConstructorValue(callExpr *ast.CallExpr, memberType semantic.Type, owner *treeAllocOwnerBinding) (C.LLVMValueRef, semantic.Type, error) {
	if memberType == nil {
		return nil, nil, fmt.Errorf("missing exact tree member constructor metadata")
	}
	family := treeExactMemberFamily(memberType)
	if family == nil {
		return nil, nil, fmt.Errorf("missing tree family metadata for %s", memberType.String())
	}
	tag, ok := treeExactMemberTag(memberType)
	if !ok {
		return nil, nil, fmt.Errorf("missing exact tree member tag for %s", memberType.String())
	}
	resolvedOwner := treeAllocOwnerBinding{}
	if owner != nil {
		resolvedOwner = *owner
	} else {
		activeOwner, ok := s.lookupTreeAllocOwner()
		if !ok {
			return nil, nil, fmt.Errorf("tree constructor %s requires an active in <owner>: scope or explicit new[owner]", memberType.String())
		}
		resolvedOwner = activeOwner
	}
	fieldDecls := treeExactFieldDecls(memberType)
	var orderedArgs []ast.Expr
	if callExpr != nil && callExpr.ResolvedArgsValid && len(callExpr.ResolvedArgs) == len(fieldDecls) {
		orderedArgs = callExpr.ResolvedArgs
	} else if callExpr != nil {
		orderedArgs = callExpr.Args
	}
	if len(orderedArgs) != len(fieldDecls) {
		return nil, nil, fmt.Errorf("tree constructor %s expects %d fields, got %d", memberType.String(), len(fieldDecls), len(orderedArgs))
	}
	storeValue, _, err := s.ensureTreeOwnerStoreValue(resolvedOwner, family)
	if err != nil {
		return nil, nil, err
	}
	arenaValue := s.emitTreeStoreArenaValueNamed(storeValue, "tree.exact.store.arena")
	stateValue := s.emitTreeStoreStateValueNamed(storeValue, "tree.exact.store.state")
	tablePtr, err := s.emitTreeStateTablePtr(stateValue, family, memberType, "tree.exact")
	if err != nil {
		return nil, nil, err
	}
	rowIndex, err := s.emitTreeTableCountValue(tablePtr, memberType, "tree.exact")
	if err != nil {
		return nil, nil, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, err
	}
	neededCount := C.LLVMBuildAdd(s.builder, rowIndex, C.LLVMConstInt(usizeType, 1, 0), cStringFree("tree.exact.needed"))
	if err := s.emitTreeEnsureTableCapacity(arenaValue, tablePtr, memberType, neededCount, "tree.exact"); err != nil {
		return nil, nil, err
	}
	for i, fieldDecl := range fieldDecls {
		field, ok := treeExactFieldInfo(memberType, fieldDecl.Name)
		if !ok {
			return nil, nil, fmt.Errorf("missing exact tree field %s.%s", memberType.String(), fieldDecl.Name)
		}
		fieldValue, _, err := s.emitExpr(orderedArgs[i], field.Type)
		if err != nil {
			return nil, nil, err
		}
		if err := s.emitTreeStoreExactFieldValueAtIndex(tablePtr, memberType, fieldDecl.Name, rowIndex, fieldValue, "tree.exact"); err != nil {
			return nil, nil, err
		}
	}
	if err := s.emitTreeTableSetCount(tablePtr, memberType, neededCount, "tree.exact"); err != nil {
		return nil, nil, err
	}
	keyValue, err := s.buildTreeHandleKey(tag, rowIndex, "tree.exact")
	if err != nil {
		return nil, nil, err
	}
	handleValue, err := s.buildTreeHandleValue(family, stateValue, keyValue, "tree.exact")
	if err != nil {
		return nil, nil, err
	}
	return handleValue, memberType, nil
}

func (s *functionState) resolveTreeConstructorArgs(callExpr *ast.CallExpr, treeType *semantic.TreeCategoryType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string) ([]ast.Expr, map[string]ast.Expr, error) {
	if treeType == nil || variant == nil {
		return nil, nil, fmt.Errorf("missing tree constructor metadata")
	}
	if callExpr != nil && callExpr.ResolvedArgsValid && len(callExpr.ResolvedArgs) == len(variant.Payload) {
		return callExpr.ResolvedArgs, callExpr.ResolvedCommonArgs, nil
	}
	namedCount := 0
	for _, name := range argNames {
		if name != "" {
			namedCount++
		}
	}
	if namedCount == 0 {
		if callExpr != nil {
			callExpr.ResolvedArgsValid = true
			callExpr.ResolvedArgs = args
			callExpr.ResolvedCommonArgs = nil
		}
		return args, nil, nil
	}
	if namedCount != len(args) {
		return nil, nil, fmt.Errorf("tree constructor %s.%s cannot mix positional and named arguments", treeType.Name, variant.Name)
	}
	ordered := make([]ast.Expr, len(variant.Payload))
	seenPayload := make([]bool, len(variant.Payload))
	commonArgs := make(map[string]ast.Expr)
	for i, arg := range args {
		name := ""
		if i < len(argNames) {
			name = argNames[i]
		}
		if index, ok := variant.PayloadIndex(name); ok {
			if seenPayload[index] {
				return nil, nil, fmt.Errorf("tree constructor %s.%s payload field %q is specified more than once", treeType.Name, variant.Name, name)
			}
			ordered[index] = arg
			seenPayload[index] = true
			continue
		}
		if _, ok := treeType.Common[name]; ok {
			if _, exists := commonArgs[name]; exists {
				return nil, nil, fmt.Errorf("tree constructor %s.%s common field %q is specified more than once", treeType.Name, variant.Name, name)
			}
			commonArgs[name] = arg
			continue
		}
		return nil, nil, fmt.Errorf("tree constructor %s.%s has no payload or common field %q", treeType.Name, variant.Name, name)
	}
	for i, wasSeen := range seenPayload {
		if !wasSeen {
			label := variant.PayloadLabel(i)
			if label == "" {
				return nil, nil, fmt.Errorf("tree constructor %s.%s is missing argument %d", treeType.Name, variant.Name, i+1)
			}
			return nil, nil, fmt.Errorf("tree constructor %s.%s is missing payload field %q", treeType.Name, variant.Name, label)
		}
	}
	if callExpr != nil {
		callExpr.ResolvedArgsValid = true
		callExpr.ResolvedArgs = ordered
		callExpr.ResolvedCommonArgs = commonArgs
	}
	return ordered, commonArgs, nil
}

func (s *functionState) emitTreeFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	objType := s.exprType(expr.Object)
	handleValue, baseType, err := s.emitTreeHandleValue(expr.Object, objType)
	if err != nil || handleValue == nil || baseType == nil {
		return nil, nil, false, err
	}
	switch tt := baseType.(type) {
	case *semantic.TreeVariantViewType:
		if field, ok := semantic.TreeVariantSurfaceFieldInfo(tt, expr.Field); ok {
			if expr.Field == "kind" {
				if kindType, ok := semantic.TreeKindType(tt); ok && kindType != nil && semantic.SameType(field.Type, kindType) {
					llvmType, err := s.g.lowerType(kindType)
					if err != nil {
						return nil, nil, true, err
					}
					value := C.LLVMConstInt(llvmType, C.ulonglong(tt.Variant.Tag), 0)
					return value, kindType, true, nil
				}
			}
			stateValue := s.emitTreeHandleStateValue(handleValue, "tree.field")
			rowIndex, err := s.emitTreeHandleIndexValue(handleValue, "tree.field")
			if err != nil {
				return nil, nil, true, err
			}
			tablePtr, err := s.emitTreeStateTablePtr(stateValue, tt.Category.Family, tt, "tree.field")
			if err != nil {
				return nil, nil, true, err
			}
			value, rawType, err := s.emitTreeExactFieldValueAtIndex(tablePtr, tt, expr.Field, rowIndex, "tree.field")
			if err != nil {
				return nil, nil, true, err
			}
			surfaceValue, surfaceType, err := s.treeFieldSurfaceValue(value, rawType, field.Type, "tree.field")
			return surfaceValue, surfaceType, true, err
		}
		return nil, nil, true, fmt.Errorf("%s has no field %s", tt.String(), expr.Field)
	case *semantic.TreeNodeType:
		if expr.Field == "kind" {
			kindType, ok := semantic.TreeKindType(tt)
			if !ok || kindType == nil {
				return nil, nil, true, fmt.Errorf("%s has no kind", tt.String())
			}
			value, err := s.emitTreeHandleTagValue(handleValue, "tree.field.kind")
			if err != nil {
				return nil, nil, true, err
			}
			return value, kindType, true, nil
		}
		return nil, nil, true, fmt.Errorf("%s has no field %s", tt.String(), expr.Field)
	case *semantic.TreeCategoryType:
		if expr.Field == "kind" {
			kindType, ok := semantic.TreeKindType(tt)
			if !ok || kindType == nil {
				return nil, nil, true, fmt.Errorf("%s has no kind", tt.String())
			}
			value, err := s.extractTreeCategoryTagValue(handleValue, tt)
			if err != nil {
				return nil, nil, true, err
			}
			return value, kindType, true, nil
		}
		if field, ok := semantic.TreeCategorySurfaceFieldInfo(tt, expr.Field); ok {
			tagValue, err := s.emitTreeHandleTagValue(handleValue, "tree.field")
			if err != nil {
				return nil, nil, true, err
			}
			rowIndex, err := s.emitTreeHandleIndexValue(handleValue, "tree.field")
			if err != nil {
				return nil, nil, true, err
			}
			resultBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("tree.field.result"))
			failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("tree.field.fail"))
			switchInst := C.LLVMBuildSwitch(s.builder, tagValue, failBB, C.unsigned(len(tt.Variants)))
			var incomingValues []C.LLVMValueRef
			var incomingBlocks []C.LLVMBasicBlockRef
			for _, variant := range tt.Variants {
				memberType := tt.VariantViewType(variant)
				caseBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("tree.field.case"))
				tagConst, err := s.enumTagConstant(variant.Tag)
				if err != nil {
					return nil, nil, true, err
				}
				C.LLVMAddCase(switchInst, tagConst, caseBB)
				C.LLVMPositionBuilderAtEnd(s.builder, caseBB)
				stateValue := s.emitTreeHandleStateValue(handleValue, "tree.field")
				tablePtr, err := s.emitTreeStateTablePtr(stateValue, tt.Family, memberType, "tree.field")
				if err != nil {
					return nil, nil, true, err
				}
				value, rawType, err := s.emitTreeExactFieldValueAtIndex(tablePtr, memberType, expr.Field, rowIndex, "tree.field")
				if err != nil {
					return nil, nil, true, err
				}
				surfaceValue, _, err := s.treeFieldSurfaceValue(value, rawType, field.Type, "tree.field")
				if err != nil {
					return nil, nil, true, err
				}
				incomingValues = append(incomingValues, surfaceValue)
				incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
				C.LLVMBuildBr(s.builder, resultBB)
			}
			if err := s.emitTreeChildrenTrapBlock(failBB); err != nil {
				return nil, nil, true, err
			}
			C.LLVMPositionBuilderAtEnd(s.builder, resultBB)
			llvmFieldType, err := s.g.lowerType(field.Type)
			if err != nil {
				return nil, nil, true, err
			}
			phi := C.LLVMBuildPhi(s.builder, llvmFieldType, cStringFree("tree.field.phi"))
			C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
			return phi, field.Type, true, nil
		}
		return nil, nil, false, nil
	case *semantic.TreeBlockType, *semantic.TreeStructType:
		if expr.Field == "kind" {
			kindType, ok := semantic.TreeKindType(tt)
			if !ok || kindType == nil {
				return nil, nil, true, fmt.Errorf("%s has no kind", baseType.String())
			}
			llvmType, err := s.g.lowerType(kindType)
			if err != nil {
				return nil, nil, true, err
			}
			tag, ok := semantic.TreeExactTag(tt)
			if !ok {
				return nil, nil, true, fmt.Errorf("%s has no exact tree tag", baseType.String())
			}
			value := C.LLVMConstInt(llvmType, C.ulonglong(tag), 0)
			return value, kindType, true, nil
		}
		field, ok := semantic.TreeExactSurfaceFieldInfo(tt, expr.Field)
		if !ok {
			return nil, nil, true, fmt.Errorf("%s has no field %s", baseType.String(), expr.Field)
		}
		stateValue := s.emitTreeHandleStateValue(handleValue, "tree.field")
		rowIndex, err := s.emitTreeHandleIndexValue(handleValue, "tree.field")
		if err != nil {
			return nil, nil, true, err
		}
		tablePtr, err := s.emitTreeStateTablePtr(stateValue, treeExactMemberFamily(tt), tt, "tree.field")
		if err != nil {
			return nil, nil, true, err
		}
		value, rawType, err := s.emitTreeExactFieldValueAtIndex(tablePtr, tt, expr.Field, rowIndex, "tree.field")
		if err != nil {
			return nil, nil, true, err
		}
		surfaceValue, surfaceType, err := s.treeFieldSurfaceValue(value, rawType, field.Type, "tree.field")
		return surfaceValue, surfaceType, true, err
	default:
		return nil, nil, false, nil
	}
}

func (s *functionState) buildDynArrayViewValue(arrayValue C.LLVMValueRef, arrayType *semantic.DArrayType, viewType *semantic.DArrayViewType, name string) (C.LLVMValueRef, error) {
	if s == nil || arrayType == nil || viewType == nil {
		return nil, fmt.Errorf("missing dynamic array view conversion metadata")
	}
	viewLLVMType, err := s.g.lowerType(viewType)
	if err != nil {
		return nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	elemSize, err := s.sizeOfType(viewType.Elem)
	if err != nil {
		return nil, err
	}
	dataValue := C.LLVMBuildExtractValue(s.builder, arrayValue, 0, cStringFree(name+".data"))
	lenValue := C.LLVMBuildExtractValue(s.builder, arrayValue, 1, cStringFree(name+".len"))
	elemSizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(elemSize), 0)
	viewValue := C.LLVMGetUndef(viewLLVMType)
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, dataValue, 0, cStringFree(name+".view.data"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, lenValue, 1, cStringFree(name+".view.len"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, elemSizeValue, 2, cStringFree(name+".view.elem_size"))
	return viewValue, nil
}

func (s *functionState) treeFieldSurfaceValue(value C.LLVMValueRef, rawType semantic.Type, surfaceType semantic.Type, name string) (C.LLVMValueRef, semantic.Type, error) {
	if rawType == nil {
		return nil, nil, fmt.Errorf("missing raw tree field type")
	}
	if surfaceType == nil || semantic.SameType(rawType, surfaceType) {
		return value, rawType, nil
	}
	if rawOptional, ok := rawType.(*semantic.OptionalType); ok {
		surfaceOptional, ok := surfaceType.(*semantic.OptionalType)
		if !ok || surfaceOptional == nil || rawOptional == nil || rawOptional.Value == nil || surfaceOptional.Value == nil {
			return nil, nil, fmt.Errorf("unsupported tree field surface conversion from %s to %s", rawType.String(), surfaceType.String())
		}
		presentValue, err := s.extractOptionalPresent(value, rawOptional)
		if err != nil {
			return nil, nil, err
		}
		payloadValue, err := s.extractOptionalPayload(value, rawOptional)
		if err != nil {
			return nil, nil, err
		}
		surfacePayload, _, err := s.treeFieldSurfaceValue(payloadValue, rawOptional.Value, surfaceOptional.Value, name+".optional")
		if err != nil {
			return nil, nil, err
		}
		optionalValue, err := s.buildOptionalValue(surfaceOptional, presentValue, surfacePayload)
		if err != nil {
			return nil, nil, err
		}
		return optionalValue, surfaceOptional, nil
	}
	rawArray, ok := rawType.(*semantic.DArrayType)
	if !ok || rawArray == nil {
		return nil, nil, fmt.Errorf("unsupported tree field surface conversion from %s to %s", rawType.String(), surfaceType.String())
	}
	viewType, ok := surfaceType.(*semantic.DArrayViewType)
	if !ok || viewType == nil || !semantic.SameType(rawArray.Elem, viewType.Elem) {
		return nil, nil, fmt.Errorf("unsupported tree field surface conversion from %s to %s", rawType.String(), surfaceType.String())
	}
	viewValue, err := s.buildDynArrayViewValue(value, rawArray, viewType, name+".surface")
	if err != nil {
		return nil, nil, err
	}
	return viewValue, viewType, nil
}

func (s *functionState) emitTreeHandleValue(expr ast.Expr, objType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	baseType := semantic.StripAggregateStateType(objType)
	if refType, ok := baseType.(*semantic.RefType); ok {
		if _, ok := treeNodeHandleFamily(refType.Elem); !ok {
			return nil, nil, fmt.Errorf("tree field access requires a tree node base")
		}
		valuePtr, _, err := s.emitExpr(expr, objType)
		if err != nil {
			return nil, nil, err
		}
		value, err := s.loadValue(valuePtr, refType.Elem, "tree.handle.load")
		if err != nil {
			return nil, nil, err
		}
		return value, refType.Elem, nil
	}
	if _, ok := treeNodeHandleFamily(baseType); !ok {
		return nil, nil, fmt.Errorf("tree field access requires a tree node base")
	}
	value, _, err := s.emitExpr(expr, objType)
	if err != nil {
		return nil, nil, err
	}
	return value, baseType, nil
}

func (s *functionState) emitTreeCommonFieldAddress(objExpr ast.Expr, objType semantic.Type, fieldName string) (C.LLVMValueRef, semantic.Type, error) {
	return nil, nil, fmt.Errorf("tree field addresses are not supported for handle-lowered tree values")
}

func treeCategoryCommonFieldInfo(categoryType *semantic.TreeCategoryType, fieldName string) (semantic.Field, int, error) {
	for i, fieldDecl := range treeCommonFieldDecls(categoryType) {
		if fieldDecl.Name != fieldName {
			continue
		}
		field, ok := categoryType.Common[fieldName]
		if !ok {
			return semantic.Field{}, 0, fmt.Errorf("missing tree common field %s.%s", categoryType.Name, fieldName)
		}
		return field, 1 + i, nil
	}
	return semantic.Field{}, 0, fmt.Errorf("tree category %s has no common field %s", categoryType.Name, fieldName)
}

func treeCategoryPayloadFieldIndex(categoryType *semantic.TreeCategoryType) int {
	return 1 + len(treeCommonFieldDecls(categoryType))
}

func treeCategoryHasPayloadStorage(categoryType *semantic.TreeCategoryType) bool {
	if categoryType == nil {
		return false
	}
	for _, variant := range categoryType.Variants {
		if len(variant.Payload) != 0 {
			return true
		}
	}
	return false
}

func (s *functionState) treeCategoryPayloadPtr(nodePtr C.LLVMValueRef, categoryType *semantic.TreeCategoryType) (C.LLVMValueRef, error) {
	if !treeCategoryHasPayloadStorage(categoryType) {
		return nil, fmt.Errorf("tree category %s has no lowered payload storage", categoryType.Name)
	}
	storageType, err := s.g.ensureTreeCategoryBody(categoryType)
	if err != nil {
		return nil, err
	}
	return C.LLVMBuildStructGEP2(s.builder, storageType, nodePtr, C.unsigned(treeCategoryPayloadFieldIndex(categoryType)), cStringFree("tree.payload.ptr")), nil
}

func (s *functionState) extractTreeCategoryTagValue(nodeValue C.LLVMValueRef, categoryType *semantic.TreeCategoryType) (C.LLVMValueRef, error) {
	if categoryType == nil {
		return nil, fmt.Errorf("missing tree category metadata")
	}
	return s.emitTreeHandleTagValue(nodeValue, "tree.tag")
}

func (s *functionState) extractTreeVariantPayloadValues(nodeValue C.LLVMValueRef, categoryType *semantic.TreeCategoryType, variant *semantic.EnumVariant) ([]C.LLVMValueRef, error) {
	if variant == nil || len(variant.Payload) == 0 {
		return nil, nil
	}
	if categoryType == nil || categoryType.Family == nil {
		return nil, fmt.Errorf("missing tree category metadata")
	}
	stateValue := s.emitTreeHandleStateValue(nodeValue, "tree.payload")
	rowIndex, err := s.emitTreeHandleIndexValue(nodeValue, "tree.payload")
	if err != nil {
		return nil, err
	}
	memberType := categoryType.VariantViewType(variant)
	tablePtr, err := s.emitTreeStateTablePtr(stateValue, categoryType.Family, memberType, "tree.payload")
	if err != nil {
		return nil, err
	}
	values := make([]C.LLVMValueRef, 0, len(variant.Payload))
	for i := range variant.Payload {
		fieldName := variant.PayloadLabel(i)
		if fieldName == "" {
			fieldName = fmt.Sprintf("payload%d", i)
		}
		value, _, err := s.emitTreeExactFieldValueAtIndex(tablePtr, memberType, fieldName, rowIndex, "tree.payload.field")
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func (s *functionState) emitEnumConstructorValue(callExpr *ast.CallExpr, enumType *semantic.EnumType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string) (C.LLVMValueRef, semantic.Type, error) {
	if enumType == nil || variant == nil {
		return nil, nil, fmt.Errorf("missing enum constructor metadata")
	}
	if enumType.Packed {
		return nil, nil, fmt.Errorf("packed enum constructor %s.%s must be allocated with new[%s]", enumType.Name, variant.Name, enumType.StoreType.Name)
	}
	orderedArgs, err := s.resolveEnumConstructorArgs(callExpr, enumType, variant, args, argNames)
	if err != nil {
		return nil, nil, err
	}
	if len(orderedArgs) != len(variant.Payload) {
		return nil, nil, fmt.Errorf("enum constructor %s.%s expects %d arguments, got %d", enumType.Name, variant.Name, len(variant.Payload), len(args))
	}
	if enumIsTagOnly(enumType) {
		tagValue, err := s.enumTagConstant(variant.Tag)
		if err != nil {
			return nil, nil, err
		}
		return tagValue, enumType, nil
	}
	enumPtr, err := s.emitStackTempZeroed(enumType, "enum.ctor")
	if err != nil {
		return nil, nil, err
	}
	enumLLVMType, err := s.g.lowerType(enumType)
	if err != nil {
		return nil, nil, err
	}
	tagValue, err := s.enumTagConstant(variant.Tag)
	if err != nil {
		return nil, nil, err
	}
	tagPtr := C.LLVMBuildStructGEP2(s.builder, enumLLVMType, enumPtr, 0, cStringFree("enum.tag.ptr"))
	C.LLVMBuildStore(s.builder, tagValue, tagPtr)
	if len(variant.Payload) > 0 {
		payloadPtr, err := s.enumPayloadPtr(enumPtr, enumType)
		if err != nil {
			return nil, nil, err
		}
		if len(variant.Payload) == 1 {
			argValue, _, err := s.emitExpr(orderedArgs[0], variant.Payload[0])
			if err != nil {
				return nil, nil, err
			}
			if !llvmValueIsZeroConstant(argValue) {
				C.LLVMBuildStore(s.builder, argValue, payloadPtr)
			}
		} else {
			payloadType, err := s.g.lowerEnumVariantPayloadType(variant)
			if err != nil {
				return nil, nil, err
			}
			aggregate := C.LLVMGetUndef(payloadType)
			allZero := true
			for i, payload := range variant.Payload {
				argValue, _, err := s.emitExpr(orderedArgs[i], payload)
				if err != nil {
					return nil, nil, err
				}
				if !llvmValueIsZeroConstant(argValue) {
					allZero = false
				}
				aggregate = C.LLVMBuildInsertValue(s.builder, aggregate, argValue, C.unsigned(i), cStringFree("enum.payload.ins"))
			}
			if !allZero {
				C.LLVMBuildStore(s.builder, aggregate, payloadPtr)
			}
		}
	}
	value, err := s.loadValue(enumPtr, enumType, "enum.value")
	if err != nil {
		return nil, nil, err
	}
	return value, enumType, nil
}

func (s *functionState) emitPackedEnumConstructorAlloc(callExpr *ast.CallExpr, storeValue C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string) (C.LLVMValueRef, semantic.Type, error) {
	if enumType == nil || variant == nil || !enumType.Packed {
		return nil, nil, fmt.Errorf("missing packed enum constructor metadata")
	}
	orderedArgs, commonArgs, err := s.resolvePackedEnumConstructorArgs(callExpr, enumType, variant, args, argNames)
	if err != nil {
		return nil, nil, err
	}
	if len(orderedArgs) != len(variant.Payload) {
		return nil, nil, fmt.Errorf("enum constructor %s.%s expects %d arguments, got %d", enumType.Name, variant.Name, len(variant.Payload), len(args))
	}
	tagValue, err := s.enumTagConstant(variant.Tag)
	if err != nil {
		return nil, nil, err
	}
	if s.canInlinePackedEnumVariant(enumType, variant) {
		var payloadValue C.LLVMValueRef
		var payloadType semantic.Type
		if len(orderedArgs) == 1 {
			payloadType = variant.Payload[0]
			payloadValue, _, err = s.emitExpr(orderedArgs[0], payloadType)
			if err != nil {
				return nil, nil, err
			}
		}
		inlineHandle, err := s.buildInlinePackedEnumHandle(tagValue, payloadValue, payloadType)
		if err != nil {
			return nil, nil, err
		}
		return inlineHandle, enumType, nil
	}
	rowType, err := s.g.ensurePackedEnumStorageType(enumType)
	if err != nil {
		return nil, nil, err
	}
	sideWords, err := s.g.packedEnumCommonSideTableWordCount(enumType)
	if err != nil {
		return nil, nil, err
	}
	tailPlan, err := s.preparePackedEnumTailPayloadPlan(variant, orderedArgs)
	if err != nil {
		return nil, nil, err
	}
	allocPtr, enumValue, rowSizeValue, err := s.emitPackedEnumStorageAlloc(storeValue, enumType, tailPlan, tagValue)
	if err != nil {
		return nil, nil, err
	}
	rowValue := C.LLVMConstNull(rowType)
	rowValue = C.LLVMBuildInsertValue(s.builder, rowValue, tagValue, 0, cStringFree("packed.enum.tag.ins"))
	commonValues := make(map[string]C.LLVMValueRef, len(enumType.Decl.Common))
	for _, commonDecl := range enumType.Decl.Common {
		arg, ok := commonArgs[commonDecl.Name]
		if !ok {
			continue
		}
		layout, err := s.g.packedEnumCommonFieldLayout(enumType, commonDecl.Name)
		if err != nil {
			return nil, nil, err
		}
		fieldValue, _, err := s.emitExpr(arg, layout.Field.Type)
		if err != nil {
			return nil, nil, err
		}
		commonValues[commonDecl.Name] = fieldValue
		if layout.StoredInline {
			rowValue = C.LLVMBuildInsertValue(s.builder, rowValue, fieldValue, C.unsigned(layout.RowFieldIndex), cStringFree("packed.enum.common.ins"))
		}
	}
	C.LLVMBuildStore(s.builder, rowValue, allocPtr)
	if len(variant.Payload) > 0 {
		payloadPtr, err := s.enumPayloadPtr(allocPtr, enumType)
		if err != nil {
			return nil, nil, err
		}
		var tailDataPtr C.LLVMValueRef
		if tailPlan != nil {
			tailDataPtr, err = s.emitPackedEnumTailDataPtr(allocPtr, rowSizeValue)
			if err != nil {
				return nil, nil, err
			}
		}
		if len(variant.Payload) == 1 {
			argValue, err := s.emitPackedEnumConstructorPayloadValue(variant, 0, orderedArgs[0], tailPlan, tailDataPtr)
			if err != nil {
				return nil, nil, err
			}
			if !llvmValueIsZeroConstant(argValue) {
				C.LLVMBuildStore(s.builder, argValue, payloadPtr)
			}
		} else {
			payloadType, err := s.g.lowerEnumVariantPayloadType(variant)
			if err != nil {
				return nil, nil, err
			}
			aggregate := C.LLVMGetUndef(payloadType)
			allZero := true
			for i, payload := range variant.Payload {
				_ = payload
				argValue, err := s.emitPackedEnumConstructorPayloadValue(variant, i, orderedArgs[i], tailPlan, tailDataPtr)
				if err != nil {
					return nil, nil, err
				}
				if !llvmValueIsZeroConstant(argValue) {
					allZero = false
				}
				aggregate = C.LLVMBuildInsertValue(s.builder, aggregate, argValue, C.unsigned(i), cStringFree("packed.enum.payload.ins"))
			}
			if !allZero {
				C.LLVMBuildStore(s.builder, aggregate, payloadPtr)
			}
		}
	}
	ops := &packedStoreOps{s: s, storeValue: storeValue, storeType: enumType.StoreType}
	if sideWords > 0 {
		wordBytes := uint64(s.g.wordBits / 8)
		if wordBytes == 0 {
			wordBytes = 8
		}
		sideBufferType := &semantic.ArrayType{Elem: s.g.result.NamedTypes["uintptr"], HasConstSize: true, ConstSize: int64(sideWords)}
		sideBufferPtr, err := s.createEntryAlloca("packed.side.words", sideBufferType)
		if err != nil {
			return nil, nil, err
		}
		sideBufferLLVMType, err := s.g.lowerType(sideBufferType)
		if err != nil {
			return nil, nil, err
		}
		C.LLVMBuildStore(s.builder, C.LLVMConstNull(sideBufferLLVMType), sideBufferPtr)
		for _, commonDecl := range enumType.Decl.Common {
			layout, err := s.g.packedEnumCommonFieldLayout(enumType, commonDecl.Name)
			if err != nil {
				return nil, nil, err
			}
			if layout.StoredInline {
				continue
			}
			fieldValue, ok := commonValues[commonDecl.Name]
			if !ok {
				continue
			}
			if err := s.emitPackSideTableFieldValue(sideBufferPtr, layout.SideWordOffset*wordBytes, fieldValue, layout.Field.Type, "packed.side.pack"); err != nil {
				return nil, nil, err
			}
		}
		if err := ops.recordSideWords(sideBufferPtr, "packed.side.record"); err != nil {
			return nil, nil, err
		}
	}
	if err := ops.recordPrefixWords(allocPtr, "packed.prefix.record"); err != nil {
		return nil, nil, err
	}
	mode := s.g.packedModeForEnum(enumType)
	if mode != packedEnumABIVariantSparse && tailPlan != nil {
		if err := s.emitPackedStoreRecordTag(storeValue, enumType.StoreType, tagValue); err != nil {
			return nil, nil, err
		}
	}
	return enumValue, enumType, nil
}

func (s *functionState) canInlinePackedEnumVariant(enumType *semantic.EnumType, variant *semantic.EnumVariant) bool {
	return false
}

func (s *functionState) buildInlinePackedEnumHandle(tagValue C.LLVMValueRef, payloadValue C.LLVMValueRef, payloadType semantic.Type) (C.LLVMValueRef, error) {
	uintptrLLVMType, err := s.g.lowerBuiltin("uintptr")
	if err != nil {
		return nil, err
	}
	handleValue := C.LLVMBuildZExt(s.builder, tagValue, uintptrLLVMType, cStringFree("packed.inline.tag.zext"))
	handleValue = C.LLVMBuildShl(s.builder, handleValue, C.LLVMConstInt(uintptrLLVMType, 49, 0), cStringFree("packed.inline.tag.shift"))
	if payloadValue != nil && payloadType != nil {
		payloadBits := C.LLVMBuildZExt(s.builder, payloadValue, uintptrLLVMType, cStringFree("packed.inline.payload.zext"))
		payloadBits = C.LLVMBuildShl(s.builder, payloadBits, C.LLVMConstInt(uintptrLLVMType, 1, 0), cStringFree("packed.inline.payload.shift"))
		handleValue = C.LLVMBuildOr(s.builder, handleValue, payloadBits, cStringFree("packed.inline.payload.or"))
	}
	return C.LLVMBuildOr(s.builder, handleValue, C.LLVMConstInt(uintptrLLVMType, 1, 0), cStringFree("packed.inline.handle")), nil
}

func (s *functionState) emitPackedStoreRecordTag(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType, tagValue C.LLVMValueRef) error {
	if storeType == nil {
		return fmt.Errorf("missing packed store type for tag record")
	}
	ops := &packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}
	return ops.recordTag(tagValue, "packed.tag.record")
}

type packedEnumTailPayloadPlan struct {
	index         int
	viewType      *semantic.DArrayViewType
	elemSizeValue C.LLVMValueRef
	lenValue      C.LLVMValueRef
	byteCount     C.LLVMValueRef
	sourceData    C.LLVMValueRef
	literal       *ast.ListLitExpr
}

func (s *functionState) preparePackedEnumTailPayloadPlan(variant *semantic.EnumVariant, orderedArgs []ast.Expr) (*packedEnumTailPayloadPlan, error) {
	if variant == nil {
		return nil, nil
	}
	tailIndex, ok := variant.TailPayloadIndex()
	if !ok {
		return nil, nil
	}
	viewType, ok := variant.TailPayloadViewType()
	if !ok || viewType == nil {
		return nil, fmt.Errorf("packed enum %s tail payload metadata is inconsistent", variant.Name)
	}
	if tailIndex >= len(orderedArgs) {
		return nil, fmt.Errorf("packed enum %s tail payload argument is missing", variant.Name)
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	elemSize, err := s.sizeOfType(viewType.Elem)
	if err != nil {
		return nil, err
	}
	elemSizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(elemSize), 0)
	arg := orderedArgs[tailIndex]
	if literal, ok := arg.(*ast.ListLitExpr); ok {
		lenValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(len(literal.Elems)), 0)
		byteCount := C.LLVMConstInt(usizeLLVMType, C.ulonglong(uint64(len(literal.Elems))*elemSize), 0)
		return &packedEnumTailPayloadPlan{index: tailIndex, viewType: viewType, elemSizeValue: elemSizeValue, lenValue: lenValue, byteCount: byteCount, literal: literal}, nil
	}
	sourceType := s.exprType(arg)
	if _, ok := sourceType.(*semantic.DArrayViewType); !ok {
		if _, ok := sourceType.(*semantic.ViewType); !ok {
			return nil, fmt.Errorf("packed enum %s tail payload expects a list literal or view-compatible source, got %s", variant.Name, sourceType.String())
		}
	}
	viewValue, _, err := s.emitExpr(arg, sourceType)
	if err != nil {
		return nil, err
	}
	lenValue := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("packed.tail.src.len"))
	sourceData := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("packed.tail.src.data"))
	byteCount := C.LLVMBuildMul(s.builder, lenValue, elemSizeValue, cStringFree("packed.tail.bytes"))
	return &packedEnumTailPayloadPlan{index: tailIndex, viewType: viewType, elemSizeValue: elemSizeValue, lenValue: lenValue, byteCount: byteCount, sourceData: sourceData}, nil
}

func (s *functionState) emitPackedEnumTailDataPtr(allocPtr C.LLVMValueRef, rowSizeValue C.LLVMValueRef) (C.LLVMValueRef, error) {
	i8Type, err := s.g.lowerBuiltin("u8")
	if err != nil {
		return nil, err
	}
	indices := []C.LLVMValueRef{rowSizeValue}
	return C.LLVMBuildGEP2(s.builder, i8Type, allocPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("packed.tail.data")), nil
}

func (s *functionState) emitPackedEnumConstructorPayloadValue(variant *semantic.EnumVariant, index int, arg ast.Expr, tailPlan *packedEnumTailPayloadPlan, tailDataPtr C.LLVMValueRef) (C.LLVMValueRef, error) {
	if tailPlan != nil && index == tailPlan.index {
		return s.emitPackedEnumTailPayloadValue(tailPlan, tailDataPtr)
	}
	argValue, _, err := s.emitExpr(arg, variant.Payload[index])
	if err != nil {
		return nil, err
	}
	return argValue, nil
}

func (s *functionState) emitPackedEnumTailPayloadValue(plan *packedEnumTailPayloadPlan, tailDataPtr C.LLVMValueRef) (C.LLVMValueRef, error) {
	if plan == nil || plan.viewType == nil {
		return nil, fmt.Errorf("missing packed enum tail payload plan")
	}
	viewLLVMType, err := s.g.lowerType(plan.viewType)
	if err != nil {
		return nil, err
	}
	if plan.literal != nil {
		elemLLVMType, err := s.g.lowerType(plan.viewType.Elem)
		if err != nil {
			return nil, err
		}
		usizeType := s.g.result.NamedTypes["usize"]
		usizeLLVMType, err := s.g.lowerType(usizeType)
		if err != nil {
			return nil, err
		}
		for i, elem := range plan.literal.Elems {
			elemValue, _, err := s.emitExpr(elem, plan.viewType.Elem)
			if err != nil {
				return nil, err
			}
			indexValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(i), 0)
			indices := []C.LLVMValueRef{indexValue}
			elemPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, tailDataPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("packed.tail.elem.ptr"))
			C.LLVMBuildStore(s.builder, elemValue, elemPtr)
		}
	} else {
		if err := s.emitPackedEnumTailMemcpy(tailDataPtr, plan.sourceData, plan.byteCount); err != nil {
			return nil, err
		}
	}
	viewValue := C.LLVMGetUndef(viewLLVMType)
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, tailDataPtr, 0, cStringFree("packed.tail.view.data"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, plan.lenValue, 1, cStringFree("packed.tail.view.len"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, plan.elemSizeValue, 2, cStringFree("packed.tail.view.elem_size"))
	return viewValue, nil
}

func (s *functionState) emitPackedEnumTailMemcpy(dstData C.LLVMValueRef, srcData C.LLVMValueRef, byteCount C.LLVMValueRef) error {
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	usizeType := s.g.result.NamedTypes["usize"]
	memcpyType := s.g.cachedRuntimeHelperType("arena_memcpy", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_memcpy", Params: []semantic.Type{voidRefType, voidRefType, usizeType}, Return: voidRefType}
	})
	memcpyCallee, err := s.g.ensureFunctionDeclared("arena_memcpy", memcpyType)
	if err != nil {
		return err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return err
	}
	memcpyCall := s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{dstData, srcData, byteCount}, "packed.tail.memcpy")
	s.addCallSiteEnumAttribute(memcpyCall, C.uint(1), "noalias")
	s.addCallSiteEnumAttribute(memcpyCall, C.uint(2), "noalias")
	return nil
}

func (s *functionState) emitPackedEnumStorageAlloc(storeValue C.LLVMValueRef, enumType *semantic.EnumType, tailPlan *packedEnumTailPayloadPlan, fixedTagValue C.LLVMValueRef) (C.LLVMValueRef, C.LLVMValueRef, C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, nil, nil, fmt.Errorf("missing packed enum storage metadata")
	}
	storeType := enumType.StoreType
	if storeType == nil {
		return nil, nil, nil, fmt.Errorf("packed enum %s is missing store metadata", enumType.Name)
	}
	ops := &packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}
	rowSizeValue, err := ops.rowBytesValue("packed.alloc.store")
	if err != nil {
		return nil, nil, nil, err
	}
	totalSizeValue := rowSizeValue
	if tailPlan != nil {
		totalSizeValue = C.LLVMBuildAdd(s.builder, rowSizeValue, tailPlan.byteCount, cStringFree("packed.alloc.bytes"))
	}
	return ops.allocateStorage(enumType, totalSizeValue, tailPlan != nil, fixedTagValue, "packed.alloc.store")
}

func (s *functionState) resolveEnumConstructorArgs(callExpr *ast.CallExpr, enumType *semantic.EnumType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string) ([]ast.Expr, error) {
	if variant == nil {
		return nil, fmt.Errorf("missing enum constructor metadata")
	}
	if callExpr != nil && callExpr.ResolvedArgsValid && len(callExpr.ResolvedArgs) == len(variant.Payload) && callExpr.ResolvedCommonArgs == nil {
		return callExpr.ResolvedArgs, nil
	}
	namedCount := 0
	for _, name := range argNames {
		if name != "" {
			namedCount++
		}
	}
	if namedCount == 0 {
		if callExpr != nil {
			callExpr.ResolvedArgsValid = true
			callExpr.ResolvedArgs = args
			callExpr.ResolvedCommonArgs = nil
		}
		return args, nil
	}
	if namedCount != len(args) {
		return nil, fmt.Errorf("enum constructor %s.%s cannot mix positional and named arguments", enumType.Name, variant.Name)
	}
	if !variant.HasNamedPayloads() {
		return nil, fmt.Errorf("enum constructor %s.%s does not declare named payload fields", enumType.Name, variant.Name)
	}
	ordered := make([]ast.Expr, len(variant.Payload))
	seen := make([]bool, len(variant.Payload))
	for i, arg := range args {
		name := ""
		if i < len(argNames) {
			name = argNames[i]
		}
		index, ok := variant.PayloadIndex(name)
		if !ok {
			return nil, fmt.Errorf("enum constructor %s.%s has no payload field %q", enumType.Name, variant.Name, name)
		}
		if seen[index] {
			return nil, fmt.Errorf("enum constructor %s.%s payload field %q is specified more than once", enumType.Name, variant.Name, name)
		}
		ordered[index] = arg
		seen[index] = true
	}
	for i, wasSeen := range seen {
		if !wasSeen {
			label := variant.PayloadLabel(i)
			if label == "" {
				return nil, fmt.Errorf("enum constructor %s.%s is missing argument %d", enumType.Name, variant.Name, i+1)
			}
			return nil, fmt.Errorf("enum constructor %s.%s is missing payload field %q", enumType.Name, variant.Name, label)
		}
	}
	if callExpr != nil {
		callExpr.ResolvedArgsValid = true
		callExpr.ResolvedArgs = ordered
		callExpr.ResolvedCommonArgs = nil
	}
	return ordered, nil
}

func (s *functionState) resolvePackedEnumConstructorArgs(callExpr *ast.CallExpr, enumType *semantic.EnumType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string) ([]ast.Expr, map[string]ast.Expr, error) {
	if enumType == nil || variant == nil {
		return nil, nil, fmt.Errorf("missing packed enum constructor metadata")
	}
	if callExpr != nil && callExpr.ResolvedArgsValid && len(callExpr.ResolvedArgs) == len(variant.Payload) {
		return callExpr.ResolvedArgs, callExpr.ResolvedCommonArgs, nil
	}
	namedCount := 0
	for _, name := range argNames {
		if name != "" {
			namedCount++
		}
	}
	if namedCount == 0 {
		if callExpr != nil {
			callExpr.ResolvedArgsValid = true
			callExpr.ResolvedArgs = args
			callExpr.ResolvedCommonArgs = nil
		}
		return args, nil, nil
	}
	if namedCount != len(args) {
		return nil, nil, fmt.Errorf("enum constructor %s.%s cannot mix positional and named arguments", enumType.Name, variant.Name)
	}
	ordered := make([]ast.Expr, len(variant.Payload))
	seenPayload := make([]bool, len(variant.Payload))
	commonArgs := make(map[string]ast.Expr)
	for i, arg := range args {
		name := ""
		if i < len(argNames) {
			name = argNames[i]
		}
		if index, ok := variant.PayloadIndex(name); ok {
			if seenPayload[index] {
				return nil, nil, fmt.Errorf("enum constructor %s.%s payload field %q is specified more than once", enumType.Name, variant.Name, name)
			}
			ordered[index] = arg
			seenPayload[index] = true
			continue
		}
		if _, ok := enumType.Common[name]; ok {
			if _, exists := commonArgs[name]; exists {
				return nil, nil, fmt.Errorf("packed enum constructor %s.%s common field %q is specified more than once", enumType.Name, variant.Name, name)
			}
			commonArgs[name] = arg
			continue
		}
		return nil, nil, fmt.Errorf("packed enum constructor %s.%s has no payload or common field %q", enumType.Name, variant.Name, name)
	}
	for i, wasSeen := range seenPayload {
		if !wasSeen {
			label := variant.PayloadLabel(i)
			if label == "" {
				return nil, nil, fmt.Errorf("enum constructor %s.%s is missing argument %d", enumType.Name, variant.Name, i+1)
			}
			return nil, nil, fmt.Errorf("enum constructor %s.%s is missing payload field %q", enumType.Name, variant.Name, label)
		}
	}
	if callExpr != nil {
		callExpr.ResolvedArgsValid = true
		callExpr.ResolvedArgs = ordered
		callExpr.ResolvedCommonArgs = commonArgs
	}
	return ordered, commonArgs, nil
}
