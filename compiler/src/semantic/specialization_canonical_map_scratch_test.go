//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

type canonicalScratchRecord struct {
	node    ast.Node
	dynamic any
}

func TestCanonicalMembershipMapScratchReferenceParity(t *testing.T) {
	leaf := &ast.IntLit{Value: "3"}
	other := &ast.IntLit{Value: "4"}
	scope := &Scope{}
	symbol := &Symbol{Node: leaf, Type: &BuiltinType{Name: "i64"}}
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	cyclic["symbol"] = symbol
	cyclic["scope"] = scope
	cyclic["leaf"] = leaf
	values := []any{
		map[ast.Node]any{leaf: other, other: (*ast.IntLit)(nil)},
		map[string]canonicalScratchRecord{"one": {leaf, symbol}, "zero": {}},
		map[string]any{"one": cyclic, "nil": nil, "typednil": (*ast.IntLit)(nil)},
		map[*Symbol]*Scope{symbol: scope, nil: nil},
		map[int]any{}, map[int]any{0: leaf},
	}
	current, reference := newCanonicalMembershipIndexTestScan(), newCanonicalMembershipTestScan()
	for _, value := range values {
		current.walk(reflect.ValueOf(value), false)
		reference.walkCanonicalReference(reflect.ValueOf(value), false)
	}
	assertCanonicalMembershipParity(t, current, reference)
	if !current.owned[leaf] || !current.owned[other] || !current.rootSymbols[symbol] || !current.rootScopes[scope] {
		t.Fatal("underlying identity lost")
	}
	records := values[1].(map[string]canonicalScratchRecord)
	if records["one"].node != leaf || records["one"].dynamic != symbol || records["zero"].node != nil {
		t.Fatal("source map changed")
	}
}

func BenchmarkCanonicalMembershipMapScratch(b *testing.B) {
	nodes := make(map[ast.Node]any, 32768)
	for i := 0; i < 32768; i++ {
		node := &ast.IntLit{Value: "3"}
		nodes[node] = node
	}
	for _, reference := range []bool{false, true} {
		name := "candidate"
		if reference {
			name = "frozen_reference"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var scan *specializationPublication
				if reference {
					scan = newCanonicalMembershipTestScan()
					scan.walkCanonicalReference(reflect.ValueOf(nodes), false)
				} else {
					scan = newCanonicalMembershipIndexTestScan()
					scan.walk(reflect.ValueOf(nodes), false)
				}
				canonicalMembershipBenchmarkCatalog = scan
			}
		})
	}
}
