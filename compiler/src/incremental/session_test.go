package incremental

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

func smallSession(t *testing.T, evaluators map[string]Evaluator, limits Limits) *Session {
	t.Helper()
	s, err := NewSession(evaluators, limits)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func testLimits() Limits {
	return Limits{Queries: 8, Inputs: 16, InputEdges: 32, Dependencies: 32, Bytes: 16 << 10}
}
func readPublication(name, value string, absent bool) Publication {
	return Publication{Key: Key{"read", name}, Inputs: []Change{{Key: Key{"input", name}, Value: value, Remove: absent}}, MaxResultBytes: 64, MaxDependencies: 1}
}
func sessionRead(r *Reader, name string) (string, error) {
	value, present := r.Input(Key{"input", name})
	if !present {
		return "missing", nil
	}
	return value, nil
}
func publishSession(t *testing.T, s *Session, pubs ...Publication) Published {
	t.Helper()
	result, err := s.Publish(context.Background(), pubs)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func sessionValue(t *testing.T, rev Revision, key Key, want string) {
	t.Helper()
	got, err := rev.Evaluate(context.Background(), key)
	if err != nil || got != want {
		t.Fatalf("got %q/%v want %q", got, err, want)
	}
}

func TestSessionPublicationGenerationAndRejection(t *testing.T) {
	s := smallSession(t, map[string]Evaluator{"read": sessionRead}, testLimits())
	p := readPublication("q", "A", false)
	a := publishSession(t, s, p)
	sessionValue(t, a.Revision, p.Key, "A")
	before := s.Stats().Queries
	b := publishSession(t, s, p)
	sessionValue(t, b.Revision, p.Key, "A")
	if s.Stats().Queries.Executed != before.Executed {
		t.Fatal("unchanged snapshot lost memo")
	}
	if _, err := a.Revision.Evaluate(context.Background(), p.Key); !errors.Is(err, ErrStaleRevision) {
		t.Fatal("old handle survived new generation")
	}
	conflict := p
	conflict.Inputs = []Change{{Key: p.Inputs[0].Key, Value: "B"}, {Key: p.Inputs[0].Key, Value: "C"}}
	if _, err := s.Publish(context.Background(), []Publication{conflict}); err == nil {
		t.Fatal("conflicting batch committed")
	}
	if _, err := b.Revision.Evaluate(context.Background(), p.Key); !errors.Is(err, ErrStaleRevision) {
		t.Fatal("rejected world reused old handle")
	}
	if s.Stats().Ready {
		t.Fatal("invalid world marked ready")
	}
	p.Inputs[0].Value = "B"
	c := publishSession(t, s, p)
	sessionValue(t, c.Revision, p.Key, "B")
}
func TestSessionPartialAdmissionNeverReturnsOldWorld(t *testing.T) {
	limits := testLimits()
	limits.Queries = 1
	s := smallSession(t, map[string]Evaluator{"read": sessionRead}, limits)
	p := readPublication("retained", "A", false)
	a := publishSession(t, s, p)
	sessionValue(t, a.Revision, p.Key, "A")
	fresh := readPublication("new", "B", false)
	b := publishSession(t, s, fresh, p)
	if len(b.Skipped) != 1 || b.Skipped[0] != fresh.Key {
		t.Fatal("retained admission priority lost")
	}
	sessionValue(t, b.Revision, p.Key, "A")
	p.Inputs[0].Value = strings.Repeat("x", limits.Bytes)
	c := publishSession(t, s, p, fresh)
	if len(c.Skipped) != 1 || c.Skipped[0] != p.Key {
		t.Fatal("oversized query not explicitly skipped")
	}
	sessionValue(t, c.Revision, fresh.Key, "B")
	if _, err := c.Revision.Evaluate(context.Background(), p.Key); !errors.Is(err, ErrUnpublishedQuery) {
		t.Fatal("oversized new world served old value")
	}
}
func TestSessionRetiringNegativeInputInvalidatesMemo(t *testing.T) {
	s := smallSession(t, map[string]Evaluator{"read": sessionRead}, testLimits())
	p := readPublication("q", "", true)
	a := publishSession(t, s, p)
	sessionValue(t, a.Revision, p.Key, "missing")
	// The evaluator still asks for this input, but the new publisher no longer
	// declares its absence. It must fail, even though the DB value stayed absent.
	p.Inputs = nil
	b := publishSession(t, s, p)
	if _, err := b.Revision.Evaluate(context.Background(), p.Key); !errors.Is(err, ErrUnpublishedInput) {
		t.Fatalf("negative authority retirement failed: %v", err)
	}
	p = readPublication("q", "new", false)
	c := publishSession(t, s, p)
	sessionValue(t, c.Revision, p.Key, "new")
}
func TestSessionParentsCannotReadRetiredQueriesOrUnpublishedInputs(t *testing.T) {
	child := readPublication("q", "A", false)
	parent := Publication{Key: Key{"parent", "q"}, MaxResultBytes: 64, MaxDependencies: 1}
	s := smallSession(t, map[string]Evaluator{"read": sessionRead, "parent": func(r *Reader, name string) (string, error) { return r.Query(Key{"read", name}) }}, testLimits())
	a := publishSession(t, s, child, parent)
	sessionValue(t, a.Revision, parent.Key, "A")
	b := publishSession(t, s, parent)
	if _, err := b.Revision.Evaluate(context.Background(), parent.Key); !errors.Is(err, ErrUnpublishedQuery) {
		t.Fatalf("retired child leaked through memo: %v", err)
	}
	child.Inputs[0].Value = "B"
	c := publishSession(t, s, child, parent)
	sessionValue(t, c.Revision, parent.Key, "B")
}
func TestSessionReservationsAreEnforcedWithoutMemoizingFailure(t *testing.T) {
	s := smallSession(t, map[string]Evaluator{"read": sessionRead}, testLimits())
	p := readPublication("q", "larger", false)
	p.MaxResultBytes = 1
	a := publishSession(t, s, p)
	for i := 0; i < 2; i++ {
		if _, err := a.Revision.Evaluate(context.Background(), p.Key); !errors.Is(err, ErrResultBudget) {
			t.Fatal("result reservation ignored")
		}
	}
	if s.Stats().DatabaseQueries != 0 || s.Stats().Queries.Executed != 2 {
		t.Fatal("failed oversized result memoized")
	}
	p.MaxResultBytes = 64
	p.MaxDependencies = 0
	b := publishSession(t, s, p)
	if _, err := b.Revision.Evaluate(context.Background(), p.Key); !errors.Is(err, ErrDependencyBudget) {
		t.Fatal("dependency reservation ignored")
	}
	p.MaxDependencies = 1
	c := publishSession(t, s, p)
	sessionValue(t, c.Revision, p.Key, "larger")
	p.MaxResultBytes = 1
	d := publishSession(t, s, p)
	if _, err := d.Revision.Evaluate(context.Background(), p.Key); !errors.Is(err, ErrResultBudget) {
		t.Fatal("smaller reservation reused oversized memo")
	}
}
func TestSessionChurnCompactsInputTombstones(t *testing.T) {
	limits := testLimits()
	limits.Queries = 2
	limits.Inputs = 2
	limits.InputEdges = 2
	s := smallSession(t, map[string]Evaluator{"read": sessionRead}, limits)
	for i := 0; i < 100; i++ {
		p := readPublication(fmt.Sprint(i), fmt.Sprint(i), i%2 == 0)
		result := publishSession(t, s, p)
		want := fmt.Sprint(i)
		if i%2 == 0 {
			want = "missing"
		}
		sessionValue(t, result.Revision, p.Key, want)
		stats := s.Stats()
		if stats.RetainedQueries > limits.Queries || stats.RetainedInputs > limits.Inputs || stats.AccountedBytes > limits.Bytes || stats.DatabaseInputs > 2*limits.Inputs || stats.DatabaseQueries > limits.Queries {
			t.Fatalf("unbounded cache: %+v", stats)
		}
	}
	if s.Stats().Compactions == 0 || s.Stats().Queries.Executed != 100 {
		t.Fatal("compaction lost statistics or did not run")
	}
}
func TestSessionSnapshotIsolationAndCancellation(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	s := smallSession(t, map[string]Evaluator{"read": func(r *Reader, name string) (string, error) {
		value, _ := r.Input(Key{"input", name})
		if value == "A" {
			close(started)
			<-release
		}
		second, _ := r.Input(Key{"input", "second"})
		return value + second, nil
	}}, testLimits())
	p := readPublication("q", "A", false)
	p.Inputs = append(p.Inputs, Change{Key: Key{"input", "second"}, Value: "A"})
	p.MaxDependencies = 2
	a := publishSession(t, s, p)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); sessionValue(t, a.Revision, p.Key, "AA") }()
	<-started
	published := make(chan Published, 1)
	go func() { p.Inputs[0].Value = "B"; p.Inputs[1].Value = "B"; published <- publishSession(t, s, p) }()
	close(release)
	wg.Wait()
	b := <-published
	sessionValue(t, b.Revision, p.Key, "BB")
	if _, err := a.Revision.Evaluate(context.Background(), p.Key); !errors.Is(err, ErrStaleRevision) {
		t.Fatal("old world still available")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Revision.Evaluate(ctx, p.Key); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored")
	}
}
func TestSessionOwnsKeysInputsAndResults(t *testing.T) {
	limits := testLimits()
	limits.Bytes = 4 << 20
	source := strings.Repeat("n", 1<<20)
	value := strings.Repeat("v", 1<<20)
	name := source[:5]
	s := smallSession(t, map[string]Evaluator{"read": func(r *Reader, name string) (string, error) { v, _ := r.Input(Key{"input", name}); return v[:1], nil }}, limits)
	p := readPublication(name, value, false)
	p.MaxResultBytes = 1
	a := publishSession(t, s, p)
	sessionValue(t, a.Revision, p.Key, "v")
	canonical := s.queryKeys[p.Key]
	if unsafe.StringData(canonical.Name) == unsafe.StringData(source) {
		t.Fatal("key retained caller source buffer")
	}
	owned := s.inputs[p.Inputs[0].Key].Value
	if unsafe.StringData(owned) == unsafe.StringData(value) {
		t.Fatal("input retained caller buffer")
	}
	cached := s.db.results[canonical].value
	if unsafe.StringData(cached) == unsafe.StringData(owned) {
		t.Fatal("small result retained large input buffer")
	}
	b := publishSession(t, s, p)
	sessionValue(t, b.Revision, p.Key, "v")
	if unsafe.StringData(s.inputs[p.Inputs[0].Key].Value) != unsafe.StringData(owned) {
		t.Fatal("unchanged input copied again")
	}
}

