//go:build cgo

package semantic

import (
	"fmt"
	"reflect"
	"testing"

	"elisacore/src/ast"
)

// scopedArenaInStoreStmt creates an analysis-only Store Ident for a RegionStmt.
// Its exact binding role is (owning RegionStmt, same name, same source position),
// not arbitrary equality with a source Ident. Account for all observations on
// this node before the retained oracle projects away historical body nodes.
// Resolve the binding through ExprFacts.base -> optimizationSymbolIDs ->
// Symbol.Node. Only facts whose binder is absent from BOTH current source and
// current function CFG are historical. Current-binding duplicates may coalesce
// only when EVERY observation agrees; current base IDs are never rewritten.
func accountRetainedScopedRegionFacts(t testing.TB, original, copied *Result, clones map[PositionedFunctionNode]PositionedFunctionNode) (map[*ast.RegionStmt]*ast.Ident, error) {
	t.Helper()
	source := map[PositionedFunctionNode]PositionedFunctionNode{}
	mapIncrementalCurrentAST(t, reflect.ValueOf(original.File), reflect.ValueOf(original.File), source)
	// Symbol IDs are invocation-local names, not sortable generations. Resolve
	// them through the actual analyzer's symbol table and binder node identity.
	symbols := map[string]*Symbol{}
	if original.analyzer != nil {
		for sym, base := range original.analyzer.optimizationSymbolIDs {
			if prior := symbols[base]; prior != nil && prior != sym {
				return nil, fmt.Errorf("ambiguous optimization symbol identity %q", base)
			}
			symbols[base] = sym
		}
	}
	cfgNodes := map[PositionedFunctionNode]PositionedFunctionNode{}
	for fn, analysis := range original.FunctionAnalyses {
		if _, current := source[fn]; !current || analysis == nil || analysis.CFG == nil {
			continue
		}
		mapIncrementalCurrentAST(t, reflect.ValueOf(analysis.CFG), reflect.ValueOf(analysis.CFG), cfgNodes)
	}
	grouped := map[*ast.RegionStmt][]*ast.Ident{}
	historical := []*ast.Ident{}
	for expr, typ := range original.ExprTypes {
		ident, ok := expr.(*ast.Ident)
		if !ok {
			continue
		}
		if _, syntactic := source[ident]; syntactic {
			continue
		}
		fact, hasFact := original.ExprFacts[ident]
		sym := symbols[fact.base]
		if !hasFact || sym == nil || sym.Kind != SymbolRegion {
			continue
		}
		owner, ok := sym.Node.(*ast.RegionStmt)
		if !ok || owner.OwnerName != "" || len(owner.Body) == 0 || ident.Name != owner.Name || ident.Position != owner.Position {
			continue
		}
		if !SameType(typ, original.analyzer.namedTypes["Arena"]) {
			return nil, fmt.Errorf("synthetic region-owner %q has non-Arena type %v", ident.Name, typ)
		}
		current, ok := clones[ident].(*ast.Ident)
		if !ok {
			return nil, fmt.Errorf("synthetic region-owner clone unavailable")
		}
		if _, active := source[owner]; !active {
			if _, referenced := cfgNodes[owner]; referenced {
				return nil, fmt.Errorf("non-source region binder %q is still referenced by current function analysis", owner.Name)
			}
			// This is a proven old binder, not another observation of the live
			// binding. Apply the retained oracle's historical-node projection.
			historical = append(historical, current)
			continue
		}
		grouped[owner] = append(grouped[owner], current)
	}
	fields := reflect.ValueOf(copied).Elem()
	for _, node := range historical {
		key := reflect.ValueOf(node)
		for i := 0; i < fields.NumField(); i++ {
			table := fields.Field(i)
			if table.Kind() == reflect.Map && !table.IsNil() && key.Type().AssignableTo(table.Type().Key()) {
				table.SetMapIndex(key, reflect.Value{})
			}
		}
	}
	result := map[*ast.RegionStmt]*ast.Ident{}
	for owner, nodes := range grouped {
		observations := make([]map[int]reflect.Value, len(nodes))
		for i, node := range nodes {
			observations[i] = map[int]reflect.Value{}
			key := reflect.ValueOf(node)
			for f := 0; f < fields.NumField(); f++ {
				table := fields.Field(f)
				if table.Kind() != reflect.Map || table.IsNil() || !key.Type().AssignableTo(table.Type().Key()) {
					continue
				}
				if value := table.MapIndex(key); value.IsValid() {
					observations[i][f] = value
				}
			}
		}
		for i := 1; i < len(nodes); i++ {
			if len(observations[i]) != len(observations[0]) {
				return nil, fmt.Errorf("synthetic region-owner fact inventory differs for %q", owner.Name)
			}
			for field, value := range observations[0] {
				other, found := observations[i][field]
				if !found || !reflect.DeepEqual(value.Interface(), other.Interface()) {
					return nil, fmt.Errorf("conflicting synthetic region-owner observations for %q in %s: %s", owner.Name, fields.Type().Field(field).Name, retainedCanonicalFirstDifference(value, other))
				}
			}
		}
		canonical := nodes[0]
		for i := 1; i < len(nodes); i++ {
			for field := range observations[i] {
				fields.Field(field).SetMapIndex(reflect.ValueOf(nodes[i]), reflect.Value{})
			}
		}
		currentOwner, ok := clones[owner].(*ast.RegionStmt)
		if !ok {
			return nil, fmt.Errorf("synthetic region binding lost its AST owner")
		}
		result[currentOwner] = canonical
	}
	return result, nil
}

