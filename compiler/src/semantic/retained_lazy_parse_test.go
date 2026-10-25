//go:build cgo

package semantic

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/unparse"
)

const lazyParseRetainedSource = `def first(x: i64) -> i64:
    return x + 1

def second(x: i64) -> i64:
    return x + 2

def main() -> i64:
    return first(4)
`

func lazyParseRetainedBaseline(t *testing.T) *RetainedAnalysis {
	t.Helper()
	src := lazyParseRetainedSource
	initial, state := AnalyzeRetained(retainedParse(t, src), retainedParse(t, src), []byte(src), AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	return reloadRetainedFastPrepareForTest(t, state)
}

func TestRetainedLazyParseOnlyWhenFastPreparationDeclines(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	cases := []struct {
		name, after string
		calls       int
	}{
		{"fast", strings.Replace(lazyParseRetainedSource, "x + 1", "x + 3", 1), 0},
		{"decline", strings.Replace(lazyParseRetainedSource, "first(4)", "second(4)", 1), 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state := lazyParseRetainedBaseline(t)
			calls := 0
			scratch := NewLazyParsedRetainedScratch(func() (*ast.File, error) {
				calls++
				return retainedParse(t, c.after), nil
			}, AnalyzeOptions{})
			if !scratch.AcceptsScratch(nil) {
				t.Fatal("explicit lazy parser not admitted")
			}
			warm, err := state.UpdatePrepared(retainedParse(t, c.after), nil, []byte(c.after), scratch)
			if err != nil {
				t.Fatal(err)
			}
			if calls != c.calls {
				t.Fatalf("parse calls=%d want=%d", calls, c.calls)
			}
			if state.LastStats.FastPrepared != (c.calls == 0) || state.LastStats.ScratchPrepared != (c.calls == 1) {
				t.Fatalf("wrong preparation path: %+v", state.LastStats)
			}
			cold := AnalyzeWithOptions(retainedParse(t, c.after), AnalyzeOptions{})
			assertRetainedCanonicalLiveResultParity(t, cold, warm)
			if c.calls == 0 && (scratch.file != nil || scratch.pre != nil) {
				t.Fatal("unused lazy parser produced a graph")
			}
			if c.calls == 1 {
				scratch.Wait()
				if calls != 1 {
					t.Fatal("joining prepared lazy parser repeated parsing")
				}
			}
		})
	}
}

func TestRetainedLazyParseFailureLeavesBaselineAndAllowsFreshRetry(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	state := lazyParseRetainedBaseline(t)
	after := strings.Replace(lazyParseRetainedSource, "first(4)", "second(4)", 1)
	calls := 0
	scratch := NewLazyParsedRetainedScratch(func() (*ast.File, error) { calls++; return nil, fmt.Errorf("deliberate parse rejection") }, AnalyzeOptions{})
	if _, err := state.UpdatePrepared(retainedParse(t, after), nil, []byte(after), scratch); err == nil || !strings.Contains(err.Error(), "deliberate parse rejection") {
		t.Fatalf("missing parse failure: %v", err)
	}
	if calls != 1 {
		t.Fatalf("parse calls=%d", calls)
	}
	scratch.Wait() // A failed on-demand parse is still joinable and never retried.
	if calls != 1 {
		t.Fatal("failed parser restarted")
	}
	if _, ok := state.ResultForSource([]byte(lazyParseRetainedSource)); !ok {
		t.Fatal("early parse failure consumed old baseline")
	}
	retry := NewLazyParsedRetainedScratch(func() (*ast.File, error) { return retainedParse(t, after), nil }, AnalyzeOptions{})
	warm, err := state.UpdatePrepared(retainedParse(t, after), nil, []byte(after), retry)
	if err != nil {
		t.Fatal(err)
	}
	assertRetainedCanonicalLiveResultParity(t, AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{}), warm)
}

