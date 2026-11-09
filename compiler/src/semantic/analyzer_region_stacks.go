package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"os"
	"reflect"
)

// regionStackCap bounds the number of own (growable-tail) stacks per region before the overflow
// growables are merged into one shared CHAINED stack.
//
// Policy (the death-time region model): EVERY growable gets its own bump-stack so it can grow at
// its tail without fragmenting any sibling, and a region's crossing lifetimes are resolved by
// separation rather than rejected. The cap is therefore set high enough to mean "every growable"
// for all realistic code — its only remaining job is a safety valve against a pathological region
// with dozens of independently-growing containers. On POSIX an UNUSED growable stack costs nothing
// (the reserve_commit reservation is lazy), and a used one costs one VMA + lazily-committed virtual
// space; on non-overcommit targets growables stay chained (no pre-reservation). So the practical
// cost of a high cap is a VMA per actually-used growable, which is cheap until the count is huge.
//
// Lifting this from its original value of 4 both removes merge-stack fragmentation for real code
// and eliminates the "interleaved object lifetimes ... stack budget exhausted" rejection for any
// region with <= this many crossing growables. Once the cross-region stack-reuse POOL lands (it
// makes extra stacks free to recycle), the merge can be dropped entirely and this cap removed.
var regionStackCap = 64

