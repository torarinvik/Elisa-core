//go:build cgo

package semantic

import (
	"maps"
	"reflect"
	"strconv"
	"testing"

	"elisacore/src/ast"
)

var specializationStoreWriteCacheCloneSink *Analyzer

func specializationStoreWriteCacheContext(source *Analyzer) (*Analyzer, *specializationContext) {
	worker := cloneAnalyzerValueForSpecialization(source).Addr().Interface().(*Analyzer)
	context := &specializationContext{
		base:         &Result{analyzer: source},
		worker:       worker,
		syntax:       newSpecializationSyntax(),
		declarations: map[*ast.FuncDecl]*ast.FuncDecl{},
	}
	worker.parallel = &parallelWorkerState{
		worker:         0,
		shadows:        map[*ast.FuncDecl]*ast.FuncDecl{},
		readShadows:    map[*ast.FuncDecl]*ast.FuncDecl{},
		original:       map[*ast.FuncDecl]*ast.FuncDecl{},
		specialization: context,
	}
	return worker, context
}

func TestSpecializationWriteCachesStartEmptyAndPreserveNilness(t *testing.T) {
	call := &ast.CallExpr{}
	decl := &ast.FuncDecl{Name: "foreign"}
	populated := &Analyzer{
		storeScanCallWriteCache: map[*ast.CallExpr][]storeScanCallWrite{
			call: {{local: "local"}},
		},
		storageViewCalleeWriteCache: map[storageViewCalleeWriteKey]storageViewCalleeWrites{
			{decl: decl, param: 0}: {Fields: map[string]bool{"left": true}},
		},
	}
	worker := cloneAnalyzerValueForSpecialization(populated).Addr().Interface().(*Analyzer)
	if worker.storeScanCallWriteCache == nil || len(worker.storeScanCallWriteCache) != 0 {
		t.Fatalf("specialization retained store-scan call rows: %#v", worker.storeScanCallWriteCache)
	}
	if worker.storageViewCalleeWriteCache == nil || len(worker.storageViewCalleeWriteCache) != 0 {
		t.Fatalf("specialization retained storage-view rows: %#v", worker.storageViewCalleeWriteCache)
	}
	if len(populated.storeScanCallWriteCache) != 1 || len(populated.storageViewCalleeWriteCache) != 1 || !populated.storageViewCalleeWriteCache[storageViewCalleeWriteKey{decl: decl, param: 0}].Fields["left"] {
		t.Fatal("empty specialization clone changed canonical caches")
	}

	nilWorker := cloneAnalyzerValueForSpecialization(&Analyzer{}).Addr().Interface().(*Analyzer)
	if nilWorker.storeScanCallWriteCache != nil || nilWorker.storageViewCalleeWriteCache != nil {
		t.Fatal("nil cache shape changed in specialization clone")
	}
	emptySource := &Analyzer{
		storeScanCallWriteCache:     map[*ast.CallExpr][]storeScanCallWrite{},
		storageViewCalleeWriteCache: map[storageViewCalleeWriteKey]storageViewCalleeWrites{},
	}
	emptyWorker := cloneAnalyzerValueForSpecialization(emptySource).Addr().Interface().(*Analyzer)
	if emptyWorker.storeScanCallWriteCache == nil || len(emptyWorker.storeScanCallWriteCache) != 0 || emptyWorker.storageViewCalleeWriteCache == nil || len(emptyWorker.storageViewCalleeWriteCache) != 0 {
		t.Fatal("non-nil empty cache shape changed in specialization clone")
	}

	// The ordinary body-worker policy is intentionally unchanged: both caches
	// remain isolated, including the nested storage-view field sets.
	bodyWorker := cloneAnalyzerValueForBodyWorker(populated).Addr().Interface().(*Analyzer)
	if len(bodyWorker.storeScanCallWriteCache) != 1 || bodyWorker.storeScanCallWriteCache[call][0].local != "local" || cap(bodyWorker.storeScanCallWriteCache[call]) != len(bodyWorker.storeScanCallWriteCache[call]) {
		t.Fatal("body-worker call-plan cloning changed")
	}
	key := storageViewCalleeWriteKey{decl: decl, param: 0}
	bodyWorker.storageViewCalleeWriteCache[key].Fields["worker-only"] = true
	if populated.storageViewCalleeWriteCache[key].Fields["worker-only"] {
		t.Fatal("body-worker storage-view mutation reached canonical nested field set")
	}
}

