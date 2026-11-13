package semantic

import (
	"strings"
	"testing"
)

func TestLocalArrayViewsCannotEscapeReturn(t *testing.T) {
	cases := map[string]string{
		"direct":       "def leak() -> view[i64]:\n    scratch: mutable i64[4] = zeroed\n    return scratch[0:2]\n",
		"binding":      "def leak() -> view[i64]:\n    a: i64[4] = [1, 2, 3, 4]\n    v: view[i64] = a[0:4]\n    return v\n",
		"struct_field": "struct S:\n    buf: u8[4]\ndef leak() -> view[u8]:\n    s: S = S{buf: [104, 105, 0, 0]}\n    return s.buf[0:2]\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeTreeTestSourceWithSemanticErrors(t, name+".elisa", src)
			if got := strings.Join(result.Errors(), "\n"); !strings.Contains(got, "function-local storage") {
				t.Fatalf("expected local storage escape diagnostic, got: %s", got)
			}
		})
	}
}
