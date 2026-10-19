package semantic
import "testing"
func BenchmarkMutableBorrowProductionTerminal(b *testing.B) {
 for _,c:=range []struct{name string;t Type}{{"scalar",&BuiltinType{Name:"i64"}},{"type-param",&TypeParamType{Name:"T"}},{"mutable-ref",&RefType{Mutable:true}},{"immutable-ref",&RefType{Elem:&BuiltinType{Name:"i64"}}}} {
  b.Run(c.name,func(b *testing.B){a:=&Analyzer{};b.ReportAllocs();for i:=0;i<b.N;i++ {benchmarkMutableBorrowSink=a.hasMutableBorrowCapability(c.t)}})
 }
}
