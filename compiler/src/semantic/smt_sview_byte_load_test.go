package semantic

import (
	"strings"
	"testing"
)

func TestSMTStableSviewByteLoadRange(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantError  bool
	}{
		{"checked", "return text[index]", false},
		{"raw", "can Unsafe.UncheckedIndex:\n        return text.data[index].i64()", false},
		{"raw-plus-one", "can Unsafe.UncheckedIndex:\n        return text.data[index].i64() + 1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "def byte_value(text: sview, index: usize) -> i64:\n    requires index < text.len\n    ensure result >= 0 and result <= 255\n    " + tc.body + "\n"
			result := analyzeContractStrict(t, "sview_byte_range.elisa", source)
			if (len(result.Errors()) != 0) != tc.wantError {
				t.Fatalf("want error=%v, got:\n%s", tc.wantError, allDiagnostics(result))
			}
			if tc.wantError && !strings.Contains(allDiagnostics(result), "could not be proven") {
				t.Fatalf("expected unproven byte-range obligation, got:\n%s", allDiagnostics(result))
			}
		})
	}
}

func TestSMTRawSviewLoadDoesNotOverclaim(t *testing.T) {
	for _, tc := range []struct {
		name, source string
	}{
		{"different-index", `def wrong(text: sview, index: usize, other: usize) -> i64:
    requires index < text.len and other < text.len
    ensure result == text.data[other].i64()
    can Unsafe.UncheckedIndex:
        return text.data[index].i64()
`},
		{"checked-outside-view", `def wrong(text: sview) -> i64:
    requires text.len == 0
    ensure result == text.data[0].i64()
    can Unsafe.UncheckedIndex:
        return text[0]
`},
		{"aliased-write", `def wrong(text: sview, storage: mutable darray[u8]&, index: usize) -> i64:
    requires index < text.len and storage.count > 0
    ensure result == text.data[index].i64()
    can Unsafe.UncheckedIndex:
        before: i64 = text.data[index].i64()
        storage[0] <- 17
        return before
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeContractStrict(t, "sview_byte_negative.elisa", tc.source)
			if !strings.Contains(allDiagnostics(result), "could not be proven") {
				t.Fatalf("false byte-load contract accepted:\n%s", allDiagnostics(result))
			}
		})
	}
}

func TestSMTGuardedSviewByteLoadRange(t *testing.T) {
	result := analyzeContractStrict(t, "sview_byte_guarded.elisa", `def byte_value(view: sview, index: i64) -> i64:
    ensure result >= 0 and result <= 255
    trusted Unsafe.UncheckedIndex:
        0 return if view.len < 0
        view_len: usize = view.len.usize()
        return 0 if:
            view_len.i64() != view.len
            index < 0 or index >= view.len
        index_usize: usize = index.usize()
        0 return if index_usize.i64() != index
        return view.data[index_usize].i64()
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("guarded byte range was not proven:\n%s", allDiagnostics(result))
	}
}
