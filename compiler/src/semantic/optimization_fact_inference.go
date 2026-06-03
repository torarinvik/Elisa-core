package semantic

import (
	"elisacore/src/ast"
)

func optimizationFactsForType(t Type) OptimizationFacts {
	if t == nil {
		return OptimizationFacts{}
	}
	switch tt := t.(type) {
	case *RefType:
		facts := optimizationFactsForType(tt.Elem)
		if builtin, ok := tt.Elem.(*BuiltinType); ok && builtin.Name == "u8" && tt.Storage == RefStorageStatic {
			facts.ReadOnly = true
			facts.Contiguous = true
			facts.UnitStride = true
		}
		return facts
	case *ArrayType:
		return OptimizationFacts{
			Contiguous: true,
			UnitStride: true,
			Extent: &OptimizationExtent{
				Kind:         OptimizationExtentArraySize,
				Size:         tt.Size,
				HasConstSize: tt.HasConstSize,
				ConstSize:    tt.ConstSize,
			},
		}
	case *DArrayType:
		facts := OptimizationFacts{Contiguous: true, UnitStride: true}
		if !isWildcardShape(tt.Shape) {
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentShape, Shape: tt.Shape}
		}
		return facts
	case *ViewType:
		facts := OptimizationFacts{Contiguous: true, UnitStride: true}
		if tt.Begin != "" || tt.End != "" {
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: tt.Begin, End: tt.End}
		}
		return facts
	case *DArrayViewType:
		facts := OptimizationFacts{Contiguous: true, UnitStride: true}
		if tt.SurfaceName == "packedview" || tt.SurfaceName == "packedtags" {
			facts.ReadOnly = true
		}
		if tt.Begin != "" || tt.End != "" {
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: tt.Begin, End: tt.End}
		}
		return facts
	case *DStrType:
		facts := OptimizationFacts{ReadOnly: true, Contiguous: true, UnitStride: true}
		if !isWildcardShape(tt.Shape) {
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentShape, Shape: tt.Shape}
		}
		return facts
	case *SViewType:
		facts := OptimizationFacts{ReadOnly: true, Contiguous: true, UnitStride: true}
		if tt.Begin != "" || tt.End != "" {
			facts.Extent = &OptimizationExtent{Kind: OptimizationExtentViewBounds, Begin: tt.Begin, End: tt.End}
		}
		return facts
	case *GenericInstanceType:
		if _, ok := dynArrayRuntimeInstance(tt); ok {
			return OptimizationFacts{Contiguous: true, UnitStride: true}
		}
		if _, ok := ChunksExactViewInstance(tt); ok {
			return OptimizationFacts{Contiguous: true, UnitStride: true}
		}
		return OptimizationFacts{}
	case *StructType:
		if dynArrayView, ok := dynArrayViewRuntimeType(tt); ok && dynArrayView != nil {
			return OptimizationFacts{Contiguous: true, UnitStride: true}
		}
		if stringView, ok := stringViewRuntimeType(tt); ok && stringView != nil {
			return OptimizationFacts{ReadOnly: true, Contiguous: true, UnitStride: true}
		}
		return OptimizationFacts{}
	default:
		return OptimizationFacts{}
	}
}

func (a *Analyzer) inferExprOptimizationFacts(expr ast.Expr, t Type) OptimizationFacts {
	return a.inferExprOptimizationFactsWithBase(expr, t, optimizationFactsForType(t))
}

