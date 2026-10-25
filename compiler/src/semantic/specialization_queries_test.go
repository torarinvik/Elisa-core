//go:build cgo

package semantic

import (
	"reflect"
	"runtime"
	"sync"
	"testing"
	"weak"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestSpecializationQueriesKeepOwnedHelperAbsenceAndForeignFallback(t *testing.T) {
	original, private, foreign := &ast.CallExpr{}, &ast.CallExpr{}, &ast.CallExpr{}
	root := &Result{analyzer: &Analyzer{compilerBuiltinHelperCalls: map[*ast.CallExpr]string{original: "old-helper", foreign: "foreign-helper"}}}
	owned := map[ast.Node]bool{private: true}
	canonical := map[ast.Node]ast.Node{original: private}
	queries := newSpecializationQueries(&Analyzer{}, root, &ast.FuncDecl{}, nil, owned, canonical)
	result := &Result{specializationQueries: queries, specializationOwned: owned, specializationNodes: canonical}
	if result.IsCompilerBuiltinHelperCall(original, "old-helper") {
		t.Fatal("canonical current alias revived a missing private helper fact")
	}
	if result.IsCompilerBuiltinHelperCall(private, "old-helper") {
		t.Fatal("owned missing helper fact fell back to canonical authority")
	}
	if !result.IsCompilerBuiltinHelperCall(foreign, "foreign-helper") {
		t.Fatal("unrelated canonical helper query lost root authority")
	}
	queries.helpers[private] = "current-helper"
	if !result.IsCompilerBuiltinHelperCall(original, "current-helper") || !result.IsCompilerBuiltinHelperCall(private, "current-helper") {
		t.Fatal("private/canonical current helper aliases disagree")
	}
}

func TestSpecializationQueriesCaptureStaticBranchBeforeGenericScopeEnds(t *testing.T) {
	thenStmt, elseStmt := &ast.ReturnStmt{Value: &ast.IntLit{Value: "1"}}, &ast.ReturnStmt{Value: &ast.IntLit{Value: "0"}}
	stmt := &ast.StaticIfStmt{Cond: &ast.BinaryExpr{Op: lexer.TOKEN_GT, Left: &ast.Ident{Name: "N"}, Right: &ast.IntLit{Value: "0"}}, Then: []ast.Stmt{thenStmt}, Else: []ast.Stmt{elseStmt}}
	source := &Analyzer{parallel: &parallelWorkerState{specialization: &specializationContext{}}}
	source.withConstParams([]string{"N"}, []Type{&ConstValueType{Value: ConstValue{Kind: ConstInt, Int: 0}}}, func() {
		selected := source.activeStmtBranch(stmt)
		if len(selected) != 1 || selected[0] != elseStmt {
			t.Fatal("scoped concrete branch selection failed")
		}
	})
	if len(source.constParamScopes) != 0 {
		t.Fatal("fixture did not leave generic scope")
	}
	inactive := &ast.StaticIfStmt{Cond: &ast.BoolLit{Value: true}, Then: []ast.Stmt{thenStmt}}
	owned := map[ast.Node]bool{stmt: true, inactive: true}
	original := &ast.StaticIfStmt{}
	canonical := map[ast.Node]ast.Node{original: stmt}
	root := &Result{analyzer: &Analyzer{}}
	queries := newSpecializationQueries(source, root, &ast.FuncDecl{Body: []ast.Stmt{stmt}}, nil, owned, canonical)
	result := &Result{specializationQueries: queries, specializationOwned: owned, specializationNodes: canonical}
	for _, node := range []*ast.StaticIfStmt{stmt, original} {
		got := result.ActiveStaticStmtBranch(node)
		if len(got) != 1 || got[0] != elseStmt {
			t.Fatal("query re-evaluated a concrete branch after its generic scope ended")
		}
	}
	if got := result.ActiveStaticStmtBranch(inactive); got != nil {
		t.Fatal("owned inactive static statement acquired a branch")
	}
	foreign := &ast.StaticIfStmt{Cond: &ast.BoolLit{Value: true}, Then: []ast.Stmt{thenStmt}}
	if got := result.ActiveStaticStmtBranch(foreign); len(got) != 1 || got[0] != thenStmt {
		t.Fatal("unrelated canonical static query lost root behavior")
	}
}

func specializationQueryLifetimeFixture() (*specializationQueries, weak.Pointer[Analyzer]) {
	ownedCall, foreignCall := &ast.CallExpr{}, &ast.CallExpr{}
	source := &Analyzer{programRegions: map[string]bool{"program": true}, exprTypes: map[ast.Expr]Type{foreignCall: &BuiltinType{Name: "i64"}}, retained: &RetainedAnalysis{}}
	source.parallel = &parallelWorkerState{specialization: &specializationContext{worker: source}}
	root := &Result{NamedTypes: map[string]Type{"i64": &BuiltinType{Name: "i64"}}, StaticImpls: map[string]*StaticImpl{}}
	queries := newSpecializationQueries(source, root, &ast.FuncDecl{}, map[*ast.CallExpr]string{ownedCall: "owned", foreignCall: "foreign"}, map[ast.Node]bool{ownedCall: true}, nil)
	return queries, weak.Make(source)
}

func TestSpecializationQueriesReleaseConstructionAnalyzer(t *testing.T) {
	queries, source := specializationQueryLifetimeFixture()
	if len(queries.helpers) != 1 {
		t.Fatal("query context retained unrelated helper rows")
	}
	if queries.types == nil || queries.types.retained != nil || queries.types.parallel != nil || queries.types.exprTypes != nil || queries.types.resolvedTypeNames != nil || queries.types.globalScope != nil {
		t.Fatal("query context retained whole construction state")
	}
	released := false
	for attempt := 0; attempt < 8; attempt++ {
		runtime.GC()
		if source.Value() == nil {
			released = true
			break
		}
	}
	runtime.KeepAlive(queries)
	if !released {
		t.Fatal("published queries kept the construction Analyzer alive")
	}
}

func TestSpecializationQueryTypePredicatesUseOnlyPrivateCaches(t *testing.T) {
	source := &Analyzer{programRegions: map[string]bool{"program": true}}
	root := &Result{NamedTypes: map[string]Type{"i64": &BuiltinType{Name: "i64"}}, StaticImpls: map[string]*StaticImpl{}, shared: &sharedCodegen{}}
	queries := newSpecializationQueries(source, root, &ast.FuncDecl{}, nil, nil, nil)
	result := &Result{specializationQueries: queries, shared: root.shared}
	var workers sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for attempt := 0; attempt < 8; attempt++ {
				if !result.IsProgramRegion("program") {
					t.Error("program region identity lost")
				}
				if region, ok := result.ProgramLivedArgRegion(&SViewType{Region: "program"}); !ok || region != "program" {
					t.Error("program-lived view query failed")
				}
				if _, ok := result.ProgramLivedArgRegion(&SViewType{Region: "other"}); ok {
					t.Error("non-program view accepted")
				}
			}
		}()
	}
	workers.Wait()
	if source.programLivedTypes != nil || source.regionRefsMemo != nil {
		t.Fatal("predicate query populated construction caches")
	}
	if len(queries.types.programLivedTypes) == 0 || len(queries.types.regionRefsMemo) == 0 {
		t.Fatal("fixture did not populate small private query caches")
	}
	allowed := map[string]bool{"namedTypes": true, "staticImpls": true, "programRegions": true, "programLivedTypes": true, "regionRefsMemo": true}
	value := reflect.ValueOf(queries.types).Elem()
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if field.Kind() == reflect.Map && !field.IsNil() && !allowed[value.Type().Field(i).Name] {
			t.Fatalf("tiny query analyzer retained unrelated map %s", value.Type().Field(i).Name)
		}
	}

}

func TestSpecializationQueriesKeepCanonicalSchedulingPriorities(t *testing.T) {
	fn := &ast.FuncDecl{Name: "first"}
	root := &Result{analyzer: &Analyzer{retained: &RetainedAnalysis{LastStats: RetainedUpdateStats{Changed: 1}, rbChanged: []*ast.FuncDecl{fn}}, funcDeclSymbols: map[*ast.FuncDecl]*Symbol{fn: &Symbol{Name: "Module.first"}}}}
	result := &Result{specializationRoot: root}
	got := result.CodegenFunctionPriorities()
	if len(got) != 1 || got[0] != "Module.first" {
		t.Fatalf("private scheduling query lost canonical priorities: %v", got)
	}
}
