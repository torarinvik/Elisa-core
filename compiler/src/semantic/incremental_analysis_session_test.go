//go:build cgo

package semantic

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/incremental"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func parseIncrementalSessionFile(t *testing.T, source string) *ast.File {
	t.Helper()
	l := lexer.New("incremental_session.elisa", []byte(source))
	tokens := l.Tokenize()
	if len(l.Errors()) != 0 {
		t.Fatal(l.Errors())
	}
	p := parser.New(tokens)
	file := p.ParseFile("incremental_session.elisa")
	if len(p.Errors()) != 0 {
		t.Fatal(p.Errors())
	}
	return file
}
func assertIncrementalResultParity(t *testing.T, cold, warm *Result) {
	t.Helper()
	// Re-key public source-node maps onto the current invocation, then compare
	// every published field except invocation objects/costs. No output inventory
	// is silently limited to the maps restored by the cache.
	mapping := map[PositionedFunctionNode]PositionedFunctionNode{}
	collect := func(file *ast.File) map[string]*ast.FuncDecl {
		out := map[string]*ast.FuncDecl{}
		var walk func([]ast.Decl, string)
		walk = func(decls []ast.Decl, namespace string) {
			for _, decl := range decls {
				switch node := decl.(type) {
				case *ast.NamespaceDecl:
					walk(node.Decls, joinQualifiedName(namespace, node.Name))
				case *ast.FuncDecl:
					out[joinQualifiedName(namespace, node.Name)] = node
				}
			}
		}
		walk(file.Decls, "")
		return out
	}
	currentFunctions := collect(warm.File)
	for name, fn := range collect(cold.File) {
		other := currentFunctions[name]
		digest, nodes, err := functionSyntaxFingerprint(fn)
		if err != nil {
			t.Fatal(err)
		}
		currentDigest, current, err := functionSyntaxFingerprint(other)
		if err != nil || digest != currentDigest || len(nodes) != len(current) {
			t.Fatal("current syntax differs")
		}
		for i, node := range nodes {
			mapping[node] = current[i]
		}
		left, _ := cold.GlobalScope.Lookup(name)
		right, _ := warm.GlobalScope.Lookup(name)
		if !reflect.DeepEqual(left.Type, right.Type) {
			t.Fatalf("function type/summary differs: %s", name)
		}
	}
	left, right := reflect.ValueOf(*cold), reflect.ValueOf(*warm)
	baseMap, reverseBaseMap := map[string]string{}, map[string]string{}
	for i := 0; i < left.NumField(); i++ {
		name := left.Type().Field(i).Name
		switch name {
		case "File", "LoweredFile", "GlobalScope", "SMTProfile", "analyzer":
			continue
		}
		a, b := left.Field(i), right.Field(i)
		if a.Kind() == reflect.Map && !a.IsNil() {
			remapped := reflect.MakeMapWithSize(a.Type(), a.Len())
			iter := a.MapRange()
			for iter.Next() {
				key := iter.Key()
				if node, ok := key.Interface().(PositionedFunctionNode); ok {
					current, found := mapping[node]
					if !found {
						t.Fatalf("unaccounted source key in %s: %T", name, node)
					}
					value := reflect.ValueOf(current)
					if !value.Type().AssignableTo(a.Type().Key()) {
						t.Fatalf("invalid key kind in %s", name)
					}
					key = value
				}
				value := iter.Value()
				if name == "ExprFacts" {
					fact := value.Interface().(OptimizationFacts)
					current := warm.ExprFacts[key.Interface().(ast.Expr)]
					if (fact.base == "") != (current.base == "") {
						t.Fatal("optimization base presence differs")
					}
					if fact.base != "" {
						if prior, ok := baseMap[fact.base]; ok && prior != current.base {
							t.Fatal("optimization alias relation differs")
						}
						if prior, ok := reverseBaseMap[current.base]; ok && prior != fact.base {
							t.Fatal("optimization alias relation merged")
						}
						baseMap[fact.base] = current.base
						reverseBaseMap[current.base] = fact.base
						fact.base = current.base // invocation-local identity; preserve the alias relation
					}
					value = reflect.ValueOf(fact)
				}
				remapped.SetMapIndex(key, value)
			}
			a = remapped
		}
		if !reflect.DeepEqual(a.Interface(), b.Interface()) {
			t.Fatalf("published metadata differs: %s", name)
		}
	}
}

