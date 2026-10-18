//go:build cgo

package semantic

import (
	"context"
	"crypto/sha256"
	"elisacore/src/incremental"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func bindingTestKey(t *testing.T, session *IncrementalAnalysisSession, name string) string {
	t.Helper()
	digest := sha256.Sum256([]byte(name))
	suffix := "/local-name-v2/" + hex.EncodeToString(digest[:])
	for key := range session.previous {
		if strings.HasSuffix(key.Name, suffix) {
			return key.Name
		}
	}
	t.Fatalf("local %s not admitted", name)
	return ""
}
func bindingTestSession() (*IncrementalAnalysisSession, map[incremental.Key]int) {
	session := NewIncrementalAnalysisSession()
	counts := map[incremental.Key]int{}
	evaluators := primitiveQueryEvaluators()
	for kind, evaluator := range evaluators {
		evaluators[kind] = func(reader *incremental.Reader, name string) (string, error) {
			counts[incremental.Key{Kind: kind, Name: name}]++
			return evaluator(reader, name)
		}
	}
	evaluators["test-binding-input"] = func(reader *incremental.Reader, name string) (string, error) {
		value, ok := reader.Input(primitiveInput(primitiveBindingRecipeKind, name))
		if !ok {
			return "", errPrimitiveProductMiss
		}
		return value, nil
	}
	session.db = incremental.New(evaluators)
	return session, counts
}
func bindingTestRecipe(t *testing.T, session *IncrementalAnalysisSession, name string) primitiveBindingRecipe {
	t.Helper()
	key := incremental.Key{Kind: "test-binding-input", Name: name}
	encoded, err := session.db.Evaluate(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	session.db.EvictQuery(key)
	var recipe primitiveBindingRecipe
	if err := json.Unmarshal([]byte(encoded), &recipe); err != nil {
		t.Fatal(err)
	}
	return recipe
}
func bindingTestSetRecipe(t *testing.T, session *IncrementalAnalysisSession, name string, recipe primitiveBindingRecipe) {
	t.Helper()
	encoded, err := json.Marshal(recipe)
	if err != nil {
		t.Fatal(err)
	}
	session.db.Apply(incremental.Change{Key: primitiveInput(primitiveBindingRecipeKind, name), Value: string(encoded)})
}

func TestIncrementalBindingQueriesTrackUpstreamEdges(t *testing.T) {
	source := "def work(value: i64) -> i64:\n    first: i64 = value + 1\n    second: i64 = first * 2\n    unrelated: i64 = 7\n    return second + unrelated\n"
	session, counts := bindingTestSession()
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	first, second, unrelated := bindingTestKey(t, session, "first"), bindingTestKey(t, session, "second"), bindingTestKey(t, session, "unrelated")
	query := incremental.Key{Kind: primitiveLocalTypingKind, Name: second}
	original, err := session.db.Evaluate(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	recipe := bindingTestRecipe(t, session, second)
	if len(recipe.Dependencies) != 1 || recipe.Dependencies[0].Query != first {
		t.Fatal("consumer has no actual producer query edge")
	}
	other := bindingTestRecipe(t, session, unrelated)
	other.Own = strings.Repeat("a", 64)
	bindingTestSetRecipe(t, session, unrelated, other)
	before := counts[query]
	if value, err := session.db.Evaluate(context.Background(), query); err != nil || value != original || counts[query] != before {
		t.Fatal("unrelated input reexecuted consumer typing")
	}
	producer := bindingTestRecipe(t, session, first)
	changed := producer
	changed.Own = strings.Repeat("b", 64)
	bindingTestSetRecipe(t, session, first, changed)
	// Only the producer's input changes; the consumer recipe and typing product
	// remain untouched. A precomputed consumer source cannot pass this test.
	if _, err := session.db.Evaluate(context.Background(), query); err == nil {
		t.Fatal("upstream change reused stale consumer typing product")
	}
	bindingTestSetRecipe(t, session, first, producer)
	if value, err := session.db.Evaluate(context.Background(), query); err != nil || value != original {
		t.Fatal("equal recovery failed")
	}
	current := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, source)), current)
}

func TestIncrementalBindingQueriesReplaceConsumedEdges(t *testing.T) {
	source := "def work() -> i64:\n    first: i64 = 1\n    second: i64 = 1\n    selected: i64 = first + 1\n    downstream: i64 = selected * 2\n    return downstream\n"
	session := NewIncrementalAnalysisSession()
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	switched := strings.Replace(source, "selected: i64 = first", "selected: i64 = second", 1)
	result := session.Analyze(parseIncrementalSessionFile(t, switched), AnalyzeOptions{})
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, switched)), result)
	edited := strings.Replace(switched, "first: i64 = 1", "first: i64 = 3", 1)
	before := session.Stats()
	result = session.Analyze(parseIncrementalSessionFile(t, edited), AnalyzeOptions{})
	if session.Stats().LocalInitializersReused-before.LocalInitializersReused != 3 || session.Stats().LocalInitializersChecked-before.LocalInitializersChecked != 1 {
		t.Fatal("obsolete producer edge remained installed")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, edited)), result)
	edited = strings.Replace(edited, "second: i64 = 1", "second: i64 = 4", 1)
	before = session.Stats()
	result = session.Analyze(parseIncrementalSessionFile(t, edited), AnalyzeOptions{})
	if session.Stats().LocalInitializersReused-before.LocalInitializersReused != 1 || session.Stats().LocalInitializersChecked-before.LocalInitializersChecked != 3 {
		t.Fatal("new transitive producer edge was not installed")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, edited)), result)
}

