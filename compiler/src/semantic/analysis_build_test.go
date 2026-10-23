package semantic

import (
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
