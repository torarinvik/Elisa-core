package semantic

import (
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func newSemanticHardeningTestAnalyzer() *Analyzer {
	return &Analyzer{
		file: &ast.File{Filename: "semantic_hardening.llcontext"},
		currentFuncDecl: &ast.FuncDecl{
			Position: lexer.Pos{File: "semantic_hardening.llcontext", Line: 1, Col: 1},
			Name:     "hardening_target",
		},
	}
}

func nestedOptionalType(depth int) Type {
	var current Type = &BuiltinType{Name: "i64"}
	for i := 0; i < depth; i++ {
		current = &OptionalType{Value: current}
	}
	return current
}

func requireSemanticHardeningError(t *testing.T, analyzer *Analyzer, want string) {
	t.Helper()
	if len(analyzer.diagnostics) == 0 {
		t.Fatalf("expected semantic hardening diagnostic containing %q", want)
	}
	got := analyzer.diagnostics[0].String()
	if !strings.Contains(got, want) {
		t.Fatalf("expected semantic hardening diagnostic containing %q, got %v", want, got)
	}
	if !strings.Contains(got, "hardening_target") {
		t.Fatalf("expected diagnostic to mention function context, got %v", got)
	}
}

func TestSubstituteTypeReportsRecursionLimit(t *testing.T) {
	analyzer := newSemanticHardeningTestAnalyzer()
	result := analyzer.substituteType(nestedOptionalType(semanticSubstitutionDepthLimit+2), nil, nil, nil, nil)
	if !IsInvalidType(result) {
		t.Fatalf("expected invalidType after substitution hardening, got %T", result)
	}
	requireSemanticHardeningError(t, analyzer, "type substitution recursion limit")
}

func TestCloneTrackedValueTypeReportsRecursionLimit(t *testing.T) {
	analyzer := newSemanticHardeningTestAnalyzer()
	result := analyzer.cloneTrackedValueType(nestedOptionalType(semanticCloneDepthLimit + 2))
	if !IsInvalidType(result) {
		t.Fatalf("expected invalidType after tracked-type clone hardening, got %T", result)
	}
	requireSemanticHardeningError(t, analyzer, "tracked-type clone recursion limit")
}

func TestContainsAffineHandleValuesReportsRecursionLimit(t *testing.T) {
	analyzer := newSemanticHardeningTestAnalyzer()
	if analyzer.containsAffineHandleValues(nestedOptionalType(semanticTraversalDepthLimit+2), map[string]bool{}) {
		t.Fatal("expected affine traversal hardening to return false on deeply nested type")
	}
	requireSemanticHardeningError(t, analyzer, "affine-handle traversal recursion limit")
}

func TestContainsBorrowedOwnerRefValuesReportsRecursionLimit(t *testing.T) {
	analyzer := newSemanticHardeningTestAnalyzer()
	if analyzer.containsBorrowedOwnerRefValues(nestedOptionalType(semanticTraversalDepthLimit+2), map[string]bool{}) {
		t.Fatal("expected borrowed-owner traversal hardening to return false on deeply nested type")
	}
	requireSemanticHardeningError(t, analyzer, "borrowed-owner traversal recursion limit")
}

func TestInferFuncReturnProvenanceForLocalIdentHandlesSelfAliasCycle(t *testing.T) {
	analyzer := newSemanticHardeningTestAnalyzer()
	analyzer.currentScope = NewScope(nil)
	analyzer.returnProvenanceLocalInProgress = map[*Symbol]bool{}

	fnType := &FuncType{Name: "self_alias", Return: &BuiltinType{Name: "i64"}}
	decl := &ast.VarDeclStmt{Name: "self_alias", Value: &ast.Ident{Name: "self_alias"}}
	sym := &Symbol{Name: "self_alias", Kind: SymbolLocal, Type: fnType, Node: decl}
	analyzer.currentScope.Define(sym)

	if analyzer.inferFuncReturnProvenanceForLocalIdent(&ast.Ident{Name: "self_alias"}, fnType) {
		t.Fatal("expected recursive local function alias provenance inference to stop conservatively")
	}
	if fnType.ReturnProvenanceKnown {
		t.Fatal("expected recursive local function alias to leave return provenance unknown")
	}
	if len(analyzer.returnProvenanceLocalInProgress) != 0 {
		t.Fatal("expected local provenance recursion guard to be cleared after inference")
	}
}

func TestFunctionValueTypeForExprCachesCanonicalReturnProvenance(t *testing.T) {
	paramType := &RefType{Elem: &BuiltinType{Name: "i64"}, State: RefStateNonNull}
	fnDecl := &ast.FuncDecl{
		Name:   "id_ref",
		Params: []ast.ParamDecl{{Name: "value"}},
		Body: []ast.Stmt{
			&ast.ReturnStmt{Value: &ast.Ident{Name: "value"}},
		},
	}
	fnType := &FuncType{
		Name:               "id_ref",
		Params:             []Type{paramType},
		ExplicitParamCount: 1,
		Return:             paramType,
	}
	globalScope := NewScope(nil)
	if _, ok := globalScope.Define(&Symbol{Name: "id_ref", Kind: SymbolFunc, Type: fnType, Node: fnDecl}); !ok {
		t.Fatal("expected global function symbol definition to succeed")
	}

	analyzer := &Analyzer{
		globalScope:                      globalScope,
		currentScope:                     NewScope(globalScope),
		namedTypes:                       map[string]Type{},
		exprTypes:                        map[ast.Expr]Type{},
		returnProvenanceInProgress:       map[*ast.FuncDecl]bool{},
		returnProvenanceLocalInProgress:  map[*Symbol]bool{},
		returnBorrowedOwnerRefInProgress: map[*ast.FuncDecl]bool{},
		returnBorrowedOwnerLocalProgress: map[*Symbol]bool{},
		sinkParamInferenceInProgress:     map[*ast.FuncDecl]bool{},
		currentRegionRefs:                map[*Symbol]regionRefState{},
		currentAffineValues:              map[affineValueKey]affineValueState{},
		currentBorrowedOwnerRefs:         map[*Symbol]borrowedOwnerRefState{},
		currentFunctionValues:            map[*Symbol]*FuncType{},
		currentSpecializedValueTypes:     map[*Symbol]Type{},
		currentValueBindings:             map[*Symbol]ast.Expr{},
		currentPackedVariantViews:        map[*Symbol]*PackedVariantViewType{},
		currentPackedStores:              map[string]*PackedEnumStoreType{},
		currentPackedStoreResolutions:    map[*Symbol]packedStoreResolution{},
		currentFunctionUsedPermissions:   map[string]bool{},
		functionAnalyses:                 map[*ast.FuncDecl]*FunctionAnalysis{},
		suppressOptimizationFacts:        true,
	}

	resolved, ok := analyzer.functionValueTypeForExpr(&ast.Ident{Name: "id_ref"})
	if !ok || resolved == nil {
		t.Fatal("expected functionValueTypeForExpr to resolve the function symbol")
	}
	if !resolved.ReturnProvenanceKnown {
		t.Fatal("expected resolved function value type to know return provenance")
	}
	if !regionRefStateHasParamDep(resolved.ReturnProvenance, 0) {
		t.Fatalf("expected resolved function value type to carry parameter return provenance, got %#v", resolved.ReturnProvenance)
	}
	if !fnType.ReturnProvenanceKnown {
		t.Fatal("expected canonical function type to cache inferred return provenance")
	}
	if !regionRefStateHasParamDep(fnType.ReturnProvenance, 0) {
		t.Fatalf("expected canonical function type to store parameter return provenance, got %#v", fnType.ReturnProvenance)
	}
}

func TestInferFuncReturnProvenanceForExprCachesCanonicalGlobalType(t *testing.T) {
	paramType := &RefType{Elem: &BuiltinType{Name: "i64"}, State: RefStateNonNull}
	fnDecl := &ast.FuncDecl{
		Name:   "id_ref",
		Params: []ast.ParamDecl{{Name: "value"}},
		Body: []ast.Stmt{
			&ast.ReturnStmt{Value: &ast.Ident{Name: "value"}},
		},
	}
	canonical := &FuncType{
		Name:               "id_ref",
		Params:             []Type{paramType},
		ExplicitParamCount: 1,
		Return:             paramType,
	}
	globalScope := NewScope(nil)
	if _, ok := globalScope.Define(&Symbol{Name: "id_ref", Kind: SymbolFunc, Type: canonical, Node: fnDecl}); !ok {
		t.Fatal("expected global function symbol definition to succeed")
	}

	analyzer := &Analyzer{
		globalScope:                      globalScope,
		namedTypes:                       map[string]Type{},
		exprTypes:                        map[ast.Expr]Type{},
		returnProvenanceInProgress:       map[*ast.FuncDecl]bool{},
		returnProvenanceLocalInProgress:  map[*Symbol]bool{},
		returnBorrowedOwnerRefInProgress: map[*ast.FuncDecl]bool{},
		returnBorrowedOwnerLocalProgress: map[*Symbol]bool{},
		sinkParamInferenceInProgress:     map[*ast.FuncDecl]bool{},
		currentRegionRefs:                map[*Symbol]regionRefState{},
		currentAffineValues:              map[affineValueKey]affineValueState{},
		currentBorrowedOwnerRefs:         map[*Symbol]borrowedOwnerRefState{},
		currentFunctionValues:            map[*Symbol]*FuncType{},
		currentSpecializedValueTypes:     map[*Symbol]Type{},
		currentValueBindings:             map[*Symbol]ast.Expr{},
		currentPackedVariantViews:        map[*Symbol]*PackedVariantViewType{},
		currentPackedStores:              map[string]*PackedEnumStoreType{},
		currentPackedStoreResolutions:    map[*Symbol]packedStoreResolution{},
		currentFunctionUsedPermissions:   map[string]bool{},
		functionAnalyses:                 map[*ast.FuncDecl]*FunctionAnalysis{},
		suppressOptimizationFacts:        true,
	}
	transient := &FuncType{
		Name:               canonical.Name,
		Params:             []Type{paramType},
		ExplicitParamCount: 1,
		Return:             paramType,
	}

	analyzer.inferFuncReturnProvenanceForExpr(&ast.Ident{Name: "id_ref"}, transient)

	if !transient.ReturnProvenanceKnown {
		t.Fatal("expected transient function type to receive inferred return provenance")
	}
	if !regionRefStateHasParamDep(transient.ReturnProvenance, 0) {
		t.Fatalf("expected transient function type to carry parameter return provenance, got %#v", transient.ReturnProvenance)
	}
	if !canonical.ReturnProvenanceKnown {
		t.Fatal("expected canonical global function type to cache inferred return provenance")
	}
	if !regionRefStateHasParamDep(canonical.ReturnProvenance, 0) {
		t.Fatalf("expected canonical global function type to store parameter return provenance, got %#v", canonical.ReturnProvenance)
	}
}
