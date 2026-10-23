package statecache

import (
	"bytes"
	"crypto/sha256"
	"reflect"
	"testing"
)

type graph struct {
	Name    string
	Next    *graph
	Alias   *graph
	Values  map[string]*graph
	Same    map[string]*graph
	Data    []byte
	Copy    []byte
	Dynamic any
	Empty   []byte
	Nil     []byte
	Signed  int64
	Float   float64
}

func graphRegistry() *Registry { return New([]reflect.Type{reflect.TypeFor[graph]()}, nil, nil) }
func TestGraphRoundTripIdentity(t *testing.T) {
	root := &graph{Name: "root", Data: []byte{0, 0xff, 3}, Empty: []byte{}, Signed: -9223372036854775807, Float: -2.75}
	child := &graph{Name: "child", Next: root}
	root.Next = child
	root.Alias = child
	root.Dynamic = child
	root.Values = map[string]*graph{"root": root, "child": child}
	root.Same = root.Values
	root.Copy = root.Data
	data, err := graphRegistry().Encode(root)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := graphRegistry().Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	got := restored.(*graph)
	if got.Next.Next != got || got.Next != got.Alias || got.Dynamic.(*graph) != got.Next || got.Values["child"] != got.Next {
		t.Fatal("pointer identity lost")
	}
	got.Values["other"] = got
	if got.Same["other"] != got {
		t.Fatal("map identity lost")
	}
	got.Data[0] = 8
	if got.Copy[0] != 8 {
		t.Fatal("slice identity lost")
	}
	if got.Empty == nil || got.Nil != nil || got.Signed != root.Signed || got.Float != root.Float {
		t.Fatal("scalar or nil state changed")
	}
	if !reflect.DeepEqual(root.Data, []byte{0, 0xff, 3}) {
		t.Fatal("decode aliases original input graph")
	}
}
func TestGraphRejectsCorruptionAndSchemaMismatch(t *testing.T) {
	r := graphRegistry()
	data, err := r.Encode(&graph{Name: "source"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range data {
		copy := append([]byte(nil), data...)
		copy[i] ^= 0xff
		if _, err := r.Decode(copy); err == nil {
			t.Fatalf("accepted corruption at %d", i)
		}
	}
	for i := 0; i < len(data); i++ {
		if _, err := r.Decode(data[:i]); err == nil {
			t.Fatalf("accepted truncation at %d", i)
		}
	}
	other := New([]reflect.Type{reflect.TypeFor[graph]()}, func(_ reflect.Type, name string) bool { return name != "Name" }, nil)
	if _, err := other.Decode(data); err == nil {
		t.Fatal("accepted different schema")
	}
	// Even a valid checksum cannot authorize trailing payload.
	payload := append(append([]byte(nil), data[:len(data)-32]...), 0)
	digest := sha256.Sum256(payload)
	payload = append(payload, digest[:]...)
	if _, err := r.Decode(payload); err == nil {
		t.Fatal("accepted trailing data")
	}
}
func TestGraphRejectsProcessLocalState(t *testing.T) {
	type local struct {
		Callback func()
		Address  uintptr
		Channel  chan int
	}
	r := New([]reflect.Type{reflect.TypeFor[local]()}, nil, nil)
	for _, v := range []local{{Callback: func() {}}, {Address: 42}, {Channel: make(chan int)}} {
		if _, err := r.Encode(v); err == nil {
			t.Fatal("accepted process-local state")
		}
	}
	if _, err := r.Encode(local{}); err != nil {
		t.Fatal("zero runtime state must remain zero", err)
	}
}
func TestGraphPolicyOnlyOmitsDeclaredRuntimeField(t *testing.T) {
	type local struct {
		Text     string
		Callback func()
	}
	r := New([]reflect.Type{reflect.TypeFor[local]()}, func(_ reflect.Type, name string) bool { return name != "Callback" }, nil)
	data, err := r.Encode(&local{Text: "kept", Callback: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	value, err := r.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	got := value.(*local)
	if got.Text != "kept" || got.Callback != nil {
		t.Fatal("field policy changed saved state")
	}
}
func TestGraphRejectsUnregisteredInterfaceValue(t *testing.T) {
	r := graphRegistry()
	if _, err := r.Encode(&graph{Dynamic: struct{ Secret string }{"x"}}); err == nil {
		t.Fatal("accepted unknown interface type")
	}
}
func BenchmarkGraphRoundTrip(b *testing.B) {
	r := graphRegistry()
	root := &graph{Values: map[string]*graph{}, Data: bytes.Repeat([]byte("data"), 4096)}
	for i := 0; i < 1000; i++ {
		root.Next = &graph{Name: "node", Next: root.Next}
	}
	encoded, err := r.Encode(root)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(encoded)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Decode(encoded); err != nil {
			b.Fatal(err)
		}
	}
}

func TestGraphRejectsUnsupportedInteriorAliases(t *testing.T) {
	type interior struct {
		Values  []int
		Tail    []int
		Element *int
	}
	r := New([]reflect.Type{reflect.TypeFor[interior]()}, nil, nil)
	values := []int{1, 2, 3}
	for _, v := range []*interior{{Values: values, Tail: values[1:]}, {Values: values, Element: &values[1]}} {
		if _, err := r.Encode(v); err == nil {
			t.Fatal("interior alias silently duplicated")
		}
	}
}

func TestGraphDecodeAllocationBudget(t *testing.T) {
	r := graphRegistry()
	data, err := r.Encode(&graph{Data: make([]byte, 100)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DecodeWithLimit(data, 10); err == nil {
		t.Fatal("allocation budget ignored")
	}
}

func TestGraphCanonicalStatelessMarkers(t *testing.T) {
	type marker struct{}
	type root struct{ Value *marker }
	canonical := &marker{}
	r := New([]reflect.Type{reflect.TypeFor[root](), reflect.TypeFor[marker]()}, nil, nil)
	r.InternZeroPointer(canonical)
	data, err := r.Encode(&root{Value: canonical})
	if err != nil {
		t.Fatal(err)
	}
	v, err := r.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if v.(*root).Value != canonical {
		t.Fatal("canonical marker identity changed")
	}
}

func FuzzGraphDecode(f *testing.F) {
	r := graphRegistry()
	valid, err := r.Encode(&graph{Name: "seed", Values: map[string]*graph{"x": {Name: "child"}}})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid[41 : len(valid)-32])
	f.Add([]byte{1, 2, 3})
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > 256<<10 {
			t.Skip()
		}
		data := append(append([]byte(nil), valid[:41]...), body...)
		digest := sha256.Sum256(data)
		data = append(data, digest[:]...)
		_, _ = r.DecodeWithLimit(data, 1<<20)
	})
}