func TestIncrementalSessionActuallySkipsPrimaryTyping(t *testing.T) {
	const source = `def first() -> i64:
    return 20 + 1

def second() -> i64:
    return (10 * 2) + 1
`
	session := NewIncrementalAnalysisSession()
	cold := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	if len(cold.Errors()) != 0 {
		t.Fatal(cold.Errors())
	}
	first := session.Stats()
	if first.PrimaryBodiesChecked != 2 || first.PrimaryBodiesReused != 0 || first.PrimaryValueChecks == 0 || first.PrimaryExprChecks == 0 {
		t.Fatalf("no primary cold work: %+v", first)
	}
	shifted := "\n\n" + source
	warm := session.Analyze(parseIncrementalSessionFile(t, shifted), AnalyzeOptions{})
	after := session.Stats()
	if after.PrimaryBodiesReused-first.PrimaryBodiesReused != 2 || after.ReusedExpressionNodes-first.ReusedExpressionNodes == 0 {
		t.Fatalf("no actual reuse: %+v", after)
	}
	if after.PrimaryBodiesChecked != first.PrimaryBodiesChecked || after.PrimaryValueChecks != first.PrimaryValueChecks || after.PrimaryExprChecks != first.PrimaryExprChecks || after.PrimaryElementScans != first.PrimaryElementScans {
		t.Fatalf("warm body retyped expressions: before=%+v after=%+v", first, after)
	}
	fresh := Analyze(parseIncrementalSessionFile(t, shifted))
	assertIncrementalResultParity(t, fresh, warm)
	// One body edit invalidates only its function; the independent body reuses.
	edited := strings.Replace(shifted, "20 + 1", "20 + 2", 1)
	result := session.Analyze(parseIncrementalSessionFile(t, edited), AnalyzeOptions{})
	changed := session.Stats()
	if changed.PrimaryBodiesChecked-after.PrimaryBodiesChecked != 1 || changed.PrimaryBodiesReused-after.PrimaryBodiesReused != 1 {
		t.Fatalf("edit did not stay local: %+v / %+v", after, changed)
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, edited)), result)
	// Semantic options are a tracked input even for an unchanged source body.
	before := session.Stats()
	session.Analyze(parseIncrementalSessionFile(t, edited), AnalyzeOptions{TargetDebug: true})
	options := session.Stats()
	if options.PrimaryBodiesChecked-before.PrimaryBodiesChecked != 2 {
		t.Fatal("changed options did not invalidate")
	}
}

func TestIncrementalSessionEligibilityIsClosed(t *testing.T) {
	for _, source := range []string{
		`def work(value: i64) -> i64:
    return value + 1
`,
		`def work() -> i64:
    ensure result > 0
    return 1
`,
		`def work() -> i64:
    local = 1
    return local
`,
		`def work() -> i64:
    return missing()
`,
		`def work() -> i32:
    return 1
`,
		`def work() -> i64:
    return 1 / 0
`,
	} {
		session := NewIncrementalAnalysisSession()
		session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
		result := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
		if session.Stats().PrimaryBodiesReused != 0 {
			t.Fatalf("unsupported body reused: %s", source)
		}
		assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, source)), result)
	}
	fn := parseProductFunction(t, "def work() -> i64:\n    return 20 + 1\n")
	nodes, ok := primitiveReturnExpressions(fn)
	if !ok || len(nodes) != 3 {
		t.Fatalf("eligibility lost its actual expression nodes: %v %d", ok, len(nodes))
	}
}

