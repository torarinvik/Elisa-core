//go:build cgo

package semantic

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func sparseTestKey(t *testing.T, typ reflect.Type) reflect.Value {
	t.Helper()
	if typ.Kind() == reflect.Pointer {
		return reflect.New(typ.Elem())
	}
	var node ast.Node = &ast.BoolLit{}
	if typ == reflect.TypeFor[ast.TypeExpr]() {
		node = &ast.NamedType{Name: "i64"}
	}
	value := reflect.ValueOf(node)
	if !value.Type().AssignableTo(typ) {
		t.Fatalf("no source key for %v", typ)
	}
	return value.Convert(typ)
}

func TestSparseSpecializationAllFactMapsKeepMembershipAndIteration(t *testing.T) {
	rt := reflect.TypeOf(Result{})
	for _, policy := range sparseSpecializationMaps {
		t.Run(policy.result, func(t *testing.T) {
			field, _ := rt.FieldByName(policy.result)
			original, private, foreign, oldMissing, privateMissing := sparseTestKey(t, field.Type.Key()), sparseTestKey(t, field.Type.Key()), sparseTestKey(t, field.Type.Key()), sparseTestKey(t, field.Type.Key()), sparseTestKey(t, field.Type.Key())
			root := &Result{}
			overlay := &Result{specializationRoot: root, specializationNodes: map[ast.Node]ast.Node{original.Interface().(ast.Node): private.Interface().(ast.Node), oldMissing.Interface().(ast.Node): privateMissing.Interface().(ast.Node)}, specializationOwned: map[ast.Node]bool{private.Interface().(ast.Node): true, privateMissing.Interface().(ast.Node): true}}
			baseMap := reflect.MakeMap(field.Type)
			zero := reflect.Zero(field.Type.Elem())
			baseMap.SetMapIndex(original, zero)
			baseMap.SetMapIndex(foreign, zero)
			baseMap.SetMapIndex(oldMissing, zero)
			reflect.ValueOf(root).Elem().FieldByName(policy.result).Set(baseMap)
			ownMap := reflect.MakeMap(field.Type)
			ownMap.SetMapIndex(private, zero)
			reflect.ValueOf(overlay).Elem().FieldByName(policy.result).Set(ownMap)
			lookup := reflect.ValueOf(overlay).MethodByName("Lookup" + policy.result)
			for _, key := range []reflect.Value{original, private, foreign} {
				got := lookup.Call([]reflect.Value{key})
				if !got[1].Bool() || !reflect.DeepEqual(got[0].Interface(), zero.Interface()) {
					t.Fatal("stored false/nil/zero observation lost membership")
				}
			}
			for _, key := range []reflect.Value{oldMissing, privateMissing} {
				if lookup.Call([]reflect.Value{key})[1].Bool() {
					t.Fatal("owned missing observation revived the template row")
				}
			}
			seen := map[ast.Node]bool{}
			callbackType := reflect.FuncOf([]reflect.Type{field.Type.Key(), field.Type.Elem()}, []reflect.Type{reflect.TypeFor[bool]()}, false)
			callback := reflect.MakeFunc(callbackType, func(args []reflect.Value) []reflect.Value {
				node := args[0].Interface().(ast.Node)
				if seen[node] {
					t.Fatal("logical iteration duplicated a key")
				}
				seen[node] = true
				return []reflect.Value{reflect.ValueOf(true)}
			})
			reflect.ValueOf(overlay).MethodByName("Range" + policy.result).Call([]reflect.Value{callback})
			if len(seen) != 3 || !seen[original.Interface().(ast.Node)] || !seen[private.Interface().(ast.Node)] || !seen[foreign.Interface().(ast.Node)] {
				t.Fatalf("incomplete logical iteration: %d", len(seen))
			}
			length := reflect.ValueOf(overlay).MethodByName(policy.result + "Len").Call(nil)[0].Int()
			if length != 3 {
				t.Fatalf("logical map length=%d want3", length)
			}
			calls := 0
			stop := reflect.MakeFunc(callbackType, func([]reflect.Value) []reflect.Value { calls++; return []reflect.Value{reflect.ValueOf(false)} })
			reflect.ValueOf(overlay).MethodByName("Range" + policy.result).Call([]reflect.Value{stop})
			if calls != 1 {
				t.Fatalf("early stop made %d callbacks", calls)
			}
		})
	}
}

