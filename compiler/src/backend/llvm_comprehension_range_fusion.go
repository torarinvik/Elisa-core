//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
)

func (s *functionState) fusedPushExtendComprehensionBlock(dstExpr, arg ast.Expr, dstDarray *semantic.DArrayType) (*ast.ExprBlock, bool) {
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
	if !ok || comp == nil || comp.Key != nil || comp.Set || comp.Parallel {
		return nil, false
	}
	if comp.RangeEnd == nil {
		srcType := s.exprType(comp.Source)
		if ref, ok := srcType.(*semantic.RefType); ok && ref != nil {
			srcType = ref.Elem
		}
		if _, isDArray := srcType.(*semantic.DArrayType); isDArray {
			srcIdent, identOK := comp.Source.(*ast.Ident)
			if !identOK || srcIdent.Name == dstIdent.Name {
				return nil, false
			}
		}
	}
	pos := comp.Position
	pushCall := &ast.CallExpr{Position: pos, Func: &ast.FieldExpr{Position: pos, Object: dstIdent, Field: "push"}, Args: []ast.Expr{comp.Value}}
	loop := comprehensionLoopStmt(comp, pushCall)

	var usizeType semantic.Type
	if s.g != nil && s.g.result != nil && s.g.hasNamedTypes() {
		usizeType = s.g.namedType("usize")
	}
	value := ast.Expr(&ast.IntLit{Position: pos, Value: "0"})
	if usizeType != nil && s.g.result != nil && s.g.semanticReadView().HasOwnExprTypesMap() {
		{
			key, value := value, usizeType
			s.g.setResultExprType(key, value)
		}
	}

	return &ast.ExprBlock{
		Position: pos,
		Stmts:    []ast.Stmt{loop},
		Value:    value,
	}, true
}

// exprContainsCall reports whether an expression contains a function/method call. Used to gate the
// AutovecExpected marker: a call in the loop body legitimately blocks auto-vectorization (the
// callee may not inline), so warning about it would be noise rather than a real defect. Unknown
// node types are treated conservatively as "contains a call" so the marker (and thus the warning)
// is only set for clearly-simple, call-free bodies — false negatives, never false positives.
func exprContainsCall(e ast.Expr) bool {
	return ast.ExprContainsCall(e)
}

// comprehensionBodyCallFree reports whether a comprehension's value expression and all of its
// per-element head bindings are call-free — the precondition for marking its fused loop as
// AutovecExpected.
func comprehensionBodyCallFree(expr *ast.ListComprehensionExpr) bool {
	if exprContainsCall(expr.Value) {
		return false
	}
	for _, b := range expr.Bindings {
		if vd, ok := b.(*ast.VarDeclStmt); ok && exprContainsCall(vd.Value) {
			return false
		}
	}
	return true
}

// comprehensionRangeBoundReEvaluable reports whether a range bound can be evaluated more than
// once without changing observable behavior (no side effects, no recomputation cost that matters).
// Plain identifiers and integer literals qualify; anything else (calls, field/index access,
// arithmetic) is conservatively rejected so the comprehension keeps the push fallback.
func comprehensionRangeBoundReEvaluable(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.Ident:
		return true
	case *ast.IntLit:
		return true
	case *ast.ParenExpr:
		return comprehensionRangeBoundReEvaluable(n.Inner)
	default:
		return false
	}
}

