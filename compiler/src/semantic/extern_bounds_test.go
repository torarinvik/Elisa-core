//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func externBoundsErrors(t *testing.T, name, src string) string {
	t.Helper()
	return strings.Join(analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name, src, AnalyzeOptions{}).Errors(), "\n")
}

// @bounds(buf, count) rewrites the Elisa-facing signature: the pointer becomes a bounded
// view and the length disappears, so the call passes one view. docs/127 §3.3 legacy form.
func TestExternBoundsRewritesSignature(t *testing.T) {
	src := `@callconv(c)
@bounds(text, cap)
extern strnlen(text: u8&, cap: usize) -> usize

def main() -> i64:
    t: mutable darray[u8] = []
    t.push(0)
    return strnlen(t[0:1]).i64()`
	if errs := externBoundsErrors(t, "bounds_ok.elisa", src); errs != "" {
		t.Fatalf("bounded call must analyze, got: %v", errs)
	}
}

// D6: a caller still passing the length gets told where it now comes from.
func TestExternBoundsRejectsExplicitLength(t *testing.T) {
	src := `@callconv(c)
@bounds(text, cap)
extern strnlen(text: u8&, cap: usize) -> usize

def main() -> i64:
    t: mutable darray[u8] = []
    t.push(0)
    return strnlen(t[0:1], 1).i64()`
	errs := externBoundsErrors(t, "bounds_d6.elisa", src)
	want := `function "strnlen" expects 1 arguments, got 2; the length parameter "cap" is supplied by @bounds from the view's count, so remove the explicit argument`
	if !strings.Contains(errs, want) {
		t.Fatalf("expected D6, got: %v", errs)
	}
}

func TestExternBoundsRejectsMalformed(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"nocc", "@bounds(p, n)\nextern f(p: u8&, n: usize) -> usize", `@bounds on extern function "f" requires @callconv(c)`},
		{"badptr", "@callconv(c)\n@bounds(p, n)\nextern f(p: usize, n: usize) -> usize", `pointer parameter "p" must be a non-optional reference`},
		{"badlen", "@callconv(c)\n@bounds(p, n)\nextern f(p: u8&, n: f32) -> usize", `length parameter "n" must be an integer`},
		{"mentions", "@callconv(c)\n@bounds(p, n)\nextern f(p: u8&, n: usize) -> usize requires n > 0", `contract names the length parameter "n", which @bounds supplies from p.count`},
		{"odd", "@callconv(c)\n@bounds(p)\nextern f(p: u8&, n: usize) -> usize", `expects pairs of parameter names`},
		{"unknown", "@callconv(c)\n@bounds(p, m)\nextern f(p: u8&, n: usize) -> usize", `names unknown parameter "m"`},
	} {
		errs := externBoundsErrors(t, "bounds_"+tc.name+".elisa", tc.src)
		if !strings.Contains(errs, tc.want) {
			t.Fatalf("%s: expected %q, got: %v", tc.name, tc.want, errs)
		}
	}
}
