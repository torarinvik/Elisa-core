//go:build cgo

package semantic

import (
	"context"
	"elisacore/src/incremental"
	"encoding/json"
	"strings"
	"testing"
)

func lexicalTestSpec(domain string) lexicalSelectionSpec {
	return lexicalSelectionSpec{Site: FunctionSourceAnchor{FunctionKey: "fixture", Tree: [32]byte{1}, Kind: "use"}, Role: "read", Domain: domain, Name: "x"}
}

func TestLexicalTrackedNegativePrefixAndNilPolicies(t *testing.T) {
	for _, domain := range []string{"symbol", "local-symbol", "type-param", "const-param", "shape-param", "assoc-type", "param-interface", "permission-param", "error-set-param", "region-param"} {
		t.Run(domain, func(t *testing.T) {
			db := incremental.New(lexicalQueryEvaluators())
			spec := lexicalTestSpec(domain)
			frames := []lexicalFrameValue{{}, {Present: true, Binding: "outer"}, {Present: true, Binding: "unread"}}
			publish := func() incremental.Key {
				t.Helper()
				p, err := lexicalPublication(spec, frames)
				if err != nil {
					t.Fatal(err)
				}
				db.Apply(p.Inputs...)
				return p.Key
			}
			key := publish()
			read := func(want lexicalSelectionProduct) {
				t.Helper()
				encoded, err := db.Evaluate(context.Background(), key)
				if err != nil {
					t.Fatal(err)
				}
				var got lexicalSelectionProduct
				if err = json.Unmarshal([]byte(encoded), &got); err != nil || got != want {
					t.Fatalf("got %+v, want %+v: %v", got, want, err)
				}
			}
			read(lexicalSelectionProduct{Binding: "outer", Present: true, Found: true})
			before := db.Stats().Executed
			frames[2].Binding = "changed unread suffix"
			publish()
			read(lexicalSelectionProduct{Binding: "outer", Present: true, Found: true})
			if db.Stats().Executed != before {
				t.Fatal("unread suffix invalidated selection")
			}
			frames[0] = lexicalFrameValue{Present: true, Binding: "inner"}
			publish()
			read(lexicalSelectionProduct{Binding: "inner", Present: true, Found: true})
			if db.Stats().Executed == before {
				t.Fatal("negative prefix was not tracked")
			}
			frames[0] = lexicalFrameValue{Present: true, Nil: true}
			if strings.HasSuffix(domain, "permission-param") || domain == "error-set-param" || domain == "region-param" {
				if _, err := lexicalPublication(spec, frames); err == nil {
					t.Fatal("nil boolean binder accepted")
				}
			} else {
				publish()
				read(lexicalSelectionProduct{Present: true, Nil: true, Found: domain != "param-interface"})
			}
			frames = []lexicalFrameValue{{}, {}, {}}
			publish()
			read(lexicalSelectionProduct{})
			frames[2] = lexicalFrameValue{Present: true, Binding: "new outer"}
			publish()
			read(lexicalSelectionProduct{Binding: "new outer", Present: true, Found: true})
		})
	}
}

