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
	StackOf    map[string]int // allocation name -> stack id (0 == shared fixed/reserved stack)
	StackKind  map[int]string // stack id -> "shared" | "growable" | "merge"
	StackCount int
	Merged     bool
}

var dumpRegionStacks = os.Getenv("ELISA_DUMP_REGION_STACKS") != ""

// assignRegionStacks computes the B1 partition for one inferred region: every unreserved growable
// gets its own stack, all reserved (fixed-footprint) allocations share stack 0, and growables
// beyond regionStackCap share a merge stack. Analysis-only (B1a) — recorded for codegen and
// inspection; no behavior change yet.
func (a *Analyzer) assignRegionStacks(region *ast.RegionStmt) RegionStackAssignment {
	asn := RegionStackAssignment{StackOf: map[string]int{}, StackKind: map[int]string{0: "shared"}, StackCount: 1}
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

	next := 1
	mergeStack := -1
	for _, n := range order {
		if scan.reserved[n] {
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
		fmt.Fprintf(os.Stderr, "  stack %d (%s): %v\n", s, asn.StackKind[s], byStack[s])
	}
}
