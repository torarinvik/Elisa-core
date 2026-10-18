//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"strings"
	"testing"
)

func TestSMTBooleanArrayReads(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		pass         bool
	}{
		{"reflexive", `def same(flags: darray[bool]&, i: usize) -> bool:
    requires i < flags.count
    ensure result == flags[i]
    return flags[i]
`, true},
		{"false_claim", `def wrong(flags: darray[bool]&, i: usize) -> bool:
    requires i < flags.count
    ensure result == (not flags[i])
    return flags[i]
`, false},
		{"other_array", `def other(flags: darray[bool]&, other_flags: darray[bool]&, i: usize) -> bool:
    requires i < flags.count and i < other_flags.count
    ensure result == other_flags[i]
    return flags[i]
`, false},
		{"other_index", `def other(flags: darray[bool]&, i: usize, j: usize) -> bool:
    requires i < flags.count and j < flags.count
    ensure result == flags[j]
    return flags[i]
`, false},
		{"write", `def wrong(flags: mutable darray[bool]&, i: usize) -> bool:
    requires i < flags.count
    ensure result == flags[i]
    prior: bool = flags[i]
    flags[i] <- not prior
    return prior
`, false},
		{"callee_substitution", `def read_flag(flags: darray[bool]&, i: usize) -> bool:
    requires i < flags.count
    ensure result == flags[i]
    return flags[i]

def caller(flags: darray[bool]&, i: usize) -> bool:
    requires i < flags.count
    ensure result == flags[i]
    return read_flag(flags, i)
`, true},
		{"callee_write", `def poke(flags: mutable darray[bool]&, i: usize):
    requires i < flags.count
    flags[i] <- not flags[i]

def wrong(flags: mutable darray[bool]&, i: usize) -> bool:
    requires i < flags.count
    ensure result == flags[i]
    prior: bool = flags[i]
    poke(flags, i)
    return prior
`, false},
		{"alias_write", `def poke(flags: mutable darray[bool]&, i: usize):
    requires i < flags.count
    flags[i] <- not flags[i]

def wrong(flags: mutable darray[bool]&, alias_flags: mutable darray[bool]&, i: usize) -> bool:
    requires i < flags.count and i < alias_flags.count
    ensure result == flags[i]
    prior: bool = flags[i]
    poke(alias_flags, i)
    return prior
`, false},
		{"literal_write", `def wrong(flags: mutable darray[bool]&, i: usize) -> bool:
    requires i < flags.count
    ensure result == flags[i]
    flags[i] <- true
    return false
`, false},
		{"read_calls_across_write", `def read_mut(flags: mutable darray[bool]&, i: usize) -> bool:
    requires i < flags.count
    ensure result == flags[i]
    return flags[i]

def wrong(flags: mutable darray[bool]&, i: usize) -> bool:
    requires i < flags.count
    ensure result == true
    first: bool = read_mut(flags, i)
    flags[i] <- not first
    second: bool = read_mut(flags, i)
    return first == second
`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := analyzeContractStrict(t, "bool_array.elisa", tc.source)
			if tc.pass && len(r.Errors()) != 0 {
				t.Fatal(r.Errors())
			}
			if !tc.pass && !strings.Contains(strings.Join(r.Errors(), "\n"), "could not be proven") {
				t.Fatalf("false proof accepted or wrong failure: %v", r.Errors())
			}
		})
	}
}

