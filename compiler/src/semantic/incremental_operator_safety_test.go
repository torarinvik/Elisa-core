//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestIncrementalOperatorAbsenceMatchesActualDispatch(t *testing.T) {
	integer, wide := &BuiltinType{Name: "int"}, &BuiltinType{Name: "i64"}
	method := &Symbol{Name: "__add__"}
	cases := []struct {
		name   string
		impls  map[string]*StaticImpl
		absent bool
	}{
		{"empty", nil, true},
		{"unrelated receiver", map[string]*StaticImpl{"x": {Receiver: &StructType{Name: "Other"}, Methods: map[string]*Symbol{"__add__": method}}}, true},
		{"unrelated method", map[string]*StaticImpl{"x": {Receiver: wide, Methods: map[string]*Symbol{"other": method}}}, true},
		{"numeric neg bypass", map[string]*StaticImpl{"x": {Receiver: wide, Methods: map[string]*Symbol{"__neg__": method}}}, true},
		{"generic separate resolver", map[string]*StaticImpl{"x": {Receiver: &TypeParamType{Name: "T"}, TypeParams: []string{"T"}, Methods: map[string]*Symbol{"__add__": method}}}, true},
		{"exact wide", map[string]*StaticImpl{"x": {Receiver: wide, Methods: map[string]*Symbol{"__add__": method}}}, false},
		{"exact int", map[string]*StaticImpl{"x": {Receiver: integer, Methods: map[string]*Symbol{"__mul__": method}}}, false},
		{"ambiguous", map[string]*StaticImpl{"x": {Receiver: wide, Methods: map[string]*Symbol{"__sub__": method}}, "y": {Receiver: wide, Methods: map[string]*Symbol{"__sub__": method}}}, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			a := &Analyzer{staticImpls: test.impls}
			absent := primitiveOperatorCandidatesAbsent(a, integer, wide)
			if absent != test.absent {
				t.Fatal("wrong candidate absence classification")
			}
			if absent {
				for _, receiver := range []Type{integer, wide} {
					for _, name := range []string{"__add__", "__sub__", "__mul__"} {
						if _, found := a.buildOverloadCall(&ast.BinaryExpr{}, name, receiver); found {
							t.Fatal("claimed absence but actual operator resolver found a call")
						}
					}
				}
			}
		})
	}
}

func TestIncrementalOperatorProofInvalidatesDuringCollection(t *testing.T) {
	integer, wide := &BuiltinType{Name: "int"}, &BuiltinType{Name: "i64"}
	a := &Analyzer{staticImpls: map[string]*StaticImpl{}}
	proof := &a.incrementalOperators
	if !proof.operatorsAbsent(a, integer, wide) || !proof.valid {
		t.Fatal("absence was not memoized")
	}
	proof.beginCollection()
	a.staticImpls["op"] = &StaticImpl{Receiver: wide, Methods: map[string]*Symbol{"__add__": {Name: "__add__"}}}
	if proof.operatorsAbsent(a, integer, wide) || proof.valid {
		t.Fatal("collection reused stale absence or memoized incomplete registry")
	}
	proof.endCollection()
	if proof.operatorsAbsent(a, integer, wide) {
		t.Fatal("applicable candidate ignored after collection")
	}
	proof.beginCollection()
	delete(a.staticImpls, "op")
	proof.endCollection()
	if !proof.operatorsAbsent(a, integer, wide) {
		t.Fatal("candidate removal did not restore absence")
	}
	a.collectStaticImpls(nil)
	if proof.valid {
		t.Fatal("production collection boundary did not invalidate proof")
	}
}

func TestIncrementalOperatorSourceAdditionAndRemoval(t *testing.T) {
	source := "def work(value: i64) -> i64:\n    return value + 1\n"
	implementation := `protocol Add:
    def __add__(left: Self, right: Self) -> Self

impl Add for i64:
    def __add__(left: i64, right: i64) -> i64:
        return left

`
	session := NewIncrementalAnalysisSession()
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	before := session.Stats()
	changed := session.Analyze(parseIncrementalSessionFile(t, implementation+source), AnalyzeOptions{})
	if len(changed.Errors()) != 0 {
		t.Fatal(changed.Errors())
	}
	if session.Stats().PrimaryBodiesReused != before.PrimaryBodiesReused {
		t.Fatal("applicable primitive operator failed to block reuse")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, implementation+source)), changed)
	before = session.Stats()
	restored := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	if session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
		t.Fatal("operator removal failed to restore reuse")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, source)), restored)
}

func TestIncrementalOperatorParityIncludesPrivateDeclarationKeys(t *testing.T) {
	makeFile := func() *ast.File {
		file := parseIncrementalSessionFile(t, "def work(value: i64) -> i64:\n    return value + 1\n")
		file.DeclVisibility = map[ast.Decl]string{file.Decls[0]: "private"}
		return file
	}
	session := NewIncrementalAnalysisSession()
	session.Analyze(makeFile(), AnalyzeOptions{})
	before := session.Stats()
	warm := session.Analyze(makeFile(), AnalyzeOptions{})
	if session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
		t.Fatal("private function did not reuse")
	}
	assertIncrementalResultParity(t, Analyze(makeFile()), warm)
}
