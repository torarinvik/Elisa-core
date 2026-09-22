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

func (s *functionState) emitTernaryExpr(expr *ast.TernaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	resultType := s.exprType(expr)
	parentScope := s.scope
	condScope, hasConditionBindings, err := s.createConditionBindingScope(expr.Cond)
	if err != nil {
		return nil, nil, err
	}
	if hasConditionBindings {
		s.scope = condScope
	}
	parentBlock := C.LLVMGetInsertBlock(s.builder)
	thenBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("ternary.then"))
	elseBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("ternary.else"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("ternary.end"))
	if err := s.emitConditionBranchWithBindings(expr.Cond, thenBB, elseBB, ast.BranchHintNone); err != nil {
		s.scope = parentScope
		return nil, nil, err
	}

	C.LLVMPositionBuilderAtEnd(s.builder, thenBB)
	if hasConditionBindings {
		s.scope = condScope
	}
	leftValue, _, err := s.emitExpr(expr.Value, resultType)
	if err != nil {
		s.scope = parentScope
		return nil, nil, err
	}
	// An arm that already terminated (`raise`, or a diverging `else: return -1` branch —
	// docs/119 §4.1) never reaches the merge, so it contributes no phi incoming.
	thenEnd := C.LLVMGetInsertBlock(s.builder)
	thenReaches := C.LLVMGetBasicBlockTerminator(thenEnd) == nil
	if thenReaches {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, elseBB)
	s.scope = parentScope
	rightValue, _, err := s.emitExpr(expr.Alt, resultType)
	if err != nil {
		s.scope = parentScope
		return nil, nil, err
	}
	elseEnd := C.LLVMGetInsertBlock(s.builder)
	elseReaches := C.LLVMGetBasicBlockTerminator(elseEnd) == nil
	if elseReaches {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	s.scope = parentScope
	// Both arms void (`f(x) if cond else g(x)`): the arms were emitted for their effects
	// and there is no value to merge. A phi of void is not a legal instruction, so build
	// none — the merge block itself is the whole result. Void cannot be bound or operated
	// on, so this expression only ever appears in statement position, where the discarded
	// value is never read.
	if isVoidType(resultType) {
		return nil, resultType, nil
	}
	if !thenReaches && !elseReaches {
		C.LLVMBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	phiType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, phiType, cStringFree("termp"))
	values := []C.LLVMValueRef{}
	blocks := []C.LLVMBasicBlockRef{}
	if thenReaches {
		values = append(values, leftValue)
		blocks = append(blocks, thenEnd)
	}
	if elseReaches {
		values = append(values, rightValue)
		blocks = append(blocks, elseEnd)
	}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	_ = parentBlock
	return phi, resultType, nil
}
func (s *functionState) emitAddrOfExpr(expr *ast.AddrOfExpr) (C.LLVMValueRef, semantic.Type, error) {
	if slot, refType, ok := s.referenceBindingSlot(expr); ok {
		return slot, &semantic.RefType{Elem: refType, State: semantic.RefStateNonNull}, nil
	}
	return s.emitPlaceAddrOfExpr(expr)
}

// emitPlaceAddrOfExpr lowers `&place` as the address of the place itself, reading
// a reference binding as the place it refers to (as `r.field` does). A cast of
// `&x` means exactly this; see emitCastExpr.
func (s *functionState) emitPlaceAddrOfExpr(expr *ast.AddrOfExpr) (C.LLVMValueRef, semantic.Type, error) {
	ptr, operandType, err := s.emitAddress(expr.Operand)
	if err != nil {
		return nil, nil, err
	}
	return ptr, &semantic.RefType{Elem: operandType, State: semantic.RefStateNonNull}, nil
}

