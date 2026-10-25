package semantic

import "elisacore/src/ast"

func (r *Result) specializationLookupNode(node ast.Node) (ast.Node, bool) {
	if r == nil {
		return node, false
	}
	if mapped := r.specializationNodes[node]; mapped != nil {
		node = mapped
	}
	return node, r.specializationOwned[node]
}

// CanonicalCodegenResult is the immutable program context. Private current-body
// facts are deliberately not substituted into unrelated program declarations.
func (r *Result) CanonicalCodegenResult() *Result {
	if r != nil && r.specializationRoot != nil {
		return r.specializationRoot
	}
	return r
}

// CodegenDeclarationResult selects canonical facts for a canonical imported
// function, while compiler-generated closures built from private syntax inherit
// the current private owner/view. Unknown synthetic declarations inherit; their
// absent facts remain absent rather than reviving a template by source span.
func (r *Result) CodegenDeclarationResult(decl *ast.FuncDecl) *Result {
	if r == nil || r.specializationRoot == nil || decl == nil {
		return r
	}
	if decl == r.specializationCurrent {
		return r
	}
	if _, private := r.specializationCanonical[decl]; private {
		return r
	}
	root := r.specializationRoot
	if _, known := root.FunctionAnalyses[decl]; known {
		return root
	}
	if root.analyzer != nil {
		if _, known := root.analyzer.funcDeclSymbols[decl]; known {
			return root
		}
	}
	if root.GlobalScope != nil {
		if symbol, ok := root.GlobalScope.Lookup(decl.Name); ok && symbol != nil && symbol.Node == decl {
			return root
		}
	}
	return r
}

// Span authority is narrower than the full logical proof map: a private current
// body cannot borrow another function's proof merely through an equal source span.
func (r *Result) IndexProofSpanCount() int {
	if r == nil {
		return 0
	}
	if r.specializationRoot != nil {
		return len(r.specializationSpanProofs)
	}
	return len(r.IndexBoundsProven)
}
func (r *Result) RangeIndexProofSpanAuthority(yield func(*ast.IndexExpr, bool) bool) {
	if r == nil || yield == nil {
		return
	}
	proofs := r.IndexBoundsProven
	if r.specializationRoot != nil {
		proofs = r.specializationSpanProofs
	}
	for key, value := range proofs {
		if !yield(key, value) {
			return
		}
	}
}

func (r *Result) CanonicalCodegenFunction(fn *ast.FuncDecl) *ast.FuncDecl {
	if r != nil {
		if original := r.specializationCanonical[fn]; original != nil {
			return original
		}
	}
	return fn
}

// IndexProofSpanResult prevents a current private body from lending span
// authority to a typed canonical foreign expression inspected by a helper.
// Rebuilt/new nodes belong to the active body view; current canonical aliases
// normalize to that view as well.
func (r *Result) IndexProofSpanResult(expr *ast.IndexExpr) *Result {
	if r == nil || r.specializationRoot == nil || expr == nil {
		return r
	}
	if _, owned := r.specializationLookupNode(expr); owned {
		return r
	}
	if r.specializationProofMask[expr] {
		return r
	}
	root := r.specializationRoot
	if _, known := root.LookupExprTypes(expr); known {
		return root
	}
	if _, known := root.LookupIndexBoundsProven(expr); known {
		return root
	}
	return r
}

func (r *Result) IndexProofSpanBlocked(expr *ast.IndexExpr) bool {
	return r != nil && r.specializationRoot != nil && r.specializationProofMask[expr] && r.specializationNodes[expr] == nil
}