func TestIncrementalSessionSafetyFailureAndRemoval(t *testing.T) {
	const source = "def work() -> i64:\n    return 20 + 1\n"
	const impl = `struct BuilderTag:
    tag: int

protocol Builder:
    type State
    def state() -> State

impl Builder for BuilderTag:
    type State = int
    def state() -> int:
        return 1

`
	var session IncrementalAnalysisSession // zero value is usable too
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	before := session.Stats()
	blocked := session.Analyze(parseIncrementalSessionFile(t, impl+source), AnalyzeOptions{})
	if len(blocked.Errors()) != 0 {
		t.Fatal(blocked.Errors())
	}
	if session.Stats().PrimaryBodiesReused != before.PrimaryBodiesReused {
		t.Fatal("new static impl did not block reuse")
	}
	restored := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, source)), restored)
	if session.Stats().PrimaryBodiesReused != before.PrimaryBodiesReused+1 {
		t.Fatal("safe environment did not restore reuse")
	}
	// A new contract/error must never be hidden by previously valid body facts.
	invalid := "def work() -> i64:\n    ensure result == 0\n    return 20 + 1\n"
	before = session.Stats()
	result := session.Analyze(parseIncrementalSessionFile(t, invalid), AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) == 0 || session.Stats().PrimaryBodiesReused != before.PrimaryBodiesReused {
		t.Fatal("cached result hid a new error")
	}
	assertIncrementalResultParity(t, AnalyzeWithOptions(parseIncrementalSessionFile(t, invalid), AnalyzeOptions{EnforceStrictProofs: true}), result)
	// Removal releases the large product; re-addition performs fresh primary work.
	session.Analyze(parseIncrementalSessionFile(t, ""), AnalyzeOptions{})
	before = session.Stats()
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	after := session.Stats()
	if after.PrimaryBodiesChecked-before.PrimaryBodiesChecked != 1 || after.PrimaryBodiesReused != before.PrimaryBodiesReused {
		t.Fatal("removed product remained reusable")
	}
	session.retiredQueries = 4096
	session.Analyze(parseIncrementalSessionFile(t, ""), AnalyzeOptions{})
	if session.Stats().DatabaseResets == 0 {
		t.Fatal("historical key tombstones were not bounded")
	}
}

func BenchmarkIncrementalSessionClosedReturns(b *testing.B) {
	var buildExpr func(int) string
	buildExpr = func(depth int) string {
		if depth == 0 {
			return "1"
		}
		return "(" + buildExpr(depth-1) + " + " + buildExpr(depth-1) + ")"
	}
	for _, depth := range []int{0, 5, 7, 9} {
		b.Run(fmt.Sprintf("leaves_%d", 1<<depth), func(b *testing.B) {
			var program strings.Builder
			for i := 0; i < 16; i++ {
				fmt.Fprintf(&program, "def work%d() -> i64:\n    return %s\n\n", i, buildExpr(depth))
			}
			source := program.String()
			parse := func() *ast.File {
				l := lexer.New("benchmark.elisa", []byte(source))
				p := parser.New(l.Tokenize())
				file := p.ParseFile("benchmark.elisa")
				if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
					b.Fatal("invalid benchmark source")
				}
				return file
			}
			b.Run("cold", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					result := Analyze(parse())
					if len(result.Errors()) != 0 {
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
				for i := 0; i < b.N; i++ {
					result := session.Analyze(parse(), AnalyzeOptions{})
					if len(result.Errors()) != 0 {
						b.Fatal(result.Errors())
					}
				}
				b.StopTimer()
				after := session.Stats()
				b.ReportMetric(float64(after.PrimaryBodiesReused-before.PrimaryBodiesReused)/float64(b.N), "reused-bodies/op")
				if after.PrimaryValueChecks != before.PrimaryValueChecks || after.PrimaryExprChecks != before.PrimaryExprChecks {
					b.Fatal("warm benchmark did primary typing")
				}
			})
		})
	}
}

func TestIncrementalSessionCorruptProductAndBounds(t *testing.T) {
	const source = "def work() -> i64:\n    return 20 + 1\n"
	session := NewIncrementalAnalysisSession()
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	for key := range session.previous {
		session.db.Apply(incremental.Change{Key: primitiveInput("function-product", key.Name), Value: "broken cached record"})
	}
	before := session.Stats()
	result := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	after := session.Stats()
	if after.PrimaryBodiesChecked-before.PrimaryBodiesChecked != 1 || after.PrimaryBodiesReused != before.PrimaryBodiesReused {
		t.Fatal("corrupt record did not take cold path")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, source)), result)
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	if session.Stats().PrimaryBodiesReused != after.PrimaryBodiesReused+1 {
		t.Fatal("corrupt record was not replaced")
	}
	fn := parseProductFunction(t, "def work() -> i64:\n    return 1\n")
	literal := fn.Body[0].(*ast.ReturnStmt).Value.(*ast.IntLit)
	literal.Value = strings.Repeat("1", primitiveSourceByteBudget+1)
	if _, ok := primitiveReturnExpressions(fn); ok {
		t.Fatal("oversized literal entered optional hash path")
	}
	literal.Value = "1"
	fn.Name = strings.Repeat("x", primitiveSourceByteBudget+1)
	if _, ok := primitiveReturnHeader(fn); ok {
		t.Fatal("oversized identity entered optional key path")
	}
	for typ, expected := range primitiveASTSchemas {
		fields := make([]reflect.StructField, typ.NumField())
		for i := range fields {
			fields[i] = typ.Field(i)
		}
		if !primitiveSchemaMatches(reflect.StructOf(fields), expected) {
			t.Fatal("baseline schema rejected")
		}
		fields = append(fields, reflect.StructField{Name: "FutureZeroFeature", Type: reflect.TypeOf(false)})
		if primitiveSchemaMatches(reflect.StructOf(fields), expected) {
			t.Fatal("zero-valued schema extension silently accepted")
		}
	}
}