// referenceBindingSlot handles `&r` where `r` is an immutable reference binding
// (`r: T&`, `r: mutable T&`). The analyzer types that expression `T&&` -- a
// reference to the reference -- and rejects it wherever a `T&` is expected, so
// wherever it survives as a value the destination really is `T&&` (a `T&&`
// parameter, a generic `T`, an inferred local) and the value must be the
// address of r's own slot. emitAddress auto-dereferences a reference binding, which is
// right for its other callers (`r.field`, `r[i]`, stores through `r`) but here
// handed the callee the referent: `takes_rr(&counter)` then dereferenced the
// Counter's first word as a pointer.
//
// The operand of a cast is not a value: `(&r).cast[T&]` is the reborrow
// idiom, and emitCastExpr lowers it through emitPlaceAddrOfExpr instead. The
// backend's own synthesized nodes (emitLockStmt's `(&mu).cast[Mutex&]`) are
// casts too, and have no recorded type besides -- only the ANALYZER's type is
// consulted here, since the fallback typing in functionState.exprType would
// call any `&r` a `T&&`.
func (s *functionState) referenceBindingSlot(expr *ast.AddrOfExpr) (C.LLVMValueRef, semantic.Type, bool) {
	if expr == nil {
		return nil, nil, false
	}
	operand := expr.Operand
	for {
		paren, ok := operand.(*ast.ParenExpr)
		if !ok || paren == nil {
			break
		}
		operand = paren.Inner
	}
	ident, ok := operand.(*ast.Ident)
	if !ok || ident == nil {
		return nil, nil, false
	}
	binding, ok := s.lookupBinding(ident.Name)
	// A mutable reference binding already yields its slot from emitAddress.
	if !ok || binding.mutable {
		return nil, nil, false
	}
	if _, ok := binding.typ.(*semantic.RefType); !ok {
		return nil, nil, false
	}
	var analyzed semantic.Type
	if specialized, ok := s.specializedExprTypes[expr]; ok && specialized != nil {
		analyzed = specialized
	} else {
		analyzed = s.g.exprType(expr)
	}
	outer, ok := analyzed.(*semantic.RefType)
	if !ok || outer == nil {
		return nil, nil, false
	}
	if _, ok := outer.Elem.(*semantic.RefType); !ok {
		return nil, nil, false
	}
	return binding.ptr, binding.typ, true
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
		resolved, err := s.resolveGenericArgForParam(arg, params[i])
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
	// A large literal is built in MEMORY rather than as a first-class aggregate.
	// `insertvalue` on a multi-KB struct makes LLVM scalarize the field it is
	// inserting: a struct holding a 3.5KB field came out of the O2 pipeline as
	// 3,200 byte-wise loads and 3,223 insertvalues in ONE basic block, and
	// SelectionDAG is superlinear in block size -- 98% of an eighty-six second
	// compile was AArch64 instruction selection on that one block. Built in
	// memory, each big field goes in through storeValue (which already lowers a
	// large aggregate to memcpy) and the single load at the end is turned into a
	// memcpy by whatever stores it.
	if sizeBytes, sizeErr := s.g.abiSizeOfType(structType); sizeErr == nil && sizeBytes >= largeStructLiteralMemoryThresholdBytes {
		return s.emitStructLitExprInMemory(expr, structType, llvmType, fields)
	}
	value := C.LLVMGetUndef(llvmType)
	for i, spread := range expr.Spreads {
		if i > 0 {
			return nil, nil, fmt.Errorf("struct literal lowering supports at most one non-pack struct spread")
		}
		spreadValue, spreadType, err := s.emitExpr(spread, structType)
		if err != nil {
			return nil, nil, err
		}
		if !semantic.AssignableTo(structType, spreadType) {
			return nil, nil, fmt.Errorf("struct literal spread expects %s, got %s", structType, spreadType)
		}
		value = spreadValue
	}
	args := expr.LoweredArgs()
	for _, i := range structLitEvalOrder(expr, args) {
		arg := args[i]
		if i >= len(fields) {
			continue
		}
		if arg == nil {
			if len(expr.Spreads) != 0 {
				continue
			}
			return nil, nil, fmt.Errorf("struct literal field %d was not resolved", i)
		}
		fieldValue, _, err := s.emitExpr(arg, fields[i].Type)
		if err != nil {
			return nil, nil, err
		}
		value = C.LLVMBuildInsertValue(s.builder, value, fieldValue, C.unsigned(i), cStringFree("ins"))
	}
	if err := s.emitStructInvariantChecks(value, structType); err != nil {
		return nil, nil, err
	}
	return value, structType, nil
}

// largeStructLiteralMemoryThresholdBytes is the size at/above which a struct
// literal is assembled through memory instead of `insertvalue`. Matched to
// largeAggregateCopyMemcpyThresholdBytes so that a field big enough to be
// memcpy'd is also big enough to be written straight into its slot.
const largeStructLiteralMemoryThresholdBytes = largeAggregateCopyMemcpyThresholdBytes

// emitStructLitExprInMemory assembles a struct literal field by field into a
// stack slot and loads it back once. Semantically identical to the
// `insertvalue` form -- Elisa aggregates are POD -- but every large field moves
// as a memcpy rather than as thousands of scalar loads in one basic block.
//
// The slot is not zeroed first. Without a spread every field is written (the
// value form errors when one is missing, and so does this), and with one the
// spread overwrites the whole struct before the fields go in, so the only bytes
// left untouched are PADDING -- which the value form leaves as `undef` anyway
// and which nothing reads: struct equality is field-wise, and `memcmp` is used
// only for byte views.
func (s *functionState) emitStructLitExprInMemory(expr *ast.StructLitExpr, structType semantic.Type, llvmType C.LLVMTypeRef, fields []structLiteralField) (C.LLVMValueRef, semantic.Type, error) {
	alloca, err := s.createTempStorage("structlit", structType)
	if err != nil {
		return nil, nil, err
	}
	for i, spread := range expr.Spreads {
		if i > 0 {
			return nil, nil, fmt.Errorf("struct literal lowering supports at most one non-pack struct spread")
		}
		spreadValue, spreadType, err := s.emitExpr(spread, structType)
		if err != nil {
			return nil, nil, err
		}
		if !semantic.AssignableTo(structType, spreadType) {
			return nil, nil, fmt.Errorf("struct literal spread expects %s, got %s", structType, spreadType)
		}
		if err := s.storeValue(alloca, spreadValue, structType, "structlit.spread"); err != nil {
			return nil, nil, err
		}
	}
	args := expr.LoweredArgs()
	for _, i := range structLitEvalOrder(expr, args) {
		arg := args[i]
		if i >= len(fields) {
			continue
		}
		if arg == nil {
			// Supplied by the spread already stored above; without one the
			// value form errors here, and so does this.
			if len(expr.Spreads) != 0 {
				continue
			}
			return nil, nil, fmt.Errorf("struct literal field %d was not resolved", i)
		}
		fieldValue, _, err := s.emitExpr(arg, fields[i].Type)
		if err != nil {
			return nil, nil, err
		}
		fieldPtr := C.LLVMBuildStructGEP2(s.builder, llvmType, alloca, C.unsigned(i), cStringFree("structlit.field.ptr"))
		if err := s.storeValue(fieldPtr, fieldValue, fields[i].Type, "structlit.field"); err != nil {
			return nil, nil, err
		}
	}
	if st, ok := structType.(*semantic.StructType); ok {
		if err := s.emitStructInvariantChecksAt(alloca, st); err != nil {
			return nil, nil, err
		}
	}
	return C.LLVMBuildLoad2(s.builder, llvmType, alloca, cStringFree("structlit.val")), structType, nil
}

