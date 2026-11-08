package semantic

import (
	"cmp"
	"slices"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Go map iteration order changes from run to run, and the compiler has no goroutines, so a map
// walk is the only way its output (diagnostics, IR, facts, emitted text) can differ between two
// runs on the same input. Walks whose effect reaches output iterate through these helpers.

// posLess orders two source positions: file, then offset, then line/column.
func posLess(a, b lexer.Pos) bool {
	if a.File != b.File {
		return a.File < b.File
	}
	if a.Offset != b.Offset {
		return a.Offset < b.Offset
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Col < b.Col
}

func nodePos(node ast.Node) lexer.Pos {
	if node == nil {
		return lexer.Pos{}
	}
	return node.Pos()
}

func comparePosition(a, b lexer.Pos) int {
	if order := cmp.Compare(a.File, b.File); order != 0 {
		return order
	}
	if order := cmp.Compare(a.Offset, b.Offset); order != 0 {
		return order
	}
	if order := cmp.Compare(a.Line, b.Line); order != 0 {
		return order
	}
	return cmp.Compare(a.Col, b.Col)
}

// symbolLess orders symbols by name, then by declaration position.
func symbolLess(a, b *Symbol) bool {
	if a == nil || b == nil {
		return a == nil && b != nil
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	return posLess(nodePos(a.Node), nodePos(b.Node))
}

// sortedSymbolKeys returns the keys of a symbol-keyed map in symbolLess order.
func sortedSymbolKeys[V any](m map[*Symbol]V) []*Symbol {
	keys := make([]*Symbol, 0, len(m))
	for sym := range m {
		keys = append(keys, sym)
	}
	slices.SortFunc(keys, func(a, b *Symbol) int {
		if a == nil || b == nil {
			if a == b {
				return 0
			}
			if a == nil {
				return -1
			}
			return 1
		}
		if order := cmp.Compare(a.Name, b.Name); order != 0 {
			return order
		}
		return comparePosition(nodePos(a.Node), nodePos(b.Node))
	})
	return keys
}

// sortedFuncDeclKeys returns the keys of a declaration-keyed map in source order.
func sortedFuncDeclKeys[V any](m map[*ast.FuncDecl]V) []*ast.FuncDecl {
	keys := make([]*ast.FuncDecl, 0, len(m))
	for decl := range m {
		keys = append(keys, decl)
	}
	slices.SortFunc(keys, func(a, b *ast.FuncDecl) int {
		if a == nil || b == nil {
			if a == b {
				return 0
			}
			if a == nil {
				return -1
			}
			return 1
		}
		if order := cmp.Compare(a.Name, b.Name); order != 0 {
			return order
		}
		return comparePosition(a.Pos(), b.Pos())
	})
	return keys
}

// orderedStructFieldNames walks a struct's fields in declaration order, falling back to sorted
// names when the struct has no declaration (builtin or synthesized).
func orderedStructFieldNames(st *StructType) []string {
	if st == nil {
		return nil
	}
	if names := structFieldOrder(st); len(names) == len(st.Fields) {
		complete := true
		for _, name := range names {
			if _, ok := st.Fields[name]; !ok {
				complete = false
				break
			}
		}
		if complete {
			return names
		}
	}
	return sortedFieldNames(st.Fields)
}
