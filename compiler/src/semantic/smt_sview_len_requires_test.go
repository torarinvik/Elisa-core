//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// preconditionFailures counts call-site precondition failures (each case file has one caller).
func preconditionFailures(errs []string) int {
	n := 0
	for _, e := range errs {
		if strings.Contains(e, "precondition of \"") {
			n++
		}
	}
	return n
}

const smtSViewLenCallees = `
def callee(text: sview, from: usize) -> usize:
    requires from <= text.len
    return from

def two(text: sview, other: sview) -> usize:
    requires other.len <= text.len
    return 0

def exact(text: sview, n: usize) -> usize:
    requires n <= text.len and text.len <= n
    return 0
`

// A callee `requires` over an sview formal's `.len` discharges from the caller's view length, for a
// view parameter, an immutable local, a local slice, and a slice argument (modeled with the
// runtime's clamping), and a same-named view argument still uses the caller's own length facts.
func TestSMTSViewLenCalleeRequiresDischarges(t *testing.T) {
	src := smtSViewLenCallees + `
def zero_from(text: sview) -> usize:
    return callee(text, 0.usize())

def from_local(text: sview) -> usize:
    n: usize = text.len
    return callee(text, n)

def local_slice(text: sview, a: usize, b: usize) -> usize:
    return 0 if a > b or b > text.len
    span: sview = text[a:b]
    return callee(span, 0.usize())

def slice_in_bounds(text: sview, a: usize, b: usize) -> usize:
    return 0 if a > b or b > text.len
    return exact(text[a:b], b - a)

def slice_any_bounds(text: sview, a: usize, b: usize) -> usize:
    return callee(text[a:b], 0.usize())

def swapped_ok(text: sview, other: sview) -> usize:
    requires text.len <= other.len
    return two(other, text)

def same_name(text: sview, from: usize) -> usize:
    requires from <= text.len
    return callee(text, from)
`
	result := analyzeContractStrict(t, "sview_len_requires.elisa", src)
	if n := preconditionFailures(result.Errors()); n != 0 {
		t.Fatalf("expected every callee requires to discharge; got %d failures: %v", n, result.Errors())
	}
}

// A clause that mentions only some formals discharges even when an UNMENTIONED argument (here a byte
// read of the view, outside the SMT fragment in a non-ghost function) cannot be translated; a
// mentioned formal with a false value still fails.
func TestSMTCallRequiresIgnoresUnmentionedUntranslatableArgument(t *testing.T) {
	src := `
def needs(text: sview, from: usize, byte: u8) -> usize:
    requires from <= text.len
    return from

def first_byte_ok(text: sview) -> usize:
    return 0 if text.len < 1
    return needs(text, 1.usize(), text[0].u8())
`
	result := analyzeContractStrict(t, "unmentioned_arg.elisa", src)
	if n := preconditionFailures(result.Errors()); n != 0 {
		t.Fatalf("expected the requires to discharge; got: %v", result.Errors())
	}
	bad := `
def needs(text: sview, from: usize, byte: u8) -> usize:
    requires from <= text.len
    return from

def first_byte_bad(text: sview) -> usize:
    return 0 if text.len < 1
    return needs(text, 2.usize(), text[0].u8())
`
	result = analyzeContractStrict(t, "unmentioned_arg_bad.elisa", bad)
	if preconditionFailures(result.Errors()) == 0 {
		t.Fatalf("2 <= text.len is not known from text.len >= 1; got: %v", result.Errors())
	}
}

// SOUNDNESS:
//   - past_end: 1 <= text.len is not known for an arbitrary view;
//   - slice_unclamped: `text[a:b]` CLAMPS at runtime, so its length is not b - a when b > text.len;
//   - swapped: the callee's `other` is the caller's `text`; resolving the callee's `other.len`
//     through the caller's scope would read the caller's `other` and "prove" the caller's own
//     requires back to itself.
func TestSMTSViewLenCalleeRequiresSoundness(t *testing.T) {
	cases := map[string]string{
		"past_end": `
def past_end(text: sview) -> usize:
    return callee(text, 1.usize())
`,
		"slice_unclamped": `
def slice_unclamped(text: sview, a: usize, b: usize) -> usize:
    requires a <= b
    return exact(text[a:b], b - a)
`,
		"swapped": `
def swapped(text: sview, other: sview) -> usize:
    requires other.len <= text.len
    return two(other, text)
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeContractStrict(t, name+".elisa", smtSViewLenCallees+body)
			if preconditionFailures(result.Errors()) == 0 {
				t.Fatalf("%s: callee requires must stay unproven; got: %v", name, result.Errors())
			}
		})
	}
}
