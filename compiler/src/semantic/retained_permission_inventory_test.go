package semantic

import (
	"fmt"
	"reflect"
	"testing"

	"elisacore/src/ast"
)

// Unchanged fb341977 implementation: independent row/restoration oracle.
func oldPermissionSummaryCaller(p *retainedPermissions, a *Analyzer, decls []scopedDecl, fn *ast.FuncDecl, body func()) bool {
	functions := permissionFunctionDecls(decls)
	current := -1
	for i, sd := range functions {
		if sd.Decl == fn {
			current = i
			break
		}
	}
	if current < 0 {
		return false
	}
	currentType := permissionDeclType(a, functions[current])
	solved := map[*FuncType]permissionRowSnapshot{}
	for i, sd := range functions {
		ft := permissionDeclType(a, sd)
		if ft == nil {
			continue
		}
		decl, _ := sd.Decl.(*ast.FuncDecl)
		if i < current && (p.bodyEdited[decl] || p.summaryReanalyzed[decl]) {
			continue // this body has already run in the current cold-order replay
		}
		// In a cold source-order body pass, fn and later functions still expose
		// declaration-phase rows. p.initial is the previous solve's starting
		// snapshot and can include effects inferred by bodies that ran later in
		// that previous pass, so prefer the fresh declaration snapshot here.
		start, known := p.declared[ft]
		if !known {
			start, known = p.initial[ft]
		}
		if !known || start.identical(ft) {
			continue
		}
		solved[ft] = snapshotPermissionRow(ft)
		start.restore(ft)
	}
	body()
	for ft, row := range solved {
		if ft != currentType {
			row.restore(ft)
		}
	}
	return true
}

func inventoryTestRow(label string) permissionRowSnapshot {
	return permissionRowSnapshot{
		refs:           []ast.PermissionRef{{Name: label + ".refs"}},
		mutable:        []ast.PermissionRef{{Name: label + ".mutable"}},
		returned:       []ast.PermissionRef{{Name: label + ".returned"}},
		returnedGlobal: []ast.PermissionRef{{Name: label + ".returnedGlobal"}},
		families:       []string{label + ".families"},
	}
}

func TestPermissionInventoryPreservesOrderedScopedMembership(t *testing.T) {
	first, method, last := &ast.FuncDecl{Name: "first"}, &ast.FuncDecl{Name: "method"}, &ast.FuncDecl{Name: "last"}
	var nilFunction *ast.FuncDecl
	usings := []string{"A", "B"}
	declarations := []scopedDecl{
		{Decl: first, Namespace: "Outer", Usings: usings, Private: true},
		{Decl: &ast.ImplDecl{Members: []ast.ImplMember{method, &ast.ImplAssociatedTypeDecl{}, first}}, Namespace: "Impl", Usings: usings},
		{Decl: nilFunction}, {Decl: &ast.GlobalDecl{}}, {Decl: last}, {Decl: first},
	}
	inventory := newRetainedPermissionInventory(declarations)
	expected := permissionFunctionDecls(declarations)
	if !reflect.DeepEqual(expected, inventory.functions) {
		t.Fatal("executable inventory/scope/source order changed")
	}
	for i, scoped := range expected {
		fn := scoped.Decl.(*ast.FuncDecl)
		ordinal := inventory.first[fn]
		if ordinal > i || expected[ordinal].Decl != fn {
			t.Fatal("index did not preserve first duplicate occurrence")
		}
	}
	if len(inventory.functions) != 6 || inventory.first[first] != 0 || inventory.first[nilFunction] != 3 {
		t.Fatal("duplicate/nil inventory entry lost")
	}
	// Inventory owns the executable list, not the caller's top-level backing array.
	declarations[0].Decl = last
	if inventory.functions[0].Decl != first {
		t.Fatal("top-level list replacement mutated immutable inventory")
	}
}

