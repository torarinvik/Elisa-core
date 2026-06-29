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
	"strings"
	"unsafe"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func llvmNamespaceFromName(name string) string {
	name = strings.TrimSpace(name)
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[:idx]
	}
	return ""
}

func llvmVisibleGlobalNames(namespace string, name string) []string {
	name = strings.TrimSpace(name)
	namespace = strings.TrimSpace(namespace)
	if name == "" {
		return nil
	}
	if strings.Contains(name, ".") || strings.Contains(name, "::") {
		return []string{strings.ReplaceAll(name, "::", ".")}
	}
	if namespace == "" {
		return []string{name}
	}
	return []string{namespace + "." + name, name}
}

func (g *llvmGenerator) defineGlobal(name string, decl *ast.GlobalDecl, typ semantic.Type, global C.LLVMValueRef) error {
	if decl == nil || global == nil {
		return nil
	}
	// Runtime singleton globals (the `__elisa_` prefix) may be defined both by the
	// default runtime object and by a program that includes a runtime fragment
	// directly. Weak linkage dedupes them to one definition at link time.
	if strings.HasPrefix(name, "__elisa_") {
		C.LLVMSetLinkage(global, C.LLVMWeakAnyLinkage)
	}
	if decl.Value == nil {
		zero, err := g.constZero(typ)
		if err != nil {
			return err
		}
		C.LLVMSetInitializer(global, zero)
		return nil
	}
	value, err := g.constExprValueInNamespace(decl.Value, typ, llvmNamespaceFromName(name))
	if err != nil {
		return err
	}
	C.LLVMSetInitializer(global, value)
	return nil
}

func (g *llvmGenerator) constExprValue(expr ast.Expr, expected semantic.Type) (C.LLVMValueRef, error) {
	return g.constExprValueInNamespace(expr, expected, "")
}

