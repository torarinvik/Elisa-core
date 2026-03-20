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
	if resolvedExpr, resolvedType, changed, err := g.resolveConstAggregateExpr(expr); err != nil {
		return nil, err
	} else if changed {
		coercedType := expected
		if coercedType == nil {
			coercedType = resolvedType
		}
		return g.constExprValue(resolvedExpr, coercedType)
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
		fields, err := g.structLiteralFields(stType)
		if err != nil {
			return nil, err
		}
		values := make([]C.LLVMValueRef, 0, len(fields))
		for i, arg := range n.Args {
			if i >= len(fields) {
				break
			}
			fieldValue, err := g.constExprValue(arg, fields[i].Type)
			if err != nil {
				return nil, err
			}
			values = append(values, fieldValue)
		}
		return C.LLVMConstNamedStruct(llvmType, llvmValueSlicePtr(values), C.unsigned(len(values))), nil
	case *ast.ListLitExpr:
		arrayType := expected
		if arrayType == nil {
			arrayType = g.exprType(expr)
		}
		fixedArray, ok := arrayType.(*semantic.ArrayType)
		if !ok {
			return nil, fmt.Errorf("array literal global initializer requires a fixed array type")
		}
		elemLLVMType, err := g.lowerType(fixedArray.Elem)
		if err != nil {
			return nil, err
		}
		values := make([]C.LLVMValueRef, 0, len(n.Elems))
		for _, elem := range n.Elems {
			elemValue, err := g.constExprValue(elem, fixedArray.Elem)
			if err != nil {
				return nil, err
			}
			values = append(values, elemValue)
		}
		return C.LLVMConstArray2(elemLLVMType, llvmValueSlicePtr(values), C.ulonglong(len(values))), nil
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
	case *ast.MoveExpr:
		return g.constExprValue(n.Operand, expected)
	default:
		if value, ok := g.evalConstExpr(expr); ok {
			coercedType := expected
			if coercedType == nil {
				coercedType = constValueType(g.result, value)
			}
			if coercedType == nil {
				return nil, fmt.Errorf("could not infer type for constant global initializer %T", expr)
			}
			return g.constValueAsLLVM(value, coercedType)
		}
		return nil, fmt.Errorf("unsupported global initializer expression %T", expr)
	}
}

func (g *llvmGenerator) resolveConstAggregateExpr(expr ast.Expr) (ast.Expr, semantic.Type, bool, error) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		resolved, resolvedType, changed, err := g.resolveConstAggregateExpr(n.Inner)
		if err != nil {
			return nil, nil, false, err
		}
		if changed {
			return resolved, resolvedType, true, nil
		}
		return n.Inner, g.exprType(n.Inner), true, nil
	case *ast.MoveExpr:
		resolved, resolvedType, changed, err := g.resolveConstAggregateExpr(n.Operand)
		if err != nil {
			return nil, nil, false, err
		}
		if changed {
			return resolved, resolvedType, true, nil
		}
		return n.Operand, g.exprType(n.Operand), true, nil
	case *ast.Ident:
		decl, declType, ok := g.lookupGlobalConstDecl(n.Name)
		if !ok || decl.Value == nil {
			return nil, nil, false, nil
		}
		return decl.Value, declType, true, nil
	case *ast.IndexExpr:
		resolvedObj, objType, changed, err := g.resolveConstAggregateExpr(n.Object)
		if err != nil {
			return nil, nil, false, err
		}
		if !changed {
			resolvedObj = n.Object
			objType = g.exprType(n.Object)
		}
		arrayType, ok := constAggregateIndexedArrayType(objType)
		if !ok {
			return nil, nil, false, nil
		}
		indexValue, ok := g.evalConstExpr(n.Index)
		if !ok || indexValue.Kind != semantic.ConstInt {
			return nil, nil, false, fmt.Errorf("global initializer index must be a compile-time integer constant")
		}
		switch obj := resolvedObj.(type) {
		case *ast.ListLitExpr:
			if indexValue.Int < 0 || int(indexValue.Int) >= len(obj.Elems) {
				return nil, nil, false, fmt.Errorf("global initializer index %d out of bounds", indexValue.Int)
			}
			selected := obj.Elems[indexValue.Int]
			if resolved, resolvedType, changed, err := g.resolveConstAggregateExpr(selected); err != nil {
				return nil, nil, false, err
			} else if changed {
				return resolved, resolvedType, true, nil
			}
			return selected, arrayType.Elem, true, nil
		case *ast.ZeroedLit:
			return &ast.ZeroedLit{Position: obj.Position}, arrayType.Elem, true, nil
		default:
			return nil, nil, false, nil
		}
	case *ast.FieldExpr:
		resolvedObj, objType, changed, err := g.resolveConstAggregateExpr(n.Object)
		if err != nil {
			return nil, nil, false, err
		}
		if !changed {
			resolvedObj = n.Object
			objType = g.exprType(n.Object)
		}
		fieldType, index, _, pointerLike, err := g.fieldInfo(objType, n.Field)
		if err != nil {
			return nil, nil, false, nil
		}
		if pointerLike {
			return nil, nil, false, nil
		}
		structType := objType
		if ref, ok := objType.(*semantic.RefType); ok {
			structType = ref.Elem
		}
		if runtimeBacked := g.runtimeBackedStructType(structType); runtimeBacked != nil {
			structType = runtimeBacked
		}
		switch obj := resolvedObj.(type) {
		case *ast.StructLitExpr:
			fields, err := g.structLiteralFields(structType)
			if err != nil {
				return nil, nil, false, err
			}
			if index < 0 || index >= len(obj.Args) {
				return nil, nil, false, fmt.Errorf("global initializer field index %d out of bounds", index)
			}
			selected := obj.Args[index]
			if resolved, resolvedType, changed, err := g.resolveConstAggregateExpr(selected); err != nil {
				return nil, nil, false, err
			} else if changed {
				return resolved, resolvedType, true, nil
			}
			return selected, fields[index].Type, true, nil
		case *ast.ZeroedLit:
			return &ast.ZeroedLit{Position: obj.Position}, fieldType, true, nil
		default:
			return nil, nil, false, nil
		}
	default:
		return nil, nil, false, nil
	}
}

func (g *llvmGenerator) lookupGlobalConstDecl(name string) (*ast.GlobalDecl, semantic.Type, bool) {
	if g == nil || g.result == nil || g.result.GlobalScope == nil {
		return nil, nil, false
	}
	sym, ok := g.result.GlobalScope.Lookup(name)
	if !ok || sym == nil {
		return nil, nil, false
	}
	decl, ok := sym.Node.(*ast.GlobalDecl)
	if !ok {
		return nil, nil, false
	}
	return decl, sym.Type, true
}

func constAggregateIndexedArrayType(t semantic.Type) (*semantic.ArrayType, bool) {
	if arrayType, ok := t.(*semantic.ArrayType); ok {
		return arrayType, true
	}
	return nil, false
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
