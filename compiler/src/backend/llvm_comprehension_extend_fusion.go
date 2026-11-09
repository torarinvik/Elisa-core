//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
)

func (s *functionState) fusedDictExtendComprehensionBlock(dstExpr, arg ast.Expr, dstDarray *semantic.DArrayType) (*ast.ExprBlock, bool) {
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
	if !ok || comp == nil || comp.Key != nil || comp.Set || comp.Filter != nil || comp.RangeEnd != nil || comp.Parallel {
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
	if _, isDict := srcType.(*semantic.DictType); !isDict {
		return nil, false
	}
	var usizeType semantic.Type
	if s.g != nil && s.g.result != nil && s.g.hasNamedTypes() {
		usizeType = s.g.namedType("usize")
	}
	if usizeType == nil {
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
	nName := s.g.nextSyntheticName("extend.dict.n.")
	baseName := s.g.nextSyntheticName("extend.dict.base.")
	idxName := s.g.nextSyntheticName("extend.dict.i.")
	nIdent := reg(&ast.Ident{Position: pos, Name: nName}, usizeType)
	baseIdent := reg(&ast.Ident{Position: pos, Name: baseName}, usizeType)
	idxIdent := reg(&ast.Ident{Position: pos, Name: idxName}, usizeType)

	nDecl := &ast.VarDeclStmt{Position: pos, Name: nName, Value: reg(&ast.FieldExpr{Position: pos, Object: srcIdent, Field: "count"}, usizeType)}
	baseDecl := &ast.VarDeclStmt{Position: pos, Name: baseName, Value: reg(&ast.FieldExpr{Position: pos, Object: dstIdent, Field: "count"}, usizeType)}
	resizeCall := &ast.CallExpr{
		Position: pos,
		Func:     &ast.FieldExpr{Position: pos, Object: dstIdent, Field: "resize"},
		Args:     []ast.Expr{reg(&ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: baseIdent, Right: nIdent}, usizeType)},
	}
	idxDecl := &ast.VarDeclStmt{Position: pos, Name: idxName, Mutable: true, Value: reg(&ast.IntLit{Position: pos, Value: "0"}, usizeType)}

	storeTarget := reg(&ast.IndexExpr{Position: pos, Object: dstIdent, Index: reg(&ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: baseIdent, Right: idxIdent}, usizeType)}, dstDarray.Elem)
	store := &ast.AssignStmt{Position: pos, Target: storeTarget, Value: comp.Value}
	bump := &ast.AssignStmt{
		Position: pos,
		Target:   reg(&ast.Ident{Position: pos, Name: idxName}, usizeType),
		Value:    reg(&ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: idxIdent, Right: reg(&ast.IntLit{Position: pos, Value: "1"}, usizeType)}, usizeType),
	}
	body := append([]ast.Stmt{}, comp.Bindings...)
	body = append(body, ast.Stmt(store), ast.Stmt(bump))

	loop := &ast.IterForStmt{Position: pos, Pattern: comprehensionBindPattern(comp), Mode: ast.IterBindValue, Source: comp.Source, Body: body}

	return &ast.ExprBlock{
		Position: pos,
		Stmts: []ast.Stmt{
			nDecl,
			baseDecl,
			&ast.ExprStmt{Position: pos, Expr: resizeCall},
			idxDecl,
			loop,
		},
		Value: reg(&ast.IntLit{Position: pos, Value: "0"}, usizeType),
	}, true
}

