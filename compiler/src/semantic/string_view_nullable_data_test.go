package semantic

import "testing"

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
	if errs := result.Errors(); len(errs) == 0 {
		t.Fatal("StringView.data is mutable; replacing the backing can invalidate existing views")
	}
}