func TestLexicalCombinedRevisionRebindsCurrentValues(t *testing.T) {
	ctx := context.Background()
	session, err := incremental.NewSession(lexicalQueryEvaluators(), incremental.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	makeSnapshot := func(prefix string) (lexicalSnapshot, *Symbol, incremental.Key, incremental.Key) {
		t.Helper()
		fn := parseProductFunction(t, prefix+"def work(value: i64) -> i64:\n    return value\n")
		index, err := IndexFunctionSource(fn, productContext())
		if err != nil {
			t.Fatal(err)
		}
		global := NewScope(nil)
		scope := NewScope(global)
		symbol := &Symbol{Name: "value", Kind: SymbolParam, Node: fn, ParamIndex: 0}
		scope.Symbols["value"] = symbol
		a := &Analyzer{permissionParamScopes: []map[string]bool{{"P": true}, {"P": false}}}
		symbols := []lexicalSymbolRequest{{Index: index, Site: fn.Body[0], Role: "read", Name: "value", Current: scope, Global: global}}
		binders := []lexicalBinderRequest{{Index: index, Site: fn.Body[0], Role: "permission", Domain: "permission-param", Name: "P", Sources: []lexicalBinderSource{{index, fn, "outer"}, {index, fn, "inner"}}}}
		snapshot, skipped, err := publishLexicalSnapshot(ctx, session, a, symbols, binders)
		if err != nil || len(skipped) != 0 {
			t.Fatalf("publication: %v %v", err, skipped)
		}
		var sk, bk incremental.Key
		for k := range snapshot.symbols.requests {
			sk = k
		}
		for k := range snapshot.binders.requests {
			bk = k
		}
		got, found, err := snapshot.symbols.lookup(ctx, sk)
		if err != nil || !found || got != symbol {
			t.Fatalf("symbol: %v %v %v", got, found, err)
		}
		value, found, err := snapshot.binders.lookup(ctx, bk)
		if err != nil || !found || value != true {
			t.Fatalf("boolean false should pass through: %v %v %v", value, found, err)
		}
		return snapshot, symbol, sk, bk
	}
	old, oldSymbol, sk, _ := makeSnapshot("")
	executed := session.Stats().Queries.Executed
	fresh, freshSymbol, freshKey, _ := makeSnapshot("\n\n")
	if oldSymbol == freshSymbol || sk != freshKey {
		t.Fatal("fixture failed to relocate same source identity")
	}
	if session.Stats().Queries.Executed != executed {
		t.Fatal("equal portable selection should reuse memo")
	}
	got, _, err := fresh.symbols.lookup(ctx, freshKey)
	if err != nil || got != freshSymbol {
		t.Fatal("stale symbol retained")
	}
	if _, _, err := old.symbols.lookup(ctx, sk); err == nil {
		t.Fatal("old revision remained live")
	}
}

func TestLexicalAnchorBatchSealsAndFailsClosed(t *testing.T) {
	source := "def work(value: i64) -> i64:\n" + strings.Repeat("    value = value + 1\n", 256) + "    return value\n"
	fn := parseProductFunction(t, source)
	index, err := IndexFunctionSource(fn, productContext())
	if err != nil {
		t.Fatal(err)
	}
	batch := &lexicalAnchorBatch{}
	for _, stmt := range fn.Body {
		if _, err := batch.add(index, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.validate(); err != nil {
		t.Fatal(err)
	}
	if batch.scans != 1 {
		t.Fatalf("scanned %d times", batch.scans)
	}
	if _, err := batch.add(index, fn); err == nil {
		t.Fatal("sealed batch admitted unvalidated source")
	}
	removed := fn.Body[0]
	fn.Body = fn.Body[1:]
	if err := batch.validate(); err == nil {
		t.Fatal("removed source accepted")
	}
	if _, err := batch.anchor(index, removed); err == nil {
		t.Fatal("failed validation retained authority")
	}
}

func TestLexicalSymbolSnapshotTracksScopeMutations(t *testing.T) {
	ctx := context.Background()
	session, err := incremental.NewSession(lexicalQueryEvaluators(), incremental.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	fn := parseProductFunction(t, "def work(value: i64) -> i64:\n    return value\n")
	index, err := IndexFunctionSource(fn, productContext())
	if err != nil {
		t.Fatal(err)
	}
	global := NewScope(nil)
	outer := NewScope(global)
	inner := NewScope(outer)
	first := &Symbol{Name: "x"}
	global.Symbols["x"] = first
	lookup := func(local bool, want *Symbol, found bool) {
		t.Helper()
		snapshot, _, err := publishLexicalSnapshot(ctx, session, nil, []lexicalSymbolRequest{{Index: index, Site: fn.Body[0], Role: "read", Name: "x", Current: inner, Global: global, LocalOnly: local}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for key := range snapshot.symbols.requests {
			got, ok, err := snapshot.symbols.lookup(ctx, key)
			if err != nil || got != want || ok != found {
				t.Fatalf("got %p %v %v, want %p %v", got, ok, err, want, found)
			}
		}
	}
	lookup(false, first, true)
	lookup(true, nil, false)
	// Present nil stops lookup, even with a nonnil outer global slot.
	inner.Symbols["x"] = nil
	lookup(false, nil, true)
	delete(inner.Symbols, "x")
	lookup(false, first, true)
	// The portable global slot stays equal while its current symbol changes.
	second := &Symbol{Name: "x"}
	global.Symbols["x"] = second
	lookup(false, second, true)
	delete(global.Symbols, "x")
	lookup(false, nil, false)
	inner.Parent = inner
	if _, _, err := publishLexicalSnapshot(ctx, session, nil, []lexicalSymbolRequest{{Index: index, Site: fn.Body[0], Role: "read", Name: "x", Current: inner}}, nil); err == nil {
		t.Fatal("cyclic scope accepted")
	}
}

func TestLexicalBinderNilAdaptersAndOwnerValidation(t *testing.T) {
	ctx := context.Background()
	session, err := incremental.NewSession(lexicalQueryEvaluators(), incremental.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	fn := parseProductFunction(t, "def work(value: i64) -> i64:\n    return value\n")
	index, err := IndexFunctionSource(fn, productContext())
	if err != nil {
		t.Fatal(err)
	}
	a := &Analyzer{typeParamScopes: []map[string]Type{{"T": nil}}, typeParamInterfaceScopes: []map[string]*StaticInterface{{"T": nil}}}
	for _, domain := range []string{"type-param", "param-interface"} {
		request := lexicalBinderRequest{Index: index, Site: fn.Body[0], Role: "read", Name: "T", Domain: domain, Sources: []lexicalBinderSource{{index, fn, "generic"}}}
		snapshot, _, err := publishLexicalSnapshot(ctx, session, a, nil, []lexicalBinderRequest{request})
		if err != nil {
			t.Fatal(err)
		}
		for key := range snapshot.binders.requests {
			value, found, err := snapshot.binders.lookup(ctx, key)
			if err != nil || value != nil || found != (domain == "type-param") {
				t.Fatalf("nil policy: %v %v %v", value, found, err)
			}
		}
		request.Sources = nil
		if _, _, err := publishLexicalSnapshot(ctx, session, a, nil, []lexicalBinderRequest{request}); err == nil {
			t.Fatal("missing source frame accepted")
		}
		foreign := parseProductFunction(t, "def other() -> i64:\n    return 1\n")
		request.Sources = []lexicalBinderSource{{index, foreign, "generic"}}
		if _, _, err := publishLexicalSnapshot(ctx, session, a, nil, []lexicalBinderRequest{request}); err == nil {
			t.Fatal("foreign owner accepted")
		}
	}
}

func TestLexicalSourceLocalShadowInsertionAndRemoval(t *testing.T) {
	ctx := context.Background()
	session, err := incremental.NewSession(lexicalQueryEvaluators(), incremental.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	fn := parseProductFunction(t, "def work(value: i64) -> i64:\n    x = value\n    return x\n")
	index, err := IndexFunctionSource(fn, productContext())
	if err != nil {
		t.Fatal(err)
	}
	global := NewScope(nil)
	scope := NewScope(global)
	outer := &Symbol{Name: "x"}
	global.Symbols["x"] = outer
	read := func(site PositionedFunctionNode, want *Symbol) incremental.Key {
		t.Helper()
		snapshot, _, err := publishLexicalSnapshot(ctx, session, nil, []lexicalSymbolRequest{{Index: index, Site: site, Role: "read", Name: "x", Current: scope, Global: global}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for key := range snapshot.symbols.requests {
			got, found, err := snapshot.symbols.lookup(ctx, key)
			if err != nil || !found || got != want {
				t.Fatalf("local lookup: %p %v %v, want %p", got, found, err, want)
			}
			return key
		}
		t.Fatal("missing query")
		return incremental.Key{}
	}
	before := read(fn.Body[0], outer)
	local := &Symbol{Name: "x", Kind: SymbolLocal, Node: fn.Body[0]}
	scope.Define(local)
	after := read(fn.Body[1], local)
	if before == after {
		t.Fatal("distinct use sites collided")
	}
	delete(scope.Symbols, "x")
	read(fn.Body[1], outer)
	scope.Define(local)
	read(fn.Body[1], local)
	// Replacing the current Symbol does not turn the source slot into a pointer cache.
	replacement := &Symbol{Name: "x", Kind: SymbolLocal, Node: fn.Body[0]}
	scope.Symbols["x"] = replacement
	read(fn.Body[1], replacement)
}
