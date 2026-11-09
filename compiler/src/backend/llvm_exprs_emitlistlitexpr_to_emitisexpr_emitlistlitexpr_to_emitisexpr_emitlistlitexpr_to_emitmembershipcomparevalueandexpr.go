//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>

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

// elisa_coreAttachDarrayAndComprehensionScope combines the shared darray header/element
// separation with a comprehension's fresh-source/destination separation. LLVM has one metadata
// slot for each list, so setting the two domains separately would overwrite one of them.
static void elisa_coreAttachDarrayAndComprehensionScope(LLVMValueRef inst, LLVMContextRef ctx,
	const char* comprehensionDomainName, const char* comprehensionAliasScopeName,
	const char* comprehensionNoAliasScopeName) {
	if (inst == NULL || ctx == NULL || comprehensionDomainName == NULL ||
		comprehensionAliasScopeName == NULL || comprehensionNoAliasScopeName == NULL ||
		comprehensionDomainName[0] == '\0' || comprehensionAliasScopeName[0] == '\0' ||
		comprehensionNoAliasScopeName[0] == '\0') {
		return;
	}
	LLVMMetadataRef darrayDomain = elisa_coreCreateAliasScopeDomain(ctx, "elisa.darray.aa");
	LLVMMetadataRef comprehensionDomain = elisa_coreCreateAliasScopeDomain(ctx, comprehensionDomainName);
	LLVMMetadataRef aliasScopes[2];
	aliasScopes[0] = elisa_coreCreateAliasScope(ctx, darrayDomain, "elt");
	aliasScopes[1] = elisa_coreCreateAliasScope(ctx, comprehensionDomain, comprehensionAliasScopeName);
	LLVMMetadataRef noAliasScopes[2];
	noAliasScopes[0] = elisa_coreCreateAliasScope(ctx, darrayDomain, "hdr");
	noAliasScopes[1] = elisa_coreCreateAliasScope(ctx, comprehensionDomain, comprehensionNoAliasScopeName);
	elisa_coreSetMetadataList(inst, ctx, "alias.scope", aliasScopes, 2);
	elisa_coreSetMetadataList(inst, ctx, "noalias", noAliasScopes, 2);
}
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"unsafe"
)

