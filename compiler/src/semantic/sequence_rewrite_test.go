//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// Sequence-rewrite (`rewrite items as sequence[T]`) coverage over darray/view
// carriers — independent of the retired tree construct (docs/81 Phase 6).
func TestAnalyzeSequenceRewriteExpr(t *testing.T) {
	analyzeTreeTestSource(t, "sequence_rewrite_surface.elisa", `def keep_non_zero(owner: mutable Arena&, items: view[u32]) -> darray[u32]:
	can Abort.Panic, Memory.Allocate:
		in owner:
			return rewrite items as sequence[u32]:
				item when item != 0u32:
					emit item
`)
}
func TestAnalyzeSequenceRewriteUsesExpectedRegionParam(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "sequence_rewrite_region_param.elisa", `def keep_non_zero[@r](items: darray[u32] @r) -> darray[u32] @r:
	return rewrite items as sequence[u32]:
		item when item != 0u32:
			emit item
`)
	all := strings.Join(result.Errors(), "\n")
	if strings.Contains(all, "requires an active in <owner>: scope") {
		t.Fatalf("sequence rewrite into a live region-param darray must be allowed; got:\n%s", all)
	}
}
func TestAnalyzeSequenceRewriteEmitAllExpr(t *testing.T) {
	analyzeTreeTestSource(t, "sequence_rewrite_emit_all_surface.elisa", `def concat(owner: mutable Arena&, left: view[u32], right: view[u32]) -> darray[u32]:
	can Abort.Panic, Memory.Allocate:
		in owner:
			segments: darray[view[u32]] = [left, right]
			return rewrite segments as sequence[u32]:
				segment:
					emit all segment
`)
}