func TestSpecializationStoreScanCallWriteCacheImportsOnlyOwnedPlans(t *testing.T) {
	firstValue := &ast.FieldExpr{Object: &ast.Ident{Name: "source"}, Field: "value"}
	firstArg := &ast.Ident{Name: "target"}
	first := &ast.CallExpr{Func: &ast.Ident{Name: "store"}, Args: []ast.Expr{firstArg, firstValue}}
	nilPlanCall := &ast.CallExpr{Func: &ast.Ident{Name: "no_writes"}}
	emptyPlanCall := &ast.CallExpr{Func: &ast.Ident{Name: "also_no_writes"}}
	fn := &ast.FuncDecl{Name: "owned", TypeParams: []string{"T"}, Body: []ast.Stmt{
		&ast.ExprStmt{Expr: first},
		&ast.ExprStmt{Expr: nilPlanCall},
		&ast.ExprStmt{Expr: emptyPlanCall},
	}}
	foreignCall := &ast.CallExpr{Func: &ast.Ident{Name: "foreign"}}
	foreign := &ast.FuncDecl{Name: "foreign", Body: []ast.Stmt{&ast.ExprStmt{Expr: foreignCall}}}
	plan := []storeScanCallWrite{{local: "target", value: firstValue, arg: firstArg, callee: foreign, from: 1, into: 0}}
	emptyPlan := make([]storeScanCallWrite, 0)
	source := &Analyzer{storeScanCallWriteCache: map[*ast.CallExpr][]storeScanCallWrite{
		first:         plan,
		nilPlanCall:   nil,
		emptyPlanCall: emptyPlan,
		foreignCall:   {{local: "foreign-only"}},
	}}
	worker, context := specializationStoreWriteCacheContext(source)
	if len(worker.storeScanCallWriteCache) != 0 {
		t.Fatal("clone retained whole-program call-plan rows before syntax import")
	}
	private := context.copyDecl(fn)
	if context.err != nil {
		t.Fatal(context.err)
	}
	privateFirst := private.Body[0].(*ast.ExprStmt).Expr.(*ast.CallExpr)
	privateNil := private.Body[1].(*ast.ExprStmt).Expr.(*ast.CallExpr)
	privateEmpty := private.Body[2].(*ast.ExprStmt).Expr.(*ast.CallExpr)
	if len(worker.storeScanCallWriteCache) != 3 {
		t.Fatalf("imported %d plans, want only the three owned call rows", len(worker.storeScanCallWriteCache))
	}
	if _, present := worker.storeScanCallWriteCache[first]; present {
		t.Fatal("canonical call key leaked into the private plan map")
	}
	if _, present := worker.storeScanCallWriteCache[foreignCall]; present {
		t.Fatal("unowned foreign call plan was imported")
	}
	got, present := worker.storeScanCallWriteCache[privateFirst]
	if !present || len(got) != 1 || cap(got) != len(got) {
		t.Fatalf("owned plan missing or retained source slice capacity: present=%v len=%d cap=%d", present, len(got), cap(got))
	}
	if got[0].value != privateFirst.Args[1] || got[0].arg != privateFirst.Args[0] || got[0].callee != foreign {
		t.Fatal("plan did not remap owned expression references while retaining canonical callee identity")
	}
	if copiedNil, ok := worker.storeScanCallWriteCache[privateNil]; !ok || copiedNil != nil {
		t.Fatal("present nil negative-plan entry lost membership or nilness")
	}
	if copiedEmpty, ok := worker.storeScanCallWriteCache[privateEmpty]; !ok || copiedEmpty == nil || len(copiedEmpty) != 0 {
		t.Fatal("present empty negative-plan entry lost membership or nonnil empty slice")
	}
	got[0].local = "private-mutation"
	if source.storeScanCallWriteCache[first][0].local != "target" || source.storeScanCallWriteCache[first][0].value != firstValue || source.storeScanCallWriteCache[first][0].arg != firstArg {
		t.Fatal("private plan mutation reached canonical row")
	}
}

