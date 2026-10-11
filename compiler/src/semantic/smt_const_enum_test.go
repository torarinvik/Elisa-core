//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestSMTConstEnumImplication(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		reject     bool
	}{
		{"valid", "return 1 if kind == Kind.First\n    return 2", false},
		{"invalid", "return 2", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "const enum Kind of u8:\n    First\n    Second\ndef code(kind: Kind) -> i64:\n    ensure kind == Kind.First implies result == 1\n    " + tc.body + "\n"
			result := analyzeContractStrict(t, tc.name+".elisa", src)
			rejected := strings.Contains(allDiagnostics(result), "could not be proven")
			if rejected != tc.reject {
				t.Fatalf("reject=%v want=%v errors=%v", rejected, tc.reject, result.Errors())
			}
		})
	}
}

func TestSMTConstEnumExplicitValuesAndNamedContract(t *testing.T) {
	result := analyzeContractStrict(t, "enum_explicit.elisa", `
const enum Kind of u8:
    First = 17
    Second = 23
const FIRST_CODE: i64 = 110
const SECOND_CODE: i64 = 114
def code(kind: Kind) -> i64:
    ensure kind == Kind.First implies result == FIRST_CODE
    ensure kind == Kind.Second implies result == SECOND_CODE
    match kind:
        Kind.First:
            return FIRST_CODE
        Kind.Second:
            return SECOND_CODE
    return 0
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("explicit enum values and named contract must prove: %v", errs)
	}
}

func TestSMTConstEnumMatchFactsDoNotProveWrongPaths(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"wrong_arm", "match kind:\n        Kind.First:\n            return 114\n        _:\n            return 0"},
		{"guarded_exit", "match kind:\n        Kind.First if flag:\n            return 110\n        _:\n            return 0"},
		{"continuing_arm", "match kind:\n        Kind.First:\n            pass\n        _:\n            return 0\n    return 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "const enum Kind of u8:\n    First = 17\n    Second = 23\ndef code(kind: Kind, flag: bool) -> i64:\n    ensure kind == Kind.First implies result == 110\n    " + tc.body + "\n"
			result := analyzeContractStrict(t, tc.name+".elisa", src)
			if !strings.Contains(allDiagnostics(result), "could not be proven") {
				t.Fatalf("invalid match path accepted: %v", result.Errors())
			}
		})
	}
}

func TestSMTConstEnumMutableSubjectDoesNotRetainArmFact(t *testing.T) {
	result := analyzeContractStrict(t, "enum_mutation.elisa", `
const enum Kind of u8:
    First
    Second
def check(initial: Kind) -> bool:
    ensure result == true
    current: mutable Kind = initial
    match current:
        Kind.First:
            current <- Kind.Second
            return current == Kind.First
        _:
            return true
`)
	if !strings.Contains(allDiagnostics(result), "could not be proven") {
		t.Fatalf("mutable subject retained stale match fact: %v", result.Errors())
	}
}
