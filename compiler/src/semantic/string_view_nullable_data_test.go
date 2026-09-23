package semantic

import "testing"

// StringView is the runtime representation of sview. Its data word is nullable
// even though well-formed safe views have a usable backing pointer whenever
// len > 0. Keep the compiler's builtin carrier model aligned with the runtime
// declaration so malformed-view defenses can be exercised without a type
// mismatch masking the runtime behavior.
func TestStringViewCarrierAllowsNullableData(t *testing.T) {
	analyzeTreeTestSource(t, "elisacore_runtime_prelude.elisa", `def malformed_view() -> StringView:
    data: u8&? = null
    return StringView{data: data, len: 4}
`)
}
