package semantic

import (
	"elisacore/src/ast"
	"fmt"
)

func (a *Analyzer) recordImmutableSymbolOptimizationFacts(sym *Symbol, expr ast.Expr) {
	if a == nil || sym == nil || expr == nil || sym.Mutable || a.symbolFacts == nil {
		return
	}
	facts, ok := a.exprFacts[expr]
	if !ok {
		return
	}
	facts.Exclusive = false
	a.symbolFacts[sym] = facts
}

func (a *Analyzer) lookupIdentOptimizationFacts(ident *ast.Ident) (OptimizationFacts, bool) {
	if a == nil || ident == nil || a.symbolFacts == nil {
		return OptimizationFacts{}, false
	}
	if a.currentScope != nil {
		if sym, ok := a.currentScope.Lookup(ident.Name); ok {
			if facts, ok := a.symbolFacts[sym]; ok {
				return facts, true
			}
			if root := symbolAliasRoot(sym); root != nil && root != sym {
				if facts, ok := a.symbolFacts[root]; ok {
					return facts, true
				}
			}
		}
	}
	if a.globalScope != nil {
		if sym, ok := a.globalScope.Lookup(ident.Name); ok {
			if facts, ok := a.symbolFacts[sym]; ok {
				return facts, true
			}
		}
	}
	return OptimizationFacts{}, false
}

