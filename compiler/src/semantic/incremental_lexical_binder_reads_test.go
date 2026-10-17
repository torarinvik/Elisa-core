//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func binderReadFixture(t *testing.T, prefix string) (*ast.FuncDecl, *ast.NamedType, *Analyzer, *lexicalReadInvocation) {
	t.Helper()
	fn := parseProductFunction(t, prefix+"def work[T](value: T) -> T:\n    copy: T = value\n    return copy\n")
	index, err := IndexFunctionSource(fn, productContext())
	if err != nil {
		t.Fatal(err)
	}
	site := fn.Body[0].(*ast.VarDeclStmt).Type.(*ast.NamedType)
	reads := &lexicalReadInvocation{primaryOwner: fn, indexes: map[*ast.FuncDecl]*FunctionSourceIndex{fn: index}}
	global := NewScope(nil)
	scope := NewScope(global)
	scope.Define(&Symbol{Name: "copy", Kind: SymbolLocal, Node: fn.Body[0]})
	a := &Analyzer{currentFuncDecl: fn, currentScope: scope, globalScope: global, lexicalReads: reads, lexicalBinders: &lexicalBinderOwnership{}}
	return fn, site, a, reads
}

func TestLexicalBinderReadPointCompositionAndFreshTypes(t *testing.T) {
	session := NewIncrementalAnalysisSession()
	for pass, name := range []string{"i64", "bool"} {
		prefix := ""
		if pass != 0 {
			prefix = "\n\n"
		}
		fn, site, a, reads := binderReadFixture(t, prefix)
		value := &BuiltinType{Name: name}
		a.withSourceTypeParams(fn, []string{"T"}, []Type{value}, func() {
			got, found := a.lookupTypeParamAt(site)
			if !found || got != value {
				t.Fatal("live lookup changed")
			}
			a.lookupIdentifierScope(fn.Body[1].(*ast.ReturnStmt).Value.(*ast.Ident))
			a.typeParamScopes[0]["T"] = &BuiltinType{Name: "later mutation"}
		})
		before := session.Stats()
		reads.publish(session)
		after := session.Stats()
		if after.LexicalBinderSelectionsVerified-before.LexicalBinderSelectionsVerified != 1 || after.LexicalSelectionsVerified-before.LexicalSelectionsVerified != 1 || after.LexicalVerificationFailures != 0 {
			t.Fatalf("incoherent read worlds: %+v", after)
		}
		if pass == 1 && (after.LexicalQueriesExecuted != before.LexicalQueriesExecuted || after.LexicalQueryMemoHits-before.LexicalQueryMemoHits != 2) {
			t.Fatalf("selection failed to reuse with fresh Type objects: %+v -> %+v", before, after)
		}
	}
	if session.lexical.Stats().Publications != 2 || session.lexical.Stats().RetainedQueries != 2 {
		t.Fatal("symbol and binder reads retired each other")
	}
}

func TestLexicalTypeBinderNilAndNegativeReads(t *testing.T) {
	for _, domain := range []string{"type-param", "assoc-type"} {
		t.Run(domain, func(t *testing.T) {
			fn, site, a, reads := binderReadFixture(t, "")
			lookup := func() (Type, bool) {
				if domain == "type-param" {
					return a.lookupTypeParamAt(site)
				}
				return a.lookupInterfaceAssocTypeAt(site)
			}
			if value, found := lookup(); value != nil || found {
				t.Fatal("empty stack is not a miss")
			}
			session := NewIncrementalAnalysisSession()
			reads.publish(session)
			before := session.Stats()
			// A new current world at the same site adds an owned nil blocker.
			reads = &lexicalReadInvocation{primaryOwner: fn, indexes: reads.indexes}
			a.lexicalReads = reads
			active := func() {
				if domain == "type-param" {
					a.typeParamScopes[0]["T"] = nil
				}
				if value, found := lookup(); value != nil || !found {
					t.Fatal("present nil should stop and report found")
				}
			}
			if domain == "type-param" {
				a.withSourceTypeParams(fn, []string{"T"}, nil, active)
			} else {
				a.withSourceInterfaceAssocTypes(fn, map[string]Type{"T": nil}, active)
			}
			reads.publish(session)
			if stats := session.Stats(); stats.LexicalBinderSelectionsVerified-before.LexicalBinderSelectionsVerified != 1 || stats.LexicalVerificationFailures != 0 || stats.LexicalQueriesExecuted-before.LexicalQueriesExecuted != 1 {
				t.Fatalf("nil blocker parity failed: %+v", stats)
			}
		})
	}
}

