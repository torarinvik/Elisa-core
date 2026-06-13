package semantic

import (
	"strings"
	"testing"
)

func TestAnalyzeCloneBuiltinSupportsDArrayTargets(t *testing.T) {
	analyzeTreeTestSource(t, "clone_builtin_surface.elisa", `struct Pair:
	items: darray[u32]

def clone_pair(owner: mutable Arena&, source_items: view[u32]) -> Pair:
	can Abort.Panic, Memory.Allocate:
		in owner:
			return Pair{items: clone[darray[u32]](source_items)}
`)
}

func TestAnalyzeCloneBuiltinUsesRegionParamTarget(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "clone_builtin_region_param.elisa", `def copy[@r](items: darray[u8] @r) -> darray[u8] @r:
    return clone[darray[u8] @r](items)
`)
	all := strings.Join(result.Errors(), "\n")
	if strings.Contains(all, "requires an active in <owner>: scope") {
		t.Fatalf("clone into a live region-param darray target must be allowed; got:\n%s", all)
	}
}

func TestAnalyzeCloneBuiltinRejectsAllocatingCloneWithoutOwner(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "clone_builtin_owner_required.elisa", `def clone_items(items: view[u32]) -> darray[u32]:
	return clone[darray[u32]](items)
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `clone of "darray[u32]" requires an active in <owner>: scope`) {
		t.Fatalf("expected clone owner diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeCloneBuiltinSupportsStructsWithSViewFields(t *testing.T) {
	analyzeTreeTestSource(t, "clone_builtin_sview_struct.elisa", `struct NameBox:
	name: sview

def clone_name_box(box: NameBox) -> NameBox:
	return clone[NameBox](box)
`)
}

// clone[darray[u8]](sview) deep-copies a bounded string view's bytes into the
// active region, producing an owned darray[u8]. This is the sanctioned,
// length-bounded way to persist a string (docs/26).
func TestAnalyzeCloneBuiltinPersistsSViewIntoOwnedBytes(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "clone_sview_to_owned.elisa", `def persist(owner: mutable Arena&, text: sview) -> darray[u8]:
    can Abort.Panic, Memory.Allocate:
        in owner:
            return clone[darray[u8]](text)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no diagnostics for sview->darray clone, got:\n%s", strings.Join(errs, "\n"))
	}
}

// dstr is the owned-dynamic-string spelling of darray[u8]; clone[dstr](sview)
// persists a bounded view into owned bytes exactly like clone[darray[u8]].
func TestAnalyzeCloneBuiltinPersistsSViewIntoDStr(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "clone_sview_to_dstr.elisa", `def persist(owner: mutable Arena&, text: sview) -> dstr:
    can Abort.Panic, Memory.Allocate:
        in owner:
            return clone[dstr](text)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no diagnostics for sview->dstr clone, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Cloning an sview into a non-u8 darray is a type mismatch and must be rejected.
func TestAnalyzeCloneBuiltinRejectsSViewIntoNonByteDArray(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "clone_sview_wrong_elem.elisa", `def persist(owner: mutable Arena&, text: sview) -> darray[u32]:
    can Abort.Panic, Memory.Allocate:
        in owner:
            return clone[darray[u32]](text)
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "clone cannot clone") {
		t.Fatalf("expected sview->darray[u32] rejection, got:\n%s", all)
	}
}

func TestAnalyzeCloneBuiltinRejectsReferences(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "clone_builtin_rejects_refs.elisa", `def clone_ref(value: i64&) -> i64&:
	return clone[i64&](value)
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `clone cannot clone i64& into i64& in v1`) {
		t.Fatalf("expected clone ref rejection, got:\n%s", all)
	}
}
