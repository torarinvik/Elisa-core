package semantic

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func TestBodyScheduleMarksOnlyRecursiveFunctionGroups(t *testing.T) {
	source := `def direct(n: i64) -> i64:
    if n == 0:
        return 0
    return direct(n - 1)

def left(n: i64) -> i64:
    return right(n)

def right(n: i64) -> i64:
    return left(n)

def indirect(n: i64) -> i64:
    recur = indirect
    if n == 0:
        return 0
    return recur(n - 1)

def caller(n: i64) -> i64:
    return left(n)
`
	l := lexer.New("parallel_schedule.elisa", []byte(source))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile("parallel_schedule.elisa")
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	var bodies []scopedDecl
	decls := map[string]*ast.FuncDecl{}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			bodies = append(bodies, scopedDecl{Decl: fn})
			decls[fn.Name] = fn
		}
	}
	schedule := planBodySchedule(bodies, 3)
	for name, want := range map[string]bool{"direct": true, "left": true, "right": true, "indirect": true, "caller": false} {
		if got := schedule.cyclic[decls[name]]; got != want {
			t.Errorf("%s recursive summary classification: got %v, want %v", name, got, want)
		}
	}

	call := func(name string) ast.Stmt {
		return &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: name}}}
	}
	oneWayCaller := &ast.FuncDecl{Name: "one_way_caller", Body: []ast.Stmt{call("one_way_leaf")}}
	oneWayLeaf := &ast.FuncDecl{Name: "one_way_leaf"}
	mutualLeft := &ast.FuncDecl{Name: "mutual_left", Body: []ast.Stmt{call("mutual_right")}}
	mutualRight := &ast.FuncDecl{Name: "mutual_right", Body: []ast.Stmt{call("mutual_left")}}
	impl := &ast.ImplDecl{Members: []ast.ImplMember{oneWayCaller, oneWayLeaf, mutualLeft, mutualRight}}
	implSchedule := planBodySchedule([]scopedDecl{{Decl: impl}}, 1)
	for fn, want := range map[*ast.FuncDecl]bool{
		oneWayCaller: false,
		oneWayLeaf:   false,
		mutualLeft:   true,
		mutualRight:  true,
	} {
		if got := implSchedule.cyclic[fn]; got != want {
			t.Errorf("impl method %s recursive summary classification: got %v, want %v", fn.Name, got, want)
		}
	}
}

func TestParallelAnalyzerCloneSharesOnlyFrozenDeclarationMaps(t *testing.T) {
	identifier := &ast.Ident{Name: "expr"}
	base := &Analyzer{
		functionTypes:    map[string]*FuncType{"known": {Name: "known"}},
		privateTypeNames: map[string]bool{"Hidden.Item": true},
		aliasRefinements: map[string]*ast.RefinementTypeExpr{"Index": {}},
		exprTypes:        map[ast.Expr]Type{identifier: &BuiltinType{Name: "i64"}},
	}
	worker := cloneAnalyzerValueForParallel(base).Addr().Interface().(*Analyzer)
	base.functionTypes["later"] = &FuncType{Name: "later"}
	if worker.functionTypes["later"] == nil {
		t.Fatal("frozen declaration map was copied instead of shared")
	}
	base.privateTypeNames["Hidden.New"] = true
	if !worker.privateTypeNames["Hidden.New"] {
		t.Fatal("frozen visibility map was copied instead of shared")
	}
	base.aliasRefinements["Later"] = &ast.RefinementTypeExpr{}
	if worker.aliasRefinements["Later"] == nil {
		t.Fatal("frozen refinement map was copied instead of shared")
	}
	base.exprTypes[&ast.Ident{Name: "other"}] = &BuiltinType{Name: "bool"}
	if _, ok := worker.exprTypes[&ast.Ident{Name: "other"}]; ok {
		t.Fatal("mutable expression-type map leaked across workers")
	}

	base.retained = &RetainedAnalysis{}
	retainedWorker := cloneAnalyzerValueForParallel(base).Addr().Interface().(*Analyzer)
	base.functionTypes["retained-only"] = &FuncType{Name: "retained-only"}
	if _, ok := retainedWorker.functionTypes["retained-only"]; ok {
		t.Fatal("retained analysis must keep declaration maps isolated")
	}
}

