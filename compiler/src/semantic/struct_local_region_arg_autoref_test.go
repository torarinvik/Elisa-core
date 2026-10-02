package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

const structLocalRegionArgStore = `module NameStore:
    struct Store:
        current: mutable darray[u8]

    def new_store() -> Store:
        return Store{current: []}

    def append[@r](store: mutable Store& @r, byte: u8) -> void:
        store.current.push(byte)

def append2(s: mutable NameStore::Store&) -> void:
    s.current.push(1)

`

// A struct VALUE passed to a `mutable T& @r` parameter used to be retyped as a
// region ref in place, with no address taken: a bare field argument
// (`p.names`) reached LLVM as a by-value load (invalid IR), and an immutable
// local was silently accepted as a mutable reference. The argument is now
// auto-referenced like any other `T&` argument and keeps its mutability.
func TestStructLocalRegionArgIsAutoReferenced(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "struct_local_region_arg_field.elisa", structLocalRegionArgStore+`struct Pool:
    names: mutable NameStore::Store
    tag: u8

def main() -> i32:
    p: mutable Pool = Pool{names: NameStore::new_store(), tag: 1}
    NameStore::append(p.names, 1)
    return 0
`)
	mainSym, ok := result.GlobalScope.Lookup("main")
	if !ok {
		t.Fatal("expected main symbol")
	}
	// main allocates, so its body is wrapped in the implicit auto region.
	body := mainSym.Node.(*ast.FuncDecl).Body
	if region, ok := body[0].(*ast.RegionStmt); ok && len(body) == 1 {
		body = region.Body
	}
	stmt := body[1].(*ast.ExprStmt)
	call := stmt.Expr.(*ast.CallExpr)
	if !call.ResolvedArgsValid || len(call.ResolvedArgs) != 2 {
		t.Fatalf("expected two resolved args, got %#v", call.ResolvedArgs)
	}
	addr, ok := call.ResolvedArgs[0].(*ast.AddrOfExpr)
	if !ok {
		t.Fatalf("expected field arg to be auto-referenced, got %T", call.ResolvedArgs[0])
	}
	ref, ok := result.ExprTypes[addr].(*RefType)
	if !ok || !ref.Mutable || ref.Region == "" {
		t.Fatalf("expected a mutable region ref for the auto-referenced arg, got %v", result.ExprTypes[addr])
	}
}

func TestStructLocalRegionArgImmutableLocalRejected(t *testing.T) {
	cases := map[string]string{
		"region_param": `def main() -> i32:
    q: NameStore::Store = NameStore::new_store()
    NameStore::append(q, 1)
    return 0
`,
		"inferred_region": `def main() -> i32:
    q: NameStore::Store = NameStore::new_store()
    append2(q)
    return 0
`,
	}
	for name, body := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "struct_local_region_arg_"+name+".elisa", structLocalRegionArgStore+body)
		errs := strings.Join(result.Errors(), "\n")
		if !strings.Contains(errs, "expects mutable NameStore.Store&") {
			t.Fatalf("%s: immutable local must not bind a mutable ref, got: %q", name, errs)
		}
	}
}
