//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

type canonicalDispatchCustomType struct{ Node ast.Node }

func (canonicalDispatchCustomType) String() string { panic("identity dispatch must not call methods") }
func (canonicalDispatchCustomType) isType()        {}

type canonicalDispatchPlain struct {
	Node   ast.Node
	Value  any
	hidden any
}

func TestCanonicalDispatchTypePolicyAndFullOracle(t *testing.T) {
	leaf := &ast.IntLit{Value: "9"}
	scope := &Scope{}
	symbol := &Symbol{Node: leaf}
	custom := canonicalDispatchCustomType{leaf}
	for _, v := range []any{custom, &custom, leaf, symbol, scope} {
		if !canonicalMembershipPolicyFor(reflect.TypeOf(v)).dispatch {
			t.Fatal("lost identity implementation", reflect.TypeOf(v))
		}
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[Type](), reflect.TypeFor[ast.Node](), reflect.TypeFor[any]()} {
		if !canonicalMembershipPolicyFor(typ).dispatch {
			t.Fatal("open interface classified closed", typ)
		}
	}
	plain := canonicalDispatchPlain{leaf, custom, symbol}
	for _, v := range []any{plain, &plain, []any{custom, symbol}, map[string]any{"leaf": leaf, "scope": scope}} {
		if canonicalMembershipPolicyFor(reflect.TypeOf(v)).dispatch {
			t.Fatal("plain concrete container unnecessarily dispatches", reflect.TypeOf(v))
		}
		current, reference := newCanonicalMembershipTestScan(), newCanonicalMembershipTestScan()
		current.walk(reflect.ValueOf(v), true)
		reference.walkCanonicalReference(reflect.ValueOf(v), true)
		assertCanonicalMembershipParity(t, current, reference)
	}
	current, reference := newCanonicalMembershipTestScan(), newCanonicalMembershipTestScan()
	for _, v := range []any{custom, &custom, (*canonicalDispatchCustomType)(nil), (*ast.IntLit)(nil), plain, &plain} {
		current.walk(reflect.ValueOf(v), true)
		reference.walkCanonicalReference(reflect.ValueOf(v), true)
	}
	assertCanonicalMembershipParity(t, current, reference)
	if plain.Node != leaf || plain.Value != custom || plain.hidden != symbol {
		t.Fatal("input changed")
	}
}
