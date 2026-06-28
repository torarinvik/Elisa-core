//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersStringViewLiteralEquality(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_sview_literal_eq.elisa", `def is_hello(text: sview) -> bool:
    return text == "hello"

def literal_left(text: sview) -> bool:
    return "hello" == text

def valid_suffix(view: sview) -> bool:
    return any suffix in ["u", "u8", "usize"] where view == suffix
`)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"define i1 @is_hello(",
		"define i1 @literal_left(",
		"define i1 @valid_suffix(",
		"svlit.len.eq = icmp eq i64",
		"call i64 @ctx_string_view_eq(",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected sview/string-literal equality lowering to include %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call i64 @ctx_string_view_eq(%StringView %text") {
		t.Fatalf("expected direct literal equality to use specialized lowering, got runtime helper call:\n%s", output)
	}
}