func TestSpecializationReusesStoreScanIndexForPrivateSyntax(t *testing.T) {
	decl := &ast.VarDeclStmt{Name: "local", Value: &ast.IntLit{Value: "1"}}
	assign := &ast.AssignStmt{Target: &ast.Ident{Name: "local"}, Value: &ast.IntLit{Value: "2"}}
	expr := &ast.ExprStmt{Expr: &ast.Ident{Name: "local"}}
	ret := &ast.ReturnStmt{Value: &ast.Ident{Name: "local"}}
	fn := &ast.FuncDecl{Name: "indexed", TypeParams: []string{"T"}, Body: []ast.Stmt{decl, assign, expr, ret}}
	source := &Analyzer{}
	sourceIndex := source.storeScanIndex(fn)
	sourceIndex.viewLocalNodes()
	worker, context := specializationStoreWriteCacheContext(source)
	foreignIndex := worker.storeScanIndex(fn)
	private := worker.parallel.readShadows[fn]
	if foreignIndex == nil || private == nil || worker.storeScanIndexCache[private] != foreignIndex || len(foreignIndex.flat) != len(sourceIndex.flat) {
		t.Fatal("specialization did not clone and cache the source index for a private callee")
	}
	privateExprForCache := private.Body[2].(*ast.ExprStmt)
	sourceBoundCount := len(sourceIndex.bound)
	privateOldBound, privateHadBound := foreignIndex.bound[privateExprForCache]
	foreignIndex.bound[privateExprForCache] = nil
	if len(sourceIndex.bound) != sourceBoundCount {
		t.Fatal("specialization shared its mutable statement cache map with the canonical index")
	}
	if privateHadBound {
		foreignIndex.bound[privateExprForCache] = privateOldBound
	} else {
		delete(foreignIndex.bound, privateExprForCache)
	}
	private = context.copyDecl(fn)
	if context.err != nil {
		t.Fatal(context.err)
	}
	privateIndex := worker.storeScanIndexCache[private]
	if privateIndex == nil {
		t.Fatal("specialization rebuilt instead of reusing the source store-scan index")
	}
	if !privateIndex.viewNodesReady || len(privateIndex.viewNodes) != len(sourceIndex.viewNodes) {
		t.Fatal("specialization did not reuse the view-local node index")
	}
	if len(privateIndex.flat) != len(sourceIndex.flat) || len(privateIndex.returns) != len(sourceIndex.returns) {
		t.Fatalf("copied index shape differs: private flat/returns=%d/%d source=%d/%d", len(privateIndex.flat), len(privateIndex.returns), len(sourceIndex.flat), len(sourceIndex.returns))
	}
	privateDecl := private.Body[0].(*ast.VarDeclStmt)
	privateAssign := private.Body[1].(*ast.AssignStmt)
	privateExpr := private.Body[2].(*ast.ExprStmt)
	privateRet := private.Body[3].(*ast.ReturnStmt)
	for _, node := range privateIndex.viewNodes {
		if node == decl || node == assign || node == expr || node == ret {
			t.Fatal("view-local index retained source syntax identities")
		}
	}
	flatNodes := make(map[any]bool, len(privateIndex.flat))
	for _, node := range privateIndex.flat {
		flatNodes[node] = true
	}
	if privateIndex.decls["local"][0] != privateDecl || !flatNodes[privateDecl] || !flatNodes[privateAssign] || !flatNodes[privateExpr] || !flatNodes[privateRet] || flatNodes[decl] || flatNodes[assign] || flatNodes[expr] || flatNodes[ret] || privateIndex.returns[0] != privateRet {
		t.Fatal("store-scan index retained source syntax identities")
	}
	if _, ok := privateIndex.bound[privateExpr]; !ok {
		t.Fatal("private statement-to-binder index did not rebind its syntax key")
	}
}

