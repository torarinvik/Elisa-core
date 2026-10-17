//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/incremental"
	"fmt"
	"strings"
	"testing"
)

func TestLookupSelectionCurrentEnvironmentMatchesResolvers(t *testing.T) {
	a := lookupTraceAnalyzer()
	a.currentUsings = []string{"Hidden.Inner", "Public"}
	a.privateModules["Hidden.Inner"] = true
	a.globalScope.Symbols["Hidden.Inner.target"] = &Symbol{Name: "Hidden.Inner.target"}
	a.globalScope.Symbols["Public.target"] = &Symbol{Name: "Public.target"}
	a.namedTypes["Hidden.Inner.target"] = &StructType{Name: "Hidden.Inner.target"}
	a.namedTypes["Public.target"] = &StructType{Name: "Public.target"}
	a.permissions = map[string]*PermissionSet{"Public.target": {Name: "Public.target"}, "Hidden.Inner.target": {Name: "Hidden.Inner.target"}}
	db := newLookupSelectionDatabase()
	for _, kind := range []string{"value", "type", "permission", "enclosing"} {
		a.privateModules["Hidden.Inner"] = true
		a.globalScope.Symbols["Hidden.Inner.target"] = &Symbol{Name: "Hidden.Inner.target"}
		a.namedTypes["Hidden.Inner.target"] = &StructType{Name: "Hidden.Inner.target"}
		delete(a.globalScope.Symbols, "Local.target")
		delete(a.currentScope.Symbols, "target")
		for step := 0; step < 4; step++ {
			if kind == "enclosing" {
				a.globalScope.Symbols["Local.target"] = &Symbol{Name: "Local.target"}
				if step%2 == 0 {
					a.currentScope.Symbols["target"] = &Symbol{Name: "target"}
				} else {
					delete(a.currentScope.Symbols, "target")
				}
			}
			key, changes, err := a.lookupSelectionInputs(kind, "target", "validated-source-site")
			if err != nil {
				t.Fatal(err)
			}
			db.Apply(changes...)
			product, err := readLookupSelection(db, key)
			if err != nil {
				t.Fatal(err)
			}
			name := ""
			found := false
			switch kind {
			case "value":
				_, name, found = a.lookupVisibleGlobal("target")
			case "type":
				_, name, found = a.lookupVisibleType("target")
			case "permission":
				_, name, found = a.lookupVisiblePermission("target")
			case "enclosing":
				_, name, found = a.enclosingNamespaceMember("target")
			}
			if kind == "value" || kind == "type" {
				want := "Public.target"
				if step == 1 {
					want = "Hidden.Inner.target"
				}
				if name != want {
					t.Fatalf("%s step%d did not exercise visibility transition: %q", kind, step, name)
				}
			}
			if kind == "permission" && name != "Hidden.Inner.target" {
				t.Fatal("permission lookup unexpectedly adopted type/value privacy policy")
			}
			if product.Name != name || product.Found != found {
				t.Fatalf("%s step%d stale query: %+v vs %q/%v", kind, step, product, name, found)
			}
			// Independently change declaration presence and module visibility.
			if step == 0 {
				a.privateModules["Hidden.Inner"] = false
			}
			if step == 1 {
				delete(a.globalScope.Symbols, "Hidden.Inner.target")
				delete(a.namedTypes, "Hidden.Inner.target")
			}
		}
	}
	// The namespace fallback comes from the actual function type, not an audit
	// owner label; it must match the resolver with an empty explicit namespace.
	a.currentNamespace = ""
	a.currentFuncType = &FuncType{Name: "Public.caller"}
	key, changes, err := a.lookupSelectionInputs("value", "target", "")
	if err != nil {
		t.Fatal(err)
	}
	db.Apply(changes...)
	product, err := readLookupSelection(db, key)
	_, name, found := a.lookupVisibleGlobal("target")
	if err != nil || product.Name != name || product.Found != found {
		t.Fatal("function namespace fallback not represented")
	}
}

