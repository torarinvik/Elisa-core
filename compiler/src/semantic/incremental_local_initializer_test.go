//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"fmt"
	"strings"
	"testing"
)

func TestIncrementalLocalInitializersReuseCurrentBindings(t *testing.T) {
	source := "def work(value: i64) -> i64:\n    first: i64 = value + 1\n    second: i64 = first * 2\n    return second + 1\n"
	session := NewIncrementalAnalysisSession()
	first := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	if len(first.Errors()) != 0 {
		t.Fatal(first.Errors())
	}
	if session.Stats().LocalInitializersChecked != 2 {
		t.Fatalf("initializers not checked: %+v", session.Stats())
	}
	before := session.Stats()
	warm := session.Analyze(parseIncrementalSessionFile(t, "\n\n"+source), AnalyzeOptions{})
	if session.Stats().LocalInitializersReused-before.LocalInitializersReused != 2 {
		t.Fatalf("initializers not reused: %+v", session.Stats())
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, "\n\n"+source)), warm)
	if session.Stats().PrimaryBodiesReused != before.PrimaryBodiesReused {
		t.Fatal("initializer reuse counted as whole body reuse")
	}
	// The earlier unchanged prefix remains reusable; the edited initializer is checked.
	edited := strings.Replace(source, "first * 2", "first * 3", 1)
	before = session.Stats()
	result := session.Analyze(parseIncrementalSessionFile(t, edited), AnalyzeOptions{})
	if session.Stats().LocalInitializersReused-before.LocalInitializersReused != 1 || session.Stats().LocalInitializersChecked-before.LocalInitializersChecked != 1 {
		t.Fatalf("wrong edit invalidation: before%+v after%+v", before, session.Stats())
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, edited)), result)
	// Changing a preceding value invalidates downstream constant/range-dependent work.
	edited = strings.Replace(edited, "value + 1", "value + 2", 1)
	before = session.Stats()
	result = session.Analyze(parseIncrementalSessionFile(t, edited), AnalyzeOptions{})
	if session.Stats().LocalInitializersReused != before.LocalInitializersReused || session.Stats().LocalInitializersChecked-before.LocalInitializersChecked != 2 {
		t.Fatal("preceding value edit failed to invalidate later arithmetic")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, edited)), result)
}

