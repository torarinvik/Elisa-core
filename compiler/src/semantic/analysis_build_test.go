package semantic

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAnalysisBuildRoundTripAndFinish(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "1")
	cases := []string{retainedBefore, `struct Net:
    fd: i64
protocol Sender:
    def send(self: Self, n: i64) -> void can[Atomics.Load]
impl Sender for Net:
    def send(self: Net, n: i64) -> void can[Atomics.Load, Atomics.Store]:
        return
`, `def positive(x: i64) -> i64:
    requires x > 0
    ensure result > 0
    return x
def caller() -> i64:
    return positive(-1)
`}
	for i, source := range cases {
		b, err := BeginAnalysisBuild(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !b.MatchesInvocation(b.file.Filename, []byte(source), AnalyzeOptions{}) || b.MatchesInvocation(b.file.Filename, []byte(source+"\n"), AnalyzeOptions{}) {
			t.Fatal("build revision matching failed")
		}
		paused := 0
		for {
			data, err := b.MarshalState()
			if err != nil {
				t.Fatalf("case %d step %d checkpoint: %v", i, paused, err)
			}
			b, err = RestoreAnalysisBuild(data)
			if err != nil {
				t.Fatal(err)
			}
			before, total := b.Progress()
			postBefore, _ := b.PostProgress()
			result, state, err := b.Advance(1, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if result == nil {
				if state != nil {
					t.Fatal("unfinished analysis exposed retained state")
				}
				after, _ := b.Progress()
				postAfter, _ := b.PostProgress()
				if after-before+postAfter-postBefore != 1 || after > total {
					t.Fatalf("invalid progress: %d -> %d of %d", before, after, total)
				}
				paused++
				continue
			}
			if state == nil {
				t.Fatal("finished analysis omitted retained state")
			}
			cold := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
			assertIncrementalResultParity(t, cold, result)
			if i == 1 && !strings.Contains(retainedDiagnostics(result), "subset of the protocol") {
				t.Fatal("unfinished protocol obligation was lost")
			}
			if _, err := state.MarshalState(); err != nil {
				t.Fatal(err)
			}
			// Post-phase diagnostics must remain outside body windows when a
			// later retained update removes and replays final-check results.
			updated, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
			if err != nil {
				t.Fatal(err)
			}
			if retainedDiagnostics(updated) != retainedDiagnostics(cold) {
				t.Fatalf("case %d resumed final checks changed retained diagnostic replay:\ngot:\n%s\nwant:\n%s", i, retainedDiagnostics(updated), retainedDiagnostics(cold))
			}
			if i == 1 {
				shifted := "\n\n" + source
				updated, err = state.Update(retainedParse(t, shifted), retainedParse(t, shifted), []byte(shifted))
				if err != nil {
					t.Fatal(err)
				}
				want := AnalyzeWithOptions(retainedParse(t, shifted), AnalyzeOptions{})
				if retainedDiagnostics(updated) != retainedDiagnostics(want) {
					t.Fatalf("shifted protocol check differs:\ngot: %s\nwant: %s", retainedDiagnostics(updated), retainedDiagnostics(want))
				}
			}
			if _, err := b.MarshalState(); err == nil {
				t.Fatal("completed analysis admitted as unfinished checkpoint")
			}
			break
		}
		if paused == 0 {
			t.Fatal("fixture never stopped between declarations")
		}
	}
}

func TestAnalysisBuildRejectsActiveJournal(t *testing.T) {
	b, err := BeginAnalysisBuild(retainedParse(t, retainedBefore), retainedParse(t, retainedBefore), []byte(retainedBefore), AnalyzeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	aliases := map[string]returnBorrowFlow{}
	b.retained.a.beginReturnBorrowBranch(aliases)
	if _, err := b.MarshalState(); err == nil {
		t.Fatal("saved active branch")
	}
	b.retained.a.endReturnBorrowBranch(aliases)
	b.retained.a.pushStorageViewLoopUseFrame(b.retained.a.globalScope, nil)
	if _, err := b.MarshalState(); err == nil {
		t.Fatal("saved active loop frame")
	}
	b.retained.a.popStorageViewLoopUseFrame()
	if _, err := b.MarshalState(); err != nil {
		t.Fatal(err)
	}
}

func TestAnalysisBuildBoundedBodyBoundaryResumesBothFormats(t *testing.T) {
	const source = "def value() -> i64:\n    return 7\n"
	b, err := BeginAnalysisBuild(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result, state, err := b.Advance(2, 0)
	if err != nil || result != nil || state != nil {
		t.Fatalf("body boundary exposed result: %v", err)
	}
	done, total := b.Progress()
	post, _ := b.PostProgress()
	if done != total || done != 1 || post != 0 || b.LastStats.PostPhases != 0 {
		t.Fatalf("final check ran in body step: %d/%d, post %d", done, total, post)
	}
	framed, err := b.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(framed), "ELISTATE4") {
		t.Fatal("checkpoint is not framed")
	}
	legacy, err := semanticStateRegistry().Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	cold := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
	for _, data := range [][]byte{legacy, framed} {
		restored, err := RestoreAnalysisBuild(data)
		if err != nil {
			t.Fatal(err)
		}
		result, state, err := restored.Advance(0, 0)
		if err != nil || result == nil || state == nil {
			t.Fatalf("boundary did not finish: %v", err)
		}
		assertIncrementalResultParity(t, cold, result)
		updated, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		if retainedDiagnostics(updated) != retainedDiagnostics(cold) {
			t.Fatal("post-check diagnostics changed on replay")
		}
	}
}

func TestAnalysisBuildUnboundedAndEmptyRemainProductive(t *testing.T) {
	for _, source := range []string{"def value() -> i64:\n    return 7\n", "# empty\n"} {
		b, err := BeginAnalysisBuild(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
		if err != nil {
			t.Fatal(err)
		}
		result, state, err := b.Advance(0, 0)
		if err != nil || result == nil || state == nil {
			t.Fatalf("unbounded call did not finish: %v", err)
		}
		assertIncrementalResultParity(t, AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{}), result)
	}
	const empty = "# empty\n"
	b, err := BeginAnalysisBuild(retainedParse(t, empty), retainedParse(t, empty), []byte(empty), AnalyzeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result, _, err := b.Advance(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	post, _ := b.PostProgress()
	if result == nil && post == 0 {
		t.Fatal("empty bounded call made no progress")
	}
	result, state, err := b.Advance(0, 0)
	if err != nil || result == nil || state == nil {
		t.Fatalf("empty boundary did not finish: %v", err)
	}
}

func TestAnalysisBuildCheckpointsWithinLateBorrowPhase(t *testing.T) {
	// Initial builds analyze bodies serially; hold that policy constant for the
	// reference so invocation-local alias domains compare within the same mode.
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	source := `struct BorrowedBox:
    value: i64&
global root: i64 = 9

def pack(value: i64&) -> BorrowedBox:
    return BorrowedBox{value: value}

def relay(value: i64&) -> BorrowedBox:
    return pack(value)
`
	for i := 0; i < 6; i++ {
		source += fmt.Sprintf("\ndef escapes%d() -> BorrowedBox:\n    local: i64 = 1\n    return relay(&local)\n", i)
	}
	b, err := BeginAnalysisBuild(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var result *Result
	var state *RetainedAnalysis
	partial := 0
	bodyEnd := -1
	for step := 0; step < 200; step++ {
		data, err := b.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		b, err = RestoreAnalysisBuild(data)
		if err != nil {
			t.Fatal(err)
		}
		result, state, err = b.Advance(0, time.Nanosecond)
		if err != nil {
			t.Fatal(err)
		}
		if b.LastStats.PostFrames > 1 {
			t.Fatalf("one-frame budget consumed %d", b.LastStats.PostFrames)
		}
		if b.lateStarted {
			if bodyEnd < 0 {
				bodyEnd = b.retained.bodyEnd
			}
			if b.retained.bodyEnd != bodyEnd {
				t.Fatal("late diagnostics moved the body boundary")
			}
			if b.postNext == 0 {
				partial++
				if result != nil || state != nil {
					t.Fatal("unfinished phase exposed a result")
				}
			}
		}
		if result != nil {
			break
		}
	}
	if result == nil || state == nil || partial < 2 {
		t.Fatalf("late-frame checkpoints missing: partial=%d result=%v", partial, result != nil)
	}
	cold := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
	if !strings.Contains(retainedDiagnostics(cold), "function-local storage") {
		t.Fatalf("fixture did not exercise late borrow rejection: %s", retainedDiagnostics(cold))
	}
	assertIncrementalResultParity(t, cold, result)
	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, current := range []string{source, "\n\n" + source} {
		got, err := state.Update(retainedParse(t, current), retainedParse(t, current), []byte(current))
		if err != nil {
			t.Fatal(err)
		}
		want := AnalyzeWithOptions(retainedParse(t, current), AnalyzeOptions{})
		if strings.HasPrefix(current, "\n") {
			// Retained syntax keeps declaration-relative expanded-buffer offsets
			// by design. Its rendered diagnostic locations must still move.
			if retainedDiagnostics(want) != retainedDiagnostics(got) {
				t.Fatal("shifted late diagnostics differ")
			}
		} else {
			assertIncrementalResultParity(t, want, got)
		}
	}
}

func TestAnalysisBuildLateFramesKeepLastAnalysisAcrossCheckpoint(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	source := `struct BorrowedBox:
    value: i64&
def pack(value: i64&) -> BorrowedBox:
    return BorrowedBox{value: value}
def escapes() -> BorrowedBox:
    local: i64 = 1
    return pack(&local)
`
	b, err := BeginAnalysisBuild(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = b.Advance(len(b.retained.decls), 0); err != nil {
		t.Fatal(err)
	}
	frames := b.retained.a.returnBorrowLateFrames
	if len(frames) == 0 {
		t.Fatal("fixture has no deferred frames")
	}
	last := frames[len(frames)-1]
	if last.frame.fn.Name != "escapes" || len(last.frame.pending) == 0 {
		t.Fatal("fixture lacks the final pending escape check")
	}
	// Simulate the same declaration being reanalyzed without pending checks. The
	// earlier analysis must not report its obsolete check, even before the final
	// duplicate is reached in a later process.
	empty := *last.frame
	empty.pending = nil
	last.frame = &empty
	b.retained.a.returnBorrowLateFrames = append(frames, last)
	var result *Result
	for i := 0; i < 30; i++ {
		data, err := b.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		b, err = RestoreAnalysisBuild(data)
		if err != nil {
			t.Fatal(err)
		}
		result, _, err = b.Advance(0, time.Nanosecond)
		if err != nil {
			t.Fatal(err)
		}
		if result != nil {
			break
		}
	}
	if result == nil {
		t.Fatal("analysis did not finish")
	}
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatalf("obsolete late checks escaped: %v", errors)
	}
}

func TestAnalysisBuildEmptyLatePhaseAndInvalidCursor(t *testing.T) {
	b, err := BeginAnalysisBuild(retainedParse(t, ""), retainedParse(t, ""), nil, AnalyzeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b.lateNext = 1
	if _, err := b.MarshalState(); err == nil {
		t.Fatal("accepted cursor before phase initialization")
	}
	b.lateStarted = true
	if _, err := b.MarshalState(); err == nil {
		t.Fatal("accepted cursor beyond saved frames")
	}
	b.lateStarted = false
	b.lateNext = 0
	var result *Result
	for i := 0; i < 20; i++ {
		data, err := b.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		b, err = RestoreAnalysisBuild(data)
		if err != nil {
			t.Fatal(err)
		}
		result, _, err = b.Advance(0, time.Nanosecond)
		if err != nil {
			t.Fatal(err)
		}
		if b.LastStats.PostFrames != 0 {
			t.Fatal("empty phase consumed a frame")
		}
		if result != nil {
			break
		}
	}
	if result == nil {
		t.Fatal("empty late phase failed to finish")
	}
	assertIncrementalResultParity(t, AnalyzeWithOptions(retainedParse(t, ""), AnalyzeOptions{}), result)
}