func TestPermissionInventoryExactOldNestedReplayAndDynamicMasks(t *testing.T) {
	funcs := make([]*ast.FuncDecl, 6)
	types := make([]*FuncType, 5)
	for i := range funcs {
		funcs[i] = &ast.FuncDecl{Name: fmt.Sprintf("fn%d", i)}
	}
	for i := range types {
		types[i] = &FuncType{Name: fmt.Sprintf("type%d", i)}
	}
	// Duplicate declaration, shared FuncType, missing symbol and nil type all stay
	// in the complete scan. Rebinding a live type after inventory construction
	// proves that membership reuse does not cache permissionDeclType results.
	declarations := []scopedDecl{{Decl: funcs[0]}, {Decl: funcs[1]}, {Decl: funcs[2]}, {Decl: funcs[1]}, {Decl: funcs[3]}, {Decl: funcs[4]}, {Decl: funcs[5]}}
	a := &Analyzer{funcDeclSymbols: map[*ast.FuncDecl]*Symbol{}}
	bindings := map[*ast.FuncDecl]*FuncType{funcs[0]: types[0], funcs[1]: types[1], funcs[2]: types[1], funcs[3]: types[2], funcs[5]: nil}
	initial, declared, live := map[*FuncType]permissionRowSnapshot{}, map[*FuncType]permissionRowSnapshot{}, map[*FuncType]permissionRowSnapshot{}
	writes := make([]permissionRowSnapshot, 8)
	for i, ft := range types {
		initial[ft] = inventoryTestRow(fmt.Sprintf("initial%d", i))
		declared[ft] = inventoryTestRow(fmt.Sprintf("declared%d", i))
		live[ft] = inventoryTestRow(fmt.Sprintf("live%d", i))
	}
	for i := range writes {
		writes[i] = inventoryTestRow(fmt.Sprintf("write%d", i))
	}
	inventory := newRetainedPermissionInventory(declarations)
	run := func(cached bool) []permissionRowSnapshot {
		p := &retainedPermissions{initial: initial, declared: declared, bodyEdited: map[*ast.FuncDecl]bool{}, summaryReanalyzed: map[*ast.FuncDecl]bool{}}
		a.funcDeclSymbols = map[*ast.FuncDecl]*Symbol{}
		for fn, ft := range bindings {
			a.funcDeclSymbols[fn] = &Symbol{Type: ft}
		}
		for ft, row := range live {
			row.restore(ft)
		}
		// fn3 now refers to a different live type; its original type still exists.
		a.funcDeclSymbols[funcs[3]].Type = types[3]
		var trace []permissionRowSnapshot
		observe := func() {
			for _, ft := range types {
				trace = append(trace, snapshotPermissionRow(ft))
			}
		}
		call := func(fn *ast.FuncDecl, body func()) bool {
			if cached {
				return p.withInitialRowsForSummaryCallerUsingInventory(a, declarations, inventory, fn, body)
			}
			return oldPermissionSummaryCaller(p, a, declarations, fn, body)
		}
		p.bodyEdited[funcs[0]] = true
		for round := 0; round < 3; round++ {
			p.summaryReanalyzed[funcs[1]] = true
			if !call(funcs[1], func() {
				observe()
				writes[round].restore(types[1])
				p.summaryReanalyzed[funcs[3]] = true
				if !call(funcs[3], func() { observe(); writes[round+3].restore(types[3]) }) {
					t.Fatal("nested caller missing")
				}
				observe()
			}) {
				t.Fatal("outer caller missing")
			}
			observe()
			// Masks are intentionally changed between rounds; they cannot be frozen in
			// the ordered inventory or treated as an immutable caller subset.
			delete(p.bodyEdited, funcs[0])
			p.summaryReanalyzed[funcs[0]] = round%2 == 0
		}
		ran := false
		if call(&ast.FuncDecl{Name: "absent"}, func() { ran = true }) || ran {
			t.Fatal("absent caller must request general fallback")
		}
		// A caller absent from the ordered inventory must still use the exact
		// general outer restoration policy, rather than silently skipping body.
		p.edited = map[*ast.FuncDecl]bool{funcs[1]: true}
		var context *retainedPermissionInventory
		if cached {
			context = inventory
		}
		p.withInitialRowsUsingInventory(a, declarations, context, func() { observe(); writes[7].restore(types[1]); writes[6].restore(types[3]) }, &ast.FuncDecl{Name: "absent"})
		observe()
		// Empty initial inventory compatibility is an unconditional body call.
		empty := &retainedPermissions{}
		empty.withInitialRowsUsingInventory(a, declarations, inventory, func() { ran = true }, funcs[1])
		if !ran {
			t.Fatal("nil initial map skipped body")
		}
		return trace
	}
	old, cached := run(false), run(true)
	if len(old) != len(cached) {
		t.Fatal("row observation count changed")
	}
	for i := range old {
		ft := &FuncType{}
		cached[i].restore(ft)
		if !old[i].identical(ft) || !reflect.DeepEqual(old[i], cached[i]) {
			t.Fatalf("full slice identity/value restoration differs at observation %d", i)
		}
	}
}
