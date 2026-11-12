package parser

import (
	"elisacore/src/lexer"
	"reflect"
	"strings"
	"testing"
)

func TestLargeBorrowedParsePreservesExpressionTree(t *testing.T) {
	const filename = "large_expressions.elisa"
	source := strings.Repeat("def example(x: i32) -> i32:\n    return use(x.field, x.field)\n", 4096)
	tokens := lexer.New(filename, []byte(source)).Tokenize()
	plain := New(tokens)
	batched := NewWithBorrowedTokens(tokens)
	if batched.expressionStorage == nil {
		t.Fatal("input did not enable batched allocation")
	}
	want := plain.ParseFile(filename)
	got := batched.ParseFile(filename)
	if len(plain.Errors()) != 0 || len(batched.Errors()) != 0 {
		t.Fatalf("parse errors: plain %v, batched %v", plain.Errors(), batched.Errors())
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("batched parse changed the expression tree")
	}
}
