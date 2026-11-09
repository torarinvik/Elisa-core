package semantic

import (
	"elisacore/src/ast"
	"fmt"
)

// commitRetainedPlainDefaults validates and retires the plain-default transaction.
func (r *RetainedAnalysis) commitRetainedPlainDefaults(plainManaged bool, plainRemoved []*retainedPlainDefaultProduct, plainBodies []*retainedPlainBodyRetirement, plainRebound map[*ast.FuncDecl]bool) error {
	if plainManaged {
		r.prunePlainDefaultRetainedOwners(plainRemoved)
		identityProducts := append([]*retainedPlainDefaultProduct(nil), plainRemoved...)
		for _, retirement := range plainBodies {
			identityProducts = append(identityProducts, retirement.Product)
		}
		if err := r.prunePlainDeadIdentities(identityProducts); err != nil {
			return fmt.Errorf("plain identity commit: %v", err)
		}
		if err := r.verifyPlainDefaultRemoval(plainRemoved); err != nil {
			return fmt.Errorf("plain default commit: %v", err)
		}
	}
	for _, retirement := range plainBodies {
		if err := r.verifyPlainBodyRetirement(retirement); err != nil {
			return fmt.Errorf("plain retired body commit: %v", err)
		}
	}
	r.refreshPlainDefaultProductsAfterBodyCleanup(plainRebound)
	return nil
}
