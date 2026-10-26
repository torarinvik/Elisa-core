//go:build cgo

package semantic

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"elisacore/src/ast"
)

func resolvedTypeNameRowsForTest(result *Result) []string {
	rows := make([]string, 0, len(result.analyzer.resolvedTypeNames))
	for expr, canonical := range result.analyzer.resolvedTypeNames {
		pos := expr.Pos()
		rows = append(rows, fmt.Sprintf("%T@%s:%d=%s", expr, pos.File, pos.Offset, canonical))
	}
	sort.Strings(rows)
	return rows
}

func resolvedTypeNameBodyRowsForTest(state *RetainedAnalysis, name string) []ast.TypeExpr {
	var function *ast.FuncDecl
	for _, declaration := range state.decls {
		if fn, ok := declaration.Decl.(*ast.FuncDecl); ok && fn.Name == name {
			function = fn
			break
		}
	}
	if function == nil {
		return nil
	}
	var rows []ast.TypeExpr
	walkStoreScanNodes(function.Body, func(node any) {
		if expr, ok := node.(ast.TypeExpr); ok {
			if _, present := state.a.resolvedTypeNames[expr]; present {
				rows = append(rows, expr)
			}
		}
	})
	return rows
}

func assertResolvedTypeNameParity(t *testing.T, got, want *Result) {
	t.Helper()
	if actual, expected := retainedDiagnostics(got), retainedDiagnostics(want); actual != expected {
		t.Fatalf("diagnostics differ\nretained:\n%s\ncold:\n%s", actual, expected)
	}
	if actual, expected := resolvedTypeNameRowsForTest(got), resolvedTypeNameRowsForTest(want); fmt.Sprint(actual) != fmt.Sprint(expected) {
		t.Fatalf("resolved type-name rows differ\nretained: %v\ncold: %v", actual, expected)
	}
	assertRetainedCanonicalLiveResultParity(t, want, got)
}

func TestRetainedResolvedTypeNamesBodyRowsReplaceAndRestoreExactly(t *testing.T) {
	const baseline = `module Sample:
    public:
        struct Box:
            value: i32

        struct Other:
            value: i32

        def changed() -> i32:
            return 1

def main() -> i32:
    return Sample::changed()
`
	boxBody := strings.Replace(baseline, "            return 1", "            local: Box = Box{value: 7}\n            return local.value", 1)
	otherBody := strings.Replace(boxBody, "local: Box = Box{value: 7}", "local: Other = Other{value: 9}", 1)
	plainBody := strings.Replace(otherBody, "            local: Other = Other{value: 9}\n            return local.value", "            return 3", 1)
	staticBody := strings.Replace(baseline, "            return 1", "            static if true:\n                local: Box = Box{value: 7}\n                return local.value\n            static else:\n                local: Other = Other{value: 9}\n                return local.value", 1)
	staticElseBody := strings.Replace(staticBody, "static if true:", "static if false:", 1)
	if boxBody == baseline || otherBody == boxBody || plainBody == otherBody || staticBody == baseline || staticElseBody == staticBody {
		t.Fatal("fixture edits did not apply")
	}
	options := AnalyzeOptions{}
	initial, state := AnalyzeRetained(retainedParse(t, baseline), retainedParse(t, baseline), []byte(baseline), options)
	if state == nil {
		t.Fatal("baseline analysis was not retained")
	}
	assertResolvedTypeNameParity(t, initial, AnalyzeWithOptions(retainedParse(t, baseline), options))

	for index, source := range []string{boxBody, otherBody, plainBody, staticBody, staticElseBody, boxBody} {
		oldBodyRows := resolvedTypeNameBodyRowsForTest(state, "changed")
		updated, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatalf("edit %d fell back: %v", index, err)
		}
		for _, expr := range oldBodyRows {
			if _, present := state.a.resolvedTypeNames[expr]; present {
				t.Fatalf("edit %d retained a resolved type-name row for the retired body AST", index)
			}
		}
		cold := AnalyzeWithOptions(retainedParse(t, source), options)
		assertResolvedTypeNameParity(t, updated, cold)
		if index == 0 && len(resolvedTypeNameBodyRowsForTest(state, "changed")) == 0 {
			t.Fatal("fixture did not record a body-local qualified type-name row")
		}
		if index == 0 {
			checkpoint, err := state.MarshalState()
			if err != nil {
				t.Fatalf("marshal after first body edit: %v", err)
			}
			state, err = RestoreAnalysisState(checkpoint)
			if err != nil {
				t.Fatalf("restore after first body edit: %v", err)
			}
		}
	}
}