// fusedFilteredExtendComprehensionBlock is the FILTERED counterpart of
// fusedExtendComprehensionBlock: `dst.extend([ v for x in src if cond ])`. The output length isn't
// known ahead (the filter selects a subset), so it can't presize exactly — instead it reserves the
// UPPER bound (`dst.count + src.count`) in one growth, then conditionally pushes into the reserved
// capacity. Still no intermediate darray and no memcpy (unlike the materialized extend, which
// builds a temp by push then memcpy's it). reserve (capacity only, count unchanged) avoids the
// zero-fill that a resize-to-upper-bound would do on container-element tails; the pushes into the
// pre-reserved buffer never realloc, so their capacity check is a predictable never-taken branch.
// Returns ok=false (materialized fallback) unless: dst a plain ident, and a FILTERED list
// comprehension over a plain darray-source ident (range/dict/set fall back).
//
// The source is iterated BY INDEX over a snapshotted bound `__n = src.count` (not a live iterator),
// so self-filtered-extend (`xs.extend([v for v in xs if c])`, where src IS dst) is correct: the
// source semantics evaluate the comprehension fully before extend appends, and snapshotting the
// bound reproduces that — the loop reads the original front [0,__n) while pushes land in the
// reserved tail [__base, __base+__n), disjoint since __base>=__n, with no realloc to move the
// buffer. A live `for v in src` would instead re-read the growing count and re-consume its own
// appended elements.
//
//	{ __n = src.count; __base = dst.count; dst.reserve(__base + __n);
//	  for __i in 0..<__n: name = src[__i]; <bindings>; if cond: dst.push(value); 0 }
func (s *functionState) fusedFilteredExtendComprehensionBlock(dstExpr, arg ast.Expr, dstDarray *semantic.DArrayType) (*ast.ExprBlock, bool) {
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
	// This path is specifically the FILTERED, single-binder list comprehension (Filter != nil).
	// Filter-free goes to fusedExtendComprehensionBlock; range/dict/set/two-binder fall back.
	if !ok || comp == nil || comp.Key != nil || comp.Set || comp.Filter == nil || comp.RangeEnd != nil || comp.SecondName != "" {
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
	srcDarray, ok := srcType.(*semantic.DArrayType)
	if !ok || srcDarray == nil {
		return nil, false
	}
	var usizeType semantic.Type
	if s.g != nil && s.g.result != nil && s.g.hasNamedTypes() {
		usizeType = s.g.namedType("usize")
	}
	if usizeType == nil {
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
	nIdent := reg(&ast.Ident{Position: pos, Name: nName}, usizeType)
	baseIdent := reg(&ast.Ident{Position: pos, Name: baseName}, usizeType)
	idxIdent := reg(&ast.Ident{Position: pos, Name: idxName}, usizeType)

	nDecl := &ast.VarDeclStmt{Position: pos, Name: nName, Value: reg(&ast.FieldExpr{Position: pos, Object: srcIdent, Field: "count"}, usizeType)}
	baseDecl := &ast.VarDeclStmt{Position: pos, Name: baseName, Value: reg(&ast.FieldExpr{Position: pos, Object: dstIdent, Field: "count"}, usizeType)}
	// Upper bound = __base + __n. reserve grows capacity once; the filtered pushes stay within it.
	reserveCall := &ast.CallExpr{Position: pos, Func: &ast.FieldExpr{Position: pos, Object: dstIdent, Field: "reserve"}, Args: []ast.Expr{reg(&ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: baseIdent, Right: nIdent}, usizeType)}}

	bindName := comp.Name
	if bindName == "_" {
		bindName = s.g.nextSyntheticName("extend.elem.")
	}
	elemDecl := &ast.VarDeclStmt{Position: pos, Name: bindName, Value: reg(&ast.IndexExpr{Position: pos, Object: srcIdent, Index: idxIdent}, srcDarray.Elem)}
	pushCall := &ast.CallExpr{Position: pos, Func: &ast.FieldExpr{Position: pos, Object: dstIdent, Field: "push"}, Args: []ast.Expr{comp.Value}}
	pushStmt := &ast.ExprStmt{Position: pos, Expr: pushCall}

	body := []ast.Stmt{ast.Stmt(elemDecl)}
	body = append(body, comp.Bindings...)
	body = append(body, &ast.IfStmt{Position: pos, Cond: comp.Filter, Then: []ast.Stmt{pushStmt}})

	startLit := reg(&ast.IntLit{Position: pos, Value: "0"}, usizeType)
	loop := &ast.ForStmt{Position: pos, Name: idxName, Start: startLit, End: nIdent, Op: lexer.TOKEN_RANGE_LT, Body: body}

	return &ast.ExprBlock{
		Position: pos,
		Stmts: []ast.Stmt{
			nDecl,
			baseDecl,
			&ast.ExprStmt{Position: pos, Expr: reserveCall},
			loop,
		},
		Value: reg(&ast.IntLit{Position: pos, Value: "0"}, usizeType),
	}, true
}

// fusedRangeExtendComprehensionBlock is the RANGE-source counterpart of
// fusedExtendComprehensionBlock: `dst.extend([ value for name in start..<end ])`. Like the
// fresh-init range fast path (indexedStoreRangeComprehensionBlock) it presizes once and fills by
// indexed store (vectorizable) — but appends into an existing `dst` instead of a fresh result, so
// the dense output offset is shifted by the current length `__base`:
//
//	{ __base = dst.count;
//	  dst.resize( __base + ((end - start) if end > start else 0) );
//	  for name in start..<end: <bindings>; dst[__base + (name - start)] <- value;
//	  0 }
//
// The clamp keeps the grown count non-negative (the (end-start) branch is only selected when
// end > start, so its unsigned underflow when start >= end is computed-but-discarded). __base is
// snapshotted BEFORE resize (resize changes dst.count). The store index `__base + (name - start)`
// mixes usize (__base) and the range's numeric type (name - start); emitBinaryExpr unifies them via
// CommonNumericType and the darray index coerces the sum to usize — the same rangeType-index the
// fresh-init range block already proves works. A range source can never alias `dst` (it is a range
// literal, not a container), so no snapshot-for-self-extend concern applies. Returns ok=false
// (materialized fallback) unless: dst a plain ident, and a FILTER-FREE, default-step, exclusive
// (`..<`) range list comprehension whose bounds are side-effect-free re-evaluable (ident/int-lit,
// since start/end are read multiple times). Filtered/stepped/inclusive ranges fall back.
func (s *functionState) fusedRangeExtendComprehensionBlock(dstExpr, arg ast.Expr, dstDarray *semantic.DArrayType) (*ast.ExprBlock, bool) {
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
	// Filter-free, default-step, exclusive range list comprehension only. Filtered goes nowhere
	// (falls back to materialized), non-range goes to the darray-source fused paths above.
	if !ok || comp == nil || comp.Key != nil || comp.Set || comp.Filter != nil || comp.RangeEnd == nil {
		return nil, false
	}
	if comp.RangeStep != nil || comp.RangeOp != lexer.TOKEN_RANGE_LT {
		return nil, false
	}
	// start/end are re-read (clamp ternary evaluates each twice, loop once, store index once), so
	// they must be side-effect-free and re-evaluable — same restriction as the fresh-init range block.
	if !comprehensionRangeBoundReEvaluable(comp.Source) || !comprehensionRangeBoundReEvaluable(comp.RangeEnd) {
		return nil, false
	}
	var usizeType semantic.Type
	if s.g != nil && s.g.result != nil && s.g.hasNamedTypes() {
		usizeType = s.g.namedType("usize")
	}
	if usizeType == nil {
		return nil, false
	}
	pos := comp.Position
	start := comp.Source
	end := comp.RangeEnd
	rangeType := s.exprType(end)
	if rangeType == nil {
		rangeType = s.exprType(start)
	}
	if rangeType == nil {
		return nil, false
	}
	var boolType semantic.Type
	if s.g != nil && s.g.result != nil && s.g.hasNamedTypes() {
		boolType = s.g.namedType("bool")
	}
	reg := func(e ast.Expr, t semantic.Type) ast.Expr {
		if t != nil && s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnExprTypesMap() {
			{
				key, value := e, t
				s.g.setResultExprType(key, value)
			}
		}
		return e
	}
	// The store index `name - start` references the loop variable, so a `_` (discard) binder can't
	// be read; substitute a synthetic name (the value never references `_`).
	loopName := comp.Name
	if loopName == "_" {
		loopName = s.g.nextSyntheticName("extend.range.i.")
	}

	baseName := s.g.nextSyntheticName("extend.base.")
	baseIdent := reg(&ast.Ident{Position: pos, Name: baseName}, usizeType)
	baseDecl := &ast.VarDeclStmt{Position: pos, Name: baseName, Value: reg(&ast.FieldExpr{Position: pos, Object: dstIdent, Field: "count"}, usizeType)}

	// count = (end - start) if end > start else 0   — clamped, never negative.
	count := reg(&ast.TernaryExpr{
		Position: pos,
		Value:    reg(&ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_MINUS, Left: end, Right: start}, rangeType),
		Cond:     reg(&ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_GT, Left: end, Right: start}, boolType),
		Alt:      reg(&ast.IntLit{Position: pos, Value: "0"}, rangeType),
	}, rangeType)
	resizeCall := &ast.CallExpr{
		Position: pos,
		Func:     &ast.FieldExpr{Position: pos, Object: dstIdent, Field: "resize"},
		Args:     []ast.Expr{reg(&ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: baseIdent, Right: count}, usizeType)},
	}

	offset := reg(&ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_MINUS, Left: reg(&ast.Ident{Position: pos, Name: loopName}, rangeType), Right: start}, rangeType)
	storeIndex := reg(&ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: baseIdent, Right: offset}, usizeType)
	storeTarget := reg(&ast.IndexExpr{Position: pos, Object: dstIdent, Index: storeIndex}, dstDarray.Elem)
	store := &ast.AssignStmt{Position: pos, Target: storeTarget, Value: comp.Value}
	body := append([]ast.Stmt{}, comp.Bindings...)
	body = append(body, ast.Stmt(store))

	loop := &ast.ForStmt{
		Position:        pos,
		Name:            loopName,
		Start:           start,
		End:             end,
		Op:              lexer.TOKEN_RANGE_LT,
		Body:            body,
		AutovecExpected: comprehensionBodyCallFree(comp),
		AutovecReason:   "extend range comprehension map",
	}

	return &ast.ExprBlock{
		Position: pos,
		Stmts: []ast.Stmt{
			baseDecl,
			&ast.ExprStmt{Position: pos, Expr: resizeCall},
			loop,
		},
		Value: reg(&ast.IntLit{Position: pos, Value: "0"}, usizeType),
	}, true
}