func TestIncrementalBindingQueriesFallbackAndEqualAdmission(t *testing.T) {
	session, counts := bindingTestSession()
	session.maxTrackedBodies = 2
	original := "def work(value: i64) -> i64:\n    downstream: i64 = value * 2\n    return downstream\n"
	session.Analyze(parseIncrementalSessionFile(t, original), AnalyzeOptions{})
	inserted := "def work(value: i64) -> i64:\n    upstream: i64 = value + 1\n    downstream: i64 = upstream * 2\n    return downstream\n"
	session.Analyze(parseIncrementalSessionFile(t, inserted), AnalyzeOptions{})
	downstream := bindingTestKey(t, session, "downstream")
	recipe := bindingTestRecipe(t, session, downstream)
	if len(recipe.Dependencies) != 1 || recipe.Dependencies[0].Query != "" || len(recipe.Dependencies[0].Value) != 64 {
		t.Fatal("unadmitted upstream lacks a current immutable fallback")
	}
	edited := strings.Replace(inserted, "value + 1", "value + 3", 1)
	before := session.Stats()
	current := session.Analyze(parseIncrementalSessionFile(t, edited), AnalyzeOptions{})
	if session.Stats().LocalInitializersChecked-before.LocalInitializersChecked != 1 || session.Stats().LocalInitializersReused != before.LocalInitializersReused {
		t.Fatal("same-type unadmitted producer edit failed to invalidate consumer")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, edited)), current)
	// Admitting the producer replaces an inline dependency with a query edge but
	// preserves its value fingerprint. The existing typing query must not execute.
	query := incremental.Key{Kind: primitiveLocalTypingKind, Name: downstream}
	// Cold fallback publishes its replacement product lazily. Materialize that
	// current product before measuring red-green validation; otherwise the memo
	// still describes the source from before the edit and must execute once.
	if _, err := session.db.Evaluate(context.Background(), query); err != nil {
		t.Fatal("current consumer product unavailable:", err)
	}
	session.maxTrackedBodies = 3
	executed := counts[query]
	before = session.Stats()
	current = session.Analyze(parseIncrementalSessionFile(t, edited), AnalyzeOptions{})
	if counts[query] != executed || session.Stats().LocalInitializersReused-before.LocalInitializersReused != 1 {
		t.Fatal("equal source admission failed to prune typing reexecution")
	}
	upstream := bindingTestKey(t, session, "upstream")
	recipe = bindingTestRecipe(t, session, downstream)
	if recipe.Dependencies[0].Query != upstream {
		t.Fatal("admitted producer query edge missing")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, edited)), current)
}

func TestIncrementalBindingRecipeBudgetsKeepUsefulHits(t *testing.T) {
	source := "def work(value: i64) -> i64:\n    local: i64 = value + 1\n    return local\n\ndef hot() -> i64:\n    return 42\n"
	session := NewIncrementalAnalysisSession()
	session.maxBindingSourceBytes = 1
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	before := session.Stats()
	current := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	if stats := session.Stats(); stats.RetainedBindingSourceBytes != 0 || stats.RetainedBindingSourceEdges != 0 || stats.BindingSpecAdmissionMisses != 2 || stats.PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
		t.Fatalf("recipe pressure disabled useful hits or exceeded budget: %+v", stats)
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, source)), current)
	session.maxBindingSourceBytes = 0
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	before = session.Stats()
	current = session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	if session.Stats().LocalInitializersReused-before.LocalInitializersReused != 1 || session.Stats().RetainedBindingSourceBytes == 0 {
		t.Fatal("recipe budget recovery failed to readmit local")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, source)), current)
}

func TestIncrementalBindingRecipeBudgetRetainsOtherOwner(t *testing.T) {
	source := "def first(value: i64) -> i64:\n    old: i64 = value + 1\n    return old\n\ndef second(value: i64) -> i64:\n    old: i64 = value + 2\n    return old\n"
	session := NewIncrementalAnalysisSession()
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	session.maxBindingSourceBytes = 1024 // two existing zero-edge recipes' reservation
	inserted := strings.Replace(source, "    old: i64 = value + 1", "    added: i64 = 7\n    old: i64 = value + 1", 1)
	before := session.Stats()
	current := session.Analyze(parseIncrementalSessionFile(t, inserted), AnalyzeOptions{})
	if session.Stats().LocalInitializersReused-before.LocalInitializersReused != 2 {
		t.Fatal("new pending recipe displaced retained local in another owner")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, inserted)), current)
}

