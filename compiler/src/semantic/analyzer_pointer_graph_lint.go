package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// perfLint emits a performance-friction diagnostic at the configured severity: a warning by
// default (a nudge that keeps prototyping fluid), or a hard error under `-Wperf`
// (EnforcePerfLints) so shipped code can ban the anti-pattern outright. The graduated lever
// from docs/70 — same diagnostic, different teeth.
func (a *Analyzer) perfLint(pos lexer.Pos, format string, args ...interface{}) {
	if a.enforcePerfLints {
		a.errorf(pos, format, args...)
		return
	}
	a.warnf(pos, format, args...)
}

// The pointer-graph lint (docs/70). A struct that references itself through a ref field with
// NO region provenance is a raw self-referential pointer graph — the classic linked
// list / tree / intrusive list built from bare pointers. It is both unsafe (no tracked
// lifetime) and slow (pointer chasing is cache-hostile), so it is the structural
// anti-pattern the handle-into-store style (packed enums, `darray`/`Store` indices) exists
// to replace.
//
// Crucially this does NOT flag a `@owner`-tracked self-ref (`next: Node& @owner` in a
// `struct Node[region owner]`): that is a sound single-region graph whose whole lifetime is
// one decision. Only the un-provenance'd raw kind (`heap Node&?` or a bare `Node&?`) is
// flagged — friction lands on the slow/unsafe pattern, not the safe one.
func (a *Analyzer) checkPointerGraphStruct(stDecl *ast.StructDecl, st *StructType) {
	if stDecl == nil || st == nil || st.Builtin {
		return
	}
	// `@intrusive` is the explicit acknowledgment: "yes, this is a deliberate raw
	// self-referential node" (free-lists, intrusive queues, hash chains). Friction, but
	// possible — the runtime's low-level primitives that IMPLEMENT the safe abstractions
	// opt out this way; ordinary code is warned by default.
	if structDeclHasAnnotation(stDecl, "intrusive") {
		return
	}
	for _, field := range stDecl.Fields {
		resolved, ok := st.Fields[field.Name]
		if !ok {
			continue
		}
		if !rawSelfReferentialRef(resolved.Type, st) {
			continue
		}
		a.perfLint(field.Position, "field %q makes %q a raw self-referential pointer graph (a linked node / tree of bare pointers — unsafe to outlive and cache-hostile to traverse). Prefer a handle into a store: a packed enum, or an index into a `darray`/`Store`. For a region-unified graph instead, give the field provenance — `struct %s[region owner]` with `%s: ... @owner`", field.Name, st.Name, st.Name, field.Name)
	}
}

// checkPointerGraphCycles extends the docs/70 lint to INDIRECT cycles: a raw pointer graph
// spread across two or more struct types (`struct A: next: heap B&?` / `struct B: back: heap
// A&?` — the A→B→A loop). These are the same pointer-jungle anti-pattern as a direct self-ref,
// just split across types, so they earn the same diagnostic.
//
// The directed graph has an edge A→B when A has a field that is a `*RefType` with `Region == ""`
// (a RAW ref — `@owner`-tracked refs are sound single-region graphs and create NO edge) whose
// element is struct B. Refs to non-struct types (a `darray.items` buffer pointer) are ignored,
// so a lone buffer pointer is never part of a cycle.
//
// False-positive avoidance: we SKIP edges that pass THROUGH an `@intrusive` struct (an
// acknowledged raw boundary). An `@intrusive` node is a deliberate cut in the graph, so a cycle
// that can only close by routing through one is not flagged for the other participants either.
// This is the lower-false-positive choice: acknowledging one node in a runtime cycle silences
// the whole loop, matching the direct-self-ref ergonomics. Pure self-loops (length 1) are left
// entirely to checkPointerGraphStruct so we never double-warn.
func (a *Analyzer) checkPointerGraphCycles(decls []scopedDecl) {
	// Collect the (decl, type) pairs for non-builtin structs, indexed by *StructType identity.
	type structNode struct {
		decl *ast.StructDecl
		st   *StructType
	}
	nodes := map[*StructType]*structNode{}
	var order []*StructType
	for _, scoped := range decls {
		stDecl, ok := scoped.Decl.(*ast.StructDecl)
		if !ok || stDecl == nil {
			continue
		}
		st, _ := a.namedTypes[joinQualifiedName(scoped.Namespace, stDecl.Name)].(*StructType)
		if st == nil || st.Builtin {
			continue
		}
		if _, seen := nodes[st]; seen {
			continue
		}
		nodes[st] = &structNode{decl: stDecl, st: st}
		order = append(order, st)
	}

	intrusive := func(st *StructType) bool {
		n := nodes[st]
		return n != nil && structDeclHasAnnotation(n.decl, "intrusive")
	}

	// rawRefEdges returns the raw-ref edges out of st: the target struct and the field that
	// carries it. Self-loops are excluded (the direct check owns those).
	type edge struct {
		field  *ast.FieldDecl
		target *StructType
	}
	edgesOf := func(node *structNode) []edge {
		var out []edge
		for i := range node.decl.Fields {
			field := &node.decl.Fields[i]
			resolved, ok := node.st.Fields[field.Name]
			if !ok {
				continue
			}
			ref, ok := resolved.Type.(*RefType)
			if !ok || ref == nil || ref.Region != "" {
				continue
			}
			elem, ok := ref.Elem.(*StructType)
			if !ok || elem == node.st {
				continue
			}
			if nodes[elem] == nil {
				continue
			}
			out = append(out, edge{field: field, target: elem})
		}
		return out
	}

	// DFS cycle detection over the raw-ref graph, skipping @intrusive nodes (acknowledged
	// boundaries — they neither participate nor route a cycle). For each non-intrusive struct
	// found on a back-edge, flag the field that closes the cycle. Each struct is flagged at
	// most once (lowest-positioned field is deterministic via field order).
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[*StructType]int{}
	flagged := map[*StructType]bool{}

	var dfs func(st *StructType)
	dfs = func(st *StructType) {
		color[st] = gray
		node := nodes[st]
		for _, e := range edgesOf(node) {
			if intrusive(e.target) {
				continue
			}
			switch color[e.target] {
			case white:
				dfs(e.target)
			case gray:
				// Back-edge: st → e.target closes a cycle. Flag st on this edge.
				if !flagged[st] {
					flagged[st] = true
					a.perfLint(e.field.Position, "field %q makes %q part of a raw pointer-graph cycle (%s → %s → ... → %s — a graph of bare pointers spread across types, unsafe to outlive and cache-hostile to traverse). Prefer a handle into a store: a packed enum, or an index into a `darray`/`Store`. For a region-unified graph instead, give the refs provenance (`[region owner]` + `@owner`), or acknowledge the raw structure with `@intrusive`", e.field.Name, st.Name, st.Name, e.target.Name, st.Name)
				}
			}
		}
		color[st] = black
	}

	for _, st := range order {
		if intrusive(st) {
			continue
		}
		if color[st] == white {
			dfs(st)
		}
	}
}

// rawSelfReferentialRef reports whether a field type is a ref to the SAME struct that
// carries no region provenance. Pointer identity against the enclosing StructType keeps it
// precise (direct self-reference only — the linked-list/tree case); a `@owner`-tracked
// ref (Region != "") is a sound single-region graph and is not flagged.
func rawSelfReferentialRef(fieldType Type, st *StructType) bool {
	ref, ok := fieldType.(*RefType)
	if !ok || ref == nil {
		return false
	}
	if ref.Region != "" {
		return false
	}
	elem, ok := ref.Elem.(*StructType)
	return ok && elem == st
}
