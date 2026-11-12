package ownerinventory

import (
	"elisacore/src/ast"
	"testing"
)

func TestParameterTagsRejectsDuplicateAndChargesWork(t *testing.T) {
	fn := &ast.FuncDecl{Name: "f", Params: []ast.ParamDecl{{Name: "x"}, {Name: "y"}}}
	typeName := func(ast.TypeExpr) string { return "i64" }
	work := 0
	tags, supported, ready := ParameterTags(fn, true, 100, typeName, func(n int) bool { work += n; return true })
	if !ready || !supported || tags[0] != 4 || tags[1] != 4 || work != 214 {
		t.Fatalf("tags=%v supported=%v ready=%v work=%d", tags[:2], supported, ready, work)
	}
	fn.Params[1].Name = "x"
	_, supported, ready = ParameterTags(fn, true, 100, typeName, nil)
	if supported || !ready {
		t.Fatal("duplicate parameter must be rejected")
	}
	_, supported, ready = ParameterTags(fn, true, 100, typeName, func(int) bool { return false })
	if supported || ready {
		t.Fatal("exhausted work budget must stop analysis")
	}
}

func TestParameterTagsCoversSupportedAndUnsupportedTypes(t *testing.T) {
	types := []string{"bool", "char", "int", "i64", "i8", "i16", "i32", "u8", "u16", "u32", "u64", "isize", "usize", "uintptr"}
	for i, name := range types {
		t.Run(name, func(t *testing.T) {
			fn := &ast.FuncDecl{Name: "f", Params: []ast.ParamDecl{{Name: "x"}}}
			tags, supported, ready := ParameterTags(fn, true, 100, func(ast.TypeExpr) string { return name }, nil)
			if !ready || !supported || tags[0] != byte(i+1) {
				t.Fatalf("tag=%d supported=%v ready=%v", tags[0], supported, ready)
			}
		})
	}
	fn := &ast.FuncDecl{Name: "f", Params: []ast.ParamDecl{{Name: "x"}}}
	_, supported, ready := ParameterTags(fn, true, 100, func(ast.TypeExpr) string { return "f64" }, nil)
	if supported || !ready {
		t.Fatal("unsupported type must decline")
	}
}
