package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"reflect"
)

// Only live analyzer frames are kept here. Map identity is checked within this
// invocation, never serialized or used as a query key. Recording at the actual
// push prevents a request from relabelling an unrelated live source node as owner.
// Uninstrumented/direct stack pushes and ownerless synthesized frames decline.
type lexicalBinderFrame struct {
	owner    ast.Node
	bindings any
}
type lexicalBinderOwnership struct {
	frames           map[string][]lexicalBinderFrame
	owned, ownerless uint64
}

func (o *lexicalBinderOwnership) push(domain string, owner ast.Node, bindings any) {
	if o == nil {
		return
	}
	if owner == nil {
		o.ownerless++
	} else {
		o.owned++
	}
	if o.frames == nil {
		o.frames = map[string][]lexicalBinderFrame{}
	}
	o.frames[domain] = append(o.frames[domain], lexicalBinderFrame{owner, bindings})
}
func (o *lexicalBinderOwnership) pop(domain string) {
	if o == nil {
		return
	}
	frames := o.frames[domain]
	if len(frames) == 0 {
		panic("unbalanced lexical binder ownership")
	}
	frames[len(frames)-1] = lexicalBinderFrame{}
	o.frames[domain] = frames[:len(frames)-1]
}
func (o *lexicalBinderOwnership) validate(domain string, sources []lexicalBinderSource, bindings []any) error {
	if o == nil {
		if len(bindings) == 0 && len(sources) == 0 {
			return nil
		}
		return fmt.Errorf("binder ownership not recorded")
	}
	frames := o.frames[domain]
	if len(frames) != len(sources) || len(frames) != len(bindings) {
		return fmt.Errorf("untracked binder frame")
	}
	for i, frame := range frames {
		if frame.owner == nil || frame.owner != sources[i].Owner || sources[i].Role != domain {
			return fmt.Errorf("binder source does not own live frame")
		}
		// reflect documents UnsafePointer for Map. Retaining the map in frame keeps
		// its allocation alive; this identity is used only during current preparation.
		left, right := reflect.ValueOf(frame.bindings), reflect.ValueOf(bindings[i])
		if left.Kind() != reflect.Map || right.Kind() != reflect.Map || left.Type() != right.Type() || left.UnsafePointer() != right.UnsafePointer() {
			return fmt.Errorf("binder map replaced outside owned scope")
		}
	}
	return nil
}
