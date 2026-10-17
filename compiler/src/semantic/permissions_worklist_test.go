//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// The prior whole-program sweep is the scheduling oracle. Both solvers use the
// production equations, so differences expose missing scheduling dependencies.
func permissionReferenceSweep(a *Analyzer, decls []scopedDecl) int {
	evaluations := 0
	for iter := 0; iter < len(decls)+4; iter++ {
		changed := false
		for _, decl := range decls {
			_, different := a.recomputeFunctionPermissionEffects(decl)
			changed = changed || different
			evaluations++
		}
		if !changed {
			break
		}
	}
	return evaluations
}

type permissionGraphFixture struct {
	a         *Analyzer
	decls     []scopedDecl
	functions []*ast.FuncDecl
	types     []*FuncType
}

func newPermissionGraphFixture(n int) *permissionGraphFixture {
	a := &Analyzer{globalScope: NewScope(nil), functionTypes: map[string]*FuncType{}, exprTypes: map[ast.Expr]Type{}, funcDeclSymbols: map[*ast.FuncDecl]*Symbol{}, implicitDropSites: map[ast.Node][]DropSite{}, dropHooks: map[string]*dropHook{}, resolvedCastHooks: map[ast.Expr]*Symbol{}}
	a.currentScope = a.globalScope
	f := &permissionGraphFixture{a: a}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("f%d", i)
		fn := &ast.FuncDecl{Name: name}
		ft := &FuncType{Name: name}
		sym := &Symbol{Name: name, Kind: SymbolFunc, Node: fn, Type: ft}
		a.globalScope.Symbols[name] = sym
		a.funcDeclSymbols[fn] = sym
		a.functionTypes[name] = ft
		f.functions = append(f.functions, fn)
		f.types = append(f.types, ft)
		f.decls = append(f.decls, scopedDecl{Decl: fn})
	}
	leaf := &FuncType{Name: "native", PermissionRefs: []ast.PermissionRef{{Name: "Io", Member: "Close"}, {Name: "Global", Member: "Write"}}, MutableGlobalPermissionRefs: []ast.PermissionRef{{Name: "Global", Member: "Write"}}}
	a.functionTypes["native"] = leaf
	a.globalScope.Symbols["native"] = &Symbol{Name: "native", Kind: SymbolExternFunc, Type: leaf}
	return f
}
func (f *permissionGraphFixture) call(ft *FuncType) *ast.CallExpr {
	ident := &ast.Ident{Name: ft.Name}
	f.a.exprTypes[ident] = ft
	return &ast.CallExpr{Func: ident}
}
func (f *permissionGraphFixture) edge(from, to int) {
	ft := f.a.functionTypes["native"]
	if to >= 0 {
		ft = f.types[to]
	}
	f.functions[from].Body = append(f.functions[from].Body, &ast.ExprStmt{Expr: f.call(ft)})
}
func permissionFixtureRows(f *permissionGraphFixture) map[string]any {
	rows := map[string]any{}
	for name, ft := range f.a.functionTypes {
		rows[name] = struct {
			Refs, Globals, Returned, ReturnedGlobals []ast.PermissionRef
			Families                                 []string
		}{ft.PermissionRefs, ft.MutableGlobalPermissionRefs, ft.ReturnedFunctionPermissionRefs, ft.ReturnedFunctionGlobalPermissionRefs, ft.Permissions}
	}
	return rows
}

