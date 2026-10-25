package semantic

import (
	"fmt"
	"reflect"
	"runtime"
	"testing"

	"elisacore/src/ast"
)

// Verbatim 695c49fb row-scope bodies with only entry-point names redirected.
func priorPermissionSnapshotOuter(p *retainedPermissions, a *Analyzer, decls []scopedDecl, inventory *retainedPermissionInventory, body func(), preserveSummaryReanalyzed ...*ast.FuncDecl) {
	if p.initial == nil {
		body()
		return
	}
	if len(preserveSummaryReanalyzed) != 0 && priorPermissionSnapshotCaller(p, a, decls, inventory, preserveSummaryReanalyzed[0], body) {
		return
	}
	editedTypes := map[*FuncType]bool{}
	for fn := range p.edited {
		sym := a.funcDeclSymbols[fn]
		if sym == nil {
			continue
		}
		ft, _ := sym.Type.(*FuncType)
		if ft == nil {
			continue
		}
		editedTypes[ft] = true
		// An edited function keeps its type when its signature did not change, and with
		// it the solved row a recursive call would read; its body starts from its
		// declared row, as in a fresh analysis. The body then writes its own row.
		if start, known := p.declared[ft]; known {
			start.restore(ft)
		} else if p.declared != nil {
			p.declared[ft] = snapshotPermissionRow(ft) // a new type: fresh from this update's declaration phase
		}
	}
	// Every other row reads as its initial one; most already are, so only rows the
	// solve moved are swapped.
	solved := map[*FuncType]permissionRowSnapshot{}
	for ft, start := range p.initial {
		if editedTypes[ft] || start.identical(ft) {
			continue
		}
		solved[ft] = snapshotPermissionRow(ft)
		start.restore(ft)
	}
	body()
	// Summary-dependent callers may be enrolled while body() is running. The outer
	// update scope keeps all of them; a nested caller scope keeps only the caller it
	// just analyzed, restoring other rows to the state they had before that scope.
	keepSummaryReanalyzed := make(map[*ast.FuncDecl]bool, len(p.summaryReanalyzed))
	if len(preserveSummaryReanalyzed) == 0 {
		for fn := range p.summaryReanalyzed {
			keepSummaryReanalyzed[fn] = true
		}
	} else {
		for _, fn := range preserveSummaryReanalyzed {
			keepSummaryReanalyzed[fn] = true
		}
	}
	for fn := range keepSummaryReanalyzed {
		sym := a.funcDeclSymbols[fn]
		if sym == nil {
			continue
		}
		ft, _ := sym.Type.(*FuncType)
		if ft != nil {
			editedTypes[ft] = true
		}
	}
	for ft, row := range solved {
		if !editedTypes[ft] {
			row.restore(ft)
		}
	}
}

