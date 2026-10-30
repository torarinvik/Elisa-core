//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/smt"
)

func TestSMTViewEqualityDefinitionsPreserveRelationAndScope(t *testing.T) {
	tr := &smtTranslator{a: &Analyzer{}}
	first := tr.sviewContentEquality("left", "offset", "length", "right", "0", "other_length")
	again := tr.sviewContentEquality("left", "offset", "length", "right", "0", "other_length")
	if first != again || len(tr.auxDecls) != 4 {
		t.Fatal("identical windows did not share one definition")
	}
	definition := tr.auxDecls[2]
	witness := tr.auxDecls[3]
	if !strings.Contains(definition, "(assert (=> "+first+" (and (>= length 0)") ||
		!strings.Contains(definition, "(>= other_length 0)") ||
		!strings.Contains(definition, "(forall ((|@sview_byte_0| Int))") {
		t.Fatalf("equality lost its exact length/byte definition: %s", definition)
	}
	if !strings.Contains(witness, "(or "+first+" (and (<= 0 |@sview_diff_0|)") ||
		!strings.Contains(witness, "(not (= (select left (+ offset |@sview_diff_0|)) (select right (+ 0 |@sview_diff_0|))))") {
		t.Fatalf("equality lost its inequality witness: %s", witness)
	}
	preamble := tr.factPreamble()
	if !strings.Contains(preamble, definition) || !strings.Contains(preamble, witness) {
		t.Fatal("exact equality definition was dropped from the solver preamble")
	}
	context := smtPreambleContext(preamble)
	for _, dependency := range []string{"left", "offset", "length", "right", "other_length"} {
		if !strings.Contains(context, dependency) {
			t.Fatalf("hypothesis relevance lost %s through equality naming", dependency)
		}
	}
	shadowed := &smtTranslator{}
	shadowed.sviewContentEquality("left", "(ite (forall ((q_i Int)) (> q_i 0)) q_i 0)", "length", "right", "0", "length")
	if len(shadowed.auxDecls) != 0 {
		t.Fatal("nested binder shadowing permitted top-level hoisting")
	}
	bound := &smtTranslator{}
	inline := bound.sviewContentEquality("left", "q_index", "length", "right", "0", "length")
	if len(bound.auxDecls) != 0 || !strings.Contains(inline, "(forall") || !strings.Contains(inline, "q_index") {
		t.Fatal("source quantifier binder was hoisted out of scope")
	}
}

func TestSMTViewEqualityWitnessMatchesOriginal(t *testing.T) {
	tr := &smtTranslator{a: &Analyzer{}}
	eq := tr.sviewContentEquality("left", "lo", "length", "right", "ro", "other_length")
	inline := (&smtTranslator{}).sviewContentEquality("left", "(ite (= q_index q_index) lo lo)", "length", "right", "ro", "other_length")
	inline = strings.ReplaceAll(inline, "(ite (= q_index q_index) lo lo)", "lo")
	decls := "(declare-const left (Array Int Int))\n(declare-const right (Array Int Int))\n" +
		"(declare-const lo Int)\n(declare-const ro Int)\n(declare-const length Int)\n(declare-const other_length Int)\n"
	solver := openCOISolver(t, 2_000_000)
	query := decls + tr.factPreamble() + "(assert (xor " + eq + " " + inline + "))\n"
	if got, _ := solver.Check(query); got != smt.Unsat {
		t.Fatalf("witness and inline view equality differ: %v", got)
	}
}

// View content equality: equal lengths (both non-negative) and equal bytes, over the modeled
// byte arrays of parameters and their slices.
func TestSMTViewContentEquality(t *testing.T) {
	src := `
def find(text: sview, needle: sview) -> usize:
    ensure result <= text.len
    ensure result < text.len implies (needle.len > 0 and needle.len <= text.len and result <= text.len - needle.len and text[result:result + needle.len] == needle)
    text_length: usize = text.len
    needle_length: usize = needle.len
    if needle_length == 0 or text_length < needle_length:
        return text_length
    search_limit: usize = text_length - needle_length + 1
    index: mutable usize = 0
    while index < search_limit |index, search_limit, text, needle, needle_length, text_length|:
        decreases search_limit - index
        invariant index <= search_limit
        if text[index:index + needle_length] == needle:
            return index
        index <- index + 1
    return text_length

def findbad(text: sview, needle: sview) -> usize:
    ensure result < text.len implies text[result:result + needle.len] == needle
    text_length: usize = text.len
    needle_length: usize = needle.len
    if needle_length == 0 or text_length < needle_length:
        return text_length
    return 0

def eqsym(a: sview, b: sview) -> bool:
    ensure result == (b == a)
    return a == b

def eqbytes(a: sview, b: sview) -> bool:
    requires a.len > 0
    ensure result implies a[0] == b[0]
    return a == b

def eqbad(a: sview, b: sview) -> bool:
    requires a.len > 1
    ensure result implies a[0] == b[1]
    return a == b

def eq_local(a: sview, b: sview) -> bool:
    requires a.len > 0
    ensure result implies a[0] == b[0]
    equal: bool = a == b
    return equal

`
	result := analyzeContractStrict(t, "view_eq.elisa", src)
	for _, name := range []string{"find", "eqsym", "eqbytes", "eq_local"} {
		if n := ensureFailuresFor(result.Errors(), name); n != 0 {
			t.Fatalf("%s: expected proof, got %v", name, result.Errors())
		}
	}
	// SOUNDNESS: index 0 is not a match for every text, and equal views do not relate a[0] to b[1].
	for _, name := range []string{"findbad", "eqbad"} {
		if ensureFailuresFor(result.Errors(), name) == 0 {
			t.Fatalf("%s: false ensure must stay unproven; got %v", name, result.Errors())
		}
	}
}

// An array's length symbol must not share a name with a local spelled `<array>_len`.
func TestSMTArrayLengthSymbolDistinctFromLocal(t *testing.T) {
	src := `
def okc(view: sview) -> bool:
    requires view.len > 1
    ensure result == (view[0] == view[1])
    view_len: i64 = view.len
    return view[0] == view[1] and view_len > 1


def okc_bad(view: sview) -> bool:
    requires view.len > 1
    ensure result == (view[0] != view[1])
    view_len: i64 = view.len
    return view[0] == view[1] and view_len > 1

`
	result := analyzeContractStrict(t, "array_len_name.elisa", src)
	if n := ensureFailuresFor(result.Errors(), "okc"); n != 0 {
		t.Fatalf("okc: expected proof, got %v", result.Errors())
	}
	if ensureFailuresFor(result.Errors(), "okc_bad") == 0 {
		t.Fatalf("okc_bad: false ensure must stay unproven; got %v", result.Errors())
	}
}
