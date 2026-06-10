package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/unparse"
)

func TestParseFunctionDefaultArgs(t *testing.T) {
	file, errs := parseSourceFile(t, "def build(x: i64, ys: darray[u32] = [], cond: i64? = null) -> i64:\n    return x\n\nextern reserve(xs: darray[i64], want: usize = 0) -> void\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	buildDecl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[0])
	}
	if buildDecl.Params[1].DefaultValue == nil || buildDecl.Params[2].DefaultValue == nil {
		t.Fatalf("expected defaults on trailing function params, got %#v", buildDecl.Params)
	}
	reserveDecl, ok := file.Decls[1].(*ast.ExternFuncDecl)
	if !ok {
		t.Fatalf("expected extern function decl, got %T", file.Decls[1])
	}
	if reserveDecl.Params[1].DefaultValue == nil {
		t.Fatalf("expected default on extern param, got %#v", reserveDecl.Params)
	}
	formatted := unparse.FormatDecl(buildDecl) + "\n" + unparse.FormatDecl(reserveDecl)
	for _, want := range []string{"ys: darray[u32] = []", "cond: i64? = null", "want: usize = 0"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseRejectsDefaultsInDisallowedPositions(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "export func",
			src:  "def add(x: i64, y: i64) -> i64:\n    return x + y\n\nexport func add_export(x: i64 = 1, y: i64) -> i64 = add\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := parseSourceFile(t, tc.src)
			if len(errs) == 0 {
				t.Fatalf("expected parser errors for %s", tc.name)
			}
			if !strings.Contains(strings.Join(errs, "\n"), "parameter defaults are not allowed here") {
				t.Fatalf("expected default rejection diagnostic for %s, got %v", tc.name, errs)
			}
		})
	}
}

func TestParseFunctionCallArgForwarding(t *testing.T) {
	file, errs := parseSourceFile(t, "def consume(parser: i64, offset: i64, width: i64 = 0) -> i64:\n    return parser + offset + width\n\ndef build(parser: i64, offset: i64) -> i64:\n    return consume(.., width: 9)\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	buildDecl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[1])
	}
	ret, ok := buildDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return statement, got %T", buildDecl.Body[0])
	}
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected call expression, got %T", ret.Value)
	}
	if !call.HasArgForward {
		t.Fatal("expected call argument forwarding to be recorded")
	}
	if len(call.Args) != 1 || len(call.ArgNames) != 1 || call.ArgNames[0] != "width" {
		t.Fatalf("expected one named explicit arg after forwarding, got args=%d names=%v", len(call.Args), call.ArgNames)
	}
	formatted := unparse.FormatDecl(buildDecl)
	if !strings.Contains(formatted, "consume(.., width: 9)") {
		t.Fatalf("expected formatted output to preserve call forwarding, got:\n%s", formatted)
	}
}

func TestParseRejectsInvalidFunctionCallArgForwarding(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "duplicate",
			src:  "def consume(x: i64) -> i64:\n    return x\n\ndef build(x: i64) -> i64:\n    return consume(.., ..)\n",
			want: "call argument forwarding `..` can only appear once",
		},
		{
			name: "not first",
			src:  "def consume(x: i64, y: i64) -> i64:\n    return x + y\n\ndef build(x: i64, y: i64) -> i64:\n    return consume(y: y, ..)\n",
			want: "call argument forwarding `..` must appear before other call arguments",
		},
		{
			name: "positional explicit arg",
			src:  "def consume(x: i64, y: i64) -> i64:\n    return x + y\n\ndef build(x: i64, y: i64) -> i64:\n    return consume(.., y)\n",
			want: "call argument forwarding `..` only supports named arguments",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := parseSourceFile(t, tc.src)
			if len(errs) == 0 {
				t.Fatalf("expected parser errors for %s", tc.name)
			}
			if !strings.Contains(strings.Join(errs, "\n"), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, errs)
			}
		})
	}
}