func TestCanonicalStoreScanIndexKeepsAuxiliarySyntax(t *testing.T) {
	aux := &ast.CallArgItem{}
	source := &storeScanDeclIndex{flat: []any{aux}}
	private := cloneStoreScanIndexForSyntax(source, nil)
	if private == nil || len(private.flat) != 1 || private.flat[0] != aux {
		t.Fatal("canonical index rejected an auxiliary syntax pointer")
	}
	private.flat[0] = nil
	if source.flat[0] != aux {
		t.Fatal("canonical index clone shared its mutable slice")
	}
}

func TestPrivateStoreScanIndexRemapsAuxiliarySyntax(t *testing.T) {
	aux := &ast.CallArgItem{}
	privateAux := &ast.CallArgItem{}
	ident := &ast.Ident{Name: "source"}
	privateIdent := &ast.Ident{Name: "source"}
	value := reflect.ValueOf(aux)
	auxiliary := map[specializationPointer]reflect.Value{
		{value.Type(), value.Pointer()}: reflect.ValueOf(privateAux),
	}
	source := &storeScanDeclIndex{flat: []any{ident, aux}, paramFactNodes: []any{aux}, paramFactNodesReady: true}
	private := cloneStoreScanIndexForSyntax(source, map[ast.Node]ast.Node{ident: privateIdent}, auxiliary)
	if private == nil || len(private.flat) != 2 || private.flat[0] != privateIdent || private.flat[1] != privateAux {
		t.Fatal("private index did not remap ordinary and auxiliary syntax")
	}
	if !private.paramFactNodesReady || len(private.paramFactNodes) != 1 || private.paramFactNodes[0] != privateAux {
		t.Fatal("parameter-fact index did not remap auxiliary syntax")
	}
	if cloneStoreScanIndexForSyntax(source, map[ast.Node]ast.Node{ident: privateIdent}, nil) != nil {
		t.Fatal("index reuse must decline when an auxiliary pointer is missing")
	}
}

func TestSpecializationReusesIndexWithResolvedPatternArgument(t *testing.T) {
	pattern := &ast.MatchVariantPattern{EnumName: "Choice", Variant: "Some", Args: []ast.MatchPatternArg{{Name: "value", Pattern: &ast.MatchBindPattern{Name: "item"}}}}
	pattern.ResolvedArgs = []*ast.MatchPatternArg{&pattern.Args[0]}
	fn := &ast.FuncDecl{Name: "indexed", TypeParams: []string{"T"}, Body: []ast.Stmt{&ast.MatchStmt{Value: &ast.Ident{Name: "x"}, Arms: []ast.MatchArm{{Pattern: pattern}}}}}
	source := &Analyzer{}
	sourceIndex := source.storeScanIndex(fn)
	worker, context := specializationStoreWriteCacheContext(source)
	private := context.copyDecl(fn)
	if context.err != nil {
		t.Fatal(context.err)
	}
	index := worker.storeScanIndexCache[private]
	if index == nil || len(index.flat) != len(sourceIndex.flat) {
		t.Fatal("specialization rebuilt or lost the indexed syntax")
	}
	privatePattern := private.Body[0].(*ast.MatchStmt).Arms[0].Pattern.(*ast.MatchVariantPattern)
	if privatePattern.ResolvedArgs[0] != &privatePattern.Args[0] {
		t.Fatal("copied pattern lost its interior alias")
	}
	found := false
	for _, node := range index.flat {
		if node == pattern.ResolvedArgs[0] {
			t.Fatal("index retained the source pattern argument")
		}
		if node == privatePattern.ResolvedArgs[0] {
			found = true
		}
	}
	if !found {
		t.Fatal("index omitted the copied pattern argument")
	}
}

