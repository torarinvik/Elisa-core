//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"os"
	"strings"
	"testing"
)

func TestSViewFrameDirectSelfCallChecksWholeBody(t *testing.T) {
	typ := &BuiltinType{Name: "u64"}
	fn := &ast.FuncDecl{Name: "loop", Params: []ast.ParamDecl{{Name: "n", Type: &ast.NamedType{Name: "u64"}}}}
	self := &ast.CallExpr{Func: &ast.Ident{Name: "loop"}, Args: []ast.Expr{&ast.IntLit{Value: "0"}}}
	sym := &Symbol{Name: "loop", Kind: SymbolFunc, Node: fn, Type: &FuncType{Params: []Type{typ}, Return: typ, ReturnBorrowedOwnerRefsKnown: true}}
	scope := NewScope(nil)
	scope.Define(sym)
	a := &Analyzer{globalScope: scope, currentScope: scope, funcDeclSymbols: map[*ast.FuncDecl]*Symbol{fn: sym}, exprTypes: map[ast.Expr]Type{}, namedTypes: map[string]Type{"u64": typ}}

	gate := &sviewFrameGate{a: a, decl: fn}
	if !gate.directSelfCallEligible(fn) {
		t.Fatal("resolved monomorphic self signature declined")
	}
	fn.TypeParams = []string{"T"}
	if gate.directSelfCallEligible(fn) {
		t.Fatal("generic declaration admitted as same specialization")
	}
	fn.TypeParams = nil
	functionType := sym.Type.(*FuncType)
	functionType.TypeParams = []string{"T"}
	if gate.directSelfCallEligible(fn) {
		t.Fatal("generic function type admitted as same specialization")
	}
	functionType.TypeParams = nil
	fn.RegionParams = []string{"r"}
	if gate.directSelfCallEligible(fn) {
		t.Fatal("region-generic self call admitted")
	}
	fn.RegionParams = nil
	fn.PermissionParams = []string{"P"}
	if gate.directSelfCallEligible(fn) {
		t.Fatal("permission-generic self call admitted")
	}
	fn.PermissionParams = nil
	functionType.ReturnBorrowedOwnerRefsKnown = false
	if gate.directSelfCallEligible(fn) {
		t.Fatal("unresolved effect/return surface admitted")
	}
	functionType.ReturnBorrowedOwnerRefsKnown = false
	functionType.Return = &BuiltinType{Name: "bool"}
	if !gate.directSelfCallEligible(fn) {
		t.Fatal("builtin bool needs no borrowed-return owner summary")
	}
	functionType.Return = typ
	functionType.ReturnBorrowedOwnerRefsKnown = true
	functionType.Params[0] = nil
	if gate.directSelfCallEligible(fn) {
		t.Fatal("unresolved parameter type admitted")
	}
	functionType.Params[0] = typ
	functionType.Params[0] = &RefType{Elem: typ, Mutable: true}
	if gate.directSelfCallEligible(fn) {
		t.Fatal("mutable reference signature admitted")
	}
	functionType.Params[0] = typ
	functionType.Params[0] = &DArrayType{Elem: typ}
	if gate.directSelfCallEligible(fn) {
		t.Fatal("owning aggregate argument admitted across implicit transfer/drop")
	}
	functionType.Params[0] = typ
	fn.Variadic = true
	if gate.directSelfCallEligible(fn) {
		t.Fatal("variadic self signature admitted")
	}
	fn.Variadic = false

	fn.Body = []ast.Stmt{&ast.ReturnStmt{Value: self}}
	if !a.computeSViewFrameWriteFree(fn) {
		t.Fatal("direct self call in otherwise read-only body declined")
	}
	fn.Body = []ast.Stmt{&ast.VarDeclStmt{Name: "prior", Value: self}, &ast.AssignStmt{Target: &ast.IndexExpr{Object: &ast.Ident{Name: "buffer"}, Index: &ast.IntLit{Value: "0"}}, Value: &ast.IntLit{Value: "1"}}}
	if a.computeSViewFrameWriteFree(fn) {
		t.Fatal("write following self call admitted")
	}
	fn.Body = []ast.Stmt{&ast.ReturnStmt{Value: self}}
	self.Args = []ast.Expr{&ast.CallExpr{Func: &ast.Ident{Name: "unknown_effect"}}}
	if a.computeSViewFrameWriteFree(fn) {
		t.Fatal("effectful or unknown self argument admitted")
	}
	self.Args = []ast.Expr{&ast.IntLit{Value: "0"}}
	self.HasArgForward = true
	if a.computeSViewFrameWriteFree(fn) {
		t.Fatal("forwarded self arguments admitted")
	}
	self.HasArgForward = false
	self.Args = nil
	if a.computeSViewFrameWriteFree(fn) {
		t.Fatal("missing/defaulted self argument admitted")
	}
	self.Args = []ast.Expr{&ast.IntLit{Value: "0"}}
	fn.Params[0].Name = "loop"
	if a.computeSViewFrameWriteFree(fn) {
		t.Fatal("shadowing parameter treated as direct self call")
	}
}

func TestSMTReadOnlyRecursiveAnnotationLookup(t *testing.T) {
	data, err := os.ReadFile("testdata/smt_assoc_impl_scope_early.elisa")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	good := analyzeContractStrict(t, "annotation_scope.elisa", source)
	if es := good.Errors(); len(es) != 0 {
		t.Fatal(es)
	}
	hit := "                return true if annotations[index].owner"
	if strings.Count(source, hit) != 1 {
		t.Fatal("runtime hit missing or ambiguous")
	}
	wrong := analyzeContractStrict(t, "annotation_scope_wrong.elisa", strings.Replace(source, hit, "                return false if annotations[index].owner", 1))
	if !strings.Contains(strings.Join(wrong.Errors(), "\n"), "could not be proven") {
		t.Fatalf("wrong hit accepted or wrong failure: %v", wrong.Errors())
	}
	lines := strings.Split(source, "\n")
	changed := false
	for i, line := range lines {
		if strings.Contains(line, "ensure index < annotations.count implies result ==") {
			lines[i] = "            ensure index < annotations.count implies result == false"
			changed = true
		}
	}
	if !changed {
		t.Fatal("recursive step contract missing")
	}
	badStep := analyzeContractStrict(t, "annotation_scope_false_step.elisa", strings.Join(lines, "\n"))
	if !strings.Contains(strings.Join(badStep.Errors(), "\n"), `ensure postcondition of "line_is_impl_scope_from" could not be proven`) {
		t.Fatalf("false recursive contract not rejected at definition: %v", badStep.Errors())
	}
}
