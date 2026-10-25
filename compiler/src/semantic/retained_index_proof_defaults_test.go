package semantic

import (
	"elisacore/src/ast"
	"strings"
	"testing"
)

const retainedProofNamedDefaultSource = `global values: i32[2] = [11, 22]
def consume(value: i32 = values[0]) -> i32:
    return value
def use() -> i32:
    return consume()
`

const retainedProofExternDefaultSource = `global values: i32[2] = [11, 22]
extern consume(value: i32 = values[0]) -> i32
def use() -> i32:
    return consume()
`

const retainedProofStructBodyDefaultSource = `global values: i32[2] = [11, 22]
struct Holder:
    value: i32 = values[0]
def build() -> i32:
    holder: Holder = Holder{}
    return holder.value
`

const retainedProofStructGlobalDefaultSource = `global values: i32[2] = [11, 22]
struct Holder:
    value: i32 = values[0]
global holder: Holder = Holder{}
def build() -> i32:
    return holder.value
`

const retainedProofStructHeaderDefaultSource = `global values: i32[2] = [11, 22]
struct Holder:
    value: i32 = values[0]
def consume(holder: Holder = Holder{}) -> i32:
    return holder.value
def build() -> i32:
    return consume()
`

const retainedProofGlobalInitializerSource = `global values: i32[2] = [11, 22]
global selected: i32 = values[0]
def use() -> i32:
    return values[1]
`

const retainedProofConstInitializerSource = `const values: i32[2] = [11, 22]
const selected: i32 = values[0]
def use() -> i32:
    return selected
`

const retainedProofLambdaSource = `def read() -> fn(darray[i32]&, usize) -> i32:
    return fn(values: darray[i32]&, index: usize) -> i32:
        trusted Unsafe.UncheckedIndex:
            if index < values.count:
                return values[index]
        return 0
`

func retainedProofNamedDecl(t *testing.T, result *Result, name string) ast.Node {
	t.Helper()
	sym, ok := result.GlobalScope.Lookup(name)
	if !ok || sym == nil || sym.Node == nil {
		t.Fatalf("declaration %s not found", name)
	}
	return sym.Node
}

func TestRetainedIndexedParameterDefaultsKeepExactHeaderOwner(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	for _, fixture := range []struct{ name, source string }{{"named", retainedProofNamedDefaultSource}, {"extern", retainedProofExternDefaultSource}} {
		t.Run(fixture.name, func(t *testing.T) {
			src := fixture.source
			result, state := AnalyzeRetained(retainedParse(t, src), retainedParse(t, src), []byte(src), AnalyzeOptions{})
			if len(result.Errors()) != 0 {
				t.Fatalf("actual default fixture rejected: %v", result.Errors())
			}
			owner := retainedProofNamedDecl(t, result, "consume")
			var index *ast.IndexExpr
			switch decl := owner.(type) {
			case *ast.FuncDecl:
				index, _ = decl.Params[0].DefaultValue.(*ast.IndexExpr)
			case *ast.ExternFuncDecl:
				index, _ = decl.Params[0].DefaultValue.(*ast.IndexExpr)
			}
			if index == nil || !result.IndexBoundsProven[index] {
				t.Fatal("actual indexed parameter default not proved")
			}
			if state.a.indexBoundsProofOwners[index] != owner || !state.a.indexBoundsProofHeaders[index] {
				t.Fatal("parameter default lacks exact header declaration ownership")
			}
			data, err := state.MarshalState()
			if err != nil {
				t.Fatal(err)
			}
			state, err = RestoreAnalysisState(data)
			if err != nil {
				t.Fatal(err)
			}
			old, ok := state.ResultForSource([]byte(src))
			if !ok {
				t.Fatal("restored source missing")
			}
			owner = retainedProofNamedDecl(t, old, "consume")
			switch decl := owner.(type) {
			case *ast.FuncDecl:
				index = decl.Params[0].DefaultValue.(*ast.IndexExpr)
			case *ast.ExternFuncDecl:
				index = decl.Params[0].DefaultValue.(*ast.IndexExpr)
			}
			next := strings.Replace(src, "return consume()", "return consume() + 0i32", 1)
			updated, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next))
			if err != nil || len(updated.Errors()) != 0 {
				t.Fatalf("body edit with valid indexed default declined/rejected: %v", err)
			}
			if retainedProofNamedDecl(t, updated, "consume") != owner || !updated.IndexBoundsProven[index] || state.a.indexBoundsProofOwners[index] != owner || !state.a.indexBoundsProofHeaders[index] {
				t.Fatal("body-only edit discarded or reclassified an unchanged header proof")
			}
		})
	}
}