func (a *Analyzer) inferExprOptimizationFactsWithBase(expr ast.Expr, t Type, facts OptimizationFacts) OptimizationFacts {
	if expr == nil {
		return facts
	}
	switch n := expr.(type) {
	case *ast.Ident:
		if identFacts, ok := a.lookupIdentOptimizationFacts(n); ok {
			facts = identFacts
		}
		if facts.base == "" {
			facts.base = a.optimizationBaseForExpr(n)
		}
	case *ast.IndexExpr:
		if resolved, ok := a.resolveIndexedValueExpr(n.Object, n.Index); ok {
			if resolvedFacts, ok := a.exprFacts[resolved]; ok {
				facts = resolvedFacts
				facts.Exclusive = false
			}
			if facts.base == "" {
				facts.base = a.optimizationBaseForExpr(resolved)
			}
		}
		if chunkFacts, ok := a.inferChunksExactItemOptimizationFacts(n); ok {
			facts = overlayOptimizationFacts(facts, chunkFacts)
		}
		if n.Fallback != nil {
			facts = a.inferRecoveredExprOptimizationFacts(&ast.IndexExpr{Position: n.Position, Object: n.Object, Index: n.Index}, n.Fallback, facts)
		}
	case *ast.FieldExpr:
		if resolved, ok := a.resolveProjectedFieldValueExpr(n.Object, n.Field); ok {
			if resolvedFacts, ok := a.exprFacts[resolved]; ok {
				facts = resolvedFacts
				facts.Exclusive = false
			}
		}
		if splitFacts, ok := a.inferSplitViewFieldOptimizationFacts(n); ok {
			facts = overlayOptimizationFacts(facts, splitFacts)
		}
		if n.Field == "values" {
			if tableInfo, ok := a.nodeTableInfoForExpr(n.Object); ok && tableInfo.Enum != nil {
				if objectFacts, ok := a.exprFacts[n.Object]; ok && objectFacts.Exclusive {
					facts.Exclusive = true
				}
				facts.ReadOnly = false
				facts.Contiguous = true
				facts.UnitStride = true
				if base := a.optimizationBaseForExpr(n.Object); base != "" {
					facts.base = base + ".values"
				} else {
					facts.base = optimizationExprString(expr)
				}
				if tableInfo.CountExpr != "" {
					facts.Extent = &OptimizationExtent{
						Kind:  OptimizationExtentViewBounds,
						Begin: "0",
						End:   tableInfo.CountExpr,
					}
				}
				facts.PackedStoreProvenance = PackedStoreProvenance{
					HasPackedStoreDeps:       true,
					HasFrozenPackedStoreDeps: true,
				}
				facts.FrozenPackedStoreOnly = true
			}
		}
		if n.Field == "tags" {
			if storeType, ok := a.exprTypes[n.Object].(*PackedEnumStoreType); ok && IsFrozenPackedEnumStoreType(storeType) {
				facts.ReadOnly = true
				facts.Contiguous = true
				facts.UnitStride = true
				facts.base = optimizationExprString(expr)
				facts.Extent = &OptimizationExtent{
					Kind:  OptimizationExtentViewBounds,
					Begin: "0",
					End:   optimizationExprString(&ast.FieldExpr{Position: n.Position, Object: n.Object, Field: "count"}),
				}
			}
		}
		if facts.base == "" {
			facts.base = a.optimizationBaseForExpr(n.Object)
		}
	case *ast.CallExpr:
		facts = a.inferCallOptimizationFacts(n, facts)
	case *ast.AllocExpr:
		if _, ok := t.(*RefType); ok {
			facts.Exclusive = true
			facts.base = optimizationExprIdentity(expr)
		}
	case *ast.StringLit:
		facts.ReadOnly = true
		facts.Contiguous = true
		facts.UnitStride = true
	case *ast.SliceExpr:
		if objectFacts, ok := a.lookupOptimizationFactsForExpr(n.Object); ok {
			if facts.base == "" {
				facts.base = objectFacts.base
				if facts.base == "" {
					facts.base = a.optimizationBaseForExpr(n.Object)
				}
			}
			facts.ReadOnly = facts.ReadOnly || objectFacts.ReadOnly
			facts.Contiguous = facts.Contiguous || objectFacts.Contiguous
			facts.UnitStride = facts.UnitStride || objectFacts.UnitStride
		}
		if field := a.sliceFullSpanField(n.Object); field != "" {
			if extent := a.inferRelativeOptimizationExtent(n.Object, n.Start, n.End, field); extent != nil {
				facts.Extent = extent
			}
		} else if fieldExpr, ok := stripOptimizationParens(n.Object).(*ast.FieldExpr); ok && fieldExpr.Field == "tags" {
			if extent := a.inferRelativeOptimizationExtent(fieldExpr.Object, n.Start, n.End, "count"); extent != nil {
				facts.Extent = extent
			}
		}
		if facts.base == "" {
			facts.base = a.optimizationBaseForExpr(n.Object)
		}
	case *ast.TernaryExpr:
		facts = a.inferRecoveredExprOptimizationFacts(n.Value, n.Alt, facts)
	case *ast.TryExpr:
		facts = a.inferRecoveredExprOptimizationFacts(n.Value, n.Fallback, facts)
	case *ast.UnwrapElseExpr:
		facts = a.inferRecoveredExprOptimizationFacts(n.Value, n.Fallback, facts)
	case *ast.GetExpr:
		facts = a.inferRecoveredExprOptimizationFacts(n.Value, n.Fallback, facts)
	case *ast.OptionalBindExpr:
		if valueFacts, ok := a.lookupOptimizationFactsForExpr(n.Value); ok {
			facts = overlayOptimizationFacts(facts, valueFacts)
		}
	}
	if typeMayCarryRegionProvenanceForOptimization(t) {
		if provenance, ok := a.exprPackedStoreProvenance(expr); ok {
			facts.PackedStoreProvenance = provenance
			facts.FrozenPackedStoreOnly = provenance.DependsOnlyOnFrozenPackedStores()
		}
	}
	return facts
}

