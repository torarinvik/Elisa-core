package semantic

import (
	"strings"
	"testing"
)

func TestUnannotatedSViewParameterReturnIsAllowed(t *testing.T) {
	analyzeTreeTestSource(t, "sview_unannotated_forward.elisa", `def forward(view: sview) -> sview:
	return view
`)
	result := analyzeTreeTestSourceWithSemanticErrors(t, "sview_region_erasure.elisa", `def forward[@r](view: sview @r) -> sview:
	return view
`)
	if diagnostics := strings.Join(result.Errors(), "\n"); !strings.Contains(diagnostics, "does not carry that region") {
		t.Fatalf("expected an explicitly tied sview return to be rejected when its region is erased, got:\n%s", diagnostics)
	}
}

func TestRegionAnnotatedSViewParameterReturnIsAccepted(t *testing.T) {
	analyzeTreeTestSource(t, "sview_region_annotated_return.elisa", `def forward[@r](view: sview @r) -> sview @r:
	return view

def relay[@r](view: sview @r) -> sview @r:
	return forward(view)
`)
}

func TestSViewForwardingPreservesActualArgumentRegion(t *testing.T) {
	analyzeTreeTestSource(t, "sview_forwarding_same_lifetime.elisa", `def choose_second(left: sview, right: sview) -> sview:
	return right

def view_length(view: sview) -> i64:
	return view.len

def safe(view: sview) -> i64:
	forwarded: sview = choose_second(view, view)
	return view_length(forwarded)
`)
	result := analyzeTreeTestSourceWithSemanticErrors(t, "sview_forwarding_region.elisa", `
def choose_second(left: sview, right: sview) -> sview:
	return right

def view_length(view: sview) -> i64:
	return view.len

def bad() -> i64:
	can Memory.Allocate, Abort.Panic:
		region outer(4096):
			outer_bytes: mutable darray[u8] @outer = []
			outer_bytes.push(65)
			outer_bytes.push(0)
			retained: mutable sview @outer = outer_bytes.as_sview()
			region inner(4096):
				inner_bytes: mutable darray[u8] @inner = []
				inner_bytes.push(66)
				inner_bytes.push(0)
				short_view: sview @inner = inner_bytes.as_sview()
				retained <- choose_second(retained, short_view)
			return view_length(retained)
`)
	symbol, ok := result.GlobalScope.Lookup("choose_second")
	if !ok {
		t.Fatal("choose_second was not entered in the global scope")
	}
	functionType, ok := symbol.Type.(*FuncType)
	if !ok || !functionType.ReturnProvenanceKnown || !regionRefStateHasParamDep(functionType.ReturnProvenance, 1) {
		t.Fatalf("expected choose_second's return provenance to depend on its second sview parameter, got %#v", symbol.Type)
	}
	diagnostics := strings.Join(result.Errors(), "\n")
	if !strings.Contains(diagnostics, `value in region "inner" is stored into longer-lived region "outer"`) {
		t.Fatalf("expected the call result to retain the actual argument's region and reject storing it into a longer-lived region, got:\n%s", diagnostics)
	}
}

func TestNestedSViewForwardingRegionReachesStoreCheck(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "nested_sview_forwarding_region.elisa", `

struct ViewHolder:
	view: sview

def choose_second(left: sview, right: sview) -> sview:
	return right

def bad() -> i64:
	can Memory.Allocate, Abort.Panic:
		region outer(4096):
			outer_bytes: mutable darray[u8] @outer = []
			outer_bytes.push(65)
			outer_bytes.push(0)
			retained: mutable ViewHolder = ViewHolder{view: outer_bytes.as_sview()}
			region inner(4096):
				inner_bytes: mutable darray[u8] @inner = []
				inner_bytes.push(66)
				inner_bytes.push(0)
				short_view: sview @inner = inner_bytes.as_sview()
				retained <- ViewHolder{view: choose_second(retained.view, short_view)}
			return retained.view.len
`)
	diagnostics := strings.Join(result.Errors(), "\n")
	if !strings.Contains(diagnostics, `value in region "inner" is stored into`) {
		t.Fatalf("expected nested field provenance to reject storing the inner view in an outer binding, got:\n%s", diagnostics)
	}
}

func TestLiveRegionDependencyNamesHandlesCyclicFieldFacts(t *testing.T) {
	region := &Symbol{Name: "inner", Kind: SymbolRegion}
	fields := map[string]regionRefState{}
	state := regionRefState{
		Deps: map[*Symbol]regionDependencyState{
			region: {Valid: true},
		},
		Fields: fields,
	}
	fields["cycle"] = state

	regions := liveRegionDependencyNames(state)
	if len(regions) != 1 || regions[0] != "inner" {
		t.Fatalf("expected cyclic field provenance to collect its live region once, got %v", regions)
	}
}
