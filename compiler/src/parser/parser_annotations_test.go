package parser

import (
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"testing"
)

func TestParseAlignedStructAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@align(32)\nstruct Vec4:\n    x: f32\n    y: f32\n    z: f32\n    w: f32\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one struct annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "align" {
		t.Fatalf("expected align annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 1 || decl.Annotations[0].Args[0] != "32" {
		t.Fatalf("expected align(32), got %#v", decl.Annotations[0].Args)
	}
}
func TestParseAlign64StructAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@align(64)\nstruct Counter:\n    value: i64\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one struct annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "align" {
		t.Fatalf("expected align annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 1 || decl.Annotations[0].Args[0] != "64" {
		t.Fatalf("expected align(64), got %#v", decl.Annotations[0].Args)
	}
}
func TestParsePackedEnumProfileAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@packed_profile(build_heavy)\npacked enum Expr:\n    common:\n        span: int\n    Lit(value: int)\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.EnumDecl)
	if !ok {
		t.Fatalf("expected enum decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one enum annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "packed_profile" {
		t.Fatalf("expected packed_profile annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 1 || decl.Annotations[0].Args[0] != "build_heavy" {
		t.Fatalf("expected packed_profile(build_heavy), got %#v", decl.Annotations[0].Args)
	}
}
func TestParseInlineFunctionAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@inline(always)\ndef fold(value: int) -> int:\n    return value\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one function annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "inline" {
		t.Fatalf("expected inline annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 1 || decl.Annotations[0].Args[0] != "always" {
		t.Fatalf("expected inline(always), got %#v", decl.Annotations[0].Args)
	}
}
func TestParseBracketedAnnotationList(t *testing.T) {
	// `@[a, b(args)]` desugars to the same flat annotation list as stacking them per `@` line, and
	// composes with stacked annotations and arg-bearing entries.
	src := "@[noalloc, nolock, differential(256)]\n@inbounds\ndef f(x: int) -> int:\n    return x\n"
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	got := make([]string, 0, len(decl.Annotations))
	for _, a := range decl.Annotations {
		got = append(got, a.Name)
	}
	want := []string{"noalloc", "nolock", "differential", "inbounds"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("annotation %d: expected %q, got %q (full: %v)", i, want[i], got[i], got)
		}
	}
	// the arg-bearing entry keeps its args inside the bracket
	if a := decl.Annotations[2]; len(a.Args) != 1 || a.Args[0] != "256" {
		t.Fatalf("expected differential(256) inside the bracket, got %#v", a.Args)
	}
}
func TestParseNoRecurseFunctionAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@norecurse\ndef fold(value: int) -> int:\n    return value\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one function annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "norecurse" {
		t.Fatalf("expected norecurse annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 0 {
		t.Fatalf("expected norecurse to take no args, got %#v", decl.Annotations[0].Args)
	}
}
func TestParseHotFunctionAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@hot\ndef fold(value: int) -> int:\n    return value\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one function annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "hot" {
		t.Fatalf("expected hot annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 0 {
		t.Fatalf("expected hot to take no args, got %#v", decl.Annotations[0].Args)
	}
}
func TestParseGuardNonNullFunctionAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@guard_nonnull(box)\ndef has_box(box: Box&?) -> bool:\n    return box != null\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one function annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "guard_nonnull" {
		t.Fatalf("expected guard_nonnull annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 1 || decl.Annotations[0].Args[0] != "box" {
		t.Fatalf("expected guard_nonnull(box), got %#v", decl.Annotations[0].Args)
	}
}
func TestParseGuardVariantFunctionAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@guard_variant(node, Expr.Int)\ndef is_int(node: Expr) -> bool:\n    return node is Expr.Int\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one function annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "guard_variant" {
		t.Fatalf("expected guard_variant annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 2 || decl.Annotations[0].Args[0] != "node" || decl.Annotations[0].Args[1] != "Expr.Int" {
		t.Fatalf("expected guard_variant(node, Expr.Int), got %#v", decl.Annotations[0].Args)
	}
}
func TestParseLinkNameExternAnnotations(t *testing.T) {
	file, errs := parseSourceFile(t, "@link_name(cos)\nextern c_cos(x: f64) -> f64\n\n@link_name(errno)\nextern c_errno: int\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.ExternFuncDecl)
	if !ok {
		t.Fatalf("expected extern func decl, got %T", file.Decls[0])
	}
	if len(fn.Annotations) != 1 || fn.Annotations[0].Name != "link_name" {
		t.Fatalf("expected @link_name on extern func, got %#v", fn.Annotations)
	}
	if len(fn.Annotations[0].Args) != 1 || fn.Annotations[0].Args[0] != "cos" {
		t.Fatalf("expected @link_name(cos), got %#v", fn.Annotations[0].Args)
	}
	varDecl, ok := file.Decls[1].(*ast.ExternVarDecl)
	if !ok {
		t.Fatalf("expected extern var decl, got %T", file.Decls[1])
	}
	if len(varDecl.Annotations) != 1 || varDecl.Annotations[0].Name != "link_name" {
		t.Fatalf("expected @link_name on extern var, got %#v", varDecl.Annotations)
	}
	if len(varDecl.Annotations[0].Args) != 1 || varDecl.Annotations[0].Args[0] != "errno" {
		t.Fatalf("expected @link_name(errno), got %#v", varDecl.Annotations[0].Args)
	}
	formatted := unparse.FormatFile(file)
	expected := "@link_name(cos)\nextern c_cos(x: f64) -> f64\n\n@link_name(errno)\nextern c_errno: int\n"
	if formatted != expected {
		t.Fatalf("expected formatted extern annotations to round-trip, got:\n%s", formatted)
	}
}
