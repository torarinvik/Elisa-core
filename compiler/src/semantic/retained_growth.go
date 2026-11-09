package semantic

import (
	"elisacore/src/ast"
	"fmt"
)

// retainedGrowthForUpdate accounts for declaration count changes represented in the prebody snapshot.
func retainedGrowthForUpdate(r *RetainedAnalysis, paired retainedDeclarationPairing, comparison retainedComparison, oldDecls, newDecls []ast.Decl, topLevel map[ast.Decl]bool, memberOf map[ast.Decl]*ast.NamespaceDecl, namespaceOf map[*ast.NamespaceDecl]string) (retainedGrowth, int, error) {
	added, removed := comparison.added, comparison.removed
	addedConsts, removedConsts := paired.addedConsts, paired.removedConsts
	addedStructs, removedStructs := paired.addedStructs, paired.removedStructs
	constEnumPairs := comparison.constEnumPairs
	enumPairs, hierarchyPairs := comparison.enumPairs, comparison.hierarchyPairs
	usingsAdded, usingsRemoved := paired.usingsAdded, paired.usingsRemoved
	constsIn, constsOut, globalsIn := 0, 0, 0
	for _, d := range removedConsts {
		if _, isConst := d.(*ast.ConstDecl); isConst {
			constsOut++
		}
	}
	for _, d := range addedConsts {
		if _, isConst := d.(*ast.ConstDecl); isConst {
			constsIn++
		} else {
			globalsIn++
		}
	}
	if len(enumPairs)+len(hierarchyPairs) != 0 {
		if !retainedEnumStoreSetsEqual(oldDecls, newDecls) {
			return retainedGrowth{}, 0, fmt.Errorf("an enum edit changes which enums are recursive or store-backed")
		}
		storeBacked := retainedStoreBackedEnums(newDecls)
		for _, e := range append(append([][2]*ast.EnumDecl(nil), enumPairs...), hierarchyPairs...) {
			sb := storeBacked[e[1].Name]
			if d := retainedConstVariantCount(e[1], sb) - retainedConstVariantCount(e[0], sb); d > 0 {
				constsIn += d
			} else {
				constsOut -= d
			}
		}
	}
	for _, pair := range constEnumPairs {
		if delta := len(pair[1].Members) - len(pair[0].Members); delta > 0 {
			constsIn += delta
		} else {
			constsOut -= delta
		}
	}
	growth := retainedGrowth{addedFuncs: len(added), removedFuncs: len(removed), addedConsts: constsIn, removedConsts: constsOut, addedGlobals: globalsIn, usings: usingsAdded+usingsRemoved != 0}
	for _, st := range removedStructs {
		namespace := ""
		if block := r.oldMemberBlock(st); block != "" {
			namespace = block
		}
		growth.removedStructs = append(growth.removedStructs, joinQualifiedName(namespace, st.Name))
	}
	for _, st := range addedStructs {
		namespace := ""
		if block := memberOf[st]; block != nil && !topLevel[st] {
			namespace = namespaceOf[block]
		}
		growth.addedStructs = append(growth.addedStructs, joinQualifiedName(namespace, st.Name))
	}
	return growth, globalsIn, nil
}
