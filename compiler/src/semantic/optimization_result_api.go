package semantic

import (
	"llcontext/src/ast"
)

func (r *Result) ExprOptimizationFacts(expr ast.Expr) (OptimizationFacts, bool) {
	if r == nil || expr == nil || r.ExprFacts == nil {
		return OptimizationFacts{}, false
	}
	facts, ok := r.ExprFacts[expr]
	return facts, ok
}

func (r *Result) ExprsHaveSameExtent(left, right ast.Expr) bool {
	if r == nil {
		return false
	}
	leftFacts, ok := r.ExprOptimizationFacts(left)
	if !ok {
		return false
	}
	rightFacts, ok := r.ExprOptimizationFacts(right)
	if !ok {
		return false
	}
	return leftFacts.SameExtent(rightFacts)
}

func (r *Result) ExprsHaveEqualExtentSize(left, right ast.Expr) bool {
	if r == nil {
		return false
	}
	leftFacts, ok := r.ExprOptimizationFacts(left)
	if !ok {
		return false
	}
	rightFacts, ok := r.ExprOptimizationFacts(right)
	if !ok {
		return false
	}
	return leftFacts.SameExtentSize(rightFacts)
}

func (r *Result) ExprsAreDisjoint(left, right ast.Expr) bool {
	if r == nil {
		return false
	}
	leftFacts, ok := r.ExprOptimizationFacts(left)
	if !ok {
		return false
	}
	rightFacts, ok := r.ExprOptimizationFacts(right)
	if !ok {
		return false
	}
	return leftFacts.Disjoint(rightFacts)
}

func (r *Result) ExprSupportsDenseWrite(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	facts, ok := r.ExprOptimizationFacts(expr)
	if !ok {
		return false
	}
	return facts.Contiguous && facts.UnitStride && !facts.ReadOnly
}

func (r *Result) ExprPackedStoreProvenance(expr ast.Expr) (PackedStoreProvenance, bool) {
	if r == nil {
		return PackedStoreProvenance{}, false
	}
	facts, ok := r.ExprOptimizationFacts(expr)
	if !ok {
		return PackedStoreProvenance{}, false
	}
	if !facts.PackedStoreProvenance.HasAnyPackedStoreProvenance() {
		return PackedStoreProvenance{}, false
	}
	return facts.PackedStoreProvenance, true
}

func (r *Result) ExprHasPackedStoreProvenance(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	provenance, ok := r.ExprPackedStoreProvenance(expr)
	if !ok {
		return false
	}
	return provenance.HasAnyPackedStoreProvenance()
}

func (r *Result) ExprDependsOnFrozenPackedStores(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	provenance, ok := r.ExprPackedStoreProvenance(expr)
	if !ok {
		return false
	}
	return provenance.DependsOnFrozenPackedStores()
}

func (r *Result) ExprDependsOnNonFrozenPackedStores(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	provenance, ok := r.ExprPackedStoreProvenance(expr)
	if !ok {
		return false
	}
	return provenance.DependsOnNonFrozenPackedStores()
}

func (r *Result) ExprHasOnlyFrozenPackedStoreDeps(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	provenance, ok := r.ExprPackedStoreProvenance(expr)
	if !ok {
		return false
	}
	return provenance.HasOnlyFrozenPackedStoreDeps()
}

func (r *Result) ExprHasMixedPackedStoreProvenance(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	provenance, ok := r.ExprPackedStoreProvenance(expr)
	if !ok {
		return false
	}
	return provenance.HasMixedProvenance()
}

func (r *Result) ExprDependsOnlyOnFrozenPackedStores(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	provenance, ok := r.ExprPackedStoreProvenance(expr)
	if !ok {
		return false
	}
	return provenance.DependsOnlyOnFrozenPackedStores()
}