func (g *llvmGenerator) constExprValueInNamespace(expr ast.Expr, expected semantic.Type, namespace string) (C.LLVMValueRef, error) {
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
		return g.constExprValueInNamespace(resolvedExpr, coercedType, namespace)
	}
	switch n := expr.(type) {
	case *ast.IntLit:
		llvmType, err := g.lowerType(actual)
		if err != nil {
			return nil, err
		}
		value, err := parseConstIntLiteralForType(n, actual)
		if err != nil {
			return nil, err
		}
		return C.LLVMConstInt(llvmType, C.ulonglong(value), boolToLLVMBool(value < 0)), nil
	case *ast.FloatLit:
		llvmType, err := g.lowerType(actual)
		if err != nil {
			return nil, err
		}
		value, err := strconv.ParseFloat(n.Value, 64)
		if err != nil {
			return nil, err
		}
		return C.LLVMConstReal(llvmType, C.double(value)), nil
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
	case *ast.CharLit:
		llvmType, err := g.lowerType(actual)
		if err != nil {
			return nil, err
		}
		value, ok := semantic.ParseCharLiteral(n)
		if !ok {
			return nil, fmt.Errorf("failed to parse char literal %q", n.Value)
		}
		return C.LLVMConstInt(llvmType, C.ulonglong(value), 0), nil
	case *ast.Ident:
		for _, candidate := range llvmVisibleGlobalNames(namespace, n.Name) {
			if value, ok := g.constValue(candidate); ok {
				coercedType := expected
				if coercedType == nil && g.result != nil && g.result.GlobalScope != nil {
					if sym, ok := g.result.GlobalScope.Lookup(candidate); ok && sym.Kind == semantic.SymbolConst {
						coercedType = sym.Type
					}
				}
				if coercedType == nil {
					coercedType = constValueType(g.result, value)
				}
				return g.constValueAsLLVM(value, coercedType)
			}
		}
		if value, ok := g.constValue(n.Name); ok {
			coercedType := expected
			if coercedType == nil && g.result != nil && g.result.GlobalScope != nil {
				if sym, ok := g.result.GlobalScope.Lookup(n.Name); ok && sym.Kind == semantic.SymbolConst {
					coercedType = sym.Type
				}
			}
			if coercedType == nil {
				coercedType = constValueType(g.result, value)
			}
			return g.constValueAsLLVM(value, coercedType)
		}
		for _, candidate := range llvmVisibleGlobalNames(namespace, n.Name) {
			if global, ok := g.globals[candidate]; ok {
				return global, nil
			}
		}
		return nil, fmt.Errorf("identifier %s is not a constant global initializer", n.Name)
	case *ast.FieldExpr:
		if value, ok, err := g.constEnumFieldExprValue(n, actual); err != nil {
			return nil, err
		} else if ok {
			return value, nil
		}
		if value, ok := g.evalConstExpr(expr); ok {
			coercedType := expected
			if coercedType == nil {
				coercedType = g.exprType(expr)
			}
			if coercedType == nil {
				coercedType = constValueType(g.result, value)
			}
			if coercedType == nil {
				return nil, fmt.Errorf("could not infer type for constant global initializer %T", expr)
			}
			return g.constValueAsLLVM(value, coercedType)
		}
		if parts, ok := qualifiedFieldParts(expr); ok && len(parts) > 0 {
			name := strings.Join(parts, ".")
			for _, candidate := range llvmVisibleGlobalNames(namespace, name) {
				if value, ok := g.constValue(candidate); ok {
					coercedType := expected
					if coercedType == nil && g.result != nil && g.result.GlobalScope != nil {
						if sym, ok := g.result.GlobalScope.Lookup(candidate); ok && sym.Kind == semantic.SymbolConst {
							coercedType = sym.Type
						}
					}
					if coercedType == nil {
						coercedType = g.exprType(expr)
					}
					if coercedType == nil {
						coercedType = constValueType(g.result, value)
					}
					if coercedType == nil {
						return nil, fmt.Errorf("could not infer type for constant global initializer %T", expr)
					}
					return g.constValueAsLLVM(value, coercedType)
				}
			}
		}
		return nil, fmt.Errorf("unsupported global initializer expression %T", expr)
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
		args := n.LoweredArgs()
		for i, arg := range args {
			if i >= len(fields) {
				break
			}
			if arg == nil {
				return nil, fmt.Errorf("global struct literal field %d was not resolved", i)
			}
			fieldValue, err := g.constExprValueInNamespace(arg, fields[i].Type, namespace)
			if err != nil {
				return nil, err
			}
			values = append(values, fieldValue)
		}
		return C.LLVMConstNamedStruct(llvmType, llvmValueSlicePtr(values), C.unsigned(len(values))), nil
	case *ast.ListLitExpr:
		literalType := expected
		if literalType == nil {
			literalType = g.exprType(expr)
		}
		if dictType, ok := literalType.(*semantic.DictType); ok && n.Brace && len(n.Keys) > 0 {
			return g.constDictLiteralValue(n, dictType, namespace)
		}
		fixedArray, ok := literalType.(*semantic.ArrayType)
		if !ok {
			return nil, fmt.Errorf("array literal global initializer requires a fixed array type")
		}
		elemLLVMType, err := g.lowerType(fixedArray.Elem)
		if err != nil {
			return nil, err
		}
		values := make([]C.LLVMValueRef, 0, len(n.Elems))
		for _, elem := range n.Elems {
			elemValue, err := g.constExprValueInNamespace(elem, fixedArray.Elem, namespace)
			if err != nil {
				return nil, err
			}
			values = append(values, elemValue)
		}
		return C.LLVMConstArray2(elemLLVMType, llvmValueSlicePtr(values), C.uint64_t(len(values))), nil
	case *ast.CastExpr:
		if value, ok := g.evalConstExpr(expr); ok {
			coercedType := expected
			if coercedType == nil {
				coercedType = g.exprType(expr)
			}
			return g.constValueAsLLVM(value, coercedType)
		}
		targetType, err := g.lowerType(expected)
		if err != nil {
			return nil, err
		}
		inner, err := g.constExprValueInNamespace(n.Operand, g.exprType(n.Operand), namespace)
		if err != nil {
			return nil, err
		}
		if isNumericCastType(g.exprType(n.Operand)) && isPointerLikeType(expected) {
			return C.LLVMConstIntToPtr(inner, targetType), nil
		}
		if isPointerLikeType(g.exprType(n.Operand)) && isNumericCastType(expected) {
			return C.LLVMConstPtrToInt(inner, targetType), nil
		}
		return inner, nil
	case *ast.ParenExpr:
		return g.constExprValueInNamespace(n.Inner, expected, namespace)
	case *ast.MoveExpr:
		return g.constExprValueInNamespace(n.Operand, expected, namespace)
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

func (g *llvmGenerator) constEnumFieldExprValue(expr *ast.FieldExpr, expected semantic.Type) (C.LLVMValueRef, bool, error) {
	constEnumType, ok := semantic.StripAggregateStateType(expected).(*semantic.ConstEnumType)
	if !ok || constEnumType == nil {
		return nil, false, nil
	}
	parts, ok := qualifiedFieldParts(expr)
	if !ok || len(parts) == 0 {
		return nil, false, nil
	}
	for i := len(parts) - 1; i >= 0; i-- {
		memberName := strings.Join(parts[i:], ".")
		member, ok := constEnumType.Member(memberName)
		if !ok || member == nil {
			continue
		}
		llvmType, err := g.lowerType(constEnumType)
		if err != nil {
			return nil, false, err
		}
		return C.LLVMConstInt(llvmType, C.ulonglong(member.Value), boolToLLVMBool(member.Value < 0)), true, nil
	}
	return nil, false, nil
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
			args := obj.LoweredArgs()
			if index < 0 || index >= len(args) {
				return nil, nil, false, fmt.Errorf("global initializer field index %d out of bounds", index)
			}
			selected := args[index]
			if selected == nil {
				return nil, nil, false, fmt.Errorf("global initializer field %d was not resolved", index)
			}
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
	arrayType := C.LLVMArrayType2(C.LLVMInt8TypeInContext(g.context), C.uint64_t(len(bytes)))
	data := C.CBytes(bytes)
	defer C.free(data)
	// `bytes` already includes the trailing NUL and `arrayType` is sized to len(bytes),
	// so the constant data array must also span len(bytes) (DontNullTerminate=1, since the
	// NUL is already present). Using len(bytes)-1 produced a [len-1 x i8] initializer for a
	// [len x i8] global — an off-by-one that the verifier rejects (notably for "" -> [0 x i8]
	// vs [1 x i8]).
	initializer := C.LLVMConstStringInContext(g.context, (*C.char)(data), C.unsigned(len(bytes)), 1)
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
	case semantic.ConstFloat:
		llvmType, err := g.lowerType(expected)
		if err != nil {
			return nil, err
		}
		return C.LLVMConstReal(llvmType, C.double(value.Float)), nil
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
	case semantic.ConstFloat:
		return result.NamedTypes["f64"]
	case semantic.ConstBool:
		return result.NamedTypes["bool"]
	case semantic.ConstString:
		return &semantic.RefType{Elem: result.NamedTypes["u8"], State: semantic.RefStateNonNull}
	default:
		return nil
	}
}

func parseConstIntLiteral(expr *ast.IntLit) (int64, error) {
	return parseConstIntLiteralForType(expr, nil)
}

// parseConstIntLiteralForType parses a const/global integer literal, honoring the declared target
// type. A suffixless literal exceeding i64 max but fitting u64 is accepted when the target is a
// 64-bit unsigned type (u64/usize/uintptr) — the declared type stands in for the `u64` suffix,
// matching how function-body literals already lower (emitIntLiteral parses via ParseUint). Suffixed
// and signed-typed literals are unchanged; a too-large literal in a signed context still errors.
func parseConstIntLiteralForType(expr *ast.IntLit, target semantic.Type) (int64, error) {
	if v, ok := semantic.ParseIntLiteralForType(expr, target); ok {
		return v, nil
	}
	return 0, fmt.Errorf("invalid integer literal %q", expr.Value)
}