func TestSessionTransitiveNegativeAuthorityRetirement(t *testing.T) {
	for _, compact := range []bool{false, true} {
		t.Run(fmt.Sprint(compact), func(t *testing.T) {
			child := Publication{Key: Key{"read", "q"}, MaxResultBytes: 64, MaxDependencies: 1}
			parent := Publication{Key: Key{"parent", "q"}, MaxResultBytes: 64, MaxDependencies: 1}
			owner := Publication{Key: Key{"owner", "negative"}, Inputs: []Change{{Key: Key{"input", "q"}, Remove: true}}}
			s := smallSession(t, map[string]Evaluator{"read": sessionRead, "parent": func(r *Reader, name string) (string, error) { return r.Query(Key{"read", name}) }, "owner": func(*Reader, string) (string, error) { return "", nil }}, testLimits())
			a := publishSession(t, s, owner, child, parent)
			sessionValue(t, a.Revision, parent.Key, "missing")
			if compact {
				s.debtInputs = s.limits.Inputs
			}
			b := publishSession(t, s, child, parent)
			if _, err := b.Revision.Evaluate(context.Background(), parent.Key); !errors.Is(err, ErrUnpublishedInput) {
				t.Fatalf("transitive negative reader reused after retirement: %v", err)
			}
			if compact && s.Stats().Compactions == 0 {
				t.Fatal("fixture did not compact")
			}
			c := publishSession(t, s, owner, child, parent)
			sessionValue(t, c.Revision, parent.Key, "missing")
		})
	}
}
func TestSessionPartialChildAdmissionFailsParentClosed(t *testing.T) {
	limits := testLimits()
	child := readPublication("q", "A", false)
	parent := Publication{Key: Key{"parent", "q"}, MaxResultBytes: 64, MaxDependencies: 1}
	s := smallSession(t, map[string]Evaluator{"read": sessionRead, "parent": func(r *Reader, name string) (string, error) { return r.Query(Key{"read", name}) }}, limits)
	a := publishSession(t, s, child, parent)
	sessionValue(t, a.Revision, parent.Key, "A")
	child.Inputs[0].Value = strings.Repeat("B", limits.Bytes)
	b := publishSession(t, s, child, parent)
	if len(b.Skipped) != 1 || b.Skipped[0] != child.Key {
		t.Fatal("child capacity miss was not explicit")
	}
	if _, err := b.Revision.Evaluate(context.Background(), parent.Key); !errors.Is(err, ErrUnpublishedQuery) {
		t.Fatal("cached parent reused dropped child")
	}
}