func (s *functionState) emitListLitExpr(expr *ast.ListLitExpr, expected semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	// A `{k: v, ...}` brace literal is a populated dict literal: materialize an empty dict and
	// insert each pair (arena sourced from the active region scope).
	if expr.Brace && len(expr.Keys) > 0 {
		if dictType, ok := s.dictLiteralTargetType(expr, expected); ok {
			return s.emitDictLiteralExpr(expr, dictType)
		}
	}
	if expr.Brace && len(expr.Keys) == 0 {
		if setType, ok := s.setLiteralTargetType(expr, expected); ok {
			if len(expr.Elems) == 0 {
				zero, err := s.zeroValue(setType)
				if err != nil {
					return nil, nil, err
				}
				return zero, setType, nil
			}
			return s.emitSetLiteralExpr(expr, setType)
		}
	}
	// An empty `{}` against a dict type is an empty (zero-initialized) dict — the dict analogue
	// of an empty `[]` darray literal. Lower it to the dict's zero value (a zeroed header); the
	// backing allocates lazily on the first insert.
	if expr.Brace && len(expr.Elems) == 0 {
		if dictType, ok := s.dictLiteralTargetType(expr, expected); ok {
			zero, err := s.zeroValue(dictType)
			if err != nil {
				return nil, nil, err
			}
			return zero, dictType, nil
		}
	}
	hasSpread := false
	for _, spread := range expr.Spreads {
		if spread {
			hasSpread = true
			break
		}
	}
	if arrayType, ok, err := s.listLiteralTargetArrayType(expr, expected); err != nil {
		return nil, nil, err
	} else if ok {
		if hasSpread {
			return nil, nil, fmt.Errorf("array literal spread requires a darray target")
		}
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
	if hasSpread {
		return s.emitSpreadListLitExpr(expr, darrayType)
	}
	if len(expr.Elems) == 0 {
		zero, err := s.zeroValue(darrayType)
		if err != nil {
			return nil, nil, err
		}
		return zero, darrayType, nil
	}
	owner, ok := treeAllocOwnerBinding{}, false
	// A stack-tagged destination (task_00a7fdf3): a seeded container stored into a darray that is
	// routed to its own parallel arena must allocate its backing INTO that same arena, so the
	// backing and the darray's later growth ops (incl. a grower's realloc) never straddle the
	// region base arena and the parallel arena. This takes priority over the analyzer's own owner
	// resolution — whether it left Owner nil (a fresh VarDecl seed) or attached a region owner (a
	// reassignment, whose darray type carries no region). Read-and-clear so only this outermost
	// sunk container consumes it (a nested inner literal falls back to normal resolution).
	if s.currentDArraySinkTag != "" {
		sinkTag := s.currentDArraySinkTag
		s.currentDArraySinkTag = ""
		owner, ok = s.regionArenaOwner(sinkTag)
	}
	if !ok && expr.Owner != nil {
		var err error
		owner, ok, err = s.classifyTreeAllocOwnerExpr(expr.Owner)
		if err != nil {
			return nil, nil, err
		}
	} else if !ok {
		owner, ok = s.regionArenaOwner(darrayType.Region)
		if !ok {
			owner, ok = s.lookupTreeAllocOwner()
		}
	}
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, fmt.Errorf("%s", semantic.NoContainerRegionMessage("darray literal"))
	}
	arenaRef, err := s.treeOwnerArenaRefValue(owner, "darray.literal.owner.arena")
	if err != nil {
		return nil, nil, err
	}
	if arenaRef == nil {
		return nil, nil, fmt.Errorf("%s", semantic.NoContainerRegionMessage("darray literal"))
	}
	llvmType, err := s.g.lowerType(darrayType)
	if err != nil {
		return nil, nil, err
	}
	elemLLVMType, err := s.g.lowerType(darrayType.Elem)
	if err != nil {
		return nil, nil, err
	}
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, err
	}
	elemSize, err := s.sizeOfType(darrayType.Elem)
	if err != nil {
		return nil, nil, err
	}
	literalCount := len(expr.Elems)
	// Small headroom so a literal-then-push builder does not realloc on its first push, but
	// never a fixed large floor: a 256-element floor made every `[a, b, c, d]` reserve
	// 256*sizeof(elem) bytes (2 KiB for usize), which dominated memory for loop-local literals.
	// Later growth doubles (emitSafeDoubledCapacity), so this only affects the first realloc.
	literalCapacity := literalCount
	if literalCapacity < darrayLiteralMinCapacity {
		literalCapacity = darrayLiteralMinCapacity
	}
	byteCount := C.LLVMConstInt(usizeLLVMType, C.ulonglong(uint64(literalCapacity)*elemSize), 0)
	arenaType := s.g.namedType("Arena")
	voidType := s.g.namedType("void")
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
	allocPtr := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{arenaRef, byteCount}, "darray.literal.alloc")
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
	countValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(literalCount), 0)
	capacityValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(literalCapacity), 0)
	current := C.LLVMGetUndef(llvmType)
	current = C.LLVMBuildInsertValue(s.builder, current, allocPtr, 0, cStringFree("darray.literal.items"))
	current = C.LLVMBuildInsertValue(s.builder, current, countValue, 1, cStringFree("darray.literal.count"))
	current = C.LLVMBuildInsertValue(s.builder, current, capacityValue, 2, cStringFree("darray.literal.capacity"))
	return current, darrayType, nil
}

func (s *functionState) attachDarrayAndComprehensionScope(inst C.LLVMValueRef, domainName string, aliasScopeName string, noAliasScopeName string) {
	if s == nil || s.g == nil || inst == nil {
		return
	}
	domainC := cString(domainName)
	defer C.free(unsafe.Pointer(domainC))
	aliasC := cString(aliasScopeName)
	defer C.free(unsafe.Pointer(aliasC))
	noAliasC := cString(noAliasScopeName)
	defer C.free(unsafe.Pointer(noAliasC))
	C.elisa_coreAttachDarrayAndComprehensionScope(inst, s.g.context, domainC, aliasC, noAliasC)
}

// darrayLiteralMinCapacity is the minimum element capacity reserved for a darray literal.
const darrayLiteralMinCapacity = 8