func priorPermissionSnapshotCaller(p *retainedPermissions, a *Analyzer, decls []scopedDecl, inventory *retainedPermissionInventory, fn *ast.FuncDecl, body func()) bool {
	if inventory == nil {
		inventory = newRetainedPermissionInventory(decls)
	}
	functions := inventory.functions
	current, present := inventory.first[fn]
	if !present {
		current = -1
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

func TestPermissionSnapshotFramesExactPriorNestedAndExceptionalPaths(t *testing.T) {
	for _, scenario := range []string{"normal", "nested_panic", "outer_panic", "goexit"} {
		t.Run(scenario, func(t *testing.T) {
			funcs := make([]*ast.FuncDecl, 5)
			types := make([]*FuncType, 4)
			for i := range funcs {
				funcs[i] = &ast.FuncDecl{Name: fmt.Sprintf("fn%d", i)}
			}
			for i := range types {
				types[i] = &FuncType{Name: fmt.Sprintf("type%d", i)}
			}
			declarations := []scopedDecl{{Decl: funcs[0]}, {Decl: funcs[1]}, {Decl: funcs[1]}, {Decl: funcs[2]}, {Decl: funcs[3]}, {Decl: funcs[4]}}
			a := &Analyzer{funcDeclSymbols: map[*ast.FuncDecl]*Symbol{funcs[0]: {Type: types[0]}, funcs[1]: {Type: types[1]}, funcs[2]: {Type: types[2]}, funcs[3]: {Type: types[1]}}}
			initial, declared, live := map[*FuncType]permissionRowSnapshot{}, map[*FuncType]permissionRowSnapshot{}, map[*FuncType]permissionRowSnapshot{}
			for i, ft := range types {
				initial[ft] = inventoryTestRow(fmt.Sprintf("initial%d", i))
				declared[ft] = inventoryTestRow(fmt.Sprintf("declared%d", i))
				live[ft] = inventoryTestRow(fmt.Sprintf("live%d", i))
			}
			writes := make([]permissionRowSnapshot, 6)
			for i := range writes {
				writes[i] = inventoryTestRow(fmt.Sprintf("write%d", i))
			}
			// Include nil and nonnil-empty headers with spare capacity. Their
			// contents compare equal at length zero but their shallow headers
			// remain distinguishable and must survive save/restore exactly.
			writes[3].mutable = make([]ast.PermissionRef, 0, 5)
			writes[4].families = make([]string, 0, 9)
			writes[4].returned = nil
			panicToken := new(int)
			run := func(buffered bool) []permissionRowSnapshot {
				for ft, row := range live {
					row.restore(ft)
				}
				p := &retainedPermissions{initial: initial, declared: declared, edited: map[*ast.FuncDecl]bool{funcs[0]: true}, bodyEdited: map[*ast.FuncDecl]bool{funcs[0]: true}, summaryReanalyzed: map[*ast.FuncDecl]bool{}}
				inventory := newRetainedPermissionInventory(declarations)
				var trace []permissionRowSnapshot
				observe := func() {
					for _, ft := range types {
						trace = append(trace, snapshotPermissionRow(ft))
					}
				}
				outer := func(body func()) {
					if buffered {
						p.withInitialRowsUsingInventory(a, declarations, inventory, body)
					} else {
						priorPermissionSnapshotOuter(p, a, declarations, inventory, body)
					}
				}
				caller := func(fn *ast.FuncDecl, body func()) {
					if buffered {
						p.withInitialRowsForSummaryCallerUsingInventory(a, declarations, inventory, fn, body)
					} else {
						priorPermissionSnapshotCaller(p, a, declarations, inventory, fn, body)
					}
				}
				caught := func(body func()) {
					defer func() {
						if value := recover(); value != nil && value != panicToken {
							panic(value)
						}
					}()
					body()
				}
				body := func() {
					observe()
					writes[0].restore(types[0])
					p.summaryReanalyzed[funcs[1]] = true
					caught(func() {
						caller(funcs[1], func() {
							observe()
							writes[1].restore(types[1])
							p.summaryReanalyzed[funcs[2]] = true
							caller(funcs[2], func() {
								observe()
								writes[2].restore(types[2])
								if scenario == "nested_panic" {
									panic(panicToken)
								}
								if scenario == "goexit" {
									runtime.Goexit()
								}
							})
							observe()
						})
					})
					observe()
					// Reuse the released nested depth after either normal unwinding or a
					// recovered two-frame panic. An outer saved map must remain untouched.
					delete(p.bodyEdited, funcs[0])
					p.summaryReanalyzed[funcs[0]] = true
					caller(funcs[2], func() { observe(); writes[3].restore(types[2]); writes[4].restore(types[1]) })
					observe()
					if scenario == "outer_panic" {
						panic(panicToken)
					}
				}
				if scenario == "goexit" {
					done := make(chan struct{})
					go func() { defer close(done); outer(body) }()
					<-done
				} else {
					caught(func() { outer(body) })
				}
				observe()
				if buffered {
					if inventory.snapshotDepth != 0 {
						t.Fatal("exception left active snapshot depth")
					}
					for _, frame := range inventory.snapshots {
						if len(frame.saved) != 0 {
							t.Fatal("idle frame retains FuncType/slice references")
						}
					}
					// A second complete scope uses the same maps, while its snapshots start
					// empty and reflect the CURRENT live rows left by the prior scope.
					pointers := make([]uintptr, len(inventory.snapshots))
					for i, frame := range inventory.snapshots {
						pointers[i] = reflect.ValueOf(frame.saved).Pointer()
					}
					outer(func() { observe(); caller(funcs[2], func() { observe(); writes[5].restore(types[2]) }) })
					for i, pointer := range pointers {
						if reflect.ValueOf(inventory.snapshots[i].saved).Pointer() != pointer {
							t.Fatal("released saved-row map was allocated again")
						}
					}
					if inventory.snapshotDepth != 0 {
						t.Fatal("second scope failed to release")
					}
				} else {
					outer(func() { observe(); caller(funcs[2], func() { observe(); writes[5].restore(types[2]) }) })
				}
				observe()
				return trace
			}
			prior, buffered := run(false), run(true)
			if len(prior) != len(buffered) {
				t.Fatal("row observation count changed")
			}
			for i := range prior {
				ft := &FuncType{}
				buffered[i].restore(ft)
				if !prior[i].identical(ft) || !exactPermissionSnapshotHeaders(prior[i], buffered[i]) {
					t.Fatalf("full shallow identity/value restoration mismatch at %d", i)
				}
			}
		})
	}
}

func TestPermissionSnapshotFramesNilContextAndMissingCaller(t *testing.T) {
	a := &Analyzer{funcDeclSymbols: map[*ast.FuncDecl]*Symbol{}}
	inventory := newRetainedPermissionInventory(nil)
	p := &retainedPermissions{initial: map[*FuncType]permissionRowSnapshot{}}
	ran := false
	if p.withInitialRowsForSummaryCallerUsingInventory(a, nil, inventory, &ast.FuncDecl{}, func() { ran = true }) || ran || inventory.snapshotDepth != 0 || len(inventory.snapshots) != 0 {
		t.Fatal("missing caller acquired scratch or ran body")
	}
	p.withInitialRowsUsingInventory(a, nil, nil, func() { ran = true })
	if !ran {
		t.Fatal("nil inventory compatibility skipped body")
	}
}

// Compare the complete shallow headers too, including empty-slice pointer/cap
// and nil-vs-empty shape, beyond the older len/first-element skip predicate.
func exactPermissionSnapshotHeaders(left, right permissionRowSnapshot) bool {
	if !reflect.DeepEqual(left, right) {
		return false
	}
	a, b := reflect.ValueOf(left), reflect.ValueOf(right)
	for i := 0; i < a.NumField(); i++ {
		x, y := a.Field(i), b.Field(i)
		if x.IsNil() != y.IsNil() || x.Len() != y.Len() || x.Cap() != y.Cap() || x.Pointer() != y.Pointer() {
			return false
		}
	}
	return true
}