func TestIncrementalBindingEdgeBudgetAndRetirement(t *testing.T) {
	session := NewIncrementalAnalysisSession()
	session.maxBindingSourceEdges = 8
	var source strings.Builder
	source.WriteString("def work() -> i64:\n    local0: i64 = 1\n")
	for i := 1; i < 32; i++ {
		var terms []string
		for j := 0; j < i; j++ {
			terms = append(terms, fmt.Sprintf("local%d", j))
		}
		fmt.Fprintf(&source, "    local%d: i64 = %s\n", i, strings.Join(terms, " + "))
	}
	source.WriteString("    return local31\n")
	session.Analyze(parseIncrementalSessionFile(t, source.String()), AnalyzeOptions{})
	before := session.Stats()
	current := session.Analyze(parseIncrementalSessionFile(t, source.String()), AnalyzeOptions{})
	if stats := session.Stats(); stats.RetainedBindingSourceEdges > 8 || stats.LocalInitializersReused <= before.LocalInitializersReused || stats.BindingSpecAdmissionMisses == 0 {
		t.Fatal("dense dependency graph ignored edge budget")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, source.String())), current)
	// Removal releases both typing and binding-source query kinds immediately.
	session.Analyze(parseIncrementalSessionFile(t, "def other() -> i64:\n    return 1\n"), AnalyzeOptions{})
	if stats := session.Stats(); stats.RetainedBindingSourceBytes != 0 || stats.RetainedBindingSourceEdges != 0 {
		t.Fatal("removed graph retained recipe accounting")
	}
	_, queries := session.db.EntryCounts()
	if queries > 1 {
		t.Fatalf("removed graph retained query memos: %d", queries)
	}
}

func TestIncrementalBindingQueriedRecipeOmitsTransitiveSnapshot(t *testing.T) {
	source := "def work(value: i64) -> i64:\n    first: i64 = value + 1\n    second: i64 = first * 2\n    return second\n"
	session, counts := bindingTestSession()
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	second := bindingTestKey(t, session, "second")
	before := bindingTestRecipe(t, session, second)
	if before.Dependencies[0].Value != "" || before.Dependencies[0].Query == "" {
		t.Fatal("queried dependency embeds a flattened fallback")
	}
	query := incremental.Key{Kind: primitiveBindingSourceKind, Name: second}
	executed := counts[query]
	edited := strings.Replace(source, "value + 1", "value + 3", 1)
	current := session.Analyze(parseIncrementalSessionFile(t, edited), AnalyzeOptions{})
	after := bindingTestRecipe(t, session, second)
	oldJSON, _ := json.Marshal(before)
	newJSON, _ := json.Marshal(after)
	if string(oldJSON) != string(newJSON) || counts[query] <= executed {
		t.Fatal("ordinary edit was not propagated through an explicit query edge")
	}
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, edited)), current)
}

func TestIncrementalBindingQueryChurnCompactsAllKinds(t *testing.T) {
	session := NewIncrementalAnalysisSession()
	for i := 0; i < 4100; i++ {
		source := fmt.Sprintf("def work(value: i64) -> i64:\n    local%d: i64 = value + 1\n    consumer: i64 = local%d * 2\n    return consumer\n", i, i)
		result := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
		if len(result.Errors()) != 0 {
			t.Fatal(result.Errors())
		}
		if i == 20 {
			_, queries := session.db.EntryCounts()
			if queries > 8 {
				t.Fatalf("retired binding-source memos retained: %d", queries)
			}
		}
	}
	inputs, queries := session.db.EntryCounts()
	if session.Stats().DatabaseResets == 0 || inputs > 128 || queries > 8 {
		t.Fatalf("binding graph history not compacted: inputs%d queries%d stats%+v", inputs, queries, session.Stats())
	}
}

func TestIncrementalBindingMissingAndCyclicInputsFailCold(t *testing.T) {
	source := "def work(value: i64) -> i64:\n    first: i64 = value + 1\n    second: i64 = first * 2\n    return second\n"
	session, _ := bindingTestSession()
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	first, second := bindingTestKey(t, session, "first"), bindingTestKey(t, session, "second")
	original := bindingTestRecipe(t, session, first)
	query := incremental.Key{Kind: primitiveLocalTypingKind, Name: second}
	expected, err := session.db.Evaluate(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	session.db.Apply(incremental.Change{Key: primitiveInput(primitiveBindingRecipeKind, first), Remove: true})
	if _, err := session.db.Evaluate(context.Background(), query); err == nil {
		t.Fatal("missing producer input accepted as an empty value")
	}
	bindingTestSetRecipe(t, session, first, original)
	if value, err := session.db.Evaluate(context.Background(), query); err != nil || value != expected {
		t.Fatal("missing-input recovery failed")
	}
	cyclic := original
	cyclic.Dependencies = []primitiveBindingDependency{{Identity: strings.Repeat("a", 64), Query: first}}
	bindingTestSetRecipe(t, session, first, cyclic)
	if _, err := session.db.Evaluate(context.Background(), query); err == nil {
		t.Fatal("cyclic source query reused a successful typing product")
	}
	bindingTestSetRecipe(t, session, first, original)
	if value, err := session.db.Evaluate(context.Background(), query); err != nil || value != expected {
		t.Fatal("cycle recovery failed")
	}
}
