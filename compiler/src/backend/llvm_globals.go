//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"
	"strconv"
	"unsafe"

	"llcontext/src/ast"
	"llcontext/src/semantic"
)

func (g *llvmGenerator) defineGlobal(decl *ast.GlobalDecl, typ semantic.Type, global C.LLVMValueRef) error {
	if decl == nil || global == nil {
		return nil
	}
	if decl.Value == nil {
		zero, err := g.constZero(typ)
		if err != nil {
			return err
		}
		C.LLVMSetInitializer(global, zero)
		return nil
	}
	value, err := g.constExprValue(decl.Value, typ)
	if err != nil {
		return err
	}
	C.LLVMSetInitializer(global, value)
	return nil
}

func (g *llvmGenerator) constExprValue(expr ast.Expr, expected semantic.Type) (C.LLVMValueRef, error) {
	actual := expected
	if actual == nil {
		actual = g.exprType(expr)
	}
	switch n := expr.(type) {
	case *ast.IntLit:
		llvmType, err := g.lowerType(actual)
		if err != nil {
			return nil, err
		}
		value, err := parseConstIntLiteral(n)
		if err != nil {
			return nil, err
		}
		return C.LLVMConstInt(llvmType, C.ulonglong(value), boolToLLVMBool(value < 0)), nil
	case *ast.BoolLit:
		llvmType, err := g.lowerBuiltin("bool")
		if err != nil {
			return nil, err
		}
		var raw C.ulonglong
		if n.Value {
			raw = 1
		}
		return C.LLVMConstInt(llvmType, raw, 0), nil
	case *ast.NullLit:
		llvmType, err := g.lowerType(expected)
		if err != nil {
			return nil, err
		}
		return C.LLVMConstNull(llvmType), nil
	case *ast.ZeroedLit:
		return g.constZero(expected)
	case *ast.StringLit:
		return g.constGlobalStringPtr(n.Value)
	case *ast.Ident:
		if value, ok := g.constValue(n.Name); ok {
			coercedType := expected
			if coercedType == nil {
				coercedType = constValueType(g.result, value)
			}
			return g.constValueAsLLVM(value, coercedType)
		}
		if global, ok := g.globals[n.Name]; ok {
			return global, nil
		}
		return nil, fmt.Errorf("identifier %s is not a constant global initializer", n.Name)
	case *ast.StructLitExpr:
		stType := g.exprType(expr)
		if expected != nil {
			stType = expected
		}
		llvmType, err := g.lowerType(stType)
		if err != nil {
			return nil, err
		}
		st, ok := stType.(*semantic.StructType)
		if !ok {
			return nil, fmt.Errorf("struct literal %s is not a concrete repr(c) struct", n.Name)
		}
		values := make([]C.LLVMValueRef, 0, len(st.Decl.Fields))
		for i, arg := range n.Args {
			if i >= len(st.Decl.Fields) {
				break
			}
			fieldDecl := st.Decl.Fields[i]
			field := st.Fields[fieldDecl.Name]
			fieldValue, err := g.constExprValue(arg, field.Type)
			if err != nil {
				return nil, err
			}
			values = append(values, fieldValue)
		}
		return C.LLVMConstNamedStruct(llvmType, llvmValueSlicePtr(values), C.unsigned(len(values))), nil
	case *ast.CastExpr:
		targetType, err := g.lowerType(expected)
		if err != nil {
			return nil, err
		}
		inner, err := g.constExprValue(n.Operand, g.exprType(n.Operand))
		if err != nil {
			return nil, err
		}
		if semantic.IsNumericType(g.exprType(n.Operand)) && isPointerLikeType(expected) {
			return C.LLVMConstIntToPtr(inner, targetType), nil
		}
		if isPointerLikeType(g.exprType(n.Operand)) && semantic.IsNumericType(expected) {
			return C.LLVMConstPtrToInt(inner, targetType), nil
		}
		return inner, nil
	case *ast.ParenExpr:
		return g.constExprValue(n.Inner, expected)
	default:
		return nil, fmt.Errorf("unsupported global initializer expression %T", expr)
	}
}

func (g *llvmGenerator) constZero(t semantic.Type) (C.LLVMValueRef, error) {
	llvmType, err := g.lowerType(t)
	if err != nil {
		return nil, err
	}
	return C.LLVMConstNull(llvmType), nil
}

func (g *llvmGenerator) constGlobalStringPtr(value string) (C.LLVMValueRef, error) {
	bytes := append([]byte(value), 0)
	arrayType := C.LLVMArrayType2(C.LLVMInt8TypeInContext(g.context), C.ulonglong(len(bytes)))
	data := C.CBytes(bytes)
	defer C.free(data)
	initializer := C.LLVMConstStringInContext(g.context, (*C.char)(data), C.unsigned(len(bytes)-1), 1)
	name := cString(".str")
	defer C.free(unsafe.Pointer(name))
	global := C.LLVMAddGlobal(g.module, arrayType, name)
	C.LLVMSetLinkage(global, C.LLVMPrivateLinkage)
	C.LLVMSetGlobalConstant(global, 1)
	C.LLVMSetUnnamedAddress(global, C.LLVMGlobalUnnamedAddr)
	C.LLVMSetInitializer(global, initializer)
	zero32 := C.LLVMConstInt(C.LLVMInt32TypeInContext(g.context), 0, 0)
	indices := []C.LLVMValueRef{zero32, zero32}
	return C.LLVMConstInBoundsGEP2(arrayType, global, llvmValueSlicePtr(indices), C.unsigned(len(indices))), nil
}

func (g *llvmGenerator) constValueAsLLVM(value semantic.ConstValue, expected semantic.Type) (C.LLVMValueRef, error) {
	switch value.Kind {
	case semantic.ConstInt:
		llvmType, err := g.lowerType(expected)
		if err != nil {
			return nil, err
		}
		return C.LLVMConstInt(llvmType, C.ulonglong(value.Int), boolToLLVMBool(value.Int < 0)), nil
	case semantic.ConstBool:
		llvmType, err := g.lowerBuiltin("bool")
		if err != nil {
			return nil, err
		}
		var raw C.ulonglong
		if value.Bool {
			raw = 1
		}
		return C.LLVMConstInt(llvmType, raw, 0), nil
	case semantic.ConstString:
		return g.constGlobalStringPtr(value.String)
	default:
		return nil, fmt.Errorf("unsupported const value kind %d", value.Kind)
	}
}

func constValueType(result *semantic.Result, value semantic.ConstValue) semantic.Type {
	if result == nil {
		return nil
	}
	switch value.Kind {
	case semantic.ConstInt:
		return result.NamedTypes["int"]
	case semantic.ConstBool:
		return result.NamedTypes["bool"]
	case semantic.ConstString:
		return &semantic.RefType{Elem: result.NamedTypes["u8"], State: semantic.RefStateNonNull}
	default:
		return nil
	}
}

func parseConstIntLiteral(expr *ast.IntLit) (int64, error) {
	return strconv.ParseInt(expr.Value, 0, 64)
}
