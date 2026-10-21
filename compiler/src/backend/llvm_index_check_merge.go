//go:build cgo

package backend

/*
#include <string.h>
#include <llvm-c/Core.h>

// Attaches !prof branch_weights {likely, unlikely} to a conditional branch.
static void elisacoreSetLikelyBranchWeights(LLVMContextRef ctx, LLVMValueRef br, unsigned likely, unsigned unlikely) {
	LLVMTypeRef i32 = LLVMInt32TypeInContext(ctx);
	LLVMMetadataRef ops[3];
	ops[0] = LLVMMDStringInContext2(ctx, "branch_weights", strlen("branch_weights"));
	ops[1] = LLVMValueAsMetadata(LLVMConstInt(i32, likely, 0));
	ops[2] = LLVMValueAsMetadata(LLVMConstInt(i32, unlikely, 0));
	LLVMMetadataRef node = LLVMMDNodeInContext2(ctx, ops, 3);
	unsigned kind = LLVMGetMDKindIDInContext(ctx, "prof", strlen("prof"));
	LLVMSetMetadata(br, kind, LLVMMetadataAsValue(ctx, node));
}
*/
import "C"

import (
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
)

// Consecutive-index check merging.
//
// `v = (a[i] << 16) | (a[i + 1] << 8) | a[i + 2]` pays one bounds check per access (i, i+1, i+2
// < a.count), and LLVM cannot drop the later ones: each guarded access is a separate trap site, so
// the checks must stay in order. When a top-level expression is PURE (no calls other than numeric
// conversions, no assignments, no operator overloads), every evaluation of it is interchangeable,
// so it is emitted twice:
//
//	if K < a.count and i < a.count - K:   // one overflow-safe check for offsets 0..K
//	    fast: the expression with the grouped accesses' checks elided (in range by the guard)
//	else:
//	    slow: the ORIGINAL expression, every check in its original order
//
// The slow path is the unmerged code, so whatever the original would do when an access is out of
// range (trap at that access, an earlier overflow/division trap, a short-circuit that skips the
// access) happens at exactly the same site. The fast path differs only in checks the guard has
// already discharged. A group is one container identifier and one usize base identifier indexed at
// `base` and/or `base + c` with at least two distinct offsets.
const indexMergeMaxOffset = 1 << 16

type indexMergeAccess struct {
	expr   *ast.IndexExpr
	offset uint64
}

type indexMergeGroup struct {
	object   *ast.Ident
	base     *ast.Ident
	accesses []indexMergeAccess
	maxOff   uint64
	offsets  map[uint64]bool
}

var numericConversionMethods = map[string]bool{
	"u8": true, "u16": true, "u32": true, "u64": true, "usize": true,
	"i8": true, "i16": true, "i32": true, "i64": true, "isize": true,
	"f32": true, "f64": true, "int": true,
}

// indexMergePureExpr reports whether evaluating expr twice is indistinguishable from evaluating it
// once: no call (other than a no-argument numeric conversion), no assignment, no operator overload,
// no block, lambda or allocation. Traps are allowed (both copies keep the original sites).
func indexMergePureExpr(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.Ident, *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.CharLit:
		return true
	case *ast.ParenExpr:
		return indexMergePureExpr(n.Inner)
	case *ast.BinaryExpr:
		return n.LoweredCall == nil && indexMergePureExpr(n.Left) && indexMergePureExpr(n.Right)
	case *ast.UnaryExpr:
		return n.LoweredCall == nil && indexMergePureExpr(n.Operand)
	case *ast.IndexExpr:
		return n.Fallback == nil && n.Index2 == nil && indexMergePureExpr(n.Object) && indexMergePureExpr(n.Index)
	case *ast.FieldExpr:
		return !n.Safe && indexMergePureExpr(n.Object)
	case *ast.TernaryExpr:
		// Conditional evaluation is fine: the slow path is the original expression, so an access
		// on the arm not taken is skipped exactly as before.
		return indexMergePureExpr(n.Value) && indexMergePureExpr(n.Cond) && indexMergePureExpr(n.Alt)
	case *ast.CastExpr:
		// `.u32()` / `.cast[T]`: a value conversion (a narrowing one may trap, which is fine).
		return !n.RefShorthand && indexMergePureExpr(n.Operand)
	case *ast.CallExpr:
		field, ok := n.Func.(*ast.FieldExpr)
		return ok && !n.Safe && n.SafeReceiver == nil && len(n.Args) == 0 && numericConversionMethods[field.Field] &&
			indexMergePureExpr(field.Object)
	default:
		return false
	}
}

