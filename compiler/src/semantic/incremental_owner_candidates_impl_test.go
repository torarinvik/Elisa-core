package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/incremental"
	"iter"
)

// primitiveOwnerCandidates is the legacy prefix-iterator comparison helper
// used by candidate tests and benchmarks. Production Analyze uses
// preparePrimitiveOwnerCandidates to cap and materialize the whole walk before
// touching session state.
func primitiveOwnerCandidates(decls []ast.Decl, origin string, previous map[incremental.Key]bool, limit int, keepQualifiedNames bool) iter.Seq[primitiveOwnerCandidate] {
	stream := func(yield func(primitiveOwnerCandidate) bool) {
		walkPrimitiveOwnerDeclarations(decls, "", 0, len(origin), nil, nil, func(fn *ast.FuncDecl, qualified string) bool {
			identity, name, retained, identityValid, _ := preparePrimitiveOwnerCandidateWithBudget(fn, qualified, origin, previous, nil)
			if !keepQualifiedNames {
				qualified = ""
			}
			return yield(primitiveOwnerCandidate{function: fn, qualifiedName: qualified, name: name, identity: identity, identityValid: identityValid, retained: retained, auditLexical: keepQualifiedNames})
		})
	}
	if len(previous) == 0 || limit <= 0 || keepQualifiedNames {
		return stream
	}
	var candidates []primitiveOwnerCandidateRecord
	preparedBytes := 0
	prefixOccurrences := 0
	complete := walkPrimitiveOwnerDeclarations(decls, "", 0, len(origin), &preparedBytes, nil, func(fn *ast.FuncDecl, qualified string) bool {
		if len(candidates) >= limit {
			return false
		}
		identity, _, retained, identityValid, ready := preparePrimitiveOwnerCandidateWithBudget(fn, qualified, origin, previous, func(amount int) bool {
			return reservePrimitiveOwnerCandidateBytes(&preparedBytes, amount, primitiveOwnerCandidateWorkBudget)
		})
		if !ready {
			return false
		}
		prefixOccurrences++
		candidates = append(candidates, primitiveOwnerCandidateRecord{function: fn, identity: identity, identityValid: identityValid, retained: retained})
		return true
	})
	if !complete {
		prefixLength := prefixOccurrences
		return func(yield func(primitiveOwnerCandidate) bool) {
			for _, record := range candidates {
				candidate := primitiveOwnerCandidate{function: record.function, identity: record.identity, identityValid: record.identityValid, retained: record.retained, auditLexical: keepQualifiedNames}
				if !yield(candidate) {
					return
				}
			}
			skip := prefixLength
			walkPrimitiveOwnerDeclarations(decls, "", 0, len(origin), nil, &skip, func(fn *ast.FuncDecl, qualified string) bool {
				identity, name, retained, identityValid, _ := preparePrimitiveOwnerCandidateWithBudget(fn, qualified, origin, previous, nil)
				if !keepQualifiedNames {
					qualified = ""
				}
				return yield(primitiveOwnerCandidate{function: fn, qualifiedName: qualified, name: name, identity: identity, identityValid: identityValid, retained: retained, auditLexical: keepQualifiedNames})
			})
		}
	}
	return func(yield func(primitiveOwnerCandidate) bool) {
		for _, record := range candidates {
			candidate := primitiveOwnerCandidate{function: record.function, identity: record.identity, identityValid: record.identityValid, retained: record.retained, auditLexical: keepQualifiedNames}
			if !yield(candidate) {
				return
			}
		}
	}
}
