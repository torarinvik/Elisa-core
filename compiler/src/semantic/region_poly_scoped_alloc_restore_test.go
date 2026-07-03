//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

// A scoped region (`region NAME:`, including the compiler-synthesized `__auto_*`
// body/loop wraps) must NOT leave its name as the ambient allocation scope after
// the block closes. analyzeScopedArenaStmt sets currentAllocExpr (via
// analyzeRegionDecl) but must restore it on exit — otherwise a region-poly call
// AFTER a nested scoped region (e.g. a loop-tightened per-iteration region)
// threads the inner region instead of the enclosing one. That mis-threaded
// region name is out of scope at the real call site, so the backend fails with
// `unknown identifier "__auto_N" during LLVM lowering` (surfaced wiring up the
// Elisa JetBrains LSP client, Elisa-LSP a564a28). Here we assert the semantic
// invariant directly: two region-poly calls straddling a scoped loop region
// thread DIFFERENT regions (pre-fix they wrongly thread the same loop region).
func TestScopedRegionRestoresAmbientAllocForLaterRegionPolyCall(t *testing.T) {
	src := `def build_pair(a: i64, b: i64) -> darray[i64]:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[i64] = []
        out.push(a)
        out.push(b)
        return out

def caller(items: darray[i64]) -> i64:
    can Memory.Allocate, Abort.Panic:
        total: mutable i64 = 0
        for x in items:
            leaf: darray[i64] = build_pair(x, x)
            total <- total + leaf[0]
        result: darray[i64] = build_pair(1, 2)
        return total + result[0]
`
	result := analyzeTreeTestSource(t, "region_poly_scoped_alloc_restore.elisa", src)

	// Collect, in source order, the threaded region-arg name of every call to
	// build_pair (region-polymorphic: its result region is inferred/threaded).
	var regionArgs []string
	forEachCallExpr(reflect.ValueOf(result.File), func(call *ast.CallExpr) {
		if id, ok := call.Func.(*ast.Ident); !ok || id.Name != "build_pair" {
			return
		}
		for _, arg := range call.ResolvedImplicitArgs {
			if ident, ok := arg.(*ast.Ident); ok {
				regionArgs = append(regionArgs, ident.Name)
			}
		}
	})

	if len(regionArgs) < 2 {
		t.Fatalf("expected >=2 threaded region args for build_pair calls, got %d: %v", len(regionArgs), regionArgs)
	}
	inLoop := regionArgs[0]
	afterLoop := regionArgs[len(regionArgs)-1]
	if inLoop == afterLoop {
		t.Fatalf("region-poly call after a scoped loop region threaded the SAME region %q as the in-loop call: "+
			"analyzeScopedArenaStmt failed to restore currentAllocExpr (the __auto_N lowering bug). all args: %v",
			inLoop, regionArgs)
	}
}

// forEachCallExpr walks the AST (reflection over pointers/structs/slices/
// interfaces) invoking fn for every *ast.CallExpr, in field/source order.
func forEachCallExpr(v reflect.Value, fn func(*ast.CallExpr)) {
	seen := map[uintptr]bool{}
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Ptr:
			if v.IsNil() {
				return
			}
			if p := v.Pointer(); seen[p] {
				return
			} else {
				seen[p] = true
			}
			if call, ok := v.Interface().(*ast.CallExpr); ok {
				fn(call)
			}
			walk(v.Elem())
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				f := v.Field(i)
				if f.CanInterface() {
					walk(f)
				}
			}
		}
	}
	walk(v)
}
