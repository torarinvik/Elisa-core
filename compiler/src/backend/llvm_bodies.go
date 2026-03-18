//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"
	"sort"
	"strings"
	"unsafe"

	"llcontext/src/ast"
	"llcontext/src/semantic"
)

type valueBinding struct {
	ptr C.LLVMValueRef
	typ semantic.Type
}

type codegenScope struct {
	parent   *codegenScope
	bindings map[string]valueBinding
}

type functionState struct {
	g            *llvmGenerator
	decl         *ast.FuncDecl
	fnValue      C.LLVMValueRef
	fnType       *semantic.FuncType
	builder      C.LLVMBuilderRef
	scope        *codegenScope
	typeMap      map[string]semantic.Type
	resultSlot   C.LLVMValueRef
	regions      []regionBinding
	packedStores map[string]packedStoreBinding
}

type regionBinding struct {
	name string
	ptr  C.LLVMValueRef
	typ  semantic.Type
}

type packedStoreBinding struct {
	value C.LLVMValueRef
	typ   *semantic.PackedEnumStoreType
}

func (g *llvmGenerator) defineFunctionBody(decl *ast.FuncDecl, fnType *semantic.FuncType, fnValue C.LLVMValueRef) error {
	return g.defineFunctionBodyWithBindings(decl, fnType, fnValue, nil)
}

func (g *llvmGenerator) defineFunctionBodyWithBindings(decl *ast.FuncDecl, fnType *semantic.FuncType, fnValue C.LLVMValueRef, typeBindings map[string]semantic.Type) error {
	if decl == nil || fnType == nil || fnValue == nil {
		return fmt.Errorf("cannot define function body without declaration, type, and value")
	}
	if C.LLVMCountBasicBlocks(fnValue) != 0 {
		return nil
	}

	builder := C.LLVMCreateBuilderInContext(g.context)
	defer C.LLVMDisposeBuilder(builder)

	entryName := cString("entry")
	defer C.free(unsafe.Pointer(entryName))
	entry := C.LLVMAppendBasicBlockInContext(g.context, fnValue, entryName)
	C.LLVMPositionBuilderAtEnd(builder, entry)

	state := &functionState{
		g:            g,
		decl:         decl,
		fnValue:      fnValue,
		fnType:       fnType,
		builder:      builder,
		scope:        &codegenScope{bindings: map[string]valueBinding{}},
		typeMap:      typeBindings,
		packedStores: map[string]packedStoreBinding{},
	}

	paramOffset := 0
	if _, ok := nonVoidErrorUnion(fnType.Return); ok {
		state.resultSlot = C.LLVMGetParam(fnValue, 0)
		paramOffset = 1
	}

	for i, param := range decl.Params {
		if i >= len(fnType.Params) {
			break
		}
		alloca, err := state.createEntryAlloca(param.Name, fnType.Params[i])
		if err != nil {
			return err
		}
		paramValue := C.LLVMGetParam(fnValue, C.unsigned(i+paramOffset))
		C.LLVMBuildStore(builder, paramValue, alloca)
		state.defineBinding(param.Name, valueBinding{ptr: alloca, typ: fnType.Params[i]})
	}

	if err := state.emitBlock(decl.Body, false); err != nil {
		return err
	}

	if !state.currentBlockTerminated() {
		if err := state.emitRegionCleanup(); err != nil {
			return err
		}
		if isVoidType(fnType.Return) {
			C.LLVMBuildRetVoid(builder)
		} else if retUnion, ok := fnType.Return.(*semantic.ErrorUnionType); ok && isVoidType(retUnion.Value) {
			zeroCode, err := state.errorCodeConstant(0)
			if err != nil {
				return err
			}
			C.LLVMBuildRet(builder, zeroCode)
		} else {
			return fmt.Errorf("function %s may fall through without returning a value", decl.Name)
		}
	}

	return nil
}