func TestRetainedScopedRegionFactsRequireEveryDuplicateObservation(t *testing.T) {
	result := AnalyzeWithOptions(retainedParse(t, "def region_work() -> i64:\n    region r(4096):\n        return 0\n"), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	// Discover one known wrapper role, then emulate the historical duplicate left
	// by reanalysis. Copy all its observations, rather than only its type.
	nodes := map[PositionedFunctionNode]PositionedFunctionNode{}
	copied := cloneRetainedCanonicalResultRecording(result, nodes)
	roles, err := accountRetainedScopedRegionFacts(t, result, copied, nodes)
	if err != nil || len(roles) != 1 {
		t.Fatalf("fixture did not expose one scoped wrapper role: %v %v", roles, err)
	}
	var originalIdent *ast.Ident
	for original, current := range nodes {
		for _, ident := range roles {
			if current == ident {
				originalIdent, _ = original.(*ast.Ident)
			}
		}
	}
	if originalIdent == nil {
		t.Fatal("missing original wrapper identifier")
	}
	duplicate := *originalIdent
	fields := reflect.ValueOf(result).Elem()
	for i := 0; i < fields.NumField(); i++ {
		table := fields.Field(i)
		key := reflect.ValueOf(originalIdent)
		if table.Kind() != reflect.Map || table.IsNil() || !key.Type().AssignableTo(table.Type().Key()) {
			continue
		}
		if value := table.MapIndex(key); value.IsValid() {
			table.SetMapIndex(reflect.ValueOf(&duplicate), value)
		}
	}
	originalCount := len(result.ExprTypes)
	nodes = map[PositionedFunctionNode]PositionedFunctionNode{}
	copied = cloneRetainedCanonicalResultRecording(result, nodes)
	if _, err := accountRetainedScopedRegionFacts(t, result, copied, nodes); err != nil {
		t.Fatal(err)
	}
	if len(result.ExprTypes) != originalCount {
		t.Fatal("oracle mutated original published facts")
	}
	// An altered duplicate must fail rather than being hidden by selecting another.
	result.ExprTypes[&duplicate] = &BuiltinType{Name: "i64"}
	nodes = map[PositionedFunctionNode]PositionedFunctionNode{}
	copied = cloneRetainedCanonicalResultRecording(result, nodes)
	if _, err := accountRetainedScopedRegionFacts(t, result, copied, nodes); err == nil {
		t.Fatal("conflicting historical observation silently dropped")
	}
}

func TestRetainedScopedRegionFactsUseLiveBinderIdentity(t *testing.T) {
	result := AnalyzeWithOptions(retainedParse(t, "def region_work() -> i64:\n    region r(4096):\n        return 0\n"), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	nodes := map[PositionedFunctionNode]PositionedFunctionNode{}
	copied := cloneRetainedCanonicalResultRecording(result, nodes)
	roles, err := accountRetainedScopedRegionFacts(t, result, copied, nodes)
	if err != nil || len(roles) != 1 {
		t.Fatalf("missing live wrapper role: %v %v", roles, err)
	}
	var ident *ast.Ident
	for original, current := range nodes {
		for _, role := range roles {
			if current == role {
				ident, _ = original.(*ast.Ident)
			}
		}
	}
	if ident == nil {
		t.Fatal("missing original wrapper")
	}
	currentFact := result.ExprFacts[ident]
	var binder *ast.RegionStmt
	for sym, base := range result.analyzer.optimizationSymbolIDs {
		if base == currentFact.base {
			binder, _ = sym.Node.(*ast.RegionStmt)
		}
	}
	if binder == nil {
		t.Fatal("missing exact region binder")
	}
	historicalBinder := *binder
	historicalIdent := *ident
	historicalFact := currentFact
	// A numerically larger ID is deliberately historical: identity/reachability,
	// not ID ordering, must identify the current binding.
	historicalFact.base = "symbol@999999"
	historicalSymbol := &Symbol{Name: binder.Name, Kind: SymbolRegion, Type: result.analyzer.namedTypes["Arena"], Node: &historicalBinder}
	result.analyzer.optimizationSymbolIDs[historicalSymbol] = historicalFact.base
	result.ExprTypes[&historicalIdent] = result.ExprTypes[ident]
	result.ExprFacts[&historicalIdent] = historicalFact
	originalCount := len(result.ExprTypes)
	nodes = map[PositionedFunctionNode]PositionedFunctionNode{}
	copied = cloneRetainedCanonicalResultRecording(result, nodes)
	roles, err = accountRetainedScopedRegionFacts(t, result, copied, nodes)
	if err != nil || len(roles) != 1 {
		t.Fatalf("historical binder not projected: %v %v", roles, err)
	}
	if len(result.ExprTypes) != originalCount || len(copied.ExprTypes) != originalCount-1 {
		t.Fatal("projection changed originals or retained a proven historical role")
	}
	for _, current := range roles {
		if copied.ExprFacts[current].base != currentFact.base {
			t.Fatal("current base rewritten or historical generation selected")
		}
	}
	// A binder still used by current structured analysis is not historical, even
	// when absent from the source AST; it must not be silently filtered.
	attached := false
	for _, analysis := range result.FunctionAnalyses {
		if analysis != nil && analysis.CFG != nil && len(analysis.CFG.Blocks) != 0 {
			analysis.CFG.Blocks[0].Nodes = append(analysis.CFG.Blocks[0].Nodes, &historicalBinder)
			attached = true
			break
		}
	}
	if !attached {
		t.Fatal("fixture has no current CFG")
	}
	nodes = map[PositionedFunctionNode]PositionedFunctionNode{}
	copied = cloneRetainedCanonicalResultRecording(result, nodes)
	if _, err := accountRetainedScopedRegionFacts(t, result, copied, nodes); err == nil {
		t.Fatal("current CFG reference incorrectly treated as historical")
	}
}