// RegionStackAssignment partitions an inferred region's fresh container allocations into parallel
// bump-stacks (lifetime inference, Phase B; docs/71). Stack 0 is the shared stack for
// fixed-footprint allocations (reserved growables); stacks 1..N each hold one unreserved growable
// as its tail so it can grow freely. When the over-split cap is hit, the overflow growables share
// one merge stack and Merged is set. B1a computes and records this; B1b consumes it in codegen.
type RegionStackAssignment struct {
	StackOf       map[string]int   // allocation name -> stack id (0 == shared fixed/reserved stack)
	StackKind     map[int]string   // stack id -> "shared" | "growable" | "merge"
	StackStrategy map[int]string   // stack id -> "chained" (default) | "reserve_commit" (Phase C)
	StackCapacity map[int]ast.Expr // reserve_commit stack id -> proven element-count bound N
	StackElemType map[int]Type     // reserve_commit stack id -> the darray's element type (for sizing)
	// StackBlockBound: default (lazy) reserve_commit stack id -> proven element-count bound that
	// codegen may use to back the stack with a frame block when it fits (analyzer_region_stack_block.go).
	StackBlockBound map[int]ast.Expr
	// StackEarlyFreeAfter (Phase B2): own-stack id -> byte offset of the top-level statement after
	// which the stack's arena can be freed early (the object dies before region exit and nothing
	// references it past then). Absent when the stack must live to region exit.
	StackEarlyFreeAfter map[int]int
	StackCount          int
	Merged              bool
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
// targetMmapOvercommit reports whether the target reserves virtual memory lazily (anonymous mmap
// overcommit). The default reserve_commit backing (docs/73) reserves a generous range whose pages
// commit on first touch; that is free on POSIX but NOT on Windows VirtualAlloc or the libc-malloc
// backend, where it would commit eagerly. On those targets the default growable stays chained.
// Defaults to false (chained) when the target const is absent — the safe, portable choice.
func (a *Analyzer) targetMmapOvercommit() bool {
	if v, ok := a.constValues["ELISA_TARGET_OS_POSIX"]; ok && v.Kind == ConstBool {
		return v.Bool
	}
	return false
}

// isFreshAutoStructAllocation reports whether vd binds a `new[auto] T(...)` allocation — a
// fixed-footprint heap struct placed in the inferred region. It is tracked like a reserved/fixed
// container: it lives on the shared stack 0 and is reclaimed in bulk at region exit/reset (it never
// grows, so it gets no own growable stack and no per-object early-free).
func isFreshAutoStructAllocation(vd *ast.VarDeclStmt) bool {
	if vd == nil || vd.Value == nil {
		return false
	}
	v := vd.Value
	for {
		paren, ok := v.(*ast.ParenExpr)
		if !ok {
			break
		}
		v = paren.Inner
	}
	alloc, ok := v.(*ast.AllocExpr)
	return ok && alloc != nil && alloc.AutoRegion
}

// collectAmbientForwardedContainers returns the subset of `candidates` (fresh region container
// names) that are passed — bare, `&name`, or reborrow-cast — at the ambient-grown param position
// of a call to a void grower (a callee the region-param pre-pass gave an
// AmbientGrownContainerRegion, recorded in a.ambientGrownCalleeParamIndex). Such a container's
// arena receives the callee's adopted payload allocations interleaved with its own growth, so
// its stack must keep the chained (relocating) backing.
func (a *Analyzer) collectAmbientForwardedContainers(body []ast.Stmt, candidates map[string]bool) map[string]bool {
	out := map[string]bool{}
	if a == nil || len(a.ambientGrownCalleeParamIndex) == 0 || len(candidates) == 0 {
		return out
	}
	checkCall := func(expr ast.Expr) {
		calleeName, args, isCall := ast.PrepassCallShape(expr)
		if !isCall {
			return
		}
		idx, ok := a.ambientGrownCalleeParamIndex[calleeName]
		if !ok || idx >= len(args) {
			return
		}
		for n := range candidates {
			if forwardsParamIdent(args[idx], n) {
				out[n] = true
			}
		}
	}
	walkPruned(body, func(node any) bool {
		if expr, ok := node.(ast.Expr); ok {
			checkCall(expr)
		}
		return true
	})
	return out
}

// collectRegionParamForwardedContainers returns fresh region containers passed at a parameter
// position whose type carries a callee region parameter. Such a call shares the candidate's
// allocation region with the callee. The callee can allocate between two growth operations on the
// candidate even when it does not insert a region-valued payload into the candidate itself. A
// reserve_commit backing would then make the next realloc a non-tail growth, so the backing must
// remain chained.
func (a *Analyzer) regionParamCalleeIndices(calleeName string) map[int]bool {
	if a == nil {
		return nil
	}
	if indices := a.regionParamCalleeParamIndex[calleeName]; len(indices) != 0 {
		return indices
	}
	for i := len(calleeName) - 1; i >= 0; i-- {
		if calleeName[i] != '.' {
			continue
		}
		if indices := a.regionParamCalleeParamIndex[calleeName[i+1:]]; len(indices) != 0 {
			return indices
		}
		break
	}
	return nil
}

func (a *Analyzer) collectRegionParamForwardedContainers(body []ast.Stmt, candidates map[string]bool) map[string]bool {
	out := map[string]bool{}
	if a == nil || len(a.regionParamCalleeParamIndex) == 0 || len(candidates) == 0 {
		return out
	}
	checkCall := func(expr ast.Expr) {
		calleeName, args, isCall := ast.PrepassCallShape(expr)
		if !isCall {
			return
		}
		for idx := range a.regionParamCalleeIndices(calleeName) {
			if idx < 0 || idx >= len(args) {
				continue
			}
			for n := range candidates {
				if forwardsParamIdent(args[idx], n) {
					out[n] = true
				}
			}
		}
	}
	walkPruned(body, func(node any) bool {
		if expr, ok := node.(ast.Expr); ok {
			checkCall(expr)
		}
		return true
	})
	return out
}

func (a *Analyzer) assignRegionStacks(region *ast.RegionStmt, paramNames map[string]bool) RegionStackAssignment {
	asn := RegionStackAssignment{StackOf: map[string]int{}, StackKind: map[int]string{0: "shared"}, StackStrategy: map[int]string{}, StackCapacity: map[int]ast.Expr{}, StackElemType: map[int]Type{}, StackBlockBound: map[int]ast.Expr{}, StackEarlyFreeAfter: map[int]int{}, StackCount: 1}
	order := make([]string, 0, 4)
	seen := map[string]bool{}
	elemTypeByName := map[string]Type{}
	fixedStruct := map[string]bool{} // new[auto] structs: fixed-footprint, shared stack 0
	for _, stmt := range flattenRegionTopLevel(region.Body) {
		vd, ok := stmt.(*ast.VarDeclStmt)
		if !ok || seen[vd.Name] {
			continue
		}
		if a.isFreshRegionAllocation(vd) {
			seen[vd.Name] = true
			order = append(order, vd.Name)
			if dt, ok := StripAggregateStateType(a.exprType(vd.Value)).(*DArrayType); ok {
				elemTypeByName[vd.Name] = dt.Elem
			}
			continue
		}
		if isFreshAutoStructAllocation(vd) {
			seen[vd.Name] = true
			order = append(order, vd.Name)
			fixedStruct[vd.Name] = true
		}
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

	// Containers forwarded to an ambient-grown-container callee (a void grower, docs/76): the
	// callee routes its ADOPTED payload allocations into the container's arena, interleaving
	// foreign allocations between the container's growth steps. A tail-only reserve_commit stack
	// would then panic at the first non-tail realloc ("cannot grow in place"), so such a
	// container keeps the chained (relocating) backing. The proven-bound interior-ref path below
	// is unaffected: its capacity is preallocated up front, so it never reallocs at all.
	ambientForwarded := a.collectAmbientForwardedContainers(region.Body, seen)
	// The broader region-parameter summary covers ordinary region-requiring callees too. The
	// ambient map remains separate because it also controls the callee's ambient allocation binding.
	regionParamForwarded := a.collectRegionParamForwardedContainers(region.Body, seen)

	next := 1
	mergeStack := -1
	for _, n := range order {
		// new[auto] structs are fixed-footprint: they share stack 0 (the region's main arena) and
		// are reclaimed in bulk at region exit/reset — no own growable stack, no per-object early
		// free. This counts them as tracked region members (so the lifetime-class summary is exact).
		if fixedStruct[n] {
			asn.StackOf[n] = 0
			continue
		}
		// Phase C (docs/72): a darray with an interior reference taken into it gets a
		// reserve_commit stack (stable base) ONLY when its maximum footprint is PROVABLE — its
		// sole element growth is a single counting-loop push, bound N. A bare `reserve(n)` is a
		// hint, not a hard limit, so it is NOT sufficient; reserve_commit on an unbounded darray
		// would panic on overflow (worse than the current compile error).
		if interiorRef[n] {
			if bound, ok := hardBoundForName(region.Body, n); ok && boundInScopeAtRegionEntry(bound, paramNames) {
				if elem, ok := elemTypeByName[n]; ok {
					asn.StackOf[n] = next
					asn.StackKind[next] = "growable"
					asn.StackStrategy[next] = "reserve_commit"
					asn.StackCapacity[next] = bound
					asn.StackElemType[next] = elem
					next++
					continue
				}
			}
		}
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
		// docs/73: the DEFAULT backing for an unreserved own-growable tail is reserve_commit — a
		// contiguous bump stack that grows in place (no relocate-and-leave-a-hole), with a default
		// reservation (StackCapacity left nil). This makes fragmentation structurally impossible
		// and the base stable (interior refs survive). Applies in loops too: codegen sets the
		// strategy lazily (no eager reserve) and arena_reset keeps the reservation across
		// iterations. Only the merge stack (multiple growables sharing one stack) stays chained.
		// docs/73 §5: gated on mmap-overcommit targets (POSIX). On Windows (VirtualAlloc) and the
		// libc-malloc backend a 256 MiB reservation would commit eagerly, so those keep chained —
		// the checker reads this same strategy, so trust and runtime stay consistent everywhere.
		if a.targetMmapOvercommit() && !ambientForwarded[n] && !regionParamForwarded[n] {
			asn.StackStrategy[next] = "reserve_commit"
			if elem, ok := elemTypeByName[n]; ok {
				if bound, ok := a.stackBlockAdvisoryBound(region.Body, n, elem, paramNames); ok {
					asn.StackBlockBound[next] = bound
					asn.StackElemType[next] = elem
				}
			}
		}
		next++
	}
	// Phase B2 (docs/71): an own growable stack whose object provably dies before region exit and
	// is never aliased can have its arena freed early — right after its last use — rather than at
	// region exit. The escape check is the conservative D-merge: if the object is aliased (address
	// taken, copied, or passed out), it stays to region exit.
	topLevel := flattenRegionTopLevel(region.Body)
	for n, stack := range asn.StackOf {
		if stack <= 0 || asn.StackKind[stack] == "merge" {
			continue // shared/merge stacks hold more than one object; cannot free individually
		}
		if off, ok := earlyFreeableObject(topLevel, n); ok {
			asn.StackEarlyFreeAfter[stack] = off
		}
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

// earlyFreeableObject returns the byte offset of the last top-level statement referencing `name`,
// when that object can be freed there: it is referenced ONLY as a method/field receiver or an index
// base (never aliased — no address taken, no bare-value use that could copy its buffer pointer), and
// it dies strictly before the region's final statement. ok=false means keep it to region exit.
func earlyFreeableObject(topLevel []ast.Stmt, name string) (int, bool) {
	if objectMayEscape(topLevel, name) {
		return 0, false
	}
	// A local whose value was computed from the object (`v = buf.as_sview()`, an enum payload or
	// tuple holding such a view, a closure capturing it) may still point into its buffer: the
	// object is live until the last use of ANY such derived local, not just its own last mention.
	derived := earlyFreeDerivedNames(topLevel, name)
	lastRef := -1
	for i, stmt := range topLevel {
		for n := range derived {
			if subtreeReferencesIdent(stmt, n) {
				lastRef = i
				break
			}
		}
	}
	if lastRef < 0 || lastRef >= len(topLevel)-1 {
		return 0, false // unused, or still live in the final statement (dies at region exit)
	}
	return topLevel[lastRef].Pos().Offset, true
}

// earlyFreeDerivedNames returns name plus every local (transitively) bound or assigned from a value
// that mentions one of them. Over-approximate on purpose: a scalar derived local (`n = buf.count`)
// only delays the early free, while a missed borrow (`v = buf.as_sview()`) frees a live buffer.
func earlyFreeDerivedNames(body []ast.Stmt, name string) map[string]bool {
	derived := map[string]bool{name: true}
	mentions := func(v reflect.Value) bool {
		if !v.IsValid() {
			return false
		}
		for n := range derived {
			if reflectSubtreeReferencesIdent(v, n) {
				return true
			}
		}
		return false
	}
	for changed := true; changed; {
		changed = false
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
				walk(v.Elem())
			case reflect.Struct:
				if vd, ok := v.Interface().(ast.VarDeclStmt); ok && earlyFreeScalarDeclType(vd.Type) {
					// `n: i64 = buf.count` copies a scalar out: it cannot point into buf.
				} else if value := reflectFieldByName(v, "Value"); value.IsValid() && mentions(value) {
					for _, field := range []string{"Name", "Names", "Target", "Pattern", "Store"} {
						for _, bound := range earlyFreeBoundNames(reflectFieldByName(v, field)) {
							if bound != "" && !derived[bound] {
								derived[bound] = true
								changed = true
							}
						}
					}
				}
				if call, ok := v.Interface().(ast.CallExpr); ok {
					// `hs.push(H{f: fn() => s[0]})` / `fill(&out, s)`: a derived value stored into a
					// container keeps the object alive as long as the container.
					argMentions := false
					for _, arg := range call.Args {
						if mentions(reflect.ValueOf(arg)) {
							argMentions = true
						}
					}
					if argMentions {
						var roots []string
						if field, ok := call.Func.(*ast.FieldExpr); ok {
							roots = append(roots, earlyFreePlaceRoot(field.Object))
						}
						for _, arg := range call.Args {
							if unary, ok := arg.(*ast.UnaryExpr); ok && unary.Op == lexer.TOKEN_AMPERSAND {
								roots = append(roots, earlyFreePlaceRoot(unary.Operand))
							}
							if addr, ok := arg.(*ast.AddrOfExpr); ok {
								roots = append(roots, earlyFreePlaceRoot(addr.Operand))
							}
						}
						for _, root := range roots {
							if root != "" && !derived[root] {
								derived[root] = true
								changed = true
							}
						}
					}
				}
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
	}
	return derived
}

// earlyFreeScalarDeclType: a declared plain value scalar (not `uintptr`, which can hold an address).
func earlyFreeScalarDeclType(t ast.TypeExpr) bool {
	named, ok := t.(*ast.NamedType)
	if !ok || named == nil || named.Region != "" {
		return false
	}
	switch named.Name {
	case "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64", "isize", "usize", "f32", "f64", "bool", "char":
		return true
	}
	return false
}

// earlyFreeBoundNames collects every `Name` string and identifier under a binding/target field.
func earlyFreeBoundNames(v reflect.Value) []string {
	out := []string{}
	var walk func(v reflect.Value, fieldName string)
	walk = func(v reflect.Value, fieldName string) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.String:
			if fieldName == "Name" || fieldName == "Names" {
				out = append(out, v.String())
			}
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if id, ok := v.Interface().(*ast.Ident); ok {
				out = append(out, id.Name)
				return
			}
			walk(v.Elem(), fieldName)
		case reflect.Struct:
			t := v.Type()
			rfs := reflectFields(t)
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i), rfs[i].Name)
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), fieldName)
			}
		}
	}
	walk(v, "Name")
	return out
}

// reflectSubtreeReferencesIdent is subtreeReferencesIdent over an arbitrary reflected value.
func reflectSubtreeReferencesIdent(root reflect.Value, name string) bool {
	found := false
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if found || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if id, ok := v.Interface().(*ast.Ident); ok && id.Name == name {
				found = true
				return
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
	walk(root)
	return found
}
