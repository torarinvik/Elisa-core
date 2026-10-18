package incremental

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func publishDelta(t *testing.T, s *Session, d Delta) Published {
	t.Helper()
	w, err := s.PublishDelta(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestSessionDeltaOwnershipAndFailure(t *testing.T) {
	s := smallSession(t, map[string]Evaluator{"read": sessionRead}, testLimits())
	a, b := readPublication("a", "A", false), readPublication("b", "B", false)
	a.Inputs = append(a.Inputs, Change{Key: Key{"shared", "x"}, Value: "old"})
	b.Inputs = append(b.Inputs, a.Inputs[1])
	w := publishDelta(t, s, Delta{Upsert: []Publication{a, b}})
	sessionValue(t, w.Revision, a.Key, "A")
	sessionValue(t, w.Revision, b.Key, "B")
	before := s.db.revision
	a.Inputs[0].Value = "edited"
	w = publishDelta(t, s, Delta{Upsert: []Publication{a}})
	if s.db.revision != before+1 {
		t.Fatal("delta advanced more than once")
	}
	sessionValue(t, w.Revision, a.Key, "edited")
	sessionValue(t, w.Revision, b.Key, "B")
	a.Inputs[1].Value = "new"
	if _, err := s.PublishDelta(context.Background(), Delta{Upsert: []Publication{a}}); err == nil {
		t.Fatal("changed shared input under untouched owner")
	}
	if _, err := w.Revision.Evaluate(context.Background(), b.Key); !errors.Is(err, ErrStaleRevision) {
		t.Fatal("failed delta left handle live", err)
	}
	if s.inputs[a.Inputs[1].Key].Value != "old" {
		t.Fatal("failure mutated DB inventory")
	}
	b.Inputs[1].Value = "new"
	w = publishDelta(t, s, Delta{Upsert: []Publication{a, b}})
	sessionValue(t, w.Revision, a.Key, "edited")
	w = publishDelta(t, s, Delta{Remove: []Key{a.Key}})
	sessionValue(t, w.Revision, b.Key, "B")
	if _, ok := s.inputs[a.Inputs[1].Key]; !ok {
		t.Fatal("shared cell retired with live owner")
	}
	if _, err := w.Revision.Evaluate(context.Background(), a.Key); !errors.Is(err, ErrUnpublishedQuery) {
		t.Fatal(err)
	}
	w = publishDelta(t, s, Delta{Remove: []Key{b.Key}})
	if s.Stats().RetainedInputs != 0 || s.Stats().InputEdges != 0 || s.Stats().AccountedBytes != 0 {
		t.Fatal(s.Stats())
	}
	if len(s.delta.refs) != 0 || len(s.delta.owned) != 0 {
		t.Fatal("ownership leaked")
	}
	_ = w
}

func TestSessionDeltaBudgetRemovalAndMode(t *testing.T) {
	limits := testLimits()
	limits.Queries = 1
	limits.Inputs = 1
	limits.InputEdges = 1
	s := smallSession(t, map[string]Evaluator{"read": sessionRead}, limits)
	a, b := readPublication("a", "A", false), readPublication("b", "B", false)
	w := publishDelta(t, s, Delta{Upsert: []Publication{a}})
	baseline := s.Stats()
	if _, err := s.PublishDelta(context.Background(), Delta{Upsert: []Publication{b}}); err == nil {
		t.Fatal("overbudget accepted")
	}
	if s.Stats().AccountedBytes != baseline.AccountedBytes {
		t.Fatal("failure changed budgets")
	}
	w = publishDelta(t, s, Delta{Remove: []Key{a.Key}, Upsert: []Publication{b}})
	sessionValue(t, w.Revision, b.Key, "B")
	// A full publication retains its existing behavior and invalidates delta metadata.
	w = publishSession(t, s, a)
	if s.delta != nil {
		t.Fatal("full mode retained ownership")
	}
	if _, err := s.PublishDelta(context.Background(), Delta{}); err == nil {
		t.Fatal("implicit full-to-delta conversion")
	}
	w = publishDelta(t, s, Delta{Reset: true, Upsert: []Publication{b}})
	sessionValue(t, w.Revision, b.Key, "B")
	// Oversized replacement fails atomically; empty delta can explicitly recover
	// the retained world, but cannot restore an old handle.
	b.Inputs[0].Value = strings.Repeat("x", limits.Bytes)
	if _, err := s.PublishDelta(context.Background(), Delta{Upsert: []Publication{b}}); err == nil {
		t.Fatal("oversized value accepted")
	}
	w = publishDelta(t, s, Delta{})
	sessionValue(t, w.Revision, b.Key, "B")
}

func TestSessionDeltaHistoryMatchesFullPublication(t *testing.T) {
	eval := map[string]Evaluator{"read": sessionRead}
	delta := smallSession(t, eval, DefaultLimits())
	full := smallSession(t, eval, DefaultLimits())
	inventory := map[Key]Publication{}
	random := rand.New(rand.NewSource(71))
	for revision := 0; revision < 200; revision++ {
		name := fmt.Sprint(random.Intn(24))
		p := readPublication(name, fmt.Sprint(random.Intn(4)), random.Intn(4) == 0)
		d := Delta{}
		if random.Intn(4) == 0 {
			d.Remove = []Key{p.Key}
			delete(inventory, p.Key)
		} else {
			d.Upsert = []Publication{p}
			inventory[p.Key] = p
		}
		dw := publishDelta(t, delta, d)
		var all []Publication
		for _, p := range inventory {
			all = append(all, p)
		}
		fw := publishSession(t, full, all...)
		for _, p := range inventory {
			want, err := fw.Revision.Evaluate(context.Background(), p.Key)
			if err != nil {
				t.Fatal(err)
			}
			sessionValue(t, dw.Revision, p.Key, want)
		}
		ds, fs := delta.Stats(), full.Stats()
		if ds.RetainedQueries != fs.RetainedQueries || ds.RetainedInputs != fs.RetainedInputs || ds.InputEdges != fs.InputEdges {
			t.Fatalf("revision %d inventories differ: %+v %+v", revision, ds, fs)
		}
	}
}

func TestSessionDeltaCancellationAndNegativeAuthority(t *testing.T) {
	s := smallSession(t, map[string]Evaluator{"read": sessionRead}, testLimits())
	p := readPublication("x", "ignored", true)
	w := publishDelta(t, s, Delta{Upsert: []Publication{p}})
	sessionValue(t, w.Revision, p.Key, "missing")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.PublishDelta(ctx, Delta{Remove: []Key{p.Key}}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	sessionValue(t, w.Revision, p.Key, "missing") // pre-gate cancellation preserves handle
	p.Inputs = nil
	w = publishDelta(t, s, Delta{Upsert: []Publication{p}})
	if _, err := w.Revision.Evaluate(context.Background(), p.Key); !errors.Is(err, ErrUnpublishedInput) {
		t.Fatal("retired negative authority reused", err)
	}
}

func TestSessionDeltaReservationSwapAndCompaction(t *testing.T) {
	s := smallSession(t, map[string]Evaluator{"read": sessionRead}, testLimits())
	a, b := readPublication("a", strings.Repeat("v", 1024), false), readPublication("b", "B", false)
	publishDelta(t, s, Delta{Upsert: []Publication{a, b}})
	// Trade input payload for a result reservation in the same owner. Checking
	// additions before subtracting old input storage would reject this exact fit.
	s.limits.Bytes = s.bytes
	a.Inputs[0].Value = "v"
	a.MaxResultBytes += 1023
	w := publishDelta(t, s, Delta{Upsert: []Publication{a}})
	sessionValue(t, w.Revision, a.Key, "v")
	oldLive, oldInputs, oldQK, oldIK := s.live, s.inputs, s.queryKeys, s.inputKeys
	oldOwned, oldRefs := s.delta.owned, s.delta.refs
	inner := oldRefs[a.Inputs[0].Key]
	s.debtBytes = s.limits.Bytes
	w = publishDelta(t, s, Delta{Remove: []Key{b.Key}})
	if s.Stats().Compactions != 1 || s.Stats().RetirementBytes != 0 {
		t.Fatal(s.Stats())
	}
	sentinel := Key{"sentinel", "unowned"}
	oldLive[sentinel] = sessionQuery{}
	oldInputs[sentinel] = Change{}
	oldQK[sentinel] = sentinel
	oldIK[sentinel] = sentinel
	oldOwned[sentinel] = nil
	oldRefs[sentinel] = nil
	inner[sentinel] = struct{}{}
	if _, ok := s.live[sentinel]; ok {
		t.Fatal("live map capacity retained")
	}
	if _, ok := s.inputs[sentinel]; ok {
		t.Fatal("input map capacity retained")
	}
	if _, ok := s.queryKeys[sentinel]; ok {
		t.Fatal("query authority map retained")
	}
	if _, ok := s.inputKeys[sentinel]; ok {
		t.Fatal("input authority map retained")
	}
	if _, ok := s.delta.owned[sentinel]; ok {
		t.Fatal("owner map retained")
	}
	if _, ok := s.delta.refs[sentinel]; ok {
		t.Fatal("reverse map retained")
	}
	if _, ok := s.delta.refs[a.Inputs[0].Key][sentinel]; ok {
		t.Fatal("shared assertion map retained")
	}
	sessionValue(t, w.Revision, a.Key, "v")
}

func TestSessionDeltaPreparedValuesAreOwned(t *testing.T) {
	s := smallSession(t, map[string]Evaluator{"read": sessionRead}, testLimits())
	p := readPublication("x", "original", false)
	w := publishDelta(t, s, Delta{Upsert: []Publication{p, p}})
	p.Inputs[0] = Change{Key: Key{"input", "other"}, Value: "replacement"}
	sessionValue(t, w.Revision, p.Key, "original")
	if _, err := s.PublishDelta(context.Background(), Delta{Remove: []Key{p.Key}, Upsert: []Publication{p}}); err == nil {
		t.Fatal("remove/upsert ambiguity accepted")
	}
	if _, err := w.Revision.Evaluate(context.Background(), p.Key); !errors.Is(err, ErrStaleRevision) {
		t.Fatal(err)
	}
}

func TestSessionDeltaSharedBatchHistory(t *testing.T) {
	shared := Key{"shared", "root"}
	eval := map[string]Evaluator{"read": func(r *Reader, name string) (string, error) {
		own, ok := r.Input(Key{"input", name})
		root, present := r.Input(shared)
		return fmt.Sprintf("%t:%s/%t:%s", ok, own, present, root), nil
	}}
	delta, full := smallSession(t, eval, DefaultLimits()), smallSession(t, eval, DefaultLimits())
	inventory := map[Key]Publication{}
	random := rand.New(rand.NewSource(101))
	root := Change{Key: shared, Value: "start"}
	for revision := 0; revision < 150; revision++ {
		patch := Delta{}
		p := readPublication(fmt.Sprint(random.Intn(16)), fmt.Sprint(revision), false)
		p.MaxDependencies = 2
		if random.Intn(3) == 0 {
			patch.Remove = []Key{p.Key}
			delete(inventory, p.Key)
		} else {
			p.Inputs = append(p.Inputs, root)
			inventory[p.Key] = p
			patch.Upsert = []Publication{p}
		}
		if random.Intn(3) == 0 {
			root = Change{Key: shared, Value: fmt.Sprint(revision), Remove: random.Intn(4) == 0}
			patch.Upsert = nil
			for key, p := range inventory {
				// Copy caller arrays to keep the reference world independent of mutation.
				p.Inputs = append([]Change(nil), p.Inputs...)
				p.Inputs[1] = root
				inventory[key] = p
				patch.Upsert = append(patch.Upsert, p)
			}
		}
		dw := publishDelta(t, delta, patch)
		var all []Publication
		for _, p := range inventory {
			all = append(all, p)
		}
		fw := publishSession(t, full, all...)
		for key := range inventory {
			want, err := fw.Revision.Evaluate(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
			sessionValue(t, dw.Revision, key, want)
		}
		edges := 0
		for key, owned := range delta.delta.owned {
			edges += len(owned)
			if _, ok := inventory[key]; !ok {
				t.Fatal("stale owner", key)
			}
			for _, input := range owned {
				if _, ok := delta.delta.refs[input][key]; !ok {
					t.Fatal("missing reverse ownership")
				}
			}
		}
		if edges != delta.Stats().InputEdges || len(inventory) != len(delta.live) {
			t.Fatal("ownership accounting diverged")
		}
	}
}

func TestSessionDeltaReservationOnlyChangeEvictsMemo(t *testing.T) {
	s := smallSession(t, map[string]Evaluator{
		"read":   sessionRead,
		"parent": func(r *Reader, _ string) (string, error) { return r.Query(Key{"read", "x"}) },
	}, testLimits())
	leaf := readPublication("x", "payload", false)
	parent := Publication{Key: Key{"parent", "root"}, MaxResultBytes: 64, MaxDependencies: 1}
	w := publishDelta(t, s, Delta{Upsert: []Publication{leaf, parent}})
	sessionValue(t, w.Revision, parent.Key, "payload")
	before := s.db.revision
	leaf.MaxResultBytes = 1
	w = publishDelta(t, s, Delta{Upsert: []Publication{leaf}})
	if s.db.revision != before+1 {
		t.Fatal("reservation eviction did not advance once")
	}
	if _, err := w.Revision.Evaluate(context.Background(), parent.Key); !errors.Is(err, ErrResultBudget) {
		t.Fatal("parent reused old result budget", err)
	}
	leaf.MaxResultBytes = 64
	leaf.MaxDependencies = 0
	w = publishDelta(t, s, Delta{Upsert: []Publication{leaf}})
	if _, err := w.Revision.Evaluate(context.Background(), parent.Key); !errors.Is(err, ErrDependencyBudget) {
		t.Fatal("parent reused old dependency budget", err)
	}
	leaf.MaxDependencies = 1
	w = publishDelta(t, s, Delta{Upsert: []Publication{leaf}})
	sessionValue(t, w.Revision, parent.Key, "payload")
}

func TestSessionDeltaPreparedInvalidatesBeforeConstruction(t *testing.T) {
	s := smallSession(t, map[string]Evaluator{"read": sessionRead}, testLimits())
	p := readPublication("x", "original", false)
	w := publishDelta(t, s, Delta{Upsert: []Publication{p}})
	failed := errors.New("unsupported current frame")
	before := s.db.revision
	called := false
	_, err := s.PublishDeltaPrepared(context.Background(), func() (Delta, error) {
		called = true
		if s.ready || s.epoch == w.Revision.epoch {
			t.Fatal("preparation started before old generation invalidated")
		}
		return Delta{}, failed
	})
	if !called || !errors.Is(err, failed) {
		t.Fatal(err)
	}
	if s.db.revision != before || s.inputs[p.Inputs[0].Key].Value != "original" {
		t.Fatal("failed preparation mutated retained data")
	}
	if _, err := w.Revision.Evaluate(context.Background(), p.Key); !errors.Is(err, ErrStaleRevision) {
		t.Fatal("old environment survived failed preparation", err)
	}
	w = publishDelta(t, s, Delta{})
	sessionValue(t, w.Revision, p.Key, "original")
	if _, err := s.PublishDeltaPrepared(context.Background(), nil); err == nil {
		t.Fatal("nil preparation accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err = s.PublishDeltaPrepared(ctx, func() (Delta, error) { cancel(); return Delta{Remove: []Key{p.Key}}, nil })
	if !errors.Is(err, context.Canceled) || s.Stats().Ready || len(s.live) != 1 {
		t.Fatal("post-preparation cancellation committed", err, s.Stats())
	}
}

func TestSessionDeltaBootstrapsEmptyFullWorld(t *testing.T) {
	s := smallSession(t, map[string]Evaluator{"read": sessionRead}, testLimits())
	old := publishSession(t, s)
	p := readPublication("x", "current", false)
	w := publishDelta(t, s, Delta{Upsert: []Publication{p}})
	sessionValue(t, w.Revision, p.Key, "current")
	if _, err := old.Revision.Evaluate(context.Background(), p.Key); !errors.Is(err, ErrStaleRevision) {
		t.Fatal(err)
	}
}
