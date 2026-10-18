//go:build cgo

package semantic

import (
	"os/exec"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func analyzeRuntimeQuantifierStrict(t *testing.T, filename, src string) *Result {
	t.Helper()
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; SMT discharge test skipped")
	}
	l := lexer.New(filename, []byte(src))
	p := parser.New(l.Tokenize())
	file := p.ParseFile(filename)
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	return AnalyzeWithOptions(file, AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
}

func hasSMTProvenEnsure(result *Result, function string) bool {
	for _, proof := range result.ProofReport {
		if proof.Subject == "ensure "+function && proof.Predicate == "ensure" && proof.Outcome == ProofProvenSMT {
			return true
		}
	}
	return false
}

func TestSMTRuntimeRangeQuantifierMatchesDArraySpecification(t *testing.T) {
	result := analyzeRuntimeQuantifierStrict(t, "runtime_quantifier_darray.elisa", `
def all_positive(values: darray[i64]) -> bool:
    ensure result == (forall i in 0 ..< values.count: values[i] > 0)
    return forall i in 0 ..< values.count: values[i] > 0
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected the runtime range quantifier to match its specification, got: %v", errs)
	}
	if !hasSMTProvenEnsure(result, "all_positive") {
		t.Fatalf("expected the darray quantifier ensure to be proven by SMT, got: %+v", result.ProofReport)
	}
}

func TestSMTRuntimeRangeQuantifierPreservesOuterRangeEndpoint(t *testing.T) {
	result := analyzeRuntimeQuantifierStrict(t, "runtime_quantifier_nested_shadow.elisa", `
def nested(values: darray[i64]) -> bool:
	ensure result == (forall i in 0 ..< values.count: forall j in 0 ..< i: values[j] > 0)
	return forall i in 0 ..< values.count: forall j in 0 ..< i: values[j] > 0
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected nested runtime quantifiers to match their specification, got: %v", errs)
	}
	if !hasSMTProvenEnsure(result, "nested") {
		t.Fatalf("expected nested darray quantifier ensure to be proven by SMT, got: %+v", result.ProofReport)
	}
}

func TestSMTIndexedSViewGhostCallsKeepQuantifierBinder(t *testing.T) {
	source := `
ghost def find_spec(view: sview, needle: sview) -> i64:
    return view.len

ghost def bound_spec(view: sview) -> i64:
    return view.len

ghost def calls_match(facts: darray[sview]) -> bool:
    ensure forall i in 0 ..< facts.count:
        find_spec(facts[i], "size >=") == find_spec(facts[i], "size >=")
        and bound_spec(facts[i]) == bound_spec(facts[i])
    return true
	`
	result := analyzeRuntimeQuantifierStrict(t, "indexed_sview_ghost_calls.elisa", source)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected indexed sview ghost-call congruence to be proven, got: %v", errs)
	}
	if !hasSMTProvenEnsure(result, "calls_match") {
		t.Fatalf("expected indexed sview ghost-call ensure to be proven by SMT, got: %+v", result.ProofReport)
	}
	fnSymbol, ok := result.analyzer.globalScope.Lookup("calls_match")
	if !ok || fnSymbol == nil {
		t.Fatal("calls_match declaration is missing from the analyzed program")
	}
	fn, ok := fnSymbol.Node.(*ast.FuncDecl)
	if !ok || fn == nil {
		t.Fatal("calls_match symbol is not a function declaration")
	}
	var quantifier *ast.QuantifierExpr
	result.analyzer.walkStaticStmts(fn.Body, func(expr ast.Expr) bool {
		if q, ok := expr.(*ast.QuantifierExpr); ok {
			quantifier = q
			return true
		}
		return false
	})
	if quantifier == nil {
		// Ensure expressions are not part of the statement walk.
		for _, ensure := range fn.EnsureValues {
			if q, ok := ensure.(*ast.QuantifierExpr); ok {
				quantifier = q
				break
			}
		}
	}
	if quantifier == nil {
		t.Fatal("calls_match quantifier is missing from the AST")
	}
	analyzer := result.analyzer
	analyzer.currentFuncDecl = fn
	analyzer.currentScope = NewScope(analyzer.globalScope)
	analyzer.currentScope.Define(&Symbol{Name: "facts", Kind: SymbolParam, Type: &DArrayType{Elem: &SViewType{}}, Node: fn, ParamIndex: 0})
	tr := analyzer.newSMTTranslator(nil)
	triggers := tr.collectSelectTriggers(quantifier.Body, map[string]string{"i": "q_i"}, quantifier.Vars)
	foundIndexedViewTrigger := false
	for _, trigger := range triggers {
		if strings.Contains(trigger, "$edata") && strings.Contains(trigger, "q_i") {
			foundIndexedViewTrigger = true
		}
	}
	if !foundIndexedViewTrigger {
		t.Fatalf("expected an indexed-view data trigger containing q_i, got %v", triggers)
	}
	var indexedCalls []*ast.CallExpr
	walkStoreScanNodes([]ast.Stmt{&ast.ExprStmt{Expr: quantifier.Body}}, func(node any) {
		if call, ok := node.(*ast.CallExpr); ok {
			indexedCalls = append(indexedCalls, call)
		}
	})
	if len(indexedCalls) == 0 {
		t.Fatal("calls_match quantifier contains no calls to use for the trigger subterm check")
	}
	callTerm, ok := tr.termEnv(indexedCalls[0], map[string]string{"i": "q_i"})
	if !ok {
		t.Fatal("indexed sview call did not produce an SMT term")
	}
	for _, trigger := range triggers {
		if strings.Contains(trigger, "$edata") && !strings.Contains(callTerm, trigger) {
			t.Fatalf("indexed-view trigger is not a subterm of the scalar-call term: trigger=%s term=%s", trigger, callTerm)
		}
	}
	for _, axiom := range tr.auxDecls {
		if strings.Contains(axiom, "(assert (>= ") && strings.Contains(axiom, "q_i") {
			t.Fatalf("quantifier binder escaped into a top-level view-length assertion: %s", axiom)
		}
	}
}