func exprRequiresOptimizationFactInference(expr ast.Expr) bool {
	switch expr.(type) {
	case *ast.Ident, *ast.IndexExpr, *ast.FieldExpr, *ast.CallExpr, *ast.AllocExpr, *ast.SliceExpr, *ast.TernaryExpr, *ast.TryExpr, *ast.UnwrapElseExpr, *ast.GetExpr:
		return true
	default:
		return false
	}
}

func typeMayCarryRegionProvenanceForOptimization(t Type) bool {
	switch tt := t.(type) {
	case nil:
		return false
	case *InvalidType, *NeverType, *NullType, *BuiltinType, *RefStorageValueType, *ErrorSetType, *ConstEnumType, *FuncType, *OpaqueType:
		return false
	case *ErrorUnionType:
		return typeMayCarryRegionProvenanceForOptimization(tt.Value)
	case *OptionalType:
		return typeMayCarryRegionProvenanceForOptimization(tt.Value)
	case *ArrayType:
		return typeMayCarryRegionProvenanceForOptimization(tt.Elem)
	case *DictType:
		return typeMayCarryRegionProvenanceForOptimization(tt.Key) || typeMayCarryRegionProvenanceForOptimization(tt.Value)
	case *AggregateStateType:
		return typeMayCarryRegionProvenanceForOptimization(tt.Base)
	default:
		return true
	}
}

func (a *Analyzer) inferRecoveredExprOptimizationFacts(value ast.Expr, fallback ast.Expr, facts OptimizationFacts) OptimizationFacts {
	if a == nil {
		return facts
	}
	if fallback == nil || a.exprDefinitelyNever(fallback) {
		if valueFacts, ok := a.lookupOptimizationFactsForExpr(value); ok {
			facts = overlayOptimizationFacts(facts, valueFacts)
		}
		if facts.base == "" {
			facts.base = a.optimizationBaseForExpr(value)
		}
		return facts
	}
	valueFacts, valueOK := a.lookupOptimizationFactsForExpr(value)
	fallbackFacts, fallbackOK := a.lookupOptimizationFactsForExpr(fallback)
	if valueOK && fallbackOK {
		if merged, ok := mergeAlternativeOptimizationFacts(valueFacts, fallbackFacts); ok {
			facts = overlayOptimizationFacts(facts, merged)
		}
	}
	if facts.base == "" {
		facts.base = a.sharedOptimizationBaseForExprs(value, fallback)
	}
	return facts
}

func overlayOptimizationFacts(dst OptimizationFacts, src OptimizationFacts) OptimizationFacts {
	if src.Exclusive {
		dst.Exclusive = true
	}
	if src.ReadOnly {
		dst.ReadOnly = true
	}
	if src.Contiguous {
		dst.Contiguous = true
	}
	if src.UnitStride {
		dst.UnitStride = true
	}
	if src.Extent != nil {
		dst.Extent = cloneOptimizationExtent(src.Extent)
	}
	if src.base != "" {
		dst.base = src.base
	}
	return dst
}

func overlayViewCarrierOptimizationFacts(dst OptimizationFacts, src OptimizationFacts) OptimizationFacts {
	dst = overlayOptimizationFacts(dst, src)
	if src.Extent != nil {
		dst.Extent = nil
	}
	return dst
}

func mergeAlternativeOptimizationFacts(left OptimizationFacts, right OptimizationFacts) (OptimizationFacts, bool) {
	merged := OptimizationFacts{}
	if left.base != "" && left.base == right.base {
		merged.base = left.base
	}
	merged.ReadOnly = left.ReadOnly || right.ReadOnly
	merged.Contiguous = left.Contiguous && right.Contiguous
	merged.UnitStride = left.UnitStride && right.UnitStride
	if SameOptimizationExtent(left.Extent, right.Extent) {
		merged.Extent = cloneOptimizationExtent(left.Extent)
		if merged.base != "" && left.Exclusive && right.Exclusive {
			merged.Exclusive = true
		}
	} else if SameOptimizationExtentSize(left.Extent, right.Extent) {
		if sizeExtent, ok := optimizationSizeOnlyExtent(left.Extent); ok {
			merged.Extent = sizeExtent
		} else if sizeExtent, ok := optimizationSizeOnlyExtent(right.Extent); ok {
			merged.Extent = sizeExtent
		}
	}
	if !merged.Exclusive && !merged.ReadOnly && !merged.Contiguous && !merged.UnitStride && merged.Extent == nil && merged.base == "" {
		return OptimizationFacts{}, false
	}
	return merged, true
}
