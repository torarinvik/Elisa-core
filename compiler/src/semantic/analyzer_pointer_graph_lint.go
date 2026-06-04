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
