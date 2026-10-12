package parser

import (
	"elisacore/src/ast"
	"testing"
)

func TestLoopValueKeepsFollowingCanBlockSeparate(t *testing.T) {
	tests := []struct {
		name, body string
		canIndex   int
	}{
		{"for_binding", "    total: i64 = for i in 0..<3 |sum: i64 = 0| -> sum:\n        sum <- sum + i\n", 1},
		{"for_assignment", "    total: mutable i64 = 0\n    total <- for i in 0..<3 |total| -> total:\n        total <- total + i\n", 2},
		{"while_binding", "    total: i64 = while i < 3 |i: i64 = 0, sum: i64 = 0| -> sum:\n        sum <- sum + i\n        i <- i + 1\n", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := "def f() -> i64:\n" + tt.body + "    can Console.Write:\n        emit(total)\n    return total\n"
			file, errs := parseSourceFile(t, source)
			if len(errs) != 0 {
				t.Fatalf("loop followed by sibling can block failed: %v", errs)
			}
			fn := file.Decls[0].(*ast.FuncDecl)
			if len(fn.Body) != tt.canIndex+2 {
				t.Fatalf("got %d statements, want %d", len(fn.Body), tt.canIndex+2)
			}
			if _, ok := fn.Body[tt.canIndex].(*ast.CanStmt); !ok {
				t.Fatalf("sibling grant is %T, want CanStmt", fn.Body[tt.canIndex])
			}
			if _, ok := fn.Body[tt.canIndex+1].(*ast.ReturnStmt); !ok {
				t.Fatalf("following return is %T", fn.Body[tt.canIndex+1])
			}
		})
	}
}

func TestExpressionPostfixCanStillParses(t *testing.T) {
	file, errs := parseSourceFile(t, "def f() -> i64:\n    total: i64 = compute() can Memory.Allocate\n    return total\n")
	if len(errs) != 0 {
		t.Fatalf("postfix grant failed: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	decl := fn.Body[0].(*ast.VarDeclStmt)
	if _, ok := decl.Value.(*ast.CanExpr); !ok {
		t.Fatalf("value is %T, want CanExpr", decl.Value)
	}
}
