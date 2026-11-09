//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
)

func (s *functionState) indexedStoreComprehensionBlock(expr *ast.ListComprehensionExpr, resultName string, resultInit ast.Expr, resultIdent *ast.Ident) (*ast.ExprBlock, bool) {
	if expr.Filter != nil || expr.SecondName != "" {
		return nil, false
	}
	if expr.RangeEnd != nil {
		return s.indexedStoreRangeComprehensionBlock(expr, resultName, resultInit, resultIdent)
	}
	srcIdent, ok := expr.Source.(*ast.Ident)
	if !ok {
		return nil, false
	}
	srcType := s.exprType(srcIdent)
	if ref, ok := srcType.(*semantic.RefType); ok {
		srcType = ref.Elem
	}
	srcDarray, ok := srcType.(*semantic.DArrayType)
	if !ok {
		return nil, false
	}
	pos := expr.Position
	// A `_` (discard) element binder can't be declared as a usable local; substitute a synthetic
	// name (the value never references `_`, so this is safe).
	bindName := expr.Name
	if bindName == "_" {
		bindName = s.g.nextSyntheticName("list.comp.elem.")
	}
	idxName := s.g.nextSyntheticName("list.comp.i.")
	idxIdent := &ast.Ident{Position: pos, Name: idxName}
	var usizeType semantic.Type
	if s.g != nil && s.g.result != nil && s.g.hasNamedTypes() {
		usizeType = s.g.namedType("usize")
	}
	srcCount := func() ast.Expr {
		fe := &ast.FieldExpr{Position: pos, Object: srcIdent, Field: "count"}
		if usizeType != nil && s.g.semanticReadView().HasOwnExprTypesMap() {
			{
				key, value := fe, usizeType
				s.g.setResultExprType(key, value)
			}
		}
		return fe
	}
	// Register the synthesized index expressions' element type so backend type inference (which
	// has no structural fallback for IndexExpr) can resolve the element binding and the store.
	registerElemType := func(e ast.Expr) ast.Expr {
		if s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnExprTypesMap() {
			{
				key, value := e, srcDarray.Elem
				s.g.setResultExprType(key, value)
			}
			// The synthesized loop is exactly `0..<src.count`, and the result
			// was resized to that count immediately before it. Both indexed
			// accesses are therefore proved in bounds; retaining that fact keeps
			// the mandatory bounds watchdog out of the vectorizable loop.
			if index, ok := e.(*ast.IndexExpr); ok {
				{
					key, value := index, true
					s.g.setResultIndexBoundsProven(key, value)
				}
			}
		}
		return e
	}
	markSynthesizedIndexProof := func(index *ast.IndexExpr) *ast.IndexExpr {
		if s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnIndexBoundsProvenMap() {
			{
				key, value := index, true
				s.g.setResultIndexBoundsProven(key, value)
			}
		}
		return index
	}

	resultDecl := &ast.VarDeclStmt{Position: pos, Name: resultName, Mutable: true, Value: resultInit}
	resizeCall := &ast.CallExpr{
		Position: pos,
		Func:     &ast.FieldExpr{Position: pos, Object: resultIdent, Field: "resize"},
		Args:     []ast.Expr{srcCount()},
	}

	sourceIndex := markSynthesizedIndexProof(&ast.IndexExpr{Position: pos, Object: srcIdent, Index: idxIdent})
	resultIndex := markSynthesizedIndexProof(&ast.IndexExpr{Position: pos, Object: resultIdent, Index: idxIdent})
	elemDecl := &ast.VarDeclStmt{Position: pos, Name: bindName, Value: registerElemType(sourceIndex)}
	store := &ast.AssignStmt{Position: pos, Target: registerElemType(resultIndex), Value: expr.Value}
	body := []ast.Stmt{ast.Stmt(elemDecl)}
	body = append(body, expr.Bindings...)
	body = append(body, ast.Stmt(store))

	startLit := &ast.IntLit{Position: pos, Value: "0"}
	if usizeType != nil && s.g.semanticReadView().HasOwnExprTypesMap() {
		{
			key, value := startLit, usizeType
			s.g.setResultExprType(key, value)
		}
	}
	loop := &ast.ForStmt{
		Position:        pos,
		Name:            idxName,
		Start:           startLit,
		End:             srcCount(),
		Op:              lexer.TOKEN_RANGE_LT,
		Body:            body,
		AutovecExpected: comprehensionBodyCallFree(expr),
		AutovecReason:   "comprehension map",
	}

	return &ast.ExprBlock{
		Position: pos,
		Stmts: []ast.Stmt{
			resultDecl,
			&ast.ExprStmt{Position: pos, Expr: resizeCall},
			loop,
		},
		Value: resultIdent,
	}, true
}