func TestPermissionWorklistMatchesSweep(t *testing.T) {
	cases := map[string]func(*permissionGraphFixture){
		"reverse_chain": func(f *permissionGraphFixture) {
			for i := 0; i < len(f.types)-1; i++ {
				f.edge(i, i+1)
			}
			f.edge(len(f.types)-1, -1)
		},
		"cycle_diamond": func(f *permissionGraphFixture) {
			f.edge(0, 1)
			f.edge(0, 2)
			f.edge(1, 3)
			f.edge(2, 3)
			f.edge(3, 0)
			f.edge(3, -1)
		},
		"specialized_copy": func(f *permissionGraphFixture) {
			f.functions[0].Body = []ast.Stmt{&ast.ExprStmt{Expr: f.call(&FuncType{Name: "f3"})}}
			f.edge(3, 4)
			f.edge(4, -1)
		},
		"source_alias": func(f *permissionGraphFixture) {
			f.functions[0].Body = []ast.Stmt{&ast.ExprStmt{Expr: f.call(&FuncType{Name: "alias", MutableGlobalSourceNames: []string{"f3"}})}}
			f.edge(3, 4)
			f.edge(4, -1)
		},
		"callback_transfer": func(f *permissionGraphFixture) {
			arg := &ast.Ident{Name: "f3"}
			f.a.exprTypes[arg] = f.types[3]
			call := f.call(&FuncType{Name: "higher"})
			call.Args = []ast.Expr{arg}
			f.functions[0].Body = []ast.Stmt{&ast.ExprStmt{Expr: call}}
			f.edge(3, 4)
			f.edge(4, -1)
		},
		"returned_callback": func(f *permissionGraphFixture) {
			f.functions[0].Body = []ast.Stmt{&ast.ExprStmt{Expr: f.call(&FuncType{Name: "thunk", FunctionReturnSourceNames: []string{"f2"}})}}
			ident := &ast.Ident{Name: "f3"}
			f.a.exprTypes[ident] = f.types[3]
			f.types[2].Return = &FuncType{}
			f.functions[2].Body = []ast.Stmt{&ast.ReturnStmt{Value: ident}}
			f.edge(3, 4)
			f.edge(4, -1)
		},
		"lambda_globals": func(f *permissionGraphFixture) {
			f.functions[0].Body = []ast.Stmt{&ast.ExprStmt{Expr: f.call(&FuncType{Name: "thunk", FunctionReturnSourceNames: []string{"f2"}})}}
			lambda := &ast.LambdaExpr{Body: []ast.Stmt{&ast.ExprStmt{Expr: f.call(f.types[3])}}}
			ft := &FuncType{Name: "lambda"}
			f.a.exprTypes[lambda] = ft
			f.a.functionTypes["lambda"] = ft
			f.types[2].Return = &FuncType{}
			f.functions[2].Body = []ast.Stmt{&ast.ReturnStmt{Value: lambda}}
			f.edge(3, 4)
			f.edge(4, -1)
		},
		"drop_only": func(f *permissionGraphFixture) {
			f.a.implicitDropSites[f.functions[0]] = []DropSite{{TypeName: "R"}}
			f.a.dropHooks["R"] = &dropHook{FnType: f.types[3]}
			f.edge(3, 4)
			f.edge(4, -1)
		},
		"cast_hook": func(f *permissionGraphFixture) {
			cast := &ast.CastExpr{Operand: &ast.IntLit{Value: "0"}}
			f.a.resolvedCastHooks[cast] = f.a.funcDeclSymbols[f.functions[3]]
			f.functions[0].Body = []ast.Stmt{&ast.ExprStmt{Expr: cast}}
			f.edge(3, 4)
			f.edge(4, -1)
		},
		"lowered_initializer": func(f *permissionGraphFixture) {
			lit := &ast.StructLitExpr{}
			f.a.loweredInitCalls = map[*ast.StructLitExpr]*ast.CallExpr{lit: f.call(f.types[3])}
			f.functions[0].Body = []ast.Stmt{&ast.ExprStmt{Expr: lit}}
			f.edge(3, 4)
			f.edge(4, -1)
		},
		"lowered_operator": func(f *permissionGraphFixture) {
			f.functions[0].Body = []ast.Stmt{&ast.ExprStmt{Expr: &ast.BinaryExpr{LoweredCall: f.call(f.types[3])}}}
			f.edge(3, 4)
			f.edge(4, -1)
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			work, reference := newPermissionGraphFixture(16), newPermissionGraphFixture(16)
			setup(work)
			setup(reference)
			evaluations := work.a.inferFunctionPermissionEffectsWorklist(work.decls)
			oldEvaluations := permissionReferenceSweep(reference.a, reference.decls)
			if !reflect.DeepEqual(permissionFixtureRows(work), permissionFixtureRows(reference)) {
				t.Fatalf("rows differ:\nwork %#v\nsweep %#v", permissionFixtureRows(work), permissionFixtureRows(reference))
			}
			if work.a.permissionInference != nil {
				t.Fatal("dependency observer leaked past inference")
			}
			t.Logf("evaluations worklist=%d sweep=%d", evaluations, oldEvaluations)
		})
	}
}

func TestPermissionWorklistRandomGraphs(t *testing.T) {
	for seed := int64(0); seed < 100; seed++ {
		setup := func(f *permissionGraphFixture) {
			r := rand.New(rand.NewSource(seed))
			for i := range f.types {
				for j := 0; j < 3; j++ {
					f.edge(i, r.Intn(len(f.types)+1)-1)
				}
			}
		}
		work, reference := newPermissionGraphFixture(24), newPermissionGraphFixture(24)
		setup(work)
		setup(reference)
		work.a.inferFunctionPermissionEffectsWorklist(work.decls)
		permissionReferenceSweep(reference.a, reference.decls)
		if !reflect.DeepEqual(permissionFixtureRows(work), permissionFixtureRows(reference)) {
			t.Fatalf("seed %d rows differ", seed)
		}
	}
}