func TestLookupSelectionFreshAnalyzerEditHistory(t *testing.T) {
	sources := []string{
		"module M:\n    def lookup_dependency_target() -> i64:\n        return 1\n",
		"module M:\n    def lookup_dependency_target() -> i64:\n        return 2\n",
		"def unrelated() -> i64:\n    return 0\n",
		"module M:\n    def lookup_dependency_target() -> bool:\n        return true\n",
	}
	db := newLookupSelectionDatabase()
	var keyName string
	for step, index := range []int{0, 0, 1, 2, 2, 3, 0} {
		fullSource := sources[index] + "\nusing M\ndef caller() -> i64:\n    return lookup_dependency_target()\n"
		result := Analyze(parseIncrementalSessionFile(t, fullSource))
		wantErrors := index == 2 || index == 3
		if (len(result.Errors()) != 0) != wantErrors {
			t.Fatalf("step%d current caller typing lost: %v", step, result.Errors())
		}
		a := result.analyzer
		a.withResolutionContext("", []string{"M"}, func() {
			key, changes, err := a.lookupSelectionInputs("value", "lookup_dependency_target", "")
			if err != nil {
				t.Fatal(err)
			}
			if step > 0 && key.Name != keyName {
				t.Fatal("unchanged lookup context changed identity")
			}
			keyName = key.Name
			db.Apply(incremental.Change{Key: incremental.Key{Kind: "declaration-source", Name: "project"}, Value: fullSource})
			db.Apply(changes...)
			got, err := readLookupSelection(db, key)
			_, name, found := a.lookupVisibleGlobal("lookup_dependency_target")
			if err != nil || got.Name != name || got.Found != found {
				t.Fatalf("step%d stale name selection: %+v/%v", step, got, err)
			}
		})
	}
	// Changing the selected function's type intentionally leaves name selection
	// equal. A parent body must independently track its Type/callee summary.
	if db.Stats().MemoHits == 0 || db.Stats().Validated == 0 {
		t.Fatal("fresh history did not exercise query reuse")
	}
}

func TestLookupSelectionCurrentAliasPublication(t *testing.T) {
	a := lookupTraceAnalyzer()
	a.globalScope.Symbols["Public.target"] = &Symbol{Name: "Public.target"}
	db := newLookupSelectionDatabase()
	var original string
	for step := 0; step < 2; step++ {
		key, changes, err := a.lookupSelectionInputs("value", "P.target", "")
		if err != nil {
			t.Fatal(err)
		}
		if step == 0 {
			original = key.Name
		} else if original != key.Name {
			t.Fatal("alias declaration changed request identity")
		}
		db.Apply(changes...)
		got, err := readLookupSelection(db, key)
		_, name, found := a.lookupVisibleGlobal("P.target")
		if err != nil || got.Name != name || got.Found != found || found != (step == 1) {
			t.Fatal("alias candidate generation not tracked")
		}
		a.registerModuleAlias(&ast.UsingDecl{Name: "Public", Alias: "P"}, "")
	}
}

func TestLookupQueriesBoundGeneratedImportNamesBeforeAllocation(t *testing.T) {
	a := lookupTraceAnalyzer()
	a.currentUsings = nil
	for i := 0; i < 64; i++ {
		a.currentUsings = append(a.currentUsings, fmt.Sprintf("M%d", i))
	}
	longName := strings.Repeat("x", 2048)
	if _, err := lookupSelectionKey(a.currentLookupSelectionSpec("value", longName, "")); err != nil {
		t.Fatal("fixture must fit context encoding budget")
	}
	if _, _, err := a.lookupSelectionInputs("value", longName, ""); err == nil {
		t.Fatal("generated selection name budget ignored")
	}
	if a.visibleCandidates.byName != nil {
		t.Fatal("oversized candidate table allocated before preflight")
	}
	if _, _, err := a.lookupAmbiguityInputs(longName); err == nil {
		t.Fatal("generated ambiguity name budget ignored")
	}
}
