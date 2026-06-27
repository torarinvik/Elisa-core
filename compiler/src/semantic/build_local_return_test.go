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

// SOUNDNESS: a region-polymorphic call's result is allocated in the caller's ambient region even
// though its return type is region-less (the region rides the threaded arena, not the type). When
// such a result — a darray, OR a struct that transitively holds region-allocated data — is stored
// through a mutable ref parameter (or any storage that outlives the function's region), it dangles
// once that region is freed. This was a silent use-after-free; it must now be rejected.
func TestRegionPolyResultStoredThroughRefParamRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "rp_store_escape.elisa", `struct Holder:
    items: mutable darray[i64]
def make_items() -> darray[i64]:
    out: mutable darray[i64] = []
    out.push(7)
    return out
def store_into(dst: mutable Holder&) -> void:
    dst.items <- make_items()
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "escapes its `in auto:` scope") {
		t.Fatalf("storing a region-poly result through a ref param must be rejected as an escape, got:\n%s", all)
	}
}

// A region-poly result holding region data in a STRUCT (the UserSettings_Load shape) is the same
// dangling store — the struct type carries no region but its darray field lives in the auto region.
func TestRegionPolyStructResultStoredThroughRefParamRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "rp_struct_store_escape.elisa", `struct Users:
    user: mutable darray[i64]
struct Mgr:
    users: mutable Users
def make_users() -> Users:
    return Users{user: [1, 2, 3]}
def bad_store(dst: mutable Mgr&) -> void:
    dst.users <- make_users()
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "escapes its `in auto:` scope") {
		t.Fatalf("storing a region-poly STRUCT result through a ref param must be rejected, got:\n%s", all)
	}
}

// Must NOT over-fire: returning a region-poly result (the caller adopts its region) and binding it
// to a local that does not outlive the region are both legitimate and must still compile.
func TestRegionPolyResultReturnAndLocalBindStillAccepted(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "rp_store_ok.elisa", `struct Users:
    user: mutable darray[i64]
def make_users() -> Users:
    return Users{user: [1, 2, 3]}
def forward() -> Users:
    return make_users()
def use_local() -> i64:
    u: Users = make_users()
    return u.user[0]
`)
}

// The same region-poly-result dangle through the in-place container mutations (push / dict.put),
// not just assignment: pushing/putting a region-poly result into a container whose storage
// outlives the function dangles once the ambient region frees. Both must be rejected.
func TestRegionPolyResultPushedIntoRefParamContainerRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "rp_push_escape.elisa", `def make_item() -> darray[i64]:
    out: mutable darray[i64] = []
    out.push(7)
    return out
def fill(dst: mutable darray[darray[i64]]&) -> void:
    dst.push(make_item())
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "escapes its `in auto:` scope") {
		t.Fatalf("pushing a region-poly result into a ref-param container must be rejected, got:\n%s", all)
	}
}

func TestRegionPolyResultPutIntoRefParamDictRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "rp_put_escape.elisa", `def make_item() -> darray[i64]:
    out: mutable darray[i64] = []
    out.push(7)
    return out
def fill(dst: mutable dict[cstr, darray[i64]]&) -> void:
    _ = dst.put("k", make_item())
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "escapes its `in auto:` scope") {
		t.Fatalf("putting a region-poly result into a ref-param dict must be rejected, got:\n%s", all)
	}
}
