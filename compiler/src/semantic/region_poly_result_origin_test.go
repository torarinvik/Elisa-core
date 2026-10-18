package semantic

import (
	"strings"
	"testing"
)

func TestOutParamOnlyRegionPolyStaticResultKeepsStaticOrigin(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rp_outparam_global_result.elisa", `packed enum Expr:
    Int(value: i64)
    Pair(left: Expr, right: Expr)

struct Packet:
    body: sview

def write_handle_and_return_static(out: mutable Expr&, node: Expr) -> Packet:
    out <- node
    return Packet{body: "stable"}

def store_static_result(out: mutable Expr&, node: Expr) -> i64:
    kept: mutable Packet = Packet{body: ""}
    region inner(4096):
        kept <- write_handle_and_return_static(out, node)
    return kept.body[0].i64() - 115
`, AnalyzeOptions{})
	if !regionPolymorphicFuncType(t, result, "write_handle_and_return_static").RegionPolymorphic {
		t.Fatal("the fixture must exercise region polymorphism caused by a packed-handle out write")
	}
	if got := strings.Join(result.Errors(), "\n"); got != "" {
		t.Fatalf("a static result must not be assigned the unrelated hidden caller arena, got:\n%s", got)
	}
}

func TestOutParamOnlyRegionPolyResultKeepsItsSourceRegion(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "rp_outparam_param_result.elisa", `packed enum Expr:
    Int(value: i64)
    Pair(left: Expr, right: Expr)

def write_handle_and_return_source(out: mutable Expr&, node: Expr, source: darray[i64]) -> darray[i64]:
    out <- node
    return source

def append_result[@source, @other](out: mutable Expr&, node: Expr, source: darray[i64] @source, dst: mutable darray[darray[i64]]& @source, other: mutable darray[darray[i64]]& @other) -> void:
    out <- node
    row: darray[i64] = write_handle_and_return_source(out, node, source)
    dst.push(row)
    other.push(row)
`)
	if !regionPolymorphicFuncType(t, result, "write_handle_and_return_source").RegionPolymorphic {
		t.Fatal("the producer must be region-polymorphic because it writes a packed-handle out parameter")
	}
	if !regionPolymorphicFuncType(t, result, "append_result").RegionPolymorphic {
		t.Fatal("the caller must exercise a hidden arena independently of its explicit target regions")
	}
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "rp_outparam_param_result.elisa:13:") ||
		strings.Contains(errs, "rp_outparam_param_result.elisa:12:") ||
		!strings.Contains(errs, `region "source"`) || !strings.Contains(errs, `region "other"`) {
		t.Fatalf("the result must retain @source: storing into @source is safe, while storing into @other is not; got:\n%s", errs)
	}
}
