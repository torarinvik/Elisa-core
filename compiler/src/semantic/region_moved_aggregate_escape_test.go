package semantic

import (
	"strings"
	"testing"
)

func TestMovedAggregateRetainsNamedRegionReturnTaint(t *testing.T) {
	const prefix = `struct Report:
    entries: mutable darray[u32] = []
`
	for _, tc := range []struct{ name, returnType, value string }{
		{"plain", "Report", "report"},
		{"move", "Report", "move report"},
		{"parenthesized_move", "Report", "(move report)"},
		{"nested_move", "darray[Report]", "[move report]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := prefix + "def invalid() -> " + tc.returnType + `:
    can Memory.Allocate, Abort.Panic:
        region short(4096):
            report: mutable Report = Report{entries: []}
            report.entries.push(17)
            return ` + tc.value + "\n"
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, tc.name+".elisa", source, AnalyzeOptions{})
			if !strings.Contains(strings.Join(result.Errors(), "\n"), `scope-owned region "short" escapes via return`) {
				t.Fatalf("missing named-region return rejection: %v", result.Errors())
			}
		})
	}
}

func TestMovedAggregateKeepsCallerAndProgramRegions(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "valid_moved_owners.elisa", `global region stable
struct Report:
    entries: mutable darray[u32] = []
def caller_owned(arena: mutable Arena&) -> Report:
    can Memory.Allocate, Abort.Panic:
        in arena:
            report: mutable Report = Report{entries: []}
            report.entries.push(17)
            return move report
def program_owned() -> Report:
    can Memory.Allocate, Abort.Panic:
        in stable:
            report: mutable Report = Report{entries: []}
            report.entries.push(17)
            return move report
`, AnalyzeOptions{})
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatalf("long-lived moved return rejected: %v", errors)
	}
}