// fusedPushExtendComprehensionBlock is the general catch-all fusion: for any list comprehension
// (not a dict/set-RESULT comprehension) not already handled by a more specific fast path above —
// notably filtered/stepped/inclusive ranges and a filtered dict source — it pushes each element
// DIRECTLY into `dst` instead of materializing a temp darray that extend then memcpy's and discards.
// It reuses `comprehensionLoopStmt` verbatim (the same iteration/filter/bindings construction the
// materialized fresh-init path already uses and this file already exercises), swapping only the sink
// from "append to a fresh result" to "push onto dst":
//
//	<comprehensionLoopStmt's loop, with sink = dst.push(value)>
//
// No presize here (the exact or upper-bound count generally isn't knowable for a stepped/inclusive
// range or a filtered iterable without reimplementing that arithmetic) — the win is solely skipping
// the intermediate darray and its memcpy; dst's push still grows by amortized doubling.
//
// SAFETY — self-extend / iterator invalidation: growing `dst` while iterating a live darray that
// aliases it would invalidate the iterator mid-loop (a hazard the materialized path never has, since
// it iterates the SOURCE while writing into a separate temp, not dst). This function is reached only
// for comprehensions the earlier, more specific fused paths declined: those already cover every case
// where `comp.Source` is a plain-ident darray (filter-free and filtered, self-extend-safe by
// construction). So by the time we get here, a darray-typed source can only be a non-ident expression
// (its potential aliasing with dst can't be proven) — REJECTED, falls back to the fully materialized
// path. Range/dict/set sources can never alias a darray dst, so no restriction is needed there.