func TestRetainedFastPrepareRebindsTypedBodyRows(t *testing.T) {
	previousMode := retainedFastPrepareMode
	retainedFastPrepareMode = "on"
	defer func() { retainedFastPrepareMode = previousMode }()

	const typed = `module Sample:
    public:
        struct Box:
            value: i32

        def changed() -> i32:
            local: Box = Box{value: 7}
            return local.value

def main() -> i32:
    return Sample::changed()
`
	options := AnalyzeOptions{}
	first, state := AnalyzeRetained(retainedParse(t, typed), retainedParse(t, typed), []byte(typed), options)
	if state == nil {
		t.Fatal("typed fast-preparation baseline was not retained")
	}
	if len(resolvedTypeNameBodyRowsForTest(state, "changed")) == 0 {
		t.Fatal("typed baseline did not retain any source type-name rows")
	}
	assertResolvedTypeNameParity(t, first, AnalyzeWithOptions(retainedParse(t, typed), options))
	checkpoint, err := state.MarshalState()
	if err != nil {
		t.Fatalf("marshal typed baseline: %v", err)
	}
	state, err = RestoreAnalysisState(checkpoint)
	if err != nil {
		t.Fatalf("restore typed baseline: %v", err)
	}

	for _, value := range []string{"8", "11"} {
		oldRows := resolvedTypeNameBodyRowsForTest(state, "changed")
		source := strings.Replace(typed, "value: 7", "value: "+value, 1)
		updated, updateErr := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if updateErr != nil {
			t.Fatalf("numeric-only typed edit %s fell back: %v", value, updateErr)
		}
		if !state.LastStats.FastPrepared {
			t.Fatalf("numeric-only typed edit %s did not use fast preparation: %+v", value, state.LastStats)
		}
		for _, expr := range oldRows {
			if _, present := state.a.resolvedTypeNames[expr]; present {
				t.Fatalf("numeric-only typed edit %s retained an old TypeExpr key", value)
			}
		}
		cold := AnalyzeWithOptions(retainedParse(t, source), options)
		assertResolvedTypeNameParity(t, updated, cold)
		checkpoint, err = state.MarshalState()
		if err != nil {
			t.Fatalf("marshal after numeric-only edit %s: %v", value, err)
		}
		state, err = RestoreAnalysisState(checkpoint)
		if err != nil {
			t.Fatalf("restore after numeric-only edit %s: %v", value, err)
		}
	}
}

func TestResolvedTypeNameContributionComparisonRejectsUnplannedOwnerChanges(t *testing.T) {
	old := resolvedTypeNameContributions{
		functions: map[string]resolvedTypeNameFunctionContribution{
			"A::work#0": {header: "header-a", body: "body-a"},
			"B::work#0": {header: "header-b", body: "body-b"},
		},
		declarations: map[string]string{"decl:Box#0": "box-type"},
	}
	owners := map[string]bool{"A::work#0": true, "B::work#0": true}

	current := old
	current.functions = map[string]resolvedTypeNameFunctionContribution{
		"A::work#0": {header: "header-a", body: "body-a"},
		"B::work#0": {header: "header-b", body: "canonical-name-changed"},
	}
	if reason := compareResolvedTypeNameContributions(old, current, owners, owners, 0, 0, nil, map[string]bool{"A::work#0": true}); reason == "" {
		t.Fatal("same-size canonical change in an unchanged namespace owner was accepted")
	}

	current = old
	current.functions = map[string]resolvedTypeNameFunctionContribution{
		"A::work#0": {header: "header-a", body: "body-a-edited"},
		"B::work#0": {header: "header-b", body: "body-b"},
	}
	if reason := compareResolvedTypeNameContributions(old, current, owners, owners, 0, 0, nil, map[string]bool{"A::work#0": true}); reason != "" {
		t.Fatalf("planned body reanalysis was rejected: %s", reason)
	}

	current.functions["A::work#0"] = resolvedTypeNameFunctionContribution{header: "changed-header", body: "body-a-edited"}
	if reason := compareResolvedTypeNameContributions(old, current, owners, owners, 0, 0, nil, map[string]bool{"A::work#0": true}); reason == "" {
		t.Fatal("header contribution change was accepted as a body reanalysis")
	}

	current = old
	current.declarations = map[string]string{"decl:Box#0": "changed-declaration-type"}
	if reason := compareResolvedTypeNameContributions(old, current, owners, owners, 0, 0, nil, nil); reason == "" {
		t.Fatal("non-function declaration contribution change was accepted")
	}
}