// indexMergeBaseOffset splits a usize index `b` / `b + c` / `(b + c)` / `c + b` into its base
// identifier and non-negative constant offset.
func indexMergeBaseOffset(index ast.Expr) (*ast.Ident, uint64, bool) {
	switch n := index.(type) {
	case *ast.ParenExpr:
		return indexMergeBaseOffset(n.Inner)
	case *ast.Ident:
		return n, 0, true
	case *ast.BinaryExpr:
		if n.LoweredCall != nil || n.Op != lexer.TOKEN_PLUS {
			return nil, 0, false
		}
		if id, ok := n.Left.(*ast.Ident); ok {
			if c, ok := indexMergeOffsetLit(n.Right); ok {
				return id, c, true
			}
		}
		if id, ok := n.Right.(*ast.Ident); ok {
			if c, ok := indexMergeOffsetLit(n.Left); ok {
				return id, c, true
			}
		}
	}
	return nil, 0, false
}

func indexMergeOffsetLit(expr ast.Expr) (uint64, bool) {
	lit, ok := expr.(*ast.IntLit)
	if !ok || lit.IsHex || (lit.Suffix != "" && lit.Suffix != "usize") {
		return 0, false
	}
	v, err := strconv.ParseUint(lit.Value, 10, 64)
	if err != nil || v > indexMergeMaxOffset {
		return 0, false
	}
	return v, true
}

// indexMergeContainerType is the countable container behind an indexed identifier, or nil.
func indexMergeContainerType(t semantic.Type) semantic.Type {
	if ref, ok := t.(*semantic.RefType); ok {
		t = ref.Elem
	}
	switch t.(type) {
	case *semantic.DArrayType, *semantic.ViewType:
		return t
	}
	return nil
}

func (s *functionState) collectIndexMergeGroups(expr ast.Expr) []*indexMergeGroup {
	usize := s.g.result.NamedTypes["usize"]
	var groups []*indexMergeGroup
	byKey := map[[2]string]*indexMergeGroup{}
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		switch n := e.(type) {
		case *ast.ParenExpr:
			walk(n.Inner)
		case *ast.BinaryExpr:
			walk(n.Left)
			walk(n.Right)
		case *ast.UnaryExpr:
			walk(n.Operand)
		case *ast.FieldExpr:
			walk(n.Object)
		case *ast.CastExpr:
			walk(n.Operand)
		case *ast.TernaryExpr:
			walk(n.Value)
			walk(n.Cond)
			walk(n.Alt)
		case *ast.CallExpr:
			if field, ok := n.Func.(*ast.FieldExpr); ok {
				walk(field.Object)
			}
		case *ast.IndexExpr:
			walk(n.Object)
			walk(n.Index)
			obj, ok := n.Object.(*ast.Ident)
			if !ok || indexMergeContainerType(s.exprType(obj)) == nil {
				return
			}
			if s.g.resultIndexBoundsProven(n) || !semantic.SameType(s.exprType(n.Index), usize) {
				return
			}
			base, off, ok := indexMergeBaseOffset(n.Index)
			if !ok || base.Name == obj.Name || !semantic.SameType(s.exprType(base), usize) {
				return
			}
			key := [2]string{obj.Name, base.Name}
			g := byKey[key]
			if g == nil {
				g = &indexMergeGroup{object: obj, base: base, offsets: map[uint64]bool{}}
				byKey[key] = g
				groups = append(groups, g)
			}
			g.accesses = append(g.accesses, indexMergeAccess{expr: n, offset: off})
			g.offsets[off] = true
			if off > g.maxOff {
				g.maxOff = off
			}
		}
	}
	walk(expr)
	kept := groups[:0]
	for _, g := range groups {
		if len(g.offsets) >= 2 {
			kept = append(kept, g)
		}
	}
	return kept
}