func TestSessionPreCanceledPublicationPreservesCurrentWorld(t *testing.T) {
	s := smallSession(t, map[string]Evaluator{"read": sessionRead}, testLimits())
	p := readPublication("q", "A", false)
	a := publishSession(t, s, p)
	sessionValue(t, a.Revision, p.Key, "A")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 100; i++ {
		if _, err := s.Publish(ctx, []Publication{p}); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled publication accepted")
		}
		sessionValue(t, a.Revision, p.Key, "A")
	}
}

func TestSessionPreparationFailureInvalidatesOldGeneration(t *testing.T) {
	s := smallSession(t, map[string]Evaluator{"read": sessionRead}, testLimits())
	p := readPublication("q", "A", false)
	a := publishSession(t, s, p)
	sessionValue(t, a.Revision, p.Key, "A")
	sentinel := errors.New("preparation failed")
	if _, err := s.PublishPrepared(context.Background(), func() ([]Publication, error) { return nil, sentinel }); !errors.Is(err, sentinel) {
		t.Fatal("preparation error lost")
	}
	if _, err := a.Revision.Evaluate(context.Background(), p.Key); !errors.Is(err, ErrStaleRevision) {
		t.Fatal("failed preparation left old world available")
	}
	b := publishSession(t, s, p)
	sessionValue(t, b.Revision, p.Key, "A")
}

func TestSessionConstantQueryChurnCompactsKeyDebt(t *testing.T) {
	limits := testLimits()
	limits.Queries = 1
	limits.Inputs = 0
	limits.InputEdges = 0
	limits.Dependencies = 0
	limits.Bytes = 4096
	s := smallSession(t, map[string]Evaluator{"constant": func(*Reader, string) (string, error) { return "ok", nil }}, limits)
	for i := 0; i < 100; i++ {
		p := Publication{Key: Key{"constant", strings.Repeat("q", 512) + fmt.Sprint(i)}, MaxResultBytes: 2}
		current := publishSession(t, s, p)
		sessionValue(t, current.Revision, p.Key, "ok")
		stats := s.Stats()
		if stats.RetirementBytes > limits.Bytes || stats.DatabaseQueries > 1 || stats.DatabaseInputs != 0 {
			t.Fatal("constant query metadata unbounded")
		}
	}
	if s.Stats().Compactions == 0 {
		t.Fatal("byte-debt compaction never exercised")
	}
}
