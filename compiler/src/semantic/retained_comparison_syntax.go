package semantic

import (
	"bytes"
	"elisacore/src/ast"
	"elisacore/src/astscan"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
	"fmt"
	"reflect"
	"regexp"
	"sort"
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

func retainedLineKey(p lexer.Pos) string {
	var digits [41]byte
	suffix := strconv.AppendInt(digits[:0], int64(p.Line), 10)
	suffix = append(suffix, ':')
	suffix = strconv.AppendInt(suffix, int64(p.Col), 10)
	var key strings.Builder
	key.Grow(len(p.File) + 1 + len(suffix))
	key.WriteString(p.File)
	key.WriteByte(':')
	key.Write(suffix)
	return key.String()
}

var retainedDeclType = reflect.TypeOf((*ast.Decl)(nil)).Elem()

// retainedWalkDecls lists every declaration in source order, descending into
// declaration containers (modules, impls, conditional blocks) but not bodies.
func retainedWalkDecls(file *ast.File) []ast.Decl {
	var out []ast.Decl
	var visit func(v reflect.Value)
	visit = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if v.IsNil() {
				return
			}
			if v.Kind() == reflect.Pointer && v.CanInterface() {
				switch node := v.Interface().(type) {
				case *ast.FuncDecl:
					out = append(out, node)
					return
				case *ast.NamespaceDecl:
					out = append(out, node)
					for _, member := range node.Decls {
						visit(reflect.ValueOf(member))
					}
					return
				case ast.Decl:
					out = append(out, node)
				case *ast.Ident, *ast.IntLit, *ast.FloatLit, *ast.StringLit,
					*ast.CharLit, *ast.BoolLit, *ast.NullLit, *ast.ZeroedLit:
					return
				}
			}
			visit(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				switch f := v.Field(i); f.Kind() {
				case reflect.Slice, reflect.Interface, reflect.Pointer:
					visit(f)
				}
			}
		case reflect.Slice:
			if v.Type().Elem().Kind() == reflect.Uint8 {
				return
			}
			if v.Type() == reflect.TypeFor[[]ast.Decl]() {
				for i := 0; i < v.Len(); i++ {
					visit(v.Index(i).Elem())
				}
				return
			}
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i))
			}
		}
	}
	if file == nil {
		return nil
	}
	for _, decl := range file.Decls {
		visit(reflect.ValueOf(decl))
	}
	return out
}

var retainedPosType = reflect.TypeOf(lexer.Pos{})

func retainedShiftPos(p lexer.Pos, marks map[string][][2]int) lexer.Pos {
	ms := marks[p.File]
	if len(ms) == 0 || p.Line == 0 {
		return p
	}
	d := 0
	for _, m := range ms {
		if p.Line >= m[0] {
			d = m[1]
		}
	}
	if d != 0 {
		p.Line += d
		if p.EndLine != 0 {
			p.EndLine += d
		}
	}
	return p
}

// retainedShiftDecl moves every position inside an unchanged declaration by
// the line delta of the edit above it. Byte offsets are left as they were: they
// index the expanded buffer, are never printed, and stay consistent within a
// declaration.
func retainedShiftDecl(decl ast.Decl, marks map[string][][2]int) {
	retainedShiftDeclPositions(decl, marks, false)
}

// Nonfunction containers and their members are paired independently. Shift only
// this declaration's own positions so nested members are never shifted twice.
func retainedShiftDeclOwn(decl ast.Decl, marks map[string][][2]int) {
	retainedShiftDeclPositions(decl, marks, true)
}

func retainedShiftDeclPositions(decl ast.Decl, marks map[string][][2]int, ownOnly bool) {
	root := reflect.ValueOf(decl).Pointer()
	seen := map[uintptr]bool{}
	var visit func(v reflect.Value)
	visit = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer:
			if ownOnly && !v.IsNil() && v.Pointer() != root && v.Type().Implements(retainedDeclType) {
				return
			}
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			visit(v.Elem())
		case reflect.Interface:
			if !v.IsNil() && v.Elem().Kind() == reflect.Pointer {
				visit(v.Elem())
			}
		case reflect.Struct:
			if v.Type() == retainedPosType {
				if v.CanSet() {
					v.Set(reflect.ValueOf(retainedShiftPos(v.Interface().(lexer.Pos), marks)))
				}
				return
			}
			for i := 0; i < v.NumField(); i++ {
				visit(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i))
			}
		}
	}
	visit(reflect.ValueOf(decl))
}