// backendStructTypeOf unwraps a struct type, or a reference to one, to its *StructType (else nil).
func backendStructTypeOf(t semantic.Type) *semantic.StructType {
	switch v := t.(type) {
	case *semantic.StructType:
		return v
	case *semantic.RefType:
		if st, ok := v.Elem.(*semantic.StructType); ok {
			return st
		}
	}
	return nil
}

// emitStructInvariantChecks verifies a struct value's field invariants (debug builds only) by
// materializing it to an addressable temp, binding `self` to it, and checking each invariant. Used
// right after construction and after a field store. `zeroed` construction bypasses this (it never
// reaches emitStructLitExpr), which is the intended low-level escape.
func (s *functionState) emitStructInvariantChecks(value C.LLVMValueRef, structType semantic.Type) error {
	st, ok := structType.(*semantic.StructType)
	if !ok || st.Decl == nil || len(st.Decl.Invariants) == 0 {
		return nil
	}
	if s.g.optLevel != OptimizationLevel0 && !s.g.forceContracts {
		return nil
	}
	alloca, err := s.createEntryAlloca("self.inv", structType)
	if err != nil {
		return err
	}
	C.LLVMBuildStore(s.builder, value, alloca)
	return s.emitStructInvariantChecksAt(alloca, st)
}

// emitStructInvariantChecksAt checks each invariant with `self` bound to an existing struct address
// (used after a field store, where the struct is already addressable). Assumes debug-gating already
// decided by the caller for the value form; re-checks it here so the address form is safe too.
func (s *functionState) emitStructInvariantChecksAt(structPtr C.LLVMValueRef, st *semantic.StructType) error {
	if st == nil || st.Decl == nil || len(st.Decl.Invariants) == 0 {
		return nil
	}
	if s.g.optLevel != OptimizationLevel0 && !s.g.forceContracts {
		return nil
	}
	s.pushScope()
	defer s.popScope()
	s.defineBinding("self", valueBinding{ptr: structPtr, typ: st, mutable: false})
	for _, inv := range st.Decl.Invariants {
		if inv == nil {
			continue
		}
		if err := s.emitContractCheck(inv, "struct invariant failed"); err != nil {
			return err
		}
	}
	return nil
}
func (s *functionState) emitRecordUpdateExpr(expr *ast.RecordUpdateExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || expr.Base == nil {
		return nil, nil, fmt.Errorf("invalid record update")
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
func (s *functionState) rewriteDefaultExactMemberType(_ ast.Expr) (semantic.Type, bool) {
	return nil, false
}
func (s *functionState) emitTreeExactMemberUpdateExpr(_ *ast.RecordUpdateExpr, memberType semantic.Type, _ *treeAllocOwnerBinding) (C.LLVMValueRef, semantic.Type, error) {
	return nil, nil, fmt.Errorf("tree exact member update is no longer supported for %s", memberType.String())
}

// structLitEvalOrder returns the declared field index of each initializer in the order
// the initializers were WRITTEN. A struct literal evaluates its initializers
// left-to-right as written (docs/18), whatever order the fields are declared in:
// stage1 always did, stage0 walked the declared order, and the compiler's own
// `Runtime{...}` literal -- whose initializers each declare a runtime symbol --
// then declared them in a generation-dependent order and broke the gen3 fixpoint.
func structLitEvalOrder(expr *ast.StructLitExpr, lowered []ast.Expr) []int {
	order := make([]int, 0, len(lowered))
	seen := make(map[int]bool, len(lowered))
	for _, written := range expr.Args {
		for i, arg := range lowered {
			if arg == written && !seen[i] {
				order = append(order, i)
				seen[i] = true
				break
			}
		}
	}
	for i := range lowered {
		if !seen[i] {
			order = append(order, i)
		}
	}
	return order
}
