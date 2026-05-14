package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestAnalyzeKeywordMapLowersToCallableFunction(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "keywordmap_callable.elisa", `const enum LuaTokenKind of i16:
    NAME = 0
    AND = 1
    BREAK = 2

keywordmap lua_keyword: sview -> LuaTokenKind:
    "and" => .AND
    "break" => .BREAK
    _ => .NAME

def classify(text: sview) -> LuaTokenKind:
    return lua_keyword(text)
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("expected keywordmap to analyze cleanly, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
	if result.LoweredFile == nil || len(result.LoweredFile.Decls) < 2 {
		t.Fatalf("expected lowered file declarations, got %#v", result.LoweredFile)
	}
	fn, ok := result.LoweredFile.Decls[1].(*ast.FuncDecl)
	if !ok || fn.Name != "lua_keyword" {
		t.Fatalf("expected keywordmap to lower to lua_keyword function, got %T %#v", result.LoweredFile.Decls[1], result.LoweredFile.Decls[1])
	}
	if len(fn.Body) != 1 {
		t.Fatalf("expected lowered keywordmap function body, got %#v", fn.Body)
	}
	if _, ok := fn.Body[0].(*ast.MatchStmt); !ok {
		t.Fatalf("expected lowered keywordmap body to be match statement, got %T", fn.Body[0])
	}
}

func TestAnalyzeKeywordMapAcceptsCStrInput(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "keywordmap_cstr_callable.elisa", `const enum TokenKind of i16:
    NAME = 0
    PROGRAM = 1

keywordmap token_kind_for_text: cstr -> TokenKind:
    "program" => .PROGRAM
    _ => .NAME

def classify(text: cstr) -> TokenKind:
    return token_kind_for_text(text)
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("expected cstr keywordmap to analyze cleanly, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
	fn, ok := result.LoweredFile.Decls[1].(*ast.FuncDecl)
	if !ok || fn.Name != "token_kind_for_text" {
		t.Fatalf("expected cstr keywordmap to lower to token_kind_for_text function, got %T %#v", result.LoweredFile.Decls[1], result.LoweredFile.Decls[1])
	}
	if len(fn.Params) != 1 {
		t.Fatalf("expected lowered keywordmap param, got %#v", fn.Params)
	}
}
