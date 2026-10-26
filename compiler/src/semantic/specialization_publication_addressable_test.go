//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"reflect"
	"testing"
)

type publicationAddressableProbe struct {
	hidden   any
	Elements []any
	Mapping  map[string]any
	Next     *publicationAddressableProbe
}

func publicationAddressableScan(private bool) *specializationPublication {
	p := newCanonicalMembershipTestScan()
	p.spanNodes = map[*ast.IndexExpr]bool{}
	if private {
		p.context = &specializationContext{}
	}
	return p
}
func assertPublicationAddressableParity(t *testing.T, current, reference *specializationPublication) {
	t.Helper()
	if fmt.Sprint(current.err) != fmt.Sprint(reference.err) {
		t.Fatalf("error mismatch %v vs %v", current.err, reference.err)
	}
	if current.err == nil {
		assertCanonicalMembershipParity(t, current, reference)
	}
	if !reflect.DeepEqual(current.seen, reference.seen) {
		t.Fatal("exact pre-cut visited roles changed")
	}
	if !reflect.DeepEqual(current.owned, reference.owned) || !reflect.DeepEqual(current.rootSymbols, reference.rootSymbols) || !reflect.DeepEqual(current.rootScopes, reference.rootScopes) || !reflect.DeepEqual(current.spanNodes, reference.spanNodes) {
		t.Fatal("publication graph/role state changed")
	}
	queue := func(nodes []ast.Node) map[ast.Node]int {
		out := map[ast.Node]int{}
		for _, node := range nodes {
			out[node]++
		}
		return out
	}
	if current.context != nil {
		if !reflect.DeepEqual(queue(current.queue), queue(reference.queue)) {
			t.Fatal("private publication work queue membership changed")
		}
	} else if len(current.queue) != 0 {
		t.Fatalf("canonical membership scan retained a private publication queue of %d nodes", len(current.queue))
	}
}
func TestPublicationAddressableStructWalkParityAndReadOnly(t *testing.T) {
	index := &ast.IndexExpr{Index: &ast.IntLit{Value: "3"}}
	symbol := &Symbol{Node: index}
	scope := &Scope{}
	probe := &publicationAddressableProbe{hidden: index, Elements: []any{symbol, scope}, Mapping: map[string]any{"node": index}}
	probe.Next = probe
	hidden := reflect.ValueOf(probe).Elem().Field(0)
	if !hidden.CanAddr() || hidden.CanInterface() {
		t.Fatal("fixture lacks addressable unexported field")
	}
	values := []reflect.Value{reflect.ValueOf(probe), reflect.ValueOf(probe).Elem(), reflect.ValueOf(*probe), reflect.ValueOf([]publicationAddressableProbe{*probe}).Index(0), reflect.ValueOf(map[string]publicationAddressableProbe{"v": *probe}).MapIndex(reflect.ValueOf("v")), reflect.ValueOf(any(*probe)), settable(hidden)}
	for i, value := range values {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			current, reference := publicationAddressableScan(false), publicationAddressableScan(false)
			current.walk(value, true)
			reference.walkAddressableReference(value, true)
			assertPublicationAddressableParity(t, current, reference)
			if probe.Next != probe || probe.hidden != index || len(probe.Elements) != 2 || probe.Elements[0] != symbol || probe.Elements[1] != scope || len(probe.Mapping) != 1 || probe.Mapping["node"] != index || symbol.Node != index || index.Index.(*ast.IntLit).Value != "3" {
				t.Fatal("input graph mutated")
			}
		})
	}
}
func TestPublicationAddressablePrivateValidationAndErrors(t *testing.T) {
	index := &ast.IndexExpr{Index: &ast.IntLit{Value: "4"}}
	param := ast.ParamDecl{Name: "p", DefaultValue: index}
	values := []reflect.Value{reflect.ValueOf(&param), reflect.ValueOf(&param).Elem(), reflect.ValueOf(param), reflect.ValueOf([]ast.ParamDecl{param}).Index(0), reflect.ValueOf(map[int]ast.ParamDecl{0: param}).MapIndex(reflect.ValueOf(0)), reflect.ValueOf(publicationAddressableProbe{hidden: index}), reflect.ValueOf(&Analyzer{}), reflect.ValueOf(&Scope{})}
	for i, value := range values {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			current, reference := publicationAddressableScan(true), publicationAddressableScan(true)
			current.walk(value, true)
			reference.walkAddressableReference(value, true)
			assertPublicationAddressableParity(t, current, reference)
			if i >= 5 && current.err == nil {
				t.Fatal("unknown/resource/private scope was admitted")
			}
			if param.Name != "p" || param.DefaultValue != index || index.Index.(*ast.IntLit).Value != "4" {
				t.Fatal("private validation mutated input")
			}
		})
	}
}
func TestPublicationAddressableActualCanonicalGraphReadOnly(t *testing.T) {
	root, _ := privateSpecializationFixture(t)
	snapshot, err := ast.NewSyntaxSnapshot(root.File, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := snapshot.CopyFile()
	if err != nil {
		t.Fatal(err)
	}
	current := canonicalMembershipCatalogTestScan(root, false)
	reference := canonicalMembershipCatalogTestScan(root, true)
	assertCanonicalMembershipIndexesEqual(t, current, reference)
	after, err := ast.NewSyntaxSnapshot(root.File, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := after.CopyFile()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("actual canonical syntax mutated")
	}
}

var publicationAddressableSink *specializationPublication

func BenchmarkPublicationAddressableStructWalk(b *testing.B) {
	index := &ast.IndexExpr{Index: &ast.IntLit{Value: "3"}}
	input := make([]publicationAddressableProbe, 8192)
	for i := range input {
		input[i].hidden = index
	}
	for _, mode := range []string{"old_reference", "addressable"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				p := publicationAddressableScan(false)
				if mode == "old_reference" {
					p.walkAddressableReference(reflect.ValueOf(input), false)
				} else {
					p.walk(reflect.ValueOf(input), false)
				}
				publicationAddressableSink = p
			}
		})
	}
}

