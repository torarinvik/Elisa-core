package semantic

import (
	"strings"

	"elisacore/src/ast"
)

// hotDisjointKernelCandidate is a `@hot` function with two or more scalar-numeric
// container-reference (`darray[T]&`) parameters — the shape that vectorizes into a tight
// element loop ONLY when the backend can prove the parameter buffers disjoint (docs/84). We
// record the candidate during per-function analysis and judge it in finalize, once the
// whole-program FuncDisjointParams fact is known.
type hotDisjointKernelCandidate struct {
	decl            *ast.FuncDecl
	containerParams []int
}

// collectHotDisjointKernelCandidate records a `@hot` kernel whose signature could vectorize if
// its container buffers are proven disjoint. Cheap structural filter only (>=2 numeric
// darray-ref params); the disjointness verdict is deferred to lintHotKernelsForDisjointness.
func (a *Analyzer) collectHotDisjointKernelCandidate(fn *ast.FuncDecl, fnType *FuncType) {
	if a == nil || fn == nil || fnType == nil {
		return
	}
	params := numericContainerRefParamIndices(fnType)
	if len(params) < 2 {
		return
	}
	a.hotDisjointKernelCandidates = append(a.hotDisjointKernelCandidates, hotDisjointKernelCandidate{
		decl:            fn,
		containerParams: params,
	})
}

// numericContainerRefParamIndices returns the indices of the parameters that are references to a
// darray with a scalar-numeric element type — the params whose element streams the backend can
// (when proven disjoint) tag with per-parameter alias scopes. Mirrors the backend's
// numeric-element eligibility gate so the lint and the optimization agree on the candidate set.
func numericContainerRefParamIndices(fnType *FuncType) []int {
	if fnType == nil {
		return nil
	}
	var indices []int
	for i, param := range fnType.Params {
		if !isContainerRefType(param) {
			continue
		}
		ref, ok := param.(*RefType)
		if !ok || ref == nil {
			continue
		}
		darr, ok := StripAggregateStateType(ref.Elem).(*DArrayType)
		if !ok || darr == nil || !IsNumericType(darr.Elem) {
			continue
		}
		indices = append(indices, i)
	}
	return indices
}

// lintHotKernelsForDisjointness is the docs/84 §3.4 performance hint: a `@hot` kernel whose
// numeric container-ref parameters are NOT provably disjoint at every call site will keep LLVM's
// runtime alias-guard and fail to vectorize. It is emitted through the graduated perf-lint lever
// (a stern warning by default, a hard error under `-Wperf`), naming the unproven parameter pair
// and the structural remedies. Runs after finalizeFuncDisjointParams so the whole-program fact is
// final.
//
// Fail-quiet on the optimization, loud on the friction: when the pair IS proven distinct we say
// nothing (it vectorizes under -fnoalias); the hint is purely about code shaped so the compiler
// cannot prove what it needs.
func (a *Analyzer) lintHotKernelsForDisjointness() {
	if a == nil {
		return
	}
	for _, cand := range a.hotDisjointKernelCandidates {
		if cand.decl == nil || len(cand.containerParams) < 2 {
			continue
		}
		info := a.funcDisjointParams[cand.decl]
		unproven := unprovenDisjointPairNames(cand.decl, cand.containerParams, info)
		if len(unproven) == 0 {
			continue
		}
		a.perfLint(cand.decl.Pos(),
			"@hot kernel %q will not vectorize: its buffers for %s are not provably disjoint, so LLVM keeps the runtime alias-guard around the element loop. Ensure every caller passes distinct buffers (the analyzer proves this for fresh `[]`/`clone` locals, but not for forwarded or header-copied darrays), or use `Slice.split` for the parallel / dynamic-index case. Once proven, build with `-fnoalias` to enable the vectorization",
			cand.decl.Name, strings.Join(unproven, ", "))
	}
}

// unprovenDisjointPairNames returns a human-readable list of the parameter pairs that are NOT
// proven distinct (e.g. `y/x`), in declaration order. Empty when every pair is proven distinct.
func unprovenDisjointPairNames(decl *ast.FuncDecl, containerParams []int, info *FuncDisjointParamInfo) []string {
	var pairs []string
	for x := 0; x < len(containerParams); x++ {
		for y := x + 1; y < len(containerParams); y++ {
			i, j := containerParams[x], containerParams[y]
			if info.PairDistinct(i, j) {
				continue
			}
			pairs = append(pairs, paramPairLabel(decl, i, j))
		}
	}
	return pairs
}

// paramPairLabel renders a `name_i/name_j` label for two parameter indices, falling back to
// positional `#i/#j` when names are unavailable.
func paramPairLabel(decl *ast.FuncDecl, i, j int) string {
	return paramLabel(decl, i) + "/" + paramLabel(decl, j)
}

func paramLabel(decl *ast.FuncDecl, index int) string {
	if decl != nil && index >= 0 && index < len(decl.Params) && decl.Params[index].Name != "" {
		return decl.Params[index].Name
	}
	return "#" + itoaParam(index)
}

func itoaParam(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
