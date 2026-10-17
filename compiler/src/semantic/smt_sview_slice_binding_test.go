//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

const sliceBindingFirst = `ghost def first(view: sview) -> u8:
    requires view.len > 0
    return view[0].u8()
`

func TestSMTViewSliceArgumentContents(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		reject       bool
	}{
		{"correct_offset", sliceBindingFirst + `def check(text: sview) -> bool:
    requires text.len > 1
    ensure result == (first(text[1:text.len]) == text[1].u8())
    return true
`, false},
		{"wrong_offset", sliceBindingFirst + `def check(text: sview) -> bool:
    requires text.len > 1
    ensure result == (first(text[1:text.len]) == text[0].u8())
    return true
`, true},
		{"clamped_end", sliceBindingFirst + `def check(text: sview) -> bool:
    requires text.len > 1 and text.len < 100
    ensure result == (first(text[1:999]) == text[1].u8())
    return true
`, false},
		{"different_lengths", `ghost def length(view: sview) -> i64:
    return view.len
def check(text: sview) -> bool:
    requires text.len > 2
    ensure result == (length(text[0:1]) != length(text[0:2]))
    return true
`, false},
		{"same_window", sliceBindingFirst + `def check(text: sview) -> bool:
    requires text.len > 1
    ensure result == (first(text[1:text.len]) == first(text[1:text.len]))
    return true
`, false},
		{"negative_start", sliceBindingFirst + `def check(text: sview, start: i64) -> bool:
    requires text.len > 0 and start < 0
    ensure result == (first(text[start:text.len]) == text[0].u8())
    return true
`, false},
		{"negative_end", sliceBindingFirst + `def check(text: sview, end: i64) -> bool:
    requires text.len > 1 and end < 0
    ensure result == (first(text[1:end]) == text[1].u8())
    return true
`, false},
		{"unsigned_start_read_as_negative", sliceBindingFirst + `def check(text: sview, start: usize) -> bool:
    requires text.len > 0 and start >= 9223372036854775808u64
    ensure result == (first(text[start:text.len]) == text[0].u8())
    return true
`, false},
		{"opaque_mutation", strings.Replace(sliceBindingFirst, "ghost def first", "def first", 1) + `extern mutate() -> usize
def check(text: sview) -> u8:
    requires text.len > 1
    ensure result == first(text[1:text.len])
    before: u8 = first(text[1:text.len])
    can Unsafe.RawExtern:
        ignored: usize = mutate()
    return before
`, true},
		{"runtime_reader_without_mutation", strings.Replace(sliceBindingFirst, "ghost def first", "def first", 1) + `def check(text: sview) -> u8:
    requires text.len > 1
    ensure result == first(text[1:text.len])
    before: u8 = first(text[1:text.len])
    return before
`, false},
		{"immutable_local_slice", strings.Replace(sliceBindingFirst, "ghost def first", "def first", 1) + `def check(text: sview) -> u8:
    requires text.len > 1
    ensure result == text[1].u8()
    part: sview = text[1:text.len]
    return first(part)
`, false},
		{"local_slice_captures_old_bound", `def first(view: sview) -> u8:
    return 0 if view.len <= 0
    return view[0].u8()
def check(text: sview) -> u8:
    requires text.len > 1
    ensure result == text[1].u8()
    start: mutable usize = 0
    part: sview = text[start:text.len]
    start <- 1
    return first(part)
`, true},
		{"saved_read_captures_old_bound", strings.Replace(sliceBindingFirst, "ghost def first", "def first", 1) + `def check(text: sview) -> u8:
    requires text.len > 1
    ensure result == text[1].u8()
    start: mutable usize = 0
    before: u8 = first(text[start:text.len])
    start <- 1
    return before
`, true},
		{"varying_quantified_offset", `ghost def head(view: sview) -> u8:
    return 0 if view.len <= 0
    return view[0].u8()
def check(text: sview) -> bool:
    requires text.len > 1
    ensure result == (forall index in 0..<text.len: head(text[index:text.len]) == text[0].u8())
    return true
`, true},
		{"contents_do_not_imply_address", `def same_address(left: sview, right: sview) -> bool:
    return left.data == right.data
def check(a: sview, b: sview) -> bool:
    requires a.len > 1 and b.len > 1
    requires a == b
    requires a.data != b.data
    ensure result == same_address(a[1:a.len], b[1:b.len])
    return true
`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := analyzeContractStrict(t, "view_slice_binding.elisa", tc.source)
			for _, diagnostic := range r.Errors() {
				if !strings.Contains(diagnostic, `ensure postcondition of "check"`) {
					t.Fatalf("unrelated error: %s", allDiagnostics(r))
				}
			}
			if rejected := ensureFailuresFor(r.Errors(), "check") != 0; rejected != tc.reject {
				t.Fatalf("rejected %v, want %v: %s", rejected, tc.reject, allDiagnostics(r))
			}
		})
	}
}

func TestSMTSliceLocalDoesNotReplaceLiteralFormal(t *testing.T) {
	r := analyzeContractStrict(t, "slice_literal_formal.elisa", `def need_x(view: sview) -> usize:
    requires view.len > 0
    requires view[0].u8() == 120
    return 1
def check(text: sview) -> usize:
    requires text.len > 1
    requires text[1].u8() == 120
    view: sview = text[1:text.len]
    return need_x("b")
`)
	for _, diagnostic := range r.Errors() {
		if !strings.Contains(diagnostic, `precondition of "need_x"`) {
			t.Fatalf("unrelated error: %s", allDiagnostics(r))
		}
	}
	if !strings.Contains(strings.Join(r.Errors(), "\n"), `precondition of "need_x"`) {
		t.Fatal("caller's slice local proved the literal's false byte precondition")
	}
}

func TestSMTSliceContentEqualityRespectsLiteralFormal(t *testing.T) {
	r := analyzeContractStrict(t, "slice_literal_equality.elisa", `def need_same(view: sview, peer: sview) -> usize:
    requires view == peer
    return 1
def check(text: sview) -> usize:
    requires text.len > 1
    requires text[1].u8() == 120
    view: sview = text[1:2]
    return need_same("b", text[1:2])
`)
	for _, diagnostic := range r.Errors() {
		if !strings.Contains(diagnostic, `precondition of "need_same"`) {
			t.Fatalf("unrelated error: %s", allDiagnostics(r))
		}
	}
	if !strings.Contains(strings.Join(r.Errors(), "\n"), `precondition of "need_same"`) {
		t.Fatal("caller's slice local proved the literal's false content equality")
	}
}
