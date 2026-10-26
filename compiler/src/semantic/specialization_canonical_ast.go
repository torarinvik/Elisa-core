package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// A generated AST walk delegates unknown interfaces, maps, and inaccessible
// fields back through this marker. The original membership walker therefore
// keeps its complete semantic metadata policy and map/pointer identity guards.
func canonicalASTWalkOpaque(value reflect.Value, visit func(any) bool, _ map[uintptr]bool) {
	if !value.IsValid() {
		return
	}
	switch value.Kind() {
	case reflect.Map, reflect.Slice, reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			return
		}
	}
	// Avoid boxing a reflect.Value for absent optional AST fields. Keep
	// nonnil empty maps: the complete walk still records their map identity.
	visit(value)
}

// walkCanonicalAST accelerates only the AST portion of the canonical catalog.
// It neither changes the catalog's roots nor infers ownership from a fact-map
// key. Unknown AST implementations retain the original reflective walk.
func (p *specializationPublication) walkCanonicalAST(node ast.Node) bool {
	return canonicalASTWalkTyped(node, func(value any) bool {
		if opaque, ok := value.(reflect.Value); ok {
			p.walk(opaque, false)
			return false
		}
		if child, ok := value.(ast.Node); ok {
			if p.owned[child] {
				return false
			}
			p.owned[child] = true
			return true
		}
		// Generated helpers also visit pointers to AST field records and lexer
		// positions. Keep the same pruning and pointer guard as reflection.
		v := reflect.ValueOf(value)
		if !v.IsValid() || !canonicalMembershipMayContainIdentity(v.Type()) {
			return false
		}
		if v.Kind() == reflect.Pointer {
			key := specializationPointer{v.Type(), v.Pointer()}
			if p.seen[key]&1 != 0 {
				return false
			}
			p.seen[key] |= 1
		}
		return true
	}, nil)
}
