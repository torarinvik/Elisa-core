package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func newSemanticHardeningTestAnalyzer() *Analyzer {
	return &Analyzer{
		file: &ast.File{Filename: "semantic_hardening.elisa"},
		currentFuncDecl: &ast.FuncDecl{
			Position: lexer.Pos{File: "semantic_hardening.elisa", Line: 1, Col: 1},
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

func recursiveTrackedStructType() Type {
	node := &StructType{Name: "TrackedNode", Fields: map[string]Field{}}
	nodeRef := &RefType{Elem: node, Mutable: true, State: RefStateNonNull}
	node.Fields["next"] = Field{Name: "next", Type: nodeRef}
	return node
}

func recursiveTrackedStructTypePair() (Type, Type) {
	left := &StructType{Name: "TrackedNode", Fields: map[string]Field{}}
	right := &StructType{Name: "TrackedNode", Fields: map[string]Field{}}
	left.Fields["next"] = Field{Name: "next", Type: &RefType{Elem: left, Mutable: true, State: RefStateNonNull}}
	right.Fields["next"] = Field{Name: "next", Type: &RefType{Elem: right, Mutable: true, State: RefStateNonNull}}
	return left, right
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

func TestCloneSpecializedValueTypeMapReusesSharedRecursiveClone(t *testing.T) {
	analyzer := newSemanticHardeningTestAnalyzer()
	recursive := recursiveTrackedStructType()
	left := &Symbol{Name: "left", Kind: SymbolLocal, Type: recursive}
	right := &Symbol{Name: "right", Kind: SymbolLocal, Type: recursive}

	cloned := analyzer.cloneSpecializedValueTypeMap(map[*Symbol]Type{
		left:  recursive,
		right: recursive,
	})
	if len(cloned) != 2 {
		t.Fatalf("expected cloned map to preserve both bindings, got %d entries", len(cloned))
	}
	if IsInvalidType(cloned[left]) || IsInvalidType(cloned[right]) {
		t.Fatalf("expected recursive tracked type clone to remain valid, got %#v and %#v", cloned[left], cloned[right])
	}
	if cloned[left] != cloned[right] {
		t.Fatal("expected shared recursive tracked type clone to be memoized across map entries")
	}
	clonedStruct, ok := cloned[left].(*StructType)
	if !ok {
		t.Fatalf("expected cloned recursive tracked type to remain a struct, got %T", cloned[left])
	}
	nextRef, ok := clonedStruct.Fields["next"].Type.(*RefType)
	if !ok {
		t.Fatalf("expected cloned recursive field to remain a ref, got %T", clonedStruct.Fields["next"].Type)
	}
	if nextRef.Elem != cloned[left] {
		t.Fatal("expected cloned recursive field to point at the memoized struct clone")
	}
}

func TestMergeSpecializedValueTypesPreservesRecursiveStructCycles(t *testing.T) {
	analyzer := newSemanticHardeningTestAnalyzer()
	left, right := recursiveTrackedStructTypePair()

	merged, ok := analyzer.mergeSpecializedValueTypes(left, right)
	if !ok {
		t.Fatal("expected recursive specialized struct types to merge successfully")
	}
	mergedStruct, ok := merged.(*StructType)
	if !ok {
		t.Fatalf("expected merged recursive type to remain a struct, got %T", merged)
	}
	nextRef, ok := mergedStruct.Fields["next"].Type.(*RefType)
	if !ok {
		t.Fatalf("expected merged recursive field to remain a ref, got %T", mergedStruct.Fields["next"].Type)
	}
	if nextRef.Elem != mergedStruct {
		t.Fatal("expected merged recursive field to point at the merged struct placeholder")
	}
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