func (s *functionState) emitFunctionReturn(value C.LLVMValueRef, actual semantic.Type) error {
	if err := s.emitRegionCleanup(); err != nil {
		return err
	}
	if retUnion, ok := s.fnType.Return.(*semantic.ErrorUnionType); ok {
		coerced, err := s.coerceValue(value, actual, retUnion)
		if err != nil {
			return err
		}
		if isVoidType(retUnion.Value) {
			C.LLVMBuildRet(s.builder, coerced)
			return nil
		}
		if s.resultSlot == nil {
			return fmt.Errorf("function %s is missing a hidden return slot for %s", s.decl.Name, retUnion.String())
		}
		errorCode, err := s.extractErrorUnionCode(coerced, retUnion)
		if err != nil {
			return err
		}
		payload, err := s.extractErrorUnionPayload(coerced, retUnion)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, payload, s.resultSlot)
		C.LLVMBuildRet(s.builder, errorCode)
		return nil
	}
	coerced, err := s.coerceValue(value, actual, s.fnType.Return)
	if err != nil {
		return err
	}
	C.LLVMBuildRet(s.builder, coerced)
	return nil
}

func (s *functionState) emitRegionCleanup() error {
	for i := len(s.regions) - 1; i >= 0; i-- {
		if err := s.emitArenaFree(s.regions[i].ptr, s.regions[i].typ); err != nil {
			return err
		}
	}
	return nil
}

func (s *functionState) emitBlock(stmts []ast.Stmt, scoped bool) error {
	if scoped {
		s.pushScope()
		defer s.popScope()
	}
	for _, stmt := range stmts {
		if s.currentBlockTerminated() {
			break
		}
		if err := s.emitStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *functionState) emitStmt(stmt ast.Stmt) error {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		var declType semantic.Type
		var err error
		if n.Type != nil {
			declType, err = s.resolveTypeExpr(n.Type)
			if err != nil {
				return err
			}
		} else if n.Value != nil {
			declType = s.exprType(n.Value)
			if declType == nil {
				return fmt.Errorf("cannot infer type for variable %s", n.Name)
			}
		} else {
			return fmt.Errorf("variable %s requires a type or initializer", n.Name)
		}
		alloca, err := s.createEntryAlloca(n.Name, declType)
		if err != nil {
			return err
		}
		s.defineBinding(n.Name, valueBinding{ptr: alloca, typ: declType})
		if n.Value != nil {
			value, _, err := s.emitExpr(n.Value, declType)
			if err != nil {
				return err
			}
			C.LLVMBuildStore(s.builder, value, alloca)
		}
		return nil
	case *ast.RegionStmt:
		arenaType := s.g.result.NamedTypes["Arena"]
		if arenaType == nil {
			return fmt.Errorf("missing builtin Arena type for region %s", n.Name)
		}
		alloca, err := s.createEntryAlloca(n.Name, arenaType)
		if err != nil {
			return err
		}
		zero, err := s.zeroValue(arenaType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, zero, alloca)
		s.defineBinding(n.Name, valueBinding{ptr: alloca, typ: arenaType})
		s.regions = append(s.regions, regionBinding{name: n.Name, ptr: alloca, typ: arenaType})
		return s.emitRegionInit(alloca, arenaType, n.Capacity)
	case *ast.DestroyStmt:
		binding, ok := s.lookupBinding(n.Name)
		if !ok {
			return fmt.Errorf("unknown region %q during LLVM lowering", n.Name)
		}
		return s.emitArenaFree(binding.ptr, binding.typ)
	case *ast.AssignStmt:
		ptr, targetType, err := s.emitAddress(n.Target)
		if err != nil {
			return err
		}
		value, _, err := s.emitExpr(n.Value, targetType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, value, ptr)
		return nil
	case *ast.AsRefAssignStmt:
		ptr, targetType, err := s.emitAddress(n.Target)
		if err != nil {
			return err
		}
		value, _, err := s.emitExpr(n.Value, targetType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, value, ptr)
		return nil
	case *ast.AugAssignStmt:
		ptr, targetType, err := s.emitAddress(n.Target)
		if err != nil {
			return err
		}
		current, err := s.loadValue(ptr, targetType, "aug.cur")
		if err != nil {
			return err
		}
		value, _, err := s.emitExpr(n.Value, targetType)
		if err != nil {
			return err
		}
		result, err := s.emitAugmentedValue(n.Op, current, value, targetType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, result, ptr)
		return nil
	case *ast.ReturnStmt:
		if n.Value == nil {
			if retUnion, ok := s.fnType.Return.(*semantic.ErrorUnionType); ok && isVoidType(retUnion.Value) {
				zeroCode, err := s.errorCodeConstant(0)
				if err != nil {
					return err
				}
				C.LLVMBuildRet(s.builder, zeroCode)
				return nil
			}
			C.LLVMBuildRetVoid(s.builder)
			return nil
		}
		value, valueType, err := s.emitExpr(n.Value, nil)
		if err != nil {
			return err
		}
		return s.emitFunctionReturn(value, valueType)
	case *ast.IfStmt:
		return s.emitIf(n)
	case *ast.MatchStmt:
		return s.emitMatch(n)
	case *ast.InStoreStmt:
		return s.emitInStore(n)
	case *ast.WhileStmt:
		return s.emitWhile(n)
	case *ast.PassStmt:
		return nil
	case *ast.PanicStmt:
		if n.Message != nil {
			if _, _, err := s.emitExpr(n.Message, nil); err != nil {
				return err
			}
		}
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
	case *ast.ExprStmt:
		_, _, err := s.emitExpr(n.Expr, nil)
		return err
	case *ast.DiscardStmt:
		_, _, err := s.emitExpr(n.Value, nil)
		return err
	case *ast.StaticIfStmt:
		return s.emitStaticIf(n)
	case *ast.StaticErrorStmt:
		return fmt.Errorf("static error should not reach LLVM lowering")
	default:
		return fmt.Errorf("unsupported statement %T", stmt)
	}
}

