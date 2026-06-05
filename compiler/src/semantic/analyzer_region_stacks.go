package semantic

import (
	"fmt"
	"os"
	"reflect"
	"sort"

	"elisacore/src/ast"
)

// regionStackCap bounds the number of own (growable-tail) stacks per region before the overflow
// growables are merged into one shared CHAINED stack — so a function with many small growables
// does not allocate many separate blocks (docs/71, over-split guard).
const regionStackCap = 4

// RegionStackAssignment partitions an inferred region's fresh container allocations into parallel
// bump-stacks (lifetime inference, Phase B; docs/71). Stack 0 is the shared stack for
// fixed-footprint allocations (reserved growables); stacks 1..N each hold one unreserved growable
// as its tail so it can grow freely. When the over-split cap is hit, the overflow growables share
// one merge stack and Merged is set. B1a computes and records this; B1b consumes it in codegen.
type RegionStackAssignment struct {
	StackOf       map[string]int    // allocation name -> stack id (0 == shared fixed/reserved stack)
	StackKind     map[int]string    // stack id -> "shared" | "growable" | "merge"
	StackStrategy map[int]string    // stack id -> "chained" (default) | "reserve_commit" (Phase C)
	StackCount    int
	Merged        bool
}

// stackStrategy returns the backing strategy for a stack (chained unless Phase C assigned one).
func (asn RegionStackAssignment) stackStrategy(stack int) string {
	if s, ok := asn.StackStrategy[stack]; ok && s != "" {
		return s
	}
	return "chained"
}

var dumpRegionStacks = os.Getenv("ELISA_DUMP_REGION_STACKS") != ""

// assignRegionStacks computes the B1 partition for one inferred region: every unreserved growable
// gets its own stack, all reserved (fixed-footprint) allocations share stack 0, and growables
// beyond regionStackCap share a merge stack. Analysis-only (B1a) — recorded for codegen and
// inspection; no behavior change yet.
func (a *Analyzer) assignRegionStacks(region *ast.RegionStmt) RegionStackAssignment {
	asn := RegionStackAssignment{StackOf: map[string]int{}, StackKind: map[int]string{0: "shared"}, StackStrategy: map[int]string{}, StackCount: 1}
	order := make([]string, 0, 4)
	seen := map[string]bool{}
	for _, stmt := range region.Body {
		vd, ok := stmt.(*ast.VarDeclStmt)
		if !ok || !a.isFreshRegionAllocation(vd) || seen[vd.Name] {
			continue
		}
		seen[vd.Name] = true
		order = append(order, vd.Name)
	}
	if len(order) == 0 {
		return asn
	}

	// Reserved (fixed-footprint) growables: reuse the growth scan's reserve detection.
	tracked := map[string]int{}
	for _, n := range order {
		tracked[n] = 0
	}
	scan := &regionGrowthScan{tracked: tracked, reserved: map[string]bool{}}
	scan.walkGrowth(reflect.ValueOf(region.Body))
	// Phase C: darrays that have an interior reference taken into them (`&x[i]`). A reserved
	// (bounded) such darray gets its own reserve_commit stack so the base never moves and the
	// reference stays valid across growth.
	interiorRef := collectInteriorRefNames(region.Body, seen)

	next := 1
	mergeStack := -1
	for _, n := range order {
		if scan.reserved[n] {
			if interiorRef[n] {
				// bounded + interior ref -> own reserve_commit stack (Phase C, docs/72)
				asn.StackOf[n] = next
				asn.StackKind[next] = "growable"
				asn.StackStrategy[next] = "reserve_commit"
				next++
				continue
			}
			asn.StackOf[n] = 0 // shared fixed/reserved stack
			continue
		}
		if next > regionStackCap {
			if mergeStack < 0 {
				mergeStack = next
				asn.StackKind[mergeStack] = "merge"
			}
			asn.StackOf[n] = mergeStack
			asn.Merged = true
			continue
		}
		asn.StackOf[n] = next
		asn.StackKind[next] = "growable"
		next++
	}
	maxStack := 0
	for _, s := range asn.StackOf {
		if s > maxStack {
			maxStack = s
		}
	}
	asn.StackCount = maxStack + 1
	return asn
}

// collectInteriorRefNames returns the tracked container names that have an interior reference
// taken into them anywhere in body — i.e. `&name[...]` (an address-of an index expression). These
// are the darrays whose backing must not relocate for the reference to stay valid (Phase C).
func collectInteriorRefNames(body []ast.Stmt, tracked map[string]bool) map[string]bool {
	names := map[string]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if addr, ok := v.Interface().(*ast.AddrOfExpr); ok {
				if idx, ok := stripParenExpr(addr.Operand).(*ast.IndexExpr); ok {
					if id, ok := stripParenExpr(idx.Object).(*ast.Ident); ok && tracked[id.Name] {
						names[id.Name] = true
					}
				}
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(body))
	return names
}

func stripParenExpr(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.Inner
	}
}

func (a *Analyzer) dumpRegionStackAssignment(region *ast.RegionStmt, asn RegionStackAssignment) {
	note := ""
	if asn.Merged {
		note = " [over-split cap hit -> merge stack]"
	}
	fmt.Fprintf(os.Stderr, "region %s: %d stack(s)%s\n", region.Name, asn.StackCount, note)
	byStack := map[int][]string{}
	for name, s := range asn.StackOf {
		byStack[s] = append(byStack[s], name)
	}
	stacks := make([]int, 0, len(byStack))
	for s := range byStack {
		stacks = append(stacks, s)
	}
	sort.Ints(stacks)
	for _, s := range stacks {
		sort.Strings(byStack[s])
		fmt.Fprintf(os.Stderr, "  stack %d (%s, %s): %v\n", s, asn.StackKind[s], asn.stackStrategy(s), byStack[s])
	}
}