func TestResolvedTypeNameOwnershipUsesExactASTReferences(t *testing.T) {
	sharedValue := &ast.Ident{Name: "x"}
	left := &ast.FuncDecl{Name: "work", Body: []ast.Stmt{&ast.ReturnStmt{Value: sharedValue}}}
	right := &ast.FuncDecl{Name: "work", Body: []ast.Stmt{&ast.ReturnStmt{Value: sharedValue}}}
	benign := []scopedDecl{{Decl: left, Namespace: "Left"}, {Decl: right, Namespace: "Right"}}
	keys := retainedPreBodyFunctionKeys(benign)
	if keys[left] == keys[right] {
		t.Fatal("same-named declarations in different namespaces shared a stable owner key")
	}
	certificate, _ := captureResolvedTypeNameContributions(nil, benign, keys, true)
	if certificate.failure != "" {
		t.Fatalf("a benign shared non-TypeExpr AST node blocked ownership: %s", certificate.failure)
	}

	sharedType := &ast.NamedType{Name: "Box"}
	left = &ast.FuncDecl{Name: "typed", Params: []ast.ParamDecl{{Name: "x", Type: sharedType}}}
	right = &ast.FuncDecl{Name: "typed", Params: []ast.ParamDecl{{Name: "x", Type: sharedType}}}
	aliased := []scopedDecl{{Decl: left, Namespace: "Left"}, {Decl: right, Namespace: "Right"}}
	keys = retainedPreBodyFunctionKeys(aliased)
	certificate, _ = captureResolvedTypeNameContributions(map[ast.TypeExpr]string{sharedType: "Types.Box"}, aliased, keys, true)
	if certificate.failure == "" {
		t.Fatal("a TypeExpr pointer shared across declaration owners was admitted")
	}
	certificate, _ = captureResolvedTypeNameContributions(nil, aliased, keys, true)
	if certificate.failure == "" {
		t.Fatal("a shared TypeExpr pointer was admitted before its canonical row was produced")
	}

	left = &ast.FuncDecl{
		Name:   "header_body_alias",
		Params: []ast.ParamDecl{{Name: "x", Type: sharedType}},
		Body:   []ast.Stmt{&ast.VarDeclStmt{Name: "y", Type: sharedType}},
	}
	aliased = []scopedDecl{{Decl: left, Namespace: "Local"}}
	keys = retainedPreBodyFunctionKeys(aliased)
	certificate, _ = captureResolvedTypeNameContributions(map[ast.TypeExpr]string{sharedType: "Types.Box"}, aliased, keys, true)
	if certificate.failure == "" {
		t.Fatal("a TypeExpr pointer shared across a function header and body was admitted")
	}

	unowned := &ast.NamedType{Name: "Box"}
	benignKeys := retainedPreBodyFunctionKeys(benign)
	certificate, _ = captureResolvedTypeNameContributions(map[ast.TypeExpr]string{unowned: "Types.Box"}, benign, benignKeys, true)
	if certificate.failure == "" {
		t.Fatal("an analyzer key without exact source-node identity was admitted by position")
	}

	unknown := &ast.NamedType{Name: "Box"}
	live := &ast.FuncDecl{Name: "cleanup"}
	a := &Analyzer{resolvedTypeNames: map[ast.TypeExpr]string{unknown: "Types.Box"}}
	retirement, err := prepareResolvedTypeNameRetirement(a, nil, nil, []*ast.FuncDecl{live}, nil, nil, false)
	if err != nil {
		t.Fatalf("cleanup rejected an unowned row instead of preserving it: %v", err)
	}
	if err := retirement.apply(a); err != nil {
		t.Fatalf("apply empty exact cleanup plan: %v", err)
	}
	if got := a.resolvedTypeNames[unknown]; got != "Types.Box" {
		t.Fatalf("cleanup retired an unowned type-name key: %q", got)
	}
}