func TestRetainedIndexedStructDefaultBelongsToConstructionBody(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	src := retainedProofStructBodyDefaultSource
	result, state := AnalyzeRetained(retainedParse(t, src), retainedParse(t, src), []byte(src), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	fn := retainedProofNamedDecl(t, result, "build").(*ast.FuncDecl)
	old := []*ast.IndexExpr{}
	for expr, proven := range result.IndexBoundsProven {
		if !proven {
			continue
		}
		if state.a.indexBoundsProofOwners[expr] != fn || state.a.indexBoundsProofHeaders[expr] {
			t.Fatal("struct-default clone escaped the construction body's lifetime")
		}
		old = append(old, expr)
	}
	if len(old) == 0 {
		t.Fatal("actual struct default produced no bounds proof")
	}
	next := strings.Replace(src, "value: i32 = values[0]", "value: i32 = values[1]", 1)
	updated, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next))
	if err != nil || len(updated.Errors()) != 0 {
		t.Fatalf("actual body-owned struct default edit declined/rejected: %v", err)
	}
	for _, expr := range old {
		if updated.IndexBoundsProven[expr] {
			t.Fatal("old struct-default clone proof survived accepted caller reanalysis")
		}
	}
	count := 0
	for expr, proven := range updated.IndexBoundsProven {
		if proven && state.a.indexBoundsProofOwners[expr] == fn {
			count++
		}
	}
	if count == 0 {
		t.Fatal("new struct-default clone did not regain its valid proof")
	}
}

func TestRetainedStructDefaultNonBodyConsumerDeclinesBeforeMutation(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	for _, fixture := range []struct{ name, source string }{{"global", retainedProofStructGlobalDefaultSource}, {"header", retainedProofStructHeaderDefaultSource}} {
		t.Run(fixture.name, func(t *testing.T) {
			src := fixture.source
			result, state := AnalyzeRetained(retainedParse(t, src), retainedParse(t, src), []byte(src), AnalyzeOptions{})
			if len(result.Errors()) != 0 {
				t.Fatal(result.Errors())
			}
			next := strings.Replace(src, "value: i32 = values[0]", "value: i32 = values[1]", 1)
			cold := AnalyzeWithOptions(retainedParse(t, next), AnalyzeOptions{})
			if len(cold.Errors()) != 0 {
				t.Fatal("edited default must remain cold-valid", cold.Errors())
			}
			if _, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next)); err == nil || !strings.Contains(err.Error(), "struct default") {
				t.Fatalf("unsupported non-body default consumer was not rejected explicitly: %v", err)
			}
			if current, ok := state.ResultForSource([]byte(src)); !ok || current != result {
				t.Fatal("default admission failure mutated the retained revision")
			}
		})
	}
}

