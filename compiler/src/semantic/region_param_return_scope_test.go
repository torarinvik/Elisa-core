package semantic

import (
	"strings"
	"testing"
)

// The grown-container prepass must resolve a callee's return annotation in the
// callee's module context. Resolving it in the caller's ambient context makes
// `make() -> Type` report a false unknown-type error when Type is imported.
func TestGrownContainerPrepassUsesCalleeReturnScope(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "callee_return_scope.elisa", `module Model:
    struct Type:
        value: i64

using Model
module Check:
    def make() -> Type:
        return Type{value: 1}
    def grow(out: mutable darray[Type]&):
        out.push(make())
`).Errors(), " | ")
	if errs != "" {
		t.Fatalf("callee return annotations must resolve in their declaration scope, got: %s", errs)
	}
}
