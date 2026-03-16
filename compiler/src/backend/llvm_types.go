//go:build cgo

package backend

/*
#cgo darwin CFLAGS: -I/opt/homebrew/opt/llvm/include -I/usr/local/opt/llvm/include
#cgo darwin LDFLAGS: -L/opt/homebrew/opt/llvm/lib -L/usr/local/opt/llvm/lib -lLLVM-C -lLLVM
#cgo linux CFLAGS: -I/usr/include -I/usr/lib/llvm-21/include -I/usr/lib/llvm-20/include -I/usr/lib/llvm-19/include -I/usr/lib/llvm-18/include -I/usr/lib/llvm-17/include -I/usr/lib/llvm-16/include -I/usr/lib/llvm-15/include
#cgo linux LDFLAGS: -L/usr/lib/llvm-21/lib -L/usr/lib/llvm-20/lib -L/usr/lib/llvm-19/lib -L/usr/lib/llvm-18/lib -L/usr/lib/llvm-17/lib -L/usr/lib/llvm-16/lib -L/usr/lib/llvm-15/lib -lLLVM-C -lLLVM
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"
	"strings"
	"unsafe"

	"llcontext/src/ast"
	"llcontext/src/semantic"
)

func isVoidRefLikeType(t semantic.Type) bool {
	ref, ok := t.(*semantic.RefType)
	if !ok {
		return false
	}
	builtin, ok := ref.Elem.(*semantic.BuiltinType)
	return ok && builtin.Name == "void"
}

func (g *llvmGenerator) noteType(t semantic.Type) error {
	if t == nil {
		return nil
	}
	switch tt := t.(type) {
	case *semantic.InvalidType, *semantic.NullType, *semantic.BuiltinType, *semantic.TypeParamType, *semantic.DStrType:
		return nil
	case *semantic.SViewType:
		if st, ok := g.lookupStructType("CtxStringView"); ok {
			_, err := g.ensureStructBody(st.Name, st)
			return err
		}
		return fmt.Errorf("missing runtime struct CtxStringView")
	case *semantic.RefType:
		return g.noteType(tt.Elem)
	case *semantic.ArrayType:
		return g.noteType(tt.Elem)
	case *semantic.DArrayType:
		if isVoidRefLikeType(tt.Elem) {
			_, err := g.ensureRuntimeCtxList()
			return err
		}
		_, err := g.ensureRuntimeDynArray(tt.Elem)
		return err
	case *semantic.DArrayViewType:
		_, err := g.ensureRuntimeDynArrayView()
		return err
	case *semantic.DListType:
		_, err := g.ensureRuntimeCtxList()
		return err
	case *semantic.DListViewType:
		_, err := g.ensureRuntimeCtxListView()
		return err
	case *semantic.StructType:
		if len(tt.TypeParams) == 0 {
			_, err := g.ensureStructBody(tt.Name, tt)
			return err
		}
		_, err := g.ensureNamedStructType(tt.Name)
		return err
	case *semantic.OpaqueType:
		_, err := g.ensureNamedStructType(tt.Name)
		return err
	case *semantic.GenericInstanceType:
		_, err := g.ensureGenericInstanceStruct(tt)
		return err
	case *semantic.FuncType:
		for _, param := range tt.Params {
			if err := g.noteType(param); err != nil {
				return err
			}
		}
		return g.noteType(tt.Return)
	default:
		return fmt.Errorf("unsupported semantic type %T", t)
	}
}

func (g *llvmGenerator) ensureFunctionDeclared(name string, fn *semantic.FuncType) (C.LLVMValueRef, error) {
	if value, ok := g.functions[name]; ok {
		return value, nil
	}
	value, err := g.addFunction(name, fn)
	if err != nil {
		return nil, err
	}
	g.functions[name] = value
	return value, nil
}

func (g *llvmGenerator) ensureGlobalDeclared(name string, t semantic.Type, external bool) (C.LLVMValueRef, error) {
	if value, ok := g.globals[name]; ok {
		return value, nil
	}
	value, err := g.addGlobal(name, t, external)
	if err != nil {
		return nil, err
	}
	g.globals[name] = value
	return value, nil
}

func (g *llvmGenerator) addFunction(name string, fn *semantic.FuncType) (C.LLVMValueRef, error) {
	if intrinsicID, overloadedParamTypes, ok, err := g.lookupIntrinsic(name, fn); err != nil {
		return nil, err
	} else if ok {
		return C.LLVMGetIntrinsicDeclaration(g.module, intrinsicID, llvmTypeSlicePtr(overloadedParamTypes), C.size_t(len(overloadedParamTypes))), nil
	}

	fnType, err := g.lowerFunctionType(fn)
	if err != nil {
		return nil, err
	}
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	value := C.LLVMAddFunction(g.module, nameC, fnType)
	C.LLVMSetLinkage(value, C.LLVMExternalLinkage)
	return value, nil
}

func (g *llvmGenerator) lookupIntrinsic(name string, fn *semantic.FuncType) (C.uint, []C.LLVMTypeRef, bool, error) {
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	intrinsicID := C.LLVMLookupIntrinsicID(nameC, C.size_t(len(name)))
	if intrinsicID == 0 {
		return 0, nil, false, nil
	}
	paramTypes := make([]C.LLVMTypeRef, 0, len(fn.Params))
	for _, param := range fn.Params {
		paramType, err := g.lowerType(param)
		if err != nil {
			return 0, nil, false, err
		}
		paramTypes = append(paramTypes, paramType)
	}
	return intrinsicID, paramTypes, true, nil
}

func (g *llvmGenerator) addGlobal(name string, t semantic.Type, external bool) (C.LLVMValueRef, error) {
	globalType, err := g.lowerType(t)
	if err != nil {
		return nil, err
	}
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	value := C.LLVMAddGlobal(g.module, globalType, nameC)
	C.LLVMSetLinkage(value, C.LLVMExternalLinkage)
	_ = external
	return value, nil
}

func (g *llvmGenerator) lowerFunctionType(fn *semantic.FuncType) (C.LLVMTypeRef, error) {
	returnType, err := g.lowerType(fn.Return)
	if err != nil {
		return nil, err
	}
	params := make([]C.LLVMTypeRef, 0, len(fn.Params))
	for _, param := range fn.Params {
		paramType, err := g.lowerType(param)
		if err != nil {
			return nil, err
		}
		params = append(params, paramType)
	}
	return C.LLVMFunctionType(returnType, llvmTypeSlicePtr(params), C.unsigned(len(params)), boolToLLVMBool(fn.Variadic)), nil
}

func (g *llvmGenerator) lowerType(t semantic.Type) (C.LLVMTypeRef, error) {
	if t == nil {
		return C.LLVMVoidTypeInContext(g.context), nil
	}
	switch tt := t.(type) {
	case *semantic.InvalidType:
		return C.LLVMVoidTypeInContext(g.context), nil
	case *semantic.NullType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.BuiltinType:
		return g.lowerBuiltin(tt.Name)
	case *semantic.TypeParamType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.RefType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.ArrayType:
		elemType, err := g.lowerType(tt.Elem)
		if err != nil {
			return nil, err
		}
		if tt.HasConstSize {
			return C.LLVMArrayType2(elemType, C.ulonglong(tt.ConstSize)), nil
		}
		return nil, fmt.Errorf("array type %s is missing a compile-time constant size", tt.String())
	case *semantic.DArrayType:
		if isVoidRefLikeType(tt.Elem) {
			if _, err := g.ensureRuntimeCtxList(); err != nil {
				return nil, err
			}
			return C.LLVMPointerTypeInContext(g.context, 0), nil
		}
		return g.ensureRuntimeDynArray(tt.Elem)
	case *semantic.DArrayViewType:
		return g.ensureRuntimeDynArrayView()
	case *semantic.DListType:
		if _, err := g.ensureRuntimeCtxList(); err != nil {
			return nil, err
		}
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.DListViewType:
		return g.ensureRuntimeCtxListView()
	case *semantic.DStrType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.SViewType:
		st, ok := g.lookupStructType("CtxStringView")
		if !ok {
			return nil, fmt.Errorf("missing runtime struct CtxStringView")
		}
		return g.ensureStructBody(st.Name, st)
	case *semantic.StructType:
		if len(tt.TypeParams) == 0 {
			return g.ensureStructBody(tt.Name, tt)
		}
		return nil, fmt.Errorf("cannot lower generic struct %s without concrete type arguments", tt.Name)
	case *semantic.OpaqueType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.GenericInstanceType:
		return g.ensureGenericInstanceStruct(tt)
	case *semantic.FuncType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	default:
		return nil, fmt.Errorf("unsupported semantic type %T", t)
	}
}

func (g *llvmGenerator) lowerBuiltin(name string) (C.LLVMTypeRef, error) {
	switch name {
	case "void":
		return C.LLVMVoidTypeInContext(g.context), nil
	case "bool":
		return C.LLVMInt1TypeInContext(g.context), nil
	case "char":
		return C.LLVMInt64TypeInContext(g.context), nil
	case "i8", "u8":
		return C.LLVMInt8TypeInContext(g.context), nil
	case "i16", "u16":
		return C.LLVMInt16TypeInContext(g.context), nil
	case "i32", "u32":
		return C.LLVMInt32TypeInContext(g.context), nil
	case "i64", "u64":
		return C.LLVMInt64TypeInContext(g.context), nil
	case "int", "isize", "usize", "uintptr":
		if g.wordBits == 32 {
			return C.LLVMInt32TypeInContext(g.context), nil
		}
		return C.LLVMInt64TypeInContext(g.context), nil
	default:
		return nil, fmt.Errorf("unsupported builtin type %q", name)
	}
}

func (g *llvmGenerator) lookupStructType(name string) (*semantic.StructType, bool) {
	t, ok := g.result.NamedTypes[name]
	if !ok {
		return nil, false
	}
	st, ok := t.(*semantic.StructType)
	return st, ok
}

func (g *llvmGenerator) ensureNamedStructType(name string) (C.LLVMTypeRef, error) {
	if ty, ok := g.structTypes[name]; ok {
		return ty, nil
	}
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	ty := C.LLVMStructCreateNamed(g.context, nameC)
	g.structTypes[name] = ty
	return ty, nil
}

func (g *llvmGenerator) ensureStructBody(name string, st *semantic.StructType) (C.LLVMTypeRef, error) {
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] || st == nil || st.Decl == nil {
		return ty, nil
	}
	fields := make([]C.LLVMTypeRef, 0, len(st.Decl.Fields))
	for _, fieldDecl := range st.Decl.Fields {
		field, ok := st.Fields[fieldDecl.Name]
		if !ok {
			return nil, fmt.Errorf("missing semantic field %s.%s", name, fieldDecl.Name)
		}
		fieldType, err := g.lowerType(field.Type)
		if err != nil {
			return nil, err
		}
		fields = append(fields, fieldType)
	}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}

func (g *llvmGenerator) ensureRuntimeDynArray(elem semantic.Type) (C.LLVMTypeRef, error) {
	name := runtimeDynArrayName(elem)
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] {
		return ty, nil
	}
	countType, err := g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	fields := []C.LLVMTypeRef{
		C.LLVMPointerTypeInContext(g.context, 0),
		countType,
		countType,
	}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}

func (g *llvmGenerator) ensureRuntimeDynArrayView() (C.LLVMTypeRef, error) {
	return g.ensureRuntimeSizedStruct("DynArrayView", 3)
}

func (g *llvmGenerator) ensureRuntimeCtxList() (C.LLVMTypeRef, error) {
	return g.ensureRuntimeSizedStruct("CtxListHandle", 3)
}

func (g *llvmGenerator) ensureRuntimeCtxListView() (C.LLVMTypeRef, error) {
	return g.ensureRuntimeSizedStruct("CtxListView", 2)
}

func (g *llvmGenerator) ensureRuntimeSizedStruct(name string, fieldCount int) (C.LLVMTypeRef, error) {
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] {
		return ty, nil
	}
	countType, err := g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	fields := make([]C.LLVMTypeRef, 0, fieldCount)
	fields = append(fields, C.LLVMPointerTypeInContext(g.context, 0))
	for i := 1; i < fieldCount; i++ {
		fields = append(fields, countType)
	}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}

func (g *llvmGenerator) ensureGenericInstanceStruct(inst *semantic.GenericInstanceType) (C.LLVMTypeRef, error) {
	name := mangleGenericType(inst.Name, inst.Args)
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] {
		return ty, nil
	}
	base, ok := inst.Base.(*semantic.StructType)
	if !ok {
		return nil, fmt.Errorf("generic instance %s does not resolve to a struct base", inst.Name)
	}
	if len(base.TypeParams) != len(inst.Args) {
		return nil, fmt.Errorf("generic instance %s has %d args, expected %d", inst.Name, len(inst.Args), len(base.TypeParams))
	}
	subst := make(map[string]semantic.Type, len(base.TypeParams))
	for i, param := range base.TypeParams {
		subst[param] = inst.Args[i]
	}
	if base.Decl == nil {
		return nil, fmt.Errorf("generic struct %s is missing declaration metadata", base.Name)
	}
	fields := make([]C.LLVMTypeRef, 0, len(base.Decl.Fields))
	for _, fieldDecl := range base.Decl.Fields {
		field, ok := base.Fields[fieldDecl.Name]
		if !ok {
			return nil, fmt.Errorf("missing semantic field %s.%s", base.Name, fieldDecl.Name)
		}
		fieldType, err := g.lowerType(substituteType(field.Type, subst))
		if err != nil {
			return nil, err
		}
		fields = append(fields, fieldType)
	}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}

func substituteType(t semantic.Type, subst map[string]semantic.Type) semantic.Type {
	if t == nil {
		return nil
	}
	switch tt := t.(type) {
	case *semantic.TypeParamType:
		if mapped, ok := subst[tt.Name]; ok {
			return mapped
		}
		return t
	case *semantic.RefType:
		return &semantic.RefType{Elem: substituteType(tt.Elem, subst), State: tt.State}
	case *semantic.ArrayType:
		return &semantic.ArrayType{Elem: substituteType(tt.Elem, subst), Size: tt.Size, HasConstSize: tt.HasConstSize, ConstSize: tt.ConstSize, SurfaceName: tt.SurfaceName}
	case *semantic.DArrayType:
		return &semantic.DArrayType{Elem: substituteType(tt.Elem, subst), Shape: tt.Shape, SurfaceName: tt.SurfaceName}
	case *semantic.DArrayViewType:
		return &semantic.DArrayViewType{Elem: substituteType(tt.Elem, subst), Begin: tt.Begin, End: tt.End, SurfaceName: tt.SurfaceName}
	case *semantic.DListType:
		return &semantic.DListType{Elem: substituteType(tt.Elem, subst), Shape: tt.Shape}
	case *semantic.DListViewType:
		return &semantic.DListViewType{Elem: substituteType(tt.Elem, subst)}
	case *semantic.SViewType:
		return &semantic.SViewType{Begin: tt.Begin, End: tt.End}
	case *semantic.GenericInstanceType:
		args := make([]semantic.Type, 0, len(tt.Args))
		for _, arg := range tt.Args {
			args = append(args, substituteType(arg, subst))
		}
		return &semantic.GenericInstanceType{Name: tt.Name, Base: substituteType(tt.Base, subst), Args: args}
	case *semantic.FuncType:
		params := make([]semantic.Type, 0, len(tt.Params))
		for _, param := range tt.Params {
			params = append(params, substituteType(param, subst))
		}
		return &semantic.FuncType{
			Name:                   tt.Name,
			TypeParams:             append([]string(nil), tt.TypeParams...),
			ShapeParams:            append([]string(nil), tt.ShapeParams...),
			FreshReturnShapeParams: append([]string(nil), tt.FreshReturnShapeParams...),
			Params:                 params,
			Return:                 substituteType(tt.Return, subst),
			Variadic:               tt.Variadic,
		}
	default:
		return t
	}
}

func runtimeDynArrayName(elem semantic.Type) string {
	return mangleGenericType("DynArray", []semantic.Type{elem})
}

func mangleGenericType(base string, args []semantic.Type) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, sanitizeIdentifier(arg.String()))
	}
	if len(parts) == 0 {
		return sanitizeIdentifier(base)
	}
	return sanitizeIdentifier(base) + "__" + strings.Join(parts, "__")
}

func sanitizeIdentifier(s string) string {
	var out strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			out.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			out.WriteRune(r)
		case r >= '0' && r <= '9':
			out.WriteRune(r)
		default:
			out.WriteByte('_')
		}
	}
	value := strings.Trim(out.String(), "_")
	if value == "" {
		return "anon"
	}
	return value
}

func boolToLLVMBool(v bool) C.LLVMBool {
	if v {
		return 1
	}
	return 0
}

func cString(s string) *C.char {
	return C.CString(s)
}

func llvmTypeSlicePtr(types []C.LLVMTypeRef) *C.LLVMTypeRef {
	if len(types) == 0 {
		return nil
	}
	return (*C.LLVMTypeRef)(unsafe.Pointer(&types[0]))
}

func declIsExternVar(decl ast.Decl) bool {
	_, ok := decl.(*ast.ExternVarDecl)
	return ok
}

func llvmValueSlicePtr(values []C.LLVMValueRef) *C.LLVMValueRef {
	if len(values) == 0 {
		return nil
	}
	return (*C.LLVMValueRef)(unsafe.Pointer(&values[0]))
}

func llvmBlockSlicePtr(blocks []C.LLVMBasicBlockRef) *C.LLVMBasicBlockRef {
	if len(blocks) == 0 {
		return nil
	}
	return (*C.LLVMBasicBlockRef)(unsafe.Pointer(&blocks[0]))
}

func (g *llvmGenerator) exprType(expr ast.Expr) semantic.Type {
	if g.result == nil || expr == nil {
		return nil
	}
	if t, ok := g.result.ExprTypes[expr]; ok {
		return t
	}
	return nil
}

func (g *llvmGenerator) constValue(name string) (semantic.ConstValue, bool) {
	if g.result == nil {
		return semantic.ConstValue{}, false
	}
	value, ok := g.result.ConstValues[name]
	return value, ok
}
