package semantic

import (
	"strings"
	"testing"
)

func TestLocalReferenceBindingAndPointeeMutabilityAreIndependent(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "local_reference_binding_mutability.elisa", `def rebind(readonly: i64&, next: i64&) -> i64&:
	mutable cursor: i64& = readonly
	cursor <- next
	return cursor

def write_through(writable: mutable i64&) -> i64:
	const cursor: mutable i64& = writable
	cursor <- 9
	return 0
`)
	if diagnostics := allDiagnostics(result); strings.TrimSpace(diagnostics) != "" {
		t.Fatalf("independent binding/pointee qualifiers should type-check, got:\n%s", diagnostics)
	}
}
