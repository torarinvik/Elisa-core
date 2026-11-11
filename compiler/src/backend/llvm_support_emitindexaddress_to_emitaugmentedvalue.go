//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
)

// indexBoundsProven reports whether the semantic analyzer proved expr's index in-bounds,
// allowing the backend to stamp the resulting GEP with the LLVM `inbounds` keyword.
// This is sound: the analyzer only sets IndexBoundsProven when it has a complete proof
// (loop-range containment, refinement interval, or const-index check), and inbounds
// is only an optimization hint — a wrong inbounds flag would produce undefined behaviour,
// so we only set it when the proof is complete.
func (s *functionState) indexBoundsProven(expr *ast.IndexExpr) bool {
	if expr == nil || s == nil || s.g == nil || s.g.result == nil {
		return false
	}
	proven := s.indexBoundsProvenImpl(expr)
	if s.g.inputReads != nil {
		s.g.inputReads.codegenFactRead(semantic.CodegenFactRead{Result: s.g.result, Table: "IndexProofSpan", Operation: "decision", Key: expr, Value: proven, Present: true})
	}
	return proven
}

func (s *functionState) indexBoundsProvenImpl(expr *ast.IndexExpr) bool {
	view := s.g.indexProofSpanResult(expr)
	if s.g.semanticReadViewFor(view).ValueIndexBoundsProven(expr) {
		return true
	}
	if s.g.indexProofSpanBlocked(view, expr) {
		return false
	}
	current := view == s.g.result
	if current && s.g.boundsOverlay[expr] {
		return true
	}
	// Rebuilt nodes use authority from the semantic view that owns the actual
	// expression, never an equal-count foreign view or another body's overlay.
	shared := s.g.sharedLookups
	if shared != nil && shared.result != view {
		if program := shared.programLookups(); program.result == view {
			shared = program
		}
	}
	if shared != nil && shared.result == view && view.IndexProofSpanCount() == shared.proofCount {
		if s.g.observedIndexSpan(view, "shared-span", shared.indexProofSpans(), expr.Position) {
			return true
		}
		if !current {
			return false
		}
		if s.g.provenIndexSpans == nil || s.g.provenIndexSpansResult != view || s.g.provenIndexSpansSize != len(s.g.boundsOverlay) {
			s.g.provenIndexSpans = make(map[lexer.Pos]bool, len(s.g.boundsOverlay))
			for indexed, proven := range s.g.boundsOverlay {
				if indexed != nil && proven {
					s.g.provenIndexSpans[indexed.Position] = true
				}
			}
			s.g.provenIndexSpansSize = len(s.g.boundsOverlay)
			s.g.provenIndexSpansResult = view
		}
		return s.g.observedIndexSpan(view, "overlay-span", s.g.provenIndexSpans, expr.Position)
	}
	return s.g.observedIndexSpan(view, "derived-span", s.g.provenIndexPositionsFor(view), expr.Position)
}

// observedIndexSpan snapshots the queried position and membership; it is a
// diagnostic observation, not authority for reusing generated code.
func (g *llvmGenerator) observedIndexSpan(view *semantic.Result, operation string, spans map[lexer.Pos]bool, pos lexer.Pos) bool {
	value, present := spans[pos]
	if g.inputReads != nil {
		g.inputReads.codegenFactRead(semantic.CodegenFactRead{Result: view, Table: "IndexProofSpan", Operation: operation, Key: pos, Value: value, Present: present})
	}
	return value
}

func (g *llvmGenerator) provenIndexPositions() map[lexer.Pos]bool {
	return g.provenIndexPositionsFor(g.result)
}

func (g *llvmGenerator) provenIndexPositionsFor(view *semantic.Result) map[lexer.Pos]bool {
	overlaySize := 0
	if view == g.result {
		overlaySize = len(g.boundsOverlay)
	}
	size := view.IndexProofSpanCount() + overlaySize
	if g.provenIndexSpans == nil || g.provenIndexSpansResult != view || g.provenIndexSpansSize != size {
		g.provenIndexSpans = make(map[lexer.Pos]bool, size)
		view.RangeIndexProofSpanAuthority(func(expr *ast.IndexExpr, proven bool) bool {
			if expr != nil && proven {
				g.provenIndexSpans[expr.Position] = true
			}
			return true
		})
		if view == g.result {
			for expr, proven := range g.boundsOverlay {
				if expr != nil && proven {
					g.provenIndexSpans[expr.Position] = true
				}
			}
		}
		g.provenIndexSpansSize = size
		g.provenIndexSpansResult = view
	}
	return g.provenIndexSpans
}