func TestSMTIndexedSViewGhostCallsKeepMutableContainersOpaque(t *testing.T) {
	result := analyzeRuntimeQuantifierStrict(t, "indexed_sview_mutable_ghost_calls.elisa", `
ghost def find_spec(view: sview, needle: sview) -> i64:
    return view.len

ghost def calls_match(facts: mutable darray[sview]) -> bool:
    ensure forall i in 0 ..< facts.count:
        find_spec(facts[i], "size >=") == find_spec(facts[i], "size >=")
    return true
`)
	if hasSMTProvenEnsure(result, "calls_match") {
		t.Fatalf("mutable indexed sview calls must remain opaque: %+v", result.ProofReport)
	}
}

func TestSMTIndexedSViewCallMetadataFlowsThroughCallee(t *testing.T) {
	result := analyzeRuntimeQuantifierStrict(t, "indexed_sview_call_metadata.elisa", `
ghost def bound_spec(view: sview) -> i64:
    ensure result == view.len
    return view.len

def step(view: sview) -> i64:
    ensure result == bound_spec(view)
    return view.len

def indexed_step(facts: darray[sview]) -> i64:
    requires facts.count > 0
    ensure result == step(facts[0])
    return facts[0].len
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected indexed view metadata to survive helper-call binding, got: %v", errs)
	}
	if !hasSMTProvenEnsure(result, "indexed_step") {
		t.Fatalf("expected indexed view helper ensure to be proven by SMT, got: %+v", result.ProofReport)
	}
}

func TestSMTRuntimeRangeQuantifierKeepsUnsupportedContainersOpaque(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "mutable_value",
			src: `
def all_positive(values: mutable darray[i64]) -> bool:
    ensure result == (forall i in 0 ..< values.count: values[i] > 0)
    return forall i in 0 ..< values.count: values[i] > 0
`,
		},
		{
			name: "reference",
			src: `
def all_positive(values: darray[i64]&) -> bool:
    ensure result == (forall i in 0 ..< values.count: values[i] > 0)
    return forall i in 0 ..< values.count: values[i] > 0
`,
		},
		{
			name: "struct_elements",
			src: `
struct Row:
    value: i64

def all_positive(values: darray[Row]) -> bool:
    ensure result == (forall i in 0 ..< values.count: values[i].value > 0)
    return forall i in 0 ..< values.count: values[i].value > 0
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeRuntimeQuantifierStrict(t, "runtime_quantifier_"+tc.name+".elisa", tc.src)
			if hasSMTProvenEnsure(result, "all_positive") {
				t.Fatalf("unsupported container shape must not be promoted to a quantified proof: %+v", result.ProofReport)
			}
		})
	}
}
