package semantic

import "elisacore/src/ast"

type retainedDeclarationNameIndex struct {
	newNames, oldValueNames, oldTypeNames map[string]bool
	oldFunctionNames, newFunctionNames    map[*ast.FuncDecl]string
	oldFuncNameCount, newFuncNameCount    map[string]int
	newDeclIndex                          map[ast.Decl]int
	newTypeNames, newValueNames           map[string]bool
}

// retainedIndexDeclarationNames records both sides of an update before pairing declarations.
func retainedIndexDeclarationNames(pristine, file *ast.File, oldDecls, newDecls []ast.Decl) retainedDeclarationNameIndex {
	newNames := map[string]bool{}
	oldValueNames := map[string]bool{} // consts, globals and functions: names an added const must not take
	oldTypeNames := map[string]bool{}  // structs and enums: names an added struct must not take
	oldFunctionNames := retainedQualifiedFunctionNames(pristine)
	newFunctionNames := retainedQualifiedFunctionNames(file)
	oldFuncNameCount := map[string]int{}
	for _, d := range oldDecls {
		if f, ok := d.(*ast.FuncDecl); ok {
			if oldFunctionNames[f] == "" {
				oldFunctionNames[f] = f.Name
			}
			oldFuncNameCount[oldFunctionNames[f]]++
		}
		switch n := d.(type) {
		case *ast.StructDecl:
			oldTypeNames[n.Name] = true
		case *ast.EnumDecl:
			oldTypeNames[n.Name] = true
		case *ast.FuncDecl:
			oldValueNames[n.Name] = true
		case *ast.ConstDecl:
			oldValueNames[n.Name] = true
		case *ast.GlobalDecl:
			oldValueNames[n.Name] = true
		}
	}
	newValueNames := map[string]bool{} // consts, globals and functions of the edited program
	newFuncNameCount := map[string]int{}
	newDeclIndex := make(map[ast.Decl]int, len(newDecls))
	for j, d := range newDecls {
		newDeclIndex[d] = j
		if f, ok := d.(*ast.FuncDecl); ok {
			if newFunctionNames[f] == "" {
				newFunctionNames[f] = f.Name
			}
			newFuncNameCount[newFunctionNames[f]]++
		}
	}
	newTypeNames := map[string]bool{} // structs and enums of the edited program
	for _, d := range newDecls {
		switch n := d.(type) {
		case *ast.StructDecl:
			newTypeNames[n.Name] = true
		case *ast.EnumDecl:
			newTypeNames[n.Name] = true
		case *ast.FuncDecl:
			newNames[n.Name] = true
			newValueNames[n.Name] = true
		case *ast.ConstDecl:
			newValueNames[n.Name] = true
		case *ast.GlobalDecl:
			newValueNames[n.Name] = true
		}
	}
	return retainedDeclarationNameIndex{newNames, oldValueNames, oldTypeNames, oldFunctionNames, newFunctionNames, oldFuncNameCount, newFuncNameCount, newDeclIndex, newTypeNames, newValueNames}
}
