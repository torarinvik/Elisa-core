package semantic

import (
	"testing"

	"elisacore/src/ast"
)

// Monotonicity: the element refinement only ever LOWERS an edge (Header -> Elements). Reading
// every edge as Header must therefore give every directed store scan a mode at least as high:
// a precise kind that raised a verdict would mean some scan decision is anti-monotone (it once
// was: a revisited, provisional summary key read as "stores nothing" made the verdict depend on
// which path met it first -- storeScanFramesSource).
func TestStoreFlowKindsAreMonotone(t *testing.T) {
	type nameKey struct {
		fn           string
		other, index int
	}
	for _, src := range []struct{ name, text string }{
		{"store_flow_kinds.elisa", storeFlowKindsSource},
		{"store_scan_frames.elisa", storeScanFramesSource},
	} {
		modes := func(off bool) map[nameKey]storeMode {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, src.name, src.text, AnalyzeOptions{})
			a := result.analyzer
			a.currentScope = a.globalScope
			a.storeScanCache = nil
			a.storeFlowKindsOff = off
			out := map[nameKey]storeMode{}
			for _, d := range result.File.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || len(fn.Body) == 0 {
					continue
				}
				for other := range fn.Params {
					for index := range fn.Params {
						if other != index {
							out[nameKey{fn.Name, other, index}] = a.calleeStoreModeInto(fn, other, index)
						}
					}
				}
			}
			return out
		}
		precise, header := modes(false), modes(true)
		if len(precise) == 0 {
			t.Fatalf("%s: no scans", src.name)
		}
		for k, m := range precise {
			if h := header[k]; m > h {
				t.Errorf("%s: %s(%d -> %d) precise mode %d exceeds all-header mode %d", src.name, k.fn, k.other, k.index, m, h)
			}
		}
	}
}
