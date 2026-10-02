package semantic

import (
	"strings"
	"testing"
)

// A ref cast cannot lengthen the storage it borrows: returning `(&x).cast[static u8&]` of a
// frame scalar dangles whether or not the function opts into Unsafe.PointerCast.
func TestStaticCastOfLocalScalarReturnRejected(t *testing.T) {
	for _, grant := range []string{"", " can[Unsafe.PointerCast]"} {
		src := "def build() -> static u8&" + grant + ":\n    x: mutable u8 = 65\n    return (&x).cast[static u8&]\n"
		result := analyzeTreeTestSourceWithSemanticErrors(t, "static_cast_local_return.elisa", src)
		if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "returning a reference into function-local storage") {
			t.Fatalf("grant %q: expected the dangling static cast to be rejected; got:\n%s", grant, all)
		}
	}
}
