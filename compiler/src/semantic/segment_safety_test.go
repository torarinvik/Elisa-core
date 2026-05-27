package semantic

import (
	"strings"
	"testing"
)

func TestAsyncEntryRequiresExplicitSegmentContract(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "async_entry_requires_segment_contract.elisa", `
@async_entry
def alarm_handler() -> void:
    return
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !strings.Contains(all, `@async_entry function "alarm_handler" enters with unknown segment owner`) {
		t.Fatalf("expected async entry without segment contract to be rejected, got:\n%s", all)
	}
}

func TestAsyncEntrySegmentEstablishingIsAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "async_entry_segment_establishing.elisa", `
@async_entry
@segment_establishing
def alarm_handler() -> void:
    return
`)
	if all := allDiagnostics(result); strings.Contains(all, `@async_entry function "alarm_handler" enters with unknown segment owner`) {
		t.Fatalf("expected segment-establishing async entry to be accepted, got:\n%s", all)
	}
}

func TestSegmentAgnosticRejectsSegmentGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "segment_agnostic_rejects_segment_grant.elisa", `
@segment_agnostic
def alarm_handler() -> void:
    can Segment.Host:
        return
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !strings.Contains(all, `@segment_agnostic code cannot grant Segment.Host, Segment.Guest, or Unsafe.SegmentMutation`) {
		t.Fatalf("expected segment-agnostic function to reject segment can block, got:\n%s", all)
	}
}

func TestSegmentAgnosticRejectsSegmentDependentCall(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "segment_agnostic_rejects_segment_call.elisa", `
extern host_only() -> void can[Segment.Host]

@segment_agnostic
def alarm_handler() -> void:
    can Segment.Host:
        host_only()
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !strings.Contains(all, `@segment_agnostic code cannot call "host_only" because it requires Segment.Host, Segment.Guest, or Unsafe.SegmentMutation`) {
		t.Fatalf("expected segment-agnostic function to reject segment-dependent call, got:\n%s", all)
	}
}
