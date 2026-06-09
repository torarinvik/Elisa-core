package semantic

import (
	"strings"
	"testing"
)

// Stage 1 (region-return inference): a function that builds a container in the
// default inferred region (no explicit arena, no `@r`) and RETURNS it is the
// canonical owned-container builder (`def join(...) -> dstr`, `def make() -> darray[T]`).
// It must be accepted: the function is region-polymorphic, the caller threads its
// region in via the hidden `__region_auto` param, and the backend adopts the
// synthesized `__auto_*` region into it (regionPolyAutoAdopts), so the result
// outlives the call. This was previously rejected as an `in auto:` escape.
func TestBuildLocalReturnDarrayIsAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "build_local_return_darray.elisa", `def collect(n: usize) -> darray[i64]:
    out: mutable darray[i64] = []
    i: mutable usize = 0
    while i < n:
        out.push(i.i64())
        i <- i + 1
    return out
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no diagnostics for build-local-return darray builder, got:\n%s", strings.Join(errs, "\n"))
	}
}

// The same for an owned string (`dstr` is darray[u8]): a string builder that
// appends bytes into the inferred region and returns it.
func TestBuildLocalReturnDstrIsAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "build_local_return_dstr.elisa", `def repeat(c: u8, n: usize) -> dstr:
    out: mutable dstr = []
    i: mutable usize = 0
    while i < n:
        out.push(c)
        i <- i + 1
    return out
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no diagnostics for build-local-return dstr builder, got:\n%s", strings.Join(errs, "\n"))
	}
}

// A GENERIC build-local-return builder is now accepted: region-poly `__region_auto` threading
// composes with generic specialization (specializeFuncType carries RegionPolymorphic; the instance
// threads + adopts the caller region). Runtime soundness across specializations is covered under
// ASan in TestBuildLocalReturnGenericAdoptedNoUAF.
func TestGenericBuildLocalReturnAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "build_local_return_generic.elisa", `def make_gen[T](n: usize, seed: T) -> darray[T]:
    out: mutable darray[T] = []
    _ = out.resize(n)
    i: mutable usize = 0
    while i < n:
        out[i] <- seed
        i <- i + 1
    return out
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a generic build-local-return builder to be accepted, got:\n%s", strings.Join(errs, "\n"))
	}
}

// SOUNDNESS FLOOR: the build-local-return relaxation must NOT leak to the
// store-into-longer-lived-storage path. Building in the inferred region and
// storing into a caller field is still a use-after-free (only `return` threads
// the region; a stored value has no caller region to live in).
func TestBuildLocalStoreIntoFieldStillRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "build_local_store_field.elisa", `struct Cache:
    items: mutable darray[i64]

def fill(self: mutable Cache&, n: usize) -> void:
    out: mutable darray[i64] = []
    i: mutable usize = 0
    while i < n:
        out.push(i.i64())
        i <- i + 1
    self.items <- out
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "escapes") || !strings.Contains(joined, "store into longer-lived storage") {
		t.Fatalf("expected escape error for storing an inferred-region collection into a caller field, got:\n%s", joined)
	}
}