func BenchmarkSpecializationStoreScanIndexWithPatternArgs(b *testing.B) {
	fn := &ast.FuncDecl{Name: "indexed"}
	for i := 0; i < 100; i++ {
		pattern := &ast.MatchVariantPattern{EnumName: "Choice", Variant: "Some", Args: []ast.MatchPatternArg{{Name: "value", Pattern: &ast.MatchBindPattern{Name: "item"}}}}
		pattern.ResolvedArgs = []*ast.MatchPatternArg{&pattern.Args[0]}
		fn.Body = append(fn.Body, &ast.MatchStmt{Value: &ast.Ident{Name: "x"}, Arms: []ast.MatchArm{{Pattern: pattern}}})
	}
	source := &Analyzer{}
	index := source.storeScanIndex(fn)
	syntax := newSpecializationSyntax()
	syntax.prepare(reflect.ValueOf(fn))
	if err := syntax.validate(); err != nil {
		b.Fatal(err)
	}
	private := syntax.copy(reflect.ValueOf(fn)).Interface().(*ast.FuncDecl)
	for _, reuse := range []bool{false, true} {
		name := "rebuild"
		if reuse {
			name = "reuse"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var result *storeScanDeclIndex
				if reuse {
					result = cloneStoreScanIndexForSyntax(index, syntax.nodes, syntax.pointers)
				} else {
					a := &Analyzer{}
					result = a.storeScanIndex(private)
				}
				if result == nil || len(result.flat) != len(index.flat) {
					b.Fatal("lost syntax index")
				}
			}
		})
	}
}

func BenchmarkCanonicalStoreScanNodeSliceCopy(b *testing.B) {
	nodes := make([]any, 512)
	for i := range nodes {
		nodes[i] = &ast.Ident{Name: "value"}
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		result, ok := remapStoreScanNodeSlice(nil, nodes)
		if !ok || len(result) != len(nodes) {
			b.Fatal("lost nodes")
		}
	}
}

func TestSpecializationStorageViewCacheRecomputesAndMemoizesShadow(t *testing.T) {
	field := &ast.FieldExpr{Object: &ast.Ident{Name: "pair"}, Field: "left"}
	call := &ast.CallExpr{Func: &ast.FieldExpr{Object: field, Field: "clear"}}
	decl := &ast.FuncDecl{Name: "writer", Params: []ast.ParamDecl{{Name: "pair", Mutable: true}}, Body: []ast.Stmt{&ast.ExprStmt{Expr: call}}}
	source := &Analyzer{funcDeclSymbols: map[*ast.FuncDecl]*Symbol{decl: {Name: "writer"}}}
	canonical := source.storageViewCalleeWritesFor([]*ast.FuncDecl{decl}, 0)
	if canonical.All || !canonical.Fields["left"] || len(canonical.Fields) != 1 {
		t.Fatalf("fixture summary = %+v, want only the left field", canonical)
	}
	canonicalFields := maps.Clone(canonical.Fields)
	worker, _ := specializationStoreWriteCacheContext(source)
	if len(worker.storageViewCalleeWriteCache) != 0 {
		t.Fatal("specialization inherited canonical composite-key rows")
	}
	shadow := worker.readDecl(decl)
	if shadow == nil || shadow == decl || worker.parallel.original[shadow] != decl {
		t.Fatal("foreign function was not given a private specialization shadow")
	}
	got := worker.storageViewCalleeWritesFor([]*ast.FuncDecl{decl}, 0)
	if got.All || !got.Fields["left"] || got.Fields["right"] || len(got.Fields) != 1 {
		t.Fatalf("shadow recomputation = %+v, want only left", got)
	}
	key := storageViewCalleeWriteKey{decl: shadow, param: 0}
	if _, present := worker.storageViewCalleeWriteCache[key]; !present {
		t.Fatal("shadow summary was not memoized under the private declaration key")
	}
	if _, present := worker.storageViewCalleeWriteCache[storageViewCalleeWriteKey{decl: decl, param: 0}]; present {
		t.Fatal("canonical declaration key leaked into the private summary map")
	}
	if !reflect.DeepEqual(source.storageViewCalleeWriteCache[storageViewCalleeWriteKey{decl: decl, param: 0}].Fields, canonicalFields) {
		t.Fatal("private recomputation changed canonical nested field facts")
	}

	// A distinct conservative row makes the next query's cache hit observable;
	// this is confined to the private test analyzer and does not alter the source.
	worker.storageViewCalleeWriteCache[key] = storageViewCalleeWrites{All: true}
	if memo := worker.storageViewCalleeWritesFor([]*ast.FuncDecl{decl}, 0); !memo.All {
		t.Fatalf("repeat query missed its shadow-keyed memo: %+v", memo)
	}
	if !reflect.DeepEqual(source.storageViewCalleeWriteCache[storageViewCalleeWriteKey{decl: decl, param: 0}].Fields, canonicalFields) {
		t.Fatal("private memo write changed canonical nested field facts")
	}

	// Empty summaries are negative memo entries too: the shadow recomputes
	// and stores the empty result rather than confusing absence with a hit.
	emptyDecl := &ast.FuncDecl{Name: "no_writer", Params: []ast.ParamDecl{{Name: "pair", Mutable: true}}, Body: []ast.Stmt{}}
	emptySource := &Analyzer{funcDeclSymbols: map[*ast.FuncDecl]*Symbol{emptyDecl: {Name: "no_writer"}}}
	if empty := emptySource.storageViewCalleeWritesFor([]*ast.FuncDecl{emptyDecl}, 0); empty.All || len(empty.Fields) != 0 {
		t.Fatalf("empty source summary = %+v", empty)
	}
	emptyWorker, _ := specializationStoreWriteCacheContext(emptySource)
	emptyShadow := emptyWorker.readDecl(emptyDecl)
	empty := emptyWorker.storageViewCalleeWritesFor([]*ast.FuncDecl{emptyDecl}, 0)
	emptyKey := storageViewCalleeWriteKey{decl: emptyShadow, param: 0}
	if empty.All || len(empty.Fields) != 0 {
		t.Fatalf("empty shadow summary = %+v", empty)
	}
	if memo, present := emptyWorker.storageViewCalleeWriteCache[emptyKey]; !present || memo.All || len(memo.Fields) != 0 {
		t.Fatalf("empty summary negative memo lost membership: present=%v value=%+v", present, memo)
	}
}

