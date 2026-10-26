package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"testing"
)

func TestIRInputReadReportIsDiagnosticAndKeepsNegativeQueries(t *testing.T) {
	decl := &ast.FuncDecl{Name: "f"}
	symbol := &semantic.Symbol{Name: "f", Node: decl}
	recorder := &irInputReadRecorder{}
	recorder.body(decl, decl, "function-emission")
	recorder.body(decl, decl, "function-emission")
	recorder.body(decl, decl, "literal-wrapper-stability")
	recorder.query(nil, "test-global", "global", "f", symbol, true)
	recorder.query(nil, "test-global", "global", "absent", nil, false)
	report := recorder.snapshot()
	if len(report.Bodies) != 2 || len(report.Queries) != 2 || report.Queries[1].Found || report.Queries[1].Name != "absent" {
		t.Fatal("observed body uses or negative query evidence lost")
	}
	if len(report.Uncovered) == 0 || report.Lowered {
		t.Fatal("diagnostic observations claimed complete input authority")
	}
	report.Bodies[0].Use = "modified"
	report.Queries[0].Name = "modified"
	if again := recorder.snapshot(); again.Bodies[0].Use != "function-emission" || again.Queries[0].Name != "f" {
		t.Fatal("report mutation changed the recorder's immutable observations")
	}
}