func TestRetainedGlobalInitializerProofHasDeclarationLifetime(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	src := retainedProofGlobalInitializerSource
	result, state := AnalyzeRetained(retainedParse(t, src), retainedParse(t, src), []byte(src), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	owner := retainedProofNamedDecl(t, result, "selected")
	old := []*ast.IndexExpr{}
	for expr, proven := range result.IndexBoundsProven {
		if proven && state.a.indexBoundsProofOwners[expr] == owner {
			old = append(old, expr)
		}
	}
	if len(old) == 0 {
		t.Fatal("global initializer proof lacks its exact declaration owner")
	}
	next := strings.Replace(src, "global selected: i32 = values[0]\n", "", 1)
	updated, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next))
	if err != nil || len(updated.Errors()) != 0 {
		t.Fatalf("unused global removal declined/rejected: %v", err)
	}
	for _, expr := range old {
		if updated.IndexBoundsProven[expr] {
			t.Fatal("removed global's proof survived")
		}
	}
}

func TestRetainedConstInitializerProofRetractsOnReplacement(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	src := retainedProofConstInitializerSource
	result, state := AnalyzeRetained(retainedParse(t, src), retainedParse(t, src), []byte(src), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	owner := retainedProofNamedDecl(t, result, "selected")
	old := []*ast.IndexExpr{}
	for expr, proven := range result.IndexBoundsProven {
		if proven && state.a.indexBoundsProofOwners[expr] == owner {
			old = append(old, expr)
		}
	}
	if len(old) == 0 {
		t.Fatal("const initializer proof lacks its exact declaration owner")
	}
	next := strings.Replace(src, "const selected: i32 = values[0]", "const selected: i32 = values[1]", 1)
	updated, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next))
	if err != nil || len(updated.Errors()) != 0 {
		t.Fatalf("typed const value edit declined/rejected: %v", err)
	}
	for _, expr := range old {
		if updated.IndexBoundsProven[expr] {
			t.Fatal("replaced const initializer's proof survived")
		}
	}
}

func TestRetainedLambdaProofFollowsEnclosingSourceFunction(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	src := retainedProofLambdaSource
	result, state := AnalyzeRetained(retainedParse(t, src), retainedParse(t, src), []byte(src), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	fn := retainedProofNamedDecl(t, result, "read").(*ast.FuncDecl)
	old := []*ast.IndexExpr{}
	for expr, proven := range result.IndexBoundsProven {
		if !proven {
			continue
		}
		if state.a.indexBoundsProofOwners[expr] != fn {
			t.Fatal("lambda proof belongs to a synthetic/global owner rather than its retained source function")
		}
		old = append(old, expr)
	}
	if len(old) == 0 {
		t.Fatal("actual guarded lambda access not proved")
	}
	next := strings.Replace(src, "if index < values.count", "if index > values.count", 1)
	updated, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next))
	if err != nil || len(updated.Errors()) != 0 {
		t.Fatalf("actual lambda body edit declined/rejected: %v", err)
	}
	for _, expr := range old {
		if updated.IndexBoundsProven[expr] {
			t.Fatal("old lambda proof survives enclosing function invalidation")
		}
	}
	cold := AnalyzeWithOptions(retainedParse(t, next), AnalyzeOptions{})
	if len(cold.Errors()) != 0 || len(cold.IndexBoundsProven) != 0 || len(updated.IndexBoundsProven) != 0 {
		t.Fatalf("unproved lambda access differs from cold: cold errors=%v cold proofs=%d warm proofs=%d", cold.Errors(), len(cold.IndexBoundsProven), len(updated.IndexBoundsProven))
	}
}