func TestSparseSpecializationMemoRowsStayFunctionSized(t *testing.T) {
	var source strings.Builder
	source.WriteString("def bounce[T](value: T) -> T:\n    return value\n\ndef first[T](value: T) -> T:\n    return bounce(value)\n")
	for i := 0; i < 128; i++ {
		fmt.Fprintf(&source, "\ndef unrelated_%d(value: i64) -> i64:\n    return value + %d\n", i, i)
	}
	root := analyzeTreeTestSource(t, "sparse-memo.elisa", source.String())
	var fn *ast.FuncDecl
	for _, decl := range root.File.Decls {
		if candidate, ok := decl.(*ast.FuncDecl); ok && candidate.Name == "first" {
			fn = candidate
		}
	}
	if fn == nil {
		t.Fatal("missing first")
	}
	root.BeginSharedCodegen()
	defer root.EndSharedCodegen()
	for _, name := range []string{"i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64"} {
		product, err := root.SpecializeFunction(fn, []Type{root.NamedTypes[name]})
		if err != nil {
			t.Fatal(err)
		}
		view := product.Result
		if view.analyzer != nil {
			t.Fatal("memo product retained its construction Analyzer")
		}
		if view.specializationQueries == nil || view.specializationQueries.types == nil {
			t.Fatal("missing bounded query context")
		}
		if view.specializationQueries.types.parallel != nil || view.specializationQueries.types.retained != nil {
			t.Fatal("query context retained a construction owner")
		}
		if view.GlobalScope != root.GlobalScope {
			t.Fatal("product retained a cloned global scope")
		}
		for _, policy := range sparseSpecializationMaps {
			table := reflect.ValueOf(view).Elem().FieldByName(policy.result)
			if table.Len() > len(view.specializationOwned) {
				t.Fatalf("%s contains more rows than owned syntax", policy.result)
			}
			it := table.MapRange()
			for it.Next() {
				if !view.specializationOwned[it.Key().Interface().(ast.Node)] {
					t.Fatalf("%s retained an unrelated/canonical row", policy.result)
				}
			}
		}
		if len(view.ExprTypes) >= len(root.ExprTypes) {
			t.Fatal("per-function product retained whole-program expression map")
		}
		// Canonical foreign queries still work without materializing their rows.
		var foreign ast.Expr
		for expr := range root.ExprTypes {
			if view.specializationNodes[expr] == nil {
				foreign = expr
				break
			}
		}
		if foreign == nil {
			t.Fatal("fixture lacks unrelated canonical facts")
		}
		got, present := view.LookupExprTypes(foreign)
		if !present || got != root.ExprTypes[foreign] {
			t.Fatal("sparse publication lost canonical foreign fallback")
		}
	}
	if len(root.shared.bodies) != 8 {
		t.Fatalf("memo has%d products", len(root.shared.bodies))
	}
}

func TestSparseSpecializationRetractsOriginalProofAndScopesSpans(t *testing.T) {
	pos := lexer.Pos{File: "shared-default.elisa", Line: 3, Col: 4, Offset: 10, EndOffset: 11}
	old, current, other, detached := &ast.IndexExpr{Position: pos}, &ast.IndexExpr{Position: pos}, &ast.IndexExpr{Position: pos}, &ast.IndexExpr{Position: pos}
	root := &Result{IndexBoundsProven: map[*ast.IndexExpr]bool{old: true, other: true, detached: true}}
	view := &Result{specializationRoot: root, specializationNodes: map[ast.Node]ast.Node{old: current}, specializationOwned: map[ast.Node]bool{current: true}, specializationProofMask: map[*ast.IndexExpr]bool{old: true, detached: true}, IndexBoundsProven: map[*ast.IndexExpr]bool{}, specializationSpanProofs: map[*ast.IndexExpr]bool{}}
	if view.ValueIndexBoundsProven(old) || view.ValueIndexBoundsProven(current) || view.ValueIndexBoundsProven(detached) {
		t.Fatal("old target proof survived fresh owned miss")
	}
	if !view.ValueIndexBoundsProven(other) {
		t.Fatal("unrelated canonical proof was cleared")
	}
	count := 0
	view.RangeIndexProofSpanAuthority(func(*ast.IndexExpr, bool) bool { count++; return true })
	if count != 0 || view.IndexProofSpanCount() != 0 {
		t.Fatal("unrelated same-span proof became current rebuilt-node authority")
	}
}

