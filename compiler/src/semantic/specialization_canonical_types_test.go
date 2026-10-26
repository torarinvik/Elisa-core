//go:build cgo

package semantic

import (
	"fmt"
	"reflect"
	"runtime"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

type canonicalMembershipPlainProbe struct {
	Position lexer.Pos
	Values   []int
}
type canonicalMembershipCycleProbe struct {
	Next    *canonicalMembershipCycleProbe
	Payload any
}
type canonicalMembershipClosedCycleProbe struct {
	Next *canonicalMembershipClosedCycleProbe
}

func newCanonicalMembershipTestScan() *specializationPublication {
	return &specializationPublication{owned: map[ast.Node]bool{}, rootSymbols: map[*Symbol]bool{}, rootScopes: map[*Scope]bool{}, seen: map[specializationPointer]uint8{}}
}

func newCanonicalMembershipIndexTestScan() *specializationPublication {
	scan := newCanonicalMembershipTestScan()
	scan.canonicalMembershipScan = true
	return scan
}

func assertCanonicalMembershipIndexesEqual(t *testing.T, current, reference *specializationPublication) {
	t.Helper()
	if current.err != nil || reference.err != nil {
		t.Fatalf("walk errors: current=%v reference=%v", current.err, reference.err)
	}
	if !reflect.DeepEqual(current.owned, reference.owned) || !reflect.DeepEqual(current.rootSymbols, reference.rootSymbols) || !reflect.DeepEqual(current.rootScopes, reference.rootScopes) {
		t.Fatal("canonical node/symbol/scope index changed")
	}
}

func assertCanonicalMembershipParity(t *testing.T, current, reference *specializationPublication) {
	t.Helper()
	assertCanonicalMembershipIndexesEqual(t, current, reference)
	// Canonical walks use owned as the visited set for AST pointers. Preserve
	// exact parity for other relevant pointer/map identities and their role bits.
	expected := map[specializationPointer]uint8{}
	for key, bits := range reference.seen {
		if canonicalMembershipMayContainIdentity(key.typ) && (!current.canonicalMembershipScan || !key.typ.Implements(astNodeType)) {
			expected[key] = bits
		}
	}
	if !reflect.DeepEqual(current.seen, expected) {
		t.Fatalf("relevant seen state changed: current=%d expected=%d", len(current.seen), len(expected))
	}
}

func TestCanonicalMembershipTypeClassifierConservativeClosure(t *testing.T) {
	for _, test := range []struct {
		name     string
		typ      reflect.Type
		relevant bool
	}{
		{"position", reflect.TypeFor[lexer.Pos](), false},
		{"position_pointer", reflect.TypeFor[*lexer.Pos](), false},
		{"plain_metadata", reflect.TypeFor[canonicalMembershipPlainProbe](), false},
		{"primitive_map", reflect.TypeFor[map[string][]int](), false},
		{"leaf_node_pointer", reflect.TypeFor[*ast.IntLit](), true},
		{"node_interface", reflect.TypeFor[ast.Node](), true},
		{"open_interface", reflect.TypeFor[any](), true},
		{"open_map", reflect.TypeFor[map[string]any](), true},
		{"symbol", reflect.TypeFor[*Symbol](), true},
		{"scope", reflect.TypeFor[*Scope](), true},
		{"nominal_type", reflect.TypeFor[*BuiltinType](), true},
		{"closed_cycle_fallback", reflect.TypeFor[*canonicalMembershipClosedCycleProbe](), true},
		{"unknown_function", reflect.TypeFor[func()](), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			for i := 0; i < 2; i++ {
				if got := canonicalMembershipMayContainIdentity(test.typ); got != test.relevant {
					t.Fatalf("relevance=%v expected=%v", got, test.relevant)
				}
			}
		})
	}
}