// emitIndexAddress computes the address of an indexed element. userFacing marks
// a genuine user `arr[i]` access (vs compiler-internal carrier reads): only such
// accesses receive the debug-mode bounds watchdog, so debug verification stays
// at the user-operation boundary and never instruments internal machinery.
func (s *functionState) emitIndexAddress(expr *ast.IndexExpr, userFacing bool, readOnly ...bool) (llvmIRValueRef, semantic.Type, error) {
	if expr != nil && expr.Fallback != nil {
		return nil, nil, fmt.Errorf("safe index fallback is not addressable")
	}
	if ptr, elemType, handled, err := s.emitNodeTableIndexAddress(expr); handled {
		return ptr, elemType, err
	}
	objType := s.exprType(expr.Object)
	indexValue, _, err := s.emitExpr(expr.Index, s.g.namedType("usize"))
	if err != nil {
		return nil, nil, err
	}
	// docs/84 Increment 3b: if the indexed object is a proven-distinct container-ref
	// parameter, stage its per-param alias scope so the element load/store gets the
	// distinct alias.scope/noalias instead of the shared "elt" scope. Single-use: it is
	// consumed (and cleared) inside emitRuntimePointerIndexedAddressWithType. Cleared
	// unconditionally first so a non-runtime path (array/plain ref) leaves no stale scope.
	s.pendingDisjointScope = s.disjointScopeForObject(expr.Object)
	// guaranteed: a proof, static check, or dominating trapping guard puts the index in range.
	guaranteed := s.indexBoundsProven(expr)
	zero := llvmConstInt(llvmInt32TypeInContext(s.g.context), 0, 0)
	switch t := objType.(type) {
	case *semantic.ArrayType:
		object := expr.Object
		// An effect grant around a direct global read does not create a mutable
		// lvalue. For a read-only index, however, taking the global's address
		// avoids copying the complete array just to load one element.
		if len(readOnly) != 0 && readOnly[0] {
			unwrapped := object
			for {
				paren, ok := unwrapped.(*ast.ParenExpr)
				if !ok {
					break
				}
				unwrapped = paren.Inner
			}
			if grant, ok := unwrapped.(*ast.CanExpr); ok {
				if ident, ok := grant.Expr.(*ast.Ident); ok {
					if _, local := s.lookupBinding(ident.Name); !local {
						if sym, _, found := s.lookupVisibleGlobalSymbol(ident.Name); found && sym.Kind == semantic.SymbolGlobal {
							object = ident
						}
					}
				}
			}
		}
		arrayPtr, _, err := s.emitAddressOrTemp(object)
		if err != nil {
			return nil, nil, err
		}
		if userFacing {
			g, err := s.emitDebugIndexBoundsGuard(expr, arrayPtr, t, indexValue)
			if err != nil {
				return nil, nil, err
			}
			guaranteed = g
		}
		arrayLLVMType, err := s.g.lowerType(t)
		if err != nil {
			return nil, nil, err
		}
		indices := []llvmIRValueRef{zero, indexValue}
		ptr := llvmBuildGEP2(s.builder, arrayLLVMType, arrayPtr, llvmValueSlicePtr(indices), llvmIRUnsigned(len(indices)), cStringFree("idx.ptr"))
		if guaranteed {
			llvmSetIsInBounds(ptr, 1)
		}
		return ptr, t.Elem, nil
	case *semantic.DArrayType:
		containerPtr, _, err := s.emitAddressOrTemp(expr.Object)
		if err != nil {
			return nil, nil, err
		}
		if userFacing {
			g, err := s.emitDebugIndexBoundsGuard(expr, containerPtr, t, indexValue)
			if err != nil {
				return nil, nil, err
			}
			guaranteed = g
		}
		ptr, elemType, err := s.emitRuntimeIndexedAddress(containerPtr, t, t.Elem, indexValue)
		if err != nil {
			return nil, nil, err
		}
		s.markComprehensionElementPtr(expr.Object, ptr)
		if guaranteed {
			llvmSetIsInBounds(ptr, 1)
		}
		return ptr, elemType, nil
	case *semantic.ViewType:
		containerPtr, _, err := s.emitAddressOrTemp(expr.Object)
		if err != nil {
			return nil, nil, err
		}
		if userFacing {
			g, err := s.emitDebugIndexBoundsGuard(expr, containerPtr, t, indexValue)
			if err != nil {
				return nil, nil, err
			}
			guaranteed = g
		}
		ptr, elemType, err := s.emitRuntimeIndexedAddress(containerPtr, t, t.Elem, indexValue)
		if err != nil {
			return nil, nil, err
		}
		if guaranteed {
			llvmSetIsInBounds(ptr, 1)
		}
		return ptr, elemType, nil
	case *semantic.RefType:
		basePtr, _, err := s.emitExpr(expr.Object, nil)
		if err != nil {
			return nil, nil, err
		}
		// Indexing through a reference dereferences basePtr; guard it so a null/near-null
		// ref traps at the index site (covers `nullRef[i]` reads and writes).
		if err := s.emitDebugPointerDerefGuard(basePtr); err != nil {
			return nil, nil, err
		}
		if arrayElem, ok := t.Elem.(*semantic.ArrayType); ok {
			if userFacing {
				g, err := s.emitDebugIndexBoundsGuard(expr, basePtr, arrayElem, indexValue)
				if err != nil {
					return nil, nil, err
				}
				guaranteed = g
			}
			arrayLLVMType, err := s.g.lowerType(arrayElem)
			if err != nil {
				return nil, nil, err
			}
			indices := []llvmIRValueRef{zero, indexValue}
			ptr := llvmBuildGEP2(s.builder, arrayLLVMType, basePtr, llvmValueSlicePtr(indices), llvmIRUnsigned(len(indices)), cStringFree("idx.ptr"))
			if guaranteed {
				llvmSetIsInBounds(ptr, 1)
			}
			return ptr, arrayElem.Elem, nil
		}
		if elemType, ok := runtimeIndexedElemType(t.Elem); ok {
			if userFacing {
				g, err := s.emitDebugIndexBoundsGuard(expr, basePtr, t.Elem, indexValue)
				if err != nil {
					return nil, nil, err
				}
				guaranteed = g
			}
			ptr, _, err := s.emitRuntimeIndexedAddress(basePtr, t.Elem, elemType, indexValue)
			if err != nil {
				return nil, nil, err
			}
			s.markComprehensionElementPtr(expr.Object, ptr)
			if guaranteed {
				llvmSetIsInBounds(ptr, 1)
			}
			return ptr, elemType, nil
		}
		elemLLVMType, err := s.g.lowerType(t.Elem)
		if err != nil {
			return nil, nil, err
		}
		indices := []llvmIRValueRef{indexValue}
		ptr := llvmBuildGEP2(s.builder, elemLLVMType, basePtr, llvmValueSlicePtr(indices), llvmIRUnsigned(len(indices)), cStringFree("idx.ptr"))
		if guaranteed {
			llvmSetIsInBounds(ptr, 1)
		}
		return ptr, t.Elem, nil
	default:
		return nil, nil, fmt.Errorf("indexing is not implemented for %s", objType.String())
	}
}
func (s *functionState) emitRuntimeIndexedAddress(containerPtr llvmIRValueRef, containerType semantic.Type, elemType semantic.Type, indexValue llvmIRValueRef) (llvmIRValueRef, semantic.Type, error) {
	containerLLVMType, err := s.g.lowerType(containerType)
	if err != nil {
		return nil, nil, err
	}
	return s.emitRuntimePointerIndexedAddressWithType(containerPtr, containerLLVMType, elemType, indexValue)
}