func TestSparseSpecializationKeepsModuleContextAndStaticChoice(t *testing.T) {
	root := analyzeTreeTestSource(t, "sparse-module.elisa", `def add(value: i64) -> bool:
    return true
module Scoped:
    def add(value: i64) -> i64:
        return value + 5
    def generic[T](value: T) -> i64:
        static if false:
            return 0
        static else:
            return add(7)
`)
	symbol, ok := root.GlobalScope.Lookup("Scoped.generic")
	if !ok {
		t.Fatal("missing scoped generic")
	}
	fn := symbol.Node.(*ast.FuncDecl)
	product, err := root.SpecializeFunction(fn, []Type{root.NamedTypes["i64"]})
	if err != nil {
		t.Fatal(err)
	}
	stmt := product.Decl.Body[0].(*ast.StaticIfStmt)
	selected := product.Result.ActiveStaticStmtBranch(stmt)
	if len(selected) != 1 {
		t.Fatal("private static selection lost after scope teardown")
	}
	call := selected[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	// Direct callable resolution need not publish a ResolvedValueNames row.
	// Its concrete callee type is semantic authority; the global add above is
	// deliberately incompatible so an empty/wrong namespace cannot pass.
	calleeType, present := product.Result.LookupExprTypes(call.Func)
	callee, callable := calleeType.(*FuncType)
	if !present || !callable || callee == nil || callee.Name != "Scoped.add" {
		t.Fatalf("concrete reanalysis lost declaring module: callee=%v present=%v", calleeType, present)
	}
	typ, present := product.Result.LookupExprTypes(call)
	if !present || IsInvalidType(typ) || !SameType(typ, root.NamedTypes["i64"]) {
		t.Fatal("private module call has no concrete semantic type")
	}
}

func TestSparsePublicationClosesFactHeldSyntheticSyntax(t *testing.T) {
	root := analyzeTreeTestSource(t, "sparse-synthetic.elisa", `def first[T](value: i64) -> i64:
    return value + 1
`)
	fn := root.File.Decls[0].(*ast.FuncDecl)
	worker := cloneAnalyzerValueForBodyWorker(root.analyzer).Addr().Interface().(*Analyzer)
	isolateWorkerScopes(root.analyzer, worker)
	context := &specializationContext{base: root, worker: worker, syntax: newSpecializationSyntax(), declarations: map[*ast.FuncDecl]*ast.FuncDecl{}}
	worker.parallel = &parallelWorkerState{worker: 0, shadows: map[*ast.FuncDecl]*ast.FuncDecl{}, readShadows: map[*ast.FuncDecl]*ast.FuncDecl{}, original: map[*ast.FuncDecl]*ast.FuncDecl{}, specialization: context}
	private := context.copyDecl(fn)
	if context.err != nil {
		t.Fatal(context.err)
	}
	binary := private.Body[0].(*ast.ReturnStmt).Value.(*ast.BinaryExpr)
	ident := &ast.Ident{Name: "fact_held"}
	synthetic := &ast.CallExpr{Func: ident, Args: []ast.Expr{&ast.IntLit{Value: "2"}}}
	// Controlled semantic observations exercise the publication boundary. The
	// call is held only in another fact value, not in the function source AST.
	if worker.lawIsCalls == nil {
		worker.lawIsCalls = map[*ast.BinaryExpr]*ast.CallExpr{}
	}
	worker.lawIsCalls[binary] = synthetic
	worker.exprTypes[synthetic] = root.NamedTypes["i64"]
	worker.resolvedValueNames[ident] = "Scoped.fact_held"
	view, err := newSpecializationPublication(context, private).publish()
	if err != nil {
		t.Fatal(err)
	}
	if got, present := view.LookupLawIsCalls(binary); !present || got != synthetic {
		t.Fatal("primary fact did not survive publication")
	}
	if got, present := view.LookupExprTypes(synthetic); !present || got != root.NamedTypes["i64"] {
		t.Fatal("fact-held synthesized expression lost its dependent type")
	}
	if got, present := view.LookupResolvedValueNames(ident); !present || got != "Scoped.fact_held" {
		t.Fatal("transitive synthetic child lost its dependent name resolution")
	}
	if !view.specializationOwned[synthetic] || !view.specializationOwned[ident] {
		t.Fatal("fact graph was not closed to a fixed point")
	}
}