func TestCanonicalMembershipWalkParityOpaqueAliasesCyclesAndLeafNodes(t *testing.T) {
	leaf := &ast.IntLit{Value: "8"}
	scope := &Scope{}
	symbol := &Symbol{Node: leaf, Type: &BuiltinType{Name: "i64"}}
	cycle := &canonicalMembershipCycleProbe{}
	cycle.Next = cycle
	cycle.Payload = map[string]any{"leaf": leaf, "symbol": symbol, "scope": scope}
	plain := &canonicalMembershipPlainProbe{Values: []int{1, 2, 3}}
	values := []any{leaf, leaf, cycle, plain, lexer.Pos{}, &lexer.Pos{}, []any{symbol, symbol, scope}, map[string]any{"opaque": cycle, "nominal": &BuiltinType{Name: "i64"}}, (*ast.IntLit)(nil), [2]any{leaf, scope}}
	current, reference := newCanonicalMembershipIndexTestScan(), newCanonicalMembershipTestScan()
	for _, value := range values {
		current.walk(reflect.ValueOf(value), false)
		reference.walkCanonicalReference(reflect.ValueOf(value), false)
	}
	assertCanonicalMembershipParity(t, current, reference)
	if !current.owned[leaf] || !current.rootSymbols[symbol] || !current.rootScopes[scope] {
		t.Fatal("leaf or borrowed identity dropped")
	}
	if _, seen := current.seen[specializationPointer{reflect.TypeOf(plain), reflect.ValueOf(plain).Pointer()}]; seen {
		t.Fatal("provably AST-free pointer was not pruned")
	}
}

func TestCanonicalMembershipWalkUsesOwnedForASTAliasesAndCycles(t *testing.T) {
	leaf := &ast.IntLit{Value: "9"}
	root := &ast.BinaryExpr{Left: leaf, Right: leaf}
	call := &ast.CallExpr{Func: root, Args: []ast.Expr{leaf, root}}
	root.LoweredCall = call

	container := &canonicalMembershipCycleProbe{}
	container.Next = container
	facts := map[string]any{"node": root, "container": container}
	facts["self"] = facts
	container.Payload = facts

	current, reference := newCanonicalMembershipIndexTestScan(), newCanonicalMembershipTestScan()
	current.walk(reflect.ValueOf(container), false)
	reference.walkCanonicalReference(reflect.ValueOf(container), false)
	assertCanonicalMembershipParity(t, current, reference)

	for _, node := range []ast.Node{root, call, leaf} {
		if !current.owned[node] {
			t.Fatalf("canonical AST node %T was not indexed", node)
		}
		if _, seen := current.seen[specializationPointer{reflect.TypeOf(node), reflect.ValueOf(node).Pointer()}]; seen {
			t.Fatalf("canonical AST pointer %T redundantly entered seen", node)
		}
	}
	containerKey := specializationPointer{reflect.TypeOf(container), reflect.ValueOf(container).Pointer()}
	if _, seen := current.seen[containerKey]; !seen {
		t.Fatal("non-AST cyclic pointer lost its seen guard")
	}
	factsKey := specializationPointer{reflect.TypeOf(facts), reflect.ValueOf(facts).Pointer()}
	if current.seen[factsKey]&1 == 0 {
		t.Fatal("cyclic map lost its seen guard")
	}
}

// Deliberately mirrors the production first catalog scan without using shared
// indexes. Source analysis and catalog warming never enter its measured loop.
func canonicalMembershipCatalogTestScan(root *Result, reference bool) *specializationPublication {
	scan := newCanonicalMembershipTestScan()
	scan.canonicalMembershipScan = !reference
	walk := scan.walk
	if reference {
		walk = scan.walkCanonicalReference
	}
	rv := reflect.ValueOf(root).Elem()
	for i := 0; i < rv.NumField(); i++ {
		if rv.Type().Field(i).PkgPath == "" {
			walk(rv.Field(i), false)
		}
	}
	for _, path := range specializationASTMapPaths() {
		table := specializationField(reflect.ValueOf(root.analyzer).Elem(), path)
		if table.IsNil() {
			continue
		}
		if reference {
			// Keep the frozen traversal for exact index parity.
			it := table.MapRange()
			for it.Next() {
				walk(it.Key(), false)
				walk(it.Value(), false)
			}
		} else {
			// Candidate path: use the map-level seen guard to avoid re-reading a
			// backing table already reached through an exported Result field.
			scan.walk(table, false)
		}
	}
	if root.GlobalScope != nil {
		scan.rootScopes[root.GlobalScope] = true
		for _, symbol := range root.GlobalScope.Symbols {
			walk(reflect.ValueOf(symbol), false)
		}
	}
	return scan
}

