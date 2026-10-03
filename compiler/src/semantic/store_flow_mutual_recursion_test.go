package semantic

import (
	"strings"
	"testing"
)

// The forwarder `eb` is summarized while `ea` (which stores r into out) is still iterating on
// its first assumption. That provisional summary used to be memoized, so `eb(out, &x, 2)` was
// "kept apart" and a frame reference escaped into the caller's darray.
const storeFlowMutualRecursionSource = `def eb(out: mutable darray[i64&]&, r: i64&, n: i64) -> void:
    if n > 0:
        ea(out, r, n - 1)

def ea(out: mutable darray[i64&]&, r: i64&, n: i64) -> void:
    if n > 0:
        out.push(r)
        eb(out, r, n - 1)

def fill(out: mutable darray[i64&]&) -> void:
    x: i64 = 77
    eb(out, &x, 2)
`

func TestStoreFlowMutualRecursionForwarderRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_flow_mutual_recursion_bad.elisa", storeFlowMutualRecursionSource)
	errs := result.Errors()
	for _, e := range errs {
		if strings.Contains(e, ":12:") && strings.Contains(e, "function-local storage") {
			return
		}
	}
	t.Fatalf("want the forwarded local store (line 12) rejected, got: %s", strings.Join(errs, "\n"))
}

// A cycle that never stores r anywhere keeps its precise summary: r stays apart from out.
func TestStoreFlowMutualRecursionNoStoreAllowed(t *testing.T) {
	source := `def eb(out: mutable darray[i64]&, r: i64&, n: i64) -> void:
    if n > 0:
        ea(out, r, n - 1)

def ea(out: mutable darray[i64]&, r: i64&, n: i64) -> void:
    if n > 0:
        out.push(n)
        eb(out, r, n - 1)

def fill(out: mutable darray[i64]&) -> void:
    x: i64 = 77
    eb(out, &x, 2)
`
	result := analyzeFunctionAnalysisTestSource(t, "store_flow_mutual_recursion_ok.elisa", source)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("must be allowed, got: %s", strings.Join(errs, "\n"))
	}
}