func (s *functionState) emitInStore(stmt *ast.InStoreStmt) error {
	storeValue, actualType, err := s.emitExpr(stmt.Store, nil)
	if err != nil {
		return err
	}
	storeType, ok := actualType.(*semantic.PackedEnumStoreType)
	if !ok {
		return fmt.Errorf("in-store block requires a packed enum store, got %s", actualType.String())
	}
	savedStores := s.packedStores
	s.packedStores = s.clonePackedStores()
	if s.packedStores == nil {
		s.packedStores = map[string]packedStoreBinding{}
	}
	s.packedStores[storeType.Enum.Name] = packedStoreBinding{value: storeValue, typ: storeType}
	defer func() {
		s.packedStores = savedStores
	}()
	return s.emitBlock(stmt.Body, true)
}

func (s *functionState) emitRegionInit(arenaPtr C.LLVMValueRef, arenaType semantic.Type, capacityExpr ast.Expr) error {
	capacityType := s.g.result.NamedTypes["usize"]
	var capacityValue C.LLVMValueRef
	if capacityExpr != nil {
		value, _, err := s.emitExpr(capacityExpr, capacityType)
		if err != nil {
			return err
		}
		capacityValue = value
	} else {
		usizeLLVMType, err := s.g.lowerType(capacityType)
		if err != nil {
			return err
		}
		capacityValue = C.LLVMConstInt(usizeLLVMType, 8*1024, 0)
	}
	regionType := s.g.result.NamedTypes["Region"]
	if regionType == nil {
		return fmt.Errorf("missing builtin Region type for region initialization")
	}
	regionRefType := &semantic.RefType{Elem: regionType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{Name: "new_region", Params: []semantic.Type{capacityType}, Return: regionRefType}
	callee, err := s.g.ensureFunctionDeclared("new_region", helperType)
	if err != nil {
		return err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return err
	}
	regionValue := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{capacityValue}, "region.init")
	arenaLLVMType, err := s.g.lowerType(arenaType)
	if err != nil {
		return err
	}
	beginPtr := C.LLVMBuildStructGEP2(s.builder, arenaLLVMType, arenaPtr, 0, cStringFree("region.begin"))
	endPtr := C.LLVMBuildStructGEP2(s.builder, arenaLLVMType, arenaPtr, 1, cStringFree("region.end"))
	C.LLVMBuildStore(s.builder, regionValue, beginPtr)
	C.LLVMBuildStore(s.builder, regionValue, endPtr)
	return nil
}

func (s *functionState) emitArenaFree(arenaPtr C.LLVMValueRef, arenaType semantic.Type) error {
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	helperType := &semantic.FuncType{Name: "arena_free", Params: []semantic.Type{arenaRefType}, Return: s.g.result.NamedTypes["void"]}
	callee, err := s.g.ensureFunctionDeclared("arena_free", helperType)
	if err != nil {
		return err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return err
	}
	s.buildCall(llvmFnType, callee, []C.LLVMValueRef{arenaPtr}, "")
	return nil
}