// Current-walker baseline that retains the old manual analyzer-map traversal.
// It isolates map-pointer deduplication from the older walker/type-pruning changes.
func canonicalMembershipCatalogManualCurrentTestScan(root *Result) *specializationPublication {
	scan := newCanonicalMembershipIndexTestScan()
	rv := reflect.ValueOf(root).Elem()
	for i := 0; i < rv.NumField(); i++ {
		if rv.Type().Field(i).PkgPath == "" {
			scan.walk(rv.Field(i), false)
		}
	}
	for _, path := range specializationASTMapPaths() {
		table := specializationField(reflect.ValueOf(root.analyzer).Elem(), path)
		if table.IsNil() {
			continue
		}
		it := table.MapRange()
		for it.Next() {
			scan.walk(it.Key(), false)
			scan.walk(it.Value(), false)
		}
	}
	if root.GlobalScope != nil {
		scan.rootScopes[root.GlobalScope] = true
		for _, symbol := range root.GlobalScope.Symbols {
			scan.walk(reflect.ValueOf(symbol), false)
		}
	}
	return scan
}

func TestCanonicalMembershipFirstCatalogMatchesOldIndex(t *testing.T) {
	root, _ := privateSpecializationFixture(t)
	detached := &ast.IntLit{Value: "71"}
	root.analyzer.exprTypes[detached] = root.NamedTypes["i64"]
	current := canonicalMembershipCatalogTestScan(root, false)
	reference := canonicalMembershipCatalogTestScan(root, true)
	assertCanonicalMembershipIndexesEqual(t, current, reference)
	if len(current.queue) != 0 {
		t.Fatalf("canonical membership scan retained a private publication queue of %d nodes", len(current.queue))
	}
	if !current.owned[detached] {
		t.Fatal("analyzer-private synthesized syntax missed")
	}
	root.BeginSharedCodegen()
	defer root.EndSharedCodegen()
	production := canonicalSpecializationNodes(root)
	if !reflect.DeepEqual(root.shared.canonicalSymbols, reference.rootSymbols) || !reflect.DeepEqual(root.shared.canonicalScopes, reference.rootScopes) {
		t.Fatal("production borrowed identity indexes differ from old walker")
	}
	if !reflect.DeepEqual(production, reference.owned) {
		t.Fatal("actual production catalog differs from old index")
	}
}

func TestCanonicalMembershipCatalogDeduplicatesAliasesAndScansPrivateFactMaps(t *testing.T) {
	root, _ := privateSpecializationFixture(t)
	if root.ExprTypes == nil || root.analyzer.exprTypes == nil || reflect.ValueOf(root.ExprTypes).Pointer() != reflect.ValueOf(root.analyzer.exprTypes).Pointer() {
		t.Fatal("fixture did not retain the public expression type map alias")
	}
	publicExpr := &ast.IntLit{Value: "72"}
	root.analyzer.exprTypes[publicExpr] = root.NamedTypes["i64"]

	privateKey := &ast.IndexExpr{}
	privateFact := &ast.IntLit{Value: "73"}
	root.analyzer.indexBoundsProofOrigins = map[*ast.IndexExpr]ast.Node{privateKey: privateFact}

	privateDecl := &ast.FuncDecl{}
	privateSymbol := &Symbol{Name: "private_catalog_symbol", Node: privateDecl}
	if root.analyzer.funcDeclSymbols == nil {
		root.analyzer.funcDeclSymbols = map[*ast.FuncDecl]*Symbol{}
	}
	root.analyzer.funcDeclSymbols[privateDecl] = privateSymbol

	current := canonicalMembershipCatalogTestScan(root, false)
	reference := canonicalMembershipCatalogTestScan(root, true)
	assertCanonicalMembershipIndexesEqual(t, current, reference)
	for _, node := range []ast.Node{publicExpr, privateKey, privateFact, privateDecl} {
		if !current.owned[node] {
			t.Fatalf("canonical AST node %T was not indexed", node)
		}
	}
	if !current.rootSymbols[privateSymbol] {
		t.Fatal("private analyzer fact symbol identity was not indexed")
	}
	if root.GlobalScope == nil || !current.rootScopes[root.GlobalScope] {
		t.Fatal("canonical global scope identity was not indexed")
	}

	publicMap := reflect.ValueOf(root.ExprTypes)
	publicMapKey := specializationPointer{publicMap.Type(), publicMap.Pointer()}
	if current.seen[publicMapKey]&1 == 0 {
		t.Fatal("public aliased map lost its map-level seen entry")
	}
	privateMap := reflect.ValueOf(root.analyzer.indexBoundsProofOrigins)
	privateMapKey := specializationPointer{privateMap.Type(), privateMap.Pointer()}
	if current.seen[privateMapKey]&1 == 0 {
		t.Fatal("distinct private fact map was not traversed through the map-level walker")
	}
}

