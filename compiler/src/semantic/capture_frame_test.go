//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// docs/119 §6.4 obligation 2: a value block with an empty capture set is provably pure
// over outer state (E4 rejects all outer writes; no capture licenses one), so provers
// may treat outer facts as surviving it. exprBlockPureOverOuter is the queryable form.
func TestExprBlockPureOverOuter(t *testing.T) {
	pure := &ast.ExprBlock{Value: &ast.IntLit{Value: "0"}}
	if !exprBlockPureOverOuter(pure) {
		t.Fatal("an empty-capture value block must be pure over outer state")
	}
	captured := &ast.ExprBlock{Value: &ast.IntLit{Value: "0"}, Captures: []string{"world"}}
	if exprBlockPureOverOuter(captured) {
		t.Fatal("a capturing value block is NOT pure over outer state")
	}
	if exprBlockPureOverOuter(nil) {
		t.Fatal("nil is not a pure block")
	}
}

// docs/119 §6.4: a capture list is a local frame. A captured `mutable T&` place mutated
// inside a value block is a write to that place, so under a declared `changes` clause it
// must lie within the frame — enforced by the existing frame machinery (the value block's
// statements are analyzed normally, so the write reaches checkFramePlace).

func TestCaptureMutationOutsideChangesIsFrameError(t *testing.T) {
	requireOneErrorContaining(t, `
struct World:
    tick: mutable i64
    dirty: mutable i64

def advance(w: mutable World&, xs: darray[i64]) changes w.tick:
    r: i64 =
        for x in xs |acc = 0, w| -> acc:
            w.dirty <- w.dirty + x
            acc <- acc + 1
    w.tick <- w.tick + r
`, `outside the `+"`changes`"+` set`)
}

func TestCaptureMutationInsideChangesIsClean(t *testing.T) {
	errs := semanticErrorsFor(t, `
struct World:
    tick: mutable i64
    dirty: mutable i64

def advance(w: mutable World&, xs: darray[i64]) changes w.tick, w.dirty:
    r: i64 =
        for x in xs |acc = 0, w| -> acc:
            w.dirty <- w.dirty + x
            acc <- acc + 1
    w.tick <- w.tick + r
`)
	for _, e := range errs {
		if strings.Contains(e, "changes") || strings.Contains(e, "frame") {
			t.Fatalf("a capture write inside the changes set must be clean, got: %v", errs)
		}
	}
}