func TestPermissionWorklistSkipsUnrelatedBodies(t *testing.T) {
	f := newPermissionGraphFixture(256)
	for i := 0; i < 63; i++ {
		f.edge(i, i+1)
	}
	f.edge(63, -1)
	evaluations := f.a.inferFunctionPermissionEffectsWorklist(f.decls)
	if evaluations > 2*len(f.decls)+64 {
		t.Fatalf("unrelated bodies rescanned: %d", evaluations)
	}
	if len(f.types[0].PermissionRefs) != 2 {
		t.Fatalf("chain did not converge: %#v", f.types[0].PermissionRefs)
	}
}

func BenchmarkPermissionEffectInference(b *testing.B) {
	for _, n := range []int{16, 128} {
		for _, mode := range []string{"sweep", "worklist"} {
			b.Run(fmt.Sprintf("chain%d/%s", n, mode), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					f := newPermissionGraphFixture(n)
					for j := 0; j < n-1; j++ {
						f.edge(j, j+1)
					}
					f.edge(n-1, -1)
					b.StartTimer()
					if mode == "sweep" {
						permissionReferenceSweep(f.a, f.decls)
					} else {
						f.a.inferFunctionPermissionEffectsWorklist(f.decls)
					}
				}
			})
		}
	}
}

func BenchmarkPermissionEffectInferenceStable(b *testing.B) {
	for _, n := range []int{16, 128} {
		for _, mode := range []string{"sweep", "worklist"} {
			b.Run(fmt.Sprintf("chain%d/%s", n, mode), func(b *testing.B) {
				f := newPermissionGraphFixture(n)
				for j := 0; j < n-1; j++ {
					f.edge(j, j+1)
				}
				f.edge(n-1, -1)
				permissionReferenceSweep(f.a, f.decls)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if mode == "sweep" {
						permissionReferenceSweep(f.a, f.decls)
					} else {
						f.a.inferFunctionPermissionEffectsWorklist(f.decls)
					}
				}
			})
		}
	}
}

func permissionSourceChain(n int) string {
	var source strings.Builder
	source.WriteString("extern native() can[Memory.Allocate]\n")
	for i := 0; i < n; i++ {
		callee := "native"
		if i+1 < n {
			callee = fmt.Sprintf("work%d", i+1)
		}
		fmt.Fprintf(&source, "def work%d():\n    %s()\n\n", i, callee)
	}
	return source.String()
}

func TestPermissionWorklistCompleteAnalysisParity(t *testing.T) {
	for _, n := range []int{1, 16, 64} {
		source := permissionSourceChain(n)
		reference := analyzeWithEffectInference(parseIncrementalSessionFile(t, source), AnalyzeOptions{}, nil, func(a *Analyzer, decls []scopedDecl) { permissionReferenceSweep(a, permissionFunctionDecls(decls)) })
		actual := Analyze(parseIncrementalSessionFile(t, source))
		if len(actual.Errors()) != 0 {
			t.Fatal(actual.Errors())
		}
		assertIncrementalResultParity(t, reference, actual)
	}
}

func BenchmarkPermissionInferenceWholeAnalysis(b *testing.B) {
	for _, n := range []int{16, 128} {
		for _, mode := range []string{"sweep", "worklist"} {
			b.Run(fmt.Sprintf("chain%d/%s", n, mode), func(b *testing.B) {
				source := permissionSourceChain(n)
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					l := lexer.New("effect_bench.elisa", []byte(source))
					p := parser.New(l.Tokenize())
					file := p.ParseFile("effect_bench.elisa")
					if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
						b.Fatalf("lexer %v parser %v", l.Errors(), p.Errors())
					}
					var result *Result
					if mode == "sweep" {
						result = analyzeWithEffectInference(file, AnalyzeOptions{}, nil, func(a *Analyzer, decls []scopedDecl) { permissionReferenceSweep(a, permissionFunctionDecls(decls)) })
					} else {
						result = Analyze(file)
					}
					if len(result.Errors()) != 0 {
						b.Fatal(result.Errors())
					}
				}
			})
		}
	}
}

func TestPermissionWorklistStableFastPath(t *testing.T) {
	for _, withCalls := range []bool{false, true} {
		f := newPermissionGraphFixture(64)
		if withCalls {
			for i := 0; i < 63; i++ {
				f.edge(i, i+1)
			}
			f.edge(63, -1)
			permissionReferenceSweep(f.a, f.decls)
		}
		before := permissionFixtureRows(f)
		if n := f.a.inferFunctionPermissionEffectsWorklist(f.decls); n != len(f.decls) {
			t.Fatalf("stable graph took %d evaluations", n)
		}
		if f.a.permissionInference != nil || !reflect.DeepEqual(before, permissionFixtureRows(f)) {
			t.Fatal("stable pass changed rows or retained observer")
		}
	}
}
