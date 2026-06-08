//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>

static int elisacoreLLVMIsZeroValue(LLVMValueRef value) {
	return LLVMIsAConstant(value) != NULL && LLVMIsNull(value);
}

// Allow FP contraction (a*b+c -> fma) on a floating-point instruction, matching clang's default
// -ffp-contract=on. Only the AllowContract flag is set; no reassociation / no-nan / no-inf, so this
// does not change value semantics beyond the single-rounding fused multiply-add the C ABI permits.
static void elisacoreSetFPContract(LLVMValueRef v) {
	if (v != NULL && LLVMCanValueUseFastMathFlags(v)) {
		LLVMSetFastMathFlags(v, LLVMFastMathAllowContract);
	}
}

// Allow contraction AND reciprocal (a/b -> a * (1/b)) on a floating-point instruction, matching
// clang's -ffp-contract=fast -freciprocal-math. For a loop-invariant divisor this lets LICM hoist
// the single reciprocal out of the loop, turning a per-iteration fdiv (slow, poorly pipelined) into
// a multiply. This relaxes division rounding by up to ~1 ulp, the same relaxed-FP tier as contract.
static void elisacoreSetFPContractReciprocal(LLVMValueRef v) {
	if (v != NULL && LLVMCanValueUseFastMathFlags(v)) {
		LLVMSetFastMathFlags(v, LLVMFastMathAllowContract | LLVMFastMathAllowReciprocal);
	}
}

