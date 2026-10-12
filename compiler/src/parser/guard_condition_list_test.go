package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
	"testing"
)

func TestGuardConditionLists(t *testing.T) {
	for _, head := range []string{"return 7", "7 return", "return", "break", "continue", "work()", "value <- 7", "panic(\"stop\")"} {
		t.Run(head, func(t *testing.T) {
			src := "def probe(a: bool, b: bool) -> i64:\n    " + head + " if:\n        a\n        b\n    return 9\n"
			file, errs, _ := parseSourceWithNotices(t, src)
			if len(errs) != 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			fn := file.Decls[0].(*ast.FuncDecl)
			if len(fn.Body) != 2 {
				t.Fatalf("lost following statement: %d", len(fn.Body))
			}
			guard, ok := fn.Body[0].(*ast.IfStmt)
			if !ok {
				t.Fatalf("expected desugared guard, got %T", fn.Body[0])
			}
			or, ok := guard.Cond.(*ast.BinaryExpr)
			if !ok || or.Op != lexer.TOKEN_OR {
				t.Fatalf("expected ordered OR, got %#v", guard.Cond)
			}
			if or.Left.(*ast.Ident).Name != "a" || or.Right.(*ast.Ident).Name != "b" {
				t.Fatal("condition order changed")
			}
		})
	}
}

func TestGuardConditionListRejectsEmptyAndStatements(t *testing.T) {
	for _, src := range []string{
		"def probe() -> i64:\n    return 1 if:\n    return 2\n",
		"def probe() -> i64:\n    return 1 if:\n        value = true\n    return 2\n",
	} {
		_, errs, _ := parseSourceWithNotices(t, src)
		if len(errs) == 0 {
			t.Fatalf("expected expression-list error for %q", src)
		}
	}
}

func TestValueFirstReturnStyleNotice(t *testing.T) {
	_, errs, notices := parseSourceWithNotices(t, "def probe(flag: bool) -> i64:\n    1 return if flag\n    return 2 if flag\n    return 3\n")
	if len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "prefer `return value if condition`") {
		t.Fatalf("notices: %v", notices)
	}
}

func TestGuardListsDoNotChangeTernaryOrValueIf(t *testing.T) {
	_, errs, notices := parseSourceWithNotices(t, "def probe(flag: bool) -> i64:\n    value: i64 = 1 if flag else 2\n    return if flag:\n        value\n    else:\n        3\n")
	if len(errs) != 0 || len(notices) != 0 {
		t.Fatalf("errors=%v notices=%v", errs, notices)
	}
}
