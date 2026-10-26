//go:build cgo

package semantic

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func projectSparseMapsReference(p *specializationPublication, result *Result) {
	c := p.context
	rv := reflect.ValueOf(result).Elem()
	av := reflect.ValueOf(c.worker).Elem()
	rootv := reflect.ValueOf(c.base).Elem()
	for _, entry := range sparseSpecializationMaps {
		dst := rv.FieldByName(entry.result)
		if entry.analyzer == "" {
			continue
		}
		source := settable(av.FieldByName(entry.analyzer))
		if !rootv.FieldByName(entry.result).IsNil() || !source.IsNil() {
			dst.Set(reflect.MakeMap(dst.Type()))
		} else {
			dst.Set(reflect.Zero(dst.Type()))
		}
	}
	for cursor := 0; cursor < len(p.queue); cursor++ {
		node := p.queue[cursor]
		nodeValue := reflect.ValueOf(node)
		for _, entry := range sparseSpecializationMaps {
			dst := rv.FieldByName(entry.result)
			if entry.analyzer == "" {
				continue
			}
			source := settable(av.FieldByName(entry.analyzer))
			if source.IsNil() || !nodeValue.Type().AssignableTo(source.Type().Key()) {
				continue
			}
			value := source.MapIndex(nodeValue)
			if !value.IsValid() {
				continue
			}
			if dst.IsNil() {
				dst.Set(reflect.MakeMap(dst.Type()))
			}
			dst.SetMapIndex(nodeValue, value)
			p.walk(value, true)
		}
	}
}

func specializationSparseProjectionFixture(t *testing.T) (*Result, *Analyzer, *specializationContext, *ast.FuncDecl) {
	t.Helper()
	root := analyzeTreeTestSource(t, "specialization_sparse_projection.elisa", `def first[T](value: i64) -> i64:
    return value + 1
`)
	target := testFuncDeclByName(t, root.File, "first")
	root.BeginSharedCodegen()
	if _, err := root.SpecializeFunction(target, []Type{root.NamedTypes["i64"]}); err != nil {
		root.EndSharedCodegen()
		t.Fatal(err)
	}
	worker := cloneAnalyzerValueForSpecialization(root.analyzer).Addr().Interface().(*Analyzer)
	isolateWorkerScopes(root.analyzer, worker)
	context := &specializationContext{base: root, worker: worker, syntax: newSpecializationSyntax(), declarations: map[*ast.FuncDecl]*ast.FuncDecl{}}
	worker.parallel = &parallelWorkerState{worker: 0, shadows: map[*ast.FuncDecl]*ast.FuncDecl{}, readShadows: map[*ast.FuncDecl]*ast.FuncDecl{}, original: map[*ast.FuncDecl]*ast.FuncDecl{}, specialization: context}
	private := context.copyDecl(target)
	if context.err != nil {
		worker.closeSMT()
		root.EndSharedCodegen()
		t.Fatal(context.err)
	}
	if private == nil || len(private.Body) == 0 {
		worker.closeSMT()
		root.EndSharedCodegen()
		t.Fatal("fixture has no specialized body")
	}
	// A fact-held synthetic call and its child force projection to grow the
	// owned queue while it runs, exercising transitive fact closure.
	binary := private.Body[0].(*ast.ReturnStmt).Value.(*ast.BinaryExpr)
	ident := &ast.Ident{Name: "fact_held"}
	synthetic := &ast.CallExpr{Func: ident, Args: []ast.Expr{&ast.IntLit{Value: "2"}}}
	if worker.lawIsCalls == nil {
		worker.lawIsCalls = map[*ast.BinaryExpr]*ast.CallExpr{}
	}
	worker.lawIsCalls[binary] = synthetic
	if worker.exprTypes == nil {
		worker.exprTypes = map[ast.Expr]Type{}
	}
	worker.exprTypes[synthetic] = root.NamedTypes["i64"]
	if worker.resolvedValueNames == nil {
		worker.resolvedValueNames = map[*ast.Ident]string{}
	}
	worker.resolvedValueNames[ident] = "Scoped.fact_held"
	return root, worker, context, private
}

func TestSparsePublicationPreboundProjectionMatchesReferenceClosure(t *testing.T) {
	root, worker, context, private := specializationSparseProjectionFixture(t)
	defer worker.closeSMT()
	defer root.EndSharedCodegen()
	candidate := newSpecializationPublication(context, private)
	reference := newSpecializationPublication(context, private)
	candidateResult, referenceResult := *root, *root
	candidate.projectSparseMaps(&candidateResult)
	projectSparseMapsReference(reference, &referenceResult)
	if !reflect.DeepEqual(candidate.owned, reference.owned) || !reflect.DeepEqual(candidate.queue, reference.queue) || fmt.Sprint(candidate.err) != fmt.Sprint(reference.err) {
		t.Fatal("prebound projection changed queue closure, owned syntax, or error state")
	}
	for _, entry := range sparseSpecializationMaps {
		got := reflect.ValueOf(&candidateResult).Elem().FieldByName(entry.result)
		want := reflect.ValueOf(&referenceResult).Elem().FieldByName(entry.result)
		if !reflect.DeepEqual(got.Interface(), want.Interface()) {
			t.Fatalf("prebound projection changed sparse table %s", entry.result)
		}
	}
	binary := private.Body[0].(*ast.ReturnStmt).Value.(*ast.BinaryExpr)
	synthetic := worker.lawIsCalls[binary]
	if _, present := candidateResult.ExprTypes[synthetic]; !present {
		t.Fatal("transitive fact-held type row was not projected")
	}
	var ident *ast.Ident
	for candidate, name := range worker.resolvedValueNames {
		if name == "Scoped.fact_held" {
			ident = candidate
			break
		}
	}
	if got, present := candidateResult.ResolvedValueNames[ident]; !present || got != "Scoped.fact_held" {
		t.Fatal("transitive fact-held name row was not projected")
	}
}

