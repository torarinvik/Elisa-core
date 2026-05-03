//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

void llctxSetBranchWeights(LLVMValueRef branch, LLVMContextRef ctx, unsigned trueWeight, unsigned falseWeight);
*/
import "C"

import (
	"fmt"
	"llcontext/src/ast"
	"llcontext/src/semantic"
)

func (s *functionState) emitTupleBindStmt(stmt *ast.TupleBindStmt) error {
	if stmt == nil {
		return nil
	}
	value, valueType, err := s.emitExpr(stmt.Value, nil)
	if err != nil {
		return err
	}
	if _, ok := semantic.StripAggregateStateType(valueType).(*semantic.TupleType); !ok {
		return fmt.Errorf("tuple destructuring requires a tuple value, got %s", valueType.String())
	}
	fields, err := s.g.structLiteralFields(valueType)
	if err != nil {
		return err
	}
	if len(stmt.Names) != len(fields) {
		return fmt.Errorf("tuple destructuring expects %d bindings, got %d", len(fields), len(stmt.Names))
	}
	limit := len(stmt.Names)
	if len(fields) < limit {
		limit = len(fields)
	}
	for i := 0; i < limit; i++ {
		name := stmt.Names[i].Name
		fieldValue := C.LLVMBuildExtractValue(s.builder, value, C.unsigned(i), cStringFree("tuple.field"))
		if name == "_" {
			continue
		}
		if stmt.Declare {
			alloca, err := s.createEntryAlloca(name, fields[i].Type)
			if err != nil {
				return err
			}
			s.defineBinding(name, valueBinding{ptr: alloca, typ: fields[i].Type, mutable: false})
			C.LLVMBuildStore(s.builder, fieldValue, alloca)
			continue
		}
		target := &ast.Ident{Position: stmt.Names[i].Position, Name: name}
		s.invalidatePackedEnumStorageExpr(target)
		s.invalidatePackedEnumStoreOriginExpr(target)
		s.invalidatePackedCommonFieldValuesExpr(target)
		s.invalidatePackedVariantViewExpr(target)
		binding, ok := s.lookupBinding(name)
		if !ok {
			return fmt.Errorf("unknown tuple destructuring target %q", name)
		}
		if !binding.mutable {
			return fmt.Errorf("cannot assign to immutable binding %q", name)
		}
		coerced, err := s.coerceValue(fieldValue, fields[i].Type, binding.typ)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, coerced, binding.ptr)
		s.bindPackedStoreValue(binding.typ, coerced)
		s.invalidatePackedReadCaches()
	}
	return nil
}
func moveBindFieldName(arg ast.MoveBindArg) string {
	if arg.Field != "" {
		return arg.Field
	}
	return arg.Name
}
func (s *functionState) emitLetDestructureStmt(stmt *ast.LetDestructureStmt) error {
	if stmt == nil || stmt.Pattern == nil || stmt.Value == nil {
		return nil
	}
	value, valueType, err := s.emitExpr(stmt.Value, nil)
	if err != nil {
		return err
	}
	if _, ok := semantic.StripAggregateStateType(valueType).(*semantic.StoreRowViewType); ok {
		tempName := s.g.nextSyntheticName("let.destructure.")
		tempAlloca, err := s.createEntryAlloca(tempName, valueType)
		if err != nil {
			return err
		}
		s.defineBinding(tempName, valueBinding{ptr: tempAlloca, typ: valueType, mutable: false})
		C.LLVMBuildStore(s.builder, value, tempAlloca)
		tempIdent := &ast.Ident{Position: stmt.Position, Name: tempName}
		for _, arg := range stmt.Pattern.Args {
			fieldExpr := &ast.FieldExpr{Position: arg.Position, Object: tempIdent, Field: moveBindFieldName(arg)}
			fieldValue, fieldType, err := s.emitExpr(fieldExpr, nil)
			if err != nil {
				return err
			}
			if err := s.emitMoveBindLocal(arg.Name, fieldType, fieldValue); err != nil {
				return err
			}
		}
		return nil
	}
	fields, err := s.g.structLiteralFields(valueType)
	if err != nil {
		return err
	}
	for _, arg := range stmt.Pattern.Args {
		fieldName := moveBindFieldName(arg)
		field, ok := lookupStructLiteralField(fields, fieldName)
		if !ok {
			return fmt.Errorf("unknown field %q in let destructuring", fieldName)
		}
		fieldValue := C.LLVMBuildExtractValue(s.builder, value, C.unsigned(field.Index), cStringFree("let.field"))
		if err := s.emitMoveBindLocal(arg.Name, field.Type, fieldValue); err != nil {
			return err
		}
	}
	return nil
}
func (s *functionState) emitMoveBindStmt(stmt *ast.MoveBindStmt) error {
	if stmt == nil {
		return nil
	}
	value, valueType, err := s.emitExpr(stmt.Value, nil)
	if err != nil {
		return err
	}
	switch p := stmt.Pattern.(type) {
	case *ast.MoveBindNamePattern:
		if err := s.emitMoveBindLocal(p.Name, valueType, value); err != nil {
			return err
		}
		if enumType, ok := valueType.(*semantic.EnumType); ok && enumType.Packed {
			origin, ok, err := s.resolvePackedNodeStoreBinding(stmt.Value, enumType)
			if err != nil {
				return err
			}
			if ok {
				s.bindPackedEnumStoreOrigin(p.Name, enumType, origin)
			}
		}
		return nil
	case *ast.MoveBindStructPattern:
		fields, err := s.g.structLiteralFields(valueType)
		if err != nil {
			return err
		}
		if p.Brace {
			for _, arg := range p.Args {
				fieldName := moveBindFieldName(arg)
				field, ok := lookupStructLiteralField(fields, fieldName)
				if !ok {
					return fmt.Errorf("unknown field %q in move-as destructuring", fieldName)
				}
				fieldValue := C.LLVMBuildExtractValue(s.builder, value, C.unsigned(field.Index), cStringFree("move.as.field"))
				if err := s.emitMoveBindLocal(arg.Name, field.Type, fieldValue); err != nil {
					return err
				}
			}
			return nil
		}
		limit := len(p.Args)
		if len(fields) < limit {
			limit = len(fields)
		}
		for i := 0; i < limit; i++ {
			fieldValue := C.LLVMBuildExtractValue(s.builder, value, C.unsigned(i), cStringFree("move.as.field"))
			if err := s.emitMoveBindLocal(p.Args[i].Name, fields[i].Type, fieldValue); err != nil {
				return err
			}
		}
		return nil
	case *ast.MoveBindVariantPattern:
		enumType, ok := resolveMatchableEnumType(valueType)
		if !ok {
			return fmt.Errorf("move-as variant pattern requires an enum value, got %s", valueType.String())
		}
		storeBinding, err := s.resolvePackedMatchStoreBinding(enumType, stmt.Value, stmt.Store)
		if err != nil {
			return err
		}
		successBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("move.as.variant.ok"))
		failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("move.as.variant.fail"))
		contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("move.as.variant.cont"))
		matchPattern := &ast.MatchVariantPattern{Position: p.Position, EnumName: p.EnumName, Variant: p.Variant, Args: append([]ast.MatchPatternArg(nil), p.Args...)}
		if _, _, err := s.emitMatchPatternTest(matchPattern, value, nil, enumType, storeBinding, stmt.Value, nil, successBB, failBB); err != nil {
			return err
		}
		C.LLVMPositionBuilderAtEnd(s.builder, successBB)
		if !s.currentBlockTerminated() {
			C.LLVMBuildBr(s.builder, contBB)
		}
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
		C.LLVMPositionBuilderAtEnd(s.builder, contBB)
		return nil
	default:
		return fmt.Errorf("unsupported move-as pattern %T", stmt.Pattern)
	}
}
func (s *functionState) emitDeferStmt(stmt *ast.DeferStmt) error {
	if stmt == nil {
		return nil
	}
	binding := scopedCleanupBinding{
		kind:      scopedCleanupDeferBody,
		deferBody: &deferredBodyBinding{stmt: stmt},
	}
	if stmt.Mode == ast.DeferModeFunction {
		captured, err := s.captureDeferFunctionScope(stmt)
		if err != nil {
			return err
		}
		binding.deferBody.captureScope = captured
		s.registerFunctionCleanup(binding)
		return nil
	}
	s.registerScopedCleanup(binding)
	return nil
}
func (s *functionState) emitScopeStmt(stmt *ast.ScopeStmt) error {
	if stmt == nil {
		return nil
	}
	guardType := s.exprType(stmt.Guard)
	if guardType == nil {
		return fmt.Errorf("missing semantic type for scope guard")
	}
	guardValue, _, err := s.emitExpr(stmt.Guard, guardType)
	if err != nil {
		return err
	}
	tempName := s.g.nextSyntheticName("scope.guard.")
	guardAlloca, err := s.createEntryAlloca(tempName, guardType)
	if err != nil {
		return err
	}
	C.LLVMBuildStore(s.builder, guardValue, guardAlloca)
	s.pushScope()
	scope := s.scope
	defer s.popScope()
	s.registerScopedCleanup(scopedCleanupBinding{kind: scopedCleanupValue, name: tempName, ptr: guardAlloca, typ: guardType})
	if err := s.emitBlock(stmt.Body, false); err != nil {
		s.discardScopeCleanups(scope)
		return err
	}
	if s.currentBlockTerminated() {
		s.discardScopeCleanups(scope)
		return nil
	}
	return s.emitScopeCleanups(scope)
}
func (s *functionState) emitBuiltinCheckpointDArray(target ast.Expr, targetType semantic.Type, name string) (checkpointBinding, error) {
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(targetType)
	if !ok || darrayType == nil {
		return checkpointBinding{}, fmt.Errorf("checkpoint %q requires region or darray target", name)
	}
	darrayPtr, _, err := s.emitBuiltinDArrayReceiverPtr(target, receiverRefType)
	if err != nil {
		return checkpointBinding{}, err
	}
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(darrayPtr, darrayType)
	if err != nil {
		return checkpointBinding{}, err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return checkpointBinding{}, err
	}
	countValue := C.LLVMBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree(name+".checkpoint.count"))
	markAlloca, err := s.createEntryAlloca(name+".checkpoint.mark", usizeType)
	if err != nil {
		return checkpointBinding{}, err
	}
	C.LLVMBuildStore(s.builder, countValue, markAlloca)
	return checkpointBinding{kind: checkpointBindingDArray, name: name, targetPtr: darrayPtr, targetType: darrayType, markPtr: markAlloca, markType: usizeType}, nil
}
func (s *functionState) emitRestoreCheckpointBinding(binding checkpointBinding) error {
	switch binding.kind {
	case checkpointBindingRegion:
		markValue, err := s.loadValue(binding.markPtr, binding.markType, binding.name)
		if err != nil {
			return err
		}
		return s.emitArenaRewind(binding.targetPtr, binding.targetType, markValue, binding.markType)
	case checkpointBindingDArray:
		darrayType, ok := binding.targetType.(*semantic.DArrayType)
		if !ok || darrayType == nil {
			return fmt.Errorf("checkpoint %q is not bound to a darray", binding.name)
		}
		countPtr, _, err := s.emitBuiltinDArrayCountPtr(binding.targetPtr, darrayType)
		if err != nil {
			return err
		}
		markValue, err := s.loadValue(binding.markPtr, binding.markType, binding.name)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, markValue, countPtr)
		return nil
	default:
		return fmt.Errorf("unsupported checkpoint kind %d", binding.kind)
	}
}
func (s *functionState) emitCheckpointBindingFromTarget(name string, target ast.Expr, targetType semantic.Type) (checkpointBinding, error) {
	var binding checkpointBinding
	if ident, ok := target.(*ast.Ident); ok {
		if regionBinding, ok := s.lookupBinding(ident.Name); ok && regionBinding.typ != nil && regionBinding.typ.String() == "Arena" {
			markType := s.g.result.NamedTypes["ArenaMark"]
			if markType == nil {
				return checkpointBinding{}, fmt.Errorf("missing builtin ArenaMark type for region checkpoints")
			}
			markAlloca, err := s.createEntryAlloca(name+".checkpoint.mark", markType)
			if err != nil {
				return checkpointBinding{}, err
			}
			markValue, err := s.emitArenaSnapshot(regionBinding.ptr, regionBinding.typ)
			if err != nil {
				return checkpointBinding{}, err
			}
			C.LLVMBuildStore(s.builder, markValue, markAlloca)
			binding = checkpointBinding{kind: checkpointBindingRegion, name: name, targetPtr: regionBinding.ptr, targetType: regionBinding.typ, markPtr: markAlloca, markType: markType}
		}
	}
	if binding.markPtr == nil {
		var err error
		binding, err = s.emitBuiltinCheckpointDArray(target, targetType, name)
		if err != nil {
			return checkpointBinding{}, err
		}
	}
	return binding, nil
}
func (s *functionState) emitCheckpointStmt(stmt *ast.CheckpointStmt) error {
	if stmt == nil {
		return nil
	}
	if s.checkpoints == nil {
		s.checkpoints = map[string]checkpointBinding{}
	}
	name := stmt.Name
	targetType := s.exprType(stmt.Target)
	binding, err := s.emitCheckpointBindingFromTarget(name, stmt.Target, targetType)
	if err != nil {
		return err
	}
	saved, hadSaved := s.checkpoints[name]
	s.checkpoints[name] = binding
	defer func() {
		if hadSaved {
			s.checkpoints[name] = saved
		} else {
			delete(s.checkpoints, name)
		}
	}()
	if len(stmt.Body) == 0 {
		return nil
	}
	if err := s.emitBlock(stmt.Body, true); err != nil {
		return err
	}
	if s.currentBlockTerminated() {
		return nil
	}
	return s.emitRestoreCheckpointBinding(binding)
}
func (s *functionState) emitGroupedCheckpointStmt(stmt *ast.GroupedCheckpointStmt) error {
	if stmt == nil {
		return nil
	}
	bindings := make([]checkpointBinding, 0, len(stmt.Targets))
	for i, target := range stmt.Targets {
		name := fmt.Sprintf("__grouped_checkpoint_%d_%d_%d", stmt.Position.Line, stmt.Position.Col, i)
		binding, err := s.emitCheckpointBindingFromTarget(name, target, s.exprType(target))
		if err != nil {
			return err
		}
		bindings = append(bindings, binding)
	}
	if err := s.emitBlock(stmt.Body, true); err != nil {
		return err
	}
	if s.currentBlockTerminated() {
		return nil
	}
	for i := len(bindings) - 1; i >= 0; i-- {
		if err := s.emitRestoreCheckpointBinding(bindings[i]); err != nil {
			return err
		}
	}
	return nil
}
func (s *functionState) emitRestoreCheckpointStmt(stmt *ast.RestoreCheckpointStmt) error {
	if stmt == nil {
		return nil
	}
	binding, ok := s.checkpoints[stmt.Name]
	if !ok {
		return fmt.Errorf("unknown checkpoint %q during LLVM lowering", stmt.Name)
	}
	return s.emitRestoreCheckpointBinding(binding)
}
func (s *functionState) emitMoveBindLocal(name string, typ semantic.Type, value C.LLVMValueRef) error {
	if name == "_" {
		return nil
	}
	alloca, err := s.createEntryAlloca(name, typ)
	if err != nil {
		return err
	}
	s.defineBinding(name, valueBinding{ptr: alloca, typ: typ})
	C.LLVMBuildStore(s.builder, value, alloca)
	s.bindPackedStoreValue(typ, value)
	return nil
}
func treeChildrenSourceInfo(sourceType semantic.Type) (*semantic.TreeCategoryType, *semantic.EnumVariant, bool) {
	switch tt := sourceType.(type) {
	case *semantic.TreeCategoryType:
		return tt, nil, tt != nil
	case *semantic.TreeVariantViewType:
		if tt == nil || tt.Category == nil || tt.Variant == nil {
			return nil, nil, false
		}
		return tt.Category, tt.Variant, true
	default:
		return nil, nil, false
	}
}
func treeChildrenExactSourceInfo(sourceType semantic.Type) (semantic.Type, *semantic.TreeType, bool) {
	switch tt := semantic.StripAggregateStateType(sourceType).(type) {
	case *semantic.TreeBlockType:
		return tt, tt.Family, tt != nil && tt.Family != nil
	case *semantic.TreeStructType:
		return tt, tt.Family, tt != nil && tt.Family != nil
	case *semantic.TreeNodeType:
		return tt, tt.Family, tt != nil && tt.Family != nil
	default:
		return nil, nil, false
	}
}
func (s *functionState) emitTreeChildrenTrapBlock(block C.LLVMBasicBlockRef) error {
	C.LLVMPositionBuilderAtEnd(s.builder, block)
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
	return nil
}
func (s *functionState) emitTreeStructuralSequenceCount(payloadValue C.LLVMValueRef, payloadType semantic.Type, name string) (C.LLVMValueRef, error) {
	if optionalType, ok := payloadType.(*semantic.OptionalType); ok {
		presentValue, err := s.extractOptionalPresent(payloadValue, optionalType)
		if err != nil {
			return nil, err
		}
		innerValue, err := s.extractOptionalPayload(payloadValue, optionalType)
		if err != nil {
			return nil, err
		}
		seqCount, err := s.emitTreeStructuralSequenceCount(innerValue, optionalType.Value, name+".optional")
		if err != nil {
			return nil, err
		}
		usizeLLVMType, err := s.g.lowerType(s.g.result.NamedTypes["usize"])
		if err != nil {
			return nil, err
		}
		zeroValue := C.LLVMConstInt(usizeLLVMType, 0, 0)
		return C.LLVMBuildSelect(s.builder, presentValue, seqCount, zeroValue, cStringFree(name+".tree.children.seq.count")), nil
	}
	tempAlloca, err := s.emitStackTempValue(payloadValue, payloadType, name+".tree.children.seq")
	if err != nil {
		return nil, err
	}
	return s.emitIterLoopCount(nil, tempAlloca, payloadType, name+".tree.children.seq")
}
func (s *functionState) emitTreeStructuralSequenceItemValue(payloadValue C.LLVMValueRef, payloadType semantic.Type, indexValue C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	if optionalType, ok := payloadType.(*semantic.OptionalType); ok {
		innerValue, err := s.extractOptionalPayload(payloadValue, optionalType)
		if err != nil {
			return nil, nil, err
		}
		return s.emitTreeStructuralSequenceItemValue(innerValue, optionalType.Value, indexValue, name+".optional")
	}
	tempAlloca, err := s.emitStackTempValue(payloadValue, payloadType, name+".tree.children.seq")
	if err != nil {
		return nil, nil, err
	}
	return s.emitIterLoopElementValue(nil, tempAlloca, payloadType, indexValue, name+".tree.children.seq")
}
func (s *functionState) coerceTreeChildrenItemValue(value C.LLVMValueRef, actualType semantic.Type, itemType semantic.Type) (C.LLVMValueRef, error) {
	if actualType == nil || itemType == nil {
		return value, nil
	}
	if !semantic.AssignableTo(itemType, actualType) {
		return nil, fmt.Errorf("tree structural child item type mismatch: expected %s, got %s", itemType.String(), actualType.String())
	}
	return s.coerceValue(value, actualType, itemType)
}
func (s *functionState) emitTreeVariantStructuralChildCount(nodeValue C.LLVMValueRef, categoryType *semantic.TreeCategoryType, variant *semantic.EnumVariant, name string) (C.LLVMValueRef, error) {
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	total := C.LLVMConstInt(usizeLLVMType, 0, 0)
	if variant == nil || !variant.HasStructuralPayloads() {
		return total, nil
	}
	payloadValues, err := s.extractTreeVariantPayloadValues(nodeValue, categoryType, variant)
	if err != nil {
		return nil, err
	}
	for payloadIndex, payloadType := range variant.Payload {
		switch variant.PayloadRelation(payloadIndex) {
		case ast.EnumPayloadRelationChild:
			childCount := C.LLVMConstInt(usizeLLVMType, 1, 0)
			if optionalType, ok := payloadType.(*semantic.OptionalType); ok {
				presentValue, err := s.extractOptionalPresent(payloadValues[payloadIndex], optionalType)
				if err != nil {
					return nil, err
				}
				zeroValue := C.LLVMConstInt(usizeLLVMType, 0, 0)
				childCount = C.LLVMBuildSelect(s.builder, presentValue, childCount, zeroValue, cStringFree(name+".tree.children.count"))
			}
			total = C.LLVMBuildAdd(s.builder, total, childCount, cStringFree(name+".tree.children.count"))
		case ast.EnumPayloadRelationChildren:
			seqCount, err := s.emitTreeStructuralSequenceCount(payloadValues[payloadIndex], payloadType, name)
			if err != nil {
				return nil, err
			}
			total = C.LLVMBuildAdd(s.builder, total, seqCount, cStringFree(name+".tree.children.count"))
		}
	}
	return total, nil
}
