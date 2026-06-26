package semantic

import (
	"elisacore/src/ast"
)

// Named contract registration and lookup (docs/111 Part 3 S0).
//
// A `contract Name(params):` declaration is a top-level named bundle of requires/ensure/changes/preserves
// clauses, parameterised over its params. This module implements the storage and lookup mechanisms that
// allow contract declarations to be resolved at function definition sites when a `uses Name(args)` clause
// references them.
//
// Contract declarations are stored in a symbol-table-like registry (namedContracts) keyed by qualified name.
// A contract is "registered" when the analyzer encounters its declaration during symbol collection; it can
// then be looked up via lookupNamedContract(name), which returns the FuncDecl if found. This is the storage
// layer; actual `uses` expansion and enforcement happen in analyzer_named_contracts.go.

// registerNamedContracts scans the entire program for contract declarations and registers them in the
// analyzer's namedContracts table. Called once, early in analysis, before body analysis and expansion.
// Contracts are keyed by both unqualified and fully qualified names (namespace::name).
func (a *Analyzer) registerNamedContracts(decls []scopedDecl) {
	if a.namedContracts == nil {
		a.namedContracts = make(map[string]*ast.FuncDecl)
	}
	for _, sd := range decls {
		if fn, ok := sd.Decl.(*ast.FuncDecl); ok && fn != nil && fn.IsContract {
			// Register by unqualified name
			a.namedContracts[fn.Name] = fn
			// Also register by fully qualified name (namespace::name) for module-scoped lookup
			qualifiedName := joinQualifiedName(sd.Namespace, fn.Name)
			a.namedContracts[qualifiedName] = fn
		}
	}
}

// lookupNamedContract resolves a contract name to its FuncDecl declaration. It consults the global
// namedContracts table, supporting both unqualified (Name) and fully qualified (Module::Name) lookups.
// Returns nil if no contract with that name is registered; the caller is responsible for reporting
// an error (see analyzer_named_contracts.go expandOneUse for typical error handling).
func (a *Analyzer) lookupNamedContract(name string) *ast.FuncDecl {
	if a.namedContracts == nil {
		return nil
	}
	return a.namedContracts[name]
}