func (s *functionState) emitSpreadListLitExpr(expr *ast.ListLitExpr, darrayType *semantic.DArrayType) (C.LLVMValueRef, semantic.Type, error) {
	resultName := s.g.nextSyntheticName("list.spread.result.")
	resultInit := &ast.ListLitExpr{Position: expr.Position}
	if s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnExprTypesMap() {
		{
			key, value := resultInit, darrayType
			s.g.setResultExprType(key, value)
		}
	}
	resultIdent := &ast.Ident{Position: expr.Position, Name: resultName}
	body := make([]ast.Stmt, 0, len(expr.Elems))
	for i, elem := range expr.Elems {
		methodName := "push"
		if i < len(expr.Spreads) && expr.Spreads[i] {
			methodName = "extend"
		}
		call := &ast.CallExpr{
			Position: elem.Pos(),
			Func: &ast.FieldExpr{
				Position: expr.Position,
				Object:   resultIdent,
				Field:    methodName,
			},
			Args: []ast.Expr{elem},
		}
		body = append(body, &ast.ExprStmt{Position: elem.Pos(), Expr: call})
	}
	var stmts []ast.Stmt
	stmts = append(stmts, &ast.VarDeclStmt{Position: expr.Position, Name: resultName, Mutable: true, Value: resultInit})
	if expr.Owner != nil {
		stmts = append(stmts, &ast.InStoreStmt{Position: expr.Position, Store: expr.Owner, Body: body})
	} else {
		stmts = append(stmts, body...)
	}
	block := &ast.ExprBlock{
		Position: expr.Position,
		Stmts:    stmts,
		Value:    resultIdent,
	}
	return s.emitExprBlock(block, darrayType)
}
func (s *functionState) emitListComprehensionExpr(expr *ast.ListComprehensionExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr.LoweredParallel != nil {
		// `by par` map: the analyzer desugared this to a presize + parallel `par_map` fill (it knew
		// the element type). Emit that block in place of the sequential comprehension.
		return s.emitExpr(expr.LoweredParallel, s.exprType(expr))
	}
	if expr.Key != nil {
		return s.emitDictComprehensionExpr(expr)
	}
	if expr.Set {
		return s.emitSetComprehensionExpr(expr)
	}
	resultType, ok := s.exprType(expr).(*semantic.DArrayType)
	if !ok || resultType == nil {
		return nil, nil, fmt.Errorf("list comprehension requires a resolved darray result type")
	}
	resultName := s.g.nextSyntheticName("list.comp.result.")
	resultInit := &ast.ListLitExpr{Position: expr.Position, Owner: expr.Owner}
	if s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnExprTypesMap() {
		{
			key, value := resultInit, resultType
			s.g.setResultExprType(key, value)
		}
	}
	resultIdent := &ast.Ident{Position: expr.Position, Name: resultName}

	// task_00a7fdf3: a comprehension stored into a stack-tagged darray must build its synthetic
	// result in that SAME parallel arena, so the resized/pushed backing and a later grower's
	// reallocs share one arena (else the backing lands in the region base arena and the grower's
	// realloc in the parallel arena straddles → `assert a.end != null`). Alias the destination's
	// stack tag onto the synthetic result name (consumed once) so the desugar block's growth ops
	// route there via darrayGrowthOwner.
	if s.currentDArraySinkTag != "" {
		if s.darrayStackTag == nil {
			s.darrayStackTag = map[string]string{}
		}
		s.darrayStackTag[resultName] = s.currentDArraySinkTag
		s.currentDArraySinkTag = ""
		defer delete(s.darrayStackTag, resultName)
	}

	// Vectorizable fast path: a no-filter map over a re-evaluable darray source lowers to a
	// presized indexed-store loop (`result.resize(src.count); for i: result[i] <- value`)
	// rather than per-element `push`. push carries a per-iteration capacity check + conditional
	// realloc — a loop-carried control dependency LLVM cannot hoist, so the push build loop stays
	// scalar. The indexed store into the presized buffer has no such barrier and auto-vectorizes
	// at -O3 (docs/79; the proven "map into preallocated darray" shape).
	if block, ok := s.indexedStoreComprehensionBlock(expr, resultName, resultInit, resultIdent); ok {
		return s.emitExprBlock(block, resultType)
	}

	pushCall := &ast.CallExpr{
		Position: expr.Position,
		Func: &ast.FieldExpr{
			Position: expr.Position,
			Object:   resultIdent,
			Field:    "push",
		},
		Args: []ast.Expr{expr.Value},
	}
	block := comprehensionDesugarBlock(expr, resultName, resultInit, resultIdent, pushCall)
	return s.emitExprBlock(block, resultType)
}

// indexedStoreComprehensionBlock builds the vectorizable presized-indexed-store desugar for a
// list comprehension, or returns ok=false when the comprehension is not eligible. Eligible when:
// there is no filter (output length == source length), the source is a re-evaluable plain
// identifier (no double-eval side effects), and that source is a darray (integer-indexable with a
// `.count`). Lowers `[ value for name in src ]` to:
//
//	{ result: mutable darray[T] = []; result.resize(src.count);
//	  for __i in 0..<src.count: name = src[__i]; <bindings>; result[__i] <- value;
//	  result }
