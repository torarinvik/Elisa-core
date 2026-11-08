package semantic

import "testing"

var darrayStringBenchSink string

func BenchmarkDArrayTypeString(b *testing.B) {
	t := &DArrayType{Elem: &BuiltinType{Name: "u32"}, Shape: &WildcardShape{}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		darrayStringBenchSink = t.String()
	}
}