// indexedStoreRangeComprehensionBlock is the range-source counterpart of
// indexedStoreComprehensionBlock: `[ value for name in start..<end ]` (no filter, default step,
// exclusive `..<`) lowers to a presized indexed-store loop so the fused build loop vectorizes
// instead of per-element push. The output index is the dense offset `name - start`:
//
//	{ result: mutable darray[T] = []; result.resize( (end - start) if end > start else 0 );
//	  for name in start..<end: <bindings>; result[name - start] <- value;
//	  result }
//
// The resize bound is clamped to a non-negative count via the ternary; the (end - start)
// branch is only selected when end > start, so its unsigned-domain underflow when start >= end
// is computed-but-discarded. start/end reuse the comprehension's already-analyzed range nodes,
// so the synthesized loop and arithmetic inherit their types with no extra registration.
func (s *functionState) indexedStoreRangeComprehensionBlock(expr *ast.ListComprehensionExpr, resultName string, resultInit ast.Expr, resultIdent *ast.Ident) (*ast.ExprBlock, bool) {
	if expr.RangeStep != nil || expr.RangeOp != lexer.TOKEN_RANGE_LT {
		return nil, false
	}
	// start/end are reused 3x (the clamp ternary evaluates each twice, the loop once), so they
	// must be side-effect-free and re-evaluable — otherwise we'd change evaluation count vs the
	// single-evaluation push fallback. Restrict to plain identifiers and integer literals.
	if !comprehensionRangeBoundReEvaluable(expr.Source) {
		return nil, false
	}
	pos := expr.Position
	start := expr.Source
	end := expr.RangeEnd
	// A computed end (`0..<(n + 1).usize()`) is bound once to a synthetic local ahead of the
	// result, so it is still evaluated exactly once (after the re-evaluable start, as the push
	// fallback orders them) and the presized fill applies to the common `n + 1` table shape.
	var endDecl ast.Stmt
	if !comprehensionRangeBoundReEvaluable(end) {
		endType := s.exprType(end)
		if endType == nil {
			return nil, false
		}
		endName := s.g.nextSyntheticName("comp.range.end.")
		endDecl = &ast.VarDeclStmt{Position: pos, Name: endName, Value: end}
		endIdent := &ast.Ident{Position: pos, Name: endName}
		if s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnExprTypesMap() {
			s.g.setResultExprType(endIdent, endType)
		} else {
			return nil, false
		}
		end = endIdent
	}
	// The index `loopName - start` references the loop variable, so it needs a real name. A `_`
	// (discard) binder can't be read; substitute a synthetic name (the value never references `_`,
	// so this is safe). Without this the synthesized `_ - start` reads an unbound discard.
	loopName := expr.Name
	if loopName == "_" {
		loopName = s.g.nextSyntheticName("comp.range.i.")
	}

	// The synthesized arithmetic/ternary nodes have no analyzed types; the backend infers a
	// TernaryExpr's result from its branches' exprType (nil -> void) and compares operands in
	// their exprType, so register each. rangeType is the range's numeric type; resize coerces it
	// to usize. start/end themselves are reused analyzed nodes and need no registration.
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

	// count = (end - start) if end > start else 0   — clamped, never negative.
	count := reg(&ast.TernaryExpr{
		Position: pos,
		Value:    reg(&ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_MINUS, Left: end, Right: start}, rangeType),
		Cond:     reg(&ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_GT, Left: end, Right: start}, boolType),
		Alt:      reg(&ast.IntLit{Position: pos, Value: "0"}, rangeType),
	}, rangeType)

	resultDecl := &ast.VarDeclStmt{Position: pos, Name: resultName, Mutable: true, Value: resultInit}
	resizeCall := &ast.CallExpr{
		Position: pos,
		Func:     &ast.FieldExpr{Position: pos, Object: resultIdent, Field: "resize"},
		Args:     []ast.Expr{count},
	}

	storeIndex := reg(&ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_MINUS, Left: reg(&ast.Ident{Position: pos, Name: loopName}, rangeType), Right: start}, rangeType)
	resultIndex := &ast.IndexExpr{Position: pos, Object: resultIdent, Index: storeIndex}
	// For every executed range iteration, end > start and 0 <= loopName-start < end-start;
	// the clamped resize therefore establishes a real bounds proof for this synthesized store.
	if s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnIndexBoundsProvenMap() {
		{
			key, value := resultIndex, true
			s.g.setResultIndexBoundsProven(key, value)
		}
	}
	store := &ast.AssignStmt{Position: pos, Target: resultIndex, Value: expr.Value}
	body := append([]ast.Stmt{}, expr.Bindings...)
	body = append(body, ast.Stmt(store))

	loop := &ast.ForStmt{
		Position:        pos,
		Name:            loopName,
		Start:           start,
		End:             end,
		Op:              lexer.TOKEN_RANGE_LT,
		Body:            body,
		AutovecExpected: comprehensionBodyCallFree(expr),
		AutovecReason:   "comprehension map",
	}

	return &ast.ExprBlock{
		Position: pos,
		Stmts:    comprehensionRangeStmts(endDecl, resultDecl, &ast.ExprStmt{Position: pos, Expr: resizeCall}, loop),
		Value:    resultIdent,
	}, true
}

