package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestParamRetentionChecksEveryMatchingArgument(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "duplicate_retention.elisa", `
def keep_second(first: darray[i64]&, second: darray[i64]&) -> void:
    alias: darray[i64]& = second

def forward(value: darray[i64]&) -> void:
    keep_second(value, value)

def ignore_both(first: darray[i64]&, second: darray[i64]&) -> void:
    return

def forward_readonly(value: darray[i64]&) -> void:
    ignore_both(value, value)

def forward_again(value: darray[i64]&) -> void:
    forward(value)
`, AnalyzeOptions{RecordDeathTimeCohorts: true})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	var keeper, forward, readonly, again *ast.FuncDecl
	for _, decl := range result.ActiveFile().Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			if fn.Name == "keep_second" {
				keeper = fn
			}
			if fn.Name == "forward" {
				forward = fn
			}
			if fn.Name == "forward_readonly" {
				readonly = fn
			}
			if fn.Name == "forward_again" {
				again = fn
			}
		}
	}
	if keeper == nil || forward == nil || readonly == nil || again == nil {
		t.Fatal("missing test functions")
	}
	summary := result.analyzer.paramRetained
	if len(summary[keeper]) < 2 || summary[keeper][0] || !summary[keeper][1] {
		t.Fatalf("invalid keeper control: %v", summary[keeper])
	}
	if len(summary[forward]) == 0 || !summary[forward][0] {
		t.Fatal("retaining second argument was hidden by identical non-retaining first argument")
	}
	if len(summary[readonly]) == 0 || summary[readonly][0] {
		t.Fatal("duplicate read-only arguments were conservatively marked retained")
	}
	if len(summary[again]) == 0 || !summary[again][0] {
		t.Fatal("duplicate-argument retention failed to propagate to its caller")
	}
}
