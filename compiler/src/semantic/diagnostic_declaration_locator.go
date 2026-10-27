package semantic

import (
	"fmt"

	"elisacore/src/ast"
)

// DiagnosticFunctionLocator identifies a source declaration in one parsed
// file graph. It is a locator only: it does not encode syntax, semantic facts,
// dependencies, or prove that a declaration is unchanged across revisions.
// Ordinals are deliberately conservative: insertion or reordering can change
// later locators. The caller must separately authenticate the source root and
// all values before using any observation for cache admission.
type DiagnosticFunctionLocator struct {
	SourceRoot string
	Ordinal    int
	Name       string
}

// DiagnosticFunctionLocators indexes only declarations reached from the
// ordered source declarations; worker-private or generated declarations are
// absent. Repeated declaration pointers are ambiguous and fail the entire
// index instead of accidentally assigning an arbitrary locator.
func DiagnosticFunctionLocators(file *ast.File) (map[*ast.FuncDecl]DiagnosticFunctionLocator, error) {
	if file == nil || file.Filename == "" {
		return nil, fmt.Errorf("missing diagnostic source root")
	}
	index := make(map[*ast.FuncDecl]DiagnosticFunctionLocator)
	for ordinal, decl := range retainedWalkDecls(file) {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fn == nil || fn.Name == "" {
			return nil, fmt.Errorf("invalid function declaration at ordinal %d", ordinal)
		}
		if _, exists := index[fn]; exists {
			return nil, fmt.Errorf("ambiguous repeated declaration at ordinal %d", ordinal)
		}
		index[fn] = DiagnosticFunctionLocator{SourceRoot: file.Filename, Ordinal: ordinal, Name: fn.Name}
	}
	return index, nil
}
