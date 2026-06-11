//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
	"strings"
	"unsafe"
)

func (s *functionState) invalidatePackedCommonFieldValues(name string) {
	if name == "" {
		return
	}
	for scope := s.scope; scope != nil; scope = scope.parent {
		if key := scope.packedCommonValueName; key == name || strings.HasPrefix(key, name+".") {
			scope.packedCommonValueName = ""
			scope.packedCommonValueBinding = packedCommonFieldValueBinding{}
		}
		for key := range scope.packedCommonValues {
			if key == name || strings.HasPrefix(key, name+".") {
				delete(scope.packedCommonValues, key)
			}
		}
	}
}
func packedVariantViewTargetName(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name, true
	case *ast.ParenExpr:
		return packedVariantViewTargetName(n.Inner)
	default:
		return "", false
	}
}
func (s *functionState) invalidatePackedVariantViewExpr(expr ast.Expr) {
	if name, ok := packedVariantViewTargetName(expr); ok {
		s.invalidatePackedVariantView(name)
	}
}
func (s *functionState) invalidatePackedCommonFieldValuesExpr(expr ast.Expr) {
	if path, ok := s.packedEnumStoragePath(expr); ok {
		s.invalidatePackedCommonFieldValues(path)
	}
}
func (s *functionState) updatePackedVariantViewDecodedPtr(name string, ptr C.LLVMValueRef) {
	if name == "" || ptr == nil {
		return
	}
	for scope := s.scope; scope != nil; scope = scope.parent {
		if scope.packedViewName == name {
			binding := scope.packedViewBinding
			if binding.typ == nil {
				continue
			}
			binding.ptr = ptr
			binding.ptrBlock = C.LLVMGetInsertBlock(s.builder)
			scope.packedViewBinding = binding
			return
		}
		binding, ok := scope.packedViewPtrs[name]
		if !ok {
			continue
		}
		binding.ptr = ptr
		scope.packedViewPtrs[name] = binding
		return
	}
}
func (s *functionState) lookupPackedEnumStorage(name string, enumType *semantic.EnumType) (C.LLVMValueRef, bool) {
	if name == "" || enumType == nil || !enumType.Packed {
		return nil, false
	}
	for scope := s.scope; scope != nil; scope = scope.parent {
		binding, ok := scope.packedEnumPtrs[name]
		if !ok {
			continue
		}
		if binding.typ == enumType && binding.ptr != nil && (binding.block == nil || binding.block == C.LLVMGetInsertBlock(s.builder)) {
			return binding.ptr, true
		}
	}
	return nil, false
}
func (s *functionState) bindPackedEnumStoreOrigin(name string, enumType *semantic.EnumType, store *packedStoreBinding) {
	if name == "" || enumType == nil || !enumType.Packed || store == nil || store.typ == nil || store.value == nil {
		return
	}
	if store.typ.Enum != enumType {
		return
	}
	if s.scope == nil {
		s.scope = &codegenScope{}
	}
	if s.scope.packedEnumStoreName == "" || s.scope.packedEnumStoreName == name {
		s.scope.packedEnumStoreName = name
		s.scope.packedEnumStoreBinding = *store
		return
	}
	if s.scope.packedEnumStores == nil {
		s.scope.packedEnumStores = map[string]packedStoreBinding{}
	}
	s.scope.packedEnumStores[name] = *store
}
func (s *functionState) lookupPackedEnumStoreOrigin(name string, enumType *semantic.EnumType) (packedStoreBinding, bool) {
	if name == "" || enumType == nil || !enumType.Packed {
		return packedStoreBinding{}, false
	}
	for scope := s.scope; scope != nil; scope = scope.parent {
		if scope.packedEnumStoreName == name {
			binding := scope.packedEnumStoreBinding
			if binding.typ == enumTypeStoreType(enumType, binding.typ) && binding.value != nil {
				return binding, true
			}
		}
		binding, ok := scope.packedEnumStores[name]
		if !ok {
			continue
		}
		if binding.typ == enumTypeStoreType(enumType, binding.typ) && binding.value != nil {
			return binding, true
		}
	}
	return packedStoreBinding{}, false
}
func enumTypeStoreType(enumType *semantic.EnumType, storeType *semantic.PackedEnumStoreType) *semantic.PackedEnumStoreType {
	if enumType == nil || storeType == nil || storeType.Enum != enumType {
		return nil
	}
	return storeType
}
func (s *functionState) invalidatePackedEnumStorage(name string) {
	if name == "" {
		return
	}
	for scope := s.scope; scope != nil; scope = scope.parent {
		for key := range scope.packedEnumPtrs {
			if key == name || strings.HasPrefix(key, name+".") {
				delete(scope.packedEnumPtrs, key)
			}
		}
	}
}
func (s *functionState) invalidatePackedEnumStoreOrigin(name string) {
	if name == "" {
		return
	}
	for scope := s.scope; scope != nil; scope = scope.parent {
		if key := scope.packedEnumStoreName; key == name || strings.HasPrefix(key, name+".") {
			scope.packedEnumStoreName = ""
			scope.packedEnumStoreBinding = packedStoreBinding{}
		}
		for key := range scope.packedEnumStores {
			if key == name || strings.HasPrefix(key, name+".") {
				delete(scope.packedEnumStores, key)
			}
		}
	}
}
func (s *functionState) invalidatePackedEnumStorageExpr(expr ast.Expr) {
	if path, ok := s.packedEnumStoragePath(expr); ok {
		s.invalidatePackedEnumStorage(path)
	}
}
func (s *functionState) invalidatePackedEnumStoreOriginExpr(expr ast.Expr) {
	if path, ok := s.packedEnumStoragePath(expr); ok {
		s.invalidatePackedEnumStoreOrigin(path)
	}
}
func (s *functionState) emitConstValue(value semantic.ConstValue) (C.LLVMValueRef, semantic.Type, error) {
	return s.emitConstValueWithType(value, nil)
}
func (s *functionState) emitConstValueWithType(value semantic.ConstValue, actual semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	switch value.Kind {
	case semantic.ConstInt:
		resultType := actual
		if resultType == nil || semantic.IsInvalidType(resultType) {
			resultType = s.g.result.NamedTypes["int"]
		}
		llvmType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, nil, err
		}
		return C.LLVMConstInt(llvmType, C.ulonglong(value.Int), boolToLLVMBool(value.Int < 0)), resultType, nil
	case semantic.ConstFloat:
		resultType := actual
		if resultType == nil || semantic.IsInvalidType(resultType) {
			resultType = s.g.result.NamedTypes["f64"]
		}
		llvmType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, nil, err
		}
		return C.LLVMConstReal(llvmType, C.double(value.Float)), resultType, nil
	case semantic.ConstBool:
		llvmType, err := s.g.lowerBuiltin("bool")
		if err != nil {
			return nil, nil, err
		}
		var raw C.ulonglong
		if value.Bool {
			raw = 1
		}
		return C.LLVMConstInt(llvmType, raw, 0), s.g.result.NamedTypes["bool"], nil
	case semantic.ConstString:
		name := cString("cstr")
		defer C.free(unsafe.Pointer(name))
		text := cString(value.String)
		defer C.free(unsafe.Pointer(text))
		return C.LLVMBuildGlobalStringPtr(s.builder, text, name), &semantic.RefType{Elem: s.g.result.NamedTypes["u8"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageStatic, ExplicitStorage: true}, nil
	default:
		return nil, nil, fmt.Errorf("unsupported const kind %d", value.Kind)
	}
}
func (s *functionState) zeroValue(t semantic.Type) (C.LLVMValueRef, error) {
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		return nil, err
	}
	return C.LLVMConstNull(llvmType), nil
}
func (s *functionState) ensureTrapFunction() (C.LLVMValueRef, error) {
	if value, ok := s.g.functions["llvm.trap"]; ok {
		return value, nil
	}
	voidType := s.g.result.NamedTypes["void"]
	trapType := &semantic.FuncType{Name: "llvm.trap", Return: voidType}
	return s.g.ensureFunctionDeclared("llvm.trap", trapType)
}
func (s *functionState) emitTrapUnreachable(name string) error {
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
func (s *functionState) ensureAbortFunction() (C.LLVMValueRef, *semantic.FuncType, error) {
	voidType := s.g.result.NamedTypes["void"]
	fnType := &semantic.FuncType{Name: "abort", Return: voidType}
	value, err := s.g.ensureFunctionDeclared("abort", fnType)
	if err != nil {
		return nil, nil, err
	}
	return value, fnType, nil
}
func (s *functionState) ensurePrintfFunction() (C.LLVMValueRef, *semantic.FuncType, error) {
	voidType := s.g.result.NamedTypes["void"]
	intType := s.g.result.NamedTypes["int"]
	u8RefType := &semantic.RefType{Elem: s.g.result.NamedTypes["u8"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	_ = voidType
	fnType := &semantic.FuncType{Name: "printf", Params: []semantic.Type{u8RefType}, Return: intType, Variadic: true}
	value, err := s.g.ensureFunctionDeclared("printf", fnType)
	if err != nil {
		return nil, nil, err
	}
	return value, fnType, nil
}
func (s *functionState) ensureBacktraceFunction() (C.LLVMValueRef, *semantic.FuncType, error) {
	intType := s.g.result.NamedTypes["int"]
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	fnType := &semantic.FuncType{Name: "backtrace", Params: []semantic.Type{voidRefType, intType}, Return: intType}
	value, err := s.g.ensureFunctionDeclared("backtrace", fnType)
	if err != nil {
		return nil, nil, err
	}
	return value, fnType, nil
}
func (s *functionState) ensureBacktraceSymbolsFDFunction() (C.LLVMValueRef, *semantic.FuncType, error) {
	voidType := s.g.result.NamedTypes["void"]
	intType := s.g.result.NamedTypes["int"]
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	fnType := &semantic.FuncType{Name: "backtrace_symbols_fd", Params: []semantic.Type{voidRefType, intType, intType}, Return: voidType}
	value, err := s.g.ensureFunctionDeclared("backtrace_symbols_fd", fnType)
	if err != nil {
		return nil, nil, err
	}
	return value, fnType, nil
}
func (s *functionState) emitPanicWithBacktrace(pos lexer.Pos, message ast.Expr) error {
	fileName := strings.TrimSpace(pos.File)
	if fileName == "" {
		fileName = "<unknown>"
	}
	if s.cleanupDepth == 0 {
		if err := s.emitActiveScopedCleanup(); err != nil {
			return err
		}
		if s.currentBlockTerminated() {
			return nil
		}
	}

	defaultMessage := s.emitGlobalCStringLiteral("panic", "panic.default.message")
	messagePtr := defaultMessage
	if message != nil {
		value, valueType, err := s.emitExpr(message, nil)
		if err != nil {
			return err
		}
		coerced, ok, err := s.coercePanicMessageToCString(value, valueType, defaultMessage)
		if err != nil {
			return err
		}
		if ok {
			messagePtr = coerced
		}
	}

	printfFn, printfType, err := s.ensurePrintfFunction()
	if err != nil {
		return err
	}
	printfLLVMType, err := s.g.lowerFunctionType(printfType)
	if err != nil {
		return err
	}
	formatPtr := s.emitGlobalCStringLiteral("panic at %s:%d:%d: %s\n", "panic.format")
	filePtr := s.emitGlobalCStringLiteral(fileName, "panic.file")
	intLLVMType, err := s.g.lowerType(s.g.result.NamedTypes["int"])
	if err != nil {
		return err
	}
	lineValue := C.LLVMConstInt(intLLVMType, C.ulonglong(pos.Line), 0)
	colValue := C.LLVMConstInt(intLLVMType, C.ulonglong(pos.Col), 0)
	s.buildCall(printfLLVMType, printfFn, []C.LLVMValueRef{formatPtr, filePtr, lineValue, colValue, messagePtr}, "")

	backtraceFn, backtraceType, err := s.ensureBacktraceFunction()
	if err != nil {
		return err
	}
	backtraceLLVMType, err := s.g.lowerFunctionType(backtraceType)
	if err != nil {
		return err
	}
	bufferType := C.LLVMArrayType2(C.LLVMPointerTypeInContext(s.g.context, 0), 64)
	buffer := C.LLVMBuildAlloca(s.builder, bufferType, cStringFree("panic.backtrace.buffer"))
	bufferPtr := C.LLVMBuildBitCast(s.builder, buffer, C.LLVMPointerTypeInContext(s.g.context, 0), cStringFree("panic.backtrace.ptr"))
	frameLimit := C.LLVMConstInt(intLLVMType, 64, 0)
	frameCount := s.buildCall(backtraceLLVMType, backtraceFn, []C.LLVMValueRef{bufferPtr, frameLimit}, "panic.backtrace.count")

	backtraceHeader := s.emitGlobalCStringLiteral("backtrace:\n", "panic.backtrace.header")
	s.buildCall(printfLLVMType, printfFn, []C.LLVMValueRef{backtraceHeader}, "")

	backtraceSymbolsFn, backtraceSymbolsType, err := s.ensureBacktraceSymbolsFDFunction()
	if err != nil {
		return err
	}
	backtraceSymbolsLLVMType, err := s.g.lowerFunctionType(backtraceSymbolsType)
	if err != nil {
		return err
	}
	stderrFD := C.LLVMConstInt(intLLVMType, 2, 0)
	s.buildCall(backtraceSymbolsLLVMType, backtraceSymbolsFn, []C.LLVMValueRef{bufferPtr, frameCount, stderrFD}, "")

	abortFn, abortType, err := s.ensureAbortFunction()
	if err != nil {
		return err
	}
	abortLLVMType, err := s.g.lowerFunctionType(abortType)
	if err != nil {
		return err
	}
	s.buildCall(abortLLVMType, abortFn, nil, "")
	C.LLVMBuildUnreachable(s.builder)
	return nil
}
func (s *functionState) coercePanicMessageToCString(value C.LLVMValueRef, valueType semantic.Type, fallback C.LLVMValueRef) (C.LLVMValueRef, bool, error) {
	refType, ok := valueType.(*semantic.RefType)
	if !ok || !semantic.SameType(refType.Elem, s.g.result.NamedTypes["u8"]) {
		return fallback, false, nil
	}
	llvmType, err := s.g.lowerType(refType)
	if err != nil {
		return nil, false, err
	}
	if refType.State == semantic.RefStateNullable {
		isNull := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), value, C.LLVMConstNull(llvmType), cStringFree("panic.message.is_null"))
		value = C.LLVMBuildSelect(s.builder, isNull, fallback, value, cStringFree("panic.message.ptr"))
	}
	return value, true, nil
}
func backendAggregateStates(expr *ast.AggregateStateTypeExpr) []semantic.RefState {
	if expr == nil {
		return nil
	}
	if len(expr.States) != 0 {
		states := make([]semantic.RefState, len(expr.States))
		for i, state := range expr.States {
			states[i] = semantic.RefState(state)
		}
		return states
	}
	return []semantic.RefState{semantic.RefState(expr.State)}
}
