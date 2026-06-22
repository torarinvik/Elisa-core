package semantic

import (
	"strings"
	"testing"
)

// requiresReportSource has one requires-bearing function called at two sites: a provable site
// (constant arg satisfies `off >= 0`) and an unprovable site (an unbounded parameter arg).
const requiresReportSource = `
def use_off(buf: u8&, off: i32) -> u8:
    requires off >= 0
    return buf[off.usize()]

def provable_site(buf: u8&) -> u8:
    k: i32 = 5
    return use_off(buf, k)

def unprovable_site(buf: u8&, n: i32) -> u8:
    return use_off(buf, n)
`

// With -requires-report ON, the aggregation reports 1 provable of 2 call sites and pins the
// unprovable site to its source location.
func TestRequiresReportOnAggregatesSites(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "requires_report.elisa", requiresReportSource, AnalyzeOptions{RequiresReport: true})

	if len(result.RequiresReport) != 1 {
		t.Fatalf("expected exactly one requires-report entry, got %d: %+v", len(result.RequiresReport), result.RequiresReport)
	}
	entry := result.RequiresReport[0]
	if entry.DeclName != "use_off" {
		t.Fatalf("expected entry for use_off, got %q", entry.DeclName)
	}
	if entry.Provable != 1 || entry.Unprovable != 1 {
		t.Fatalf("expected 1/2 provable (1 unprovable), got provable=%d unprovable=%d", entry.Provable, entry.Unprovable)
	}
	if len(entry.UnprovableSites) != 1 {
		t.Fatalf("expected one unprovable site location, got %d: %v", len(entry.UnprovableSites), entry.UnprovableSites)
	}
	site := entry.UnprovableSites[0].String()
	// The unprovable call use_off(buf, n) is on line 11 of the source (1-based, leading newline).
	if !strings.Contains(site, "requires_report.elisa:11:") {
		t.Fatalf("expected the unprovable site on line 11, got %q", site)
	}
}

// With -requires-report OFF, the report is empty and the analysis diagnostics are byte-identical to
// a plain run (the flag has ZERO behavior change otherwise).
func TestRequiresReportOffIsInert(t *testing.T) {
	off := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "requires_report_off.elisa", requiresReportSource, AnalyzeOptions{})
	if len(off.RequiresReport) != 0 {
		t.Fatalf("expected no requires-report entries when the flag is off, got %+v", off.RequiresReport)
	}

	on := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "requires_report_off.elisa", requiresReportSource, AnalyzeOptions{RequiresReport: true})
	if allDiagnostics(off) != allDiagnostics(on) {
		t.Fatalf("requires-report changed diagnostics:\n off=%q\n on =%q", allDiagnostics(off), allDiagnostics(on))
	}
}
