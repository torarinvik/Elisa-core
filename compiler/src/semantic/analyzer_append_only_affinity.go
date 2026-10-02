package semantic

import 	"elisacore/src/ast"

// induceAppendOnlyAffinity: a struct that holds an @append_only store by value anywhere inside it
// is affine. Two header copies of a store would append into the same spare capacity, so one copy
// overwrites bytes behind the other's live views -- a silent wrong answer. Like a declared
// `__drop__`, holding a store induces affinity: the holder becomes a user droppable affine struct,
// which keeps it borrowable (`Pool&`) while by-value copies, assignment and record updates are
// rejected by the ordinary affine move-site checks.
func (a *Analyzer) induceAppendOnlyAffinity(decls []scopedDecl) {
	isStore := func(t Type) bool {
		st, ok := t.(*StructType)
		return ok && st.AppendOnly
	}
	var holders []*StructType
	for _, scoped := range decls {
		st, ok := scoped.Decl.(*ast.StructDecl)
		if !ok {
			continue
		}
		if structType, ok := a.namedTypes[joinQualifiedName(scoped.Namespace, st.Name)].(*StructType); ok && !structType.AppendOnly {
			holders = append(holders, structType)
		}
	}
	for _, structType := range holders {
		if structType.Affine {
			continue
		}
		for _, field := range structType.Fields {
			if a.typeContainsWithSeen(field.Type, isStore, "append-only affinity traversal", map[Type]bool{}, 0) {
				structType.Affine = true
				structType.Droppable = true
				break
			}
		}
	}
}