func TestRetainedMultipleChangedFunctionsDoNotLeaveWorkerProofs(t *testing.T) {
	// Initial analysis must use workers. An update with two changed functions
	// could also fork if the retainedProofsScoped serial guard regressed; worker
	// map merges cannot propagate proof deletions back to the retained root.
	t.Setenv("ELISA_PARALLEL_BODIES", "4")
	template := `def NAME(xs: darray[i32]&, i: usize) -> i32:
    trusted Unsafe.UncheckedIndex:
        if i < xs.count:
            return xs[i]
        return 0

`
	src := ""
	for _, name := range []string{"first", "second", "third", "fourth"} {
		src += strings.Replace(template, "NAME", name, 1)
	}
	result, state := AnalyzeRetained(retainedParse(t, src), retainedParse(t, src), []byte(src), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	old := map[string]*ast.IndexExpr{}
	for _, decl := range result.File.Decls {
		fn := decl.(*ast.FuncDecl)
		index := fn.Body[0].(*ast.CanStmt).Body[0].(*ast.IfStmt).Then[0].(*ast.ReturnStmt).Value.(*ast.IndexExpr)
		if !result.IndexBoundsProven[index] {
			t.Fatalf("initial %s proof missing", fn.Name)
		}
		old[fn.Name] = index
	}
	next := strings.Replace(src, "if i < xs.count:", "if i > xs.count:", 2)
	updated, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next))
	if err != nil || len(updated.Errors()) != 0 || state.LastStats.Changed != 2 {
		t.Fatalf("two-body retained edit failed: err=%v stats=%+v", err, state.LastStats)
	}
	for _, name := range []string{"first", "second"} {
		if _, present := updated.IndexBoundsProven[old[name]]; present {
			t.Fatalf("%s detached proof survived; worker deletion did not reach retained root", name)
		}
	}
	for _, name := range []string{"third", "fourth"} {
		if !updated.IndexBoundsProven[old[name]] {
			t.Fatalf("unchanged %s proof was cleared", name)
		}
	}
	for expr, proven := range updated.IndexBoundsProven {
		if !proven {
			continue
		}
		fn, ok := state.a.indexBoundsProofOwners[expr].(*ast.FuncDecl)
		if !ok || (fn.Name != "third" && fn.Name != "fourth") {
			t.Fatal("changed-function proof remains after two-body reanalysis")
		}
	}
}

func TestRetainedExistingGlobalInitializerEditRemainsUnsupported(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	src := retainedProofGlobalInitializerSource
	result, state := AnalyzeRetained(retainedParse(t, src), retainedParse(t, src), []byte(src), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	next := strings.Replace(src, "global selected: i32 = values[0]", "global selected: i32 = values[1]", 1)
	cold := AnalyzeWithOptions(retainedParse(t, next), AnalyzeOptions{})
	if len(cold.Errors()) != 0 {
		t.Fatal("edited global fixture must be cold-valid", cold.Errors())
	}
	if _, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next)); err == nil || !strings.Contains(err.Error(), "declaration") {
		t.Fatalf("existing global edit unexpectedly entered unsupported initializer update path: %v", err)
	}
	if current, ok := state.ResultForSource([]byte(src)); !ok || current != result {
		t.Fatal("existing global rejection changed retained revision")
	}
}

func TestRetainedNamedIndexedDefaultReplacementRetractsOldHeaderProof(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	src := retainedProofNamedDefaultSource
	result, state := AnalyzeRetained(retainedParse(t, src), retainedParse(t, src), []byte(src), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	owner := retainedProofNamedDecl(t, result, "consume").(*ast.FuncDecl)
	index := owner.Params[0].DefaultValue.(*ast.IndexExpr)
	if !result.IndexBoundsProven[index] {
		t.Fatal("actual old indexed default not proved")
	}
	next := strings.Replace(src, "value: i32 = values[0]", "value: i32 = values[1]", 1)
	updated, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next))
	if err != nil || len(updated.Errors()) != 0 {
		t.Fatalf("actual indexed default signature replacement declined/rejected: %v", err)
	}
	newOwner := retainedProofNamedDecl(t, updated, "consume").(*ast.FuncDecl)
	if newOwner == owner {
		t.Fatal("signature replacement path did not remove/redeclare function")
	}
	if updated.IndexBoundsProven[index] {
		t.Fatal("replaced header retained its old proof")
	}
	freshIndex := newOwner.Params[0].DefaultValue.(*ast.IndexExpr)
	if !updated.IndexBoundsProven[freshIndex] || state.a.indexBoundsProofOwners[freshIndex] != newOwner || !state.a.indexBoundsProofHeaders[freshIndex] {
		t.Fatal("new indexed header did not retain its correct proof and role")
	}
}
