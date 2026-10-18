//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/incremental"
	"strings"
	"testing"
)

func TestLexicalReadPointSurvivesLaterScopeMutation(t *testing.T) {
	fn := parseProductFunction(t, "def work(value: i64) -> i64:\n    x = value\n    return x\n")
	index, err := IndexFunctionSource(fn, productContext())
	if err != nil {
		t.Fatal(err)
	}
	global := NewScope(nil)
	scope := NewScope(global)
	outer := &Symbol{Name: "x"}
	global.Symbols["x"] = outer
	first := fn.Body[0].(*ast.VarDeclStmt).Value.(*ast.Ident)
	last := fn.Body[1].(*ast.ReturnStmt).Value.(*ast.Ident)
	// The first source site intentionally queries x: this exercises independent
	// lookup roles, without claiming to type-check this synthetic observation.
	request := lexicalSymbolRequest{Index: index, Site: first, Role: "read", Name: "x", Current: scope, Global: global}
	before, err := observeLexicalSymbol(request, outer, true)
	if err != nil {
		t.Fatal(err)
	}
	local := &Symbol{Name: "x", Kind: SymbolLocal, Node: fn.Body[0]}
	scope.Define(local)
	request.Site = last
	after, err := observeLexicalSymbol(request, local, true)
	if err != nil {
		t.Fatal(err)
	}
	delete(scope.Symbols, "x")
	global.Symbols["x"] = nil
	// Later changes to Symbol metadata cannot rewrite a previous binding slot.
	local.Name = "later mutated name"
	reads := &lexicalReadInvocation{observations: []lexicalSymbolObservation{before, after}}
	session := NewIncrementalAnalysisSessionWithOptions(IncrementalAnalysisSessionOptions{AuditLexicalReads: true})
	reads.publish(session)
	stats := session.Stats()
	if stats.LexicalSelectionsVerified != 2 || stats.LexicalVerificationFailures != 0 || stats.LexicalReadsDropped != 0 || stats.LexicalQueriesExecuted != 2 {
		t.Fatalf("read points were not frozen: %+v", stats)
	}
}

func TestIncrementalSessionPublishesActualIdentifierReads(t *testing.T) {
	const source = `def work(value: i64) -> i64:
    if value > 0:
        return value
    return value + 1
`
	session := NewIncrementalAnalysisSessionWithOptions(IncrementalAnalysisSessionOptions{AuditLexicalReads: true})
	for i, text := range []string{source, "\n\n" + source} {
		before := session.Stats()
		got := session.Analyze(parseIncrementalSessionFile(t, text), AnalyzeOptions{})
		if len(got.Errors()) != 0 {
			t.Fatal(got.Errors())
		}
		assertIncrementalResultParity(t, AnalyzeWithOptions(parseIncrementalSessionFile(t, text), AnalyzeOptions{}), got)
		after := session.Stats()
		if after.LexicalSelectionsVerified <= before.LexicalSelectionsVerified || after.LexicalVerificationFailures != 0 || after.LexicalPublicationFailures != 0 {
			t.Fatalf("no verified live reads: %+v", after)
		}
		if i == 0 && after.LexicalQueriesExecuted == 0 {
			t.Fatal("cold selection query did not execute")
		}
		if i == 1 && (after.LexicalQueriesExecuted != before.LexicalQueriesExecuted || after.LexicalQueryMemoHits <= before.LexicalQueryMemoHits) {
			t.Fatalf("unchanged relocated reads did not reuse query memo: before %+v, after %+v", before, after)
		}
	}
	// Removing all functions retires the old selection universe.
	session.Analyze(parseIncrementalSessionFile(t, ""), AnalyzeOptions{})
	if session.lexical.Stats().RetainedQueries != 0 {
		t.Fatal("removed function reads retained")
	}
}

