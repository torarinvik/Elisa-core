//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"unsafe"
)

func (s *functionState) emitAtomicRuntimeCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	callName := callIdentName(expr)
	if !isBackendAtomicRuntimeCall(callName) {
		return nil, nil, false, nil
	}
	if callName == "fence" {
		if len(expr.Args) != 1 {
			return nil, nil, true, fmt.Errorf("fence expects 1 argument, got %d", len(expr.Args))
		}
		name := C.CString("")
		defer C.free(unsafe.Pointer(name))
		C.LLVMBuildFence(s.builder, backendAtomicOrderingSeqCst(), 0, name)
		return nil, s.g.result.NamedTypes["void"], true, nil
	}
	if len(expr.Args) == 0 {
		return nil, nil, true, fmt.Errorf("%s expects an atomic slot argument", callName)
	}
	slotPtr, payloadType, atomicLLVMType, ok, err := s.emitAtomicSlotPtr(expr.Args[0], "atomic."+callName+".slot")
	if err != nil || !ok {
		return nil, nil, ok, err
	}
	payloadLLVMType, err := s.g.lowerType(payloadType)
	if err != nil {
		return nil, nil, true, err
	}
	valuePtr := C.LLVMBuildStructGEP2(s.builder, atomicLLVMType, slotPtr, 0, cStringFree("atomic."+callName+".value.ptr"))
	order := backendAtomicOrderingSeqCst()
	switch callName {
	case "load":
		if len(expr.Args) != 2 {
			return nil, nil, true, fmt.Errorf("load expects 2 arguments, got %d", len(expr.Args))
		}
		load := C.LLVMBuildLoad2(s.builder, payloadLLVMType, valuePtr, cStringFree("atomic.load"))
		C.LLVMSetOrdering(load, order)
		return load, payloadType, true, nil
	case "store":
		if len(expr.Args) != 3 {
			return nil, nil, true, fmt.Errorf("store expects 3 arguments, got %d", len(expr.Args))
		}
		value, _, err := s.emitExpr(expr.Args[1], payloadType)
		if err != nil {
			return nil, nil, true, err
		}
		value, err = s.coerceValue(value, s.exprType(expr.Args[1]), payloadType)
		if err != nil {
			return nil, nil, true, err
		}
		store := C.LLVMBuildStore(s.builder, value, valuePtr)
		C.LLVMSetOrdering(store, order)
		return nil, s.g.result.NamedTypes["void"], true, nil
	case "exchange":
		if len(expr.Args) != 3 {
			return nil, nil, true, fmt.Errorf("exchange expects 3 arguments, got %d", len(expr.Args))
		}
		value, _, err := s.emitExpr(expr.Args[1], payloadType)
		if err != nil {
			return nil, nil, true, err
		}
		value, err = s.coerceValue(value, s.exprType(expr.Args[1]), payloadType)
		if err != nil {
			return nil, nil, true, err
		}
		return C.LLVMBuildAtomicRMW(s.builder, C.LLVMAtomicRMWBinOpXchg, valuePtr, value, order, 0), payloadType, true, nil
	case "compare_exchange":
		if len(expr.Args) != 5 {
			return nil, nil, true, fmt.Errorf("compare_exchange expects 5 arguments, got %d", len(expr.Args))
		}
		expected, _, err := s.emitExpr(expr.Args[1], payloadType)
		if err != nil {
			return nil, nil, true, err
		}
		expected, err = s.coerceValue(expected, s.exprType(expr.Args[1]), payloadType)
		if err != nil {
			return nil, nil, true, err
		}
		desired, _, err := s.emitExpr(expr.Args[2], payloadType)
		if err != nil {
			return nil, nil, true, err
		}
		desired, err = s.coerceValue(desired, s.exprType(expr.Args[2]), payloadType)
		if err != nil {
			return nil, nil, true, err
		}
		cmpxchg := C.LLVMBuildAtomicCmpXchg(s.builder, valuePtr, expected, desired, order, order, 0)
		return C.LLVMBuildExtractValue(s.builder, cmpxchg, 1, cStringFree("atomic.cmpxchg.ok")), s.g.result.NamedTypes["bool"], true, nil
	case "fetch_add", "fetch_sub", "fetch_or", "fetch_and", "fetch_xor":
		if len(expr.Args) != 3 {
			return nil, nil, true, fmt.Errorf("%s expects 3 arguments, got %d", callName, len(expr.Args))
		}
		value, _, err := s.emitExpr(expr.Args[1], payloadType)
		if err != nil {
			return nil, nil, true, err
		}
		value, err = s.coerceValue(value, s.exprType(expr.Args[1]), payloadType)
		if err != nil {
			return nil, nil, true, err
		}
		return C.LLVMBuildAtomicRMW(s.builder, backendAtomicRMWOp(callName), valuePtr, value, order, 0), payloadType, true, nil
	default:
		return nil, nil, false, nil
	}
}

func (s *functionState) emitAtomicSlotPtr(expr ast.Expr, name string) (C.LLVMValueRef, semantic.Type, C.LLVMTypeRef, bool, error) {
	slotType := s.exprType(expr)
	refType, ok := slotType.(*semantic.RefType)
	if !ok {
		return nil, nil, nil, false, nil
	}
	instance, ok := refType.Elem.(*semantic.GenericInstanceType)
	if !ok || instance.Name != "atomic" || len(instance.Args) != 1 {
		return nil, nil, nil, false, nil
	}
	slotPtr, _, err := s.emitExpr(expr, slotType)
	if err != nil {
		return nil, nil, nil, true, err
	}
	atomicLLVMType, err := s.g.lowerType(instance)
	if err != nil {
		return nil, nil, nil, true, err
	}
	return slotPtr, instance.Args[0], atomicLLVMType, true, nil
}

func isBackendAtomicRuntimeCall(name string) bool {
	switch name {
	case "load", "store", "exchange", "compare_exchange", "fetch_add", "fetch_sub", "fetch_or", "fetch_and", "fetch_xor", "fence":
		return true
	default:
		return false
	}
}

func backendAtomicOrderingSeqCst() C.LLVMAtomicOrdering {
	return C.LLVMAtomicOrderingSequentiallyConsistent
}

func backendAtomicRMWOp(name string) C.LLVMAtomicRMWBinOp {
	switch name {
	case "fetch_add":
		return C.LLVMAtomicRMWBinOpAdd
	case "fetch_sub":
		return C.LLVMAtomicRMWBinOpSub
	case "fetch_or":
		return C.LLVMAtomicRMWBinOpOr
	case "fetch_and":
		return C.LLVMAtomicRMWBinOpAnd
	case "fetch_xor":
		return C.LLVMAtomicRMWBinOpXor
	default:
		return C.LLVMAtomicRMWBinOpXchg
	}
}