// emitDebugIndexBoundsGuard is the index watchdog: every dynamic container index
// that the analyzer did not PROVE in-bounds is compared against the container's
// count at runtime and traps on violation, AT EVERY OPTIMIZATION LEVEL.
//
// It used to be debug-only ("debug verifies what release assumes"), which made
// a release build memory-unsafe by construction: an unproven index -- including
// one inside `trusted Unsafe.UncheckedIndex` -- GEPed past the array silently.
// The self-hosted compiler mirrored that gate and shipped an out-of-bounds store
// into a neighbouring global (2026-09-09, a text copy landing on
// UiFlat.widget_count). The language is memory safe only if the check is the
// default everywhere; what a proof elides is the only zero-cost path, and
// `trusted` discharges the PERMISSION, not the runtime check. Only containers
// carrying a compile-time or runtime count are guarded.
// It reports guaranteed=true when, on return, the builder is positioned where 0 <= index < count
// is guaranteed: the analyzer proved it, the constant index is statically in range, or a trapping
// guard was just emitted and dominates the insertion point. Only then may the element GEP carry
// `inbounds`; an access with no guarantee (no countable bound) must stay a plain GEP.
func (s *functionState) emitDebugIndexBoundsGuard(expr *ast.IndexExpr, containerPtr llvmIRValueRef, containerType semantic.Type, indexValue llvmIRValueRef) (guaranteed bool, err error) {
	if s == nil || s.g == nil {
		return false, nil
	}
	// No optimization-level gate: see the comment above. -fbounds-check
	// (ELISACORE_FORCE_BOUNDS_CHECK) still exists for the dereference guard below.
	// Watchdog subsumption (docs/85 §9.6, docs/86 86-3): the analyzer proved this index in-bounds,
	// so the debug guard would be redundant. Skip it — a proven access is never double-instrumented.
	if expr != nil && s.g.result != nil && s.g.resultIndexBoundsProven(expr) {
		return true, nil
	}
	// A dominating merged guard (consecutive-index check merging) already established
	// index < count for this access on the current path.
	if expr != nil && s.mergedIndexGuarded[expr] {
		return true, nil
	}
	var countValue llvmIRValueRef
	switch t := containerType.(type) {
	case *semantic.DArrayType, *semantic.ViewType:
		cv, err := s.emitContainerCountValue(containerPtr, containerType, "wd.count")
		if err != nil {
			return false, err
		}
		countValue = cv
	case *semantic.ArrayType:
		// Fixed-size array: the bound is the compile-time element count. This catches
		// out-of-range indexing of `array[T, N]` (framebuffers, lookup tables, byte
		// buffers) which otherwise GEPs past the array silently. Non-const-size arrays
		// carry no static length here, so they're left unguarded.
		if !t.HasConstSize {
			return false, nil
		}
		usizeLLVM, err := s.g.lowerType(s.g.namedType("usize"))
		if err != nil {
			return false, err
		}
		countValue = llvmConstInt(usizeLLVM, llvmIRUnsignedLongLong(t.ConstSize), 0)
	default:
		return false, nil
	}
	// A compile-time-constant index into a known bound can never be out of range at runtime when it
	// is in range here, so the guard would be a `br i1 true` with a dead trap arm. Emitting it adds
	// no verification value (a constant index has no runtime variability to catch) and splits the
	// basic block, which defeats per-block read CSE at repeated indexed accesses (e.g.
	// `a[0].f + a[0].f`). Elide it for the provably-in-bounds constant case.
	if llvmConstIndexInBounds(indexValue, countValue) != 0 {
		return true, nil
	}
	guardedFrom := llvmGetInsertBlock(s.builder)
	inBounds := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULT), indexValue, countValue, cStringFree("wd.in_bounds"))
	okBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("wd.ok"))
	failBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("wd.fail"))
	llvmBuildCondBr(s.builder, inBounds, okBB, failBB)
	// wd.ok is a straight-line continuation of the guarded block (wd.fail always traps), so record it
	// for read-cache block canonicalization (see straightLineBlockParent).
	s.recordStraightLineBlock(okBB, guardedFrom)
	llvmPositionBuilderAtEnd(s.builder, failBB)
	// When -ftrace is also on, record the offending index and the container count into
	// the trace ring just before trapping, so the fault dump shows the exact values
	// (e.g. `oob.index = 4294967295`, `oob.count = 1712`) at the right site.
	if s.g.emitTrace && s.g.trace != nil {
		usize := s.g.namedType("usize")
		line := 0
		s.g.trace.recordValue(s, line, "oob.index", indexValue, usize)
		s.g.trace.recordValue(s, line, "oob.count", countValue, usize)
	}
	if err := s.emitTrapUnreachable("wd.trap"); err != nil {
		return false, err
	}
	llvmPositionBuilderAtEnd(s.builder, okBB)
	return true, nil
}
func (s *functionState) emitRuntimePointerIndexedAddress(containerPtr llvmIRValueRef, lowerContainer func() (llvmIRTypeRef, error), elemType semantic.Type, indexValue llvmIRValueRef) (llvmIRValueRef, semantic.Type, error) {
	containerLLVMType, err := lowerContainer()
	if err != nil {
		return nil, nil, err
	}
	return s.emitRuntimePointerIndexedAddressWithType(containerPtr, containerLLVMType, elemType, indexValue)
}
func (s *functionState) emitRuntimePointerIndexedAddressWithType(containerPtr llvmIRValueRef, containerLLVMType llvmIRTypeRef, elemType semantic.Type, indexValue llvmIRValueRef) (llvmIRValueRef, semantic.Type, error) {
	// docs/84 Increment 3b: consume the staged per-param disjoint scope exactly once and
	// clear it, so a later non-index caller of this function (iter loops, node tables) can
	// never pick up a stale scope from a previous indexing site.
	disjointScope := s.pendingDisjointScope
	s.pendingDisjointScope = nil
	dataFieldPtr := llvmBuildStructGEP2(s.builder, containerLLVMType, containerPtr, 0, cStringFree("idx.data.ptr"))
	dataPtr, err := s.loadValue(dataFieldPtr, &semantic.RefType{Elem: elemType, State: semantic.RefStateNullable, Storage: semantic.RefStorageAny}, "idx.data")
	if err != nil {
		return nil, nil, err
	}
	elemLLVMType, err := s.g.lowerType(elemType)
	if err != nil {
		return nil, nil, err
	}
	indices := []llvmIRValueRef{indexValue}
	ptr := llvmBuildGEP2(s.builder, elemLLVMType, dataPtr, llvmValueSlicePtr(indices), llvmIRUnsigned(len(indices)), cStringFree("idx.ptr"))
	// The data-pointer load reads header memory; every "elt"-tagged access is a scalar leaf
	// that can never be header memory, so the hdr tag is sound for any element type.
	s.tagDarrayHeaderLoad(dataPtr)
	if st, ok := elemType.(*semantic.StructType); ok && !isRuntimeContainerCarrierStruct(st) {
		// A struct element that is not alias-safe as a whole (it holds a header somewhere) still
		// has scalar leaf fields; propagateAliasSafeFieldPtr tags those.
		s.markElementDerivedPtr(ptr)
	}
	if isAliasSafeScalarElemType(elemType) {
		// Scalar element buffer can never contain a darray header, so the data-pointer load (header
		// memory) provably never aliases the element stores (buffer memory). Tag both so LLVM hoists
		// the base-pointer load out of hot loops. See aliasSafeElementPtrs.
		s.markAliasSafeElementPtr(ptr)
		// docs/84 Increment 4: register this element address under its proven-distinct
		// parameter's scope. tagDarrayElementAccess combines the shared darray hdr/elt
		// scope with the per-function disjoint-param scope in one metadata list, preserving
		// both the base-pointer-hoist and cross-param NoAlias claims.
		if disjointScope != nil {
			if s.disjointElementPtrs == nil {
				s.disjointElementPtrs = map[llvmIRValueRef]*disjointParamScope{}
			}
			s.disjointElementPtrs[ptr] = disjointScope
		}
	}
	return ptr, elemType, nil
}
