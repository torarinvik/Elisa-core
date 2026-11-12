package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/retainedsyntax"
	"elisacore/src/unparse"
	"reflect"
)

// funcComparisonText is what Update compares for a function whose source changed.
type funcComparisonText struct {
	header         string // signature, gensyms normalized
	gensyms        string // offset-named identifiers the function mentions
	bodyTypeSyntax string // pointer-free source TypeExpr fingerprint for a projected body
	bodyTypeNames  string // source TypeExpr syntax-key inventory for a projected body
}

func retainedText(fn *ast.FuncDecl) funcComparisonText {
	return funcComparisonText{
		header:  retainedNormalized(retainedHeader(fn)),
		gensyms: retainedSharedGensyms(unparse.FormatDecl(fn)),
	}
}

func (r *RetainedAnalysis) comparisonText(fn *ast.FuncDecl) funcComparisonText {
	if text, ok := r.texts[fn]; ok {
		return text
	}
	return retainedText(fn)
}

var retainedGensym = retainedsyntax.Gensym
var retainedLocalGensym = retainedsyntax.LocalGensym
var retainedLocalGensymRef = retainedsyntax.LocalGensymRef

func retainedLocalGensymName(name string) bool { return retainedsyntax.LocalGensymName(name) }
func retainedShiftLocalGensyms(text string, shift int) string {
	return retainedsyntax.ShiftLocalGensyms(text, shift)
}
func retainedNormalized(text string) string    { return retainedsyntax.NormalizeGensyms(text) }
func retainedSharedGensyms(text string) string { return retainedsyntax.SharedGensyms(text) }

func retainedHeader(fn *ast.FuncDecl) string { return retainedsyntax.Header(fn) }

// These syntax-only operations live outside the analyzer package so edits to
// their implementation do not force Go to recompile the whole semantic package.
var retainedDeclType = reflect.TypeOf((*ast.Decl)(nil)).Elem()
var retainedPosType = reflect.TypeOf(lexer.Pos{})

func retainedLineKey(p lexer.Pos) string       { return retainedsyntax.LineKey(p) }
func retainedWalkDecls(f *ast.File) []ast.Decl { return retainedsyntax.WalkDecls(f) }
func retainedShiftPos(p lexer.Pos, m map[string][][2]int) lexer.Pos {
	return retainedsyntax.ShiftPos(p, m)
}
func retainedShiftDecl(d ast.Decl, m map[string][][2]int)    { retainedsyntax.ShiftDecl(d, m) }
func retainedShiftDeclOwn(d ast.Decl, m map[string][][2]int) { retainedsyntax.ShiftDeclOwn(d, m) }
func retainedDeclSourceStart(d ast.Decl) int                 { return retainedsyntax.DeclSourceStart(d) }
func retainedSpans(d []ast.Decl, s []byte) []string          { return retainedsyntax.Spans(d, s) }
func retainedWithoutLineDirectives(s []byte) string          { return retainedsyntax.WithoutLineDirectives(s) }
func retainedSameDeclKind(a, b ast.Decl) bool                { return retainedsyntax.SameDeclKind(a, b) }