func TestSMTBooleanArrayIndexAdmission(t *testing.T) {
	scope := NewScope(nil)
	for name, typ := range map[string]Type{
		"flags":  &DArrayType{Elem: &BuiltinType{Name: "bool"}},
		"ints":   &DArrayType{Elem: &BuiltinType{Name: "i64"}},
		"scalar": &BuiltinType{Name: "bool"},
	} {
		scope.Define(&Symbol{Name: name, Kind: SymbolParam, Type: typ})
	}
	a := &Analyzer{currentScope: scope, exprTypes: map[ast.Expr]Type{}}
	tr := a.newSMTTranslator(nil)
	index := func(name string) *ast.IndexExpr {
		return &ast.IndexExpr{Object: &ast.Ident{Name: name}, Index: &ast.Ident{Name: "i"}}
	}
	env := map[string]string{"flags": "actual_flags", "i": "q_index"}
	got, ok := tr.booleanArrayIndexTerm(index("flags"), env)
	if !ok || got != "(not (= (select actual_flags q_index) 0))" {
		t.Fatalf("array/index substitution lost: %q %v", got, ok)
	}

	scope.Define(&Symbol{Name: "flags_alias", Kind: SymbolParam, Type: &DArrayType{Elem: &BuiltinType{Name: "bool"}}})
	env["flags_alias"] = "actual_flags"
	alias, aliasOK := tr.booleanArrayIndexTerm(index("flags_alias"), env)
	if !aliasOK || alias != got {
		t.Fatalf("actual alias identity lost: %q vs %q", alias, got)
	}
	wrongType := index("ints")
	a.exprTypes[wrongType] = &BuiltinType{Name: "bool"}
	if term, ok := tr.booleanArrayIndexTerm(wrongType, env); ok {
		t.Fatalf("bool result annotation overrode integer container type: %s", term)
	}
	for _, name := range []string{"ints", "scalar", "unknown"} {
		if term, ok := tr.booleanArrayIndexTerm(index(name), env); ok {
			t.Fatalf("unsupported %s admitted: %s", name, term)
		}
	}
	fallback := index("flags")
	fallback.Fallback = &ast.BoolLit{Value: false}
	if term, ok := tr.booleanArrayIndexTerm(fallback, env); ok {
		t.Fatalf("fallback read admitted: %s", term)
	}
}

func TestSMTBooleanArrayLengthOnlyBindingDeclines(t *testing.T) {
	scope := NewScope(nil)
	scope.Define(&Symbol{Name: "flags", Kind: SymbolParam, Type: &DArrayType{Elem: &BuiltinType{Name: "bool"}}})
	a := &Analyzer{currentScope: scope, exprTypes: map[ast.Expr]Type{}}
	tr := a.newSMTTranslator(nil)
	tr.lengthOnly = map[string]bool{"length_only": true}
	n := &ast.IndexExpr{Object: &ast.Ident{Name: "flags"}, Index: &ast.IntLit{Value: "0"}}
	if term, ok := tr.booleanArrayIndexTerm(n, map[string]string{"flags": "length_only"}); ok {
		t.Fatalf("length-only token used as element array: %s", term)
	}
}

func TestSMTBooleanArrayUntypedCalleeCannotBorrowCallerType(t *testing.T) {
	scope := NewScope(nil)
	scope.Define(&Symbol{Name: "flags", Kind: SymbolParam, Type: &DArrayType{Elem: &BuiltinType{Name: "bool"}}})
	a := &Analyzer{currentScope: scope, exprTypes: map[ast.Expr]Type{}}
	tr := a.newSMTTranslator(nil)
	tr.calleeBodyDepth = 1
	object := &ast.Ident{Name: "flags"}
	n := &ast.IndexExpr{Object: object, Index: &ast.IntLit{Value: "0"}}
	env := map[string]string{"flags": "actual_flags"}
	if term, ok := tr.booleanArrayIndexTerm(n, env); ok {
		t.Fatalf("untyped callee borrowed caller bool array: %s", term)
	}
	a.exprTypes[object] = &DArrayType{Elem: &BuiltinType{Name: "i64"}}
	if term, ok := tr.booleanArrayIndexTerm(n, env); ok {
		t.Fatalf("callee integer array borrowed caller bool type: %s", term)
	}
	a.exprTypes[object] = &DArrayType{Elem: &BuiltinType{Name: "bool"}}
	if _, ok := tr.booleanArrayIndexTerm(n, env); !ok {
		t.Fatal("typed callee bool array with explicit actual binding declined")
	}
	if term, ok := tr.booleanArrayIndexTerm(n, nil); ok {
		t.Fatalf("unbound callee array borrowed caller identity: %s", term)
	}
}
