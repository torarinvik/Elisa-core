package semantic

import (
	"elisacore/src/ast"
	"fmt"
)

type retainedDeclarationPairing struct {
	removed, added               []*ast.FuncDecl
	pairs                        [][2]int
	addedConsts, removedConsts   []ast.Decl
	addedStructs, removedStructs []*ast.StructDecl
	insertedBefore               map[int]bool
	overloadNames                map[string]bool
	usingsAdded, usingsRemoved   int
}

// retainedPairDeclarations accepts only supported declaration additions and removals.
func retainedPairDeclarations(oldDecls, newDecls []ast.Decl, names retainedDeclarationNameIndex, topLevel map[ast.Decl]bool, memberOf map[ast.Decl]*ast.NamespaceDecl, oldRemovable map[ast.Decl]bool) (retainedDeclarationPairing, error) {
	newNames, oldValueNames, oldTypeNames := names.newNames, names.oldValueNames, names.oldTypeNames
	oldFunctionNames, newFunctionNames := names.oldFunctionNames, names.newFunctionNames
	oldFuncNameCount, newFuncNameCount := names.oldFuncNameCount, names.newFuncNameCount
	newTypeNames, newValueNames := names.newTypeNames, names.newValueNames
	var removed []*ast.FuncDecl
	var pairs [][2]int
	var added []*ast.FuncDecl
	var addedConsts []ast.Decl           // added typed consts and globals with fresh names
	var removedConsts []ast.Decl         // consts and globals whose name is gone from the edited program
	var addedStructs []*ast.StructDecl   // added plain structs with fresh names
	var removedStructs []*ast.StructDecl // plain structs whose name is gone from the edited program
	insertedBefore := map[int]bool{}     // pair index preceded by an added declaration
	overloadNames := map[string]bool{}   // qualified names with an edited overload set
	usingsAdded, usingsRemoved := 0, 0   // wildcard `using` lines added or removed
	for i, j := 0, 0; i < len(oldDecls) || j < len(newDecls); {
		if i < len(oldDecls) && j < len(newDecls) && retainedSameDeclKind(oldDecls[i], newDecls[j]) {
			pairs = append(pairs, [2]int{i, j})
			i, j = i+1, j+1
			continue
		}
		// A removed struct or const: its name is gone from the edited source.
		if i < len(oldDecls) {
			if ost, ok := oldDecls[i].(*ast.StructDecl); ok && retainedPlainStruct(ost) && !newTypeNames[ost.Name] && !newValueNames[ost.Name] {
				removedStructs = append(removedStructs, ost)
				insertedBefore[len(pairs)] = true
				i++
				continue
			}
		}
		if i < len(oldDecls) {
			if oc, ok := oldDecls[i].(*ast.ConstDecl); ok && oc.Type != nil && !newValueNames[oc.Name] {
				removedConsts = append(removedConsts, oc)
				insertedBefore[len(pairs)] = true
				i++
				continue
			}
			if og, ok := oldDecls[i].(*ast.GlobalDecl); ok && og.Type != nil && !newValueNames[og.Name] {
				removedConsts = append(removedConsts, og)
				insertedBefore[len(pairs)] = true
				i++
				continue
			}
		}
		// A removed wildcard `using`: its block's declarations take their new usings below.
		if i < len(oldDecls) && retainedWildcardUsing(oldDecls[i]) {
			usingsRemoved++
			insertedBefore[len(pairs)] = true
			i++
			continue
		}
		// A removed top-level or module/extend function: its name is gone.
		if i < len(oldDecls) {
			if of, ok := oldDecls[i].(*ast.FuncDecl); ok && oldRemovable[of] && newFuncNameCount[oldFunctionNames[of]] == 0 && retainedPlainFunction(of) {
				removed = append(removed, of)
				insertedBefore[len(pairs)] = true
				i++
				continue
			}
			// A removed overload: the name survives in another same-named function, which is
			// re-declared with the rest of its overload set below.
			if of, ok := oldDecls[i].(*ast.FuncDecl); ok && newFuncNameCount[oldFunctionNames[of]] != 0 && retainedPlainFunction(of) &&
				oldFuncNameCount[oldFunctionNames[of]] > newFuncNameCount[oldFunctionNames[of]] &&
				(j == len(newDecls) || !retainedSameDeclKind(of, newDecls[j])) {
				overloadNames[oldFunctionNames[of]] = true
				removed = append(removed, of)
				insertedBefore[len(pairs)] = true
				i++
				continue
			}
		}
		if j == len(newDecls) {
			return retainedDeclarationPairing{}, fmt.Errorf("declaration count changed (%d -> %d)", len(oldDecls), len(newDecls))
		}
		if retainedWildcardUsing(newDecls[j]) {
			usingsAdded++
			insertedBefore[len(pairs)] = true
			j++
			continue
		}
		if sd, ok := newDecls[j].(*ast.StructDecl); ok && retainedPlainStruct(sd) && (topLevel[sd] || memberOf[sd] != nil) && !oldTypeNames[sd.Name] && !oldValueNames[sd.Name] {
			addedStructs = append(addedStructs, sd)
			insertedBefore[len(pairs)] = true
			j++
			continue
		}
		if cd, ok := newDecls[j].(*ast.ConstDecl); ok && cd.Type != nil && (topLevel[cd] || memberOf[cd] != nil) && !oldValueNames[cd.Name] && !newNames[cd.Name] {
			addedConsts = append(addedConsts, cd)
			insertedBefore[len(pairs)] = true
			j++
			continue
		}
		if gd, ok := newDecls[j].(*ast.GlobalDecl); ok && gd.Type != nil && (topLevel[gd] || memberOf[gd] != nil) && !oldValueNames[gd.Name] && !newNames[gd.Name] {
			addedConsts = append(addedConsts, gd)
			insertedBefore[len(pairs)] = true
			j++
			continue
		}
		nf, ok := newDecls[j].(*ast.FuncDecl)
		if ok && oldFuncNameCount[newFunctionNames[nf]] != 0 && newFuncNameCount[newFunctionNames[nf]] > oldFuncNameCount[newFunctionNames[nf]] &&
			(topLevel[nf] || memberOf[nf] != nil) && retainedPlainFunction(nf) {
			// An overload of an existing name: registration order decides which one owns the
			// bare name, so every same-named function is re-declared with it below.
			overloadNames[newFunctionNames[nf]] = true
			added = append(added, nf)
			insertedBefore[len(pairs)] = true
			j++
			continue
		}
		if !ok || !(topLevel[nf] || memberOf[nf] != nil) || oldFuncNameCount[newFunctionNames[nf]] != 0 || !retainedPlainFunction(nf) {
			return retainedDeclarationPairing{}, fmt.Errorf("declaration set changed at %s", retainedLineKey(newDecls[j].Pos()))
		}
		added = append(added, nf)
		insertedBefore[len(pairs)] = true
		j++
	}
	if len(pairs)+len(removed)+len(removedConsts)+len(removedStructs)+usingsRemoved != len(oldDecls) {
		return retainedDeclarationPairing{}, fmt.Errorf("declaration count changed (%d -> %d)", len(oldDecls), len(newDecls))
	}
	return retainedDeclarationPairing{removed, added, pairs, addedConsts, removedConsts, addedStructs, removedStructs, insertedBefore, overloadNames, usingsAdded, usingsRemoved}, nil
}
