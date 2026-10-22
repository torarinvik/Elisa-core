package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"testing"
)

func TestSViewFrameConstantTypeUsesInspectedNamespace(t *testing.T) {
	fn := &ast.FuncDecl{Name: "owner"}
	typ := &BuiltinType{Name: "usize"}
	global := NewScope(nil)
	global.Define(&Symbol{Name: "Owner.BUCKETS", Kind: SymbolConst, Type: typ})
	global.Define(&Symbol{Name: "Caller.BUCKETS", Kind: SymbolConst, Type: &SViewType{}})
	a := &Analyzer{globalScope: global, currentScope: NewScope(nil), currentFuncDecl: &ast.FuncDecl{Name: "caller"}, currentNamespace: "Caller", funcDeclSymbols: map[*ast.FuncDecl]*Symbol{fn: {Name: "Owner.owner", Kind: SymbolFunc, Node: fn}}, exprTypes: map[ast.Expr]Type{}}
	id := &ast.Ident{Name: "BUCKETS"}
	gate := &sviewFrameGate{a: a, decl: fn, types: map[string]Type{}}
	if got := gate.receiverType(id, 0); got != typ {
		t.Fatalf("inspected constant type lost: %v", got)
	}
	gate.types["BUCKETS"] = nil
	if got := gate.receiverType(id, 0); got != nil {
		t.Fatalf("untyped local shadow borrowed global type: %v", got)
	}
	delete(gate.types, "BUCKETS")
	gate.decl = &ast.FuncDecl{Name: "other"}
	if got := gate.receiverType(id, 0); got != nil {
		t.Fatalf("callee borrowed caller namespace: %v", got)
	}
	gate.decl = fn
	global.Symbols["Owner.BUCKETS"].Mutable = true
	if got := gate.receiverType(id, 0); got != nil {
		t.Fatalf("mutable value treated as constant: %v", got)
	}
}

func TestSViewFrameConstantCastBeforeBodyTyping(t *testing.T) {
	fn := &ast.FuncDecl{Name: "bucket"}
	fn.Body = []ast.Stmt{&ast.ReturnStmt{Value: &ast.CastExpr{
		Operand: &ast.Ident{Name: "BUCKETS"}, Target: &ast.NamedType{Name: "u64"},
		Origin: ast.CastExprOriginPostfixShorthand,
	}}}
	global := NewScope(nil)
	global.Define(&Symbol{Name: "Owner.BUCKETS", Kind: SymbolConst, Type: &BuiltinType{Name: "usize"}})
	a := &Analyzer{globalScope: global, exprTypes: map[ast.Expr]Type{},
		funcDeclSymbols: map[*ast.FuncDecl]*Symbol{fn: {Name: "Owner.bucket", Kind: SymbolFunc, Node: fn}}}
	if !a.computeSViewFrameWriteFree(fn) {
		t.Fatal("immutable numeric constant cast rejected before expression typing")
	}
	global.Symbols["Owner.BUCKETS"].Type = &SViewType{}
	if a.computeSViewFrameWriteFree(fn) {
		t.Fatal("non-numeric constant authorized numeric cast")
	}
	global.Symbols["Owner.BUCKETS"].Type = &BuiltinType{Name: "usize"}
	global.Symbols["Owner.BUCKETS"].Mutable = true
	if a.computeSViewFrameWriteFree(fn) {
		t.Fatal("mutable global authorized numeric cast before expression typing")
	}
}

func TestSViewFrameNamedTupleFieldBeforeBodyTyping(t *testing.T) {
	boolean := &BuiltinType{Name: "bool"}
	integer := &BuiltinType{Name: "i64"}
	gate := &sviewFrameGate{a: &Analyzer{exprTypes: map[ast.Expr]Type{}}, types: map[string]Type{
		"record": &TupleType{Fields: []TupleField{{Name: "valid", Type: boolean}, {Name: "value", Type: integer}}},
	}}
	for name, want := range map[string]Type{"valid": boolean, "value": integer, "missing": nil} {
		expr := &ast.FieldExpr{Object: &ast.Ident{Name: "record"}, Field: name}
		if got := gate.receiverType(expr, 0); got != want {
			t.Fatalf("tuple field %s: got %v, want %v", name, got, want)
		}
	}
}

func TestSViewFrameUnresolvedTupleFieldDeclinesOperator(t *testing.T) {
	field := &ast.FieldExpr{Object: &ast.Ident{Name: "record"}, Field: "value"}
	gate := &sviewFrameGate{a: &Analyzer{exprTypes: map[ast.Expr]Type{}}, types: map[string]Type{
		"record": &TupleType{Fields: []TupleField{{Name: "value", Type: &TypeParamType{Name: "T"}}}},
	}}
	if got := gate.receiverType(field, 0); got != nil {
		t.Fatalf("uninstantiated tuple field borrowed a concrete type: %v", got)
	}
	if gate.binaryHasNoOverload(&ast.BinaryExpr{Op: lexer.TOKEN_PLUS, Left: field, Right: &ast.IntLit{}}) {
		t.Fatal("an unresolved generic field cannot prove an operator write-free")
	}
}