func TestParallelBodyWorkerMemoOverlaysUseFrozenBases(t *testing.T) {
	typ := &BuiltinType{Name: "i64"}
	sym := &Symbol{Name: "local", Kind: SymbolLocal}
	nameExpr := &ast.NamedType{Name: "Alias"}
	base := &Analyzer{
		typeShapesFrozen:  true,
		affineHandleMemo:  map[Type]bool{typ: false},
		programLivedTypes: map[Type]bool{typ: true},
		smtLocalDefDeps:   map[*Symbol][]string{sym: {"x", "y"}},
		resolvedTypeNames: map[ast.TypeExpr]string{nameExpr: "Hidden.Alias"},
	}
	worker := cloneAnalyzerValueForBodyWorker(base).Addr().Interface().(*Analyzer)
	if worker.affineHandleMemo != nil || worker.programLivedTypes != nil || worker.smtLocalDefDeps != nil || worker.resolvedTypeNames != nil {
		t.Fatal("body worker copied a frozen memo instead of using an overlay")
	}
	worker.parallel = &parallelWorkerState{
		predicateMemoBases:    parallelPredicateMemoBases{affineHandles: base.affineHandleMemo},
		programLivedTypesBase: base.programLivedTypes,
		smtLocalDefDepsBase:   base.smtLocalDefDeps,
		resolvedTypeNamesBase: base.resolvedTypeNames,
	}
	if worker.memoTypePredicate(&worker.affineHandleMemo, typ, func() bool { t.Fatal("cached predicate recomputed"); return true }) {
		t.Fatal("cached false predicate changed")
	}
	if !worker.typeIsProgramLivedCached(typ) {
		t.Fatal("cached program-lived result was missed")
	}
	if got := worker.smtLocalDefinitionDeps(sym, &ast.VarDeclStmt{Value: &ast.Ident{Name: "unused"}}); len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Fatalf("cached SMT local dependencies were missed: %v", got)
	}
	if got := worker.resolvedTypeName(nameExpr); got != "Hidden.Alias" {
		t.Fatalf("cached type name was missed: %q", got)
	}
	if worker.affineHandleMemo != nil || worker.programLivedTypes != nil || worker.smtLocalDefDeps != nil || worker.resolvedTypeNames != nil {
		t.Fatal("read-only memo lookup wrote into the worker overlay")
	}
	other := &BuiltinType{Name: "bool"}
	if !worker.memoTypePredicate(&worker.affineHandleMemo, other, func() bool { return true }) {
		t.Fatal("worker memo did not store a computed miss")
	}
	if worker.affineHandleMemo[other] != true || base.affineHandleMemo[other] {
		t.Fatal("worker memo miss was not isolated from its frozen base")
	}
}

func TestParallelWorkerCopiesDeclarationSnapshot(t *testing.T) {
	second := &ast.FuncDecl{Name: "second"}
	snapshot, err := ast.NewSyntaxSnapshot(&ast.File{Decls: []ast.Decl{second}}, 1<<20)
	if err != nil {
		t.Fatalf("capture shared snapshot: %v", err)
	}
	worker := &parallelWorkerState{
		snapshots: []*ast.SyntaxSnapshot{snapshot, snapshot},
		at:        map[*ast.FuncDecl][2]int{second: {1, -1}},
	}
	copied := worker.copyDecl(second)
	if copied == nil || copied == second || copied.Name != "second" {
		t.Fatalf("shared snapshot copied wrong declaration: %#v", copied)
	}
}