func BenchmarkSpecializationStoreWriteCacheClone(b *testing.B) {
	for _, size := range []struct {
		name     string
		callRows int
		viewRows int
	}{
		{name: "empty", callRows: 0, viewRows: 0},
		{name: "snapshot_rows", callRows: 30283, viewRows: 3001},
	} {
		source := &Analyzer{
			storeScanCallWriteCache:     make(map[*ast.CallExpr][]storeScanCallWrite, size.callRows),
			storageViewCalleeWriteCache: make(map[storageViewCalleeWriteKey]storageViewCalleeWrites, size.viewRows),
		}
		for i := 0; i < size.callRows; i++ {
			call := &ast.CallExpr{}
			source.storeScanCallWriteCache[call] = []storeScanCallWrite{{local: "local", value: &ast.Ident{Name: "value"}, arg: &ast.Ident{Name: "arg"}}}
		}
		for i := 0; i < size.viewRows; i++ {
			decl := &ast.FuncDecl{Name: "foreign"}
			fields := make(map[string]bool, 8)
			for field := 0; field < 8; field++ {
				fields["field_"+strconv.Itoa(field)] = true
			}
			source.storageViewCalleeWriteCache[storageViewCalleeWriteKey{decl: decl, param: i}] = storageViewCalleeWrites{Fields: fields}
		}
		b.Run(size.name+"/deep_clone_surrogate", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Surrogate: deep-clone the whole analyzer using the generic
				// reflective value policy that these two fields previously took.
				specializationStoreWriteCacheCloneSink = deepCloneValue(reflect.ValueOf(source).Elem()).Addr().Interface().(*Analyzer)
			}
		})
		b.Run(size.name+"/specialization_empty", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				specializationStoreWriteCacheCloneSink = cloneAnalyzerValueForSpecialization(source).Addr().Interface().(*Analyzer)
			}
		})
	}
	specializationStoreWriteCacheCloneSink = nil
}
