package backend

import (
	"regexp"
	"strings"
	"testing"
)

func TestLLVMReturnedMovedAggregateKeepsCallerArena(t *testing.T) {
	const source = `struct Report:
    entries: mutable darray[u32] = []
def Report() -> Report:
    return Report{entries: []}
def fill(report: lmut Report) -> void:
    can Memory.Allocate, Abort.Panic:
        report.entries <- report.entries.push(17)
def produce() -> Report:
    can Memory.Allocate, Abort.Panic:
        report: mutable Report = Report()
        report <- fill(report)
        return move report
def wrapper() -> Report:
    return produce()
def main() -> i32:
    can Memory.Allocate, Abort.Panic:
        report: Report = wrapper()
        return report.entries[0].i32() - 17
`
	result := parseAndAnalyzeBackendTest(t, "moved_report_arena.elisa", source)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"produce", "wrapper"} {
		marker := "@" + name + "("
		start := -1
		for _, line := range strings.Split(output, "\n") {
			if strings.HasPrefix(line, "define ") && strings.Contains(line, marker) {
				start = strings.Index(output, line)
				break
			}
		}
		if start < 0 {
			t.Fatalf("missing %s", name)
		}
		body := output[start:]
		end := strings.Index(body, "\n}")
		if end < 0 {
			t.Fatalf("unterminated %s", name)
		}
		body = body[:end]
		header := strings.SplitN(body, "\n", 2)[0]
		wantPointers := 1
		if strings.Contains(header, " sret(") {
			wantPointers++
		}
		if strings.Count(header, "ptr ") < wantPointers {
			t.Errorf("%s has no caller arena: %s", name, header)
		}
		arenas := regexp.MustCompile(`ptr (%[0-9]+)`).FindAllStringSubmatch(header, -1)
		if len(arenas) == 0 {
			t.Fatalf("%s has no caller arena argument", name)
		}
		callerArena := arenas[len(arenas)-1][1]
		// Private scratch may be freed; the returned buffer's caller arena may not.
		if strings.Contains(body, "call void @arena_free(ptr "+callerArena+")") {
			t.Errorf("%s frees its caller arena", name)
		}
		if name == "produce" && !strings.Contains(body, "@fill(ptr %report, ptr "+callerArena+")") {
			t.Error("moved report's fill does not use the caller arena")
		}
	}
}
