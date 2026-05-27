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

func TestSegmentFlowRejectsHostCallAfterGuestTransition(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "segment_flow_rejects_host_after_guest.elisa", `
extern load_guest() -> void can[Unsafe.SegmentMutation, Segment.Guest]
extern host_only() -> void can[Segment.Host]

def run() -> void:
    can Unsafe.SegmentMutation, Segment.Guest:
        load_guest()
    can Segment.Host:
        host_only()
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !strings.Contains(all, `segment owner mismatch: call to "host_only" requires Segment.Host but current ambient segment is Segment.Guest`) {
		t.Fatalf("expected host call after guest transition to be rejected, got:\n%s", all)
	}
}

func TestSegmentFlowAcceptsHostCallAfterHostTransition(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "segment_flow_accepts_host_restore.elisa", `
extern load_guest() -> void can[Unsafe.SegmentMutation, Segment.Guest]
extern restore_host() -> void can[Unsafe.SegmentMutation, Segment.Host]
extern host_only() -> void can[Segment.Host]

def run() -> void:
    can Unsafe.SegmentMutation, Segment.Guest:
        load_guest()
    can Unsafe.SegmentMutation, Segment.Host:
        restore_host()
    can Segment.Host:
        host_only()
`)
	if all := allDiagnostics(result); strings.Contains(all, `segment owner mismatch`) {
		t.Fatalf("expected host call after host transition to be accepted, got:\n%s", all)
	}
}

func TestSegmentFlowRejectsGuestCallInHostState(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "segment_flow_rejects_guest_in_host.elisa", `
extern guest_only() -> void can[Segment.Guest]

def run() -> void:
    can Segment.Guest:
        guest_only()
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !strings.Contains(all, `segment owner mismatch: call to "guest_only" requires Segment.Guest but current ambient segment is Segment.Host`) {
		t.Fatalf("expected guest call in host state to be rejected, got:\n%s", all)
	}
}
