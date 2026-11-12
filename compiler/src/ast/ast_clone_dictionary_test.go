package ast

import "testing"

func TestCloneDictionaryLiteralPreservesKeysAndIsolation(t *testing.T) {
	original := &ListLitExpr{Brace: true, Keys: []Expr{&Ident{Name: "key"}}, Elems: []Expr{&Ident{Name: "value"}}}
	clone, ok := CloneExpr(original).(*ListLitExpr)
	if !ok || len(clone.Keys) != 1 || len(clone.Elems) != 1 {
		t.Fatalf("dictionary clone lost entries: %#v", clone)
	}
	clone.Keys[0].(*Ident).Name = "changed-key"
	clone.Elems[0].(*Ident).Name = "changed-value"
	if original.Keys[0].(*Ident).Name != "key" || original.Elems[0].(*Ident).Name != "value" {
		t.Fatal("dictionary clone aliases original keys or values")
	}
	if CloneExpr(&ListLitExpr{Brace: true, Keys: []Expr{}}).(*ListLitExpr).Keys == nil {
		t.Fatal("empty dictionary lost its non-nil keys marker")
	}
	if CloneExpr(&ListLitExpr{Brace: true}).(*ListLitExpr).Keys != nil {
		t.Fatal("set literal acquired a dictionary keys marker")
	}
	if CloneExpr(&ListLitExpr{Brace: true, Keys: []Expr{&StructLitExpr{}}}) != nil {
		t.Fatal("dictionary with an unsupported key should not be cloned")
	}
}

func TestCloneDictionarySubstitutesKeysValuesAndOwner(t *testing.T) {
	original := &ListLitExpr{Brace: true, Keys: []Expr{&Ident{Name: "key"}}, Elems: []Expr{&Ident{Name: "value"}}, Owner: &Ident{Name: "owner"}}
	substitutions := map[string]Expr{"key": &Ident{Name: "new-key"}, "value": &Ident{Name: "new-value"}, "owner": &Ident{Name: "new-owner"}}
	clone, ok := CloneExprSubst(original, substitutions).(*ListLitExpr)
	if !ok || clone.Keys[0].(*Ident).Name != "new-key" || clone.Elems[0].(*Ident).Name != "new-value" || clone.Owner.(*Ident).Name != "new-owner" {
		t.Fatalf("dictionary substitution did not reach all children: %#v", clone)
	}
	if original.Keys[0].(*Ident).Name != "key" || original.Elems[0].(*Ident).Name != "value" || original.Owner.(*Ident).Name != "owner" {
		t.Fatal("dictionary substitution changed the original")
	}
}