func TestIncrementalLocalInitializerErrorfulReuseAndParity(t *testing.T) {
	cleanSource := "def work(value: i64) -> i64:\n    first: i64 = value + 1\n    second: i64 = first * 2\n    return second\n"
	badFunction := "def broken(value: i64) -> i64:\n    bad: i64 = value + true\n    return bad\n\n"
	badFile := parseIncrementalSessionFile(t, badFunction)
	badDecl, ok := badFile.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatal("diagnostic fixture did not parse as a function")
	}
	if _, admitted := primitiveLocalInitializers(badDecl); admitted {
		t.Fatal("diagnostic-producing expression unexpectedly entered the closed initializer cache grammar")
	}
	session := NewIncrementalAnalysisSession()
	coldClean := Analyze(parseIncrementalSessionFile(t, cleanSource))
	warmClean := session.Analyze(parseIncrementalSessionFile(t, cleanSource), AnalyzeOptions{})
	if len(coldClean.Errors()) != 0 || len(warmClean.Errors()) != 0 {
		t.Fatalf("clean fixture has errors: cold=%v warm=%v", coldClean.Errors(), warmClean.Errors())
	}
	assertIncrementalResultParity(t, coldClean, warmClean)
	before := session.Stats()
	warmClean = session.Analyze(parseIncrementalSessionFile(t, cleanSource), AnalyzeOptions{})
	if got := session.Stats().LocalInitializersReused - before.LocalInitializersReused; got != 2 {
		t.Fatalf("clean snapshot was not reusable before the errorful edit: reused=%d stats=%+v", got, session.Stats())
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, cleanSource)), warmClean)

	// A diagnostic-producing initializer that is outside the closed grammar
	// stays on the ordinary cold function path. Valid locals in the following
	// function remain eligible, and the new valid local is published despite
	// that unrelated diagnostic.
	workWithFresh := strings.Replace(cleanSource, "    return second\n", "    fresh: i64 = value + 9\n    return second + fresh\n", 1)
	errorfulSource := badFunction + workWithFresh
	coldErrorful := Analyze(parseIncrementalSessionFile(t, errorfulSource))
	if all := strings.Join(coldErrorful.Errors(), "\n"); !strings.Contains(all, "operator requires numeric operands") {
		t.Fatalf("diagnostic fixture did not emit its initializer diagnostic:\n%s", all)
	}
	before = session.Stats()
	firstErrorful := session.Analyze(parseIncrementalSessionFile(t, errorfulSource), AnalyzeOptions{})
	after := session.Stats()
	if got := after.LocalInitializersReused - before.LocalInitializersReused; got != 2 {
		t.Fatalf("valid prior initializers were blocked by an unrelated diagnostic: reused=%d stats=%+v", got, after)
	}
	if got := after.LocalInitializersChecked - before.LocalInitializersChecked; got != 1 {
		t.Fatalf("new valid initializer was not analyzed and published after the earlier diagnostic: checked=%d stats=%+v", got, after)
	}
	if after.PrimaryBodiesReused != before.PrimaryBodiesReused {
		t.Fatalf("errorful run reused a whole body instead of safe initializer facts: before=%+v after=%+v", before, after)
	}
	if got := strings.Join(firstErrorful.Errors(), "\n"); got != strings.Join(coldErrorful.Errors(), "\n") {
		t.Fatalf("first errorful analysis diagnostics differ from cold analysis:\ncold: %s\nfirst: %s", strings.Join(coldErrorful.Errors(), "\n"), got)
	}
	assertIncrementalResultParity(t, coldErrorful, firstErrorful)
	// A newly published valid local is reused on the next errorful run. The
	// diagnostic-producing function remains on its ordinary cold path; these
	// counters include only initializer queries admitted by the closed grammar.
	before = session.Stats()
	warmErrorful := session.Analyze(parseIncrementalSessionFile(t, errorfulSource), AnalyzeOptions{})
	after = session.Stats()
	if got := after.LocalInitializersReused - before.LocalInitializersReused; got != 3 {
		t.Fatalf("errorful warm run did not reuse exactly the valid locals: reused=%d stats=%+v", got, after)
	}
	if got := after.LocalInitializersChecked - before.LocalInitializersChecked; got != 0 {
		t.Fatalf("valid errorful warm run unexpectedly rechecked admitted locals: checked=%d stats=%+v", got, after)
	}
	if got := strings.Join(warmErrorful.Errors(), "\n"); got != strings.Join(coldErrorful.Errors(), "\n") {
		t.Fatalf("warm errorful analysis diagnostics differ from cold analysis:\ncold: %s\nwarm: %s", strings.Join(coldErrorful.Errors(), "\n"), got)
	}
	if !strings.Contains(strings.Join(warmErrorful.Errors(), "\n"), "operator requires numeric operands") {
		t.Fatalf("warm fallback lost the initializer diagnostic: %v", warmErrorful.Errors())
	}
	assertIncrementalResultParity(t, coldErrorful, warmErrorful)

	// Changing a consumed input invalidates its dependent local, even while the
	// unrelated earlier error remains; the independent fresh local still hits.
	changedSource := strings.Replace(errorfulSource, "first: i64 = value + 1", "first: i64 = value + 2", 1)
	coldChanged := Analyze(parseIncrementalSessionFile(t, changedSource))
	before = session.Stats()
	changed := session.Analyze(parseIncrementalSessionFile(t, changedSource), AnalyzeOptions{})
	after = session.Stats()
	if got := after.LocalInitializersReused - before.LocalInitializersReused; got != 1 {
		t.Fatalf("consumed-binding edit reused invalid locals: reused=%d stats=%+v", got, after)
	}
	if got := after.LocalInitializersChecked - before.LocalInitializersChecked; got != 2 {
		t.Fatalf("consumed-binding edit did not recheck the changed and dependent locals: checked=%d stats=%+v", got, after)
	}
	if got := strings.Join(changed.Errors(), "\n"); got != strings.Join(coldChanged.Errors(), "\n") {
		t.Fatalf("changed errorful analysis diagnostics differ from cold analysis:\ncold: %s\ncurrent: %s", strings.Join(coldChanged.Errors(), "\n"), got)
	}
	assertIncrementalResultParity(t, coldChanged, changed)

	// Removing the earlier diagnostic drops its source entry and leaves the
	// refreshed valid products reusable with exact cold-result parity.
	cleanChangedSource := strings.TrimPrefix(changedSource, badFunction)
	coldAfterRemoval := Analyze(parseIncrementalSessionFile(t, cleanChangedSource))
	before = session.Stats()
	afterRemoval := session.Analyze(parseIncrementalSessionFile(t, cleanChangedSource), AnalyzeOptions{})
	after = session.Stats()
	if len(coldAfterRemoval.Errors()) != 0 || len(afterRemoval.Errors()) != 0 {
		t.Fatalf("removing the earlier diagnostic did not restore clean output: cold=%v warm=%v", coldAfterRemoval.Errors(), afterRemoval.Errors())
	}
	if got := after.LocalInitializersReused - before.LocalInitializersReused; got != 3 {
		t.Fatalf("clean snapshot after error removal did not reuse valid locals: reused=%d stats=%+v", got, after)
	}
	if got := after.LocalInitializersChecked - before.LocalInitializersChecked; got != 0 {
		t.Fatalf("clean snapshot after error removal unexpectedly rechecked valid locals: checked=%d stats=%+v", got, after)
	}
	assertIncrementalResultParity(t, coldAfterRemoval, afterRemoval)
}

