package semantic

import (
	"strings"
	"testing"
)

// StringView is the runtime representation of sview. Even an empty view has a
// valid, non-null data pointer, so nullable data must be rejected at the type
// boundary rather than deferred to every consumer.
func TestStringViewCarrierRequiresNonNullData(t *testing.T) {
	analyzeTreeTestSource(t, "elisacore_runtime_prelude.elisa", `def valid_view(data: u8&) -> StringView:
    return StringView{data: data, len: 0}
`)

	result := analyzeTreeTestSourceWithSemanticErrors(t, "elisacore_runtime_prelude.elisa", `def malformed_view() -> StringView:
    data: u8&? = null
    return StringView{data: data, len: 4}
`)
	if errs := result.Errors(); len(errs) == 0 {
		t.Fatal("StringView accepted nullable data; its backing pointer must always be valid")
	}
}

func TestStringViewBackingCannotBeReassigned(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "elisacore_runtime_prelude.elisa", `def replace_backing(view: mutable StringView, data: u8&) -> void:
    view.data <- data
`)
	if errs := strings.Join(result.Errors(), "\n"); !strings.Contains(errs, `field "data" is immutable`) {
		t.Fatalf("StringView.data can be reassigned; expected immutable-field diagnostic, got:\n%s", errs)
	}
}

func TestStringViewLengthCannotBeWidened(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "elisacore_runtime_prelude.elisa", `def widen_view(view: mutable sview, length: i64) -> void:
    view.len <- length
`)
	if errs := strings.Join(result.Errors(), "\n"); !strings.Contains(errs, `field "len" is immutable`) {
		t.Fatalf("sview.len can be widened past its backing allocation; expected immutable-field diagnostic, got:\n%s", errs)
	}
}
