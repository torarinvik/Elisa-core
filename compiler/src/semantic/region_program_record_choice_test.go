package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

const programRecordChoiceSource = `global region source

struct Subset:
    restricted: bool
    variants: darray[sview @source] @source

def make_subset(name: sview @source) -> Subset:
    can Memory.Allocate, Abort.Panic:
        variants: mutable darray[sview @source] @source = []
        variants.push(name)
        return Subset{restricted: true, variants: variants}

def choose(name: sview @source, pick: bool) -> Subset:
    can Memory.Allocate, Abort.Panic:
        empty_variants: darray[sview @source] @source = []
        empty: Subset = Subset{restricted: false, variants: empty_variants}
        subset: Subset = make_subset(name) if pick else empty
        return subset
`

func TestProgramRecordProducerChoiceKeepsDeclaredStorage(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "program_record_choice.elisa", programRecordChoiceSource, AnalyzeOptions{})
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatalf("source-owned record choice rejected: %v", errors)
	}
}

func TestProgramRecordChoiceStillRejectsLocalStorage(t *testing.T) {
	cases := []struct{ name, source string }{
		{"source_field", `global region source
struct Item:
    text: sview @source
def bad() -> Item:
    can Memory.Allocate, Abort.Panic:
        text: mutable dstr = []
        text.push(65)
        return Item{text: text.as_sview()}
`},
		{"mixed_fields_and_choice", `global region source
struct Mixed:
    safe: sview @source
    values: darray[sview]
def bad(pick: bool) -> void:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[Mixed] @source = []
        region short(4096):
            text: mutable dstr = []
            text.push(65)
            values: mutable darray[sview] = []
            values.push(text.as_sview())
            local: Mixed = Mixed{safe: "safe", values: values}
            empty: Mixed = Mixed{safe: "safe", values: []}
            out.push(local if pick else empty)
`},
		{"hidden_enum_storage", `global region source
enum Payload:
    Empty
    Items(values: darray[sview])
struct Mixed:
    safe: sview @source
    payload: Payload
def bad(pick: bool) -> void:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[Mixed] @source = []
        region short(4096):
            text: mutable dstr = []
            text.push(65)
            values: mutable darray[sview] = []
            values.push(text.as_sview())
            local: Mixed = Mixed{safe: "safe", payload: Payload.Items(values)}
            empty: Mixed = Mixed{safe: "safe", payload: Payload.Empty}
            out.push(local if pick else empty)
`},
		{"program_and_local_branches", `global region source
def bad(pick: bool) -> void:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[sview @source] @source = []
        region short(4096):
            text: mutable dstr = []
            text.push(65)
            out.push(text.as_sview() if pick else "safe")
`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, c.name+".elisa", c.source, AnalyzeOptions{})
			found := false
			for _, err := range result.Errors() {
				if strings.Contains(err, "region") && (strings.Contains(err, "dangling") || strings.Contains(err, "outlive") || strings.Contains(err, "program-lived")) {
					found = true
				}
			}
			if !found {
				t.Fatalf("local storage escape lost its lifetime diagnostic: %v", result.Errors())
			}
		})
	}
}

func TestProgramRecordStorageContractRequiresActualProgramBinding(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "program_record_binding.elisa", programRecordChoiceSource, AnalyzeOptions{})
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatal(errors)
	}
	a := result.analyzer
	a.currentScope = a.globalScope
	typ := result.NamedTypes["Subset"]
	if typ == nil {
		t.Fatal("missing Subset")
	}
	expr := &ast.Ident{Name: "unbound"}
	if got := a.valueStoreRegion(expr, typ); got != "source" {
		t.Fatalf("program binding: %q", got)
	}
	// A same-spelled lexical region cannot provide the new program-owned proof.
	oldScope := a.currentScope
	a.currentScope = &Scope{Parent: oldScope, Symbols: map[string]*Symbol{
		"source": {Name: "source", Kind: SymbolRegion, Node: &ast.RegionStmt{Name: "source"}},
	}}
	if got := a.valueStoreRegion(expr, typ); got == "source" {
		t.Fatal("local shadow supplied program ownership")
	}
	a.currentScope = oldScope
}