// retainedDeclSourceStart includes leading annotations in the declaration's
// comparison span. Pos() names the declaration keyword, which otherwise leaves
// a first declaration's annotations outside every span and assigns later ones
// to the preceding declaration.
func retainedDeclSourceStart(d ast.Decl) int {
	start := d.Pos().Offset
	var annotations []ast.Annotation
	switch d := d.(type) {
	case *ast.StructDecl:
		annotations = d.Annotations
	case *ast.EnumDecl:
		annotations = d.Annotations
	case *ast.FuncDecl:
		annotations = d.Annotations
	case *ast.StoreDecl:
		annotations = d.Annotations
	case *ast.ExternFuncDecl:
		annotations = d.Annotations
	case *ast.ExternVarDecl:
		annotations = d.Annotations
	case *ast.ExternTypeDecl:
		annotations = d.Annotations
	case *ast.ExportFuncDecl:
		annotations = d.Annotations
	case *ast.ImplDecl:
		annotations = d.Annotations
	}
	for _, annotation := range annotations {
		if offset := annotation.Position.Offset; offset >= 0 && offset < start {
			start = offset
		}
	}
	return start
}

// retainedSpans returns each declaration's source text: from its start to the
// next declaration's start in the expanded buffer (the last runs to the end).
// A container's span is therefore its own header, and comments and blank lines
// count as changes, so a declaration whose positions moved is re-analyzed.
func retainedSpans(decls []ast.Decl, src []byte) []string {
	// A span runs to the next declaration start in the source, not in walk
	// order: synthesized declarations (machine expansions) are walked out of
	// source order, and a walk-order span would cover unrelated edits.
	starts := make([]int, 0, len(decls))
	for _, d := range decls {
		starts = append(starts, retainedDeclSourceStart(d))
	}
	sort.Ints(starts)
	out := make([]string, len(decls))
	for i, d := range decls {
		start := retainedDeclSourceStart(d)
		end := len(src)
		if k := sort.SearchInts(starts, start+1); k < len(starts) {
			end = starts[k]
		}
		if start < 0 || start > end || end > len(src) {
			out[i] = fmt.Sprintf("\x00unordered %d", i) // never equal: forces a fallback or splice
			continue
		}
		span := src[start:end]
		// The span stays in its own file region: text past a directive belongs to the
		// region the directive opens.
		if k := bytes.Index(span, []byte("\n#line ")); k >= 0 {
			span = span[:k+1]
		}
		// The first directive after a newline was already trimmed above, so
		// only a directive at the beginning of this span can remain. Avoid a
		// second scan of ordinary declaration text.
		if bytes.HasPrefix(span, []byte("#line ")) {
			out[i] = retainedWithoutLineDirectives(span)
		} else {
			out[i] = string(span)
		}
	}
	return out
}

// retainedWithoutLineDirectives drops include-splice `#line` directives from a
// span. They restate positions, which the update tracks per file from the
// declarations themselves; an edit above an include renumbers the directive
// that resumes the parent file without changing any declaration.
func retainedWithoutLineDirectives(span []byte) string {
	if !bytes.Contains(span, []byte("#line ")) {
		return string(span)
	}
	var b strings.Builder
	b.Grow(len(span))
	for len(span) > 0 {
		line := span
		if k := bytes.IndexByte(span, '\n'); k >= 0 {
			line, span = span[:k+1], span[k+1:]
		} else {
			span = nil
		}
		if bytes.HasPrefix(line, []byte("#line ")) {
			continue
		}
		b.Write(line)
	}
	return b.String()
}

// retainedSameDeclKind reports whether two declarations can be the same one
// across an edit: the same node type, and for functions the same name.
func retainedSameDeclKind(a, b ast.Decl) bool {
	if reflect.TypeOf(a) != reflect.TypeOf(b) {
		return false
	}
	switch fa := a.(type) {
	case *ast.FuncDecl:
		return fa.Name == b.(*ast.FuncDecl).Name
	case *ast.ConstDecl:
		// By name, so an added const is not paired with the next one; the same for types.
		return fa.Name == b.(*ast.ConstDecl).Name
	case *ast.StructDecl:
		return fa.Name == b.(*ast.StructDecl).Name
	case *ast.ConstEnumDecl:
		return fa.Name == b.(*ast.ConstEnumDecl).Name
	case *ast.EnumDecl:
		return fa.Name == b.(*ast.EnumDecl).Name
	case *ast.GlobalDecl:
		return fa.Name == b.(*ast.GlobalDecl).Name
	case *ast.UsingDecl:
		fb := b.(*ast.UsingDecl)
		return fa.Name == fb.Name && fa.Member == fb.Member && fa.Alias == fb.Alias
	}
	return true
}