func specializationPublicationLargeBodySource(locals int) string {
	var source strings.Builder
	fmt.Fprintln(&source, "def large[T](seed: T) -> T:")
	for i := 0; i < locals; i++ {
		if i == 0 {
			fmt.Fprintln(&source, "    local_0: T = seed")
		} else {
			fmt.Fprintf(&source, "    local_%d: T = local_%d\n", i, i-1)
		}
	}
	if locals == 0 {
		fmt.Fprintln(&source, "    return seed")
	} else {
		fmt.Fprintf(&source, "    return local_%d\n", locals-1)
	}
	return source.String()
}

func specializationPublicationLargeBodyFixture(b *testing.B, locals int) (*Result, *Analyzer, *specializationContext, *ast.FuncDecl) {
	b.Helper()
	source := specializationPublicationLargeBodySource(locals)
	lex := lexer.New("specialization-publication-large.elisa", []byte(source))
	tokens := lex.Tokenize()
	if errs := lex.Errors(); len(errs) != 0 {
		b.Fatal(errs)
	}
	parse := parser.New(tokens)
	file := parse.ParseFile("specialization-publication-large.elisa")
	if errs := parse.Errors(); len(errs) != 0 {
		b.Fatal(errs)
	}
	root := Analyze(file)
	if errs := root.Errors(); len(errs) != 0 {
		b.Fatal(errs)
	}
	target := testFuncDeclByNameBenchmark(b, root.File, "large")
	args := []Type{root.NamedTypes["i64"]}
	root.BeginSharedCodegen()
	if _, err := root.SpecializeFunction(target, args); err != nil {
		root.EndSharedCodegen()
		b.Fatal(err)
	}
	worker := cloneAnalyzerValueForSpecialization(root.analyzer).Addr().Interface().(*Analyzer)
	isolateWorkerScopes(root.analyzer, worker)
	context := &specializationContext{base: root, worker: worker, syntax: newSpecializationSyntax(), declarations: map[*ast.FuncDecl]*ast.FuncDecl{}}
	worker.parallel = &parallelWorkerState{worker: 0, shadows: map[*ast.FuncDecl]*ast.FuncDecl{}, readShadows: map[*ast.FuncDecl]*ast.FuncDecl{}, original: map[*ast.FuncDecl]*ast.FuncDecl{}, specialization: context}
	private := context.copyDecl(target)
	if context.err != nil {
		worker.closeSMT()
		root.EndSharedCodegen()
		b.Fatal(context.err)
	}
	worker.analyzeFuncWithTypeArgs(private, args)
	for _, diagnostic := range worker.diagnostics {
		if diagnostic.Severity == DiagnosticSeverityError {
			worker.closeSMT()
			root.EndSharedCodegen()
			b.Fatal(diagnostic.String())
		}
	}
	return root, worker, context, private
}

func testFuncDeclByNameBenchmark(b *testing.B, file *ast.File, name string) *ast.FuncDecl {
	b.Helper()
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == name {
			return fn
		}
	}
	b.Fatalf("missing function %s", name)
	return nil
}

var specializationPublicationLargeBodySink *Result

var specializationSparseProjectionSink *Result

func BenchmarkSpecializationSparseProjectionReferenceVsPrebound(b *testing.B) {
	for _, locals := range []int{128, 512} {
		for _, reference := range []bool{true, false} {
			name := "prebound"
			if reference {
				name = "reference"
			}
			b.Run(fmt.Sprintf("%s/owned_locals_%d", name, locals), func(b *testing.B) {
				root, worker, context, private := specializationPublicationLargeBodyFixture(b, locals)
				defer worker.closeSMT()
				defer root.EndSharedCodegen()
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					publication := newSpecializationPublication(context, private)
					view := *root
					if reference {
						projectSparseMapsReference(publication, &view)
					} else {
						publication.projectSparseMaps(&view)
					}
					specializationSparseProjectionSink = &view
				}
			})
		}
	}
}

func BenchmarkSpecializationPublicationWarmedSharedLargeOwnedBody(b *testing.B) {
	for _, locals := range []int{128, 512} {
		b.Run(fmt.Sprintf("owned_locals_%d", locals), func(b *testing.B) {
			root, worker, context, private := specializationPublicationLargeBodyFixture(b, locals)
			defer worker.closeSMT()
			defer root.EndSharedCodegen()
			if root.shared.canonicalNodes == nil || root.shared.canonicalSymbols == nil || root.shared.canonicalScopes == nil {
				b.Fatal("canonical membership indexes were not warmed")
			}
			if _, err := newSpecializationPublication(context, private).publish(); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				publication := newSpecializationPublication(context, private)
				view, err := publication.publish()
				if err != nil {
					b.Fatal(err)
				}
				specializationPublicationLargeBodySink = view
			}
		})
	}
}
