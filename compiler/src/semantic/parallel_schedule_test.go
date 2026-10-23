package semantic

import (
	"reflect"
	"sync/atomic"
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

func TestBodyScheduleWaitsForOverloadedOperatorSummary(t *testing.T) {
	cases := []struct {
		name    string
		op      lexer.TokenKind
		unary   bool
		methods []string
	}{
		{name: "add", op: lexer.TOKEN_PLUS, methods: []string{"__add__", "combine", "combine_span", "lua_span_union"}},
		{name: "subtract", op: lexer.TOKEN_MINUS, methods: []string{"__sub__"}},
		{name: "multiply", op: lexer.TOKEN_STAR, methods: []string{"__mul__"}},
		{name: "divide", op: lexer.TOKEN_SLASH, methods: []string{"__div__"}},
		{name: "equal", op: lexer.TOKEN_EQEQ, methods: []string{"__eq__"}},
		{name: "not_equal", op: lexer.TOKEN_BANGEQ, methods: []string{"__eq__"}},
		{name: "less_than", op: lexer.TOKEN_LT, methods: []string{"__cmp__"}},
		{name: "greater_equal", op: lexer.TOKEN_GTEQ, methods: []string{"__cmp__"}},
		{name: "negate", op: lexer.TOKEN_MINUS, unary: true, methods: []string{"__neg__"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var expr ast.Expr
			left := &ast.Ident{Name: "value"}
			if tc.unary {
				expr = &ast.UnaryExpr{Op: tc.op, Operand: left}
			} else {
				expr = &ast.BinaryExpr{Op: tc.op, Left: left, Right: &ast.IntLit{Value: "1"}}
			}
			caller := &ast.FuncDecl{Name: "caller", Body: []ast.Stmt{&ast.ReturnStmt{Value: expr}}}
			bodies := []scopedDecl{{Decl: caller}}
			targets := make([]*ast.FuncDecl, 0, len(tc.methods))
			for _, method := range tc.methods {
				var targetExpr ast.Expr = &ast.IntLit{Value: "1"}
				if tc.name == "add" && method == "__add__" {
					// This numeric operation must not turn the conservative candidate
					// edge into a recursive-summary classification for __add__.
					targetExpr = &ast.BinaryExpr{Op: lexer.TOKEN_PLUS, Left: &ast.IntLit{Value: "1"}, Right: &ast.IntLit{Value: "2"}}
				}
				target := &ast.FuncDecl{
					Name: method,
					Body: []ast.Stmt{&ast.ReturnStmt{Value: targetExpr}},
				}
				targets = append(targets, target)
				bodies = append(bodies, scopedDecl{Decl: &ast.ImplDecl{Members: []ast.ImplMember{target}}})
			}
			schedule := planBodySchedule(bodies, 2)
			callerUnit := schedule.unitOf[0]
			targetUnits := make(map[int]bool, len(tc.methods))
			for body := 1; body < len(bodies); body++ {
				targetUnits[schedule.unitOf[body]] = true
			}
			if schedule.pending[callerUnit] != len(targetUnits) {
				t.Fatalf("caller has %d dependency units, want all %d possible targets", schedule.pending[callerUnit], len(targetUnits))
			}
			for worker := 0; worker < len(targetUnits); worker++ {
				unit := schedule.claim(worker % 2)
				if !targetUnits[unit] {
					t.Fatalf("caller or unrelated unit became runnable before possible targets finished: claimed unit %d", unit)
				}
				schedule.finish(unit)
			}
			if got := schedule.claim(0); got != callerUnit {
				t.Fatalf("caller remained blocked after all operator targets finished: got unit %d, want %d", got, callerUnit)
			}
			for _, target := range targets {
				if schedule.cyclic[target] {
					t.Fatalf("possible operator edge incorrectly classified %s as recursive", target.Name)
				}
			}
		})
	}
}

func TestBodyScheduleWaitsForDefaultArgumentSummary(t *testing.T) {
	reader := &ast.FuncDecl{Name: "reader"}
	consume := &ast.FuncDecl{
		Name: "consume",
		Params: []ast.ParamDecl{{
			Name:         "callback",
			DefaultValue: &ast.Ident{Name: "reader"},
		}},
	}
	caller := &ast.FuncDecl{
		Name: "caller",
		Body: []ast.Stmt{&ast.ReturnStmt{Value: &ast.CallExpr{Func: &ast.Ident{Name: "consume"}}}},
	}
	bodies := []scopedDecl{{Decl: reader}, {Decl: consume}, {Decl: caller}}
	schedule := planBodySchedule(bodies, 2)
	readerUnit, consumeUnit, callerUnit := schedule.unitOf[0], schedule.unitOf[1], schedule.unitOf[2]
	if readerUnit == consumeUnit || consumeUnit == callerUnit || readerUnit == callerUnit {
		t.Fatalf("default argument dependencies collapsed bodies into one unit: reader=%d consume=%d caller=%d", readerUnit, consumeUnit, callerUnit)
	}
	if schedule.pending[consumeUnit] != 1 || schedule.pending[callerUnit] != 1 {
		t.Fatalf("pending dependencies: consume=%d caller=%d, want one callee each", schedule.pending[consumeUnit], schedule.pending[callerUnit])
	}
	for _, want := range []int{readerUnit, consumeUnit, callerUnit} {
		if got := schedule.claim(0); got != want {
			t.Fatalf("claimed unit %d, want callee-first unit %d", got, want)
		}
		schedule.finish(want)
	}

	selfDefault := &ast.FuncDecl{
		Name: "self_default",
		Params: []ast.ParamDecl{{
			Name:         "callback",
			DefaultValue: &ast.Ident{Name: "self_default"},
		}},
	}
	selfSchedule := planBodySchedule([]scopedDecl{{Decl: selfDefault}}, 1)
	if selfSchedule.cyclic[selfDefault] {
		t.Fatal("function-valued default reference was incorrectly classified as recursive")
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

	// A retained analysis's workers share the frozen maps too: updates run serially, so
	// nothing writes them while workers run, and per-worker copies kept ~2 GB live.
	base.retained = &RetainedAnalysis{}
	retainedWorker := cloneAnalyzerValueForParallel(base).Addr().Interface().(*Analyzer)
	base.functionTypes["retained-shared"] = &FuncType{Name: "retained-shared"}
	if _, ok := retainedWorker.functionTypes["retained-shared"]; !ok {
		t.Fatal("retained analysis copied a frozen declaration map")
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

func TestParallelBodyWorkerKeepsShadowableCallGraphCachesLocal(t *testing.T) {
	fn := &ast.FuncDecl{Name: "owner"}
	callee := &ast.FuncDecl{Name: "callee"}
	shadow := &ast.FuncDecl{Name: "callee"}
	base := &Analyzer{
		callEdgeCache:   map[*ast.FuncDecl][]*ast.FuncDecl{fn: {callee}},
		storeFlowSCC:    map[*ast.FuncDecl][]*ast.FuncDecl{fn: {fn, callee}},
		storeFlowActive: map[*ast.FuncDecl]bool{fn: true},
	}
	worker := cloneAnalyzerValueForBodyWorker(base).Addr().Interface().(*Analyzer)
	if worker.callEdgeCache[fn][0] != callee || worker.storeFlowSCC[fn][1] != callee || worker.storeFlowActive != nil {
		t.Fatal("body worker lost canonical graph entries or inherited active solver state")
	}
	worker.callEdgeCache[fn] = []*ast.FuncDecl{shadow}
	worker.storeFlowSCC[fn] = append(worker.storeFlowSCC[fn], shadow)
	if base.callEdgeCache[fn][0] != callee || base.storeFlowSCC[fn][1] != callee || !base.storeFlowActive[fn] {
		t.Fatal("body-worker clone changed the base analyzer's call-graph state")
	}

	for _, path := range exportDeclMapPaths() {
		name := reflect.TypeOf(Analyzer{}).Field(path[0]).Name
		if parallelBodyWorkerLocalOnlyMap(name) {
			t.Errorf("worker-private map %s is included in unit exports", name)
		}
	}

	slots := make([]atomic.Int32, 2)
	slots[0].Store(0)
	slots[1].Store(1)
	p := &parallelWorkerState{
		worker:   0,
		at:       map[*ast.FuncDecl][2]int{fn: {0, -1}, callee: {1, -1}},
		slots:    slots,
		original: map[*ast.FuncDecl]*ast.FuncDecl{shadow: callee},
	}
	worker = &Analyzer{
		callEdgeCache:   map[*ast.FuncDecl][]*ast.FuncDecl{fn: {shadow}},
		storeFlowSCC:    map[*ast.FuncDecl][]*ast.FuncDecl{fn: {fn, shadow}},
		storeFlowActive: map[*ast.FuncDecl]bool{fn: true},
	}
	dst := &Analyzer{}
	baseValue := reflect.ValueOf(dst).Elem()
	baseSnapshot := reflect.New(reflect.TypeOf(Analyzer{})).Elem()
	baseSnapshot.Set(baseValue)
	mergeValue(baseValue, reflect.ValueOf(worker).Elem(), baseSnapshot, p)
	if dst.callEdgeCache != nil || dst.storeFlowSCC != nil || dst.storeFlowActive != nil {
		t.Fatal("worker-private call-graph state leaked into the base analyzer during merge")
	}
}

func TestParallelWorkerDoesNotExportShadowCallGraphCaches(t *testing.T) {
	call := func(name string) ast.Stmt { return &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: name}}} }
	owner := &ast.FuncDecl{Name: "owner", Body: []ast.Stmt{call("callee")}}
	callee := &ast.FuncDecl{Name: "callee", Body: []ast.Stmt{call("owner")}}
	snapshot := func(fn *ast.FuncDecl) *ast.SyntaxSnapshot {
		s, err := ast.NewSyntaxSnapshot(&ast.File{Decls: []ast.Decl{fn}}, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	slots := make([]atomic.Int32, 2)
	slots[0].Store(0)
	slots[1].Store(1)
	p := &parallelWorkerState{
		worker: 0, snapshots: []*ast.SyntaxSnapshot{snapshot(owner), snapshot(callee)},
		at:          map[*ast.FuncDecl][2]int{owner: {0, -1}, callee: {1, -1}},
		slots:       slots,
		shadows:     map[*ast.FuncDecl]*ast.FuncDecl{},
		readShadows: map[*ast.FuncDecl]*ast.FuncDecl{},
		original:    map[*ast.FuncDecl]*ast.FuncDecl{},
		imported:    map[*ast.FuncDecl]bool{},
	}
	a := &Analyzer{
		funcDeclSymbols: map[*ast.FuncDecl]*Symbol{owner: {Name: "owner"}, callee: {Name: "callee"}},
		parallel:        p,
	}
	if edges := a.callEdgesOf(owner); len(edges) != 1 || edges[0] == callee || p.original[edges[0]] != callee {
		t.Fatalf("expected the cold worker call-edge cache to contain a foreign shadow, got %#v", edges)
	}
	component := a.storeFlowComponent(owner)
	foundShadow := false
	for _, member := range component {
		if p.original[member] == callee {
			foundShadow = true
		}
	}
	if !foundShadow {
		t.Fatalf("expected the store-flow SCC cache to contain the foreign shadow, got %#v", component)
	}

	s := &bodySchedule{
		funcs: [][]*ast.FuncDecl{{owner}}, exported: make([]map[int]reflect.Value, 1),
		nodes: make([][]reflect.Value, 1), stmts: make([][]ast.Stmt, 1),
		done: []chan struct{}{make(chan struct{})}, sccOf: []int{0},
		sccLeft: map[int]int{0: 1}, sccPublish: map[int][][2]*FuncType{},
	}
	s.exportUnit(a, 0)
	for pathIndex, path := range exportDeclMapPaths() {
		name := reflect.TypeOf(Analyzer{}).Field(path[0]).Name
		if name == "callEdgeCache" || name == "storeFlowSCC" {
			if _, exported := s.exported[0][pathIndex]; exported {
				t.Errorf("worker-local shadow cache %s was exported", name)
			}
		}
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
