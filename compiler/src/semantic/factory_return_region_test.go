package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestExplicitFactoryReturnRegionMutableHelper(t *testing.T) {
	src := `struct Rows:
    values: mutable darray[i64] = []
struct Table:
    rows: mutable Rows = Rows{}
def make_table[@r]() -> Table @r:
    return Table{}
def append_row(table: mutable Table&) -> void:
    can Memory.Allocate:
        table.rows.values.push(1)
def main() -> i64:
    region r(4096):
        table: mutable Table = make_table()
        append_row(&table) can Memory.Allocate
        return 0
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "factory_region.elisa", src, AnalyzeOptions{})
	if len(r.Errors()) != 0 {
		t.Fatalf("explicit caller-owned factory region lost: %s", allDiagnostics(r))
	}
}

func TestExplicitFactoryReturnCannotEscapeOwner(t *testing.T) {
	src := `struct Rows:
    values: mutable darray[i64] = []
struct Table:
    rows: mutable Rows = Rows{}
def make_table[@r]() -> Table @r:
    return Table{}
def escape() -> Table:
    region r(4096):
        table: Table = make_table()
        return table
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "factory_escape.elisa", src, AnalyzeOptions{})
	if len(r.Errors()) == 0 {
		t.Fatal("factory return escaped its scoped caller owner")
	}
}

func TestFactoryReturnRegionReanalysisClearsBinding(t *testing.T) {
	call := &ast.CallExpr{}
	a := &Analyzer{declaredCallReturnRegions: map[*ast.CallExpr]string{call: "previous_owner"}}
	a.analyzeResolvedCallExprWithExpected(call, nil, nil, nil)
	if _, exists := a.declaredCallReturnRegions[call]; exists {
		t.Fatal("unresolved reanalysis kept prior return-region provenance")
	}
}
