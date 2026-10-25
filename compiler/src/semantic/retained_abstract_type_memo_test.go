package semantic

import "testing"

// These controls mutate a nominal type directly. They establish the abstract
// memo dependency and invalidator behavior; they do not claim that a new
// reference-field addition is admitted by RetainedAnalysis.Update.
func TestRetainedTypeMemoClearRefreshesAbstractParameterLayout(t *testing.T) {
	a := &Analyzer{typeShapesFrozen: true}
	typ := &StructType{Name: "Layout", Fields: map[string]Field{"old": {Name: "old", Type: &SViewType{}}}}
	initial, ok := a.abstractParamRegionRefState(typ, 0, nil)
	if !ok || len(initial.Fields) != 1 {
		t.Fatal("initial abstract layout missing source field")
	}
	key := abstractParamRegionRefKey{typeOf: typ, paramIndex: 0}
	if _, present := a.abstractParamRegionRefMemo[key]; !present {
		t.Fatal("initial layout was not memoized")
	}
	typ.Fields["added"] = Field{Name: "added", Type: &SViewType{}}
	fresh := &Analyzer{typeShapesFrozen: true}
	ground, ok := fresh.abstractParamRegionRefState(typ, 0, nil)
	if !ok || len(ground.Fields) != 2 {
		t.Fatal("fresh layout did not observe added field")
	}
	a.clearRetainedTypeMemos()
	updated, ok := a.abstractParamRegionRefStateForBinding(typ, 0)
	if !ok || len(updated.Fields) != len(ground.Fields) {
		t.Fatal("memoized abstract layout survived field addition")
	}
	for _, name := range []string{"old", "added"} {
		if _, present := updated.Fields[name]; !present {
			t.Fatalf("updated abstract layout omitted %s", name)
		}
	}
	delete(typ.Fields, "old")
	a.clearRetainedTypeMemos()
	updated, ok = a.abstractParamRegionRefStateForBinding(typ, 0)
	if !ok || len(updated.Fields) != 1 {
		t.Fatal("memoized abstract layout survived field removal")
	}
	if _, present := updated.Fields["old"]; present {
		t.Fatal("removed field survived abstract-layout invalidation")
	}
	if _, present := updated.Fields["added"]; !present {
		t.Fatal("surviving field lost during abstract-layout invalidation")
	}
}

func TestRetainedTypeMemoClearRefreshesAbstractReferenceRestart(t *testing.T) {
	a := &Analyzer{typeShapesFrozen: true}
	typ := &StructType{Name: "Restart", Fields: map[string]Field{"text": {Name: "text", Type: &SViewType{}}}}
	blocked := "Restart&"
	if !a.abstractRefElementCanRestart(typ, blocked) {
		t.Fatal("reference-free initial layout must permit restart")
	}
	key := abstractRefRestartKey{element: typ, blocked: blocked}
	if verdict, present := a.abstractRefRestartMemo[key]; !present || !verdict {
		t.Fatal("initial positive restart answer was not memoized")
	}
	typ.Fields["borrow"] = Field{Name: "borrow", Type: &RefType{Elem: &BuiltinType{Name: "u8"}, State: RefStateNonNull}}
	fresh := &Analyzer{typeShapesFrozen: true}
	if fresh.abstractRefElementCanRestart(typ, blocked) {
		t.Fatal("fresh nested-reference layout incorrectly permits restart")
	}
	a.clearRetainedTypeMemos()
	if a.abstractRefElementCanRestart(typ, blocked) {
		t.Fatal("positive restart memo survived nested-reference addition")
	}
	delete(typ.Fields, "borrow")
	a.clearRetainedTypeMemos()
	if !a.abstractRefElementCanRestart(typ, blocked) {
		t.Fatal("negative restart memo survived nested-reference removal")
	}
}
