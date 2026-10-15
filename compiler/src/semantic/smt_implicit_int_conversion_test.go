package semantic

import (
	"strings"
	"testing"
)

func strictSMTErrors(t *testing.T, name, src string) []string {
	t.Helper()
	return analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name, src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true}).Errors()
}

func TestSviewSliceLengthIsClamped(t *testing.T) {
	src := `def fob(text: sview, from: usize) -> usize:
    requires from <= text.len
    ensure result >= from and result <= text.len
    return from

def inline(text: sview, s: usize) -> usize:
    requires s <= text.len
    return fob(text[s:text.len], 0.usize())

def local(text: sview, s: usize) -> usize:
    requires s <= text.len
    sub: sview = text[s:text.len]
    return fob(sub, 0.usize())

def exact(text: sview, s: usize, e: usize) -> bool:
    requires s <= e and e <= text.len
    ensure result
    return text[s:e].len == (e - s).i64()
`
	if errs := strictSMTErrors(t, "slice_len.elisa", src); len(errs) != 0 {
		t.Fatalf("in-range slice lengths should prove, got:\n%s", strings.Join(errs, "\n"))
	}
	bad := strings.Replace(src, "    requires s <= e and e <= text.len\n", "    requires s <= e\n", 1)
	if errs := strictSMTErrors(t, "slice_len_bad.elisa", bad); !strings.Contains(strings.Join(errs, "\n"), "could not be proven") {
		t.Fatalf("an unbounded slice end is clamped; end - start must not prove, got:\n%s", strings.Join(errs, "\n"))
	}
}

// SOUNDNESS: an implicit integer conversion that is not value-preserving is a bit copy / truncation
// in codegen. Equating the target with the unconverted value assumed it was already in range: an i64
// stored into a usize "proved" result <= i64 max for a negative argument, and a char stored into a u8
// "proved" d == 44 iff c == 44 (c == 300 also truncates to 44). (selfhost-proofs states the first case
// over sview.len; main assumes sview.len >= 0, so it is stated over an i64 parameter here.)
func TestImplicitIntConversionIsNotIdentity(t *testing.T) {
	for name, body := range map[string]string{
		"local":   "    n: usize = x\n    return n\n",
		"return":  "    return x\n",
		"mutable": "    n: mutable usize = 0\n    n <- x\n    return n\n",
	} {
		src := "def h(x: i64) -> usize:\n    ensure result <= 9223372036854775807\n" + body
		if errs := strictSMTErrors(t, "conv_"+name+".elisa", src); !strings.Contains(strings.Join(errs, "\n"), "could not be proven") {
			t.Fatalf("%s: a wrapped negative value must not prove result <= i64 max", name)
		}
		ok := "def h(x: i64) -> usize:\n    requires x >= 0\n    ensure result <= 9223372036854775807\n" + body
		if errs := strictSMTErrors(t, "conv_ok_"+name+".elisa", ok); len(errs) != 0 {
			t.Fatalf("%s: a non-negative value converts exactly, got:\n%s", name, strings.Join(errs, "\n"))
		}
	}
	trunc := "def low_is(c: char) -> bool:\n    ensure result == (c == 44)\n    d: u8 = c\n    return d == 44\n"
	if errs := strictSMTErrors(t, "char_u8.elisa", trunc); !strings.Contains(strings.Join(errs, "\n"), "could not be proven") {
		t.Fatalf("c == 300 also gives d == 44; the ensure must not prove, got:\n%s", strings.Join(errs, "\n"))
	}
}

// A view's length is non-negative, so `length: usize = view.len` is the identity and a suffix check
// written over either spelling proves; a struct field merely NAMED len gets no such exemption.
func TestImplicitViewLengthConversionIsExact(t *testing.T) {
	src := `def has_suffix(text: sview, suffix: u8) -> bool:
    ensure result == (text.len > 0 and text[text.len - 1] == suffix)
    length: usize = text.len
    return false if length == 0
    return text[length - 1] == suffix
`
	if errs := strictSMTErrors(t, "view_len_conv.elisa", src); len(errs) != 0 {
		t.Fatalf("a view length converts exactly, got:\n%s", strings.Join(errs, "\n"))
	}
	fake := `struct Span:
    len: i64

def h(s: Span) -> usize:
    ensure result <= 9223372036854775807
    n: usize = s.len
    return n
`
	if errs := strictSMTErrors(t, "field_len_conv.elisa", fake); !strings.Contains(strings.Join(errs, "\n"), "could not be proven") {
		t.Fatalf("a negative i64 field named len wraps; the ensure must not prove")
	}
}