func TestLexicalReadPointConflictingVisitsDecline(t *testing.T) {
	fn := parseProductFunction(t, "def work(value: i64) -> i64:\n    x = value\n    return x\n")
	index, err := IndexFunctionSource(fn, productContext())
	if err != nil {
		t.Fatal(err)
	}
	global := NewScope(nil)
	scope := NewScope(global)
	global.Symbols["x"] = &Symbol{Name: "x"}
	request := lexicalSymbolRequest{Index: index, Site: fn.Body[1], Role: "read", Name: "x", Current: scope, Global: global}
	first, err := observeLexicalSymbol(request, global.Symbols["x"], true)
	if err != nil {
		t.Fatal(err)
	}
	scope.Symbols["x"] = &Symbol{Name: "x", Kind: SymbolLocal, Node: fn.Body[0]}
	second, err := observeLexicalSymbol(request, scope.Symbols["x"], true)
	if err != nil {
		t.Fatal(err)
	}
	session := NewIncrementalAnalysisSessionWithOptions(IncrementalAnalysisSessionOptions{AuditLexicalReads: true})
	reads := &lexicalReadInvocation{observations: []lexicalSymbolObservation{first, second, first}}
	reads.publish(session)
	if stats := session.Stats(); stats.LexicalSelectionsVerified != 0 || stats.LexicalReadsDropped == 0 || stats.LexicalVerificationFailures != 0 {
		t.Fatalf("conflicting same-site environments admitted: %+v", stats)
	}
}

func TestLexicalReadFreePrimitiveAvoidsSourceIndex(t *testing.T) {
	const source = "def work() -> i64:\n    return (10 + 2) * 3\n"
	session := NewIncrementalAnalysisSessionWithOptions(IncrementalAnalysisSessionOptions{AuditLexicalReads: true})
	for i := 0; i < 2; i++ {
		result := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
		if len(result.Errors()) != 0 {
			t.Fatal(result.Errors())
		}
	}
	stats := session.Stats()
	if stats.PrimaryBodiesReused == 0 {
		t.Fatal("fixture did not reuse primary body")
	}
	if stats.LexicalSourceIndexesBuilt != 0 || stats.LexicalReadsObserved != 0 || stats.LexicalSelectionsVerified != 0 {
		t.Fatalf("read-free primitive paid for lexical source scans: %+v", stats)
	}
}

func TestLexicalParameterReadRemainsLiveDuringPrimitiveReplay(t *testing.T) {
	const source = "def work(value: i64) -> i64:\n    return value + 1\n"
	session := NewIncrementalAnalysisSessionWithOptions(IncrementalAnalysisSessionOptions{AuditLexicalReads: true})
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	before := session.Stats()
	result := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	after := session.Stats()
	if after.PrimaryBodiesReused <= before.PrimaryBodiesReused {
		t.Fatal("body not reused")
	}
	if after.LexicalReadsObserved <= before.LexicalReadsObserved {
		t.Fatal("parameter read skipped during primitive replay")
	}
	if after.LexicalQueryMemoHits <= before.LexicalQueryMemoHits || after.LexicalQueriesExecuted != before.LexicalQueriesExecuted {
		t.Fatal("parameter selection did not reuse memo")
	}
}

func TestLexicalObservationBudgetDeclinesExplicitly(t *testing.T) {
	fn := parseProductFunction(t, "def work(value: i64) -> i64:\n    return value\n")
	index, err := IndexFunctionSource(fn, productContext())
	if err != nil {
		t.Fatal(err)
	}
	site := fn.Body[0].(*ast.ReturnStmt).Value.(*ast.Ident)
	scope := NewScope(nil)
	scope.Symbols["value"] = &Symbol{Name: "value", Kind: SymbolParam, Node: fn, ParamIndex: 0}
	reads := &lexicalReadInvocation{primaryOwner: fn, indexes: map[*ast.FuncDecl]*FunctionSourceIndex{fn: index}, frames: lexicalReadMaxFrames}
	a := &Analyzer{currentFuncDecl: fn, currentScope: scope, lexicalReads: reads}
	if got, found := a.lookupIdentifierScope(site); !found || got != scope.Symbols["value"] {
		t.Fatal("capture budget altered resolution")
	}
	if reads.dropped != 1 || len(reads.observations) != 0 {
		t.Fatal("frame overflow not explicit")
	}
	reads.frames = 0
	reads.observations = make([]lexicalSymbolObservation, lexicalReadMaxObservations)
	a.lookupIdentifierScope(site)
	if reads.dropped != 2 || len(reads.observations) != lexicalReadMaxObservations {
		t.Fatal("observation overflow not explicit")
	}
}

