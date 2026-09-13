package ast

import "strings"

// ModulePathSpelling renders an internal qualified name in its SOURCE spelling.
//
// Namespaces are joined with "." inside the compiler, and symbol names are printed that
// way (`Pascal.Semantic.SymbolId`). But `::` is the only separator a user may WRITE for a
// module path -- `.` accesses a value member -- and the parser rejects the dotted form.
//
// So every emitter that produces SOURCE (the formatter, the interface writer) or that
// tells a user what to type (a diagnostic, a doc "declaration:" line) must convert first.
// It lives in `ast` because the parser, the analyzer and the unparser all need it and all
// three already depend on this package.
func ModulePathSpelling(name string) string {
	return strings.ReplaceAll(name, ".", "::")
}

// SourceSpelling is the type name as the user wrote it: the recorded Spelling when the
// parser saw real separators, else the (single-segment or synthesized) Name.
// SourceSpelling is the GenericType head as written, `::` separators included.
func (n *GenericType) SourceSpelling() string {
	if n == nil {
		return ""
	}
	if n.Spelling != "" {
		return n.Spelling
	}
	return n.Name
}

func (n *NamedType) SourceSpelling() string {
	if n == nil {
		return ""
	}
	if n.Spelling != "" {
		return n.Spelling
	}
	return n.Name
}
