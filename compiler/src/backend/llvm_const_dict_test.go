//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRConstDictLiteralEmitsStaticBuckets(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_const_dict.elisa", `
const NUMBERS: dict[cstr, u8] = {"one": 1, "two": 2, "three": 3}

def count() -> usize:
    return NUMBERS.count
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, want := range []string{
		"@.const.dict.buckets.",
		"private unnamed_addr constant",
		"@NUMBERS = internal constant",
		"i8 1",
		"i8 2",
		"i8 3",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected const dict IR to contain %q, got:\n%s", want, output)
		}
	}
}

func TestGenerateLLVMIRConstDictLiteralAcceptsConstEnumValues(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_const_dict_enum_values.elisa", `
const enum TokenKind of u8:
    Ident
    Def
    Return

const KEYWORDS: dict[cstr, TokenKind] = {"def": TokenKind.Def, "return": TokenKind.Return}

def count() -> usize:
    return KEYWORDS.count
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, want := range []string{
		"@.const.dict.buckets.",
		"@KEYWORDS = internal constant",
		"i8 1",
		"i8 2",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected const dict enum-value IR to contain %q, got:\n%s", want, output)
		}
	}
}
