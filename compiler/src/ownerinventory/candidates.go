package ownerinventory

import "elisacore/src/ast"

func WalkCandidates(decls []ast.Decl, namespace string, depth int, originBytes int, preparedBytes *int, skipCandidates *int, sourceBudget, workBudget int, preparationBytes func(int, int) (int, bool), reserve func(*int, int, int) bool, yield func(*ast.FuncDecl, string) bool) bool {
	if depth > 128 {
		return true
	}
	for _, decl := range decls {
		if nested, ok := decl.(*ast.NamespaceDecl); ok {
			if len(nested.Name) > sourceBudget-len(namespace)-1 {
				continue
			}
			namespaceBytes := len(namespace) + len(nested.Name)
			if namespace != "" {
				namespaceBytes++
			}
			if preparedBytes != nil && !reserve(preparedBytes, namespaceBytes, workBudget) {
				return false
			}
			if !WalkCandidates(nested.Decls, joinQualified(namespace, nested.Name), depth+1, originBytes, preparedBytes, skipCandidates, sourceBudget, workBudget, preparationBytes, reserve, yield) {
				return false
			}
			continue
		}
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn == nil || fn.Name == "" || len(fn.Name) > sourceBudget {
			continue
		}
		qualifiedBytes := len(namespace) + len(fn.Name)
		if namespace != "" {
			qualifiedBytes++
		}
		if qualifiedBytes > sourceBudget {
			continue
		}
		if skipCandidates != nil && *skipCandidates > 0 {
			*skipCandidates = *skipCandidates - 1
			continue
		}
		// Reserve qualification and key materialization before constructing the
		// qualified name. Parameter work is charged by candidate preparation.
		candidateWork, ok := preparationBytes(originBytes, qualifiedBytes)
		if !ok {
			return false
		}
		if preparedBytes != nil && !reserve(preparedBytes, candidateWork, workBudget) {
			return false
		}
		qualified := joinQualified(namespace, fn.Name)
		if !yield(fn, qualified) {
			return false
		}
	}
	return true
}

func joinQualified(namespace, name string) string {
	if namespace == "" {
		return name
	}
	if name == "" {
		return namespace
	}
	return namespace + "." + name
}
