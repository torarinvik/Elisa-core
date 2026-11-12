package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/astscan"
	"elisacore/src/lexer"
	"elisacore/src/retainedsyntax"
	"elisacore/src/unparse"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
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

// The parser names synthesized declarations and auto arenas after their byte
// offset (`__auto_10116619`, `__MachineMode_<hash>_10116619`), so an edit renames
// them in every later declaration of the expansion without changing it.
var retainedGensym = regexp.MustCompile(`(__[A-Za-z][A-Za-z0-9_]*?)_[0-9]+\b`)

// Auto arenas and runtime quantifier results are bindings local to the body or
// expression block that declares them.
var retainedLocalGensym = regexp.MustCompile(`^__(auto|quantifier)_[0-9]+$`)

var retainedLocalGensymRef = regexp.MustCompile(`\b__(auto|quantifier)_([0-9]+)\b`)

// retainedLocalGensymName recognizes the exact language of retainedLocalGensym
// without paying regexp-engine overhead in reflective source scans. Keep the
// regexp as an independent oracle in tests; this predicate only classifies a
// spelling and does not grant binder authority.
func retainedLocalGensymName(name string) bool {
	return astscan.IsLocalGeneratedName(name)
}

// retainedShiftLocalGensyms renames the body-local gensyms in text for a body that moved by
// shift bytes.
func retainedShiftLocalGensyms(text string, shift int) string {
	if shift == 0 || !strings.Contains(text, "__") {
		return text
	}
	return retainedLocalGensymRef.ReplaceAllStringFunc(text, func(name string) string {
		m := retainedLocalGensymRef.FindStringSubmatch(name)
		n, err := strconv.Atoi(m[2])
		if err != nil {
			return name
		}
		return "__" + m[1] + "_" + strconv.Itoa(n+shift)
	})
}

func retainedNormalized(text string) string {
	if !strings.Contains(text, "__") {
		return text
	}
	return retainedGensym.ReplaceAllString(text, "${1}_")
}

// retainedSharedGensyms lists the offset-named identifiers a body mentions that
// may name declarations outside it. A spliced body keeps its new names while the
// retained declarations keep the old ones, so these must not change.
func retainedSharedGensyms(text string) string {
	var out []string
	for _, name := range retainedGensym.FindAllString(text, -1) {
		if !retainedLocalGensymName(name) {
			out = append(out, name)
		}
	}
	return strings.Join(out, " ")
}

// retainedHeader is fn without its statements. Its contracts are part of it (the
// formatter does not print them): callers' proofs read them, so a contract edit is a
// signature edit.
func retainedHeader(fn *ast.FuncDecl) string {
	c := *fn
	c.Body = nil
	header := unparse.FormatDecl(&c)
	var b strings.Builder
	// Most contracts are empty, but their labels are always part of the fingerprint.
	b.Grow(len(header) + 224)
	b.WriteString(header)
	writeRetainedContractPart(&b, "\nrequires: ", fn.Requires)
	writeRetainedContractPart(&b, "\nrequires-proofs: ", fn.RequiresProofs)
	writeRetainedContractPart(&b, "\nensure: ", fn.EnsureValues)
	writeRetainedContractPart(&b, "\nensure-proofs: ", fn.EnsureProofs)
	writeRetainedContractPart(&b, "\nensures: ", fn.Ensures)
	writeRetainedContractPart(&b, "\ndecreases: ", fn.Decreases)
	b.WriteString("\ndecreases-wild: ")
	b.WriteString(fn.DecreasesWild)
	writeRetainedContractPart(&b, "\nchanges: ", fn.Changes)
	writeRetainedContractPart(&b, "\npreserves: ", fn.Preserves)
	writeRetainedContractPart(&b, "\nfulfills: ", fn.Fulfills)
	writeRetainedContractPart(&b, "\ncontract-includes: ", fn.ContractIncludes)
	writeRetainedContractPart(&b, "\nuses: ", fn.Uses)
	writeRetainedContractPart(&b, "\nparam-contracts: ", fn.ParamContracts)
	writeRetainedContractPart(&b, "\nforbids: ", fn.Forbids)
	return b.String()
}

func writeRetainedContractPart(b *strings.Builder, label string, value any) {
	b.WriteString(label)
	retainedWriteSyntax(b, reflect.ValueOf(value))
}

// retainedWriteSyntax prints v without source positions: expressions and statements
// through the formatter, other values field by field.
func retainedWriteSyntax(b *strings.Builder, v reflect.Value) {
	if !v.IsValid() {
		b.WriteString("<nil>")
		return
	}
	if v.Kind() == reflect.Interface || v.Kind() == reflect.Ptr {
		if v.IsNil() {
			b.WriteString("<nil>")
			return
		}
		switch n := v.Interface().(type) {
		case ast.Expr:
			b.WriteString(unparse.FormatExpr(n))
			return
		case ast.Stmt:
			b.WriteString(unparse.FormatStmt(n))
			return
		}
		retainedWriteSyntax(b, v.Elem())
		return
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		b.WriteString("[")
		for i := 0; i < v.Len(); i++ {
			retainedWriteSyntax(b, v.Index(i))
			b.WriteString(";")
		}
		b.WriteString("]")
	case reflect.Struct:
		if v.Type() == retainedPosType {
			return
		}
		b.WriteString("{")
		for i := 0; i < v.NumField(); i++ {
			if !v.Type().Field(i).IsExported() {
				continue
			}
			retainedWriteSyntax(b, v.Field(i))
			b.WriteString(",")
		}
		b.WriteString("}")
	case reflect.Map:
		b.WriteString(fmt.Sprint(v.Len()))
	default:
		fmt.Fprint(b, v.Interface())
	}
}

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
