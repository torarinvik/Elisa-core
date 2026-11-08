package semantic

import (
	"fmt"
	"testing"
)

func BenchmarkProtocolLiveLeafPathsScalarFields(b *testing.B) {
	fields := make(map[string]Field, 64)
	for i := 0; i < 64; i++ {
		name := fmt.Sprint("f", i)
		fields[name] = Field{Name: name, Type: &BuiltinType{Name: "i64"}}
	}
	typ := &StructType{Name: "Many", Fields: fields}
	a := &Analyzer{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if len(a.protocolLiveLeafPaths(typ, "", map[string]bool{})) != 0 {
			b.Fatal("unexpected protocol")
		}
	}
}
func BenchmarkProtocolCarrierScalarFields(b *testing.B) {
	fields := make(map[string]Field, 64)
	for i := 0; i < 64; i++ {
		name := fmt.Sprint("f", i)
		fields[name] = Field{Name: name, Type: &BuiltinType{Name: "i64"}}
	}
	typ := &StructType{Name: "Many", Fields: fields}
	a := &Analyzer{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if a.containsTrackedProtocolCarrierValues(typ, map[string]bool{}) {
			b.Fatal("unexpected protocol")
		}
	}
}

func BenchmarkProtocolFieldContainsCarrier(b *testing.B) {
	fields := make(map[string]Field, 64)
	for i := 0; i < 64; i++ {
		name := fmt.Sprint("f", i)
		fields[name] = Field{Name: name, Type: &StructType{Name: name}}
	}
	typ := &StructType{Name: "Many", Fields: fields}
	a := &Analyzer{}
	seen := map[string]bool{"Outer": true}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if a.protocolFieldContainsCarrier(typ, seen) {
			b.Fatal("unexpected protocol")
		}
	}
}

func TestProtocolFieldContainsCarrierKeepsSeenPrivate(t *testing.T) {
	seen := map[string]bool{"outer": true}
	typ := &StructType{Name: "inner", Fields: map[string]Field{"x": {Type: &BuiltinType{Name: "i64"}}}}
	a := &Analyzer{}
	if a.protocolFieldContainsCarrier(typ, seen) {
		t.Fatal("unexpected protocol")
	}
	if len(seen) != 1 || !seen["outer"] {
		t.Fatalf("caller seen set changed: %v", seen)
	}
}
