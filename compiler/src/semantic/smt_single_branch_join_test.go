package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
	"testing"
)

func (a *Analyzer) referenceRecordIfJoinSMTFact(branches [][]smtFact) {
	if a == nil || a.currentScope == nil || len(branches) == 0 {
		return
	}
	pins := map[string]*Symbol{}
	deps := map[string]bool{}
	var disjuncts []ast.Expr
	for _, facts := range branches {
		var conj ast.Expr
		kept := 0
		for _, fact := range facts {
			if fact.Expr == nil || fact.Binders != nil || kept >= ifJoinMaxFactsPerBranch {
				continue
			}
			current := true
			for name, pinned := range fact.Pins {
				now, _ := a.currentScope.Lookup(name)
				if now != pinned {
					current = false
					break
				}
				if prior, seen := pins[name]; seen && prior != pinned {
					current = false
					break
				}
			}
			if !current {
				continue
			}
			for name, pinned := range fact.Pins {
				pins[name] = pinned
			}
			for dep := range fact.Deps {
				deps[dep] = true
			}
			kept++
			if conj == nil {
				conj = fact.Expr
			} else {
				conj = &ast.BinaryExpr{Position: fact.Expr.Pos(), Op: lexer.TOKEN_AND, Left: conj, Right: fact.Expr}
			}
		}
		if conj == nil {
			return // this branch constrains nothing: the disjunction is trivially true
		}
		disjuncts = append(disjuncts, conj)
	}
	var joined ast.Expr
	for _, d := range disjuncts {
		if joined == nil {
			joined = d
		} else {
			joined = &ast.BinaryExpr{Position: d.Pos(), Op: lexer.TOKEN_OR, Left: joined, Right: d}
		}
	}
	if len(disjuncts) == 1 {
		// A single fall-through branch: its facts hold outright; record them as separate facts.
		for _, fact := range branches[0] {
			if fact.Expr == nil || fact.Binders != nil {
				continue
			}
			current := true
			for name, pinned := range fact.Pins {
				if now, _ := a.currentScope.Lookup(name); now != pinned {
					current = false
					break
				}
			}
			if current {
				a.currentScope.smtAssertFacts = append(a.currentScope.smtAssertFacts, fact)
			}
		}
		return
	}
	a.currentScope.smtAssertFacts = append(a.currentScope.smtAssertFacts, smtFact{Expr: joined, Deps: deps, Pins: pins})
}

func singleBranchJoinFixture(count int, mixed bool) (*Scope, []smtFact) {
	scope := NewScope(nil)
	sym := &Symbol{Name: "p"}
	scope.Define(sym)
	facts := make([]smtFact, count)
	for i := range facts {
		facts[i] = smtFact{Expr: &ast.Ident{Name: "p"}, Deps: map[string]bool{"p": true}, Pins: map[string]*Symbol{"p": sym}}
		if mixed {
			if i%5 == 0 {
				facts[i].Expr = nil
			}
			if i%7 == 0 {
				facts[i].Binders = map[string]*Symbol{"binder": sym}
			}
			if i%11 == 0 {
				facts[i].Pins = map[string]*Symbol{"p": {Name: "p"}}
			}
		}
	}
	return scope, facts
}

func TestSingleBranchJoinPreservesPriorFactSelection(t *testing.T) {
	for _, count := range []int{0, 1, 24, 40, 256} {
		for branches := 0; branches <= 2; branches++ {
			scope, facts := singleBranchJoinFixture(count, true)
			seed := smtFact{Expr: &ast.BoolLit{Value: true}}
			scope.smtAssertFacts = []smtFact{seed}
			other := *scope
			other.smtAssertFacts = []smtFact{seed}
			var inputs [][]smtFact
			for i := 0; i < branches; i++ {
				inputs = append(inputs, facts)
			}
			before := &Analyzer{currentScope: scope}
			after := &Analyzer{currentScope: &other}
			before.referenceRecordIfJoinSMTFact(inputs)
			after.recordIfJoinSMTFact(inputs)
			if !reflect.DeepEqual(scope.smtAssertFacts, other.smtAssertFacts) {
				t.Fatalf("facts changed for %d entries, %d branches", count, branches)
			}
		}
	}
}

func BenchmarkSingleBranchFactJoin(b *testing.B) {
	for _, tc := range []struct {
		name string
		run  func(*Analyzer, [][]smtFact)
	}{{"prior", (*Analyzer).referenceRecordIfJoinSMTFact}, {"direct", (*Analyzer).recordIfJoinSMTFact}} {
		b.Run(tc.name, func(b *testing.B) {
			scope, facts := singleBranchJoinFixture(40, false)
			a := &Analyzer{currentScope: scope}
			inputs := [][]smtFact{facts}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				scope.smtAssertFacts = scope.smtAssertFacts[:0]
				tc.run(a, inputs)
				if len(scope.smtAssertFacts) != 40 {
					b.Fatal("lost single-branch facts")
				}
			}
		})
	}
}