func TestResolvedTypeNameRetirementCleansRemovedFunctionRows(t *testing.T) {
	typeExpr := &ast.NamedType{Name: "Box"}
	function := &ast.FuncDecl{Name: "removed", Body: []ast.Stmt{&ast.VarDeclStmt{Name: "value", Type: typeExpr}}}
	decls := []scopedDecl{{Decl: function, Namespace: "Sample"}}
	keys := retainedPreBodyFunctionKeys(decls)
	certificate, locations := captureResolvedTypeNameContributions(map[ast.TypeExpr]string{typeExpr: "Sample.Box"}, decls, keys, true)
	if certificate.failure != "" {
		t.Fatalf("fixture type-name ownership was not certified: %s", certificate.failure)
	}
	a := &Analyzer{resolvedTypeNames: map[ast.TypeExpr]string{typeExpr: "Sample.Box"}}
	retirement, err := prepareResolvedTypeNameRetirement(a, resolvedTypeNameSourceOwners(locations), keys, nil, []*ast.FuncDecl{function}, nil, true)
	if err != nil {
		t.Fatalf("prepare removed-function type-name retirement: %v", err)
	}
	if err := retirement.apply(a); err != nil {
		t.Fatalf("apply removed-function type-name retirement: %v", err)
	}
	if _, present := a.resolvedTypeNames[typeExpr]; present {
		t.Fatal("removed function retained its resolved type-name row")
	}
}

func TestRetainedRefineAliasKeepsDeclarationOwnedTypeNameAfterLocalRemoval(t *testing.T) {
	const baseline = `module Types:
    struct Amount:
        value: i64
using Types
refine Positive = Amount where self.value > 0

module First:
    def left(value: Types::Amount) -> i64:
        checked: Positive = value
        return checked.value

module Second:
    def right(value: Types::Amount) -> i64:
        checked: Positive = value
        return checked.value
`
	const edited = `module Types:
    struct Amount:
        value: i64
using Types
refine Positive = Amount where self.value > 0

module First:
    def left(value: Types::Amount) -> i64:
        return value.value

module Second:
    def right(value: Types::Amount) -> i64:
        checked: Positive = value
        return checked.value
`
	_, state := AnalyzeRetained(retainedParse(t, baseline), retainedParse(t, baseline), []byte(baseline), AnalyzeOptions{})
	if state == nil {
		t.Fatal("refine-alias baseline was not retained")
	}
	var refine *ast.RefineDecl
	var left, right *ast.FuncDecl
	for _, declaration := range retainedWalkDecls(state.a.file) {
		switch node := declaration.(type) {
		case *ast.RefineDecl:
			refine = node
		case *ast.FuncDecl:
			switch node.Name {
			case "left":
				left = node
			case "right":
				right = node
			}
		}
	}
	if refine == nil || refine.Base == nil || left == nil || right == nil {
		t.Fatal("fixture declarations were not analyzed")
	}
	checkedType := func(fn *ast.FuncDecl) *ast.WhereRefinementTypeExpr {
		var found *ast.WhereRefinementTypeExpr
		walkStoreScanNodes(fn.Body, func(node any) {
			if declaration, ok := node.(*ast.VarDeclStmt); ok && declaration.Name == "checked" {
				found, _ = declaration.Type.(*ast.WhereRefinementTypeExpr)
			}
		})
		return found
	}
	if leftChecked, rightChecked := checkedType(left), checkedType(right); leftChecked == nil || rightChecked == nil || leftChecked.Base != refine.Base || rightChecked.Base != refine.Base {
		t.Fatal("local refine aliases did not share the declaration-owned base TypeExpr")
	}
	canonical := state.a.resolvedTypeNames[refine.Base]
	if canonical == "" {
		t.Fatal("refine declaration base has no canonical type-name row")
	}

	updated, err := state.Update(retainedParse(t, edited), retainedParse(t, edited), []byte(edited))
	if err != nil {
		t.Fatalf("removing the first local refine alias fell back: %v", err)
	}
	if got := state.a.resolvedTypeNames[refine.Base]; got != canonical {
		t.Fatalf("body cleanup retired or changed declaration-owned type-name row: got %q want %q", got, canonical)
	}
	if rightChecked := checkedType(right); rightChecked == nil || rightChecked.Base != refine.Base {
		t.Fatal("unchanged second body lost its declaration-owned refine base")
	}
	assertResolvedTypeNameParity(t, updated, AnalyzeWithOptions(retainedParse(t, edited), AnalyzeOptions{}))
}
