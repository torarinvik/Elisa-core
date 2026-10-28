package ast

import (
	"testing"

	"elisacore/src/lexer"
)

func TestSyntaxSnapshotPreservesCompactNumericSuffix(t *testing.T) {
	tokens := []lexer.Token{{
		Kind:   lexer.TOKEN_INT_LIT,
		Suffix: lexer.NumericSuffixU64,
		Text:   "42",
		Pos:    lexer.Pos{File: "snapshot.elisa", Line: 1, Col: 1},
	}}
	file := &File{Decls: []Decl{&StaticGenerateDecl{Body: []StaticGenerateStmt{
		&StaticGenerateEmitDecl{Tokens: tokens},
	}}}}
	snapshot, err := NewSyntaxSnapshot(file, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	tokens[0].Text = "99"
	tokens[0].Suffix = lexer.NumericSuffixNone

	copy, err := snapshot.CopyFile()
	if err != nil {
		t.Fatal(err)
	}
	emit := copy.Decls[0].(*StaticGenerateDecl).Body[0].(*StaticGenerateEmitDecl)
	if len(emit.Tokens) != 1 || emit.Tokens[0].Kind != lexer.TOKEN_INT_LIT || emit.Tokens[0].Text != "42" || emit.Tokens[0].Suffix != lexer.NumericSuffixU64 || emit.Tokens[0].SuffixString() != "u64" {
		t.Fatalf("snapshot token = %#v; want preserved typed integer", emit.Tokens)
	}
}