func (s *functionState) emitIf(stmt *ast.IfStmt) error {
	stmt = normalizeIf(stmt)
	condValue, _, err := s.emitExpr(stmt.Cond, s.g.result.NamedTypes["bool"])
	if err != nil {
		return err
	}

	thenName := cString("if.then")
	defer C.free(unsafe.Pointer(thenName))
	thenBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, thenName)

	mergeName := cString("if.end")
	defer C.free(unsafe.Pointer(mergeName))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, mergeName)

	var elseBB C.LLVMBasicBlockRef
	if len(stmt.Else) > 0 {
		elseName := cString("if.else")
		defer C.free(unsafe.Pointer(elseName))
		elseBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, elseName)
		C.LLVMBuildCondBr(s.builder, condValue, thenBB, elseBB)
	} else {
		C.LLVMBuildCondBr(s.builder, condValue, thenBB, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, thenBB)
	if err := s.emitBlock(stmt.Then, true); err != nil {
		return err
	}
	thenTerminated := s.currentBlockTerminated()
	if !thenTerminated {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	elseTerminated := false
	if len(stmt.Else) > 0 {
		C.LLVMPositionBuilderAtEnd(s.builder, elseBB)
		if err := s.emitBlock(stmt.Else, true); err != nil {
			return err
		}
		elseTerminated = s.currentBlockTerminated()
		if !elseTerminated {
			C.LLVMBuildBr(s.builder, mergeBB)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(stmt.Else) > 0 && thenTerminated && elseTerminated {
		C.LLVMBuildUnreachable(s.builder)
	}
	return nil
}

func (s *functionState) emitWhile(stmt *ast.WhileStmt) error {
	condName := cString("while.cond")
	defer C.free(unsafe.Pointer(condName))
	condBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, condName)

	bodyName := cString("while.body")
	defer C.free(unsafe.Pointer(bodyName))
	bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, bodyName)

	exitName := cString("while.end")
	defer C.free(unsafe.Pointer(exitName))
	exitBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, exitName)

	C.LLVMBuildBr(s.builder, condBB)
	C.LLVMPositionBuilderAtEnd(s.builder, condBB)
	condValue, _, err := s.emitExpr(stmt.Cond, s.g.result.NamedTypes["bool"])
	if err != nil {
		return err
	}
	C.LLVMBuildCondBr(s.builder, condValue, bodyBB, exitBB)

	C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
	if err := s.emitBlock(stmt.Body, true); err != nil {
		return err
	}
	if !s.currentBlockTerminated() {
		C.LLVMBuildBr(s.builder, condBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, exitBB)
	return nil
}

func (s *functionState) emitMatch(stmt *ast.MatchStmt) error {
	enumType, ok := s.exprType(stmt.Value).(*semantic.EnumType)
	if !ok {
		return fmt.Errorf("match requires an enum value")
	}
	if stmt.Store != nil {
		if _, _, err := s.emitExpr(stmt.Store, nil); err != nil {
			return err
		}
	} else if enumType.Packed {
		if _, ok := s.lookupPackedStore(enumType); !ok {
			return fmt.Errorf("missing active packed enum store for %s", enumType.Name)
		}
	}
	enumValue, _, err := s.emitExpr(stmt.Value, enumType)
	if err != nil {
		return err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.fail"))
	allTerminated := true

	for i, arm := range stmt.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(stmt.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.next"))
		}
		if err := s.emitMatchPatternTest(arm.Pattern, enumValue, enumType, bodyBB, nextBB); err != nil {
			return err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if err := s.emitBlock(arm.Body, false); err != nil {
			s.popScope()
			return err
		}
		s.popScope()
		if !s.currentBlockTerminated() {
			allTerminated = false
			C.LLVMBuildBr(s.builder, mergeBB)
		}

		if nextBB != mergeBB {
			C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if matchIsExhaustive(enumType, stmt.Arms) {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if allTerminated && matchIsExhaustive(enumType, stmt.Arms) {
		C.LLVMBuildUnreachable(s.builder)
	}
	return nil
}

func (s *functionState) emitMatchExpr(expr *ast.MatchExpr) (C.LLVMValueRef, semantic.Type, error) {
	resultType := s.exprType(expr)
	enumType, ok := s.exprType(expr.Value).(*semantic.EnumType)
	if !ok {
		return nil, nil, fmt.Errorf("match requires an enum value")
	}
	if expr.Store != nil {
		if _, _, err := s.emitExpr(expr.Store, nil); err != nil {
			return nil, nil, err
		}
	} else if enumType.Packed {
		if _, ok := s.lookupPackedStore(enumType); !ok {
			return nil, nil, fmt.Errorf("missing active packed enum store for %s", enumType.Name)
		}
	}
	enumValue, _, err := s.emitExpr(expr.Value, enumType)
	if err != nil {
		return nil, nil, err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(expr.Arms))
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(expr.Arms))
	for i, arm := range expr.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(expr.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.expr.next"))
		}
		if err := s.emitMatchPatternTest(arm.Pattern, enumValue, enumType, bodyBB, nextBB); err != nil {
			return nil, nil, err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		armValue, reachable, err := s.emitMatchExprArmBody(arm.Body, resultType)
		if err != nil {
			s.popScope()
			return nil, nil, err
		}
		if reachable && !s.currentBlockTerminated() {
			armEnd := C.LLVMGetInsertBlock(s.builder)
			incomingValues = append(incomingValues, armValue)
			incomingBlocks = append(incomingBlocks, armEnd)
			C.LLVMBuildBr(s.builder, mergeBB)
		}
		s.popScope()

		if nextBB != mergeBB {
			C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if semantic.IsNeverType(resultType) {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		llvmType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, nil, err
		}
		undefValue := C.LLVMGetUndef(llvmType)
		failEnd := C.LLVMGetInsertBlock(s.builder)
		incomingValues = append(incomingValues, undefValue)
		incomingBlocks = append(incomingBlocks, failEnd)
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingValues) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	if len(incomingValues) == 1 || semantic.IsNeverType(resultType) {
		return incomingValues[0], resultType, nil
	}
	llvmType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, llvmType, cStringFree("match.expr.phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}

func (s *functionState) emitMatchExprArmBody(body []ast.Stmt, resultType semantic.Type) (C.LLVMValueRef, bool, error) {
	if len(body) == 0 {
		return nil, false, fmt.Errorf("match expression arm must end with an expression")
	}
	for i, stmt := range body {
		isLast := i == len(body)-1
		if !isLast {
			if err := s.emitStmt(stmt); err != nil {
				return nil, false, err
			}
			if s.currentBlockTerminated() {
				return nil, false, nil
			}
			continue
		}
		if exprStmt, ok := stmt.(*ast.ExprStmt); ok {
			value, _, err := s.emitExpr(exprStmt.Expr, resultType)
			if err != nil {
				return nil, false, err
			}
			return value, true, nil
		}
		if err := s.emitStmt(stmt); err != nil {
			return nil, false, err
		}
		if s.currentBlockTerminated() {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("match expression arm must end with an expression")
	}
	return nil, false, fmt.Errorf("match expression arm must end with an expression")
}

func (s *functionState) emitMatchPatternTest(pattern ast.MatchPattern, actualValue C.LLVMValueRef, actualType semantic.Type, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) error {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		C.LLVMBuildBr(s.builder, successBB)
		return nil
	case *ast.MatchBindPattern:
		alloca, err := s.createEntryAlloca(p.Name, actualType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, actualValue, alloca)
		s.defineBinding(p.Name, valueBinding{ptr: alloca, typ: actualType})
		C.LLVMBuildBr(s.builder, successBB)
		return nil
	case *ast.MatchVariantPattern:
		enumType, ok := actualType.(*semantic.EnumType)
		if !ok {
			return fmt.Errorf("variant pattern %s.%s requires enum type, got %s", p.EnumName, p.Variant, actualType.String())
		}
		variant, ok := enumType.Variant(p.Variant)
		if !ok {
			return fmt.Errorf("enum %s has no variant %s", enumType.Name, p.Variant)
		}
		tagValue, err := s.extractEnumTagValue(actualValue, enumType)
		if err != nil {
			return err
		}
		tagConst, err := s.enumTagConstant(variant.Tag)
		if err != nil {
			return err
		}
		matchedBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.pattern.ok"))
		pred := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), tagValue, tagConst, cStringFree("match.tag"))
		C.LLVMBuildCondBr(s.builder, pred, matchedBB, failureBB)

		C.LLVMPositionBuilderAtEnd(s.builder, matchedBB)
		orderedArgs, err := s.resolveMatchPatternArgs(p, variant)
		if err != nil {
			return err
		}
		if len(orderedArgs) == 0 {
			C.LLVMBuildBr(s.builder, successBB)
			return nil
		}
		payloadValues, err := s.extractEnumVariantPayloadValues(actualValue, enumType, variant)
		if err != nil {
			return err
		}
		for i := range orderedArgs {
			if orderedArgs[i] == nil {
				continue
			}
			nextSuccess := successBB
			if i != len(orderedArgs)-1 {
				nextSuccess = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.pattern.next"))
			}
			if err := s.emitMatchPatternTest(orderedArgs[i].Pattern, payloadValues[i], variant.Payload[i], nextSuccess, failureBB); err != nil {
				return err
			}
			if i != len(orderedArgs)-1 {
				C.LLVMPositionBuilderAtEnd(s.builder, nextSuccess)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported match pattern %T", pattern)
	}
}

func (s *functionState) resolveMatchPatternArgs(pattern *ast.MatchVariantPattern, variant *semantic.EnumVariant) ([]*ast.MatchPatternArg, error) {
	ordered := make([]*ast.MatchPatternArg, len(variant.Payload))
	if len(pattern.Args) == 0 {
		return ordered, nil
	}
	namedCount := 0
	for i := range pattern.Args {
		if pattern.Args[i].Name != "" {
			namedCount++
		}
	}
	if namedCount == 0 {
		if len(pattern.Args) != len(variant.Payload) {
			return nil, fmt.Errorf("match arm %s.%s expects %d payload patterns, got %d", pattern.EnumName, pattern.Variant, len(variant.Payload), len(pattern.Args))
		}
		for i := range pattern.Args {
			ordered[i] = &pattern.Args[i]
		}
		return ordered, nil
	}
	if namedCount != len(pattern.Args) {
		return nil, fmt.Errorf("match arm %s.%s cannot mix positional and named payload patterns", pattern.EnumName, pattern.Variant)
	}
	if !variant.HasNamedPayloads() {
		return nil, fmt.Errorf("match arm %s.%s uses named payload patterns but the variant payloads are unnamed", pattern.EnumName, pattern.Variant)
	}
	seen := map[int]bool{}
	for i := range pattern.Args {
		arg := &pattern.Args[i]
		index, ok := variant.PayloadIndex(arg.Name)
		if !ok {
			return nil, fmt.Errorf("match arm %s.%s has no payload field %q", pattern.EnumName, pattern.Variant, arg.Name)
		}
		if seen[index] {
			return nil, fmt.Errorf("match arm %s.%s matches payload field %q more than once", pattern.EnumName, pattern.Variant, arg.Name)
		}
		seen[index] = true
		ordered[index] = arg
	}
	missing := make([]string, 0)
	for i := range ordered {
		if ordered[i] == nil {
			missing = append(missing, variant.PayloadLabel(i))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("match arm %s.%s is missing named payload patterns for: %s", pattern.EnumName, pattern.Variant, strings.Join(missing, ", "))
	}
	return ordered, nil
}

func (s *functionState) extractEnumTagValue(enumValue C.LLVMValueRef, enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	if enumType != nil && enumType.Packed {
		return s.loadEnumTag(enumValue, enumType)
	}
	if enumIsTagOnly(enumType) {
		return enumValue, nil
	}
	return C.LLVMBuildExtractValue(s.builder, enumValue, 0, cStringFree("match.tag.value")), nil
}

func (s *functionState) extractEnumVariantPayloadValues(enumValue C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant) ([]C.LLVMValueRef, error) {
	if variant == nil || len(variant.Payload) == 0 {
		return nil, nil
	}
	if enumType != nil && enumType.Packed {
		return s.loadEnumVariantPayload(enumValue, enumType, variant)
	}
	enumPtr, err := s.emitStackTempValue(enumValue, enumType, "match.payload.tmp")
	if err != nil {
		return nil, err
	}
	return s.loadEnumVariantPayload(enumPtr, enumType, variant)
}

func matchIsExhaustive(enumType *semantic.EnumType, arms []ast.MatchArm) bool {
	if enumType == nil {
		return false
	}
	covered := map[string]bool{}
	for _, arm := range arms {
		switch pattern := arm.Pattern.(type) {
		case *ast.MatchWildcardPattern:
			return true
		case *ast.MatchVariantPattern:
			covered[pattern.Variant] = true
		}
	}
	return len(covered) == len(enumType.Variants)
}

func (s *functionState) loadEnumTag(enumPtr C.LLVMValueRef, enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	if enumType != nil && !enumType.Packed && enumIsTagOnly(enumType) {
		tagType, err := s.g.lowerBuiltin("u32")
		if err != nil {
			return nil, err
		}
		return C.LLVMBuildLoad2(s.builder, tagType, enumPtr, cStringFree("match.tag.value")), nil
	}
	enumLLVMType, err := s.loweredEnumStorageType(enumType)
	if err != nil {
		return nil, err
	}
	tagPtr := C.LLVMBuildStructGEP2(s.builder, enumLLVMType, enumPtr, 0, cStringFree("match.tag.ptr"))
	tagType, err := s.g.lowerBuiltin("u32")
	if err != nil {
		return nil, err
	}
	return C.LLVMBuildLoad2(s.builder, tagType, tagPtr, cStringFree("match.tag.value")), nil
}

func (s *functionState) loadEnumVariantPayload(enumPtr C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant) ([]C.LLVMValueRef, error) {
	if variant == nil || len(variant.Payload) == 0 {
		return nil, nil
	}
	payloadPtr, err := s.enumPayloadPtr(enumPtr, enumType)
	if err != nil {
		return nil, err
	}
	payloadType, err := s.g.lowerEnumVariantPayloadType(variant)
	if err != nil {
		return nil, err
	}
	if len(variant.Payload) == 1 {
		value := C.LLVMBuildLoad2(s.builder, payloadType, payloadPtr, cStringFree("match.payload"))
		return []C.LLVMValueRef{value}, nil
	}
	aggregate := C.LLVMBuildLoad2(s.builder, payloadType, payloadPtr, cStringFree("match.payload"))
	values := make([]C.LLVMValueRef, 0, len(variant.Payload))
	for i := range variant.Payload {
		values = append(values, C.LLVMBuildExtractValue(s.builder, aggregate, C.unsigned(i), cStringFree("match.payload.field")))
	}
	return values, nil
}

func (s *functionState) enumPayloadPtr(enumPtr C.LLVMValueRef, enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	if enumIsTagOnly(enumType) {
		return nil, fmt.Errorf("enum %s has no lowered payload storage", enumType.Name)
	}
	enumLLVMType, err := s.loweredEnumStorageType(enumType)
	if err != nil {
		return nil, err
	}
	payloadIndex := 1
	if enumType != nil && enumType.Packed {
		payloadIndex, err = s.g.packedEnumPayloadFieldIndex(enumType)
		if err != nil {
			return nil, err
		}
	}
	return C.LLVMBuildStructGEP2(s.builder, enumLLVMType, enumPtr, C.unsigned(payloadIndex), cStringFree("enum.payload.ptr")), nil
}

func (s *functionState) enumTagConstant(tag uint32) (C.LLVMValueRef, error) {
	tagType, err := s.g.lowerBuiltin("u32")
	if err != nil {
		return nil, err
	}
	return C.LLVMConstInt(tagType, C.ulonglong(tag), 0), nil
}

func (s *functionState) emitStaticIf(stmt *ast.StaticIfStmt) error {
	branch, err := s.activeStmtBranch(stmt)
	if err != nil {
		return err
	}
	for _, inner := range branch {
		if s.currentBlockTerminated() {
			break
		}
		if err := s.emitStmt(inner); err != nil {
			return err
		}
	}
	return nil
}

func (s *functionState) activeStmtBranch(stmt *ast.StaticIfStmt) ([]ast.Stmt, error) {
	selected, ok := s.evalConstBoolExpr(stmt.Cond)
	if !ok {
		return nil, fmt.Errorf("static if condition must be a compile-time bool")
	}
	if selected {
		return stmt.Then, nil
	}
	for _, elif := range stmt.Elifs {
		selected, ok := s.evalConstBoolExpr(elif.Cond)
		if !ok {
			return nil, fmt.Errorf("static elif condition must be a compile-time bool")
		}
		if selected {
			return elif.Body, nil
		}
	}
	return stmt.Else, nil
}