func TestCanonicalMembershipMapSeenRetainsDistinctSpanRole(t *testing.T) {
	index := &ast.IndexExpr{}
	facts := map[*ast.IndexExpr]ast.Node{index: index}
	scan := newCanonicalMembershipIndexTestScan()
	scan.spanNodes = map[*ast.IndexExpr]bool{}
	value := reflect.ValueOf(facts)
	scan.walk(value, false)
	scan.walk(value, true)
	scan.walk(value, false)

	key := specializationPointer{value.Type(), value.Pointer()}
	if got := scan.seen[key]; got != 3 {
		t.Fatalf("map seen roles=%b, want both ordinary and span roles", got)
	}
	if !scan.spanNodes[index] {
		t.Fatal("span-role revisit did not retain the index expression")
	}
}

func TestCanonicalMembershipPruningDoesNotSkipPrivateValidation(t *testing.T) {
	private := &specializationPublication{context: &specializationContext{}}
	private.walk(reflect.ValueOf(canonicalMembershipPlainProbe{}), false)
	if private.err == nil {
		t.Fatal("AST-free unknown private fact bypassed closed schema validation")
	}
}

var canonicalMembershipBenchmarkCatalog *specializationPublication

func BenchmarkCanonicalSpecializationFirstCatalog(b *testing.B) {
	for _, size := range []int{0, 512} {
		b.Run(fmt.Sprintf("unrelated_%d", size), func(b *testing.B) {
			root, _, _ := specializationBenchmarkAnalyze(b, specializationBenchmarkSource(size))
			if root.shared != nil {
				b.Fatal("fixture unexpectedly warmed shared catalog")
			}
			for _, mode := range []string{"reference", "pruned_warm_types", "pruned_cold_types"} {
				b.Run(mode, func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if mode == "pruned_cold_types" {
							canonicalMembershipTypeCache.Clear()
						}
						canonicalMembershipBenchmarkCatalog = canonicalMembershipCatalogTestScan(root, mode == "reference")
					}
					runtime.KeepAlive(root)
				})
			}
		})
	}
}

func BenchmarkCanonicalSpecializationAliasedPublicAndPrivateFactMaps(b *testing.B) {
	for _, rows := range []int{4096, 16384} {
		b.Run(fmt.Sprintf("rows_%d", rows), func(b *testing.B) {
			root, _, _ := specializationBenchmarkAnalyze(b, specializationBenchmarkSource(512))
			if root.shared != nil || root.ExprTypes == nil || root.analyzer.exprTypes == nil || reflect.ValueOf(root.ExprTypes).Pointer() != reflect.ValueOf(root.analyzer.exprTypes).Pointer() {
				b.Fatal("fixture did not retain the aliased public expression type map")
			}
			for i := 0; i < rows; i++ {
				root.analyzer.exprTypes[&ast.IntLit{Value: "74"}] = root.NamedTypes["i64"]
			}
			privateFacts := make(map[*ast.IndexExpr]ast.Node, rows)
			for i := 0; i < rows; i++ {
				privateFacts[&ast.IndexExpr{}] = &ast.IntLit{Value: "75"}
			}
			root.analyzer.indexBoundsProofOrigins = privateFacts

			for _, manualEntries := range []bool{false, true} {
				name := "candidate_map_seen"
				if manualEntries {
					name = "manual_current_entries"
				}
				b.Run(name, func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if manualEntries {
							canonicalMembershipBenchmarkCatalog = canonicalMembershipCatalogManualCurrentTestScan(root)
						} else {
							canonicalMembershipBenchmarkCatalog = canonicalMembershipCatalogTestScan(root, false)
						}
					}
					runtime.KeepAlive(root)
				})
			}
		})
	}
}