// emitExprWithMergedIndexChecks applies consecutive-index check merging to a pure expression with
// at least one index group. handled=false leaves emission to the ordinary path.
func (s *functionState) emitExprWithMergedIndexChecks(expr ast.Expr, expected semantic.Type) (C.LLVMValueRef, semantic.Type, bool, error) {
	if s == nil || s.g == nil || s.g.result == nil || s.builder == nil || s.fnValue == nil || s.indexMergeDepth > 0 || s.g.forceBoundsCheck {
		return nil, nil, false, nil
	}
	switch expr.(type) {
	case *ast.BinaryExpr, *ast.ParenExpr, *ast.CallExpr, *ast.UnaryExpr, *ast.CastExpr, *ast.TernaryExpr:
	default:
		return nil, nil, false, nil // a lone index has nothing to merge with
	}
	if !indexMergePureExpr(expr) {
		return nil, nil, false, nil
	}
	groups := s.collectIndexMergeGroups(expr)
	if len(groups) == 0 {
		return nil, nil, false, nil
	}
	s.indexMergeDepth++
	defer func() { s.indexMergeDepth-- }()

	usizeLLVM, err := s.g.lowerType(s.g.result.NamedTypes["usize"])
	if err != nil {
		return nil, nil, true, err
	}
	var allOK C.LLVMValueRef
	for _, g := range groups {
		objType := s.exprType(g.object)
		container := indexMergeContainerType(objType)
		var containerPtr C.LLVMValueRef
		if _, isRef := objType.(*semantic.RefType); isRef {
			containerPtr, _, err = s.emitExpr(g.object, nil)
		} else {
			containerPtr, _, err = s.emitAddressOrTemp(g.object)
		}
		if err != nil {
			return nil, nil, true, err
		}
		count, err := s.emitContainerCountValue(containerPtr, container, "merge.count")
		if err != nil {
			return nil, nil, true, err
		}
		base, _, err := s.emitExpr(g.base, nil)
		if err != nil {
			return nil, nil, true, err
		}
		k := C.LLVMConstInt(usizeLLVM, C.ulonglong(g.maxOff), 0)
		// base + c < count for every c in 0..K  <=>  K < count and base < count - K (no overflow).
		roomy := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntUGT), count, k, cStringFree("merge.room"))
		limit := C.LLVMBuildSub(s.builder, count, k, cStringFree("merge.limit"))
		inRange := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), base, limit, cStringFree("merge.in_range"))
		ok := C.LLVMBuildAnd(s.builder, roomy, inRange, cStringFree("merge.ok"))
		if allOK == nil {
			allOK = ok
		} else {
			allOK = C.LLVMBuildAnd(s.builder, allOK, ok, cStringFree("merge.ok"))
		}
	}
	fastBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("merge.fast"))
	slowBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("merge.slow"))
	joinBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("merge.join"))
	br := C.LLVMBuildCondBr(s.builder, allOK, fastBB, slowBB)
	C.elisacoreSetLikelyBranchWeights(s.g.context, br, 2000, 1)

	C.LLVMPositionBuilderAtEnd(s.builder, fastBB)
	prevGuarded := s.mergedIndexGuarded
	s.mergedIndexGuarded = map[*ast.IndexExpr]bool{}
	for _, g := range groups {
		for _, a := range g.accesses {
			s.mergedIndexGuarded[a.expr] = true
		}
	}
	fastValue, fastType, err := s.emitExpr(expr, expected)
	s.mergedIndexGuarded = prevGuarded
	if err != nil {
		return nil, nil, true, err
	}
	fastEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, joinBB)

	C.LLVMPositionBuilderAtEnd(s.builder, slowBB)
	slowValue, _, err := s.emitExpr(expr, expected)
	if err != nil {
		return nil, nil, true, err
	}
	slowEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, joinBB)

	C.LLVMPositionBuilderAtEnd(s.builder, joinBB)
	if fastValue == nil || slowValue == nil {
		return fastValue, fastType, true, nil
	}
	valueType := C.LLVMTypeOf(fastValue)
	if C.LLVMGetTypeKind(valueType) == C.LLVMVoidTypeKind || C.LLVMTypeOf(slowValue) != valueType {
		return nil, nil, true, errIndexMergeShape
	}
	phi := C.LLVMBuildPhi(s.builder, valueType, cStringFree("merge.value"))
	incomingValues := []C.LLVMValueRef{fastValue, slowValue}
	incomingBlocks := []C.LLVMBasicBlockRef{fastEnd, slowEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), &incomingBlocks[0], 2)
	return phi, fastType, true, nil
}

type indexMergeShapeError struct{}

func (indexMergeShapeError) Error() string {
	return "consecutive-index check merging: fast and slow paths produced different value shapes"
}

var errIndexMergeShape error = indexMergeShapeError{}