func TestIncrementalSessionLargeRemovalCompactsTombstones(t *testing.T) {
	session := NewIncrementalAnalysisSession()
	var changes []incremental.Change
	for i := 0; i < 4097; i++ {
		name := fmt.Sprintf("removed-%d", i)
		source := primitiveInput("function-source", name)
		session.previous[source] = true
		changes = append(changes, incremental.Change{Key: source, Value: "source"}, incremental.Change{Key: primitiveInput("function-product", name), Value: strings.Repeat("payload", 128)})
	}
	session.db.Apply(changes...)
	session.Analyze(parseIncrementalSessionFile(t, ""), AnalyzeOptions{})
	inputs, queries := session.db.EntryCounts()
	if inputs > 2 || queries != 0 || len(session.previous) != 0 {
		t.Fatalf("removed owners were reintroduced: inputs=%d queries=%d", inputs, queries)
	}
}

func TestIncrementalSessionQualifiedModuleIdentity(t *testing.T) {
	const source = `module A:
    def work() -> i64:
        return 20 + 1

module B:
    def work() -> i64:
        return 30 + 2
`
	session := NewIncrementalAnalysisSession()
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	before := session.Stats()
	warm := session.Analyze(parseIncrementalSessionFile(t, "\n"+source), AnalyzeOptions{})
	if session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 2 {
		t.Fatal("module functions did not reuse independently")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, "\n"+source)), warm)
	edited := strings.Replace(source, "20 + 1", "20 + 2", 1)
	before = session.Stats()
	result := session.Analyze(parseIncrementalSessionFile(t, edited), AnalyzeOptions{})
	if session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 1 || session.Stats().PrimaryBodiesChecked-before.PrimaryBodiesChecked != 1 {
		t.Fatal("same leaf name conflated module bodies")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, edited)), result)
}

func TestIncrementalSessionIneligibleOwnerLosesProduct(t *testing.T) {
	const source = "def work() -> i64:\n    return 20 + 1\n"
	session := NewIncrementalAnalysisSession()
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	unsupported := "def work() -> i64:\n    return missing()\n"
	result := session.Analyze(parseIncrementalSessionFile(t, unsupported), AnalyzeOptions{})
	if len(result.Errors()) == 0 {
		t.Fatal("unsupported error did not reproduce")
	}
	before := session.Stats()
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	after := session.Stats()
	if after.PrimaryBodiesChecked-before.PrimaryBodiesChecked != 1 || after.PrimaryBodiesReused != before.PrimaryBodiesReused {
		t.Fatal("ineligible owner kept its old product")
	}
}

func TestIncrementalSessionStrictProfilesAndColdFallback(t *testing.T) {
	source := "def work() -> i64:\n    return (20 + 1) * 2\n"
	options := AnalyzeOptions{EnforceUnsafePermissions: true, EnforceGlobalPermissions: true, EnforceProgressSafety: true, EnforceStrictConcurrency: true, EnforcePerfLints: true, FlowLintMode: FlowLintStrict, EnforceStrictProofs: true, RequireExternContracts: true, RecordDeathTimeCohorts: true}
	session := NewIncrementalAnalysisSession()
	session.Analyze(parseIncrementalSessionFile(t, source), options)
	before := session.Stats()
	current := session.Analyze(parseIncrementalSessionFile(t, "\n"+source), options)
	if session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
		t.Fatal("strict profile did not reuse")
	}
	assertIncrementalResultParity(t, AnalyzeWithOptions(parseIncrementalSessionFile(t, "\n"+source), options), current)
	source = "def work(value: i64) -> i64:\n    return value + 1\n"
	before = session.Stats()
	current = session.Analyze(parseIncrementalSessionFile(t, source), options)
	if session.Stats().ColdFallbackAnalyses-before.ColdFallbackAnalyses != 1 {
		t.Fatal("all-ineligible program did not use the cold API")
	}
	assertIncrementalResultParity(t, AnalyzeWithOptions(parseIncrementalSessionFile(t, source), options), current)
}