// fusedExtendComprehensionBlock builds the fused desugar for `dst.extend([ value for name in src ])`
// — a filter-free list comprehension over a plain darray identifier — appending DIRECTLY into `dst`
// with no intermediate darray. Instead of materializing the comprehension into a temp and memcpy-ing
// it (the general extend path), it presizes `dst` ONCE by the source count and fills the appended
// tail slots by indexed store (which auto-vectorizes at -O3, like the fresh-init fast path). Returns
// ok=false — caller falls back to the materialized extend — unless every precondition holds: `dst`
// is a plain identifier (so it is re-evaluable without side effects across `.count`/`.resize`/index
// store), and the argument is a filter-free, darray-source list comprehension.
//
//	{ __n: usize = src.count;          // snapshot BEFORE resize — correct even when src IS dst
//	  __base: usize = dst.count;       //   (self-extend: read region [0,__n) and written tail
//	  dst.resize(__base + __n);        //    region [__base,__base+__n) are disjoint since base>=n)
//	  for __i in 0..<__n: name = src[__i]; <bindings>; dst[__base + __i] <- value; 0 }
func (s *functionState) fusedExtendComprehensionBlock(dstExpr, arg ast.Expr, dstDarray *semantic.DArrayType) (*ast.ExprBlock, bool) {
	if dstDarray == nil {
		return nil, false
	}
	dstIdent, ok := dstExpr.(*ast.Ident)
	if !ok {
		return nil, false
	}
	inner := arg
	for {
		if p, ok := inner.(*ast.ParenExpr); ok && p != nil {
			inner = p.Inner
			continue
		}
		break
	}
	comp, ok := inner.(*ast.ListComprehensionExpr)
	// Filter-free, single-binder, non-range list comprehension — the exact shape the fresh-init
	// fast path (indexedStoreComprehensionBlock) vectorizes. Filtered / range / two-binder
	// sources fall back.
	if !ok || comp == nil || comp.Key != nil || comp.Set || comp.Filter != nil || comp.RangeEnd != nil || comp.SecondName != "" {
		return nil, false
	}
	srcIdent, ok := comp.Source.(*ast.Ident)
	if !ok {
		return nil, false
	}
	srcType := s.exprType(srcIdent)
	if ref, ok := srcType.(*semantic.RefType); ok && ref != nil {
		srcType = ref.Elem
	}
	var usizeType, u8Type semantic.Type
	if s.g != nil && s.g.result != nil && s.g.hasNamedTypes() {
		usizeType = s.g.namedType("usize")
		u8Type = s.g.namedType("u8")
	}
	if usizeType == nil {
		return nil, false
	}
	// Any indexable, count-bearing source with the same read-N-then-store shape fuses. darray reads
	// `.count` (usize); an sview reads `.len` (i64) and yields u8 per element. Both support `src[i]`,
	// so the loop body is identical modulo element type. The i64 length on sview mixes with the usize
	// base in the store index — emitBinaryExpr unifies via CommonNumericType and the darray index
	// coerces the sum, exactly as the range-source fusion already relies on.
	var srcElem semantic.Type
	var countField string
	var countType semantic.Type
	switch st := srcType.(type) {
	case *semantic.DArrayType:
		if st == nil {
			return nil, false
		}
		srcElem, countField, countType = st.Elem, "count", usizeType
	case *semantic.SViewType:
		if u8Type == nil {
			return nil, false
		}
		srcElem, countField, countType = u8Type, "len", s.g.namedType("i64")
		if countType == nil {
			return nil, false
		}
	default:
		return nil, false
	}
	pos := comp.Position
	reg := func(e ast.Expr, t semantic.Type) ast.Expr {
		if t != nil && s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnExprTypesMap() {
			{
				key, value := e, t
				s.g.setResultExprType(key, value)
			}
		}
		return e
	}
	nName := s.g.nextSyntheticName("extend.n.")
	baseName := s.g.nextSyntheticName("extend.base.")
	idxName := s.g.nextSyntheticName("extend.i.")
	// The loop counter runs in the source's count type (usize for darray, i64 for sview); the base
	// stays usize. The store index's usize+countType sum coerces on the darray-index boundary.
	nIdent := reg(&ast.Ident{Position: pos, Name: nName}, countType)
	baseIdent := reg(&ast.Ident{Position: pos, Name: baseName}, usizeType)
	idxIdent := reg(&ast.Ident{Position: pos, Name: idxName}, countType)

	nDecl := &ast.VarDeclStmt{Position: pos, Name: nName, Value: reg(&ast.FieldExpr{Position: pos, Object: srcIdent, Field: countField}, countType)}
	baseDecl := &ast.VarDeclStmt{Position: pos, Name: baseName, Value: reg(&ast.FieldExpr{Position: pos, Object: dstIdent, Field: "count"}, usizeType)}
	resizeCall := &ast.CallExpr{
		Position: pos,
		Func:     &ast.FieldExpr{Position: pos, Object: dstIdent, Field: "resize"},
		Args:     []ast.Expr{reg(&ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: baseIdent, Right: nIdent}, usizeType)},
	}

	// A `_` (discard) element binder can't be declared as a usable local; substitute a synthetic
	// name (the value never references `_`, so this is safe).
	bindName := comp.Name
	if bindName == "_" {
		bindName = s.g.nextSyntheticName("extend.elem.")
	}
	elemDecl := &ast.VarDeclStmt{Position: pos, Name: bindName, Value: reg(&ast.IndexExpr{Position: pos, Object: srcIdent, Index: idxIdent}, srcElem)}
	storeTarget := reg(&ast.IndexExpr{Position: pos, Object: dstIdent, Index: reg(&ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: baseIdent, Right: idxIdent}, usizeType)}, dstDarray.Elem)
	store := &ast.AssignStmt{Position: pos, Target: storeTarget, Value: comp.Value}
	body := []ast.Stmt{ast.Stmt(elemDecl)}
	body = append(body, comp.Bindings...)
	body = append(body, ast.Stmt(store))

	startLit := reg(&ast.IntLit{Position: pos, Value: "0"}, countType)
	loop := &ast.ForStmt{
		Position:        pos,
		Name:            idxName,
		Start:           startLit,
		End:             nIdent,
		Op:              lexer.TOKEN_RANGE_LT,
		Body:            body,
		AutovecExpected: comprehensionBodyCallFree(comp),
		AutovecReason:   "extend comprehension map",
	}

	return &ast.ExprBlock{
		Position: pos,
		Stmts: []ast.Stmt{
			nDecl,
			baseDecl,
			&ast.ExprStmt{Position: pos, Expr: resizeCall},
			loop,
		},
		// The extend call's own return value (the receiver ptr) is re-derived by the caller; this
		// throwaway value only satisfies emitExprBlock's non-nil-Value requirement and is DCE'd.
		Value: reg(&ast.IntLit{Position: pos, Value: "0"}, usizeType),
	}, true
}

