package semantic

import (
	"bytes"
	"sync"
	"testing"
)

// Preserve the previous workspace implementation as a differential oracle. The
// encoder is unchanged; these checks target reuse, cycle numbering and ownership.
func unpooledTypeIDKey(t Type) (string, bool) {
	var b bytes.Buffer
	b.Grow(64)
	var stack [16]typeIDActiveFrame
	active := &typeIDActive{frames: stack[:0]}
	next := 1
	if !appendTypeIDKey(&b, t, active, &next) {
		return "", false
	}
	return b.String(), true
}
func typeIDScratchFixtures() []Type {
	scalar := &BuiltinType{Name: "i64"}
	cycle := &RefType{}
	cycle.Elem = cycle
	shared := &OptionalType{Value: scalar}
	dag := &TupleType{Fields: []TupleField{{Type: shared}, {Type: shared}}}
	recursive := &StructType{Name: "Recursive", Fields: map[string]Field{}}
	recursive.Fields["next"] = Field{Type: &RefType{Elem: recursive}}
	recursive.Fields["value"] = Field{Type: scalar}
	deep := Type(scalar)
	for i := 0; i < 96; i++ {
		deep = &OptionalType{Value: deep}
	}
	return []Type{nil, scalar, &RefType{Elem: scalar, Mutable: true}, recursive, &FuncType{Params: []Type{scalar, recursive}, Return: scalar}, deep, &PackedVariantViewType{}, cycle, dag}
}
func TestTypeIDScratchMatchesUnpooledAndDropsReferences(t *testing.T) {
	fixtures := typeIDScratchFixtures()
	for _, typ := range fixtures {
		want, wantOK := unpooledTypeIDKey(typ)
		for i := 0; i < 12; i++ {
			got, ok := canonicalTypeIDKey(typ)
			if got != want || ok != wantOK {
				t.Fatalf("reused workspace changed key for %T", typ)
			}
			// Other invocations may overwrite scratch but cannot alter returned strings.
			canonicalTypeIDKey(&BuiltinType{Name: "different"})
			if got != want {
				t.Fatal("returned key shared workspace bytes")
			}
		}
		active := &typeIDActive{frames: make([]typeIDActiveFrame, 0, 16)}
		var builder bytes.Buffer
		next := 1
		appendTypeIDKey(&builder, typ, active, &next)
		if len(active.frames) != 0 {
			t.Fatal("encoder left an active path")
		}
		for _, frame := range active.frames[:cap(active.frames)] {
			if frame.t != nil || frame.id != 0 {
				t.Fatal("scratch retained source type graph")
			}
		}
	}
	// Pooling cannot turn a structural key into pointer-identity memoization.
	mutable := &RefType{Elem: &BuiltinType{Name: "i64"}}
	before, _ := canonicalTypeIDKey(mutable)
	mutable.Mutable = true
	after, _ := canonicalTypeIDKey(mutable)
	if before == after {
		t.Fatal("workspace reused stale mutable-type result")
	}
}
func TestTypeIDScratchConcurrentIsolation(t *testing.T) {
	fixtures := typeIDScratchFixtures()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for repeat := 0; repeat < 40; repeat++ {
				for _, typ := range fixtures {
					want, wantOK := unpooledTypeIDKey(typ)
					got, ok := canonicalTypeIDKey(typ)
					if got != want || ok != wantOK {
						t.Error("concurrent traversal scratch leaked")
					}
				}
			}
		}()
	}
	wg.Wait()
}
func BenchmarkCanonicalTypeIDWorkspace(b *testing.B) {
	fixtures := typeIDScratchFixtures()
	for _, mode := range []string{"unpooled", "pooled"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				typ := fixtures[i%5]
				if mode == "pooled" {
					canonicalTypeIDKey(typ)
				} else {
					unpooledTypeIDKey(typ)
				}
			}
		})
	}
}