func TestLexicalTypeBinderDeclinesUnownedOrReplacedFrame(t *testing.T) {
	for _, replace := range []bool{false, true} {
		fn, site, a, reads := binderReadFixture(t, "")
		value := &BuiltinType{Name: "i64"}
		active := func() {
			if replace {
				a.typeParamScopes[0] = map[string]Type{"T": value}
			}
			got, found := a.lookupTypeParamAt(site)
			if !found || got != value {
				t.Fatal("decline altered resolver behavior")
			}
		}
		if replace {
			a.withSourceTypeParams(fn, []string{"T"}, []Type{value}, active)
		} else {
			a.withTypeParams([]string{"T"}, []Type{value}, active)
		}
		session := NewIncrementalAnalysisSession()
		reads.publish(session)
		if stats := session.Stats(); stats.LexicalBinderReadsDropped != 1 || stats.LexicalBinderSelectionsVerified != 0 || stats.LexicalVerificationFailures != 0 {
			t.Fatalf("unowned frame admitted: %+v", stats)
		}
	}
}

func TestLexicalTypeBinderConflictingRepeatedVisitDeclines(t *testing.T) {
	fn, site, a, reads := binderReadFixture(t, "")
	outer, inner := &BuiltinType{Name: "i64"}, &BuiltinType{Name: "bool"}
	a.withSourceTypeParams(fn, []string{"T"}, []Type{outer}, func() {
		a.lookupTypeParamAt(site)
		a.withSourceTypeParams(fn.Body[0], []string{"T"}, []Type{inner}, func() { a.lookupTypeParamAt(site) })
		a.lookupTypeParamAt(site)
	})
	session := NewIncrementalAnalysisSession()
	reads.publish(session)
	if stats := session.Stats(); stats.LexicalBinderSelectionsVerified != 0 || stats.LexicalBinderReadsDropped == 0 || stats.LexicalVerificationFailures != 0 {
		t.Fatalf("conflicting repeated binder read admitted: %+v", stats)
	}
}

func TestIncrementalSessionPublishesActualTypeBinderReads(t *testing.T) {
	for _, source := range []string{
		"def work[T](value: T) -> T:\n    copy: T = value\n    return copy\n",
		"def work(value: i64) -> i64:\n    copy: i64 = value\n    return copy\n",
	} {
		session := NewIncrementalAnalysisSession()
		for pass, text := range []string{source, "\n\n" + source} {
			before := session.Stats()
			result := session.Analyze(parseIncrementalSessionFile(t, text), AnalyzeOptions{})
			after := session.Stats()
			if len(result.Errors()) != 0 {
				t.Fatal(result.Errors())
			}
			assertIncrementalResultParity(t, AnalyzeWithOptions(parseIncrementalSessionFile(t, text), AnalyzeOptions{}), result)
			if after.LexicalBinderReadsObserved <= before.LexicalBinderReadsObserved || after.LexicalBinderSelectionsVerified <= before.LexicalBinderSelectionsVerified || after.LexicalVerificationFailures != 0 {
				t.Fatalf("no actual type resolver reads: %+v", after)
			}
			if pass == 1 && after.LexicalQueryMemoHits <= before.LexicalQueryMemoHits {
				t.Fatal("no repeated binder selection memo reuse")
			}
		}
	}
}

func TestLexicalTypedNilBinderPreservesLiveLookup(t *testing.T) {
	fn, site, a, reads := binderReadFixture(t, "")
	var typedNil *TypeParamType
	a.withSourceTypeParams(fn, []string{"T"}, nil, func() {
		a.typeParamScopes[0]["T"] = typedNil
		value, found := a.lookupTypeParamAt(site)
		if !found || value != typedNil {
			t.Fatal("typed nil lookup changed")
		}
	})
	session := NewIncrementalAnalysisSession()
	reads.publish(session)
	if stats := session.Stats(); stats.LexicalBinderSelectionsVerified != 1 || stats.LexicalVerificationFailures != 0 {
		t.Fatalf("typed nil rebinding failed: %+v", stats)
	}
}

func TestLexicalBinderCaptureBudgetPreservesResolver(t *testing.T) {
	fn, site, a, reads := binderReadFixture(t, "")
	value := &BuiltinType{Name: "i64"}
	reads.frames = lexicalReadMaxFrames
	a.withSourceTypeParams(fn, []string{"T"}, []Type{value}, func() {
		got, found := a.lookupTypeParamAt(site)
		if got != value || !found {
			t.Fatal("frame budget changed lookup")
		}
	})
	if reads.bindersDropped != 1 || len(reads.binders) != 0 {
		t.Fatal("binder frame overflow not explicit")
	}
	reads.frames = 0
	reads.observations = make([]lexicalSymbolObservation, lexicalReadMaxObservations)
	a.withSourceTypeParams(fn, []string{"T"}, []Type{value}, func() { a.lookupTypeParamAt(site) })
	if reads.bindersDropped != 2 || len(reads.binders) != 0 {
		t.Fatal("shared observation budget not enforced")
	}
}
