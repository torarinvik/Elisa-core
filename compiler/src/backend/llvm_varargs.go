//go:build cgo

package backend

import (
	"fmt"
	"unsafe"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// emitVaArgCall lowers the compiler-owned generic surface
//
//	va_arg[T](storage)
//
// to LLVM's va_arg instruction. LLVM exposes this as a builder operation rather
// than an ordinary callable intrinsic, so leaving the call to the normal extern
// path would produce an unresolved `va_arg` symbol at link time. The generic
// return type supplies the exact ABI type selected by the source program while
// the storage argument remains the opaque pointer produced by va_start.
func (s *functionState) emitVaArgCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil || s == nil || s.g == nil {
		return nil, nil, true, fmt.Errorf("va_arg requires an active function context")
	}
	name := callIdentName(expr)
	if name == "" {
		name = callSpecializedIdentName(expr)
	}
	if name != "va_arg" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("va_arg expects one va_list storage argument, got %d", len(expr.Args))
	}
	resultType := s.exprType(expr)
	if resultType == nil || semantic.IsInvalidType(resultType) {
		return nil, nil, true, fmt.Errorf("va_arg has no valid result type")
	}
	storage, _, err := s.emitExpr(expr.Args[0], nil)
	if err != nil {
		return nil, nil, true, err
	}
	if storage == nil {
		return nil, nil, true, fmt.Errorf("va_arg storage expression emitted no value")
	}
	llvmResultType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, fmt.Errorf("va_arg result type %s: %w", resultType, err)
	}
	nameC := cString("va_arg")
	defer llvmFree(unsafe.Pointer(nameC))
	value := llvmBuildVAArg(s.builder, storage, llvmResultType, nameC)
	if value == nil {
		return nil, nil, true, fmt.Errorf("LLVM failed to build va_arg for result type %s", resultType)
	}
	return value, resultType, true, nil
}

// emitVaCopyCall lowers the compiler-owned va_copy compatibility primitive.
// LLVM models va_copy as an overloaded intrinsic whose overload is the pointer
// address space, so declaring it as an ordinary extern or relying on the
// generic @intrinsic spelling can manufacture a wrongly mangled symbol. Resolve
// the intrinsic explicitly and call the declaration LLVM gives us.
func (s *functionState) emitVaCopyCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil || s == nil || s.g == nil {
		return nil, nil, true, fmt.Errorf("va_copy requires an active function context")
	}
	name := callIdentName(expr)
	if name != "llvm_va_copy" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 {
		return nil, nil, true, fmt.Errorf("va_copy expects destination and source storage arguments, got %d", len(expr.Args))
	}
	destination, _, err := s.emitExpr(expr.Args[0], nil)
	if err != nil {
		return nil, nil, true, err
	}
	source, _, err := s.emitExpr(expr.Args[1], nil)
	if err != nil {
		return nil, nil, true, err
	}
	if destination == nil || source == nil {
		return nil, nil, true, fmt.Errorf("va_copy storage expression emitted no value")
	}
	intrinsicName := cStringFree("llvm.va_copy")
	intrinsicID := llvmLookupIntrinsicID(intrinsicName, llvmIRSize(len("llvm.va_copy")))
	if intrinsicID == 0 {
		return nil, nil, true, fmt.Errorf("unknown LLVM intrinsic llvm.va_copy")
	}
	intrinsic := llvmGetIntrinsicDeclaration(s.g.module, intrinsicID, llvmTypeSlicePtr([]llvmIRTypeRef{llvmTypeOf(destination)}), 1)
	if intrinsic == nil {
		return nil, nil, true, fmt.Errorf("LLVM failed to declare llvm.va_copy")
	}
	args := []llvmIRValueRef{destination, source}
	// LLVM still dereferences the name for a void call, so use a non-nil empty
	// C string rather than cStringFree("") (which intentionally returns nil).
	emptyName := cString("")
	defer llvmFree(unsafe.Pointer(emptyName))
	call := llvmBuildCall2(s.builder, llvmGlobalGetValueType(intrinsic), intrinsic, llvmValueSlicePtr(args), 2, emptyName)
	if call == nil {
		return nil, nil, true, fmt.Errorf("LLVM failed to build llvm.va_copy")
	}
	return call, s.exprType(expr), true, nil
}
