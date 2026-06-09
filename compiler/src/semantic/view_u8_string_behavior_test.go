package semantic

import "testing"

// Step 4a: view[u8] is the unified string slice — it now carries string
// behavior everywhere sview did. A string literal types as view[u8]
// (contextual typing) and the synthetic .len field is available, exactly
// as for sview.
func TestViewU8CarriesStringBehavior(t *testing.T) {
	analyzeTreeTestSource(t, "view_u8_string_behavior.elisa", `def takes_view(text: view[u8]) -> i64:
    return text.len

def use_string_literal() -> i64:
    return takes_view("hello")
`)
}
