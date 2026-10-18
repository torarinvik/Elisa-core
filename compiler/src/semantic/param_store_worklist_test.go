package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func BenchmarkParamStoreTargetsChain(b *testing.B) {
	for _, n := range []int{16, 64, 256} {
		for _, reverse := range []bool{false, true} {
			b.Run(fmt.Sprintf("depth%d/reverse%v", n, reverse), func(b *testing.B) {
				funcs := make([]*ast.FuncDecl, n)
				a := &Analyzer{globalScope: NewScope(nil), funcDeclSymbols: map[*ast.FuncDecl]*Symbol{}}
				for i := range funcs {
					fn := &ast.FuncDecl{Name: fmt.Sprintf("relay%d", i), Params: []ast.ParamDecl{{Name: "value"}}}
					if i == n-1 {
						fn.Body = []ast.Stmt{&ast.AssignStmt{Target: &ast.Ident{Name: "sink"}, Value: &ast.Ident{Name: "value"}}}
					} else {
						fn.Body = []ast.Stmt{&ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: fmt.Sprintf("relay%d", i+1)}, Args: []ast.Expr{&ast.Ident{Name: "value"}}}}}
					}
					sym := &Symbol{Name: fn.Name, Node: fn, Type: &FuncType{Params: []Type{&SViewType{}}}}
					a.funcDeclSymbols[fn] = sym
					a.globalScope.Define(sym)
					funcs[i] = fn
				}
				first := funcs[0]
				if reverse {
					for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
						funcs[i], funcs[j] = funcs[j], funcs[i]
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					got := a.computeParamStoreTargets(funcs)
					if !got[first][0][storeTargetGlobal] {
						b.Fatal("chain lost store target")
					}
				}
			})
		}
	}
}

func TestParamStoreTargetsWorklistParity(t *testing.T) {
	rng := rand.New(rand.NewSource(20261018))
	for round := 0; round < 32; round++ {
		n := 8 + rng.Intn(41)
		funcs := make([]*ast.FuncDecl, n)
		a := &Analyzer{globalScope: NewScope(nil), funcDeclSymbols: map[*ast.FuncDecl]*Symbol{}}
		for i := range funcs {
			fn := &ast.FuncDecl{Name: fmt.Sprintf("relay%d", i), Params: []ast.ParamDecl{{Name: "value"}}}
			funcs[i] = fn
			if rng.Intn(8) == 0 {
				fn.Body = append(fn.Body, &ast.AssignStmt{Target: &ast.Ident{Name: "sink"}, Value: &ast.Ident{Name: "value"}})
			}
			for k := rng.Intn(4); k > 0; k-- {
				fn.Body = append(fn.Body, &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: fmt.Sprintf("relay%d", rng.Intn(n))}, Args: []ast.Expr{&ast.Ident{Name: "value"}}}})
			}
			sym := &Symbol{Name: fn.Name, Node: fn, Type: &FuncType{Params: []Type{&SViewType{}}}}
			a.funcDeclSymbols[fn] = sym
			a.globalScope.Define(sym)
		}
		rng.Shuffle(len(funcs), func(i, j int) { funcs[i], funcs[j] = funcs[j], funcs[i] })
		want := a.computeParamStoreTargetsFixedPoint(funcs)
		got := a.computeParamStoreTargets(funcs)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round%d: worklist summaries differ", round)
		}
	}
}

func TestParamStoreTargetsWorklistMatrixParity(t *testing.T) {
	rng := rand.New(rand.NewSource(20261019))
	names := []string{"p0", "p1", "p2"}
	for round := 0; round < 64; round++ {
		n := 8 + rng.Intn(25)
		funcs := make([]*ast.FuncDecl, n)
		a := &Analyzer{globalScope: NewScope(nil), funcDeclSymbols: map[*ast.FuncDecl]*Symbol{}}
		for i := range funcs {
			fn := &ast.FuncDecl{Name: fmt.Sprintf("matrix%d", i)}
			for _, name := range names {
				fn.Params = append(fn.Params, ast.ParamDecl{Name: name})
			}
			for k := rng.Intn(3); k > 0; k-- {
				target := []string{"p0", "p1", "p2", "sink"}[rng.Intn(4)]
				fn.Body = append(fn.Body, &ast.AssignStmt{Target: &ast.Ident{Name: target}, Value: &ast.Ident{Name: names[rng.Intn(3)]}})
			}
			for k := rng.Intn(3); k > 0; k-- {
				args := []ast.Expr{}
				for j := 0; j < 3; j++ {
					args = append(args, &ast.Ident{Name: []string{"p0", "p1", "p2", "globalArg"}[rng.Intn(4)]})
				}
				fn.Body = append(fn.Body, &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: fmt.Sprintf("matrix%d", rng.Intn(n))}, Args: args}})
			}
			funcs[i] = fn
			sym := &Symbol{Name: fn.Name, Node: fn, Type: &FuncType{Params: []Type{&SViewType{}, &SViewType{}, &SViewType{}}}}
			a.funcDeclSymbols[fn] = sym
			a.globalScope.Define(sym)
		}
		want := a.computeParamStoreTargetsFixedPoint(funcs)
		for order := 0; order < 4; order++ {
			sequence := append([]*ast.FuncDecl(nil), funcs...)
			switch order {
			case 1:
				for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
					sequence[i], sequence[j] = sequence[j], sequence[i]
				}
			case 2:
				rng.Shuffle(n, func(i, j int) { sequence[i], sequence[j] = sequence[j], sequence[i] })
			case 3:
				sequence = append(sequence, nil, funcs[0], funcs[n-1], funcs[0])
			}
			got := a.computeParamStoreTargets(sequence)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round%d order%d full matrix differs", round, order)
			}
		}
	}
	a := &Analyzer{}
	if len(a.computeParamStoreTargets(nil)) != 0 {
		t.Fatal("empty graph")
	}
}
