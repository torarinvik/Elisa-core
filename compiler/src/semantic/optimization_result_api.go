package semantic

import (
	"elisacore/src/ast"
)

func (r *Result) ExprOptimizationFacts(expr ast.Expr) (OptimizationFacts, bool) {
	if r == nil || expr == nil {
		return OptimizationFacts{}, false
	}
	return exprOptimizationFactsFrom(r, expr, r.ExprFacts != nil)
}

// ExprOptimizationFacts reads optimization facts through the receiver-local
// sparse view. Keep the physical-map nil check: unlike LookupExprFacts on its
// own, this Result API intentionally does not inherit when ExprFacts is nil.
func (v CodegenReadView) ExprOptimizationFacts(expr ast.Expr) (OptimizationFacts, bool) {
	if v.Result == nil || expr == nil {
		return OptimizationFacts{}, false
	}
	return exprOptimizationFactsFrom(v, expr, v.HasOwnExprFactsMap())
}

func exprOptimizationFactsFrom[T interface {
	LookupExprFacts(ast.Expr) (OptimizationFacts, bool)
}](reader T, expr ast.Expr, ownMapPresent bool) (OptimizationFacts, bool) {
	if !ownMapPresent {
		return OptimizationFacts{}, false
	}
	return reader.LookupExprFacts(expr)
}

func exprsHaveSameExtent[T interface {
	ExprOptimizationFacts(ast.Expr) (OptimizationFacts, bool)
}](reader T, left, right ast.Expr) bool {
	leftFacts, ok := reader.ExprOptimizationFacts(left)
	if !ok {
		return false
	}
	rightFacts, ok := reader.ExprOptimizationFacts(right)
	if !ok {
		return false
	}
	return leftFacts.SameExtent(rightFacts)
}

func exprsHaveEqualExtentSize[T interface {
	ExprOptimizationFacts(ast.Expr) (OptimizationFacts, bool)
}](reader T, left, right ast.Expr) bool {
	leftFacts, ok := reader.ExprOptimizationFacts(left)
	if !ok {
		return false
	}
	rightFacts, ok := reader.ExprOptimizationFacts(right)
	if !ok {
		return false
	}
	return leftFacts.SameExtentSize(rightFacts)
}

func exprsAreDisjoint[T interface {
	ExprOptimizationFacts(ast.Expr) (OptimizationFacts, bool)
}](reader T, left, right ast.Expr) bool {
	leftFacts, ok := reader.ExprOptimizationFacts(left)
	if !ok {
		return false
	}
	rightFacts, ok := reader.ExprOptimizationFacts(right)
	if !ok {
		return false
	}
	return leftFacts.Disjoint(rightFacts)
}

func exprSupportsDenseWrite[T interface {
	ExprOptimizationFacts(ast.Expr) (OptimizationFacts, bool)
}](reader T, expr ast.Expr) bool {
	facts, ok := reader.ExprOptimizationFacts(expr)
	return ok && facts.Contiguous && facts.UnitStride && !facts.ReadOnly
}

func exprPackedStoreProvenance[T interface {
	ExprOptimizationFacts(ast.Expr) (OptimizationFacts, bool)
}](reader T, expr ast.Expr) (PackedStoreProvenance, bool) {
	facts, ok := reader.ExprOptimizationFacts(expr)
	if !ok || !facts.PackedStoreProvenance.HasAnyPackedStoreProvenance() {
		return PackedStoreProvenance{}, false
	}
	return facts.PackedStoreProvenance, true
}

// IsCompilerBuiltinHelperCall reports whether semantic analysis resolved this
// exact call expression through a compiler-owned helper implementation. A
// source function that merely reuses the helper's spelling is not a builtin.
func (r *Result) IsCompilerBuiltinHelperCall(call *ast.CallExpr, name string) bool {
	if r == nil || call == nil || name == "" {
		return false
	}
	if queries := r.specializationQueries; queries != nil {
		node, owned := r.specializationLookupNode(call)
		if owned {
			private, ok := node.(*ast.CallExpr)
			if !ok {
				return false
			}
			return queries.helpers[private] == name
		}
		if queries.root != nil {
			return queries.root.IsCompilerBuiltinHelperCall(call, name)
		}
		return false
	}
	if shared := r.shared; shared != nil {
		return shared.compilerBuiltinHelperCalls[call] == name
	}
	if r.analyzer == nil {
		return false
	}
	defer r.lockAnalyzer()()
	return r.analyzer.isCompilerBuiltinHelperCall(call, name)
}

func (r *Result) ExprsHaveSameExtent(left, right ast.Expr) bool {
	if r == nil {
		return false
	}
	return exprsHaveSameExtent(r, left, right)
}

func (v CodegenReadView) ExprsHaveSameExtent(left, right ast.Expr) bool {
	return exprsHaveSameExtent(v, left, right)
}

func (r *Result) ExprsHaveEqualExtentSize(left, right ast.Expr) bool {
	if r == nil {
		return false
	}
	return exprsHaveEqualExtentSize(r, left, right)
}

func (v CodegenReadView) ExprsHaveEqualExtentSize(left, right ast.Expr) bool {
	return exprsHaveEqualExtentSize(v, left, right)
}

