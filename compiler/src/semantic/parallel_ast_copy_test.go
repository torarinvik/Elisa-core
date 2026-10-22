package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func TestASTFallbackCopyPreservesCyclesAndNodeSharing(t *testing.T) {
	name := &ast.Ident{Name: "call"}
	call := &ast.CallExpr{Func: name}
	call.Args = []ast.Expr{call, name}
	fn := &ast.FuncDecl{Name: "f", Body: []ast.Stmt{&ast.ExprStmt{Expr: call}, &ast.ExprStmt{Expr: call}}}
	file := &ast.File{Decls: []ast.Decl{fn}, DeclVisibility: map[ast.Decl]string{fn: "private"}}
	copy := copyASTGraph(reflect.ValueOf(file), map[uintptr]reflect.Value{}).Interface().(*ast.File)
	if !reflect.DeepEqual(file.Decls, copy.Decls) {
		t.Fatal("fallback changed graph values")
	}
	copiedFn := copy.Decls[0].(*ast.FuncDecl)
	copiedCall := copiedFn.Body[0].(*ast.ExprStmt).Expr.(*ast.CallExpr)
	if copiedFn == fn || copiedCall == call {
		t.Fatal("fallback shares mutable source nodes")
	}
	if copiedCall.Args[0] != copiedCall || copiedFn.Body[1].(*ast.ExprStmt).Expr != copiedCall || copiedCall.Args[1] != copiedCall.Func {
		t.Fatal("fallback lost node identities")
	}
	if copy.DeclVisibility[copiedFn] != "private" || copy.DeclVisibility[fn] != "" {
		t.Fatal("visibility map key did not follow copied declaration")
	}
	copiedCall.Func.(*ast.Ident).Name = "changed"
	copiedFn.Body[1] = nil
	if name.Name != "call" || fn.Body[1] == nil {
		t.Fatal("copy mutation reached source")
	}
}

var astFallbackCopySink ast.Decl

func BenchmarkASTFallbackCopy(b *testing.B) {
	fn := &ast.FuncDecl{Name: "f"}
	for i := 0; i < 256; i++ {
		fn.Body = append(fn.Body, &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: "f"}, Args: []ast.Expr{&ast.IntLit{Value: "1"}}}})
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		astFallbackCopySink = copyASTDecl(fn)
	}
}
