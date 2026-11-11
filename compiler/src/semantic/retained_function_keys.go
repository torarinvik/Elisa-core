package semantic

import (
	"strconv"

	"elisacore/src/ast"
)

// retainedPreBodyFunctionKeys assigns keys in declaration order so overloads
// keep stable occurrence numbers within each namespace.
func retainedPreBodyFunctionKeys(decls []scopedDecl) map[*ast.FuncDecl]string {
	return collectRetainedPreBodyFunctionKeys(decls, nil)
}

func collectRetainedPreBodyFunctionKeys(decls []scopedDecl, ordered *[]*ast.FuncDecl) map[*ast.FuncDecl]string {
	keys := make(map[*ast.FuncDecl]string, len(decls))
	occurrences := make(map[string]int, len(decls))
	record := func(namespace string, fn *ast.FuncDecl) {
		base := namespace + "::" + fn.Name
		occurrence := occurrences[base]
		keys[fn] = base + "#" + strconv.Itoa(occurrence)
		occurrences[base] = occurrence + 1
		if ordered != nil {
			*ordered = append(*ordered, fn)
		}
	}
	for _, scoped := range decls {
		if fn, ok := scoped.Decl.(*ast.FuncDecl); ok {
			if fn != nil {
				record(scoped.Namespace, fn)
			}
			continue
		}
		for _, declaration := range retainedWalkDecls(&ast.File{Decls: []ast.Decl{scoped.Decl}}) {
			if fn, ok := declaration.(*ast.FuncDecl); ok {
				record(scoped.Namespace, fn)
			}
		}
	}
	return keys
}
