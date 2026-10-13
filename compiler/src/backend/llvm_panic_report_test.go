//go:build cgo

package backend

import (
	"strings"
	"testing"
)

const panicReportProbeSrc = `@inline(never)
def lookup(table: darray[i64]&, i: usize) -> i64 can[Abort.Panic]:
    v: i64 = table[i]
    if v == -1:
        panic("lookup failed")
    return v

def main() -> int can[Memory.Allocate, Abort.Panic]:
    t: mutable darray[i64] = []
    t.push(3)
    return lookup(&t, 0).int()
`

// Every panic site is one cold call to a single outlined reporter whose memory effects are
// argmem-read + inaccessiblemem only, so a read-only function whose only write is its panic arm
// is inferred memory(read) and callers keep loaded headers across the call.
func TestPanicReportHelperIsOutlinedAndCold(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "panic_report_probe.elisa", panicReportProbeSrc)
	ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel3)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt error: %v", err)
	}
	if !strings.Contains(ir, "@elisa.panic.report(") {
		t.Fatalf("expected the outlined panic reporter, got:\n%s", ir)
	}
	// After IPO the reporter may be specialised (constant args folded into global reads), so accept
	// any memory effect that writes nothing but inaccessible memory.
	reporter := functionBody(t, ir, "elisa.panic.report")
	reporterHeader := reporter[:strings.Index(reporter, "{")]
	reporterAttr := strings.TrimSpace(reporterHeader[strings.LastIndex(reporterHeader, "#"):])
	reporterOK := false
	for _, line := range strings.Split(ir, "\n") {
		if strings.HasPrefix(line, "attributes "+reporterAttr+" ") {
			reporterOK = strings.Contains(line, "noreturn") && strings.Contains(line, "cold") &&
				strings.Contains(line, "inaccessiblemem: readwrite") &&
				(strings.Contains(line, "memory(read") || strings.Contains(line, "memory(argmem: read"))
		}
	}
	if !reporterOK {
		t.Fatalf("panic reporter must be cold noreturn and write only inaccessible memory, got:\n%s", ir)
	}
	lookup := functionBody(t, ir, "lookup")
	if strings.Contains(lookup, "@printf") || strings.Contains(lookup, "@backtrace") {
		t.Fatalf("panic arm must not inline printf/backtrace into the caller:\n%s", lookup)
	}
	header := lookup[:strings.Index(lookup, "{")]
	attrRef := header[strings.LastIndex(header, "#"):]
	attrRef = strings.TrimSpace(attrRef)
	for _, line := range strings.Split(ir, "\n") {
		if strings.HasPrefix(line, "attributes "+attrRef+" ") {
			if !strings.Contains(line, "memory(read") && !strings.Contains(line, "memory(argmem: read") {
				t.Fatalf("read-only function with a panic arm must be inferred read-only, got %s", line)
			}
			return
		}
	}
	t.Fatalf("attribute group %s of lookup not found:\n%s", attrRef, ir)
}
