package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestSMTArrayTermRejectsDatatypeProjectionToken(t *testing.T) {
	tr := (&Analyzer{}).newSMTTranslator(nil)
	ident := &ast.Ident{Name: "view"}
	field := &ast.FieldExpr{Object: &ast.Ident{Name: "record"}, Field: "view"}
	for _, tc := range []struct {
		expr ast.Expr
		env  map[string]string
	}{
		{ident, map[string]string{"view": dtToken("@local$view")}},
		{field, map[string]string{"record.view": dtToken("@local$view")}},
	} {
		if term, ok := tr.arrayTermEnv(tc.expr, tc.env); ok {
			t.Fatalf("datatype marker leaked into SMT array term %q", term)
		}
	}
	if term, ok := tr.arrayTermEnv(ident, map[string]string{"view": "v_valid_array"}); !ok || term != "v_valid_array" {
		t.Fatalf("valid array binding rejected: %q, %v", term, ok)
	}
	bad := map[string]string{"view": dtToken("@callproj$0")}
	if term, ok := tr.termEnv(ident, bad); ok {
		t.Fatalf("datatype marker leaked into scalar term %q", term)
	}
	if term, ok := tr.boolTerm(ident, bad); ok {
		t.Fatalf("datatype marker leaked into boolean term %q", term)
	}
}