func TestRetainedLazyParseRejectsPrimaryASTBeforePreparation(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	state := lazyParseRetainedBaseline(t)
	after := strings.Replace(lazyParseRetainedSource, "first(4)", "second(4)", 1)
	primary := retainedParse(t, after)
	scratch := NewLazyParsedRetainedScratch(func() (*ast.File, error) { return primary, nil }, AnalyzeOptions{})
	if _, err := state.UpdatePrepared(primary, nil, []byte(after), scratch); err == nil || !strings.Contains(err.Error(), "independent") {
		t.Fatalf("primary alias admitted: %v", err)
	}
	beforeJoin := unparse.FormatFile(primary)
	scratch.Wait()
	scratch.Wait()
	if scratch.pre != nil || unparse.FormatFile(primary) != beforeJoin {
		t.Fatal("joining rejected alias declaration-prepared the primary")
	}
	if NewLazyRetainedScratch(nil, AnalyzeOptions{}).AcceptsScratch(nil) {
		t.Fatal("nil identity admitted without lazy parser")
	}
}

func TestRetainedLazyParseNilCallbackFailsClosed(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	state := lazyParseRetainedBaseline(t)
	after := strings.Replace(lazyParseRetainedSource, "first(4)", "second(4)", 1)
	scratch := NewLazyParsedRetainedScratch(nil, AnalyzeOptions{})
	if _, err := state.UpdatePrepared(retainedParse(t, after), nil, []byte(after), scratch); err == nil || !strings.Contains(err.Error(), "missing independent scratch parser") {
		t.Fatalf("nil parser did not fail closed: %v", err)
	}
	scratch.Wait()
	if scratch.pre != nil {
		t.Fatal("nil callback declaration-prepared an absent AST")
	}
	if _, ok := state.ResultForSource([]byte(lazyParseRetainedSource)); !ok {
		t.Fatal("missing parser consumed baseline")
	}
}

func TestRetainedLazyParseIdentityObservationDoesNotReadMaterializingFile(t *testing.T) {
	parsed := retainedParse(t, lazyParseRetainedSource)
	entered, release := make(chan struct{}), make(chan struct{})
	scratch := NewLazyParsedRetainedScratch(func() (*ast.File, error) {
		close(entered)
		<-release
		return parsed, nil
	}, AnalyzeOptions{})
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		if err := scratch.materialize(); err != nil {
			t.Errorf("materialize: %v", err)
		}
	}()
	<-entered
	// Identity answers are immutable even while the callback publishes s.file.
	observationsDone := make(chan struct{})
	workers.Add(1)
	go func() {
		defer workers.Done()
		defer close(observationsDone)
		for i := 0; i < 10000; i++ {
			if !scratch.AcceptsScratch(nil) || scratch.AcceptsScratch(parsed) {
				t.Error("lazy parser identity depends on materialization")
				return
			}
		}
	}()
	close(release)
	<-observationsDone
	workers.Wait()
	if !scratch.AcceptsScratch(nil) || scratch.AcceptsScratch(parsed) {
		t.Fatal("materialized lazy parser accepted nonnil identity")
	}
}

func TestRetainedLazyParseRejectedAliasJoinCannotApplyDeclarationRewrite(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	base := retainedClassificationFixture
	initial, state := AnalyzeRetained(retainedParse(t, base), retainedParse(t, base), []byte(base), AnalyzeOptions{})
	if errors := initial.Errors(); len(errors) != 0 {
		t.Fatal(errors)
	}
	state = reloadRetainedFastPrepareForTest(t, state)
	after := strings.Replace(base, "value: i64 = 0", "value: i64 = observe(node)", 1)
	// A real declaration preparation of this source wraps main's call in a
	// store-hosting region; verify that joining our rejected future cannot do so.
	independentlyPrepared := retainedParse(t, after)
	unprepared := unparse.FormatFile(independentlyPrepared)
	if done := PrepareRetainedScratch(independentlyPrepared, AnalyzeOptions{}); done.pre == nil || unparse.FormatFile(independentlyPrepared) == unprepared {
		t.Fatal("fixture no longer triggers declaration rewriting")
	}
	primary := retainedParse(t, after)
	before := unparse.FormatFile(primary)
	scratch := NewLazyParsedRetainedScratch(func() (*ast.File, error) { return primary, nil }, AnalyzeOptions{})
	if _, err := state.UpdatePrepared(primary, nil, []byte(after), scratch); err == nil || !strings.Contains(err.Error(), "independent") {
		t.Fatalf("primary alias admitted: %v", err)
	}
	scratch.Wait()
	if scratch.pre != nil || unparse.FormatFile(primary) != before {
		t.Fatal("owner join rewrote rejected primary AST")
	}
	if _, ok := state.ResultForSource([]byte(base)); !ok {
		t.Fatal("rejected alias consumed original baseline")
	}
}
