package semantic

import (
	"fmt"
)

func (e *OptimizationExtent) String() string {
	if e == nil {
		return "<unknown>"
	}
	switch e.Kind {
	case OptimizationExtentShape:
		if e.Shape == nil {
			return "shape<?>"
		}
		return fmt.Sprintf("shape<%s>", e.Shape.String())
	case OptimizationExtentArraySize:
		if e.HasConstSize {
			return fmt.Sprintf("size<%d>", e.ConstSize)
		}
		return fmt.Sprintf("size<%s>", e.Size)
	case OptimizationExtentViewBounds:
		return fmt.Sprintf("bounds<%s:%s>", e.Begin, e.End)
	default:
		return "<unknown>"
	}
}
func (f OptimizationFacts) HasExactExtent() bool {
	return f.Extent != nil
}
func (f OptimizationFacts) SameExtent(other OptimizationFacts) bool {
	return SameOptimizationExtent(f.Extent, other.Extent)
}
func (f OptimizationFacts) SameExtentSize(other OptimizationFacts) bool {
	return SameOptimizationExtentSize(f.Extent, other.Extent)
}
func (f OptimizationFacts) Disjoint(other OptimizationFacts) bool {
	return OptimizationFactsDisjoint(f, other)
}
func (f OptimizationFacts) HasAnyFacts() bool {
	return f.Exclusive ||
		f.ReadOnly ||
		f.Contiguous ||
		f.UnitStride ||
		f.FrozenPackedStoreOnly ||
		f.Extent != nil ||
		f.base != "" ||
		f.PackedStoreProvenance.HasAnyFacts()
}
func OptimizationFactsDisjoint(a, b OptimizationFacts) bool {
	if a.base == "" || b.base == "" {
		return false
	}
	if a.base != b.base {
		return a.Exclusive && b.Exclusive
	}
	return optimizationExtentsDisjoint(a.Extent, b.Extent)
}
func SameOptimizationExtent(a, b *OptimizationExtent) bool {
	if a == nil || b == nil {
		return false
	}
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case OptimizationExtentShape:
		return SameShape(a.Shape, b.Shape)
	case OptimizationExtentArraySize:
		if a.HasConstSize && b.HasConstSize {
			return a.ConstSize == b.ConstSize
		}
		return a.Size == b.Size
	case OptimizationExtentViewBounds:
		if a.Begin == b.Begin && a.End == b.End {
			return true
		}
		aBegin, aBeginOK := parseOptimizationAffineExpr(a.Begin)
		bBegin, bBeginOK := parseOptimizationAffineExpr(b.Begin)
		aEnd, aEndOK := parseOptimizationAffineExpr(a.End)
		bEnd, bEndOK := parseOptimizationAffineExpr(b.End)
		return aBeginOK && bBeginOK && aEndOK && bEndOK && optimizationAffineExprEqual(aBegin, bBegin) && optimizationAffineExprEqual(aEnd, bEnd)
	default:
		return false
	}
}
func SameOptimizationExtentSize(a, b *OptimizationExtent) bool {
	if a == nil || b == nil {
		return false
	}
	if SameOptimizationExtent(a, b) {
		return true
	}
	aSize, aSizeOK := optimizationExtentAffineSize(a)
	bSize, bSizeOK := optimizationExtentAffineSize(b)
	if aSizeOK && bSizeOK && optimizationAffineExprEqual(aSize, bSize) {
		return true
	}
	switch {
	case a.Kind == OptimizationExtentShape && b.Kind == OptimizationExtentShape:
		return SameShape(a.Shape, b.Shape)
	case a.Kind == OptimizationExtentArraySize && b.Kind == OptimizationExtentArraySize:
		if a.HasConstSize && b.HasConstSize {
			return a.ConstSize == b.ConstSize
		}
		return a.Size != "" && a.Size == b.Size
	case a.Kind == OptimizationExtentArraySize && b.Kind == OptimizationExtentViewBounds:
		if a.HasConstSize {
			if bSize, ok := optimizationExtentConstSize(b); ok {
				return a.ConstSize == bSize
			}
		}
		return a.Size != "" && a.Size == b.Size
	case a.Kind == OptimizationExtentViewBounds && b.Kind == OptimizationExtentArraySize:
		return SameOptimizationExtentSize(b, a)
	case a.Kind == OptimizationExtentViewBounds && b.Kind == OptimizationExtentViewBounds:
		if a.Size != "" && a.Size == b.Size {
			return true
		}
		aBegin, aBeginOK := optimizationConstInt(a.Begin)
		aEnd, aEndOK := optimizationConstInt(a.End)
		bBegin, bBeginOK := optimizationConstInt(b.Begin)
		bEnd, bEndOK := optimizationConstInt(b.End)
		if aBeginOK && aEndOK && bBeginOK && bEndOK {
			return (aEnd - aBegin) == (bEnd - bBegin)
		}
	}
	return false
}
func cloneOptimizationExtent(extent *OptimizationExtent) *OptimizationExtent {
	if extent == nil {
		return nil
	}
	cloned := *extent
	return &cloned
}
func optimizationExtentsDisjoint(a, b *OptimizationExtent) bool {
	if a == nil || b == nil {
		return false
	}
	if a.Kind != OptimizationExtentViewBounds || b.Kind != OptimizationExtentViewBounds {
		return false
	}
	if a.End != "" && a.End == b.Begin {
		return true
	}
	if b.End != "" && b.End == a.Begin {
		return true
	}
	if cmp, ok := optimizationExtentValueCompare(a.End, b.Begin, a.Size); ok && cmp <= 0 {
		return true
	}
	if cmp, ok := optimizationExtentValueCompare(b.End, a.Begin, b.Size); ok && cmp <= 0 {
		return true
	}
	if a.Size != "" && a.Size == b.Size {
		aBeginScale, aBeginBase, aBeginScaleOK := optimizationScaledExtentValue(a.Begin)
		aEndScale, aEndBase, aEndScaleOK := optimizationScaledExtentValue(a.End)
		bBeginScale, bBeginBase, bBeginScaleOK := optimizationScaledExtentValue(b.Begin)
		bEndScale, bEndBase, bEndScaleOK := optimizationScaledExtentValue(b.End)
		if aBeginScaleOK && aEndScaleOK && bBeginScaleOK && bEndScaleOK && aBeginBase == aEndBase && aBeginBase == bBeginBase && aBeginBase == bEndBase {
			if aEndScale <= bBeginScale || bEndScale <= aBeginScale {
				return true
			}
		}
	}
	aBegin, aBeginOK := optimizationConstInt(a.Begin)
	aEnd, aEndOK := optimizationConstInt(a.End)
	bBegin, bBeginOK := optimizationConstInt(b.Begin)
	bEnd, bEndOK := optimizationConstInt(b.End)
	if aEndOK && bBeginOK && aEnd <= bBegin {
		return true
	}
	if bEndOK && aBeginOK && bEnd <= aBegin {
		return true
	}
	return false
}
func optimizationExtentConstSize(extent *OptimizationExtent) (int64, bool) {
	if extent == nil {
		return 0, false
	}
	if extent.HasConstSize {
		return extent.ConstSize, true
	}
	if extent.Size != "" {
		if value, ok := optimizationConstInt(extent.Size); ok {
			return value, true
		}
	}
	if extent.Kind == OptimizationExtentViewBounds {
		begin, beginOK := optimizationConstInt(extent.Begin)
		end, endOK := optimizationConstInt(extent.End)
		if beginOK && endOK {
			return end - begin, true
		}
	}
	return 0, false
}
func optimizationExtentValueCompare(left string, right string, size string) (int, bool) {
	leftAffine, ok := parseOptimizationAffineExpr(left)
	if !ok {
		return 0, false
	}
	rightAffine, ok := parseOptimizationAffineExpr(right)
	if !ok {
		return 0, false
	}
	if cmp, ok := optimizationAffineCompare(leftAffine, rightAffine); ok {
		return cmp, true
	}
	if size != "" {
		if _, ok := optimizationConstInt(size); !ok {
			if cmp, ok := optimizationAffineCompareByPositiveSymbol(leftAffine, rightAffine, optimizationTrimExtentExpr(size)); ok {
				return cmp, true
			}
		}
	}
	if cmp, ok := optimizationAffineCompareBySinglePositiveTerm(leftAffine, rightAffine); ok {
		return cmp, true
	}
	return 0, false
}
func optimizationExtentAffineSize(extent *OptimizationExtent) (optimizationAffineExpr, bool) {
	if extent == nil {
		return optimizationAffineExpr{}, false
	}
	if extent.HasConstSize {
		out := optimizationAffineExpr{Terms: map[string]int64{}}
		out.Const = extent.ConstSize
		return out, true
	}
	if extent.Size != "" {
		return parseOptimizationAffineExpr(extent.Size)
	}
	if extent.Kind != OptimizationExtentViewBounds {
		return optimizationAffineExpr{}, false
	}
	begin, beginOK := parseOptimizationAffineExpr(extent.Begin)
	end, endOK := parseOptimizationAffineExpr(extent.End)
	if !beginOK || !endOK {
		return optimizationAffineExpr{}, false
	}
	return end.sub(begin), true
}
func optimizationSizeOnlyExtent(extent *OptimizationExtent) (*OptimizationExtent, bool) {
	if extent == nil {
		return nil, false
	}
	if extent.HasConstSize {
		return &OptimizationExtent{Kind: OptimizationExtentArraySize, HasConstSize: true, ConstSize: extent.ConstSize, Size: fmt.Sprintf("%d", extent.ConstSize)}, true
	}
	if extent.Kind == OptimizationExtentArraySize && extent.Size != "" {
		return &OptimizationExtent{Kind: OptimizationExtentArraySize, Size: extent.Size}, true
	}
	sizeExpr, ok := optimizationExtentAffineSize(extent)
	if !ok {
		return nil, false
	}
	if optimizationAffineIsConst(sizeExpr) {
		return &OptimizationExtent{Kind: OptimizationExtentArraySize, HasConstSize: true, ConstSize: sizeExpr.Const, Size: fmt.Sprintf("%d", sizeExpr.Const)}, true
	}
	return &OptimizationExtent{Kind: OptimizationExtentArraySize, Size: optimizationAffineExprString(sizeExpr)}, true
}
func optimizationScaledExtentValue(value string) (int64, string, bool) {
	trimmed := optimizationTrimExtentExpr(value)
	if trimmed == "" {
		return 0, "", false
	}
	if constValue, ok := optimizationConstInt(trimmed); ok {
		return constValue, "", true
	}
	sep := " * "
	sepIdx := -1
	for i := 0; i+len(sep) <= len(trimmed); i++ {
		if trimmed[i:i+len(sep)] == sep {
			sepIdx = i
			break
		}
	}
	if sepIdx < 0 {
		return 1, trimmed, true
	}
	left := trimmed[:sepIdx]
	right := trimmed[sepIdx+len(sep):]
	multiplier, ok := optimizationConstInt(left)
	if !ok {
		return 0, "", false
	}
	if right == "" {
		return 0, "", false
	}
	return multiplier, right, true
}
