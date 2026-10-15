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

const smtStructFieldCallees = `
struct S:
    items: darray[i64]
    names: darray[sview]
    n: i64

def need_items(p: S, k: usize) -> usize:
    requires k <= p.items.count
    return k

def need_names(p: S, k: usize) -> usize:
    requires k <= p.names.count
    return k

def need_n(p: S, k: i64) -> i64:
    requires k <= p.n
    return k

def names_len(xs: darray[sview], k: usize) -> usize:
    requires k <= xs.count
    return k
`

// A callee requires over a field of an opaque struct formal (one with container fields, bound as a
// datatype token) discharges from the caller's facts about the ARGUMENT's field, and a requires
// over the count of a container with non-integer elements discharges by length.
func TestSMTStructFormalFieldAndCountRequiresDischarge(t *testing.T) {
	src := smtStructFieldCallees + `
def ok_items(p: S, q: S) -> usize:
    requires 3 <= q.items.count
    return need_items(q, 3.usize())

def ok_names(p: S, q: S) -> usize:
    requires 3 <= q.names.count
    return need_names(q, 3.usize())

def ok_n(p: S, q: S) -> i64:
    requires 3 <= q.n
    return need_n(q, 3)

def names_ok(ys: darray[sview]) -> usize:
    requires 2 <= ys.count
    return names_len(ys, 2.usize())
`
	result := analyzeContractStrict(t, "struct_field_requires.elisa", src)
	if n := preconditionFailures(result.Errors()); n != 0 {
		t.Fatalf("expected every callee requires to discharge; got %d: %v", n, result.Errors())
	}
}

// SOUNDNESS: the callee formal `p` is bound to the caller's `q`; resolving the callee's `p.n` (or
// `p.items.count`, `p.names.count`) by spelling read the CALLER's `p`, so the caller's facts about
// `p` "proved" a requirement about `q`. Every stage0 before this fix accepted all three. A length-only
// container binding must also not borrow another container's length.
func TestSMTStructFormalFieldDoesNotAliasCallerVariable(t *testing.T) {
	cases := map[string]string{
		"alias_items": `
def alias_items(p: S, q: S) -> usize:
    requires 3 <= p.items.count
    return need_items(q, 3.usize())
`,
		"alias_names": `
def alias_names(p: S, q: S) -> usize:
    requires 3 <= p.names.count
    return need_names(q, 3.usize())
`,
		"alias_n": `
def alias_n(p: S, q: S) -> i64:
    requires 3 <= p.n
    return need_n(q, 3)
`,
		"names_other": `
def names_other(ys: darray[sview], xs: darray[sview]) -> usize:
    requires 2 <= ys.count
    return names_len(xs, 2.usize())
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeContractStrict(t, name+".elisa", smtStructFieldCallees+body)
			if preconditionFailures(result.Errors()) == 0 {
				t.Fatalf("%s: callee requires must stay unproven; got: %v", name, result.Errors())
			}
		})
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