// Frozen pre-cut walker for exact index comparison.
func (p *specializationPublication) walkCanonicalReference(v reflect.Value, span bool) {
	if !v.IsValid() || p.err != nil {
		return
	}
	if p.context != nil {
		if err := validateSparseFactValueType(v); err != nil {
			p.err = err
			return
		}
	}
	if v.CanInterface() {
		switch value := v.Interface().(type) {
		case *Symbol:
			if value == nil {
				return
			}
			key := specializationPointer{v.Type(), reflect.ValueOf(value).Pointer()}
			if p.seen[key]&4 != 0 {
				return
			}
			p.seen[key] |= 4
			if p.context == nil {
				p.rootSymbols[value] = true
			} else if p.rootSymbols[value] {
				return
			}
			p.walkCanonicalReference(reflect.ValueOf(value.Node), false)
			p.walkCanonicalReference(reflect.ValueOf(value.Type), false)
			p.walkCanonicalReference(reflect.ValueOf(value.AliasOf), false)
			return
		case *Scope:
			if value == nil {
				return
			}
			if p.context == nil {
				p.rootScopes[value] = true
				return
			}
			if !p.rootScopes[value] {
				p.err = fmt.Errorf("specialization fact retains a private semantic scope")
			}
			return
		case *StructType, *EnumType, *ConstEnumType, *OpaqueType, *BitGroupType, *BuiltinType, *BitIntType, *TypeParamType, *ErrorSetType:
			return
		case ast.Node:
			if reflect.ValueOf(value).Kind() == reflect.Pointer && reflect.ValueOf(value).IsNil() {
				return
			}
			if p.rootNodes != nil && p.rootNodes[value] {
				return
			}
			if decl, ok := value.(ast.Decl); ok && p.rootNodes != nil && decl != p.target {
				// An unrelated declaration is a canonical symbol reference;
				// it is not part of this function's published body closure.
				return
			}
			if !p.owned[value] {
				p.owned[value] = true
				p.queue = append(p.queue, value)
			}
			if index, ok := value.(*ast.IndexExpr); ok && span {
				p.spanNodes[index] = true
			}
		case Type:
			// Structural type wrappers can carry private default/contract AST.
			// They are kept, but those templates alone are not emitted accesses.
			span = false
		}
	}
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			p.walkCanonicalReference(v.Elem(), span)
		}
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		key := specializationPointer{v.Type(), v.Pointer()}
		role := uint8(1)
		if span {
			role = 2
		}
		if p.seen[key]&role != 0 {
			return
		}
		p.seen[key] |= role
		p.walkCanonicalReference(settable(v.Elem()), span)
	case reflect.Struct:
		addressable := reflect.New(v.Type()).Elem()
		addressable.Set(v)
		for i := 0; i < v.NumField(); i++ {
			p.walkCanonicalReference(settable(addressable.Field(i)), span)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			p.walkCanonicalReference(v.Index(i), span)
		}
	case reflect.Map:
		if v.IsNil() {
			return
		}
		key := specializationPointer{v.Type(), v.Pointer()}
		role := uint8(1)
		if span {
			role = 2
		}
		if p.seen[key]&role != 0 {
			return
		}
		p.seen[key] |= role
		it := v.MapRange()
		for it.Next() {
			p.walkCanonicalReference(it.Key(), span)
			p.walkCanonicalReference(it.Value(), span)
		}
	}
}