// Set ALL fast-math flags (reassoc, nnan, ninf, nsz, arcp, contract) on an FP instruction, matching
// clang -ffast-math. Enables FP reassociation -> auto-vectorization of reduction/elementwise loops.
// Used only inside functions annotated @fast_math (opt-in; reorders FP, results may differ).
static void elisacoreSetFPFast(LLVMValueRef v) {
	if (v != NULL && LLVMCanValueUseFastMathFlags(v)) {
		LLVMSetFastMathFlags(v, LLVMFastMathAll);
	}
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
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitListLitExpr(expr *ast.ListLitExpr, expected semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	// A `{k: v, ...}` brace literal is a populated dict literal: materialize an empty dict and
	// insert each pair (arena sourced from the active region scope).
	if expr.Brace && len(expr.Keys) > 0 {
		if dictType, ok := s.dictLiteralTargetType(expr, expected); ok {
			return s.emitDictLiteralExpr(expr, dictType)
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
	if expr.Owner != nil {
		var err error
		owner, ok, err = s.classifyTreeAllocOwnerExpr(expr.Owner)
		if err != nil {
			return nil, nil, err
		}
	} else {
		owner, ok = s.regionArenaOwner(darrayType.Region)
		if !ok {
			owner, ok = s.lookupTreeAllocOwner()
		}
	}
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, fmt.Errorf("darray literal requires an active in <arena>: scope")
	}
	arenaRef, err := s.treeOwnerArenaRefValue(owner, "darray.literal.owner.arena")
	if err != nil {
		return nil, nil, err
	}
	if arenaRef == nil {
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
	literalCount := len(expr.Elems)
	literalCapacity := literalCount
	if literalCapacity < 256 {
		literalCapacity = 256
	}
	byteCount := C.LLVMConstInt(usizeLLVMType, C.ulonglong(uint64(literalCapacity)*elemSize), 0)
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
func (s *functionState) emitSpreadListLitExpr(expr *ast.ListLitExpr, darrayType *semantic.DArrayType) (C.LLVMValueRef, semantic.Type, error) {
	resultName := s.g.nextSyntheticName("list.spread.result.")
	resultInit := &ast.ListLitExpr{Position: expr.Position}
	if s.g != nil && s.g.result != nil && s.g.result.ExprTypes != nil {
		s.g.result.ExprTypes[resultInit] = darrayType
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
	resultType, ok := s.exprType(expr).(*semantic.DArrayType)
	if !ok || resultType == nil {
		return nil, nil, fmt.Errorf("list comprehension requires a resolved darray result type")
	}
	resultName := s.g.nextSyntheticName("list.comp.result.")
	resultInit := &ast.ListLitExpr{Position: expr.Position, Owner: expr.Owner}
	if s.g != nil && s.g.result != nil && s.g.result.ExprTypes != nil {
		s.g.result.ExprTypes[resultInit] = resultType
	}
	resultIdent := &ast.Ident{Position: expr.Position, Name: resultName}
	pushCall := &ast.CallExpr{
		Position: expr.Position,
		Func: &ast.FieldExpr{
			Position: expr.Position,
			Object:   resultIdent,
			Field:    "push",
		},
		Args: []ast.Expr{expr.Value},
	}
	body := []ast.Stmt{&ast.ExprStmt{Position: expr.Position, Expr: pushCall}}
	var loopStmt ast.Stmt
	if expr.RangeEnd != nil {
		loopStmt = &ast.ForStmt{
			Position: expr.Position,
			Name:     expr.Name,
			Start:    expr.Source,
			End:      expr.RangeEnd,
			Step:     expr.RangeStep,
			Op:       expr.RangeOp,
			Body:     body,
		}
		if expr.Filter != nil {
			loopStmt.(*ast.ForStmt).Body = []ast.Stmt{&ast.IfStmt{Position: expr.Position, Cond: expr.Filter, Then: body}}
		}
	} else {
		loopStmt = &ast.IterForStmt{
			Position: expr.Position,
			Pattern:  &ast.MoveBindNamePattern{Position: expr.Position, Name: expr.Name},
			Mode:     ast.IterBindValue,
			Source:   expr.Source,
			Filter:   expr.Filter,
			Body:     body,
		}
	}
	block := &ast.ExprBlock{
		Position: expr.Position,
		Stmts: []ast.Stmt{
			&ast.VarDeclStmt{Position: expr.Position, Name: resultName, Mutable: true, Value: resultInit},
			loopStmt,
		},
		Value: resultIdent,
	}
	return s.emitExprBlock(block, resultType)
}
func (s *functionState) emitQueryExpr(expr *ast.QueryExpr) (C.LLVMValueRef, semantic.Type, error) {
	resultType := s.exprType(expr)
	if resultType == nil {
		return nil, nil, fmt.Errorf("query expression requires a resolved result type")
	}
	resultName := s.g.nextSyntheticName("query.result.")
	resultIdent := &ast.Ident{Position: expr.Position, Name: resultName}
	var init ast.Expr
	var body []ast.Stmt
	filter := expr.Filter
	patternFilter := expr.PatternFilter
	switch expr.Kind {
	case ast.QueryExprAny:
		init = &ast.BoolLit{Position: expr.Position, Value: false}
		body = []ast.Stmt{&ast.AssignStmt{Position: expr.Position, Target: resultIdent, Value: &ast.BoolLit{Position: expr.Position, Value: true}}}
	case ast.QueryExprAll:
		init = &ast.BoolLit{Position: expr.Position, Value: true}
		if expr.PatternFilter != nil {
			itemName := expr.Name
			if expr.PatternFilterSubject != "" {
				itemName = expr.PatternFilterSubject
			}
			item := &ast.Ident{Position: expr.Position, Name: itemName}
			failValue := ast.Expr(&ast.BoolLit{Position: expr.Position, Value: false})
			if expr.Filter != nil {
				failValue = &ast.UnaryExpr{Position: expr.Filter.Pos(), Op: lexer.TOKEN_NOT, Operand: expr.Filter}
			}
			wildcardValue := &ast.BoolLit{Position: expr.PatternFilter.Pos(), Value: true}
			filter = &ast.MatchExpr{
				Position: expr.PatternFilter.Pos(),
				Value:    item,
				Arms: []ast.MatchArm{
					{Position: expr.PatternFilter.Pos(), Pattern: expr.PatternFilter, Body: []ast.Stmt{&ast.ExprStmt{Position: failValue.Pos(), Expr: failValue}}},
					{Position: expr.PatternFilter.Pos(), Pattern: &ast.MatchWildcardPattern{Position: expr.PatternFilter.Pos()}, Body: []ast.Stmt{&ast.ExprStmt{Position: expr.PatternFilter.Pos(), Expr: wildcardValue}}},
				},
			}
			patternFilter = nil
			if s.g != nil && s.g.result != nil && s.g.result.ExprTypes != nil {
				boolType := s.g.result.NamedTypes["bool"]
				s.g.result.ExprTypes[failValue] = boolType
				s.g.result.ExprTypes[wildcardValue] = boolType
				s.g.result.ExprTypes[filter] = boolType
			}
		} else if expr.Filter != nil {
			filter = &ast.UnaryExpr{Position: expr.Position, Op: lexer.TOKEN_NOT, Operand: expr.Filter}
		}
		body = []ast.Stmt{&ast.AssignStmt{Position: expr.Position, Target: resultIdent, Value: &ast.BoolLit{Position: expr.Position, Value: false}}}
	case ast.QueryExprCount:
		init = &ast.IntLit{Position: expr.Position, Value: "0", Suffix: "usize"}
		body = []ast.Stmt{&ast.AugAssignStmt{Position: expr.Position, Op: lexer.TOKEN_PLUSEQ, Target: resultIdent, Value: &ast.IntLit{Position: expr.Position, Value: "1", Suffix: "usize"}}}
	case ast.QueryExprEach:
		darrayType, ok := resultType.(*semantic.DArrayType)
		if !ok || darrayType == nil {
			return nil, nil, fmt.Errorf("each query expression requires a darray result type")
		}
		init = &ast.ListLitExpr{Position: expr.Position, Owner: expr.Owner}
		if s.g != nil && s.g.result != nil && s.g.result.ExprTypes != nil {
			s.g.result.ExprTypes[init] = darrayType
		}
		value := expr.Projection
		if value == nil {
			value = &ast.Ident{Position: expr.Position, Name: expr.Name}
		}
		pushCall := &ast.CallExpr{
			Position: expr.Position,
			Func: &ast.FieldExpr{
				Position: expr.Position,
				Object:   resultIdent,
				Field:    "push",
			},
			Args: []ast.Expr{value},
		}
		body = []ast.Stmt{&ast.ExprStmt{Position: expr.Position, Expr: pushCall}}
	case ast.QueryExprFirst:
		optionalType, ok := resultType.(*semantic.OptionalType)
		if !ok || optionalType == nil {
			return nil, nil, fmt.Errorf("first query expression requires an optional result type")
		}
		init = &ast.NullLit{Position: expr.Position}
		if s.g != nil && s.g.result != nil && s.g.result.ExprTypes != nil {
			s.g.result.ExprTypes[init] = optionalType
		}
		nullCheck := &ast.BinaryExpr{Position: expr.Position, Op: lexer.TOKEN_EQEQ, Left: resultIdent, Right: &ast.NullLit{Position: expr.Position}}
		if expr.Filter != nil {
			filter = &ast.BinaryExpr{Position: expr.Position, Op: lexer.TOKEN_AND, Left: nullCheck, Right: expr.Filter}
		} else {
			filter = nullCheck
		}
		value := expr.Projection
		if value == nil {
			value = &ast.Ident{Position: expr.Position, Name: expr.Name}
		}
		body = []ast.Stmt{&ast.AssignStmt{Position: expr.Position, Target: resultIdent, Value: value}}
	default:
		return nil, nil, fmt.Errorf("unknown query expression kind %d", expr.Kind)
	}
	if s.g != nil && s.g.result != nil && s.g.result.ExprTypes != nil {
		s.g.result.ExprTypes[init] = resultType
	}
	pattern := ast.MoveBindPattern(&ast.MoveBindNamePattern{Position: expr.Position, Name: expr.Name})
	if expr.Pattern != nil {
		pattern = expr.Pattern
	}
	loopStmt := ast.Stmt(&ast.IterForStmt{
		Position:             expr.Position,
		Pattern:              pattern,
		Mode:                 ast.IterBindValue,
		Source:               expr.Source,
		PatternFilter:        patternFilter,
		PatternFilterSubject: expr.PatternFilterSubject,
		Filter:               filter,
		Body:                 body,
	})
	if expr.Owner != nil {
		loopStmt = &ast.InStoreStmt{Position: expr.Position, Store: expr.Owner, Body: []ast.Stmt{loopStmt}}
	}
	block := &ast.ExprBlock{
		Position: expr.Position,
		Stmts: []ast.Stmt{
			&ast.VarDeclStmt{Position: expr.Position, Name: resultName, Mutable: true, Value: init},
			loopStmt,
		},
		Value: resultIdent,
	}
	return s.emitExprBlock(block, resultType)
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
// dictLiteralTargetType resolves the dict type an empty `{}` literal should lower to, preferring
// the expected (target) type and falling back to the literal's own resolved type. Mirrors
// listLiteralTargetDArrayType for the dict analogue of `[]`.
func (s *functionState) dictLiteralTargetType(expr *ast.ListLitExpr, expected semantic.Type) (*semantic.DictType, bool) {
	if dictType, ok := semantic.StripAggregateStateType(expected).(*semantic.DictType); ok && dictType != nil {
		return dictType, true
	}
	if dictType, ok := semantic.StripAggregateStateType(s.exprType(expr)).(*semantic.DictType); ok && dictType != nil {
		return dictType, true
	}
	return nil, false
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
	// Route through storeValue so large `zeroed` aggregates lower to llvm.memset
	// (and large aggregate copies to memcpy) rather than a first-class
	// `store <bigtype> zeroinitializer`, which llc -O0 expands into a giant
	// per-element SelectionDAG (e.g. a 1MB struct took ~220s to compile).
	if err := s.storeValue(alloca, value, t, name); err != nil {
		return nil, err
	}
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
	// Auto-deref a reference operand to its numeric/bool pointee for `==`/`!=`.
	// binaryOperandType leaves a ref as-is for equality (so the genuine pointer/null
	// comparison in emitPointerCompareExpr keeps pointer operands); but that path has
	// already bailed by now, so any remaining ref-vs-value equality must compare the
	// pointee. Without this, `someRef != 300` (e.g. a `T&` returned inline from a call)
	// would emit the operand as a raw pointer and the icmp would compare the address.
	if expr.Op == lexer.TOKEN_EQEQ || expr.Op == lexer.TOKEN_BANGEQ {
		operandType = backendValueContextOperandType(operandType)
	}

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
			return s.fpContract(C.LLVMBuildFAdd(s.builder, left, right, cStringFree("addtmp"))), resultType, nil
		}
		return C.LLVMBuildAdd(s.builder, left, right, cStringFree("addtmp")), resultType, nil
	case lexer.TOKEN_MINUS:
		if isFloatType(operandType) {
			return s.fpContract(C.LLVMBuildFSub(s.builder, left, right, cStringFree("subtmp"))), resultType, nil
		}
		return C.LLVMBuildSub(s.builder, left, right, cStringFree("subtmp")), resultType, nil
	case lexer.TOKEN_STAR:
		if isFloatType(operandType) {
			return s.fpContract(C.LLVMBuildFMul(s.builder, left, right, cStringFree("multmp"))), resultType, nil
		}
		return C.LLVMBuildMul(s.builder, left, right, cStringFree("multmp")), resultType, nil
	case lexer.TOKEN_SLASH:
		if isFloatType(operandType) {
			return s.fpContractReciprocal(C.LLVMBuildFDiv(s.builder, left, right, cStringFree("divtmp"))), resultType, nil
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
	list, ok := s.membershipCandidateList(expr.Right)
	if !ok || list == nil {
		return nil, nil, fmt.Errorf("membership operator requires a list literal or tokenset on the right-hand side")
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
func (s *functionState) membershipCandidateList(expr ast.Expr) (*ast.ListLitExpr, bool) {
	if list, ok := expr.(*ast.ListLitExpr); ok {
		return list, true
	}
	ident, ok := expr.(*ast.Ident)
	if !ok || ident == nil || s == nil || s.g == nil || s.g.result == nil || s.g.result.GlobalScope == nil {
		return nil, false
	}
	sym, ok := s.g.result.GlobalScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return nil, false
	}
	switch decl := sym.Node.(type) {
	case *ast.TokenSetDecl:
		if decl == nil {
			return nil, false
		}
		return decl.Value, true
	case *ast.CharsetDecl:
		return s.backendCharsetMembershipList(decl), true
	default:
		return nil, false
	}
}

func (s *functionState) backendCharsetMembershipList(decl *ast.CharsetDecl) *ast.ListLitExpr {
	if decl == nil {
		return nil
	}
	elems := s.backendCharsetMembershipElems(decl.Terms, map[*ast.CharsetDecl]bool{decl: true})
	return &ast.ListLitExpr{Position: decl.Position, Elems: elems}
}

func (s *functionState) backendCharsetMembershipElems(terms []ast.LexerCharClassTerm, visiting map[*ast.CharsetDecl]bool) []ast.Expr {
	elems := make([]ast.Expr, 0, len(terms))
	for _, term := range terms {
		if term.Ref {
			ref := s.backendCharsetRef(term.Name)
			if ref == nil || visiting[ref] {
				continue
			}
			visiting[ref] = true
			elems = append(elems, s.backendCharsetMembershipElems(ref.Terms, visiting)...)
			delete(visiting, ref)
			continue
		}
		start := &ast.CharLit{Position: term.Position, Value: term.Start}
		if term.Range {
			elems = append(elems, &ast.MembershipRangeExpr{Position: term.Position, Start: start, End: &ast.CharLit{Position: term.Position, Value: term.End}, Op: lexer.TOKEN_RANGE})
			continue
		}
		elems = append(elems, start)
	}
	return elems
}

func (s *functionState) backendCharsetRef(name string) *ast.CharsetDecl {
	if s == nil || s.g == nil || s.g.result == nil || s.g.result.GlobalScope == nil {
		return nil
	}
	sym, ok := s.g.result.GlobalScope.Lookup(name)
	if !ok || sym == nil {
		return nil
	}
	ref, _ := sym.Node.(*ast.CharsetDecl)
	return ref
}
func (s *functionState) emitMembershipCompareValueAndExpr(leftValue C.LLVMValueRef, leftType semantic.Type, rightExpr ast.Expr) (C.LLVMValueRef, error) {
	if rangeExpr, ok := rightExpr.(*ast.MembershipRangeExpr); ok {
		return s.emitMembershipRangeCompareValueAndExpr(leftValue, leftType, rangeExpr)
	}
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

func (s *functionState) emitMembershipRangeCompareValueAndExpr(leftValue C.LLVMValueRef, leftType semantic.Type, rangeExpr *ast.MembershipRangeExpr) (C.LLVMValueRef, error) {
	if rangeExpr == nil {
		return nil, fmt.Errorf("membership range expression is nil")
	}
	startType := s.exprType(rangeExpr.Start)
	endType := s.exprType(rangeExpr.End)
	operandType := s.binaryOperandType(lexer.TOKEN_LTEQ, leftType, startType)
	operandType = s.binaryOperandType(lexer.TOKEN_LTEQ, operandType, endType)
	coercedLeft, err := s.coerceValue(leftValue, leftType, operandType)
	if err != nil {
		return nil, err
	}
	startValue, _, err := s.emitExpr(rangeExpr.Start, operandType)
	if err != nil {
		return nil, err
	}
	endValue, _, err := s.emitExpr(rangeExpr.End, operandType)
	if err != nil {
		return nil, err
	}
	lowerPred, err := llvmIntPredicate(lexer.TOKEN_GTEQ, operandType)
	if err != nil {
		return nil, err
	}
	upperOp := lexer.TOKEN_LTEQ
	if rangeExpr.Op == lexer.TOKEN_RANGE_LT {
		upperOp = lexer.TOKEN_LT
	}
	upperPred, err := llvmIntPredicate(upperOp, operandType)
	if err != nil {
		return nil, err
	}
	lower := C.LLVMBuildICmp(s.builder, lowerPred, coercedLeft, startValue, cStringFree("membership.range.lower"))
	upper := C.LLVMBuildICmp(s.builder, upperPred, coercedLeft, endValue, cStringFree("membership.range.upper"))
	return C.LLVMBuildAnd(s.builder, lower, upper, cStringFree("membership.range")), nil
}

// fpAllowContract marks a floating-point instruction as contraction-allowed so the backend may fuse
// multiply-add into fma (clang's default). Returns the same value for convenient inline use.
func fpAllowContract(v C.LLVMValueRef) C.LLVMValueRef {
	C.elisacoreSetFPContract(v)
	return v
}

// fpAllowContractReciprocal marks an FP instruction as contraction- and reciprocal-allowed, so the
// backend may hoist a loop-invariant divisor's reciprocal and replace per-iteration division with a
// multiply. Used for fdiv. Returns the same value for convenient inline use.
func fpAllowContractReciprocal(v C.LLVMValueRef) C.LLVMValueRef {
	C.elisacoreSetFPContractReciprocal(v)
	return v
}

// fnFastMath reports whether the enclosing function opted into full fast-math FP (@fast_math).
func (s *functionState) fnFastMath() bool {
	return s != nil && s.fnType != nil && s.fnType.FastMath
}

// fpContract applies contraction (FMA) by default, or full fast-math when the function is @fast_math.
func (s *functionState) fpContract(v C.LLVMValueRef) C.LLVMValueRef {
	if s.fnFastMath() {
		C.elisacoreSetFPFast(v)
		return v
	}
	return fpAllowContract(v)
}

// fpContractReciprocal applies contraction+reciprocal by default (for fdiv), or full fast-math when
// the function is @fast_math.
func (s *functionState) fpContractReciprocal(v C.LLVMValueRef) C.LLVMValueRef {
	if s.fnFastMath() {
		C.elisacoreSetFPFast(v)
		return v
	}
	return fpAllowContractReciprocal(v)
}
