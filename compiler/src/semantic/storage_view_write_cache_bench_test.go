package semantic

import (
	"elisacore/src/ast"
	"strconv"
	"testing"
)

func BenchmarkStorageViewWriteCacheHit(b *testing.B) {
	decl := &ast.FuncDecl{Name: "parser_step", Params: []ast.ParamDecl{{Name: "parser", Mutable: true}}}
	fields := map[string]bool{}
	for i := 0; i < 32; i++ {
		fields["field_"+strconv.Itoa(i)] = true
	}
	a := &Analyzer{storageViewCalleeWriteCache: map[storageViewCalleeWriteKey]storageViewCalleeWrites{
		{decl: decl, param: 0}: {Fields: fields},
	}}
	decls := []*ast.FuncDecl{decl}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := a.storageViewCalleeWritesFor(decls, 0); !got.Fields["field_0"] || len(got.Fields) != 32 {
			b.Fatal("cached write summary changed")
		}
	}
}