func TestIncrementalLocalLiteralConversionsAndReadmission(t *testing.T) {
	session := NewIncrementalAnalysisSession()
	source := "def work() -> i64:\n    first: i64 = 20\n    second: i64 = (first + 1) * 2\n    return second\n"
	for _, step := range []string{source, source, strings.Replace(source, "first: i64 = 20", "first: i32 = 20", 1), source, source} {
		current := session.Analyze(parseIncrementalSessionFile(t, step), AnalyzeOptions{})
		assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, step)), current)
	}
	if session.Stats().LocalInitializersReused != 4 {
		t.Fatalf("missing warm initializer hits: %+v", session.Stats())
	}
}

func TestIncrementalLocalUnsupportedBindingsStayCold(t *testing.T) {
	for _, body := range []string{
		"    first: i64 = later + 1\n    later: i64 = 2\n    return first\n",
		"    first: i64 = 1\n    first: i64 = 2\n    return first\n",
		"    first: mutable i64 = 1\n    return first\n",
		"    first = 1\n    return first\n",
		"    first: i64 = missing\n    return first\n",
	} {
		source := "def work() -> i64:\n" + body
		session := NewIncrementalAnalysisSession()
		session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
		result := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
		if session.Stats().LocalInitializersReused != 0 {
			t.Fatalf("unsupported locals reused: %s", body)
		}
		assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, source)), result)
	}
}

func TestIncrementalLocalRemovalRetainsUnchangedPrefix(t *testing.T) {
	source := "def work() -> i64:\n    first: i64 = 20\n    second: i64 = first + 1\n    return first\n"
	session := NewIncrementalAnalysisSession()
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	removed := strings.Replace(source, "    second: i64 = first + 1\n", "", 1)
	before := session.Stats()
	result := session.Analyze(parseIncrementalSessionFile(t, removed), AnalyzeOptions{})
	if session.Stats().LocalInitializersReused-before.LocalInitializersReused != 1 {
		t.Fatal("removing later declaration invalidated unchanged prefix")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, removed)), result)
	before = session.Stats()
	result = session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	if session.Stats().LocalInitializersReused-before.LocalInitializersReused != 1 || session.Stats().LocalInitializersChecked-before.LocalInitializersChecked != 1 {
		t.Fatal("removed local query retained a usable product")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, source)), result)
}

