package semantic

import "elisacore/src/ast"

// checkStructByValueCycles rejects a struct that contains itself by value, directly
// (`struct A: a: A`) or through other structs (`A{b: B}`, `B{a: A}`). Such a type has no
// finite layout; the analyzer used to accept it and the LLVM backend then recursed through
// lowerType/ensureStructBody until a fatal Go stack overflow. Mirrors the enum rule
// ("cannot contain %q by value; use a reference type instead"). Fields are walked in
// declaration order (st.Fields is a map) so the reported field is deterministic.
func (a *Analyzer) checkStructByValueCycles(decls []scopedDecl) {
	for _, scoped := range decls {
		decl, ok := scoped.Decl.(*ast.StructDecl)
		if !ok || decl == nil {
			continue
		}
		root, _ := a.namedTypes[joinQualifiedName(scoped.Namespace, decl.Name)].(*StructType)
		if root == nil || root.Builtin || len(root.TypeParams) != 0 {
			continue
		}
		for _, field := range decl.Fields {
			if field.Ghost || field.IsTail || field.BitGroup != nil {
				continue
			}
			child, ok := root.Fields[field.Name].Type.(*StructType)
			if !ok || child == nil {
				continue
			}
			if child == root || structReachesByValue(child, root, map[*StructType]bool{}) {
				a.errorf(field.Position, "struct %q cannot contain %q by value; use a reference type instead", decl.Name, decl.Name)
				break
			}
		}
	}
}

func structReachesByValue(from, target *StructType, visited map[*StructType]bool) bool {
	if from == nil || visited[from] || from.Decl == nil {
		return false
	}
	visited[from] = true
	for _, field := range from.Decl.Fields {
		if field.Ghost || field.IsTail || field.BitGroup != nil {
			continue
		}
		child, ok := from.Fields[field.Name].Type.(*StructType)
		if !ok || child == nil {
			continue
		}
		if child == target || structReachesByValue(child, target, visited) {
			return true
		}
	}
	return false
}