func (a *Analyzer) inferCallOptimizationFacts(call *ast.CallExpr, facts OptimizationFacts) OptimizationFacts {
	if call == nil {
		return facts
	}
	switch optimizationHelperName(call.Func) {
	case "readonly":
		if sourceFacts, ok := a.exprFactsForCallArg(call, 0); ok {
			facts = overlayOptimizationFacts(facts, sourceFacts)
		}
		facts.ReadOnly = true
		facts.Exclusive = false
	case "split_at":
		if sourceFacts, ok := a.exprFactsForCallArg(call, 0); ok {
			facts.base = sourceFacts.base
			facts.ReadOnly = sourceFacts.ReadOnly
			facts.Contiguous = sourceFacts.Contiguous
			facts.UnitStride = sourceFacts.UnitStride
			facts.Exclusive = false
		}
	case "chunks_exact":
		if sourceFacts, ok := a.exprFactsForCallArg(call, 0); ok {
			facts.base = sourceFacts.base
			facts.ReadOnly = sourceFacts.ReadOnly
			facts.Contiguous = sourceFacts.Contiguous
			facts.UnitStride = sourceFacts.UnitStride
		}
		if callName := optimizationExprString(call); callName != "" {
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: "0", End: callName + ".len"}
		}
		facts.Exclusive = false
	case "tree_tags":
		facts.ReadOnly = true
		facts.Contiguous = true
		facts.UnitStride = true
		facts.Exclusive = false
		if callName := optimizationExprString(call); callName != "" {
			facts.base = callName
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: "0", End: callName + ".len"}
		}
	case "tree_column":
		facts.ReadOnly = true
		facts.Contiguous = true
		facts.UnitStride = true
		facts.Exclusive = false
		if callName := optimizationExprString(call); callName != "" {
			facts.base = callName
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: "0", End: callName + ".len"}
		}
	case "column":
		facts.ReadOnly = true
		facts.Contiguous = true
		facts.UnitStride = true
		facts.Exclusive = false
		if callName := optimizationExprString(call); callName != "" {
			facts.base = callName
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: "0", End: callName + ".len"}
		}
	case "arena_da_view", "ctx_string_view", "sview", "string_view":
		if baseExpr, ok := optimizationCallArg(call, 0); ok {
			facts.base = a.optimizationBaseForExpr(baseExpr)
			if baseFacts, ok := a.lookupOptimizationFactsForExpr(baseExpr); ok {
				facts = overlayViewCarrierOptimizationFacts(facts, baseFacts)
			}
		}
		if extent := a.inferViewHelperExtent(call, 0, 1, 2, "count"); extent != nil {
			facts.Extent = extent
		}
	case "arena_da_view_prefix":
		if viewExpr, ok := optimizationCallArg(call, 0); ok {
			facts.base = a.optimizationBaseForExpr(viewExpr)
			if viewFacts, ok := a.lookupOptimizationFactsForExpr(viewExpr); ok {
				facts = overlayViewCarrierOptimizationFacts(facts, viewFacts)
			}
		}
		if facts.Extent == nil {
			if viewExpr, ok := optimizationCallArg(call, 0); ok {
				if endExpr, ok := optimizationCallArg(call, 1); ok {
					zeroExpr := &ast.IntLit{Position: call.Position, Value: "0"}
					facts.Extent = a.inferRelativeOptimizationExtent(viewExpr, zeroExpr, endExpr, "len")
				}
			}
		}
	case "arena_da_view_suffix":
		if viewExpr, ok := optimizationCallArg(call, 0); ok {
			facts.base = a.optimizationBaseForExpr(viewExpr)
			if viewFacts, ok := a.lookupOptimizationFactsForExpr(viewExpr); ok {
				facts = overlayViewCarrierOptimizationFacts(facts, viewFacts)
			}
		}
		if facts.Extent == nil {
			if viewExpr, ok := optimizationCallArg(call, 0); ok {
				if startExpr, ok := optimizationCallArg(call, 1); ok {
					endExpr := &ast.FieldExpr{Position: call.Position, Object: viewExpr, Field: "len"}
					facts.Extent = a.inferRelativeOptimizationExtent(viewExpr, startExpr, endExpr, "len")
				}
			}
		}
	case "string_view_prefix", "ctx_string_view_prefix":
		if viewExpr, ok := optimizationCallArg(call, 0); ok {
			facts.base = a.optimizationBaseForExpr(viewExpr)
			if viewFacts, ok := a.lookupOptimizationFactsForExpr(viewExpr); ok {
				facts = overlayViewCarrierOptimizationFacts(facts, viewFacts)
			}
		}
		if facts.Extent == nil {
			if viewExpr, ok := optimizationCallArg(call, 0); ok {
				if endExpr, ok := optimizationCallArg(call, 1); ok {
					zeroExpr := &ast.IntLit{Position: call.Position, Value: "0"}
					facts.Extent = a.inferRelativeOptimizationExtent(viewExpr, zeroExpr, endExpr, "len")
				}
			}
		}
	case "string_view_suffix", "ctx_string_view_suffix":
		if viewExpr, ok := optimizationCallArg(call, 0); ok {
			facts.base = a.optimizationBaseForExpr(viewExpr)
			if viewFacts, ok := a.lookupOptimizationFactsForExpr(viewExpr); ok {
				facts = overlayViewCarrierOptimizationFacts(facts, viewFacts)
			}
		}
		if facts.Extent == nil {
			if viewExpr, ok := optimizationCallArg(call, 0); ok {
				if startExpr, ok := optimizationCallArg(call, 1); ok {
					endExpr := &ast.FieldExpr{Position: call.Position, Object: viewExpr, Field: "len"}
					facts.Extent = a.inferRelativeOptimizationExtent(viewExpr, startExpr, endExpr, "len")
				}
			}
		}
	case "arena_da_view_slice", "ctx_string_view_slice", "string_view_slice":
		if viewExpr, ok := optimizationCallArg(call, 0); ok {
			facts.base = a.optimizationBaseForExpr(viewExpr)
			if viewFacts, ok := a.lookupOptimizationFactsForExpr(viewExpr); ok {
				facts = overlayViewCarrierOptimizationFacts(facts, viewFacts)
			}
		}
		if extent := a.inferViewHelperExtent(call, 0, 1, 2, "len"); extent != nil {
			facts.Extent = extent
		}
	case "arena_da_from_view":
		if viewFacts, ok := a.exprFactsForCallArg(call, 1); ok && viewFacts.HasExactExtent() {
			facts.Extent = cloneOptimizationExtent(viewFacts.Extent)
		}
	case "ctx_string_from_view":
		if viewFacts, ok := a.exprFactsForCallArg(call, 0); ok && viewFacts.HasExactExtent() {
			facts.Extent = cloneOptimizationExtent(viewFacts.Extent)
		}
	case "ctx_string_slice":
		if extent := a.inferViewHelperExtent(call, 0, 1, 2, "len"); extent != nil {
			facts.Extent = extent
		}
	case "string_view_copy":
		if viewFacts, ok := a.exprFactsForCallArg(call, 0); ok && viewFacts.HasExactExtent() {
			facts.Extent = cloneOptimizationExtent(viewFacts.Extent)
		}
	}
	return facts
}

