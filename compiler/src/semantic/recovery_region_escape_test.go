package semantic

import (
	"strings"
	"testing"
)

func TestRecoveryReturnChecksRegionEscapeInsideCall(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "recovery_return_region.elisa", `
error ProbeError:
	Failed
def maybe() -> i64 error[ProbeError]:
	raise ProbeError.Failed
def consume(value: i64) -> void:
	pass
def bad(fallback: sview) -> sview:
	can Memory.Allocate, Abort.Panic:
		region inner(4096):
			bytes: mutable darray[u8] @inner = [65.u8()]
			consume(try maybe() else return bytes.as_sview())
		return fallback
`)
	if diagnostics := strings.Join(result.Errors(), "\n"); !strings.Contains(diagnostics, "escapes via return") {
		t.Fatalf("expected the nested recovery return to reject the region escape, got:\n%s", diagnostics)
	}
}

func TestRecoveryReturnAllowsCallerOwnedView(t *testing.T) {
	analyzeTreeTestSource(t, "recovery_return_caller_view.elisa", `
error ProbeError:
	Failed
def maybe() -> i64 error[ProbeError]:
	raise ProbeError.Failed
def consume(value: i64) -> void:
	pass
def safe(fallback: sview) -> sview:
	consume(try maybe() else return fallback)
	return fallback
`)
}

func TestRecoveryReturnChecksRegionEscapeInsideArray(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "recovery_return_array_region.elisa", `
error ProbeError:
	Failed
def maybe(value: sview) -> sview error[ProbeError]:
	raise ProbeError.Failed
def consume(values: darray[sview]) -> void:
	pass
def bad(fallback: sview) -> sview:
	can Memory.Allocate, Abort.Panic:
		region inner(4096):
			bytes: mutable darray[u8] @inner = [65.u8()]
			view: sview = bytes.as_sview()
			consume([try maybe(view) else return view])
		return fallback
`)
	if diagnostics := strings.Join(result.Errors(), "\n"); !strings.Contains(diagnostics, "escapes via return") {
		t.Fatalf("expected the array-contained recovery return to reject the region escape, got:\n%s", diagnostics)
	}
}