func TestIncrementalLocalBindingsRequireCurrentOwnerAndDeclaration(t *testing.T) {
	fn := parseProductFunction(t, "def work() -> i64:\n    first: i64 = 20\n    second: i64 = first + 1\n    return second\n")
	inputs, ok := primitiveLocalInitializers(fn)
	if !ok {
		t.Fatal("no local plan")
	}
	canonical := &BuiltinType{Name: "i64"}
	analyzer := &Analyzer{currentScope: NewScope(nil), currentReturn: canonical, currentFuncDecl: fn, namedTypes: map[string]Type{"i64": canonical}}
	if !inputs[0].currentBindings(analyzer) {
		t.Fatal("current zero-parameter owner rejected")
	}
	analyzer.currentFuncDecl = nil
	if inputs[0].currentBindings(analyzer) {
		t.Fatal("missing owner accepted for empty binding set")
	}
	analyzer.currentFuncDecl = fn
	local := &Symbol{Name: "first", Kind: SymbolLocal, Type: canonical, Node: inputs[0].declaration}
	analyzer.currentScope.Define(local)
	if !inputs[1].currentBindings(analyzer) {
		t.Fatal("current local rejected")
	}
	local.Node = fn
	if inputs[1].currentBindings(analyzer) {
		t.Fatal("wrong local declaration accepted")
	}
}

func BenchmarkIncrementalLocalInitializers(b *testing.B) {
	var tree func(int) string
	tree = func(depth int) string {
		if depth == 0 {
			return "1"
		}
		return "(" + tree(depth-1) + " + " + tree(depth-1) + ")"
	}
	var source strings.Builder
	for fn := 0; fn < 8; fn++ {
		fmt.Fprintf(&source, "def work%d(value: i64) -> i64:\n", fn)
		previous := "value"
		for local := 0; local < 8; local++ {
			name := fmt.Sprintf("local%d", local)
			fmt.Fprintf(&source, "    %s: i64 = %s + %s\n", name, previous, tree(5))
			previous = name
		}
		fmt.Fprintf(&source, "    return %s\n\n", previous)
	}
	text := source.String()
	parse := func() *ast.File {
		l := lexer.New("local_benchmark.elisa", []byte(text))
		p := parser.New(l.Tokenize())
		file := p.ParseFile("local_benchmark.elisa")
		if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
			b.Fatal("invalid benchmark source")
		}
		return file
	}
	b.Run("cold", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if result := Analyze(parse()); len(result.Errors()) != 0 {
				b.Fatal(result.Errors())
			}
		}
	})
	b.Run("warm", func(b *testing.B) {
		session := NewIncrementalAnalysisSession()
		session.Analyze(parse(), AnalyzeOptions{})
		before := session.Stats()
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if result := session.Analyze(parse(), AnalyzeOptions{}); len(result.Errors()) != 0 {
				b.Fatal(result.Errors())
			}
		}
		b.StopTimer()
		after := session.Stats()
		hits := after.LocalInitializersReused - before.LocalInitializersReused
		b.ReportMetric(float64(hits)/float64(b.N), "reused-initializers/op")
		if hits != uint64(b.N)*64 {
			b.Fatal("missing local reuse")
		}
	})
}