func (a *Analyzer) optimizationBaseForExpr(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	stripped := stripOptimizationParens(expr)
	switch n := stripped.(type) {
	case *ast.Ident:
		if sym := a.lookupOptimizationSymbol(n); sym != nil {
			if a != nil && a.symbolFacts != nil {
				if facts, ok := a.symbolFacts[sym]; ok && facts.base != "" {
					return facts.base
				}
			}
			return optimizationSymbolIdentity(sym)
		}
	case *ast.AllocExpr:
		return optimizationExprIdentity(n)
	case *ast.IndexExpr:
		if n.Fallback != nil {
			if a.exprDefinitelyNever(n.Fallback) {
				return a.optimizationBaseForExpr(&ast.IndexExpr{Position: n.Position, Object: n.Object, Index: n.Index})
			}
			return a.sharedOptimizationBaseForExprs(&ast.IndexExpr{Position: n.Position, Object: n.Object, Index: n.Index}, n.Fallback)
		}
		if resolved, ok := a.resolveIndexedValueExpr(n.Object, n.Index); ok {
			return a.optimizationBaseForExpr(resolved)
		}
	case *ast.MoveExpr:
		return a.optimizationBaseForExpr(n.Operand)
	case *ast.CanExpr:
		return a.optimizationBaseForExpr(n.Expr)
	case *ast.TernaryExpr:
		return a.sharedOptimizationBaseForExprs(n.Value, n.Alt)
	case *ast.TryExpr:
		if n.Fallback == nil || a.exprDefinitelyNever(n.Fallback) {
			return a.optimizationBaseForExpr(n.Value)
		}
		return a.sharedOptimizationBaseForExprs(n.Value, n.Fallback)
	case *ast.UnwrapElseExpr:
		if a.exprDefinitelyNever(n.Fallback) {
			return a.optimizationBaseForExpr(n.Value)
		}
		return a.sharedOptimizationBaseForExprs(n.Value, n.Fallback)
	case *ast.OptionalBindExpr:
		return a.optimizationBaseForExpr(n.Value)
	}
	if a != nil && a.exprFacts != nil {
		if facts, ok := a.exprFacts[stripped]; ok && facts.base != "" {
			return facts.base
		}
	}
	return ""
}

func (a *Analyzer) sharedOptimizationBaseForExprs(left ast.Expr, right ast.Expr) string {
	leftBase := a.optimizationBaseForExpr(left)
	if leftBase == "" || right == nil {
		return leftBase
	}
	rightBase := a.optimizationBaseForExpr(right)
	if leftBase == rightBase {
		return leftBase
	}
	return ""
}

func (a *Analyzer) lookupOptimizationSymbol(ident *ast.Ident) *Symbol {
	if a == nil || ident == nil {
		return nil
	}
	if a.currentScope != nil {
		if sym, ok := a.currentScope.Lookup(ident.Name); ok {
			return sym
		}
	}
	if a.globalScope != nil {
		if sym, ok := a.globalScope.Lookup(ident.Name); ok {
			return sym
		}
	}
	return nil
}

func optimizationSymbolIdentity(sym *Symbol) string {
	if sym == nil {
		return ""
	}
	return fmt.Sprintf("symbol@%p", sym)
}

func optimizationExprIdentity(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	return fmt.Sprintf("expr@%p", expr)
}

func (a *Analyzer) inferViewHelperExtent(call *ast.CallExpr, baseIndex, startIndex, endIndex int, fullSpanField string) *OptimizationExtent {
	baseExpr, ok := optimizationCallArg(call, baseIndex)
	if !ok {
		return nil
	}
	startExpr, hasStart := optimizationCallArg(call, startIndex)
	endExpr, hasEnd := optimizationCallArg(call, endIndex)
	if !hasStart || !hasEnd {
		return nil
	}
	return a.inferRelativeOptimizationExtent(baseExpr, startExpr, endExpr, fullSpanField)
}

