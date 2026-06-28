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

func TestStringViewComparesDirectlyWithStringLiteral(t *testing.T) {
	analyzeTreeTestSource(t, "sview_string_literal_eq.elisa", `def is_hello(text: sview) -> bool:
    return text == "hello"

def is_not_hello(text: sview) -> bool:
    return text != "hello"

def literal_left(text: sview) -> bool:
    return "hello" == text

def valid_suffix(view: sview) -> bool:
    return any suffix in ["u", "u8", "usize"] where view == suffix
`)
}

func TestRawU8RefSliceProducesStringView(t *testing.T) {
	analyzeTreeTestSource(t, "raw_u8_ref_slice_sview.elisa", `def window(source: u8&, start: usize, end: usize) -> sview:
    return source[start:end]

def has_prefix(source: u8&) -> bool:
    return source[0:5] == "hello"
`)
}