func comprehensionRangeStmts(endDecl ast.Stmt, rest ...ast.Stmt) []ast.Stmt {
	if endDecl == nil {
		return rest
	}
	return append([]ast.Stmt{endDecl}, rest...)
}

// comprehensionLoopStmt builds the fused loop for a comprehension desugar: the per-element
// head bindings (recomputed each iteration), then the `sink` call wrapped in the `if filter`.
// The filter is emitted in-body (not the iterator's Filter field) so head bindings are in scope.
func comprehensionLoopStmt(expr *ast.ListComprehensionExpr, sink ast.Expr) ast.Stmt {
	loopBody := append([]ast.Stmt{}, expr.Bindings...)
	sinkStmt := ast.Stmt(&ast.ExprStmt{Position: expr.Position, Expr: sink})
	if expr.Filter != nil {
		loopBody = append(loopBody, &ast.IfStmt{Position: expr.Position, Cond: expr.Filter, Then: []ast.Stmt{sinkStmt}})
	} else {
		loopBody = append(loopBody, sinkStmt)
	}
	if expr.RangeEnd != nil {
		return &ast.ForStmt{Position: expr.Position, Name: expr.Name, Start: expr.Source, End: expr.RangeEnd, Step: expr.RangeStep, Op: expr.RangeOp, Body: loopBody}
	}
	return &ast.IterForStmt{Position: expr.Position, Pattern: comprehensionBindPattern(expr), Mode: ast.IterBindValue, Source: expr.Source, Body: loopBody}
}

// comprehensionBindPattern builds the iterator binding pattern for a comprehension head:
// a plain name for the single-binder head, a two-element tuple destructure for the
// two-binder `for k, v in src` head (dict entries, darray-of-tuples).
func comprehensionBindPattern(expr *ast.ListComprehensionExpr) ast.MoveBindPattern {
	if expr.SecondName != "" {
		return &ast.MoveBindTuplePattern{Position: expr.Position, Args: []ast.MoveBindArg{
			{Position: expr.Position, Name: expr.Name},
			{Position: expr.Position, Name: expr.SecondName},
		}}
	}
	return &ast.MoveBindNamePattern{Position: expr.Position, Name: expr.Name}
}

// comprehensionDesugarBlock wraps the result declaration + fused loop into the ExprBlock
// `{ result: mutable = init; <loop>; result }` shared by the list/dict/set comprehension paths.
func comprehensionDesugarBlock(expr *ast.ListComprehensionExpr, resultName string, resultInit ast.Expr, resultIdent *ast.Ident, sink ast.Expr) *ast.ExprBlock {
	return &ast.ExprBlock{
		Position: expr.Position,
		Stmts: []ast.Stmt{
			&ast.VarDeclStmt{Position: expr.Position, Name: resultName, Mutable: true, Value: resultInit},
			comprehensionLoopStmt(expr, sink),
		},
		Value: resultIdent,
	}
}

// emitDictComprehensionExpr lowers `{ key: value for name in source [if filter] }` into
//
//	{ result: mutable dict[K,V] = {};  for name in source: (if filter:) result.insert(key, value);  result }
//
// — a single fused loop with no intermediate collection (docs/79). Mirrors the darray
// comprehension path, substituting an empty braced dict literal and `insert` for `push`.
