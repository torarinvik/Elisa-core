package parser

import (
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/unparse"
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
			name: "context field",
			src:  "context ParseCtx:\n    parser: i64 = 1\n",
		},
		{
			name: "with signature",
			src:  "def build() with parser: i64 = 1 -> i64:\n    return parser\n",
		},
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