func TestLexicalPublicationAdmissionOrderIsDeterministic(t *testing.T) {
	fn := parseProductFunction(t, "def work(value: i64) -> i64:\n    x = value\n    return x\n")
	index, err := IndexFunctionSource(fn, productContext())
	if err != nil {
		t.Fatal(err)
	}
	global := NewScope(nil)
	scope := NewScope(global)
	symbol := &Symbol{Name: "x"}
	global.Symbols["x"] = symbol
	var observations []lexicalSymbolObservation
	var want string
	for _, site := range []PositionedFunctionNode{fn.Body[0], fn.Body[1]} {
		observation, err := observeLexicalSymbol(lexicalSymbolRequest{Index: index, Site: site, Role: "read", Name: "x", Current: scope, Global: global}, symbol, true)
		if err != nil {
			t.Fatal(err)
		}
		observations = append(observations, observation)
		anchors, err := index.Capture([]PositionedFunctionNode{site})
		if err != nil {
			t.Fatal(err)
		}
		key, err := lexicalSelectionKey(lexicalSelectionSpec{Site: anchors[0], Role: "read", Domain: "symbol", Name: "x"})
		if err != nil {
			t.Fatal(err)
		}
		if want == "" || key.Name < want {
			want = key.Name
		}
	}
	for i := 0; i < 8; i++ {
		var selected []string
		registry := lexicalQueryEvaluators()
		registry[lexicalSelectionKind] = func(r *incremental.Reader, name string) (string, error) {
			selected = append(selected, name)
			return evaluateLexicalSelection(r, name)
		}
		limits := incremental.DefaultLimits()
		limits.Queries = 1
		owner, err := incremental.NewSession(registry, limits)
		if err != nil {
			t.Fatal(err)
		}
		session := NewIncrementalAnalysisSessionWithOptions(IncrementalAnalysisSessionOptions{AuditLexicalReads: true})
		session.lexical = owner
		reads := &lexicalReadInvocation{observations: observations}
		reads.publish(session)
		if len(selected) != 1 || selected[0] != want || session.Stats().LexicalQueriesSkipped != 1 {
			t.Fatalf("nondeterministic admission: selected %v, want %s", selected, want)
		}
	}
}