// fusedDictExtendComprehensionBlock is the DICT-source counterpart of
// fusedExtendComprehensionBlock: `dst.extend([ value for k, v in src ])` (or the single-binder
// entry-tuple head) over a plain dict identifier, filter-free. A dict's count is exact and the
// comprehension is filter-free, so the output length is `src.count` — presize `dst` once and fill
// the appended tail slots through a RUNNING COUNTER (a dict has no integer element indexing, so
// the store index is `__base + __i` with `__i` incremented per iteration, not the loop index of an
// indexed source). Depends on `for ... in someDict` running exactly `src.count` iterations — the
// dict-iteration overcount bug that previously blocked this path (task_dcf0ced9) is fixed and
// pinned by TestRunCLIDictIterationVisitsExactlyCountEntries; a presized buffer plus an overcount
// would be an out-of-bounds store. A dict source can never alias a darray dst, so no self-extend
// snapshotting concern applies. Returns ok=false (later fused paths / materialized fallback)
// unless: dst a plain ident, and a FILTER-FREE list comprehension over a plain dict-source ident.
//
//	{ __n: usize = src.count; __base: usize = dst.count; dst.resize(__base + __n);
//	  __i: mutable usize = 0;
//	  for <binders> in src: <bindings>; dst[__base + __i] <- value; __i <- __i + 1;
//	  0 }