func (a *Analyzer) inferRelativeOptimizationExtent(baseExpr ast.Expr, startExpr ast.Expr, endExpr ast.Expr, fullSpanField string) *OptimizationExtent {
	if a == nil || baseExpr == nil || startExpr == nil || endExpr == nil {
		return nil
	}
	begin := optimizationExprString(startExpr)
	end := optimizationExprString(endExpr)
	if begin == "" || end == "" {
		return nil
	}
	baseFacts, ok := a.lookupOptimizationFactsForExpr(baseExpr)
	if ok && baseFacts.HasExactExtent() {
		if isZeroOptimizationExpr(startExpr) && optimizationFieldMatches(endExpr, baseExpr, fullSpanField) {
			return cloneOptimizationExtent(baseFacts.Extent)
		}
		if optimizationFieldMatches(endExpr, baseExpr, fullSpanField) && baseFacts.Extent != nil && baseFacts.Extent.Kind == OptimizationExtentViewBounds {
			composed := composeOptimizationExtentWithExactBase(baseFacts.Extent, begin, end)
			if composed != nil {
				composed.End = baseFacts.Extent.End
			}
			return composed
		}
		return composeOptimizationExtentWithExactBase(baseFacts.Extent, begin, end)
	}
	return &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: begin, End: end}
}

func optimizationHelperName(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.ParenExpr:
		return optimizationHelperName(n.Inner)
	case *ast.SpecializeExpr:
		return optimizationHelperName(n.Operand)
	default:
		return ""
	}
}

func (a *Analyzer) exprFactsForCallArg(call *ast.CallExpr, index int) (OptimizationFacts, bool) {
	expr, ok := optimizationCallArg(call, index)
	if !ok {
		return OptimizationFacts{}, false
	}
	return a.lookupOptimizationFactsForExpr(expr)
}

func (a *Analyzer) lookupOptimizationFactsForExpr(expr ast.Expr) (OptimizationFacts, bool) {
	if a == nil || expr == nil {
		return OptimizationFacts{}, false
	}
	if facts, ok := a.exprFacts[expr]; ok {
		return facts, true
	}
	ident, ok := stripOptimizationParens(expr).(*ast.Ident)
	if !ok {
		return OptimizationFacts{}, false
	}
	return a.lookupIdentOptimizationFacts(ident)
}

func (a *Analyzer) boundCallExpr(expr ast.Expr) (*ast.CallExpr, bool) {
	if expr == nil {
		return nil, false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.boundCallExpr(n.Inner)
	case *ast.CastExpr:
		return a.boundCallExpr(n.Operand)
	case *ast.MoveExpr:
		return a.boundCallExpr(n.Operand)
	case *ast.Ident:
		if a.currentScope == nil {
			return nil, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok || sym.Mutable {
			return nil, false
		}
		if a.currentValueBindings != nil {
			if valueExpr, ok := a.currentValueBindings[sym]; ok && valueExpr != nil {
				return a.boundCallExpr(valueExpr)
			}
		}
		decl, ok := sym.Node.(*ast.VarDeclStmt)
		if !ok || decl.Value == nil {
			return nil, false
		}
		return a.boundCallExpr(decl.Value)
	case *ast.CallExpr:
		return n, true
	default:
		return nil, false
	}
}

func (a *Analyzer) inferSplitViewFieldOptimizationFacts(expr *ast.FieldExpr) (OptimizationFacts, bool) {
	if a == nil || expr == nil || expr.Field == "" {
		return OptimizationFacts{}, false
	}
	call, ok := a.boundCallExpr(expr.Object)
	if !ok || callIdentName(call) != "split_at" || len(call.Args) < 2 {
		return OptimizationFacts{}, false
	}
	if expr.Field != "left" && expr.Field != "right" {
		return OptimizationFacts{}, false
	}
	baseFacts, ok := a.lookupOptimizationFactsForExpr(call.Args[0])
	if !ok {
		return OptimizationFacts{}, false
	}
	facts := baseFacts
	if facts.base == "" {
		facts.base = a.optimizationBaseForExpr(call.Args[0])
	}
	if facts.Extent != nil {
		index := optimizationExprString(call.Args[1])
		if index != "" {
			begin := "0"
			end := optimizationExprString(&ast.FieldExpr{Position: call.Position, Object: call.Args[0], Field: "len"})
			if facts.Extent.Kind == OptimizationExtentViewBounds {
				if facts.Extent.Begin != "" {
					begin = facts.Extent.Begin
				}
				if facts.Extent.End != "" {
					end = facts.Extent.End
				}
			}
			if expr.Field == "right" {
				facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: optimizationAddOffsetExpr(begin, index), End: end}
			} else {
				facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: begin, End: optimizationAddOffsetExpr(begin, index)}
			}
			if expr.Field == "left" && optimizationFieldMatches(call.Args[1], call.Args[0], "len") {
				facts.Extent = cloneOptimizationExtent(baseFacts.Extent)
			}
			if expr.Field == "right" && isZeroOptimizationExpr(call.Args[1]) {
				facts.Extent = cloneOptimizationExtent(baseFacts.Extent)
			}
		}
	}
	return facts, true
}