func TestLexicalReadVerificationFailureRevokesUniverse(t *testing.T) {
	fn := parseProductFunction(t, "def work(value: i64) -> i64:\n    return value\n")
	index, err := IndexFunctionSource(fn, productContext())
	if err != nil {
		t.Fatal(err)
	}
	global := NewScope(nil)
	global.Symbols["x"] = &Symbol{Name: "x"}
	observation, err := observeLexicalSymbol(lexicalSymbolRequest{Index: index, Site: fn.Body[0], Role: "read", Name: "x", Current: global, Global: global}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	session := NewIncrementalAnalysisSessionWithOptions(IncrementalAnalysisSessionOptions{AuditLexicalReads: true})
	reads := &lexicalReadInvocation{observations: []lexicalSymbolObservation{observation}}
	reads.publish(session)
	if session.Stats().LexicalVerificationFailures != 1 || session.lexical.Stats().RetainedQueries != 0 {
		t.Fatal("mismatched selection retained authority")
	}
}

func TestLexicalUnsupportedReadDoesNotPoisonOwner(t *testing.T) {
	fn := parseProductFunction(t, "def work(value: i64) -> i64:\n    x = value\n    return x\n")
	index, err := IndexFunctionSource(fn, productContext())
	if err != nil {
		t.Fatal(err)
	}
	global := NewScope(nil)
	scope := NewScope(global)
	parameter := &Symbol{Name: "value", Kind: SymbolParam, Node: fn, ParamIndex: 0}
	scope.Symbols["value"] = parameter
	unsupported := &Symbol{Name: "x", Kind: SymbolLocal}
	scope.Symbols["x"] = unsupported
	var observations []lexicalSymbolObservation
	for _, request := range []lexicalSymbolRequest{
		{Index: index, Site: fn.Body[0], Role: "read", Name: "value", Current: scope, Global: global},
		{Index: index, Site: fn.Body[1], Role: "read", Name: "x", Current: scope, Global: global},
	} {
		symbol, found := scope.Lookup(request.Name)
		observation, err := observeLexicalSymbol(request, symbol, found)
		if err != nil {
			t.Fatal(err)
		}
		observations = append(observations, observation)
	}
	session := NewIncrementalAnalysisSessionWithOptions(IncrementalAnalysisSessionOptions{AuditLexicalReads: true})
	reads := &lexicalReadInvocation{observations: observations}
	reads.publish(session)
	if stats := session.Stats(); stats.LexicalSelectionsVerified != 1 || stats.LexicalReadsDropped != 1 || stats.LexicalVerificationFailures != 0 {
		t.Fatalf("unsupported read poisoned valid parameter: %+v", stats)
	}
}

func TestLexicalRemovedSiteDoesNotPoisonSurvivingSite(t *testing.T) {
	fn := parseProductFunction(t, "def work(value: i64) -> i64:\n    x = value\n    return value\n")
	index, err := IndexFunctionSource(fn, productContext())
	if err != nil {
		t.Fatal(err)
	}
	global := NewScope(nil)
	scope := NewScope(global)
	parameter := &Symbol{Name: "value", Kind: SymbolParam, Node: fn, ParamIndex: 0}
	scope.Symbols["value"] = parameter
	var observations []lexicalSymbolObservation
	for _, site := range []PositionedFunctionNode{fn.Body[0], fn.Body[1]} {
		observation, err := observeLexicalSymbol(lexicalSymbolRequest{Index: index, Site: site, Role: "read", Name: "value", Current: scope, Global: global}, parameter, true)
		if err != nil {
			t.Fatal(err)
		}
		observations = append(observations, observation)
	}
	removed := fn.Body[0]
	surviving := fn.Body[1]
	batch := &lexicalAnchorBatch{}
	batch.add(index, removed)
	batch.add(index, surviving)
	fn.Body = fn.Body[1:]
	batch.validateAvailable()
	if batch.scans != 1 {
		t.Fatal("partial validation rescanned owner")
	}
	if _, err := batch.anchor(index, removed); err == nil {
		t.Fatal("removed site has authority")
	}
	if _, err := batch.anchor(index, surviving); err != nil {
		t.Fatal("surviving site lost authority:", err)
	}
	if _, err := batch.add(index, fn); err == nil {
		t.Fatal("partial batch not sealed")
	}
	session := NewIncrementalAnalysisSessionWithOptions(IncrementalAnalysisSessionOptions{AuditLexicalReads: true})
	reads := &lexicalReadInvocation{observations: observations}
	reads.publish(session)
	if stats := session.Stats(); stats.LexicalSelectionsVerified != 1 || stats.LexicalReadsDropped != 1 || stats.LexicalVerificationFailures != 0 {
		t.Fatalf("removed site poisoned live site: %+v", stats)
	}
}

func TestLexicalUnsupportedRepeatedVisitRevokesPriorVisit(t *testing.T) {
	fn := parseProductFunction(t, "def work(value: i64) -> i64:\n    x = value\n    return x\n")
	index, err := IndexFunctionSource(fn, productContext())
	if err != nil {
		t.Fatal(err)
	}
	global := NewScope(nil)
	scope := NewScope(global)
	symbol := &Symbol{Name: "x", Kind: SymbolLocal, Node: fn.Body[0]}
	scope.Symbols["x"] = symbol
	request := lexicalSymbolRequest{Index: index, Site: fn.Body[1], Role: "read", Name: "x", Current: scope, Global: global}
	supported, err := observeLexicalSymbol(request, symbol, true)
	if err != nil {
		t.Fatal(err)
	}
	symbol.Node = nil
	unsupported, err := observeLexicalSymbol(request, symbol, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, observations := range [][]lexicalSymbolObservation{{supported, unsupported, supported}, {unsupported, supported}} {
		session := NewIncrementalAnalysisSessionWithOptions(IncrementalAnalysisSessionOptions{AuditLexicalReads: true})
		reads := &lexicalReadInvocation{observations: observations}
		reads.publish(session)
		if stats := session.Stats(); stats.LexicalSelectionsVerified != 0 || stats.LexicalReadsDropped == 0 || stats.LexicalVerificationFailures != 0 {
			t.Fatalf("partly unsupported site admitted: %+v", stats)
		}
	}
}

func TestLexicalPartialLivenessRevokesUnscannableIndex(t *testing.T) {
	fn := parseProductFunction(t, "def work(value: i64) -> i64:\n    return value\n")
	index, err := IndexFunctionSource(fn, productContext())
	if err != nil {
		t.Fatal(err)
	}
	site := fn.Body[0]
	batch := &lexicalAnchorBatch{}
	if _, err := batch.add(index, site); err != nil {
		t.Fatal(err)
	}
	batch.validateAvailable()
	if _, err := batch.anchor(index, site); err != nil {
		t.Fatal(err)
	}
	fn.Name = strings.Repeat("x", functionSyntaxMaxBytes+1)
	batch.validateAvailable()
	if _, err := batch.anchor(index, site); err == nil {
		t.Fatal("failed source graph retained old anchor authority")
	}
	if _, err := index.Capture([]PositionedFunctionNode{site}); err == nil {
		t.Fatal("strict Capture accepted unscannable graph")
	}
}