// Frozen immediate pre-cut walker, including the existing type-pruning policy.
func (p *specializationPublication) walkAddressableReference(v reflect.Value, span bool) {
	if !v.IsValid() || p.err != nil {
		return
	}
	if p.context == nil && !canonicalMembershipMayContainIdentity(v.Type()) {
		return
	}
	if p.context != nil {
		if err := validateSparseFactValueType(v); err != nil {
			p.err = err
			return
		}
	}
	if v.CanInterface() {
		switch value := v.Interface().(type) {
		case *Symbol:
			if value == nil {
				return
			}
			key := specializationPointer{v.Type(), reflect.ValueOf(value).Pointer()}
			if p.seen[key]&4 != 0 {
				return
			}
			p.seen[key] |= 4
			if p.context == nil {
				p.rootSymbols[value] = true
			} else if p.rootSymbols[value] {
				return
			}
			p.walkAddressableReference(reflect.ValueOf(value.Node), false)
			p.walkAddressableReference(reflect.ValueOf(value.Type), false)
			p.walkAddressableReference(reflect.ValueOf(value.AliasOf), false)
			return
		case *Scope:
			if value == nil {
				return
			}
			if p.context == nil {
				p.rootScopes[value] = true
				return
			}
			if !p.rootScopes[value] {
				p.err = fmt.Errorf("specialization fact retains a private semantic scope")
			}
			return
		case *StructType, *EnumType, *ConstEnumType, *OpaqueType, *BitGroupType, *BuiltinType, *BitIntType, *TypeParamType, *ErrorSetType:
			return
		case ast.Node:
			if reflect.ValueOf(value).Kind() == reflect.Pointer && reflect.ValueOf(value).IsNil() {
				return
			}
			if p.rootNodes != nil && p.rootNodes[value] {
				return
			}
			if decl, ok := value.(ast.Decl); ok && p.rootNodes != nil && decl != p.target {
				// An unrelated declaration is a canonical symbol reference;
				// it is not part of this function's published body closure.
				return
			}
			if !p.owned[value] {
				p.owned[value] = true
				p.queue = append(p.queue, value)
			}
			if index, ok := value.(*ast.IndexExpr); ok && span {
				p.spanNodes[index] = true
			}
		case Type:
			// Structural type wrappers can carry private default/contract AST.
			// They are kept, but those templates alone are not emitted accesses.
			span = false
		}
	}
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			p.walkAddressableReference(v.Elem(), span)
		}
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		key := specializationPointer{v.Type(), v.Pointer()}
		role := uint8(1)
		if span {
			role = 2
		}
		if p.seen[key]&role != 0 {
			return
		}
		p.seen[key] |= role
		p.walkAddressableReference(settable(v.Elem()), span)
	case reflect.Struct:
		addressable := reflect.New(v.Type()).Elem()
		addressable.Set(v)
		for i := 0; i < v.NumField(); i++ {
			p.walkAddressableReference(settable(addressable.Field(i)), span)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			p.walkAddressableReference(v.Index(i), span)
		}
	case reflect.Map:
		if v.IsNil() {
			return
		}
		key := specializationPointer{v.Type(), v.Pointer()}
		role := uint8(1)
		if span {
			role = 2
		}
		if p.seen[key]&role != 0 {
			return
		}
		p.seen[key] |= role
		it := v.MapRange()
		for it.Next() {
			p.walkAddressableReference(it.Key(), span)
			p.walkAddressableReference(it.Value(), span)
		}
	}
}
