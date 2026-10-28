package statecache

import (
	"reflect"
	"testing"
	"unsafe"
)

type descriptorAliasNode struct{ Value int }
type descriptorAliasRoot struct {
	Nodes    []descriptorAliasNode
	Pointers []*descriptorAliasNode
}

func TestPrototypeDescriptorAliasIndexPreservesManyInteriorPointers(t *testing.T) {
	const count = 10_000
	root := &descriptorAliasRoot{
		Nodes:    make([]descriptorAliasNode, count),
		Pointers: make([]*descriptorAliasNode, count),
	}
	for i := range root.Nodes {
		root.Nodes[i].Value = i
		root.Pointers[i] = &root.Nodes[i]
	}
	r := New([]reflect.Type{reflect.TypeFor[descriptorAliasRoot]()}, nil, nil)
	data, err := r.EncodeFramed(root)
	if err != nil {
		t.Fatal(err)
	}

	// Inspect the actual descriptor table after its normal validation and
	// alias-resolution path. This reports geometric-growth slack without a
	// timing or allocation benchmark.
	payload := data[:len(data)-32]
	d := &decoder{registry: r, data: payload, at: 41, objects: []reflect.Value{{}}, budget: 4 << 30, strings: []string{""}, framed: true}
	rootTypeID, err := d.uint()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.typ(rootTypeID); err != nil {
		t.Fatal(err)
	}
	if err := d.readObjects(); err != nil {
		t.Fatal(err)
	}
	if d.aliases.len() < count {
		t.Fatalf("decoded aliases = %d, want at least %d", d.aliases.len(), count)
	}
	if got, want := unsafe.Sizeof(objectDescription{}), uintptr(12); got != want {
		t.Fatalf("descriptor size = %d, want %d", got, want)
	}
	t.Logf("objects=%d aliases=%d alias-capacity=%d alias-table-bytes=%d descriptor-bytes=%d", len(d.objects)-1, d.aliases.len(), d.aliases.capacity(), d.aliases.capacity()*uint64(unsafe.Sizeof(nodeAlias{})), (len(d.objects)-1)*int(unsafe.Sizeof(objectDescription{})))

	decoded, err := r.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	got := decoded.(*descriptorAliasRoot)
	for i := range got.Nodes {
		if got.Pointers[i] != &got.Nodes[i] || got.Pointers[i].Value != i {
			t.Fatalf("alias %d lost its owner or value", i)
		}
	}
}
