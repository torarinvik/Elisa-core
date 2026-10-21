package semantic

import "testing"

const movedReportSource = `global region source
struct Entry:
    name: sview @source
    line: u32
struct Report:
    entries: mutable darray[Entry] = []
def Report() -> Report:
    return Report{entries: []}
def fill(report: lmut Report) -> void:
    can Memory.Allocate, Abort.Panic:
        report.entries <- report.entries.push(Entry{name: "live", line: 17})
def produce() -> Report:
    can Memory.Allocate, Abort.Panic:
        report: mutable Report = Report()
        report <- fill(report)
        return move report
def literal() -> Report:
    can Memory.Allocate, Abort.Panic:
        report: mutable Report = Report{entries: []}
        report <- fill(report)
        return move report
def wrapper() -> Report:
    return produce()
def moved_container() -> darray[u32]:
    can Memory.Allocate, Abort.Panic:
        values: mutable darray[u32] = []
        values.push(17)
        return move values
def borrowed(report: Report&) -> darray[Entry]:
    return report.entries
def scalar(report: Report&) -> usize:
    return report.entries.count
`

func TestRegionPolymorphicMovedOwningReturns(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "moved_report.elisa", movedReportSource, AnalyzeOptions{})
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatalf("valid moved owning return rejected: %v", errors)
	}
	for _, name := range []string{"produce", "literal", "wrapper", "moved_container"} {
		if !regionPolymorphicFuncType(t, result, name).RegionPolymorphic {
			t.Errorf("%s does not adopt caller arena for moved owning return", name)
		}
	}
	for _, name := range []string{"borrowed", "scalar"} {
		if regionPolymorphicFuncType(t, result, name).RegionPolymorphic {
			t.Errorf("%s unnecessarily adopted caller arena", name)
		}
	}
}

func TestRegionPolymorphicMoveKeepsExplicitArenaOwnership(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "explicit_moved_report.elisa", movedReportSource+`
def explicit(arena: mutable Arena&) -> Report:
    can Memory.Allocate, Abort.Panic:
        in arena:
            report: mutable Report = Report{entries: []}
            report.entries.push(Entry{name: "live", line: 17})
            return move report
def invalid_named() -> Report:
    can Memory.Allocate, Abort.Panic:
        region short(4096):
            report: mutable Report = Report{entries: []}
            report.entries.push(Entry{name: "live", line: 17})
            return move report
`, AnalyzeOptions{})
	for _, name := range []string{"explicit", "invalid_named"} {
		if regionPolymorphicFuncType(t, result, name).RegionPolymorphic {
			t.Errorf("%s adopted explicitly owned storage", name)
		}
	}
}