func TestIncrementalLocalSyntaxValidationRejectsMutation(t *testing.T) {
	for _, mutate := range []func(*primitiveLocalInitializer){
		func(input *primitiveLocalInitializer) {
			input.declaration.Value.(*ast.BinaryExpr).Op = lexer.TOKEN_STAR
		},
		func(input *primitiveLocalInitializer) {
			input.declaration.Value.(*ast.BinaryExpr).Left.(*ast.Ident).Name = "other"
		},
		func(input *primitiveLocalInitializer) { input.declaration.Value = &ast.IntLit{Value: "1"} },
		func(input *primitiveLocalInitializer) {
			input.declaration.Value.(*ast.BinaryExpr).Right = &ast.IntLit{Value: "2"}
		},
		func(input *primitiveLocalInitializer) {
			input.declaration.Value.(*ast.BinaryExpr).LoweredCall = &ast.CallExpr{}
		},
		func(input *primitiveLocalInitializer) {
			input.declaration.Value.(*ast.BinaryExpr).Right.(*ast.IntLit).Suffix = "i64"
		},
		func(input *primitiveLocalInitializer) {
			input.declaration.Value.(*ast.BinaryExpr).Right.(*ast.IntLit).Value = "2"
		},
		func(input *primitiveLocalInitializer) {
			input.declaration.Value.(*ast.BinaryExpr).Left = input.declaration.Value
		},
	} {
		fn := parseProductFunction(t, "def work(value: i64) -> i64:\n    local: i64 = value + 1\n    return local\n")
		inputs, ok := primitiveLocalInitializers(fn)
		if !ok || !inputs[0].currentSyntax() {
			t.Fatal("pristine syntax rejected")
		}
		mutate(inputs[0])
		if inputs[0].currentSyntax() {
			t.Fatal("changed syntax accepted")
		}
	}
}

func TestIncrementalLocalConsumedValuesIgnoreUnrelatedEdit(t *testing.T) {
	source := "def work(value: i64) -> i64:\n    unrelated: i64 = 1\n    first: i64 = value + 1\n    second: i64 = first * 2\n    return second + unrelated\n"
	session := NewIncrementalAnalysisSession()
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	edited := strings.Replace(source, "unrelated: i64 = 1", "unrelated: i64 = 2", 1)
	before := session.Stats()
	current := session.Analyze(parseIncrementalSessionFile(t, edited), AnalyzeOptions{})
	after := session.Stats()
	if after.LocalInitializersReused-before.LocalInitializersReused != 2 || after.LocalInitializersChecked-before.LocalInitializersChecked != 1 {
		t.Fatalf("unread value invalidated queries: before%+v after%+v", before, after)
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, edited)), current)
	edited = strings.Replace(edited, "value + 1", "value + 3", 1)
	before = session.Stats()
	current = session.Analyze(parseIncrementalSessionFile(t, edited), AnalyzeOptions{})
	after = session.Stats()
	if after.LocalInitializersReused-before.LocalInitializersReused != 1 || after.LocalInitializersChecked-before.LocalInitializersChecked != 2 {
		t.Fatal("transitive consumed value failed to invalidate")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, edited)), current)
}

func TestIncrementalLocalUnrelatedInsertionPreservesOwners(t *testing.T) {
	original := "def work(value: i64) -> i64:\n    first: i64 = value + 1\n    second: i64 = first * 2\n    return second\n"
	inserted := strings.Replace(original, "    first:", "    unrelated: i64 = 7\n    first:", 1)
	session := NewIncrementalAnalysisSession()
	session.Analyze(parseIncrementalSessionFile(t, original), AnalyzeOptions{})
	before := session.Stats()
	current := session.Analyze(parseIncrementalSessionFile(t, inserted), AnalyzeOptions{})
	after := session.Stats()
	if after.LocalInitializersReused-before.LocalInitializersReused != 2 || after.LocalInitializersChecked-before.LocalInitializersChecked != 1 {
		t.Fatal("unrelated insertion shifted existing query owners")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, inserted)), current)
	before = session.Stats()
	current = session.Analyze(parseIncrementalSessionFile(t, original), AnalyzeOptions{})
	if session.Stats().LocalInitializersReused-before.LocalInitializersReused != 2 {
		t.Fatal("unrelated removal invalidated existing owners")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, original)), current)
}
