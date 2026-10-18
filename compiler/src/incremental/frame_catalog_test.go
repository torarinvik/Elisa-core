package incremental

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func catalogForTest(t *testing.T, frames []CatalogFrame) *FrameCatalog {
	t.Helper()
	c, err := NewFrameCatalog(frames, DefaultFrameCatalogLimits())
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func catalogPublish(t *testing.T, s *Session, frames []CatalogFrame, lookups ...FrameLookup) Published {
	t.Helper()
	w, err := s.PublishDeltaPrepared(context.Background(), func() (Delta, error) {
		c, err := NewFrameCatalog(frames, DefaultFrameCatalogLimits())
		if err != nil {
			return Delta{}, err
		}
		d := Delta{}
		for _, lookup := range lookups {
			p, err := c.PrepareLookup(lookup)
			if err != nil {
				return Delta{}, err
			}
			d.Upsert = append(d.Upsert, p.Cells...)
			d.Upsert = append(d.Upsert, p.Query)
		}
		return d, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return w
}
func catalogSelection(t *testing.T, w Published, lookup FrameLookup) FrameSelection {
	t.Helper()
	key, err := FrameLookupKey(lookup)
	if err != nil {
		t.Fatal(err)
	}
	value, err := w.Revision.Evaluate(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	result, err := DecodeFrameSelection(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestFrameCatalogOwnershipAndValidation(t *testing.T) {
	frames := []CatalogFrame{{ID: "module/root", Names: map[string]CatalogBinding{"x": {Binding: "decl/x"}}}, {ID: "module/local", Parent: "module/root", Names: map[string]CatalogBinding{}}}
	c := catalogForTest(t, frames)
	p, err := c.PrepareLookup(FrameLookup{"module/local", "x"})
	if err != nil {
		t.Fatal(err)
	}
	frames[0].Names["x"] = CatalogBinding{Binding: "changed"}
	frames[1].Parent = "missing"
	again, err := c.PrepareLookup(FrameLookup{"module/local", "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p, again) {
		t.Fatal("caller maps retained")
	}
	tests := []struct {
		name   string
		frames []CatalogFrame
		limits FrameCatalogLimits
	}{
		{"duplicate", []CatalogFrame{{ID: "a"}, {ID: "a"}}, DefaultFrameCatalogLimits()},
		{"missing parent", []CatalogFrame{{ID: "a", Parent: "missing"}}, DefaultFrameCatalogLimits()},
		{"cycle", []CatalogFrame{{ID: "a", Parent: "b"}, {ID: "b", Parent: "a"}}, DefaultFrameCatalogLimits()},
		{"nil identity", []CatalogFrame{{ID: "a", Names: map[string]CatalogBinding{"x": {Nil: true, Binding: "not-nil"}}}}, DefaultFrameCatalogLimits()},
		{"empty identity", []CatalogFrame{{ID: "a", Names: map[string]CatalogBinding{"x": {}}}}, DefaultFrameCatalogLimits()},
		{"invalid utf8", []CatalogFrame{{ID: string([]byte{255})}}, DefaultFrameCatalogLimits()},
		{"frame limit", []CatalogFrame{{ID: "a"}}, FrameCatalogLimits{0, 10, 4096, 10}},
		{"binding limit", []CatalogFrame{{ID: "a", Names: map[string]CatalogBinding{"x": {Binding: "x"}}}}, FrameCatalogLimits{10, 0, 4096, 10}},
		{"byte limit", []CatalogFrame{{ID: "a"}}, FrameCatalogLimits{10, 10, 1, 10}},
		{"depth limit", []CatalogFrame{{ID: "a", Parent: "b"}, {ID: "b"}}, FrameCatalogLimits{10, 10, 4096, 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewFrameCatalog(test.frames, test.limits); err == nil {
				t.Fatal("invalid catalog admitted")
			}
		})
	}
	var zero FrameCatalog
	if _, err := zero.PrepareLookup(FrameLookup{"a", "x"}); err == nil {
		t.Fatal("zero catalog certified absence")
	}
	if _, err := c.PrepareLookup(FrameLookup{"missing", "x"}); err == nil {
		t.Fatal("unknown frame certified absence")
	}
}

func TestFrameCatalogTracksShadowingParentAndNegativeCells(t *testing.T) {
	s := smallSession(t, FrameCatalogEvaluators(), DefaultLimits())
	frames := []CatalogFrame{
		{ID: "module/root", Names: map[string]CatalogBinding{"x": {Binding: "global/x"}}},
		{ID: "module/other", Names: map[string]CatalogBinding{"x": {Binding: "other/x"}}},
		{ID: "module/local", Parent: "module/root", Names: map[string]CatalogBinding{}},
	}
	lookup := FrameLookup{"module/local", "x"}
	missing := FrameLookup{"module/local", "absent"}
	w := catalogPublish(t, s, frames, lookup, missing)
	if got := catalogSelection(t, w, lookup); got != (FrameSelection{"module/root", "global/x", true, false}) {
		t.Fatal(got)
	}
	if got := catalogSelection(t, w, missing); got != (FrameSelection{}) {
		t.Fatal(got)
	}
	key, _ := FrameLookupKey(lookup)
	deps := s.db.results[key].dependencies
	want := map[Key]bool{
		{frameCatalogNameKind, catalogName("module/local", "x")}: true,
		{frameCatalogParentKind, "module/local"}:                 true,
		{frameCatalogNameKind, catalogName("module/root", "x")}:  true,
	}
	if len(deps) != len(want) {
		t.Fatal("unexpected dependency count", deps)
	}
	for _, dep := range deps {
		if dep.query || !want[dep.key] {
			t.Fatal("negative/parent dependency missing", deps)
		}
		delete(want, dep.key)
	}
	frames[2].Names["x"] = CatalogBinding{Binding: "local/x"}
	w = catalogPublish(t, s, frames, lookup, missing)
	if got := catalogSelection(t, w, lookup); got.Binding != "local/x" {
		t.Fatal(got)
	}
	frames[2].Names["x"] = CatalogBinding{Nil: true}
	w = catalogPublish(t, s, frames, lookup, missing)
	if got := catalogSelection(t, w, lookup); !got.Present || !got.Nil || got.Binding != "" {
		t.Fatal("nil blocker ignored", got)
	}
	delete(frames[2].Names, "x")
	frames[2].Parent = "module/other"
	w = catalogPublish(t, s, frames, lookup, missing)
	if got := catalogSelection(t, w, lookup); got.Binding != "other/x" {
		t.Fatal("parent edge reused", got)
	}
	frames[1].Names["absent"] = CatalogBinding{Binding: "new/absent"}
	w = catalogPublish(t, s, frames, lookup, missing)
	if got := catalogSelection(t, w, missing); got.Binding != "new/absent" {
		t.Fatal("negative lookup reused", got)
	}
	// Unrelated names do not change selected-name inputs or reexecute the query.
	before := s.Stats().Queries.Executed
	frames[0].Names["unrelated"] = CatalogBinding{Binding: "another/decl"}
	w = catalogPublish(t, s, frames, lookup, missing)
	catalogSelection(t, w, lookup)
	if s.Stats().Queries.Executed != before {
		t.Fatal("unrelated catalog entry invalidated lookup")
	}
	// A broken current catalog must invalidate the old generation, not serve it.
	_, err := s.PublishDeltaPrepared(context.Background(), func() (Delta, error) {
		_, err := NewFrameCatalog([]CatalogFrame{{ID: "broken", Parent: "missing"}}, DefaultFrameCatalogLimits())
		return Delta{}, err
	})
	if err == nil {
		t.Fatal("incomplete catalog admitted")
	}
	if _, err := w.Revision.Evaluate(context.Background(), key); !errors.Is(err, ErrStaleRevision) {
		t.Fatal(err)
	}
}

func TestFrameCatalogRetainedHistoryMatchesOrdinaryScopeWalk(t *testing.T) {
	s := smallSession(t, FrameCatalogEvaluators(), DefaultLimits())
	frames := []CatalogFrame{
		{ID: "unit/root", Names: map[string]CatalogBinding{}},
		{ID: "unit/alternate", Names: map[string]CatalogBinding{}},
		{ID: "unit/local", Parent: "unit/root", Names: map[string]CatalogBinding{}},
	}
	random := rand.New(rand.NewSource(347))
	lookups := []FrameLookup{{"unit/local", "x"}, {"unit/local", "y"}}
	for revision := 0; revision < 200; revision++ {
		at := random.Intn(3)
		name := []string{"x", "y"}[random.Intn(2)]
		switch random.Intn(3) {
		case 0:
			delete(frames[at].Names, name)
		case 1:
			frames[at].Names[name] = CatalogBinding{Nil: true}
		case 2:
			frames[at].Names[name] = CatalogBinding{Binding: fmt.Sprintf("unit/decl/%d/%s/%d", at, name, revision)}
		}
		if random.Intn(5) == 0 {
			frames[2].Parent = []string{"unit/root", "unit/alternate", ""}[random.Intn(3)]
		}
		w := catalogPublish(t, s, frames, lookups...)
		for _, lookup := range lookups {
			want := FrameSelection{}
			current := lookup.Frame
			for current != "" {
				var frame CatalogFrame
				for _, candidate := range frames {
					if candidate.ID == current {
						frame = candidate
						break
					}
				}
				if cell, present := frame.Names[lookup.Name]; present {
					want = FrameSelection{current, cell.Binding, true, cell.Nil}
					break
				}
				current = frame.Parent
			}
			if got := catalogSelection(t, w, lookup); got != want {
				t.Fatalf("revision %d: got %+v want %+v", revision, got, want)
			}
		}
	}
}

func TestFrameCatalogKeysUseExactIdentity(t *testing.T) {
	a, _ := FrameLookupKey(FrameLookup{"module:a\x00b", "c"})
	b, _ := FrameLookupKey(FrameLookup{"module:a", "b\x00c"})
	if a == b {
		t.Fatal("delimiter identity collision")
	}
	frames := []CatalogFrame{{ID: "module:a\x00b", Names: map[string]CatalogBinding{"c": {Binding: "quoted\\\"slot"}}}}
	s := smallSession(t, FrameCatalogEvaluators(), DefaultLimits())
	w := catalogPublish(t, s, frames, FrameLookup{"module:a\x00b", "c"})
	if got := catalogSelection(t, w, FrameLookup{"module:a\x00b", "c"}); got.Binding != "quoted\\\"slot" {
		t.Fatal(got)
	}
}

func TestFrameCatalogEncodedBudgets(t *testing.T) {
	tooLong := strings.Repeat("x", frameCatalogIdentityBytes+1)
	if _, err := FrameLookupKey(FrameLookup{"unit/root", tooLong}); err == nil {
		t.Fatal("oversized lookup allocated/admitted")
	}
	if _, err := NewFrameCatalog([]CatalogFrame{{ID: tooLong}}, DefaultFrameCatalogLimits()); err == nil {
		t.Fatal("oversized frame admitted")
	}
	escaped := strings.Repeat("\x01", frameCatalogIdentityBytes)
	frames := []CatalogFrame{{ID: escaped, Names: map[string]CatalogBinding{escaped: {Binding: escaped}}}}
	c := catalogForTest(t, frames)
	p, err := c.PrepareLookup(FrameLookup{escaped, escaped})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Query.Key.Name) > frameCatalogEncodedBytes || p.Query.MaxResultBytes > frameCatalogEncodedBytes {
		t.Fatal("encoded budget escaped", len(p.Query.Key.Name), p.Query.MaxResultBytes)
	}
	s := smallSession(t, FrameCatalogEvaluators(), DefaultLimits())
	w := publishDelta(t, s, Delta{Upsert: append(p.Cells, p.Query)})
	if got := catalogSelection(t, w, FrameLookup{escaped, escaped}); got.Binding != escaped {
		t.Fatal("escaped identity changed")
	}
	// A long chain of maximally escaped identities must stop before growing an
	// unbounded intermediate publication, even though each identity is valid.
	frames = nil
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("%02d", i) + escaped[2:]
		parent := ""
		if i > 0 {
			parent = frames[i-1].ID
		}
		frames = append(frames, CatalogFrame{ID: id, Parent: parent})
	}
	c = catalogForTest(t, frames)
	if c.depth != len(frames) {
		t.Fatal("catalog did not retain measured depth", c.depth)
	}
	if _, err := c.PrepareLookup(FrameLookup{frames[len(frames)-1].ID, escaped}); err == nil {
		t.Fatal("oversized publication admitted")
	}
}

func TestFrameCatalogCellEditInvalidatesOmittedLookupOwners(t *testing.T) {
	s := smallSession(t, FrameCatalogEvaluators(), DefaultLimits())
	frames := []CatalogFrame{
		{ID: "unit/root", Names: map[string]CatalogBinding{"x": {Binding: "decl/a"}}},
		{ID: "unit/local", Parent: "unit/root", Names: map[string]CatalogBinding{}},
	}
	root, local := FrameLookup{"unit/root", "x"}, FrameLookup{"unit/local", "x"}
	c := catalogForTest(t, frames)
	d, err := c.LookupDelta([]FrameLookup{root, local}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	w := publishDelta(t, s, d)
	if catalogSelection(t, w, root).Binding != "decl/a" || catalogSelection(t, w, local).Binding != "decl/a" {
		t.Fatal("seed failed")
	}
	for _, lookup := range []FrameLookup{root, local} {
		key, _ := FrameLookupKey(lookup)
		if len(s.delta.owned[key]) != 0 {
			t.Fatal("lookup owns shared catalog cells")
		}
	}
	frames[0].Names["x"] = CatalogBinding{Binding: "decl/b"}
	c = catalogForTest(t, frames)
	cell, err := c.NamePublication("unit/root", "x")
	if err != nil {
		t.Fatal(err)
	}
	// Neither memoized lookup owner is republished. Exactly one cell owner changes.
	w = publishDelta(t, s, Delta{Upsert: []Publication{cell}})
	fresh := smallSession(t, FrameCatalogEvaluators(), DefaultLimits())
	fw := catalogPublish(t, fresh, frames, root, local)
	for _, lookup := range []FrameLookup{root, local} {
		if got, want := catalogSelection(t, w, lookup), catalogSelection(t, fw, lookup); got != want || got.Binding != "decl/b" {
			t.Fatal("retained lookup missed cell edit", got, want)
		}
	}
	// Retiring the cell's sole owner removes authority and invalidates the same
	// omitted lookup owners, even if a value was previously memoized.
	w = publishDelta(t, s, Delta{Remove: []Key{cell.Key}})
	key, _ := FrameLookupKey(local)
	if _, err := w.Revision.Evaluate(context.Background(), key); !errors.Is(err, ErrUnpublishedInput) {
		t.Fatal("retired cell authority survived", err)
	}
}

func TestFrameCatalogBatchBudgetsIncludeCellOwners(t *testing.T) {
	c := catalogForTest(t, []CatalogFrame{{ID: "unit/root", Names: map[string]CatalogBinding{"x": {Binding: "decl/x"}}}})
	lookups := []FrameLookup{{"unit/root", "x"}, {"unit/root", "x"}}
	limits := DefaultLimits()
	d, err := c.LookupDelta(lookups, limits)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Upsert) != 2 {
		t.Fatal("expected deduplicated query/name owners", len(d.Upsert))
	}
	limits.Queries = 1
	if _, err := c.LookupDelta(lookups[:1], limits); err == nil {
		t.Fatal("cell owners did not consume query slots")
	}
	limits = DefaultLimits()
	limits.Inputs = 0
	if _, err := c.LookupDelta(lookups, limits); err == nil {
		t.Fatal("cell input limit ignored")
	}
	limits = DefaultLimits()
	limits.Bytes = 1
	if _, err := c.LookupDelta(lookups, limits); err == nil {
		t.Fatal("preparation allocated beyond batch allowance")
	}
	limits = DefaultLimits()
	estimate, err := c.lookupPublicationBudget(lookups[0])
	if err != nil {
		t.Fatal(err)
	}
	limits.Bytes = estimate
	if _, err := c.LookupDelta(lookups, limits); err == nil {
		t.Fatal("duplicate requests bypassed preparation work cap")
	}
}

func TestFrameCatalogStopsAtSelectionAndBoundsManualEvaluator(t *testing.T) {
	c := catalogForTest(t, []CatalogFrame{{ID: "root", Names: map[string]CatalogBinding{"x": {Binding: "root/x"}}}, {ID: "local", Parent: "root", Names: map[string]CatalogBinding{"x": {Binding: "local/x"}}}})
	plan, err := c.PrepareLookup(FrameLookup{"local", "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Cells) != 1 || plan.Query.MaxDependencies != 1 {
		t.Fatal("hidden ancestor cells prepared", plan)
	}
	s := smallSession(t, FrameCatalogEvaluators(), DefaultLimits())
	lookup := FrameLookup{"manual/0", "x"}
	key, _ := FrameLookupKey(lookup)
	d := Delta{Upsert: []Publication{{Key: key, MaxResultBytes: 64, MaxDependencies: 2 * (frameCatalogMaxDepth + 1)}}}
	for i := 0; i <= frameCatalogMaxDepth; i++ {
		frame := fmt.Sprintf("manual/%d", i)
		parent := ""
		if i < frameCatalogMaxDepth {
			parent = fmt.Sprintf("manual/%d", i+1)
		}
		d.Upsert = append(d.Upsert, catalogCellOwner(Change{Key: Key{frameCatalogNameKind, catalogName(frame, "x")}, Remove: true}), catalogCellOwner(Change{Key: Key{frameCatalogParentKind, frame}, Value: parent}))
	}
	w := publishDelta(t, s, d)
	if _, err := w.Revision.Evaluate(context.Background(), key); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatal("manually published chain escaped evaluator depth cap", err)
	}
}