func (a *Analyzer) inferChunksExactItemOptimizationFacts(expr *ast.IndexExpr) (OptimizationFacts, bool) {
	if a == nil || expr == nil {
		return OptimizationFacts{}, false
	}
	objectType := a.exprTypes[expr.Object]
	if _, ok := ChunksExactViewItemType(objectType); !ok {
		return OptimizationFacts{}, false
	}
	objectFacts, ok := a.exprFacts[expr.Object]
	if !ok {
		return OptimizationFacts{}, false
	}
	facts := objectFacts
	facts.Exclusive = false
	if facts.base == "" {
		facts.base = a.optimizationBaseForExpr(expr.Object)
	}
	chunkSize := ""
	chunkSizeConst, chunkSizeConstOK := int64(0), false
	if call, ok := a.boundCallExpr(expr.Object); ok && callIdentName(call) == "chunks_exact" && len(call.Args) >= 2 {
		chunkSize = optimizationExprString(call.Args[1])
		chunkSizeConst, chunkSizeConstOK = a.resolveProjectedFieldConstIntExpr(call.Args[1])
	}
	if chunkSize == "" {
		chunkSize = optimizationExprString(&ast.FieldExpr{Position: expr.Position, Object: expr.Object, Field: "chunk_size"})
	}
	chunkIndex, chunkIndexOK := a.resolveProjectedFieldConstIntExpr(expr.Index)
	sourceBegin := "0"
	if call, ok := a.boundCallExpr(expr.Object); ok && callIdentName(call) == "chunks_exact" && len(call.Args) >= 1 {
		if sourceFacts, ok := a.lookupOptimizationFactsForExpr(call.Args[0]); ok && sourceFacts.Extent != nil && sourceFacts.Extent.Kind == OptimizationExtentViewBounds && sourceFacts.Extent.Begin != "" {
			sourceBegin = sourceFacts.Extent.Begin
		}
	}
	if chunkSize != "" {
		switch {
		case chunkIndexOK && chunkSizeConstOK:
			offset := fmt.Sprintf("%d", chunkIndex*chunkSizeConst)
			begin := optimizationAddOffsetExpr(sourceBegin, offset)
			end := optimizationAddOffsetExpr(begin, fmt.Sprintf("%d", chunkSizeConst))
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Size: chunkSize, Begin: begin, End: end}
		case chunkIndexOK:
			begin := optimizationAddOffsetExpr(sourceBegin, optimizationChunkExtentOffsetExpr(chunkSize, chunkIndex))
			end := optimizationAddOffsetExpr(sourceBegin, optimizationChunkExtentOffsetExpr(chunkSize, chunkIndex+1))
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Size: chunkSize, Begin: begin, End: end}
		default:
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentArraySize, Size: chunkSize, HasConstSize: chunkSizeConstOK, ConstSize: chunkSizeConst}
		}
	}
	return facts, true
}
