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