func (r *Result) ExprsAreDisjoint(left, right ast.Expr) bool {
	if r == nil {
		return false
	}
	return exprsAreDisjoint(r, left, right)
}

func (v CodegenReadView) ExprsAreDisjoint(left, right ast.Expr) bool {
	return exprsAreDisjoint(v, left, right)
}

func (r *Result) ExprSupportsDenseWrite(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	return exprSupportsDenseWrite(r, expr)
}

func (v CodegenReadView) ExprSupportsDenseWrite(expr ast.Expr) bool {
	return exprSupportsDenseWrite(v, expr)
}

func (r *Result) ExprPackedStoreProvenance(expr ast.Expr) (PackedStoreProvenance, bool) {
	if r == nil {
		return PackedStoreProvenance{}, false
	}
	return exprPackedStoreProvenance(r, expr)
}

func (v CodegenReadView) ExprPackedStoreProvenance(expr ast.Expr) (PackedStoreProvenance, bool) {
	return exprPackedStoreProvenance(v, expr)
}

func (r *Result) ExprHasPackedStoreProvenance(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	return exprHasPackedStoreProvenance(r, expr)
}

func (v CodegenReadView) ExprHasPackedStoreProvenance(expr ast.Expr) bool {
	return exprHasPackedStoreProvenance(v, expr)
}

func (r *Result) ExprDependsOnFrozenPackedStores(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	return exprDependsOnFrozenPackedStores(r, expr)
}

func (v CodegenReadView) ExprDependsOnFrozenPackedStores(expr ast.Expr) bool {
	return exprDependsOnFrozenPackedStores(v, expr)
}

func (r *Result) ExprDependsOnNonFrozenPackedStores(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	return exprDependsOnNonFrozenPackedStores(r, expr)
}

func (v CodegenReadView) ExprDependsOnNonFrozenPackedStores(expr ast.Expr) bool {
	return exprDependsOnNonFrozenPackedStores(v, expr)
}

func (r *Result) ExprHasOnlyFrozenPackedStoreDeps(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	return exprHasOnlyFrozenPackedStoreDeps(r, expr)
}

func (v CodegenReadView) ExprHasOnlyFrozenPackedStoreDeps(expr ast.Expr) bool {
	return exprHasOnlyFrozenPackedStoreDeps(v, expr)
}

func (r *Result) ExprHasMixedPackedStoreProvenance(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	return exprHasMixedPackedStoreProvenance(r, expr)
}

func (v CodegenReadView) ExprHasMixedPackedStoreProvenance(expr ast.Expr) bool {
	return exprHasMixedPackedStoreProvenance(v, expr)
}

func (r *Result) ExprDependsOnlyOnFrozenPackedStores(expr ast.Expr) bool {
	if r == nil {
		return false
	}
	return exprDependsOnlyOnFrozenPackedStores(r, expr)
}

func (v CodegenReadView) ExprDependsOnlyOnFrozenPackedStores(expr ast.Expr) bool {
	return exprDependsOnlyOnFrozenPackedStores(v, expr)
}

func exprHasPackedStoreProvenance[T interface {
	ExprOptimizationFacts(ast.Expr) (OptimizationFacts, bool)
}](reader T, expr ast.Expr) bool {
	provenance, ok := exprPackedStoreProvenance(reader, expr)
	return ok && provenance.HasAnyPackedStoreProvenance()
}

func exprDependsOnFrozenPackedStores[T interface {
	ExprOptimizationFacts(ast.Expr) (OptimizationFacts, bool)
}](reader T, expr ast.Expr) bool {
	provenance, ok := exprPackedStoreProvenance(reader, expr)
	return ok && provenance.DependsOnFrozenPackedStores()
}

func exprDependsOnNonFrozenPackedStores[T interface {
	ExprOptimizationFacts(ast.Expr) (OptimizationFacts, bool)
}](reader T, expr ast.Expr) bool {
	provenance, ok := exprPackedStoreProvenance(reader, expr)
	return ok && provenance.DependsOnNonFrozenPackedStores()
}

func exprHasOnlyFrozenPackedStoreDeps[T interface {
	ExprOptimizationFacts(ast.Expr) (OptimizationFacts, bool)
}](reader T, expr ast.Expr) bool {
	provenance, ok := exprPackedStoreProvenance(reader, expr)
	return ok && provenance.HasOnlyFrozenPackedStoreDeps()
}

func exprHasMixedPackedStoreProvenance[T interface {
	ExprOptimizationFacts(ast.Expr) (OptimizationFacts, bool)
}](reader T, expr ast.Expr) bool {
	provenance, ok := exprPackedStoreProvenance(reader, expr)
	return ok && provenance.HasMixedProvenance()
}

func exprDependsOnlyOnFrozenPackedStores[T interface {
	ExprOptimizationFacts(ast.Expr) (OptimizationFacts, bool)
}](reader T, expr ast.Expr) bool {
	provenance, ok := exprPackedStoreProvenance(reader, expr)
	return ok && provenance.DependsOnlyOnFrozenPackedStores()
}
