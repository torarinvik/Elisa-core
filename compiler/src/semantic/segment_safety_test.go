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
@reentrant_safe
def alarm_handler() -> void:
    return
`)
	if all := allDiagnostics(result); strings.Contains(all, `@async_entry function "alarm_handler" enters with unknown segment owner`) {
		t.Fatalf("expected segment-establishing async entry to be accepted, got:\n%s", all)
	}
}

func TestAsyncEntryRequiresReentrantSafe(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "async_entry_requires_reentrant_safe.elisa", `
@async_entry
@segment_establishing
def alarm_handler() -> void:
    return
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !strings.Contains(all, `@async_entry function "alarm_handler" can interrupt arbitrary code; add @reentrant_safe`) {
		t.Fatalf("expected async entry without reentrant contract to be rejected, got:\n%s", all)
	}
}

func TestReentrantSafeRejectsUnmarkedCall(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "reentrant_safe_rejects_unmarked_call.elisa", `
def ordinary() -> void:
    return

@reentrant_safe
def alarm_helper() -> void:
    ordinary()
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !strings.Contains(all, `@reentrant_safe code cannot call "ordinary" because it is not marked @reentrant_safe`) {
		t.Fatalf("expected reentrant-safe function to reject unmarked call, got:\n%s", all)
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
@segment_transition(guest)
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
@segment_transition(guest)
extern load_guest() -> void can[Unsafe.SegmentMutation, Segment.Guest]
@segment_transition(host)
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

func TestSegmentFlowRejectsSegmentCallFromUnknownState(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "segment_flow_rejects_unknown.elisa", `
@segment_transition(guest)
extern load_guest() -> void can[Unsafe.SegmentMutation, Segment.Guest]
extern host_only() -> void can[Segment.Host]

def run(flag: bool) -> void:
    if flag:
        can Unsafe.SegmentMutation, Segment.Guest:
            load_guest()
    can Segment.Host:
        host_only()
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !strings.Contains(all, `segment owner unknown: call to "host_only" requires Segment.Host; establish Segment.Host before crossing this boundary`) {
		t.Fatalf("expected Segment.Host call from unknown state to be rejected, got:\n%s", all)
	}
}

func TestSegmentTransitionAnnotationDrivesAmbientOwner(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "segment_transition_annotation.elisa", `
@segment_transition(guest)
extern load_guest() -> void can[Unsafe.SegmentMutation]
extern host_only() -> void can[Segment.Host]

def run() -> void:
    can Unsafe.SegmentMutation:
        load_guest()
    can Segment.Host:
        host_only()
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !strings.Contains(all, `segment owner mismatch: call to "host_only" requires Segment.Host but current ambient segment is Segment.Guest`) {
		t.Fatalf("expected explicit segment transition annotation to move ambient owner to guest, got:\n%s", all)
	}
}

func TestSegmentTransitionAnnotationRejectsInvalidTarget(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "segment_transition_bad_target.elisa", `
@segment_transition(kernel)
extern load_kernel() -> void can[Unsafe.SegmentMutation]
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !strings.Contains(all, `@segment_transition on extern function "load_kernel" uses unsupported target "kernel" (expected host or guest)`) {
		t.Fatalf("expected invalid segment transition target to be rejected, got:\n%s", all)
	}
}

func TestSegmentMutatingExternRequiresExplicitTransition(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "segment_transition_required.elisa", `
extern load_guest() -> void can[Unsafe.SegmentMutation, Segment.Guest]
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !strings.Contains(all, `extern function "load_guest" mutates the active segment; add @segment_transition(host) or @segment_transition(guest)`) {
		t.Fatalf("expected segment-mutating extern to require an explicit transition contract, got:\n%s", all)
	}
}
